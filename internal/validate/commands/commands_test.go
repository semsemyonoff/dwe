package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/usercommands/registry"
	"devbox-cli/internal/validate"
)

func TestCommandsValidator(t *testing.T) {
	tests := []struct {
		name      string
		buildDir  func(t *testing.T) string
		checkDiag func(*testing.T, []validate.Diagnostic)
	}{
		{
			name: "empty_commands_directory",
			buildDir: func(t *testing.T) string {
				dir := t.TempDir()
				cmdDir := filepath.Join(dir, "devbox", "commands")
				require.NoError(t, os.MkdirAll(cmdDir, 0o755))
				return dir
			},
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				require.Len(t, diags, 1)
				require.Equal(t, validate.SeverityOK, diags[0].Severity)
				require.Equal(t, "commands", diags[0].Target)
			},
		},
		{
			name: "good_command_file",
			buildDir: func(t *testing.T) string {
				dir := t.TempDir()
				cmdDir := filepath.Join(dir, "devbox", "commands")
				require.NoError(t, os.MkdirAll(cmdDir, 0o755))

				// Create a valid command file
				cmdFile := filepath.Join(cmdDir, "test.yml")
				content := `commands:
  test:
    description: Test command
    type: shell
    cmd: echo hello
`
				require.NoError(t, os.WriteFile(cmdFile, []byte(content), 0o644))
				return dir
			},
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				require.Len(t, diags, 1)
				require.Equal(t, validate.SeverityOK, diags[0].Severity)
				require.Contains(t, diags[0].Message, "command files valid")
			},
		},
		{
			name: "workflow_missing_command",
			buildDir: func(t *testing.T) string {
				dir := t.TempDir()
				cmdDir := filepath.Join(dir, "devbox", "commands")
				require.NoError(t, os.MkdirAll(cmdDir, 0o755))

				// Create a workflow that references a non-existent command
				cmdFile := filepath.Join(cmdDir, "workflow.yml")
				content := `commands:
  my-workflow:
    description: My workflow
    type: workflow
    steps:
      - command: nonexistent-cmd
`
				require.NoError(t, os.WriteFile(cmdFile, []byte(content), 0o644))
				return dir
			},
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				require.Len(t, diags, 1)
				require.Equal(t, validate.SeverityError, diags[0].Severity)
				// The full command ID includes the group prefix from the file path
				require.Equal(t, "commands:workflow.my-workflow", diags[0].Target)
				require.Contains(t, diags[0].Message, "references unknown command")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectRoot := tt.buildDir(t)
			v := &CommandsValidator{}
			ctx := validate.Context{
				ProjectRoot: projectRoot,
				Cfg:         nil,
			}
			diags := v.Run(ctx)
			tt.checkDiag(t, diags)
		})
	}
}

func TestCommandsValidatorID(t *testing.T) {
	v := &CommandsValidator{}
	require.Equal(t, "commands", v.ID())
	require.Equal(t, "commands", v.Domain())
}

func TestAllFunction(t *testing.T) {
	validators := All()
	require.Len(t, validators, 1)
	require.Equal(t, "commands", validators[0].ID())
}

// Test that BuildRegistryFromParsed works correctly
func TestBuildRegistryFromParsed(t *testing.T) {
	cmd1 := &model.CommandDef{
		ID:        "cmd1",
		LocalName: "cmd1",
		Group:     "",
		Type:      model.CommandTypeShell,
	}
	cmd2 := &model.CommandDef{
		ID:        "cmd2",
		LocalName: "cmd2",
		Group:     "",
		Type:      model.CommandTypeWorkflow,
		Steps: []model.WorkflowStep{
			{Command: "cmd1"},
		},
	}

	cf1 := &model.CommandFile{
		Commands: map[string]model.CommandDef{
			"cmd1": *cmd1,
		},
	}
	cf2 := &model.CommandFile{
		Commands: map[string]model.CommandDef{
			"cmd2": *cmd2,
		},
	}

	reg, err := registry.BuildRegistryFromParsed([]*model.CommandFile{cf1, cf2})
	require.NoError(t, err)
	require.NotNil(t, reg)

	// Verify diagnostics are empty (valid registry)
	issues := reg.Diagnostics()
	require.Empty(t, issues)
}

// Test duplicate command IDs
func TestBuildRegistryFromParsedDuplicates(t *testing.T) {
	cmd1 := &model.CommandDef{
		ID:        "cmd1",
		LocalName: "cmd1",
		Group:     "",
		Type:      model.CommandTypeShell,
	}

	cf1 := &model.CommandFile{
		Commands: map[string]model.CommandDef{
			"cmd1": *cmd1,
		},
	}
	cf2 := &model.CommandFile{
		Commands: map[string]model.CommandDef{
			"cmd1": *cmd1,
		},
	}

	_, err := registry.BuildRegistryFromParsed([]*model.CommandFile{cf1, cf2})
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate command ID")
}
