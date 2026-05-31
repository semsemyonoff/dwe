package stack

import (
	"strings"
	"testing"

	"github.com/semsemyonoff/devbox/internal/core/project/config"
)

func TestBuildCustomColumns_Empty(t *testing.T) {
	cfg := &config.DevboxConfig{}
	if got := BuildCustomColumns(cfg, config.ServiceTypeApp); got != nil {
		t.Errorf("services: want nil, got %v", got)
	}
	if got := BuildCustomColumns(cfg, config.ServiceTypeTool); got != nil {
		t.Errorf("tools: want nil, got %v", got)
	}
}

func TestBuildCustomColumns_NilCfg(t *testing.T) {
	if got := BuildCustomColumns(nil, config.ServiceTypeApp); got != nil {
		t.Errorf("nil cfg: want nil, got %v", got)
	}
}

func TestBuildCustomColumns_SingleService(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"main": {Type: config.ServiceTypeApp, Status: []config.StatusColumn{
				{Name: "CONTAINER", Value: "{{ .ServiceCfg.Container }}"},
				{Name: "TAG", Value: "v1"},
			}},
		},
	}
	got := BuildCustomColumns(cfg, config.ServiceTypeApp)
	want := []string{"CONTAINER", "TAG"}
	if !equalSlice(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildCustomColumns_OverlappingAndDisjoint(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"beta": {Type: config.ServiceTypeApp, Status: []config.StatusColumn{
				{Name: "BETA_ONLY", Value: "b"},
				{Name: "SHARED", Value: "b"},
			}},
			"alpha": {Type: config.ServiceTypeApp, Status: []config.StatusColumn{
				{Name: "ALPHA_ONLY", Value: "a"},
				{Name: "SHARED", Value: "a"},
			}},
			"gamma": {Type: config.ServiceTypeApp, Status: []config.StatusColumn{
				{Name: "GAMMA_ONLY", Value: "g"},
			}},
		},
	}
	// Alphabetical iteration: alpha, beta, gamma.
	// First-encounter order: ALPHA_ONLY, SHARED, BETA_ONLY, GAMMA_ONLY.
	want := []string{"ALPHA_ONLY", "SHARED", "BETA_ONLY", "GAMMA_ONLY"}

	// Repeat to confirm stability across runs.
	for i := range 5 {
		got := BuildCustomColumns(cfg, config.ServiceTypeApp)
		if !equalSlice(got, want) {
			t.Errorf("iteration %d: got %v, want %v", i, got, want)
		}
	}
}

func TestBuildCustomColumns_Tools(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"mailpit": {Type: config.ServiceTypeTool, Status: []config.StatusColumn{{Name: "ENDPOINT", Value: "x"}}},
			"adminer": {Type: config.ServiceTypeTool, Status: []config.StatusColumn{{Name: "VERSION", Value: "x"}}},
		},
	}
	got := BuildCustomColumns(cfg, config.ServiceTypeTool)
	want := []string{"VERSION", "ENDPOINT"} // adminer < mailpit alphabetically
	if !equalSlice(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRenderCustomCells_Success(t *testing.T) {
	defs := []config.StatusColumn{
		{Name: "CONTAINER", Value: "{{ .ServiceCfg.Container }}"},
		{Name: "LITERAL", Value: "fixed"},
	}
	data := map[string]any{
		"ServiceCfg": config.ServiceConfig{Container: "app-main"},
	}
	got, errs := RenderCustomCells(defs, data)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if got["CONTAINER"] != "app-main" {
		t.Errorf("CONTAINER: got %q, want %q", got["CONTAINER"], "app-main")
	}
	if got["LITERAL"] != "fixed" {
		t.Errorf("LITERAL: got %q, want %q", got["LITERAL"], "fixed")
	}
}

func TestRenderCustomCells_NilEmpty(t *testing.T) {
	got, errs := RenderCustomCells(nil, nil)
	if got != nil {
		t.Errorf("want nil map, got %v", got)
	}
	if errs != nil {
		t.Errorf("want nil errs, got %v", errs)
	}
}

func TestRenderCustomCells_SinglePartialFailure(t *testing.T) {
	defs := []config.StatusColumn{
		{Name: "OK", Value: "{{ .V }}"},
		{Name: "BAD", Value: "{{ nosuchfunc }}"}, // parse failure
	}
	data := map[string]any{"V": "ok"}
	got, errs := RenderCustomCells(defs, data)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d (%v)", len(errs), errs)
	}
	if _, ok := got["BAD"]; ok {
		t.Errorf("BAD should be omitted on failure, got %q", got["BAD"])
	}
	if got["OK"] != "ok" {
		t.Errorf("OK: got %q, want %q", got["OK"], "ok")
	}
}

func TestRenderCustomCells_MultipleFailures(t *testing.T) {
	defs := []config.StatusColumn{
		{Name: "BAD1", Value: "{{ broken1 }}"},
		{Name: "BAD2", Value: "{{ broken2 }}"},
		{Name: "OK", Value: "literal"},
	}
	got, errs := RenderCustomCells(defs, map[string]any{})
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %d (%v)", len(errs), errs)
	}
	if got["OK"] != "literal" {
		t.Errorf("OK: got %q", got["OK"])
	}
}

func TestRenderCustomCells_HermeticContract(t *testing.T) {
	// env, exec, network, FS helpers must not exist in the hermetic FuncMap.
	defs := []config.StatusColumn{
		{Name: "TRY_ENV", Value: `{{ env "PATH" }}`},
	}
	_, errs := RenderCustomCells(defs, map[string]any{})
	if len(errs) != 1 {
		t.Fatalf("expected hermetic contract to reject env helper, got %d errs", len(errs))
	}
	if !strings.Contains(errs[0].Error(), "env") {
		t.Errorf("error should mention env helper, got %v", errs[0])
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
