package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/filesgate"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFileRecorder_MixedProjectAndServiceSteps tests that project-level and
// service-scoped steps are routed to the correct subtrees in the state file.
func TestFileRecorder_MixedProjectAndServiceSteps(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	// Start with empty state
	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services:      make(map[string]*journal.ServiceState),
	}

	serviceHashes := map[string]string{
		"main": "svc-main-hash",
		"db":   "svc-db-hash",
	}
	projectHash := "project-hash"

	rec := NewFileRecorder(statePath, state, serviceHashes, projectHash, true)

	// Simulate pipeline execution
	rec.OnPipelineStart("deploy", 4)

	// Project-level step in pre-deploy phase
	projectStep := ResolvedStep{
		Phase: config.DeployPhase{Name: "pre-deploy"},
		Step:  config.DeployStep{Name: "render-env", Type: "shell", Cmd: "echo test"},
	}
	rec.OnStepStart(projectStep.StepAddress(), projectStep, "project-step-hash")
	rec.OnStepFinish(projectStep.StepAddress(), projectStep, "project-step-hash", 10)

	// Service step for main
	mainStep := ResolvedStep{
		Service: "main",
		Phase:   config.DeployPhase{Name: "setup"},
		Step:    config.DeployStep{Name: "create-dirs", Type: "shell", Cmd: "mkdir -p src"},
	}
	rec.OnStepStart(mainStep.StepAddress(), mainStep, "main-create-dirs-hash")
	rec.OnStepFinish(mainStep.StepAddress(), mainStep, "main-create-dirs-hash", 5)

	// Another service step for db
	dbStep := ResolvedStep{
		Service: "db",
		Phase:   config.DeployPhase{Name: "setup"},
		Step:    config.DeployStep{Name: "init-db", Type: "shell", Cmd: "psql init"},
	}
	rec.OnStepStart(dbStep.StepAddress(), dbStep, "db-init-hash")
	rec.OnStepFinish(dbStep.StepAddress(), dbStep, "db-init-hash", 20)

	// Another project-level step
	projectStep2 := ResolvedStep{
		Phase: config.DeployPhase{Name: "finalize"},
		Step:  config.DeployStep{Name: "summary", Type: "shell", Cmd: "echo done"},
	}
	rec.OnStepStart(projectStep2.StepAddress(), projectStep2, "project-summary-hash")
	rec.OnStepFinish(projectStep2.StepAddress(), projectStep2, "project-summary-hash", 3)

	rec.OnPipelineFinish(true)

	// Verify state file was written
	require.FileExists(t, statePath)

	// Load and verify state structure
	loaded, err := journal.Load(statePath)
	require.NoError(t, err)

	// Verify project-level phases and steps
	require.NotNil(t, loaded.Project)
	require.NotNil(t, loaded.Project.Phases)
	assert.Len(t, loaded.Project.Phases, 2)

	// Check pre-deploy phase
	assert.NotNil(t, loaded.Project.Phases["pre-deploy"])
	assert.NotNil(t, loaded.Project.Phases["pre-deploy"].Steps)
	renderEnvStep, ok := loaded.Project.Phases["pre-deploy"].Steps["render-env"]
	assert.True(t, ok)
	assert.Equal(t, journal.StatusOk, renderEnvStep.Status)
	assert.Equal(t, "project-step-hash", renderEnvStep.ActionHash)
	assert.Equal(t, int64(10), renderEnvStep.DurationMs)

	// Check finalize phase
	assert.NotNil(t, loaded.Project.Phases["finalize"])
	summaryStep, ok := loaded.Project.Phases["finalize"].Steps["summary"]
	assert.True(t, ok)
	assert.Equal(t, journal.StatusOk, summaryStep.Status)

	// Verify service-level phases and steps
	assert.Len(t, loaded.Services, 2)

	// Check main service
	require.NotNil(t, loaded.Services["main"])
	require.NotNil(t, loaded.Services["main"].Phases)
	mainSetup, ok := loaded.Services["main"].Phases["setup"]
	assert.True(t, ok)
	mainCreateDirs, ok := mainSetup.Steps["create-dirs"]
	assert.True(t, ok)
	assert.Equal(t, journal.StatusOk, mainCreateDirs.Status)
	assert.Equal(t, "main-create-dirs-hash", mainCreateDirs.ActionHash)
	assert.Equal(t, int64(5), mainCreateDirs.DurationMs)

	// Check db service
	require.NotNil(t, loaded.Services["db"])
	dbSetup, ok := loaded.Services["db"].Phases["setup"]
	assert.True(t, ok)
	dbInitStep, ok := dbSetup.Steps["init-db"]
	assert.True(t, ok)
	assert.Equal(t, journal.StatusOk, dbInitStep.Status)
	assert.Equal(t, "db-init-hash", dbInitStep.ActionHash)

	// Verify config hashes were stamped
	assert.Equal(t, projectHash, loaded.Project.ConfigHash)
	assert.Equal(t, "svc-main-hash", loaded.Services["main"].ConfigHash)
	assert.Equal(t, "svc-db-hash", loaded.Services["db"].ConfigHash)
}

