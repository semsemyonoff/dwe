// Package ide provides IDE template pack resolution and rendering.
//
// IDE packs are manifest-driven (same schema as AI/git): each pack has a
// manifest.yml declaring `render:` and `symlinks:` entries.
package ide

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/semsemyonoff/dwe/internal/core/execution/templates/manifest"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/packcommon"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/packroot"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/pathsafe"
)

// SkippedService carries information about a service that was skipped during IDE rendering.
type SkippedService struct {
	Name   string // service name
	Reason string // "service-disabled" | "ide-disabled" | "ide-policy" | "empty-dir" | "lost-collision"
	Dir    string // set for "lost-collision" only
	Winner string // set for "lost-collision" only (name of the winning service)
}

// RenderEntry describes a template file to render. Alias to the shared schema.
type RenderEntry = manifest.RenderEntry

// SymlinkEntry describes a relative symlink to create. Alias to the shared schema.
type SymlinkEntry = manifest.SymlinkEntry

// Manifest defines the IDE template pack manifest. Alias to the shared schema.
type Manifest = manifest.File

// ImplicitPackCandidates returns the implicit-chain pack name candidates for a
// service. See packcommon.ImplicitPackCandidates.
var ImplicitPackCandidates = packcommon.ImplicitPackCandidates

// ExtendsDepth computes the depth of a service's extends chain.
// See packcommon.ExtendsDepth.
var ExtendsDepth = packcommon.ExtendsDepth

// ExtendsRoot walks the extends chain from name and returns the chain root.
// See packcommon.ExtendsRoot.
var ExtendsRoot = packcommon.ExtendsRoot

// SelectServices filters and resolves IDE-enabled services.
func SelectServices(services map[string]config.ServiceConfig) (selected []string, skipped []SkippedService) {
	var allSkipped []SkippedService

	enabled := make(map[string]config.ServiceConfig)
	for name, svc := range services {
		if !svc.Enabled {
			allSkipped = append(allSkipped, SkippedService{Name: name, Reason: "service-disabled"})
			continue
		}
		if ideEnabled, explicit := svc.IDERenderEnabledExplicit(); !ideEnabled {
			reason := "ide-policy"
			if explicit {
				reason = "ide-disabled"
			}
			allSkipped = append(allSkipped, SkippedService{Name: name, Reason: reason})
			continue
		}
		enabled[name] = svc
	}

	dirNormalized := make(map[string]config.ServiceConfig)
	for name, svc := range enabled {
		if strings.TrimSpace(svc.Dir) == "" || filepath.Clean(svc.Dir) == "." {
			allSkipped = append(allSkipped, SkippedService{Name: name, Reason: "empty-dir"})
			continue
		}
		dirNormalized[name] = svc
	}

	dirGroups := make(map[string][]string)
	for name, svc := range dirNormalized {
		cleanDir := filepath.Clean(svc.Dir)
		dirGroups[cleanDir] = append(dirGroups[cleanDir], name)
	}

	selectedSet := make(map[string]bool)
	for dir, names := range dirGroups {
		if len(names) == 1 {
			selectedSet[names[0]] = true
			continue
		}

		sort.Strings(names)
		var deepest string
		maxDepth := -1
		for _, name := range names {
			depth, _ := ExtendsDepth(services, name)
			if depth > maxDepth {
				maxDepth = depth
				deepest = name
			}
		}

		selectedSet[deepest] = true
		for _, name := range names {
			if name != deepest {
				allSkipped = append(allSkipped, SkippedService{
					Name:   name,
					Reason: "lost-collision",
					Dir:    dir,
					Winner: deepest,
				})
			}
		}
	}

	for name := range selectedSet {
		selected = append(selected, name)
	}
	sort.Strings(selected)

	sort.Slice(allSkipped, func(i, j int) bool {
		return allSkipped[i].Name < allSkipped[j].Name
	})
	skipped = allSkipped

	return selected, skipped
}

// ValidateTemplateKey validates that s is a single directory key without path traversal.
func ValidateTemplateKey(s string) error {
	if s == "" {
		return nil
	}
	if strings.ContainsAny(s, "/\\") {
		return fmt.Errorf("template key %q contains path separator", s)
	}
	if strings.HasPrefix(s, ".") {
		return fmt.Errorf("template key %q starts with dot", s)
	}
	return nil
}

