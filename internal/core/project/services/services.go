// Package services contains view-helpers derived from the merged devbox
// config Services map. It exists so command-layer packages can share
// ordering, filtering, and completion logic without importing each other.
package services

import (
	"maps"
	"slices"

	"devbox-cli/internal/core/project/config"
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
func BuildRows(cfg *config.DevboxConfig) []Row {
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