// TestFileRecorder_FailedStep tests state recording when a step fails.
func TestFileRecorder_FailedStep(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services:      make(map[string]*journal.ServiceState),
	}

	rec := NewFileRecorder(statePath, state, map[string]string{}, "project-hash", true)
	rec.OnPipelineStart("deploy", 2)

	// First step succeeds
	step1 := ResolvedStep{
		Service: "main",
		Phase:   config.DeployPhase{Name: "setup"},
		Step:    config.DeployStep{Name: "step1", Type: "shell", Cmd: "echo ok"},
	}
	rec.OnStepStart(step1.StepAddress(), step1, "hash1")
	rec.OnStepFinish(step1.StepAddress(), step1, "hash1", 5)

	// Second step fails
	step2 := ResolvedStep{
		Service: "main",
		Phase:   config.DeployPhase{Name: "setup"},
		Step:    config.DeployStep{Name: "step2", Type: "shell", Cmd: "exit 1"},
	}
	rec.OnStepStart(step2.StepAddress(), step2, "hash2")
	rec.OnStepFail(step2.StepAddress(), step2, "hash2", 10, fmt.Errorf("step failed"))

	rec.OnPipelineFinish(false)

	// Load and verify
	loaded, err := journal.Load(statePath)
	require.NoError(t, err)

	require.NotNil(t, loaded.Services["main"])
	steps := loaded.Services["main"].Phases["setup"].Steps

	// First step should be ok
	step1State := steps["step1"]
	assert.Equal(t, journal.StatusOk, step1State.Status)

	// Second step should be failed
	step2State := steps["step2"]
	assert.Equal(t, journal.StatusFailed, step2State.Status)

	// Service status should be failed
	assert.Equal(t, journal.StatusFailed, loaded.Services["main"].Status)
	assert.Equal(t, journal.StatusFailed, loaded.Project.LastRun.Status)
}

// TestFileRecorder_SkippedStep tests that skipped steps are recorded correctly.
func TestFileRecorder_SkippedStep(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services:      make(map[string]*journal.ServiceState),
	}

	rec := NewFileRecorder(statePath, state, map[string]string{"main": "hash"}, "project-hash", true)
	rec.OnPipelineStart("deploy", 2)

	step1 := ResolvedStep{
		Service: "main",
		Phase:   config.DeployPhase{Name: "setup"},
		Step:    config.DeployStep{Name: "step1", Type: "shell", Cmd: "echo ok"},
	}
	rec.OnStepStart(step1.StepAddress(), step1, "hash1")
	rec.OnStepFinish(step1.StepAddress(), step1, "hash1", 5)

	step2 := ResolvedStep{
		Service: "main",
		Phase:   config.DeployPhase{Name: "setup"},
		Step:    config.DeployStep{Name: "step2", Type: "shell", Cmd: "echo skipped"},
	}
	// Use a when-condition skip (not state-based) so the skip is recorded in the journal.
	// State-based skips intentionally do not overwrite the previous StatusOk entry.
	rec.OnStepSkip(step2.StepAddress(), step2, "hash2", "when: some-condition")

	rec.OnPipelineFinish(true)

	loaded, err := journal.Load(statePath)
	require.NoError(t, err)

	steps := loaded.Services["main"].Phases["setup"].Steps
	assert.Equal(t, journal.StatusSkipped, steps["step2"].Status)
}

