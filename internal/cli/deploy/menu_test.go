package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/validate/env"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/core/workflow/keygate"
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

// --- keygate wiring ---------------------------------------------------------

// isolateHome points HOME at a temp dir and clears the env identity sources, so
// a developer running the suite with DWE_AGE_KEY set gets the same outcome as
// CI and no test ever reads the real ~/.config/dwe/keys.
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(secrets.EnvKey, "")
	t.Setenv(secrets.EnvKeyFile, "")
	t.Setenv("DWE_NONINTERACTIVE", "")
}

// writeSecretsProject lays down a project whose defaults.yml carries one marker
// encrypted to recipient, i.e. the state of a fresh clone on a machine without
// the key. Returns the project root.
func writeSecretsProject(t *testing.T, recipient, marker string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"),
		[]byte("project:\n  name: test\nsecrets:\n  recipient: "+recipient+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace", "defaults.yml"),
		[]byte("vars:\n  token: "+marker+"\n"), 0o644))
	return root
}

// stubMenuTTY makes the menu believe it is on a terminal and closes it on the
// first frame, so a test only exercises what happens before the menu loop.
func stubMenuTTY(t *testing.T, choice menuChoice) {
	t.Helper()
	oldIsInteractive := widgets.IsInteractiveFn
	oldSelectFn := selectMenuItemFn
	t.Cleanup(func() {
		widgets.IsInteractiveFn = oldIsInteractive
		selectMenuItemFn = oldSelectFn
	})
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	selectMenuItemFn = func(context.Context, *cobra.Command, *journal.PendingApply, bool) (menuChoice, error) {
		return choice, nil
	}
}

// stubKeygate installs a gate stub and returns a pointer to the Options it was
// called with (nil while it has not been called).
func stubKeygate(t *testing.T, imported bool, err error) **keygate.Options {
	t.Helper()
	old := keygateEnsureFn
	t.Cleanup(func() { keygateEnsureFn = old })
	var seen *keygate.Options
	keygateEnsureFn = func(_ context.Context, opts keygate.Options) (bool, error) {
		seen = &opts
		return imported, err
	}
	return &seen
}

// TestRunDeployMenu_KeygateOptions pins what the menu hands the gate: the
// project root, the raw layers, the interactivity inputs it evaluated itself,
// and both interactive hooks.
func TestRunDeployMenu_KeygateOptions(t *testing.T) {
	isolateHome(t)
	id, err := secrets.Keygen()
	require.NoError(t, err)
	marker, err := secrets.Encrypt("s3cr3t-value", id.Recipient())
	require.NoError(t, err)

	cases := []struct {
		name           string
		output         string
		nonInteractive string
		wantJSON       bool
		wantNonInter   bool
	}{
		{name: "text", output: ""},
		{name: "json", output: "json", wantJSON: true},
		{name: "DWE_NONINTERACTIVE", nonInteractive: "1", wantNonInter: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DWE_NONINTERACTIVE", tc.nonInteractive)
			root := writeSecretsProject(t, id.Recipient(), marker)
			flags := &cmdctx.RootFlags{
				ConfigPath: filepath.Join(root, "workspace.yml"),
				Output:     tc.output,
			}

			stubMenuTTY(t, menuExit)
			seen := stubKeygate(t, false, nil)

			cmd := &cobra.Command{}
			cmd.SetOut(bytes.NewBuffer(nil))
			cmd.SetErr(bytes.NewBuffer(nil))

			require.NoError(t, runDeployMenu(cmd, flags))

			opts := *seen
			require.NotNil(t, opts, "the gate must run on every menu invocation")
			assert.Equal(t, root, opts.BaseDir)
			assert.NotEmpty(t, opts.Layers, "the gate decides on the raw layers")
			assert.True(t, opts.Interactive, "stdin was stubbed as a terminal")
			assert.False(t, opts.Yes, "the deploy menu has no --yes")
			assert.Equal(t, tc.wantJSON, opts.OutputJSON)
			assert.Equal(t, tc.wantNonInter, opts.NonInteractive)
			assert.NotNil(t, opts.Prompt, "the form must be wired")
			assert.NotNil(t, opts.Confirm, "the confirmation must be wired")
			assert.NotNil(t, opts.Out, "the import report needs a sink")
		})
	}
}

