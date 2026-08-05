package pipeline

import (
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
	"github.com/semsemyonoff/dwe/internal/core/execution/filesgate"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
)

func newParallelStep(name string, max int, failFast *bool, subs ...config.DeployStep) config.DeployStep {
	return config.DeployStep{
		Name: name,
		Parallel: &config.ParallelGroup{
			MaxConcurrent: max,
			FailFast:      failFast,
			Steps:         subs,
		},
	}
}

func TestResolvePhaseSteps_parallelHappyPath(t *testing.T) {
	cfg := &config.DweConfig{}
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			newParallelStep("group", 0, nil,
				config.DeployStep{Name: "a", Type: "shell", Cmd: "echo a"},
				config.DeployStep{Name: "b", Type: "shell", Cmd: "echo b"},
			),
		},
	}
	resolved, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolved) != 1 || resolved[0].Parallel == nil {
		t.Fatalf("expected 1 parallel step, got %+v", resolved)
	}
	rp := resolved[0].Parallel
	if len(rp.Steps) != 2 {
		t.Fatalf("expected 2 sub-steps, got %d", len(rp.Steps))
	}
	// Default MaxConcurrent: min(NumCPU, len)
	want := min(runtime.NumCPU(), 2)
	if rp.MaxConcurrent != want {
		t.Errorf("MaxConcurrent default = %d, want %d", rp.MaxConcurrent, want)
	}
	if !rp.FailFast {
		t.Errorf("FailFast default should be true")
	}
}

func TestResolvePhaseSteps_parallelExplicitDefaults(t *testing.T) {
	cfg := &config.DweConfig{}
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			newParallelStep("group", 99, new(false),
				config.DeployStep{Name: "a", Type: "shell", Cmd: "echo a"},
				config.DeployStep{Name: "b", Type: "shell", Cmd: "echo b"},
			),
		},
	}
	resolved, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rp := resolved[0].Parallel
	if rp.MaxConcurrent != 2 {
		t.Errorf("MaxConcurrent cap = %d, want 2", rp.MaxConcurrent)
	}
	if rp.FailFast {
		t.Errorf("FailFast should be false (explicit)")
	}
}

func TestResolvePhaseSteps_rejectsNestedParallel(t *testing.T) {
	cfg := &config.DweConfig{}
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			newParallelStep("outer", 0, nil,
				config.DeployStep{Name: "leaf", Type: "shell", Cmd: "echo a"},
				newParallelStep("inner", 0, nil,
					config.DeployStep{Name: "x", Type: "shell", Cmd: "x"},
					config.DeployStep{Name: "y", Type: "shell", Cmd: "y"},
				),
			),
		},
	}
	_, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if !errors.Is(err, ErrNestedParallel) {
		t.Fatalf("expected ErrNestedParallel, got %v", err)
	}
}

func TestResolvePhaseSteps_rejectsUnnamedSubStep(t *testing.T) {
	cfg := &config.DweConfig{}
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			newParallelStep("group", 0, nil,
				config.DeployStep{Name: "", Type: "shell", Cmd: "echo a"},
				config.DeployStep{Name: "b", Type: "shell", Cmd: "echo b"},
			),
		},
	}
	_, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if !errors.Is(err, ErrUnnamedSubStep) {
		t.Fatalf("expected ErrUnnamedSubStep, got %v", err)
	}
}

