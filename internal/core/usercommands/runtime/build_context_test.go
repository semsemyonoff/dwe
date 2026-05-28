package runtime

import (
	"path/filepath"
	"testing"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/usercommands/model"
)

// TestBuildRunContext_BasicExecution tests basic param/context resolution.
func TestBuildRunContext_BasicExecution(t *testing.T) {
	tmpdir := t.TempDir()

	cfg := &config.DevboxConfig{
		Raw: map[string]any{
			"db": map[string]any{
				"host": "localhost",
				"port": 5432,
			},
		},
	}

	def := &model.CommandDef{
		ID:   "test.cmd",
		Type: model.CommandTypeShell,
		Params: map[string]model.ParamDef{
			"name": {Type: model.ParamTypeString},
		},
		Context: map[string]model.ContextDef{
			"db": {From: "db"},
		},
		Cmd: "echo hello",
	}

	with := map[string]any{"name": "alice"}

	rctx, err := BuildRunContext(cfg, nil, def, with, tmpdir)
	if err != nil {
		t.Fatalf("BuildRunContext returned error: %v", err)
	}

	// Verify populated fields.
	if rctx.Cmd != def {
		t.Errorf("Cmd mismatch")
	}
	if rctx.ProjectRoot != tmpdir {
		t.Errorf("ProjectRoot = %q, want %q", rctx.ProjectRoot, tmpdir)
	}
	if rctx.Config != cfg {
		t.Errorf("Config mismatch")
	}
	if rctx.Render == nil {
		t.Errorf("Render is nil")
	}
	if rctx.Params == nil {
		t.Errorf("Params is nil")
	}
	if rctx.Context == nil {
		t.Errorf("Context is nil")
	}

	// Verify IO fields are zero-valued.
	if rctx.Stdout != nil {
		t.Errorf("Stdout should be nil")
	}
	if rctx.Stderr != nil {
		t.Errorf("Stderr should be nil")
	}
	if rctx.Stdin != nil {
		t.Errorf("Stdin should be nil")
	}
	if rctx.SkipConfirm != false {
		t.Errorf("SkipConfirm should be false")
	}
	if rctx.NonInteractive != false {
		t.Errorf("NonInteractive should be false")
	}
}

// TestBuildRunContext_DockerConfigMissing tests tolerance for missing docker.yml.
func TestBuildRunContext_DockerConfigMissing(t *testing.T) {
	tmpdir := t.TempDir()

	cfg := &config.DevboxConfig{
		Raw: map[string]any{},
	}

	def := &model.CommandDef{
		ID:      "test.cmd",
		Type:    model.CommandTypeShell,
		Params:  map[string]model.ParamDef{},
		Context: map[string]model.ContextDef{},
		Cmd:     "echo hello",
	}

	rctx, err := BuildRunContext(cfg, nil, def, map[string]any{}, tmpdir)
	if err != nil {
		t.Fatalf("BuildRunContext returned error: %v", err)
	}

	// DockerConfig should be initialized (not nil) even though docker.yml doesn't exist.
	if rctx.DockerConfig == nil {
		t.Errorf("DockerConfig is nil; should be initialized")
	}
}

// TestBuildRunContext_ParamResolveError surfaces param resolution errors.
func TestBuildRunContext_ParamResolveError(t *testing.T) {
	tmpdir := t.TempDir()

	cfg := &config.DevboxConfig{
		Raw: map[string]any{},
	}

	def := &model.CommandDef{
		ID:   "test.cmd",
		Type: model.CommandTypeShell,
		Params: map[string]model.ParamDef{
			"required_param": {
				Type:     model.ParamTypeString,
				Required: true,
			},
		},
		Context: map[string]model.ContextDef{},
		Cmd:     "echo hello",
	}

	// Provide no value for required_param — should error.
	with := map[string]any{}

	_, err := BuildRunContext(cfg, nil, def, with, tmpdir)
	if err == nil {
		t.Errorf("expected error for missing required param, got nil")
	}
	if err != nil {
		t.Logf("error: %v", err)
		// The error should mention the param, even if it's wrapped.
	}
}

