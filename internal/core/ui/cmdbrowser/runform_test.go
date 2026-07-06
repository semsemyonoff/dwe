package cmdbrowser

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// runFormTestItems mirrors editTestItems: two leaves under a single "db" group so
// the list panel shows them directly (index 0 = db.migrate, 1 = db.seed). The
// ParamCount badge on db.migrate is cosmetic here — the RunFormSpec closures own
// whether a form actually opens.
func runFormTestItems() []Item {
	return []Item{
		{ID: "db.migrate", Description: "apply migrations", Type: "shell", ParamCount: 1},
		{ID: "db.seed", Description: "load fixtures", Type: "shell"},
	}
}

// runFormRecord captures what the RunFormSpec closures saw so tests can assert
// the plugin threaded the right index and force flag into BuildForm and harvested
// from the right index.
type runFormRecord struct {
	buildCalls   int
	buildIdx     int
	buildForce   bool
	harvestCalls int
	harvestIdx   int
}

// runFormConfig steers the BuildForm/Harvest closures per test.
type runFormConfig struct {
	buildErr  error             // BuildForm returns (nil, buildErr)
	noForm    bool              // BuildForm returns (nil, nil) — no form needed
	emptyForm bool              // BuildForm returns an empty *ask.Form (nil Huh)
	values    map[string]string // Harvest returns this (nil → {"name": typed})
}

// newRunFormBrowser builds a ModeRun browser over runFormTestItems with a working
// RunFormSpec: BuildForm makes a single-input form (unless steered to error / no
// form / empty form), and Harvest records the call and returns the bound value.
// The browser is sized to a real body and focused on the list so onSelect
// resolves a row.
func newRunFormBrowser(t *testing.T, rec *runFormRecord, cfg runFormConfig) *browser {
	t.Helper()
	items := runFormTestItems()
	opts := DefaultOptions()
	opts.Mode = ModeRun
	opts.RunForm = &RunFormSpec{
		BuildForm: func(idx int, force bool) (*ask.Form, error) {
			rec.buildCalls++
			rec.buildIdx = idx
			rec.buildForce = force
			if cfg.buildErr != nil {
				return nil, cfg.buildErr
			}
			if cfg.noForm {
				return nil, nil
			}
			if cfg.emptyForm {
				// Zero fields → ask.Build returns &Form{empty:true} with a nil huh
				// form (the trapped-overlay guard input).
				return ask.Build("run "+items[idx].ID, nil, ask.RunOptions{ShowHelp: falsePtr()})
			}
			return ask.Build("run "+items[idx].ID, []ask.Field{{
				Key:   "name",
				Kind:  ask.FieldInput,
				Title: "name",
			}}, ask.RunOptions{ShowHelp: falsePtr()})
		},
		Harvest: func(idx int, res ask.Result) map[string]string {
			rec.harvestCalls++
			rec.harvestIdx = idx
			if cfg.values != nil {
				return cfg.values
			}
			return map[string]string{"name": res.String("name")}
		},
	}
	b := newBrowser("dwe", items, opts)
	b.Resize(tui.Region{Width: 80, Height: 24})
	b.active = panelList
	return b
}

// openRunFormViaSelect drives Enter through onSelect (as HandleAction would) and
// pumps the returned form Init cmd so the embedded input is focused.
func (p *pumper) openRunFormViaSelect(b *browser) {
	p.t.Helper()
	cmd, handled := b.onSelect()
	if !handled {
		p.t.Fatal("onSelect not handled")
	}
	p.pump(b, cmd)
}

