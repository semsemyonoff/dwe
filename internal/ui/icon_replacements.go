package ui

// ambiguousIconReplacements maps a problematic base codepoint (an emoji whose
// Emoji_Presentation = No — e.g. U+1F6E2 🛢, U+2699 ⚙) to safe alternatives
// that all terminals render as 2 cells. Keys are the bare codepoint without
// VS16; SuggestSafeIcons strips a trailing VS16 from its argument before
// lookup so "⚙" and "⚙️" hit the same entry.
//
// Curated by hand for the dev-environment vocabulary (services, tools, files,
// network, mail, etc.). Add an entry when a new ambiguous icon shows up in
// user configs; the iconReplacementsAreSafe test guards that every alt passes
// !IsAmbiguousWidthIcon, so a bad replacement is caught at test time.
// Note: VS16-suffixed forms (e.g. ⚙️, 🛠️) are NOT safe alternatives —
// IsAmbiguousWidthIcon strips VS16 before checking, so the VS16 form has the
// same terminal-rendering risk as the bare codepoint. Every entry below must
// be a codepoint with Emoji_Presentation = Yes (raw uniseg width = 2). The
// iconReplacementsAreSafe test enforces this invariant.
var ambiguousIconReplacements = map[string][]string{
	// Storage / containers
	"🛢": {"🪣", "📦", "💾"}, // oil drum → bucket / package / floppy
	"🗂": {"📁", "📂"},      // card index dividers → folder

	// Tools / settings
	"⚙": {"🔧", "🔨"}, // gear → wrench / hammer
	"⚒": {"🔨", "🔧"}, // hammer & pick → hammer / wrench
	"⛏": {"🔨"},      // pickaxe → hammer
	"⛓": {"🔗"},      // chains → link
	"✂": {"🪓"},      // scissors → axe
	"✏": {"📝"},      // pencil → memo
	"✒": {"📝"},      // black nib → memo

	// Time
	"⏱": {"⏰"}, // stopwatch → alarm clock
	"⏲": {"⏰"}, // timer clock → alarm clock

	// Mail / phone
	"✉": {"📧", "📨", "📩"}, // envelope → e-mail / incoming / outgoing
	"☎": {"📞", "📱"},      // telephone → receiver / mobile

	// Weather / nature
	"☀": {"🌞", "🔆"}, // sun → sun-with-face / high-brightness
	"☁": {"⛅"},      // cloud → sun-behind-cloud
	"❄": {"⛄"},      // snowflake → snowman
	"☂": {"☔"},      // umbrella → umbrella with rain
	"☘": {"🍀"},      // shamrock → four-leaf clover
	"♨": {"🔥"},      // hot springs → fire

	// Alerts / status
	"⚠": {"🚨", "❗"}, // warning → siren / exclamation
	"☑": {"✅"},      // checkbox → check mark
	"☒": {"❌"},      // ballot box with x → cross mark
	"♻": {"🌱"},      // recycle → seedling

	// Travel
	"✈": {"🛫", "🛬"}, // airplane → take-off / landing
}

// SuggestSafeIcons returns up to limit safe replacement codepoints for icon.
// Returns nil when icon is not in the curated replacement map. Every entry in
// the map is a codepoint with Emoji_Presentation = Yes, so the returned
// strings can be embedded directly in user-facing messages without further
// processing.
//
// limit ≤ 0 returns all curated alternatives.
func SuggestSafeIcons(icon string, limit int) []string {
	base := stripTrailingVS16(icon)
	alts, ok := ambiguousIconReplacements[base]
	if !ok || len(alts) == 0 {
		return nil
	}
	if limit <= 0 || limit > len(alts) {
		limit = len(alts)
	}
	out := make([]string, limit)
	copy(out, alts[:limit])
	return out
}
