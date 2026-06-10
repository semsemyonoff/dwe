package bridge

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/shared/lock"
)

// ensureTestSetup returns an EnsureConfig over a fresh project root with a
// recording spawn seam. Tests holding the pidfile flock simulate a live
// daemon: the holder pid in the file is the test process, which is alive.
func ensureTestSetup(t *testing.T) (EnsureConfig, *[]SpawnSpec) {
	t.Helper()
	root := t.TempDir()
	var spawned []SpawnSpec
	cfg := EnsureConfig{
		ProjectRoot: root,
		Spawn: func(spec SpawnSpec) error {
			spawned = append(spawned, spec)
			return nil
		},
	}
	return cfg, &spawned
}

// interceptTerminate swaps the SIGTERM seam for the test's lifetime. The
// pidfile flock holder in these tests is the test process itself — a real
// SIGTERM would kill the run.
func interceptTerminate(t *testing.T, fn func(pid int) error) *[]int {
	t.Helper()
	var pids []int
	prev := terminateDaemon
	terminateDaemon = func(pid int) error {
		pids = append(pids, pid)
		if fn != nil {
			return fn(pid)
		}
		return nil
	}
	t.Cleanup(func() { terminateDaemon = prev })
	return &pids
}

func TestEnsureSpawnsAndCleansStaleEndpoints(t *testing.T) {
	cfg, spawned := ensureTestSetup(t)
	bridgeDir := DefaultBridgeDir(cfg.ProjectRoot)
	if err := os.MkdirAll(bridgeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Leftovers from a dead daemon: endpoints must go, the token must stay.
	for _, name := range []string{sockFileName, portFileName, tokenFileName} {
		if err := os.WriteFile(filepath.Join(bridgeDir, name), []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	started, err := Ensure(cfg)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !started {
		t.Fatal("Ensure: started = false, want true")
	}
	if len(*spawned) != 1 {
		t.Fatalf("spawn called %d times, want 1", len(*spawned))
	}
	spec := (*spawned)[0]
	if spec.ProjectRoot != cfg.ProjectRoot {
		t.Errorf("spec.ProjectRoot = %q, want %q", spec.ProjectRoot, cfg.ProjectRoot)
	}
	if want := LogPath(bridgeDir); spec.LogPath != want {
		t.Errorf("spec.LogPath = %q, want %q", spec.LogPath, want)
	}
	for _, name := range []string{sockFileName, portFileName} {
		if _, err := os.Stat(filepath.Join(bridgeDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("stale %s not removed (err = %v)", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(bridgeDir, tokenFileName)); err != nil {
		t.Errorf("token file must survive ensure: %v", err)
	}
}

func TestEnsureCreatesBridgeDirRestrictive(t *testing.T) {
	cfg, _ := ensureTestSetup(t)
	bridgeDir := DefaultBridgeDir(cfg.ProjectRoot)

	if _, err := Ensure(cfg); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	info, err := os.Stat(bridgeDir)
	if err != nil {
		t.Fatalf("bridge dir not created: %v", err)
	}
	if got := info.Mode().Perm(); got != bridgeDirPerm {
		t.Errorf("bridge dir mode = %o, want %o", got, bridgeDirPerm)
	}
}

func TestEnsureForwardsExecPath(t *testing.T) {
	cfg, spawned := ensureTestSetup(t)
	cfg.ExecPath = "/opt/dwe/bin/dwe"

	if _, err := Ensure(cfg); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got := (*spawned)[0].ExecPath; got != cfg.ExecPath {
		t.Errorf("spec.ExecPath = %q, want %q", got, cfg.ExecPath)
	}
}

func TestEnsureNoopWhenDaemonAlive(t *testing.T) {
	cfg, spawned := ensureTestSetup(t)
	bridgeDir := DefaultBridgeDir(cfg.ProjectRoot)
	if err := os.MkdirAll(bridgeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := lock.Acquire(PidPath(bridgeDir))
	if err != nil {
		t.Fatalf("acquiring pidfile as fake daemon: %v", err)
	}
	defer func() { _ = held.Release() }()

	started, err := Ensure(cfg)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if started {
		t.Error("Ensure: started = true, want false (daemon alive)")
	}
	if len(*spawned) != 0 {
		t.Errorf("spawn called %d times, want 0", len(*spawned))
	}
}

func TestEnsureRejectsRelativeRoot(t *testing.T) {
	_, err := Ensure(EnsureConfig{ProjectRoot: "relative/path"})
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("Ensure(relative root) err = %v, want absolute-path error", err)
	}
}

func TestEnsureSpawnErrorPropagates(t *testing.T) {
	cfg, _ := ensureTestSetup(t)
	spawnErr := errors.New("fork failed")
	cfg.Spawn = func(SpawnSpec) error { return spawnErr }

	started, err := Ensure(cfg)
	if started {
		t.Error("started = true on spawn failure")
	}
	if !errors.Is(err, spawnErr) {
		t.Fatalf("err = %v, want wrapped %v", err, spawnErr)
	}
}

func TestProbeDaemonNoPidfile(t *testing.T) {
	dir := t.TempDir()
	bridgeDir := filepath.Join(dir, ".dwe", "bridge") // never created

	probe, err := ProbeDaemon(bridgeDir)
	if err != nil {
		t.Fatalf("ProbeDaemon: %v", err)
	}
	if probe.Running || probe.PID != 0 {
		t.Errorf("probe = %+v, want zero (not running)", probe)
	}
	if _, err := os.Stat(bridgeDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ProbeDaemon must not create the bridge dir (err = %v)", err)
	}
}

func TestProbeDaemonStalePidfile(t *testing.T) {
	bridgeDir := t.TempDir()
	// Pidfile exists but nobody holds the flock — a dead daemon.
	l, err := lock.Acquire(PidPath(bridgeDir))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}

	probe, err := ProbeDaemon(bridgeDir)
	if err != nil {
		t.Fatalf("ProbeDaemon: %v", err)
	}
	if probe.Running {
		t.Error("Running = true, want false (stale pidfile)")
	}
}

func TestProbeDaemonReportsHolder(t *testing.T) {
	bridgeDir := t.TempDir()
	held, err := lock.Acquire(PidPath(bridgeDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()
	// The pidfile is written once, at daemon startup — its mtime is the
	// start time. Backdate it to verify StartedAt comes from the stat.
	started := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(PidPath(bridgeDir), started, started); err != nil {
		t.Fatal(err)
	}

	probe, err := ProbeDaemon(bridgeDir)
	if err != nil {
		t.Fatalf("ProbeDaemon: %v", err)
	}
	if !probe.Running {
		t.Fatal("Running = false, want true (flock held)")
	}
	if probe.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", probe.PID, os.Getpid())
	}
	if !probe.StartedAt.Equal(started) {
		t.Errorf("StartedAt = %v, want pidfile mtime %v", probe.StartedAt, started)
	}
}

func TestStopDaemonNoPidfile(t *testing.T) {
	dir := t.TempDir()
	bridgeDir := filepath.Join(dir, ".dwe", "bridge") // never created

	signaled, err := StopDaemon(bridgeDir)
	if err != nil {
		t.Fatalf("StopDaemon: %v", err)
	}
	if signaled {
		t.Error("signaled = true, want false")
	}
	if _, err := os.Stat(bridgeDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("StopDaemon must not create the bridge dir (err = %v)", err)
	}
}

func TestStopDaemonStalePidfile(t *testing.T) {
	bridgeDir := t.TempDir()
	pids := interceptTerminate(t, nil)
	// Pidfile exists but nobody holds the flock — a dead daemon.
	l, err := lock.Acquire(PidPath(bridgeDir))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}

	signaled, err := StopDaemon(bridgeDir)
	if err != nil {
		t.Fatalf("StopDaemon: %v", err)
	}
	if signaled {
		t.Error("signaled = true, want false (stale pidfile)")
	}
	if len(*pids) != 0 {
		t.Errorf("terminate called %d times, want 0", len(*pids))
	}
}

func TestStopDaemonSignalsHolder(t *testing.T) {
	bridgeDir := t.TempDir()
	pids := interceptTerminate(t, nil)
	held, err := lock.Acquire(PidPath(bridgeDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()

	signaled, err := StopDaemon(bridgeDir)
	if err != nil {
		t.Fatalf("StopDaemon: %v", err)
	}
	if !signaled {
		t.Fatal("signaled = false, want true")
	}
	if len(*pids) != 1 || (*pids)[0] != os.Getpid() {
		t.Errorf("terminate pids = %v, want [%d]", *pids, os.Getpid())
	}
}

func TestStopDaemonTerminateErrorPropagates(t *testing.T) {
	bridgeDir := t.TempDir()
	termErr := errors.New("kill refused")
	interceptTerminate(t, func(int) error { return termErr })
	held, err := lock.Acquire(PidPath(bridgeDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()

	signaled, err := StopDaemon(bridgeDir)
	if signaled {
		t.Error("signaled = true on terminate failure")
	}
	if !errors.Is(err, termErr) {
		t.Fatalf("err = %v, want wrapped %v", err, termErr)
	}
}

func TestCycleSignalsThenSpawns(t *testing.T) {
	cfg, _ := ensureTestSetup(t)
	bridgeDir := DefaultBridgeDir(cfg.ProjectRoot)
	if err := os.MkdirAll(bridgeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := lock.Acquire(PidPath(bridgeDir))
	if err != nil {
		t.Fatal(err)
	}

	// Order matters: SIGTERM must precede the spawn (a cycle is never
	// "ensure then SIGTERM the daemon just spawned" — design D6).
	var order []string
	interceptTerminate(t, func(int) error {
		order = append(order, "signal")
		// Simulate the old daemon exiting: drop the flock.
		return held.Release()
	})
	cfg.Spawn = func(SpawnSpec) error {
		order = append(order, "spawn")
		return nil
	}

	started, err := Cycle(cfg)
	if err != nil {
		t.Fatalf("Cycle: %v", err)
	}
	if !started {
		t.Error("started = false, want true")
	}
	want := []string{"signal", "spawn"}
	if len(order) != len(want) || order[0] != want[0] || order[1] != want[1] {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestCycleWithoutRunningDaemonJustEnsures(t *testing.T) {
	cfg, spawned := ensureTestSetup(t)
	pids := interceptTerminate(t, nil)

	started, err := Cycle(cfg)
	if err != nil {
		t.Fatalf("Cycle: %v", err)
	}
	if !started {
		t.Error("started = false, want true")
	}
	if len(*pids) != 0 {
		t.Errorf("terminate called %d times, want 0", len(*pids))
	}
	if len(*spawned) != 1 {
		t.Errorf("spawn called %d times, want 1", len(*spawned))
	}
}

func TestCycleTimesOutWhenDaemonWontDie(t *testing.T) {
	cfg, spawned := ensureTestSetup(t)
	cfg.WaitTimeout = 150 * time.Millisecond
	bridgeDir := DefaultBridgeDir(cfg.ProjectRoot)
	if err := os.MkdirAll(bridgeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := lock.Acquire(PidPath(bridgeDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()
	interceptTerminate(t, nil) // signal recorded, daemon "ignores" it

	started, err := Cycle(cfg)
	if started {
		t.Error("started = true, want false")
	}
	if err == nil || !strings.Contains(err.Error(), "did not release") {
		t.Fatalf("err = %v, want pidfile-release timeout", err)
	}
	if len(*spawned) != 0 {
		t.Errorf("spawn called %d times after timeout, want 0", len(*spawned))
	}
}
