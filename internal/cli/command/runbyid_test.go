package command

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/shared/bridgeclient"
)

// stubOrchestratorSeams replaces the four package-level seams in runbyid.go
// and widgets.IsInteractiveFn, restoring them on cleanup.
// Subtests using this MUST NOT call t.Parallel().
func stubOrchestratorSeams(t *testing.T) *orchestratorStubs {
	t.Helper()
	s := &orchestratorStubs{}
	origAsk := runAsk
	origConfirm := confirmRun
	origRun := runUserCommand
	origNotify := notifyContext
	origInteractive := widgets.IsInteractiveFn
	t.Cleanup(func() {
		runAsk = origAsk
		confirmRun = origConfirm
		runUserCommand = origRun
		notifyContext = origNotify
		widgets.IsInteractiveFn = origInteractive
	})
	return s
}

type orchestratorStubs struct {
	formCalls    int
	formTitle    string
	formFields   []ask.Field
	formValues   ask.Result
	formErr      error
	confirmCalls int
	confirmTitle string
	confirmVals  map[string]string
	confirmOK    bool
	confirmErr   error
	runCalls     int
	runCtx       context.Context
	runRC        usercommands.RunContext
	runErr       error
}

func (s *orchestratorStubs) installForm() {
	runAsk = func(ctx context.Context, title string, fields []ask.Field, opts ask.RunOptions) (ask.Result, error) {
		s.formCalls++
		s.formTitle = title
		s.formFields = fields
		if s.formErr != nil {
			return ask.Result{}, s.formErr
		}
		// Return the configured stub values or defaults.
		if s.formValues.IsEmpty() {
			// formValues not set: populate with field defaults.
			defaults := make(map[string]any)
			for _, f := range fields {
				switch f.Kind {
				case ask.FieldInput, ask.FieldSelect:
					defaults[f.Key] = f.Default
				case ask.FieldMultiselect:
					if f.Defaults != nil {
						defaults[f.Key] = f.Defaults
					} else {
						defaults[f.Key] = []string{}
					}
				case ask.FieldConfirm:
					defaults[f.Key] = false
				}
			}
			return ask.NewResultForTest(defaults), nil
		}
		return s.formValues, nil
	}
}

func (s *orchestratorStubs) installConfirm() {
	confirmRun = func(title string, values map[string]string) (bool, error) {
		s.confirmCalls++
		s.confirmTitle = title
		s.confirmVals = values
		return s.confirmOK, s.confirmErr
	}
}

func (s *orchestratorStubs) installRunner() {
	runUserCommand = func(ctx context.Context, rc usercommands.RunContext) error {
		s.runCalls++
		s.runCtx = ctx
		s.runRC = rc
		return s.runErr
	}
}

func newTestRegistry(defs ...*usercommands.CommandDef) *usercommands.Registry {
	reg := usercommands.NewEmptyRegistry()
	for _, d := range defs {
		reg.AddCommandForTest(d)
	}
	return reg
}

func newCfg() *config.DweConfig {
	return &config.DweConfig{Raw: map[string]any{}}
}

// --- inspect routing -----------------------------------------------------

func TestRunCommandByID_Inspect_WritesToStdout(t *testing.T) {
	s := stubOrchestratorSeams(t)
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.up", LocalName: "up", Group: "db",
		Type: usercommands.CommandTypeShell, Cmd: "echo hi",
	}
	reg := newTestRegistry(def)
	out := &bytes.Buffer{}
	err := runCommandByID(context.Background(), strings.NewReader(""), out, io.Discard,
		newCfg(), reg, t.TempDir(), "db.up", runOpts{Inspect: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "db.up") {
		t.Errorf("inspect output should contain id; got: %q", out.String())
	}
	if s.runCalls != 0 {
		t.Errorf("runner should not be invoked on inspect; got %d calls", s.runCalls)
	}
}

