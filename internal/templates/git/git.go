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
	Reason string // "service-disabled" | "git-disabled" | "git-policy" | "empty-dir" | "lost-collision"
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

// ExtendsRoot walks the extends chain from name and returns the chain root
// (first ancestor with empty Extends). Returns name itself when the service
// has no extends or is unknown. The 32-hop cycle guard mirrors ExtendsDepth.
func ExtendsRoot(services map[string]config.ServiceConfig, name string) string {
	const maxDepth = 32
	current := name
	for range maxDepth {
		svc, ok := services[current]
		if !ok || svc.Extends == "" {
			return current
		}
		current = svc.Extends
	}
	return current
}

// ResolveTemplatePack resolves a git template pack directory for a service.
// Returns (packDir, packName, found, err). Explicit svc.Render.Git.Template is strict.
// Implicit chain: serviceName → default; returns found=false when exhausted.
// Semantics: err != nil means hard failure; err == nil && !found means implicit chain exhausted.
func ResolveTemplatePack(svc config.ServiceConfig, projectRoot, serviceName string) (string, string, bool, error) {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", "", false, fmt.Errorf("resolve project root: %w", err)
	}

	if svc.Render.Git.Template != "" {
		if err := manifest.ValidatePackName(svc.Render.Git.Template); err != nil {
			return "", "", false, fmt.Errorf("invalid render.git.template %q: %w", svc.Render.Git.Template, err)
		}
		candidate := filepath.Join(absRoot, "devbox", "templates", "git", svc.Render.Git.Template)
		fi, err := os.Lstat(candidate)
		if err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return "", "", false, fmt.Errorf("git template pack %q is a symlink; symlinked packs are not supported", svc.Render.Git.Template)
			}
			if !fi.IsDir() {
				return "", "", false, fmt.Errorf("git template pack %q is not a directory", svc.Render.Git.Template)
			}
			if err := pathsafe.CheckNoSymlinks(absRoot, candidate, "git template pack"); err != nil {
				return "", "", false, err
			}
			return candidate, svc.Render.Git.Template, true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", "", false, fmt.Errorf("stat git template pack %q: %w", svc.Render.Git.Template, err)
		}
		return "", "", false, fmt.Errorf("git template pack %q not found (required by explicit render.git.template setting)", svc.Render.Git.Template)
	}

	// Implicit chain: service-name → default. Skip the service-name candidate
	// silently if the name is not a valid pack name; default is always tried.
	var candidates []string
	if manifest.ValidatePackName(serviceName) == nil {
		candidates = append(candidates, serviceName)
	}
	candidates = append(candidates, "default")
	for _, name := range candidates {
		candidate := filepath.Join(absRoot, "devbox", "templates", "git", name)
		fi, err := os.Lstat(candidate)
		if err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return "", "", false, fmt.Errorf("git template pack %q is a symlink; symlinked packs are not supported", name)
			}
			if !fi.IsDir() {
				return "", "", false, fmt.Errorf("git template pack %q is not a directory", name)
			}
			if err := pathsafe.CheckNoSymlinks(absRoot, candidate, "git template pack"); err != nil {
				return "", "", false, err
			}
			return candidate, name, true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", "", false, fmt.Errorf("stat git template pack %q: %w", name, err)
		}
	}

	return "", "", false, nil
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
		if gitEnabled, explicit := svc.GitRenderEnabledExplicit(); !gitEnabled {
			reason := "git-policy"
			if explicit {
				reason = "git-disabled"
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
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", 0, fmt.Errorf("stat %s: %w", srcDir, err)
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
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", 0, fmt.Errorf("stat %s: %w", hooksDir, err)
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
func ValidateManifest(m *manifest.File, projectRoot, packName, destRoot string, sink ...func(rel string, fromOverride bool)) error {
	label := "git pack " + packName
	if err := manifest.ValidateShape(m, destRoot, label); err != nil {
		return err
	}
	// Git-specific: every `to` must be a basename (no separators, no traversal).
	for i, e := range m.Render {
		if strings.ContainsAny(e.To, "/\\") {
			return fmt.Errorf("%s: render[%d]: to must be a basename (got %q)", label, i, e.To)
		}
	}
	// Git-specific: symlinks block must be empty.
	if len(m.Symlinks) > 0 {
		return fmt.Errorf("%s: symlinks are not supported for git packs", label)
	}
	resolve := func(rel string) (string, bool, error) {
		return packroot.Resolve(projectRoot, "git", packName, rel)
	}
	var s func(string, bool)
	if len(sink) > 0 {
		s = sink[0]
	}
	return manifest.ValidateSourcesWith(m, resolve, s, label)
}

// TemplateData is passed to git-hook templates.
//
// Service is the canonical config identity (root of the extends chain) — use
// it for raw-config lookups keyed by service name (e.g. `(index .Cfg.Raw.git.hooks .Service)`).
// Resolved is the actual rendering service (the deepest-extends collision
// winner) and equals Service when the rendering service has no extends chain.
// ServiceCfg is the merged service block of the rendering service (Resolved),
// so fields like .ServiceCfg.Container reflect the extender's overlay.
type TemplateData struct {
	Project    config.ProjectConfig
	Service    string
	Resolved   string
	ServiceCfg config.ServiceConfig
	Runtime    config.RuntimeConfig
	Services   map[string]config.ServiceConfig
	Cfg        *config.DevboxConfig
}

// AppServices returns services whose Type is "app".
func (d TemplateData) AppServices() map[string]config.ServiceConfig {
	return filterServices(d.Services, config.ServiceTypeApp)
}

// ToolServices returns services whose Type is "tool".
func (d TemplateData) ToolServices() map[string]config.ServiceConfig {
	return filterServices(d.Services, config.ServiceTypeTool)
}

// InfraServices returns services whose Type is "infra".
func (d TemplateData) InfraServices() map[string]config.ServiceConfig {
	return filterServices(d.Services, config.ServiceTypeInfra)
}

func filterServices(svcs map[string]config.ServiceConfig, t config.ServiceType) map[string]config.ServiceConfig {
	out := make(map[string]config.ServiceConfig, len(svcs))
	for name, svc := range svcs {
		if svc.Type == t {
			out[name] = svc
		}
	}
	return out
}

// Context carries the inputs for rendering all hooks for one service.
// Service is the canonical config identity (root of the extends chain);
// Resolved is the rendering service (the collision-policy winner). They
// match when the rendering service has no extends chain.
type Context struct {
	ProjectRoot string
	Cfg         *config.DevboxConfig
	Service     string
	Resolved    string
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
	if ctx.Cfg == nil {
		return errors.New("git: nil cfg")
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

	resolved := ctx.Resolved
	if resolved == "" {
		resolved = ctx.Service
	}
	data := TemplateData{
		Project:    ctx.Cfg.Project,
		Service:    ctx.Service,
		Resolved:   resolved,
		ServiceCfg: ctx.ServiceCfg,
		Runtime:    ctx.Cfg.Runtime,
		Services:   ctx.Cfg.Services,
		Cfg:        ctx.Cfg,
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
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat hook destination %s: %w", absDest, err)
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
