package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// buildTestForm builds a raw single-input huh form (tui must not import ask, so
// tests construct huh directly). The bound value pointer is returned so tests
// can assert typed input reaches it. A Description is included so form.View()
// renders at exactly the requested WithWidth (verified: a description-bearing
// group fills its width), making the box-dimension assertions deterministic.
func buildTestForm(value *string) *huh.Form {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Key("value").
				Value(value).
				Title("Value").
				Description("A description long enough to fill the group width for deterministic sizing."),
		),
	)
}

// buildTallTestForm builds a single-group huh form with n inputs so form.View()
// is tall enough to exercise the MaxHeight cap. ShowHelp is disabled to mirror
// the real consumer (ask.Build(..., ShowHelp:false)) and keep the group footer
// empty, so the rendered height math is deterministic.
func buildTallTestForm(n int) *huh.Form {
	fields := make([]huh.Field, n)
	for i := range fields {
		fields[i] = huh.NewInput().
			Key("k").
			Title("Field").
			Description("A description long enough to fill the group width for deterministic sizing.")
	}
	return huh.NewForm(huh.NewGroup(fields...)).WithShowHelp(false)
}

// cmdSliceType is tea.Cmd's reflect type, used to flatten Batch/Sequence
// messages (both are []tea.Cmd underlying) without importing the unexported
// sequenceMsg type.
var cmdSliceType = reflect.TypeFor[tea.Cmd]()

// asCmdSlice reports whether msg is a slice of tea.Cmd (tea.BatchMsg or the
// unexported sequenceMsg) and returns its elements.
func asCmdSlice(msg tea.Msg) ([]tea.Cmd, bool) {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Elem() != cmdSliceType {
		return nil, false
	}
	cmds := make([]tea.Cmd, v.Len())
	for i := range cmds {
		cmds[i], _ = v.Index(i).Interface().(tea.Cmd)
	}
	return cmds, true
}

// execFast runs c and returns its message, but SKIPS it (ok=false) if it does
// not return promptly. huh focuses fields with a cursor-blink command that is a
// tea.Tick — it sleeps ~0.5s before yielding a BlinkMsg, and feeding that back
// produces another blink, an endless slow loop. bubbletea runs those async; a
// synchronous test pump must not. The field-progression cmds that drive
// completion (NextField, nextGroup) return instantly, so a short timeout cleanly
// separates the two.
func execFast(c tea.Cmd) (tea.Msg, bool) {
	ch := make(chan tea.Msg, 1)
	go func() { ch <- c() }()
	select {
	case m := <-ch:
		return m, true
	case <-time.After(50 * time.Millisecond):
		return nil, false // tick/blink — skip, bubbletea would run it async
	}
}

// pump drives cmd's message tree through fo.Update the way bubbletea's event
// loop would: execute the cmd, flatten any Batch/Sequence, feed leaf messages
// back into fo.Update, and recurse on the follow-up cmds. This is what makes the
// asynchronous huh completion (Enter → NextField → nextGroup → StateCompleted)
// observable in a unit test. Slow tick/blink cmds are skipped (see execFast).
// Bounded to avoid an infinite loop on a bug.
func pump(t *testing.T, fo *FormOverlay, cmd tea.Cmd) {
	t.Helper()
	steps := 0
	var run func(c tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		steps++
		if steps > 2000 {
			t.Fatal("pump exceeded step budget (likely a cmd loop)")
		}
		msg, ok := execFast(c)
		if !ok || msg == nil {
			return
		}
		if cmds, ok := asCmdSlice(msg); ok {
			for _, sub := range cmds {
				run(sub)
			}
			return
		}
		run(fo.Update(msg))
	}
	run(cmd)
}

func typeKey(fo *FormOverlay, t *testing.T, r rune) {
	t.Helper()
	pump(t, fo, fo.Update(tea.KeyPressMsg{Code: r, Text: string(r)}))
}

func TestFormOverlayTypedKeysMutateValue(t *testing.T) {
	var val string
	fo := NewFormOverlay(buildTestForm(&val), Region{Width: 100, Height: 24}, FormOverlayOptions{})
	// Focus the input by pumping Init (group.Init focuses the first field).
	pump(t, fo, fo.Init())

	for _, r := range "hi" {
		typeKey(fo, t, r)
	}
	if val != "hi" {
		t.Fatalf("bound value = %q, want %q", val, "hi")
	}
	if fo.State() != huh.StateNormal {
		t.Fatalf("State = %v, want StateNormal while editing", fo.State())
	}
}

