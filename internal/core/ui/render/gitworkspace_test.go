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

func TestGitWorkspaceAt_NarrowWidthTriggersRecordMode(t *testing.T) {
	longDir := "very/long/nested/service/directory/path/that/definitely/wont/fit/in/a/narrow/terminal"
	rows := []statusview.GitWorkspaceRow{
		{Service: "app", Dir: longDir, Branch: "feature/some-long-branch-name", SHA: "abcdef12", Dirty: true, AheadBehind: "+1/-2"},
	}
	got := stripANSI(GitWorkspaceAt(rows, 40))
	if !strings.Contains(got, "app") {
		t.Errorf("expected service name to survive narrow rendering: %q", got)
	}
}
