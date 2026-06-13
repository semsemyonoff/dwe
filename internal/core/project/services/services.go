// Package services contains view-helpers derived from the merged dwe
// config Services map. It exists so command-layer packages can share
// ordering, filtering, and completion logic without importing each other.
package services

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// Row is a single row of the services topology view used by status, info,
// snapshot, and toggle commands.
type Row struct {
	Name      string
	Type      string
	Icon      string
	Dir       string
	Container string
	Mandatory bool
	Enabled   bool
}

// BuildRows returns the ordered, manageable service rows from cfg.
// Required infra services are filtered out — they are not user-managed.
func BuildRows(cfg *config.DweConfig) []Row {
	rows := make([]Row, 0, len(cfg.Services))
	for _, name := range SortedNames(cfg.Services) {
		svc := cfg.Services[name]
		if !IsManageable(svc) {
			continue
		}
		rows = append(rows, Row{
			Name:      name,
			Type:      string(svc.Type),
			Icon:      svc.DisplayIcon(),
			Dir:       svc.Dir,
			Container: svc.Container,
			Mandatory: svc.Required,
			Enabled:   svc.Enabled,
		})
	}
	return rows
}

// IsManageable reports whether the service participates in user-facing
// enable/disable/status flows. Required infra services are hidden.
func IsManageable(svc config.ServiceConfig) bool {
	return !svc.IsInfra() || !svc.Required
}

// SortedNames returns service names ordered by type (app, tool, infra, …)
// and then alphabetically within each type group.
func SortedNames(services map[string]config.ServiceConfig) []string {
	names := slices.Sorted(maps.Keys(services))
	slices.SortStableFunc(names, func(a, b string) int {
		ra, rb := typeRank(services[a].Type), typeRank(services[b].Type)
		if ra != rb {
			return ra - rb
		}
		return 0
	})
	return names
}

// DetectByCwd returns the name of the service whose source directory (svc.Dir,
// already extends-resolved, relative to root) contains cwd. When several
// services match because their dirs nest, the deepest match wins. Returns ""
// when no service owns cwd.
//
// A service whose dir resolves to the project root (e.g. `dir: .`) or escapes
// it (`dir: ..`, an absolute path outside root) is skipped — it would otherwise
// claim every cwd in (or above) the project. This mirrors the prompt's
// standalone detectService, but reads the already-resolved typed config instead
// of re-parsing service.yml stubs.
func DetectByCwd(services map[string]config.ServiceConfig, root, cwd string) string {
	if root == "" || cwd == "" {
		return ""
	}
	// Canonicalize both via EvalSymlinks before comparing: the project root is
	// already symlink-resolved (project.Locate), but cwd comes from os.Getwd()
	// which on macOS often stays logical (/tmp, /var, symlinked HOME). Without
	// this, the prefix check silently misses and cwd-detection no-ops. Best
	// effort — fall back to the raw path when the target can't be resolved.
	if r, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = r
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	sep := string(filepath.Separator)
	cwdClean := filepath.Clean(cwd)
	rootClean := filepath.Clean(root)
	rootPrefix := rootClean + sep

	var bestName string
	var bestLen int
	for _, name := range SortedNames(services) {
		dir := services[name].Dir
		if dir == "" {
			continue
		}
		var resolved string
		if filepath.IsAbs(dir) {
			resolved = filepath.Clean(dir)
		} else {
			resolved = filepath.Clean(filepath.Join(rootClean, dir))
		}
		if resolved == rootClean || !strings.HasPrefix(resolved, rootPrefix) {
			continue
		}
		if cwdClean != resolved && !strings.HasPrefix(cwdClean, resolved+sep) {
			continue
		}
		if len(resolved) > bestLen {
			bestName = name
			bestLen = len(resolved)
		}
	}
	return bestName
}

func typeRank(t config.ServiceType) int {
	switch t {
	case config.ServiceTypeApp:
		return 0
	case config.ServiceTypeTool:
		return 1
	case config.ServiceTypeInfra:
		return 2
	default:
		return 3
	}
}
