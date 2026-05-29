package stack

import "devbox-cli/internal/core/ui/render"

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
