package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/semsemyonoff/dwe/internal/shared/i18n"

	"charm.land/lipgloss/v2"
)

// helpHasToken reports whether tok appears as a standalone token in the rendered
// help content, splitting on whitespace and the key-list separator (","). It
// avoids the false positives a raw substring check would hit on descriptions
// that merely contain tok (e.g. "Describe" contains "esc").
func helpHasToken(content, tok string) bool {
	return slices.Contains(strings.FieldsFunc(content, func(r rune) bool {
		return unicode.IsSpace(r) || r == ','
	}), tok)
}

// ansiSGR matches the SGR escape sequences lipgloss emits (colors, bold, faint).
// The help golden strips these so it stays byte-stable regardless of the
// terminal background lipgloss.HasDarkBackground() detects (which selects the
// light vs dark palette variant and would otherwise differ across CI machines).
var ansiSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiSGR.ReplaceAllString(s, "") }

// TestBuildHelpOverlay_golden pins the help-modal layout generated from the
// built-in registry (ANSI-stripped — see ansiSGR). Regenerate with
// UPDATE_GOLDEN=1 go test ./internal/core/ui/tui/...
func TestBuildHelpOverlay_golden(t *testing.T) {
	reg := NewRegistry()
	ov := buildHelpOverlay(reg, i18n.TranslatorOrNop(nil), "en", 80, 24)
	got := stripANSI(ov.Content)

	goldenPath := filepath.Join("testdata", "help_default.golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("creating testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
		t.Logf("updated golden: %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden %s: %v", goldenPath, err)
	}
	if got != string(want) {
		t.Errorf("help overlay mismatch:\ngot:\n%s\n\nwant:\n%s", got, want)
	}
}

// TestBuildHelpOverlay_fitsWidth asserts the help body never exceeds the body
// region width it is built for, across the frame's width buckets, and that the
// reported overlay dimensions match the rendered content.
func TestBuildHelpOverlay_fitsWidth(t *testing.T) {
	reg := NewRegistry()
	for _, width := range []int{60, 79, 80, 99, 100} {
		ov := buildHelpOverlay(reg, i18n.TranslatorOrNop(nil), "en", width, 24)

		if ov.Width > width {
			t.Errorf("width %d: overlay width = %d; want <= %d", width, ov.Width, width)
		}
		for i, line := range strings.Split(ov.Content, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Errorf("width %d: row %d width = %d; exceeds body width", width, i, got)
			}
		}
		if got := lipgloss.Width(ov.Content); got != ov.Width {
			t.Errorf("width %d: reported Width = %d; rendered = %d", width, ov.Width, got)
		}
		if got := lipgloss.Height(ov.Content); got != ov.Height {
			t.Errorf("width %d: reported Height = %d; rendered = %d", width, ov.Height, got)
		}
	}
}

// TestBuildHelpOverlay_fitsHeight asserts the help body never exceeds the body
// region height it is built for, down to the smallest permitted terminal. Without
// the height clamp the modal overflows the frame because Composite does not clip
// vertically (regression guard for the small-terminal corruption at h=10..13).
func TestBuildHelpOverlay_fitsHeight(t *testing.T) {
	reg := NewRegistry()
	// Inner body heights at the smallest permitted terminals (h - statusLineRows
	// - 2*(borderSize+vPadding)); the overlay must fit within each.
	for _, height := range []int{4, 5, 6, 7, 10, 21} {
		ov := buildHelpOverlay(reg, i18n.TranslatorOrNop(nil), "en", 80, height)

		if ov.Height > height {
			t.Errorf("height %d: overlay height = %d; want <= %d", height, ov.Height, height)
		}
		if got := lipgloss.Height(ov.Content); got != ov.Height {
			t.Errorf("height %d: reported Height = %d; rendered = %d", height, ov.Height, got)
		}
	}
}

// TestBuildHelpOverlay_containsBindings asserts every built-in binding's keys and
// description appear in the rendered modal, so the help is registry-driven rather
// than hardcoded.
func TestBuildHelpOverlay_containsBindings(t *testing.T) {
	reg := NewRegistry()
	ov := buildHelpOverlay(reg, i18n.TranslatorOrNop(nil), "en", 80, 24)

	for _, sec := range reg.Sections() {
		if !strings.Contains(ov.Content, sec.Name) {
			t.Errorf("help modal missing section %q", sec.Name)
		}
		for _, e := range sec.Entries {
			if !strings.Contains(ov.Content, e.Binding.Desc) {
				t.Errorf("help modal missing description %q", e.Binding.Desc)
			}
			for _, k := range e.Binding.Keys {
				if !strings.Contains(ov.Content, k) {
					t.Errorf("help modal missing key %q", k)
				}
			}
		}
	}
}

// TestBuildHelpOverlay_nilTranslator guards the nil-Translator path: a nil tr
// must not panic (it falls back to a NopTranslator).
func TestBuildHelpOverlay_nilTranslator(t *testing.T) {
	reg := NewRegistry()
	ov := buildHelpOverlay(reg, nil, "en", 80, 24)
	if ov.Content == "" {
		t.Fatal("nil translator should still render help content")
	}
}

// TestBuildHelpOverlay_aliasesHiddenFromHelp locks the dispatch-vs-display
// split: Binding.Aliases dispatch (Match resolves them) but are absent from the
// rendered help modal, while Binding.Keys appear in the modal. It also pins that
// "esc" is NOT a quit key/alias — it must never resolve to an action (esc only
// closes overlays, handled by the frame before the registry; see NewRegistry).
func TestBuildHelpOverlay_aliasesHiddenFromHelp(t *testing.T) {
	reg := NewRegistry()

	// "esc" must not be a registry action — it never quits the TUI.
	if a, ok := reg.Match("esc"); ok {
		t.Fatalf("Match(\"esc\") resolved to %q; esc must not be a registry action", a)
	}

	// Verify display: build the help overlay and strip ANSI for comparison.
	ov := buildHelpOverlay(reg, i18n.TranslatorOrNop(nil), "en", 80, 24)
	content := stripANSI(ov.Content)

	// Canonical keys must appear in the modal.
	for _, k := range []string{"q", "ctrl+c"} {
		if !strings.Contains(content, k) {
			t.Errorf("help modal missing canonical key %q for ActionQuit", k)
		}
	}

	// "esc" must NOT appear in the modal as a key token. Tokenize on whitespace
	// and the key separator so a future description that merely contains the
	// substring "esc" (e.g. "Describe", "Reset") cannot false-trip this — only a
	// standalone "esc" token (i.e. a leaked key) fails.
	if helpHasToken(content, "esc") {
		t.Errorf("help modal contains %q as a token; esc is not a quit key", "esc")
	}

	// Verify the generic alias mechanism via a plugin-registered action: an alias
	// dispatches through Match but stays hidden from the help modal.
	const actionTest Action = "test.action"
	if err := reg.Register(actionTest, Binding{
		Keys:    []string{"x"},
		Aliases: []string{"y"},
		Desc:    "Test action",
		Section: "Test",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ov2 := buildHelpOverlay(reg, i18n.TranslatorOrNop(nil), "en", 80, 24)
	content2 := stripANSI(ov2.Content)

	if !helpHasToken(content2, "x") {
		t.Error("help modal missing canonical key \"x\"")
	}
	if helpHasToken(content2, "y") {
		t.Error("help modal contains alias \"y\"; aliases must be hidden from help")
	}
	// Alias "y" must still dispatch.
	if a, ok := reg.Match("y"); !ok || a != actionTest {
		t.Errorf("Match(\"y\") = %q, %v; want %q, true", a, ok, actionTest)
	}
}