func TestRunCommandByID_InspectPrivate_OK(t *testing.T) {
	s := stubOrchestratorSeams(t)
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.secret", LocalName: "secret", Group: "db",
		Type: usercommands.CommandTypeShell, Cmd: "echo s",
		Private: true,
	}
	reg := newTestRegistry(def)
	out := &bytes.Buffer{}
	err := runCommandByID(context.Background(), strings.NewReader(""), out, io.Discard,
		newCfg(), reg, t.TempDir(), "db.secret", runOpts{Inspect: true})
	if err != nil {
		t.Fatalf("inspect of private must not error: %v", err)
	}
	if !strings.Contains(out.String(), "db.secret") {
		t.Errorf("inspect output should contain id; got: %q", out.String())
	}
	if s.runCalls != 0 {
		t.Errorf("runner must not run; got %d calls", s.runCalls)
	}
}

func TestRunCommandByID_UnknownID_Error(t *testing.T) {
	_ = stubOrchestratorSeams(t)
	reg := newTestRegistry()
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "missing", runOpts{})
	if err == nil {
		t.Fatal("expected error for unknown id")
	}
}

// --- private guard -------------------------------------------------------

func TestRunCommandByID_PrivateDirectRun_Error(t *testing.T) {
	s := stubOrchestratorSeams(t)
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.secret", LocalName: "secret", Group: "db",
		Type: usercommands.CommandTypeShell, Cmd: "echo s", Private: true,
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.secret", runOpts{})
	if err == nil {
		t.Fatal("expected private guard error")
	}
	if !strings.Contains(err.Error(), "private") {
		t.Errorf("error should mention private; got %v", err)
	}
	if s.runCalls != 0 {
		t.Errorf("runner must not run; got %d calls", s.runCalls)
	}
}

// --- form invocation -----------------------------------------------------

func TestRunCommandByID_FormInvokedWhenRequiredUnsatisfied(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	// Stub the form to return a user-supplied value so BuildRunContext's
	// required-check passes after the form runs.
	s.formValues = ask.NewResultForTest(map[string]any{"env": "prod"})
	s.installForm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Cmd: "echo",
		Params: map[string]model.ParamDef{
			"env": {Type: model.ParamTypeString, Required: true},
		},
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.up", runOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.formCalls != 1 {
		t.Errorf("form should be invoked when a required param has no default; got %d", s.formCalls)
	}
	if len(s.formFields) != 1 || s.formFields[0].Key != "env" || !s.formFields[0].Required {
		t.Errorf("expected required field env; got %+v", s.formFields)
	}
	if s.runCalls != 1 {
		t.Errorf("runner should be invoked once; got %d", s.runCalls)
	}
}

// TestRunCommandByID_FormSkippedWhenAllDefaultsPresent verifies the new
// "auto-skip form when every required param is already satisfied" semantics:
// when interactive but every param has a usable default, the form is bypassed
// and the prefilled values flow straight through to the runner.
func TestRunCommandByID_FormSkippedWhenAllDefaultsPresent(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	s.installForm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Cmd: "echo",
		Params: map[string]model.ParamDef{
			"env": {Type: model.ParamTypeString, Default: "dev"},
		},
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.up", runOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.formCalls != 0 {
		t.Errorf("form should be auto-skipped when every required param is defaulted; got %d calls", s.formCalls)
	}
	if s.runCalls != 1 {
		t.Errorf("runner should be invoked once; got %d", s.runCalls)
	}
	if got, _ := s.runRC.Params["env"].(string); got != "dev" {
		t.Errorf("runner should receive prefilled default env=dev; got %v", s.runRC.Params["env"])
	}
}

// TestRunCommandByID_ForceParamFormOpensFormDespiteDefaults covers the TUI
// edit-params key: when the cmdbrowser returns ForceParamForm, the form must
// open even though every required param is already satisfied.
func TestRunCommandByID_ForceParamFormOpensFormDespiteDefaults(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	s.installForm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Cmd: "echo",
		Params: map[string]model.ParamDef{
			"env": {Type: model.ParamTypeString, Default: "dev"},
		},
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.up", runOpts{ForceParamForm: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.formCalls != 1 {
		t.Errorf("form should open under ForceParamForm even with defaults; got %d", s.formCalls)
	}
	if len(s.formFields) != 1 || !strings.Contains(s.formFields[0].Title, "default") {
		t.Errorf("default-sourced field should have (default) in title; got %+v", s.formFields)
	}
}

