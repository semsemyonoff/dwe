package bridge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/semsemyonoff/dwe/internal/shared/lock"
)

// Daemon process lifecycle files (design D8 on-disk layout). daemon.pid is a
// bridge-private flock — never reuse lock.AcquireProjectLocks here: the
// bridge daemon must be probeable/startable without touching the project
// deploy/snapshot locks.
const (
	pidFileName = "daemon.pid"
	logFileName = "daemon.log"

	logFilePerm = 0o644
)

// defaultCycleWait bounds how long Cycle waits for the SIGTERMed daemon to
// release the pidfile flock before giving up.
const defaultCycleWait = 5 * time.Second

// cyclePollInterval is the flock re-probe cadence inside Cycle's wait.
const cyclePollInterval = 50 * time.Millisecond

// PidPath returns the daemon pidfile path (PID + flock holder, design D8).
func PidPath(bridgeDir string) string { return filepath.Join(bridgeDir, pidFileName) }

// LogPath returns the daemon log file path (append-only; rotation is out of
// V1 scope, design D12).
func LogPath(bridgeDir string) string { return filepath.Join(bridgeDir, logFileName) }

// SpawnSpec describes the detached daemon process Ensure starts.
type SpawnSpec struct {
	// ExecPath is the dwe binary; empty means the current executable —
	// resolved inside the production spawn, never by tests (the
	// os.Executable test-recursion hazard, see LaunchFunc).
	ExecPath string
	// ProjectRoot is the absolute project root, passed as --project-root.
	ProjectRoot string
	// LogPath receives the daemon's stdout/stderr (append — design D6).
	LogPath string
}

// SpawnFunc is the injectable daemon-spawn seam: production detaches a
// `dwe bridge daemon` process (setsid, fds released); tests record the spec.
type SpawnFunc func(spec SpawnSpec) error

// terminateDaemon delivers the graceful-shutdown signal (SIGTERM) to a
// daemon by PID. Package var so tests can intercept: in tests the holder of
// the pidfile flock is the test process itself, and a real SIGTERM would
// kill the test run.
var terminateDaemon = terminateDaemonOS

// EnsureConfig configures the daemon process lifecycle operations
// (Ensure/Cycle — design D6). Only ProjectRoot is required.
type EnsureConfig struct {
	// ProjectRoot is the absolute project root, forwarded to the daemon as
	// --project-root.
	ProjectRoot string
	// BridgeDir overrides the runtime directory; empty means
	// DefaultBridgeDir(ProjectRoot).
	BridgeDir string
	// ExecPath is the dwe binary to spawn; empty means the current
	// executable (resolved lazily by the production spawn).
	ExecPath string
	// Spawn overrides daemon process creation; nil means the production
	// detached spawn.
	Spawn SpawnFunc
	// WaitTimeout bounds Cycle's wait for the old daemon to exit after
	// SIGTERM; 0 means defaultCycleWait.
	WaitTimeout time.Duration
}

func (cfg EnsureConfig) bridgeDir() (string, error) {
	if !filepath.IsAbs(cfg.ProjectRoot) {
		return "", fmt.Errorf("bridge: project root must be absolute, got %q", cfg.ProjectRoot)
	}
	if cfg.BridgeDir != "" {
		return cfg.BridgeDir, nil
	}
	return DefaultBridgeDir(cfg.ProjectRoot), nil
}

func (cfg EnsureConfig) waitTimeout() time.Duration {
	if cfg.WaitTimeout > 0 {
		return cfg.WaitTimeout
	}
	return defaultCycleWait
}

