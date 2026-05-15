// Package git provides git-hooks template pack resolution and rendering.
//
// The git renderer mirrors AI/IDE in pack discovery and override handling but
// writes to <svc.Dir>/src/.git/hooks/ with mode 0755. Worktree/submodule
// support (where .git is a file pointer) is deferred — such services are
// skipped with a warning.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"devbox-cli/internal/config"
	"devbox-cli/internal/pathsafe"
	"devbox-cli/internal/render"
	"devbox-cli/internal/templates/manifest"
	"devbox-cli/internal/templates/packroot"
)

// SkippedService carries information about a service that was skipped during git rendering.
type SkippedService struct {
	Name   string // service name
	Reason string // "service-disabled" | "git-disabled" | "empty-dir" | "lost-collision"
	Dir    string // set for "lost-collision" only
	Winner string // set for "lost-collision" only (name of the winning service)
}

// DirStatus describes the state of <svc.Dir>/src/.git.
type DirStatus int

const (
	// DirOK indicates src/.git is a regular directory and hooks can be written.
	DirOK DirStatus = iota
	// DirMissing indicates src/.git does not exist.
	DirMissing
	// DirWorktree indicates src/.git is a file (worktree or submodule pointer).
	DirWorktree
)

// validateTemplateKey rejects path separators, absolute paths, and leading dots.
func validateTemplateKey(s string) error {
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

// validateServiceNameAsPackKey allows leading dots but rejects path separators
// and explicit traversals — matches AI/IDE.
func validateServiceNameAsPackKey(s string) error {
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

// ResolveTemplatePack resolves a git template pack directory for a service.
// Returns (packDir, packName, err). Explicit svc.Git.Template is strict.
// Implicit chain: serviceName → default; ErrNotExist falls through.
func ResolveTemplatePack(svc config.ServiceConfig, projectRoot, serviceName string) (string, string, error) {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve project root: %w", err)
	}

	if err := validateTemplateKey(svc.Git.Template); err != nil {
		return "", "", fmt.Errorf("invalid git.template %q: %w", svc.Git.Template, err)
	}
	if err := validateServiceNameAsPackKey(serviceName); err != nil {
		return "", "", fmt.Errorf("service name cannot be used as implicit template pack key: %w", err)
	}

	if svc.Git.Template != "" {
		candidate := filepath.Join(absRoot, "devbox", "templates", "git", svc.Git.Template)
		fi, err := os.Lstat(candidate)
		if err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return "", "", fmt.Errorf("git template pack %q is a symlink; symlinked packs are not supported", svc.Git.Template)
			}
			if !fi.IsDir() {
				return "", "", fmt.Errorf("git template pack %q is not a directory", svc.Git.Template)
			}
			if err := pathsafe.CheckNoSymlinks(absRoot, candidate, "git template pack"); err != nil {
				return "", "", err
			}
			return candidate, svc.Git.Template, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("stat git template pack %q: %w", svc.Git.Template, err)
		}
		return "", "", fmt.Errorf("git template pack %q not found (required by explicit git.template setting)", svc.Git.Template)
	}

	candidates := []string{serviceName, "default"}
	for _, name := range candidates {
		candidate := filepath.Join(absRoot, "devbox", "templates", "git", name)
		fi, err := os.Lstat(candidate)
		if err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return "", "", fmt.Errorf("git template pack %q is a symlink; symlinked packs are not supported", name)
			}
			if !fi.IsDir() {
				return "", "", fmt.Errorf("git template pack %q is not a directory", name)
			}
			if err := pathsafe.CheckNoSymlinks(absRoot, candidate, "git template pack"); err != nil {
				return "", "", err
			}
			return candidate, name, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("stat git template pack %q: %w", name, err)
		}
	}

	return "", "", fmt.Errorf("git template pack not found (tried %s, default): %w", serviceName, os.ErrNotExist)
}

