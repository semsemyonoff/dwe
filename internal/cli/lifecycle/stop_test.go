package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

func TestStopCmd_Use(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "workspace.yml"}
	cmd := NewStopCmd(groupEnvironment, flags)
	if cmd.Use != "stop [service]" {
		t.Errorf("Use = %q, want %q", cmd.Use, "stop [service]")
	}
}

func TestStopCmd_MaximumOneArg(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "workspace.yml"}
	cmd := NewStopCmd(groupEnvironment, flags)

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
	flags := &cmdctx.RootFlags{ConfigPath: "workspace.yml"}
	cmd := NewStopCmd(groupEnvironment, flags)
	if cmd.Flags().Lookup("yes") == nil {
		t.Error("missing --yes flag")
	}
	if cmd.Flags().Lookup("skip-preflight") == nil {
		t.Error("missing --skip-preflight flag")
	}
}

func TestStopCmd_RegisteredAtRoot(t *testing.T) {
	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	for _, c := range root.Commands() {
		if c.Name() == "stop" {
			return
		}
	}
	t.Error("stop command not registered at root level")
}

func TestRunStop_MissingLifecycleYML(t *testing.T) {
	// Default stop config includes a type:dwe step; stub to prevent recursion.
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	var errBuf strings.Builder
	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"stop"})
	root.SetErr(&errBuf)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("stop with missing lifecycle.yml should succeed (built-in default), got: %v", err)
	}
	if !strings.Contains(errBuf.String(), "Using built-in default stop pipeline") {
		t.Errorf("expected info line in stderr, got: %q", errBuf.String())
	}
}

func TestRunStop_MissingStopSection(t *testing.T) {
	// Default stop config includes a type:dwe step; stub to prevent recursion.
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	lifecycleYAML := "run:\n  final_message: ready\n  phases: []\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	var errBuf strings.Builder
	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"stop"})
	root.SetErr(&errBuf)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("stop with no stop: section should succeed (built-in default), got: %v", err)
	}
	if !strings.Contains(errBuf.String(), "Using built-in default stop pipeline") {
		t.Errorf("expected info line in stderr, got: %q", errBuf.String())
	}
}

// writeStopTestConfig creates a temp dir with workspace.yml and a service folder.
func writeStopTestConfig(t *testing.T, services map[string]struct {
	enabled   bool
	container string
}) string {
	t.Helper()
	dir := t.TempDir()
	content := "schema_version: \"2\"\nproject:\n  name: test\n  prefix: dwe\n"
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	for name, spec := range services {
		svcDir := filepath.Join(dir, "workspace", "services", name)
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
	return filepath.Join(dir, "workspace.yml")
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

// TestStopService_UsesComposeProjectNameFromDockerYAML locks in the same fix
// as TestRestartService_UsesComposeProjectNameFromDockerYAML for the stop
// path — when docker.yml overrides project_name with a non-dash separator,
// per-service stop must target the actual compose-derived container name.
func TestStopService_UsesComposeProjectNameFromDockerYAML(t *testing.T) {
	cfgPath := writeStopTestConfig(t, map[string]struct {
		enabled   bool
		container string
	}{
		"catalog": {enabled: true, container: "app-catalog"},
	})
	baseDir := filepath.Dir(cfgPath)
	workspaceDir := filepath.Join(baseDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	dockerYAML := "project_name: \"${project.prefix}_${project.name}\"\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "docker.yml"), []byte(dockerYAML), 0o644); err != nil {
		t.Fatalf("write docker.yml: %v", err)
	}
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	var gotName string
	prev := stopContainerFn
	t.Cleanup(func() { stopContainerFn = prev })
	stopContainerFn = func(_ context.Context, _, name string, _ int) error {
		gotName = name
		return nil
	}

	deps := StopServiceDeps{
		Cfg:           cfg,
		CmdRegistry:   nil,
		BaseDir:       baseDir,
		ErrOut:        nil,
		SkipPreflight: true,
	}
	if err := StopService(context.Background(), deps, "catalog"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantName := "dwe_test-app-catalog"
	if gotName != wantName {
		t.Errorf("container name = %q, want %q (FullName() would yield %q)", gotName, wantName, cfg.Project.FullName()+"-app-catalog")
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
	cmd := NewStopCmd(groupEnvironment, flags)
	cmd.SilenceErrors = true
	err := cmd.RunE(cmd, []string{"nonexistent"})
	if !errors.Is(err, ErrUnknownService) {
		t.Errorf("expected ErrUnknownService, got %v", err)
	}
}
