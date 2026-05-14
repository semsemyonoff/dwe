package pipeline

import (
	"fmt"
	"time"

	"devbox-cli/internal/deploy/journal"
)

// FileRecorder records step execution to a state file for idempotent re-runs.
// It accumulates results in memory and flushes to disk after each step and at pipeline finish.
//
// A single FileRecorder instance routes all steps (project-level and service-scoped)
// to the correct subtree in the state using ResolvedStep.Service and ResolvedStep.Phase.Name.
// Project-level steps (Service == "") update project.phases.<name>.steps.<step>,
// while service steps update services.<service>.phases.<name>.steps.<step>.
type FileRecorder struct {
	statePath             string
	state                 *journal.ProjectState
	serviceConfigHashes   map[string]string
	projectConfigHash     string
	servicesSeenInThisRun map[string]bool
	stampProjectHash      bool
	pipelineStartTime     time.Time
	flushErr              error
}

// Err returns the last flush error encountered during recording, if any.
// Callers should check this after RunWithOptions succeeds to detect cases where
// steps ran successfully but state was not persisted to disk.
func (r *FileRecorder) Err() error {
	return r.flushErr
}

// NewFileRecorder constructs a FileRecorder that will record state to the given path.
// It accepts pre-computed config hashes that will be stamped on OnPipelineFinish.
//
// statePath is the path to .devbox/deploy/state.yml.
// state is the loaded ProjectState (or a new one if file was absent).
// serviceConfigHashes maps service names to their current journal.ServiceConfigHash.
// projectConfigHash is the current journal.ProjectConfigHash.
// stampProjectHash controls whether OnPipelineFinish stamps the project config hash.
// Pass true only for full project deploys (serviceName == ""); a service-only run
// includes the implicit env step (Service == "") but must not stamp the project hash
// because the actual project deploy steps did not execute.
func NewFileRecorder(
	statePath string,
	state *journal.ProjectState,
	serviceConfigHashes map[string]string,
	projectConfigHash string,
	stampProjectHash bool,
) *FileRecorder {
	return &FileRecorder{
		statePath:             statePath,
		state:                 state,
		serviceConfigHashes:   serviceConfigHashes,
		projectConfigHash:     projectConfigHash,
		servicesSeenInThisRun: make(map[string]bool),
		stampProjectHash:      stampProjectHash,
	}
}

// OnPipelineStart is called once before any steps execute.
func (r *FileRecorder) OnPipelineStart(name string, totalSteps int) {
	r.pipelineStartTime = time.Now()

	if r.state.Project == nil {
		r.state.Project = &journal.ProjectLevelState{}
	}
	if r.state.Project.LastRun == nil {
		r.state.Project.LastRun = &journal.LastRun{}
	}
	if r.state.Project.Phases == nil {
		r.state.Project.Phases = make(map[string]*journal.PhaseState)
	}

	// Only update project.LastRun for full deploys. A service-only run
	// (stampProjectHash == false) must not overwrite the project's prior
	// last_run status; doing so would clear a failure marker and allow
	// subsequent full deploys to exit "already up-to-date" incorrectly.
	if r.stampProjectHash {
		r.state.Project.LastRun.StartedAt = r.pipelineStartTime
		r.state.Project.LastRun.Status = journal.StatusInProgress
	}

	if r.state.Services == nil {
		r.state.Services = make(map[string]*journal.ServiceState)
	}
}

