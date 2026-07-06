package cmdbrowser

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
)

// runFormHint is the footer hint row rendered inside the param-form overlay box.
// Like editFormHint it is the single authoritative key hint (huh's own help line
// is suppressed via ShowHelp:false at BuildForm time — its ctrl+c=quit hint would
// be wrong under the Frame, where ctrl+c hard-quits the whole TUI). Hardcoded
// English, matching Stage 6's decision that form chrome i18n is out of scope.
const runFormHint = "enter run · esc cancel"

// runFormState owns the param-form overlay shown while the user fills a command's
// parameters in ModeRun (the command browser). The browser holds a *runFormState;
// nil means no param form is in progress. It mirrors editState: fo is the
// embeddable form host, idx remembers which item's params are being collected so
// the harvest targets the right command, force records whether the form was
// opened via the force-form key (`e`) so Result.ForceParamForm is set correctly,
// and form is kept so Result() can harvest the bound values on completion.
type runFormState struct {
	fo    *tui.FormOverlay
	form  *ask.Form
	idx   int
	force bool
	token int // CloseToken tagging this overlay, echoed in its CloseOverlayMsg
}

// openRunForm builds the param form for items[idx] via the caller's RunFormSpec
// and opens it as a capturing overlay. A BuildForm error shows an error flash and
// opens nothing. A nil form — OR an empty ask.Form whose Huh() is nil (ask.Build
// returns &Form{empty:true} with a nil huh form when given zero fields; hosting it
// would be an inert cancel-only overlay that never completes, trapping the user)
// — means "no form needed": the browser commits Result{Action: ActionRun} with NO
// Values and quits, so the orchestrator recomputes the identical prefilled from
// the same --set values and runs the command exactly as the exit-and-run path
// does today. Otherwise the form is wrapped in a [tui.FormOverlay] and published
// via PendingOverlay on the next drain.
//
// MaxHeight is derived from the body region the overlay composites over, so a
// multi-field command form scrolls inside huh's group viewport instead of
// overflowing the body (the vars openEdit passes 0 → content-driven, unchanged).
func (b *browser) openRunForm(idx int, force bool) tea.Cmd {
	form, err := b.opts.RunForm.BuildForm(idx, force)
	if err != nil {
		return b.setStatusFlash(flashError(err.Error()))
	}
	if form == nil || form.Huh() == nil {
		b.result = Result{
			Idx:            idx,
			Action:         actionForMode(b.opts.Mode),
			SkipConfirm:    b.skipConfirm,
			ForceParamForm: force,
		}
		return tea.Quit
	}
	b.editTokenSeq++
	token := b.editTokenSeq
	fo := tui.NewFormOverlay(form.Huh(), b.body, tui.FormOverlayOptions{
		Hint:       runFormHint,
		CloseToken: token,
		MaxHeight:  b.body.Height,
	})
	b.runForm = &runFormState{fo: fo, form: form, idx: idx, force: force, token: token}
	b.runFormPending = true
	return fo.Init()
}

// updateRunForm drives the param-form overlay while it is open. It forwards every
// message (typed keys AND async blink/next-group ticks) to the embedded form,
// re-marks the overlay pending so the Frame republishes the fresh snapshot, then
// polls the form State. Completion harvests + quits; abort/esc cancels back into
// the browser (no command runs). It is called for EVERY message while
// b.runForm != nil, so the flash-clear tick is peeled off before this in Update
// (it must never reach the form).
func (b *browser) updateRunForm(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(tui.OverlayClosedMsg); ok {
		// Esc / click-outside popped the overlay Frame-side (cancel). Clear the
		// run-form state so a later unmatched raw key cannot re-mark it pending and
		// resurrect the closed form; the browser stays open and no command runs.
		b.runForm = nil
		b.runFormPending = false
		return nil
	}
	cmd := b.runForm.fo.Update(msg)
	b.runFormPending = true
	switch b.runForm.fo.State() {
	case huh.StateCompleted:
		return b.finishRunForm()
	case huh.StateAborted:
		// Unreachable in practice (ctrl+c never reaches the form — the Frame
		// reserves it), but handled defensively as a cancel.
		token := b.runForm.token
		b.runForm = nil
		b.runFormPending = false
		return requestCloseOverlay(token)
	}
	return cmd
}

// finishRunForm runs on StateCompleted: it harvests the submitted params via the
// caller's Harvest closure into Result.Values, commits the ActionRun Result
// (carrying idx, the current skip-confirm flag, and the force flag), clears the
// run-form state, and returns a CloseOverlayMsg batched with tea.Quit so the
// overlay pops and the browser exits — the command runs after teardown.
func (b *browser) finishRunForm() tea.Cmd {
	idx := b.runForm.idx
	force := b.runForm.force
	token := b.runForm.token
	res := b.runForm.form.Result()
	b.runForm = nil
	b.runFormPending = false

	b.result = Result{
		Idx:            idx,
		Action:         actionForMode(b.opts.Mode),
		SkipConfirm:    b.skipConfirm,
		ForceParamForm: force,
		Values:         b.opts.RunForm.Harvest(idx, res),
	}
	return tea.Batch(requestCloseOverlay(token), tea.Quit)
}
