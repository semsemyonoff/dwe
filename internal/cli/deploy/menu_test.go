package deploy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/validate/env"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/core/workflow/setup"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunDeployMenu_NonTTY_PrintsHelpAndExits(t *testing.T) {
	cmd := &cobra.Command{}
	flags := &cmdctx.RootFlags{ConfigPath: "/tmp/nonexistent"}

	// Stub stdin as non-TTY
	oldIsInteractive := widgets.IsInteractiveFn
	defer func() { widgets.IsInteractiveFn = oldIsInteractive }()
	widgets.IsInteractiveFn = func(stdin io.Reader) bool { return false }

	err := runDeployMenu(cmd, flags)
	if err == nil {
		t.Fatal("expected error for non-TTY")
	}

	// Should be a usageError with exit code 2
	if _, ok := errors.AsType[usageError](err); !ok {
		t.Errorf("expected usageError, got %T", err)
	}
}

func TestRunDeployMenu_MenuDispatch_Run(t *testing.T) {
	tmpdir := t.TempDir()

	workspaceDir := filepath.Join(tmpdir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "workspace.yml"), []byte("project:\n  name: test"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(workspaceDir, "workspace.yml")}

	oldIsInteractive := widgets.IsInteractiveFn
	oldSelectFn := selectMenuItemFn
	oldRunDeployRunFn := runDeployRunFn
	defer func() {
		widgets.IsInteractiveFn = oldIsInteractive
		selectMenuItemFn = oldSelectFn
		runDeployRunFn = oldRunDeployRunFn
	}()

	widgets.IsInteractiveFn = func(stdin io.Reader) bool { return true }

	selectMenuItemFn = func(ctx context.Context, cmd *cobra.Command, pending *journal.PendingApply, showWizard bool) (menuChoice, error) {
		return menuRun, nil
	}

	runDeployCalled := false
	runDeployRunFn = func(ctx context.Context, cmd *cobra.Command, flags *cmdctx.RootFlags, opts deployRunOpts) error {
		runDeployCalled = true
		assert.Equal(t, "", opts.ServiceName)
		return nil
	}

	cmd := &cobra.Command{}
	cmd.SetOut(bytes.NewBuffer(nil))
	cmd.SetErr(bytes.NewBuffer(nil))

	err := runDeployMenu(cmd, flags)
	require.NoError(t, err)
	require.True(t, runDeployCalled, "runDeployRun should have been called")
}

func TestRunDeployMenu_MenuDispatch_Exit(t *testing.T) {
	tmpdir := t.TempDir()

	workspaceDir := filepath.Join(tmpdir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "workspace.yml"), []byte("project:\n  name: test"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(workspaceDir, "workspace.yml")}

	oldIsInteractive := widgets.IsInteractiveFn
	oldSelectFn := selectMenuItemFn
	defer func() {
		widgets.IsInteractiveFn = oldIsInteractive
		selectMenuItemFn = oldSelectFn
	}()

	widgets.IsInteractiveFn = func(stdin io.Reader) bool { return true }

	selectMenuItemFn = func(ctx context.Context, cmd *cobra.Command, pending *journal.PendingApply, showWizard bool) (menuChoice, error) {
		return menuExit, nil
	}

	cmd := &cobra.Command{}
	cmd.SetOut(bytes.NewBuffer(nil))
	cmd.SetErr(bytes.NewBuffer(nil))

	err := runDeployMenu(cmd, flags)
	require.NoError(t, err)
}

func TestRunDeployMenu_SetupYMLErrors_BlocksMenu(t *testing.T) {
	tmpdir := t.TempDir()

	workspaceDir := filepath.Join(tmpdir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "workspace.yml"), []byte("project:\n  name: test"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(workspaceDir, "workspace.yml")}

	oldIsInteractive := widgets.IsInteractiveFn
	oldSelectFn := selectMenuItemFn
	oldLoadSetupFn := loadSetupYAMLFn
	defer func() {
		widgets.IsInteractiveFn = oldIsInteractive
		selectMenuItemFn = oldSelectFn
		loadSetupYAMLFn = oldLoadSetupFn
	}()

	widgets.IsInteractiveFn = func(stdin io.Reader) bool { return true }

	// Stub loader to return a parse error (unknown field) without requiring a real file.
	loadSetupYAMLFn = func(path string) (*setup.Config, error) {
		return nil, errors.New("yaml: unmarshal errors: field unknownfield not found in type setup.Question")
	}

	selectCalled := false
	selectMenuItemFn = func(ctx context.Context, cmd *cobra.Command, pending *journal.PendingApply, showWizard bool) (menuChoice, error) {
		selectCalled = true
		return menuExit, nil
	}

	cmd := &cobra.Command{}
	cmd.SetOut(bytes.NewBuffer(nil))
	errBuf := bytes.NewBuffer(nil)
	cmd.SetErr(errBuf)

	err := runDeployMenu(cmd, flags)
	require.Error(t, err)

	// Must be a deployValidationError (exit code 1), not a plain error, so fang
	// suppresses the double-print (diagnostics table already written to stderr).
	var dve *deployValidationError
	require.ErrorAs(t, err, &dve, "expected deployValidationError so fang suppresses double-print")
	assert.Equal(t, 1, dve.ExitCode())

	// Menu must not have been shown.
	assert.False(t, selectCalled, "menu selector must not be called when setup.yml has errors")

	// Diagnostics table must have been written to stderr.
	assert.NotEmpty(t, errBuf.String(), "diagnostics must be printed to stderr")
}

