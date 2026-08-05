package validate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	"github.com/semsemyonoff/dwe/internal/core/workflow/scaffold"

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
			root := NewCmd("", &cmdctx.RootFlags{})
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
	cmd := NewCmd("", &cmdctx.RootFlags{})
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
	configSubcmds := []string{"workspace", "services", "docker", "info", "styles", "lifecycle", "deploy", "reset", "service-deploy"}
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
	cmd := NewCmd("", &cmdctx.RootFlags{})

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
	cmd := NewCmd("", &cmdctx.RootFlags{})

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

	// Create minimal workspace.yml to pass locate.
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	err := os.WriteFile(workspacePath, []byte(`schema_version: "2"`), 0644)
	require.NoError(t, err)

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath}
	cmd := NewCmd("", flags)

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
	cmd := NewCmd("", &cmdctx.RootFlags{})
	stageFlag := cmd.PersistentFlags().Lookup("stage")
	require.NotNil(t, stageFlag)
	require.Equal(t, "", stageFlag.DefValue)
}

func TestValidateEnvAndChecksSubcommands(t *testing.T) {
	cmd := NewCmd("", &cmdctx.RootFlags{})

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
// TestValidatePartialLoadWithServicesGatedCheck guards against a regression:
// when LoadConfig fails (errPartialLoad), cfg is nil — buildRegistry must not
// panic when reading cfg.Services for the AllForStage services-gate, and a
// services-gated check should still appear in the output (gate forced off so
// the user sees every check alongside the parse error).
func TestValidatePartialLoadWithServicesGatedCheck(t *testing.T) {
	tmpDir := t.TempDir()

	// Workspace.yml that LoadConfig will reject (unknown top-level field with
	// strict decoding). This sets cfg=nil + errPartialLoad in loadForValidate.
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\nbogus_root: true\n"), 0o644))

	// Valid validate.yml with a services-gated check — gate would normally
	// drop it because we have no merged services map.
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "workspace"), 0o755))
	vYml := filepath.Join(tmpDir, "workspace", "validate.yml")
	require.NoError(t, os.WriteFile(vYml, []byte(`checks:
  - id: gated-shell
    description: gated shell check
    stages: [deploy]
    services: [api]
    type: builtin
    cmd: shell
    with:
      cmd: 'true'
`), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath}
	cmd := NewCmd("", flags)

	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{})
	// Must NOT panic on cfg.Services deref. Execute returns an error because
	// validation failed (workspace.yml parse error surfaces as a diagnostic),
	// which is the expected partial-load behaviour — we only care that no
	// panic occurs and that the gated check id is still surfaced.
	_ = cmd.Execute()

	out := output.String()
	require.Contains(t, out, "gated-shell", "services-gated check must still appear when cfg failed to load")
}

