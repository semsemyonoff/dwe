package i18n

import (
	"io/fs"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestBuiltinCoverage ensures all non-en built-in locales have the same ui.* keys as en.yml.
// This locks the contract for when new locales are added: they must cover all ui.* strings.
func TestBuiltinCoverage(t *testing.T) {
	// Load en.yml to get the baseline UI keys
	enData, err := fs.ReadFile(builtinFS, "translations/en.yml")
	if err != nil {
		t.Fatalf("failed to read en.yml: %v", err)
	}

	var enBundle Bundle
	dec := yaml.NewDecoder(strings.NewReader(string(enData)))
	dec.KnownFields(true)
	if err := dec.Decode(&enBundle); err != nil {
		t.Fatalf("failed to parse en.yml: %v", err)
	}

	// Get the set of ui.* keys from en
	enUIKeys := make(map[string]bool)
	for key := range enBundle.UI {
		enUIKeys[key] = true
	}

	if len(enUIKeys) == 0 {
		t.Skip("en.yml has no ui.* keys; skipping coverage check")
	}

	// Walk all other .yml files in the embedded translations
	entries, err := fs.ReadDir(builtinFS, "translations")
	if err != nil {
		t.Fatalf("failed to read embedded translations dir: %v", err)
	}

	var missingByLocale map[string][]string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yml") {
			continue
		}

		locale := strings.TrimSuffix(name, ".yml")
		if locale == "en" {
			// Skip English; we're comparing against it
			continue
		}

		// Load and parse the non-en locale
		data, err := fs.ReadFile(builtinFS, "translations/"+name)
		if err != nil {
			t.Errorf("failed to read %s: %v", name, err)
			continue
		}

		var locBundle Bundle
		dec := yaml.NewDecoder(strings.NewReader(string(data)))
		dec.KnownFields(true)
		if err := dec.Decode(&locBundle); err != nil {
			t.Errorf("failed to parse %s: %v", name, err)
			continue
		}

		// Check that all en UI keys are present in this locale
		var missing []string
		for key := range enUIKeys {
			if _, ok := locBundle.UI[key]; !ok {
				missing = append(missing, key)
			}
		}

		if len(missing) > 0 {
			if missingByLocale == nil {
				missingByLocale = make(map[string][]string)
			}
			missingByLocale[locale] = missing
		}
	}

	if len(missingByLocale) > 0 {
		var msg strings.Builder
		msg.WriteString("missing ui.* keys in non-en locales:\n")
		for locale, keys := range missingByLocale {
			msg.WriteString("  ")
			msg.WriteString(locale)
			msg.WriteString(":\n")
			for _, key := range keys {
				msg.WriteString("    - ")
				msg.WriteString(key)
				msg.WriteString("\n")
			}
		}
		t.Error(msg.String())
	}
}
