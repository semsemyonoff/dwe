package render

import (
	"fmt"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
)

// PendingBanner returns a formatted warning string for outstanding pending
// operations recorded in the deploy journal. Returns an empty string when p is
// nil or has no operations — safe to pass to writeNonEmpty.
//
// Output uses the warning palette so the banner stands out on plain status
// output. One line per op:
//
//   - PendingDeploy  → "⚠ Pending: deploy required for: a, b\n  Run: devbox deploy run"
//   - PendingRestart → "⚠ Pending: restart required\n  Run: devbox restart"
func PendingBanner(p *journal.PendingApply) string {
	if p == nil || len(p.Operations) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, op := range p.Operations {
		switch op.Kind {
		case journal.PendingDeploy:
			services := op.ServiceNames()
			banner := fmt.Sprintf("⚠ Pending: deploy required for: %s", strings.Join(services, ", "))
			fmt.Fprintln(&sb, styles.StyleWarning(banner))
			fmt.Fprintln(&sb, "  Run: "+styles.StyleKey("devbox deploy run"))
		case journal.PendingRestart:
			fmt.Fprintln(&sb, styles.StyleWarning("⚠ Pending: restart required"))
			fmt.Fprintln(&sb, "  Run: "+styles.StyleKey("devbox restart"))
		}
	}
	return sb.String()
}
