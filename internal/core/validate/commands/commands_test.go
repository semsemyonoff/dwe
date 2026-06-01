package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func TestValidator(t *testing.T) {
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
		{
			name: "reserved_top_level_id_list_warns",
			buildDir: func(t *testing.T) string {
				dir := t.TempDir()
				cmdDir := filepath.Join(dir, "devbox", "commands")
				require.NoError(t, os.MkdirAll(cmdDir, 0o755))
				// A file named ".yml" produces an empty group, so the command id
				// equals its local name — the only way to author a root-level id.
				cmdFile := filepath.Join(cmdDir, ".yml")
				content := `commands:
  list:
    description: shadows reserved subcommand
    type: shell
    cmd: echo hi
`
				require.NoError(t, os.WriteFile(cmdFile, []byte(content), 0o644))
				return dir
			},
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				var found bool
				for _, d := range diags {
					if d.Severity == validate.SeverityWarning &&
						d.Target == "commands:list" &&
						strings.Contains(d.Message, `command id "list" conflicts with the reserved subcommand "devbox commands list"`) &&
						strings.Contains(d.Message, "interactive browser") {
						found = true
					}
				}
				require.True(t, found, "expected reserved-id warning for list; got: %#v", diags)
			},
		},
		{
			name: "grouped_list_id_does_not_warn",
			buildDir: func(t *testing.T) string {
				dir := t.TempDir()
				cmdDir := filepath.Join(dir, "devbox", "commands")
				require.NoError(t, os.MkdirAll(cmdDir, 0o755))
				cmdFile := filepath.Join(cmdDir, "services.yml")
				content := `commands:
  list:
    description: grouped list is fine
    type: shell
    cmd: echo hi
`
				require.NoError(t, os.WriteFile(cmdFile, []byte(content), 0o644))
				return dir
			},
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				for _, d := range diags {
					if d.Severity == validate.SeverityWarning && strings.Contains(d.Message, "reserved subcommand") {
						t.Fatalf("did not expect reserved warning for services.list; got: %#v", d)
					}
				}
			},
		},
		{
			name: "no_reserved_id_no_warning",
			buildDir: func(t *testing.T) string {
				dir := t.TempDir()
				cmdDir := filepath.Join(dir, "devbox", "commands")
				require.NoError(t, os.MkdirAll(cmdDir, 0o755))
				cmdFile := filepath.Join(cmdDir, "ok.yml")
				content := `commands:
  hello:
    description: ok
    type: shell
    cmd: echo hi
`
				require.NoError(t, os.WriteFile(cmdFile, []byte(content), 0o644))
				return dir
			},
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				for _, d := range diags {
					if d.Severity == validate.SeverityWarning && strings.Contains(d.Message, "reserved subcommand") {
						t.Fatalf("did not expect reserved warning; got: %#v", d)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectRoot := tt.buildDir(t)
			v := &Validator{}
			ctx := validate.Context{
				ProjectRoot: projectRoot,
				Cfg:         nil,
			}
			diags := v.Run(ctx)
			tt.checkDiag(t, diags)
		})
	}
}

