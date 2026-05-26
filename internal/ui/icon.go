package ui

import "github.com/charmbracelet/lipgloss"

// variationSelector16 (U+FE0F) forces emoji presentation for a preceding
// codepoint that has a text-default Unicode presentation.
const variationSelector16 = '️'

// NormalizeIcon ensures a single-codepoint emoji icon includes the Variation
// Selector-16 so that every terminal — and lipgloss.Width — agrees the glyph
// occupies two cells.
//
// Background: codepoints like U+1F6E2 (🛢), U+2699 (⚙) and many of the
// "miscellaneous symbol" blocks have Emoji_Presentation = No. Most modern
// terminals still render them as 2-cell emoji, but lipgloss.Width reports 1
// for the bare codepoint. That mismatch breaks our column alignment and bleeds
// the row's background style into the cell after the icon. Appending VS16
// fixes the lipgloss measurement and pins emoji presentation in terminals
// that honour it.
//
// Multi-rune icons (already-VS16-suffixed, ZWJ sequences like 🧑‍💻) are left
// untouched. ASCII icons are left untouched.
func NormalizeIcon(icon string) string {
	if icon == "" {
		return icon
	}
	runes := []rune(icon)
	if len(runes) != 1 {
		return icon
	}
	r := runes[0]
	if r < 0x80 {
		return icon
	}
	if lipgloss.Width(icon) >= 2 {
		return icon
	}
	return icon + string(variationSelector16)
}

// IconPrefix returns the normalised icon followed by a single space, or ""
// when the icon is empty. Centralises the "icon + space" prefix used by the
// status table, multi-select labels, and the info-dashboard service subgroups.
func IconPrefix(icon string) string {
	if icon == "" {
		return ""
	}
	return NormalizeIcon(icon) + " "
}
