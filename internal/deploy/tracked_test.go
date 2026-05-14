package deploy

import (
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/pipeline"

	"github.com/stretchr/testify/assert"
)

func TestTrackedServices(t *testing.T) {
	tests := []struct {
		name     string
		plan     []pipeline.ResolvedStep
		expected []string
	}{
		{
			name:     "empty plan",
			plan:     []pipeline.ResolvedStep{},
			expected: []string{},
		},
		{
			name: "project-scope steps only (no services)",
			plan: []pipeline.ResolvedStep{
				{
					Phase: config.DeployPhase{Name: "pre"},
					Step:  config.DeployStep{Name: "setup"},
					// Service is empty
				},
			},
			expected: []string{},
		},
		{
			name: "single service",
			plan: []pipeline.ResolvedStep{
				{
					Phase:   config.DeployPhase{Name: "setup"},
					Step:    config.DeployStep{Name: "init"},
					Service: "main",
				},
			},
			expected: []string{"main"},
		},
		{
			name: "multiple services sorted",
			plan: []pipeline.ResolvedStep{
				{
					Phase:   config.DeployPhase{Name: "setup"},
					Step:    config.DeployStep{Name: "init"},
					Service: "db",
				},
				{
					Phase:   config.DeployPhase{Name: "setup"},
					Step:    config.DeployStep{Name: "init"},
					Service: "app",
				},
			},
			expected: []string{"app", "db"},
		},
		{
			name: "duplicates deduplicated and sorted",
			plan: []pipeline.ResolvedStep{
				{
					Phase:   config.DeployPhase{Name: "setup"},
					Step:    config.DeployStep{Name: "init"},
					Service: "main",
				},
				{
					Phase:   config.DeployPhase{Name: "setup"},
					Step:    config.DeployStep{Name: "migrate"},
					Service: "main",
				},
				{
					Phase:   config.DeployPhase{Name: "setup"},
					Step:    config.DeployStep{Name: "init"},
					Service: "worker",
				},
			},
			expected: []string{"main", "worker"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TrackedServices(tt.plan)
			assert.Equal(t, tt.expected, result)
		})
	}
}
