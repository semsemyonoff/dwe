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

// T resolves a ui.* key for the given locale.
// uiKey is the bare key name under the "ui:" YAML block — no "ui." prefix (e.g. "docs.section.properties").
// Lookup chain: locale → "en" → fallback → ""
func (s *Store) T(locale, uiKey, fallback string) string {
	if s == nil || s.locales == nil {
		return fallback
	}

	// Try exact locale
	if bundle, ok := s.locales[locale]; ok && bundle != nil && bundle.UI != nil {
		if val, ok := bundle.UI[uiKey]; ok && val != "" {
			return val
		}
	}

	// Fall back to English
	if bundle, ok := s.locales["en"]; ok && bundle != nil && bundle.UI != nil {
		if val, ok := bundle.UI[uiKey]; ok && val != "" {
			return val
		}
	}

	// Final fallback to provided arg, then empty
	if fallback != "" {
		return fallback
	}
	return ""
}

// CommandDescription looks up a command's description.
// Lookup chain: locale → "en" → fallback → ""
func (s *Store) CommandDescription(locale, commandID, fallback string) string {
	if s == nil || s.locales == nil {
		return fallback
	}

	if bundle, ok := s.locales[locale]; ok && bundle != nil && bundle.Commands != nil {
		if cs, ok := bundle.Commands[commandID]; ok && cs.Description != "" {
			return cs.Description
		}
	}

	if bundle, ok := s.locales["en"]; ok && bundle != nil && bundle.Commands != nil {
		if cs, ok := bundle.Commands[commandID]; ok && cs.Description != "" {
			return cs.Description
		}
	}

	if fallback != "" {
		return fallback
	}
	return ""
}

// CommandConfirmationText looks up a command's confirmation text.
// Lookup chain: locale → "en" → fallback → ""
func (s *Store) CommandConfirmationText(locale, commandID, fallback string) string {
	if s == nil || s.locales == nil {
		return fallback
	}

	if bundle, ok := s.locales[locale]; ok && bundle != nil && bundle.Commands != nil {
		if cs, ok := bundle.Commands[commandID]; ok && cs.ConfirmationText != "" {
			return cs.ConfirmationText
		}
	}

	if bundle, ok := s.locales["en"]; ok && bundle != nil && bundle.Commands != nil {
		if cs, ok := bundle.Commands[commandID]; ok && cs.ConfirmationText != "" {
			return cs.ConfirmationText
		}
	}

	if fallback != "" {
		return fallback
	}
	return ""
}

// ParamDescription looks up a parameter's description.
// Lookup chain: locale → "en" → fallback → ""
func (s *Store) ParamDescription(locale, commandID, paramName, fallback string) string {
	if s == nil || s.locales == nil {
		return fallback
	}

	if bundle, ok := s.locales[locale]; ok && bundle != nil && bundle.Commands != nil {
		if cs, ok := bundle.Commands[commandID]; ok && cs.Params != nil {
			if ps, ok := cs.Params[paramName]; ok && ps.Description != "" {
				return ps.Description
			}
		}
	}

	if bundle, ok := s.locales["en"]; ok && bundle != nil && bundle.Commands != nil {
		if cs, ok := bundle.Commands[commandID]; ok && cs.Params != nil {
			if ps, ok := cs.Params[paramName]; ok && ps.Description != "" {
				return ps.Description
			}
		}
	}

	if fallback != "" {
		return fallback
	}
	return ""
}

// GroupTitle looks up a group's title.
// Lookup chain: locale → "en" → fallback → ""
func (s *Store) GroupTitle(locale, groupID, fallback string) string {
	if s == nil || s.locales == nil {
		return fallback
	}

	if bundle, ok := s.locales[locale]; ok && bundle != nil && bundle.Groups != nil {
		if gs, ok := bundle.Groups[groupID]; ok && gs.Title != "" {
			return gs.Title
		}
	}

	if bundle, ok := s.locales["en"]; ok && bundle != nil && bundle.Groups != nil {
		if gs, ok := bundle.Groups[groupID]; ok && gs.Title != "" {
			return gs.Title
		}
	}

	if fallback != "" {
		return fallback
	}
	return ""
}

// GroupDescription looks up a group's description.
// Lookup chain: locale → "en" → fallback → ""
func (s *Store) GroupDescription(locale, groupID, fallback string) string {
	if s == nil || s.locales == nil {
		return fallback
	}

	if bundle, ok := s.locales[locale]; ok && bundle != nil && bundle.Groups != nil {
		if gs, ok := bundle.Groups[groupID]; ok && gs.Description != "" {
			return gs.Description
		}
	}

	if bundle, ok := s.locales["en"]; ok && bundle != nil && bundle.Groups != nil {
		if gs, ok := bundle.Groups[groupID]; ok && gs.Description != "" {
			return gs.Description
		}
	}

	if fallback != "" {
		return fallback
	}
	return ""
}
