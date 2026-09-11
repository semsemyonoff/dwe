package lifecycle

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime"
	svcrunner "github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/runners/service"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"

	"github.com/spf13/cobra"
)

// hookComposeFixture writes a project dir with the given docker.yml and a
// POSIX script printing its COMPOSE_PROJECT_NAME, and returns the dir, a
// config and a registry holding shell, script and workflow hook commands.
func hookComposeFixture(t *testing.T, dockerYML string) (string, *config.DweConfig, *registry.Registry) {
	t.Helper()
	dir := t.TempDir()
	ws := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "docker.yml"), []byte(dockerYML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s.sh"), []byte(`printf 'SCRIPT=%s\n' "$COMPOSE_PROJECT_NAME"`), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.DweConfig{
		Raw:     map[string]any{"project": map[string]any{"name": "fixture", "prefix": "dwe"}},
		Project: config.ProjectConfig{Prefix: "dwe", Name: "fixture"},
	}
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(&model.CommandDef{
		ID: "hook.shell", Type: model.CommandTypeShell, Files: map[string]model.FileSpec{},
		Cmd: `printf 'SHELL=%s\n' "$COMPOSE_PROJECT_NAME"`,
	})
	reg.AddCommandForTest(&model.CommandDef{
		ID: "hook.script", Type: model.CommandTypeScript, Files: map[string]model.FileSpec{},
		Script: &model.ScriptDef{Path: "s.sh"},
	})
	reg.AddCommandForTest(&model.CommandDef{
		ID: "hook.workflow", Type: model.CommandTypeWorkflow, Files: map[string]model.FileSpec{},
		Steps: []model.WorkflowStep{{Command: "hook.shell"}, {Command: "hook.script"}},
	})
	return dir, cfg, reg
}

func TestRunResetHook_SeesDockerProjectName(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "ambient")
	dir, cfg, reg := hookComposeFixture(t, "project_name: custom\n")

	tests := []struct {
		hook string
		want []string
	}{
		{"hook.shell", []string{"SHELL=custom"}},
		{"hook.script", []string{"SCRIPT=custom"}},
		{"hook.workflow", []string{"SHELL=custom", "SCRIPT=custom"}},
	}
	for _, tt := range tests {
		t.Run(tt.hook, func(t *testing.T) {
			var out bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			if err := runResetHook(context.Background(), cmd, cfg, reg, dir, tt.hook); err != nil {
				t.Fatalf("runResetHook: %v (output=%s)", err, out.String())
			}
			for _, w := range tt.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output missing %q: %s", w, out.String())
				}
			}
		})
	}
}

// TestRunResetHook_ServiceExecUsesDockerProjectName pins the container side of
// the same fix: a service_exec hook builds its compose invocation with the
// docker.yml project name, not the project.prefix/name fallback.
func TestRunResetHook_ServiceExecUsesDockerProjectName(t *testing.T) {
	dir, cfg, reg := hookComposeFixture(t, "project_name: custom\n")
	reg.AddCommandForTest(&model.CommandDef{
		ID: "hook.exec", Type: model.CommandTypeServiceExec, Files: map[string]model.FileSpec{},
		Service: "app", Mode: model.ExecModeRun, Cmd: "true",
	})

	var captured runtime.RunContext
	prev := resetServiceRunHook
	t.Cleanup(func() { resetServiceRunHook = prev })
	resetServiceRunHook = func(_ context.Context, rc runtime.RunContext) error {
		captured = rc
		return nil
	}

	if err := runResetHook(context.Background(), &cobra.Command{}, cfg, reg, dir, "hook.exec"); err != nil {
		t.Fatalf("runResetHook: %v", err)
	}
	if captured.DockerConfig == nil {
		t.Fatal("RunContext.DockerConfig is nil; the hook must carry the loaded docker config")
	}
	captured.Render = &tpl.RenderContext{Host: tpl.CurrentHostInfo()}
	c, err := (&svcrunner.ExecRunner{}).BuildCommand(context.Background(), captured, captured.Compose())
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}
	i := slices.Index(c.Args, "-p")
	if i < 0 || i+1 >= len(c.Args) || c.Args[i+1] != "custom" {
		t.Errorf("compose argv = %q, want -p custom", c.Args)
	}
}

func TestRunResetHook_DockerConfigLoadErrorReturned(t *testing.T) {
	dir, cfg, reg := hookComposeFixture(t, "project_name: [\n")

	called := false
	prev := resetServiceRunHook
	t.Cleanup(func() { resetServiceRunHook = prev })
	resetServiceRunHook = func(context.Context, runtime.RunContext) error {
		called = true
		return nil
	}

	err := runResetHook(context.Background(), &cobra.Command{}, cfg, reg, dir, "hook.shell")
	if err == nil || !strings.Contains(err.Error(), "docker config") {
		t.Fatalf("runResetHook error = %v, want a docker config load error", err)
	}
	if called {
		t.Error("the hook ran despite the docker config load error")
	}
}
