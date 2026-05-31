package render

import (
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/devbox/internal/core/workflow/deploy/journal"
)

func TestRenderDeployInfo_Empty(t *testing.T) {
	out := DeployInfo(nil, time.Now(), nil)
	if out != "" {
		t.Fatalf("expected empty output, got %q", out)
	}
}

func TestRenderDeployInfo_ProjectAndServices(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	state := &journal.ProjectState{
		Project: &journal.ProjectLevelState{
			Status:     journal.StatusDeployed,
			DeployedAt: now.Add(-5 * time.Minute),
		},
	}
	rows := []DeployInfoRow{
		{Name: "main", Type: "app", DeployedAt: now.Add(-5 * time.Minute), Status: journal.StatusDeployed},
		{Name: "worker", Type: "app", NotDeployed: true},
		{Name: "adminer", Type: "tool", DeployedAt: now.Add(-5 * time.Minute), Status: journal.StatusDeployed},
	}
	out := DeployInfo(state, now, rows)
	if !strings.Contains(out, "Last deploy") {
		t.Fatalf("missing 'Last deploy' header: %q", out)
	}
	if !strings.Contains(out, "5m ago") {
		t.Fatalf("missing relative time: %q", out)
	}
	if !strings.Contains(out, "main") || !strings.Contains(out, "worker") || !strings.Contains(out, "adminer") {
		t.Fatalf("missing service names: %q", out)
	}
	if !strings.Contains(out, "not deployed") {
		t.Fatalf("missing not deployed marker: %q", out)
	}
}

func TestRelativeTime(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   time.Time
		want string
	}{
		{time.Time{}, "never"},
		{now.Add(-30 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5m ago"},
		{now.Add(-3 * time.Hour), "3h ago"},
		{now.Add(-2 * 24 * time.Hour), "2d ago"},
	}
	for _, c := range cases {
		got := relativeTime(c.in, now)
		if got != c.want {
			t.Errorf("relativeTime(%v): got %q, want %q", c.in, got, c.want)
		}
	}
}