// ResolveTemplatePack resolves a template pack directory for a service.
// Returns (packDir, packName, found, err). Explicit svc.Render.IDE.Template is strict.
// Implicit chain: serviceName → ancestors via Extends → default; returns
// found=false when exhausted. Invalid pack names in the chain are skipped
// silently. Semantics: err != nil means hard failure; err == nil && !found
// means implicit chain exhausted.
func ResolveTemplatePack(svc config.ServiceConfig, services map[string]config.ServiceConfig, projectRoot, serviceName string) (string, string, bool, error) {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", "", false, fmt.Errorf("resolve project root: %w", err)
	}

	if svc.Render.IDE.Template != "" {
		if err := manifest.ValidatePackName(svc.Render.IDE.Template); err != nil {
			return "", "", false, fmt.Errorf("invalid render.ide.template %q: %w", svc.Render.IDE.Template, err)
		}
		candidate := filepath.Join(absRoot, "workspace", "templates", "ide", svc.Render.IDE.Template)
		fi, err := os.Lstat(candidate)
		if err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return "", "", false, fmt.Errorf("ide template pack %q is a symlink; symlinked packs are not supported", svc.Render.IDE.Template)
			}
			if !fi.IsDir() {
				return "", "", false, fmt.Errorf("ide template pack %q is not a directory", svc.Render.IDE.Template)
			}
			if err := pathsafe.CheckNoSymlinks(absRoot, candidate, "ide template pack"); err != nil {
				return "", "", false, err
			}
			return candidate, svc.Render.IDE.Template, true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", "", false, fmt.Errorf("stat ide template pack %q: %w", svc.Render.IDE.Template, err)
		}
		return "", "", false, fmt.Errorf("ide template pack %q not found (required by explicit render.ide.template setting)", svc.Render.IDE.Template)
	}

	candidates := ImplicitPackCandidates(services, serviceName)
	for _, name := range candidates {
		candidate := filepath.Join(absRoot, "workspace", "templates", "ide", name)
		fi, err := os.Lstat(candidate)
		if err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return "", "", false, fmt.Errorf("ide template pack %q is a symlink; symlinked packs are not supported", name)
			}
			if !fi.IsDir() {
				return "", "", false, fmt.Errorf("ide template pack %q is not a directory", name)
			}
			if err := pathsafe.CheckNoSymlinks(absRoot, candidate, "ide template pack"); err != nil {
				return "", "", false, err
			}
			return candidate, name, true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", "", false, fmt.Errorf("stat ide template pack %q: %w", name, err)
		}
	}

	return "", "", false, nil
}

// LoadManifest loads and parses manifest.yml from the pack directory.
// IDE packs require a manifest.yml; missing manifest is a hard error and the
// returned chain includes manifest.ErrManifestMissing.
func LoadManifest(packDir string) (*Manifest, error) {
	return manifest.Load(filepath.Join(packDir, "manifest.yml"))
}

// ValidateManifest validates the manifest against shape rules (pure) and then
// verifies each render source is resolvable via packroot.Resolve, so a `from`
// satisfied only by the sibling `<pack>.local/` override is treated as valid.
// destRoot is the hub directory (the service dir).
func ValidateManifest(m *Manifest, projectRoot, packName, destRoot string, sink ...func(rel string, fromOverride bool)) error {
	label := "ide pack " + packName
	if err := manifest.ValidateShape(m, destRoot, label); err != nil {
		return err
	}
	resolve := func(rel string) (string, bool, error) {
		return packroot.Resolve(projectRoot, "ide", packName, rel)
	}
	var s func(string, bool)
	if len(sink) > 0 {
		s = sink[0]
	}
	return manifest.ValidateSourcesWith(m, resolve, s, label)
}

// TemplateData is passed to IDE config templates. Alias to the shared schema.
type TemplateData = packcommon.TemplateData

// DryRunRender resolves, parses, and executes every render entry in m against
// data without writing to disk. Returns a map from manifest `from` path to the
// first error encountered for that entry (parse, source-read, or execution
// errors — typically missingkey=error). On success returns nil.
func DryRunRender(projectRoot, packName string, m *Manifest, data TemplateData) map[string]error {
	return packcommon.DryRunRender("ide", projectRoot, packName, m, data)
}