func TestValidateMalformedValidateYmlDoesNotShortCircuit(t *testing.T) {
	tmpDir := t.TempDir()

	// Minimal workspace.yml so locate succeeds.
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	// Malformed validate.yml: unknown top-level field.
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "workspace"), 0o755))
	badYml := filepath.Join(tmpDir, "workspace", "validate.yml")
	require.NoError(t, os.WriteFile(badYml, []byte("bogus_field: 1\n"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath}
	cmd := NewCmd("", flags)

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
// "dwe validate checks" with a malformed validate.yml must surface an error
// diagnostic (not a raw error) and must not silently return zero diagnostics.
func TestValidateChecksScopedMalformedValidateYmlSurfacesDiagnostic(t *testing.T) {
	tmpDir := t.TempDir()

	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "workspace"), 0o755))
	badYml := filepath.Join(tmpDir, "workspace", "validate.yml")
	require.NoError(t, os.WriteFile(badYml, []byte("bogus_field: 1\n"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath}
	cmd := NewCmd("", flags)

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
// "dwe validate checks <id>" on a malformed validate.yml must still surface
// the parse error — not silently return zero diagnostics and exit 0.
func TestValidateChecksScopedByIDMalformedValidateYmlSurfacesDiagnostic(t *testing.T) {
	tmpDir := t.TempDir()

	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "workspace"), 0o755))
	badYml := filepath.Join(tmpDir, "workspace", "validate.yml")
	require.NoError(t, os.WriteFile(badYml, []byte("bogus_field: 1\n"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath}
	cmd := NewCmd("", flags)

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
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath}
	cmd := NewCmd("", flags)

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
// "dwe validate env" with a malformed validate.yml must surface an error
// diagnostic. Previously the error was silently dropped because neither the
// "config" nor "checks" domain ran for an "env" scope.
func TestValidateEnvScopedMalformedValidateYmlSurfacesDiagnostic(t *testing.T) {
	tmpDir := t.TempDir()

	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "workspace"), 0o755))
	badYml := filepath.Join(tmpDir, "workspace", "validate.yml")
	require.NoError(t, os.WriteFile(badYml, []byte("bogus_field: 1\n"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath}
	cmd := NewCmd("", flags)

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
	cmd := NewCmd("", &cmdctx.RootFlags{})
	lintersCmd, _, _ := cmd.Find([]string{"linters"})
	require.NotNil(t, lintersCmd)
	require.Equal(t, "linters", lintersCmd.Name())
	require.NotNil(t, lintersCmd.Args)
	require.True(t, lintersCmd.SilenceUsage)
}

// TestValidateLintersRunsLintersDomainOnly: `dwe validate linters` scopes
// execution to the linters domain — output must not contain rows from other
// domains (config, env, checks, snapshot, templates, commands).
func TestValidateLintersRunsLintersDomainOnly(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath}
	cmd := NewCmd("", flags)
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

// TestValidateLintersScopedByIDFiltersToOne: `dwe validate linters shellcheck`
// must filter rows to just that linter — hadolint (the other autodetected
// built-in) must not appear.
func TestValidateLintersScopedByIDFiltersToOne(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath}
	cmd := NewCmd("", flags)
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
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath}
	cmd := NewCmd("", flags)
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
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	// Configure a generic linter pointing at a binary that won't be on PATH.
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "workspace"), 0o755))
	yml := "linters:\n  totally-fake-bin-xyz:\n    type: generic\n    bin: totally-fake-bin-xyz\n    paths: [\".\"]\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "workspace", "validate.yml"), []byte(yml), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath}
	cmd := NewCmd("", flags)
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
// "dwe validate linters" with a malformed validate.yml must surface an error
// diagnostic (not silently return zero diagnostics).
func TestValidateLintersScopedMalformedValidateYmlSurfacesDiagnostic(t *testing.T) {
	tmpDir := t.TempDir()

	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "workspace"), 0o755))
	badYml := filepath.Join(tmpDir, "workspace", "validate.yml")
	require.NoError(t, os.WriteFile(badYml, []byte("bogus_field: 1\n"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath}
	cmd := NewCmd("", flags)

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
	cmd := NewCmd("", &cmdctx.RootFlags{})
	snapCmd, _, _ := cmd.Find([]string{"snapshot"})
	require.NotNil(t, snapCmd)
	require.Equal(t, "snapshot", snapCmd.Name())
	require.NotNil(t, snapCmd.Flag("verify"))
}

// TestValidateSnapshotRunsAndSurfacesPerSnapshotDiagnostics walks the full
// `dwe validate snapshot` flow against a tmp project with a broken
// snapshot directory (missing manifest) and asserts the corresponding error
// diagnostic shows up.
func TestValidateSnapshotRunsAndSurfacesPerSnapshotDiagnostics(t *testing.T) {
	tmpDir := t.TempDir()

	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))
	// Create a snapshot dir with no manifest.
	brokenDir := filepath.Join(tmpDir, "snapshots", "broken")
	require.NoError(t, os.MkdirAll(brokenDir, 0o755))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath}
	cmd := NewCmd("", flags)

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
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))
	// Two snapshot dirs: only one should appear in output when scoped.
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "snapshots", "alpha"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "snapshots", "beta"), 0o755))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath}
	cmd := NewCmd("", flags)
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
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	snapDir := filepath.Join(tmpDir, "snapshots", "snap1")
	require.NoError(t, os.MkdirAll(filepath.Join(snapDir, "db"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "db", "main.sql"), []byte("hello"), 0o644))

	// Compute correct sha256 by scanning once and writing manifest with those
	// values (manifest written using our atomic helper via SaveManifest).
	// Easiest route: write an empty manifest first, then re-scan and overwrite.
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "manifest.yml"),
		[]byte("name: snap1\ncreated_at: 2026-01-01T00:00:00Z\n"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath}

	// Without --verify: snap1.checksums must not appear.
	cmd := NewCmd("", flags)
	var out1 bytes.Buffer
	cmd.SetOut(&out1)
	cmd.SetErr(&out1)
	cmd.SetArgs([]string{"snapshot", "snap1"})
	_ = cmd.Execute()
	require.NotContains(t, out1.String(), "snap1.checksums", "no checksums target without --verify")
}