// TestBuildRunContext_ContextResolveError surfaces context resolution errors.
func TestBuildRunContext_ContextResolveError(t *testing.T) {
	tmpdir := t.TempDir()

	cfg := &config.DevboxConfig{
		Raw: map[string]any{},
	}

	def := &model.CommandDef{
		ID:     "test.cmd",
		Type:   model.CommandTypeShell,
		Params: map[string]model.ParamDef{},
		Context: map[string]model.ContextDef{
			"missing_ctx": {
				From:     "nonexistent.field",
				Required: true,
			},
		},
		Cmd: "echo hello",
	}

	_, err := BuildRunContext(cfg, nil, def, map[string]any{}, tmpdir)
	if err == nil {
		t.Errorf("expected error for missing context field, got nil")
	}
}

// TestBuildRunContext_WithDefaultParam tests param with default value.
func TestBuildRunContext_WithDefaultParam(t *testing.T) {
	tmpdir := t.TempDir()

	cfg := &config.DevboxConfig{
		Raw: map[string]any{},
	}

	def := &model.CommandDef{
		ID:   "test.cmd",
		Type: model.CommandTypeShell,
		Params: map[string]model.ParamDef{
			"optional_param": {
				Type:    model.ParamTypeString,
				Default: "default_value",
			},
		},
		Context: map[string]model.ContextDef{},
		Cmd:     "echo hello",
	}

	// Provide no value — should use default.
	rctx, err := BuildRunContext(cfg, nil, def, map[string]any{}, tmpdir)
	if err != nil {
		t.Fatalf("BuildRunContext returned error: %v", err)
	}

	if rctx.Params["optional_param"] != "default_value" {
		t.Errorf("param value = %v, want 'default_value'", rctx.Params["optional_param"])
	}
}

// TestBuildRunContext_ConvertWithMapType tests conversion of with map values to strings.
func TestBuildRunContext_ConvertWithMapType(t *testing.T) {
	tmpdir := t.TempDir()

	cfg := &config.DevboxConfig{
		Raw: map[string]any{},
	}

	def := &model.CommandDef{
		ID:   "test.cmd",
		Type: model.CommandTypeShell,
		Params: map[string]model.ParamDef{
			"count": {
				Type: model.ParamTypeInt,
			},
		},
		Context: map[string]model.ContextDef{},
		Cmd:     "echo hello",
	}

	// Pass with as map[string]any with various types.
	with := map[string]any{
		"count": 42,
	}

	rctx, err := BuildRunContext(cfg, nil, def, with, tmpdir)
	if err != nil {
		t.Fatalf("BuildRunContext returned error: %v", err)
	}

	if rctx.Params["count"] != 42 {
		t.Errorf("param count = %v, want 42", rctx.Params["count"])
	}
}

// TestBuildRunContext_WithTemplateRender verifies ${...} expressions in `with`
// values are rendered against project-level Raw before param validation.
func TestBuildRunContext_WithTemplateRender(t *testing.T) {
	tmpdir := t.TempDir()

	cfg := &config.DevboxConfig{
		Raw: map[string]any{
			"db": map[string]any{
				"stock_database": "app_stock",
			},
		},
	}

	def := &model.CommandDef{
		ID:   "test.cmd",
		Type: model.CommandTypeShell,
		Params: map[string]model.ParamDef{
			"database": {
				Type:    model.ParamTypeString,
				Pattern: `^[a-zA-Z0-9_][a-zA-Z0-9_-]*$`,
			},
		},
		Context: map[string]model.ContextDef{},
		Cmd:     "echo hello",
	}

	with := map[string]any{"database": "${db.stock_database}"}

	rctx, err := BuildRunContext(cfg, nil, def, with, tmpdir)
	if err != nil {
		t.Fatalf("BuildRunContext returned error: %v", err)
	}

	if rctx.Params["database"] != "app_stock" {
		t.Errorf("param database = %v, want %q", rctx.Params["database"], "app_stock")
	}
}

