package i18n

// Translator provides localized string lookups for UI and command definitions.
// It abstracts away the Store's lookup logic for consumption by runtime components.
type Translator interface {
	// CommandDescription looks up a command's localized description.
	// Returns the fallback if no translation is found.
	CommandDescription(locale, commandID, fallback string) string

	// CommandConfirmationText looks up a command's localized confirmation text.
	// Returns the fallback if no translation is found.
	CommandConfirmationText(locale, commandID, fallback string) string

	// ParamDescription looks up a parameter's localized description.
	// Returns the fallback if no translation is found.
	ParamDescription(locale, commandID, paramName, fallback string) string

	// GroupTitle looks up a group's localized title.
	// Returns the fallback if no translation is found.
	GroupTitle(locale, groupID, fallback string) string

	// GroupDescription looks up a group's localized description.
	// Returns the fallback if no translation is found.
	GroupDescription(locale, groupID, fallback string) string

	// T looks up a UI string by key for the given locale.
	// Keys correspond to entries under the ui: block in translation YAML files,
	// stored without the "ui." prefix (e.g., "docs.section.properties" not
	// "ui.docs.section.properties").
	// Returns the fallback if no translation is found.
	T(locale, uiKey, fallback string) string
}

// NopTranslator is a no-op Translator that always returns the fallback.
// Used when i18n is unavailable (e.g., nil Store or completion path).
type NopTranslator struct{}

// CommandDescription implements Translator.
func (NopTranslator) CommandDescription(_, _, fallback string) string {
	return fallback
}

// CommandConfirmationText implements Translator.
func (NopTranslator) CommandConfirmationText(_, _, fallback string) string {
	return fallback
}

// ParamDescription implements Translator.
func (NopTranslator) ParamDescription(_, _, _, fallback string) string {
	return fallback
}

// GroupTitle implements Translator.
func (NopTranslator) GroupTitle(_, _, fallback string) string {
	return fallback
}

// GroupDescription implements Translator.
func (NopTranslator) GroupDescription(_, _, fallback string) string {
	return fallback
}

// T implements Translator.
func (NopTranslator) T(_, _, fallback string) string {
	return fallback
}

// TranslatorOrNop returns the provided Store if non-nil, otherwise returns
// a NopTranslator. Used at call boundaries to ensure a Translator is always
// available without nil checks downstream.
func TranslatorOrNop(s *Store) Translator {
	if s == nil {
		return NopTranslator{}
	}
	return s
}
