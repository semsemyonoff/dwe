package stack

import (
	"maps"
	"slices"

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

// BuildToolRows returns tool rows for all declared tools in sorted order with enabled state, port, host, and container name.
// Safe when cfg.Tools is nil (nil map iterates zero times); cfg itself must be non-nil.
func BuildToolRows(cfg *config.DevboxConfig) []ToolRow {
	rows := make([]ToolRow, 0, len(cfg.Tools))
	for _, name := range slices.Sorted(maps.Keys(cfg.Tools)) {
		t := cfg.Tools[name]
		rows = append(rows, ToolRow{
			Name:      name,
			Enabled:   t.Enabled,
			Port:      t.Port,
			Host:      t.Host,
			Container: t.Container,
		})
	}
	return rows
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
