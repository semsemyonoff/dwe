package cmdbrowser

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// borderRune matches any cell that belongs to the bordered body block:
// corners, horizontal and vertical strokes of lipgloss.NormalBorder().
const borderRune = "│┌┐└┘─"

// containsBorder reports whether s belongs to the bordered body region
// (excludes the title bar and the help footer, which are intentionally not
// full-width until Task 11 lands).
func containsBorder(s string) bool { return strings.ContainsAny(s, borderRune) }

// TestModel_BodyBorderFullWidth guards against the "torn right border" bug:
// at every supported width the joined body region must render exactly w cells
// wide on every bordered row, so the right border lines up with the terminal
// edge. Prior to the v2-lipgloss frame-semantics fix the right edge fell short
// by 4 cells (two-panel) or 2 cells (single-panel).
//
// TODO(Task 11): widen the assertion to cover title bar and help footer once
// those are wrapped in a `lipgloss.NewStyle().Width(totalWidth)` envelope.
func TestModel_BodyBorderFullWidth(t *testing.T) {
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
			seenBorder := false
			for line := range strings.SplitSeq(out, "\n") {
				if !containsBorder(line) {
					continue
				}
				seenBorder = true
				if got := lipgloss.Width(line); got != w {
					t.Errorf("body line width=%d, want %d (terminal width); line=%q", got, w, line)
				}
			}
			if !seenBorder {
				t.Fatalf("no bordered body lines found in View().Content at w=%d:\n%s", w, out)
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
