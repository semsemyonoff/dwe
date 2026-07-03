package cmdbrowser

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// editTestItems are root-level (no dot → no group) so the tree's root focus
// shows them directly in the list, giving a selectable row to edit without
// expanding a group first.
func editTestItems() []Item {
	return []Item{
		{ID: "db.host", Description: "old-host", Type: "string"},
		{ID: "db.port", Description: "5432", Type: "int"},
	}
}

// falsePtr returns a pointer to false (ShowHelp:false at BuildForm time).
func falsePtr() *bool { b := false; return &b }

// commitRecord captures what the EditSpec.Commit closure saw, so tests can
// assert the plugin harvested the right index and result.
type commitRecord struct {
	calls int
	idx   int
	value string
}

// newEditBrowser builds a ModeEdit browser over editTestItems with a working
// EditSpec: BuildForm makes a single-input form defaulting to the row's current
// value, and Commit records the call and returns a replacement row + flash. The
// commitRecord and any injected errors let each test steer the outcome. The
// browser is sized to a real body and focused on the list so onSelect resolves a
// row.
func newEditBrowser(t *testing.T, rec *commitRecord, buildErr, commitErr error) *browser {
	t.Helper()
	items := editTestItems()
	opts := DefaultOptions()
	opts.Mode = ModeEdit
	opts.Edit = &EditSpec{
		BuildForm: func(idx int) (*ask.Form, error) {
			if buildErr != nil {
				return nil, buildErr
			}
			return ask.Build("edit "+items[idx].ID, []ask.Field{{
				Key:     "value",
				Kind:    ask.FieldInput,
				Title:   items[idx].ID,
				Default: items[idx].Description,
			}}, ask.RunOptions{ShowHelp: falsePtr()})
		},
		Commit: func(idx int, res ask.Result) (CommitOutcome, error) {
			rec.calls++
			rec.idx = idx
			rec.value = res.String("value")
			if commitErr != nil {
				return CommitOutcome{}, commitErr
			}
			return CommitOutcome{
				Item:  Item{ID: items[idx].ID, Description: res.String("value"), Type: "string"},
				Flash: "✓ " + items[idx].ID + " = " + res.String("value"),
			}, nil
		},
	}
	b := newBrowser("vars", items, opts)
	b.Resize(tui.Region{Width: 80, Height: 24})
	b.active = panelList
	return b
}

var cmdSliceType = reflect.TypeFor[tea.Cmd]()

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

// pumper drives huh's async cmd tree through the browser the way bubbletea's
// event loop would, WITHOUT leaking goroutines past the test. huh's cursor-blink
// tick is a ~0.5s time.Tick that would loop forever in a synchronous pump, so
// execFast runs each cmd in a goroutine and skips it on a short timeout — but it
// tracks the abandoned goroutine in wg so newPumper's t.Cleanup can wait for it
// to self-terminate (the blink fires once, sends to the buffered channel, and
// exits). This keeps the package's goleak.VerifyTestMain green.
type pumper struct {
	t  *testing.T
	wg sync.WaitGroup
}

func newPumper(t *testing.T) *pumper {
	t.Helper()
	p := &pumper{t: t}
	t.Cleanup(p.wg.Wait)
	return p
}

// execFast runs c and returns its message, SKIPPING it (ok=false) if it does not
// return within 50ms. The field-progression cmds (NextField, nextGroup) that
// drive completion return instantly; the slow blink tick is the one that times
// out — its goroutine is tracked so cleanup can await it.
func (p *pumper) execFast(c tea.Cmd) (tea.Msg, bool) {
	if c == nil {
		return nil, false
	}
	ch := make(chan tea.Msg, 1)
	p.wg.Go(func() { ch <- c() })
	select {
	case m := <-ch:
		return m, true
	case <-time.After(50 * time.Millisecond):
		return nil, false
	}
}

// pump drives cmd's message tree through b.Update: execute the cmd, flatten any
// Batch/Sequence, feed leaf messages back into b.Update, recurse on the
// follow-ups. This makes the asynchronous huh completion (Enter → NextField →
// nextGroup → StateCompleted → commit) observable in a unit test.
func (p *pumper) pump(b *browser, cmd tea.Cmd) {
	p.t.Helper()
	steps := 0
	var run func(c tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		steps++
		if steps > 2000 {
			p.t.Fatal("pump exceeded step budget (likely a cmd loop)")
		}
		msg, ok := p.execFast(c)
		if !ok || msg == nil {
			return
		}
		if cmds, ok := asCmdSlice(msg); ok {
			for _, sub := range cmds {
				run(sub)
			}
			return
		}
		run(b.Update(msg))
	}
	run(cmd)
}