// SelectServices filters and resolves git-hooks-enabled services. Mirrors IDE:
// deepest-extends-wins on Dir collisions.
func SelectServices(services map[string]config.ServiceConfig) (selected []string, skipped []SkippedService) {
	var allSkipped []SkippedService

	enabled := make(map[string]config.ServiceConfig)
	for name, svc := range services {
		if !svc.Enabled {
			allSkipped = append(allSkipped, SkippedService{Name: name, Reason: "service-disabled"})
			continue
		}
		if !svc.GitRenderEnabled() {
			allSkipped = append(allSkipped, SkippedService{Name: name, Reason: "git-disabled"})
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

// PrepareHub validates svc.Dir for containment and absence of symlinks BEFORE
// any MkdirAll under it. Returns the absolute hub directory.
func PrepareHub(absRoot, svcName string, svc config.ServiceConfig) (string, error) {
	if strings.TrimSpace(svc.Dir) == "" || filepath.Clean(svc.Dir) == "." {
		return "", fmt.Errorf("service %q has no dir; cannot render git hooks", svcName)
	}
	absHub := filepath.Join(absRoot, svc.Dir)
	if _, err := pathsafe.ContainedRel(absRoot, absHub); err != nil {
		return "", fmt.Errorf("service %q dir escapes project root: %w", svcName, err)
	}
	if err := pathsafe.CheckNoSymlinks(absRoot, absHub, "service "+svcName); err != nil {
		return "", err
	}
	return absHub, nil
}

// ResolveGitHooksDir computes <absHub>/src/.git/hooks and reports the state of
// <absHub>/src/.git. When src/.git is a regular file (worktree/submodule
// pointer), DirWorktree is returned and the caller should skip. DirMissing
// signals src/.git absent. Any symlink component in the path is a hard error.
func ResolveGitHooksDir(absHub string) (string, DirStatus, error) {
	gitDir := filepath.Join(absHub, "src", ".git")
	hooksDir := filepath.Join(gitDir, "hooks")

	// Reject symlinks in absHub/src
	srcDir := filepath.Join(absHub, "src")
	if fi, err := os.Lstat(srcDir); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", 0, fmt.Errorf("git: src is a symlink at %s", srcDir)
		}
	}

	fi, err := os.Lstat(gitDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return hooksDir, DirMissing, nil
		}
		return "", 0, fmt.Errorf("stat %s: %w", gitDir, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", 0, fmt.Errorf("git: %s is a symlink; symlinked .git is not supported", gitDir)
	}
	if !fi.IsDir() {
		// Regular file → worktree/submodule pointer; deferred
		return hooksDir, DirWorktree, nil
	}

	// .git is a real directory. Check for symlink at hooks/ explicitly.
	if hfi, err := os.Lstat(hooksDir); err == nil {
		if hfi.Mode()&os.ModeSymlink != 0 {
			return "", 0, fmt.Errorf("git: %s is a symlink; symlinked hooks dir is not supported", hooksDir)
		}
	}
	return hooksDir, DirOK, nil
}

// LoadManifest reads manifest.yml from packDir using strict decode. Parse-only:
// no shape or source-existence validation.
func LoadManifest(packDir string) (*manifest.File, error) {
	return manifest.Load(filepath.Join(packDir, "manifest.yml"))
}

// ValidateManifest validates a git pack manifest. destRoot is the hooks dir
// (the actual write destination), not the hub dir, so `to` containment math
// matches the renderer.
func ValidateManifest(m *manifest.File, projectRoot, packName, destRoot string) error {
	label := "git pack " + packName
	if err := manifest.ValidateShape(m, destRoot, label); err != nil {
		return err
	}
	// Git-specific: every `to` must be a basename (no separators, no traversal).
	for i, e := range m.Render {
		if strings.ContainsAny(e.To, "/\\") {
			return fmt.Errorf("%s: render[%d]: to must be a basename (got %q)", label, i, e.To)
		}
		if e.To == "." || e.To == ".." {
			return fmt.Errorf("%s: render[%d]: to %q is invalid", label, i, e.To)
		}
	}
	// Git-specific: symlinks block must be empty.
	if len(m.Symlinks) > 0 {
		return fmt.Errorf("%s: symlinks are not supported for git packs", label)
	}
	resolve := func(rel string) (string, bool, error) {
		return packroot.Resolve(projectRoot, "git", packName, rel)
	}
	return manifest.ValidateSources(m, resolve, label)
}

// TemplateData is passed to git-hook templates.
type TemplateData struct {
	Project    config.ProjectConfig
	Service    string
	ServiceCfg config.ServiceConfig
	Runtime    config.RuntimeConfig
}

// Context carries the inputs for rendering all hooks for one service.
type Context struct {
	ProjectRoot string
	Cfg         *config.DevboxConfig
	Service     string
	ServiceCfg  config.ServiceConfig
	PackName    string
	Manifest    *manifest.File
	HooksDir    string // absolute path to <absHub>/src/.git/hooks
	HubDir      string // absolute path to <absRoot>/<svc.Dir>
	Writer      *render.Writer
}

// RenderHooks renders every entry in ctx.Manifest into ctx.HooksDir.
// Each destination is written atomically (write then chmod 0755). A destination
// that exists as a symlink is rejected without overwrite.
func RenderHooks(ctx Context) error {
	if ctx.Manifest == nil {
		return errors.New("git: nil manifest")
	}
	absRoot, err := filepath.Abs(ctx.ProjectRoot)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}

	// Resolve the actual .git directory parent for containment checks.
	gitDir := filepath.Join(ctx.HubDir, "src", ".git")

	if err := pathsafe.CheckNoSymlinks(absRoot, ctx.HooksDir, "git hooks dir"); err != nil {
		return err
	}
	if err := os.MkdirAll(ctx.HooksDir, 0o755); err != nil {
		return fmt.Errorf("create hooks dir: %w", err)
	}

	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}
	realGitDir, err := filepath.EvalSymlinks(gitDir)
	if err != nil {
		return fmt.Errorf("resolve .git dir: %w", err)
	}
	realHooksDir, err := filepath.EvalSymlinks(ctx.HooksDir)
	if err != nil {
		return fmt.Errorf("resolve hooks dir: %w", err)
	}
	if err := pathsafe.EnsureRealUnder(realHooksDir, realRoot, realGitDir); err != nil {
		return fmt.Errorf("hooks dir resolves outside required boundaries via symlink: %w", err)
	}

	data := TemplateData{
		Project:    ctx.Cfg.Project,
		Service:    ctx.Service,
		ServiceCfg: ctx.ServiceCfg,
		Runtime:    ctx.Cfg.Runtime,
	}

	for _, entry := range ctx.Manifest.Render {
		fromOverride, err := renderOneHook(ctx, entry, data)
		if err != nil {
			return err
		}
		if fromOverride && ctx.Writer != nil {
			ctx.Writer.Info(fmt.Sprintf("using local override: devbox/templates/git/%s.local/%s", ctx.PackName, entry.From))
		}
		if ctx.Writer != nil {
			ctx.Writer.Success(fmt.Sprintf("git → %s", filepath.Join(ctx.ServiceCfg.Dir, "src/.git/hooks", entry.To)))
		}
	}
	return nil
}

