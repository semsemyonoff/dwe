package stack

import "github.com/semsemyonoff/dwe/internal/core/ui/render"

// Health represents the overall running health of the stack.
type Health int

// Health constants indicate how many active services are running.
const (
	HealthStopped Health = iota // no enabled/mandatory services are running
	HealthPartial               // some enabled/mandatory services are running
	HealthRunning               // all enabled/mandatory services are running
)

// AggregateHealth computes the overall stack health from service rows.
// Only mandatory or enabled services count toward the total.
func AggregateHealth(rows []render.ServiceTableRow) Health {
	active := 0
	running := 0
	for _, r := range rows {
		if r.Mandatory || r.Enabled {
			active++
			if r.Running {
				running++
			}
		}
	}
	if active == 0 || running == 0 {
		return HealthStopped
	}
	if running < active {
		return HealthPartial
	}
	return HealthRunning
}

// HasRuntimeStatuses reports whether topoStatus contains at least one
// non-disabled node, indicating that runtime status collection succeeded.
// A map with only NodeDisabled entries (from AugmentWithDisabled) is not
// sufficient to compute accurate health.
func HasRuntimeStatuses(topoStatus map[string]render.NodeStatus) bool {
	for _, st := range topoStatus {
		if st != render.NodeDisabled {
			return true
		}
	}
	return false
}

// HealthFromStatusInput aggregates the overall stack Health from a StatusInput.
// Topology runtime statuses (when present, i.e. at least one non-disabled entry)
// take precedence over service-row aggregation. A nil Cfg degrades to
// HealthStopped via the empty-rows path in AggregateHealth.
//
// This is the single source of truth for status aggregation, used by both the
// rendered health indicator and the opportunistic prompt-cache write performed
// at the top of the `dwe status` RunE.
func HealthFromStatusInput(in StatusInput) Health {
	if HasRuntimeStatuses(in.TopoStatus) {
		return AggregateHealthFromTopo(in.TopoStatus)
	}
	rows := collectRowsByType(in.Cfg, in.IsRunning, nil)
	return AggregateHealth(rows)
}

// AggregateHealthFromTopo computes stack health from topology node statuses.
// All non-disabled nodes are treated as active (including infrastructure containers
// such as nginx, db, redis that are not tracked in cfg.Services). Returns HealthStopped
// when topoStatus is empty.
func AggregateHealthFromTopo(topoStatus map[string]render.NodeStatus) Health {
	active := 0
	running := 0
	for _, status := range topoStatus {
		if status == render.NodeDisabled {
			continue
		}
		active++
		if status == render.NodeRunning {
			running++
		}
	}
	if active == 0 || running == 0 {
		return HealthStopped
	}
	if running < active {
		return HealthPartial
	}
	return HealthRunning
}