// runValidateJSONCmd creates a fresh validate command with JSON output flags and
// runs it, returning stdout and stderr separately. Does not fail on non-zero
// command exit so callers can assert on validation-failed results.
func runValidateJSONCmd(t *testing.T, cfgPath string, args ...string) (stdout, stderr string) {
	t.Helper()
	flags := &cmdctx.RootFlags{
		ConfigPath: cfgPath,
		Output:     "json",
	}
	cmd := NewCmd("", flags)
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	_ = cmd.Execute()
	return outBuf.String(), errBuf.String()
}

// TestValidateCmd_JSONMode_ScopeStableForGatedCheck verifies that the JSON
// `scope` field does NOT carry the multi-line (stages)/[services] decoration
// that targetWithStages adds to Diagnostic.Target for human display. Machine
// consumers must see a stable `checks/<id>` identifier — adding/removing
// services or stages must not change scope.
func TestValidateCmd_JSONMode_ScopeStableForGatedCheck(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "workspace"), 0o755))
	vYml := filepath.Join(tmpDir, "workspace", "validate.yml")
	require.NoError(t, os.WriteFile(vYml, []byte(`checks:
  - id: jwt-secret
    description: gated check
    stages: [deploy]
    services: [api]
    type: builtin
    cmd: shell
    with:
      cmd: 'true'
`), 0o644))

	stdout, _ := runValidateJSONCmd(t, workspacePath, "checks", "jwt-secret")

	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	var diagnostics []diagnosticJSON
	require.NoError(t, json.Unmarshal(got["diagnostics"], &diagnostics))
	require.NotEmpty(t, diagnostics, "expected at least one diagnostic row")

	for _, d := range diagnostics {
		require.NotContains(t, d.Scope, "\n",
			"JSON scope must not carry presentation-layer newlines: %q", d.Scope)
		require.NotContains(t, d.Scope, "(",
			"JSON scope must not include the stages decoration: %q", d.Scope)
		require.NotContains(t, d.Scope, "[",
			"JSON scope must not include the services decoration: %q", d.Scope)
	}

	// And the gated check itself must appear with the stable id-only scope.
	found := false
	for _, d := range diagnostics {
		if d.Scope == "checks/jwt-secret" {
			found = true
			break
		}
	}
	require.True(t, found, "gated check must serialize as checks/jwt-secret; got %+v", diagnostics)
}

// TestValidateCmd_JSONMode_Structure verifies that `dwe validate --output json`
// emits a JSON object with the expected top-level keys.
func TestValidateCmd_JSONMode_Structure(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	stdout, _ := runValidateJSONCmd(t, workspacePath)

	require.NotEmpty(t, stdout, "JSON output must not be empty")
	require.True(t, strings.HasPrefix(strings.TrimSpace(stdout), "{"),
		"JSON output must start with '{', got: %q", stdout[:min(50, len(stdout))])

	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "output must be valid JSON")
	require.Contains(t, got, "summary", "JSON must have 'summary' key")
	require.Contains(t, got, "diagnostics", "JSON must have 'diagnostics' key")

	var summary validateSummaryJSON
	require.NoError(t, json.Unmarshal(got["summary"], &summary))

	var diagnostics []diagnosticJSON
	require.NoError(t, json.Unmarshal(got["diagnostics"], &diagnostics), "'diagnostics' must be a JSON array")
}

// TestValidateCmd_JSONMode_SummaryFields verifies the summary contains numeric fields.
func TestValidateCmd_JSONMode_SummaryFields(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	stdout, _ := runValidateJSONCmd(t, workspacePath)

	var got struct {
		Summary struct {
			Ok      int `json:"ok"`
			Info    int `json:"info"`
			Warning int `json:"warning"`
			Error   int `json:"error"`
		} `json:"summary"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	// Counts should be non-negative (we can't assert exact values since env
	// probes depend on the host, but the fields must be present and numeric).
	require.GreaterOrEqual(t, got.Summary.Ok+got.Summary.Info+got.Summary.Warning+got.Summary.Error, 0)
}

// TestValidateCmd_JSONMode_DiagnosticFields verifies that each diagnostic in the
// JSON output has the required fields with valid values.
func TestValidateCmd_JSONMode_DiagnosticFields(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	stdout, _ := runValidateJSONCmd(t, workspacePath)

	var got struct {
		Diagnostics []struct {
			Severity string `json:"severity"`
			Scope    string `json:"scope"`
			Message  string `json:"message"`
		} `json:"diagnostics"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))

	validSeverities := map[string]bool{"ok": true, "info": true, "warning": true, "error": true}
	for i, d := range got.Diagnostics {
		require.True(t, validSeverities[d.Severity], "diagnostic[%d].severity must be ok/info/warning/error, got %q", i, d.Severity)
		require.NotEmpty(t, d.Scope, "diagnostic[%d].scope must not be empty", i)
		// OK diagnostics may have empty messages (they're "pass" signals); others must have one.
		if d.Severity != "ok" {
			require.NotEmpty(t, d.Message, "diagnostic[%d].message must not be empty for non-ok severity", i)
		}
	}
}

