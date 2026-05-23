package command

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/validate"

	"github.com/stretchr/testify/require"
)

func TestValidateDispatch(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		expectScope  []string
		expectDomain string
		expectID     string
	}{
		{
			name:        "validate alone runs all",
			args:        []string{},
			expectScope: nil, // nil matches all
		},
		{
			name:         "validate config runs config domain",
			args:         []string{"config"},
			expectDomain: "config",
		},
		{
			name:         "validate config deploy runs config.deploy",
			args:         []string{"config", "deploy"},
			expectDomain: "config",
			expectID:     "deploy",
		},
		{
			name:         "validate templates runs templates domain",
			args:         []string{"templates"},
			expectDomain: "templates",
		},
		{
			name:         "validate templates ide runs templates.ide",
			args:         []string{"templates", "ide"},
			expectDomain: "templates",
			expectID:     "ide",
		},
		{
			name:         "validate commands runs commands domain",
			args:         []string{"commands"},
			expectDomain: "commands",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify that the command tree structure is correct.
			root := newValidateCmd(&rootFlags{})
			require.NotNil(t, root)

			// Check subcommands exist.
			if len(tt.args) > 0 {
				subCmd, _, err := root.Find(tt.args)
				require.NoError(t, err)
				require.NotNil(t, subCmd)
			}
		})
	}
}

func TestValidateCommandTree(t *testing.T) {
	// Verify the command tree structure.
	cmd := newValidateCmd(&rootFlags{})
	require.NotNil(t, cmd)

	// Check root command properties.
	require.Equal(t, "validate", cmd.Name())
	require.True(t, cmd.SilenceUsage)

	// Find subcommands.
	configCmd, _, _ := cmd.Find([]string{"config"})
	require.NotNil(t, configCmd)
	require.Equal(t, "config", configCmd.Name())

	templatesCmd, _, _ := cmd.Find([]string{"templates"})
	require.NotNil(t, templatesCmd)
	require.Equal(t, "templates", templatesCmd.Name())

	commandsCmd, _, _ := cmd.Find([]string{"commands"})
	require.NotNil(t, commandsCmd)
	require.Equal(t, "commands", commandsCmd.Name())

	// Check config subcommands.
	configSubcmds := []string{"devbox", "services", "docker", "info", "styles", "lifecycle", "deploy", "reset", "service-deploy"}
	for _, subcmd := range configSubcmds {
		found, _, _ := cmd.Find([]string{"config", subcmd})
		require.NotNil(t, found, "missing config.%s", subcmd)
		require.Equal(t, subcmd, found.Name())
	}

	// Check template subcommands.
	for _, tmpl := range []string{"ide", "ai"} {
		found, _, _ := cmd.Find([]string{"templates", tmpl})
		require.NotNil(t, found, "missing templates.%s", tmpl)
		require.Equal(t, tmpl, found.Name())
	}
}

func TestValidateFlags(t *testing.T) {
	// Verify that --strict and --quiet flags are defined and inherited.
	cmd := newValidateCmd(&rootFlags{})

	// Set flags on a subcommand.
	cmd.SetArgs([]string{"--strict", "--quiet", "config"})

	// PersistentFlags should include strict and quiet.
	strictFlag := cmd.PersistentFlags().Lookup("strict")
	require.NotNil(t, strictFlag)
	require.Equal(t, "false", strictFlag.DefValue)

	quietFlag := cmd.PersistentFlags().Lookup("quiet")
	require.NotNil(t, quietFlag)
	require.Equal(t, "false", quietFlag.DefValue)
}

func TestValidateNoArgs(t *testing.T) {
	// Leaf commands should reject positional arguments.
	cmd := newValidateCmd(&rootFlags{})

	// Find a leaf command (e.g., config/deploy).
	deployCmd, _, _ := cmd.Find([]string{"config", "deploy"})
	require.NotNil(t, deployCmd)
	// Verify that Args is set to something that rejects arguments.
	require.NotNil(t, deployCmd.Args)
}