// TestFileRecorder_ServiceAllStepsSkipped tests that a service's status is
// correctly set when all its steps are skipped.
func TestFileRecorder_ServiceAllStepsSkipped(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	// Pre-populate state with a previously deployed service
	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services: map[string]*journal.ServiceState{
			"main": {
				Status: journal.StatusDeployed,
				Phases: map[string]*journal.PhaseState{
					"setup": {
						Status: journal.StatusOk,
						Steps: map[string]*journal.StepState{
							"step1": {Status: journal.StatusOk, ActionHash: "hash1", DurationMs: 5},
						},
					},
				},
			},
		},
	}

	rec := NewFileRecorder(statePath, state, map[string]string{"main": "hash"}, "project-hash", true)
	rec.OnPipelineStart("deploy", 1)

	// Only step is skipped
	step1 := ResolvedStep{
		Service: "main",
		Phase:   config.DeployPhase{Name: "setup"},
		Step:    config.DeployStep{Name: "step1", Type: "shell", Cmd: "echo ok"},
	}
	rec.OnStepSkip(step1.StepAddress(), step1, "hash1", "state")

	rec.OnPipelineFinish(true)

	loaded, err := journal.Load(statePath)
	require.NoError(t, err)

	// Service should retain its previous deployed status
	assert.Equal(t, journal.StatusDeployed, loaded.Services["main"].Status)
}

// TestFileRecorder_WhenSkipClearsPriorFailedPhase tests that a non-state skip
// (e.g. when: condition false) recomputes the phase status, so a previously-failed
// phase is cleared when the step is conditionally skipped in a resume run.
func TestFileRecorder_WhenSkipClearsPriorFailedPhase(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	// Pre-populate state as if a previous run left the phase failed.
	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services: map[string]*journal.ServiceState{
			"main": {
				Status: journal.StatusFailed,
				Phases: map[string]*journal.PhaseState{
					"setup": {
						Status: journal.StatusFailed,
						Steps: map[string]*journal.StepState{
							"step1": {Status: journal.StatusFailed, ActionHash: "hash1"},
						},
					},
				},
			},
		},
	}

	rec := NewFileRecorder(statePath, state, map[string]string{"main": "hash"}, "project-hash", true)
	rec.OnPipelineStart("deploy", 1)

	// On resume run the step's when: evaluates to false — it is skipped.
	step1 := ResolvedStep{
		Service: "main",
		Phase:   config.DeployPhase{Name: "setup"},
		Step:    config.DeployStep{Name: "step1", Type: "shell", Cmd: "echo ok"},
	}
	rec.OnStepSkip(step1.StepAddress(), step1, "hash1", "when: env.SKIP==true")

	rec.OnPipelineFinish(true)

	loaded, err := journal.Load(statePath)
	require.NoError(t, err)

	// Phase status must be cleared: no step is failed any more.
	assert.Equal(t, journal.StatusOk, loaded.Services["main"].Phases["setup"].Status)
	// Service must be marked as deployed (OnPipelineFinish saw no failed phases).
	assert.Equal(t, journal.StatusDeployed, loaded.Services["main"].Status)
}

// TestFileRecorder_Recompute tests that the Recompute function correctly
// derives project status from per-service statuses.
func TestFileRecorder_Recompute(t *testing.T) {
	tests := []struct {
		name     string
		services map[string]journal.Status
		expected journal.Status
	}{
		{
			name: "all deployed",
			services: map[string]journal.Status{
				"main": journal.StatusDeployed,
				"db":   journal.StatusDeployed,
			},
			expected: journal.StatusDeployed,
		},
		{
			name: "any failed",
			services: map[string]journal.Status{
				"main": journal.StatusDeployed,
				"db":   journal.StatusFailed,
			},
			expected: journal.StatusFailed,
		},
		{
			name: "mixed deployed and not_deployed",
			services: map[string]journal.Status{
				"main": journal.StatusDeployed,
				"db":   journal.StatusNotDeployed,
			},
			expected: journal.StatusPartial,
		},
		{
			name: "all not deployed",
			services: map[string]journal.Status{
				"main": journal.StatusNotDeployed,
				"db":   journal.StatusNotDeployed,
			},
			expected: journal.StatusNotDeployed,
		},
		{
			name: "partial is failure",
			services: map[string]journal.Status{
				"main": journal.StatusPartial,
				"db":   journal.StatusDeployed,
			},
			expected: journal.StatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &journal.ProjectState{
				SchemaVersion: "1",
				Project:       &journal.ProjectLevelState{},
				Services:      make(map[string]*journal.ServiceState),
			}

			for name, status := range tt.services {
				state.Services[name] = &journal.ServiceState{Status: status}
			}

			journal.Recompute(state)

			assert.Equal(t, tt.expected, state.Project.Status)
		})
	}
}