func TestWorkflowParallelDiagnostics(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		checkDiag func(*testing.T, []validate.Diagnostic)
	}{
		{
			name: "nested_parallel_rejected",
			yaml: `commands:
  outer:
    description: outer workflow
    type: workflow
    steps:
      - parallel:
          steps:
            - command: workflow.inner
            - parallel:
                steps:
                  - command: workflow.leaf-a
                  - command: workflow.leaf-b
  inner:
    description: inner workflow
    type: workflow
    steps:
      - command: workflow.leaf-a
  leaf-a:
    description: leaf
    type: shell
    cmd: echo a
  leaf-b:
    description: leaf
    type: shell
    cmd: echo b
`,
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				var found bool
				for _, d := range diags {
					if d.Severity == validate.SeverityError &&
						d.Target == "commands:workflow.outer" &&
						strings.Contains(d.Message, "step[0].parallel.steps[1]: nested parallel is not supported") {
						found = true
					}
				}
				require.True(t, found, "expected nested-parallel diagnostic with path; got: %#v", diags)
			},
		},
		{
			name: "confirm_in_parallel_rejected",
			yaml: `commands:
  bad:
    description: bad workflow
    type: workflow
    steps:
      - parallel:
          steps:
            - command: workflow.leaf-a
            - confirm: are you sure
  leaf-a:
    description: leaf
    type: shell
    cmd: echo a
`,
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				var found bool
				for _, d := range diags {
					if d.Severity == validate.SeverityError &&
						strings.Contains(d.Message, "step[0].parallel.steps[1]: confirm is not allowed inside a parallel group") {
						found = true
					}
				}
				require.True(t, found, "expected confirm-in-parallel diagnostic; got: %#v", diags)
			},
		},
		{
			name: "with_on_parallel_container_rejected",
			yaml: `commands:
  bad:
    description: bad workflow
    type: workflow
    steps:
      - with:
          foo: bar
        parallel:
          steps:
            - command: workflow.leaf-a
            - command: workflow.leaf-b
  leaf-a:
    description: leaf
    type: shell
    cmd: echo a
  leaf-b:
    description: leaf
    type: shell
    cmd: echo b
`,
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				var found bool
				for _, d := range diags {
					if d.Severity == validate.SeverityError &&
						strings.Contains(d.Message, "step[0]: with may not be combined with parallel") {
						found = true
					}
				}
				require.True(t, found, "expected with-on-container diagnostic; got: %#v", diags)
			},
		},
		{
			name: "parallel_steps_too_few_rejected",
			yaml: `commands:
  bad:
    description: bad workflow
    type: workflow
    steps:
      - parallel:
          steps:
            - command: workflow.leaf-a
  leaf-a:
    description: leaf
    type: shell
    cmd: echo a
`,
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				var found bool
				for _, d := range diags {
					if d.Severity == validate.SeverityError &&
						strings.Contains(d.Message, "step[0].parallel.steps: must contain at least 2 sub-steps") {
						found = true
					}
				}
				require.True(t, found, "expected parallel-steps-too-few diagnostic; got: %#v", diags)
			},
		},
		{
			name: "unknown_command_in_parallel_steps_rejected",
			yaml: `commands:
  bad:
    description: bad workflow
    type: workflow
    steps:
      - parallel:
          steps:
            - command: workflow.leaf-a
            - command: workflow.does-not-exist
  leaf-a:
    description: leaf
    type: shell
    cmd: echo a
`,
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				var found bool
				for _, d := range diags {
					if d.Severity == validate.SeverityError &&
						d.Target == "commands:workflow.bad" &&
						strings.Contains(d.Message, "step[0].parallel.steps[1]") &&
						strings.Contains(d.Message, "references unknown command") {
						found = true
					}
				}
				require.True(t, found, "expected path-qualified unknown-command diagnostic; got: %#v", diags)
			},
		},
		{
			// A workflow with a parallel-structural violation (nested parallel)
			// must surface the structural error. Per-type allowlist validation
			// at parse time prevents disallowed fields from being parsed.
			name: "parallel_violation_plus_non_step_field_violation",
			yaml: `commands:
  bad:
    description: bad workflow
    type: workflow
    steps:
      - parallel:
          steps:
            - command: workflow.leaf-a
            - parallel:
                steps:
                  - command: workflow.leaf-a
                  - command: workflow.leaf-a
  leaf-a:
    description: leaf
    type: shell
    cmd: echo a
`,
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				var foundNested bool
				for _, d := range diags {
					if d.Severity == validate.SeverityError && strings.Contains(d.Message, "nested parallel is not supported") {
						foundNested = true
					}
				}
				require.True(t, foundNested, "expected nested-parallel diagnostic; got: %#v", diags)
			},
		},
		{
			name: "valid_parallel_workflow_has_no_diagnostics",
			yaml: `commands:
  good:
    description: good workflow
    type: workflow
    steps:
      - parallel:
          steps:
            - command: workflow.leaf-a
            - command: workflow.leaf-b
  leaf-a:
    description: leaf
    type: shell
    cmd: echo a
  leaf-b:
    description: leaf
    type: shell
    cmd: echo b
`,
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				for _, d := range diags {
					require.NotEqual(t, validate.SeverityError, d.Severity, "unexpected error diagnostic: %#v", d)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cmdDir := filepath.Join(dir, "devbox", "commands")
			require.NoError(t, os.MkdirAll(cmdDir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(cmdDir, "workflow.yml"), []byte(tc.yaml), 0o644))

			v := &Validator{}
			diags := v.Run(validate.Context{ProjectRoot: dir})
			tc.checkDiag(t, diags)
		})
	}
}

