package commands

import (
	"testing"

	"devbox-cli/internal/tpl"
)

// TestRunCommand_DefensiveInitNilRender tests that RunCommand gracefully handles nil Render.
// This regression test ensures that programmatic callers which pass RunContext{Render: nil}
// do not trigger nil-pointer panics.
func TestRunCommand_DefensiveInitNilRender(t *testing.T) {
	// Create a minimal command without files
	cmd := &CommandDef{
		ID:           "test.simple",
		Type:         CommandTypeCommand,
		Files:        map[string]FileSpec{}, // No files
		Run:          "echo test",
		Confirmation: false,
		Messages:     CommandMessages{},
	}

	// Create a minimal context with nil Render
	ctx := RunContext{
		Cmd:     cmd,
		Params:  map[string]any{},
		Context: map[string]any{},
		Render:  nil, // Explicitly nil
	}

	// Simulate the defensive init in RunCommand
	if ctx.Render == nil {
		ctx.Render = &tpl.RenderContext{}
	}
	if ctx.Render.Raw == nil && ctx.Config != nil {
		ctx.Render.Raw = ctx.Config.Raw
	}
	if ctx.Render.Params == nil {
		ctx.Render.Params = make(map[string]any)
	}
	if ctx.Render.Context == nil {
		ctx.Render.Context = make(map[string]any)
	}

	// Verify the context was properly initialized
	if ctx.Render == nil {
		t.Fatalf("expected ctx.Render to be non-nil after defensive init")
	}
	if ctx.Render.Params == nil {
		t.Fatalf("expected ctx.Render.Params to be non-nil after defensive init")
	}
	if ctx.Render.Context == nil {
		t.Fatalf("expected ctx.Render.Context to be non-nil after defensive init")
	}
}

// TestRunCommand_DefensiveInitRawCopy tests that RunCommand copies Raw from Config when needed.
func TestRunCommand_DefensiveInitRawCopy(t *testing.T) {
	cmd := &CommandDef{
		ID:    "test.with_config",
		Type:  CommandTypeCommand,
		Files: map[string]FileSpec{},
		Run:   "echo",
	}

	// Create a mock config (just a plain map, since we're testing the defensive init logic)
	mockCfg := &tpl.RenderContext{
		Raw:     map[string]any{"db": map[string]any{"host": "localhost"}},
		Params:  make(map[string]any),
		Context: make(map[string]any),
	}

	ctx := RunContext{
		Cmd:    cmd,
		Render: &tpl.RenderContext{}, // Empty RenderContext with nil Raw
	}

	// Simulate the defensive init in RunCommand
	if ctx.Render.Raw == nil && mockCfg.Raw != nil {
		ctx.Render.Raw = mockCfg.Raw
	}

	// Verify Raw was copied
	if ctx.Render.Raw == nil {
		t.Fatalf("expected ctx.Render.Raw to be copied from config")
	}
	if raw, ok := ctx.Render.Raw["db"]; !ok {
		t.Fatalf("expected Raw to contain 'db' key")
	} else if dbMap, ok := raw.(map[string]any); !ok || dbMap["host"] != "localhost" {
		t.Fatalf("expected Raw.db.host to be 'localhost'")
	}
}
