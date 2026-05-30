package docs

import (
	"bytes"
	"testing"
	"testing/fstest"
)

func TestResolveContent_English(t *testing.T) {
	// Setup: a simple en file
	fsys := fstest.MapFS{
		"config/services.md": &fstest.MapFile{Data: []byte("# Services\n\nConfiguration.")},
	}
	root := DocRoot{
		Name: "devbox",
		FS:   fsys,
	}

	content, lang, stale, err := ResolveContent(root, "config/services.md", "en")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang != "en" {
		t.Errorf("expected lang=en, got %q", lang)
	}
	if stale {
		t.Errorf("expected stale=false for English, got true")
	}
	if !bytes.Equal(content, []byte("# Services\n\nConfiguration.")) {
		t.Errorf("unexpected content: %q", string(content))
	}
}

func TestResolveContent_TranslationWithMatchingHash(t *testing.T) {
	// Setup: en file and translated file with matching hash in header
	fsys := fstest.MapFS{
		"config/services.md": &fstest.MapFile{Data: []byte("# Services\n\nConfiguration.")},
		"i18n/ru/config/services.md": &fstest.MapFile{
			Data: []byte("> Translated from: config/services.md @ abcd1234ef56\n\n# Сервисы\n\nКонфигурация."),
		},
	}
	root := DocRoot{
		Name: "devbox",
		FS:   fsys,
	}

	// Set the manifest to match the hash in the header
	oldHashes := ContentHashes
	defer func() { ContentHashes = oldHashes }()
	ContentHashes = map[string]string{
		"config/services.md": "abcd1234ef56",
	}

	content, lang, stale, err := ResolveContent(root, "config/services.md", "ru")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang != "ru" {
		t.Errorf("expected lang=ru, got %q", lang)
	}
	if stale {
		t.Errorf("expected stale=false for matching hash, got true")
	}
	if !bytes.Contains(content, []byte("Сервисы")) {
		t.Errorf("expected Russian translation content, got: %q", string(content))
	}
}

func TestResolveContent_TranslationWithStalHash(t *testing.T) {
	// Setup: en file and translated file with mismatched hash
	fsys := fstest.MapFS{
		"config/services.md": &fstest.MapFile{Data: []byte("# Services\n\nConfiguration.")},
		"i18n/ru/config/services.md": &fstest.MapFile{
			Data: []byte("> Translated from: config/services.md @ aabbccdd11223344\n\n# Сервисы\n\nКонфигурация."),
		},
	}
	root := DocRoot{
		Name: "devbox",
		FS:   fsys,
	}

	// Set the manifest to a different hash
	oldHashes := ContentHashes
	defer func() { ContentHashes = oldHashes }()
	ContentHashes = map[string]string{
		"config/services.md": "eeff00112233aabb",
	}

	content, lang, stale, err := ResolveContent(root, "config/services.md", "ru")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang != "ru" {
		t.Errorf("expected lang=ru, got %q", lang)
	}
	if !stale {
		t.Errorf("expected stale=true for mismatched hash, got false")
	}
	if !bytes.Contains(content, []byte("Сервисы")) {
		t.Errorf("expected Russian translation content despite staleness")
	}
}

func TestResolveContent_TranslationMissing_FallbackToEnglish(t *testing.T) {
	// Setup: en file only, no translation
	fsys := fstest.MapFS{
		"config/services.md": &fstest.MapFile{Data: []byte("# Services\n\nConfiguration.")},
	}
	root := DocRoot{
		Name: "devbox",
		FS:   fsys,
	}

	content, lang, stale, err := ResolveContent(root, "config/services.md", "ru")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang != "en" {
		t.Errorf("expected lang=en (fallback), got %q", lang)
	}
	if stale {
		t.Errorf("expected stale=false for fallback, got true")
	}
	if !bytes.Equal(content, []byte("# Services\n\nConfiguration.")) {
		t.Errorf("unexpected content: %q", string(content))
	}
}