// OnStepStart is called immediately before a step executes.
func (r *FileRecorder) OnStepStart(addr string, rs ResolvedStep, actionHash string) {
	// Initialize phase if not present
	if rs.Service == "" {
		// Project-scope step
		if r.state.Project == nil {
			r.state.Project = &journal.ProjectLevelState{}
		}
		if r.state.Project.Phases == nil {
			r.state.Project.Phases = make(map[string]*journal.PhaseState)
		}
		if r.state.Project.Phases[rs.Phase.Name] == nil {
			r.state.Project.Phases[rs.Phase.Name] = &journal.PhaseState{
				Steps: make(map[string]*journal.StepState),
			}
		}
		if r.state.Project.Phases[rs.Phase.Name].Steps == nil {
			r.state.Project.Phases[rs.Phase.Name].Steps = make(map[string]*journal.StepState)
		}
		// Note: we don't mark the step's status here; that happens in Finish/Fail
	} else {
		// Service-scope step
		if r.state.Services[rs.Service] == nil {
			r.state.Services[rs.Service] = &journal.ServiceState{
				Phases:  make(map[string]*journal.PhaseState),
				LastRun: &journal.LastRun{},
			}
		}
		if r.state.Services[rs.Service].Phases == nil {
			r.state.Services[rs.Service].Phases = make(map[string]*journal.PhaseState)
		}
		if r.state.Services[rs.Service].Phases[rs.Phase.Name] == nil {
			r.state.Services[rs.Service].Phases[rs.Phase.Name] = &journal.PhaseState{
				Steps: make(map[string]*journal.StepState),
			}
		}
		if r.state.Services[rs.Service].Phases[rs.Phase.Name].Steps == nil {
			r.state.Services[rs.Service].Phases[rs.Phase.Name].Steps = make(map[string]*journal.StepState)
		}

		// Mark the service in_progress before executing the first actual step so that a
		// process crash before OnPipelineFinish is detectable via Recompute().
		// We use LastRun.Status as the sentinel rather than servicesSeenInThisRun because
		// OnStepSkip also sets servicesSeenInThisRun (for condition-based skips) — we only
		// want to mark in_progress when a step actually starts executing.
		svc := r.state.Services[rs.Service]
		if svc.LastRun == nil {
			svc.LastRun = &journal.LastRun{}
		}
		if svc.LastRun.Status != journal.StatusInProgress {
			if svc.LastRun.StartedAt.IsZero() {
				svc.LastRun.StartedAt = r.pipelineStartTime
			}
			svc.LastRun.Status = journal.StatusInProgress
			if err := r.flush(); err != nil {
				r.flushErr = err
			}
		}

		// Track that we've seen this service so its config hash is stamped in OnPipelineFinish
		r.servicesSeenInThisRun[rs.Service] = true
	}
}

// OnStepFinish is called after a step completes successfully.
func (r *FileRecorder) OnStepFinish(addr string, rs ResolvedStep, actionHash string, durationMs int64) {
	now := time.Now()
	stepState := &journal.StepState{
		Status:     journal.StatusOk,
		FinishedAt: now,
		ActionHash: actionHash,
		DurationMs: durationMs,
	}

	if rs.Service == "" {
		// Project-scope step
		r.state.Project.Phases[rs.Phase.Name].Steps[rs.Step.Name] = stepState
		// Recompute phase status from all steps so a resume run can clear a previously
		// failed phase once all its steps succeed.
		r.state.Project.Phases[rs.Phase.Name].Status = phaseStatusFromSteps(r.state.Project.Phases[rs.Phase.Name].Steps)
	} else {
		// Service-scope step
		r.state.Services[rs.Service].Phases[rs.Phase.Name].Steps[rs.Step.Name] = stepState
		r.state.Services[rs.Service].Phases[rs.Phase.Name].Status = phaseStatusFromSteps(r.state.Services[rs.Service].Phases[rs.Phase.Name].Steps)
	}

	// Flush to disk immediately
	if err := r.flush(); err != nil {
		r.flushErr = err
	}
}