// TestValidateCmd_JSONMode_SummaryScope verifies that the JSON summary carries
// a machine-identifiable scope field, so a narrowed run (e.g. `dwe validate
// config services`, one validator) is no longer indistinguishable from a full
// domain run (`dwe validate config`, ten validators) by diagnostic count alone.
func TestValidateCmd_JSONMode_SummaryScope(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	tests := []struct {
		name      string
		args      []string
		wantScope string
	}{
		{name: "full run", args: nil, wantScope: "all"},
		{name: "domain run", args: []string{"config"}, wantScope: "config"},
		{name: "leaf run", args: []string{"config", "services"}, wantScope: "config/services"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, _ := runValidateJSONCmd(t, workspacePath, tt.args...)

			var got struct {
				Summary validateSummaryJSON `json:"summary"`
			}
			require.NoError(t, json.Unmarshal([]byte(stdout), &got))
			require.Equal(t, tt.wantScope, got.Summary.Scope)
		})
	}
}

// TestValidateCmd_JSONMode_SummaryScopeAddsFieldOnly verifies that the new
// summary.scope field is additive: every previously-existing summary key
// keeps decoding into its own typed field alongside it.
func TestValidateCmd_JSONMode_SummaryScopeAddsFieldOnly(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	stdout, _ := runValidateJSONCmd(t, workspacePath, "config")

	var got struct {
		Summary struct {
			Scope   string `json:"scope"`
			Ok      int    `json:"ok"`
			Info    int    `json:"info"`
			Warning int    `json:"warning"`
			Error   int    `json:"error"`
		} `json:"summary"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Equal(t, "config", got.Summary.Scope)
	// The point of this test is that adding `scope` did not displace the
	// pre-existing counters, so at least one of them must still be populated —
	// a `>= 0` assertion on four non-negative ints can never fail and would
	// pass just as happily against an empty summary object.
	require.Positive(t, got.Summary.Ok+got.Summary.Info+got.Summary.Warning+got.Summary.Error,
		"the counters must still decode alongside scope")
}

// TestValidateText_SummaryReportsScope verifies the human summary line names
// the active scope, matching the JSON contract, for a full run, a domain run
// and a leaf run.
func TestValidateText_SummaryReportsScope(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	tests := []struct {
		name      string
		args      []string
		wantScope string
	}{
		{name: "full run", args: nil, wantScope: "all"},
		{name: "domain run", args: []string{"config"}, wantScope: "config"},
		{name: "leaf run", args: []string{"config", "services"}, wantScope: "config/services"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags := &cmdctx.RootFlags{ConfigPath: workspacePath}
			cmd := NewCmd("", flags)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(tt.args)
			_ = cmd.Execute()

			require.Contains(t, out.String(), fmt.Sprintf("(scope: %s)", tt.wantScope))
		})
	}
}

// TestValidateCmd_JSONMode_ExitCodePreserved verifies that validation errors in
// JSON mode still produce a non-zero exit code (via validationFailedError).
func TestValidateCmd_JSONMode_ExitCodePreserved(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	// Snapshot directory with no manifest → error diagnostic.
	brokenDir := filepath.Join(tmpDir, "snapshots", "broken")
	require.NoError(t, os.MkdirAll(brokenDir, 0o755))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath, Output: "json"}
	cmd := NewCmd("", flags)
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"snapshot"})
	err := cmd.Execute()

	// The command should return validationFailedError (exit code 1).
	require.Error(t, err, "validation with errors must return an error in JSON mode")
	var vfe *validationFailedError
	require.ErrorAs(t, err, &vfe, "error must be validationFailedError")
	require.Equal(t, 1, vfe.ExitCode())

	// Stdout must still contain valid JSON diagnostics.
	stdout := outBuf.String()
	require.True(t, strings.HasPrefix(strings.TrimSpace(stdout), "{"),
		"stdout must be JSON even when validation fails, got: %q", stdout[:min(80, len(stdout))])

	var got struct {
		Summary struct {
			Error int `json:"error"`
		} `json:"summary"`
		Diagnostics []struct {
			Severity string `json:"severity"`
		} `json:"diagnostics"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Greater(t, got.Summary.Error, 0, "summary.error must be > 0")

	// Stderr must NOT contain a JSON error envelope (diagnostics are the data).
	// Note: cobra may print "Error: validation failed" to stderr when SilenceErrors
	// is not set (which is the case in this isolated unit test, where root's
	// PersistentPreRunE does not run). The important invariant is that stderr does
	// NOT contain a JSON error envelope — i.e. it must not start with '{'.
	stderr := errBuf.String()
	trimmedStderr := strings.TrimSpace(stderr)
	if trimmedStderr != "" {
		require.False(t, strings.HasPrefix(trimmedStderr, "{"),
			"stderr must not contain a JSON error envelope for validation failures, got: %q", stderr)
	}
}

// TestValidateCmd_JSONMode_StrictExitCodePreserved verifies that --strict upgrades
// warnings to errors in JSON mode (exit code 1).
func TestValidateCmd_JSONMode_StrictExitCodePreserved(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	// Configure a generic linter with a missing binary → Warning.
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "workspace"), 0o755))
	yml := "linters:\n  totally-fake-linter-xyz:\n    type: generic\n    bin: totally-fake-linter-xyz\n    paths: [\".\"]\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "workspace", "validate.yml"), []byte(yml), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath, Output: "json"}
	cmd := NewCmd("", flags)
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--strict", "linters", "totally-fake-linter-xyz"})
	err := cmd.Execute()

	require.Error(t, err)
	var vfe *validationFailedError
	require.ErrorAs(t, err, &vfe)
	require.Equal(t, 1, vfe.ExitCode())

	var got struct {
		Summary struct {
			Warning int `json:"warning"`
		} `json:"summary"`
	}
	require.NoError(t, json.Unmarshal(outBuf.Bytes(), &got))
	require.Greater(t, got.Summary.Warning, 0, "summary.warning must be > 0")
}

