//go:build unix

package mermaid

import (
	"os/exec"
	"syscall"
)

// configureCommand sets Unix-specific process settings.
// On timeout, killCommandGroup uses syscall.Kill(-pgid, SIGKILL) to reap mmdc's chrome subprocesses.
func configureCommand(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killCommandGroup kills the entire process group of cmd to reap Chrome/Puppeteer
// child processes that survive when mmdc is killed by context cancellation.
func killCommandGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
