package stack

import (
	"maps"
	"slices"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// BuildCustomColumns returns the ordered list of custom status-column names
// declared across all services of the given type. Ordering is deterministic:
// services are iterated alphabetically by name and column names are appended
// in first-encounter order during that walk.
func BuildCustomColumns(cfg *config.DweConfig, t config.ServiceType) []string {
	if cfg == nil {
		return nil
	}
	var names []string
	seen := make(map[string]struct{})
	for _, name := range slices.Sorted(maps.Keys(cfg.Services)) {
		svc := cfg.Services[name]
		if svc.Type != t {
			continue
		}
		for _, col := range svc.Status {
			if _, ok := seen[col.Name]; ok {
				continue
			}
			seen[col.Name] = struct{}{}
			names = append(names, col.Name)
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
