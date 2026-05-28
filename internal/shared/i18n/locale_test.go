package i18n

import (
	"testing"
)

func TestResolveLocale(t *testing.T) {
	tests := []struct {
		name        string
		flagLang    string
		configLang  string
		sysLang     string
		expected    string
		description string
	}{
		// Flag precedence
		{
			name:        "flag_wins",
			flagLang:    "de",
			configLang:  "ru",
			sysLang:     "fr_FR.UTF-8",
			expected:    "de",
			description: "flag takes highest precedence",
		},
		{
			name:        "flag_normalized",
			flagLang:    "ru_RU.UTF-8",
			configLang:  "",
			sysLang:     "",
			expected:    "ru",
			description: "flag value is normalized",
		},
		// Config precedence
		{
			name:        "config_wins",
			flagLang:    "",
			configLang:  "ru",
			sysLang:     "en_US.UTF-8",
			expected:    "ru",
			description: "config wins when flag absent",
		},
		{
			name:        "config_normalized",
			flagLang:    "",
			configLang:  "de-de",
			sysLang:     "",
			expected:    "de",
			description: "config value is normalized",
		},
		// System locale fallback
		{
			name:        "sys_lang_wins",
			flagLang:    "",
			configLang:  "",
			sysLang:     "ru_RU.UTF-8",
			expected:    "ru",
			description: "system LANG used when config/flag absent",
		},
		{
			name:        "sys_lang_en_us",
			flagLang:    "",
			configLang:  "",
			sysLang:     "en_US",
			expected:    "en",
			description: "en_US system locale resolves to en",
		},
		// System locale with special values
		{
			name:        "sys_lang_posix",
			flagLang:    "",
			configLang:  "",
			sysLang:     "POSIX",
			expected:    "en",
			description: "POSIX system locale falls through to en",
		},
		{
			name:        "sys_lang_c",
			flagLang:    "",
			configLang:  "",
			sysLang:     "C",
			expected:    "en",
			description: "C system locale falls through to en",
		},
		// All empty
		{
			name:        "all_empty",
			flagLang:    "",
			configLang:  "",
			sysLang:     "",
			expected:    "en",
			description: "default to en when all inputs empty",
		},
		// Whitespace handling
		{
			name:        "flag_whitespace",
			flagLang:    "  ru  ",
			configLang:  "",
			sysLang:     "",
			expected:    "ru",
			description: "flag whitespace is trimmed",
		},
		{
			name:        "config_whitespace",
			flagLang:    "",
			configLang:  "  de  ",
			sysLang:     "",
			expected:    "de",
			description: "config whitespace is trimmed",
		},
		// Complex system locale strings
		{
			name:        "sys_lang_complex",
			flagLang:    "",
			configLang:  "",
			sysLang:     "ja_JP.UTF-8",
			expected:    "ja",
			description: "Japanese system locale",
		},
		// flag/config that normalizes to empty falls through to next layer
		{
			name:        "flag_c_falls_through",
			flagLang:    "C",
			configLang:  "ru",
			sysLang:     "",
			expected:    "ru",
			description: "--lang C normalizes to empty, config wins",
		},
		{
			name:        "flag_posix_falls_through_to_default",
			flagLang:    "POSIX",
			configLang:  "",
			sysLang:     "",
			expected:    "en",
			description: "--lang POSIX normalizes to empty, falls through to en",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveLocale(tt.flagLang, tt.configLang, tt.sysLang)
			if got != tt.expected {
				t.Errorf("ResolveLocale(%q, %q, %q) = %q, want %q\n%s",
					tt.flagLang, tt.configLang, tt.sysLang, got, tt.expected, tt.description)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    string
		description string
	}{
		// Basic cases
		{
			name:        "simple_lowercase",
			input:       "ru",
			expected:    "ru",
			description: "lowercase already normalized",
		},
		{
			name:        "uppercase_to_lower",
			input:       "RU",
			expected:    "ru",
			description: "uppercase converted to lowercase",
		},
		// Underscore separator
		{
			name:        "underscore_region",
			input:       "ru_RU",
			expected:    "ru",
			description: "underscore-separated region dropped",
		},
		{
			name:        "underscore_encoding",
			input:       "ru_RU.UTF-8",
			expected:    "ru",
			description: "encoding after underscore dropped",
		},
		// Hyphen separator
		{
			name:        "hyphen_region",
			input:       "ru-ru",
			expected:    "ru",
			description: "hyphen-separated region dropped",
		},
		// Dot separator
		{
			name:        "dot_encoding",
			input:       "en.UTF-8",
			expected:    "en",
			description: "dot-separated encoding dropped",
		},
		// Special cases
		{
			name:        "c_locale",
			input:       "C",
			expected:    "",
			description: "C locale returns empty",
		},
		{
			name:        "posix_locale",
			input:       "POSIX",
			expected:    "",
			description: "POSIX locale returns empty",
		},
		{
			name:        "posix_lowercase",
			input:       "posix",
			expected:    "",
			description: "posix (lowercase) returns empty",
		},
		// Whitespace
		{
			name:        "leading_whitespace",
			input:       "  ru",
			expected:    "ru",
			description: "leading whitespace trimmed",
		},
		{
			name:        "trailing_whitespace",
			input:       "de  ",
			expected:    "de",
			description: "trailing whitespace trimmed",
		},
		{
			name:        "both_whitespace",
			input:       "  ja_JP.UTF-8  ",
			expected:    "ja",
			description: "both sides trimmed, then normalized",
		},
		// Edge cases
		{
			name:        "empty_string",
			input:       "",
			expected:    "",
			description: "empty input returns empty",
		},
		{
			name:        "only_whitespace",
			input:       "   ",
			expected:    "",
			description: "only whitespace returns empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Normalize(tt.input)
			if got != tt.expected {
				t.Errorf("Normalize(%q) = %q, want %q\n%s",
					tt.input, got, tt.expected, tt.description)
			}
		})
	}
}
