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

// BuildToolRows returns tool rows for all declared services of type "tool" in
// sorted order with enabled state, port, host, and container name.
// Single-port / single-host tools surface those values; multi-port tools
// surface the first sorted entry. Safe when cfg.Services is nil.
func BuildToolRows(cfg *config.DevboxConfig) []ToolRow {
	rows := make([]ToolRow, 0)
	for _, name := range slices.Sorted(maps.Keys(cfg.Services)) {
		svc := cfg.Services[name]
		if !svc.IsTool() {
			continue
		}
		rows = append(rows, ToolRow{
			Name:      name,
			Enabled:   svc.Enabled,
			Port:      firstPort(svc.Ports),
			Host:      firstHost(svc.Hosts),
			Container: svc.Container,
		})
	}
	return rows
}

func firstPort(m map[string]int) int {
	for _, k := range slices.Sorted(maps.Keys(m)) {
		return m[k]
	}
	return 0
}

func firstHost(m map[string]string) string {
	for _, k := range slices.Sorted(maps.Keys(m)) {
		return m[k]
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