// TestRunDeployMenu_KeygateImportFeedsTheSameInvocation is the point of running
// the gate before LoadConfigOrWrap: an identity accepted at the offer decrypts
// the config the wizard's preflight then sees, with no reload.
func TestRunDeployMenu_KeygateImportFeedsTheSameInvocation(t *testing.T) {
	isolateHome(t)
	id, err := secrets.Keygen()
	require.NoError(t, err)
	marker, err := secrets.Encrypt("s3cr3t-value", id.Recipient())
	require.NoError(t, err)

	root := writeSecretsProject(t, id.Recipient(), marker)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(root, "workspace.yml")}

	stubMenuTTY(t, menuWizard)

	oldKeygate := keygateEnsureFn
	oldPreflight := runPreWizardPreflightFn
	t.Cleanup(func() {
		keygateEnsureFn = oldKeygate
		runPreWizardPreflightFn = oldPreflight
	})
	// Stands in for a completed import: the identity becomes available to the
	// process exactly as WriteKeyfile would have made it.
	keygateEnsureFn = func(context.Context, keygate.Options) (bool, error) {
		t.Setenv(secrets.EnvKey, id.Export())
		return true, nil
	}

	var seenToken any
	runPreWizardPreflightFn = func(_ context.Context, cfg *config.DweConfig, _ string, _ io.Writer) error {
		seenToken = cfg.Vars["token"]
		// Stops the flow here; the wizard itself is not this test's subject.
		return &deployValidationError{"stop"}
	}

	cmd := &cobra.Command{}
	cmd.SetOut(bytes.NewBuffer(nil))
	cmd.SetErr(bytes.NewBuffer(nil))

	require.Error(t, runDeployMenu(cmd, flags))
	assert.Equal(t, "s3cr3t-value", seenToken, "the menu's single config load must already be decrypted")
}

// TestRunDeployMenu_KeygateRefusalsAreTyped pins that all three gate refusals
// reach the user as the same typed envelope `dwe secrets` uses, and that the
// menu never opens behind one.
func TestRunDeployMenu_KeygateRefusalsAreTyped(t *testing.T) {
	isolateHome(t)
	id, err := secrets.Keygen()
	require.NoError(t, err)
	marker, err := secrets.Encrypt("s3cr3t-value", id.Recipient())
	require.NoError(t, err)

	for _, sentinel := range []error{keygate.ErrAborted, keygate.ErrEnvSourceUnusable, keygate.ErrKeyfileUnusable} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			root := writeSecretsProject(t, id.Recipient(), marker)
			flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(root, "workspace.yml")}

			oldIsInteractive := widgets.IsInteractiveFn
			oldSelectFn := selectMenuItemFn
			oldPreflight := runPreWizardPreflightFn
			t.Cleanup(func() {
				widgets.IsInteractiveFn = oldIsInteractive
				selectMenuItemFn = oldSelectFn
				runPreWizardPreflightFn = oldPreflight
			})
			widgets.IsInteractiveFn = func(io.Reader) bool { return true }
			selectMenuItemFn = func(context.Context, *cobra.Command, *journal.PendingApply, bool) (menuChoice, error) {
				t.Error("the menu must not open when the gate refused")
				return menuExit, nil
			}
			runPreWizardPreflightFn = func(context.Context, *config.DweConfig, string, io.Writer) error {
				t.Error("preflight must not run when the gate refused")
				return nil
			}
			stubKeygate(t, false, fmt.Errorf("%w: refused", sentinel))

			cmd := &cobra.Command{}
			cmd.SetOut(bytes.NewBuffer(nil))
			cmd.SetErr(bytes.NewBuffer(nil))

			err := runDeployMenu(cmd, flags)
			require.Error(t, err)

			coded, ok := errors.AsType[*cmdctx.CodedError](err)
			require.True(t, ok, "expected a typed envelope, got %T", err)
			assert.Equal(t, "secrets_no_identity", coded.Code)
			assert.Equal(t, id.Recipient(), coded.Details["recipient"])
			assert.Equal(t, secrets.IdentityHint(id.Recipient()), coded.Hint)
			assert.NotContains(t, coded.Message, id.Export(), "a refusal must never carry key material")
		})
	}
}

