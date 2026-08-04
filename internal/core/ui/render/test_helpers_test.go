package render

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// resetStyles re-initialises the styles package palette to the built-in
// defaults for the current dark/light mode. Provided here so the renderer
// tests still co-located in package ui can reset palette state via the
// styles package's public API.
func resetStyles() {
	styles.ApplyStyles(nil)
	styles.DefSep = "—"
}

// pinGoldenPalette pins the ANSI color profile to TrueColor and the
// background mode to dark before calling resetStyles, then restores both via
// t.Cleanup. Byte-exact golden comparisons need both pinned: resetStyles
// alone resolves the palette through lipgloss.HasDarkBackground(), and
// zebraBackground (diagnostics_table.go) is a lipgloss.AdaptiveColor — so the
// same golden would hold different ANSI values on a light versus dark
// terminal without this. Tests using it must not call t.Parallel(): it
// mutates package-level lipgloss/styles state for its duration.
func pinGoldenPalette(t *testing.T) {
	t.Helper()
	savedProfile := lipgloss.ColorProfile()
	savedDark := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(savedProfile)
		lipgloss.SetHasDarkBackground(savedDark)
	})
	resetStyles()
}
