package cmdbrowser

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// ansiEscape matches CSI / OSC ANSI escape sequences. Used to assert that the
// view is monochrome under the `go test` colour profile (which mimics
// NO_COLOR / non-TTY rendering). lipgloss/v2 emits no escape codes when the
// profile is set to Ascii — go test's stdout is not a real terminal, so this
// is the implicit default.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]|\x1b\\][^\x07]*\x07")

func narrowItems() []Item {
	return []Item{
		{ID: "db.migrate", Description: "Apply pending migrations", Type: "shell"},
		{ID: "db.seed", Description: "Seed fixtures", Type: "shell"},
		{ID: "services.main.cs.build", Description: "Compile main service", Type: "workflow"},
		{ID: "services.main.cs.test", Description: "Run main service tests", Type: "workflow"},
		{ID: "lint", Description: "Run linters", Type: "shell"},
	}
}

func TestSinglePanel_InitialRenderAt70Cols(t *testing.T) {
	// Use a tall terminal so the list does not paginate; the test asserts
	// presence of items from every group.
	m := newModel("Select command", narrowItems(), DefaultOptions(), 70, 50)
	out := m.View().Content
	// Title bar present.
	if !strings.Contains(out, "Select command") {
		t.Errorf("missing title:\n%s", out)
	}
	// Pseudo-headers separate groups.
	if !strings.Contains(out, "─ db ─") {
		t.Errorf("missing 'db' pseudo-header in narrow render:\n%s", out)
	}
	if !strings.Contains(out, "─ services.main.cs ─") {
		t.Errorf("missing 'services.main.cs' pseudo-header:\n%s", out)
	}
	if !strings.Contains(out, "─ (root) ─") {
		t.Errorf("missing '(root)' pseudo-header for top-level commands:\n%s", out)
	}
	// Tree label must NOT appear in single-panel mode.
	if strings.Contains(out, "groups") {
		t.Errorf("single-panel must not show the 'groups' tree label:\n%s", out)
	}
	// IDs render.
	for _, want := range []string{"db.migrate", "services.main.cs.build", "lint"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing item %q in narrow render:\n%s", want, out)
		}
	}
}

func TestSinglePanel_NoTypeBadgesShown(t *testing.T) {
	m := newModel("t", narrowItems(), DefaultOptions(), 70, 24)
	out := m.View().Content
	// Badge format is "[shell]" / "[workflow]" — must not appear in narrow.
	for _, b := range []string{"[shell]", "[workflow]"} {
		if strings.Contains(out, b) {
			t.Errorf("narrow mode must hide type badges; found %q in:\n%s", b, out)
		}
	}
}

func TestSinglePanel_FilterActive(t *testing.T) {
	m := newModel("t", narrowItems(), DefaultOptions(), 70, 24)
	m.Update(syntheticKey("/"))
	if m.focus != focusFilter {
		t.Fatalf("expected focusFilter after '/', got %v", m.focus)
	}
	// Type "grt" (g-r-t present in "migrate" in order, absent from all other items)
	// so only db.migrate matches. Avoids 'i'/'y'/'?' which are now hotkeys in filter
	// mode and would open inspect / toggle skip-confirm / toggle help instead of
	// appending to the query.
	for _, r := range "grt" {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	out := m.View().Content
	if !strings.Contains(out, "/grt") {
		t.Errorf("filter query line missing:\n%s", out)
	}
	if !strings.Contains(out, "db.migrate") {
		t.Errorf("filter result missing db.migrate:\n%s", out)
	}
	// Filtered list does not show pseudo-headers.
	if strings.Contains(out, "─ db ─") {
		t.Errorf("filter result must not include pseudo-headers:\n%s", out)
	}
}

func TestSinglePanel_NoColorWriterStripsAllEscapes(t *testing.T) {
	// NO_COLOR is honoured by colorprofile.Writer at output time — the
	// in-memory View().Content always carries lipgloss's ANSI styling, but
	// when run under a NoTTY profile (mirroring NO_COLOR=1) the bytes the
	// terminal sees must be plain ASCII. This guards against any future
	// hand-rolled escape that bypasses the colorprofile writer.
	m := newModel("t", narrowItems(), DefaultOptions(), 70, 24)
	out := m.View().Content
	var buf bytes.Buffer
	w := &colorprofile.Writer{Forward: &buf, Profile: colorprofile.NoTTY}
	_, _ = w.Write([]byte(out))
	if ansiEscape.MatchString(buf.String()) {
		t.Errorf("NO_COLOR writer left ANSI escapes in stream:\n%q", buf.String())
	}
}

func TestSinglePanel_LongIDTruncatesWithEllipsis(t *testing.T) {
	long := "services.main.index.reindex-catalog-product-availability-search"
	items := []Item{
		{ID: long, Description: "really long description that also needs truncating to fit", Type: "shell"},
	}
	m := newModel("t", items, DefaultOptions(), 70, 24)
	out := m.View().Content
	// The full long ID must NOT appear verbatim — it would overflow the 70-col panel.
	if strings.Contains(out, long) {
		t.Errorf("long ID rendered untruncated; expected ellipsis. Output:\n%s", out)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("expected ellipsis '…' marker in truncated render:\n%s", out)
	}
	// No bordered line exceeds the panel width. The help footer below the
	// panel may legitimately exceed it (it word-wraps in the real terminal).
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.ContainsAny(line, "│┌┐└┘") {
			continue
		}
		if lipgloss.Width(line) > 70 {
			t.Errorf("bordered line wider than panel (70 cols): width=%d line=%q", lipgloss.Width(line), line)
		}
	}
}

