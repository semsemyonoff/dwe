// Package packcommon holds infrastructure shared by the ai/ide/git template
// packs: the extends-chain walkers, the render TemplateData context (and its
// service-type accessors), file preparation and writes, relative symlinks,
// and the in-memory dry-run renderer. The per-kind resolvers (collision policy,
// manifest validation) deliberately stay in their
// own packages — only the byte-identical scaffolding lives here.
package packcommon

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/semsemyonoff/dwe/internal/core/execution/templates/manifest"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/packroot"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/shared/pathsafe"
)

// maxDepth bounds every extends-chain walk (defense-in-depth cycle guard).
const maxDepth = 32

// PreparedFile holds source bytes (rendered when requested) and normalized
// permissions. Preparation never creates destination files or directories.
type PreparedFile struct {
	Data         []byte
	Mode         os.FileMode
	FromOverride bool
}

// PrepareFile resolves a pack source, then renders it or reads it verbatim.
// Any source executable bit selects 0755; all other sources select 0644.
func PrepareFile(kind, projectRoot, packName, rel string, data TemplateData, renderTemplate bool) (PreparedFile, error) {
	sourcePath, fromOverride, err := packroot.Resolve(projectRoot, kind, packName, rel)
	if err != nil {
		return PreparedFile{}, fmt.Errorf("resolve template %s: %w", rel, err)
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return PreparedFile{}, fmt.Errorf("read template %s: %w", sourcePath, err)
	}
	fi, err := os.Stat(sourcePath)
	if err != nil {
		return PreparedFile{}, fmt.Errorf("stat template %s: %w", sourcePath, err)
	}
	mode := os.FileMode(0o644)
	if fi.Mode().Perm()&0o111 != 0 {
		mode = 0o755
	}
	if renderTemplate {
		name := filepath.Base(sourcePath)
		t, err := template.New(name).Option("missingkey=error").Parse(string(source))
		if err != nil {
			return PreparedFile{}, fmt.Errorf("parse template %s: %w", name, err)
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, data); err != nil {
			return PreparedFile{}, fmt.Errorf("render template %s: %w", name, err)
		}
		source = buf.Bytes()
	}
	return PreparedFile{Data: source, Mode: mode, FromOverride: fromOverride}, nil
}

// DestLabels preserves each render kind's destination error vocabulary.
type DestLabels struct {
	Path            string
	Boundary        string
	ResolveBoundary string
}

// CheckDest checks containment, parent directories and the destination without
// writing anything. Missing directories are resolved from their nearest existing
// ancestor. Existing destinations must be regular files.
func CheckDest(dest, absDestRoot, absRoot string, labels DestLabels) (string, error) {
	absDest, err := filepath.Abs(filepath.Join(absDestRoot, dest))
	if err != nil {
		return "", fmt.Errorf("resolve destination: %w", err)
	}
	if filepath.IsAbs(dest) {
		return "", fmt.Errorf("%s %q escapes %s: path is absolute", labels.Path, dest, labels.Boundary)
	}
	if _, err := pathsafe.ContainedRel(absDestRoot, absDest); err != nil {
		return "", fmt.Errorf("%s %q escapes %s: %w", labels.Path, dest, labels.Boundary, err)
	}
	destDir := filepath.Dir(absDest)
	if err := pathsafe.CheckNoSymlinks(absRoot, destDir, "destination dir"); err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	realDestRoot, err := resolveMissingDir(absDestRoot)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", labels.ResolveBoundary, err)
	}
	realDir, err := resolveMissingDir(destDir)
	if err != nil {
		return "", fmt.Errorf("resolve dir for %s: %w", dest, err)
	}
	if err := pathsafe.EnsureRealUnder(realDir, realRoot, realDestRoot); err != nil {
		return "", fmt.Errorf("destination dir for %q resolves outside required boundaries via symlink: %w", dest, err)
	}
	fi, err := os.Lstat(absDest)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat %s: %w", dest, err)
		}
		return absDest, nil
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("destination %q is a symlink; will not overwrite", dest)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("destination %q is a directory; will not overwrite", dest)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("destination %q is not a regular file; will not overwrite", dest)
	}
	return absDest, nil
}

// resolveMissingDir projects a missing directory beneath its real existing
// ancestor, so planning can check boundaries without creating directories.
func resolveMissingDir(dir string) (string, error) {
	ancestor := dir
	for {
		fi, err := os.Lstat(ancestor)
		if err == nil {
			if !fi.IsDir() && fi.Mode()&os.ModeSymlink == 0 {
				return "", fmt.Errorf("%s is not a directory", ancestor)
			}
			realAncestor, err := filepath.EvalSymlinks(ancestor)
			if err != nil {
				return "", err
			}
			rel, err := filepath.Rel(ancestor, dir)
			if err != nil {
				return "", err
			}
			return filepath.Join(realAncestor, rel), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", err
		}
		ancestor = parent
	}
}

