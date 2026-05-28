// Package manifest defines the shared template-pack manifest schema used by
// AI, IDE, and git renderers. Per-kind constraints (e.g. git's basename-only
// `to`, no-symlinks rule) live in each kind's wrapper, not here.
package manifest

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"

	"devbox-cli/internal/shared/pathsafe"
)

// ErrManifestMissing is the sentinel returned when manifest.yml does not exist.
var ErrManifestMissing = errors.New("manifest file not found")

// packNameRe matches identifier-safe template pack names: must start with an
// alphanumeric character and may only contain alphanumerics, underscores, and
// hyphens. Names go on disk and into error messages so we keep the rule tight.
var packNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// ValidatePackName checks that name is safe to use as a template pack directory
// component. Rejects empty strings, leading dots/hyphens, path separators, and
// any character outside [A-Za-z0-9_-].
func ValidatePackName(name string) error {
	if name == "" {
		return fmt.Errorf("pack name is empty")
	}
	if !packNameRe.MatchString(name) {
		return fmt.Errorf("pack name %q is not identifier-safe (must match %s)", name, packNameRe.String())
	}
	return nil
}

// File is a template-pack manifest.
type File struct {
	Render   []RenderEntry  `yaml:"render"`
	Symlinks []SymlinkEntry `yaml:"symlinks"`
}

// RenderEntry describes a template file to render.
type RenderEntry struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// SymlinkEntry describes a relative symlink to create.
type SymlinkEntry struct {
	Link string `yaml:"link"`
	To   string `yaml:"to"`
}

// Load reads and strictly decodes a manifest YAML file. Unknown fields are
// rejected. When the file does not exist the returned error wraps BOTH
// ErrManifestMissing and os.ErrNotExist so callers can branch on either.
func Load(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("loading %s: %w: %w", path, ErrManifestMissing, err)
		}
		return nil, fmt.Errorf("loading %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var m File
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("loading %s: manifest is empty", path)
		}
		return nil, fmt.Errorf("loading %s: %w", path, err)
	}
	return &m, nil
}

// ValidateShape performs pure schema validation: no FS access. It checks that
// at least one render or symlink entry is declared, that all destination
// paths are contained under destRoot (computed without reading disk), that
// no duplicate `to`/`link` paths exist, and that every symlink target
// references a declared render output. label is included verbatim in errors.
func ValidateShape(m *File, destRoot, label string) error {
	if m == nil {
		return fmt.Errorf("%s: manifest is nil", label)
	}
	if len(m.Render) == 0 && len(m.Symlinks) == 0 {
		return fmt.Errorf("%s: manifest is empty: must define at least one render or symlink entry", label)
	}

	absDest, err := filepath.Abs(destRoot)
	if err != nil {
		return fmt.Errorf("%s: resolve dest root: %w", label, err)
	}

	renderDests := make(map[string]struct{})
	for i, e := range m.Render {
		if err := validateRenderShape(e, absDest, renderDests, i, label); err != nil {
			return err
		}
	}

	symlinkLinks := make(map[string]struct{})
	for i, e := range m.Symlinks {
		if err := validateSymlinkShape(e, absDest, renderDests, symlinkLinks, i, label); err != nil {
			return err
		}
	}
	return nil
}

func validateRenderShape(e RenderEntry, absDest string, dests map[string]struct{}, idx int, label string) error {
	prefix := fmt.Sprintf("%s: render[%d]: ", label, idx)
	if e.From == "" {
		return fmt.Errorf("%sfrom is required", prefix)
	}
	if filepath.IsAbs(e.From) {
		return fmt.Errorf("%sfrom must be relative (got %q)", prefix, e.From)
	}
	if e.To == "" {
		return fmt.Errorf("%sto is required", prefix)
	}
	if filepath.IsAbs(e.To) {
		return fmt.Errorf("%sto must be relative (got %q)", prefix, e.To)
	}
	absTo := filepath.Join(absDest, e.To)
	rel, err := pathsafe.ContainedRel(absDest, absTo)
	if err != nil {
		return fmt.Errorf("%sto escapes dest root: %w", prefix, err)
	}
	if _, dup := dests[rel]; dup {
		return fmt.Errorf("%sto %q is duplicated", prefix, e.To)
	}
	dests[rel] = struct{}{}
	return nil
}

func validateSymlinkShape(e SymlinkEntry, absDest string, renderDests, links map[string]struct{}, idx int, label string) error {
	prefix := fmt.Sprintf("%s: symlinks[%d]: ", label, idx)
	if e.Link == "" {
		return fmt.Errorf("%slink is required", prefix)
	}
	if filepath.IsAbs(e.Link) {
		return fmt.Errorf("%slink must be relative (got %q)", prefix, e.Link)
	}
	if e.To == "" {
		return fmt.Errorf("%sto is required", prefix)
	}
	if filepath.IsAbs(e.To) {
		return fmt.Errorf("%sto must be relative (got %q)", prefix, e.To)
	}
	absLink := filepath.Join(absDest, e.Link)
	relLink, err := pathsafe.ContainedRel(absDest, absLink)
	if err != nil {
		return fmt.Errorf("%slink escapes dest root: %w", prefix, err)
	}
	if _, dup := links[relLink]; dup {
		return fmt.Errorf("%slink %q is duplicated", prefix, e.Link)
	}
	links[relLink] = struct{}{}
	if _, collision := renderDests[relLink]; collision {
		return fmt.Errorf("%slink %q collides with a render destination; a path cannot be both a rendered file and a symlink", prefix, e.Link)
	}

	// `to` is interpreted as destRoot-relative (matching the render side):
	// EnsureRelativeSymlink turns it into a link-relative target at write time.
	absToTarget := filepath.Join(absDest, e.To)
	relTarget, err := pathsafe.ContainedRel(absDest, absToTarget)
	if err != nil {
		return fmt.Errorf("%sto escapes dest root: %w", prefix, err)
	}
	if _, ok := renderDests[relTarget]; !ok {
		return fmt.Errorf("%sto %q does not reference a declared render destination", prefix, e.To)
	}
	return nil
}

// ValidateSources verifies each render entry's `from` resolves to an existing
// regular file via the provided resolver. The resolver owns physical lookup
// (including override fallback) and returns (path, fromOverride, err). A
// returned path must be a regular file — symlinks are rejected via os.Lstat.
func ValidateSources(m *File, resolve func(rel string) (path string, fromOverride bool, err error), label string) error {
	return ValidateSourcesWith(m, resolve, nil, label)
}

// ValidateSourcesWith is ValidateSources with an optional sink that receives
// (rel, fromOverride) for each resolved source. Callers use the sink to
// aggregate override-hit info diagnostics without re-resolving.
func ValidateSourcesWith(m *File, resolve func(rel string) (path string, fromOverride bool, err error), sink func(rel string, fromOverride bool), label string) error {
	if m == nil {
		return fmt.Errorf("%s: manifest is nil", label)
	}
	if resolve == nil {
		return fmt.Errorf("%s: resolver is nil", label)
	}
	for i, e := range m.Render {
		prefix := fmt.Sprintf("%s: render[%d]: ", label, i)
		path, fromOverride, err := resolve(e.From)
		if err != nil {
			return fmt.Errorf("%sresolve from %q: %w", prefix, e.From, err)
		}
		fi, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("%sstat resolved source %s: %w", prefix, path, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%sresolved source is a symlink: %s", prefix, path)
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%sresolved source is not a regular file: %s", prefix, path)
		}
		if sink != nil {
			sink(e.From, fromOverride)
		}
	}
	return nil
}
