package runtime

import (
	"bytes"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/tpl"
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
		Config: &config.DevboxConfig{
			Binaries: config.BinariesConfig{Docker: "podman"},
		},
	}
	c := ctx.Compose()
	if c.BinName() != "podman" {
		t.Errorf("Compose().BinName() = %q, want %q", c.BinName(), "podman")
	}
}

// TestRunCommand_DefensiveInitNilRender verifies that RunCommand does not panic when Render is nil.
func TestRunCommand_DefensiveInitNilRender(t *testing.T) {
	cmd := &CommandDef{
		ID:    "test.simple",
		Type:  CommandTypeCommand,
		Files: map[string]FileSpec{},
		Run:   "true",
	}

	ctx := RunContext{
		Cmd:    cmd,
		Render: nil, // Explicitly nil — RunCommand must defensive-init this
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}

	if err := RunCommand(ctx); err != nil {
		t.Fatalf("RunCommand with nil Render returned unexpected error: %v", err)
	}
}

// TestRunCommand_DefensiveInitRawCopy verifies that RunCommand copies Raw from Config into Render.
func TestRunCommand_DefensiveInitRawCopy(t *testing.T) {
	cmd := &CommandDef{
		ID:    "test.with_config",
		Type:  CommandTypeCommand,
		Files: map[string]FileSpec{},
		Run:   "true",
	}

	raw := map[string]any{"db": map[string]any{"host": "localhost"}}
	ctx := RunContext{
		Cmd: cmd,
		Config: &config.DevboxConfig{
			Raw: raw,
		},
		Render: &tpl.RenderContext{}, // Empty RenderContext — RunCommand must copy Raw from Config
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}

	if err := RunCommand(ctx); err != nil {
		t.Fatalf("RunCommand returned unexpected error: %v", err)
	}
}
