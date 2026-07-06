package cmdbrowser

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// editFormHint is the footer hint row rendered inside the edit form overlay box.
// It is the single authoritative key hint (huh's own help line is suppressed via
// ShowHelp:false at BuildForm time — its ctrl+c=quit hint would be wrong under
// the Frame, where ctrl+c hard-quits the whole TUI). Hardcoded English, matching
// Stage 6's decision that form chrome i18n is out of scope.
const editFormHint = "enter save · esc cancel"

// statusFlashDuration is how long a transient status-line confirmation (a commit
// ✓ / ✗ or a BuildForm error) stays visible before the clear tick removes it.
// Mirrors the docstui status-flash pattern.
const statusFlashDuration = 2 * time.Second

// statusFlashClearMsg clears the status flash whose generation matches the
// current one (see browser.flashGen). A stale tick from an earlier flash carries
// an older gen and is ignored, so a newer flash is never wiped by an old timer.
type statusFlashClearMsg struct{ gen int }

// editState owns the form overlay shown while the user edits a single row's
// value in ModeEdit (the vars browser). The browser holds a *editState; nil
// means no edit is in progress. It mirrors inspectState: fo is the embeddable
// form host, idx remembers which item is being edited so Commit and the in-place
// row replacement target the right row, and form is kept so Result() can harvest
// the bound values on completion.
type editState struct {
	fo    *tui.FormOverlay
	form  *ask.Form
	idx   int // index into browser.items
	token int // CloseToken tagging this overlay, echoed in its CloseOverlayMsg
}

// setStatusFlash shows a transient confirmation that takes over StatusContext()
// while set and returns the Cmd that clears it after statusFlashDuration. Each
// flash bumps flashGen so its clear tick can be matched (and stale ticks
// ignored). An empty text still schedules a clear so a leftover flash is wiped.
//
// Whitespace (including newlines) is collapsed to single spaces: the status line
// is a fixed 1-row region (Frame.renderStatusLine clamps width, not height), so
// a multi-line commit/build error — e.g. a yaml.v3 "unmarshal errors:\n  line N:
// …" or a strict-root rejection with its vars: hint line — would otherwise push
// the frame layout out of alignment. varEditFlash already collapses success
// text; doing it here covers every flash (build/commit errors included).
func (b *browser) setStatusFlash(text string) tea.Cmd {
	b.flash = strings.Join(strings.Fields(text), " ")
	b.flashGen++
	gen := b.flashGen
	return tea.Tick(statusFlashDuration, func(time.Time) tea.Msg {
		return statusFlashClearMsg{gen: gen}
	})
}

// flashError formats a commit / build error as a single-line status flash.
// Success flashes are built by the caller's Commit closure (they carry the
// value), so only the error prefix lives here.
func flashError(msg string) string { return "✗ " + msg }

// openEdit builds the edit form for items[idx] via the caller's EditSpec and
// opens it as a capturing overlay. A BuildForm error (or a nil form) shows an
// error flash and opens nothing. Returns the embedded form's Init cmd so the
// Frame drives it (blink, first-field focus); the overlay itself is published
// via PendingOverlay on the next drain.
func (b *browser) openEdit(idx int) tea.Cmd {
	form, err := b.opts.Edit.BuildForm(idx)
	if err != nil {
		return b.setStatusFlash(flashError(err.Error()))
	}
	if form == nil {
		return b.setStatusFlash(flashError("edit form is unavailable"))
	}
	b.editTokenSeq++
	token := b.editTokenSeq
	fo := tui.NewFormOverlay(form.Huh(), b.body, tui.FormOverlayOptions{
		Hint:       editFormHint,
		CloseToken: token,
	})
	b.edit = &editState{fo: fo, form: form, idx: idx, token: token}
	b.editPending = true
	return fo.Init()
}

// updateEdit drives the edit form overlay while it is open. It forwards every
// message (typed keys AND async blink/next-group ticks) to the embedded form,
// re-marks the overlay pending so the Frame republishes the fresh snapshot, then
// polls the form State. Completion commits synchronously and closes; abort/esc
// cancels. It is called for EVERY message while b.edit != nil, so the flash-clear
// tick is peeled off before this in Update (it must never reach the form).
func (b *browser) updateEdit(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(tui.OverlayClosedMsg); ok {
		// Esc / click-outside popped the overlay Frame-side (cancel). Clear the
		// edit state so a later unmatched raw key cannot re-mark it pending and
		// resurrect the closed form (mirrors the inspect lingering-state guard).
		b.edit = nil
		b.editPending = false
		return nil
	}
	cmd := b.edit.fo.Update(msg)
	b.editPending = true
	switch b.edit.fo.State() {
	case huh.StateCompleted:
		return b.commitEdit()
	case huh.StateAborted:
		// Unreachable in practice (ctrl+c never reaches the form — the Frame
		// reserves it), but handled defensively as a cancel.
		token := b.edit.token
		b.edit = nil
		b.editPending = false
		return requestCloseOverlay(token)
	}
	return cmd
}

// commitEdit runs the caller's Commit closure for the edited row, replaces the
// item in place on success (a var edit never adds/removes leaves, so the tree
// shape and cursor stay put), sets a status flash, clears the edit state, and
// returns a CloseOverlayMsg cmd batched with the flash-clear tick. A Commit error
// flashes the error and still closes the overlay. The flash-clear tick fires
// while b.edit is already nil, so it lands on the normal Update path.
func (b *browser) commitEdit() tea.Cmd {
	idx := b.edit.idx
	token := b.edit.token
	res := b.edit.form.Result()
	b.edit = nil
	b.editPending = false

	outcome, err := b.opts.Edit.Commit(idx, res)
	var flashCmd tea.Cmd
	if err != nil {
		flashCmd = b.setStatusFlash(flashError(err.Error()))
	} else {
		if idx >= 0 && idx < len(b.items) {
			b.items[idx] = outcome.Item
			b.refreshList()
		}
		flashCmd = b.setStatusFlash(outcome.Flash)
	}
	return tea.Batch(requestCloseOverlay(token), flashCmd)
}

// requestCloseOverlay returns a command that asks the Frame to pop the top
// overlay WITHOUT an OverlayClosedMsg echo (the plugin already cleared its edit
// state — a plugin-initiated close). Mirrors requestFocus. token targets the
// specific edit overlay (its Overlay.CloseToken): the Frame ignores the request
// if a different overlay reached the top before this deferred cmd was processed,
// so a stale close never pops the wrong modal.
func requestCloseOverlay(token int) tea.Cmd {
	return func() tea.Msg { return tui.CloseOverlayMsg{Token: token} }
}
