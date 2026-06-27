// Package cmdbrowser implements the interactive two-panel TUI used by
// `dwe commands` when no exact command ID is supplied. It is a sibling of
// internal/core/ui — callers import it directly. A facade in internal/core/ui would
// form a cycle (ui → cmdbrowser → ui).
package cmdbrowser

import (
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// Action enumerates intents returned by the browser. ActionUnknown sits at
// iota 0 so a zero-value Result is detectable as "not set".
type Action int

// Action values returned in Result.Action. ActionUnknown sits at iota 0 so
// a zero-value Result is detectable as "not set". ActionEdit is the additive
// vars-browser intent (see ModeEdit); it is appended at the end so the existing
// command-browser values keep their numbers.
const (
	ActionUnknown Action = iota
	ActionRun
	ActionInspect
	ActionEdit
)

// Mode selects which intent the TUI is gathering. ModeUnknown sits at iota 0
// so a zero-value Options.Mode is detectable; applyDefaults promotes it to
// ModeRun. This is the only field with auto-defaulting — int/bool fields are
// preserved verbatim so legitimate user opt-outs like
// `default_expanded_depth: 0` reach the model unchanged.
type Mode int

// Mode values selecting the TUI intent. ModeUnknown sits at iota 0; the
// applyDefaults promotion treats it as ModeRun (the only auto-defaulted
// field in Options). ModeEdit is the additive vars-browser mode: Enter returns
// ActionEdit and the footer/breadcrumb adapt, but the ModeRun-only
// edit-params / skip-confirm bindings stay off — the command browser is
// untouched.
const (
	ModeUnknown Mode = iota
	ModeRun
	ModeInspect
	ModeEdit
)

// Item is one row in the browser. The caller precomputes Inspect by
// rendering the inspect view of its CommandDef into a string; cmdbrowser is
// decoupled from usercommands.CommandDef and does no rendering of command
// shapes itself. ParamCount is rendered as a small `[N]` badge in the list
// so the user can see at a glance which commands take parameters (and how
// many) before opening the param form.
type Item struct {
	ID          string
	Description string
	Type        string
	Private     bool
	ParamCount  int
	// Inspect builds the long-form description shown in the inspect viewport.
	// It receives the viewport's content width and must wrap to it — viewports
	// do not soft-wrap, so pre-rendering at a wider width clips the right edge
	// when the viewport is narrower than the terminal. A nil Inspect (or one
	// that returns "") renders a placeholder.
	Inspect func(width int) string
}

// Options carries already-resolved configuration. Defaulting happens in the
// config accessors (config.UICommands*); auto-defaulting int/bool fields
// here would silently overwrite legitimate opt-outs. Callers without a
// *config.DweConfig should use DefaultOptions().
type Options struct {
	DefaultExpandedDepth int
	AutoCollapseEmpty    bool
	ShowTypeBadges       bool
	IncludePrivate       bool
	Mode                 Mode

	// Translator + Locale carry the i18n context into the framework so the
	// help modal can localize its section/action labels. They are the only
	// non-breaking way to thread i18n through the frozen Run signature. Both
	// are nil-safe: a nil Translator resolves to i18n.NopTranslator and an
	// empty Locale falls through to the framework default. Storage/hashing
	// sites stay English per the localization contract; the breadcrumb noun
	// stays hardcoded English (see browser.itemNoun).
	Translator i18n.Translator
	Locale     string
}

// translatorOrNop returns the configured Translator, falling back to a no-op
// when none was supplied (DefaultOptions and test call sites pass none).
func (o *Options) translatorOrNop() i18n.Translator {
	if o.Translator == nil {
		return i18n.NopTranslator{}
	}
	return o.Translator
}

// applyDefaults promotes the zero Mode to ModeRun. No other field is
// touched: a zero int / bool here is a legitimate user value.
func (o *Options) applyDefaults() {
	if o.Mode == ModeUnknown {
		o.Mode = ModeRun
	}
}

// Result is the value returned by Run. Extending Result with additional
// fields later (e.g. an `edit` intent) is additive — call sites destructure
// only what they need.
//
// ForceParamForm is set when the user picks an item via the EditParams key
// (default `e`) instead of Enter. The orchestrator interprets it as
// "open the param form even if all defaults are already satisfied" — Enter
// auto-skips the form in that case. Inspect mode ignores it.
type Result struct {
	Idx            int
	Action         Action
	SkipConfirm    bool
	ForceParamForm bool
}

// DefaultOptions returns the spec defaults for callers that don't have a
// *config.DweConfig (tests and future programmatic callers). Options{}
// is intentionally NOT a useful zero value — use this factory or the
// config.UICommands* accessors.
func DefaultOptions() Options {
	return Options{
		DefaultExpandedDepth: 1,
		AutoCollapseEmpty:    true,
		ShowTypeBadges:       true,
		IncludePrivate:       false,
		Mode:                 ModeRun,
	}
}

// Run launches the interactive command browser. Returns widgets.ErrCancelled
// when the user quits via q / Esc / Ctrl-C. On TTYs narrower than 60 cols
// or shorter than 15 rows (and on terminal-size read failures), delegates
// to widgets.RunSelector. Non-TTY callers are short-circuited at the call site
// with an error before reaching this function; this code returns
// widgets.ErrCancelled defensively if it is reached anyway.
func Run(title string, items []Item, opts Options) (Result, error) {
	opts.applyDefaults()
	if len(items) == 0 {
		return Result{}, fmt.Errorf("cmdbrowser: no items to display")
	}

	if !isTerminalFn() {
		// Defence-in-depth: production callers short-circuit non-TTY at the
		// call site. RunSelector also requires a TTY so we cannot delegate.
		return Result{}, widgets.ErrCancelled
	}

	width, height, err := terminalSizeFn()
	if err != nil || width < minTwoPanelWidth || height < 15 {
		return runFallback(title, items, opts.IncludePrivate)
	}

	m := newModel(title, items, opts, width, height)
	prog := tea.NewProgram(m)

	runErr := widgets.RunWithPromptHooks(func() error {
		_, e := prog.Run()
		return e
	})
	if runErr != nil {
		if errors.Is(runErr, tea.ErrProgramPanic) {
			return Result{}, runErr
		}
		if errors.Is(runErr, tea.ErrInterrupted) || errors.Is(runErr, tea.ErrProgramKilled) {
			return Result{}, widgets.ErrCancelled
		}
		return Result{}, runErr
	}
	if m.cancelled {
		return Result{}, widgets.ErrCancelled
	}
	// ActionUnknown (zero value) means the program exited without the user
	// making a selection — treat it as a cancellation rather than silently
	// returning defs[0].
	if m.result.Action == ActionUnknown {
		return Result{}, widgets.ErrCancelled
	}
	return m.result, nil
}