// RenderTemplateFile resolves rel via packroot (override first, canonical
// fallback), renders it with data, and writes the result to dest under
// absHubDir. Returns fromOverride=true when the sibling override pack supplied
// the source.
func RenderTemplateFile(projectRoot, packName, rel string, data TemplateData, dest, absHubDir, absRoot string) (bool, error) {
	if data.Cfg == nil {
		return false, errors.New("ide: nil cfg")
	}
	sourcePath, fromOverride, err := packroot.Resolve(projectRoot, "ide", packName, rel)
	if err != nil {
		return false, fmt.Errorf("resolve template %s: %w", rel, err)
	}

	tplBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		return false, fmt.Errorf("read template %s: %w", sourcePath, err)
	}

	name := filepath.Base(sourcePath)
	t, err := template.New(name).Option("missingkey=error").Parse(string(tplBytes))
	if err != nil {
		return false, fmt.Errorf("parse template %s: %w", name, err)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return false, fmt.Errorf("render template %s: %w", name, err)
	}

	absDest, err := filepath.Abs(filepath.Join(absHubDir, dest))
	if err != nil {
		return false, fmt.Errorf("resolve destination: %w", err)
	}
	if _, err := pathsafe.ContainedRel(absHubDir, absDest); err != nil {
		return false, fmt.Errorf("dest %q escapes service dir: %w", dest, err)
	}

	destDir := filepath.Dir(absDest)
	if err := pathsafe.CheckNoSymlinks(absRoot, destDir, "destination dir"); err != nil {
		return false, err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return false, fmt.Errorf("create dir for %s: %w", dest, err)
	}

	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return false, fmt.Errorf("resolve project root: %w", err)
	}
	realHubDir, err := filepath.EvalSymlinks(absHubDir)
	if err != nil {
		return false, fmt.Errorf("resolve service dir: %w", err)
	}
	realDir, err := filepath.EvalSymlinks(destDir)
	if err != nil {
		return false, fmt.Errorf("resolve dir for %s: %w", dest, err)
	}
	if err := pathsafe.EnsureRealUnder(realDir, realRoot, realHubDir); err != nil {
		return false, fmt.Errorf("destination dir for %q resolves outside required boundaries via symlink: %w", dest, err)
	}

	if fi, err := os.Lstat(absDest); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("destination %q is a symlink; will not overwrite", dest)
	}

	if err := os.WriteFile(absDest, buf.Bytes(), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", dest, err)
	}
	return fromOverride, nil
}

// EnsureRelativeSymlink creates or updates a relative symlink inside the hub
// dir. If the symlink already points to the correct target, it is left
// unchanged. If a non-symlink file exists at linkPath, returns an error.
func EnsureRelativeSymlink(linkPath, targetWithinHub, absHubDir, absRoot string) error {
	absLink := filepath.Join(absHubDir, linkPath)
	absTarget := filepath.Join(absHubDir, targetWithinHub)

	if _, err := pathsafe.ContainedRel(absHubDir, absLink); err != nil {
		return fmt.Errorf("symlink link %q escapes hub directory: %w", linkPath, err)
	}
	if _, err := pathsafe.ContainedRel(absHubDir, absTarget); err != nil {
		return fmt.Errorf("symlink target %q escapes hub directory: %w", targetWithinHub, err)
	}

	linkDir := filepath.Dir(absLink)
	if err := pathsafe.CheckNoSymlinks(absRoot, linkDir, "symlink parent dir"); err != nil {
		return err
	}
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		return fmt.Errorf("create dir for symlink %s: %w", linkPath, err)
	}

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

	relTarget, err := filepath.Rel(linkDir, absTarget)
	if err != nil {
		return fmt.Errorf("compute relative path: %w", err)
	}
	if relTarget == "" {
		return fmt.Errorf("symlink target resolves to empty relative path")
	}

	if fi, err := os.Lstat(absLink); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			currentTarget, err := os.Readlink(absLink)
			if err != nil {
				return fmt.Errorf("read symlink %s: %w", linkPath, err)
			}
			if currentTarget == relTarget {
				return nil
			}
			if err := os.Remove(absLink); err != nil {
				return fmt.Errorf("remove symlink %s: %w", linkPath, err)
			}
			if err := os.Symlink(relTarget, absLink); err != nil {
				return fmt.Errorf("create symlink %s: %w", linkPath, err)
			}
			return nil
		}
		return fmt.Errorf("refuse to overwrite non-symlink file at %s", linkPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", linkPath, err)
	}

	if err := os.Symlink(relTarget, absLink); err != nil {
		return fmt.Errorf("create symlink %s: %w", linkPath, err)
	}
	return nil
}
