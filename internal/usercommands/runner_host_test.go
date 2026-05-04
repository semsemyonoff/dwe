package usercommands

import (
	"slices"
	"strings"
	"testing"

	"devbox-cli/internal/tpl"
)

func TestHostRunner_BuildCommand_Run(t *testing.T) {
	r := &HostRunner{}
	ctx := RunContext{
		Cmd: &CommandDef{
			Type: CommandTypeCommand,
			Run:  "echo hello",
		},
		Render:      &tpl.RenderContext{},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(ctx)
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
			Type: CommandTypeCommand,
			Argv: []string{"git", "status"},
		},
		Render:      &tpl.RenderContext{},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(ctx)
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
			Type:    CommandTypeCommand,
			Run:     "pwd",
			Workdir: "/tmp/mydir",
		},
		Render:      &tpl.RenderContext{},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(ctx)
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
			Type:    CommandTypeCommand,
			Run:     "pwd",
			Workdir: "subdir",
		},
		Render:      &tpl.RenderContext{},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(ctx)
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
			Type: CommandTypeCommand,
			Run:  "ls",
		},
		Render:      &tpl.RenderContext{},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(ctx)
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
			Type: CommandTypeCommand,
			Run:  "env",
			Env:  map[string]string{"MY_VAR": "hello"},
		},
		Params:      map[string]any{},
		Context:     map[string]any{},
		Render:      &tpl.RenderContext{},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(ctx)
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
			Type: CommandTypeCommand,
			Run:  "echo ${param.name}",
		},
		Params:  map[string]any{"name": "world"},
		Context: map[string]any{},
		Render: &tpl.RenderContext{
			Params: map[string]any{"name": "world"},
		},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(ctx)
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
			Type:    CommandTypeCommand,
			Run:     "pwd",
			Workdir: "${param.dir}",
		},
		Params:  map[string]any{"dir": "mydir"},
		Context: map[string]any{},
		Render: &tpl.RenderContext{
			Params: map[string]any{"dir": "mydir"},
		},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(ctx)
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
			Type:    CommandTypeCommand,
			Run:     "pwd",
			Workdir: "/tmp/${param.suffix}",
		},
		Params:  map[string]any{"suffix": "test"},
		Context: map[string]any{},
		Render: &tpl.RenderContext{
			Params: map[string]any{"suffix": "test"},
		},
		ProjectRoot: "/project",
	}
	c, err := r.BuildCommand(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Dir != "/tmp/test" {
		t.Errorf("expected /tmp/test, got %q", c.Dir)
	}
}

func TestNewRunner_Returns_HostRunner(t *testing.T) {
	cmd := &CommandDef{Type: CommandTypeCommand}
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