func TestValidatorID(t *testing.T) {
	v := &Validator{}
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

// TestParamValidationDiagnostics tests param widget/options validation with categorized diagnostics.
func TestParamValidationDiagnostics(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		checkDiag func(*testing.T, []validate.Diagnostic)
	}{
		{
			name: "select_without_options_rejected",
			yaml: `commands:
  test:
    type: shell
    cmd: echo hi
    params:
      db:
        type: string
        widget: select
`,
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				var found bool
				for _, d := range diags {
					if d.Severity == validate.SeverityError &&
						strings.Contains(d.Target, "params.db") &&
						strings.Contains(d.Message, "widget select requires non-empty options") {
						found = true
					}
				}
				require.True(t, found, "expected select-without-options error; got: %#v", diags)
			},
		},
		{
			name: "input_with_options_rejected",
			yaml: `commands:
  test:
    type: shell
    cmd: echo hi
    params:
      format:
        type: string
        widget: input
        options: [json, yaml]
`,
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				var found bool
				for _, d := range diags {
					if d.Severity == validate.SeverityError &&
						strings.Contains(d.Target, "params.format") &&
						strings.Contains(d.Message, "widget input does not accept options") {
						found = true
					}
				}
				require.True(t, found, "expected input-with-options error; got: %#v", diags)
			},
		},
		{
			name: "invalid_widget_value_rejected",
			yaml: `commands:
  test:
    type: shell
    cmd: echo hi
    params:
      choice:
        type: string
        widget: invalid
`,
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				var found bool
				for _, d := range diags {
					if d.Severity == validate.SeverityError &&
						strings.Contains(d.Target, "params.choice") &&
						strings.Contains(d.Message, "must be one of") {
						found = true
					}
				}
				require.True(t, found, "expected invalid-widget error; got: %#v", diags)
			},
		},
		{
			name: "pattern_with_options_rejected",
			yaml: `commands:
  test:
    type: shell
    cmd: echo hi
    params:
      email:
        type: string
        pattern: '^[^@]+@[^@]+$'
        options: [a@b.com, c@d.com]
`,
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				var found bool
				for _, d := range diags {
					if d.Severity == validate.SeverityError &&
						strings.Contains(d.Target, "params.email") &&
						strings.Contains(d.Message, "pattern and options are mutually exclusive") {
						found = true
					}
				}
				require.True(t, found, "expected pattern-with-options error; got: %#v", diags)
			},
		},
		{
			name: "separator_on_input_rejected",
			yaml: `commands:
  test:
    type: shell
    cmd: echo hi
    params:
      name:
        type: string
        widget: input
        separator: ","
`,
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				var found bool
				for _, d := range diags {
					if d.Severity == validate.SeverityError &&
						strings.Contains(d.Target, "params.name") &&
						strings.Contains(d.Message, "separator") &&
						strings.Contains(d.Message, "multiselect") {
						found = true
					}
				}
				require.True(t, found, "expected separator-on-input error; got: %#v", diags)
			},
		},
		{
			name: "duplicate_option_values_rejected",
			yaml: `commands:
  test:
    type: shell
    cmd: echo hi
    params:
      db:
        type: string
        widget: select
        options: [mysql, postgres, mysql]
`,
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				var found bool
				for _, d := range diags {
					if d.Severity == validate.SeverityError &&
						strings.Contains(d.Target, "params.db") &&
						strings.Contains(d.Message, "duplicate option value") {
						found = true
					}
				}
				require.True(t, found, "expected duplicate-options error; got: %#v", diags)
			},
		},
		{
			name: "default_not_in_options_rejected",
			yaml: `commands:
  test:
    type: shell
    cmd: echo hi
    params:
      db:
        type: string
        widget: select
        options: [mysql, postgres]
        default: mongodb
`,
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				var found bool
				for _, d := range diags {
					if d.Severity == validate.SeverityError &&
						strings.Contains(d.Target, "params.db") &&
						strings.Contains(d.Message, "default") &&
						strings.Contains(d.Message, "not found in static options") {
						found = true
					}
				}
				require.True(t, found, "expected default-not-in-options error; got: %#v", diags)
			},
		},
		{
			name: "valid_select_with_options_accepted",
			yaml: `commands:
  test:
    type: shell
    cmd: echo hi
    params:
      db:
        type: string
        widget: select
        options: [mysql, postgres]
        default: mysql
`,
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				for _, d := range diags {
					if d.Severity == validate.SeverityError && strings.Contains(d.Message, "params.db") {
						t.Fatalf("unexpected param error: %#v", d)
					}
				}
			},
		},
		{
			name: "valid_multiselect_with_separator_accepted",
			yaml: `commands:
  test:
    type: shell
    cmd: echo hi
    params:
      services:
        type: string
        widget: multiselect
        options: [web, db, cache]
        separator: ","
`,
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				for _, d := range diags {
					if d.Severity == validate.SeverityError && strings.Contains(d.Message, "params.services") {
						t.Fatalf("unexpected param error: %#v", d)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cmdDir := filepath.Join(dir, "devbox", "commands")
			require.NoError(t, os.MkdirAll(cmdDir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(cmdDir, "test.yml"), []byte(tc.yaml), 0o644))

			v := &Validator{}
			diags := v.Run(validate.Context{ProjectRoot: dir})
			tc.checkDiag(t, diags)
		})
	}
}
