package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/semsemyonoff/dwe/internal/core/execution/filesgate"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func parallelPhases(steps ...config.DeployStep) []config.DeployPhase {
	return []config.DeployPhase{{Name: "p1", Steps: steps}}
}

func groupStep(name string, subs ...config.DeployStep) config.DeployStep {
	return config.DeployStep{
		Name:     name,
		Parallel: &config.ParallelGroup{Steps: subs},
	}
}

func runDeployParallel(t *testing.T, reg *usercommands.Registry, phases []config.DeployPhase) []validate.Diagnostic {
	t.Helper()
	cfg := &config.DevboxConfig{Deploy: &config.ProjectDeployConfig{Phases: phases}}
	ctx := validate.Context{
		ProjectRoot:     t.TempDir(),
		Cfg:             cfg,
		CommandRegistry: reg,
	}
	return (&parallelGroupsValidator{}).Run(ctx)
}

// Sanity: a leaf-only phase produces no diagnostics.
func TestParallelGroupsValidator_NoGroups(t *testing.T) {
	diags := runDeployParallel(t, usercommands.NewEmptyRegistry(), parallelPhases(
		config.DeployStep{Name: "a", Type: "shell", Cmd: "true"},
	))
	require.Empty(t, diags)
}

// Happy path: a valid parallel group produces no diagnostics.
func TestParallelGroupsValidator_HappyPath(t *testing.T) {
	diags := runDeployParallel(t, usercommands.NewEmptyRegistry(), parallelPhases(
		groupStep("g1",
			config.DeployStep{Name: "a", Type: "shell", Cmd: "true"},
			config.DeployStep{Name: "b", Type: "shell", Cmd: "true"},
		),
	))
	require.Empty(t, diags)
}

func TestParallelGroupsValidator_EmptySteps(t *testing.T) {
	diags := runDeployParallel(t, usercommands.NewEmptyRegistry(), parallelPhases(
		groupStep("g1",
			config.DeployStep{Name: "a", Type: "shell", Cmd: "true"},
		),
	))
	require.Len(t, diags, 1)
	require.Equal(t, validate.SeverityError, diags[0].Severity)
	require.Contains(t, diags[0].Message, "at least two sub-steps")
	require.Contains(t, diags[0].Hint, "leaf step")
}

func TestParallelGroupsValidator_UnnamedSubStep(t *testing.T) {
	diags := runDeployParallel(t, usercommands.NewEmptyRegistry(), parallelPhases(
		groupStep("g1",
			config.DeployStep{Type: "shell", Cmd: "true"},
			config.DeployStep{Name: "b", Type: "shell", Cmd: "true"},
		),
	))
	require.Len(t, diags, 1)
	require.Equal(t, validate.SeverityError, diags[0].Severity)
	require.Contains(t, diags[0].Message, "must have a name")
}

func TestParallelGroupsValidator_NestedParallel(t *testing.T) {
	diags := runDeployParallel(t, usercommands.NewEmptyRegistry(), parallelPhases(
		groupStep("g1",
			groupStep("g2",
				config.DeployStep{Name: "x", Type: "shell", Cmd: "true"},
				config.DeployStep{Name: "y", Type: "shell", Cmd: "true"},
			),
			config.DeployStep{Name: "b", Type: "shell", Cmd: "true"},
		),
	))
	require.NotEmpty(t, diags)
	var found bool
	for _, d := range diags {
		if d.Severity == validate.SeverityError && d.Message == "nested parallel groups are not supported" {
			found = true
			require.Equal(t, "flat parallel groups only in v1", d.Hint)
		}
	}
	require.True(t, found, "expected nested parallel error: %+v", diags)
}

func TestParallelGroupsValidator_DuplicateName_CrossGroup(t *testing.T) {
	diags := runDeployParallel(t, usercommands.NewEmptyRegistry(), parallelPhases(
		groupStep("g1",
			config.DeployStep{Name: "download", Type: "shell", Cmd: "true"},
			config.DeployStep{Name: "x", Type: "shell", Cmd: "true"},
		),
		groupStep("g2",
			config.DeployStep{Name: "download", Type: "shell", Cmd: "true"},
			config.DeployStep{Name: "y", Type: "shell", Cmd: "true"},
		),
	))
	var count int
	for _, d := range diags {
		if d.Severity == validate.SeverityError && d.Message == `duplicate step name in phase: "download"` {
			count++
			require.Contains(t, d.Hint, "unique value within the phase")
		}
	}
	require.Equal(t, 1, count, "expected exactly one duplicate diagnostic, got %d: %+v", count, diags)
}