// openEditViaSelect drives Enter through onSelect (as HandleAction would) and
// pumps the returned form Init cmd so the embedded input is focused, matching
// the live path. Returns after the overlay is open.
func (p *pumper) openEditViaSelect(b *browser) {
	p.t.Helper()
	cmd, handled := b.onSelect()
	if !handled {
		p.t.Fatal("onSelect not handled")
	}
	p.pump(b, cmd)
}

func TestBrowser_EditOpensCapturingOverlay(t *testing.T) {
	var rec commitRecord
	b := newEditBrowser(t, &rec, nil, nil)

	if _, ok := b.PendingOverlay(); ok {
		t.Fatal("PendingOverlay() = true before edit opens, want false")
	}

	newPumper(t).openEditViaSelect(b)
	if b.edit == nil {
		t.Fatal("onSelect did not open the edit overlay")
	}
	if b.edit.idx != 0 {
		t.Errorf("edit idx = %d, want 0 (db.host)", b.edit.idx)
	}

	ov, ok := b.PendingOverlay()
	if !ok {
		t.Fatal("PendingOverlay() = false after openEdit, want true")
	}
	if !ov.CapturesInput {
		t.Error("edit overlay CapturesInput = false, want true")
	}
	if ov.Width <= 0 || ov.Height <= 0 {
		t.Errorf("overlay dims = %dx%d, want positive", ov.Width, ov.Height)
	}
	// No double-push: a follow-up drain without a republish yields nothing.
	if _, ok := b.PendingOverlay(); ok {
		t.Error("PendingOverlay() = true on second drain, want false (no double-push)")
	}
}

func TestBrowser_EditClosedByOverlayClosedMsg(t *testing.T) {
	var rec commitRecord
	b := newEditBrowser(t, &rec, nil, nil)
	newPumper(t).openEditViaSelect(b)
	b.PendingOverlay()
	if b.edit == nil {
		t.Fatal("edit should be open before close")
	}

	// Frame pops the capturing overlay on esc and forwards OverlayClosedMsg.
	if cmd := b.Update(tui.OverlayClosedMsg{}); cmd != nil {
		t.Errorf("OverlayClosedMsg should not return a command, got %v", cmd)
	}
	if b.edit != nil || b.editPending {
		t.Fatalf("edit=%v pending=%v after close, want nil/false", b.edit, b.editPending)
	}
	if rec.calls != 0 {
		t.Errorf("Commit called %d times on esc cancel, want 0", rec.calls)
	}

	// A later unmatched raw key must NOT resurrect the closed overlay.
	b.Update(tea.KeyPressMsg{Code: 'x'})
	if _, ok := b.PendingOverlay(); ok {
		t.Error("unmatched key after close re-marked the overlay pending; closed edit resurrected")
	}
}

func TestBrowser_EditCommitSuccess(t *testing.T) {
	var rec commitRecord
	b := newEditBrowser(t, &rec, nil, nil)
	b.list.Select(1) // db.port (origIdx 1)
	p := newPumper(t)
	p.openEditViaSelect(b)
	b.PendingOverlay()

	// Type onto the default value, then submit. Completion is asynchronous —
	// pump the cmds so the follow-up nextGroup msg drives StateCompleted → commit.
	for _, r := range "9999" {
		p.pump(b, b.Update(tea.KeyPressMsg{Code: r, Text: string(r)}))
	}
	p.pump(b, b.Update(tea.KeyPressMsg{Code: tea.KeyEnter}))

	if rec.calls != 1 {
		t.Fatalf("Commit calls = %d, want 1", rec.calls)
	}
	if rec.idx != 1 {
		t.Errorf("Commit idx = %d, want 1 (db.port)", rec.idx)
	}
	// The edit state is cleared and no overlay lingers pending.
	if b.edit != nil || b.editPending {
		t.Errorf("edit state not cleared after commit: edit=%v pending=%v", b.edit, b.editPending)
	}
	// The row is replaced in place with the committed value.
	if got := b.items[1].Description; got != rec.value {
		t.Errorf("items[1].Description = %q, want committed value %q", got, rec.value)
	}
	// The list reflects the new value.
	if li, ok := b.list.SelectedItem().(listItem); ok && li.desc != rec.value {
		t.Errorf("list row desc = %q, want %q", li.desc, rec.value)
	}
	// The status flash carries the success confirmation.
	if b.flash == "" {
		t.Error("commit success set no status flash")
	}
	if b.StatusContext() != b.flash {
		t.Errorf("StatusContext() = %q, want flash %q", b.StatusContext(), b.flash)
	}
}

