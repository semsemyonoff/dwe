package checks

import (
	"slices"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// MatchStage reports whether entry participates in the given stage. An empty
// stage string matches every entry — callers use that to iterate all entries
// without a stage filter.
func MatchStage(entry config.CheckEntry, stage string) bool {
	if stage == "" {
		return true
	}
	return slices.Contains(entry.Stages, stage)
}

// MatchAnyStage reports whether entry participates in ANY of the given stages.
// An empty (nil or zero-length) stages slice matches every entry, mirroring
// MatchStage's empty-stage semantics. The deploy final preflight uses this to
// run both the "deploy" and "post-setup" stages in a single pass.
func MatchAnyStage(entry config.CheckEntry, stages []string) bool {
	if len(stages) == 0 {
		return true
	}
	for _, s := range stages {
		if MatchStage(entry, s) {
			return true
		}
	}
	return false
}

// MatchServices reports whether entry's services-gate is satisfied for the
// given merged service map. Entries with no services: clause pass unconditionally.
// Otherwise the gate is OR: at least one listed service must be Enabled.
// Unknown service names (typos) are NOT silently ignored here — they evaluate
// as "not enabled" and contribute nothing to the OR; the config-domain
// validator surfaces them as load-time diagnostics so users see the typo.
func MatchServices(entry config.CheckEntry, services map[string]config.ServiceConfig) bool {
	if len(entry.Services) == 0 {
		return true
	}
	for _, name := range entry.Services {
		if svc, ok := services[name]; ok && svc.Enabled {
			return true
		}
	}
	return false
}
