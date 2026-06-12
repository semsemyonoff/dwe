//go:build unix

package bridge

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// spawnDetached starts `dwe bridge daemon --project-root <root>` fully
// detached (design D6 "double-fork + setsid" equivalent): its own session
// via Setsid, stdin from the null device, stdout/stderr appended to
// daemon.log, and the process handle released so the daemon is reparented
// to init when the spawning command exits.
func spawnDetached(spec SpawnSpec) error {
	execPath := spec.ExecPath
	if execPath == "" {
		var err error
		execPath, err = os.Executable()
		if err != nil {
			return fmt.Errorf("resolving dwe executable: %w", err)
		}
	}
	logFile, err := os.OpenFile(spec.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logFilePerm)
	if err != nil {
		return fmt.Errorf("opening daemon log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	cmd := exec.Command(execPath, "bridge", "daemon", "--project-root", spec.ProjectRoot)
	cmd.Dir = spec.ProjectRoot
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// terminateDaemonOS delivers SIGTERM. An already-dead pid (ESRCH) is not an
// error — the pidfile flock release is the authoritative liveness signal.
func terminateDaemonOS(pid int) error {
	err := syscall.Kill(pid, syscall.SIGTERM)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
