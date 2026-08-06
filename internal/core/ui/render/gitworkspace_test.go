package render

import (
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/ui/statusview"
)

func TestRenderGitWorkspace_Empty(t *testing.T) {
	out := GitWorkspace(nil)
	if out == "" {
		t.Fatal("expected non-empty header even for empty rows")
	}
	for _, h := range []string{"SERVICE", "BRANCH", "SHA", "DIRTY"} {
		if !strings.Contains(out, h) {
			t.Errorf("missing header %q", h)
		}
	}
}

func TestRenderGitWorkspace_PopulatedRow(t *testing.T) {
	rows := []statusview.GitWorkspaceRow{
		{
			Service:     "app",
			Dir:         "./services/app",
			Branch:      "main",
			SHA:         "abcdef12",
			Dirty:       true,
			AheadBehind: "+1/-2",
		},
	}
	out := GitWorkspace(rows)
	for _, want := range []string{"app", "main", "abcdef12", "dirty", "+1/-2"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestRenderGitWorkspace_BlankRow(t *testing.T) {
	rows := []statusview.GitWorkspaceRow{
		{Service: "app", Dir: "./services/app"}, // no own .git
	}
	out := GitWorkspace(rows)
	// Branch / SHA / DIRTY / AHEAD/BEHIND should all be "—".
	if strings.Count(out, "—") < 4 {
		t.Errorf("expected at least 4 em-dash cells, got:\n%s", out)
	}
}

func TestRenderGitWorkspace_CleanRow(t *testing.T) {
	rows := []statusview.GitWorkspaceRow{
		{Service: "app", Dir: "./services/app", Branch: "main", SHA: "deadbeef", AheadBehind: "+0/-0"},
	}
	out := GitWorkspace(rows)
	if !strings.Contains(out, "clean") {
		t.Errorf("expected 'clean' in output, got:\n%s", out)
	}
}

func TestGitWorkspaceAt_ZeroWidthMatchesGitWorkspace(t *testing.T) {
	rows := []statusview.GitWorkspaceRow{
		{Service: "app", Dir: "./services/app", Branch: "main", SHA: "abcdef12", Dirty: true, AheadBehind: "+1/-2"},
	}
	if got, want := GitWorkspaceAt(rows, 0), GitWorkspace(rows); got != want {
		t.Errorf("GitWorkspaceAt(rows, 0) diverged from GitWorkspace:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestGitWorkspace_DelegatesProbedWidthToGitWorkspaceAt pins that
// GitWorkspace passes the budget it probes straight to GitWorkspaceAt. The
// zero-width test above cannot show this: TestMain pins the probe to 0, so
// both of its sides render unbounded whatever the delegation does.
func TestGitWorkspace_DelegatesProbedWidthToGitWorkspaceAt(t *testing.T) {
	resetStyles()
	rows := []statusview.GitWorkspaceRow{
		{Service: "app", Dir: "very/long/nested/service/directory/path/that/wont/fit", Branch: "feature/some-long-branch-name", SHA: "abcdef12", Dirty: true, AheadBehind: "+1/-2"},
	}
	withTermWidth(t, 60)
	if got, want := GitWorkspace(rows), GitWorkspaceAt(rows, 60); got != want {
		t.Errorf("GitWorkspace at a probed width of 60 diverged from GitWorkspaceAt(rows, 60):\ngot:  %q\nwant: %q", got, want)
	}
}

func TestGitWorkspaceAt_NarrowWidthTriggersRecordMode(t *testing.T) {
	resetStyles()
	longDir := "very/long/nested/service/directory/path/that/definitely/wont/fit/in/a/narrow/terminal"
	rows := []statusview.GitWorkspaceRow{
		{Service: "app", Dir: longDir, Branch: "feature/some-long-branch-name", SHA: "abcdef12", Dirty: true, AheadBehind: "+1/-2"},
	}
	out := GitWorkspaceAt(rows, 40)
	got := stripANSI(out)
	// The name promises record mode, so assert it: a Contains check alone
	// passes even when the width argument is dropped and the table renders
	// unbounded.
	if isTableMode(out) {
		t.Errorf("expected record mode at width 40, got a table: %q", got)
	}
	if !strings.Contains(got, "app") {
		t.Errorf("expected service name to survive narrow rendering: %q", got)
	}
	// wrapPath breaks the dir on "/" boundaries, so it is no longer one
	// substring — but no segment may be dropped, so it reassembles once the
	// wrap newlines and the continuation indent are removed.
	if flat := strings.NewReplacer("\n", "", " ", "").Replace(got); !strings.Contains(flat, longDir) {
		t.Errorf("expected the wrapped dir to reassemble to %q, got %q", longDir, got)
	}
}
