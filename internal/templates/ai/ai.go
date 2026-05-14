// Package ai provides agents template pack resolution and rendering.
package ai

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"

	"devbox-cli/internal/config"
	"devbox-cli/internal/pathsafe"
)

// SkippedService carries information about a service that was skipped during AI rendering.
type SkippedService struct {
	Name   string // service name
	Reason string // "service-disabled" | "ai-disabled" | "empty-dir" | "lost-collision"
	Dir    string // set for "lost-collision" only
	Winner string // set for "lost-collision" only (name of the winning service)
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

// Manifest defines the agents template pack manifest.
type Manifest struct {
	Render   []RenderEntry  `yaml:"render"`
	Symlinks []SymlinkEntry `yaml:"symlinks"`
}

// ValidateTemplateKey validates that s is a single directory key without path traversal.
// It rejects path separators, absolute paths, and leading dots (which subsumes "..").
func ValidateTemplateKey(s string) error {
	if s == "" {
		return nil // empty is valid (field is optional)
	}
	// Reject path separators
	if strings.ContainsAny(s, "/\\") {
		return fmt.Errorf("template key %q contains path separator", s)
	}
	// Reject leading dots (subsumes ".." and hidden-file keys)
	if strings.HasPrefix(s, ".") {
		return fmt.Errorf("template key %q starts with dot", s)
	}
	return nil
}

// ValidateServiceNameAsPackKey validates that a service name is safe to use as an
// implicit AI template pack directory name. Less restrictive than ValidateTemplateKey:
// allows leading dots since service names are YAML map keys, not user-typed path components.
func ValidateServiceNameAsPackKey(s string) error {
	if s == "" {
		return fmt.Errorf("service name is empty")
	}
	if strings.ContainsAny(s, "/\\") {
		return fmt.Errorf("service name %q contains path separator", s)
	}
	if s == ".." || strings.HasPrefix(s, "../") || strings.HasPrefix(s, "..\\") {
		return fmt.Errorf("service name %q is a path traversal", s)
	}
	return nil
}

// ExtendsDepth computes the depth of a service's extends chain.
// Returns (depth, capped): depth is the number of hops to the root;
// capped is true if depth hit the 32-hop limit (defense-in-depth cycle guard).
func ExtendsDepth(services map[string]config.ServiceConfig, name string) (int, bool) {
	const maxDepth = 32
	depth := 0
	current := name
	for {
		if depth >= maxDepth {
			return maxDepth, true
		}
		svc, ok := services[current]
		if !ok || svc.Extends == "" {
			return depth, false
		}
		current = svc.Extends
		depth++
	}
}

// ResolveTemplatePack resolves a template pack directory for a service.
// Returns the absolute path to a directory under devbox/templates/ai/.
// Explicit is strict: if svc.AI.Template is set and does not exist, returns an error.
// Implicit chain: service-name → default, with fallthrough only on ErrNotExist.
func ResolveTemplatePack(svc config.ServiceConfig, projectRoot, serviceName string) (string, error) {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}

	// Validate template key and service name
	if err := ValidateTemplateKey(svc.AI.Template); err != nil {
		return "", fmt.Errorf("invalid ai.template %q: %w", svc.AI.Template, err)
	}
	if err := ValidateServiceNameAsPackKey(serviceName); err != nil {
		return "", fmt.Errorf("service name cannot be used as implicit template pack key: %w", err)
	}

	// Explicit candidate (strict — hard error on any condition, including not-found; never falls through)
	if svc.AI.Template != "" {
		candidate := filepath.Join(absRoot, "devbox", "templates", "ai", svc.AI.Template)
		fi, err := os.Lstat(candidate)
		if err == nil {
			// Pack exists; validate it
			if fi.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("agents template pack %q is a symlink; symlinked packs are not supported", svc.AI.Template)
			}
			if !fi.IsDir() {
				return "", fmt.Errorf("agents template pack %q is not a directory", svc.AI.Template)
			}
			// Guard against symlinks in parent path components (e.g. devbox/templates/ai -> /tmp/outside)
			if err := pathsafe.CheckNoSymlinks(absRoot, candidate, "agents template pack"); err != nil {
				return "", err
			}
			return candidate, nil
		}
		// Any error other than not-exists is a hard error
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat agents template pack %q: %w", svc.AI.Template, err)
		}
		// ErrNotExist with explicit template: strict error, no fallthrough
		return "", fmt.Errorf("agents template pack %q not found (required by explicit ai.template setting)", svc.AI.Template)
	}

	// Implicit chain: service-name → default
	candidates := []string{serviceName, "default"}
	for _, name := range candidates {
		candidate := filepath.Join(absRoot, "devbox", "templates", "ai", name)
		fi, err := os.Lstat(candidate)
		if err == nil {
			// Candidate exists; validate it
			if fi.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("agents template pack %q is a symlink; symlinked packs are not supported", name)
			}
			if !fi.IsDir() {
				return "", fmt.Errorf("agents template pack %q is not a directory", name)
			}
			// Guard against symlinks in parent path components (e.g. devbox/templates/ai -> /tmp/outside)
			if err := pathsafe.CheckNoSymlinks(absRoot, candidate, "agents template pack"); err != nil {
				return "", err
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

// LoadManifest loads and parses manifest.yml from the pack directory.
// Uses strict YAML decode: unknown fields are an error.
func LoadManifest(packDir string) (*Manifest, error) {
	manifestPath := filepath.Join(packDir, "manifest.yml")
	f, err := os.Open(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("open agents manifest: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	var m Manifest
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("agents manifest %s is empty", manifestPath)
		}
		return nil, fmt.Errorf("decode agents manifest: %w", err)
	}

	return &m, nil
}

// ValidateManifest validates the manifest structure and all entries.
// Ensures `from` files exist and don't escape the pack, `to`/`link` paths
// don't escape the hub, symlinks reference rendered outputs, and no duplicates exist.
func ValidateManifest(m *Manifest, packDir string) error {
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
func validateRenderEntry(e RenderEntry, absPackDir string, renderDests map[string]bool, seenDests map[string]bool, idx int) error {
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
	if _, err := pathsafe.ContainedRel(absPackDir, absSource); err != nil {
		return fmt.Errorf("%sfrom escapes pack directory: %w", prefix, err)
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

	// Check for duplicates using cleaned form so "AGENTS.md" and "./AGENTS.md" are treated as equal.
	if seenDests[cleaned] {
		return fmt.Errorf("%sduplicate render destination %q", prefix, e.To)
	}
	seenDests[cleaned] = true

	// Track for symlink validation (cleaned so fast-path lookup in validateSymlinkEntry works)
	renderDests[cleaned] = true

	return nil
}

// validateSymlinkEntry validates a single symlink entry.
func validateSymlinkEntry(e SymlinkEntry, renderDests map[string]bool, seenLinks map[string]bool, idx int) error {
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

	// Check for duplicates using cleaned form so "CLAUDE.md" and "./CLAUDE.md" are treated as equal.
	if seenLinks[cleaned] {
		return fmt.Errorf("%sduplicate symlink link %q", prefix, e.Link)
	}
	seenLinks[cleaned] = true

	// Reject link paths that collide with render destinations: a path cannot be both a rendered file and a symlink
	if renderDests[cleaned] {
		return fmt.Errorf("%slink %q collides with a render destination; a path cannot be both a rendered file and a symlink", prefix, e.Link)
	}

	// Validate `to`
	if e.To == "" {
		return fmt.Errorf("%sto is required and must not be empty", prefix)
	}

	if filepath.IsAbs(e.To) {
		return fmt.Errorf("%sto must be relative (got absolute path %q)", prefix, e.To)
	}

	// Must match one of the render destinations (keys are pre-cleaned in validateRenderEntry)
	if !renderDests[filepath.Clean(e.To)] {
		return fmt.Errorf("%ssymlink to %q does not match any render destination", prefix, e.To)
	}

	return nil
}

// TemplateData holds the context for rendering agents templates.
type TemplateData struct {
	Project    config.ProjectConfig
	Service    string
	ServiceCfg config.ServiceConfig
	Runtime    config.RuntimeConfig
}

// RenderTemplateFile reads a template file, renders it with the given data,
// and writes the result to dest. It enforces that dest stays inside absHubDir
// and that absHubDir stays inside absRoot (via symlink checks and boundaries).
//
// sourcePath: absolute path to the template file (ends in .tmpl)
// data: template context
// dest: relative destination path (within hub dir)
// absHubDir: resolved absolute service hub directory
// absRoot: resolved absolute project root
func RenderTemplateFile(sourcePath string, data TemplateData, dest, absHubDir, absRoot string) error {
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

// EnsureRelativeSymlink creates or updates a relative symlink. Returns (changed, error).
// If the symlink already points to the correct target, returns (false, nil).
// If a non-symlink regular file exists at linkPath, returns (false, error) with a user-friendly message.
//
// linkPath: relative path to the symlink (within hub dir)
// targetWithinHub: the destination path as stored in manifest (also relative to hub)
// absHubDir: resolved absolute hub directory
// absRoot: resolved absolute project root
func EnsureRelativeSymlink(linkPath, targetWithinHub, absHubDir, absRoot string) error {
	// Resolve both paths to absolute form inside the hub
	absLink := filepath.Join(absHubDir, linkPath)
	absTarget := filepath.Join(absHubDir, targetWithinHub)

	// Validate both stay inside hub
	_, err := pathsafe.ContainedRel(absHubDir, absLink)
	if err != nil {
		return fmt.Errorf("symlink link %q escapes hub directory: %w", linkPath, err)
	}
	_, err = pathsafe.ContainedRel(absHubDir, absTarget)
	if err != nil {
		return fmt.Errorf("symlink target %q escapes hub directory: %w", targetWithinHub, err)
	}

	// Create parent directory for the symlink (with symlink guards)
	linkDir := filepath.Dir(absLink)
	if err := pathsafe.CheckNoSymlinks(absRoot, linkDir, "symlink parent dir"); err != nil {
		return err
	}
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		return fmt.Errorf("create dir for symlink %s: %w", linkPath, err)
	}

	// Verify linkDir resolves inside both root and hub after MkdirAll (TOCTOU guard)
	realLinkDir, err := filepath.EvalSymlinks(linkDir)
	if err != nil {
		return fmt.Errorf("resolve symlink parent dir: %w", err)
	}
	realHubDir, err := filepath.EvalSymlinks(absHubDir)
	if err != nil {
		return fmt.Errorf("resolve hub dir: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}
	if err := pathsafe.EnsureRealUnder(realLinkDir, realRoot, realHubDir); err != nil {
		return fmt.Errorf("symlink parent dir resolves outside required boundaries via symlink: %w", err)
	}

	// Compute relative target (from link's directory to absolute target)
	relTarget, err := filepath.Rel(linkDir, absTarget)
	if err != nil {
		return fmt.Errorf("compute relative path: %w", err)
	}
	if relTarget == "" {
		return fmt.Errorf("symlink target resolves to empty relative path")
	}

	// Inspect existing symlink
	if fi, err := os.Lstat(absLink); err == nil {
		// Path exists
		if fi.Mode()&os.ModeSymlink != 0 {
			// It's a symlink; check target
			currentTarget, err := os.Readlink(absLink)
			if err != nil {
				return fmt.Errorf("read symlink %s: %w", linkPath, err)
			}
			if currentTarget == relTarget {
				// Already points to correct target
				return nil
			}
			// Target changed; replace it
			if err := os.Remove(absLink); err != nil {
				return fmt.Errorf("remove symlink %s: %w", linkPath, err)
			}
			if err := os.Symlink(relTarget, absLink); err != nil {
				return fmt.Errorf("create symlink %s: %w", linkPath, err)
			}
			return nil
		}
		// Not a symlink (regular file or directory)
		return fmt.Errorf("refuse to overwrite non-symlink file at %s; remove it or disable via ai.enabled: false", linkPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", linkPath, err)
	}

	// Path does not exist; create symlink
	if err := os.Symlink(relTarget, absLink); err != nil {
		return fmt.Errorf("create symlink %s: %w", linkPath, err)
	}
	return nil
}

// SelectServices filters and resolves agents-enabled services.
// It returns a list of selected service names (sorted lexicographically) and
// a list of services that were skipped with reason-specific context.
//
// Selection logic mirrors IDE rendering except for the collision-resolution direction:
//  1. Gate on both flags: services where svc.Enabled==false or ai.enabled is explicitly false are dropped.
//  2. Normalize Dir: services with empty (after TrimSpace) Dir are dropped.
//  3. Group by filepath.Clean(Dir) and resolve collisions: when multiple services
//     share the same Dir, the **shallowest** extends chain wins (the canonical
//     hub owner — opposite of IDE's deepest-wins). Ties are broken lexicographically.
func SelectServices(services map[string]config.ServiceConfig) (selected []string, skipped []SkippedService) {
	var allSkipped []SkippedService

	// Step A: gate on both Enabled and AIRenderEnabled.
	enabled := make(map[string]config.ServiceConfig)
	for name, svc := range services {
		if !svc.Enabled {
			allSkipped = append(allSkipped, SkippedService{Name: name, Reason: "service-disabled"})
			continue
		}
		if !svc.AIRenderEnabled() {
			allSkipped = append(allSkipped, SkippedService{Name: name, Reason: "ai-disabled"})
			continue
		}
		enabled[name] = svc
	}

	// Step B: drop services with empty Dir or dir equal to project root (".").
	dirNormalized := make(map[string]config.ServiceConfig)
	for name, svc := range enabled {
		if strings.TrimSpace(svc.Dir) == "" || filepath.Clean(svc.Dir) == "." {
			allSkipped = append(allSkipped, SkippedService{Name: name, Reason: "empty-dir"})
			continue
		}
		dirNormalized[name] = svc
	}

	// Step C: group by filepath.Clean(Dir) and resolve collisions.
	dirGroups := make(map[string][]string)
	for name, svc := range dirNormalized {
		cleanDir := filepath.Clean(svc.Dir)
		dirGroups[cleanDir] = append(dirGroups[cleanDir], name)
	}

	// For each group, pick the winner (shallowest extends chain; tie-break by name).
	// Rationale: the agent docs describe the hub's canonical identity. When a child
	// `extends` a parent and shares its `dir`, the parent owns the hub — the child
	// is a runtime variant, not a separate workspace.
	selectedSet := make(map[string]bool)
	for dir, names := range dirGroups {
		if len(names) == 1 {
			selectedSet[names[0]] = true
			continue
		}

		// Multiple services share this dir: find the shallowest extends chain.
		sort.Strings(names) // tie-break: lexicographically first among shallowest
		var shallowest string
		minDepth := -1
		for _, name := range names {
			depth, _ := ExtendsDepth(services, name)
			if minDepth == -1 || depth < minDepth {
				minDepth = depth
				shallowest = name
			}
		}

		selectedSet[shallowest] = true
		for _, name := range names {
			if name != shallowest {
				allSkipped = append(allSkipped, SkippedService{
					Name:   name,
					Reason: "lost-collision",
					Dir:    dir,
					Winner: shallowest,
				})
			}
		}
	}

	// Collect selected names and sort.
	for name := range selectedSet {
		selected = append(selected, name)
	}
	sort.Strings(selected)

	// Sort skipped by name for determinism.
	sort.Slice(allSkipped, func(i, j int) bool {
		return allSkipped[i].Name < allSkipped[j].Name
	})
	skipped = allSkipped

	return selected, skipped
}
