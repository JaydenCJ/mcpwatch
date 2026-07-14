// Package runner owns the server process lifecycle: spawn with stdio
// pipes suitable for JSON-RPC, stream stderr through a prefixing
// writer, and stop with an escalation ladder (close stdin → SIGTERM →
// SIGKILL) so a hung server can never wedge the dev loop.
package runner

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Proc is one running server process.
type Proc struct {
	// Stdin is the pipe feeding the server's stdin; closing it is the
	// polite way to ask an MCP server to exit.
	Stdin io.WriteCloser
	// Stdout is the pipe reading the server's stdout.
	Stdout io.ReadCloser

	cmd     *exec.Cmd
	done    chan struct{}
	waitErr error
	stopped sync.Once
}

// Start launches command (argv form) with dir as working directory (""
// keeps the parent's) and stderr streamed to errw. The child inherits
// the parent environment.
func Start(command []string, dir string, errw io.Writer) (*Proc, error) {
	if len(command) == 0 {
		return nil, errors.New("empty server command")
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Stderr = errw
	setProcAttrs(cmd)

	// Manual os.Pipe pairs instead of StdinPipe/StdoutPipe: exec.Wait
	// force-closes its own pipes, which would race the JSON-RPC reader
	// still draining stdout.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		stdoutR.Close()
		stdoutW.Close()
		return nil, err
	}
	cmd.Stdout = stdoutW
	cmd.Stdin = stdinR

	if err := cmd.Start(); err != nil {
		stdoutR.Close()
		stdoutW.Close()
		stdinR.Close()
		stdinW.Close()
		return nil, fmt.Errorf("start %s: %w", command[0], err)
	}
	// The child holds its own ends now.
	stdoutW.Close()
	stdinR.Close()

	p := &Proc{Stdin: stdinW, Stdout: stdoutR, cmd: cmd, done: make(chan struct{})}
	go func() {
		p.waitErr = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

// PID returns the child's process id.
func (p *Proc) PID() int { return p.cmd.Process.Pid }

// Done is closed once the process has exited.
func (p *Proc) Done() <-chan struct{} { return p.done }

// Exited reports whether the process has already exited.
func (p *Proc) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// ExitDescription renders the exit state for log lines: "exited
// cleanly", "exit code 3", or "killed by signal: …".
func (p *Proc) ExitDescription() string {
	select {
	case <-p.done:
	default:
		return "still running"
	}
	if p.waitErr == nil {
		return "exited cleanly"
	}
	var ee *exec.ExitError
	if errors.As(p.waitErr, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return fmt.Sprintf("exit code %d", code)
		}
		return "killed by signal: " + ee.String()
	}
	return p.waitErr.Error()
}

// Stop shuts the process down: close stdin, wait up to grace, then
// SIGTERM, wait up to grace, then SIGKILL. It always returns once the
// process is gone and is safe to call more than once.
func (p *Proc) Stop(grace time.Duration) {
	p.stopped.Do(func() {
		_ = p.Stdin.Close()
		if p.awaitExit(grace) {
			p.Stdout.Close()
			return
		}
		terminate(p.cmd)
		if p.awaitExit(grace) {
			p.Stdout.Close()
			return
		}
		kill(p.cmd)
		<-p.done
		p.Stdout.Close()
	})
}

func (p *Proc) awaitExit(grace time.Duration) bool {
	if grace <= 0 {
		return p.Exited()
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-p.done:
		return true
	case <-timer.C:
		return false
	}
}

// PrefixWriter returns a writer that prepends prefix to every line it
// forwards to w — mcpwatch uses it to tag server stderr so users can
// tell their server's logs from the watcher's.
func PrefixWriter(w io.Writer, prefix string) io.Writer {
	return &prefixWriter{w: w, prefix: []byte(prefix), atStart: true}
}

type prefixWriter struct {
	w       io.Writer
	prefix  []byte
	atStart bool
	mu      sync.Mutex
}

func (pw *prefixWriter) Write(b []byte) (int, error) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	total := len(b)
	for len(b) > 0 {
		if pw.atStart {
			if _, err := pw.w.Write(pw.prefix); err != nil {
				return total - len(b), err
			}
			pw.atStart = false
		}
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			if _, err := pw.w.Write(b); err != nil {
				return total - len(b), err
			}
			break
		}
		if _, err := pw.w.Write(b[:i+1]); err != nil {
			return total - len(b), err
		}
		b = b[i+1:]
		pw.atStart = true
	}
	return total, nil
}
