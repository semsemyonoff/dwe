package command

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/cmdbrowser"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// nopT returns the no-op translator used across the selector-closure tests.
func nopT() i18n.Translator { return i18n.NopTranslator{} }

// --- makeRunFormSpec: mode gating ----------------------------------------

// TestMakeRunFormSpec_NilOutsideModeRun asserts the param-form spec is only
// wired in ModeRun; inspect/edit have no param form so the spec is nil (leaving
// cmdbrowser's exit-and-run fall-through untouched).
func TestMakeRunFormSpec_NilOutsideModeRun(t *testing.T) {
	defs := []*usercommands.CommandDef{{ID: "db.up"}}
	for _, mode := range []cmdbrowser.Mode{cmdbrowser.ModeInspect, cmdbrowser.ModeEdit} {
		if spec := makeRunFormSpec(newCfg(), mode, defs, nil, nopT(), ""); spec != nil {
			t.Errorf("mode %v: RunForm spec should be nil, got %+v", mode, spec)
		}
	}
	if spec := makeRunFormSpec(newCfg(), cmdbrowser.ModeRun, defs, nil, nopT(), ""); spec == nil {
		t.Fatal("ModeRun: RunForm spec should be non-nil")
	}
}

// --- makeRunFormSpec.BuildForm -------------------------------------------