func TestBrowser_RunFormOpensCapturingOverlay(t *testing.T) {
	var rec runFormRecord
	b := newRunFormBrowser(t, &rec, runFormConfig{})

	if _, ok := b.PendingOverlay(); ok {
		t.Fatal("PendingOverlay() = true before run form opens, want false")
	}

	newPumper(t).openRunFormViaSelect(b)
	if b.runForm == nil {
		t.Fatal("onSelect did not open the run-form overlay")
	}
	if b.runForm.idx != 0 {
		t.Errorf("run-form idx = %d, want 0 (db.migrate)", b.runForm.idx)
	}
	// Enter must build with force=false.
	if rec.buildForce {
		t.Error("Enter built the form with force=true, want false")
	}

	ov, ok := b.PendingOverlay()
	if !ok {
		t.Fatal("PendingOverlay() = false after openRunForm, want true")
	}
	if !ov.CapturesInput {
		t.Error("run-form overlay CapturesInput = false, want true")
	}
	if ov.Width <= 0 || ov.Height <= 0 {
		t.Errorf("overlay dims = %dx%d, want positive", ov.Width, ov.Height)
	}
	// No double-push: a follow-up drain without a republish yields nothing.
	if _, ok := b.PendingOverlay(); ok {
		t.Error("PendingOverlay() = true on second drain, want false (no double-push)")
	}
}

// TestBrowser_RunFormForceFlagThreaded pins that the force-form key (`e`) routes
// through openRunForm with force=true, while Enter uses false.
func TestBrowser_RunFormForceFlagThreaded(t *testing.T) {
	var rec runFormRecord
	b := newRunFormBrowser(t, &rec, runFormConfig{})

	cmd, handled := b.onForceForm()
	if !handled {
		t.Fatal("onForceForm not handled")
	}
	if b.runForm == nil {
		t.Fatal("onForceForm did not open the run-form overlay")
	}
	if !rec.buildForce {
		t.Error("force-form built the form with force=false, want true")
	}
	if cmd == nil {
		t.Error("openRunForm returned nil cmd, want the form Init cmd")
	}
}

func TestBrowser_RunFormClosedByOverlayClosedMsg(t *testing.T) {
	var rec runFormRecord
	b := newRunFormBrowser(t, &rec, runFormConfig{})
	newPumper(t).openRunFormViaSelect(b)
	b.PendingOverlay()
	if b.runForm == nil {
		t.Fatal("run form should be open before close")
	}

	// Frame pops the capturing overlay on esc and forwards OverlayClosedMsg.
	if cmd := b.Update(tui.OverlayClosedMsg{}); cmd != nil {
		t.Errorf("OverlayClosedMsg should not return a command, got %v", cmd)
	}
	if b.runForm != nil || b.runFormPending {
		t.Fatalf("runForm=%v pending=%v after close, want nil/false", b.runForm, b.runFormPending)
	}
	// Cancel runs no command.
	if rec.harvestCalls != 0 {
		t.Errorf("Harvest called %d times on esc cancel, want 0", rec.harvestCalls)
	}
	if b.result.Action != ActionUnknown {
		t.Errorf("esc cancel set Result.Action = %v, want ActionUnknown (no run)", b.result.Action)
	}

	// A later unmatched raw key must NOT resurrect the closed overlay.
	b.Update(tea.KeyPressMsg{Code: 'x'})
	if _, ok := b.PendingOverlay(); ok {
		t.Error("unmatched key after close re-marked the overlay pending; closed run form resurrected")
	}
}

