package ui

import (
	huh "charm.land/huh/v2"
	lipgloss "charm.land/lipgloss/v2"

	"devbox-cli/internal/config"
)

// huhTheme is the package-level huh.Theme built from devbox/styles.yml.
// It defaults to ThemeBase + devbox glyph overrides (no project palette
// applied) until ApplyStyles is called.
var huhTheme huh.Theme = huh.ThemeFunc(func(isDark bool) *huh.Styles {
	s := huh.ThemeBase(isDark)
	applyFormGlyphs(s)
	return s
})

// applyFormGlyphs replaces the default huh prefix glyphs with the devbox look:
// "✓ " for selected items, "• " for unselected. Coloring is handled separately
// by buildPaletteApplier so the glyphs always render even without a palette.
func applyFormGlyphs(s *huh.Styles) {
	s.Focused.SelectedPrefix = s.Focused.SelectedPrefix.SetString("✓ ")
	s.Focused.UnselectedPrefix = s.Focused.UnselectedPrefix.SetString("• ")
	s.Blurred.SelectedPrefix = s.Blurred.SelectedPrefix.SetString("✓ ")
	s.Blurred.UnselectedPrefix = s.Blurred.UnselectedPrefix.SetString("• ")
}

// Theme returns the current package-level huh.Theme.
// All huh form/field call sites should use .WithTheme(ui.Theme()) so they
// automatically pick up palette changes from styles.yml.
func Theme() huh.Theme {
	return huhTheme
}

// buildPaletteApplier returns a function that applies project palette colors
// to a *huh.Styles in place. The returned function is safe to call multiple
// times on different *huh.Styles values (no shared state).
//
// Palette mapping (StylesColors → *huh.Styles):
//   - section_title → Focused.Title, Group.Title         (form/group title color)
//   - subheader     → Focused.Description, Group.Description
//   - label         → Focused.SelectSelector, Focused.MultiSelectSelector, Focused.Option
//   - muted         → Blurred.Title, Blurred.Description, Focused.UnselectedOption,
//     Focused.TextInput.Placeholder, Help styles
//   - enabled       → Focused.SelectedOption, Focused.SelectedPrefix (multi-select checked)
//   - warning       → Focused.ErrorIndicator, Focused.ErrorMessage
//   - info          → Focused.TextInput.Prompt, Focused.NextIndicator, Focused.PrevIndicator
func buildPaletteApplier(c *config.StylesColors) func(*huh.Styles) {
	return func(s *huh.Styles) {
		if c == nil {
			return
		}
		if c.SectionTitle != "" {
			col := lipgloss.Color(c.SectionTitle)
			s.Focused.Title = s.Focused.Title.Foreground(col).Bold(true)
			s.Group.Title = s.Group.Title.Foreground(col).Bold(true)
		}
		if c.SubHeader != "" {
			col := lipgloss.Color(c.SubHeader)
			s.Focused.Description = s.Focused.Description.Foreground(col)
			s.Group.Description = s.Group.Description.Foreground(col)
		}
		if c.Label != "" {
			col := lipgloss.Color(c.Label)
			s.Focused.SelectSelector = s.Focused.SelectSelector.Foreground(col)
			s.Focused.MultiSelectSelector = s.Focused.MultiSelectSelector.Foreground(col)
			s.Focused.Option = s.Focused.Option.Foreground(col)
		}
		if c.Muted != "" {
			col := lipgloss.Color(c.Muted)
			s.Blurred.Title = s.Blurred.Title.Foreground(col)
			s.Blurred.Description = s.Blurred.Description.Foreground(col)
			s.Focused.UnselectedOption = s.Focused.UnselectedOption.Foreground(col)
			s.Focused.UnselectedPrefix = s.Focused.UnselectedPrefix.Foreground(col)
			s.Focused.TextInput.Placeholder = s.Focused.TextInput.Placeholder.Foreground(col)
		}
		if c.Enabled != "" {
			col := lipgloss.Color(c.Enabled)
			s.Focused.SelectedOption = s.Focused.SelectedOption.Foreground(col)
			s.Focused.SelectedPrefix = s.Focused.SelectedPrefix.Foreground(col)
		}
		if c.Warning != "" {
			col := lipgloss.Color(c.Warning)
			s.Focused.ErrorIndicator = s.Focused.ErrorIndicator.Foreground(col)
			s.Focused.ErrorMessage = s.Focused.ErrorMessage.Foreground(col)
		}
		if c.Info != "" {
			col := lipgloss.Color(c.Info)
			s.Focused.TextInput.Prompt = s.Focused.TextInput.Prompt.Foreground(col)
			s.Focused.NextIndicator = s.Focused.NextIndicator.Foreground(col)
			s.Focused.PrevIndicator = s.Focused.PrevIndicator.Foreground(col)
		}
	}
}
