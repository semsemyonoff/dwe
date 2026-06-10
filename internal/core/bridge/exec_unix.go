//go:build unix

package bridge

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the subprocess in its own process group so signals
// reach the whole dwe pipeline tree, not just the leader.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalProcessGroup delivers sig to the subprocess process group.
func signalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, sig)
}

// exitStatus maps an ExitError to the wire exit code; a signal death uses
// the shell convention 128+signal (e.g. SIGINT → 130).
func exitStatus(exitErr *exec.ExitError) int {
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return exitErr.ExitCode()
}