func TestRunCommandByID_YesSkipsForm(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	s.installForm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Cmd: "echo",
		Params: map[string]model.ParamDef{
			"env": {Type: model.ParamTypeString, Default: "dev"},
		},
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.up",
		runOpts{Yes: true, SetValues: []string{"env=prod"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.formCalls != 0 {
		t.Errorf("form should be skipped with --yes; got %d calls", s.formCalls)
	}
	if s.runCalls != 1 {
		t.Errorf("runner should still be invoked; got %d calls", s.runCalls)
	}
	if s.runRC.Params["env"] != "prod" {
		t.Errorf("expected env=prod, got %v", s.runRC.Params["env"])
	}
	if !s.runRC.SkipConfirm || !s.runRC.NonInteractive {
		t.Errorf("expected SkipConfirm/NonInteractive=true; got %+v", s.runRC)
	}
}

func TestRunCommandByID_MissingRequiredNonInteractive_Error(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return false }
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Cmd: "echo",
		Params: map[string]model.ParamDef{
			"env": {Type: model.ParamTypeString, Required: true},
		},
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.up", runOpts{})
	if err == nil {
		t.Fatal("expected missing-required error")
	}
	if !strings.Contains(err.Error(), "env") {
		t.Errorf("error should mention missing param env; got %v", err)
	}
	if s.runCalls != 0 {
		t.Errorf("runner must not run; got %d calls", s.runCalls)
	}
}

func TestRunCommandByID_MissingRequiredWithYes_Error(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Cmd: "echo",
		Params: map[string]model.ParamDef{
			"env": {Type: model.ParamTypeString, Required: true},
		},
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.up", runOpts{Yes: true})
	if err == nil {
		t.Fatal("expected missing-required error under --yes")
	}
	if s.runCalls != 0 {
		t.Errorf("runner must not run; got %d calls", s.runCalls)
	}
}

func TestRunCommandByID_FormCancel_ExitZero(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	s.formErr = widgets.ErrCancelled
	s.installForm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Cmd: "echo",
		// Required-without-default forces the form to open so cancel is reachable.
		Params: map[string]model.ParamDef{"env": {Type: model.ParamTypeString, Required: true}},
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.up", runOpts{})
	if err != nil {
		t.Fatalf("form cancel should map to nil; got %v", err)
	}
	if s.runCalls != 0 {
		t.Errorf("runner must not run; got %d calls", s.runCalls)
	}
}

func TestRunCommandByID_NoParamsTTY_FormSkipped(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	s.installForm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Cmd: "echo",
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.up", runOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.formCalls != 0 {
		t.Errorf("form should not run for paramless command; got %d", s.formCalls)
	}
	if s.runCalls != 1 {
		t.Errorf("runner should run once; got %d", s.runCalls)
	}
}

// --- confirmation flow ---------------------------------------------------

