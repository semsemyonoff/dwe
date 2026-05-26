//go:build unix

package mermaid

import (
	"os/exec"
	"syscall"
)

// configureCommand sets Unix-specific process settings.
// On timeout, we'll use syscall.Kill(-pgid, SIGKILL) to reap mmdc's chrome subprocesses.
func configureCommand(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
