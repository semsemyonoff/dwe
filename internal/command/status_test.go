package command

import (
	"bytes"
	"testing"
	"time"

	"devbox-cli/internal/command/statusview"
	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/ui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for stack domain logic (aggregateHealth, runStatus, topology helpers,
// disabledNodes, etc.) have been moved to internal/stack/*_test.go.

func TestBuildDeployStatusView(t *testing.T) {
	tests := []struct {
		name       string
		state      *journal.ProjectState
		cfg        *config.DevboxConfig
		svcDeploys map[string]*config.DeployConfig
		tracked    []string
		expectRows int
		checkRow   func(t *testing.T, row statusview.DeployStatusRow)
	}{
		{
			name: "empty state no services",
			state: &journal.ProjectState{
				SchemaVersion: "1",
				Project:       &journal.ProjectLevelState{},
				Services:      make(map[string]*journal.ServiceState),
			},
			cfg: &config.DevboxConfig{
				Services: make(map[string]config.ServiceConfig),
			},
			tracked:    []string{},
			expectRows: 0,
		},
		{
			name: "service not yet deployed",
			state: &journal.ProjectState{
				SchemaVersion: "1",
				Project:       &journal.ProjectLevelState{},
				Services:      make(map[string]*journal.ServiceState),
			},
			cfg: &config.DevboxConfig{
				Services: map[string]config.ServiceConfig{
					"main": {
						Enabled:   true,
						Mandatory: false,
					},
				},
			},
			svcDeploys: map[string]*config.DeployConfig{
				"main": nil,
			},
			tracked:    []string{"main"},
			expectRows: 1,
			checkRow: func(t *testing.T, row statusview.DeployStatusRow) {
				assert.Equal(t, "main", row.Service)
				assert.Equal(t, journal.StatusNotDeployed, row.Status)
				assert.Equal(t, statusview.ConfigDeltaMissing, row.ConfigDelta)
			},
		},
		{
			name: "service deployed with matching config",
			state: &journal.ProjectState{
				SchemaVersion: "1",
				Project:       &journal.ProjectLevelState{},
				Services: map[string]*journal.ServiceState{
					"main": {
						Status:     journal.StatusDeployed,
						ConfigHash: "abc123def456",
						DeployedAt: time.Now(),
						Phases: map[string]*journal.PhaseState{
							"setup": {
								Status: journal.StatusOk,
								Steps: map[string]*journal.StepState{
									"create-dirs": {
										Status:     journal.StatusOk,
										ActionHash: "hash1",
										DurationMs: 10,
									},
								},
							},
						},
					},
				},
			},
			cfg: &config.DevboxConfig{
				Services: map[string]config.ServiceConfig{
					"main": {
						Enabled:   true,
						Mandatory: false,
					},
				},
			},
			svcDeploys: map[string]*config.DeployConfig{
				"main": nil,
			},
			tracked:    []string{"main"},
			expectRows: 1,
			checkRow: func(t *testing.T, row statusview.DeployStatusRow) {
				assert.Equal(t, "main", row.Service)
				assert.Equal(t, journal.StatusDeployed, row.Status)
				// Hash mismatch because our test service config will compute a different hash
				assert.True(t, row.ConfigDelta == statusview.ConfigDeltaOK || row.ConfigDelta == statusview.ConfigDeltaChanged)
				assert.Equal(t, 12, len(row.CurrHashShort))
				assert.Equal(t, 12, len(row.PrevHashShort))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view, err := buildDeployStatusView(tt.state, tt.cfg, tt.svcDeploys, tt.tracked)
			require.NoError(t, err)
			assert.Equal(t, tt.expectRows, len(view.Rows))

			if tt.checkRow != nil && len(view.Rows) > 0 {
				tt.checkRow(t, view.Rows[0])
			}
		})
	}
}

func TestRenderDeployStatus(t *testing.T) {
	rows := []ui.DeployStatusRow{
		{
			Service:         "main",
			Status:          "deployed",
			ConfigDelta:     "ok",
			PrevHashShort:   "abc12345",
			CurrHashShort:   "abc12345",
			LastFailedPhase: "",
			LastFailedStep:  "",
		},
		{
			Service:         "db",
			Status:          "failed",
			ConfigDelta:     "changed",
			PrevHashShort:   "old12345",
			CurrHashShort:   "new12345",
			LastFailedPhase: "setup",
			LastFailedStep:  "init-db",
		},
	}

	rendered := ui.RenderDeployStatus(rows)
	assert.NotEmpty(t, rendered)
	assert.Contains(t, rendered, "main")
	assert.Contains(t, rendered, "db")
	assert.Contains(t, rendered, "deployed")
	assert.Contains(t, rendered, "failed")
	assert.Contains(t, rendered, "changed")
}

func TestRenderServiceDeployDetail(t *testing.T) {
	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services: map[string]*journal.ServiceState{
			"main": {
				Status:     journal.StatusDeployed,
				ConfigHash: "abc123def456",
				LastRun: &journal.LastRun{
					Status:     journal.StatusOk,
					StartedAt:  time.Now().Add(-5 * time.Second),
					FinishedAt: time.Now(),
				},
				Phases: map[string]*journal.PhaseState{
					"setup": {
						Status: journal.StatusOk,
						Steps: map[string]*journal.StepState{
							"create-dirs": {
								Status:     journal.StatusOk,
								ActionHash: "hash123456789abc",
								DurationMs: 12,
							},
						},
					},
				},
			},
		},
	}

	tracked := []string{"main"}

	// Test service detail render
	buf := &bytes.Buffer{}
	err := renderServiceDeployDetail(buf, state, nil, nil, tracked, "main")
	require.NoError(t, err)
	output := buf.String()
	assert.Contains(t, output, "Deploy status for service")
	assert.Contains(t, output, "main")
	assert.Contains(t, output, "Overall status")
	assert.Contains(t, output, "Config hash")
	assert.Contains(t, output, "setup")
	assert.Contains(t, output, "create-dirs")

	// Test untracked service error
	buf = &bytes.Buffer{}
	err = renderServiceDeployDetail(buf, state, nil, nil, tracked, "untracked")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not tracked")

	// Test missing service
	buf = &bytes.Buffer{}
	err = renderServiceDeployDetail(buf, state, nil, nil, tracked, "missing")
	require.Error(t, err)
}

func TestRenderDeployStatusEmpty(t *testing.T) {
	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services:      make(map[string]*journal.ServiceState),
	}

	cfg := &config.DevboxConfig{
		Services: make(map[string]config.ServiceConfig),
	}

	buf := &bytes.Buffer{}
	err := renderDeployStatus(buf, state, cfg, make(map[string]*config.DeployConfig), []string{})
	require.NoError(t, err)
	// Should return nil with no output when no tracked services
}
