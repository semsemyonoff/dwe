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
// Output uses the warning palette so the banner stands out on plain status
// output. One line per op:
//
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
			banner := fmt.Sprintf("⚠ Pending: deploy required for: %s", strings.Join(services, ", "))
			fmt.Fprintln(&sb, StyleWarning(banner))
			fmt.Fprintln(&sb, "  Run: "+StyleKey("devbox deploy run"))
		case journal.PendingRestart:
			fmt.Fprintln(&sb, StyleWarning("⚠ Pending: restart required"))
			fmt.Fprintln(&sb, "  Run: "+StyleKey("devbox restart"))
		}
	}
	return sb.String()
}
