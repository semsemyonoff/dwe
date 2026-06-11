//go:build windows

package bridge

import (
	"os/exec"
	"syscall"
)

// The bridge daemon does not run on Windows hosts (design D2 — host-side
// Windows dwe is out of scope; WSL2-distro dwe is the Linux case). These
// stubs only keep the package compiling for cross-builds.

func setProcessGroup(_ *exec.Cmd) {}

// signalProcessGroup degrades to a hard kill: Windows has no process groups
// or graceful POSIX signals for unrelated processes.
func signalProcessGroup(cmd *exec.Cmd, _ syscall.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func exitStatus(exitErr *exec.ExitError) int {
	return exitErr.ExitCode()
}