// WriteFile rechecks the destination and writes prepared bytes. Source mode
// uses an explicit chmod to converge existing files; otherwise writes use 0644
// on creation and preserve existing permissions, as ai/ide historically do.
func WriteFile(file PreparedFile, dest, absDestRoot, absRoot string, labels DestLabels, modeFromSource bool) error {
	absDest, err := CheckDest(dest, absDestRoot, absRoot, labels)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absDest), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", dest, err)
	}
	if _, err := CheckDest(dest, absDestRoot, absRoot, labels); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if modeFromSource {
		mode = file.Mode
	}
	if err := os.WriteFile(absDest, file.Data, mode); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	if modeFromSource {
		if err := os.Chmod(absDest, mode); err != nil {
			return fmt.Errorf("chmod %s: %w", dest, err)
		}
	}
	return nil
}

// ImplicitPackCandidates returns the implicit-chain pack name candidates for a
// service: the service name, then each ancestor walked via Extends (in order),
// then "default". Duplicates and names that fail manifest.ValidatePackName are
// skipped silently. The 32-hop cycle guard mirrors ExtendsDepth.
func ImplicitPackCandidates(services map[string]config.ServiceConfig, serviceName string) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		if manifest.ValidatePackName(name) != nil {
			return
		}
		out = append(out, name)
		seen[name] = true
	}

	add(serviceName)
	current := serviceName
	for range maxDepth {
		svc, ok := services[current]
		if !ok || svc.Extends == "" {
			break
		}
		current = svc.Extends
		add(current)
	}
	add("default")
	return out
}

