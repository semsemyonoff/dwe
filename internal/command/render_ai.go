package command

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"

	"devbox-cli/internal/config"
	"devbox-cli/internal/pathsafe"
	"devbox-cli/internal/render"
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

// agentsTemplateData holds the context for rendering agents templates.
type agentsTemplateData struct {
	Project    config.ProjectConfig
	Service    string
	ServiceCfg config.ServiceConfig
	Runtime    config.RuntimeConfig
}

// renderAgentsTemplateFile reads a template file, renders it with the given data,
// and writes the result to dest. It enforces that dest stays inside absHubDir
// and that absHubDir stays inside absRoot (via symlink checks and boundaries).
//
// sourcePath: absolute path to the template file (ends in .tmpl)
// data: template context
// dest: relative destination path (within hub dir)
// absHubDir: resolved absolute service hub directory
// absRoot: resolved absolute project root
func renderAgentsTemplateFile(sourcePath string, data agentsTemplateData, dest, absHubDir, absRoot string) error {
	// Read template file
	tplBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read template %s: %w", sourcePath, err)
	}

	// Parse template with missingkey=error
	name := filepath.Base(sourcePath)
	t, err := template.New(name).Option("missingkey=error").Parse(string(tplBytes))
	if err != nil {
		return fmt.Errorf("parse template %s: %w", name, err)
	}

	// Render template to buffer
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return fmt.Errorf("render template %s: %w", name, err)
	}

	// Resolve destination and check containment within hub
	absDest, err := filepath.Abs(filepath.Join(absHubDir, dest))
	if err != nil {
		return fmt.Errorf("resolve destination: %w", err)
	}

	// Containment check: dest must be inside absHubDir
	_, err = pathsafe.ContainedRel(absHubDir, absDest)
	if err != nil {
		return fmt.Errorf("destination %q escapes hub directory: %w", dest, err)
	}

	destDir := filepath.Dir(absDest)

	// Guard against symlinks in the destination path before creating directories
	if err := pathsafe.CheckNoSymlinks(absRoot, destDir, "destination dir"); err != nil {
		return err
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", dest, err)
	}

	// Verify real directory resolves inside both root and hub after creation
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}
	realHubDir, err := filepath.EvalSymlinks(absHubDir)
	if err != nil {
		return fmt.Errorf("resolve hub dir: %w", err)
	}
	realDir, err := filepath.EvalSymlinks(destDir)
	if err != nil {
		return fmt.Errorf("resolve dir for %s: %w", dest, err)
	}
	if err := pathsafe.EnsureRealUnder(realDir, realRoot, realHubDir); err != nil {
		return fmt.Errorf("destination dir for %q resolves outside required boundaries via symlink: %w", dest, err)
	}

	// Refuse to write through a symlinked destination file
	if fi, err := os.Lstat(absDest); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("destination %q is a symlink; will not overwrite", dest)
	}

	if err := os.WriteFile(absDest, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}

	return nil
}