// OnStepFail is called when a step returns an error.
func (r *FileRecorder) OnStepFail(addr string, rs ResolvedStep, actionHash string, durationMs int64, err error) {
	now := time.Now()
	stepState := &journal.StepState{
		Status:     journal.StatusFailed,
		FinishedAt: now,
		ActionHash: actionHash,
		DurationMs: durationMs,
	}

	if rs.Service == "" {
		// Project-scope step
		r.state.Project.Phases[rs.Phase.Name].Steps[rs.Step.Name] = stepState
		r.state.Project.Phases[rs.Phase.Name].Status = journal.StatusFailed
	} else {
		// Service-scope step
		r.state.Services[rs.Service].Phases[rs.Phase.Name].Steps[rs.Step.Name] = stepState
		r.state.Services[rs.Service].Phases[rs.Phase.Name].Status = journal.StatusFailed
	}

	// Flush to disk immediately
	if err := r.flush(); err != nil {
		r.flushErr = err
	}
}

// OnStepSkip is called when a step is skipped.
func (r *FileRecorder) OnStepSkip(addr string, rs ResolvedStep, actionHash string, reason string) {
	// For state-based skips, do not overwrite the existing StatusOk journal entry.
	// Overwriting with StatusSkipped would cause Decide() to return Run on the next
	// deploy (prev.Status != StatusOk), creating an alternating run/skip cycle.
	if reason == "state" {
		return
	}

	now := time.Now()
	stepState := &journal.StepState{
		Status:     journal.StatusSkipped,
		FinishedAt: now,
		ActionHash: actionHash,
		DurationMs: 0,
	}

	if rs.Service == "" {
		// Project-scope step — initialize maps if not yet set up by OnStepStart
		if r.state.Project == nil {
			r.state.Project = &journal.ProjectLevelState{}
		}
		if r.state.Project.Phases == nil {
			r.state.Project.Phases = make(map[string]*journal.PhaseState)
		}
		if r.state.Project.Phases[rs.Phase.Name] == nil {
			r.state.Project.Phases[rs.Phase.Name] = &journal.PhaseState{
				Steps: make(map[string]*journal.StepState),
			}
		}
		if r.state.Project.Phases[rs.Phase.Name].Steps == nil {
			r.state.Project.Phases[rs.Phase.Name].Steps = make(map[string]*journal.StepState)
		}
		r.state.Project.Phases[rs.Phase.Name].Steps[rs.Step.Name] = stepState
		// Recompute phase status so a previously-failed phase is cleared when all
		// its steps either succeeded or were skipped in this run.
		r.state.Project.Phases[rs.Phase.Name].Status = phaseStatusFromSteps(r.state.Project.Phases[rs.Phase.Name].Steps)
	} else {
		// Service-scope step — initialize maps if not yet set up by OnStepStart
		if r.state.Services[rs.Service] == nil {
			r.state.Services[rs.Service] = &journal.ServiceState{
				Phases:  make(map[string]*journal.PhaseState),
				LastRun: &journal.LastRun{},
			}
		}
		if r.state.Services[rs.Service].Phases == nil {
			r.state.Services[rs.Service].Phases = make(map[string]*journal.PhaseState)
		}
		if r.state.Services[rs.Service].Phases[rs.Phase.Name] == nil {
			r.state.Services[rs.Service].Phases[rs.Phase.Name] = &journal.PhaseState{
				Steps: make(map[string]*journal.StepState),
			}
		}
		if r.state.Services[rs.Service].Phases[rs.Phase.Name].Steps == nil {
			r.state.Services[rs.Service].Phases[rs.Phase.Name].Steps = make(map[string]*journal.StepState)
		}
		r.state.Services[rs.Service].Phases[rs.Phase.Name].Steps[rs.Step.Name] = stepState
		// Recompute phase status so a previously-failed phase is cleared when all
		// its steps either succeeded or were skipped in this run.
		r.state.Services[rs.Service].Phases[rs.Phase.Name].Status = phaseStatusFromSteps(r.state.Services[rs.Service].Phases[rs.Phase.Name].Steps)
		// Track that we've seen this service so its config hash is stamped in OnPipelineFinish
		r.servicesSeenInThisRun[rs.Service] = true
	}

	// Flush to disk immediately
	if err := r.flush(); err != nil {
		r.flushErr = err
	}
}

