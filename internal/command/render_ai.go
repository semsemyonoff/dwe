package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"devbox-cli/internal/config"
	"devbox-cli/internal/pathsafe"
)

// resolveAgentsTemplatePack resolves a template pack directory for a service.
// Returns the absolute path to a directory under devbox/templates/agents/.
// Explicit is strict: if svc.AIDocs.Template is set and does not exist, returns an error.
// Implicit chain: service-name → default, with fallthrough only on ErrNotExist.
func resolveAgentsTemplatePack(svc config.ServiceConfig, projectRoot, serviceName string) (string, error) {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}

	// Validate template key and service name
	if err := validateIDETemplateKey(svc.AIDocs.Template); err != nil {
		return "", fmt.Errorf("invalid ai_docs.template %q: %w", svc.AIDocs.Template, err)
	}
	if err := validateServiceNameAsPackKey(serviceName); err != nil {
		return "", fmt.Errorf("service name cannot be used as implicit template pack key: %w", err)
	}

	// Explicit candidate (strict — hard error on any condition, including not-found; never falls through)
	if svc.AIDocs.Template != "" {
		candidate := filepath.Join(absRoot, "devbox", "templates", "agents", svc.AIDocs.Template)
		fi, err := os.Lstat(candidate)
		if err == nil {
			// Pack exists; validate it
			if fi.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("agents template pack %q is a symlink; symlinked packs are not supported", svc.AIDocs.Template)
			}
			if !fi.IsDir() {
				return "", fmt.Errorf("agents template pack %q is not a directory", svc.AIDocs.Template)
			}
			return candidate, nil
		}
		// Any error other than not-exists is a hard error
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat agents template pack %q: %w", svc.AIDocs.Template, err)
		}
		// ErrNotExist with explicit template: strict error, no fallthrough
		return "", fmt.Errorf("agents template pack %q not found (required by explicit ai_docs.template setting)", svc.AIDocs.Template)
	}

	// Implicit chain: service-name → default
	candidates := []string{serviceName, "default"}
	for _, name := range candidates {
		candidate := filepath.Join(absRoot, "devbox", "templates", "agents", name)
		fi, err := os.Lstat(candidate)
		if err == nil {
			// Candidate exists; validate it
			if fi.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("agents template pack %q is a symlink; symlinked packs are not supported", name)
			}
			if !fi.IsDir() {
				return "", fmt.Errorf("agents template pack %q is not a directory", name)
			}
			return candidate, nil
		}
		// Only ErrNotExist advances to next candidate; any other error is hard
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat agents template pack %q: %w", name, err)
		}
		// ErrNotExist: continue to next candidate
	}

	// No pack found in implicit chain
	return "", fmt.Errorf("agents template pack not found (tried %s, default): %w", serviceName, os.ErrNotExist)
}

// agentsRenderEntry describes a template file to render.
type agentsRenderEntry struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// agentsSymlinkEntry describes a relative symlink to create.
type agentsSymlinkEntry struct {
	Link string `yaml:"link"`
	To   string `yaml:"to"`
}

// agentsManifest defines the agents template pack manifest.
type agentsManifest struct {
	Render   []agentsRenderEntry  `yaml:"render"`
	Symlinks []agentsSymlinkEntry `yaml:"symlinks"`
}

