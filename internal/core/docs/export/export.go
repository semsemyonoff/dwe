package export

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/semsemyonoff/dwe/internal/core/docs"
)

// Opts configures the export behavior.
type Opts struct {
	Lang             string // Target locale (e.g., "en", "ru")
	IncludeProject   bool   // Include project docs (./docs/)
	IncludeInternals bool   // Include internals docs
	Force            bool   // Overwrite non-empty target directory
}

// Tree writes all documentation files to a target directory.
// It walks the documentation tree and exports markdown files with language fallback.
// When a translation is missing, it includes a banner in the exported content.
func Tree(dst string, roots []docs.DocRoot, opts Opts) error {
	// Filter roots based on options
	filteredRoots := []docs.DocRoot{}
	for _, root := range roots {
		switch root.Name {
		case "dwe":
			// Always include dwe root
			filteredRoots = append(filteredRoots, root)
		case "project":
			if opts.IncludeProject {
				filteredRoots = append(filteredRoots, root)
			}
		}
	}

	// Check if target directory exists and is non-empty
	info, err := os.Stat(dst)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("target exists but is not a directory: %s", dst)
		}
		// Check if directory is non-empty
		entries, err := os.ReadDir(dst)
		if err != nil {
			return fmt.Errorf("cannot read target directory: %w", err)
		}
		if len(entries) > 0 && !opts.Force {
			return fmt.Errorf("target directory is not empty (use --force to overwrite): %s", dst)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("cannot stat target directory: %w", err)
	}

	// Create target directory if it doesn't exist
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("cannot create target directory: %w", err)
	}

	// Walk and export from each root
	for _, root := range filteredRoots {
		if err := exportRoot(dst, root, opts); err != nil {
			return err
		}
	}

	return nil
}

func exportRoot(dst string, root docs.DocRoot, opts Opts) error {
	// Use root.Name as the subdir so the canonical tree shape is preserved
	// (e.g. dst/dwe/reference/config/services.md, dst/dwe/internals/...,
	// dst/project/...). The earlier override (dwe → "reference") combined
	// with canonical node.Path values like "reference/config/services.md"
	// produced "dst/reference/reference/..." and stuffed internals under
	// reference; using the root name keeps roots separated and matches the
	// source layout.
	// Export always walks the canonical English source tree; per-locale files
	// live under i18n/<locale>/ and are exported alongside as ordinary files.
	tree, err := docs.BuildTree(root, "en")
	if err != nil {
		return fmt.Errorf("cannot build tree for %s docs: %w", root.Name, err)
	}
	return walkAndExport(dst, root.Name, tree, root, opts)
}

func walkAndExport(dst, subdir string, node *docs.Node, root docs.DocRoot, opts Opts) error {
	if node == nil {
		return nil
	}

	// Skip the internals subtree when IncludeInternals is false.
	// Node.Path for a top-level directory equals its name ("internals").
	if !opts.IncludeInternals && node.Path == "internals" && root.Name == "dwe" {
		return nil
	}

	// Create subdirectory path
	targetPath := filepath.Join(dst, subdir, node.Path)

	if node.IsDir {
		// Create directory
		if err := os.MkdirAll(targetPath, 0o755); err != nil {
			return fmt.Errorf("cannot create directory: %w", err)
		}
		// Recurse into children
		if node.Children != nil {
			for _, child := range node.Children {
				if err := walkAndExport(dst, subdir, child, root, opts); err != nil {
					return err
				}
			}
		}
	} else {
		// This is a file; export it
		relPath := node.Path
		content, sourceLang, _, err := docs.ResolveContent(root, relPath, opts.Lang)
		if err != nil {
			return fmt.Errorf("cannot read %s: %w", relPath, err)
		}

		// If the source language differs from requested, prepend a banner
		if sourceLang != opts.Lang && opts.Lang != "en" {
			banner := fmt.Sprintf("> **Note:** This file is not translated to `%s`. Original English version below.\n\n", opts.Lang)
			content = append([]byte(banner), content...)
		}

		// Create parent directory
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("cannot create directory: %w", err)
		}

		// Write file
		if err := os.WriteFile(targetPath, content, 0o644); err != nil {
			return fmt.Errorf("cannot write %s: %w", targetPath, err)
		}
	}

	return nil
}

// AvailableLocales returns all locales available across the given roots.
func AvailableLocales(roots []docs.DocRoot) []string {
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
			if !seenLocales[locale] {
				locales = append(locales, locale)
				seenLocales[locale] = true
			}
		}
	}

	return locales
}
