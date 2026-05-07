package command

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
	"devbox-cli/internal/render"

	"github.com/spf13/cobra"
)

// skippedService carries information about a service that was skipped during IDE rendering.
type skippedService struct {
	Name   string // service name
	Reason string // "service-disabled" | "ide-disabled" | "ide-policy" | "empty-dir" | "lost-collision"
	Dir    string // set for "lost-collision" only
	Winner string // set for "lost-collision" only (name of the winning service)
}

// extendsDepth computes the depth of a service's extends chain.
// Returns (depth, capped): depth is the number of hops to the root;
// capped is true if depth hit the 32-hop limit (defense-in-depth cycle guard).
func extendsDepth(services map[string]config.ServiceConfig, name string) (int, bool) {
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

// selectIDEServices filters and resolves IDE-enabled services.
// It returns a list of selected service names (sorted lexicographically) and
// a list of services that were skipped with reason-specific context.
//
// Selection logic (in order):
//  1. Gate on both flags: services where svc.Enabled==false or svc.IDERenderEnabled()==false are dropped.
//  2. Normalize Dir: services with empty (after TrimSpace) Dir are dropped.
//  3. Group by filepath.Clean(Dir) and resolve collisions: when multiple services
//     share the same Dir, the deepest extends chain wins; ties are broken lexicographically.
func selectIDEServices(services map[string]config.ServiceConfig) (selected []string, skipped []skippedService) {
	var allSkipped []skippedService

	// Step A: gate on both Enabled and IDERenderEnabled.
	enabled := make(map[string]config.ServiceConfig)
	for name, svc := range services {
		if !svc.Enabled {
			allSkipped = append(allSkipped, skippedService{Name: name, Reason: "service-disabled"})
			continue
		}
		if ideEnabled, explicit := svc.IDERenderEnabledExplicit(); !ideEnabled {
			reason := "ide-policy"
			if explicit {
				reason = "ide-disabled"
			}
			allSkipped = append(allSkipped, skippedService{Name: name, Reason: reason})
			continue
		}
		enabled[name] = svc
	}

	// Step B: drop services with empty Dir.
	dirNormalized := make(map[string]config.ServiceConfig)
	for name, svc := range enabled {
		if strings.TrimSpace(svc.Dir) == "" {
			allSkipped = append(allSkipped, skippedService{Name: name, Reason: "empty-dir"})
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

	// For each group, pick the winner (deepest extends chain; tie-break by name).
	selectedSet := make(map[string]bool)
	for dir, names := range dirGroups {
		if len(names) == 1 {
			selectedSet[names[0]] = true
			continue
		}

		// Multiple services share this dir: find the deepest extends chain.
		sort.Strings(names) // tie-break: lexicographically first among deepest
		var deepest string
		maxDepth := -1
		for _, name := range names {
			depth, _ := extendsDepth(services, name)
			if depth > maxDepth {
				maxDepth = depth
				deepest = name
			}
		}

		selectedSet[deepest] = true
		for _, name := range names {
			if name != deepest {
				allSkipped = append(allSkipped, skippedService{
					Name:   name,
					Reason: "lost-collision",
					Dir:    dir,
					Winner: deepest,
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

// packEntry describes a template file in the IDE pack after walking.
type packEntry struct {
	SourcePath string // absolute path to the .tpl file
	RelPath    string // path inside the pack with .tpl stripped
}

// ideTemplateData is passed to IDE config templates.
type ideTemplateData struct {
	Project    config.ProjectConfig
	Service    string
	ServiceCfg config.ServiceConfig
	Runtime    config.RuntimeConfig
}

// newRenderIDECmd creates the `devbox render ide [service]` command.
// It generates IDE-specific config files into each service directory.
// When a service name is provided only that service is processed;
// otherwise all services matching the IDE selection policy are processed.
func newRenderIDECmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ide [service]",
		Short: "Generate IDE configs from template packs",
		Long: `Generate IDE-specific config files for each enabled service from a template pack.

The command walks the chosen template pack (devbox/templates/ide/<pack-name>/)
and renders each *.tpl file into the corresponding location within the service
directory. For example:
  devbox/templates/ide/default/.vscode/settings.json.tpl
  → services/main/.vscode/settings.json

Template pack resolution (implicit fallback):
  1. If ide.template is set in the service config, use that pack (explicit, strict)
  2. Otherwise, try devbox/templates/ide/<service-name>/
  3. If not found, use devbox/templates/ide/default/
  4. If none exist, return an error

Services that participate in IDE rendering:
  - Type 'app' (default) has ide.enabled: true by default
  - Other types require explicit ide.enabled: true in the config`,
		Args:              cobra.MaximumNArgs(1),
		SilenceUsage:      true,
		ValidArgsFunction: serviceNameCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			projectRoot := flags.ProjectRoot()
			w := render.Stdout()

			// Determine which services to process.
			var serviceNames []string
			if len(args) == 1 {
				// Explicit service argument: validate thoroughly
				name := args[0]
				if err := validateExplicitIDEArg(name, cfg.Services); err != nil {
					return err
				}

				serviceNames = []string{name}
			} else {
				// No explicit service: use selection policy
				selected, skipped := selectIDEServices(cfg.Services)
				serviceNames = selected

				// Emit warnings only for actionable skips; policy-based skips
				// (service-disabled, ide-disabled, ide-policy) are expected and not reported.
				for _, skip := range skipped {
					switch skip.Reason {
					case "empty-dir":
						w.Warning(fmt.Sprintf("ide [%s] — skipped (service has no dir)", skip.Name))
					case "lost-collision":
						w.Warning(fmt.Sprintf("ide [%s] — skipped (dir %s rendered by %s)", skip.Name, skip.Dir, skip.Winner))
					}
				}

				if len(serviceNames) == 0 {
					w.Info("no services match the IDE rendering policy")
					return nil
				}
			}

			for _, name := range serviceNames {
				svc := cfg.Services[name]
				if err := renderIDEConfigs(projectRoot, name, svc, cfg, w); err != nil {
					return fmt.Errorf("service %s: %w", name, err)
				}
			}
			return nil
		},
	}
}

// validateExplicitIDEArg validates the explicit service argument for `devbox render ide <service>`.
// Checks in priority order: not-found → disabled → no-dir → IDE policy.
// Returns nil when the service is valid and renderable.
func validateExplicitIDEArg(name string, services map[string]config.ServiceConfig) error {
	svc, ok := services[name]
	if !ok {
		return fmt.Errorf("service %q not found in config", name)
	}
	if !svc.Enabled {
		return fmt.Errorf("service %q is disabled at the project level", name)
	}
	if strings.TrimSpace(svc.Dir) == "" {
		return fmt.Errorf("service %q has no dir; cannot render IDE files", name)
	}
	enabled, explicit := svc.IDERenderEnabledExplicit()
	if !enabled {
		if explicit {
			return fmt.Errorf("service %q has ide.enabled: false", name)
		}
		return fmt.Errorf("service %q (type: %s) does not participate in IDE rendering by default; set ide.enabled: true to opt in", name, svc.Type)
	}
	return nil
}

// validateIDETemplateKey validates that s is a single directory key without path traversal.
// It rejects path separators, absolute paths, and leading dots (which subsumes "..").
func validateIDETemplateKey(s string) error {
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

// resolveIDETemplatePack resolves a template pack directory for a service.
// Returns the absolute path to a directory under devbox/templates/ide/.
// Explicit is strict: if svc.IDE.Template is set and does not exist, returns an error.
// Implicit chain: service-name → default, with fallthrough only on ErrNotExist.
func resolveIDETemplatePack(svc config.ServiceConfig, projectRoot, serviceName string) (string, error) {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}

	// Validate template key and service name
	if err := validateIDETemplateKey(svc.IDE.Template); err != nil {
		return "", fmt.Errorf("invalid ide.template %q: %w", svc.IDE.Template, err)
	}
	if err := validateIDETemplateKey(serviceName); err != nil {
		return "", fmt.Errorf("invalid service name %q: %w", serviceName, err)
	}

	// Explicit candidate (strict — no fallthrough unless not exists)
	if svc.IDE.Template != "" {
		candidate := filepath.Join(absRoot, "devbox", "templates", "ide", svc.IDE.Template)
		fi, err := os.Lstat(candidate)
		if err == nil {
			// Pack exists; validate it
			if fi.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("ide template pack %q is a symlink; symlinked packs are not supported", svc.IDE.Template)
			}
			if !fi.IsDir() {
				return "", fmt.Errorf("ide template pack %q is not a directory", svc.IDE.Template)
			}
			return candidate, nil
		}
		// Any error other than not-exists is a hard error
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat ide template pack %q: %w", svc.IDE.Template, err)
		}
		// ErrNotExist with explicit template: strict error, no fallthrough
		return "", fmt.Errorf("ide template pack %q not found (required by explicit ide.template setting)", svc.IDE.Template)
	}

	// Implicit chain: service-name → default
	candidates := []string{serviceName, "default"}
	for _, name := range candidates {
		candidate := filepath.Join(absRoot, "devbox", "templates", "ide", name)
		fi, err := os.Lstat(candidate)
		if err == nil {
			// Candidate exists; validate it
			if fi.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("ide template pack %q is a symlink; symlinked packs are not supported", name)
			}
			if !fi.IsDir() {
				return "", fmt.Errorf("ide template pack %q is not a directory", name)
			}
			return candidate, nil
		}
		// Only ErrNotExist advances to next candidate; any other error is hard
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat ide template pack %q: %w", name, err)
		}
		// ErrNotExist: continue to next candidate
	}

	// No pack found in implicit chain
	return "", fmt.Errorf("ide template pack not found (tried %s, default)", serviceName)
}

// walkIDEPack walks the template pack directory and returns all .tpl entries.
// Entries are returned with absolute SourcePath and RelPath (with .tpl stripped).
// Returns entries sorted lexicographically by RelPath.
// Rejection rules (any rejection is a hard error, not a silent skip):
// 1. Any symlink anywhere in the tree (file or directory).
// 2. Any cleaned relative path that is absolute or contains ".." segments.
// 3. Source filename is bare ".tpl" (before or after cleaning).
func walkIDEPack(packDir string) ([]packEntry, error) {
	absPackDir, err := filepath.Abs(packDir)
	if err != nil {
		return nil, fmt.Errorf("resolve pack dir: %w", err)
	}

	var entries []packEntry
	err = filepath.WalkDir(absPackDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Reject any symlink (file or directory) before processing further.
		// d.Type() is populated by WalkDir's own lstat — no extra syscall needed.
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("ide template pack contains symlink: %s", path)
		}

		// Only process files; skip directories
		if d.IsDir() {
			return nil
		}

		// Compute relative path from pack root (before stripping .tpl)
		srcRelPath, err := filepath.Rel(absPackDir, path)
		if err != nil {
			return fmt.Errorf("relative path: %w", err)
		}

		// Suffix filter: only .tpl files
		if !strings.HasSuffix(srcRelPath, ".tpl") {
			return nil // skip non-.tpl files silently
		}

		// Strip .tpl suffix
		relPath := strings.TrimSuffix(srcRelPath, ".tpl")

		// Reject bare ".tpl" files
		// Check on the filename (before cleaning) so nested "dir/.tpl" is caught
		if strings.TrimSuffix(filepath.Base(srcRelPath), ".tpl") == "" {
			return fmt.Errorf("ide template pack contains bare .tpl file: %s", srcRelPath)
		}

		// Clean the path and reject empty or "." results
		cleanRelPath := filepath.Clean(relPath)
		if cleanRelPath == "" || cleanRelPath == "." {
			return fmt.Errorf("ide template pack entry cleans to empty path: %s", relPath)
		}

		// Reject absolute or escaping paths
		if filepath.IsAbs(cleanRelPath) {
			return fmt.Errorf("ide template pack entry is absolute: %s", cleanRelPath)
		}
		if strings.HasPrefix(cleanRelPath, ".."+string(filepath.Separator)) || cleanRelPath == ".." {
			return fmt.Errorf("ide template pack entry escapes root: %s", cleanRelPath)
		}

		entries = append(entries, packEntry{
			SourcePath: path,
			RelPath:    cleanRelPath,
		})
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Sort entries lexicographically by RelPath for determinism
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].RelPath < entries[j].RelPath
	})

	// Reject duplicate RelPaths (defensive — guards against walker bugs emitting duplicates).
	// Raw-string equality: case-fold collisions on macOS/Windows are out of scope.
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if _, dup := seen[e.RelPath]; dup {
			return nil, fmt.Errorf("ide template pack contains duplicate entry %q", e.RelPath)
		}
		seen[e.RelPath] = struct{}{}
	}

	return entries, nil
}