func TestResolvePhaseSteps_rejectsEmptyParallelSteps(t *testing.T) {
	cfg := &config.DweConfig{}
	cases := []struct {
		name  string
		steps []config.DeployStep
	}{
		{"empty", nil},
		{"single", []config.DeployStep{{Name: "a", Type: "shell", Cmd: "echo a"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			phase := config.DeployPhase{
				Name: "init",
				Steps: []config.DeployStep{
					{Name: "g", Parallel: &config.ParallelGroup{Steps: c.steps}},
				},
			}
			_, err := ResolvePhaseSteps(cfg, nil, phase, "")
			if !errors.Is(err, ErrEmptyParallelSteps) {
				t.Fatalf("expected ErrEmptyParallelSteps, got %v", err)
			}
		})
	}
}

func TestResolvePhaseSteps_rejectsDuplicateNamesCrossGroup(t *testing.T) {
	cfg := &config.DweConfig{}
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			newParallelStep("g1", 0, nil,
				config.DeployStep{Name: "dup", Type: "shell", Cmd: "echo a"},
				config.DeployStep{Name: "other", Type: "shell", Cmd: "echo b"},
			),
			newParallelStep("g2", 0, nil,
				config.DeployStep{Name: "dup", Type: "shell", Cmd: "echo c"},
				config.DeployStep{Name: "yet", Type: "shell", Cmd: "echo d"},
			),
		},
	}
	_, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if !errors.Is(err, ErrDuplicateStepName) {
		t.Fatalf("expected ErrDuplicateStepName, got %v", err)
	}
}

func TestResolvePhaseSteps_rejectsDuplicateNamesLeafAndGroup(t *testing.T) {
	cfg := &config.DweConfig{}
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			{Name: "shared", Type: "shell", Cmd: "echo top"},
			newParallelStep("g", 0, nil,
				config.DeployStep{Name: "shared", Type: "shell", Cmd: "echo sub"},
				config.DeployStep{Name: "other", Type: "shell", Cmd: "echo b"},
			),
		},
	}
	_, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if !errors.Is(err, ErrDuplicateStepName) {
		t.Fatalf("expected ErrDuplicateStepName, got %v", err)
	}
}

func TestResolvePhaseSteps_rejectsBuiltinConfirmInParallel(t *testing.T) {
	cfg := &config.DweConfig{}
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			newParallelStep("g", 0, nil,
				config.DeployStep{Name: "a", Type: "builtin", Cmd: "confirm"},
				config.DeployStep{Name: "b", Type: "shell", Cmd: "echo b"},
			),
		},
	}
	_, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if !errors.Is(err, ErrInteractiveInParallel) {
		t.Fatalf("expected ErrInteractiveInParallel, got %v", err)
	}
}

func TestResolvePhaseSteps_rejectsBuiltinDaemonLogsInParallel(t *testing.T) {
	cfg := &config.DweConfig{}
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			newParallelStep("g", 0, nil,
				config.DeployStep{Name: "a", Type: "builtin", Cmd: "docker_daemon_logs"},
				config.DeployStep{Name: "b", Type: "shell", Cmd: "echo b"},
			),
		},
	}
	_, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if !errors.Is(err, ErrInteractiveInParallel) {
		t.Fatalf("expected ErrInteractiveInParallel, got %v", err)
	}
}

func TestResolvePhaseSteps_rejectsCommandResolvingToDaemonLogsBuiltin(t *testing.T) {
	cfg := &config.DweConfig{}
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(&model.CommandDef{
		ID: "queue.logs", Type: model.CommandTypeBuiltin, Cmd: "docker_daemon_logs",
	})
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			newParallelStep("g", 0, nil,
				config.DeployStep{Name: "a", Type: "command", Cmd: "queue.logs"},
				config.DeployStep{Name: "b", Type: "shell", Cmd: "echo b"},
			),
		},
	}
	_, err := ResolvePhaseSteps(cfg, reg, phase, "")
	if !errors.Is(err, ErrInteractiveInParallel) {
		t.Fatalf("expected ErrInteractiveInParallel via command→daemon_logs, got %v", err)
	}
}

