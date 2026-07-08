package deploy

import (
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/filesgate"
	"github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
)

func shellStep(name string) pipeline.ResolvedStep {
	return pipeline.ResolvedStep{Step: config.DeployStep{Name: name, Type: "shell", Cmd: "true"}}
}

func predicateStep(name string) pipeline.ResolvedStep {
	return pipeline.ResolvedStep{Step: config.DeployStep{
		Name: name,
		Type: "builtin",
		Cmd:  "file_exists",
		With: map[string]any{"path": "x"},
	}}
}

func TestHasAlwaysRunSteps(t *testing.T) {
	checkStep := shellStep("with-check")
	checkStep.Step.Check = &config.Action{Type: "builtin", Cmd: "file_exists", With: map[string]any{"path": "x"}}

	gateStep := shellStep("with-gate")
	gateStep.FilesGate = &filesgate.FilesGate{State: filesgate.StateMissing}

	gateSub := shellStep("gated-sub")
	gateSub.FilesGate = &filesgate.FilesGate{State: filesgate.StateMissing}

	tests := []struct {
		name  string
		steps []pipeline.ResolvedStep
		want  bool
	}{
		{
			name:  "plain action steps are early-gated",
			steps: []pipeline.ResolvedStep{shellStep("a"), shellStep("b")},
			want:  false,
		},
		{
			name:  "predicate body defeats the early gate",
			steps: []pipeline.ResolvedStep{shellStep("a"), predicateStep("assert")},
			want:  true,
		},
		{
			name:  "check step defeats the early gate",
			steps: []pipeline.ResolvedStep{checkStep},
			want:  true,
		},
		{
			name:  "files_gate step defeats the early gate",
			steps: []pipeline.ResolvedStep{gateStep},
			want:  true,
		},
		{
			name: "predicate body inside a parallel group defeats the early gate",
			steps: []pipeline.ResolvedStep{{
				Step:     config.DeployStep{Name: "group"},
				Parallel: &pipeline.ResolvedParallel{Steps: []pipeline.ResolvedStep{shellStep("a"), predicateStep("assert")}},
			}},
			want: true,
		},
		{
			name: "files_gate on a parallel substep defeats the early gate",
			steps: []pipeline.ResolvedStep{{
				Step:     config.DeployStep{Name: "group"},
				Parallel: &pipeline.ResolvedParallel{Steps: []pipeline.ResolvedStep{gateSub}},
			}},
			want: true,
		},
		{
			name:  "empty pipeline is early-gated",
			steps: nil,
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasAlwaysRunSteps(tt.steps); got != tt.want {
				t.Errorf("hasAlwaysRunSteps() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestMakeSkipDecider_PredicateBodyRerunsOnSecondDeploy pins the assertion
// contract: a journaled predicate-body step with status ok and a matching
// action hash still Runs on the next deploy (the StepForcesRun lever), while
// an equivalent plain action step Skips.
func TestMakeSkipDecider_PredicateBodyRerunsOnSecondDeploy(t *testing.T) {
	const (
		projectHash = "ph1"
		actionHash  = "ah1"
		phaseName   = "verify"
	)

	journaled := func(stepName string) *journal.ProjectState {
		return &journal.ProjectState{
			SchemaVersion: "1",
			Project: &journal.ProjectLevelState{
				ConfigHash: projectHash,
				Status:     journal.StatusDeployed,
				Phases: map[string]*journal.PhaseState{
					phaseName: {
						Status: journal.StatusOk,
						Steps: map[string]*journal.StepState{
							stepName: {Status: journal.StatusOk, ActionHash: actionHash},
						},
					},
				},
			},
		}
	}

	predicate := predicateStep("assert")
	predicate.Phase = config.DeployPhase{Name: phaseName}

	action := shellStep("build")
	action.Phase = config.DeployPhase{Name: phaseName}

	tests := []struct {
		name string
		rs   pipeline.ResolvedStep
		want journal.Decision
	}{
		{"journaled predicate body re-runs", predicate, journal.Run},
		{"journaled action step skips", action, journal.Skip},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decider := makeSkipDecider(Opts{}, journaled(tt.rs.Step.Name), projectHash, nil)
			if got := decider(tt.rs.StepAddress(), tt.rs, actionHash); got != tt.want {
				t.Errorf("skip decider = %v, want %v", got, tt.want)
			}
		})
	}
}
