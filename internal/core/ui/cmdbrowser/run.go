// Package cmdbrowser implements the interactive two-panel TUI used by
// `dwe commands` when no exact command ID is supplied. It is a sibling of
// internal/core/ui — callers import it directly. A facade in internal/core/ui would
// form a cycle (ui → cmdbrowser → ui).
package cmdbrowser

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// Minimum usable terminal size for the two-panel frame. Below either bound the
// browser drops to the flat huh fallback (runFallback): there is no in-TUI
// single-panel layout, so minBrowserWidth is the sole boundary between the
// framework frame and the fallback. tui.Run re-gates on its own (smaller)
// minimum internally, a harmless double-check; this is the real fallback
// boundary.
const (
	minBrowserWidth  = 80
	minBrowserHeight = 15
)

// runTUI is the package-local seam through which Run drives the framework. It
// defaults to tui.Run and is swapped in tests so the ≥80 path can be exercised
// without a real terminal. Keeping the seam local (rather than exporting tui.Run's
// capability seams) limits the framework's production API surface.
var runTUI = tui.Run

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
// preserved verbatim so a legitimate opt-out like DefaultExpandedDepth: 0
// reaches the model unchanged.
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

// EditSpec turns ModeEdit's Enter/double-click into an in-TUI form overlay
// (edit-and-stay) instead of the exit-and-return commit. The plugin builds the
// form via BuildForm, hosts it as a capturing [tui.FormOverlay], and on submit
// calls Commit synchronously to persist the edit and produce the replacement
// row + status flash. It is opt-in per Options: nil preserves today's
// exit-and-return ModeEdit behaviour byte-for-byte (see Options.Edit).
//
// Both closures are supplied by the caller (cli/vars): the plugin stays
// decoupled from what an "edit" writes. BuildForm returns an *ask.Form (built
// but not run — the plugin drives its huh model directly); Commit receives the
// harvested ask.Result. The idx is the index into the items slice passed to Run.
type EditSpec struct {
	// BuildForm builds the edit form for items[idx]. A non-nil error aborts the
	// edit with an error flash and opens no overlay.
	BuildForm func(idx int) (*ask.Form, error)
	// Commit persists the submitted form for items[idx] and returns the
	// replacement row + confirmation flash. A non-nil error closes the overlay
	// and shows an error flash instead of replacing the row.
	Commit func(idx int, res ask.Result) (CommitOutcome, error)
}

// CommitOutcome is the result of an EditSpec.Commit: the replacement row for the
// edited index (fresh value + Type badge + Inspect closure) and the status-line
// flash confirming the write (e.g. `✓ db.host = "db.internal"`). A var edit
// never adds or removes leaves, so only the one row is replaced — the tree shape
// and cursor stay put.
type CommitOutcome struct {
	Item  Item
	Flash string
}

// RunFormSpec turns ModeRun's Enter / force-form (`e`) into an in-TUI param-form
// overlay (harvest-and-quit) instead of the exit-then-form flow. It is the
// ModeRun sibling of [EditSpec], but the terminal action differs fundamentally:
// EditSpec is write-and-stay (Commit persists local.yml, the browser stays open,
// the row refreshes); RunFormSpec is harvest-and-quit (collect the param values,
// close the overlay, quit the browser — the command executes AFTER alt-screen
// teardown because it streams docker/pipeline output to the plain terminal). The
// overlay-driving plumbing (pending/token handling, FormOverlay forwarding,
// status flash) is shared with the edit machine; only the terminal step differs.
//
// It is opt-in per Options: nil (the default, and every ModeRun caller that does
// not supply one) preserves today's exit-then-form behaviour byte-for-byte (see
// Options.RunForm). Both closures are supplied by the caller (cli/command): the
// plugin stays decoupled from how the CLI builds the form and maps ask.Result →
// a param map.
type RunFormSpec struct {
	// BuildForm builds the param form for items[idx]. force is true when the user
	// pressed the force-form key (`e`); false for plain Enter — the CLI uses it to
	// auto-skip the form when all required params are already satisfied. A nil form
	// with a nil error means "no form needed" → the browser quits immediately with
	// Result{Action: ActionRun} and NO Values (byte-identical to today's
	// exit-and-run for commands with no params / already-satisfied required). A
	// non-nil error aborts with an error flash and opens no overlay.
	BuildForm func(idx int, force bool) (*ask.Form, error)
	// Harvest converts a submitted form into the param values carried out in
	// Result.Values. Kept separate from BuildForm so the plugin stays decoupled
	// from how the CLI maps ask.Result → a param map (widget / multiselect
	// specifics).
	Harvest func(idx int, res ask.Result) map[string]string
}