func TestFormOverlayAsyncCompletion(t *testing.T) {
	var val string
	fo := NewFormOverlay(buildTestForm(&val), Region{Width: 100, Height: 24}, FormOverlayOptions{})
	pump(t, fo, fo.Init())
	for _, r := range "db.internal" {
		typeKey(fo, t, r)
	}

	// The Enter Update itself must NOT complete the form — completion is
	// asynchronous (Input returns NextField; only the follow-up nextGroup Update
	// sets StateCompleted). This pins the codex finding.
	enterCmd := fo.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if fo.State() != huh.StateNormal {
		t.Fatalf("State after Enter Update = %v, want StateNormal (async completion)", fo.State())
	}

	// Pumping the returned cmds (as bubbletea would) drives completion.
	pump(t, fo, enterCmd)
	if fo.State() != huh.StateCompleted {
		t.Fatalf("State after pumping Enter cmds = %v, want StateCompleted", fo.State())
	}
	if val != "db.internal" {
		t.Fatalf("bound value = %q, want %q", val, "db.internal")
	}
}

func TestFormOverlayUpdateAfterCompletionIsNoOp(t *testing.T) {
	var val string
	fo := NewFormOverlay(buildTestForm(&val), Region{Width: 100, Height: 24}, FormOverlayOptions{})
	pump(t, fo, fo.Init())
	typeKey(fo, t, 'x')
	pump(t, fo, fo.Update(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if fo.State() != huh.StateCompleted {
		t.Fatalf("precondition: State = %v, want StateCompleted", fo.State())
	}

	// Further keys must not change the harvested value or state.
	typeKey(fo, t, 'y')
	if val != "x" {
		t.Fatalf("value after post-completion key = %q, want unchanged %q", val, "x")
	}
	if fo.State() != huh.StateCompleted {
		t.Fatalf("State after post-completion key = %v, want StateCompleted", fo.State())
	}
}

func TestFormOverlayWidthClampAndMaxWidth(t *testing.T) {
	tests := []struct {
		name    string
		body    int
		maxW    int
		wantBox int // box width = form width + border(2) + padding(2)
	}{
		{"wide body clamps to default max", 200, 0, formOverlayMaxWidth + formOverlayHChrome},
		{"body at 100 clamps to default max", 100, 0, formOverlayMaxWidth + formOverlayHChrome},
		{"narrow body drives box below max", 30, 0, 30},
		{"custom max width caps below body", 100, 40, 40 + formOverlayHChrome},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var val string
			fo := NewFormOverlay(buildTestForm(&val), Region{Width: tc.body, Height: 24}, FormOverlayOptions{MaxWidth: tc.maxW})
			if got := fo.Overlay().Width; got != tc.wantBox {
				t.Fatalf("Overlay().Width = %d, want %d", got, tc.wantBox)
			}
		})
	}
}

func TestFormOverlaySwallowsWindowSizeMsg(t *testing.T) {
	var val string
	fo := NewFormOverlay(buildTestForm(&val), Region{Width: 100, Height: 24}, FormOverlayOptions{})
	before := fo.Overlay().Width

	if cmd := fo.Update(tea.WindowSizeMsg{Width: 500, Height: 500}); cmd != nil {
		t.Fatalf("Update(WindowSizeMsg) cmd = %v, want nil (swallowed)", cmd)
	}
	if after := fo.Overlay().Width; after != before {
		t.Fatalf("Overlay().Width changed after WindowSizeMsg: before=%d after=%d", before, after)
	}
}

func TestFormOverlayHintRow(t *testing.T) {
	var val string
	hint := "enter save · esc cancel"

	withHint := NewFormOverlay(buildTestForm(&val), Region{Width: 100, Height: 24}, FormOverlayOptions{Hint: hint})
	if !strings.Contains(withHint.Overlay().Content, "esc cancel") {
		t.Fatalf("hint overlay content missing hint text")
	}

	noHint := NewFormOverlay(buildTestForm(&val), Region{Width: 100, Height: 24}, FormOverlayOptions{})
	if strings.Contains(noHint.Overlay().Content, "esc cancel") {
		t.Fatalf("no-hint overlay unexpectedly contains hint text")
	}
	// The hint row adds one line of height.
	if h1, h0 := withHint.Overlay().Height, noHint.Overlay().Height; h1 <= h0 {
		t.Fatalf("hint overlay height = %d, want > no-hint height %d", h1, h0)
	}
}