// checkNoSymlinks verifies that no existing path component between absRoot and absDir
// is a symlink. It stops at the first non-existent component (which cannot be a symlink).
// The label parameter appears in the error message to identify what is being checked.
func checkNoSymlinks(absRoot, absDir, label string) error {
	rel, err := filepath.Rel(absRoot, absDir)
	if err != nil {
		return fmt.Errorf("relative path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q is not under root %q", absDir, absRoot)
	}
	current := absRoot
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		fi, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("stat %s: %w", current, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s contains symlink at %q; symlinked paths are not supported", label, current)
		}
	}
	return nil
}

// renderIDEConfigs generates IDE config files for a single service by walking
// the resolved template pack and rendering all .tpl entries.
func renderIDEConfigs(projectRoot, name string, svc config.ServiceConfig, cfg *config.DevboxConfig, w *render.Writer) error {
	data := ideTemplateData{
		Project:    cfg.Project,
		Service:    name,
		ServiceCfg: svc,
		Runtime:    cfg.Runtime,
	}

	serviceDir := filepath.Join(projectRoot, svc.Dir)
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}
	absDir, err := filepath.Abs(serviceDir)
	if err != nil {
		return fmt.Errorf("resolve service dir: %w", err)
	}
	if !strings.HasPrefix(absDir+string(filepath.Separator), absRoot+string(filepath.Separator)) {
		return fmt.Errorf("service dir %q escapes project root", svc.Dir)
	}
	if err := checkNoSymlinks(absRoot, absDir, "service dir"); err != nil {
		return err
	}

	// Resolve the template pack
	pack, err := resolveIDETemplatePack(svc, projectRoot, name)
	if err != nil {
		return err
	}

	// Walk the pack and render each entry
	entries, err := walkIDEPack(pack)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		dest := filepath.Join(absDir, entry.RelPath)
		if err := renderIDETemplateFile(entry.SourcePath, data, dest, absDir, absRoot); err != nil {
			return err
		}
		w.Success(fmt.Sprintf("ide → %s", filepath.Join(svc.Dir, entry.RelPath)))
	}

	return nil
}

