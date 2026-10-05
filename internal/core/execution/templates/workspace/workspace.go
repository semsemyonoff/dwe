// Package workspace plans template packs that write into the project root.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/execution/templates/manifest"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/packcommon"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/packroot"
	"github.com/semsemyonoff/dwe/internal/shared/pathsafe"
)

var destLabels = packcommon.DestLabels{
	Path: "destination", Boundary: "project root", ResolveBoundary: "project root",
}

// PlannedFile holds a project-relative destination and its prepared source.
type PlannedFile struct {
	From string
	To   string
	File packcommon.PreparedFile
}

// PlannedPack preserves manifest order for the later write phase.
type PlannedPack struct {
	Name     string
	Files    []PlannedFile
	Symlinks []manifest.SymlinkEntry
}

// ValidatePacks rejects invalid identifiers and repeated pack names.
func ValidatePacks(names []string) error {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if err := manifest.ValidatePackName(name); err != nil {
			return fmt.Errorf("workspace pack: %w", err)
		}
		if seen[name] {
			return fmt.Errorf("workspace pack %q is duplicated", name)
		}
		seen[name] = true
	}
	return nil
}

// ResolvePack requires a canonical directory; there is no implicit fallback.
func ResolvePack(projectRoot, name string) (string, error) {
	if err := manifest.ValidatePackName(name); err != nil {
		return "", fmt.Errorf("workspace pack: %w", err)
	}
	if projectRoot == "" {
		return "", errors.New("workspace: project root is required")
	}
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	packDir := filepath.Join(absRoot, "workspace", "templates", "workspace", name)
	if err := pathsafe.CheckNoSymlinks(absRoot, packDir, "workspace pack"); err != nil {
		return "", err
	}
	fi, err := os.Lstat(packDir)
	if err != nil {
		return "", fmt.Errorf("stat workspace pack %q: %w", name, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("workspace pack %q is not a directory", name)
	}
	return packDir, nil
}

// Plan validates every pack and prepares all output without writing anything.
// Only .tmpl sources execute as Go templates; other sources remain verbatim.
// An error returns no partial plan.
func Plan(projectRoot string, names []string, data packcommon.TemplateData) ([]PlannedPack, error) {
	if err := ValidatePacks(names); err != nil {
		return nil, err
	}
	if projectRoot == "" {
		return nil, errors.New("workspace: project root is required")
	}
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	planned := make([]PlannedPack, 0, len(names))
	dests := []destination{}
	for _, name := range names {
		pack, err := planPack(absRoot, name, data, &dests)
		if err != nil {
			return nil, fmt.Errorf("workspace pack %q: %w", name, err)
		}
		planned = append(planned, pack)
	}
	return planned, nil
}

func planPack(root, name string, data packcommon.TemplateData, dests *[]destination) (PlannedPack, error) {
	packDir, err := ResolvePack(root, name)
	if err != nil {
		return PlannedPack{}, err
	}
	manifestPath := filepath.Join(packDir, "manifest.yml")
	if err := pathsafe.CheckNoSymlinks(root, manifestPath, "workspace manifest"); err != nil {
		return PlannedPack{}, err
	}
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return PlannedPack{}, err
	}
	if err := manifest.ValidateShape(m, root, "workspace manifest"); err != nil {
		return PlannedPack{}, err
	}
	for _, e := range m.Render {
		if err := addDestination(dests, name, e.To); err != nil {
			return PlannedPack{}, err
		}
	}
	for _, e := range m.Symlinks {
		if err := addDestination(dests, name, e.Link); err != nil {
			return PlannedPack{}, err
		}
	}
	resolve := func(rel string) (string, bool, error) {
		return packroot.Resolve(root, "workspace", name, rel)
	}
	if err := manifest.ValidateSourcesWith(m, resolve, nil, "workspace manifest"); err != nil {
		return PlannedPack{}, err
	}
	pack := PlannedPack{
		Name: name, Files: make([]PlannedFile, 0, len(m.Render)), Symlinks: m.Symlinks,
	}
	for _, e := range m.Render {
		file, err := packcommon.PrepareFile(
			"workspace", root, name, e.From, data, strings.HasSuffix(e.From, ".tmpl"),
		)
		if err != nil {
			return PlannedPack{}, err
		}
		if _, err := packcommon.CheckDest(e.To, root, root, destLabels); err != nil {
			return PlannedPack{}, err
		}
		pack.Files = append(pack.Files, PlannedFile{From: e.From, To: e.To, File: file})
	}
	for _, e := range m.Symlinks {
		if err := checkSymlinkDest(root, e.Link); err != nil {
			return PlannedPack{}, err
		}
	}
	return pack, nil
}

type destination struct {
	pack string
	path string
}

func addDestination(dests *[]destination, pack, path string) error {
	clean := filepath.Clean(path)
	parts := strings.Split(clean, string(filepath.Separator))
	for _, protected := range []string{"workspace.yml", "workspace", "services", ".dwe", ".git", ".env"} {
		if strings.EqualFold(parts[0], protected) {
			return fmt.Errorf("destination %q is protected", path)
		}
	}
	for _, other := range *dests {
		if pathsOverlap(clean, other.path) {
			return fmt.Errorf("destination %q collides with %q from pack %q", path, other.path, other.pack)
		}
	}
	*dests = append(*dests, destination{pack: pack, path: clean})
	return nil
}

func pathsOverlap(a, b string) bool {
	aParts := strings.Split(a, string(filepath.Separator))
	bParts := strings.Split(b, string(filepath.Separator))
	for i := 0; i < min(len(aParts), len(bParts)); i++ {
		if !strings.EqualFold(aParts[i], bParts[i]) {
			return false
		}
	}
	return true
}

func checkSymlinkDest(root, link string) error {
	absLink := filepath.Join(root, link)
	if err := pathsafe.CheckNoSymlinks(root, filepath.Dir(absLink), "symlink parent dir"); err != nil {
		return err
	}
	fi, err := os.Lstat(absLink)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat symlink %q: %w", link, err)
		}
		_, err := packcommon.CheckDest(link, root, root, destLabels)
		return err
	}
	// Existing links can be retargeted, but EnsureRelativeSymlink never replaces
	// regular files or directories. Check this before any writes begin.
	if fi.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("refuse to overwrite non-symlink file at %s", link)
	}
	return nil
}
