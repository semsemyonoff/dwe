package i18n

import (
	"strings"
)

// ResolveLocale picks the active locale per the documented precedence:
// 1. flagLang (docs generate --lang value)
// 2. configLang (userconfig.Language)
// 3. sysLang (system $LANG environment variable)
// 4. "en" (default)
func ResolveLocale(flagLang, configLang, sysLang string) string {
	if flagLang != "" {
		return Normalize(flagLang)
	}
	if configLang != "" {
		return Normalize(configLang)
	}
	if sysLang != "" {
		if code := ParseSystemLang(sysLang); code != "" {
			return code
		}
	}
	return "en"
}

// Normalize converts a locale code to canonical 2-letter form.
// Handles forms like "ru-ru", "ru_RU", "ru_RU.UTF-8" → "ru".
// Returns empty string if the input cannot be normalized (e.g., "C", "POSIX").
func Normalize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// Extract the language code before any separator (_, -, .)
	lang := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	})
	if len(lang) == 0 {
		return ""
	}

	code := strings.ToLower(lang[0])
	if code == "c" || code == "posix" {
		return ""
	}

	return code
}

// ParseSystemLang extracts a 2-letter language code from a system locale string.
// Handles forms like "ru_RU.UTF-8", "en_US", "C", "POSIX".
// Returns empty string for non-localizable forms ("C", "POSIX", empty).
func ParseSystemLang(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// Handle special non-localizable cases
	if s == "C" || s == "POSIX" {
		return ""
	}

	// Extract language code (before underscore, hyphen, or dot)
	lang := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	})
	if len(lang) == 0 {
		return ""
	}

	code := strings.ToLower(lang[0])
	if code == "c" || code == "posix" {
		return ""
	}

	return code
}
