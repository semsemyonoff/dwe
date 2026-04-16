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

// toolRow holds a single row of the tools topology table.
type toolRow struct {
	Name      string
	Enabled   bool
	Port      int
	Host      string
	Container string
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
			Type:      svc.Type,
			Dir:       svc.Dir,
			Container: svc.Container,
			Mandatory: svc.Mandatory,
			Enabled:   svc.Enabled,
		})
	}
	return rows
}

// buildToolRows returns the fixed tool rows with enabled state, port, host, and container name.
func buildToolRows(cfg *config.DevboxConfig) []toolRow {
	return []toolRow{
		{
			Name:      "adminer",
			Enabled:   cfg.Tools.Adminer.Enabled,
			Port:      cfg.Runtime.Ports.Adminer,
			Host:      cfg.Runtime.Hosts.Adminer,
			Container: "adminer",
		},
		{
			Name:      "redis_insight",
			Enabled:   cfg.Tools.RedisInsight.Enabled,
			Port:      cfg.Runtime.Ports.RedisInsight,
			Host:      cfg.Runtime.Hosts.RedisInsight,
			Container: "redis-insight",
		},
		{
			Name:      "mailpit",
			Enabled:   cfg.Tools.Mailpit.Enabled,
			Port:      cfg.Runtime.Ports.Mailpit,
			Host:      cfg.Runtime.Hosts.Mailpit,
			Container: "mailpit",
		},
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
