package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime"

	"github.com/spf13/cobra"
)

// toggleHookComposeDeps builds ExecuteDeps over a project dir with the given
// docker.yml, running hooks through the real runtime, and returns the deps
// with the captured output buffer.
func toggleHookComposeDeps(t *testing.T, dockerYML string) (ExecuteDeps, *strings.Builder) {
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

	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(&model.CommandDef{
		ID: "hook.shell", Type: model.CommandTypeShell, Files: map[string]model.FileSpec{},
		Cmd: `printf 'SHELL=%s\n' "$COMPOSE_PROJECT_NAME"`,
	})
	reg.AddCommandForTest(&model.CommandDef{
		ID: "hook.script", Type: model.CommandTypeScript, Files: map[string]model.FileSpec{},
		Script: &model.ScriptDef{Path: "s.sh"},
	})

	var out strings.Builder
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(""))
	return ExecuteDeps{
		Cmd:     cmd,
		BaseDir: dir,
		Cfg: &config.DweConfig{
			Raw:     map[string]any{"project": map[string]any{"name": "fixture", "prefix": "dwe"}},
			Project: config.ProjectConfig{Prefix: "dwe", Name: "fixture"},
		},
		CmdReg:     reg,
		RunUserCmd: runtime.RunCommand,
	}, &out
}

func TestRunToggleHook_SeesDockerProjectName(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "ambient")
	for _, tt := range []struct{ hook, want string }{
		{"hook.shell", "SHELL=custom"},
		{"hook.script", "SCRIPT=custom"},
	} {
		t.Run(tt.hook, func(t *testing.T) {
			deps, out := toggleHookComposeDeps(t, "project_name: custom\n")
			if err := runToggleHook(context.Background(), deps, PlanStep{CommandID: tt.hook}); err != nil {
				t.Fatalf("runToggleHook: %v (output=%s)", err, out.String())
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Errorf("output missing %q: %s", tt.want, out.String())
			}
		})
	}
}

func TestRunToggleHook_DockerConfigLoadErrorReturned(t *testing.T) {
	deps, _ := toggleHookComposeDeps(t, "project_name: [\n")
	called := false
	deps.RunUserCmd = func(context.Context, runtime.RunContext) error {
		called = true
		return nil
	}
	err := runToggleHook(context.Background(), deps, PlanStep{CommandID: "hook.shell"})
	if err == nil || !strings.Contains(err.Error(), "docker config") {
		t.Fatalf("runToggleHook error = %v, want a docker config load error", err)
	}
	if called {
		t.Error("the hook ran despite the docker config load error")
	}
}