func TestResolvePhaseSteps_acceptsBuiltinConfirmWithSkipConfirm(t *testing.T) {
	cfg := &config.DweConfig{}
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			newParallelStep("g", 0, nil,
				config.DeployStep{Name: "a", Type: "builtin", Cmd: "confirm", SkipConfirm: true},
				config.DeployStep{Name: "b", Type: "shell", Cmd: "echo b"},
			),
		},
	}
	if _, err := ResolvePhaseSteps(cfg, nil, phase, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolvePhaseSteps_groupSkipConfirmInheritedByEverySubStep(t *testing.T) {
	cfg := &config.DweConfig{}
	group := newParallelStep("g", 0, nil,
		config.DeployStep{Name: "a", Type: "builtin", Cmd: "confirm"},
		config.DeployStep{Name: "b", Type: "shell", Cmd: "echo b"},
	)
	group.SkipConfirm = true
	phase := config.DeployPhase{
		Name:  "init",
		Steps: []config.DeployStep{group},
	}
	resolved, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, sub := range resolved[0].Parallel.Steps {
		if !sub.Step.SkipConfirm {
			t.Errorf("sub-step %q SkipConfirm not inherited", sub.Step.Name)
		}
	}
}

func TestResolvePhaseSteps_rejectsCommandWithConfirmation(t *testing.T) {
	cfg := &config.DweConfig{}
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(&model.CommandDef{
		ID: "g.confirm-me", Type: model.CommandTypeShell, Cmd: "echo x", Confirmation: true,
	})
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			newParallelStep("g", 0, nil,
				config.DeployStep{Name: "a", Type: "command", Cmd: "g.confirm-me"},
				config.DeployStep{Name: "b", Type: "shell", Cmd: "echo b"},
			),
		},
	}
	_, err := ResolvePhaseSteps(cfg, reg, phase, "")
	if !errors.Is(err, ErrInteractiveInParallel) {
		t.Fatalf("expected ErrInteractiveInParallel, got %v", err)
	}
}

func TestResolvePhaseSteps_rejectsWorkflowConfirmStep(t *testing.T) {
	cfg := &config.DweConfig{}
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(&model.CommandDef{
		ID:   "g.wf",
		Type: model.CommandTypeWorkflow,
		Steps: []model.WorkflowStep{
			{Confirm: "are you sure?"},
		},
	})
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			newParallelStep("g", 0, nil,
				config.DeployStep{Name: "a", Type: "command", Cmd: "g.wf"},
				config.DeployStep{Name: "b", Type: "shell", Cmd: "echo b"},
			),
		},
	}
	_, err := ResolvePhaseSteps(cfg, reg, phase, "")
	if !errors.Is(err, ErrInteractiveInParallel) {
		t.Fatalf("expected ErrInteractiveInParallel, got %v", err)
	}
}

func TestResolvePhaseSteps_rejectsWorkflowRecursiveConfirm(t *testing.T) {
	cfg := &config.DweConfig{}
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(&model.CommandDef{
		ID: "g.leaf", Type: model.CommandTypeShell, Cmd: "echo", Confirmation: true,
	})
	reg.AddCommandForTest(&model.CommandDef{
		ID:    "g.wf",
		Type:  model.CommandTypeWorkflow,
		Steps: []model.WorkflowStep{{Command: "g.leaf"}},
	})
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			newParallelStep("g", 0, nil,
				config.DeployStep{Name: "a", Type: "command", Cmd: "g.wf"},
				config.DeployStep{Name: "b", Type: "shell", Cmd: "echo b"},
			),
		},
	}
	_, err := ResolvePhaseSteps(cfg, reg, phase, "")
	if !errors.Is(err, ErrInteractiveInParallel) {
		t.Fatalf("expected ErrInteractiveInParallel, got %v", err)
	}
}

