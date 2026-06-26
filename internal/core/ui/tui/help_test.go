package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/shared/i18n"

	"charm.land/lipgloss/v2"
)

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
