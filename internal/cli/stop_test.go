package command

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/shared/docker"
)

func TestStopCmd_Use(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := newStopCmd(flags)
	if cmd.Use != "stop [service]" {
		t.Errorf("Use = %q, want %q", cmd.Use, "stop [service]")
	}
}

func TestStopCmd_MaximumOneArg(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := newStopCmd(flags)

	// Zero args allowed.
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("expected zero args to be allowed, got: %v", err)
	}
	// One arg allowed.
	if err := cmd.Args(cmd, []string{"postgres"}); err != nil {
		t.Errorf("expected one arg to be allowed, got: %v", err)
	}
	// Two args rejected.
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("expected error when passing two arguments to stop command")
	}
}

func TestStopCmd_FlagsExist(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := newStopCmd(flags)
	if cmd.Flags().Lookup("yes") == nil {
		t.Error("missing --yes flag")
	}
	if cmd.Flags().Lookup("skip-preflight") == nil {
		t.Error("missing --skip-preflight flag")
	}
}

func TestStopCmd_RegisteredAtRoot(t *testing.T) {
	root := NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "stop" {
			return
		}
	}
	t.Error("stop command not registered at root level")
}

func TestRunStop_MissingLifecycleYML(t *testing.T) {
	// lifecycle.yml is optional for stop — the auto-injected reap phase
	// runs alone.
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	root := NewRootCmd()
	root.SetArgs([]string{"stop"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("stop with missing lifecycle.yml should succeed, got: %v", err)
	}
}

func TestRunStop_MissingStopSection(t *testing.T) {
	// lifecycle.yml without a stop: section is fine — auto-reap runs alone.
	dir := t.TempDir()
	cfgPath := makeMinimalDevboxYML(t, dir)

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}
	lifecycleYAML := "run:\n  final_message: ready\n  phases: []\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	root := NewRootCmd()
	root.SetArgs([]string{"stop"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("stop with no stop: section should succeed, got: %v", err)
	}
}

// writeStopTestConfig creates a temp dir with devbox.yml and a service folder.
func writeStopTestConfig(t *testing.T, services map[string]struct {
	enabled   bool
	container string
}) string {
	t.Helper()
	dir := t.TempDir()
	content := "schema_version: \"2\"\nproject:\n  name: test\n  prefix: devbox\n"
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}
	for name, spec := range services {
		svcDir := filepath.Join(dir, "devbox", "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		container := spec.container
		if container == "" {
			container = "app-" + name
		}
		yml := "type: app\ncontainer: " + container + "\n"
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(yml), 0o644); err != nil {
			t.Fatalf("write service.yml: %v", err)
		}
	}
	return filepath.Join(dir, "devbox.yml")
}

func TestStopService_UnknownService(t *testing.T) {
	cfgPath := writeStopTestConfig(t, map[string]struct {
		enabled   bool
		container string
	}{
		"postgres": {enabled: true, container: "pg"},
	})
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	baseDir := filepath.Dir(cfgPath)
	deps := StopServiceDeps{
		Cfg:           cfg,
		CmdRegistry:   nil,
		BaseDir:       baseDir,
		ErrOut:        nil,
		SkipPreflight: true,
	}
	err = StopService(context.Background(), deps, "nonexistent")
	if !errors.Is(err, ErrUnknownService) {
		t.Errorf("expected ErrUnknownService, got %v", err)
	}
}

func TestStopService_KnownEnabledService(t *testing.T) {
	cfgPath := writeStopTestConfig(t, map[string]struct {
		enabled   bool
		container string
	}{
		"postgres": {enabled: true, container: "pg"},
	})
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	baseDir := filepath.Dir(cfgPath)

	var gotBin, gotName string
	var gotTimeout int
	prev := stopContainerFn
	t.Cleanup(func() { stopContainerFn = prev })
	stopContainerFn = func(_ context.Context, bin, name string, timeout int) error {
		gotBin = bin
		gotName = name
		gotTimeout = timeout
		return nil
	}

	deps := StopServiceDeps{
		Cfg:           cfg,
		CmdRegistry:   nil,
		BaseDir:       baseDir,
		ErrOut:        nil,
		SkipPreflight: true,
	}
	if err := StopService(context.Background(), deps, "postgres"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Container name is projectFullName + "-" + container template.
	wantName := cfg.Project.FullName() + "-pg"
	if gotName != wantName {
		t.Errorf("container name = %q, want %q", gotName, wantName)
	}
	if gotTimeout != docker.DefaultStopTimeoutSec {
		t.Errorf("timeout = %d, want %d", gotTimeout, docker.DefaultStopTimeoutSec)
	}
	_ = gotBin // bin is determined by config.DockerBin, ok to vary
}

func TestStopService_KnownDisabledService(t *testing.T) {
	// Disabled services must also be stoppable — compose-bypassed path.
	cfgPath := writeStopTestConfig(t, map[string]struct {
		enabled   bool
		container string
	}{
		"redis": {enabled: false, container: "redis-svc"},
	})
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	baseDir := filepath.Dir(cfgPath)

	called := false
	prev := stopContainerFn
	t.Cleanup(func() { stopContainerFn = prev })
	stopContainerFn = func(_ context.Context, _, _ string, _ int) error {
		called = true
		return nil
	}

	deps := StopServiceDeps{
		Cfg:           cfg,
		CmdRegistry:   nil,
		BaseDir:       baseDir,
		ErrOut:        nil,
		SkipPreflight: true,
	}
	if err := StopService(context.Background(), deps, "redis"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("StopContainer was not called for disabled service")
	}
}

func TestStopCmd_OneArg_UnknownService(t *testing.T) {
	cfgPath := writeStopTestConfig(t, map[string]struct {
		enabled   bool
		container string
	}{
		"postgres": {enabled: true},
	})

	prev := stopContainerFn
	t.Cleanup(func() { stopContainerFn = prev })
	stopContainerFn = func(_ context.Context, _, _ string, _ int) error { return nil }

	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: filepath.Dir(cfgPath)}
	cmd := newStopCmd(flags)
	cmd.SilenceErrors = true
	err := cmd.RunE(cmd, []string{"nonexistent"})
	if !errors.Is(err, ErrUnknownService) {
		t.Errorf("expected ErrUnknownService, got %v", err)
	}
}
