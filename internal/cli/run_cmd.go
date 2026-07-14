// run_cmd.go implements `mcpwatch run`, the dev loop itself: start the
// server, print the initial capability surface, then poll the watched
// tree; once a burst of edits settles, restart the server and print the
// capability diff against the last good snapshot.
//
// The loop is glue over deterministic parts — watch.Scan/Diff for
// change detection, watch.Debouncer for the restart policy, and
// startAndDump (shared with `dump`) for the reload — so everything with
// logic in it is unit-tested without timers.
package cli

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/JaydenCJ/mcpwatch/internal/capability"
	"github.com/JaydenCJ/mcpwatch/internal/capdiff"
	"github.com/JaydenCJ/mcpwatch/internal/render"
	"github.com/JaydenCJ/mcpwatch/internal/watch"
)

const runUsage = `Usage: mcpwatch run [flags] -- <server command…>

Watch files, restart the server when they change, and print the
capability diff after every reload. Interrupt (Ctrl-C) to stop.

Flags:
`

type runOpts struct {
	common    commonServerFlags
	watchDirs multiFlag
	include   multiFlag
	exclude   multiFlag
	noDefault bool
	poll      time.Duration
	debounce  time.Duration
	dumpFile  string
	server    []string
}

func runRun(args []string, stdout, stderr io.Writer) int {
	flagArgs, server := splitServerCommand(args)
	fs := newFlagSet("run", stderr, runUsage)
	var o runOpts
	o.common.register(fs)
	fs.Var(&o.watchDirs, "watch", "file or directory to watch (repeatable; default \".\")")
	fs.Var(&o.include, "include", "only react to files matching this glob (repeatable)")
	fs.Var(&o.exclude, "exclude", "ignore files matching this glob, in addition to the defaults (repeatable)")
	fs.BoolVar(&o.noDefault, "no-default-excludes", false, "do not apply the built-in exclude list (.git, node_modules, …)")
	fs.DurationVar(&o.poll, "poll", 300*time.Millisecond, "how often to scan the watched tree")
	fs.DurationVar(&o.debounce, "debounce", 300*time.Millisecond, "quiet period after the last change before restarting")
	fs.StringVar(&o.dumpFile, "dump-file", "", "also write the latest snapshot as JSON to this file after every (re)start")
	if code := parse(fs, flagArgs); code >= 0 {
		return code
	}
	if fs.NArg() > 0 {
		return usageErr(stderr, "run: unexpected argument %q (server command goes after --)", fs.Arg(0))
	}
	if len(server) == 0 {
		return usageErr(stderr, "run: no server command; usage: mcpwatch run [flags] -- <command…>")
	}
	if o.poll <= 0 {
		return usageErr(stderr, "run: --poll must be positive")
	}
	if len(o.watchDirs) == 0 {
		o.watchDirs = multiFlag{"."}
	}
	o.server = server

	s := &session{opts: o, stdout: stdout, stderr: stderr}
	return s.loop()
}

// session is the state of one `mcpwatch run` invocation.
type session struct {
	opts     runOpts
	stdout   io.Writer
	stderr   io.Writer
	last     *capability.Snapshot // last good snapshot, nil until first success
	restarts int
}

func (s *session) logf(format string, args ...any) {
	fmt.Fprintf(s.stdout, "[mcpwatch] "+format+"\n", args...)
}

func (s *session) watchConfig() watch.Config {
	cfg := watch.Config{Roots: s.opts.watchDirs, Include: s.opts.include}
	if s.opts.noDefault {
		cfg.Exclude = append([]string{}, s.opts.exclude...)
	} else if len(s.opts.exclude) > 0 {
		cfg.Exclude = append(append([]string{}, watch.DefaultExcludes...), s.opts.exclude...)
	}
	return cfg
}

// reload runs one start→dump→stop cycle and prints either the initial
// surface (first success) or the diff against the last good snapshot.
// A failed reload keeps the previous snapshot so the next successful
// one still diffs against the last surface the user actually saw.
func (s *session) reload() {
	snap, err := startAndDump(s.opts.server, &s.opts.common, s.stderr)
	if err != nil {
		s.logf("reload failed: %v", err)
		s.logf("waiting for the next file change…")
		return
	}
	if s.opts.dumpFile != "" {
		if out, eerr := capability.Encode(snap); eerr != nil {
			s.logf("could not encode --dump-file snapshot: %v", eerr)
		} else if werr := os.WriteFile(s.opts.dumpFile, out, 0o644); werr != nil {
			s.logf("could not write --dump-file: %v", werr)
		}
	}
	if s.last == nil {
		s.logf("capability surface (%s):", snap.Counts())
		fmt.Fprintln(s.stdout)
		render.Snapshot(s.stdout, snap)
		fmt.Fprintln(s.stdout)
	} else {
		d := capdiff.Compute(s.last, snap)
		if d.Empty() {
			s.logf("no capability changes (%s)", snap.Counts())
		} else {
			render.Diff(s.stdout, d)
		}
	}
	s.last = snap
}

// loop is the timing shell: ticker-driven scans feeding the debouncer,
// with SIGINT/SIGTERM for shutdown. Everything it calls is testable
// without it.
func (s *session) loop() int {
	cfg := s.watchConfig()
	quietTicks := int(s.opts.debounce/s.opts.poll) + 1

	s.logf("watching %s (poll %s) — server: %s",
		strings.Join(s.opts.watchDirs, ", "), s.opts.poll, shellJoin(s.opts.server))
	s.reload()
	if s.last == nil {
		// The very first dump failed; keep watching so the user can
		// just fix the file and save.
		s.logf("initial start failed — edit and save to retry")
	}

	prev, err := cfg.Scan()
	if err != nil {
		return runtimeErr(s.stderr, err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)

	ticker := time.NewTicker(s.opts.poll)
	defer ticker.Stop()
	deb := watch.NewDebouncer(quietTicks)

	for {
		select {
		case <-sig:
			s.logf("interrupted — bye")
			return ExitOK
		case <-ticker.C:
			cur, err := cfg.Scan()
			if err != nil {
				s.logf("scan failed: %v (retrying)", err)
				continue
			}
			changes := watch.Diff(prev, cur)
			prev = cur
			if !changes.Empty() {
				s.logf("%s changed: %s", time.Now().Format("15:04:05"), summarizePaths(changes))
			}
			if deb.Observe(!changes.Empty()) {
				s.restarts++
				s.logf("restart #%d", s.restarts)
				s.reload()
			}
		}
	}
}

// summarizePaths lists up to three touched paths, then "and N more".
func summarizePaths(c watch.Changes) string {
	paths := c.Paths()
	if len(paths) <= 3 {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(paths[:3], ", "), len(paths)-3)
}

// shellJoin renders the server argv for the banner, quoting arguments
// that contain whitespace.
func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t") {
			quoted[i] = "'" + a + "'"
		} else {
			quoted[i] = a
		}
	}
	return strings.Join(quoted, " ")
}
