package styles

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsAmbiguousWidthIcon(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		icon string
		want bool
	}{
		{"empty", "", false},
		{"ascii letter", "a", false},
		{"ascii digit", "1", false},
		{"ascii symbol", "*", false},

		// Emoji_Presentation = Yes — safe.
		{"folder", "📁", false},
		{"books", "📚", false},
		{"key", "🔑", false},
		{"money bag", "💰", false},
		{"shopping cart", "🛒", false},
		{"whale", "🐳", false},
		{"package", "📦", false},
		{"floppy", "💾", false},
		{"wrench", "🔧", false},
		{"hammer", "🔨", false},
		{"alarm clock", "⏰", false},
		{"envelope email", "📧", false},

		// Emoji_Presentation = No — ambiguous (bare).
		{"oil drum bare", "🛢", true},
		{"card index bare", "🗂", true},
		{"gear bare", "⚙", true},
		{"warning bare", "⚠", true},
		{"sun bare", "☀", true},
		{"cloud bare", "☁", true},
		{"snowflake bare", "❄", true},
		{"envelope bare", "✉", true},
		{"telephone bare", "☎", true},
		{"stopwatch bare", "⏱", true},
		{"airplane bare", "✈", true},

		// Same codepoints with VS16 — still ambiguous (terminal disagreement
		// happens regardless of VS16).
		{"oil drum VS16", "🛢️", true},
		{"card index VS16", "🗂️", true},
		{"gear VS16", "⚙️", true},
		{"warning VS16", "⚠️", true},
		{"tools VS16", "🛠️", true},
		{"keyboard VS16", "⌨️", true},

		// Multi-cluster / ZWJ sequences — out of scope, classified as safe.
		// (Detection here is intentionally narrow; the wider rendering risk
		// for ZWJ is accepted as the cost of not over-flagging.)
		{"man technologist ZWJ", "🧑‍💻", false},
		{"flag US", "🇺🇸", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsAmbiguousWidthIcon(tt.icon),
				"icon %q (bytes=%d)", tt.icon, len(tt.icon))
		})
	}
}

func TestSafeIcon(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty stays empty", "", ""},
		{"ascii passthrough", "*", "*"},
		{"safe emoji passthrough", "📁", "📁"},
		{"ambiguous bare dropped", "🛢", ""},
		{"ambiguous VS16 dropped", "🗂️", ""},
		{"gear VS16 dropped", "⚙️", ""},
		{"gear bare dropped", "⚙", ""},
		{"ZWJ sequence passthrough", "🧑‍💻", "🧑‍💻"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, SafeIcon(tt.in))
		})
	}
}

func TestIconPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"safe emoji + space", "📁", "📁 "},
		{"ambiguous dropped, no space", "🛢", ""},
		{"ambiguous VS16 dropped, no space", "⚙️", ""},
		{"ascii + space", "*", "* "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IconPrefix(tt.in))
		})
	}
}

// TestIconReplacementsAreSafe guards the curated replacement map: every
// suggested alternative must itself be non-ambiguous so we never suggest a
// fix that has the same terminal-rendering issue.
func TestIconReplacementsAreSafe(t *testing.T) {
	t.Parallel()

	for src, alts := range ambiguousIconReplacements {
		assert.Truef(t, IsAmbiguousWidthIcon(src),
			"source %q should be ambiguous to belong in the map", src)
		assert.NotEmptyf(t, alts, "source %q must have at least one alternative", src)
		for _, alt := range alts {
			assert.Falsef(t, IsAmbiguousWidthIcon(alt),
				"alternative %q for source %q must be non-ambiguous", alt, src)
		}
	}
}

func TestSuggestSafeIcons(t *testing.T) {
	t.Parallel()

	t.Run("returns nil for unknown icon", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, SuggestSafeIcons("📁", 3))
		assert.Nil(t, SuggestSafeIcons("", 3))
	})

	t.Run("strips VS16 before lookup", func(t *testing.T) {
		t.Parallel()
		bare := SuggestSafeIcons("⚙", 0)
		vs16 := SuggestSafeIcons("⚙️", 0)
		assert.Equal(t, bare, vs16)
		assert.NotEmpty(t, bare)
	})

	t.Run("limit caps the output", func(t *testing.T) {
		t.Parallel()
		two := SuggestSafeIcons("🛢", 2)
		assert.Len(t, two, 2)
	})

	t.Run("limit ≤ 0 returns all", func(t *testing.T) {
		t.Parallel()
		all := SuggestSafeIcons("🛢", 0)
		assert.Equal(t, ambiguousIconReplacements["🛢"], all)
	})

	t.Run("limit larger than entries returns all", func(t *testing.T) {
		t.Parallel()
		all := SuggestSafeIcons("🛢", 100)
		assert.Len(t, all, len(ambiguousIconReplacements["🛢"]))
	})
}