func TestRunCommandByID_ConfirmationTemplate_RenderedTitle(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	s.confirmOK = true
	s.installForm()
	s.installConfirm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.reset", LocalName: "reset", Group: "db",
		Type: usercommands.CommandTypeShell, Cmd: "echo",
		Confirmation:     true,
		ConfirmationText: "Run ${param.task}?",
		Params: map[string]model.ParamDef{
			"task": {Type: model.ParamTypeString},
		},
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.reset",
		runOpts{Yes: false, SetValues: []string{"task=cleanup"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.confirmCalls != 1 {
		t.Errorf("expected exactly one confirmation; got %d", s.confirmCalls)
	}
	if s.confirmTitle != "Run cleanup?" {
		t.Errorf("title should be template-rendered; got %q", s.confirmTitle)
	}
	if s.runCalls != 1 {
		t.Errorf("runner should run after confirm OK; got %d", s.runCalls)
	}
	if !s.runRC.SkipConfirm {
		t.Errorf("SkipConfirm should be set after orchestrator confirmation")
	}
}

func TestRunCommandByID_ConfirmationSummary_UsesNormalizedParams(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	s.confirmOK = true
	// Form returns empty string for the optional param — orchestrator should
	// fall back to the declared Default in the summary.
	s.formValues = ask.NewResultForTest(map[string]any{"mode": ""})
	s.installForm()
	s.installConfirm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.cleanup", LocalName: "cleanup", Group: "db",
		Type: usercommands.CommandTypeShell, Cmd: "echo",
		Confirmation: true,
		Params: map[string]model.ParamDef{
			"mode": {Type: model.ParamTypeString, Default: "safe"},
		},
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.cleanup", runOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := s.confirmVals["mode"]; got != "safe" {
		t.Errorf("summary should use resolved param (Default fallback); got %q", got)
	}
}

func TestRunCommandByID_ConfirmationCancel_ExitZero(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	s.confirmErr = widgets.ErrCancelled
	s.installConfirm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.x", LocalName: "x", Group: "db",
		Type:         usercommands.CommandTypeShell,
		Cmd:          "echo",
		Confirmation: true,
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.x", runOpts{})
	if err != nil {
		t.Fatalf("confirmation cancel should map to nil; got %v", err)
	}
	if s.runCalls != 0 {
		t.Errorf("runner must not run; got %d", s.runCalls)
	}
}

func TestRunCommandByID_ConfirmationNo_ExitZero(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	s.confirmOK = false
	s.confirmErr = nil
	s.installConfirm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.x", LocalName: "x", Group: "db",
		Type:         usercommands.CommandTypeShell,
		Cmd:          "echo",
		Confirmation: true,
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.x", runOpts{})
	if err != nil {
		t.Fatalf("user No should be silent; got %v", err)
	}
	if s.runCalls != 0 {
		t.Errorf("runner must not run after No; got %d", s.runCalls)
	}
}

func TestRunCommandByID_ConfirmationGenericError_Propagates(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	s.confirmErr = errors.New("boom")
	s.installConfirm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.x", LocalName: "x", Group: "db",
		Type:         usercommands.CommandTypeShell,
		Cmd:          "echo",
		Confirmation: true,
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.x", runOpts{})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("generic confirm error should propagate; got %v", err)
	}
	if s.runCalls != 0 {
		t.Errorf("runner must not run; got %d", s.runCalls)
	}
}

// --- TUI y propagation ---------------------------------------------------

func TestRunCommandByID_TUIYesPropagation(t *testing.T) {
	// opts.Yes from caller (mimicking yesFlag || skipFromTUI at the call site)
	// must skip the orchestrator confirm and set SkipConfirm/NonInteractive on rctx.
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	s.installConfirm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.x", LocalName: "x", Group: "db",
		Type:         usercommands.CommandTypeShell,
		Cmd:          "echo",
		Confirmation: true,
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.x", runOpts{Yes: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.confirmCalls != 0 {
		t.Errorf("confirm should not run when opts.Yes; got %d", s.confirmCalls)
	}
	if !s.runRC.SkipConfirm || !s.runRC.NonInteractive {
		t.Errorf("expected SkipConfirm/NonInteractive=true on rctx; got %+v", s.runRC)
	}
}

// --- I/O wiring ---------------------------------------------------------

func TestRunCommandByID_IOChannelsAttached(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return false }
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.up", LocalName: "up", Group: "db",
		Type: usercommands.CommandTypeShell, Cmd: "echo",
	}
	reg := newTestRegistry(def)
	stdin := strings.NewReader("")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	err := runCommandByID(context.Background(), stdin, stdout, stderr,
		newCfg(), reg, t.TempDir(), "db.up", runOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.runRC.Stdin != stdin {
		t.Errorf("Stdin not attached")
	}
	if s.runRC.Stdout != stdout {
		t.Errorf("Stdout not attached")
	}
	if s.runRC.Stderr != stderr {
		t.Errorf("Stderr not attached")
	}
}