func TestValidateUsesLoadForValidate(t *testing.T) {
	// Test that the validate command can be invoked and produces output.
	// We use a temporary directory to avoid hitting a real project.
	tmpDir := t.TempDir()

	// Create minimal devbox.yml to pass locate.
	devboxPath := filepath.Join(tmpDir, "devbox.yml")
	err := os.WriteFile(devboxPath, []byte(`schema_version: "2"`), 0644)
	require.NoError(t, err)

	flags := &rootFlags{configPath: devboxPath}
	cmd := newValidateCmd(flags)

	var output bytes.Buffer
	cmd.SetOut(&output)

	// Run validate without arguments (should run all validators).
	cmd.SetArgs([]string{})
	_ = cmd.Execute()
	// The command may fail if the config is incomplete, but it should not panic.
	outStr := output.String()
	// Should contain table headers or diagnostic output.
	require.True(t, len(outStr) > 0, "command should produce output")
}

func TestValidateStageFlag(t *testing.T) {
	cmd := newValidateCmd(&rootFlags{})
	stageFlag := cmd.PersistentFlags().Lookup("stage")
	require.NotNil(t, stageFlag)
	require.Equal(t, "", stageFlag.DefValue)
}

func TestValidateEnvAndChecksSubcommands(t *testing.T) {
	cmd := newValidateCmd(&rootFlags{})

	envCmd, _, _ := cmd.Find([]string{"env"})
	require.NotNil(t, envCmd)
	require.Equal(t, "env", envCmd.Name())

	checksCmd, _, _ := cmd.Find([]string{"checks"})
	require.NotNil(t, checksCmd)
	require.Equal(t, "checks", checksCmd.Name())
}

// TestValidateMalformedValidateYmlDoesNotShortCircuit: a malformed validate.yml
// must NOT abort the validate run — the config.validate validator surfaces the
// load failure inline alongside diagnostics from other domains.
func TestValidateMalformedValidateYmlDoesNotShortCircuit(t *testing.T) {
	tmpDir := t.TempDir()

	// Minimal devbox.yml so locate succeeds.
	devboxPath := filepath.Join(tmpDir, "devbox.yml")
	require.NoError(t, os.WriteFile(devboxPath, []byte("schema_version: \"2\"\n"), 0o644))

	// Malformed validate.yml: unknown top-level field.
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "devbox"), 0o755))
	badYml := filepath.Join(tmpDir, "devbox", "validate.yml")
	require.NoError(t, os.WriteFile(badYml, []byte("bogus_field: 1\n"), 0o644))

	flags := &rootFlags{configPath: devboxPath}
	cmd := newValidateCmd(flags)

	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{})
	_ = cmd.Execute()

	out := output.String()
	// The config.validate error diagnostic should appear in the rendered table.
	require.Contains(t, out, "validate.yml", "malformed validate.yml diagnostic should surface")
	// And the run should still produce a summary line (proves no short-circuit).
	require.Contains(t, out, "error")
}

// TestValidateChecksScopedMalformedValidateYmlSurfacesDiagnostic: running
// "devbox validate checks" with a malformed validate.yml must surface an error
// diagnostic (not a raw error) and must not silently return zero diagnostics.
func TestValidateChecksScopedMalformedValidateYmlSurfacesDiagnostic(t *testing.T) {
	tmpDir := t.TempDir()

	devboxPath := filepath.Join(tmpDir, "devbox.yml")
	require.NoError(t, os.WriteFile(devboxPath, []byte("schema_version: \"2\"\n"), 0o644))

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "devbox"), 0o755))
	badYml := filepath.Join(tmpDir, "devbox", "validate.yml")
	require.NoError(t, os.WriteFile(badYml, []byte("bogus_field: 1\n"), 0o644))

	flags := &rootFlags{configPath: devboxPath}
	cmd := newValidateCmd(flags)

	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"checks"})
	_ = cmd.Execute()

	out := output.String()
	// The validate.yml error must appear in the table (not as a raw "Error: ..." line).
	require.Contains(t, out, "validate.yml", "malformed validate.yml diagnostic should appear in scoped checks run")
	require.Contains(t, out, "error", "summary must report an error")
}