func TestResolvePhaseSteps_workflowCycleGuard(t *testing.T) {
	cfg := &config.DweConfig{}
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(&model.CommandDef{
		ID:    "g.a",
		Type:  model.CommandTypeWorkflow,
		Steps: []model.WorkflowStep{{Command: "g.b"}},
	})
	reg.AddCommandForTest(&model.CommandDef{
		ID:    "g.b",
		Type:  model.CommandTypeWorkflow,
		Steps: []model.WorkflowStep{{Command: "g.a"}},
	})
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			newParallelStep("g", 0, nil,
				config.DeployStep{Name: "a", Type: "command", Cmd: "g.a"},
				config.DeployStep{Name: "b", Type: "shell", Cmd: "echo b"},
			),
		},
	}
	// Must not infinite-loop and must not flag (no confirm anywhere).
	if _, err := ResolvePhaseSteps(cfg, reg, phase, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolvePhaseSteps_registryNilSkipsCommandInteractiveCheck(t *testing.T) {
	cfg := &config.DweConfig{}
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			newParallelStep("g", 0, nil,
				// type=command with reg=nil: workflow walk skipped.
				config.DeployStep{Name: "a", Type: "command", Cmd: "anything"},
				config.DeployStep{Name: "b", Type: "shell", Cmd: "echo b"},
			),
		},
	}
	if _, err := ResolvePhaseSteps(cfg, nil, phase, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- sub_step_overrides validation -----------------------------------------

func newReadableGate() filesgate.FilesGate {
	return filesgate.FilesGate{State: filesgate.StateReadable, Require: filesgate.RequireRequired{}}
}

func makeDumpDeployRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(&model.CommandDef{
		ID:   "db.dump-deploy",
		Type: model.CommandTypeShell,
		Cmd:  "echo restore",
		Files: map[string]model.FileSpec{
			"dump": {Access: model.FileAccessRead, Path: "/tmp/dwe-test-not-there", Required: true},
		},
	})
	reg.AddCommandForTest(&model.CommandDef{
		ID:   "db.dumps-deploy",
		Type: model.CommandTypeWorkflow,
		Steps: []model.WorkflowStep{
			{Parallel: &model.WorkflowParallel{Steps: []model.WorkflowStep{
				{Name: "deploy-main", Command: "db.dump-deploy"},
				{Name: "deploy-stock", Command: "db.dump-deploy"},
				{Name: "deploy-price", Command: "db.dump-deploy"},
			}}},
		},
	})
	return reg
}

func TestResolvePhaseSteps_subStepOverridesValid(t *testing.T) {
	cfg := &config.DweConfig{}
	reg := makeDumpDeployRegistry(t)
	gate := newReadableGate()
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			{
				Name: "db-dumps",
				Type: "command",
				Cmd:  "db.dumps-deploy",
				SubStepOverrides: map[string]config.SubStepOverride{
					"deploy-main": {FilesGate: &gate},
				},
			},
		},
	}
	if _, err := ResolvePhaseSteps(cfg, reg, phase, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolvePhaseSteps_subStepOverridesUnknownKey(t *testing.T) {
	cfg := &config.DweConfig{}
	reg := makeDumpDeployRegistry(t)
	gate := newReadableGate()
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			{
				Name: "db-dumps",
				Type: "command",
				Cmd:  "db.dumps-deploy",
				SubStepOverrides: map[string]config.SubStepOverride{
					"does-not-exist": {FilesGate: &gate},
				},
			},
		},
	}
	_, err := ResolvePhaseSteps(cfg, reg, phase, "")
	if !errors.Is(err, ErrSubStepOverridesInvalid) {
		t.Fatalf("expected ErrSubStepOverridesInvalid, got %v", err)
	}
}

func TestResolvePhaseSteps_subStepOverridesAmbiguousName(t *testing.T) {
	cfg := &config.DweConfig{}
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(&model.CommandDef{
		ID:   "db.dump-deploy",
		Type: model.CommandTypeShell,
		Cmd:  "echo restore",
		Files: map[string]model.FileSpec{
			"dump": {Access: model.FileAccessRead, Path: "/tmp/x", Required: true},
		},
	})
	// Two sub-steps share `db.dump-deploy` as their effective name (no explicit Name).
	reg.AddCommandForTest(&model.CommandDef{
		ID:   "db.dumps-deploy",
		Type: model.CommandTypeWorkflow,
		Steps: []model.WorkflowStep{
			{Parallel: &model.WorkflowParallel{Steps: []model.WorkflowStep{
				{Command: "db.dump-deploy"},
				{Command: "db.dump-deploy"},
			}}},
		},
	})
	gate := newReadableGate()
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			{
				Name: "db-dumps",
				Type: "command",
				Cmd:  "db.dumps-deploy",
				SubStepOverrides: map[string]config.SubStepOverride{
					"db.dump-deploy": {FilesGate: &gate},
				},
			},
		},
	}
	_, err := ResolvePhaseSteps(cfg, reg, phase, "")
	if !errors.Is(err, ErrSubStepOverridesInvalid) {
		t.Fatalf("expected ErrSubStepOverridesInvalid, got %v", err)
	}
}