func TestResolveContent_TranslationWithEmptyManifest(t *testing.T) {
	// Setup: translation exists with header, but manifest is empty
	fsys := fstest.MapFS{
		"config/services.md": &fstest.MapFile{Data: []byte("# Services\n\nConfiguration.")},
		"i18n/ru/config/services.md": &fstest.MapFile{
			Data: []byte("> Translated from: config/services.md @ aabbccddee112233\n\n# Сервисы\n\nКонфигурация."),
		},
	}
	root := DocRoot{
		Name: "devbox",
		FS:   fsys,
	}

	// Empty manifest
	oldHashes := ContentHashes
	defer func() { ContentHashes = oldHashes }()
	ContentHashes = map[string]string{}

	content, lang, stale, err := ResolveContent(root, "config/services.md", "ru")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang != "ru" {
		t.Errorf("expected lang=ru, got %q", lang)
	}
	if stale {
		t.Errorf("expected stale=false when manifest is empty, got true")
	}
	if !bytes.Contains(content, []byte("Сервисы")) {
		t.Errorf("expected Russian translation content")
	}
}

func TestResolveContent_MalformedHeader(t *testing.T) {
	// Setup: translation with malformed header (should be treated as no header)
	fsys := fstest.MapFS{
		"config/services.md": &fstest.MapFile{Data: []byte("# Services\n\nConfiguration.")},
		"i18n/ru/config/services.md": &fstest.MapFile{
			Data: []byte("> This is just a comment, not a Translated from header\n\n# Сервисы\n\nКонфигурация."),
		},
	}
	root := DocRoot{
		Name: "devbox",
		FS:   fsys,
	}

	oldHashes := ContentHashes
	defer func() { ContentHashes = oldHashes }()
	ContentHashes = map[string]string{
		"config/services.md": "aabbccddee112233",
	}

	content, lang, stale, err := ResolveContent(root, "config/services.md", "ru")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang != "ru" {
		t.Errorf("expected lang=ru, got %q", lang)
	}
	if stale {
		t.Errorf("expected stale=false for malformed header, got true")
	}
	if !bytes.Contains(content, []byte("Сервисы")) {
		t.Errorf("expected Russian translation content")
	}
}

func TestResolveContent_ReadmeWithMatchingHash(t *testing.T) {
	// Setup: repo-root README and Russian sibling under i18n/ru/
	fsys := fstest.MapFS{
		"README.md": &fstest.MapFile{Data: []byte("# devbox\n\nDeveloper environments on Docker.")},
		"i18n/ru/README.md": &fstest.MapFile{
			Data: []byte("> Translated from: README.md @ 0123456789ab\n\n# devbox\n\nСреды разработки на Docker."),
		},
	}
	root := DocRoot{
		Name: "devbox",
		FS:   fsys,
	}

	oldHashes := ContentHashes
	defer func() { ContentHashes = oldHashes }()
	ContentHashes = map[string]string{
		"README.md": "0123456789ab",
	}

	content, lang, stale, err := ResolveContent(root, "README.md", "ru")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang != "ru" {
		t.Errorf("expected lang=ru, got %q", lang)
	}
	if stale {
		t.Errorf("expected stale=false for matching hash, got true")
	}
	if !bytes.Contains(content, []byte("Среды разработки")) {
		t.Errorf("expected Russian README content, got: %q", string(content))
	}
}

func TestResolveContent_ReadmeWithStaleHash(t *testing.T) {
	// Setup: repo-root README with Russian sibling whose header hash is outdated
	fsys := fstest.MapFS{
		"README.md": &fstest.MapFile{Data: []byte("# devbox\n\nUpdated tagline.")},
		"i18n/ru/README.md": &fstest.MapFile{
			Data: []byte("> Translated from: README.md @ aaaaaaaaaaaa\n\n# devbox\n\nСтарый перевод."),
		},
	}
	root := DocRoot{
		Name: "devbox",
		FS:   fsys,
	}

	oldHashes := ContentHashes
	defer func() { ContentHashes = oldHashes }()
	ContentHashes = map[string]string{
		"README.md": "bbbbbbbbbbbb",
	}

	content, lang, stale, err := ResolveContent(root, "README.md", "ru")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lang != "ru" {
		t.Errorf("expected lang=ru, got %q", lang)
	}
	if !stale {
		t.Errorf("expected stale=true for mismatched README hash, got false")
	}
	if !bytes.Contains(content, []byte("Старый перевод")) {
		t.Errorf("expected Russian README content despite staleness")
	}
}