// TestBrowser_EditCommitReturnsCloseOverlayCmd pins that a successful commit
// returns a CloseOverlayMsg-bearing command (the plugin-initiated close).
func TestBrowser_EditCommitReturnsCloseOverlayCmd(t *testing.T) {
	var rec commitRecord
	b := newEditBrowser(t, &rec, nil, nil)
	p := newPumper(t)
	p.openEditViaSelect(b)
	b.PendingOverlay()

	// Drive Enter and capture the terminal command tree. Completion lands on the
	// follow-up async msg, so we walk the cmd tree looking for CloseOverlayMsg.
	var sawClose bool
	var walk func(c tea.Cmd)
	walk = func(c tea.Cmd) {
		msg, ok := p.execFast(c)
		if !ok || msg == nil {
			return
		}
		if _, isClose := msg.(tui.CloseOverlayMsg); isClose {
			sawClose = true
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

	if rec.calls != 1 {
		t.Fatalf("Commit calls = %d, want 1", rec.calls)
	}
	if !sawClose {
		t.Error("successful commit did not return a CloseOverlayMsg command")
	}
}

func TestBrowser_EditCommitError(t *testing.T) {
	var rec commitRecord
	commitErr := errors.New("lock held")
	b := newEditBrowser(t, &rec, nil, commitErr)
	p := newPumper(t)
	p.openEditViaSelect(b)
	b.PendingOverlay()

	before := b.items[0].Description
	p.pump(b, b.Update(tea.KeyPressMsg{Code: tea.KeyEnter}))

	if rec.calls != 1 {
		t.Fatalf("Commit calls = %d, want 1", rec.calls)
	}
	// The row is NOT replaced on a commit error.
	if b.items[0].Description != before {
		t.Errorf("items[0].Description = %q, want unchanged %q", b.items[0].Description, before)
	}
	// The flash carries the error, prefixed with ✗.
	if b.flash == "" || b.flash[:len("✗")] != "✗" {
		t.Errorf("commit error flash = %q, want ✗-prefixed error", b.flash)
	}
	// Edit state cleared regardless.
	if b.edit != nil || b.editPending {
		t.Errorf("edit state not cleared after commit error: edit=%v pending=%v", b.edit, b.editPending)
	}
}

func TestBrowser_EditBuildFormError(t *testing.T) {
	var rec commitRecord
	buildErr := errors.New("cannot build form")
	b := newEditBrowser(t, &rec, buildErr, nil)

	cmd, handled := b.onSelect()
	if !handled {
		t.Fatal("onSelect not handled")
	}
	// No overlay opened.
	if b.edit != nil {
		t.Errorf("edit opened despite BuildForm error: %v", b.edit)
	}
	if _, ok := b.PendingOverlay(); ok {
		t.Error("PendingOverlay() = true after BuildForm error, want false")
	}
	// An error flash was set (the returned cmd is the flash-clear tick).
	if cmd == nil {
		t.Error("BuildForm error returned no flash-clear cmd")
	}
	if b.flash == "" || b.flash[:len("✗")] != "✗" {
		t.Errorf("BuildForm error flash = %q, want ✗-prefixed error", b.flash)
	}
}

// TestBrowser_EditFlashClearGenerationGated asserts a stale clear tick (an older
// generation) is ignored, while the current-generation tick clears the flash.
func TestBrowser_EditFlashClearGenerationGated(t *testing.T) {
	var rec commitRecord
	b := newEditBrowser(t, &rec, nil, nil)

	// Set two flashes; the first flash's clear tick carries the stale gen.
	b.setStatusFlash("first")
	staleGen := b.flashGen
	b.setStatusFlash("second")
	if b.flash != "second" {
		t.Fatalf("flash = %q, want %q", b.flash, "second")
	}

	// A stale clear tick (older gen) must NOT wipe the newer flash.
	b.Update(statusFlashClearMsg{gen: staleGen})
	if b.flash != "second" {
		t.Errorf("stale clear tick wiped the flash: %q, want %q", b.flash, "second")
	}

	// The current-generation clear tick clears it.
	b.Update(statusFlashClearMsg{gen: b.flashGen})
	if b.flash != "" {
		t.Errorf("current clear tick did not clear the flash: %q", b.flash)
	}
}

// TestBrowser_EditNilSpecKeepsExitAndReturn asserts Edit == nil in ModeEdit
// preserves the legacy commit-and-quit: onSelect commits a Result and returns
// tea.Quit, no overlay.
func TestBrowser_EditNilSpecKeepsExitAndReturn(t *testing.T) {
	opts := DefaultOptions()
	opts.Mode = ModeEdit // Edit stays nil
	b := newBrowser("vars", editTestItems(), opts)
	b.Resize(tui.Region{Width: 80, Height: 24})
	b.active = panelList
	b.list.Select(1)

	cmd, handled := b.onSelect()
	if !handled {
		t.Fatal("onSelect not handled")
	}
	if cmd == nil {
		t.Fatal("Edit == nil onSelect returned nil cmd, want tea.Quit")
	}
	if b.edit != nil {
		t.Errorf("Edit == nil should not open an overlay, got %v", b.edit)
	}
	res := b.Result().(Result)
	if res.Action != ActionEdit || res.Idx != 1 {
		t.Errorf("Result = %+v, want Action=ActionEdit Idx=1", res)
	}
}
