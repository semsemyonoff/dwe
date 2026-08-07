package cmdbrowser

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/charmbracelet/colorprofile"
	"go.uber.org/goleak"

	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

func TestMain(m *testing.M) {
	// Pin the lipgloss/v2 colour profile to NoTTY so View() output is
	// byte-identical across local + CI regardless of TERM / COLORTERM env.
	// Without this, snapshot assertions and ANSI-escape checks would be
	// non-deterministic.
	compat.Profile = colorprofile.NoTTY
	// goleak verifies bubbletea spawns no orphaned goroutines after Run().
	goleak.VerifyTestMain(m)
}

// withSeams installs test stubs for the package-level seams and restores
// them via t.Cleanup. Pass terminalSize as (0, 0, err) to simulate a size
// read failure.
func withSeams(t *testing.T, isTTY bool, width, height int, sizeErr error, runSelector func(string, []widgets.SelectorItem) (int, error)) {
	t.Helper()
	origTTY := isTerminalFn
	origSize := terminalSizeFn
	origSelector := runSelectorFn
	t.Cleanup(func() {
		isTerminalFn = origTTY
		terminalSizeFn = origSize
		runSelectorFn = origSelector
	})
	isTerminalFn = func() bool { return isTTY }
	terminalSizeFn = func() (int, int, error) { return width, height, sizeErr }
	if runSelector != nil {
		runSelectorFn = runSelector
	}
}

func TestDefaultOptions(t *testing.T) {
	got := DefaultOptions()
	if got.DefaultExpandedDepth != 1 || !got.AutoCollapseEmpty || !got.ShowTypeBadges || got.IncludePrivate || got.Mode != ModeRun {
		t.Errorf("DefaultOptions=%+v, want depth=1, autocollapse=true, badges=true, mode=run", got)
	}
}

func TestOptionsApplyDefaults_OnlyMode(t *testing.T) {
	// A zero Options keeps int/bool zero values; only Mode is promoted.
	opts := Options{}
	opts.applyDefaults()
	if opts.Mode != ModeRun {
		t.Errorf("applyDefaults Mode=%v, want ModeRun", opts.Mode)
	}
	if opts.DefaultExpandedDepth != 0 || opts.AutoCollapseEmpty || opts.ShowTypeBadges {
		t.Errorf("applyDefaults must not touch int/bool fields, got %+v", opts)
	}
}

func TestOptionsApplyDefaults_PreservesExplicitMode(t *testing.T) {
	opts := Options{Mode: ModeInspect}
	opts.applyDefaults()
	if opts.Mode != ModeInspect {
		t.Errorf("explicit ModeInspect overwritten: %v", opts.Mode)
	}
}

func TestRun_EmptyItems(t *testing.T) {
	withSeams(t, true, 120, 30, nil, nil)
	_, err := Run("pick", nil, DefaultOptions())
	if err == nil || !strings.Contains(err.Error(), "no items") {
		t.Errorf("Run with empty items: got err=%v", err)
	}
}

func TestRun_NonTTY_ReturnsCancelledAndDoesNotCallSelector(t *testing.T) {
	called := 0
	withSeams(t, false, 0, 0, nil, func(string, []widgets.SelectorItem) (int, error) {
		called++
		return 0, nil
	})
	_, err := Run("pick", []Item{{ID: "a"}}, DefaultOptions())
	if !errors.Is(err, widgets.ErrCancelled) {
		t.Errorf("non-TTY: want ErrCancelled, got %v", err)
	}
	if called != 0 {
		t.Errorf("RunSelector must not be invoked in non-TTY path; called=%d", called)
	}
}

