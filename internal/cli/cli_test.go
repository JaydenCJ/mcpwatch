// In-process integration tests for the CLI. The "server" the commands
// spawn is this very test binary re-executed with MCPWATCH_FAKE_SERVER
// set, at which point TestMain serves the demosrv spec named by
// MCPWATCH_FAKE_SPEC — real subprocess, real pipes, zero network, and
// the spec file is rewritten between reloads to simulate editing a
// server mid-dev-loop.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JaydenCJ/mcpwatch/internal/capability"
	"github.com/JaydenCJ/mcpwatch/internal/demosrv"
	"github.com/JaydenCJ/mcpwatch/internal/version"
	"github.com/JaydenCJ/mcpwatch/internal/watch"
)

func changesOf(paths ...string) watch.Changes {
	return watch.Changes{Modified: paths}
}

func TestMain(m *testing.M) {
	if os.Getenv("MCPWATCH_FAKE_SERVER") == "1" {
		spec, err := demosrv.LoadSpec(os.Getenv("MCPWATCH_FAKE_SPEC"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := demosrv.Serve(spec, os.Stdin, os.Stdout, os.Stderr); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeServer writes spec to a file and returns the argv that respawns
// this test binary as that server. The env vars are process-wide, which
// is fine: the suite runs these tests sequentially.
func fakeServer(t *testing.T, spec string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spec.json")
	writeSpec(t, path, spec)
	t.Setenv("MCPWATCH_FAKE_SERVER", "1")
	t.Setenv("MCPWATCH_FAKE_SPEC", path)
	return []string{os.Args[0]}
}

func writeSpec(t *testing.T, path, spec string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
}

// run invokes the CLI in-process and captures both streams.
func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb strings.Builder
	code = Run(args, &out, &errb)
	return code, out.String(), errb.String()
}

const specV1 = `{
  "name": "fake-notes", "version": "1.0.0",
  "tools": [
    {"name": "echo", "description": "Echo a message", "inputSchema": {"type":"object","required":["text"]}}
  ],
  "prompts": [{"name": "sum", "arguments": [{"name": "date", "required": true}]}]
}`

const specV2 = `{
  "name": "fake-notes", "version": "1.1.0",
  "tools": [
    {"name": "echo", "description": "Echo a message", "inputSchema": {"type":"object","required":["text","volume"]}},
    {"name": "slugify", "description": "Make a slug"}
  ],
  "prompts": [{"name": "sum", "arguments": [{"name": "date", "required": true}]}]
}`

func TestVersionCommand(t *testing.T) {
	code, out, _ := run(t, "version")
	if code != ExitOK || out != "mcpwatch "+version.Version+"\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestNoArgsAndUnknownCommandExit2WithUsage(t *testing.T) {
	code, _, errOut := run(t)
	if code != ExitUsage || !strings.Contains(errOut, "Usage:") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	code, _, errOut = run(t, "frobnicate")
	if code != ExitUsage || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestDumpTextShowsTheWholeSurface(t *testing.T) {
	server := fakeServer(t, specV1)
	code, out, errOut := run(t, append([]string{"dump", "--"}, server...)...)
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	for _, want := range []string{"fake-notes 1.0.0", "tools (1)", "echo", "prompts (1)", "sum(date)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestDumpJSONRoundTripsThroughCapabilityDecode(t *testing.T) {
	server := fakeServer(t, specV1)
	code, out, errOut := run(t, append([]string{"dump", "--format", "json", "--"}, server...)...)
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	snap, err := capability.Decode([]byte(out))
	if err != nil {
		t.Fatalf("dump --format json must be Decode-able: %v", err)
	}
	if snap.ServerInfo.Name != "fake-notes" || len(snap.Tools) != 1 || snap.Tools[0].SchemaHash == "" {
		t.Fatalf("snapshot content wrong: %+v", snap)
	}
}

func TestDumpUsageErrorsExit2(t *testing.T) {
	code, _, errOut := run(t, "dump")
	if code != ExitUsage || !strings.Contains(errOut, "no server command") {
		t.Fatalf("missing command: code=%d stderr=%q", code, errOut)
	}
	code, _, errOut = run(t, "dump", "--format", "yaml", "--", "true")
	if code != ExitUsage || !strings.Contains(errOut, "yaml") {
		t.Fatalf("bad format: code=%d stderr=%q", code, errOut)
	}
}

func TestDumpAgainstACrashingServerExits3WithItsExitState(t *testing.T) {
	server := fakeServer(t, `{"name":"broken","failStartup":"stack trace here"}`)
	code, _, errOut := run(t, append([]string{"dump", "--timeout", "5s", "--"}, server...)...)
	if code != ExitRuntime {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if !strings.Contains(errOut, "exit code 1") {
		t.Fatalf("the server's exit state must be in the error: %q", errOut)
	}
	if !strings.Contains(errOut, "[server] stack trace here") {
		t.Fatalf("the server's stderr must be forwarded with the prefix: %q", errOut)
	}

	// With --quiet-server, the server's noise is dropped.
	code, _, errOut = run(t, append([]string{"dump", "--quiet-server", "--timeout", "5s", "--"}, server...)...)
	if code != ExitRuntime {
		t.Fatalf("quiet: code=%d", code)
	}
	if strings.Contains(errOut, "stack trace here") {
		t.Fatalf("--quiet-server must drop server stderr: %q", errOut)
	}
}

// dumpTo runs `dump --format json` against the current fake spec and
// writes the snapshot to a file, for diff tests.
func dumpTo(t *testing.T, server []string, path string) {
	t.Helper()
	code, out, errOut := run(t, append([]string{"dump", "--format", "json", "--"}, server...)...)
	if code != ExitOK {
		t.Fatalf("dump failed: %d %s", code, errOut)
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiffIdenticalDumpsExitZero(t *testing.T) {
	server := fakeServer(t, specV1)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	dumpTo(t, server, a)
	dumpTo(t, server, b)
	code, out, _ := run(t, "diff", a, b)
	if code != ExitOK || !strings.Contains(out, "no capability changes") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestDiffChangedDumpsExitOneWithReadableDiff(t *testing.T) {
	server := fakeServer(t, specV1)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	dumpTo(t, server, a)
	writeSpec(t, os.Getenv("MCPWATCH_FAKE_SPEC"), specV2) // "edit the server"
	dumpTo(t, server, b)

	code, out, _ := run(t, "diff", a, b)
	if code != ExitDiff {
		t.Fatalf("differences must exit 1, got %d:\n%s", code, out)
	}
	for _, want := range []string{"tools 1 → 2", "+ slugify", "~ echo", "input schema changed", "server: fake-notes 1.0.0 → fake-notes 1.1.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestDiffJSONFormatIsMachineReadable(t *testing.T) {
	server := fakeServer(t, specV1)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	dumpTo(t, server, a)
	writeSpec(t, os.Getenv("MCPWATCH_FAKE_SPEC"), specV2)
	dumpTo(t, server, b)

	code, out, _ := run(t, "diff", "--format", "json", a, b)
	if code != ExitDiff {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out, `"name": "slugify"`) || !strings.Contains(out, `"oldCount": 1`) {
		t.Fatalf("json diff incomplete:\n%s", out)
	}
}

func TestDiffArgumentErrors(t *testing.T) {
	// Missing files are a runtime error (3)…
	code, _, errOut := run(t, "diff", "/nonexistent/a.json", "/nonexistent/b.json")
	if code != ExitRuntime || errOut == "" {
		t.Fatalf("missing files: code=%d stderr=%q", code, errOut)
	}
	// …while malformed invocations are usage errors (2).
	if code, _, _ := run(t, "diff", "only-one.json"); code != ExitUsage {
		t.Fatalf("one arg: code=%d", code)
	}
	code, _, errOut = run(t, "diff", "-", "-")
	if code != ExitUsage || !strings.Contains(errOut, "only one side") {
		t.Fatalf("double stdin: code=%d stderr=%q", code, errOut)
	}
}

func TestRunUsageErrorsExit2(t *testing.T) {
	code, _, errOut := run(t, "run", "--watch", ".")
	if code != ExitUsage || !strings.Contains(errOut, "no server command") {
		t.Fatalf("missing command: code=%d stderr=%q", code, errOut)
	}
	code, _, errOut = run(t, "run", "--poll", "0s", "--", "true")
	if code != ExitUsage || !strings.Contains(errOut, "--poll") {
		t.Fatalf("bad poll: code=%d stderr=%q", code, errOut)
	}
}

// newSession builds a run session against the fake server without
// starting the polling loop, so reload cycles can be driven directly.
func newSession(t *testing.T, server []string, dumpFile string) (*session, *strings.Builder) {
	t.Helper()
	var out strings.Builder
	opts := runOpts{server: server, dumpFile: dumpFile}
	opts.common.timeout = 10e9 // 10 s guard, never reached
	opts.common.killTimeout = 5e9
	return &session{opts: opts, stdout: &out, stderr: &out}, &out
}

func TestReloadPrintsInitialSurfaceThenDiffs(t *testing.T) {
	server := fakeServer(t, specV1)
	s, out := newSession(t, server, "")

	s.reload() // first successful reload prints the full surface
	if !strings.Contains(out.String(), "capability surface (1 tool, 0 resources, 1 prompt)") {
		t.Fatalf("initial banner missing:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "tools (1)") {
		t.Fatalf("initial dump missing:\n%s", out.String())
	}

	out.Reset()
	writeSpec(t, os.Getenv("MCPWATCH_FAKE_SPEC"), specV2)
	s.reload() // second reload prints only the diff
	got := out.String()
	for _, want := range []string{"tools 1 → 2", "+ slugify", "~ echo"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "tools (2)") {
		t.Fatalf("full dump must not repeat after a reload:\n%s", got)
	}
}

func TestReloadWithoutChangesSaysSo(t *testing.T) {
	server := fakeServer(t, specV1)
	s, out := newSession(t, server, "")
	s.reload()
	out.Reset()
	s.reload()
	if !strings.Contains(out.String(), "no capability changes (1 tool, 0 resources, 1 prompt)") {
		t.Fatalf("quiet reload message missing:\n%s", out.String())
	}
}

func TestReloadFailureKeepsDiffingAgainstLastGoodSnapshot(t *testing.T) {
	server := fakeServer(t, specV1)
	s, out := newSession(t, server, "")
	s.reload()

	// Break the server: reload must report failure and keep the old
	// snapshot as the baseline.
	out.Reset()
	writeSpec(t, os.Getenv("MCPWATCH_FAKE_SPEC"), `{"name":"broken","failStartup":"syntax error"}`)
	s.reload()
	if !strings.Contains(out.String(), "reload failed") {
		t.Fatalf("failure not reported:\n%s", out.String())
	}

	// Fix it with a different surface: the diff must be against v1,
	// not against the broken run.
	out.Reset()
	writeSpec(t, os.Getenv("MCPWATCH_FAKE_SPEC"), specV2)
	s.reload()
	if !strings.Contains(out.String(), "+ slugify") {
		t.Fatalf("diff after recovery must be against the last good surface:\n%s", out.String())
	}
}

func TestReloadWritesDumpFile(t *testing.T) {
	server := fakeServer(t, specV1)
	dumpFile := filepath.Join(t.TempDir(), "latest.json")
	s, _ := newSession(t, server, dumpFile)
	s.reload()
	data, err := os.ReadFile(dumpFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := capability.Decode(data); err != nil {
		t.Fatalf("--dump-file content must be a valid snapshot: %v", err)
	}
}

func TestBannerHelpers(t *testing.T) {
	// splitServerCommand: everything after -- belongs to the server.
	flags, server := splitServerCommand([]string{"--poll", "1s", "--", "python3", "-m", "server", "--debug"})
	if len(flags) != 2 || len(server) != 4 || server[3] != "--debug" {
		t.Fatalf("flags=%v server=%v", flags, server)
	}
	flags, server = splitServerCommand([]string{"--poll", "1s"})
	if server != nil || len(flags) != 2 {
		t.Fatalf("without -- there is no server command: %v %v", flags, server)
	}

	// summarizePaths caps the change list at three paths.
	if got := summarizePaths(changesOf("a", "b", "c", "d", "e")); got != "a, b, c and 2 more" {
		t.Fatalf("summarizePaths = %q", got)
	}
	if got := summarizePaths(changesOf("x")); got != "x" {
		t.Fatalf("single path = %q", got)
	}

	// shellJoin quotes arguments containing whitespace.
	if got := shellJoin([]string{"node", "my server.js"}); got != "node 'my server.js'" {
		t.Fatalf("shellJoin = %q", got)
	}
}
