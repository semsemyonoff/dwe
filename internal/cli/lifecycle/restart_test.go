package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

func TestRestartCmd_Use(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "workspace.yml"}
	cmd := NewRestartCmd(groupEnvironment, flags)
	if cmd.Use != "restart [service]" {
		t.Errorf("Use = %q, want %q", cmd.Use, "restart [service]")
	}
}

func TestRestartCmd_MaximumOneArg(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "workspace.yml"}
	cmd := NewRestartCmd(groupEnvironment, flags)

	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("expected zero args to be allowed, got: %v", err)
	}
	if err := cmd.Args(cmd, []string{"postgres"}); err != nil {
		t.Errorf("expected one arg to be allowed, got: %v", err)
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("expected error when passing two arguments to restart command")
	}
}

func TestRestartCmd_FlagsExist(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "workspace.yml"}
	cmd := NewRestartCmd(groupEnvironment, flags)
	if cmd.Flags().Lookup("yes") == nil {
		t.Error("missing --yes flag")
	}
}

func TestRestartCmd_RegisteredAtRoot(t *testing.T) {
	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	for _, c := range root.Commands() {
		if c.Name() == "restart" {
			return
		}
	}
	t.Error("restart command not registered at root level")
}

func TestRunRestart_MissingLifecycleYML_UsesDefault(t *testing.T) {
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	var errBuf strings.Builder
	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"restart"})
	root.SetErr(&errBuf)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("restart with missing lifecycle.yml should succeed (built-in default), got: %v", err)
	}
	if !strings.Contains(errBuf.String(), "Using built-in default run pipeline") {
		t.Errorf("expected info line in stderr, got: %q", errBuf.String())
	}
}

func TestRunRestart_MissingStopSection(t *testing.T) {
	// Missing stop: uses the default stop config (type:dwe step); stub to prevent recursion.
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

	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"restart"})
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("restart with missing stop: should succeed, got: %v", err)
	}
}

func TestRunRestart_MissingRunSection_UsesDefault(t *testing.T) {
	stubRunPhases(t)
	dir := t.TempDir()
	cfgPath := makeMinimalWorkspaceYML(t, dir)

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("creating workspace dir: %v", err)
	}
	lifecycleYAML := "stop:\n  final_message: bye\n  phases: []\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "lifecycle.yml"), []byte(lifecycleYAML), 0644); err != nil {
		t.Fatalf("writing lifecycle.yml: %v", err)
	}

	var errBuf strings.Builder
	flags := &cmdctx.RootFlags{}
	root := buildLifecycleTestRoot(flags)
	root.SetArgs([]string{"restart"})
	root.SetErr(&errBuf)
	if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	if err := root.Execute(); err != nil {
		t.Fatalf("restart with missing run: section should succeed (built-in default), got: %v", err)
	}
	if !strings.Contains(errBuf.String(), "Using built-in default run pipeline") {
		t.Errorf("expected info line in stderr, got: %q", errBuf.String())
	}
}

func TestRestartService_UnknownService(t *testing.T) {
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
	err = RestartService(context.Background(), filepath.Dir(cfgPath), cfg, "nonexistent", io.Discard)
	if !errors.Is(err, ErrUnknownService) {
		t.Errorf("expected ErrUnknownService, got %v", err)
	}
}

func TestRestartService_KnownService(t *testing.T) {
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

	var gotName string
	var gotTimeout int
	prev := restartContainerFn
	t.Cleanup(func() { restartContainerFn = prev })
	restartContainerFn = func(_ context.Context, _, name string, timeout int) error {
		gotName = name
		gotTimeout = timeout
		return nil
	}

	if err := RestartService(context.Background(), filepath.Dir(cfgPath), cfg, "postgres", io.Discard); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantName := cfg.Project.FullName() + "-pg"
	if gotName != wantName {
		t.Errorf("container name = %q, want %q", gotName, wantName)
	}
	if gotTimeout != docker.DefaultStopTimeoutSec {
		t.Errorf("timeout = %d, want %d", gotTimeout, docker.DefaultStopTimeoutSec)
	}
}