// TestFileRecorder_AtomicWrites tests that state is written to disk after each step.
func TestFileRecorder_AtomicWrites(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services:      make(map[string]*journal.ServiceState),
	}

	rec := NewFileRecorder(statePath, state, map[string]string{}, "project-hash", true)
	rec.OnPipelineStart("deploy", 2)

	// After first step, file should exist
	step1 := ResolvedStep{
		Service: "main",
		Phase:   config.DeployPhase{Name: "setup"},
		Step:    config.DeployStep{Name: "step1", Type: "shell", Cmd: "echo ok"},
	}
	rec.OnStepStart(step1.StepAddress(), step1, "hash1")
	rec.OnStepFinish(step1.StepAddress(), step1, "hash1", 5)

	// Verify file exists and is readable
	_, err := os.Stat(statePath)
	assert.NoError(t, err)
	loaded1, err := journal.Load(statePath)
	require.NoError(t, err)
	assert.NotNil(t, loaded1.Services["main"])

	// After second step, file should be updated
	step2 := ResolvedStep{
		Service: "db",
		Phase:   config.DeployPhase{Name: "setup"},
		Step:    config.DeployStep{Name: "init", Type: "shell", Cmd: "psql"},
	}
	rec.OnStepStart(step2.StepAddress(), step2, "hash2")
	rec.OnStepFinish(step2.StepAddress(), step2, "hash2", 10)

	loaded2, err := journal.Load(statePath)
	require.NoError(t, err)
	assert.Len(t, loaded2.Services, 2)
	assert.NotNil(t, loaded2.Services["db"])
}

// TestFileRecorder_MultiPhaseService tests a service with multiple phases.
func TestFileRecorder_MultiPhaseService(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services:      make(map[string]*journal.ServiceState),
	}

	rec := NewFileRecorder(statePath, state, map[string]string{"main": "hash"}, "project-hash", true)
	rec.OnPipelineStart("deploy", 4)

	// Execute steps across multiple phases for the same service
	phases := []string{"pre-deploy", "setup", "init", "finalize"}
	stepNames := []string{"validate", "create-dirs", "install-deps", "summary"}

	for i, phaseName := range phases {
		step := ResolvedStep{
			Service: "main",
			Phase:   config.DeployPhase{Name: phaseName},
			Step:    config.DeployStep{Name: stepNames[i], Type: "shell", Cmd: "echo"},
		}
		rec.OnStepStart(step.StepAddress(), step, fmt.Sprintf("hash%d", i))
		rec.OnStepFinish(step.StepAddress(), step, fmt.Sprintf("hash%d", i), int64(10*i))
	}

	rec.OnPipelineFinish(true)

	loaded, err := journal.Load(statePath)
	require.NoError(t, err)

	require.NotNil(t, loaded.Services["main"])
	assert.Len(t, loaded.Services["main"].Phases, 4)

	for i, phaseName := range phases {
		assert.NotNil(t, loaded.Services["main"].Phases[phaseName])
		assert.NotNil(t, loaded.Services["main"].Phases[phaseName].Steps[stepNames[i]])
	}
}

// TestFileRecorder_ProjectLevelOnly tests a deployment with only project-level steps.
func TestFileRecorder_ProjectLevelOnly(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services:      make(map[string]*journal.ServiceState),
	}

	rec := NewFileRecorder(statePath, state, map[string]string{}, "project-hash", true)
	rec.OnPipelineStart("deploy", 2)

	step1 := ResolvedStep{
		Phase: config.DeployPhase{Name: "pre-deploy"},
		Step:  config.DeployStep{Name: "render-env", Type: "shell", Cmd: "echo"},
	}
	rec.OnStepStart(step1.StepAddress(), step1, "hash1")
	rec.OnStepFinish(step1.StepAddress(), step1, "hash1", 5)

	step2 := ResolvedStep{
		Phase: config.DeployPhase{Name: "finalize"},
		Step:  config.DeployStep{Name: "summary", Type: "shell", Cmd: "echo"},
	}
	rec.OnStepStart(step2.StepAddress(), step2, "hash2")
	rec.OnStepFinish(step2.StepAddress(), step2, "hash2", 3)

	rec.OnPipelineFinish(true)

	loaded, err := journal.Load(statePath)
	require.NoError(t, err)

	// Only project-level state should be populated
	assert.NotNil(t, loaded.Project.Phases)
	assert.Len(t, loaded.Project.Phases, 2)
	assert.Len(t, loaded.Services, 0)

	// Verify steps
	assert.NotNil(t, loaded.Project.Phases["pre-deploy"].Steps["render-env"])
	assert.NotNil(t, loaded.Project.Phases["finalize"].Steps["summary"])
}

