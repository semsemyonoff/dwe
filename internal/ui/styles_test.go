package ui

import (
	"strings"
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
	styleCatTool = lipgloss.NewStyle().Foreground(lipgloss.Color("67"))
	styleCatInfra = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleTableBorder = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleTableHeader = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	colorFocusBorder = "12"
	colorDescription = "8"
	colorTreeCount = "8"
	colorTreeArrow = "6"
	colorFilterMatch = "12"
	colorPaginationActive = "12"
	colorPaginationInactive = "8"
	colorKey = "12"
	colorInfo = "12"
	colorSuccess = "2"
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
	ApplyStyles(cfg)
	if styleKey.GetForeground() != lipgloss.Color("203") {
		t.Errorf("styleKey foreground: got %v, want 203", styleKey.GetForeground())
	}
	if styleSectionTitle.GetForeground() != lipgloss.Color("167") {
		t.Errorf("styleSectionTitle foreground: got %v, want 167", styleSectionTitle.GetForeground())
	}
	if styleMuted.GetForeground() != lipgloss.Color("245") {
		t.Errorf("styleMuted foreground: got %v, want 245", styleMuted.GetForeground())
	}
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

func TestServiceInactiveStyle_DimmedAndNotBold(t *testing.T) {
	resetStyles()
	st := styleInactiveService()
	if !st.GetFaint() {
		t.Error("inactive service style should be faint")
	}
	if st.GetBold() {
		t.Error("inactive service style should not be bold")
	}
	if st.GetForeground() != styleMuted.GetForeground() {
		t.Errorf("inactive service foreground = %v, want muted foreground %v", st.GetForeground(), styleMuted.GetForeground())
	}
}

func TestServiceOptionStyles_UseForegroundOnlyReset(t *testing.T) {
	resetStyles()
	out := StyleServiceOptionName("app", "main")
	if out == "main" {
		t.Fatal("expected ANSI styling")
	}
	if !strings.Contains(out, "\x1b[38;5;6m") {
		t.Errorf("expected app foreground color, got %q", out)
	}
	if strings.Contains(out, "\x1b[0m") {
		t.Errorf("option styles must not emit full reset, got %q", out)
	}
	if !strings.Contains(out, "\x1b[39m") {
		t.Errorf("option styles should reset foreground only, got %q", out)
	}

	tool := StyleServiceOptionName("tool", "adminer")
	if !strings.Contains(tool, "\x1b[38;5;67m") {
		t.Errorf("expected neutral tool foreground color, got %q", tool)
	}
}

func TestPaletteAccessors_Defaults(t *testing.T) {
	resetStyles()
	cases := []struct {
		name string
		got  func() string
		want string
	}{
		{"FocusBorder", ColorFocusBorder, "12"},
		{"Description", ColorDescription, "8"},
		{"TreeCount", ColorTreeCount, "8"},
		{"TreeArrow", ColorTreeArrow, "6"},
		{"FilterMatch", ColorFilterMatch, "12"},
		{"PaginationActive", ColorPaginationActive, "12"},
		{"PaginationInactive", ColorPaginationInactive, "8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.got(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestApplyStyles_CmdbrowserPalette_Applied(t *testing.T) {
	resetStyles()
	cfg := &config.StylesConfig{
		Colors: config.StylesColors{
			FocusBorder:        "203",
			Description:        "245",
			TreeCount:          "240",
			TreeArrow:          "167",
			FilterMatch:        "214",
			PaginationActive:   "210",
			PaginationInactive: "239",
		},
	}
	ApplyStyles(cfg)
	cases := []struct {
		name string
		got  func() string
		want string
	}{
		{"FocusBorder", ColorFocusBorder, "203"},
		{"Description", ColorDescription, "245"},
		{"TreeCount", ColorTreeCount, "240"},
		{"TreeArrow", ColorTreeArrow, "167"},
		{"FilterMatch", ColorFilterMatch, "214"},
		{"PaginationActive", ColorPaginationActive, "210"},
		{"PaginationInactive", ColorPaginationInactive, "239"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.got(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestApplyStyles_CmdbrowserPalette_EmptyPreservesDefaults(t *testing.T) {
	resetStyles()
	cfg := &config.StylesConfig{
		Colors: config.StylesColors{
			FocusBorder: "203",
			// Description, TreeCount, etc. intentionally empty.
		},
	}
	ApplyStyles(cfg)
	if ColorFocusBorder() != "203" {
		t.Errorf("FocusBorder: got %q, want 203", ColorFocusBorder())
	}
	if ColorDescription() != "8" {
		t.Errorf("Description should remain default '8', got %q", ColorDescription())
	}
	if ColorTreeArrow() != "6" {
		t.Errorf("TreeArrow should remain default '6', got %q", ColorTreeArrow())
	}
}

func TestTermWidth_ReturnsPositive(t *testing.T) {
	w := TermWidth()
	if w <= 0 {
		t.Errorf("expected positive terminal width, got %d", w)
	}
}
