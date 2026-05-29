package styles

import (
	"github.com/rivo/uniseg"
)

// variationSelector16 (U+FE0F) forces emoji presentation for a preceding
// codepoint that has a text-default Unicode presentation.
const variationSelector16 = '️'

// IsAmbiguousWidthIcon reports whether icon is a single-grapheme emoji whose
// base codepoint has Emoji_Presentation = No, meaning it depends on VS16 to
// render as a 2-cell emoji. Even with VS16 appended, terminals are known to
// disagree on the actual cell width of these glyphs (e.g. 🛢 U+1F6E2,
// 🗂 U+1F5C2, ⚙ U+2699). Render code should treat ambiguous icons as
// unusable; SafeIcon drops them so column alignment stays correct.
//
// Detection logic:
//   - Empty / ASCII (single byte < 0x80) → safe.
//   - More than one grapheme cluster (e.g. ZWJ sequences like 🧑‍💻, flag
//     pairs like 🇺🇸 — counted as 1 cluster — are still handled by the
//     width check below; this branch catches multi-cluster strings that are
//     not single icons) → safe (out of scope for this check).
//   - Otherwise: peel a trailing VS16 if present and check the raw base
//     width. Width 1 means the base codepoint is text-default and rendering
//     depends on VS16 — terminals disagree on whether they honour VS16, so
//     we mark it ambiguous regardless of whether the input already carries
//     VS16.
func IsAmbiguousWidthIcon(icon string) bool {
	if icon == "" {
		return false
	}
	if len(icon) == 1 && icon[0] < 0x80 {
		return false
	}
	if uniseg.GraphemeClusterCount(icon) != 1 {
		return false
	}
	base := stripTrailingVS16(icon)
	return uniseg.StringWidth(base) == 1
}

// stripTrailingVS16 removes a trailing U+FE0F from icon, if present. Used so
// IsAmbiguousWidthIcon classifies "⚙" and "⚙️" identically: both depend on
// terminal VS16 behaviour.
func stripTrailingVS16(icon string) string {
	runes := []rune(icon)
	if len(runes) >= 2 && runes[len(runes)-1] == variationSelector16 {
		return string(runes[:len(runes)-1])
	}
	return icon
}

// SafeIcon returns the icon to render. For safe icons (ASCII, ZWJ sequences,
// or single-grapheme emojis with Emoji_Presentation = Yes), this is the icon
// unchanged. For ambiguous icons (see IsAmbiguousWidthIcon), this is the
// empty string — the icon is dropped so column-aligned UIs (status tables,
// multi-select menus, info-dashboard sections) don't break when the terminal
// disagrees with uniseg about the glyph's width.
func SafeIcon(icon string) string {
	if IsAmbiguousWidthIcon(icon) {
		return ""
	}
	return icon
}

// IconPrefix returns the safe icon followed by a single space, or "" when the
// icon is empty or ambiguous. Centralises the "icon + space" prefix used by
// the status table, multi-select labels, and the info-dashboard service
// subgroups.
func IconPrefix(icon string) string {
	safe := SafeIcon(icon)
	if safe == "" {
		return ""
	}
	return safe + " "
}
