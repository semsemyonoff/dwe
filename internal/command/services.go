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
	// Stable order: iterate over known service names in insertion order isn't
	// guaranteed with maps; sort by name for deterministic output.
	names := sortedKeys(cfg.Services)
	for _, name := range names {
		svc := cfg.Services[name]
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

// sortedKeys returns the map keys sorted alphabetically.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