// ExtendsDepth computes the depth of a service's extends chain.
// Returns (depth, capped): depth is the number of hops to the root;
// capped is true if depth hit the 32-hop limit (defense-in-depth cycle guard).
func ExtendsDepth(services map[string]config.ServiceConfig, name string) (int, bool) {
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

// TemplateData holds the context for rendering ai/ide/git templates.
//
// Service is the canonical config identity (root of the extends chain) — use
// it for raw-config lookups keyed by service name. Resolved is the actual
// rendering service (the collision-policy winner) and equals Service when the
// rendering service has no extends chain. ServiceCfg is the merged service
// block of the rendering service (Resolved).
//
// Commands and CommandGroups are the project-wide declared command index
// (usercommands.CommandIndex, built without ApplyVisibility so a rendered file
// does not flip with stack state); empty when the registry failed to load.
type TemplateData struct {
	Project       config.ProjectConfig
	Service       string
	Resolved      string
	ServiceCfg    config.ServiceConfig
	Runtime       config.RuntimeConfig
	Services      map[string]config.ServiceConfig
	Cfg           *config.DweConfig
	Commands      []model.CommandSummary
	CommandGroups []model.CommandGroupSummary
}

// ServiceCommands returns the declared commands that target this service.
//
// The join is on ServiceCfg.Container, never Resolved: a command's `service:`
// is the compose service name, and the services map key may differ (magento:
// key `magento`, `container: app-magento`) — the same reason
// config.ServiceByContainer exists. A templated `service:` (`app-${param.x}`)
// cannot be resolved at render time and matches nothing.
func (d TemplateData) ServiceCommands() []model.CommandSummary {
	container := d.ServiceCfg.Container
	if container == "" {
		return nil
	}
	var out []model.CommandSummary
	for _, c := range d.Commands {
		if c.Service == container {
			out = append(out, c)
		}
	}
	return out
}

// ServiceCommandGroups returns the groups owning at least one of
// ServiceCommands, collapsed to the shallowest: a qualifying group nested under
// another qualifying group is dropped, since listing the ancestor covers it.
// CommandGroups holds authored groups only, so the collapse never lands on a
// synthetic ancestor such as magento's `services`.
//
// A group absorbs its descendants only while it does not span another hub:
// no command under it targets the container of a service rendered into a
// different hub directory. An authored `services` parent holding node's
// commands — in a `services.node` subgroup or in its own file — would
// otherwise replace `services.magento` in every hub with one listing of all of
// them. Such a parent is kept only while it owns a hub command no qualifying
// descendant covers. Commands for containers without a hub of their own are
// helpers, not another hub: magento's `services.magento.config.set` targets
// `db`, and counting it disabled the collapse and listed all 16 subgroups.
// Order follows CommandGroups.
func (d TemplateData) ServiceCommandGroups() []model.CommandGroupSummary {
	cmds := d.ServiceCommands()
	if len(cmds) == 0 {
		return nil
	}
	var qualifying []model.CommandGroupSummary
	for _, g := range d.CommandGroups {
		for _, c := range cmds {
			if underPrefix(c.ID, g.ID) {
				qualifying = append(qualifying, g)
				break
			}
		}
	}
	otherHubs := d.otherHubContainers()
	scoped := func(g model.CommandGroupSummary) bool {
		for _, c := range d.Commands {
			if otherHubs[c.Service] && underPrefix(c.ID, g.ID) {
				return false
			}
		}
		return true
	}
	var out []model.CommandGroupSummary
	for _, g := range qualifying {
		absorbed := false
		for _, a := range qualifying {
			if a.ID != g.ID && underPrefix(g.ID, a.ID) && scoped(a) {
				absorbed = true
				break
			}
		}
		if absorbed {
			continue
		}
		if scoped(g) || ownsUncovered(g, cmds, qualifying) {
			out = append(out, g)
		}
	}
	return out
}

// otherHubContainers returns the containers of services rendered into a hub
// directory other than this service's. Services sharing this hub (an extends
// sibling such as magento-debug inherits `dir:`) are not another hub.
func (d TemplateData) otherHubContainers() map[string]bool {
	own := filepath.Clean(d.ServiceCfg.Dir)
	out := make(map[string]bool)
	for _, svc := range d.Services {
		if svc.Dir == "" || svc.Container == "" || svc.Container == d.ServiceCfg.Container {
			continue
		}
		if d.ServiceCfg.Dir != "" && filepath.Clean(svc.Dir) == own {
			continue
		}
		out[svc.Container] = true
	}
	return out
}

// ownsUncovered reports whether g holds a command from cmds that no other
// qualifying group nested under g covers.
func ownsUncovered(g model.CommandGroupSummary, cmds []model.CommandSummary, qualifying []model.CommandGroupSummary) bool {
	for _, c := range cmds {
		if !underPrefix(c.ID, g.ID) {
			continue
		}
		covered := false
		for _, q := range qualifying {
			if q.ID != g.ID && underPrefix(q.ID, g.ID) && underPrefix(c.ID, q.ID) {
				covered = true
				break
			}
		}
		if !covered {
			return true
		}
	}
	return false
}

// underPrefix reports whether id equals prefix or sits below it on a dot
// boundary — the rule registry.list applies, so `admin` never owns
// `administration`.
func underPrefix(id, prefix string) bool {
	return id == prefix || strings.HasPrefix(id, prefix+".")
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

// DryRunRender resolves, parses, and executes every render entry in m against
// data without writing to disk, resolving sources under the given pack kind
// ("ai" | "ide" | "git"). Returns a map from manifest `from` path to the first
// error encountered for that entry (parse, source-read, or execution errors —
// typically missingkey=error). On success returns nil.
func DryRunRender(kind, projectRoot, packName string, m *manifest.File, data TemplateData) map[string]error {
	if m == nil || data.Cfg == nil {
		return nil
	}
	var failures map[string]error
	for _, entry := range m.Render {
		if err := executeTemplateInMemory(kind, projectRoot, packName, entry.From, data); err != nil {
			if failures == nil {
				failures = make(map[string]error)
			}
			failures[entry.From] = err
		}
	}
	return failures
}

func executeTemplateInMemory(kind, projectRoot, packName, rel string, data TemplateData) error {
	sourcePath, _, err := packroot.Resolve(projectRoot, kind, packName, rel)
	if err != nil {
		return fmt.Errorf("resolve template %s: %w", rel, err)
	}
	tplBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read template %s: %w", sourcePath, err)
	}
	name := filepath.Base(sourcePath)
	t, err := template.New(name).Option("missingkey=error").Parse(string(tplBytes))
	if err != nil {
		return fmt.Errorf("parse template %s: %w", name, err)
	}
	if err := t.Execute(&bytes.Buffer{}, data); err != nil {
		return fmt.Errorf("render template %s: %w", name, err)
	}
	return nil
}

// EnsureRelativeSymlink creates or updates a relative symlink inside absHubDir.
// Existing non-symlink files are refused; hint supplies optional recovery advice.
func EnsureRelativeSymlink(linkPath, targetWithinHub, absHubDir, absRoot, label, hint string) error {
	absLink := filepath.Join(absHubDir, linkPath)
	absTarget := filepath.Join(absHubDir, targetWithinHub)

	if _, err := pathsafe.ContainedRel(absHubDir, absLink); err != nil {
		return fmt.Errorf("symlink link %q escapes %s directory: %w", linkPath, label, err)
	}
	if _, err := pathsafe.ContainedRel(absHubDir, absTarget); err != nil {
		return fmt.Errorf("symlink target %q escapes %s directory: %w", targetWithinHub, label, err)
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
		return fmt.Errorf("resolve %s dir: %w", label, err)
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
		message := fmt.Sprintf("refuse to overwrite non-symlink file at %s", linkPath)
		if hint != "" {
			message += "; " + hint
		}
		return errors.New(message)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", linkPath, err)
	}

	if err := os.Symlink(relTarget, absLink); err != nil {
		return fmt.Errorf("create symlink %s: %w", linkPath, err)
	}
	return nil
}