func TestParallelGroupsValidator_DuplicateName_LeafCollision(t *testing.T) {
	diags := runDeployParallel(t, usercommands.NewEmptyRegistry(), parallelPhases(
		config.DeployStep{Name: "boot", Type: "shell", Cmd: "true"},
		groupStep("g1",
			config.DeployStep{Name: "boot", Type: "shell", Cmd: "true"},
			config.DeployStep{Name: "y", Type: "shell", Cmd: "true"},
		),
	))
	var found bool
	for _, d := range diags {
		if d.Severity == validate.SeverityError && d.Message == `duplicate step name in phase: "boot"` {
			found = true
		}
	}
	require.True(t, found, "expected leaf/sub-step name collision diagnostic: %+v", diags)
}

func TestParallelGroupsValidator_InteractiveConfirmBuiltin(t *testing.T) {
	diags := runDeployParallel(t, usercommands.NewEmptyRegistry(), parallelPhases(
		groupStep("g1",
			config.DeployStep{Name: "ask", Type: "builtin", Cmd: "confirm"},
			config.DeployStep{Name: "b", Type: "shell", Cmd: "true"},
		),
	))
	var found bool
	for _, d := range diags {
		if d.Severity == validate.SeverityError && d.Message == "interactive prompt in parallel sub-step requires skip_confirm: true" {
			found = true
		}
	}
	require.True(t, found, "expected interactive diagnostic: %+v", diags)
}

func TestParallelGroupsValidator_InteractiveSkippedWithGroupSkipConfirm(t *testing.T) {
	step := groupStep("g1",
		config.DeployStep{Name: "ask", Type: "builtin", Cmd: "confirm"},
		config.DeployStep{Name: "b", Type: "shell", Cmd: "true"},
	)
	step.SkipConfirm = true
	diags := runDeployParallel(t, usercommands.NewEmptyRegistry(), parallelPhases(step))
	require.Empty(t, diags)
}

func TestParallelGroupsValidator_InteractiveSkippedWithSubSkipConfirm(t *testing.T) {
	diags := runDeployParallel(t, usercommands.NewEmptyRegistry(), parallelPhases(
		groupStep("g1",
			config.DeployStep{Name: "ask", Type: "builtin", Cmd: "confirm", SkipConfirm: true},
			config.DeployStep{Name: "b", Type: "shell", Cmd: "true"},
		),
	))
	require.Empty(t, diags)
}

func TestParallelGroupsValidator_InteractiveConfirmationCommand(t *testing.T) {
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:           "ask",
		Type:         usercommands.CommandTypeShell,
		Cmd:          "true",
		Confirmation: true,
	})
	diags := runDeployParallel(t, reg, parallelPhases(
		groupStep("g1",
			config.DeployStep{Name: "do-ask", Type: "command", Cmd: "ask"},
			config.DeployStep{Name: "b", Type: "shell", Cmd: "true"},
		),
	))
	var found bool
	for _, d := range diags {
		if d.Severity == validate.SeverityError && d.Message == "interactive prompt in parallel sub-step requires skip_confirm: true" {
			found = true
		}
	}
	require.True(t, found, "expected interactive diagnostic for confirmation:true: %+v", diags)
}

func TestParallelGroupsValidator_InteractiveWorkflow(t *testing.T) {
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:           "leaf-confirm",
		Type:         usercommands.CommandTypeShell,
		Cmd:          "true",
		Confirmation: true,
	})
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:    "wf-direct",
		Type:  usercommands.CommandTypeWorkflow,
		Steps: []usercommands.WorkflowStep{{Confirm: "ok?"}},
	})
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:    "wf-indirect",
		Type:  usercommands.CommandTypeWorkflow,
		Steps: []usercommands.WorkflowStep{{Command: "leaf-confirm"}},
	})
	diags := runDeployParallel(t, reg, parallelPhases(
		groupStep("g1",
			config.DeployStep{Name: "a", Type: "command", Cmd: "wf-direct"},
			config.DeployStep{Name: "b", Type: "command", Cmd: "wf-indirect"},
		),
	))
	var directFound, indirectFound bool
	for _, d := range diags {
		if d.Message != "interactive prompt in parallel sub-step requires skip_confirm: true" {
			continue
		}
		if d.Target == "config.deploy.phases[0].steps[0].parallel.steps[0]" {
			directFound = true
		}
		if d.Target == "config.deploy.phases[0].steps[0].parallel.steps[1]" {
			indirectFound = true
		}
	}
	require.True(t, directFound, "expected direct workflow confirm diag: %+v", diags)
	require.True(t, indirectFound, "expected indirect workflow confirm diag: %+v", diags)
}

func TestParallelGroupsValidator_LeafOnlyOnGroup(t *testing.T) {
	step := groupStep("g1",
		config.DeployStep{Name: "a", Type: "shell", Cmd: "true"},
		config.DeployStep{Name: "b", Type: "shell", Cmd: "true"},
	)
	// Defensive: simulate post-load corruption.
	step.Type = "shell"
	step.Cmd = "echo hello"
	step.ContinueOnError = true
	step.FilesGate = &filesgate.FilesGate{Command: "x", State: filesgate.StateReadable}
	diags := runDeployParallel(t, usercommands.NewEmptyRegistry(), parallelPhases(step))
	var found bool
	for _, d := range diags {
		if d.Severity == validate.SeverityError && d.Target == "config.deploy.phases[0].steps[0].parallel" {
			found = true
			require.Contains(t, d.Message, "type")
			require.Contains(t, d.Message, "cmd")
			require.Contains(t, d.Message, "files_gate")
			require.Contains(t, d.Message, "continue_on_error")
		}
	}
	require.True(t, found, "expected leaf-on-group diagnostic: %+v", diags)
}