// TestFileRecorder_ServiceDeployedWithProjectScopeFailure tests that when all
// service steps succeed but a project-scope step fails, the recorded state
// reflects both outcomes: services are deployed AND project.LastRun.Status is
// failed. The deploy command relies on this to avoid falsely treating the
// pipeline as "already up-to-date" on the next run.
func TestFileRecorder_ServiceDeployedWithProjectScopeFailure(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services:      make(map[string]*journal.ServiceState),
	}

	rec := NewFileRecorder(statePath, state, map[string]string{"main": "svc-hash"}, "proj-hash", true)
	rec.OnPipelineStart("deploy", 2)

	// Service step succeeds
	svcStep := ResolvedStep{
		Service: "main",
		Phase:   config.DeployPhase{Name: "setup"},
		Step:    config.DeployStep{Name: "build", Type: "shell", Cmd: "echo ok"},
	}
	rec.OnStepStart(svcStep.StepAddress(), svcStep, "hash1")
	rec.OnStepFinish(svcStep.StepAddress(), svcStep, "hash1", 5)

	// Project-scope step fails after service step
	projStep := ResolvedStep{
		Phase: config.DeployPhase{Name: "finalize"},
		Step:  config.DeployStep{Name: "notify", Type: "shell", Cmd: "exit 1"},
	}
	rec.OnStepStart(projStep.StepAddress(), projStep, "hash2")
	rec.OnStepFail(projStep.StepAddress(), projStep, "hash2", 3, fmt.Errorf("notification failed"))

	rec.OnPipelineFinish(false)

	loaded, err := journal.Load(statePath)
	require.NoError(t, err)

	// Service must be marked deployed — its own steps all succeeded
	require.NotNil(t, loaded.Services["main"])
	assert.Equal(t, journal.StatusDeployed, loaded.Services["main"].Status, "service should be deployed")

	// Project status is derived from services by Recompute — deployed because all services are deployed
	assert.Equal(t, journal.StatusDeployed, loaded.Project.Status, "project.status should be deployed (driven by services)")

	// LastRun must capture the pipeline failure
	require.NotNil(t, loaded.Project.LastRun)
	assert.Equal(t, journal.StatusFailed, loaded.Project.LastRun.Status, "project.last_run.status must be failed")

	// Failed project-scope step must be recorded
	require.NotNil(t, loaded.Project.Phases["finalize"])
	assert.Equal(t, journal.StatusFailed, loaded.Project.Phases["finalize"].Steps["notify"].Status)
}

// TestFileRecorder_TimestampsAreSet tests that timestamps are correctly recorded.
func TestFileRecorder_TimestampsAreSet(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services:      make(map[string]*journal.ServiceState),
	}

	rec := NewFileRecorder(statePath, state, map[string]string{"main": "hash"}, "project-hash", true)

	before := time.Now()
	rec.OnPipelineStart("deploy", 1)

	step := ResolvedStep{
		Service: "main",
		Phase:   config.DeployPhase{Name: "setup"},
		Step:    config.DeployStep{Name: "step1", Type: "shell", Cmd: "echo"},
	}
	rec.OnStepStart(step.StepAddress(), step, "hash1")
	stepBefore := time.Now()
	rec.OnStepFinish(step.StepAddress(), step, "hash1", 5)
	stepAfter := time.Now()

	after := time.Now()
	rec.OnPipelineFinish(true)

	loaded, err := journal.Load(statePath)
	require.NoError(t, err)

	// Check project timestamps
	assert.False(t, loaded.Project.DeployedAt.IsZero())
	assert.True(t, loaded.Project.DeployedAt.After(before) || loaded.Project.DeployedAt.Equal(before))
	assert.True(t, loaded.Project.DeployedAt.Before(after) || loaded.Project.DeployedAt.Equal(after))

	// Check project last run timestamps
	assert.False(t, loaded.Project.LastRun.StartedAt.IsZero())
	assert.False(t, loaded.Project.LastRun.FinishedAt.IsZero())

	// Check service timestamps
	assert.False(t, loaded.Services["main"].DeployedAt.IsZero())
	assert.False(t, loaded.Services["main"].LastRun.StartedAt.IsZero())
	assert.False(t, loaded.Services["main"].LastRun.FinishedAt.IsZero())

	// Check step timestamps
	stepState := loaded.Services["main"].Phases["setup"].Steps["step1"]
	assert.False(t, stepState.FinishedAt.IsZero())
	assert.True(t, stepState.FinishedAt.After(stepBefore) || stepState.FinishedAt.Equal(stepBefore))
	assert.True(t, stepState.FinishedAt.Before(stepAfter) || stepState.FinishedAt.Equal(stepAfter))
}