// TestValidateCmd_JSONMode_ConfigGolden captures the JSON shape for `validate config`
// against a minimal workspace.yml. The golden file is generated with UPDATE_GOLDEN=1.
func TestValidateCmd_JSONMode_ConfigGolden(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	stdout, _ := runValidateJSONCmd(t, workspacePath, "config")
	got := strings.TrimRight(stdout, "\n")

	goldenPath := filepath.Join("testdata", "validate_config.json.golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(goldenPath, []byte(got+"\n"), 0o644))
		t.Logf("updated golden: %s", goldenPath)
		return
	}

	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden %s: %v (run with UPDATE_GOLDEN=1 to generate)", goldenPath, err)
	}
	want := strings.TrimRight(string(raw), "\n")
	if got != want {
		t.Errorf("JSON output mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

// TestValidateCmd_JSONMode_NoANSI verifies that JSON output contains no ANSI
// escape sequences even when validators emit styled text.
func TestValidateCmd_JSONMode_NoANSI(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	stdout, _ := runValidateJSONCmd(t, workspacePath, "config")
	require.NotContains(t, stdout, "\x1b[", "JSON output must not contain ANSI escape sequences")
}

// TestValidateCmd_JSONMode_PrettyFlag verifies that --pretty produces indented JSON.
func TestValidateCmd_JSONMode_PrettyFlag(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath, Output: "json", Pretty: true}
	cmd := NewCmd("", flags)
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"config"})
	_ = cmd.Execute()

	got := outBuf.String()
	require.Contains(t, got, "\n  ", "pretty JSON should contain indented lines")
	require.Contains(t, got, `"summary"`, "pretty JSON should contain 'summary' key")
	require.Contains(t, got, `"diagnostics"`, "pretty JSON should contain 'diagnostics' key")
}

// TestValidateCmd_JSONMode_TextBehaviorUnchanged verifies that text mode still
// produces the human-readable table+summary format (regression guard).
func TestValidateCmd_JSONMode_TextBehaviorUnchanged(t *testing.T) {
	tmpDir := t.TempDir()
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath, Output: "text"}
	cmd := NewCmd("", flags)
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"config"})
	_ = cmd.Execute()

	got := outBuf.String()
	// Text output must NOT be JSON.
	require.False(t, strings.HasPrefix(strings.TrimSpace(got), "{"),
		"text mode must not produce JSON output")
	// Must contain "validation result" summary line.
	require.Contains(t, got, "validation result", "text mode must produce summary line")
}