func TestResolvePhaseSteps_subStepOverridesRejectsNestedWorkflow(t *testing.T) {
	cfg := &config.DweConfig{}
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(&model.CommandDef{
		ID:   "inner.wf",
		Type: model.CommandTypeWorkflow,
		Steps: []model.WorkflowStep{
			{Command: "leaf.shell"},
		},
	})
	reg.AddCommandForTest(&model.CommandDef{
		ID:   "leaf.shell",
		Type: model.CommandTypeShell,
		Cmd:  "echo",
	})
	reg.AddCommandForTest(&model.CommandDef{
		ID:   "outer.wf",
		Type: model.CommandTypeWorkflow,
		Steps: []model.WorkflowStep{
			{Name: "nested", Command: "inner.wf"},
		},
	})
	gate := newReadableGate()
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			{
				Name: "step",
				Type: "command",
				Cmd:  "outer.wf",
				SubStepOverrides: map[string]config.SubStepOverride{
					"nested": {FilesGate: &gate},
				},
			},
		},
	}
	_, err := ResolvePhaseSteps(cfg, reg, phase, "")
	if !errors.Is(err, ErrSubStepOverridesInvalid) {
		t.Fatalf("expected ErrSubStepOverridesInvalid, got %v", err)
	}
}

func TestResolvePhaseSteps_subStepOverridesRejectsNonCommandStep(t *testing.T) {
	cfg := &config.DweConfig{}
	reg := makeDumpDeployRegistry(t)
	gate := newReadableGate()
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			{
				Name: "step",
				Type: "shell",
				Cmd:  "echo x",
				SubStepOverrides: map[string]config.SubStepOverride{
					"deploy-main": {FilesGate: &gate},
				},
			},
		},
	}
	_, err := ResolvePhaseSteps(cfg, reg, phase, "")
	if !errors.Is(err, ErrSubStepOverridesInvalid) {
		t.Fatalf("expected ErrSubStepOverridesInvalid, got %v", err)
	}
}

func TestResolvePhaseSteps_subStepOverridesRejectsNonWorkflowTarget(t *testing.T) {
	cfg := &config.DweConfig{}
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(&model.CommandDef{
		ID: "g.shell", Type: model.CommandTypeShell, Cmd: "echo",
	})
	gate := newReadableGate()
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			{
				Name: "step",
				Type: "command",
				Cmd:  "g.shell",
				SubStepOverrides: map[string]config.SubStepOverride{
					"x": {FilesGate: &gate},
				},
			},
		},
	}
	_, err := ResolvePhaseSteps(cfg, reg, phase, "")
	if !errors.Is(err, ErrSubStepOverridesInvalid) {
		t.Fatalf("expected ErrSubStepOverridesInvalid, got %v", err)
	}
}