func TestParseContentHashHeader(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantHash string
		wantOK   bool
	}{
		{
			name:     "valid header with 12 hex chars",
			input:    "> Translated from: path/to/file.md @ abcd1234ef56\n",
			wantHash: "abcd1234ef56",
			wantOK:   true,
		},
		{
			name:     "valid header with 64 hex chars",
			input:    "> Translated from: file.md @ abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789\n",
			wantHash: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
			wantOK:   true,
		},
		{
			name:     "valid header with extra spaces",
			input:    ">   Translated from:   file.md   @   abcd1234ef56   \n",
			wantHash: "abcd1234ef56",
			wantOK:   true,
		},
		{
			name:     "invalid header - missing @",
			input:    "> Translated from: file.md abcd1234ef56\n",
			wantHash: "",
			wantOK:   false,
		},
		{
			name:     "invalid header - wrong prefix",
			input:    "> Outdated: file.md @ abcd1234ef56\n",
			wantHash: "",
			wantOK:   false,
		},
		{
			name:     "invalid hash - non-hex",
			input:    "> Translated from: file.md @ gggg1234ef56\n",
			wantHash: "",
			wantOK:   false,
		},
		{
			name:     "invalid hash - too short",
			input:    "> Translated from: file.md @ abcd12\n",
			wantHash: "",
			wantOK:   false,
		},
		{
			name:     "empty input",
			input:    "",
			wantHash: "",
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash, ok := parseContentHashHeader([]byte(tt.input))
			if ok != tt.wantOK {
				t.Errorf("parseContentHashHeader() ok=%v, want %v", ok, tt.wantOK)
			}
			if hash != tt.wantHash {
				t.Errorf("parseContentHashHeader() hash=%q, want %q", hash, tt.wantHash)
			}
		})
	}
}

func TestAvailableLocalesFor(t *testing.T) {
	fsys := fstest.MapFS{
		"config/services.md":         &fstest.MapFile{Data: []byte("# Services")},
		"i18n/ru/config/services.md": &fstest.MapFile{Data: []byte("# Сервисы")},
		"i18n/de/config/services.md": &fstest.MapFile{Data: []byte("# Dienste")},
		"i18n/fr/other.md":           &fstest.MapFile{Data: []byte("# Autre")}, // Different file
	}
	root := DocRoot{
		Name: "devbox",
		FS:   fsys,
	}

	locales := AvailableLocalesFor([]DocRoot{root}, "config/services.md")

	// Should contain "en" and "ru" and "de", but not "fr" (different file)
	want := map[string]bool{
		"en": true,
		"ru": true,
		"de": true,
	}

	if len(locales) != 3 {
		t.Errorf("expected 3 locales, got %d: %v", len(locales), locales)
	}

	for _, locale := range locales {
		if !want[locale] {
			t.Errorf("unexpected locale: %q", locale)
		}
	}

	localeSet := make(map[string]bool)
	for _, l := range locales {
		localeSet[l] = true
	}
	for locale := range want {
		if !localeSet[locale] {
			t.Errorf("missing expected locale: %q", locale)
		}
	}
}

func TestAvailableLocalesFor_NoI18n(t *testing.T) {
	fsys := fstest.MapFS{
		"config/services.md": &fstest.MapFile{Data: []byte("# Services")},
	}
	root := DocRoot{
		Name: "devbox",
		FS:   fsys,
	}

	locales := AvailableLocalesFor([]DocRoot{root}, "config/services.md")

	// Should only contain "en"
	if len(locales) != 1 || locales[0] != "en" {
		t.Errorf("expected [en], got %v", locales)
	}
}