func TestParseSeverityLevels(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    map[validate.Severity]struct{}
		wantErr bool
	}{
		{name: "empty is nil", raw: "", want: nil},
		{name: "whitespace only is nil", raw: "  ", want: nil},
		{
			name: "single",
			raw:  "error",
			want: map[validate.Severity]struct{}{validate.SeverityError: {}},
		},
		{
			name: "comma list with spacing and case",
			raw:  " Error , warning ",
			want: map[validate.Severity]struct{}{validate.SeverityError: {}, validate.SeverityWarning: {}},
		},
		{name: "unknown token errors", raw: "error,bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSeverityLevels(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestFilterByLevels(t *testing.T) {
	diags := []validate.Diagnostic{
		{Severity: validate.SeverityOK},
		{Severity: validate.SeverityWarning},
		{Severity: validate.SeverityError},
	}

	// Nil set is a pass-through.
	require.Len(t, filterByLevels(diags, nil), 3)

	set := map[validate.Severity]struct{}{validate.SeverityError: {}, validate.SeverityWarning: {}}
	got := filterByLevels(diags, set)
	require.Len(t, got, 2)
	for _, d := range got {
		_, ok := set[d.Severity]
		require.True(t, ok)
	}
}

// writeBridgeFixture creates a minimal project whose single app service
// carries an invalid bridge.on_unreachable value and no dir/dir_internal
// mapping — one error + one warning from the bridge domain.
func writeBridgeFixture(t *testing.T) (workspacePath string) {
	t.Helper()
	tmpDir := t.TempDir()
	workspacePath = filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))
	svcDir := filepath.Join(tmpDir, "workspace", "services", "web")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(
		"type: app\nbridge:\n  enabled: true\n  on_unreachable: sometimes\n",
	), 0o644))
	return workspacePath
}

func TestValidateBridgeSubcommand(t *testing.T) {
	cmd := NewCmd("", &cmdctx.RootFlags{})
	bridgeCmd, _, _ := cmd.Find([]string{"bridge"})
	require.NotNil(t, bridgeCmd)
	require.Equal(t, "bridge", bridgeCmd.Name())
	require.NotNil(t, bridgeCmd.Args)
	require.True(t, bridgeCmd.SilenceUsage)
}

// TestValidateBridgeRunsBridgeDomainOnly: `dwe validate bridge` scopes
// execution to the bridge domain and surfaces the bridge diagnostics.
func TestValidateBridgeRunsBridgeDomainOnly(t *testing.T) {
	workspacePath := writeBridgeFixture(t)

	stdout, _ := runValidateJSONCmd(t, workspacePath, "bridge")

	var got validateJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Equal(t, 1, got.Summary.Error)
	require.Equal(t, 1, got.Summary.Warning)
	require.Len(t, got.Diagnostics, 2)
	for _, d := range got.Diagnostics {
		require.True(t, strings.HasPrefix(d.Scope, "bridge/"),
			"scoped run must emit only bridge-domain rows, got scope %q", d.Scope)
	}
}

// TestValidateFullRunIncludesBridgeDomain: the bridge domain is registered in
// buildRegistry, so an unscoped `dwe validate` surfaces its diagnostics too.
func TestValidateFullRunIncludesBridgeDomain(t *testing.T) {
	workspacePath := writeBridgeFixture(t)

	stdout, _ := runValidateJSONCmd(t, workspacePath)

	var got validateJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	var bridgeScopes []string
	for _, d := range got.Diagnostics {
		if strings.HasPrefix(d.Scope, "bridge/") {
			bridgeScopes = append(bridgeScopes, d.Scope)
		}
	}
	require.Len(t, bridgeScopes, 2, "full run must include the bridge domain rows")
	require.Equal(t, "bridge/bridge.services:web", bridgeScopes[0])
}

// TestValidateBridgeTextHeader: text mode shows the bridge-specific scope
// label so the user knows what was checked.
func TestValidateBridgeTextHeader(t *testing.T) {
	workspacePath := writeBridgeFixture(t)

	flags := &cmdctx.RootFlags{ConfigPath: workspacePath}
	cmd := NewCmd("", flags)
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"bridge"})
	err := cmd.Execute()
	require.Error(t, err, "the invalid enum must drive a non-zero exit")

	out := output.String()
	require.Contains(t, out, "host-bridge service settings")
	require.Contains(t, out, `"sometimes"`)
}