func TestResolveStepWhen(t *testing.T) {
	cfg := &config.DweConfig{}

	t.Run("nil when keeps step with no runtime condition", func(t *testing.T) {
		rt, keep, err := resolveStepWhen(cfg, config.DeployStep{Name: "s"}, "init/s")
		if err != nil || !keep || rt != nil {
			t.Fatalf("got rt=%v keep=%v err=%v", rt, keep, err)
		}
	})

	t.Run("runtime condition is returned and step kept", func(t *testing.T) {
		c := &condition.Condition{Type: condition.TypeShell, Cmd: "true"}
		rt, keep, err := resolveStepWhen(cfg, config.DeployStep{Name: "s", When: c}, "init/s")
		if err != nil || !keep || rt != c {
			t.Fatalf("got rt=%v keep=%v err=%v", rt, keep, err)
		}
	})

	t.Run("template true keeps step, no runtime condition", func(t *testing.T) {
		c := &condition.Condition{Type: condition.TypeTemplate, Expr: "true"}
		rt, keep, err := resolveStepWhen(cfg, config.DeployStep{Name: "s", When: c}, "init/s")
		if err != nil || !keep || rt != nil {
			t.Fatalf("got rt=%v keep=%v err=%v", rt, keep, err)
		}
	})

	t.Run("template false filters step out", func(t *testing.T) {
		c := &condition.Condition{Type: condition.TypeTemplate, Expr: "false"}
		rt, keep, err := resolveStepWhen(cfg, config.DeployStep{Name: "s", When: c}, "init/s")
		if err != nil || keep || rt != nil {
			t.Fatalf("got rt=%v keep=%v err=%v", rt, keep, err)
		}
	})

	t.Run("template error is wrapped with step prefix", func(t *testing.T) {
		c := &condition.Condition{Type: condition.TypeTemplate, Expr: "{{ .Nope.Missing }}"}
		_, keep, err := resolveStepWhen(cfg, config.DeployStep{Name: "s", When: c}, "init/s")
		if err == nil {
			t.Fatal("expected error")
		}
		if keep {
			t.Fatal("expected keep=false on error")
		}
		if !strings.Contains(err.Error(), "evaluating when condition for step init/s") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})
}

func TestParseStepTimeout(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{name: "absent", raw: "", want: 0},
		{name: "explicit zero", raw: "0", want: 0},
		{name: "positive", raw: "90s", want: 90 * time.Second},
		{name: "invalid", raw: "abc", wantErr: true},
		{name: "negative", raw: "-1s", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseStepTimeout(tt.raw, "init/s")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseStepTimeout(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestResolvePhaseSteps_timeoutPopulatesResolvedStep(t *testing.T) {
	cfg := &config.DweConfig{}
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			{Name: "a", Type: "shell", Cmd: "echo a", Timeout: "90s"},
			{Name: "b", Type: "shell", Cmd: "echo b"},
		},
	}
	resolved, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolved) != 2 {
		t.Fatalf("expected 2 resolved steps, got %d", len(resolved))
	}
	if resolved[0].Timeout != 90*time.Second {
		t.Errorf("resolved[0].Timeout = %v, want 90s", resolved[0].Timeout)
	}
	if resolved[1].Timeout != 0 {
		t.Errorf("resolved[1].Timeout = %v, want 0 (absent)", resolved[1].Timeout)
	}
}

func TestResolvePhaseSteps_invalidTimeoutIsResolveError(t *testing.T) {
	cfg := &config.DweConfig{}
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			{Name: "a", Type: "shell", Cmd: "echo a", Timeout: "not-a-duration"},
		},
	}
	_, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err == nil {
		t.Fatal("expected error for invalid timeout")
	}
	if !strings.Contains(err.Error(), "init/a") {
		t.Fatalf("error should name the step: %v", err)
	}
}

func TestResolvePhaseSteps_parallelSubStepCarriesOwnTimeout(t *testing.T) {
	cfg := &config.DweConfig{}
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			newParallelStep("group", 0, nil,
				config.DeployStep{Name: "a", Type: "shell", Cmd: "echo a", Timeout: "5s"},
				config.DeployStep{Name: "b", Type: "shell", Cmd: "echo b"},
			),
		},
	}
	resolved, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rp := resolved[0].Parallel
	if rp.Steps[0].Timeout != 5*time.Second {
		t.Errorf("substep a Timeout = %v, want 5s", rp.Steps[0].Timeout)
	}
	if rp.Steps[1].Timeout != 0 {
		t.Errorf("substep b Timeout = %v, want 0 (absent)", rp.Steps[1].Timeout)
	}
}

