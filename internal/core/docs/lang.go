package docs

import (
	"bytes"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

// ResolveContent resolves documentation content with language fallback.
// It reads from the specified root (built-in or project docs) and applies the following logic:
//
// 1. If locale == "en", read the file directly from the root.
// 2. If locale != "en", attempt to read from i18n/<locale>/<relPath>.
//   - If found: parse the content-hash header ("Translated from: ... @ <hash>").
//     Return the content, the source locale, and whether the hash is stale
//     (i.e., does not match the current hash of the English file).
//   - If not found: read the English file and return it with sourceLang="en" and stale=false.
//
// Content-hash headers must match: `> Translated from: ... @ <hash>` (single >)
// where <hash> is 12-64 hex characters (typically 12).
//
// Returns (content, sourceLang, stale, error).
func ResolveContent(root DocRoot, relPath, locale string) (content []byte, sourceLang string, stale bool, err error) {
	// Normalize the locale
	locale = strings.TrimSpace(locale)
	if locale == "" {
		locale = "en"
	}

	// If English is requested, read directly.
	if locale == "en" {
		content, err = fs.ReadFile(root.FS, relPath)
		return content, "en", false, err
	}

	// Try to read from i18n/<locale>/<relPath>
	i18nPath := fmt.Sprintf("i18n/%s/%s", locale, relPath)
	translatedContent, err := fs.ReadFile(root.FS, i18nPath)
	if err == nil {
		// Successfully read the translation.
		// Check the content-hash header to determine staleness.
		headerHash, headerFound := parseContentHashHeader(translatedContent)
		if !headerFound {
			// No header found; we cannot determine staleness.
			// Assume not stale (no warning).
			return translatedContent, locale, false, nil
		}

		// Get the current English file hash.
		enHash := ContentHashFor(relPath)
		if enHash == "" {
			// Manifest is empty or missing this entry.
			// Assume not stale (no warning).
			return translatedContent, locale, false, nil
		}

		// Compare hashes.
		stale = (headerHash != enHash)
		return translatedContent, locale, stale, nil
	}

	// Translation not found; fall back to English.
	enContent, err := fs.ReadFile(root.FS, relPath)
	return enContent, "en", false, err
}

// contentHashHeaderRe matches the translation header:
// `> Translated from: <path> @ <hash>` where <hash> is 12-64 hex chars.
var contentHashHeaderRe = regexp.MustCompile(`^>\s*Translated from:\s*\S+\s*@\s*([0-9a-f]{12,64})\s*$`)

// parseContentHashHeader extracts the content hash from a translation header.
// Returns (hash, found).
func parseContentHashHeader(content []byte) (string, bool) {
	lines := bytes.SplitN(content, []byte{'\n'}, 2)
	if len(lines) == 0 {
		return "", false
	}

	firstLine := strings.TrimRight(string(lines[0]), "\r")
	matches := contentHashHeaderRe.FindStringSubmatch(firstLine)
	if len(matches) < 2 {
		return "", false
	}

	return matches[1], true
}

// AvailableLocalesFor returns all available locales for a given file path across the given roots.
// It includes "en" (always available) plus any language variants found in i18n/<locale>/<relPath>.
func AvailableLocalesFor(roots []DocRoot, relPath string) []string {
	locales := []string{"en"}
	seenLocales := map[string]bool{"en": true}

	for _, root := range roots {
		// Try to list i18n directory
		i18nDir := "i18n"
		entries, err := fs.ReadDir(root.FS, i18nDir)
		if err != nil {
			// No i18n directory in this root; skip
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			locale := entry.Name()
			if seenLocales[locale] {
				continue
			}

			// Check if this locale has a variant of the requested file
			localizePath := fmt.Sprintf("i18n/%s/%s", locale, relPath)
			_, err := fs.Stat(root.FS, localizePath)
			if err == nil {
				locales = append(locales, locale)
				seenLocales[locale] = true
			}
		}
	}

	return locales
}
