package ui

import (
	"image/color"
	"testing"

	huh "charm.land/huh/v2"
	lipgloss "charm.land/lipgloss/v2"

	"devbox-cli/internal/config"
)

func TestThemeNonNilByDefault(t *testing.T) {
	th := Theme()
	if th == nil {
		t.Fatal("Theme() returned nil before ApplyStyles")
	}
}

func TestThemeNonNilAfterApplyStylesNil(t *testing.T) {
	original := huhTheme
	t.Cleanup(func() { huhTheme = original })

	ApplyStyles(nil)
	th := Theme()
	if th == nil {
		t.Fatal("Theme() returned nil after ApplyStyles(nil)")
	}
}

func TestThemeReturnsNonNilStyles(t *testing.T) {
	original := huhTheme
	t.Cleanup(func() { huhTheme = original })

	ApplyStyles(nil)
	th := Theme()

	light := th.Theme(false)
	if light == nil {
		t.Fatal("Theme().Theme(false) returned nil")
	}

	dark := th.Theme(true)
	if dark == nil {
		t.Fatal("Theme().Theme(true) returned nil")
	}
}

func TestApplyStylesPaletteReflected(t *testing.T) {
	original := huhTheme
	t.Cleanup(func() { huhTheme = original })

	cfg := &config.StylesConfig{
		Colors: config.StylesColors{
			// ANSI 256 color "6" (cyan) — used as section_title
			SectionTitle: "6",
		},
	}
	ApplyStyles(cfg)

	th := Theme()
	// Focused.Title foreground should reflect the section_title palette entry.
	// Verified field: Focused.Title is a lipgloss.Style set by buildPaletteApplier
	// via s.Focused.Title.Foreground(lipgloss.Color("6")).Bold(true).
	styles := th.Theme(false)
	got := styles.Focused.Title.GetForeground()

	want := lipgloss.Color("6") // returns ansi.BasicColor(6)
	if got != want {
		t.Errorf("Focused.Title foreground = %v (%T), want %v (%T)", got, got, want, want)
	}
}

func TestApplyStylesGroupTitlePaletteReflected(t *testing.T) {
	original := huhTheme
	t.Cleanup(func() { huhTheme = original })

	cfg := &config.StylesConfig{
		Colors: config.StylesColors{
			SectionTitle: "6",
		},
	}
	ApplyStyles(cfg)

	styles := Theme().Theme(false)
	got := styles.Group.Title.GetForeground()
	want := lipgloss.Color("6")
	if got != want {
		t.Errorf("Group.Title foreground = %v (%T), want %v (%T)", got, got, want, want)
	}
}

func TestBuildPaletteApplierNilNoOp(t *testing.T) {
	apply := buildPaletteApplier(nil)
	s := huh.ThemeBase(false)
	// Should not panic.
	apply(s)
}

func TestBuildPaletteApplierEmptyColorsNoOp(t *testing.T) {
	apply := buildPaletteApplier(&config.StylesColors{})
	s := huh.ThemeBase(false)
	// Calling apply with an empty config should not change any foreground colors
	// on fields whose color comes from ThemeBase (they should stay as zero/default).
	apply(s)
	// The test verifies the applier runs without panicking.
}

func TestBuildPaletteApplierAllFields(t *testing.T) {
	c := &config.StylesColors{
		SectionTitle: "6",
		SubHeader:    "3",
		Label:        "12",
		Muted:        "8",
		Enabled:      "2",
		Warning:      "1",
		Info:         "14",
	}
	apply := buildPaletteApplier(c)
	s := huh.ThemeBase(false)
	apply(s)

	tests := []struct {
		name string
		got  color.Color
		want color.Color
	}{
		{"Focused.Title", s.Focused.Title.GetForeground(), lipgloss.Color("6")},
		{"Group.Title", s.Group.Title.GetForeground(), lipgloss.Color("6")},
		{"Focused.Description", s.Focused.Description.GetForeground(), lipgloss.Color("3")},
		{"Group.Description", s.Group.Description.GetForeground(), lipgloss.Color("3")},
		{"Focused.SelectSelector", s.Focused.SelectSelector.GetForeground(), lipgloss.Color("12")},
		{"Focused.MultiSelectSelector", s.Focused.MultiSelectSelector.GetForeground(), lipgloss.Color("12")},
		{"Focused.Option", s.Focused.Option.GetForeground(), lipgloss.Color("12")},
		{"Blurred.Title", s.Blurred.Title.GetForeground(), lipgloss.Color("8")},
		{"Blurred.Description", s.Blurred.Description.GetForeground(), lipgloss.Color("8")},
		{"Focused.UnselectedOption", s.Focused.UnselectedOption.GetForeground(), lipgloss.Color("8")},
		{"Focused.SelectedOption", s.Focused.SelectedOption.GetForeground(), lipgloss.Color("2")},
		{"Focused.SelectedPrefix", s.Focused.SelectedPrefix.GetForeground(), lipgloss.Color("2")},
		{"Focused.ErrorIndicator", s.Focused.ErrorIndicator.GetForeground(), lipgloss.Color("1")},
		{"Focused.ErrorMessage", s.Focused.ErrorMessage.GetForeground(), lipgloss.Color("1")},
		{"Focused.NextIndicator", s.Focused.NextIndicator.GetForeground(), lipgloss.Color("14")},
		{"Focused.PrevIndicator", s.Focused.PrevIndicator.GetForeground(), lipgloss.Color("14")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v (%T), want %v (%T)", tt.got, tt.got, tt.want, tt.want)
			}
		})
	}
}
