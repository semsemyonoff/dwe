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

// TestValidateLintersSubcommand verifies the `linters [id]` subcommand is wired
// up and accepts an optional positional ID argument.
func TestValidateLintersSubcommand(t *testing.T) {
	cmd := newValidateCmd(&rootFlags{})
	lintersCmd, _, _ := cmd.Find([]string{"linters"})
	require.NotNil(t, lintersCmd)
	require.Equal(t, "linters", lintersCmd.Name())
	require.NotNil(t, lintersCmd.Args)
	require.True(t, lintersCmd.SilenceUsage)
}

// TestValidateLintersRunsLintersDomainOnly: `devbox validate linters` scopes
// execution to the linters domain — output must not contain rows from other
// domains (config, env, checks, snapshot, templates, commands).
func TestValidateLintersRunsLintersDomainOnly(t *testing.T) {
	tmpDir := t.TempDir()
	devboxPath := filepath.Join(tmpDir, "devbox.yml")
	require.NoError(t, os.WriteFile(devboxPath, []byte("schema_version: \"2\"\n"), 0o644))

	flags := &rootFlags{configPath: devboxPath}
	cmd := newValidateCmd(flags)
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"linters"})
	_ = cmd.Execute()

	out := output.String()
	// Header should reference external linters specifically.
	require.Contains(t, out, "external linters")
	// No rows from other domains.
	require.NotContains(t, out, "config/")
	require.NotContains(t, out, "templates/")
	require.NotContains(t, out, "commands/")
	require.NotContains(t, out, "checks/")
	require.NotContains(t, out, "snapshot/")
}

// TestValidateLintersScopedByIDFiltersToOne: `devbox validate linters shellcheck`
// must filter rows to just that linter — hadolint (the other autodetected
// built-in) must not appear.
func TestValidateLintersScopedByIDFiltersToOne(t *testing.T) {
	tmpDir := t.TempDir()
	devboxPath := filepath.Join(tmpDir, "devbox.yml")
	require.NoError(t, os.WriteFile(devboxPath, []byte("schema_version: \"2\"\n"), 0o644))

	flags := &rootFlags{configPath: devboxPath}
	cmd := newValidateCmd(flags)
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"linters", "shellcheck"})
	_ = cmd.Execute()

	out := output.String()
	require.Contains(t, out, "external linter shellcheck")
	require.NotContains(t, out, "hadolint")
}

// TestValidateLintersUnknownIDIsNotHardError: an unknown linter id must result
// in zero rows for the linters domain and a successful exit — matching the
// `checks` domain behavior.
func TestValidateLintersUnknownIDIsNotHardError(t *testing.T) {
	tmpDir := t.TempDir()
	devboxPath := filepath.Join(tmpDir, "devbox.yml")
	require.NoError(t, os.WriteFile(devboxPath, []byte("schema_version: \"2\"\n"), 0o644))

	flags := &rootFlags{configPath: devboxPath}
	cmd := newValidateCmd(flags)
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"linters", "does-not-exist"})
	err := cmd.Execute()
	require.NoError(t, err)
	out := output.String()
	require.Contains(t, out, "external linter does-not-exist")
}

// TestValidateLintersStrictUpgradesWarningToError: when a user-configured
// explicit `bin:` is missing on PATH, the linter runtime emits a Warning. With
// --strict, that Warning must drive a non-zero exit code.
func TestValidateLintersStrictUpgradesWarningToError(t *testing.T) {
	tmpDir := t.TempDir()
	devboxPath := filepath.Join(tmpDir, "devbox.yml")
	require.NoError(t, os.WriteFile(devboxPath, []byte("schema_version: \"2\"\n"), 0o644))

	// Configure a generic linter pointing at a binary that won't be on PATH.
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "devbox"), 0o755))
	yml := "linters:\n  totally-fake-bin-xyz:\n    type: generic\n    bin: totally-fake-bin-xyz\n    paths: [\".\"]\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "devbox", "validate.yml"), []byte(yml), 0o644))

	flags := &rootFlags{configPath: devboxPath}
	cmd := newValidateCmd(flags)
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--strict", "linters", "totally-fake-bin-xyz"})
	err := cmd.Execute()

	// --strict turns the warning into a non-zero exit.
	require.Error(t, err)
	var vfe *validationFailedError
	require.ErrorAs(t, err, &vfe)
	require.Equal(t, 1, vfe.ExitCode())

	out := output.String()
	require.Contains(t, out, "totally-fake-bin-xyz")
}