// TestBrowser_RunFormCompleteHarvestsAndQuits pins the harvest-and-quit terminal
// action: typing then Enter drives StateCompleted → Harvest → Result.Values +
// tea.Quit + a CloseOverlayMsg command.
func TestBrowser_RunFormCompleteHarvestsAndQuits(t *testing.T) {
	var rec runFormRecord
	b := newRunFormBrowser(t, &rec, runFormConfig{})
	b.skipConfirm = true
	p := newPumper(t)
	p.openRunFormViaSelect(b)
	b.PendingOverlay()

	// Type onto the empty field, then submit. Completion is asynchronous — walk the
	// cmd tree so the follow-up nextGroup msg drives StateCompleted → harvest, and
	// capture whether the terminal batch carried a CloseOverlayMsg and a QuitMsg.
	for _, r := range "hello" {
		p.pump(b, b.Update(tea.KeyPressMsg{Code: r, Text: string(r)}))
	}

	var sawClose, sawQuit bool
	var walk func(c tea.Cmd)
	walk = func(c tea.Cmd) {
		msg, ok := p.execFast(c)
		if !ok || msg == nil {
			return
		}
		switch msg.(type) {
		case tui.CloseOverlayMsg:
			sawClose = true
			return
		case tea.QuitMsg:
			sawQuit = true
			return
		}
		if cmds, ok := asCmdSlice(msg); ok {
			for _, sub := range cmds {
				walk(sub)
			}
			return
		}
		walk(b.Update(msg))
	}
	walk(b.Update(tea.KeyPressMsg{Code: tea.KeyEnter}))

	if rec.harvestCalls != 1 {
		t.Fatalf("Harvest calls = %d, want 1", rec.harvestCalls)
	}
	if rec.harvestIdx != 0 {
		t.Errorf("Harvest idx = %d, want 0 (db.migrate)", rec.harvestIdx)
	}
	if !sawClose {
		t.Error("completion did not return a CloseOverlayMsg command")
	}
	if !sawQuit {
		t.Error("completion did not return a tea.Quit command")
	}
	// Result carries the harvested values and the run intent.
	if b.result.Action != ActionRun {
		t.Errorf("Result.Action = %v, want ActionRun", b.result.Action)
	}
	if !b.result.SkipConfirm {
		t.Error("Result.SkipConfirm = false, want true (b.skipConfirm was set)")
	}
	if got := b.result.Values["name"]; got != "hello" {
		t.Errorf("Result.Values[name] = %q, want %q (typed value harvested)", got, "hello")
	}
	// State is cleared and no overlay lingers pending.
	if b.runForm != nil || b.runFormPending {
		t.Errorf("run-form state not cleared after harvest: runForm=%v pending=%v", b.runForm, b.runFormPending)
	}
}

// TestBrowser_RunFormNoFormQuitsImmediately pins that BuildForm returning (nil,
// nil) exits the browser immediately (no overlay) with Result.Values == nil and
// ForceParamForm set per key.
func TestBrowser_RunFormNoFormQuitsImmediately(t *testing.T) {
	for _, tc := range []struct {
		name      string
		drive     func(b *browser) (tea.Cmd, bool)
		wantForce bool
	}{
		{"enter", func(b *browser) (tea.Cmd, bool) { return b.onSelect() }, false},
		{"force", func(b *browser) (tea.Cmd, bool) { return b.onForceForm() }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rec runFormRecord
			b := newRunFormBrowser(t, &rec, runFormConfig{noForm: true})

			cmd, handled := tc.drive(b)
			if !handled {
				t.Fatal("action not handled")
			}
			if b.runForm != nil {
				t.Errorf("no-form path opened an overlay: %v", b.runForm)
			}
			if _, ok := b.PendingOverlay(); ok {
				t.Error("PendingOverlay() = true after no-form quit, want false")
			}
			// The cmd is tea.Quit.
			if cmd == nil {
				t.Fatal("no-form path returned nil cmd, want tea.Quit")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Error("no-form path did not return tea.Quit")
			}
			if b.result.Action != ActionRun {
				t.Errorf("Result.Action = %v, want ActionRun", b.result.Action)
			}
			if b.result.Values != nil {
				t.Errorf("Result.Values = %v, want nil (no in-TUI harvest)", b.result.Values)
			}
			if b.result.ForceParamForm != tc.wantForce {
				t.Errorf("Result.ForceParamForm = %v, want %v", b.result.ForceParamForm, tc.wantForce)
			}
		})
	}
}