// renderIDETemplateFile reads a template file, renders it, and writes to dest.
// sourcePath is the absolute path to the .tpl file.
// data is the template context.
// dest is the destination path (may be relative to the service dir).
// absDir is the resolved absolute service directory.
// absRoot is the resolved absolute project root.
//
// The function enforces that dest (after resolution) is contained within absDir.
// It also enforces that absDir is contained within absRoot.
func renderIDETemplateFile(sourcePath string, data ideTemplateData, dest, absDir, absRoot string) error {
	// Read template file
	tplBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read template %s: %w", sourcePath, err)
	}

	// Parse template using basename as name for error messages
	name := filepath.Base(sourcePath)
	t, err := template.New(name).Parse(string(tplBytes))
	if err != nil {
		return fmt.Errorf("parse template %s: %w", name, err)
	}

	// Render template
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return fmt.Errorf("render template %s: %w", name, err)
	}

	destDir := filepath.Dir(dest)

	// Service-dir containment check: ensure dest is inside absDir
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return fmt.Errorf("resolve destination: %w", err)
	}
	rel, err := filepath.Rel(absDir, absDest)
	if err != nil {
		return fmt.Errorf("dest %q outside service dir: %w", dest, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("dest %q escapes service dir %q", dest, absDir)
	}

	// Guard against symlinks in the destination path before creating any directories.
	// checkNoSymlinks walks existing components only, so it catches a pre-existing
	// .devcontainer -> /tmp/outside symlink before MkdirAll follows it.
	if err := checkNoSymlinks(absRoot, destDir, "destination dir"); err != nil {
		return err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", dest, err)
	}

	// Verify the real directory resolves inside the project root after creation.
	// MkdirAll follows symlinks, so a .devcontainer -> /tmp/outside symlink
	// would succeed silently without this check.
	// Both paths are resolved via EvalSymlinks so the comparison works on
	// systems (macOS) where the temp dir itself is under a symlinked prefix.
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}
	realDir, err := filepath.EvalSymlinks(destDir)
	if err != nil {
		return fmt.Errorf("resolve dir for %s: %w", dest, err)
	}
	if !strings.HasPrefix(realDir+string(filepath.Separator), realRoot+string(filepath.Separator)) {
		return fmt.Errorf("destination dir for %q resolves outside project root via symlink", dest)
	}

	// Refuse to write through a symlinked destination file.
	if fi, err := os.Lstat(absDest); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("destination %q is a symlink; will not overwrite", dest)
	}

	if err := os.WriteFile(absDest, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	return nil
}