// TestFileRecorder_ServiceOnlyRunDoesNotStampProjectHash verifies that a
// service-only deploy (--service flag) does not stamp project.config_hash
// even though the implicit env step (Service == "") is always prepended to a
// service plan by ResolveServicePlan. The old projectStepsSeen heuristic was
// fooled by that implicit step; the fix is to pass stampProjectHash=false
// explicitly from deployRunCmd when serviceName != "".
func TestFileRecorder_ServiceOnlyRunDoesNotStampProjectHash(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	// Simulate state left by a previous full deploy: project hash is old-hash.
	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project: &journal.ProjectLevelState{
			ConfigHash: "old-project-hash",
			Status:     journal.StatusDeployed,
		},
		Services: make(map[string]*journal.ServiceState),
	}

	// stampProjectHash=false: caller signals this is a service-only run.
	// The project config hash has changed to new-project-hash but must not be stamped.
	rec := NewFileRecorder(statePath, state, map[string]string{"main": "svc-hash"}, "new-project-hash", false)
	rec.OnPipelineStart("deploy", 2)

	// The implicit env step is always first in a service plan (Service == "").
	// This is what previously triggered projectStepsSeen=true incorrectly.
	envStep := ResolvedStep{
		Phase: config.DeployPhase{Name: "env", Description: "Environment"},
		Step:  config.DeployStep{Name: "render-env", Type: "devbox", Cmd: "render env -o .env"},
	}
	rec.OnStepStart(envStep.StepAddress(), envStep, "env-hash")
	rec.OnStepFinish(envStep.StepAddress(), envStep, "env-hash", 3)

	// Then the service-scope step runs.
	svcStep := ResolvedStep{
		Service: "main",
		Phase:   config.DeployPhase{Name: "setup"},
		Step:    config.DeployStep{Name: "build", Type: "shell", Cmd: "echo ok"},
	}
	rec.OnStepStart(svcStep.StepAddress(), svcStep, "svc-step-hash")
	rec.OnStepFinish(svcStep.StepAddress(), svcStep, "svc-step-hash", 5)

	rec.OnPipelineFinish(true)

	loaded, err := journal.Load(statePath)
	require.NoError(t, err)

	// Project config hash must NOT have been updated even though the implicit
	// env step (project-scoped) ran — stampProjectHash=false prevents it.
	assert.Equal(t, "old-project-hash", loaded.Project.ConfigHash,
		"service-only run must not stamp the project config hash")

	// Service hash should be stamped normally.
	require.NotNil(t, loaded.Services["main"])
	assert.Equal(t, "svc-hash", loaded.Services["main"].ConfigHash)
}

