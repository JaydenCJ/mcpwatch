//go:build unix

package runner

import (
	"os/exec"
	"syscall"
)

// setProcAttrs puts the server in its own process group so that
// wrapper commands (npm run, make, shell scripts) are terminated
// together with the children they spawned.
func setProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate sends SIGTERM to the whole process group, falling back to
// the single process if the group signal fails.
func terminate(cmd *exec.Cmd) {
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
}

// kill sends SIGKILL the same way.
func kill(cmd *exec.Cmd) {
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