func configWithSourceVars() *config.DweConfig {
	return &config.DweConfig{
		Raw: map[string]any{
			"vars": map[string]any{
				"source": map[string]any{
					"repo": "https://example.com/repo.git",
					"dir":  "app",
				},
			},
		},
	}
}

func TestResolvePhaseSteps_rendersKnownHeadCmd(t *testing.T) {
	cfg := configWithSourceVars()
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			{Name: "clone", Type: "shell", Cmd: "git clone ${vars.source.repo} ${vars.source.dir}"},
		},
	}
	resolved, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "git clone https://example.com/repo.git app"
	if resolved[0].Step.Cmd != want {
		t.Errorf("Step.Cmd = %q, want %q", resolved[0].Step.Cmd, want)
	}
}

func TestResolvePhaseSteps_leavesUnknownHeadLiteral(t *testing.T) {
	cfg := configWithSourceVars()
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			{Name: "greet", Type: "shell", Cmd: "echo ${HOME}"},
		},
	}
	resolved, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved[0].Step.Cmd != "echo ${HOME}" {
		t.Errorf("Step.Cmd = %q, want unchanged", resolved[0].Step.Cmd)
	}
}

func TestResolvePhaseSteps_rendersCommandStepWith(t *testing.T) {
	cfg := configWithSourceVars()
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			{Name: "clone", Type: "command", Cmd: "source_clone", With: map[string]any{"repo": "${vars.source.repo}"}},
		},
	}
	resolved, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved[0].Step.With["repo"] != "https://example.com/repo.git" {
		t.Errorf("Step.With[repo] = %v", resolved[0].Step.With["repo"])
	}
	if phase.Steps[0].With["repo"] != "${vars.source.repo}" {
		t.Errorf("original phase.Steps[0].With mutated: %v", phase.Steps[0].With["repo"])
	}
}

func TestResolvePhaseSteps_rendersFilesGateWith(t *testing.T) {
	cfg := configWithSourceVars()
	original := &filesgate.FilesGate{With: map[string]any{"x": "${vars.source.repo}"}, State: filesgate.StateReadable}
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			{Name: "a", Type: "shell", Cmd: "true", FilesGate: original},
		},
	}
	resolved, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved[0].FilesGate.With["x"] != "https://example.com/repo.git" {
		t.Errorf("FilesGate.With[x] = %v", resolved[0].FilesGate.With["x"])
	}
	if original.With["x"] != "${vars.source.repo}" {
		t.Errorf("original FilesGate mutated: %v", original.With["x"])
	}
}

func TestResolvePhaseSteps_renderErrorFailsResolve(t *testing.T) {
	cfg := configWithSourceVars()
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			{Name: "bad", Type: "shell", Cmd: "${vars.x}{{ if }}"},
		},
	}
	_, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err == nil {
		t.Fatal("expected a render error")
	}
	if !strings.Contains(err.Error(), "init/bad") {
		t.Errorf("error should name the step: %v", err)
	}
}

func TestResolvePhaseSteps_resolvingSameConfigTwiceIsIdempotent(t *testing.T) {
	cfg := configWithSourceVars()
	phase := config.DeployPhase{
		Name: "init",
		Steps: []config.DeployStep{
			{Name: "clone", Type: "command", Cmd: "source_clone", With: map[string]any{"repo": "${vars.source.repo}"}},
		},
	}
	first, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err != nil {
		t.Fatalf("first resolve: unexpected error: %v", err)
	}
	second, err := ResolvePhaseSteps(cfg, nil, phase, "")
	if err != nil {
		t.Fatalf("second resolve: unexpected error: %v", err)
	}
	if first[0].Step.With["repo"] != second[0].Step.With["repo"] {
		t.Errorf("resolve is not byte-identical across calls: first=%v second=%v",
			first[0].Step.With["repo"], second[0].Step.With["repo"])
	}
}