// --- DWE_NONINTERACTIVE env -----------------------------------------

func TestRunCommandByID_NonInteractiveEnv_SkipsForm(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	t.Setenv("DWE_NONINTERACTIVE", "1")
	s.installForm()
	s.installConfirm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.x", LocalName: "x", Group: "db",
		Type: usercommands.CommandTypeShell, Cmd: "echo",
		Confirmation: true,
		Params: map[string]model.ParamDef{
			"env": {Type: model.ParamTypeString, Default: "dev"},
		},
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.x", runOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.formCalls != 0 {
		t.Errorf("form must be skipped under DWE_NONINTERACTIVE; got %d", s.formCalls)
	}
	if s.confirmCalls != 0 {
		t.Errorf("confirm must be skipped under DWE_NONINTERACTIVE; got %d", s.confirmCalls)
	}
	if !s.runRC.SkipConfirm || !s.runRC.NonInteractive {
		t.Errorf("expected SkipConfirm/NonInteractive=true; got %+v", s.runRC)
	}
}

// --- no-double-prompt regression ----------------------------------------

func TestRunCommandByID_NoDoublePrompt(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	s.confirmOK = true
	s.installConfirm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.x", LocalName: "x", Group: "db",
		Type: usercommands.CommandTypeShell, Cmd: "echo",
		Confirmation: true,
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.x", runOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Orchestrator must call confirm exactly once. The downstream runtime
	// ConfirmCommand is suppressed via rctx.SkipConfirm=true.
	if s.confirmCalls != 1 {
		t.Errorf("expected exactly one confirm; got %d", s.confirmCalls)
	}
	if !s.runRC.SkipConfirm {
		t.Errorf("SkipConfirm must be true to prevent runtime re-prompt")
	}
}

// --- non-TTY Y/n fallback ------------------------------------------------

// TestRunCommandByID_NonTTYWithoutYes_FallbackPreserved guards the pipe-stdin
// Y/n path: when stdin is non-TTY and neither --yes nor DWE_NONINTERACTIVE
// is set, the orchestrator must not call confirmRun and must leave
// rctx.SkipConfirm=false so RunCommand's internal ConfirmCommand uses its
// non-TTY Y/n branch.
// --- prepareParams extraction --------------------------------------------

// TestPrepareParams_MatchesInlineBehaviour asserts the extracted prepareParams
// returns the same prefilled + resolvedOpts (and the same membership errors)
// that runCommandByID's inline code produced before the refactor.
func TestPrepareParams_MatchesInlineBehaviour(t *testing.T) {
	cfg := &config.DweConfig{Raw: map[string]any{
		"vars": map[string]any{"envs": []any{"dev", "prod"}},
	}}

	tests := []struct {
		name         string
		def          *usercommands.CommandDef
		provided     map[string]string
		wantPrefill  map[string]string
		wantOptKeys  []string // params expected in resolvedOpts
		wantErrMatch string
	}{
		{
			name: "plain string default",
			def: &usercommands.CommandDef{
				ID: "db.up", Params: map[string]model.ParamDef{
					"env": {Type: model.ParamTypeString, Default: "dev"},
				},
			},
			provided:    map[string]string{},
			wantPrefill: map[string]string{"env": "dev"},
		},
		{
			name: "provided overrides default",
			def: &usercommands.CommandDef{
				ID: "db.up", Params: map[string]model.ParamDef{
					"env": {Type: model.ParamTypeString, Default: "dev"},
				},
			},
			provided:    map[string]string{"env": "prod"},
			wantPrefill: map[string]string{"env": "prod"},
		},
		{
			name: "select membership ok",
			def: &usercommands.CommandDef{
				ID: "db.up", Params: map[string]model.ParamDef{
					"env": {
						Type:    model.ParamTypeString,
						Widget:  model.WidgetSelect,
						Default: "prod",
						Options: &model.ParamOptions{From: "vars.envs"},
					},
				},
			},
			provided:    map[string]string{},
			wantPrefill: map[string]string{"env": "prod"},
			wantOptKeys: []string{"env"},
		},
		{
			name: "select --set not in options errors",
			def: &usercommands.CommandDef{
				ID: "db.up", Params: map[string]model.ParamDef{
					"env": {
						Type:    model.ParamTypeString,
						Widget:  model.WidgetSelect,
						Options: &model.ParamOptions{From: "vars.envs"},
					},
				},
			},
			provided:     map[string]string{"env": "staging"},
			wantErrMatch: "not in options",
		},
		{
			name: "select default not in options errors",
			def: &usercommands.CommandDef{
				ID: "db.up", Params: map[string]model.ParamDef{
					"env": {
						Type:    model.ParamTypeString,
						Widget:  model.WidgetSelect,
						Default: "staging",
						Options: &model.ParamOptions{From: "vars.envs"},
					},
				},
			},
			provided:     map[string]string{},
			wantErrMatch: "default value",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prefilled, resolvedOpts, err := prepareParams(cfg, tc.def, tc.provided)
			if tc.wantErrMatch != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrMatch) {
					t.Fatalf("expected error containing %q; got %v", tc.wantErrMatch, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for k, want := range tc.wantPrefill {
				if prefilled[k] != want {
					t.Errorf("prefilled[%q] = %q, want %q", k, prefilled[k], want)
				}
			}
			for _, k := range tc.wantOptKeys {
				if _, ok := resolvedOpts[k]; !ok {
					t.Errorf("resolvedOpts missing key %q", k)
				}
			}
		})
	}
}

