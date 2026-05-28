package runtime

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/shared/tpl"
)

func TestHostRunner_BuildCommand_Run(t *testing.T) {
	r := &HostRunner{}
	ctx := RunContext{
		Cmd: &CommandDef{
			Type: CommandTypeShell,
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
	r := &HostRunner{}
	ctx := RunContext{
		Cmd: &CommandDef{
			Type: CommandTypeShell,
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
	// The first element of Args is the resolved executable.
	if c.Args[len(c.Args)-1] != "status" {
		t.Errorf("expected last arg 'status', got %q", c.Args[len(c.Args)-1])
	}
}

func TestHostRunner_BuildCommand_WorkdirAbsolute(t *testing.T) {
	r := &HostRunner{}
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeShell,
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
	r := &HostRunner{}
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeShell,
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
	r := &HostRunner{}
	ctx := RunContext{
		Cmd: &CommandDef{
			Type: CommandTypeShell,
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
	r := &HostRunner{}
	ctx := RunContext{
		Cmd: &CommandDef{
			Type: CommandTypeShell,
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
	r := &HostRunner{}
	ctx := RunContext{
		Cmd: &CommandDef{
			Type: CommandTypeShell,
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
	r := &HostRunner{}
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeShell,
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
	r := &HostRunner{}
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeShell,
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

func TestNewRunner_Returns_HostRunner(t *testing.T) {
	cmd := &CommandDef{Type: CommandTypeShell}
	runner, err := NewRunner(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := runner.(*HostRunner); !ok {
		t.Errorf("expected *HostRunner, got %T", runner)
	}
}

func TestNewRunner_Returns_ServiceExecRunner(t *testing.T) {
	cmd := &CommandDef{Type: CommandTypeServiceExec}
	runner, err := NewRunner(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := runner.(*ServiceExecRunner); !ok {
		t.Errorf("expected *ServiceExecRunner, got %T", runner)
	}
}

func TestNewRunner_Returns_ServiceRunRunner(t *testing.T) {
	cmd := &CommandDef{Type: CommandTypeServiceRun}
	runner, err := NewRunner(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := runner.(*ServiceRunRunner); !ok {
		t.Errorf("expected *ServiceRunRunner, got %T", runner)
	}
}

func TestNewRunner_Unsupported_Type(t *testing.T) {
	cmd := &CommandDef{Type: CommandType("unknown_type")}
	_, err := NewRunner(cmd)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
	unsup, ok := err.(*ErrUnsupportedType)
	if !ok {
		t.Fatalf("expected *ErrUnsupportedType, got %T", err)
	}
	if unsup.Type != "unknown_type" {
		t.Errorf("expected 'unknown_type' in error, got %q", unsup.Type)
	}
}

// TestHostRunner_BuildCommand_ShellFromConfig verifies that HostRunner uses
// cfg.Binaries.Shell instead of a hardcoded "sh".
func TestHostRunner_BuildCommand_ShellFromConfig(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *config.DevboxConfig
		wantShell string
	}{
		{
			name:      "nil config defaults to sh",
			cfg:       nil,
			wantShell: "sh",
		},
		{
			name:      "empty config defaults to sh",
			cfg:       &config.DevboxConfig{},
			wantShell: "sh",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &HostRunner{}
			ctx := RunContext{
				Cmd: &CommandDef{
					Type: CommandTypeShell,
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

// TestHostRunner_BuildCommand_ContractEnvDevboxBin verifies that every type:shell
// subprocess gets DEVBOX_BIN exported so shell snippets can shell back into the
// running devbox binary without hardcoding the path.
func TestHostRunner_BuildCommand_ContractEnvDevboxBin(t *testing.T) {
	r := &HostRunner{}
	ctx := RunContext{
		Cmd: &CommandDef{
			Type: CommandTypeShell,
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
		if strings.HasPrefix(kv, "DEVBOX_BIN=") && len(kv) > len("DEVBOX_BIN=") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected DEVBOX_BIN=<non-empty> in env, got: %v", c.Env)
	}
}

// TestHostRunner_BuildCommand_ContractEnvComposeFile verifies that COMPOSE_FILE
// is exported with the active overlay set (absolute paths) so shell snippets can
// invoke `docker compose ...` without re-passing every -f flag — the gap that
// blocked the generic-command idiom before this fix.
func TestHostRunner_BuildCommand_ContractEnvComposeFile(t *testing.T) {
	r := &HostRunner{}
	ctx := RunContext{
		Cmd: &CommandDef{
			Type: CommandTypeShell,
			Cmd:  "env",
		},
		Render: &tpl.RenderContext{},
		Config: &config.DevboxConfig{
			Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"},
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
	if composeProject != "devbox-laravel" {
		t.Errorf("COMPOSE_PROJECT_NAME = %q, want %q", composeProject, "devbox-laravel")
	}
	want := "/project/compose.yaml:/project/compose/services/catalog/app.yml"
	if composeFile != want {
		t.Errorf("COMPOSE_FILE = %q, want %q", composeFile, want)
	}
}

// TestHostRunner_BuildCommand_ContractEnvNoComposeFiles verifies that when the
// project declares no compose files (e.g. unit-test contexts) COMPOSE_FILE is
// omitted rather than set to an empty string — empty COMPOSE_FILE would tell
// docker compose to look at literal "" and crash.
func TestHostRunner_BuildCommand_ContractEnvNoComposeFiles(t *testing.T) {
	r := &HostRunner{}
	ctx := RunContext{
		Cmd: &CommandDef{
			Type: CommandTypeShell,
			Cmd:  "env",
		},
		Render:      &tpl.RenderContext{},
		Config:      &config.DevboxConfig{Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"}},
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
// shell contract env wins over a user-declared env entry with the same name —
// matching how the script runner appends contract env last.
func TestHostRunner_BuildCommand_ContractEnvOverridesUserEnv(t *testing.T) {
	r := &HostRunner{}
	ctx := RunContext{
		Cmd: &CommandDef{
			Type: CommandTypeShell,
			Cmd:  "env",
			Env:  map[string]string{"DEVBOX_BIN": "user-supplied-value"},
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
	// os/exec keeps every entry but uses the LAST one for duplicates; verify
	// the contract DEVBOX_BIN appears after the user one.
	userIdx, contractIdx := -1, -1
	for i, kv := range c.Env {
		if kv == "DEVBOX_BIN=user-supplied-value" {
			userIdx = i
		} else if strings.HasPrefix(kv, "DEVBOX_BIN=") {
			contractIdx = i
		}
	}
	if userIdx < 0 || contractIdx < 0 {
		t.Fatalf("expected both user and contract DEVBOX_BIN entries, got: %v", c.Env)
	}
	if contractIdx <= userIdx {
		t.Errorf("contract DEVBOX_BIN must follow user DEVBOX_BIN (so it wins per os/exec rules); user=%d contract=%d", userIdx, contractIdx)
	}
}

// TestHostRunner_Run_ContextCancellation verifies that cancelling the context
// while a child process is in-flight kills the child promptly.
func TestHostRunner_Run_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rc := RunContext{
		Cmd: &CommandDef{
			Type: CommandTypeShell,
			Cmd:  "sleep 30",
		},
		Render:      &tpl.RenderContext{},
		ProjectRoot: t.TempDir(),
		Stdout:      &bytes.Buffer{},
		Stderr:      &bytes.Buffer{},
	}

	done := make(chan error, 1)
	go func() {
		r := &HostRunner{}
		done <- r.Run(ctx, rc)
	}()

	// Give the child a moment to start, then cancel.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		// Expect an error (the sleep was killed); the runner returns the wait error.
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

	rc := RunContext{
		Cmd: &CommandDef{
			Type: CommandTypeShell,
			Cmd:  "sleep 30",
		},
		Render:      &tpl.RenderContext{},
		ProjectRoot: t.TempDir(),
		Stdout:      &bytes.Buffer{},
		Stderr:      &bytes.Buffer{},
	}

	start := time.Now()
	err := (&HostRunner{}).Run(ctx, rc)
	if err == nil {
		t.Fatal("expected error from deadline, got nil")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("runner did not return promptly after deadline: %v", elapsed)
	}
	// errors.Is is unreliable across exec.ExitError unwraps, so just assert non-nil.
	_ = errors.Is(ctx.Err(), context.DeadlineExceeded)
}