// TestFileRecorder_ServiceOnlyRunPreservesProjectLastRunStatus verifies that a
// successful service-only deploy does not overwrite a prior project.last_run.status
// of "failed". The deploy command's early-exit guard reads LastRun.Status to detect
// incomplete prior runs; clearing it would allow it to falsely skip failed project steps.
func TestFileRecorder_ServiceOnlyRunPreservesProjectLastRunStatus(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	// Simulate state after a full deploy where service steps succeeded but a
	// project-scope step failed: Project.Status=deployed (services all ok),
	// but Project.LastRun.Status=failed (pipeline failed overall).
	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project: &journal.ProjectLevelState{
			ConfigHash: "proj-hash",
			Status:     journal.StatusDeployed,
			LastRun: &journal.LastRun{
				Status: journal.StatusFailed,
			},
			Phases: map[string]*journal.PhaseState{
				"finalize": {
					Status: journal.StatusFailed,
					Steps: map[string]*journal.StepState{
						"notify": {Status: journal.StatusFailed, ActionHash: "hash-notify"},
					},
				},
			},
		},
		Services: make(map[string]*journal.ServiceState),
	}

	// Service-only run: stampProjectHash=false.
	rec := NewFileRecorder(statePath, state, map[string]string{"main": "svc-hash"}, "proj-hash", false)
	rec.OnPipelineStart("deploy", 2)

	// Implicit env step (project-scoped) runs first in a service plan.
	envStep := ResolvedStep{
		Phase: config.DeployPhase{Name: "env"},
		Step:  config.DeployStep{Name: "render-env", Type: "devbox", Cmd: "render env"},
	}
	rec.OnStepStart(envStep.StepAddress(), envStep, "env-hash")
	rec.OnStepFinish(envStep.StepAddress(), envStep, "env-hash", 2)

	// Service step runs and succeeds.
	svcStep := ResolvedStep{
		Service: "main",
		Phase:   config.DeployPhase{Name: "setup"},
		Step:    config.DeployStep{Name: "build", Type: "shell", Cmd: "echo ok"},
	}
	rec.OnStepStart(svcStep.StepAddress(), svcStep, "svc-hash")
	rec.OnStepFinish(svcStep.StepAddress(), svcStep, "svc-hash", 5)

	// Pipeline finishes successfully (all steps in this run passed).
	rec.OnPipelineFinish(true)

	loaded, err := journal.Load(statePath)
	require.NoError(t, err)

	// The project.last_run.status must still be "failed" — the service-only run
	// must not clear the failure marker left by the prior full deploy.
	require.NotNil(t, loaded.Project.LastRun)
	assert.Equal(t, journal.StatusFailed, loaded.Project.LastRun.Status,
		"service-only run must preserve project.last_run.status=failed")

	// Project config hash must also be untouched.
	assert.Equal(t, "proj-hash", loaded.Project.ConfigHash,
		"service-only run must not stamp the project config hash")

	// Service hash is stamped normally.
	require.NotNil(t, loaded.Services["main"])
	assert.Equal(t, "svc-hash", loaded.Services["main"].ConfigHash)
}

// TestFileRecorder_ServiceInProgressOnStepStart verifies that a service's
// LastRun.Status is set to in_progress before the step executes, so a process
// crash mid-deploy is detectable via journal.Recompute().
func TestFileRecorder_ServiceInProgressOnStepStart(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services:      make(map[string]*journal.ServiceState),
	}

	rec := NewFileRecorder(statePath, state, map[string]string{"main": "svc-hash"}, "proj-hash", true)
	rec.OnPipelineStart("deploy", 1)

	step := ResolvedStep{
		Service: "main",
		Phase:   config.DeployPhase{Name: "setup"},
		Step:    config.DeployStep{Name: "step1", Type: "shell", Cmd: "echo"},
	}

	// OnStepStart must flush in_progress to disk before the step runs.
	rec.OnStepStart(step.StepAddress(), step, "hash1")

	// Simulate a crash: read state from disk without calling OnPipelineFinish.
	crashed, err := journal.Load(statePath)
	require.NoError(t, err)
	require.NotNil(t, crashed.Services["main"])
	require.NotNil(t, crashed.Services["main"].LastRun)
	assert.Equal(t, journal.StatusInProgress, crashed.Services["main"].LastRun.Status,
		"service LastRun.Status must be in_progress after OnStepStart so crashes are detectable")

	// After Recompute, the in_progress status is converted to failed.
	journal.Recompute(crashed)
	assert.Equal(t, journal.StatusFailed, crashed.Services["main"].LastRun.Status,
		"Recompute must convert in_progress → failed for crashed services")
}

// TestFileRecorder_StepHashIncludesFilesGate verifies that a gated step's recorded hash includes
// the FilesGate directive, so that changes to the files_gate directive are reflected in the state.
func TestFileRecorder_StepHashIncludesFilesGate(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services:      make(map[string]*journal.ServiceState),
	}

	rec := NewFileRecorder(statePath, state, map[string]string{}, "proj-hash", true)
	rec.OnPipelineStart("deploy", 1)

	// Step with files_gate: readable
	stepWithGate := config.DeployStep{
		Name: "step1",
		Type: "command",
		Cmd:  "my-cmd",
		FilesGate: &filesgate.FilesGate{
			State:   filesgate.StateReadable,
			Require: filesgate.RequireRequired{},
		},
	}

	resolved := ResolvedStep{
		Phase: config.DeployPhase{Name: "setup"},
		Step:  stepWithGate,
	}

	// Compute the hash as the executor does
	stepHash := journal.StepHash(stepWithGate)

	rec.OnStepStart(resolved.StepAddress(), resolved, stepHash)
	rec.OnStepFinish(resolved.StepAddress(), resolved, stepHash, 100)
	rec.OnPipelineFinish(true)

	// Verify the recorded hash includes FilesGate
	loaded, err := journal.Load(statePath)
	require.NoError(t, err)
	require.NotNil(t, loaded.Project.Phases["setup"])
	require.NotNil(t, loaded.Project.Phases["setup"].Steps["step1"])

	recordedHash := loaded.Project.Phases["setup"].Steps["step1"].ActionHash
	assert.Equal(t, stepHash, recordedHash, "recorded hash should equal StepHash (including FilesGate)")

	// Verify that the hash differs from ActionHash(step.Action()) since FilesGate is present
	actionHash := journal.ActionHash(stepWithGate.Action())
	assert.NotEqual(t, actionHash, recordedHash, "with FilesGate present, StepHash should differ from ActionHash")
}