// loadAgentsManifest loads and parses manifest.yml from the pack directory.
// Uses strict YAML decode: unknown fields are an error.
func loadAgentsManifest(packDir string) (*agentsManifest, error) {
	manifestPath := filepath.Join(packDir, "manifest.yml")
	f, err := os.Open(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("open agents manifest: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	var m agentsManifest
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode agents manifest: %w", err)
	}

	return &m, nil
}

// validateAgentsManifest validates the manifest structure and all entries.
// Ensures `from` files exist and don't escape the pack, `to`/`link` paths
// don't escape the hub, symlinks reference rendered outputs, and no duplicates exist.
func validateAgentsManifest(m *agentsManifest, packDir string) error {
	// Reject empty manifest (both lists empty is almost certainly a mistake)
	if len(m.Render) == 0 && len(m.Symlinks) == 0 {
		return errors.New("agents manifest is empty: must define at least one render or symlink entry")
	}

	absPackDir, err := filepath.Abs(packDir)
	if err != nil {
		return fmt.Errorf("resolve pack directory: %w", err)
	}

	// Collect valid render destinations for symlink validation
	renderDests := make(map[string]bool)
	// Track seen destinations and links to detect duplicates
	seenRenderDests := make(map[string]bool)
	seenSymlinkLinks := make(map[string]bool)

	// Validate all render entries
	for i, e := range m.Render {
		if err := validateRenderEntry(e, absPackDir, renderDests, seenRenderDests, i); err != nil {
			return err
		}
	}

	// Validate all symlink entries
	for i, e := range m.Symlinks {
		if err := validateSymlinkEntry(e, renderDests, seenSymlinkLinks, i); err != nil {
			return err
		}
	}

	return nil
}

// validateRenderEntry validates a single render entry.
func validateRenderEntry(e agentsRenderEntry, absPackDir string, renderDests map[string]bool, seenDests map[string]bool, idx int) error {
	prefix := fmt.Sprintf("render[%d]: ", idx)

	// Validate `from`
	if e.From == "" {
		return fmt.Errorf("%sfrom is required and must not be empty", prefix)
	}

	// Must be relative (no absolute, no leading /)
	if filepath.IsAbs(e.From) {
		return fmt.Errorf("%sfrom must be relative (got absolute path %q)", prefix, e.From)
	}

	// Must end in .tmpl
	if !strings.HasSuffix(e.From, ".tmpl") {
		return fmt.Errorf("%sfrom must end in .tmpl (got %q)", prefix, e.From)
	}

	// Resolve and check for escaping
	absSource := filepath.Join(absPackDir, e.From)
	rel, err := pathsafe.ContainedRel(absPackDir, absSource)
	if err != nil {
		return fmt.Errorf("%sfrom escapes pack directory: %w", prefix, err)
	}
	if rel == "" {
		return fmt.Errorf("%sfrom resolves to pack directory itself (must be a file)", prefix)
	}

	// Check for symlinks in the path (including parent directories)
	if err := pathsafe.CheckNoSymlinks(absPackDir, filepath.Dir(absSource), "agents template pack"); err != nil {
		return fmt.Errorf("%sfrom path contains symlink: %w", prefix, err)
	}

	// File must exist as a regular file (not symlink, not directory)
	fi, err := os.Lstat(absSource)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%sfrom file does not exist: %s", prefix, absSource)
		}
		return fmt.Errorf("%sstat from file: %w", prefix, err)
	}

	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%sfrom must not be a symlink: %q", prefix, e.From)
	}

	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%sfrom must be a regular file (not directory or device): %q", prefix, e.From)
	}

	// Validate `to`
	if e.To == "" {
		return fmt.Errorf("%sto is required and must not be empty", prefix)
	}

	// Must be relative
	if filepath.IsAbs(e.To) {
		return fmt.Errorf("%sto must be relative (got absolute path %q)", prefix, e.To)
	}

	// Cleaned form must not be . or .. and must not start with ../
	cleaned := filepath.Clean(e.To)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%sto must not escape hub directory (resolved to %q)", prefix, cleaned)
	}

	// Check for duplicates
	if seenDests[e.To] {
		return fmt.Errorf("%sduplicate render destination %q", prefix, e.To)
	}
	seenDests[e.To] = true

	// Track for symlink validation
	renderDests[e.To] = true

	return nil
}

// validateSymlinkEntry validates a single symlink entry.
func validateSymlinkEntry(e agentsSymlinkEntry, renderDests map[string]bool, seenLinks map[string]bool, idx int) error {
	prefix := fmt.Sprintf("symlinks[%d]: ", idx)

	// Validate `link`
	if e.Link == "" {
		return fmt.Errorf("%slink is required and must not be empty", prefix)
	}

	if filepath.IsAbs(e.Link) {
		return fmt.Errorf("%slink must be relative (got absolute path %q)", prefix, e.Link)
	}

	cleaned := filepath.Clean(e.Link)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%slink must not escape hub directory (resolved to %q)", prefix, cleaned)
	}

	// Check for duplicates
	if seenLinks[e.Link] {
		return fmt.Errorf("%sduplicate symlink link %q", prefix, e.Link)
	}
	seenLinks[e.Link] = true

	// Validate `to`
	if e.To == "" {
		return fmt.Errorf("%sto is required and must not be empty", prefix)
	}

	// Must match one of the render destinations (cleaned comparison)
	toClean := filepath.Clean(e.To)
	if !renderDests[toClean] {
		// Check if it matches any render dest's cleaned form
		found := false
		for dest := range renderDests {
			if filepath.Clean(dest) == toClean {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%ssymlink to %q does not match any render destination", prefix, e.To)
		}
	}

	return nil
}