// --- PrefilledParams short-circuit ---------------------------------------

// TestRunCommandByID_PrefilledParams_SkipsForm verifies that when the in-TUI
// overlay has already harvested the params, runCommandByID skips the huh form
// and builds the run context straight from PrefilledParams.
func TestRunCommandByID_PrefilledParams_SkipsForm(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	s.installForm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Cmd: "echo",
		Params: map[string]model.ParamDef{
			"env": {Type: model.ParamTypeString, Required: true},
		},
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.up",
		runOpts{PrefilledParams: map[string]string{"env": "prod"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.formCalls != 0 {
		t.Errorf("form must be skipped when PrefilledParams is set; got %d calls", s.formCalls)
	}
	if s.runCalls != 1 {
		t.Errorf("runner should run once; got %d", s.runCalls)
	}
	if got, _ := s.runRC.Params["env"].(string); got != "prod" {
		t.Errorf("runner should receive harvested env=prod; got %v", s.runRC.Params["env"])
	}
}

// TestRunCommandByID_PrefilledParams_ConfirmStillFires verifies the confirm
// block runs post-form even on the PrefilledParams path (confirm stays
// post-exit; the overlay only collects params).
func TestRunCommandByID_PrefilledParams_ConfirmStillFires(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	s.confirmOK = true
	s.installForm()
	s.installConfirm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.reset", LocalName: "reset", Group: "db",
		Type: usercommands.CommandTypeShell, Cmd: "echo",
		Confirmation: true,
		Params: map[string]model.ParamDef{
			"task": {Type: model.ParamTypeString},
		},
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.reset",
		runOpts{PrefilledParams: map[string]string{"task": "cleanup"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.formCalls != 0 {
		t.Errorf("form must be skipped on PrefilledParams path; got %d", s.formCalls)
	}
	if s.confirmCalls != 1 {
		t.Errorf("confirm should still fire post-exit; got %d", s.confirmCalls)
	}
	if got := s.confirmVals["task"]; got != "cleanup" {
		t.Errorf("confirm summary should use harvested task=cleanup; got %q", got)
	}
	if s.runCalls != 1 {
		t.Errorf("runner should run once after confirm; got %d", s.runCalls)
	}
}

// TestRunCommandByID_PrefilledParams_MissingRequiredSafetyNet asserts that a
// harvested map that omits a required param is still rejected — by resolve at
// BuildRunContext time (the final safety net), not silently run.
func TestRunCommandByID_PrefilledParams_MissingRequiredSafetyNet(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	s.installForm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.up", LocalName: "up", Group: "db", Type: usercommands.CommandTypeShell, Cmd: "echo",
		Params: map[string]model.ParamDef{
			"env": {Type: model.ParamTypeString, Required: true},
		},
	}
	reg := newTestRegistry(def)
	// Harvested map omits the required "env" (empty overlay result).
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.up",
		runOpts{PrefilledParams: map[string]string{}})
	if err == nil {
		t.Fatal("expected missing-required error from resolve safety net")
	}
	if s.formCalls != 0 {
		t.Errorf("form must not open on PrefilledParams path; got %d", s.formCalls)
	}
	if s.runCalls != 0 {
		t.Errorf("runner must not run; got %d", s.runCalls)
	}
}

func TestRunCommandByID_NonTTYWithoutYes_FallbackPreserved(t *testing.T) {
	s := stubOrchestratorSeams(t)
	widgets.IsInteractiveFn = func(io.Reader) bool { return false }
	s.installConfirm()
	s.installRunner()
	def := &usercommands.CommandDef{
		ID: "db.x", LocalName: "x", Group: "db",
		Type:         usercommands.CommandTypeShell,
		Cmd:          "echo",
		Confirmation: true,
	}
	reg := newTestRegistry(def)
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.x", runOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.confirmCalls != 0 {
		t.Errorf("confirmRun must not be called when canPromptHuh=false; got %d calls", s.confirmCalls)
	}
	if s.runCalls != 1 {
		t.Errorf("runner must be called once; got %d calls", s.runCalls)
	}
	if s.runRC.SkipConfirm {
		t.Errorf("rctx.SkipConfirm must be false to preserve the non-TTY Y/n fallback")
	}
	if s.runRC.NonInteractive {
		t.Errorf("rctx.NonInteractive must be false to preserve the non-TTY Y/n fallback")
	}
}

// --- pass-through arguments ----------------------------------------------

// TestRunCommandByID_PassThroughArgsReachRenderContext pins the one line that
// joins the CLI's `--` split to the runtime: rctx.Render.Args is where the
// caller's arguments become the command's arguments. Every unit below it
// (ArgsSpec.Resolve, RenderShellCommand, normalizeRenderContext) is tested in
// isolation, so a break here — a dropped assignment, a nil Render guard that
// starts firing — would otherwise pass CI with everything else green.
func TestRunCommandByID_PassThroughArgsReachRenderContext(t *testing.T) {
	tests := []struct {
		name     string
		args     *usercommands.ArgsSpec
		passed   []string
		wantArgs []string
	}{
		{
			name:     "prefix is inserted before the caller's args",
			args:     &usercommands.ArgsSpec{Prefix: []string{"--"}},
			passed:   []string{"-v"},
			wantArgs: []string{"--", "-v"},
		},
		{
			name:     "default applies when the caller passes none",
			args:     &usercommands.ArgsSpec{Default: []string{"--run"}},
			passed:   nil,
			wantArgs: []string{"--run"},
		},
		{
			name:     "no args block passes the caller's args through verbatim",
			args:     nil,
			passed:   []string{"a", "b"},
			wantArgs: []string{"a", "b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stubOrchestratorSeams(t)
			s.installRunner()
			def := &usercommands.CommandDef{
				ID: "site.test", LocalName: "test", Group: "site",
				Type: usercommands.CommandTypeShell, Cmd: "npm test ${args}",
				Args: tt.args,
			}
			reg := newTestRegistry(def)
			err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
				newCfg(), reg, t.TempDir(), "site.test", runOpts{PassThroughArgs: tt.passed})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if s.runCalls != 1 {
				t.Fatalf("runner should be invoked once; got %d", s.runCalls)
			}
			if s.runRC.Render == nil {
				t.Fatal("rctx.Render is nil — pass-through args would be silently dropped")
			}
			if !reflect.DeepEqual(s.runRC.Render.Args, tt.wantArgs) {
				t.Errorf("Render.Args = %v, want %v", s.runRC.Render.Args, tt.wantArgs)
			}
		})
	}
}

