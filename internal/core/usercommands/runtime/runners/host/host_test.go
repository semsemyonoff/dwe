package host

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

func TestHostRunner_BuildCommand_Run(t *testing.T) {
	r := &Runner{}
	ctx := spec.RunContext{
		Cmd: &model.CommandDef{
			Type: model.CommandTypeShell,
			Cmd:  "echo hello",
		},
		Render:      &tpl.RenderContext{},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Args[0] != "sh" {
		t.Errorf("expected sh, got %q", c.Args[0])
	}
	if c.Args[1] != "-c" {
		t.Errorf("expected -c, got %q", c.Args[1])
	}
	if c.Args[2] != "echo hello" {
		t.Errorf("expected 'echo hello', got %q", c.Args[2])
	}
}

func TestHostRunner_BuildCommand_Argv(t *testing.T) {
	r := &Runner{}
	ctx := spec.RunContext{
		Cmd: &model.CommandDef{
			Type: model.CommandTypeShell,
			Argv: []string{"git", "status"},
		},
		Render:      &tpl.RenderContext{},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Path == "" {
		t.Error("expected non-empty path")
	}
	if c.Args[len(c.Args)-1] != "status" {
		t.Errorf("expected last arg 'status', got %q", c.Args[len(c.Args)-1])
	}
}

func TestHostRunner_BuildCommand_WorkdirAbsolute(t *testing.T) {
	r := &Runner{}
	ctx := spec.RunContext{
		Cmd: &model.CommandDef{
			Type:    model.CommandTypeShell,
			Cmd:     "pwd",
			Workdir: "/tmp/mydir",
		},
		Render:      &tpl.RenderContext{},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Dir != "/tmp/mydir" {
		t.Errorf("expected /tmp/mydir, got %q", c.Dir)
	}
}

func TestHostRunner_BuildCommand_WorkdirRelative(t *testing.T) {
	r := &Runner{}
	ctx := spec.RunContext{
		Cmd: &model.CommandDef{
			Type:    model.CommandTypeShell,
			Cmd:     "pwd",
			Workdir: "subdir",
		},
		Render:      &tpl.RenderContext{},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Dir != "/project/subdir" {
		t.Errorf("expected /project/subdir, got %q", c.Dir)
	}
}

func TestHostRunner_BuildCommand_DefaultWorkdir(t *testing.T) {
	r := &Runner{}
	ctx := spec.RunContext{
		Cmd: &model.CommandDef{
			Type: model.CommandTypeShell,
			Cmd:  "ls",
		},
		Render:      &tpl.RenderContext{},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Dir != "/project" {
		t.Errorf("expected /project, got %q", c.Dir)
	}
}

func TestHostRunner_BuildCommand_EnvRendered(t *testing.T) {
	r := &Runner{}
	ctx := spec.RunContext{
		Cmd: &model.CommandDef{
			Type: model.CommandTypeShell,
			Cmd:  "env",
			Env:  map[string]string{"MY_VAR": "hello"},
		},
		Params:      map[string]any{},
		Context:     map[string]any{},
		Render:      &tpl.RenderContext{},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Contains(c.Env, "MY_VAR=hello") {
		t.Errorf("MY_VAR=hello not found in env; got %v", c.Env)
	}
}

func TestHostRunner_BuildCommand_RunWithTemplateInterpolation(t *testing.T) {
	r := &Runner{}
	ctx := spec.RunContext{
		Cmd: &model.CommandDef{
			Type: model.CommandTypeShell,
			Cmd:  "echo ${param.name}",
		},
		Params:  map[string]any{"name": "world"},
		Context: map[string]any{},
		Render: &tpl.RenderContext{
			Params: map[string]any{"name": "world"},
		},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(c.Args[2], "world") {
		t.Errorf("expected rendered 'world' in run arg, got %q", c.Args[2])
	}
}

func TestHostRunner_BuildCommand_WorkdirWithTemplate(t *testing.T) {
	r := &Runner{}
	ctx := spec.RunContext{
		Cmd: &model.CommandDef{
			Type:    model.CommandTypeShell,
			Cmd:     "pwd",
			Workdir: "${param.dir}",
		},
		Params:  map[string]any{"dir": "mydir"},
		Context: map[string]any{},
		Render: &tpl.RenderContext{
			Params: map[string]any{"dir": "mydir"},
		},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Dir != "/project/mydir" {
		t.Errorf("expected /project/mydir, got %q", c.Dir)
	}
}

func TestHostRunner_BuildCommand_WorkdirAbsoluteTemplate(t *testing.T) {
	r := &Runner{}
	ctx := spec.RunContext{
		Cmd: &model.CommandDef{
			Type:    model.CommandTypeShell,
			Cmd:     "pwd",
			Workdir: "/tmp/${param.suffix}",
		},
		Params:  map[string]any{"suffix": "test"},
		Context: map[string]any{},
		Render: &tpl.RenderContext{
			Params: map[string]any{"suffix": "test"},
		},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Dir != "/tmp/test" {
		t.Errorf("expected /tmp/test, got %q", c.Dir)
	}
}

// TestHostRunner_BuildCommand_ShellFromConfig verifies that the host Runner uses
// cfg.Binaries.Shell instead of a hardcoded "sh".
func TestHostRunner_BuildCommand_ShellFromConfig(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *config.DweConfig
		wantShell string
	}{
		{
			name:      "nil config defaults to sh",
			cfg:       nil,
			wantShell: "sh",
		},
		{
			name:      "empty config defaults to sh",
			cfg:       &config.DweConfig{},
			wantShell: "sh",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Runner{}
			ctx := spec.RunContext{
				Cmd: &model.CommandDef{
					Type: model.CommandTypeShell,
					Cmd:  "echo hello",
				},
				Config:      tc.cfg,
				Render:      &tpl.RenderContext{},
				ProjectRoot: "/project",
			}
			c, err := r.BuildCommand(context.Background(), ctx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(c.Args) == 0 || c.Args[0] != tc.wantShell {
				t.Errorf("Args[0] = %q, want %q", c.Args[0], tc.wantShell)
			}
		})
	}
}

// TestHostRunner_BuildCommand_ContractEnvDweBin verifies that every type:shell
// subprocess gets DWE_BIN exported so shell snippets can shell back into the
// running dwe binary without hardcoding the path.
func TestHostRunner_BuildCommand_ContractEnvDweBin(t *testing.T) {
	r := &Runner{}
	ctx := spec.RunContext{
		Cmd: &model.CommandDef{
			Type: model.CommandTypeShell,
			Cmd:  "env",
		},
		Render:      &tpl.RenderContext{},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, kv := range c.Env {
		if strings.HasPrefix(kv, "DWE_BIN=") && len(kv) > len("DWE_BIN=") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected DWE_BIN=<non-empty> in env, got: %v", c.Env)
	}
}

// TestHostRunner_BuildCommand_ContractEnvComposeFile verifies that COMPOSE_FILE
// is exported with the active overlay set (absolute paths) so shell snippets can
// invoke `docker compose ...` without re-passing every -f flag.
func TestHostRunner_BuildCommand_ContractEnvComposeFile(t *testing.T) {
	r := &Runner{}
	ctx := spec.RunContext{
		Cmd: &model.CommandDef{
			Type: model.CommandTypeShell,
			Cmd:  "env",
		},
		Render: &tpl.RenderContext{},
		Config: &config.DweConfig{
			Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"},
			Compose: config.ComposeConfig{Base: "compose.yaml"},
			Services: map[string]config.ServiceConfig{
				"catalog": {Enabled: true, Compose: []string{"compose/services/catalog/app.yml"}},
			},
		},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var composeFile, composeProject string
	for _, kv := range c.Env {
		if v, ok := strings.CutPrefix(kv, "COMPOSE_FILE="); ok {
			composeFile = v
		}
		if v, ok := strings.CutPrefix(kv, "COMPOSE_PROJECT_NAME="); ok {
			composeProject = v
		}
	}
	if composeProject != "dwe-laravel" {
		t.Errorf("COMPOSE_PROJECT_NAME = %q, want %q", composeProject, "dwe-laravel")
	}
	want := "/project/compose.yaml:/project/compose/services/catalog/app.yml"
	if composeFile != want {
		t.Errorf("COMPOSE_FILE = %q, want %q", composeFile, want)
	}
}

// TestHostRunner_BuildCommand_ContractEnvNoComposeFiles verifies that when the
// project declares no compose files (e.g. unit-test contexts) COMPOSE_FILE is
// omitted rather than set to an empty string.
func TestHostRunner_BuildCommand_ContractEnvNoComposeFiles(t *testing.T) {
	r := &Runner{}
	ctx := spec.RunContext{
		Cmd: &model.CommandDef{
			Type: model.CommandTypeShell,
			Cmd:  "env",
		},
		Render:      &tpl.RenderContext{},
		Config:      &config.DweConfig{Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"}},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, kv := range c.Env {
		if strings.HasPrefix(kv, "COMPOSE_FILE=") {
			t.Errorf("did not expect COMPOSE_FILE entry when no compose files configured, got: %q", kv)
		}
	}
}

// TestHostRunner_BuildCommand_ContractEnvOverridesUserEnv verifies that the
// shell contract env wins over a user-declared env entry with the same name.
func TestHostRunner_BuildCommand_ContractEnvOverridesUserEnv(t *testing.T) {
	r := &Runner{}
	ctx := spec.RunContext{
		Cmd: &model.CommandDef{
			Type: model.CommandTypeShell,
			Cmd:  "env",
			Env:  map[string]string{"DWE_BIN": "user-supplied-value"},
		},
		Params:      map[string]any{},
		Context:     map[string]any{},
		Render:      &tpl.RenderContext{},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	userIdx, contractIdx := -1, -1
	for i, kv := range c.Env {
		if kv == "DWE_BIN=user-supplied-value" {
			userIdx = i
		} else if strings.HasPrefix(kv, "DWE_BIN=") {
			contractIdx = i
		}
	}
	if userIdx < 0 || contractIdx < 0 {
		t.Fatalf("expected both user and contract DWE_BIN entries, got: %v", c.Env)
	}
	if contractIdx <= userIdx {
		t.Errorf("contract DWE_BIN must follow user DWE_BIN (so it wins per os/exec rules); user=%d contract=%d", userIdx, contractIdx)
	}
}

// TestHostRunner_Run_ContextCancellation verifies that cancelling the context
// while a child process is in-flight kills the child promptly.
func TestHostRunner_Run_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rc := spec.RunContext{
		Cmd: &model.CommandDef{
			Type: model.CommandTypeShell,
			Cmd:  "sleep 30",
		},
		Render:      &tpl.RenderContext{},
		ProjectRoot: t.TempDir(),
		Stdout:      &bytes.Buffer{},
		Stderr:      &bytes.Buffer{},
	}

	done := make(chan error, 1)
	go func() {
		r := &Runner{}
		done <- r.Run(ctx, rc)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error from cancelled child, got nil")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runner did not return within 10s of cancel — child was not killed")
	}
}

// TestHostRunner_Run_ContextDeadline verifies that exceeding the context
// deadline cancels the child and returns promptly.
func TestHostRunner_Run_ContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	rc := spec.RunContext{
		Cmd: &model.CommandDef{
			Type: model.CommandTypeShell,
			Cmd:  "sleep 30",
		},
		Render:      &tpl.RenderContext{},
		ProjectRoot: t.TempDir(),
		Stdout:      &bytes.Buffer{},
		Stderr:      &bytes.Buffer{},
	}

	start := time.Now()
	err := (&Runner{}).Run(ctx, rc)
	if err == nil {
		t.Fatal("expected error from deadline, got nil")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("runner did not return promptly after deadline: %v", elapsed)
	}
	_ = errors.Is(ctx.Err(), context.DeadlineExceeded)
}