// TestBuildRunContext_WithTemplateMissingKey verifies that a ${...} reference to
// a missing config key resolves to "" (consistent with default_from behavior).
func TestBuildRunContext_WithTemplateMissingKey(t *testing.T) {
	tmpdir := t.TempDir()

	cfg := &config.DevboxConfig{
		Raw: map[string]any{},
	}

	def := &model.CommandDef{
		ID:   "test.cmd",
		Type: model.CommandTypeShell,
		Params: map[string]model.ParamDef{
			"name": {Type: model.ParamTypeString},
		},
		Context: map[string]model.ContextDef{},
		Cmd:     "echo hello",
	}

	with := map[string]any{"name": "${db.nonexistent}"}

	rctx, err := BuildRunContext(cfg, nil, def, with, tmpdir)
	if err != nil {
		t.Fatalf("BuildRunContext returned error: %v", err)
	}

	if rctx.Params["name"] != "" {
		t.Errorf("param name = %v, want empty string", rctx.Params["name"])
	}
}

// TestBuildRunContext_WithLiteralPassthrough verifies plain string values without
// ${...} survive rendering untouched (idempotent no-op).
func TestBuildRunContext_WithLiteralPassthrough(t *testing.T) {
	tmpdir := t.TempDir()

	cfg := &config.DevboxConfig{
		Raw: map[string]any{},
	}

	def := &model.CommandDef{
		ID:   "test.cmd",
		Type: model.CommandTypeShell,
		Params: map[string]model.ParamDef{
			"name": {Type: model.ParamTypeString},
		},
		Context: map[string]model.ContextDef{},
		Cmd:     "echo hello",
	}

	with := map[string]any{"name": "literal-value"}

	rctx, err := BuildRunContext(cfg, nil, def, with, tmpdir)
	if err != nil {
		t.Fatalf("BuildRunContext returned error: %v", err)
	}

	if rctx.Params["name"] != "literal-value" {
		t.Errorf("param name = %v, want %q", rctx.Params["name"], "literal-value")
	}
}

// TestBuildRunContext_NoFilesystemSideEffects verifies no filesystem writes happen.
func TestBuildRunContext_NoFilesystemSideEffects(t *testing.T) {
	tmpdir := t.TempDir()

	cfg := &config.DevboxConfig{
		Raw: map[string]any{},
	}

	def := &model.CommandDef{
		ID:      "test.cmd",
		Type:    model.CommandTypeShell,
		Params:  map[string]model.ParamDef{},
		Context: map[string]model.ContextDef{},
		Cmd:     "echo hello",
	}

	before, err := filepath.Glob(filepath.Join(tmpdir, "*"))
	if err != nil {
		t.Fatalf("glob before: %v", err)
	}

	_, err = BuildRunContext(cfg, nil, def, map[string]any{}, tmpdir)
	if err != nil {
		t.Fatalf("BuildRunContext returned error: %v", err)
	}

	after, err := filepath.Glob(filepath.Join(tmpdir, "*"))
	if err != nil {
		t.Fatalf("glob after: %v", err)
	}

	if len(before) != len(after) {
		t.Errorf("filesystem changed: before %d entries, after %d entries", len(before), len(after))
	}
}

// TestBuildRunContext_RenderContextPopulated verifies Render is correctly initialized.
func TestBuildRunContext_RenderContextPopulated(t *testing.T) {
	tmpdir := t.TempDir()

	cfg := &config.DevboxConfig{
		Raw: map[string]any{
			"db": map[string]any{
				"host": "localhost",
			},
		},
	}

	def := &model.CommandDef{
		ID:   "test.cmd",
		Type: model.CommandTypeShell,
		Params: map[string]model.ParamDef{
			"name": {
				Type: model.ParamTypeString,
			},
		},
		Context: map[string]model.ContextDef{
			"db": {
				From: "db",
			},
		},
		Cmd: "echo hello",
	}

	with := map[string]any{"name": "alice"}

	rctx, err := BuildRunContext(cfg, nil, def, with, tmpdir)
	if err != nil {
		t.Fatalf("BuildRunContext returned error: %v", err)
	}

	// Verify RenderContext fields.
	if rctx.Render == nil {
		t.Errorf("Render is nil")
	} else {
		if rctx.Render.Params == nil {
			t.Errorf("Render.Params is nil")
		}
		if rctx.Render.Context == nil {
			t.Errorf("Render.Context is nil")
		}
		if len(rctx.Render.Raw) == 0 {
			t.Errorf("Render.Raw is empty")
		}
	}
}
