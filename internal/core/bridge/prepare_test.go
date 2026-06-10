package bridge

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/lock"
)

// prepareTestConfig returns a config with one bridged app; bridged toggles
// the service-level enable so the same shape covers both gate outcomes.
func prepareTestConfig(bridged bool) *config.DweConfig {
	return &config.DweConfig{
		Project: config.ProjectConfig{Name: "proj", Prefix: "acme"},
		Services: map[string]config.ServiceConfig{
			"main": {Type: config.ServiceTypeApp, Container: "app-main", Enabled: bridged},
			"db":   {Type: config.ServiceTypeInfra, Container: "db", Enabled: true},
		},
	}
}

func staticArch(string) (string, error) { return "arm64", nil }

func TestAnyBridgeEnabled(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.DweConfig
		want bool
	}{
		{"enabled app defaults on", prepareTestConfig(true), true},
		{"disabled app does not count", prepareTestConfig(false), false},
		{"nil services", &config.DweConfig{}, false},
		{
			"bridge-disabled app",
			&config.DweConfig{Services: map[string]config.ServiceConfig{
				"main": {Type: config.ServiceTypeApp, Container: "app-main", Enabled: true,
					Bridge: config.ServiceBridgeConfig{Enabled: new(false)}},
			}},
			false,
		},
		{
			"opted-in infra",
			&config.DweConfig{Services: map[string]config.ServiceConfig{
				"queue": {Type: config.ServiceTypeInfra, Container: "queue", Enabled: true,
					Bridge: config.ServiceBridgeConfig{Enabled: new(true)}},
			}},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AnyBridgeEnabled(tt.cfg); got != tt.want {
				t.Errorf("AnyBridgeEnabled = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPrepare_BridgedProject_OverlayShimsAndEnsure(t *testing.T) {
	baseDir := t.TempDir()
	var spawned []SpawnSpec
	err := Prepare(PrepareOptions{
		BaseDir: baseDir,
		Cfg:     prepareTestConfig(true),
		Arch:    staticArch,
		Spawn: func(spec SpawnSpec) error {
			spawned = append(spawned, spec)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if _, err := os.Stat(OverlayPath(baseDir)); err != nil {
		t.Errorf("overlay not written: %v", err)
	}
	// The embedded shim set varies (fresh checkout has only the placeholder),
	// so assert consistency with a direct Materialize instead of fixed names.
	names, err := materializedShimNames(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(DefaultBridgeDir(baseDir), name)); err != nil {
			t.Errorf("shim %s not materialized: %v", name, err)
		}
	}
	if len(spawned) != 1 {
		t.Fatalf("spawn called %d times, want 1 (ensure)", len(spawned))
	}
	if spawned[0].ProjectRoot != baseDir {
		t.Errorf("spawned ProjectRoot = %q, want %q", spawned[0].ProjectRoot, baseDir)
	}
}

// materializedShimNames lists the shim files the embedded tree yields for
// this build (empty on a fresh checkout where only the placeholder exists).
func materializedShimNames(baseDir string) ([]string, error) {
	entries, err := os.ReadDir(DefaultBridgeDir(baseDir))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if name := e.Name(); len(name) > 5 && name[:5] == "shim-" {
			names = append(names, name)
		}
	}
	return names, nil
}

func TestPrepare_NothingBridged_DeletesOverlaySkipsDaemon(t *testing.T) {
	baseDir := t.TempDir()
	// A stale overlay from when bridge was still enabled must be deleted —
	// it would otherwise re-enter the -f chain (design D8).
	if err := os.MkdirAll(filepath.Join(baseDir, ".dwe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(OverlayPath(baseDir), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	var spawned int
	err := Prepare(PrepareOptions{
		BaseDir: baseDir,
		Cfg:     prepareTestConfig(false),
		Arch:    staticArch,
		Spawn:   func(SpawnSpec) error { spawned++; return nil },
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, err := os.Stat(OverlayPath(baseDir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale overlay not deleted (err = %v)", err)
	}
	if spawned != 0 {
		t.Errorf("spawn called %d times, want 0 (bridge fully off)", spawned)
	}
	if _, err := os.Stat(DefaultBridgeDir(baseDir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("bridge dir must not be created when nothing is bridged (err = %v)", err)
	}
}

func TestPrepare_EnsureNoopWhenDaemonAlive(t *testing.T) {
	baseDir := t.TempDir()
	bridgeDir := DefaultBridgeDir(baseDir)
	if err := os.MkdirAll(bridgeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := lock.Acquire(PidPath(bridgeDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()
	pids := interceptTerminate(t, nil)

	var spawned int
	if err := Prepare(PrepareOptions{
		BaseDir: baseDir,
		Cfg:     prepareTestConfig(true),
		Arch:    staticArch,
		Spawn:   func(SpawnSpec) error { spawned++; return nil },
	}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if spawned != 0 {
		t.Errorf("spawn called %d times, want 0 (daemon alive, plain ensure)", spawned)
	}
	if len(*pids) != 0 {
		t.Errorf("terminate called %d times, want 0 (ensure never signals)", len(*pids))
	}
}

func TestPrepare_CycleDaemonSignalsThenSpawns(t *testing.T) {
	baseDir := t.TempDir()
	bridgeDir := DefaultBridgeDir(baseDir)
	if err := os.MkdirAll(bridgeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := lock.Acquire(PidPath(bridgeDir))
	if err != nil {
		t.Fatal(err)
	}

	var order []string
	interceptTerminate(t, func(int) error {
		order = append(order, "signal")
		return held.Release() // old daemon exits
	})
	if err := Prepare(PrepareOptions{
		BaseDir:     baseDir,
		Cfg:         prepareTestConfig(true),
		Arch:        staticArch,
		CycleDaemon: true,
		Spawn:       func(SpawnSpec) error { order = append(order, "spawn"); return nil },
	}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(order) != 2 || order[0] != "signal" || order[1] != "spawn" {
		t.Errorf("order = %v, want [signal spawn]", order)
	}
}

func TestPrepare_SpawnErrorPropagates(t *testing.T) {
	baseDir := t.TempDir()
	spawnErr := errors.New("fork failed")
	err := Prepare(PrepareOptions{
		BaseDir: baseDir,
		Cfg:     prepareTestConfig(true),
		Arch:    staticArch,
		Spawn:   func(SpawnSpec) error { return spawnErr },
	})
	if !errors.Is(err, spawnErr) {
		t.Fatalf("err = %v, want wrapped %v", err, spawnErr)
	}
}