// ensureRelativeSymlink creates or updates a relative symlink. Returns (changed, error).
// If the symlink already points to the correct target, returns (false, nil).
// If a non-symlink regular file exists at linkPath, returns (false, error) with a user-friendly message.
//
// linkPath: relative path to the symlink (within hub dir)
// targetWithinHub: the destination path as stored in manifest (also relative to hub)
// absHubDir: resolved absolute hub directory
// absRoot: resolved absolute project root
func ensureRelativeSymlink(linkPath, targetWithinHub, absHubDir, absRoot string) (changed bool, err error) {
	// Resolve both paths to absolute form inside the hub
	absLink := filepath.Join(absHubDir, linkPath)
	absTarget := filepath.Join(absHubDir, targetWithinHub)

	// Validate both stay inside hub
	_, err = pathsafe.ContainedRel(absHubDir, absLink)
	if err != nil {
		return false, fmt.Errorf("symlink link %q escapes hub directory: %w", linkPath, err)
	}
	_, err = pathsafe.ContainedRel(absHubDir, absTarget)
	if err != nil {
		return false, fmt.Errorf("symlink target %q escapes hub directory: %w", targetWithinHub, err)
	}

	// Create parent directory for the symlink (with symlink guards)
	linkDir := filepath.Dir(absLink)
	if err := pathsafe.CheckNoSymlinks(absRoot, linkDir, "symlink parent dir"); err != nil {
		return false, err
	}
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		return false, fmt.Errorf("create dir for symlink %s: %w", linkPath, err)
	}

	// Compute relative target (from link's directory to absolute target)
	relTarget, err := filepath.Rel(linkDir, absTarget)
	if err != nil {
		return false, fmt.Errorf("compute relative path: %w", err)
	}
	if relTarget == "" {
		return false, fmt.Errorf("symlink target resolves to empty relative path")
	}

	// Inspect existing symlink
	if fi, err := os.Lstat(absLink); err == nil {
		// Path exists
		if fi.Mode()&os.ModeSymlink != 0 {
			// It's a symlink; check target
			currentTarget, err := os.Readlink(absLink)
			if err != nil {
				return false, fmt.Errorf("read symlink %s: %w", linkPath, err)
			}
			if currentTarget == relTarget {
				// Already points to correct target
				return false, nil
			}
			// Target changed; replace it
			if err := os.Remove(absLink); err != nil {
				return false, fmt.Errorf("remove symlink %s: %w", linkPath, err)
			}
			if err := os.Symlink(relTarget, absLink); err != nil {
				return false, fmt.Errorf("create symlink %s: %w", linkPath, err)
			}
			return true, nil
		}
		// Not a symlink (regular file or directory)
		return false, fmt.Errorf("refuse to overwrite non-symlink file at %s; remove it or disable via ai_docs.enabled: false", linkPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat %s: %w", linkPath, err)
	}

	// Path does not exist; create symlink
	if err := os.Symlink(relTarget, absLink); err != nil {
		return false, fmt.Errorf("create symlink %s: %w", linkPath, err)
	}
	return true, nil
}

// renderAgentsForService renders a single service's agents documentation.
// It resolves the template pack, loads and validates the manifest, and renders
// each entry in the manifest (files + symlinks).
func renderAgentsForService(projectRoot, name string, svc config.ServiceConfig, cfg *config.DevboxConfig, w *render.Writer) error {
	// Validate that service has a directory
	if strings.TrimSpace(svc.Dir) == "" {
		return fmt.Errorf("service %q has no dir; cannot render agents docs", name)
	}

	// Resolve paths
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}
	absHubDir := filepath.Join(absRoot, svc.Dir)

	// Validate hub dir is inside root (not equal)
	_, err = pathsafe.ContainedRel(absRoot, absHubDir)
	if err != nil {
		return fmt.Errorf("service dir escapes project root: %w", err)
	}

	// Check for symlinks in the hub dir path
	if err := pathsafe.CheckNoSymlinks(absRoot, absHubDir, "service dir"); err != nil {
		return err
	}

	// Resolve template pack
	pack, err := resolveAgentsTemplatePack(svc, projectRoot, name)
	if err != nil {
		return err
	}

	// Load and validate manifest
	manifest, err := loadAgentsManifest(pack)
	if err != nil {
		return err
	}
	if err := validateAgentsManifest(manifest, pack); err != nil {
		return fmt.Errorf("invalid agents manifest: %w", err)
	}

	// Prepare template data
	data := agentsTemplateData{
		Project:    cfg.Project,
		Service:    name,
		ServiceCfg: svc,
		Runtime:    cfg.Runtime,
	}

	// Render each file in the manifest
	for _, entry := range manifest.Render {
		sourcePath := filepath.Join(pack, entry.From)
		if err := renderAgentsTemplateFile(sourcePath, data, entry.To, absHubDir, absRoot); err != nil {
			return err
		}
		w.Success(fmt.Sprintf("ai → %s", filepath.Join(svc.Dir, entry.To)))
	}

	// Create each symlink in the manifest
	for _, entry := range manifest.Symlinks {
		changed, err := ensureRelativeSymlink(entry.Link, entry.To, absHubDir, absRoot)
		if err != nil {
			return err
		}
		if changed {
			w.Success(fmt.Sprintf("ai → %s ⇒ %s", filepath.Join(svc.Dir, entry.Link), entry.To))
		}
	}

	return nil
}