// TestMakeRunFormSpec_BuildForm_NoFormWhenSatisfied asserts a command whose
// required params are already satisfied by a declared default returns a nil form
// on plain Enter (force=false) — the browser then quits-and-runs immediately.
func TestMakeRunFormSpec_BuildForm_NoFormWhenSatisfied(t *testing.T) {
	defs := []*usercommands.CommandDef{{
		ID: "db.up", Params: map[string]model.ParamDef{
			"env": {Type: model.ParamTypeString, Required: true, Default: "dev"},
		},
	}}
	spec := makeRunFormSpec(newCfg(), cmdbrowser.ModeRun, defs, nil, nopT(), "")

	form, err := spec.BuildForm(0, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if form != nil {
		t.Errorf("required already satisfied + Enter: expected nil form (quit-and-run), got %+v", form)
	}
}

// TestMakeRunFormSpec_BuildForm_FormWhenForced asserts the force flag (`e`)
// opens the form even when every required param is already satisfied.
func TestMakeRunFormSpec_BuildForm_FormWhenForced(t *testing.T) {
	defs := []*usercommands.CommandDef{{
		ID: "db.up", Params: map[string]model.ParamDef{
			"env": {Type: model.ParamTypeString, Required: true, Default: "dev"},
		},
	}}
	spec := makeRunFormSpec(newCfg(), cmdbrowser.ModeRun, defs, nil, nopT(), "")

	form, err := spec.BuildForm(0, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if form == nil || form.Huh() == nil {
		t.Fatalf("force=true must always build a form; got %+v", form)
	}
}

// TestMakeRunFormSpec_BuildForm_FormWhenRequiredMissing asserts a missing
// required param opens the form even on plain Enter.
func TestMakeRunFormSpec_BuildForm_FormWhenRequiredMissing(t *testing.T) {
	defs := []*usercommands.CommandDef{{
		ID: "db.up", Params: map[string]model.ParamDef{
			"env": {Type: model.ParamTypeString, Required: true},
		},
	}}
	spec := makeRunFormSpec(newCfg(), cmdbrowser.ModeRun, defs, nil, nopT(), "")

	form, err := spec.BuildForm(0, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if form == nil || form.Huh() == nil {
		t.Fatalf("missing required must build a form; got %+v", form)
	}
}

// TestMakeRunFormSpec_BuildForm_SetHonoured asserts --set is parsed lazily and
// its value satisfies a required param, so no form opens on Enter; dropping the
// --set makes the same command open the form.
func TestMakeRunFormSpec_BuildForm_SetHonoured(t *testing.T) {
	defs := []*usercommands.CommandDef{{
		ID: "db.up", Params: map[string]model.ParamDef{
			"env": {Type: model.ParamTypeString, Required: true},
		},
	}}

	withSet := makeRunFormSpec(newCfg(), cmdbrowser.ModeRun, defs, []string{"env=prod"}, nopT(), "")
	form, err := withSet.BuildForm(0, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if form != nil {
		t.Errorf("--set env=prod satisfies required: expected nil form, got %+v", form)
	}

	noSet := makeRunFormSpec(newCfg(), cmdbrowser.ModeRun, defs, nil, nopT(), "")
	form, err = noSet.BuildForm(0, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if form == nil {
		t.Error("without --set the required param is unsatisfied: expected a form")
	}
}

// TestMakeRunFormSpec_BuildForm_MalformedSetErrors asserts a malformed --set
// surfaces as a BuildForm error on the interactive path (lazy parse). The
// non-interactive fallback is covered separately (it never reaches BuildForm).
func TestMakeRunFormSpec_BuildForm_MalformedSetErrors(t *testing.T) {
	defs := []*usercommands.CommandDef{{
		ID: "db.up", Params: map[string]model.ParamDef{
			"env": {Type: model.ParamTypeString, Required: true},
		},
	}}
	spec := makeRunFormSpec(newCfg(), cmdbrowser.ModeRun, defs, []string{"bad"}, nopT(), "")

	form, err := spec.BuildForm(0, false)
	if err == nil {
		t.Fatal("malformed --set must surface as a BuildForm error")
	}
	if form != nil {
		t.Errorf("no form on parse error; got %+v", form)
	}
}

// TestMakeRunFormSpec_BuildForm_IndexGuard asserts an out-of-range index is a
// hard error (defensive; the plugin never passes one).
func TestMakeRunFormSpec_BuildForm_IndexGuard(t *testing.T) {
	defs := []*usercommands.CommandDef{{ID: "db.up"}}
	spec := makeRunFormSpec(newCfg(), cmdbrowser.ModeRun, defs, nil, nopT(), "")

	if _, err := spec.BuildForm(5, false); err == nil {
		t.Error("out-of-range idx must error")
	}
	if _, err := spec.BuildForm(-1, false); err == nil {
		t.Error("negative idx must error")
	}
}

// TestMakeRunFormSpec_BuildForm_EmptyOptionsZeroFields asserts a command whose
// only param is an optional select with empty resolved options yields zero
// fields → a nil form (quit-and-run), never a trapped empty overlay.
func TestMakeRunFormSpec_BuildForm_EmptyOptionsZeroFields(t *testing.T) {
	// vars.svcs resolves to an empty list, so the select has no options.
	cfg := &config.DweConfig{Raw: map[string]any{"vars": map[string]any{"svcs": []any{}}}}
	defs := []*usercommands.CommandDef{{
		ID: "db.up", Params: map[string]model.ParamDef{
			"svc": {
				Type:    model.ParamTypeString,
				Widget:  model.WidgetSelect,
				Options: &model.ParamOptions{From: "vars.svcs"},
			},
		},
	}}
	spec := makeRunFormSpec(cfg, cmdbrowser.ModeRun, defs, nil, nopT(), "")

	// force=true so the showForm predicate is satisfied — the (nil,nil) result
	// must come from the zero-fields guard, not the satisfied-required skip.
	form, err := spec.BuildForm(0, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if form != nil {
		t.Errorf("empty-options optional select → zero fields → nil form; got %+v", form)
	}
}

// --- makeRunFormSpec.Harvest ---------------------------------------------

// TestMakeRunFormSpec_Harvest_MapsResult asserts Harvest maps the submitted
// ask.Result back to the param map, joining a multiselect by its separator.
func TestMakeRunFormSpec_Harvest_MapsResult(t *testing.T) {
	cfg := &config.DweConfig{Raw: map[string]any{"vars": map[string]any{"svcs": []any{"a", "b", "c"}}}}
	defs := []*usercommands.CommandDef{{
		ID: "db.up", Params: map[string]model.ParamDef{
			"name": {Type: model.ParamTypeString, Widget: model.WidgetInput},
			"svc": {
				Type:      model.ParamTypeString,
				Widget:    model.WidgetMultiselect,
				Separator: ",",
				Options:   &model.ParamOptions{From: "vars.svcs"},
			},
		},
	}}
	spec := makeRunFormSpec(cfg, cmdbrowser.ModeRun, defs, nil, nopT(), "")

	res := ask.NewResultForTest(map[string]any{
		"name": "web",
		"svc":  []string{"a", "b"},
	})
	got := spec.Harvest(0, res)
	if got["name"] != "web" {
		t.Errorf("name = %q, want %q", got["name"], "web")
	}
	if got["svc"] != "a,b" {
		t.Errorf("svc = %q, want %q (multiselect joined by separator)", got["svc"], "a,b")
	}
}

// TestMakeRunFormSpec_Harvest_IndexGuard asserts an out-of-range index returns
// nil rather than panicking.
func TestMakeRunFormSpec_Harvest_IndexGuard(t *testing.T) {
	defs := []*usercommands.CommandDef{{ID: "db.up"}}
	spec := makeRunFormSpec(newCfg(), cmdbrowser.ModeRun, defs, nil, nopT(), "")

	if got := spec.Harvest(5, ask.NewResultForTest(nil)); got != nil {
		t.Errorf("out-of-range idx must return nil; got %v", got)
	}
}

// --- non-interactive fallback intact -------------------------------------

// TestCommandsBare_nonTTY_malformedSet_stillPrintsList asserts the lazy --set
// parse keeps the non-interactive writeCommandsList fallback intact: a malformed
// `dwe commands --set bad` in a pipe prints the list and does NOT error (the
// selector is swapped for writeCommandsList before BuildForm can run, so the
// parse never happens).
func TestCommandsBare_nonTTY_malformedSet_stillPrintsList(t *testing.T) {
	cfgPath := setupListProject(t)
	stubInteractive(t, false)

	cmd := NewCmd("", &cmdctx.RootFlags{ConfigPath: cfgPath})
	cmd.SetContext(context.Background())
	if err := cmd.Flags().Set("set", "bad"); err != nil {
		t.Fatalf("set flag: %v", err)
	}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("malformed --set in a pipe must still print the list, not error: %v", err)
	}
	if !strings.Contains(out.String(), "db.up") {
		t.Errorf("list output missing db.up\n---\n%s", out.String())
	}
}
