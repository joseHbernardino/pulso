//go:build !windows

package command

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup puts the command in its own process group so the whole tree
// a script spawns can be signalled at once.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminateProcessGroup asks the command and everything it started to end.
func terminateProcessGroup(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	return syscall.Kill(-proc.Pid, syscall.SIGTERM)
}

// killProcessGroup kills the command and everything it started.
func killProcessGroup(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	return syscall.Kill(-proc.Pid, syscall.SIGKILL)
}