// Options carries already-resolved configuration. Int/bool fields are never
// auto-defaulted here — that would silently overwrite a legitimate opt-out
// such as DefaultExpandedDepth: 0. Start from DefaultOptions() and set only
// the fields the caller cares about.
type Options struct {
	DefaultExpandedDepth int
	AutoCollapseEmpty    bool
	ShowTypeBadges       bool
	IncludePrivate       bool
	Mode                 Mode

	// Edit enables in-TUI edit-and-stay in ModeEdit: Enter opens a form overlay
	// instead of committing a Result and quitting. nil (the default) keeps the
	// legacy exit-and-return behaviour, so ModeRun / ModeInspect and any ModeEdit
	// caller that does not supply an EditSpec are untouched.
	Edit *EditSpec

	// RunForm enables the in-TUI param-form overlay in ModeRun: Enter / force-form
	// (`e`) open a form overlay over the browser, harvest the params on submit, and
	// quit with Result.Values populated (the command still executes after the TUI
	// exits). nil (the default) keeps the exit-then-form flow, so ModeInspect /
	// ModeEdit and any ModeRun caller that does not supply a RunFormSpec are
	// untouched.
	RunForm *RunFormSpec

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
	// Values carries the params harvested from the in-TUI RunForm overlay out to
	// the orchestrator (which uses them directly instead of building its own
	// form). nil means "no in-TUI harvest" — every non-browser path, and the
	// no-form-needed browser path (BuildForm returned nil), leave it nil so
	// today's behaviour is preserved exactly.
	Values map[string]string
}

// DefaultOptions returns the spec defaults every caller starts from. Options{}
// is intentionally NOT a useful zero value — build from this factory and
// override only the fields you need.
func DefaultOptions() Options {
	return Options{
		DefaultExpandedDepth: 1,
		AutoCollapseEmpty:    true,
		ShowTypeBadges:       true,
		IncludePrivate:       false,
		Mode:                 ModeRun,
	}
}

// Run launches the interactive command browser as a [tui.Plugin] on the
// framework Frame. Returns widgets.ErrCancelled when the user quits via
// q / Esc / Ctrl-C (or exits without a selection). On TTYs narrower than
// minBrowserWidth cols or shorter than minBrowserHeight rows (and on
// terminal-size read failures), delegates to the flat huh fallback
// (runFallback). Non-TTY callers are short-circuited at the call site with an
// error before reaching this function; this code returns widgets.ErrCancelled
// defensively if it is reached anyway.
//
// tui.Run owns the alt-screen, mouse mode, and widgets.RunWithPromptHooks
// wrap — the browser must NOT wrap a second time. tui.Run returns the plugin's
// Result as `any` (UNCHANGED) on a clean quit, or widgets.ErrCancelled on an
// interrupted/killed program; both are mapped straight through here, plus the
// ActionUnknown → ErrCancelled guard for an exit-without-selection.
func Run(title string, items []Item, opts Options) (Result, error) {
	opts.applyDefaults()
	if len(items) == 0 {
		return Result{}, fmt.Errorf("cmdbrowser: no items to display")
	}

	if !isTerminalFn() {
		// Defence-in-depth: production callers short-circuit non-TTY at the
		// call site. runFallback also requires a TTY so we cannot delegate.
		return Result{}, widgets.ErrCancelled
	}

	width, height, err := terminalSizeFn()
	if err != nil || width < minBrowserWidth || height < minBrowserHeight {
		return runFallback(title, items, opts.IncludePrivate)
	}

	out, runErr := runTUI(newBrowser(title, items, opts), tui.RunOptions{
		Brand:      title,
		Mouse:      true,
		Translator: opts.translatorOrNop(),
		Locale:     opts.Locale,
	})
	if runErr != nil {
		// tui.Run already maps a user-initiated exit to widgets.ErrCancelled and
		// wraps panics/real failures; pass them straight through.
		return Result{}, runErr
	}
	res, _ := out.(Result)
	// ActionUnknown (zero value) means the program exited without the user
	// making a selection — treat it as a cancellation rather than silently
	// returning items[0].
	if res.Action == ActionUnknown {
		return Result{}, widgets.ErrCancelled
	}
	return res, nil
}
