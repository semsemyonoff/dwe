package ui

import (
	"fmt"
	"strings"

	"devbox-cli/internal/deploy/journal"
)

// RenderPendingBanner returns a formatted warning string for outstanding pending
// operations recorded in the deploy journal. Returns an empty string when p is
// nil or has no operations — safe to pass to writeNonEmpty.
//
// One line per op:
//   - PendingDeploy  → "⚠ Pending: deploy required for: a, b\n  Run: devbox deploy run"
//   - PendingRestart → "⚠ Pending: restart required\n  Run: devbox restart"
func RenderPendingBanner(p *journal.PendingApply) string {
	if p == nil || len(p.Operations) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, op := range p.Operations {
		switch op.Kind {
		case journal.PendingDeploy:
			services := op.ServiceNames()
			fmt.Fprintf(&sb, "⚠ Pending: deploy required for: %s\n  Run: devbox deploy run\n", strings.Join(services, ", "))
		case journal.PendingRestart:
			fmt.Fprintf(&sb, "⚠ Pending: restart required\n  Run: devbox restart\n")
		}
	}
	return sb.String()
}
