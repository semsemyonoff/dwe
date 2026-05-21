// Package cmdbrowser palette.go is the only sanctioned bridge between the
// lipgloss v1 palette defined in internal/ui and the charm.land/lipgloss/v2
// styles required by bubbles/v2. It reads raw color strings via the
// ui.Color*() accessors and constructs v2 lipgloss styles locally — v1 styles
// are never imported here, and v2 styles never leak outside cmdbrowser.
package cmdbrowser

import (
	"charm.land/lipgloss/v2"

	"devbox-cli/internal/ui"
)

// paletteFocusBorder returns the v2 lipgloss style used for focused-panel
// borders in the two-panel command browser.
func paletteFocusBorder() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(ui.ColorFocusBorder()))
}

// paletteDescription returns the v2 lipgloss style used for secondary
// description text (item subtitles, faint captions).
func paletteDescription() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(ui.ColorDescription()))
}

// paletteTreeCount returns the v2 lipgloss style used for "(N)" counters in
// the left tree.
func paletteTreeCount() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(ui.ColorTreeCount()))
}

// paletteTreeArrow returns the v2 lipgloss style used for tree disclosure
// glyphs (▸/▾).
func paletteTreeArrow() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(ui.ColorTreeArrow()))
}

// paletteFilterMatch returns the v2 lipgloss style used to highlight
// characters matched by the active filter inside the command list.
func paletteFilterMatch() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(ui.ColorFilterMatch()))
}

// palettePaginationActive returns the v2 lipgloss style for the active
// pagination dot.
func palettePaginationActive() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(ui.ColorPaginationActive()))
}

// palettePaginationInactive returns the v2 lipgloss style for inactive
// pagination dots.
func palettePaginationInactive() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(ui.ColorPaginationInactive()))
}