// renderOneHook is a separate function so any per-entry deferred cleanup runs
// at iteration boundary, not at RenderHooks return.
func renderOneHook(ctx Context, entry manifest.RenderEntry, data TemplateData) (bool, error) {
	sourcePath, fromOverride, err := packroot.Resolve(ctx.ProjectRoot, "git", ctx.PackName, entry.From)
	if err != nil {
		return false, fmt.Errorf("resolve hook template %s: %w", entry.From, err)
	}

	tplBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		return false, fmt.Errorf("read hook template %s: %w", sourcePath, err)
	}

	name := filepath.Base(sourcePath)
	t, err := template.New(name).Option("missingkey=error").Parse(string(tplBytes))
	if err != nil {
		return false, fmt.Errorf("parse hook template %s: %w", name, err)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return false, fmt.Errorf("render hook template %s: %w", name, err)
	}

	absDest := filepath.Join(ctx.HooksDir, entry.To)
	if _, err := pathsafe.ContainedRel(ctx.HooksDir, absDest); err != nil {
		return false, fmt.Errorf("hook destination %q escapes hooks dir: %w", entry.To, err)
	}

	if fi, err := os.Lstat(absDest); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("hook destination is a symlink: %s", absDest)
		}
	}

	if err := os.WriteFile(absDest, buf.Bytes(), 0o755); err != nil {
		return false, fmt.Errorf("write hook %s: %w", entry.To, err)
	}
	// Explicit chmod: WriteFile only sets mode on create; existing 0644 stays
	// otherwise. Apply unconditionally so re-renders normalize permissions.
	if err := os.Chmod(absDest, 0o755); err != nil {
		return false, fmt.Errorf("chmod hook %s: %w", entry.To, err)
	}
	return fromOverride, nil
}