func TestFormOverlayResizeReappliesWidth(t *testing.T) {
	var val string
	fo := NewFormOverlay(buildTestForm(&val), Region{Width: 100, Height: 24}, FormOverlayOptions{})
	wide := fo.Overlay().Width

	fo.Resize(Region{Width: 30, Height: 24})
	narrow := fo.Overlay().Width
	if narrow != 30 {
		t.Fatalf("after Resize(30) box width = %d, want 30", narrow)
	}
	if narrow >= wide {
		t.Fatalf("Resize did not narrow the box: wide=%d narrow=%d", wide, narrow)
	}
}

func TestFormOverlayNilFormGuard(t *testing.T) {
	fo := NewFormOverlay(nil, Region{Width: 100, Height: 24}, FormOverlayOptions{Hint: "x"})
	if fo.State() != huh.StateNormal {
		t.Fatalf("nil-form State = %v, want StateNormal", fo.State())
	}
	if cmd := fo.Init(); cmd != nil {
		t.Fatalf("nil-form Init cmd = %v, want nil", cmd)
	}
	if cmd := fo.Update(tea.KeyPressMsg{Code: 'a', Text: "a"}); cmd != nil {
		t.Fatalf("nil-form Update cmd = %v, want nil", cmd)
	}
	// Overlay must render (empty capturing box) without panicking.
	ov := fo.Overlay()
	if !ov.CapturesInput {
		t.Fatalf("nil-form Overlay CapturesInput = false, want true")
	}
	if lipgloss.Width(ov.Content) == 0 {
		t.Fatalf("nil-form Overlay content is empty (expected bordered box)")
	}
}

func TestFormOverlayIsCapturing(t *testing.T) {
	var val string
	fo := NewFormOverlay(buildTestForm(&val), Region{Width: 100, Height: 24}, FormOverlayOptions{})
	if !fo.Overlay().CapturesInput {
		t.Fatalf("Overlay().CapturesInput = false, want true")
	}
}

func TestFormOverlayMaxHeightCapsTallForm(t *testing.T) {
	body := Region{Width: 100, Height: 24}

	uncapped := NewFormOverlay(buildTallTestForm(8), body, FormOverlayOptions{})
	tallHeight := uncapped.Overlay().Height

	const cap = 12
	capped := NewFormOverlay(buildTallTestForm(8), body, FormOverlayOptions{MaxHeight: cap})
	gotHeight := capped.Overlay().Height

	if tallHeight <= cap {
		t.Fatalf("precondition: uncapped tall form height = %d, want > cap %d (form not tall enough)", tallHeight, cap)
	}
	if gotHeight > cap {
		t.Fatalf("capped box height = %d, want ≤ MaxHeight %d", gotHeight, cap)
	}
	if gotHeight >= tallHeight {
		t.Fatalf("capped box height = %d, want < uncapped height %d (cap not engaged)", gotHeight, tallHeight)
	}
}

func TestFormOverlayMaxHeightWithHintFitsBudget(t *testing.T) {
	body := Region{Width: 100, Height: 24}
	const cap = 12
	fo := NewFormOverlay(buildTallTestForm(8), body, FormOverlayOptions{MaxHeight: cap, Hint: "enter save · esc cancel"})
	if got := fo.Overlay().Height; got > cap {
		t.Fatalf("capped box (with hint) height = %d, want ≤ MaxHeight %d (hint must fit in budget)", got, cap)
	}
	if !strings.Contains(fo.Overlay().Content, "esc cancel") {
		t.Fatalf("hint row was shaved from the capped box")
	}
}

