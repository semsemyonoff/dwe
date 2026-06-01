package pipeline

import (
	"errors"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
)

// A pipeline parallel sub-step that resolves to a workflow with `parallel:`
// must fail with ErrWorkflowNestedParallel — the executor sets
// rctx.UnderParallel = actx.Parallel before RunCommand, the workflow runner
// detects the nested parallel block, and the sentinel propagates back through
// the executor's error path.
func TestRunPipeline_ParallelSubStep_NestedWorkflowParallel_Rejected(t *testing.T) {
	reg := usercommands.NewEmptyRegistry()

	leaf := &usercommands.CommandDef{
		ID:   "leaf",
		Type: usercommands.CommandTypeShell,
		Cmd:  "true",
	}
	innerWF := &usercommands.CommandDef{
		ID:   "inner.wf",
		Type: usercommands.CommandTypeWorkflow,
		Steps: []usercommands.WorkflowStep{
			{Parallel: &usercommands.WorkflowParallel{
				Steps: []usercommands.WorkflowStep{
					{Command: "leaf"},
					{Command: "leaf"},
				},
			}},
		},
	}
	reg.AddCommandForTest(leaf)
	reg.AddCommandForTest(innerWF)

	cfg := &config.DweConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "p"}
	group := buildParallelGroupStep(phase, "g", false, 0, []config.DeployStep{
		{Name: "call-wf", Type: "command", Cmd: "inner.wf"},
		{Name: "noop", Type: "shell", Cmd: "true"},
	})

	rep := &mockReporter{}
	err := RunWithOptions(RunOptions{
		Steps:       []ResolvedStep{group},
		Reporter:    rep,
		Name:        "test",
		Config:      cfg,
		Registry:    reg,
		WorkDir:     t.TempDir(),
		SkipConfirm: true,
		Recorder:    &NopRecorder{},
		SkipDecider: func(addr string, rs ResolvedStep, h string) journal.Decision { return journal.Run },
	})
	if err == nil {
		t.Fatal("expected nested-parallel error from pipeline parallel → workflow-parallel")
	}
	// The pipeline executor masks step errors as ErrSilent in the return value
	// (see internal/core/execution/pipeline/errors.go). The original sentinel is captured in
	// the FailStep reporter event.
	var failErr error
	for _, e := range rep.events {
		if e.kind == "FailStep" && e.stepAddr == "p/call-wf" {
			failErr = e.err
			break
		}
	}
	if failErr == nil {
		t.Fatalf("expected FailStep event for p/call-wf carrying the sentinel; got events: %+v", rep.events)
	}
	if !errors.Is(failErr, runtime.ErrWorkflowNestedParallel) {
		t.Errorf("FailStep err = %v; expected ErrWorkflowNestedParallel", failErr)
	}
}

// A pipeline SEQUENTIAL step referencing a workflow-with-parallel must run
// successfully — UnderParallel is false on sequential pipeline steps, so the
// workflow's nested-parallel guard does NOT fire.
func TestRunPipeline_SequentialStep_WorkflowWithParallel_Runs(t *testing.T) {
	reg := usercommands.NewEmptyRegistry()

	leaf := &usercommands.CommandDef{
		ID:   "leaf",
		Type: usercommands.CommandTypeShell,
		Cmd:  "true",
	}
	wf := &usercommands.CommandDef{
		ID:   "wf.with-parallel",
		Type: usercommands.CommandTypeWorkflow,
		Steps: []usercommands.WorkflowStep{
			{Parallel: &usercommands.WorkflowParallel{
				Steps: []usercommands.WorkflowStep{
					{Command: "leaf"},
					{Command: "leaf"},
				},
			}},
		},
	}
	reg.AddCommandForTest(leaf)
	reg.AddCommandForTest(wf)

	cfg := &config.DweConfig{Raw: map[string]any{}}
	phase := config.DeployPhase{Name: "p"}
	steps := []ResolvedStep{
		{
			Phase: phase,
			Step:  config.DeployStep{Name: "call-wf", Type: "command", Cmd: "wf.with-parallel"},
		},
	}

	rep := &mockReporter{}
	err := RunWithOptions(RunOptions{
		Steps:       steps,
		Reporter:    rep,
		Name:        "test",
		Config:      cfg,
		Registry:    reg,
		WorkDir:     t.TempDir(),
		SkipConfirm: true,
		Recorder:    &NopRecorder{},
		SkipDecider: func(addr string, rs ResolvedStep, h string) journal.Decision { return journal.Run },
	})
	if err != nil {
		t.Fatalf("sequential pipeline step → workflow-with-parallel must succeed; got %v", err)
	}

	finishCount := 0
	for _, e := range rep.events {
		if e.kind == "FinishStep" && !strings.Contains(e.name, "wait") {
			finishCount++
		}
	}
	if finishCount == 0 {
		t.Errorf("expected at least one FinishStep; events: %+v", rep.events)
	}
}