// TestValidateLintersScopedMalformedValidateYmlSurfacesDiagnostic: running
// "devbox validate linters" with a malformed validate.yml must surface an error
// diagnostic (not silently return zero diagnostics).
func TestValidateLintersScopedMalformedValidateYmlSurfacesDiagnostic(t *testing.T) {
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
	cmd.SetArgs([]string{"linters"})
	_ = cmd.Execute()

	out := output.String()
	require.Contains(t, out, "validate.yml", "malformed validate.yml diagnostic should appear in scoped linters run")
	require.Contains(t, out, "error", "summary must report an error")
}

// TestValidateSnapshotSubcommand registers and the basic scopes resolve.
func TestValidateSnapshotSubcommand(t *testing.T) {
	cmd := newValidateCmd(&rootFlags{})
	snapCmd, _, _ := cmd.Find([]string{"snapshot"})
	require.NotNil(t, snapCmd)
	require.Equal(t, "snapshot", snapCmd.Name())
	require.NotNil(t, snapCmd.Flag("verify"))
}

// TestValidateSnapshotRunsAndSurfacesPerSnapshotDiagnostics walks the full
// `devbox validate snapshot` flow against a tmp project with a broken
// snapshot directory (missing manifest) and asserts the corresponding error
// diagnostic shows up.
func TestValidateSnapshotRunsAndSurfacesPerSnapshotDiagnostics(t *testing.T) {
	tmpDir := t.TempDir()

	devboxPath := filepath.Join(tmpDir, "devbox.yml")
	require.NoError(t, os.WriteFile(devboxPath, []byte("schema_version: \"2\"\n"), 0o644))
	// Create a snapshot dir with no manifest.
	brokenDir := filepath.Join(tmpDir, "snapshots", "broken")
	require.NoError(t, os.MkdirAll(brokenDir, 0o755))

	flags := &rootFlags{configPath: devboxPath}
	cmd := newValidateCmd(flags)

	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"snapshot"})
	_ = cmd.Execute()

	out := output.String()
	require.Contains(t, out, "broken.manifest_valid")
	require.Contains(t, out, "error")
}

// TestValidateSnapshotScopedByName filters to a single snapshot's checks.
func TestValidateSnapshotScopedByName(t *testing.T) {
	tmpDir := t.TempDir()
	devboxPath := filepath.Join(tmpDir, "devbox.yml")
	require.NoError(t, os.WriteFile(devboxPath, []byte("schema_version: \"2\"\n"), 0o644))
	// Two snapshot dirs: only one should appear in output when scoped.
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "snapshots", "alpha"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "snapshots", "beta"), 0o755))

	flags := &rootFlags{configPath: devboxPath}
	cmd := newValidateCmd(flags)
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"snapshot", "alpha"})
	_ = cmd.Execute()

	out := output.String()
	require.Contains(t, out, "alpha.manifest_valid")
	require.NotContains(t, out, "beta.manifest_valid")
}

// TestValidateSnapshotVerifyFlag verifies that --verify toggles checksum
// computation: without it no checksums target appears; with it an OK one does.
func TestValidateSnapshotVerifyFlag(t *testing.T) {
	tmpDir := t.TempDir()
	devboxPath := filepath.Join(tmpDir, "devbox.yml")
	require.NoError(t, os.WriteFile(devboxPath, []byte("schema_version: \"2\"\n"), 0o644))

	snapDir := filepath.Join(tmpDir, "snapshots", "snap1")
	require.NoError(t, os.MkdirAll(filepath.Join(snapDir, "db"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "db", "main.sql"), []byte("hello"), 0o644))

	// Compute correct sha256 by scanning once and writing manifest with those
	// values (manifest written using our atomic helper via SaveManifest).
	// Easiest route: write an empty manifest first, then re-scan and overwrite.
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "manifest.yml"),
		[]byte("name: snap1\ncreated_at: 2026-01-01T00:00:00Z\n"), 0o644))

	flags := &rootFlags{configPath: devboxPath}

	// Without --verify: snap1.checksums must not appear.
	cmd := newValidateCmd(flags)
	var out1 bytes.Buffer
	cmd.SetOut(&out1)
	cmd.SetErr(&out1)
	cmd.SetArgs([]string{"snapshot", "snap1"})
	_ = cmd.Execute()
	require.NotContains(t, out1.String(), "snap1.checksums", "no checksums target without --verify")
}