func TestSinglePanel_TabIsNoOp(t *testing.T) {
	m := newModel("t", narrowItems(), DefaultOptions(), 70, 24)
	before := m.focus
	m.Update(syntheticKey("tab"))
	if m.focus != before {
		t.Errorf("tab must be a no-op in single-panel mode; focus changed %v -> %v", before, m.focus)
	}
}

func TestSinglePanel_EnterOnHeaderIsNoOp(t *testing.T) {
	m := newModel("t", narrowItems(), DefaultOptions(), 70, 24)
	// The first list row is always a pseudo-header (groups sort
	// lexicographically; the smallest group is "" → "(root)").
	_, cmd := m.Update(syntheticKey("enter"))
	if cmd != nil {
		t.Errorf("Enter on header row must not quit (cmd should be nil), got %v", cmd)
	}
	if m.cancelled {
		t.Errorf("model must not be cancelled by Enter on a header")
	}
}

// TestTwoPanel_TotalWidthFitsTerminal verifies that the joined two-panel body
// (left + right bordered panels) does not exceed the terminal width. Prior to
// the fix rightWidth subtracted only 2 (left panel's borders) instead of 4
// (both panels' borders), making the rendered output 2 columns too wide.
func TestTwoPanel_TotalWidthFitsTerminal(t *testing.T) {
	t.Parallel()
	for _, w := range []int{80, 100, 120, 140} {
		m := newModel("pick", narrowItems(), DefaultOptions(), w, 30)
		out := m.View().Content
		for line := range strings.SplitSeq(out, "\n") {
			// Ignore the footer line — the help text may soft-wrap beyond the
			// panel width on very wide terminals (it uses its own logic).
			if !strings.ContainsAny(line, "│┌┐└┘") {
				continue
			}
			if got := lipgloss.Width(line); got > w {
				t.Errorf("w=%d: bordered line width=%d exceeds terminal; line=%q", w, got, line)
			}
		}
	}
}

func TestSinglePanel_ResizeAcrossBoundaryRebuildsList(t *testing.T) {
	// Start in two-panel mode, resize down into single-panel.
	m := newModel("t", narrowItems(), DefaultOptions(), 120, 30)
	if singlePanel(m.width) {
		t.Fatalf("expected two-panel start, got single")
	}
	m.Update(tea.WindowSizeMsg{Width: 70, Height: 24})
	if !singlePanel(m.width) {
		t.Fatalf("resize did not switch to single-panel mode")
	}
	out := m.View().Content
	if !strings.Contains(out, "─ db ─") {
		t.Errorf("list not rebuilt with headers after resize:\n%s", out)
	}
	// And resize back up.
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if singlePanel(m.width) {
		t.Fatalf("resize back did not switch to two-panel mode")
	}
	out = m.View().Content
	if !strings.Contains(out, "groups") {
		t.Errorf("two-panel layout not restored:\n%s", out)
	}
}
