//go:build !unix

package runner

import "os/exec"

// setProcAttrs is a no-op on platforms without POSIX process groups.
func setProcAttrs(cmd *exec.Cmd) {}

// terminate degrades to Kill where SIGTERM is unavailable.
func terminate(cmd *exec.Cmd) { _ = cmd.Process.Kill() }

// kill force-kills the process.
func kill(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
