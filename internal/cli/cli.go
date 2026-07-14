// Package cli wires the subcommands together: flag parsing, usage text,
// and exit codes. All I/O goes through injected writers so the whole
// CLI can be driven in-process by tests.
//
// Exit codes: 0 success (for diff: no differences), 1 differences found
// (diff only), 2 usage error, 3 runtime error.
package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/JaydenCJ/mcpwatch/internal/version"
)

// Exit codes.
const (
	ExitOK      = 0
	ExitDiff    = 1
	ExitUsage   = 2
	ExitRuntime = 3
)

const usageText = `mcpwatch — restart a stdio MCP server on file changes and diff its capabilities

Usage:
  mcpwatch run  [flags] -- <server command…>   watch, restart, and live-diff (default)
  mcpwatch dump [flags] -- <server command…>   start once, print the capability surface
  mcpwatch diff [flags] <old.json> <new.json>  compare two capability dumps
  mcpwatch version                             print the version

Run 'mcpwatch <command> -h' for the flags of each command.

Exit codes: 0 ok (diff: surfaces identical), 1 diff found differences,
2 usage error, 3 runtime error.
`

// Run executes the CLI with the given arguments (without the program
// name) and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return ExitUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "run":
		return runRun(rest, stdout, stderr)
	case "dump":
		return runDump(rest, stdout, stderr)
	case "diff":
		return runDiff(rest, stdout, stderr)
	case "version", "--version", "-V":
		fmt.Fprintf(stdout, "mcpwatch %s\n", version.Version)
		return ExitOK
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usageText)
		return ExitOK
	default:
		fmt.Fprintf(stderr, "mcpwatch: unknown command %q\n\n", cmd)
		fmt.Fprint(stderr, usageText)
		return ExitUsage
	}
}

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

// splitServerCommand separates "[flags…] -- server cmd…" into the flag
// part and the server command. The `--` is required so that server
// flags are never mistaken for mcpwatch flags.
func splitServerCommand(args []string) (flags, server []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

// newFlagSet builds a FlagSet that reports errors to stderr without
// exiting the process.
func newFlagSet(name string, stderr io.Writer, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	return fs
}

// parse runs fs over args and maps flag errors to the usage exit code;
// callers early-return when code >= 0.
func parse(fs *flag.FlagSet, args []string) int {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return ExitOK
		}
		return ExitUsage
	}
	return -1
}

func runtimeErr(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "mcpwatch: %v\n", err)
	return ExitRuntime
}

func usageErr(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "mcpwatch: "+format+"\n", args...)
	return ExitUsage
}

// commonServerFlags are shared by run and dump.
type commonServerFlags struct {
	timeout     time.Duration
	killTimeout time.Duration
	proto       string
	quietServer bool
}

func (c *commonServerFlags) register(fs *flag.FlagSet) {
	fs.DurationVar(&c.timeout, "timeout", 10*time.Second, "per-request timeout for the MCP handshake and list calls")
	fs.DurationVar(&c.killTimeout, "kill-timeout", 3*time.Second, "grace period per shutdown step (close stdin → SIGTERM → SIGKILL)")
	fs.StringVar(&c.proto, "proto", "", "MCP protocol version to request (default: the client's newest)")
	fs.BoolVar(&c.quietServer, "quiet-server", false, "discard the server's stderr instead of forwarding it")
}