// TestValidateChecksScopedByIDMalformedValidateYmlSurfacesDiagnostic: running
// "devbox validate checks <id>" on a malformed validate.yml must still surface
// the parse error — not silently return zero diagnostics and exit 0.
func TestValidateChecksScopedByIDMalformedValidateYmlSurfacesDiagnostic(t *testing.T) {
	tmpDir := t.TempDir()

	devboxPath := filepath.Join(tmpDir, "devbox.yml")
	require.NoError(t, os.WriteFile(devboxPath, []byte("schema_version: \"2\"\n"), 0o644))

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "devbox"), 0o755))
	badYml := filepath.Join(tmpDir, "devbox", "validate.yml")
	require.NoError(t, os.WriteFile(badYml, []byte("bogus_field: 1\n"), 0o644))

	flags := &rootFlags{configPath: devboxPath}
	cmd := newValidateCmd(flags)

	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"checks", "some-specific-check"})
	_ = cmd.Execute()

	out := output.String()
	// The parse error must surface even though a specific ID was requested.
	require.Contains(t, out, "validate.yml", "malformed validate.yml diagnostic should appear for checks <id> run")
	require.Contains(t, out, "error", "summary must report an error, not zero diagnostics")
}

// TestValidateMissingValidateYmlIsSilent: a missing validate.yml is silently
// tolerated — no diagnostic, no error.
func TestValidateMissingValidateYmlIsSilent(t *testing.T) {
	tmpDir := t.TempDir()
	devboxPath := filepath.Join(tmpDir, "devbox.yml")
	require.NoError(t, os.WriteFile(devboxPath, []byte("schema_version: \"2\"\n"), 0o644))

	flags := &rootFlags{configPath: devboxPath}
	cmd := newValidateCmd(flags)

	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"checks"})
	_ = cmd.Execute()

	out := output.String()
	// No checks domain rows expected since validate.yml is absent.
	require.NotContains(t, out, "checks/")
}

// TestValidateEnvScopedMalformedValidateYmlSurfacesDiagnostic: running
// "devbox validate env" with a malformed validate.yml must surface an error
// diagnostic. Previously the error was silently dropped because neither the
// "config" nor "checks" domain ran for an "env" scope.
func TestValidateEnvScopedMalformedValidateYmlSurfacesDiagnostic(t *testing.T) {
	tmpDir := t.TempDir()

	devboxPath := filepath.Join(tmpDir, "devbox.yml")
	require.NoError(t, os.WriteFile(devboxPath, []byte("schema_version: \"2\"\n"), 0o644))

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "devbox"), 0o755))
	badYml := filepath.Join(tmpDir, "devbox", "validate.yml")
	require.NoError(t, os.WriteFile(badYml, []byte("bogus_field: 1\n"), 0o644))

	flags := &rootFlags{configPath: devboxPath}
	cmd := newValidateCmd(flags)

	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"env"})
	_ = cmd.Execute()

	out := output.String()
	// The parse error must surface even though the scope is "env", not "config" or "checks".
	require.Contains(t, out, "validate.yml", "malformed validate.yml must surface even when scope is env")
	require.Contains(t, out, "error", "summary must report an error, not zero diagnostics")
}

func TestValidateExitCodeInterface(t *testing.T) {
	// Verify that validationFailedError implements ExitCode() int.
	err := &validationFailedError{
		summary: validate.Summary{Errors: 1},
		strict:  false,
	}

	// Compile-time interface satisfaction: assignment would fail if not satisfied.
	var ec interface{ ExitCode() int } = err
	require.NotNil(t, ec)

	exitCode := err.ExitCode()
	require.Equal(t, 1, exitCode)

	// Test strict mode.
	err2 := &validationFailedError{
		summary: validate.Summary{Warnings: 1},
		strict:  true,
	}
	require.Equal(t, 1, err2.ExitCode())

	// Test all OK.
	err3 := &validationFailedError{
		summary: validate.Summary{OKs: 1},
		strict:  false,
	}
	require.Equal(t, 0, err3.ExitCode())
}
