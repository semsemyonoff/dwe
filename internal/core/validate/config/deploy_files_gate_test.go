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

func makeGatePhases(cmd string) []config.DeployPhase {
	return []config.DeployPhase{
		{
			Name: "setup",
			Steps: []config.DeployStep{
				{
					Name: "check",
					Type: "shell",
					Cmd:  "true",
					FilesGate: &filesgate.FilesGate{
						Command: cmd,
						State:   filesgate.StateReadable,
						Require: filesgate.RequireRequired{},
					},
				},
			},
		},
	}
}

func makeGateCommand() *usercommands.CommandDef {
	return &usercommands.CommandDef{
		ID:   "db-download",
		Type: usercommands.CommandTypeShell,
		Cmd:  "true",
		Files: map[string]usercommands.FileSpec{
			"dump": {
				Candidates: []usercommands.FileCandidate{{Path: "dump.sql.gz"}},
				Access:     usercommands.FileAccessRead,
				Required:   true,
			},
		},
	}
}

// TestDeployFilesGateValidator_NilCfg verifies early return on nil Cfg.
func TestDeployFilesGateValidator_NilCfg(t *testing.T) {
	ctx := validate.Context{
		ProjectRoot: t.TempDir(),
		Cfg:         nil,
	}
	diags := (&deployFilesGateValidator{}).Run(ctx)
	require.Empty(t, diags)
}

// TestDeployFilesGateValidator_NilRegistry_NoGateSteps verifies no diagnostic when
// registry is absent but no steps use files_gate.
func TestDeployFilesGateValidator_NilRegistry_NoGateSteps(t *testing.T) {
	cfg := &config.DweConfig{
		Deploy: &config.ProjectDeployConfig{
			Phases: []config.DeployPhase{
				{Name: "setup", Steps: []config.DeployStep{{Name: "plain", Type: "shell", Cmd: "true"}}},
			},
		},
	}
	ctx := validate.Context{
		ProjectRoot:     t.TempDir(),
		CommandRegistry: nil,
		Cfg:             cfg,
	}
	diags := (&deployFilesGateValidator{}).Run(ctx)
	require.Empty(t, diags)
}

// TestDeployFilesGateValidator_NilRegistry_WithGateSteps verifies an info skip diagnostic
// when registry is absent but a step uses files_gate.
func TestDeployFilesGateValidator_NilRegistry_WithGateSteps(t *testing.T) {
	cfg := &config.DweConfig{
		Deploy: &config.ProjectDeployConfig{Phases: makeGatePhases("db-download")},
	}
	ctx := validate.Context{
		ProjectRoot:     t.TempDir(),
		CommandRegistry: nil,
		Cfg:             cfg,
	}
	diags := (&deployFilesGateValidator{}).Run(ctx)
	require.Len(t, diags, 1)
	require.Equal(t, validate.SeverityInfo, diags[0].Severity)
	require.Contains(t, diags[0].Message, "skipped")
}

// TestDeployFilesGateValidator_UnknownCommand verifies an error diagnostic when
// files_gate references a command not in the registry.
func TestDeployFilesGateValidator_UnknownCommand(t *testing.T) {
	reg := usercommands.NewEmptyRegistry()
	// "db-download" intentionally absent from registry.

	cfg := &config.DweConfig{
		Raw:    map[string]any{},
		Deploy: &config.ProjectDeployConfig{Phases: makeGatePhases("db-download")},
	}
	ctx := validate.Context{
		ProjectRoot:     t.TempDir(),
		CommandRegistry: reg,
		Cfg:             cfg,
	}
	diags := (&deployFilesGateValidator{}).Run(ctx)
	require.Len(t, diags, 1)
	require.Equal(t, validate.SeverityError, diags[0].Severity)
	require.Contains(t, diags[0].Message, "unknown command")
}

