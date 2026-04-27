package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"

	"devbox-cli/internal/config"
)

// resetStyles restores all package-level style vars to their hardcoded defaults.
// This prevents test state from bleeding between tests.
func resetStyles() {
	styleKey = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	styleSectionTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	styleSubheader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	styleMuted = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleInfoText = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	styleValue = lipgloss.NewStyle()
	styleEnabled = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleDisabled = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleMandatory = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	stylePartial = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleRunStopped = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleCatService = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleCatTool = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	styleCatInfra = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleTableBorder = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleTableHeader = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	defSep = "—"
}

func TestApplyStyles_Nil(t *testing.T) {
	resetStyles()
	ApplyStyles(nil)
	if defSep != "—" {
		t.Errorf("nil cfg must not change defSep: got %q", defSep)
	}
}

func TestApplyStyles_Empty(t *testing.T) {
	resetStyles()
	ApplyStyles(&config.StylesConfig{})
	if defSep != "—" {
		t.Errorf("empty cfg must not change defSep: got %q", defSep)
	}
}

func TestApplyStyles_Separator(t *testing.T) {
	resetStyles()
	ApplyStyles(&config.StylesConfig{Separator: ":"})
	if defSep != ":" {
		t.Errorf("expected defSep to be ':', got %q", defSep)
	}
}

func TestApplyStyles_Colors_Applied(t *testing.T) {
	resetStyles()
	cfg := &config.StylesConfig{
		Colors: config.StylesColors{
			Label:        "203",
			SectionTitle: "167",
			SubHeader:    "209",
			Muted:        "245",
			Warning:      "214",
			Info:         "210",
			Enabled:      "2",
			Disabled:     "245",
			Mandatory:    "203",
			Partial:      "214",
			TableBorder:  "167",
			TableHeader:  "203",
		},
	}
	// Should not panic and styles should be rebuilt.
	ApplyStyles(cfg)
}

func TestApplyStyles_PartialColors_PreservesDefault(t *testing.T) {
	resetStyles()
	// Only set muted; separator must stay at default.
	cfg := &config.StylesConfig{
		Colors: config.StylesColors{
			Muted: "245",
		},
	}
	ApplyStyles(cfg)
	if defSep != "—" {
		t.Errorf("expected defSep to remain default '—', got %q", defSep)
	}
}

func TestApplyStyles_ChangesAreVisible(t *testing.T) {
	resetStyles()
	cfg := &config.StylesConfig{
		Separator: ">>",
		Colors: config.StylesColors{
			Muted: "245",
		},
	}
	ApplyStyles(cfg)
	if defSep != ">>" {
		t.Errorf("expected defSep '>>', got %q", defSep)
	}
}

func TestStyleHelpers_NonEmpty(t *testing.T) {
	resetStyles()
	cases := []struct {
		name string
		fn   func(string) string
	}{
		{"RenderEnabled", RenderEnabled},
		{"RenderPartial", RenderPartial},
		{"RenderStopped", RenderStopped},
		{"StyleKey", StyleKey},
		{"StyleGroup", StyleGroup},
		{"StyleSectionTitle", StyleSectionTitle},
		{"StyleSubheader", StyleSubheader},
		{"StyleMuted", StyleMuted},
		{"StyleInfo", StyleInfo},
		{"StyleFailed", StyleFailed},
		{"StyleWarning", StyleWarning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := tc.fn("test")
			if out == "" {
				t.Errorf("%s returned empty string", tc.name)
			}
		})
	}
}

func TestTermWidth_ReturnsPositive(t *testing.T) {
	w := TermWidth()
	if w <= 0 {
		t.Errorf("expected positive terminal width, got %d", w)
	}
}