// TestRunDeployMenu_KeygateSkippedOnUnloadableConfig: a config that does not
// even parse is LoadConfigOrWrap's story. The gate gets nil layers, skips
// itself, and the user sees today's wording.
func TestRunDeployMenu_KeygateSkippedOnUnloadableConfig(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"),
		[]byte("project:\n  name: [unterminated\n"), 0o644))
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(root, "workspace.yml")}

	stubMenuTTY(t, menuExit)
	seen := stubKeygate(t, false, nil)

	cmd := &cobra.Command{}
	cmd.SetOut(bytes.NewBuffer(nil))
	cmd.SetErr(bytes.NewBuffer(nil))

	err := runDeployMenu(cmd, flags)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading config:", "the config error must keep its own wording")

	opts := *seen
	require.NotNil(t, opts)
	assert.Nil(t, opts.Layers, "an unreadable layer set must reach the gate as nil so it skips itself")
}

// TestRunDeployMenu_KeygateGatesThePlanPath documents that the gate sits at menu
// entry, so `plan` is covered too — a plan rendered from `<encrypted>` markers
// would otherwise print commands the deploy never runs.
func TestRunDeployMenu_KeygateGatesThePlanPath(t *testing.T) {
	isolateHome(t)
	id, err := secrets.Keygen()
	require.NoError(t, err)
	marker, err := secrets.Encrypt("s3cr3t-value", id.Recipient())
	require.NoError(t, err)

	root := writeSecretsProject(t, id.Recipient(), marker)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(root, "workspace.yml")}

	stubMenuTTY(t, menuPlan)

	var order []string
	oldKeygate := keygateEnsureFn
	oldPlan := runDeployPlanFn
	t.Cleanup(func() {
		keygateEnsureFn = oldKeygate
		runDeployPlanFn = oldPlan
	})
	keygateEnsureFn = func(context.Context, keygate.Options) (bool, error) {
		order = append(order, "gate")
		t.Setenv(secrets.EnvKey, id.Export())
		return true, nil
	}
	runDeployPlanFn = func(context.Context, *cobra.Command, *cmdctx.RootFlags, deployPlanOpts) error {
		order = append(order, "plan")
		return nil
	}

	cmd := &cobra.Command{}
	cmd.SetOut(bytes.NewBuffer(nil))
	cmd.SetErr(bytes.NewBuffer(nil))

	require.NoError(t, runDeployMenu(cmd, flags))
	assert.Equal(t, []string{"gate", "plan"}, order)
}

// TestRunDeployMenu_KeygateIsInertWithoutSecrets is the backward-compatibility
// pin: on a project with no secrets the REAL gate runs and must leave the menu
// byte-identical to the same run with the gate stubbed out.
func TestRunDeployMenu_KeygateIsInertWithoutSecrets(t *testing.T) {
	isolateHome(t)
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"),
		[]byte("project:\n  name: test\n"), 0o644))
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(root, "workspace.yml")}

	stubMenuTTY(t, menuExit)

	run := func() (string, string) {
		t.Helper()
		var out, errOut bytes.Buffer
		cmd := &cobra.Command{}
		cmd.SetOut(&out)
		cmd.SetErr(&errOut)
		require.NoError(t, runDeployMenu(cmd, flags))
		return out.String(), errOut.String()
	}

	// The real gate: it must reach its "no encrypted surface" exit silently.
	realOut, realErr := run()

	oldKeygate := keygateEnsureFn
	t.Cleanup(func() { keygateEnsureFn = oldKeygate })
	keygateEnsureFn = func(context.Context, keygate.Options) (bool, error) { return false, nil }
	stubbedOut, stubbedErr := run()

	assert.Equal(t, stubbedOut, realOut, "a project without secrets must see byte-identical stdout")
	assert.Equal(t, stubbedErr, realErr, "a project without secrets must see byte-identical stderr")
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