// TestDeployFilesGateValidator_ValidGate verifies no error diagnostics when
// files_gate references a valid command with a matching files: block.
func TestDeployFilesGateValidator_ValidGate(t *testing.T) {
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(makeGateCommand())

	cfg := &config.DweConfig{
		Raw:    map[string]any{},
		Deploy: &config.ProjectDeployConfig{Phases: makeGatePhases("db-download")},
	}
	ctx := validate.Context{
		ProjectRoot:     t.TempDir(),
		CommandRegistry: reg,
		Cfg:             cfg,
	}
	diags := (&deployFilesGateValidator{}).Run(ctx)
	for _, d := range diags {
		if d.Severity == validate.SeverityError {
			t.Errorf("unexpected error diagnostic: %s", d.Message)
		}
	}
}

// TestLifecycleFilesGateValidator_NilRegistry_WithGateSteps verifies an info skip
// diagnostic for a lifecycle.yml file that uses files_gate when registry is absent.
func TestLifecycleFilesGateValidator_NilRegistry_WithGateSteps(t *testing.T) {
	tmpDir := t.TempDir()

	lifecycleYml := `run:
  phases:
    - name: setup
      steps:
        - name: check
          type: shell
          cmd: "true"
          files_gate:
            command: db-download
            state: readable
`
	devboxDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(devboxDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(devboxDir, "lifecycle.yml"), []byte(lifecycleYml), 0o644))

	cfg := &config.DweConfig{}
	ctx := validate.Context{
		ProjectRoot:     tmpDir,
		CommandRegistry: nil,
		Cfg:             cfg,
	}
	diags := (&lifecycleFilesGateValidator{}).Run(ctx)
	require.Len(t, diags, 1)
	require.Equal(t, validate.SeverityInfo, diags[0].Severity)
	require.Contains(t, diags[0].Message, "skipped")
}

// TestDeployFilesGateValidator_ParallelSubStep verifies that files_gate on a
// parallel sub-step is validated (not silently skipped).
func TestDeployFilesGateValidator_ParallelSubStep(t *testing.T) {
	reg := usercommands.NewEmptyRegistry()
	// "db-download" intentionally absent from registry — sub-step gate must be caught.

	cfg := &config.DweConfig{
		Raw: map[string]any{},
		Deploy: &config.ProjectDeployConfig{
			Phases: []config.DeployPhase{
				{
					Name: "setup",
					Steps: []config.DeployStep{
						{
							Name: "parallel-group",
							Parallel: &config.ParallelGroup{
								Steps: []config.DeployStep{
									{
										Name: "sub",
										Type: "shell",
										Cmd:  "true",
										FilesGate: &filesgate.FilesGate{
											Command: "db-download",
											State:   filesgate.StateReadable,
											Require: filesgate.RequireRequired{},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	ctx := validate.Context{
		ProjectRoot:     t.TempDir(),
		CommandRegistry: reg,
		Cfg:             cfg,
	}
	diags := (&deployFilesGateValidator{}).Run(ctx)
	require.Len(t, diags, 1)
	require.Equal(t, validate.SeverityError, diags[0].Severity)
	require.Contains(t, diags[0].Message, "unknown command")
	require.Contains(t, diags[0].Target, "parallel.steps[0].files-gate")
}

// TestResetFilesGateValidator_UnknownCommand verifies an error diagnostic when
// reset.yml files_gate references a command not in the registry.
func TestResetFilesGateValidator_UnknownCommand(t *testing.T) {
	tmpDir := t.TempDir()

	resetYml := `phases:
  - name: cleanup
    steps:
      - name: check
        type: shell
        cmd: "true"
        files_gate:
          command: db-download
          state: readable
`
	devboxDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(devboxDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(devboxDir, "reset.yml"), []byte(resetYml), 0o644))

	reg := usercommands.NewEmptyRegistry()

	cfg := &config.DweConfig{Raw: map[string]any{}}
	ctx := validate.Context{
		ProjectRoot:     tmpDir,
		CommandRegistry: reg,
		Cfg:             cfg,
	}
	diags := (&resetFilesGateValidator{}).Run(ctx)
	require.Len(t, diags, 1)
	require.Equal(t, validate.SeverityError, diags[0].Severity)
	require.Contains(t, diags[0].Message, "unknown command")
}
