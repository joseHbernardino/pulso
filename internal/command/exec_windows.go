//go:build windows

package command

import (
	"os"
	"os/exec"
)

// setProcessGroup is a no-op: Windows has no Setpgid.
func setProcessGroup(_ *exec.Cmd) {}

// terminateProcessGroup kills the command: Windows has no SIGTERM.
func terminateProcessGroup(proc *os.Process) error {
	return killProcessGroup(proc)
}

// killProcessGroup kills the command itself.
func killProcessGroup(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	return proc.Kill()
}
