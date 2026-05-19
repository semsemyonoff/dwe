package pipeline

import "devbox-cli/internal/deploy/journal"

// Recorder receives lifecycle events from the deploy/reset pipeline executor
// and records them for state tracking and idempotency decisions.
//
// Implementations record step execution status, hashes, and timings to enable
// idempotent re-runs where previously-completed steps can be skipped.
//
// Implementations MUST be safe to call from multiple goroutines: the
// parallel-group executor invokes OnStepStart / OnStepFinish / OnStepFail /
// OnStepSkip concurrently, one call per in-flight sub-step.
type Recorder interface {
	// OnPipelineStart is called once before any steps execute.
	// name is the pipeline name (e.g. "deploy", "reset").
	// totalSteps is the number of steps in the resolved plan.
	OnPipelineStart(name string, totalSteps int)

	// OnStepStart is called immediately before a step executes.
	// addr is the full step address (e.g. "pre-deploy/render-env" or "main/setup/create-dirs").
	// rs carries the resolved step, service, and phase information.
	// actionHash is the sha256 hash of the step body (type, cmd, with) — computed once
	// before the step and passed to all of the step's lifecycle events.
	OnStepStart(addr string, rs ResolvedStep, actionHash string)

	// OnStepFinish is called after a step completes successfully.
	// durationMs is the step execution time in milliseconds.
	OnStepFinish(addr string, rs ResolvedStep, actionHash string, durationMs int64)

	// OnStepFail is called when a step returns an error.
	// durationMs is the elapsed time before the failure.
	// err is the error that caused the step to fail.
	OnStepFail(addr string, rs ResolvedStep, actionHash string, durationMs int64, err error)

	// OnStepSkip is called when a step is skipped (either via state decision or when condition).
	// reason is a human-readable explanation (e.g. "state", "when: ...", "phase when: ...").
	OnStepSkip(addr string, rs ResolvedStep, actionHash string, reason string)

	// OnPipelineFinish is called once after all steps complete or the first step fails.
	// success is true only if the pipeline completed without step failures.
	OnPipelineFinish(success bool)
}

// NopRecorder is a no-op Recorder implementation for tests and pipelines that
// do not track state. All methods are trivially safe for concurrent use.
type NopRecorder struct{}

// OnPipelineStart is a no-op.
func (NopRecorder) OnPipelineStart(name string, totalSteps int) {}

// OnStepStart is a no-op.
func (NopRecorder) OnStepStart(addr string, rs ResolvedStep, actionHash string) {}

// OnStepFinish is a no-op.
func (NopRecorder) OnStepFinish(addr string, rs ResolvedStep, actionHash string, durationMs int64) {}

// OnStepFail is a no-op.
func (NopRecorder) OnStepFail(addr string, rs ResolvedStep, actionHash string, durationMs int64, err error) {
}

// OnStepSkip is a no-op.
func (NopRecorder) OnStepSkip(addr string, rs ResolvedStep, actionHash string, reason string) {}

// OnPipelineFinish is a no-op.
func (NopRecorder) OnPipelineFinish(success bool) {}

// SkipDecider returns whether a step should be skipped based on its current state.
// It is consulted after when conditions have been evaluated but before the step executes.
//
// The decision may be based on:
// - Previous step execution state (from state.yml)
// - Action hash (whether the step body has changed)
// - Config hashes (whether the service or project configuration has changed)
// - Presence of a check condition (always returns Run when a check is present)
//
// Callers build this closure to apply project/service scope-aware config-hash
// invalidation: if configuration has changed at the scope level (project or service),
// the closure treats all steps at that scope as absent, forcing them to Run.
type SkipDecider func(addr string, rs ResolvedStep, actionHash string) journal.Decision