// TestFileRecorder_GatelessStepHashEqualsActionHash verifies backwards compatibility:
// when FilesGate is nil, the recorded hash (StepHash) equals ActionHash for gateless steps.
func TestFileRecorder_GatelessStepHashEqualsActionHash(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services:      make(map[string]*journal.ServiceState),
	}

	rec := NewFileRecorder(statePath, state, map[string]string{}, "proj-hash", true)
	rec.OnPipelineStart("deploy", 1)

	// Step WITHOUT files_gate
	gatelessStep := config.DeployStep{
		Name: "step1",
		Type: "command",
		Cmd:  "my-cmd",
		With: map[string]any{"param": "value"},
	}

	resolved := ResolvedStep{
		Phase: config.DeployPhase{Name: "setup"},
		Step:  gatelessStep,
	}

	// Both ActionHash and StepHash should be the same for gateless steps
	actionHash := journal.ActionHash(gatelessStep.Action())
	stepHash := journal.StepHash(gatelessStep)
	assert.Equal(t, actionHash, stepHash, "StepHash should equal ActionHash when FilesGate is nil")

	rec.OnStepStart(resolved.StepAddress(), resolved, stepHash)
	rec.OnStepFinish(resolved.StepAddress(), resolved, stepHash, 50)
	rec.OnPipelineFinish(true)

	// Verify the recorded hash matches ActionHash
	loaded, err := journal.Load(statePath)
	require.NoError(t, err)
	require.NotNil(t, loaded.Project.Phases["setup"])
	require.NotNil(t, loaded.Project.Phases["setup"].Steps["step1"])

	recordedHash := loaded.Project.Phases["setup"].Steps["step1"].ActionHash
	assert.Equal(t, actionHash, recordedHash, "recorded hash for gateless step should equal ActionHash")
}

// TestFileRecorder_ConcurrentStepEvents exercises the recorder under the
// access pattern the parallel-group executor will use: many goroutines each
// recording one OnStepStart + OnStepFinish for a distinct step. Without the
// mutex this would race on map writes and lose entries when journal.Save
// re-marshals a half-mutated state. Run with `go test -race`.
func TestFileRecorder_ConcurrentStepEvents(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.yml")

	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services:      make(map[string]*journal.ServiceState),
	}
	rec := NewFileRecorder(statePath, state, map[string]string{}, "project-hash", true)
	rec.OnPipelineStart("deploy", 32)

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			step := ResolvedStep{
				Phase: config.DeployPhase{Name: "setup"},
				Step:  config.DeployStep{Name: fmt.Sprintf("step-%02d", i), Type: "shell", Cmd: "true"},
			}
			hash := fmt.Sprintf("hash-%02d", i)
			rec.OnStepStart(step.StepAddress(), step, hash)
			rec.OnStepFinish(step.StepAddress(), step, hash, int64(i))
		}()
	}
	wg.Wait()

	rec.OnPipelineFinish(true)
	require.NoError(t, rec.Err())

	loaded, err := journal.Load(statePath)
	require.NoError(t, err)
	require.NotNil(t, loaded.Project)
	require.NotNil(t, loaded.Project.Phases["setup"])
	steps := loaded.Project.Phases["setup"].Steps
	require.Len(t, steps, n, "all %d concurrent step writes must survive to disk", n)
	for i := range n {
		name := fmt.Sprintf("step-%02d", i)
		require.NotNil(t, steps[name], "missing recorded step %q", name)
		assert.Equal(t, journal.StatusOk, steps[name].Status)
		assert.Equal(t, fmt.Sprintf("hash-%02d", i), steps[name].ActionHash)
	}
}
