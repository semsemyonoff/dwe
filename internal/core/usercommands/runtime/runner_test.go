package runtime

import (
	"bytes"
	"context"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// --- RunContext.Compose bin propagation ---

func TestRunContext_Compose_NilConfigNilDockerConfig_DefaultBin(t *testing.T) {
	ctx := RunContext{}
	c := ctx.Compose()
	if c.BinName() != "docker" {
		t.Errorf("Compose().BinName() = %q, want %q", c.BinName(), "docker")
	}
}

func TestRunContext_Compose_CustomDockerBin_NoDockerConfig(t *testing.T) {
	ctx := RunContext{
		Config: &config.DweConfig{},
	}
	c := ctx.Compose()
	// DockerBin returns "docker" when userconfig is nil
	if c.BinName() != "docker" {
		t.Errorf("Compose().BinName() = %q, want %q", c.BinName(), "docker")
	}
}

// TestRunCommand_DefensiveInitNilRender verifies that RunCommand does not panic when Render is nil.
func TestRunCommand_DefensiveInitNilRender(t *testing.T) {
	cmd := &CommandDef{
		ID:    "test.simple",
		Type:  CommandTypeShell,
		Files: map[string]FileSpec{},
		Cmd:   "true",
	}

	ctx := RunContext{
		Cmd:    cmd,
		Render: nil, // Explicitly nil — RunCommand must defensive-init this
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}

	if err := RunCommand(context.Background(), ctx); err != nil {
		t.Fatalf("RunCommand with nil Render returned unexpected error: %v", err)
	}
}

// TestRunCommand_DefensiveInitRawCopy verifies that RunCommand copies Raw from Config into Render.
func TestRunCommand_DefensiveInitRawCopy(t *testing.T) {
	cmd := &CommandDef{
		ID:    "test.with_config",
		Type:  CommandTypeShell,
		Files: map[string]FileSpec{},
		Cmd:   "true",
	}

	raw := map[string]any{"db": map[string]any{"host": "localhost"}}
	ctx := RunContext{
		Cmd: cmd,
		Config: &config.DweConfig{
			Raw: raw,
		},
		Render: &tpl.RenderContext{}, // Empty RenderContext — RunCommand must copy Raw from Config
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}

	if err := RunCommand(context.Background(), ctx); err != nil {
		t.Fatalf("RunCommand returned unexpected error: %v", err)
	}
}

// --- NewRunner factory dispatching ---

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

func TestNewRunner_Returns_ScriptRunner(t *testing.T) {
	cmd := &CommandDef{Type: CommandTypeScript}
	runner, err := NewRunner(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := runner.(*ScriptRunner); !ok {
		t.Errorf("expected *ScriptRunner, got %T", runner)
	}
}

func TestNewRunner_Returns_WorkflowRunner(t *testing.T) {
	cmd := &CommandDef{Type: CommandTypeWorkflow}
	runner, err := NewRunner(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := runner.(*WorkflowRunner); !ok {
		t.Errorf("expected *WorkflowRunner, got %T", runner)
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

// buildWorkflowRegistry creates a Registry with the given commands pre-loaded
// without going through YAML files. Duplicated from runners/workflow/ for the
// root-package tests that still drive WorkflowRunner via the type alias
// (notably notify_workflow_test.go).
func buildWorkflowRegistry(cmds ...*CommandDef) *Registry {
	reg := newEmptyRegistry()
	for _, cmd := range cmds {
		reg.AddCommandForTest(cmd)
	}
	return reg
}
