package deploy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"

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

func TestLoadTrackedServices_Integration(t *testing.T) {
	// This is an integration test that exercises the full path of
	// ResolvePlan + TrackedServices + LoadServiceDeployConfigs.
	// We use a simple case with no services to verify the composition works.

	testDir := t.TempDir()

	// Create minimal devbox.yml
	devboxYML := `project:
  name: test
  prefix: devbox
`
	if err := os.WriteFile(
		filepath.Join(testDir, "devbox.yml"),
		[]byte(devboxYML),
		0644,
	); err != nil {
		t.Fatalf("writing devbox.yml: %v", err)
	}

	// Create devbox dir
	devboxDir := filepath.Join(testDir, "devbox")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		t.Fatalf("creating devbox dir: %v", err)
	}

	// Create a minimal deploy.yml with no deploy_services
	deployYML := `phases:
  - name: setup
    steps:
      - name: echo
        type: shell
        cmd: echo setup
`
	if err := os.WriteFile(
		filepath.Join(devboxDir, "deploy.yml"),
		[]byte(deployYML),
		0644,
	); err != nil {
		t.Fatalf("writing deploy.yml: %v", err)
	}

	// Load config
	cfg, err := config.LoadConfig(filepath.Join(testDir, "devbox.yml"))
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Call LoadTrackedServices - should return empty list since no deploy_services: true
	tracked, svcDeploys, err := LoadTrackedServices(cfg, usercommands.NewEmptyRegistry(), testDir)
	if err != nil {
		t.Fatalf("LoadTrackedServices failed: %v", err)
	}

	// Verify no services tracked (no deploy_services: true in plan)
	assert.Equal(t, []string{}, tracked)
	assert.Len(t, svcDeploys, 0)
}