func TestRun_NarrowDelegatesToSelector(t *testing.T) {
	called := 0
	withSeams(t, true, 50, 30, nil, func(title string, items []widgets.SelectorItem) (int, error) {
		called++
		if title != "pick" {
			t.Errorf("title=%q", title)
		}
		if len(items) != 2 {
			t.Errorf("items=%d, want 2", len(items))
		}
		return 1, nil
	})
	res, err := Run("pick", []Item{{ID: "a"}, {ID: "b"}}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Errorf("runSelectorFn called=%d, want 1", called)
	}
	if res.Idx != 1 || res.Action != ActionRun {
		t.Errorf("Result=%+v", res)
	}
}

func TestRun_ShortDelegatesToSelector(t *testing.T) {
	called := 0
	withSeams(t, true, 120, 10, nil, func(string, []widgets.SelectorItem) (int, error) {
		called++
		return 0, nil
	})
	_, err := Run("pick", []Item{{ID: "a"}}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Errorf("short terminal must delegate to runSelectorFn; called=%d", called)
	}
}

func TestRun_TerminalSizeErrorDelegates(t *testing.T) {
	called := 0
	sizeErr := errors.New("ioctl boom")
	selectorErr := errors.New("selector boom")
	// The selector returns its OWN error so the assertion that the size error is
	// never surfaced actually runs: any error reaching the caller must be the
	// selector's, never the size-read failure that triggered the fallback.
	withSeams(t, true, 0, 0, sizeErr, func(string, []widgets.SelectorItem) (int, error) {
		called++
		return 0, selectorErr
	})
	_, err := Run("pick", []Item{{ID: "a"}}, DefaultOptions())
	if !errors.Is(err, selectorErr) {
		t.Errorf("fallback must propagate the selector error, got %v", err)
	}
	if errors.Is(err, sizeErr) {
		t.Errorf("size-read error must not be surfaced to the caller, got %v", err)
	}
	if called != 1 {
		t.Errorf("size-error path must delegate to runSelectorFn; called=%d", called)
	}
}

func TestRun_FallbackPropagatesSelectorError(t *testing.T) {
	withSeams(t, true, 50, 30, nil, func(string, []widgets.SelectorItem) (int, error) {
		return -1, widgets.ErrCancelled
	})
	_, err := Run("pick", []Item{{ID: "a"}}, DefaultOptions())
	if !errors.Is(err, widgets.ErrCancelled) {
		t.Errorf("want ErrCancelled, got %v", err)
	}
}

// stubRunTUI swaps the package-local runTUI seam to return a chosen result/error
// without spinning a real terminal, restoring it via t.Cleanup. The recorded
// plugin lets the test confirm Run wired a *browser (not a fallback) through.
func stubRunTUI(t *testing.T, out any, err error) *tui.RunOptions {
	t.Helper()
	orig := runTUI
	var captured tui.RunOptions
	t.Cleanup(func() { runTUI = orig })
	runTUI = func(_ tui.Plugin, opts tui.RunOptions) (any, error) {
		captured = opts
		return out, err
	}
	return &captured
}

// TestRun_WideDrivesPluginAndReturnsResult verifies the ≥80 path constructs the
// plugin and threads tui.Run's Result back unchanged.
func TestRun_WideDrivesPluginAndReturnsResult(t *testing.T) {
	withSeams(t, true, 120, 30, nil, func(string, []widgets.SelectorItem) (int, error) {
		t.Fatal("wide path must not call the fallback selector")
		return 0, nil
	})
	opts := stubRunTUI(t, Result{Idx: 2, Action: ActionRun, SkipConfirm: true}, nil)

	res, err := Run("pick", []Item{{ID: "a"}, {ID: "b"}, {ID: "c"}}, DefaultOptions())
	if err != nil {
		t.Fatalf("Run returned err=%v", err)
	}
	if res.Idx != 2 || res.Action != ActionRun || !res.SkipConfirm {
		t.Errorf("Result not threaded through unchanged: %+v", res)
	}
	if opts.Brand != "pick" || !opts.Mouse {
		t.Errorf("RunOptions=%+v, want Brand=pick Mouse=true", opts)
	}
}

