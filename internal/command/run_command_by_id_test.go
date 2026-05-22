package command

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands"
	"devbox-cli/internal/usercommands/model"
)

// stubOrchestratorSeams replaces the four package-level seams in command_cmd.go
// and ui.IsInteractiveFn, restoring them on cleanup.
// Subtests using this MUST NOT call t.Parallel().
func stubOrchestratorSeams(t *testing.T) *orchestratorStubs {
	t.Helper()
	s := &orchestratorStubs{}
	origForm := runParamForm
	origConfirm := confirmRun
	origRun := runUserCommand
	origNotify := notifyContext
	origInteractive := ui.IsInteractiveFn
	t.Cleanup(func() {
		runParamForm = origForm
		confirmRun = origConfirm
		runUserCommand = origRun
		notifyContext = origNotify
		ui.IsInteractiveFn = origInteractive
	})
	return s
}

type orchestratorStubs struct {
	formCalls    int
	formTitle    string
	formFields   []ui.ParamField
	formValues   map[string]string
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
	runParamForm = func(title string, fields []ui.ParamField) (map[string]string, error) {
		s.formCalls++
		s.formTitle = title
		s.formFields = fields
		if s.formErr != nil {
			return nil, s.formErr
		}
		out := s.formValues
		if out == nil {
			out = map[string]string{}
			for _, f := range fields {
				out[f.Name] = f.Default
			}
		}
		return out, nil
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

func newCfg() *config.DevboxConfig {
	return &config.DevboxConfig{Raw: map[string]any{}}
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
	ui.IsInteractiveFn = func(io.Reader) bool { return true }
	// Stub the form to return a user-supplied value so BuildRunContext's
	// required-check passes after the form runs.
	s.formValues = map[string]string{"env": "prod"}
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
	if len(s.formFields) != 1 || s.formFields[0].Name != "env" || !s.formFields[0].Required {
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
	ui.IsInteractiveFn = func(io.Reader) bool { return true }
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
	ui.IsInteractiveFn = func(io.Reader) bool { return true }
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
	if len(s.formFields) != 1 || !s.formFields[0].IsDefault {
		t.Errorf("default-sourced field should carry IsDefault=true; got %+v", s.formFields)
	}
}

func TestRunCommandByID_YesSkipsForm(t *testing.T) {
	s := stubOrchestratorSeams(t)
	ui.IsInteractiveFn = func(io.Reader) bool { return true }
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
	ui.IsInteractiveFn = func(io.Reader) bool { return false }
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
	ui.IsInteractiveFn = func(io.Reader) bool { return true }
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
	ui.IsInteractiveFn = func(io.Reader) bool { return true }
	s.formErr = ui.ErrCancelled
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
	ui.IsInteractiveFn = func(io.Reader) bool { return true }
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
	ui.IsInteractiveFn = func(io.Reader) bool { return true }
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
	ui.IsInteractiveFn = func(io.Reader) bool { return true }
	s.confirmOK = true
	// Form returns empty string for the optional param — orchestrator should
	// fall back to the declared Default in the summary.
	s.formValues = map[string]string{"mode": ""}
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
	ui.IsInteractiveFn = func(io.Reader) bool { return true }
	s.confirmErr = ui.ErrCancelled
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
	ui.IsInteractiveFn = func(io.Reader) bool { return true }
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
	ui.IsInteractiveFn = func(io.Reader) bool { return true }
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
	ui.IsInteractiveFn = func(io.Reader) bool { return true }
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
	ui.IsInteractiveFn = func(io.Reader) bool { return false }
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

// --- DEVBOX_NONINTERACTIVE env -----------------------------------------

func TestRunCommandByID_NonInteractiveEnv_SkipsForm(t *testing.T) {
	s := stubOrchestratorSeams(t)
	ui.IsInteractiveFn = func(io.Reader) bool { return true }
	t.Setenv("DEVBOX_NONINTERACTIVE", "1")
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
		t.Errorf("form must be skipped under DEVBOX_NONINTERACTIVE; got %d", s.formCalls)
	}
	if s.confirmCalls != 0 {
		t.Errorf("confirm must be skipped under DEVBOX_NONINTERACTIVE; got %d", s.confirmCalls)
	}
	if !s.runRC.SkipConfirm || !s.runRC.NonInteractive {
		t.Errorf("expected SkipConfirm/NonInteractive=true; got %+v", s.runRC)
	}
}

// --- no-double-prompt regression ----------------------------------------

func TestRunCommandByID_NoDoublePrompt(t *testing.T) {
	s := stubOrchestratorSeams(t)
	ui.IsInteractiveFn = func(io.Reader) bool { return true }
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
// Y/n path: when stdin is non-TTY and neither --yes nor DEVBOX_NONINTERACTIVE
// is set, the orchestrator must not call confirmRun and must leave
// rctx.SkipConfirm=false so RunCommand's internal ConfirmCommand uses its
// non-TTY Y/n branch.
func TestRunCommandByID_NonTTYWithoutYes_FallbackPreserved(t *testing.T) {
	s := stubOrchestratorSeams(t)
	ui.IsInteractiveFn = func(io.Reader) bool { return false }
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

// --- paramFieldsFromDef --------------------------------------------------

func TestParamFieldsFromDef_DeterministicOrder(t *testing.T) {
	def := &usercommands.CommandDef{
		Params: map[string]model.ParamDef{
			"zeta":  {Type: model.ParamTypeBool},
			"alpha": {Type: model.ParamTypeString, Required: true, Pattern: "^x"},
			"beta":  {Type: model.ParamTypeInt},
		},
	}
	pre := map[string]string{"alpha": "xy", "beta": "3"}
	fields := paramFieldsFromDef(def, pre, nil)
	if len(fields) != 3 {
		t.Fatalf("expected 3 fields; got %d", len(fields))
	}
	wantOrder := []string{"alpha", "beta", "zeta"}
	for i, name := range wantOrder {
		if fields[i].Name != name {
			t.Errorf("[%d]: want %q, got %q", i, name, fields[i].Name)
		}
	}
	if fields[0].Default != "xy" || !fields[0].Required || fields[0].Pattern != "^x" {
		t.Errorf("alpha field: %+v", fields[0])
	}
	if fields[1].Type != ui.FieldTypeInt {
		t.Errorf("beta should be FieldTypeInt; got %v", fields[1].Type)
	}
	if fields[2].Type != ui.FieldTypeBool {
		t.Errorf("zeta should be FieldTypeBool; got %v", fields[2].Type)
	}
}

func TestParamFieldType_DefaultsToString(t *testing.T) {
	if got := paramFieldType(""); got != ui.FieldTypeString {
		t.Errorf("empty ParamType should map to FieldTypeString; got %v", got)
	}
}
