package deploy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/command/cmdctx"
	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/setup"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/validate/env"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunDeployMenu_NonTTY_PrintsHelpAndExits(t *testing.T) {
	cmd := &cobra.Command{}
	flags := &cmdctx.RootFlags{ConfigPath: "/tmp/nonexistent"}

	// Stub stdin as non-TTY
	oldIsInteractive := ui.IsInteractiveFn
	defer func() { ui.IsInteractiveFn = oldIsInteractive }()
	ui.IsInteractiveFn = func(stdin io.Reader) bool { return false }

	err := runDeployMenu(cmd, flags)
	if err == nil {
		t.Fatal("expected error for non-TTY")
	}

	// Should be a usageError with exit code 2
	var ue usageError
	if !errors.As(err, &ue) {
		t.Errorf("expected usageError, got %T", err)
	}
}

func TestRunDeployMenu_MenuDispatch_Run(t *testing.T) {
	tmpdir := t.TempDir()

	devboxDir := filepath.Join(tmpdir, "devbox")
	require.NoError(t, os.MkdirAll(devboxDir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(devboxDir, "devbox.yml"), []byte("project:\n  name: test"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(devboxDir, "devbox.yml")}

	// Override test seams
	oldIsInteractive := ui.IsInteractiveFn
	oldSelectFn := selectMenuItemFn
	oldRunDeployRunFn := runDeployRunFn
	defer func() {
		ui.IsInteractiveFn = oldIsInteractive
		selectMenuItemFn = oldSelectFn
		runDeployRunFn = oldRunDeployRunFn
	}()

	ui.IsInteractiveFn = func(stdin io.Reader) bool { return true }

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

	devboxDir := filepath.Join(tmpdir, "devbox")
	require.NoError(t, os.MkdirAll(devboxDir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(devboxDir, "devbox.yml"), []byte("project:\n  name: test"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(devboxDir, "devbox.yml")}

	// Override test seams
	oldIsInteractive := ui.IsInteractiveFn
	oldSelectFn := selectMenuItemFn
	defer func() {
		ui.IsInteractiveFn = oldIsInteractive
		selectMenuItemFn = oldSelectFn
	}()

	ui.IsInteractiveFn = func(stdin io.Reader) bool { return true }

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

	devboxDir := filepath.Join(tmpdir, "devbox")
	require.NoError(t, os.MkdirAll(devboxDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(devboxDir, "devbox.yml"), []byte("project:\n  name: test"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(devboxDir, "devbox.yml")}

	oldIsInteractive := ui.IsInteractiveFn
	oldSelectFn := selectMenuItemFn
	oldLoadSetupFn := loadSetupYAMLFn
	defer func() {
		ui.IsInteractiveFn = oldIsInteractive
		selectMenuItemFn = oldSelectFn
		loadSetupYAMLFn = oldLoadSetupFn
	}()

	ui.IsInteractiveFn = func(stdin io.Reader) bool { return true }

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

	devboxDir := filepath.Join(tmpdir, "devbox")
	require.NoError(t, os.MkdirAll(devboxDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(devboxDir, "devbox.yml"), []byte("project:\n  name: test"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(devboxDir, "devbox.yml")}

	oldIsInteractive := ui.IsInteractiveFn
	oldSelectFn := selectMenuItemFn
	oldRunDeployPlanFn := runDeployPlanFn
	defer func() {
		ui.IsInteractiveFn = oldIsInteractive
		selectMenuItemFn = oldSelectFn
		runDeployPlanFn = oldRunDeployPlanFn
	}()

	ui.IsInteractiveFn = func(stdin io.Reader) bool { return true }

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

	devboxDir := filepath.Join(tmpdir, "devbox")
	require.NoError(t, os.MkdirAll(devboxDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(devboxDir, "devbox.yml"), []byte("project:\n  name: test"), 0o644))
	// No setup.yml — port-conflicts-only path.

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(devboxDir, "devbox.yml")}

	oldIsInteractive := ui.IsInteractiveFn
	oldSelectFn := selectMenuItemFn
	oldCollectFn := collectPortConflictsFn
	defer func() {
		ui.IsInteractiveFn = oldIsInteractive
		selectMenuItemFn = oldSelectFn
		collectPortConflictsFn = oldCollectFn
	}()

	ui.IsInteractiveFn = func(stdin io.Reader) bool { return true }

	// Inject a fake port conflict so showWizard becomes true even without setup.yml.
	collectPortConflictsFn = func(ctx context.Context, cfg *config.DevboxConfig, baseDir string) ([]env.PortConflict, error) {
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
	devboxDir := filepath.Join(tmpdir, "devbox")
	require.NoError(t, os.MkdirAll(devboxDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(devboxDir, "devbox.yml"), []byte("project:\n  name: test"), 0o644))

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(devboxDir, "devbox.yml")}

	oldIsInteractive := ui.IsInteractiveFn
	oldSelectFn := selectMenuItemFn
	oldPreflightFn := runPreWizardPreflightFn
	oldRunWizardFn := runWizardFn
	t.Cleanup(func() {
		ui.IsInteractiveFn = oldIsInteractive
		selectMenuItemFn = oldSelectFn
		runPreWizardPreflightFn = oldPreflightFn
		runWizardFn = oldRunWizardFn
	})

	ui.IsInteractiveFn = func(stdin io.Reader) bool { return true }
	selectMenuItemFn = func(ctx context.Context, cmd *cobra.Command, pending *journal.PendingApply, showWizard bool) (menuChoice, error) {
		return menuWizard, nil
	}

	preflightCalled := false
	runPreWizardPreflightFn = func(ctx context.Context, cfg *config.DevboxConfig, baseDir string, errOut io.Writer) error {
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
