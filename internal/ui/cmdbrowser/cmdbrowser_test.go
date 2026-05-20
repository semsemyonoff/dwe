package cmdbrowser

import (
	"errors"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/charmbracelet/colorprofile"
	"go.uber.org/goleak"

	"devbox-cli/internal/ui"
)

func TestMain(m *testing.M) {
	// Pin the lipgloss/v2 colour profile to NoTTY so View() output is
	// byte-identical across local + CI regardless of TERM / COLORTERM env.
	// Without this, snapshot assertions and ANSI-escape checks would be
	// non-deterministic.
	compat.Profile = colorprofile.NoTTY
	// goleak verifies bubbletea spawns no orphaned goroutines after Run().
	goleak.VerifyTestMain(m)
	os.Exit(0)
}

// withSeams installs test stubs for the package-level seams and restores
// them via t.Cleanup. Pass terminalSize as (0, 0, err) to simulate a size
// read failure.
func withSeams(t *testing.T, isTTY bool, width, height int, sizeErr error, runSelector func(string, []ui.SelectorItem) (int, error)) {
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
	if got.DefaultExpandedDepth != 3 || !got.AutoCollapseEmpty || !got.ShowTypeBadges || got.IncludePrivate || got.Mode != ModeRun {
		t.Errorf("DefaultOptions=%+v, want depth=3, autocollapse=true, badges=true, mode=run", got)
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
	withSeams(t, false, 0, 0, nil, func(string, []ui.SelectorItem) (int, error) {
		called++
		return 0, nil
	})
	_, err := Run("pick", []Item{{ID: "a"}}, DefaultOptions())
	if !errors.Is(err, ui.ErrCancelled) {
		t.Errorf("non-TTY: want ErrCancelled, got %v", err)
	}
	if called != 0 {
		t.Errorf("RunSelector must not be invoked in non-TTY path; called=%d", called)
	}
}

func TestRun_NarrowDelegatesToSelector(t *testing.T) {
	called := 0
	withSeams(t, true, 50, 30, nil, func(title string, items []ui.SelectorItem) (int, error) {
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
	withSeams(t, true, 120, 10, nil, func(string, []ui.SelectorItem) (int, error) {
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
	sentinel := errors.New("ioctl boom")
	withSeams(t, true, 0, 0, sentinel, func(string, []ui.SelectorItem) (int, error) {
		called++
		return 0, nil
	})
	_, err := Run("pick", []Item{{ID: "a"}}, DefaultOptions())
	if err != nil {
		// Selector returned nil; size error must not be surfaced.
		if errors.Is(err, sentinel) {
			t.Errorf("size error must not be surfaced to caller")
		}
		t.Fatal(err)
	}
	if called != 1 {
		t.Errorf("size-error path must delegate to runSelectorFn; called=%d", called)
	}
}

func TestRun_FallbackPropagatesSelectorError(t *testing.T) {
	withSeams(t, true, 50, 30, nil, func(string, []ui.SelectorItem) (int, error) {
		return -1, ui.ErrCancelled
	})
	_, err := Run("pick", []Item{{ID: "a"}}, DefaultOptions())
	if !errors.Is(err, ui.ErrCancelled) {
		t.Errorf("want ErrCancelled, got %v", err)
	}
}

func TestModel_ViewRendersTwoPanels(t *testing.T) {
	m := newModel("pick", []Item{{ID: "db.migrate"}, {ID: "db.seed"}}, DefaultOptions(), 120, 26)
	v := m.View()
	if !v.AltScreen {
		t.Error("View must request AltScreen")
	}
	if !strings.Contains(v.Content, "pick") {
		t.Errorf("view does not contain title; got:\n%s", v.Content)
	}
	if !strings.Contains(v.Content, "groups") {
		t.Errorf("view missing left-panel label 'groups'; got:\n%s", v.Content)
	}
	if !strings.Contains(v.Content, "db") {
		t.Errorf("view missing tree node 'db'; got:\n%s", v.Content)
	}
	if !strings.Contains(v.Content, "commands") {
		t.Errorf("view missing breadcrumb 'commands' label; got:\n%s", v.Content)
	}
}

func TestModel_CancelKeysSetCancelledAndQuit(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"esc", "esc"},
		{"q", "q"},
		{"ctrl+c", "ctrl+c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel("t", []Item{{ID: "a"}}, DefaultOptions(), 120, 26)
			msg := tea.KeyPressMsg{Code: 0, Text: tc.key}
			// Use a synthetic KeyPressMsg by stringifying directly.
			_, cmd := m.Update(syntheticKey(tc.key))
			if !m.cancelled {
				t.Errorf("cancelled flag not set after %q (msg=%v)", tc.key, msg)
			}
			if cmd == nil {
				t.Errorf("expected tea.Quit cmd, got nil")
			}
		})
	}
}

func TestModel_TabSwitchesFocus(t *testing.T) {
	m := newModel("t", []Item{{ID: "a"}}, DefaultOptions(), 120, 26)
	if m.focus != focusLeft {
		t.Fatalf("initial focus=%v, want focusLeft", m.focus)
	}
	m.Update(syntheticKey("tab"))
	if m.focus != focusRight {
		t.Errorf("after tab focus=%v, want focusRight", m.focus)
	}
	m.Update(syntheticKey("tab"))
	if m.focus != focusLeft {
		t.Errorf("after tab x2 focus=%v, want focusLeft", m.focus)
	}
}

func TestModel_WindowSizeUpdates(t *testing.T) {
	m := newModel("t", []Item{{ID: "a"}}, DefaultOptions(), 120, 26)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if m.width != 100 || m.height != 30 {
		t.Errorf("width/height not updated: %d/%d", m.width, m.height)
	}
}

// Snapshot-ish: assert the empty two-panel view at 120x26 has a stable
// shape (single render, with title and both panel placeholders). We do not
// pin a byte-identical golden file here — the lipgloss border characters
// are stable but small label changes would force constant churn in Task 3.
func TestModel_EmptyLayoutSnapshot(t *testing.T) {
	m := newModel("Select command", []Item{{ID: "db.migrate"}}, DefaultOptions(), 120, 26)
	// Drive the model with a window-size msg as bubbletea would.
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 26})
	out := m.View().Content
	for _, want := range []string{"Select command", "groups", "command", "tab"} {
		if !strings.Contains(out, want) {
			t.Errorf("snapshot missing %q\n---\n%s", want, out)
		}
	}
}

// syntheticKey builds a minimal KeyPressMsg that responds to .String() with
// the given keystroke. bubbletea/v2's KeyPressMsg.String() comes from the
// underlying Key fields; setting Text only is enough for the alphabetic
// keys, but special keys ("esc", "tab", "ctrl+c") need the Code/Mod set.
// We build a Key by parsing the string via uv.
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
