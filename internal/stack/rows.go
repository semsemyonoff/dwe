package stack

import (
	"sort"

	"devbox-cli/internal/config"
)

// ToolRow holds a single row of the tools table used by status and tool commands.
type ToolRow struct {
	Name      string
	Enabled   bool
	Port      int
	Host      string
	Container string
}

// BuildToolRows returns the fixed tool rows with enabled state, port, host, and container name.
func BuildToolRows(cfg *config.DevboxConfig) []ToolRow {
	return []ToolRow{
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

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
