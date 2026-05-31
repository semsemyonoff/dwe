package checks

import (
	"slices"

	"github.com/semsemyonoff/devbox/internal/core/project/config"
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
