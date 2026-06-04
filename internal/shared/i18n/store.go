package i18n

import (
	"slices"
	"sort"
)

// Store holds merged bundles per locale.
type Store struct {
	locales map[string]*Bundle // key: 2-letter code; "en" always present
}

// AvailableLocales returns the union of locales present in built-in and project layers.
// Always includes "en" as the first element.
func (s *Store) AvailableLocales() []string {
	if s == nil || len(s.locales) == 0 {
		return []string{"en"}
	}
	locales := make([]string, 0, len(s.locales))
	locales = append(locales, "en") // en is always first
	for k := range s.locales {
		if k != "en" {
			locales = append(locales, k)
		}
	}
	sort.Strings(locales[1:])
	return locales
}

// ClampLocale returns locale if it is present in the store, otherwise "en".
// Use this when a locale resolved from $LANG or config should only take effect
// if the project actually ships that translation.
func (s *Store) ClampLocale(locale string) string {
	if slices.Contains(s.AvailableLocales(), locale) {
		return locale
	}
	return "en"
}

// resolveLocalized runs extract against the requested locale's bundle, then the
// "en" bundle, returning the first non-empty result. When neither yields a
// value it returns fallback. Nil store / nil locale map short-circuit to
// fallback. extract is nil-safe: map indexing on absent keys (or nil sub-maps)
// returns the zero value, which is treated as "not found".
func (s *Store) resolveLocalized(locale, fallback string, extract func(*Bundle) string) string {
	if s == nil || s.locales == nil {
		return fallback
	}
	if bundle, ok := s.locales[locale]; ok && bundle != nil {
		if v := extract(bundle); v != "" {
			return v
		}
	}
	if bundle, ok := s.locales["en"]; ok && bundle != nil {
		if v := extract(bundle); v != "" {
			return v
		}
	}
	return fallback
}

// T resolves a ui.* key for the given locale.
// uiKey is the bare key name under the "ui:" YAML block — no "ui." prefix (e.g. "docs.section.properties").
// Lookup chain: locale → "en" → fallback → ""
func (s *Store) T(locale, uiKey, fallback string) string {
	return s.resolveLocalized(locale, fallback, func(b *Bundle) string {
		return b.UI[uiKey]
	})
}

// CommandDescription looks up a command's description.
// Lookup chain: locale → "en" → fallback → ""
func (s *Store) CommandDescription(locale, commandID, fallback string) string {
	return s.resolveLocalized(locale, fallback, func(b *Bundle) string {
		return b.Commands[commandID].Description
	})
}

// CommandConfirmationText looks up a command's confirmation text.
// Lookup chain: locale → "en" → fallback → ""
func (s *Store) CommandConfirmationText(locale, commandID, fallback string) string {
	return s.resolveLocalized(locale, fallback, func(b *Bundle) string {
		return b.Commands[commandID].ConfirmationText
	})
}

// ParamDescription looks up a parameter's description.
// Lookup chain: locale → "en" → fallback → ""
func (s *Store) ParamDescription(locale, commandID, paramName, fallback string) string {
	return s.resolveLocalized(locale, fallback, func(b *Bundle) string {
		return b.Commands[commandID].Params[paramName].Description
	})
}

// GroupTitle looks up a group's title.
// Lookup chain: locale → "en" → fallback → ""
func (s *Store) GroupTitle(locale, groupID, fallback string) string {
	return s.resolveLocalized(locale, fallback, func(b *Bundle) string {
		return b.Groups[groupID].Title
	})
}

// GroupDescription looks up a group's description.
// Lookup chain: locale → "en" → fallback → ""
func (s *Store) GroupDescription(locale, groupID, fallback string) string {
	return s.resolveLocalized(locale, fallback, func(b *Bundle) string {
		return b.Groups[groupID].Description
	})
}

// CommandSuccessMessage looks up a command's success message.
// Lookup chain: locale → "en" → fallback → ""
func (s *Store) CommandSuccessMessage(locale, commandID, fallback string) string {
	return s.resolveLocalized(locale, fallback, func(b *Bundle) string {
		return b.Commands[commandID].Messages.Success
	})
}

// CommandErrorMessage looks up a command's error message.
// Lookup chain: locale → "en" → fallback → ""
func (s *Store) CommandErrorMessage(locale, commandID, fallback string) string {
	return s.resolveLocalized(locale, fallback, func(b *Bundle) string {
		return b.Commands[commandID].Messages.Error
	})
}

// ParamOptionLabel looks up a parameter option's localized label.
// Lookup chain: locale → "en" → fallback → ""
func (s *Store) ParamOptionLabel(locale, commandID, paramName, optionValue, fallback string) string {
	return s.resolveLocalized(locale, fallback, func(b *Bundle) string {
		return b.Commands[commandID].Params[paramName].Options[optionValue]
	})
}
