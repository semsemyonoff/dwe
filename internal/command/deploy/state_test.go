package deploy

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"devbox-cli/internal/command/cmdctx"
	pipeline "devbox-cli/internal/core/execution/pipeline"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/deploy/journal"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeployStateShow(t *testing.T) {
	t.Run("state file exists", func(t *testing.T) {
		workDir := t.TempDir()
		stateDir := filepath.Join(workDir, ".devbox", "deploy")
		statePath := filepath.Join(stateDir, "state.yml")

		// Create a state file
		state := &journal.ProjectState{
			SchemaVersion: "1",
			Project: &journal.ProjectLevelState{
				Status:     journal.StatusDeployed,
				DeployedAt: time.Now(),
				ConfigHash: "abc123",
			},
			Services: map[string]*journal.ServiceState{
				"main": {
					Status:     journal.StatusDeployed,
					DeployedAt: time.Now(),
					ConfigHash: "def456",
				},
			},
		}
		err := journal.Save(statePath, state)
		require.NoError(t, err)

		// Create a root context pointing to the temp directory
		flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(workDir, "devbox.yml")}

		// Run the show command
		err = deployStateShowCmd(flags, io.Discard)
		assert.NoError(t, err)
	})

	t.Run("state file absent", func(t *testing.T) {
		workDir := t.TempDir()
		flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(workDir, "devbox.yml")}

		// Run the show command (file doesn't exist)
		err := deployStateShowCmd(flags, io.Discard)
		assert.NoError(t, err)
	})
}

func TestDeployStateClear(t *testing.T) {
	t.Run("clears existing state", func(t *testing.T) {
		workDir := t.TempDir()
		stateDir := filepath.Join(workDir, ".devbox", "deploy")
		statePath := filepath.Join(stateDir, "state.yml")

		// Create a state file
		state := &journal.ProjectState{
			SchemaVersion: "1",
			Project: &journal.ProjectLevelState{
				Status:     journal.StatusDeployed,
				ConfigHash: "abc123",
			},
			Services: make(map[string]*journal.ServiceState),
		}
		err := journal.Save(statePath, state)
		require.NoError(t, err)

		// Verify it exists
		_, err = os.Stat(statePath)
		require.NoError(t, err)

		flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(workDir, "devbox.yml")}

		// Clear with force=true (skip confirmation)
		err = deployStateClearCmd(flags, true)
		assert.NoError(t, err)

		// Verify file is deleted
		_, err = os.Stat(statePath)
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("no-op when state absent", func(t *testing.T) {
		workDir := t.TempDir()
		flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(workDir, "devbox.yml")}

		// Clear when file doesn't exist
		err := deployStateClearCmd(flags, true)
		assert.NoError(t, err)
	})
}

func TestDeployStateRepair(t *testing.T) {
	t.Run("recomputes status aggregates", func(t *testing.T) {
		workDir := t.TempDir()
		stateDir := filepath.Join(workDir, ".devbox", "deploy")
		statePath := filepath.Join(stateDir, "state.yml")

		// Create a state with service status that needs recompute
		state := &journal.ProjectState{
			SchemaVersion: "1",
			Project: &journal.ProjectLevelState{
				Status:     journal.StatusNotDeployed, // Wrong; should be derived from services
				ConfigHash: "abc123",
			},
			Services: map[string]*journal.ServiceState{
				"main": {
					Status:     journal.StatusDeployed,
					ConfigHash: "def456",
				},
				"secondary": {
					Status:     journal.StatusNotDeployed,
					ConfigHash: "ghi789",
				},
			},
		}
		err := journal.Save(statePath, state)
		require.NoError(t, err)

		flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(workDir, "devbox.yml")}

		// Run repair
		err = deployStateRepairCmd(flags)
		assert.NoError(t, err)

		// Load and verify status was recomputed to Partial
		loaded, err := journal.Load(statePath)
		require.NoError(t, err)
		assert.Equal(t, journal.StatusPartial, loaded.Project.Status)
	})

	t.Run("no-op when state absent", func(t *testing.T) {
		workDir := t.TempDir()
		flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(workDir, "devbox.yml")}

		// Repair when file doesn't exist
		err := deployStateRepairCmd(flags)
		assert.NoError(t, err)
	})

	t.Run("preserves step data", func(t *testing.T) {
		workDir := t.TempDir()
		stateDir := filepath.Join(workDir, ".devbox", "deploy")
		statePath := filepath.Join(stateDir, "state.yml")

		// Create state with step data
		state := &journal.ProjectState{
			SchemaVersion: "1",
			Project: &journal.ProjectLevelState{
				Status:     journal.StatusNotDeployed, // Wrong
				ConfigHash: "abc123",
				Phases: map[string]*journal.PhaseState{
					"setup": {
						Status: journal.StatusOk,
						Steps: map[string]*journal.StepState{
							"render-env": {
								Status:     journal.StatusOk,
								ActionHash: "step-hash-123",
								DurationMs: 100,
								FinishedAt: time.Now(),
							},
						},
					},
				},
			},
			Services: map[string]*journal.ServiceState{
				"main": {
					Status:     journal.StatusDeployed,
					ConfigHash: "def456",
				},
			},
		}
		err := journal.Save(statePath, state)
		require.NoError(t, err)

		flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(workDir, "devbox.yml")}

		// Run repair
		err = deployStateRepairCmd(flags)
		assert.NoError(t, err)

		// Load and verify step data is preserved
		loaded, err := journal.Load(statePath)
		require.NoError(t, err)

		require.NotNil(t, loaded.Project.Phases)
		require.NotNil(t, loaded.Project.Phases["setup"])
		require.NotNil(t, loaded.Project.Phases["setup"].Steps["render-env"])

		step := loaded.Project.Phases["setup"].Steps["render-env"]
		assert.Equal(t, journal.StatusOk, step.Status)
		assert.Equal(t, "step-hash-123", step.ActionHash)
		assert.Equal(t, int64(100), step.DurationMs)
	})
}