func TestRestartService_DisabledService(t *testing.T) {
	// Disabled services must still be restartable — compose-bypass path.
	cfgPath := writeStopTestConfig(t, map[string]struct {
		enabled   bool
		container string
	}{
		"redis": {enabled: false, container: "redis-svc"},
	})
	// writeStopTestConfig does not write local.yml, so disable explicitly here.
	localYML := "services:\n  redis:\n    enabled: false\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(cfgPath), "workspace", "local.yml"), []byte(localYML), 0o644); err != nil {
		t.Fatalf("writing local.yml: %v", err)
	}
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	if svc, ok := cfg.Services["redis"]; !ok || svc.Enabled {
		t.Fatalf("test precondition: redis should be present and disabled; ok=%v enabled=%v", ok, svc.Enabled)
	}

	called := false
	prev := restartContainerFn
	t.Cleanup(func() { restartContainerFn = prev })
	restartContainerFn = func(_ context.Context, _, _ string, _ int) error {
		called = true
		return nil
	}

	if err := RestartService(context.Background(), filepath.Dir(cfgPath), cfg, "redis", io.Discard); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("RestartContainer was not called for disabled service")
	}
}

func TestRestartService_NoSuchContainerProducesHint(t *testing.T) {
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

	prev := restartContainerFn
	t.Cleanup(func() { restartContainerFn = prev })
	restartContainerFn = func(_ context.Context, _, name string, _ int) error {
		return fmt.Errorf("%w: %s", docker.ErrNoSuchContainer, name)
	}

	err = RestartService(context.Background(), filepath.Dir(cfgPath), cfg, "postgres", io.Discard)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, want := range []string{`service "postgres"`, "dwe deploy run", "dwe run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

// TestRestartService_NotDeployed_Hint verifies that when label resolution finds
// no container for the service (never deployed / already removed), restart
// surfaces the deploy hint WITHOUT calling docker restart.
func TestRestartService_NotDeployed_Hint(t *testing.T) {
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

	prevLookup := lookupContainerFn
	t.Cleanup(func() { lookupContainerFn = prevLookup })
	lookupContainerFn = func(_ string, _ []string, _, _ string) (string, error) { return "", nil }

	called := false
	prev := restartContainerFn
	t.Cleanup(func() { restartContainerFn = prev })
	restartContainerFn = func(_ context.Context, _, _ string, _ int) error {
		called = true
		return nil
	}

	err = RestartService(context.Background(), filepath.Dir(cfgPath), cfg, "postgres", io.Discard)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, want := range []string{`service "postgres"`, "dwe deploy run", "dwe run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
	if called {
		t.Error("restartContainerFn must NOT be called when no container exists")
	}
}

// TestRestartService_UsesComposeProjectNameFromDockerYAML locks in the fix for
// a bug where `dwe restart <service>` derived the container name from
// cfg.Project.FullName() (always "<prefix>-<name>") instead of the compose
// project name configured in workspace/docker.yml. Projects that override
// project_name (e.g. "${project.prefix}_${project.name}" with underscore) used
// to get "no such container" errors because the real container is named after
// the docker.yml project name.
func TestRestartService_UsesComposeProjectNameFromDockerYAML(t *testing.T) {
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
	prev := restartContainerFn
	t.Cleanup(func() { restartContainerFn = prev })
	restartContainerFn = func(_ context.Context, _, name string, _ int) error {
		gotName = name
		return nil
	}

	if err := RestartService(context.Background(), baseDir, cfg, "catalog", io.Discard); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// workspace.yml fixture: prefix=dwe, name=test → docker.yml resolves
	// project_name to "dwe_test" (underscore, NOT the FullName() dash).
	wantName := "dwe_test-app-catalog"
	if gotName != wantName {
		t.Errorf("container name = %q, want %q (FullName() would yield %q)", gotName, wantName, cfg.Project.FullName()+"-app-catalog")
	}
}

// TestRestartService_PropagatesMalformedDockerYAML verifies that an
// unresolved/typoed ${...} reference in workspace/docker.yml is surfaced as a
// real error instead of being silently swallowed in favour of FullName() —
// otherwise a typo like ${project.naem} would make `dwe restart <svc>`
// quietly target the wrong container.
func TestRestartService_PropagatesMalformedDockerYAML(t *testing.T) {
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
	// Reference an unknown raw-config path so resolveVarTemplate errors out.
	dockerYAML := "project_name: \"${project.naem}\"\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "docker.yml"), []byte(dockerYAML), 0o644); err != nil {
		t.Fatalf("write docker.yml: %v", err)
	}
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	prev := restartContainerFn
	t.Cleanup(func() { restartContainerFn = prev })
	called := false
	restartContainerFn = func(_ context.Context, _, _ string, _ int) error {
		called = true
		return nil
	}

	err = RestartService(context.Background(), baseDir, cfg, "catalog", io.Discard)
	if err == nil {
		t.Fatal("expected error from malformed docker.yml, got nil")
	}
	if !strings.Contains(err.Error(), "compose project name") {
		t.Errorf("error %q missing 'compose project name' prefix", err.Error())
	}
	if called {
		t.Error("restartContainerFn should NOT be called when project name resolution fails")
	}
}

// TestRestartService_EmitsSuccessLine verifies that on a successful per-service
// restart, a "✓ container restarted: <name>" line is written to the supplied
// out writer. Without this line the command was silent — the user could not
// tell whether the container was actually restarted or which one.
func TestRestartService_EmitsSuccessLine(t *testing.T) {
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

	prev := restartContainerFn
	t.Cleanup(func() { restartContainerFn = prev })
	restartContainerFn = func(_ context.Context, _, _ string, _ int) error { return nil }

	var buf bytes.Buffer
	if err := RestartService(context.Background(), filepath.Dir(cfgPath), cfg, "postgres", &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantContainer := cfg.Project.FullName() + "-pg"
	wantLine := "✓ container restarted: " + wantContainer
	if !strings.Contains(buf.String(), wantLine) {
		t.Errorf("output %q missing success line %q", buf.String(), wantLine)
	}
}

// TestRestartService_NoOutputOnFailure verifies the success line is NOT emitted
// when restartContainerFn fails — otherwise the user would see a contradictory
// "✓ restarted" line followed by an error.
func TestRestartService_NoOutputOnFailure(t *testing.T) {
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

	prev := restartContainerFn
	t.Cleanup(func() { restartContainerFn = prev })
	restartContainerFn = func(_ context.Context, _, name string, _ int) error {
		return fmt.Errorf("%w: %s", docker.ErrNoSuchContainer, name)
	}

	var buf bytes.Buffer
	err = RestartService(context.Background(), filepath.Dir(cfgPath), cfg, "postgres", &buf)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output on failure, got %q", buf.String())
	}
}

func TestRestartCmd_OneArg_UnknownService(t *testing.T) {
	cfgPath := writeStopTestConfig(t, map[string]struct {
		enabled   bool
		container string
	}{
		"postgres": {enabled: true},
	})

	prev := restartContainerFn
	t.Cleanup(func() { restartContainerFn = prev })
	restartContainerFn = func(_ context.Context, _, _ string, _ int) error { return nil }

	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: filepath.Dir(cfgPath)}
	cmd := NewRestartCmd(groupEnvironment, flags)
	cmd.SilenceErrors = true
	err := cmd.RunE(cmd, []string{"nonexistent"})
	if !errors.Is(err, ErrUnknownService) {
		t.Errorf("expected ErrUnknownService, got %v", err)
	}
}