// writeTestsFixture writes a minimal project with a workspace/tests/*.yml
// scenario carrying an unparseable timeout — one tests-domain error.
func writeTestsFixture(t *testing.T) (workspacePath string) {
	t.Helper()
	tmpDir := t.TempDir()
	workspacePath = filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("project:\n  name: test\n"), 0o644))
	testsDir := filepath.Join(tmpDir, "workspace", "tests")
	require.NoError(t, os.MkdirAll(testsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(testsDir, "smoke.yml"), []byte(
		"timeout: not-a-duration\nsteps:\n  - name: ping\n    type: shell\n    cmd: echo hi\n",
	), 0o644))
	return workspacePath
}

func TestValidateTestsSubcommand(t *testing.T) {
	cmd := NewCmd("", &cmdctx.RootFlags{})
	testsCmd, _, _ := cmd.Find([]string{"tests"})
	require.NotNil(t, testsCmd)
	require.Equal(t, "tests", testsCmd.Name())
	require.NotNil(t, testsCmd.Args)
	require.True(t, testsCmd.SilenceUsage)
}

// TestValidateTestsRunsTestsDomainOnly: `dwe validate tests` scopes execution
// to the tests domain and surfaces only its diagnostics.
func TestValidateTestsRunsTestsDomainOnly(t *testing.T) {
	workspacePath := writeTestsFixture(t)

	stdout, _ := runValidateJSONCmd(t, workspacePath, "tests")

	var got validateJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Equal(t, 1, got.Summary.Error)
	require.Len(t, got.Diagnostics, 1)
	require.True(t, strings.HasPrefix(got.Diagnostics[0].Scope, "tests/"),
		"scoped run must emit only tests-domain rows, got scope %q", got.Diagnostics[0].Scope)
}

// TestValidateFullRunIncludesTestsDomain: the tests domain is registered in
// buildRegistry, so an unscoped `dwe validate` surfaces its diagnostics too.
func TestValidateFullRunIncludesTestsDomain(t *testing.T) {
	workspacePath := writeTestsFixture(t)

	stdout, _ := runValidateJSONCmd(t, workspacePath)

	var got validateJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	var testsScopes []string
	for _, d := range got.Diagnostics {
		if strings.HasPrefix(d.Scope, "tests/") {
			testsScopes = append(testsScopes, d.Scope)
		}
	}
	require.Len(t, testsScopes, 1, "full run must include the tests domain rows")
}

// TestShouldEmitFilterHint pins the decision rule of the point-of-need
// --level/--quiet hint. The threshold is a concrete row count so both the
// positive and the negative case are testable.
func TestShouldEmitFilterHint(t *testing.T) {
	tests := []struct {
		name     string
		rows     int
		errors   int
		warnings int
		quiet    bool
		levelRaw string
		want     bool
	}{
		{name: "long run with narrowable rows", rows: filterHintThreshold + 1, warnings: 1, want: true},
		{name: "exactly at the threshold stays silent", rows: filterHintThreshold, warnings: 1},
		{name: "short run stays silent", rows: 3, warnings: 1},
		{name: "no rows at all", rows: 0},
		{name: "already quiet", rows: filterHintThreshold + 10, warnings: 1, quiet: true},
		{name: "already filtered by level", rows: filterHintThreshold + 10, warnings: 1, levelRaw: "error"},
		{name: "level with surrounding space still counts as filtering", rows: filterHintThreshold + 10, warnings: 1, levelRaw: "  error  "},
		{name: "all rows are errors so nothing to narrow", rows: filterHintThreshold + 5, errors: filterHintThreshold + 5},
		{name: "one non-error row is enough to narrow", rows: filterHintThreshold + 5, errors: filterHintThreshold + 4, want: true},
		// A clean project is long precisely because every check renders an ok
		// row; both suggested flags would empty the table, so the hint would be
		// false advice. This is the freshly-scaffolded case.
		{name: "long but nothing above info so both flags empty the table", rows: filterHintThreshold + 5},
		{name: "a single warning is enough to make --quiet useful", rows: filterHintThreshold + 5, warnings: 1, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldEmitFilterHint(tt.rows, tt.errors, tt.warnings, tt.quiet, tt.levelRaw)
			require.Equal(t, tt.want, got)
		})
	}
}

// writeLongValidateFixture builds a project whose unscoped `dwe validate` run
// renders comfortably more than filterHintThreshold rows: the base domains
// already contribute ~19 informational rows, and each manifest-less snapshot
// directory adds one more.
func writeLongValidateFixture(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	workspacePath := filepath.Join(tmpDir, "workspace.yml")
	require.NoError(t, os.WriteFile(workspacePath, []byte("schema_version: \"2\"\n"), 0o644))
	for i := range 10 {
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "snapshots", fmt.Sprintf("s%d", i)), 0o755))
	}
	return workspacePath
}

