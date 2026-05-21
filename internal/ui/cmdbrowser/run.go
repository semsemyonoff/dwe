// Package cmdbrowser implements the interactive two-panel TUI used by
// `devbox commands` when no exact command ID is supplied. It is a sibling of
// internal/ui — callers import it directly. A facade in internal/ui would
// form a cycle (ui → cmdbrowser → ui).
package cmdbrowser

import (
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"devbox-cli/internal/ui"
)

// Action enumerates intents returned by the browser. ActionUnknown sits at
// iota 0 so a zero-value Result is detectable as "not set".
type Action int

// Action values returned in Result.Action. ActionUnknown sits at iota 0 so
// a zero-value Result is detectable as "not set".
const (
	ActionUnknown Action = iota
	ActionRun
	ActionInspect
)

// Mode selects which intent the TUI is gathering. ModeUnknown sits at iota 0
// so a zero-value Options.Mode is detectable; applyDefaults promotes it to
// ModeRun. This is the only field with auto-defaulting — int/bool fields are
// preserved verbatim so legitimate user opt-outs like
// `default_expanded_depth: 0` reach the model unchanged.
type Mode int

// Mode values selecting the TUI intent. ModeUnknown sits at iota 0; the
// applyDefaults promotion treats it as ModeRun (the only auto-defaulted
// field in Options).
const (
	ModeUnknown Mode = iota
	ModeRun
	ModeInspect
)

// Item is one row in the browser. The caller precomputes Inspect by
// rendering the inspect view of its CommandDef into a string; cmdbrowser is
// decoupled from usercommands.CommandDef and does no rendering of command
// shapes itself.
type Item struct {
	ID          string
	Description string
	Type        string
	Private     bool
	Inspect     string
}

// Options carries already-resolved configuration. Defaulting happens in the
// config accessors (config.UICommands*); auto-defaulting int/bool fields
// here would silently overwrite legitimate opt-outs. Callers without a
// *config.DevboxConfig should use DefaultOptions().
type Options struct {
	DefaultExpandedDepth int
	AutoCollapseEmpty    bool
	ShowTypeBadges       bool
	IncludePrivate       bool
	Mode                 Mode
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
type Result struct {
	Idx         int
	Action      Action
	SkipConfirm bool
}

// DefaultOptions returns the spec defaults for callers that don't have a
// *config.DevboxConfig (tests and future programmatic callers). Options{}
// is intentionally NOT a useful zero value — use this factory or the
// config.UICommands* accessors.
func DefaultOptions() Options {
	return Options{
		DefaultExpandedDepth: 3,
		AutoCollapseEmpty:    true,
		ShowTypeBadges:       true,
		IncludePrivate:       false,
		Mode:                 ModeRun,
	}
}

// Run launches the interactive command browser. Returns ui.ErrCancelled
// when the user quits via q / Esc / Ctrl-C. On TTYs narrower than 60 cols
// or shorter than 15 rows (and on terminal-size read failures), delegates
// to ui.RunSelector. Non-TTY callers are short-circuited at the call site
// with an error before reaching this function; this code returns
// ui.ErrCancelled defensively if it is reached anyway.
func Run(title string, items []Item, opts Options) (Result, error) {
	opts.applyDefaults()
	if len(items) == 0 {
		return Result{}, fmt.Errorf("cmdbrowser: no items to display")
	}

	if !isTerminalFn() {
		// Defence-in-depth: production callers short-circuit non-TTY at the
		// call site. RunSelector also requires a TTY so we cannot delegate.
		return Result{}, ui.ErrCancelled
	}

	width, height, err := terminalSizeFn()
	if err != nil || width < minTwoPanelWidth || height < 15 {
		return runFallback(title, items, opts.IncludePrivate)
	}

	m := newModel(title, items, opts, width, height)
	prog := tea.NewProgram(m)

	runErr := ui.RunWithPromptHooks(func() error {
		_, e := prog.Run()
		return e
	})
	if runErr != nil {
		if errors.Is(runErr, tea.ErrInterrupted) || errors.Is(runErr, tea.ErrProgramKilled) {
			return Result{}, ui.ErrCancelled
		}
		return Result{}, runErr
	}
	if m.cancelled {
		return Result{}, ui.ErrCancelled
	}
	// ActionUnknown (zero value) means the program exited without the user
	// making a selection — treat it as a cancellation rather than silently
	// returning defs[0].
	if m.result.Action == ActionUnknown {
		return Result{}, ui.ErrCancelled
	}
	return m.result, nil
}