// OnPipelineFinish is called once after all steps complete or the first step fails.
// It stamps the config hashes and derives status aggregates.
func (r *FileRecorder) OnPipelineFinish(success bool) {
	now := time.Now()

	// Stamp project config hash and last run status
	if r.state.Project == nil {
		r.state.Project = &journal.ProjectLevelState{}
	}
	if r.state.Project.LastRun == nil {
		r.state.Project.LastRun = &journal.LastRun{
			StartedAt: r.pipelineStartTime,
		}
	}

	// Only stamp project config hash for full project deploys. A --service-only
	// run includes the implicit env step (Service == "") but must not stamp the
	// project hash; the actual project deploy steps did not execute, and
	// stamping here would allow a subsequent full deploy to exit
	// "already up-to-date" and skip any changed project steps permanently.
	if r.stampProjectHash {
		r.state.Project.ConfigHash = r.projectConfigHash
	}
	// Only update project.LastRun and DeployedAt for full deploys.
	// A service-only run (stampProjectHash == false) must not overwrite the
	// project's failure marker; the early-exit guard in the deploy command
	// reads LastRun.Status to detect incomplete prior runs.
	if r.stampProjectHash {
		r.state.Project.LastRun.FinishedAt = now
		if success {
			r.state.Project.LastRun.Status = journal.StatusOk
			r.state.Project.DeployedAt = now
		} else {
			r.state.Project.LastRun.Status = journal.StatusFailed
		}
	}

	// Stamp service config hashes and compute service statuses
	for serviceName := range r.servicesSeenInThisRun {
		if r.state.Services[serviceName] == nil {
			r.state.Services[serviceName] = &journal.ServiceState{
				Phases: make(map[string]*journal.PhaseState),
			}
		}
		svcState := r.state.Services[serviceName]
		svcState.ConfigHash = r.serviceConfigHashes[serviceName]

		if svcState.LastRun == nil {
			svcState.LastRun = &journal.LastRun{
				StartedAt: r.pipelineStartTime,
			}
		} else if svcState.LastRun.StartedAt.IsZero() {
			// Fallback: set StartedAt if not already set (e.g., service seen only via OnStepSkip).
			svcState.LastRun.StartedAt = r.pipelineStartTime
		}
		svcState.LastRun.FinishedAt = now

		// Derive per-service status from its own phase outcomes, not from the
		// global pipeline success flag. A service whose steps all passed is
		// deployed even if a project-scope step later fails the overall pipeline.
		svcDeployed := true
		for _, phase := range svcState.Phases {
			if phase.Status == journal.StatusFailed {
				svcDeployed = false
				break
			}
		}
		if svcDeployed {
			svcState.LastRun.Status = journal.StatusOk
			svcState.Status = journal.StatusDeployed
			svcState.DeployedAt = now
		} else {
			svcState.LastRun.Status = journal.StatusFailed
			svcState.Status = journal.StatusFailed
		}
	}

	// Recompute project-level aggregates (status only, not hashes)
	journal.Recompute(r.state)

	// Final flush to disk
	if err := r.flush(); err != nil {
		r.flushErr = err
	}
}

// phaseStatusFromSteps derives phase status from all current step states.
// A phase is failed if any step failed; otherwise ok.
// This is used by OnStepFinish to correctly handle resume runs where a
// previously failed phase can be cleared once all its steps succeed.
func phaseStatusFromSteps(steps map[string]*journal.StepState) journal.Status {
	for _, s := range steps {
		if s.Status == journal.StatusFailed {
			return journal.StatusFailed
		}
	}
	return journal.StatusOk
}

// flush writes the current state to disk.
func (r *FileRecorder) flush() error {
	if r.statePath == "" {
		return fmt.Errorf("no state path configured")
	}
	return journal.Save(r.statePath, r.state)
}