// TestBrowser_RunFormEmptyFormQuitsImmediately pins that an empty *ask.Form (a
// non-nil form whose Huh() is nil) is treated as no-form-needed — quit-and-run,
// NOT a trapped cancel-only overlay.
func TestBrowser_RunFormEmptyFormQuitsImmediately(t *testing.T) {
	var rec runFormRecord
	b := newRunFormBrowser(t, &rec, runFormConfig{emptyForm: true})

	cmd, handled := b.onSelect()
	if !handled {
		t.Fatal("onSelect not handled")
	}
	if b.runForm != nil {
		t.Errorf("empty form opened an overlay (trapped): %v", b.runForm)
	}
	if cmd == nil {
		t.Fatal("empty-form path returned nil cmd, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("empty-form path did not return tea.Quit")
	}
	if b.result.Action != ActionRun {
		t.Errorf("Result.Action = %v, want ActionRun", b.result.Action)
	}
	if b.result.Values != nil {
		t.Errorf("Result.Values = %v, want nil", b.result.Values)
	}
}

func TestBrowser_RunFormBuildError(t *testing.T) {
	var rec runFormRecord
	b := newRunFormBrowser(t, &rec, runFormConfig{buildErr: errors.New("cannot build form")})

	cmd, handled := b.onSelect()
	if !handled {
		t.Fatal("onSelect not handled")
	}
	// No overlay opened, no quit.
	if b.runForm != nil {
		t.Errorf("run form opened despite BuildForm error: %v", b.runForm)
	}
	if _, ok := b.PendingOverlay(); ok {
		t.Error("PendingOverlay() = true after BuildForm error, want false")
	}
	if b.result.Action != ActionUnknown {
		t.Errorf("BuildForm error set Result.Action = %v, want ActionUnknown (no quit)", b.result.Action)
	}
	// An error flash was set (the returned cmd is the flash-clear tick).
	if cmd == nil {
		t.Error("BuildForm error returned no flash-clear cmd")
	}
	if b.flash == "" || !strings.HasPrefix(b.flash, "✗") {
		t.Errorf("BuildForm error flash = %q, want ✗-prefixed error", b.flash)
	}
}

// TestBrowser_InspectEnterOpensRunFormOverlay pins the harvest-and-quit contract
// for the inspect→Enter route in ModeRun: Enter while the inspect overlay is open
// must transition the capturing overlay in place to the param form, NOT commit a
// Result and quit to the legacy exit-then-form path.
func TestBrowser_InspectEnterOpensRunFormOverlay(t *testing.T) {
	var rec runFormRecord
	b := newRunFormBrowser(t, &rec, runFormConfig{})
	b.list.Select(1) // db.seed (origIdx 1)

	b.openInspect()
	if b.inspect == nil {
		t.Fatal("openInspect did not open the inspect overlay")
	}

	p := newPumper(t)
	p.pump(b, b.Update(tea.KeyPressMsg{Code: tea.KeyEnter}))

	if b.inspect != nil || b.inspectPending {
		t.Errorf("inspect state not retired after run form opened: inspect=%v pending=%v", b.inspect, b.inspectPending)
	}
	if b.runForm == nil {
		t.Fatal("inspect+Enter did not open the run-form overlay")
	}
	if b.runForm.idx != 1 {
		t.Errorf("run-form idx = %d, want 1 (db.seed, the inspected row)", b.runForm.idx)
	}
	ov, ok := b.PendingOverlay()
	if !ok {
		t.Fatal("PendingOverlay() = false after inspect→run form, want true")
	}
	if !ov.CapturesInput {
		t.Error("run-form overlay CapturesInput = false, want true")
	}
	// No Result committed, no quit — the browser stays in-TUI.
	if b.result.Action != ActionUnknown {
		t.Errorf("inspect+Enter set Result.Action = %v, want ActionUnknown (no exit-then-form)", b.result.Action)
	}
	if rec.harvestCalls != 0 {
		t.Errorf("Harvest called %d times before submit, want 0", rec.harvestCalls)
	}
}

// TestBrowser_InspectEnterRunFormBuildErrorKeepsInspect pins that when the param
// form fails to build from the inspect→Enter route, the inspect overlay is left
// intact (still valid and capturing) and the error surfaces as a status flash.
func TestBrowser_InspectEnterRunFormBuildErrorKeepsInspect(t *testing.T) {
	var rec runFormRecord
	b := newRunFormBrowser(t, &rec, runFormConfig{buildErr: errors.New("boom")})
	b.list.Select(0)

	b.openInspect()
	if b.inspect == nil {
		t.Fatal("openInspect did not open the inspect overlay")
	}

	b.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if b.runForm != nil {
		t.Errorf("run form opened despite BuildForm error: %v", b.runForm)
	}
	if b.inspect == nil {
		t.Fatal("inspect overlay retired on BuildForm error; want it kept")
	}
	if b.flash == "" {
		t.Error("BuildForm error set no status flash")
	}
	if b.result.Action != ActionUnknown {
		t.Errorf("BuildForm error set Result.Action = %v, want ActionUnknown", b.result.Action)
	}
}

// TestBrowser_InspectEnterRunFormNoFormQuits pins that inspect→Enter on a command
// needing no form retires the inspect overlay and quits-and-runs immediately.
func TestBrowser_InspectEnterRunFormNoFormQuits(t *testing.T) {
	var rec runFormRecord
	b := newRunFormBrowser(t, &rec, runFormConfig{noForm: true})
	b.list.Select(0)

	b.openInspect()
	b.PendingOverlay()

	cmd := b.updateInspect(tea.KeyPressMsg{Code: tea.KeyEnter})
	if b.runForm != nil {
		t.Errorf("no-form inspect path opened an overlay: %v", b.runForm)
	}
	if b.inspect != nil || b.inspectPending {
		t.Errorf("inspect state not retired on no-form quit: inspect=%v pending=%v", b.inspect, b.inspectPending)
	}
	if cmd == nil {
		t.Fatal("no-form inspect path returned nil cmd, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("no-form inspect path did not return tea.Quit")
	}
	if b.result.Action != ActionRun {
		t.Errorf("Result.Action = %v, want ActionRun", b.result.Action)
	}
	if b.result.Values != nil {
		t.Errorf("Result.Values = %v, want nil", b.result.Values)
	}
}

// TestBrowser_RunFormTakesRoutingPriorityOverInspect pins that while a run-form
// overlay is open, messages route to updateRunForm (NOT the inspect-clearing
// path): an OverlayClosedMsg clears the run form and does not touch inspect (which
// is nil — the inspect path would nil-panic if it were wrongly reached).
func TestBrowser_RunFormTakesRoutingPriorityOverInspect(t *testing.T) {
	var rec runFormRecord
	b := newRunFormBrowser(t, &rec, runFormConfig{})
	newPumper(t).openRunFormViaSelect(b)
	b.PendingOverlay()
	if b.runForm == nil {
		t.Fatal("run form should be open")
	}
	if b.inspect != nil {
		t.Fatal("inspect must be nil while a run form is open (mutually exclusive)")
	}

	// Routed to updateRunForm (clears run form); must not reach the inspect branch.
	b.Update(tui.OverlayClosedMsg{})
	if b.runForm != nil {
		t.Error("OverlayClosedMsg did not clear the run form (mis-routed)")
	}
}

// TestBrowser_RunFormNilSpecKeepsExitAndRun asserts RunForm == nil in ModeRun
// preserves the legacy commit-and-quit: onSelect commits a Result and returns
// tea.Quit with no overlay and no harvested Values.
func TestBrowser_RunFormNilSpecKeepsExitAndRun(t *testing.T) {
	opts := DefaultOptions()
	opts.Mode = ModeRun // RunForm stays nil
	b := newBrowser("dwe", runFormTestItems(), opts)
	b.Resize(tui.Region{Width: 80, Height: 24})
	b.active = panelList
	b.list.Select(1)

	cmd, handled := b.onSelect()
	if !handled {
		t.Fatal("onSelect not handled")
	}
	if cmd == nil {
		t.Fatal("RunForm == nil onSelect returned nil cmd, want tea.Quit")
	}
	if b.runForm != nil {
		t.Errorf("RunForm == nil should not open an overlay, got %v", b.runForm)
	}
	res := b.Result().(Result)
	if res.Action != ActionRun || res.Idx != 1 {
		t.Errorf("Result = %+v, want Action=ActionRun Idx=1", res)
	}
	if res.Values != nil {
		t.Errorf("Result.Values = %v, want nil (no in-TUI harvest)", res.Values)
	}
}