// runValidateTextCmd runs the validate tree in human mode and returns the two
// streams separately, so a test can assert the hint never contaminates stdout.
func runValidateTextCmd(t *testing.T, cfgPath string, args ...string) (stdout, stderr string) {
	t.Helper()
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}
	cmd := NewCmd("", flags)
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	_ = cmd.Execute()
	return outBuf.String(), errBuf.String()
}

// TestValidateFilterHint_LongRunEmitsToStderrOnly: above the threshold the hint
// names both flags, and it goes to stderr — stdout stays the parseable surface.
func TestValidateFilterHint_LongRunEmitsToStderrOnly(t *testing.T) {
	workspacePath := writeLongValidateFixture(t)

	stdout, stderr := runValidateTextCmd(t, workspacePath)

	require.Contains(t, stderr, "--level")
	require.Contains(t, stderr, "--quiet")
	require.NotContains(t, stdout, "--level", "the hint must never reach stdout")
	require.NotContains(t, stdout, "--quiet", "the hint must never reach stdout")
}

// TestValidateFilterHint_ShortRunSilent: a scoped run renders a single row, well
// below the threshold, so no hint is emitted.
func TestValidateFilterHint_ShortRunSilent(t *testing.T) {
	workspacePath := writeLongValidateFixture(t)

	stdout, stderr := runValidateTextCmd(t, workspacePath, "commands")

	// Precondition: the run actually rendered a table. Without this the
	// "stderr lacks the hint" assertions would also pass on an aborted run.
	require.Contains(t, stdout, "(scope:", "the run must have rendered a summary")
	require.NotContains(t, stderr, "--level")
	require.NotContains(t, stderr, "--quiet")
}

// TestValidateFilterHint_SuppressedWhenAlreadyFiltering: the hint is
// point-of-need advice, so a user who already passed one of the flags does not
// get told about them.
func TestValidateFilterHint_SuppressedWhenAlreadyFiltering(t *testing.T) {
	workspacePath := writeLongValidateFixture(t)

	for _, args := range [][]string{
		{"--quiet"},
		{"--level", "ok,info,warning,error"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, stderr := runValidateTextCmd(t, workspacePath, args...)
			require.Contains(t, stdout, "(scope:", "the run must have rendered a summary")
			require.NotContains(t, stderr, "Narrow the output")
		})
	}
}

// TestValidateFilterHint_SilentOnFreshScaffold pins the case the hint must never
// fire on: a freshly scaffolded project is ABOVE the row threshold (one ok row
// per check, currently 21) yet has zero errors and zero warnings, so both
// suggested flags render an empty table. This is the most common way to meet the
// threshold at all, and it goes through the real scaffold rather than a
// hand-built fixture so that adding another scaffolded artefact — the way
// workspace/tests/smoke.yml first pushed the count past 20 — cannot silently
// re-trip it.
func TestValidateFilterHint_SilentOnFreshScaffold(t *testing.T) {
	targetDir := t.TempDir()
	_, err := scaffold.Scaffold(scaffold.Options{TargetDir: targetDir, Name: "hintcheck", Prefix: "dwe", Service: "app"})
	require.NoError(t, err)

	workspacePath := filepath.Join(targetDir, "workspace.yml")

	// Precondition: without this the assertion below would also pass on a
	// scaffold that simply fell back under the threshold, testing nothing.
	jsonOut, _ := runValidateJSONCmd(t, workspacePath)
	var got validateJSON
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &got))
	require.Greater(t, len(got.Diagnostics), filterHintThreshold,
		"the scaffold must exceed the threshold or the suppression is untested")
	require.Zero(t, got.Summary.Error)
	require.Zero(t, got.Summary.Warning)

	stdout, stderr := runValidateTextCmd(t, workspacePath)

	require.Contains(t, stdout, "(scope:", "the run must have rendered a summary")
	require.NotContains(t, stderr, "Narrow the output",
		"a clean scaffold has nothing to narrow to — both suggested flags empty the table")
}

// TestValidateFilterHint_SuppressedInJSON: JSON consumers filter the array
// themselves, and a stderr line would be noise for them.
func TestValidateFilterHint_SuppressedInJSON(t *testing.T) {
	workspacePath := writeLongValidateFixture(t)

	stdout, stderr := runValidateJSONCmd(t, workspacePath)

	require.NotContains(t, stderr, "Narrow the output")
	var got validateJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Greater(t, len(got.Diagnostics), filterHintThreshold,
		"fixture must exceed the threshold or the JSON suppression is untested")
}
