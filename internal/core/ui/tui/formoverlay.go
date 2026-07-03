package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// formOverlayMaxWidth caps the inner form width so a wide terminal does not
// stretch a single-field prompt across the whole body. Tuned for the vars edit
// form (one input + a layered description); wider forms clamp here and let huh
// wrap.
const formOverlayMaxWidth = 72

// formOverlayHChrome is the horizontal cell cost of the overlay box around the
// form: a rounded border (1 cell each side) plus Padding(0,1) (1 cell each
// side). Mirrors cmdbrowser's inspectBoxHChrome. The embedded huh form is sized
// to body width minus this so the bordered box never overflows the body region.
const formOverlayHChrome = 4

// formOverlayMinWidth floors the computed form width so a very narrow body still
// yields a usable (if cramped) input rather than a zero-width form. The narrow-
// terminal fallback (< minBrowserWidth) means the framework path never runs this
// narrow in practice; this is a safety net only.
const formOverlayMinWidth = 10

// FormOverlayOptions configures a [FormOverlay].
type FormOverlayOptions struct {
	// MaxWidth caps the inner form width. 0 uses formOverlayMaxWidth.
	MaxWidth int
	// Hint is the footer hint row rendered below the form inside the box (e.g.
	// "enter save · esc cancel"). "" omits the hint row entirely.
	Hint string
	// CloseToken is stamped onto every [Overlay] this wrapper produces (including
	// the ReplaceTop-refreshed snapshots from blink ticks / resizes), so the
	// plugin can target a [CloseOverlayMsg] at this specific overlay and the Frame
	// ignores a stale close after the overlay was dismissed. 0 leaves the overlay
	// untargeted.
	CloseToken int
}

// FormOverlay hosts a [huh.Form] as a capturing-overlay child model inside the
// [Frame], so a plugin can run a form WITHOUT leaving the TUI (the milestone's
// second form-host mode, "in-TUI overlay, edit-and-stay"). The plugin builds a
// form (typically via ask.Build(...).Huh()) and forwards Init/Update to this
// wrapper, rendering [FormOverlay.Overlay] as a capturing [Overlay]; it polls
// [FormOverlay.State] after each Update to detect submit/abort.
//
// Embedding contract (verified against huh/v2 v2.0.3):
//
//   - SubmitCmd/CancelCmd are assigned only inside RunWithContext and are nil
//     when embedded, so completion CANNOT be detected from a returned cmd — the
//     host MUST poll [FormOverlay.State] after every forwarded message.
//   - Completion is ASYNCHRONOUS: the Enter Update leaves State == StateNormal
//     and returns a follow-up cmd (NextField → nextGroupMsg); only that later
//     Update sets StateCompleted. The host must therefore return the cmds this
//     wrapper hands back to bubbletea and re-poll State after each — tests must
//     pump the returned cmds before asserting completion.
//   - Sizing is HOST-OWNED. huh's Init returns tea.RequestWindowSize and its
//     Update auto-sizes group width/height from a tea.WindowSizeMsg while its own
//     width is 0. [FormOverlay.Update] therefore SWALLOWS tea.WindowSizeMsg — the
//     wrapper sizes the form exclusively via WithWidth at construction and in
//     [FormOverlay.Resize]; height stays content-driven.
//   - bubbles/v2 textinput uses a virtual cursor rendered inline in View(); no
//     tea.View.Cursor plumbing is needed. Cursor-blink arrives as async (non-key)
//     messages, which is why the Frame's capturing-aware drainOverlay (ReplaceTop,
//     not Push) is load-bearing for this overlay.
//
// Height caveat (design decision 1): the form height is content-driven and NOT
// clamped by this wrapper. If a form is taller than the body region, Composite's
// clampOverlay truncates it (MaxHeight) with no scroll — the submit control could
// be clipped. This is acceptable for the single-field vars form; a future taller
// consumer must add WithHeight/scroll support deliberately.
type FormOverlay struct {
	form  *huh.Form
	width int
	opts  FormOverlayOptions
}

// NewFormOverlay wraps form as a capturing overlay sized to body. form must be
// non-nil; a nil form yields a wrapper whose Update/State/Overlay are inert
// no-ops (State reports StateNormal, Overlay is empty) so a build error upstream
// never panics the Frame. body is the inner body region the overlay composites
// over — the form width is min(body.Width − chrome, MaxWidth).
func NewFormOverlay(form *huh.Form, body Region, opts FormOverlayOptions) *FormOverlay {
	fo := &FormOverlay{form: form, opts: opts}
	fo.applyWidth(body)
	return fo
}

// applyWidth computes the inner form width from body and applies it to the form.
func (fo *FormOverlay) applyWidth(body Region) {
	maxW := fo.opts.MaxWidth
	if maxW <= 0 {
		maxW = formOverlayMaxWidth
	}
	w := min(body.Width-formOverlayHChrome, maxW)
	w = max(w, formOverlayMinWidth)
	fo.width = w
	if fo.form != nil {
		fo.form.WithWidth(w)
	}
}

// Init returns the embedded form's startup command. The host batches it with its
// own work. huh's Init includes tea.RequestWindowSize, which is harmless: the
// Frame never forwards WindowSizeMsg into overlays, and Update swallows it
// anyway.
func (fo *FormOverlay) Init() tea.Cmd {
	if fo.form == nil {
		return nil
	}
	return fo.form.Init()
}

// Update forwards msg to the embedded form and returns its follow-up cmd. It
// SWALLOWS tea.WindowSizeMsg (sizing is host-owned — see the type doc) and is a
// no-op once the form has completed/aborted (huh's own Update is a no-op then
// too, but this avoids re-asserting a completed model). The host must poll
// [FormOverlay.State] after calling this.
func (fo *FormOverlay) Update(msg tea.Msg) tea.Cmd {
	if fo.form == nil {
		return nil
	}
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		return nil
	}
	model, cmd := fo.form.Update(msg)
	if f, ok := model.(*huh.Form); ok {
		fo.form = f
	}
	return cmd
}

// State reports the embedded form's completion state. Poll it after every
// forwarded Update: StateCompleted means the user submitted (harvest the
// result), StateAborted means they cancelled. A nil form reports StateNormal.
func (fo *FormOverlay) State() huh.FormState {
	if fo.form == nil {
		return huh.StateNormal
	}
	return fo.form.State
}

// Resize re-applies the form width for a new body region. The host calls it on
// terminal resize (and re-marks its pending overlay so the Frame re-renders).
func (fo *FormOverlay) Resize(body Region) {
	fo.applyWidth(body)
}

// Overlay renders the embedded form in a rounded-border box (Padding(0,1),
// mirroring the inspect overlay) with an optional footer hint row, and returns
// it as a capturing [Overlay]. Width/Height are measured from the rendered box
// so centring uses real post-border dimensions. The form view is the sole body;
// a nil form renders an empty capturing overlay.
func (fo *FormOverlay) Overlay() Overlay {
	body := ""
	if fo.form != nil {
		body = fo.form.View()
	}
	if fo.opts.Hint != "" {
		hint := lipgloss.NewStyle().
			Foreground(lipgloss.Color(styles.ColorMuted())).
			Render(fo.opts.Hint)
		body = lipgloss.JoinVertical(lipgloss.Left, body, hint)
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(styles.ColorBorder())).
		Padding(0, 1).
		Render(body)
	return Overlay{
		Content:       box,
		Width:         lipgloss.Width(box),
		Height:        lipgloss.Height(box),
		CapturesInput: true,
		CloseToken:    fo.opts.CloseToken,
	}
}
