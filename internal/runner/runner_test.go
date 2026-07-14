// Tests for process lifecycle management. They spawn tiny real
// processes (cat, sh) because pipe wiring and signal escalation are
// exactly the things a fake would hide; every wait is on a process
// that is guaranteed to exit, so nothing here is timing-sensitive.
package runner

import (
	"bufio"
	"strings"
	"testing"
	"time"
)

func TestStartWiresStdinToStdout(t *testing.T) {
	p, err := Start([]string{"cat"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop(time.Second)
	if _, err := p.Stdin.Write([]byte("hello over the pipe\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(p.Stdout).ReadString('\n')
	if err != nil || line != "hello over the pipe\n" {
		t.Fatalf("line=%q err=%v", line, err)
	}
}

func TestStopViaStdinCloseIsCleanAndIdempotent(t *testing.T) {
	p, err := Start([]string{"cat"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Stop(5 * time.Second) // cat exits on EOF ⇒ returns long before the grace
	if desc := p.ExitDescription(); desc != "exited cleanly" {
		t.Fatalf("ExitDescription = %q", desc)
	}
	p.Stop(5 * time.Second) // second call must be a no-op, not a panic
}

func TestStopEscalatesToKillForAStubbornProcess(t *testing.T) {
	// The child ignores SIGTERM and never reads stdin, so only the
	// final SIGKILL can end it. Tiny grace keeps the test fast; the
	// outcome is the same at any grace value.
	p, err := Start([]string{"sh", "-c", `trap "" TERM; while :; do sleep 1; done`}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Stop(30 * time.Millisecond)
	if !p.Exited() {
		t.Fatal("process must be gone after Stop returns")
	}
	if desc := p.ExitDescription(); !strings.Contains(desc, "signal") {
		t.Fatalf("expected a killed-by-signal description, got %q", desc)
	}
}

func TestExitCodeIsReported(t *testing.T) {
	p, err := Start([]string{"sh", "-c", "exit 3"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	<-p.Done()
	if desc := p.ExitDescription(); desc != "exit code 3" {
		t.Fatalf("ExitDescription = %q", desc)
	}
}

func TestExitDescriptionWhileRunning(t *testing.T) {
	p, err := Start([]string{"cat"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop(time.Second)
	if desc := p.ExitDescription(); desc != "still running" {
		t.Fatalf("ExitDescription = %q", desc)
	}
	if p.Exited() {
		t.Fatal("cat with open stdin must still be running")
	}
}

func TestStderrIsForwardedToTheGivenWriter(t *testing.T) {
	var buf strings.Builder
	p, err := Start([]string{"sh", "-c", "echo boom >&2"}, "", PrefixWriter(&buf, "[server] "))
	if err != nil {
		t.Fatal(err)
	}
	<-p.Done()
	if got := buf.String(); got != "[server] boom\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestStartRejectsMissingBinaryAndEmptyCommand(t *testing.T) {
	if _, err := Start([]string{"definitely-not-a-real-binary-xyz"}, "", nil); err == nil {
		t.Fatal("missing binary must fail Start")
	}
	if _, err := Start(nil, "", nil); err == nil {
		t.Fatal("empty command must fail Start")
	}
}

func TestWorkingDirectoryIsApplied(t *testing.T) {
	dir := t.TempDir()
	p, err := Start([]string{"pwd"}, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(p.Stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	<-p.Done()
	// pwd may print a symlink-resolved path; compare suffixes.
	if !strings.HasSuffix(strings.TrimSpace(line), strings.TrimPrefix(dir, "/private")) {
		t.Fatalf("pwd = %q, want %q", line, dir)
	}
}

func TestPrefixWriterSplitsAndPrefixesLines(t *testing.T) {
	var buf strings.Builder
	w := PrefixWriter(&buf, "> ")
	// Write in awkward chunks: mid-line boundaries and multi-line blobs.
	for _, chunk := range []string{"first", " line\nsecond line\nthi", "rd\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	want := "> first line\n> second line\n> third\n"
	if buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}

	// An unterminated tail must not get a spurious second prefix.
	buf.Reset()
	w = PrefixWriter(&buf, "> ")
	w.Write([]byte("no newline yet"))
	w.Write([]byte(" …still going\n"))
	if buf.String() != "> no newline yet …still going\n" {
		t.Fatalf("got %q", buf.String())
	}
}