func TestRunDeployMenu_MenuDispatch_Plan(t *testing.T) {
	tmpdir := t.TempDir()

	workspaceDir := filepath.Join(tmpdir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "workspace.yml"), []byte("project:\n  name: test"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(workspaceDir, "workspace.yml")}

	oldIsInteractive := widgets.IsInteractiveFn
	oldSelectFn := selectMenuItemFn
	oldRunDeployPlanFn := runDeployPlanFn
	defer func() {
		widgets.IsInteractiveFn = oldIsInteractive
		selectMenuItemFn = oldSelectFn
		runDeployPlanFn = oldRunDeployPlanFn
	}()

	widgets.IsInteractiveFn = func(stdin io.Reader) bool { return true }

	selectMenuItemFn = func(ctx context.Context, cmd *cobra.Command, pending *journal.PendingApply, showWizard bool) (menuChoice, error) {
		return menuPlan, nil
	}

	planCalled := false
	runDeployPlanFn = func(ctx context.Context, cmd *cobra.Command, flags *cmdctx.RootFlags, opts deployPlanOpts) error {
		planCalled = true
		assert.Equal(t, "", opts.ServiceName)
		return nil
	}

	cmd := &cobra.Command{}
	cmd.SetOut(bytes.NewBuffer(nil))
	cmd.SetErr(bytes.NewBuffer(nil))

	err := runDeployMenu(cmd, flags)
	require.NoError(t, err)
	require.True(t, planCalled, "runDeployPlan should have been called")
}

func TestRunDeployMenu_WizardShownWhenConflictsAndEmptyLocal(t *testing.T) {
	tmpdir := t.TempDir()

	workspaceDir := filepath.Join(tmpdir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "workspace.yml"), []byte("project:\n  name: test"), 0o644))
	// No setup.yml — port-conflicts-only path.

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(workspaceDir, "workspace.yml")}

	oldIsInteractive := widgets.IsInteractiveFn
	oldSelectFn := selectMenuItemFn
	oldCollectFn := collectPortConflictsFn
	defer func() {
		widgets.IsInteractiveFn = oldIsInteractive
		selectMenuItemFn = oldSelectFn
		collectPortConflictsFn = oldCollectFn
	}()

	widgets.IsInteractiveFn = func(stdin io.Reader) bool { return true }

	// Inject a fake port conflict so showWizard becomes true even without setup.yml.
	collectPortConflictsFn = func(ctx context.Context, cfg *config.DweConfig, baseDir string) ([]env.PortConflict, error) {
		return []env.PortConflict{{Service: "web", PortName: "http", RequestedPort: 8080, OccupiedBy: "other"}}, nil
	}

	var capturedShowWizard bool
	selectMenuItemFn = func(ctx context.Context, cmd *cobra.Command, pending *journal.PendingApply, showWizard bool) (menuChoice, error) {
		capturedShowWizard = showWizard
		return menuExit, nil
	}

	cmd := &cobra.Command{}
	cmd.SetOut(bytes.NewBuffer(nil))
	cmd.SetErr(bytes.NewBuffer(nil))

	require.NoError(t, runDeployMenu(cmd, flags))
	assert.True(t, capturedShowWizard, "wizard should be shown when port conflicts exist and local.yml is empty")
}

