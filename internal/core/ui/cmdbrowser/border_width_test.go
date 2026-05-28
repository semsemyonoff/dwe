package cmdbrowser

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestModel_FullFrameWidth guards against the "torn right border" bug
// (originally body-only, widened in Task 11 to cover the full frame): at every
// supported width the title bar, joined body region, and help footer must each
// render exactly w cells wide on every non-empty row, so the right edge lines
// up with the terminal edge. Prior to the v2-lipgloss frame-semantics fix
// (Task 10) the body right edge fell short by 4 cells (two-panel) or 2 cells
// (single-panel); prior to Task 11 the title bar and help footer were
// rendered without a width envelope and stopped short of the panel edge.
func TestModel_FullFrameWidth(t *testing.T) {
	t.Parallel()
	items := []Item{
		{ID: "db.migrate", Description: "Apply pending migrations", Type: "shell"},
		{ID: "db.seed", Description: "Insert seed data for development", Type: "shell"},
		{ID: "infrastructure.deployment.kubernetes.production.replicas",
			Description: "Scale the kubernetes deployment to the configured replica count and rebalance the load across availability zones",
			Type:        "workflow"},
		{ID: "infrastructure.deployment.kubernetes.production.drain", Description: "Drain a node prior to maintenance", Type: "workflow"},
		{ID: "lint", Description: "Run linters", Type: "shell"},
	}
	for _, w := range []int{60, 80, 100, 120} {
		t.Run("width_"+itoa(w), func(t *testing.T) {
			t.Parallel()
			m := newModel("pick a command", items, DefaultOptions(), w, 26)
			out := m.View().Content
			anyLine := false
			for line := range strings.SplitSeq(out, "\n") {
				if line == "" {
					continue
				}
				anyLine = true
				if got := lipgloss.Width(line); got != w {
					t.Errorf("line width=%d, want %d (terminal width); line=%q", got, w, line)
				}
			}
			if !anyLine {
				t.Fatalf("no non-empty lines in View().Content at w=%d:\n%s", w, out)
			}
		})
	}
}

// itoa is a tiny local helper so the test file pulls in no new imports.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
