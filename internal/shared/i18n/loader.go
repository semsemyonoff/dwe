package i18n

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/shared/yamlstrict"
)

//go:embed translations/*.yml
var builtinFS embed.FS

// parseBundle parses a YAML bundle with strict field validation. file names the
// bundle in unknown-field errors: the embedded file name for built-ins, the
// project-relative path for a project overlay.
func parseBundle(data []byte, file string) (*Bundle, error) {
	var b Bundle
	if err := yamlstrict.Decode(data, &b, file); err != nil {
		// EOF is valid; it means empty input
		if errors.Is(err, io.EOF) {
			return &Bundle{}, nil
		}
		return nil, err
	}
	return &b, nil
}

// Load reads embedded built-in files plus optional project overlay from
// <projectRoot>/workspace/i18n/*.yml. Each locale: built-in → project, project-wins.
// Project parse errors are NOT fatal; they are surfaced via LoadProjectBundles for the validator.
func Load(projectRoot string) (*Store, error) {
	s := &Store{
		locales: make(map[string]*Bundle),
	}

	// Load built-in translations
	entries, err := fs.ReadDir(builtinFS, "translations")
	if err != nil {
		return nil, fmt.Errorf("reading embedded translations: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yml") {
			continue
		}

		locale := strings.TrimSuffix(name, ".yml")
		data, err := fs.ReadFile(builtinFS, "translations/"+name)
		if err != nil {
			return nil, fmt.Errorf("reading embedded %s: %w", name, err)
		}

		bundle, err := parseBundle(data, "translations/"+name)
		if err != nil {
			return nil, fmt.Errorf("parsing embedded %s: %w", name, err)
		}
		s.locales[locale] = bundle
	}

	// Ensure en exists
	if s.locales["en"] == nil {
		s.locales["en"] = &Bundle{
			UI:       make(map[string]string),
			Commands: make(map[string]CommandStrings),
			Groups:   make(map[string]GroupStrings),
		}
	}

	// Load and merge project translations
	if projectRoot != "" {
		projectFiles, _ := LoadProjectBundles(projectRoot)
		for _, pf := range projectFiles {
			if pf.Bundle == nil || pf.Locale == "" {
				continue
			}
			// Deep merge: project wins
			if s.locales[pf.Locale] == nil {
				s.locales[pf.Locale] = pf.Bundle
			} else {
				mergeBundle(s.locales[pf.Locale], pf.Bundle)
			}
		}
	}

	return s, nil
}

// LoadProjectBundles returns per-file parse results for the project layer only.
// Returns (nil, nil) if workspace/i18n/ is absent.
// Returns a sentinel ProjectFile with empty Locale for directory-level failures.
// Returns per-file parse errors in each ProjectFile.ParseErr.
func LoadProjectBundles(projectRoot string) ([]ProjectFile, error) {
	dir := filepath.Join(projectRoot, "workspace", "i18n")

	stat, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		// Directory-level read error -> sentinel ProjectFile
		return []ProjectFile{{
			Path:     dir,
			Locale:   "",
			Bundle:   nil,
			ParseErr: err,
		}}, nil
	}

	if !stat.IsDir() {
		return []ProjectFile{{
			Path:     dir,
			Locale:   "",
			Bundle:   nil,
			ParseErr: fmt.Errorf("not a directory"),
		}}, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return []ProjectFile{{
			Path:     dir,
			Locale:   "",
			Bundle:   nil,
			ParseErr: err,
		}}, nil
	}

	var results []ProjectFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yml") {
			continue
		}

		rawLocale := strings.TrimSuffix(name, ".yml")
		locale := Normalize(rawLocale)
		if locale == "" {
			continue
		}
		path := filepath.Join(dir, name)

		data, err := os.ReadFile(path)
		if err != nil {
			results = append(results, ProjectFile{
				Path:     path,
				Locale:   locale,
				Bundle:   nil,
				ParseErr: err,
			})
			continue
		}

		bundle, err := parseBundle(data, filepath.ToSlash(filepath.Join("workspace", "i18n", name)))
		if err != nil {
			results = append(results, ProjectFile{
				Path:     path,
				Locale:   locale,
				Bundle:   nil,
				ParseErr: err,
			})
			continue
		}

		results = append(results, ProjectFile{
			Path:     path,
			Locale:   locale,
			Bundle:   bundle,
			ParseErr: nil,
		})
	}

	if len(results) == 0 {
		return nil, nil
	}
	return results, nil
}

// mergeBundle deep merges src into dst, with src (project) winning.
func mergeBundle(dst, src *Bundle) {
	if src == nil {
		return
	}

	// Merge UI
	if src.UI != nil {
		if dst.UI == nil {
			dst.UI = make(map[string]string)
		}
		for k, v := range src.UI {
			if v != "" {
				dst.UI[k] = v
			}
		}
	}

	// Merge Commands
	if src.Commands != nil {
		if dst.Commands == nil {
			dst.Commands = make(map[string]CommandStrings)
		}
		for id, srcCS := range src.Commands {
			dstCS := dst.Commands[id]
			if srcCS.Description != "" {
				dstCS.Description = srcCS.Description
			}
			if srcCS.ConfirmationText != "" {
				dstCS.ConfirmationText = srcCS.ConfirmationText
			}
			if srcCS.Params != nil {
				if dstCS.Params == nil {
					dstCS.Params = make(map[string]ParamStrings)
				}
				for pname, srcPS := range srcCS.Params {
					dstPS := dstCS.Params[pname]
					if srcPS.Description != "" {
						dstPS.Description = srcPS.Description
					}
					dstCS.Params[pname] = dstPS
				}
			}
			dst.Commands[id] = dstCS
		}
	}

	// Merge Groups
	if src.Groups != nil {
		if dst.Groups == nil {
			dst.Groups = make(map[string]GroupStrings)
		}
		for id, srcGS := range src.Groups {
			dstGS := dst.Groups[id]
			if srcGS.Title != "" {
				dstGS.Title = srcGS.Title
			}
			if srcGS.Description != "" {
				dstGS.Description = srcGS.Description
			}
			dst.Groups[id] = dstGS
		}
	}
}
