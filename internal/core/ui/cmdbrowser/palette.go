// Package cmdbrowser palette.go is the only sanctioned bridge between the
// lipgloss v1 palette defined in internal/core/ui and the charm.land/lipgloss/v2
// styles required by bubbles/v2. It reads raw color strings via the
// styles.Color*() accessors and constructs v2 lipgloss styles locally — v1 styles
// are never imported here, and v2 styles never leak outside cmdbrowser.
package cmdbrowser

import (
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
)

// fgStyle is the shared constructor behind every palette accessor: a fresh v2
// lipgloss style whose only set field is the foreground color. Centralizing it
// keeps the v1→v2 bridge in one place and the accessors as thin color slots.
func fgStyle(color string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color))
}

// paletteFocusBorder returns the v2 lipgloss style used for focused-panel
// borders in the two-panel command browser.
func paletteFocusBorder() lipgloss.Style {
	return fgStyle(styles.ColorAccent())
}

// paletteDescription returns the v2 lipgloss style used for secondary
// description text (item subtitles, faint captions).
func paletteDescription() lipgloss.Style {
	return fgStyle(styles.ColorMuted())
}

// paletteTreeCount returns the v2 lipgloss style used for "(N)" counters in
// the left tree.
func paletteTreeCount() lipgloss.Style {
	return fgStyle(styles.ColorMuted())
}

// paletteTreeArrow returns the v2 lipgloss style used for tree disclosure
// glyphs (▸/▾).
func paletteTreeArrow() lipgloss.Style {
	return fgStyle(styles.ColorMuted())
}

// paletteKey returns the v2 lipgloss style used for high-prominence labels
// (titles, breadcrumb path, filter query header). Bold is intentionally NOT
// applied here so callers can compose it (`paletteKey().Bold(true)`).
func paletteKey() lipgloss.Style {
	return fgStyle(styles.ColorAccent())
}

// paletteSuccess returns the v2 lipgloss style used for success/enabled
// accents (e.g. the "[--yes ON]" toggle).
func paletteSuccess() lipgloss.Style {
	return fgStyle(styles.ColorSuccess())
}

// paletteFilterMatch returns the v2 lipgloss style used to highlight
// characters matched by the active filter inside the command list.
func paletteFilterMatch() lipgloss.Style {
	return fgStyle(styles.ColorAccent())
}

// palettePaginationActive returns the v2 lipgloss style for the active
// pagination dot.
func palettePaginationActive() lipgloss.Style {
	return fgStyle(styles.ColorAccent())
}

// palettePaginationInactive returns the v2 lipgloss style for inactive
// pagination dots.
func palettePaginationInactive() lipgloss.Style {
	return fgStyle(styles.ColorMuted())
}

// paginationDotGlyph is the bullet character bubbles/v2 uses for pagination
// dots; mirroring it here keeps applyListStyles in sync with list.DefaultStyles.
const paginationDotGlyph = "•"

// applyListStyles overwrites the palette-driven fields on a bubbles/v2 list.Model.
// The list keeps its DefaultStyles for everything else; only the slots that map
// onto our exposed Color*() accessors are replaced.
func applyListStyles(l *list.Model) {
	s := l.Styles
	s.ActivePaginationDot = fgStyle(styles.ColorAccent()).SetString(paginationDotGlyph)
	s.InactivePaginationDot = fgStyle(styles.ColorMuted()).SetString(paginationDotGlyph)
	s.DefaultFilterCharacterMatch = fgStyle(styles.ColorAccent()).Underline(true)
	s.NoItems = fgStyle(styles.ColorMuted())
	l.Styles = s
}

// applyItemStyles overwrites the palette-driven fields on
// list.DefaultItemStyles. The cmdbrowser uses a custom delegate today, but the
// helper is exposed so the same palette wiring applies if a future caller
// switches to list.NewDefaultDelegate.
func applyItemStyles(s *list.DefaultItemStyles) {
	desc := lipgloss.Color(styles.ColorMuted())
	s.NormalDesc = s.NormalDesc.Foreground(desc)
	s.SelectedDesc = s.SelectedDesc.Foreground(desc)
	s.DimmedDesc = s.DimmedDesc.Foreground(desc)
	s.FilterMatch = fgStyle(styles.ColorAccent()).Underline(true)
}

// applyHelpStyles overwrites the palette-driven fields on a bubbles/v2
// help.Styles. Key labels pick up the accent color (high-prominence keys);
// descriptions and separators share the muted color for a secondary look.
func applyHelpStyles(s *help.Styles) {
	key := fgStyle(styles.ColorAccent())
	desc := fgStyle(styles.ColorMuted())
	s.ShortKey = key
	s.FullKey = key
	s.ShortDesc = desc
	s.FullDesc = desc
	s.ShortSeparator = desc
	s.FullSeparator = desc
	s.Ellipsis = desc
}

// applyViewportStyles overwrites the palette-driven fields on a bubbles/v2
// viewport.Model. Foreground is intentionally left unset on vp.Style:
// bubbles/viewport applies Style.Render around the entire visible content
// (see viewport.View()), so any baseline foreground here bleeds onto every
// unstyled segment — including the value bodies of inspect definitions,
// which use `styleText` (NoColor → terminal default) so they read in the
// natural foreground. Setting Muted here previously made value bodies and
// word-wrap continuations look dimmer than their labels. The inspect overlay
// does not currently use SetHighlights, but HighlightStyle is wired so a
// future "find in inspect" feature picks up the palette without further
// changes.
func applyViewportStyles(vp *viewport.Model) {
	vp.Style = lipgloss.NewStyle()
	vp.HighlightStyle = fgStyle(styles.ColorAccent()).Reverse(true)
}