// TestClearDeployedPending verifies that clearDeployedPending only removes
// pending entries for services that actually appeared in the plan steps,
// leaving disabled-service entries intact.
func TestClearDeployedPending(t *testing.T) {
	makeStep := func(service string) pipeline.ResolvedStep {
		return pipeline.ResolvedStep{Step: config.DeployStep{}, Service: service}
	}

	t.Run("full deploy: disabled service pending survives", func(t *testing.T) {
		dir := t.TempDir()
		statePath := filepath.Join(dir, "state.yml")
		// Pre-seed: {deploy, [postgres, web]} — postgres is disabled, web is enabled.
		require.NoError(t, journal.AddPendingOp(statePath, journal.PendingOp{
			Kind:     journal.PendingDeploy,
			Services: []string{"postgres", "web"},
		}, "hash1"))

		// Plan only contains steps for web (postgres is disabled; full deploy skips it).
		steps := []pipeline.ResolvedStep{
			{Step: config.DeployStep{}, Service: ""}, // implicit env step (no service)
			makeStep("web"),
		}
		clearDeployedPending(statePath, Opts{}, steps)

		state, err := journal.Load(statePath)
		require.NoError(t, err)
		require.NotNil(t, state.Pending, "pending should remain for postgres")
		op := state.Pending.Find(journal.PendingDeploy)
		require.NotNil(t, op)
		assert.Equal(t, []string{"postgres"}, op.Services)
	})

	t.Run("full deploy: all services in plan clears pending entirely", func(t *testing.T) {
		dir := t.TempDir()
		statePath := filepath.Join(dir, "state.yml")
		require.NoError(t, journal.AddPendingOp(statePath, journal.PendingOp{
			Kind:     journal.PendingDeploy,
			Services: []string{"alpha", "beta"},
		}, "hash1"))

		steps := []pipeline.ResolvedStep{makeStep("alpha"), makeStep("beta")}
		clearDeployedPending(statePath, Opts{}, steps)

		state, err := journal.Load(statePath)
		require.NoError(t, err)
		assert.Nil(t, state.Pending, "all services deployed: pending should be nil")
	})

	t.Run("full deploy: env-only plan (no service steps) leaves pending intact", func(t *testing.T) {
		dir := t.TempDir()
		statePath := filepath.Join(dir, "state.yml")
		require.NoError(t, journal.AddPendingOp(statePath, journal.PendingOp{
			Kind:     journal.PendingDeploy,
			Services: []string{"web"},
		}, "hash1"))

		// Only implicit env step, no per-service steps.
		steps := []pipeline.ResolvedStep{{Step: config.DeployStep{}, Service: ""}}
		clearDeployedPending(statePath, Opts{}, steps)

		state, err := journal.Load(statePath)
		require.NoError(t, err)
		require.NotNil(t, state.Pending)
		op := state.Pending.Find(journal.PendingDeploy)
		require.NotNil(t, op)
		assert.Equal(t, []string{"web"}, op.Services)
	})

	t.Run("SuppressPendingClear: nothing is cleared", func(t *testing.T) {
		dir := t.TempDir()
		statePath := filepath.Join(dir, "state.yml")
		require.NoError(t, journal.AddPendingOp(statePath, journal.PendingOp{
			Kind:     journal.PendingDeploy,
			Services: []string{"web"},
		}, "hash1"))

		steps := []pipeline.ResolvedStep{makeStep("web")}
		clearDeployedPending(statePath, Opts{SuppressPendingClear: true}, steps)

		state, err := journal.Load(statePath)
		require.NoError(t, err)
		require.NotNil(t, state.Pending, "SuppressPendingClear: pending must not be touched")
	})

	t.Run("subset deploy: only targeted services cleared", func(t *testing.T) {
		dir := t.TempDir()
		statePath := filepath.Join(dir, "state.yml")
		require.NoError(t, journal.AddPendingOp(statePath, journal.PendingOp{
			Kind:     journal.PendingDeploy,
			Services: []string{"alpha", "beta", "gamma"},
		}, "hash1"))

		clearDeployedPending(statePath, Opts{Services: []string{"alpha"}}, nil)

		state, err := journal.Load(statePath)
		require.NoError(t, err)
		require.NotNil(t, state.Pending)
		op := state.Pending.Find(journal.PendingDeploy)
		require.NotNil(t, op)
		assert.Equal(t, []string{"beta", "gamma"}, op.Services)
	})
}