// TestRun_WideThreadsTranslatorAndLocale verifies Run forwards the i18n context
// (Translator + Locale) into tui.RunOptions so the help modal can localize —
// the only non-breaking channel through the frozen Run signature.
func TestRun_WideThreadsTranslatorAndLocale(t *testing.T) {
	withSeams(t, true, 120, 30, nil, nil)
	captured := stubRunTUI(t, Result{Idx: 0, Action: ActionRun}, nil)

	tr := i18n.NopTranslator{}
	opts := DefaultOptions()
	opts.Translator = tr
	opts.Locale = "ru"
	if _, err := Run("pick", []Item{{ID: "a"}}, opts); err != nil {
		t.Fatalf("Run err=%v", err)
	}
	if captured.Locale != "ru" {
		t.Errorf("Locale not threaded into RunOptions; got %q, want ru", captured.Locale)
	}
	if captured.Translator == nil {
		t.Error("Translator not threaded into RunOptions; got nil")
	}
}

// TestRun_WideMapsCancelledThrough verifies tui.Run's widgets.ErrCancelled
// passes straight back (no second RunWithPromptHooks wrap, no remap).
func TestRun_WideMapsCancelledThrough(t *testing.T) {
	withSeams(t, true, 120, 30, nil, nil)
	stubRunTUI(t, nil, widgets.ErrCancelled)
	_, err := Run("pick", []Item{{ID: "a"}}, DefaultOptions())
	if !errors.Is(err, widgets.ErrCancelled) {
		t.Errorf("want ErrCancelled mapped through, got %v", err)
	}
}

// TestRun_WideActionUnknownIsCancelled verifies an exit without a selection
// (zero-value Action) is reported as a clean cancellation, not items[0].
func TestRun_WideActionUnknownIsCancelled(t *testing.T) {
	withSeams(t, true, 120, 30, nil, nil)
	stubRunTUI(t, Result{}, nil) // Action == ActionUnknown
	_, err := Run("pick", []Item{{ID: "a"}}, DefaultOptions())
	if !errors.Is(err, widgets.ErrCancelled) {
		t.Errorf("ActionUnknown must map to ErrCancelled, got %v", err)
	}
}

// TestRun_NarrowBelow80DelegatesToSelector verifies the raised threshold: a
// 60–79 width (the old in-TUI single-panel bucket, dropped under Variant A) now
// routes to the flat fallback and never drives the plugin.
func TestRun_NarrowBelow80DelegatesToSelector(t *testing.T) {
	called := 0
	withSeams(t, true, 70, 30, nil, func(string, []widgets.SelectorItem) (int, error) {
		called++
		return 0, nil
	})
	// Fail loudly if the plugin path is taken instead of the fallback.
	t.Cleanup(func() {})
	orig := runTUI
	t.Cleanup(func() { runTUI = orig })
	runTUI = func(_ tui.Plugin, _ tui.RunOptions) (any, error) {
		t.Fatal("width 70 must route to the fallback, not the framework")
		return nil, nil
	}
	if _, err := Run("pick", []Item{{ID: "a"}}, DefaultOptions()); err != nil {
		t.Fatalf("Run err=%v", err)
	}
	if called != 1 {
		t.Errorf("width 70 must delegate to the fallback selector; called=%d", called)
	}
}

// syntheticKey builds a minimal KeyPressMsg that responds to .String() with
// the given keystroke. bubbletea/v2's KeyPressMsg.String() comes from the
// underlying Key fields; setting Text only is enough for the alphabetic
// keys, but special keys ("esc", "tab", "ctrl+c") need the Code/Mod set.
func syntheticKey(s string) tea.KeyPressMsg {
	// Map a handful of keystroke strings used in tests. This avoids pulling
	// in unstable internal APIs from charmbracelet/ultraviolet just to spell
	// "esc" as a Key value.
	switch s {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	default:
		if len(s) == 1 {
			return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
		}
		return tea.KeyPressMsg{Text: s}
	}
}
