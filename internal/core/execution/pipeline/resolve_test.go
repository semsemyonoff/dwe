package pipeline

import (
	"errors"
	"runtime"
	"testing"

	"github.com/semsemyonoff/devbox/internal/core/execution/filesgate"
	"github.com/semsemyonoff/devbox/internal/core/project/config"
	"github.com/semsemyonoff/devbox/internal/core/usercommands/model"
	"github.com/semsemyonoff/devbox/internal/core/usercommands/registry"
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
			"dump": {Access: model.FileAccessRead, Path: "/tmp/devbox-test-not-there", Required: true},
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
	cfg := &config.DevboxConfig{SchemaVersion: "2"}
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
