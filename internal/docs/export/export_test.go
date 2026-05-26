package export

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"devbox-cli/internal/docs"
)

func TestExportTree_EmptyDirectory(t *testing.T) {
	// Create a temporary directory
	tmpdir := t.TempDir()
	target := filepath.Join(tmpdir, "output")

	// Create simple test filesystem
	fsys := fstest.MapFS{
		"reference/config/services.md": &fstest.MapFile{
			Data: []byte("# Services\n\nConfig services documentation."),
		},
	}

	roots := []docs.DocRoot{
		{
			Name: "devbox",
			FS:   fsys,
		},
	}

	opts := Opts{
		Lang:             "en",
		IncludeProject:   false,
		IncludeInternals: false,
		Force:            false,
	}

	err := Tree(target, roots, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the exported file exists
	exportedPath := filepath.Join(target, "reference", "reference", "config", "services.md")
	if _, err := os.Stat(exportedPath); err != nil {
		t.Errorf("exported file not found at %s: %v", exportedPath, err)
	}

	// Verify content
	content, err := os.ReadFile(exportedPath)
	if err != nil {
		t.Fatalf("cannot read exported file: %v", err)
	}
	if string(content) != "# Services\n\nConfig services documentation." {
		t.Errorf("unexpected content: %q", string(content))
	}
}

func TestExportTree_NonEmptyDirectoryWithoutForce(t *testing.T) {
	// Create a temporary directory with existing content
	tmpdir := t.TempDir()
	target := filepath.Join(tmpdir, "output")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("cannot create target directory: %v", err)
	}

	// Create a file in the target directory
	if err := os.WriteFile(filepath.Join(target, "existing.txt"), []byte("existing"), 0o644); err != nil {
		t.Fatalf("cannot create existing file: %v", err)
	}

	fsys := fstest.MapFS{
		"reference/config/services.md": &fstest.MapFile{
			Data: []byte("# Services\n\nConfig services documentation."),
		},
	}

	roots := []docs.DocRoot{
		{
			Name: "devbox",
			FS:   fsys,
		},
	}

	opts := Opts{
		Lang:             "en",
		IncludeProject:   false,
		IncludeInternals: false,
		Force:            false,
	}

	err := Tree(target, roots, opts)
	if err == nil {
		t.Error("expected error when directory is non-empty without --force")
	}
}

func TestExportTree_NonEmptyDirectoryWithForce(t *testing.T) {
	// Create a temporary directory with existing content
	tmpdir := t.TempDir()
	target := filepath.Join(tmpdir, "output")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("cannot create target directory: %v", err)
	}

	// Create a file in the target directory
	if err := os.WriteFile(filepath.Join(target, "existing.txt"), []byte("existing"), 0o644); err != nil {
		t.Fatalf("cannot create existing file: %v", err)
	}

	fsys := fstest.MapFS{
		"reference/config/services.md": &fstest.MapFile{
			Data: []byte("# Services\n\nConfig services documentation."),
		},
	}

	roots := []docs.DocRoot{
		{
			Name: "devbox",
			FS:   fsys,
		},
	}

	opts := Opts{
		Lang:             "en",
		IncludeProject:   false,
		IncludeInternals: false,
		Force:            true,
	}

	err := Tree(target, roots, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the exported file exists
	exportedPath := filepath.Join(target, "reference", "reference", "config", "services.md")
	if _, err := os.Stat(exportedPath); err != nil {
		t.Errorf("exported file not found at %s: %v", exportedPath, err)
	}
}

func TestExportTree_MissingTranslationBanner(t *testing.T) {
	// Create a temporary directory
	tmpdir := t.TempDir()
	target := filepath.Join(tmpdir, "output")

	// Create test filesystem with an English file only (no translation)
	fsys := fstest.MapFS{
		"reference/config/services.md": &fstest.MapFile{
			Data: []byte("# Services\n\nConfig services documentation."),
		},
	}

	roots := []docs.DocRoot{
		{
			Name: "devbox",
			FS:   fsys,
		},
	}

	// Request Russian, but only English is available
	opts := Opts{
		Lang:             "ru",
		IncludeProject:   false,
		IncludeInternals: false,
		Force:            false,
	}

	err := Tree(target, roots, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the exported file includes the banner
	exportedPath := filepath.Join(target, "reference", "reference", "config", "services.md")
	content, err := os.ReadFile(exportedPath)
	if err != nil {
		t.Fatalf("cannot read exported file: %v", err)
	}

	contentStr := string(content)
	expectedBanner := "> **Note:** This file is not translated to `ru`. Original English version below."
	if !containsString(contentStr, expectedBanner) {
		t.Errorf("expected banner not found in content:\n%s", contentStr)
	}
}

func TestAvailableLocales(t *testing.T) {
	fsys := fstest.MapFS{
		"reference/config/services.md": &fstest.MapFile{
			Data: []byte("# Services"),
		},
		"i18n/ru/reference/config/services.md": &fstest.MapFile{
			Data: []byte("> Translated from: reference/config/services.md @ abc123\n\n# Сервисы"),
		},
		"i18n/es/reference/config/services.md": &fstest.MapFile{
			Data: []byte("> Translated from: reference/config/services.md @ abc123\n\n# Servicios"),
		},
	}

	roots := []docs.DocRoot{
		{
			Name: "devbox",
			FS:   fsys,
		},
	}

	locales := AvailableLocales(roots)

	// Check that "en" is always present
	if !slices.Contains(locales, "en") {
		t.Error("expected 'en' in available locales")
	}

	// Check that ru and es are present
	hasRu := slices.Contains(locales, "ru")
	hasEs := slices.Contains(locales, "es")

	if !hasRu {
		t.Error("expected 'ru' in available locales")
	}
	if !hasEs {
		t.Error("expected 'es' in available locales")
	}
}

func containsString(text, substring string) bool {
	return strings.Contains(text, substring)
}