func TestRunDeployMenu_WizardPreflightBlocks(t *testing.T) {
	tmpdir := t.TempDir()
	workspaceDir := filepath.Join(tmpdir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "workspace.yml"), []byte("project:\n  name: test"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(workspaceDir, "workspace.yml")}

	oldIsInteractive := widgets.IsInteractiveFn
	oldSelectFn := selectMenuItemFn
	oldPreflightFn := runPreWizardPreflightFn
	oldRunWizardFn := runWizardFn
	t.Cleanup(func() {
		widgets.IsInteractiveFn = oldIsInteractive
		selectMenuItemFn = oldSelectFn
		runPreWizardPreflightFn = oldPreflightFn
		runWizardFn = oldRunWizardFn
	})

	widgets.IsInteractiveFn = func(stdin io.Reader) bool { return true }
	selectMenuItemFn = func(ctx context.Context, cmd *cobra.Command, pending *journal.PendingApply, showWizard bool) (menuChoice, error) {
		return menuWizard, nil
	}

	preflightCalled := false
	runPreWizardPreflightFn = func(ctx context.Context, cfg *config.DweConfig, baseDir string, errOut io.Writer) error {
		preflightCalled = true
		return &deployValidationError{"docker daemon not reachable"}
	}

	wizardCalled := false
	runWizardFn = func(ctx context.Context, deps setup.WizardDeps) error {
		wizardCalled = true
		return nil
	}

	cmd := &cobra.Command{}
	cmd.SetOut(bytes.NewBuffer(nil))
	cmd.SetErr(bytes.NewBuffer(nil))

	err := runDeployMenu(cmd, flags)
	require.Error(t, err)
	var dve *deployValidationError
	assert.ErrorAs(t, err, &dve, "must return deployValidationError")
	assert.True(t, preflightCalled, "pre-wizard preflight must run")
	assert.False(t, wizardCalled, "wizard must NOT run when preflight blocks")
}

func TestIsEmptyLocal(t *testing.T) {
	cases := []struct {
		name     string
		input    map[string]any
		expected bool
	}{
		{"empty map", map[string]any{}, true},
		{"nil map", nil, true},
		{"single key", map[string]any{"services": map[string]any{}}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isEmptyLocal(tc.input)
			assert.Equal(t, tc.expected, got)
		})
	}
}

// TestRunPreWizardPreflight_SecretsUnresolvedBlocks mirrors the preflight.Run
// pin: the early gate must refuse before the user answers wizard questions, and
// go quiet once the identity is available. Only the secrets rows are asserted —
// the env probes report whatever the host looks like.
func TestRunPreWizardPreflight_SecretsUnresolvedBlocks(t *testing.T) {
	root := t.TempDir()
	id, err := secrets.Keygen()
	require.NoError(t, err)
	marker, err := secrets.Encrypt("s3cr3t-value", id.Recipient())
	require.NoError(t, err)

	configPath := filepath.Join(root, "workspace.yml")
	require.NoError(t, os.WriteFile(configPath, []byte(
		"project:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+
			"\nvars:\n  token: "+marker+"\n"), 0o644))

	load := func(t *testing.T) *config.DweConfig {
		t.Helper()
		cfg, err := config.LoadConfig(configPath)
		require.NoError(t, err)
		return cfg
	}

	t.Run("without an identity", func(t *testing.T) {
		t.Setenv(secrets.EnvKey, "")
		t.Setenv(secrets.EnvKeyFile, "")
		t.Setenv("HOME", t.TempDir())

		var errOut bytes.Buffer
		err := runPreWizardPreflight(context.Background(), load(t), root, &errOut)
		require.Error(t, err, "the pre-wizard gate must block on an unresolved secret")
		assert.Contains(t, errOut.String(), "vars.token")
		assert.NotContains(t, errOut.String(), "s3cr3t-value")
	})

	t.Run("with the identity", func(t *testing.T) {
		t.Setenv(secrets.EnvKey, id.Export())
		t.Setenv(secrets.EnvKeyFile, "")

		var errOut bytes.Buffer
		// The gate must not block at all here: asserting only on the absence of
		// the secret messages would stay green while some other blocking
		// diagnostic kept the wizard shut.
		require.NoError(t, runPreWizardPreflight(context.Background(), load(t), root, &errOut))
		assert.NotContains(t, errOut.String(), "vars.token")
		assert.NotContains(t, errOut.String(), "s3cr3t-value")
		// The secrets domain now affirms a healthy setup with SeverityOK rows;
		// the gate filters them, so the wizard gains no extra banner.
		assert.NotContains(t, errOut.String(), "secrets.unresolved")
		assert.NotContains(t, errOut.String(), "readable via")
	})
}
