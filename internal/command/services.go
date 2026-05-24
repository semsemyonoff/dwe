package command

import (
	"sort"

	"devbox-cli/internal/config"
)

// serviceRow holds a single row of the services topology table.
type serviceRow struct {
	Name      string
	Type      string
	Dir       string
	Container string
	Mandatory bool
	Enabled   bool
}

// buildServiceRows returns an ordered list of service rows from cfg.
func buildServiceRows(cfg *config.DevboxConfig) []serviceRow {
	rows := make([]serviceRow, 0, len(cfg.Services))
	for _, name := range sortedServiceNames(cfg.Services) {
		svc := cfg.Services[name]
		if !isServiceManageable(svc) {
			continue
		}
		rows = append(rows, serviceRow{
			Name:      name,
			Type:      string(svc.Type),
			Dir:       svc.Dir,
			Container: svc.Container,
			Mandatory: svc.Mandatory,
			Enabled:   svc.Enabled,
		})
	}
	return rows
}

func isServiceManageable(svc config.ServiceConfig) bool {
	return !svc.IsInfra() || !svc.Mandatory
}

// sortedServiceNames returns services ordered by type first, then by name.
func sortedServiceNames(services map[string]config.ServiceConfig) []string {
	names := sortedKeys(services)
	sort.SliceStable(names, func(i, j int) bool {
		left := services[names[i]]
		right := services[names[j]]
		if l, r := serviceTypeRank(left.Type), serviceTypeRank(right.Type); l != r {
			return l < r
		}
		return names[i] < names[j]
	})
	return names
}

func serviceTypeRank(t config.ServiceType) int {
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

// sortedKeys returns the map keys sorted alphabetically.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
