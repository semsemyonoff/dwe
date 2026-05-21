package stack

import (
	"maps"
	"slices"

	"devbox-cli/internal/config"
	"devbox-cli/internal/tpl"
)

// Kind selects between service and tool when computing custom status columns.
type Kind int

const (
	// KindService selects services as the source of status[] declarations.
	KindService Kind = iota + 1
	// KindTool selects tools as the source of status[] declarations.
	KindTool
)

// BuildCustomColumns returns the ordered list of custom status-column names
// declared across all services (KindService) or all tools (KindTool).
// Ordering is deterministic: items are iterated alphabetically by name and
// column names are appended in first-encounter order during that walk.
func BuildCustomColumns(cfg *config.DevboxConfig, kind Kind) []string {
	if cfg == nil {
		return nil
	}
	var names []string
	seen := make(map[string]struct{})
	switch kind {
	case KindService:
		for _, name := range slices.Sorted(maps.Keys(cfg.Services)) {
			for _, col := range cfg.Services[name].Status {
				if _, ok := seen[col.Name]; ok {
					continue
				}
				seen[col.Name] = struct{}{}
				names = append(names, col.Name)
			}
		}
	case KindTool:
		for _, name := range slices.Sorted(maps.Keys(cfg.Tools)) {
			for _, col := range cfg.Tools[name].Status {
				if _, ok := seen[col.Name]; ok {
					continue
				}
				seen[col.Name] = struct{}{}
				names = append(names, col.Name)
			}
		}
	}
	return names
}

// RenderCustomCells evaluates each declared status column's Value template
// against data via tpl.Render (hermetic — no env/FS/network access).
// Returns the per-column rendered values keyed by column name and the slice
// of evaluation errors. Failing cells are omitted from the returned map;
// callers map a missing key to "—" when populating the table.
func RenderCustomCells(defs []config.StatusColumn, data map[string]any) (map[string]string, []error) {
	if len(defs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(defs))
	var errs []error
	for _, col := range defs {
		v, err := tpl.Render(col.Value, data)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out[col.Name] = v
	}
	return out, errs
}
