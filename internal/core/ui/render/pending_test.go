package render_test

import (
	"strings"
	"testing"

	"github.com/semsemyonoff/devbox/internal/core/ui/render"
	"github.com/semsemyonoff/devbox/internal/core/workflow/deploy/journal"
)

func TestRenderPendingBanner(t *testing.T) {
	tests := []struct {
		name        string
		pending     *journal.PendingApply
		wantEmpty   bool
		wantContain []string
		wantAbsent  []string
	}{
		{
			name:      "nil pending returns empty",
			pending:   nil,
			wantEmpty: true,
		},
		{
			name:      "empty operations returns empty",
			pending:   &journal.PendingApply{Operations: nil},
			wantEmpty: true,
		},
		{
			name: "deploy kind renders service names",
			pending: &journal.PendingApply{
				Operations: []journal.PendingOp{
					{Kind: journal.PendingDeploy, Services: []string{"a", "b"}},
				},
			},
			wantContain: []string{"Pending", "deploy required for", "a, b", "devbox deploy run"},
			wantAbsent:  []string{"restart"},
		},
		{
			name: "restart kind renders restart line",
			pending: &journal.PendingApply{
				Operations: []journal.PendingOp{
					{Kind: journal.PendingRestart},
				},
			},
			wantContain: []string{"Pending", "restart required", "devbox restart"},
			wantAbsent:  []string{"deploy required"},
		},
		{
			name: "mixed pending renders both lines",
			pending: &journal.PendingApply{
				Operations: []journal.PendingOp{
					{Kind: journal.PendingDeploy, Services: []string{"main"}},
					{Kind: journal.PendingRestart},
				},
			},
			wantContain: []string{"deploy required for: main", "restart required", "devbox deploy run", "devbox restart"},
		},
		{
			name: "deploy with single service",
			pending: &journal.PendingApply{
				Operations: []journal.PendingOp{
					{Kind: journal.PendingDeploy, Services: []string{"postgres"}},
				},
			},
			wantContain: []string{"postgres"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := render.PendingBanner(tt.pending)
			if tt.wantEmpty {
				if got != "" {
					t.Errorf("expected empty string, got %q", got)
				}
				return
			}
			for _, want := range tt.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("expected output to contain %q:\n%s", want, got)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("expected output NOT to contain %q:\n%s", absent, got)
				}
			}
		})
	}
}