// Ensure makes sure a bridge daemon is running for the project (idempotent,
// design D6): the pidfile flock acquired means the previous daemon is dead —
// its stale endpoints are removed and a fresh detached daemon spawned; the
// flock held means a daemon is alive and Ensure is a no-op. The spawned
// daemon acquires the flock itself on startup; a concurrent Ensure losing
// that race only spawns a daemon that sees the flock held and exits cleanly.
// Returns started=true when a new daemon process was spawned.
func Ensure(cfg EnsureConfig) (started bool, err error) {
	bridgeDir, err := cfg.bridgeDir()
	if err != nil {
		return false, err
	}
	// Created here (0700, design D3) rather than by lock.Acquire, whose
	// MkdirAll would apply a more permissive mode. The explicit Chmod
	// tightens a pre-existing dir too — MkdirAll leaves existing modes alone.
	if err := os.MkdirAll(bridgeDir, bridgeDirPerm); err != nil {
		return false, fmt.Errorf("bridge: creating bridge dir: %w", err)
	}
	if err := os.Chmod(bridgeDir, bridgeDirPerm); err != nil {
		return false, fmt.Errorf("bridge: tightening bridge dir permissions: %w", err)
	}

	l, err := lock.Acquire(PidPath(bridgeDir))
	if err != nil {
		if isLockHeld(err) {
			return false, nil // daemon alive — no-op
		}
		return false, fmt.Errorf("bridge: probing daemon pidfile: %w", err)
	}

	// Flock acquired ⇒ previous daemon dead. Remove its stale endpoints so
	// no shim dials a dead socket while the new daemon boots (it rewrites
	// both on startup); the token is kept — stable project identity (D6).
	for _, name := range []string{sockFileName, portFileName} {
		if err := os.Remove(filepath.Join(bridgeDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = l.Release()
			return false, fmt.Errorf("bridge: removing stale %s: %w", name, err)
		}
	}
	if err := l.Release(); err != nil {
		return false, fmt.Errorf("bridge: releasing pidfile probe: %w", err)
	}

	spawn := cfg.Spawn
	if spawn == nil {
		spawn = spawnDetached
	}
	if err := spawn(SpawnSpec{
		ExecPath:    cfg.ExecPath,
		ProjectRoot: cfg.ProjectRoot,
		LogPath:     LogPath(bridgeDir),
	}); err != nil {
		return false, fmt.Errorf("bridge: spawning daemon: %w", err)
	}
	return true, nil
}

// Cycle restarts the daemon: SIGTERM to the current holder, wait for the
// pidfile flock to be released (the daemon's exit), then Ensure. Used by
// `dwe deploy run` and whole-stack restart (design D6) so the daemon is
// never from an older dwe build. It is a single action replacing plain
// Ensure — never call it after an Ensure, or it SIGTERMs the daemon just
// spawned.
func Cycle(cfg EnsureConfig) (started bool, err error) {
	bridgeDir, err := cfg.bridgeDir()
	if err != nil {
		return false, err
	}
	signaled, err := StopDaemon(bridgeDir)
	if err != nil {
		return false, err
	}
	if signaled {
		if err := waitPidfileReleased(PidPath(bridgeDir), cfg.waitTimeout()); err != nil {
			return false, err
		}
	}
	return Ensure(cfg)
}

// DaemonProbe is the result of the pidfile-flock liveness probe.
type DaemonProbe struct {
	// Running reports whether a live process holds the pidfile flock.
	Running bool
	// PID is the flock holder's pid; 0 when not running or when the pidfile
	// content was unreadable.
	PID int
	// StartedAt approximates the daemon start time: the pidfile is written
	// exactly once per daemon lifetime — by its own startup acquire — so the
	// file mtime is the start time while the flock is held.
	StartedAt time.Time
}

// ProbeDaemon performs the pidfile-flock liveness probe (design D6): the
// flock held means a daemon is alive; acquired (and released again) means no
// daemon. A missing pidfile is a clean not-running result.
func ProbeDaemon(bridgeDir string) (DaemonProbe, error) {
	pidPath := PidPath(bridgeDir)
	info, err := os.Stat(pidPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DaemonProbe{}, nil
		}
		return DaemonProbe{}, fmt.Errorf("bridge: probing daemon pidfile: %w", err)
	}
	l, err := lock.Acquire(pidPath)
	if err == nil {
		// Nobody holds the flock — no live daemon.
		return DaemonProbe{}, l.Release()
	}
	held, ok := errors.AsType[*lock.HeldError](err)
	if !ok {
		return DaemonProbe{}, fmt.Errorf("bridge: probing daemon pidfile: %w", err)
	}
	return DaemonProbe{Running: true, PID: held.PID, StartedAt: info.ModTime()}, nil
}

// StopDaemon delivers SIGTERM to the daemon holding the pidfile flock
// (design D6: whole-stack stop / reset run). A missing pidfile or a stale
// one (holder dead) is a clean no-op. Returns signaled=true when a live
// daemon was sent the signal; it shuts down asynchronously — Cycle is the
// caller that waits.
func StopDaemon(bridgeDir string) (signaled bool, err error) {
	probe, err := ProbeDaemon(bridgeDir)
	if err != nil {
		return false, err
	}
	if !probe.Running || probe.PID <= 0 {
		return false, nil
	}
	if err := terminateDaemon(probe.PID); err != nil {
		return false, fmt.Errorf("bridge: signaling daemon (pid %d): %w", probe.PID, err)
	}
	return true, nil
}

// waitPidfileReleased polls the pidfile flock until the dying daemon
// releases it (process exit drops the flock) or the timeout elapses.
func waitPidfileReleased(pidPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		l, err := lock.Acquire(pidPath)
		if err == nil {
			return l.Release()
		}
		if !isLockHeld(err) {
			return fmt.Errorf("bridge: waiting for daemon exit: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("bridge: daemon did not release %s within %s after SIGTERM", pidFileName, timeout)
		}
		time.Sleep(cyclePollInterval)
	}
}

// isLockHeld reports whether a lock.Acquire error means "held by a live
// process" (as opposed to an I/O failure).
func isLockHeld(err error) bool {
	_, ok := errors.AsType[*lock.HeldError](err)
	return ok || errors.Is(err, lock.ErrLockHeld)
}