// --- nested-runtime marker / UserInvoked ---------------------------------
//
// These are the consumption half of the nested-runtime contract; the
// propagation half (that a pipeline's children actually inherit
// DWE_NESTED_RUNTIME) lives in package pipeline, because the child is a
// separate process whose in-process UserInvoked no test here can observe.
//
// EVERY test below must clear the marker with t.Setenv(…, ""):
// runCommandByID's os.Setenv is process-global and never cleared, and ~30
// other tests in this file call runCommandByID. t.Setenv also forbids
// t.Parallel — which stubOrchestratorSeams already requires anyway.

// runMarkerProbe runs a trivial command through runCommandByID and returns the
// UserInvoked value the runner saw.
func runMarkerProbe(t *testing.T, s *orchestratorStubs) bool {
	t.Helper()
	def := &usercommands.CommandDef{
		ID: "db.up", LocalName: "up", Group: "db",
		Type: usercommands.CommandTypeShell, Cmd: "echo hi",
	}
	reg := newTestRegistry(def)
	before := s.runCalls
	err := runCommandByID(context.Background(), strings.NewReader(""), io.Discard, io.Discard,
		newCfg(), reg, t.TempDir(), "db.up", runOpts{Yes: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.runCalls != before+1 {
		t.Fatalf("runner should be invoked once; got %d calls", s.runCalls-before)
	}
	return s.runRC.UserInvoked
}

// TestRunCommandByID_UserInvoked_ReadsMarkerBeforeWriting pins the ordering: a
// top-level invocation must not be classified nested by its own assignment,
// and the second call — a stand-in for anything this command spawns and
// re-enters with — must be.
func TestRunCommandByID_UserInvoked_ReadsMarkerBeforeWriting(t *testing.T) {
	t.Setenv(bridgeclient.EnvNestedRuntime, "")
	s := stubOrchestratorSeams(t)
	s.installRunner()

	if got := runMarkerProbe(t, s); !got {
		t.Errorf("first (top-level) invocation: UserInvoked = false, want true")
	}
	if got := runMarkerProbe(t, s); got {
		t.Errorf("second invocation (marker now set): UserInvoked = true, want false")
	}
}

// TestRunCommandByID_UserInvoked_NestedMarkerSuppresses is the consumption
// assertion: a dwe re-entered from a pipeline step (or from a type: shell
// snippet calling back through DWE_BIN) inherits the marker and must not hand
// a container a TTY.
func TestRunCommandByID_UserInvoked_NestedMarkerSuppresses(t *testing.T) {
	t.Setenv(bridgeclient.EnvNestedRuntime, "1")
	s := stubOrchestratorSeams(t)
	s.installRunner()

	if got := runMarkerProbe(t, s); got {
		t.Errorf("UserInvoked = true with the marker set, want false")
	}
}

// TestRunCommandByID_UserInvoked_BridgedStaysUserInvoked pins that the bridge
// path does NOT set the marker: core/bridge/exec.go re-execs `dwe <argv…>` as
// a plain subprocess that lands on this same line, and a bridged `dwe cmd` IS
// a user invocation — the whole reason bridgedTTYChildIO exists. Note the
// forced DWE_NONINTERACTIVE=1: the daemon sets it on every forked dwe, so the
// predicate must never key off NonInteractive.
func TestRunCommandByID_UserInvoked_BridgedStaysUserInvoked(t *testing.T) {
	t.Setenv(bridgeclient.EnvNestedRuntime, "")
	t.Setenv(bridgeclient.EnvInvokedFrom, bridgeclient.InvokedFromContainer)
	t.Setenv(bridgeclient.EnvNonInteractive, "1")
	s := stubOrchestratorSeams(t)
	s.installRunner()

	if got := runMarkerProbe(t, s); !got {
		t.Errorf("bridged invocation: UserInvoked = false, want true")
	}
	if !s.runRC.NonInteractive {
		t.Errorf("sanity: bridged invocation should be non-interactive")
	}
}
