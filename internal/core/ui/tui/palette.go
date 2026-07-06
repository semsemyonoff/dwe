package tui

import (
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"

	"charm.land/lipgloss/v2"
)

// palette.go is the single styling bridge between the lipgloss v1 palette defined
// in internal/core/ui/styles and the charm.land/lipgloss/v2 styles the framework
// renders with. It reads raw color strings via the styles.Color*() accessors and
// builds v2 styles locally — v1 lipgloss styles are never imported here, mirroring
// cmdbrowser/palette.go.

// focusedBorder returns the v2 lipgloss style for a focused panel's border: a
// normal box border drawn in the accent color so the active panel stands out.
// The Width/Height are set by the caller ([Frame.View]) to the panel's outer
// region before rendering.
func focusedBorder() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(styles.ColorAccent()))
}

// unfocusedBorder returns the v2 lipgloss style for an inactive panel's border: a
// normal box border drawn in the muted border color. Identical geometry to
// [focusedBorder]; only the border color differs, so focusing a panel never
// shifts the layout.
func unfocusedBorder() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(styles.ColorBorder()))
}