func TestParallelGroupsValidator_ServiceRunTTY(t *testing.T) {
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:          "svc-run",
		Type:        usercommands.CommandTypeServiceRun,
		Service:     "app",
		Cmd:         "true",
		ComposeArgs: []string{"-it"},
	})
	diags := runDeployParallel(t, reg, parallelPhases(
		groupStep("g1",
			config.DeployStep{Name: "a", Type: "command", Cmd: "svc-run"},
			config.DeployStep{Name: "b", Type: "shell", Cmd: "true"},
		),
	))
	var found bool
	for _, d := range diags {
		if d.Severity == validate.SeverityWarning && d.Message == "service_run sub-step uses TTY-allocating compose args" {
			found = true
			require.Contains(t, d.Hint, "cannot allocate tty")
		}
	}
	require.True(t, found, "expected TTY warning: %+v", diags)
}

// Registry-nil tolerance: command-target lookups skipped, builtin still flagged.
func TestParallelGroupsValidator_RegistryNilTolerance(t *testing.T) {
	cfg := &config.DevboxConfig{Deploy: &config.ProjectDeployConfig{Phases: parallelPhases(
		groupStep("g1",
			config.DeployStep{Name: "ask", Type: "builtin", Cmd: "confirm"},
			config.DeployStep{Name: "wf", Type: "command", Cmd: "wf-direct"},
		),
	)}}
	ctx := validate.Context{
		ProjectRoot:     t.TempDir(),
		Cfg:             cfg,
		CommandRegistry: nil,
	}
	diags := (&parallelGroupsValidator{}).Run(ctx)
	var builtinFlagged, commandFlagged bool
	for _, d := range diags {
		if d.Message != "interactive prompt in parallel sub-step requires skip_confirm: true" {
			continue
		}
		if d.Target == "config.deploy.phases[0].steps[0].parallel.steps[0]" {
			builtinFlagged = true
		}
		if d.Target == "config.deploy.phases[0].steps[0].parallel.steps[1]" {
			commandFlagged = true
		}
	}
	require.True(t, builtinFlagged, "builtin confirm should always be flagged")
	require.False(t, commandFlagged, "command target requires registry; should not be flagged when nil")
}

// Lifecycle validator: hits the run-phases path.
func TestLifecycleParallelGroupsValidator_Run(t *testing.T) {
	tmp := t.TempDir()
	devboxDir := filepath.Join(tmp, "devbox")
	require.NoError(t, os.MkdirAll(devboxDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(`run:
  phases:
    - name: boot
      steps:
        - name: g1
          parallel:
            steps:
              - type: shell
                cmd: "true"
              - name: b
                type: shell
                cmd: "true"
`), 0o644))
	cfg := &config.DevboxConfig{}
	diags := (&lifecycleParallelGroupsValidator{}).Run(validate.Context{
		ProjectRoot:     tmp,
		Cfg:             cfg,
		CommandRegistry: usercommands.NewEmptyRegistry(),
	})
	var unnamedFound bool
	for _, d := range diags {
		if d.Severity == validate.SeverityError && d.Message == "parallel sub-step must have a name" {
			unnamedFound = true
			require.Contains(t, d.Target, "config.lifecycle.run")
		}
	}
	require.True(t, unnamedFound, "expected lifecycle.run unnamed sub-step diag: %+v", diags)
}

func TestResetParallelGroupsValidator(t *testing.T) {
	tmp := t.TempDir()
	devboxDir := filepath.Join(tmp, "devbox")
	require.NoError(t, os.MkdirAll(devboxDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(devboxDir, "reset.yml"), []byte(`phases:
  - name: tear-down
    steps:
      - name: g1
        parallel:
          steps:
            - name: only-one
              type: shell
              cmd: "true"
`), 0o644))
	cfg := &config.DevboxConfig{}
	diags := (&resetParallelGroupsValidator{}).Run(validate.Context{
		ProjectRoot:     tmp,
		Cfg:             cfg,
		CommandRegistry: usercommands.NewEmptyRegistry(),
	})
	var found bool
	for _, d := range diags {
		if d.Severity == validate.SeverityError && d.Message == "parallel group must declare at least two sub-steps (got 1)" {
			found = true
			require.Contains(t, d.Target, "config.reset")
		}
	}
	require.True(t, found, "expected reset empty-group diag: %+v", diags)
}