func TestFormOverlayMaxHeightDoesNotPadShortForm(t *testing.T) {
	body := Region{Width: 100, Height: 24}
	var val string
	// A short (single-field) form with a large MaxHeight cap must render at its
	// content height, NOT padded up to the cap. Compare against the content-driven
	// (MaxHeight == 0) render.
	contentDriven := NewFormOverlay(buildTestForm(&val), body, FormOverlayOptions{})
	capped := NewFormOverlay(buildTestForm(&val), body, FormOverlayOptions{MaxHeight: 24})

	if cd, cp := contentDriven.Overlay().Height, capped.Overlay().Height; cd != cp {
		t.Fatalf("short form with MaxHeight=24 height = %d, want content-driven height %d (no padding to cap)", cp, cd)
	}
	if h := capped.Overlay().Height; h >= 24 {
		t.Fatalf("short capped form height = %d, want well below cap 24 (no padding)", h)
	}
}

func TestFormOverlayMaxHeightZeroByteIdentical(t *testing.T) {
	body := Region{Width: 100, Height: 24}
	var a, b string
	base := NewFormOverlay(buildTestForm(&a), body, FormOverlayOptions{Hint: "h"})
	zero := NewFormOverlay(buildTestForm(&b), body, FormOverlayOptions{Hint: "h", MaxHeight: 0})
	if base.Overlay().Content != zero.Overlay().Content {
		t.Fatalf("MaxHeight == 0 render differs from the no-MaxHeight render")
	}
}

func TestFormOverlayMaxHeightResizeShrinkThenGrow(t *testing.T) {
	// Regression for the one-way WithHeight trap: a tall form capped in a small
	// body must UN-clamp back to its full content height when Resize grows the
	// budget past the natural height (clamp recomputed from the stored natural
	// height, not a re-measure of the already-capped view).
	small := Region{Width: 100, Height: 12}
	fo := NewFormOverlay(buildTallTestForm(8), small, FormOverlayOptions{MaxHeight: 12})
	capped := fo.Overlay().Height
	if capped > 12 {
		t.Fatalf("precondition: capped height = %d, want ≤ 12", capped)
	}

	// Grow the body well past the natural height. Resize must re-derive the height
	// budget from the new body (no manual opts mutation — the production path).
	fo.Resize(Region{Width: 100, Height: 240})
	grown := fo.Overlay().Height
	if grown <= capped {
		t.Fatalf("after grow-Resize height = %d, want > capped height %d (un-clamp failed)", grown, capped)
	}

	// The un-clamped height must match a freshly built uncapped overlay's height.
	reference := NewFormOverlay(buildTallTestForm(8), Region{Width: 100, Height: 240}, FormOverlayOptions{}).Overlay().Height
	if grown != reference {
		t.Fatalf("un-clamped height = %d, want full content height %d", grown, reference)
	}
}

func TestFormOverlayMaxHeightResizeShrinkReclamps(t *testing.T) {
	// A tall form opened in a large body renders uncapped; a Resize that shrinks
	// the body below the natural height MUST re-clamp so the box fits the smaller
	// body (else clampOverlay would lossily truncate the hint / submit row). The
	// budget tracks the body height with no manual opts mutation (production path).
	large := Region{Width: 100, Height: 240}
	fo := NewFormOverlay(buildTallTestForm(8), large, FormOverlayOptions{MaxHeight: 240})
	uncapped := fo.Overlay().Height

	fo.Resize(Region{Width: 100, Height: 12})
	shrunk := fo.Overlay().Height
	if shrunk >= uncapped {
		t.Fatalf("after shrink-Resize height = %d, want < uncapped height %d (re-clamp failed)", shrunk, uncapped)
	}
	if shrunk > 12 {
		t.Fatalf("after shrink-Resize height = %d, want ≤ new body height 12", shrunk)
	}
}

func TestFormOverlayMaxHeightNilFormGuard(t *testing.T) {
	fo := NewFormOverlay(nil, Region{Width: 100, Height: 24}, FormOverlayOptions{MaxHeight: 12, Hint: "x"})
	if fo.State() != huh.StateNormal {
		t.Fatalf("nil-form State = %v, want StateNormal", fo.State())
	}
	// Must not panic on Overlay/Resize with MaxHeight set and a nil form.
	if !fo.Overlay().CapturesInput {
		t.Fatalf("nil-form Overlay CapturesInput = false, want true")
	}
	fo.Resize(Region{Width: 80, Height: 40})
	if !fo.Overlay().CapturesInput {
		t.Fatalf("nil-form Overlay after Resize CapturesInput = false, want true")
	}
}
