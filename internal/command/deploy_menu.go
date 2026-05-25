package command

import (
	"context"
	"errors"
	"fmt"
	"os"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/localconfig"
	"devbox-cli/internal/setup"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/validate"
	valsetup "devbox-cli/internal/validate/setup"
	"devbox-cli/internal/validate/env"

	"github.com/spf13/cobra"
	huh "charm.land/huh/v2"
)

// Test seams (package-level vars that tests can override).
var (
	collectPortConflictsFn = env.CollectPortConflicts
	loadSetupYAMLFn        = setup.LoadSetupYAML
	newHuhAskerFn          = setup.NewHuhAsker
	runWizardFn            = setup.Run
	selectMenuItemFn       func(ctx context.Context, cmd *cobra.Command, pending *journal.PendingApply) (menuChoice, error)
	runDeployRunFn         = runDeployRun
	runDeployPlanFn        = runDeployPlan
)

type menuChoice string

const (
	menuRun           menuChoice = "run"
	menuRunService    menuChoice = "run_service"
	menuPlan          menuChoice = "plan"
	menuPlanService   menuChoice = "plan_service"
	menuWizard        menuChoice = "wizard"
	menuExit          menuChoice = "exit"
)

// runDeployMenu is the entry point for `devbox deploy` without subcommands.
// It opens an interactive TUI menu if stdin/stdout are TTY, otherwise prints help.
func runDeployMenu(cmd *cobra.Command, flags *rootFlags) error {
	// Check TTY
	if !ui.IsInteractiveFn(os.Stdin) {
		cmd.Help()
		return usageError("devbox deploy: requires a subcommand or interactive TTY")
	}

	ctx := cmd.Context()
	baseDir := flags.ProjectRoot()
	localPath := fmt.Sprintf("%s/devbox/local.yml", baseDir)
	setupPath := fmt.Sprintf("%s/devbox/setup.yml", baseDir)

	// Load config (already validated by root's PersistentPreRunE)
	cfg, err := config.LoadConfig(flags.configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Load and check local.yml for malformed content
	existing, err := localconfig.LoadLocalYAML(localPath)
	if err != nil {
		return fmt.Errorf("read existing local.yml: %w", err)
	}

	// Probe port conflicts (bypass preflight to allow wizard to fix them)
	conflicts, err := collectPortConflictsFn(ctx, cfg, baseDir)
	if err != nil {
		return fmt.Errorf("probing port conflicts: %w", err)
	}

	// Load setup.yml (capture both result and error; do NOT short-circuit on error)
	setupCfg, setupErr := loadSetupYAMLFn(setupPath)

	// Run setup.*validators to surface any issues before the menu
	validators := valsetup.All(setupCfg, setupErr, setupPath)
	registry := validate.NewRegistry()
	for _, v := range validators {
		registry.Register(v)
	}
	vctx := validate.Context{
		Ctx:         ctx,
		ProjectRoot: baseDir,
		ConfigPath:  flags.configPath,
		Cfg:         cfg,
	}
	diags := registry.Run(vctx)

	// Render diagnostics if any
	if len(diags) > 0 {
		rows := ui.FormatDiagnostics(diags, false)
		ui.RenderDiagnosticsTable(rows)

		// Check for errors
		for _, diag := range diags {
			if diag.Severity >= validate.SeverityError {
				return errors.New("setup.yml has errors; fix them before deploying")
			}
		}
	}

	// Determine wizard visibility: shown iff local.yml is empty/missing AND (setup.yml exists OR conflicts exist)
	showWizard := isEmptyLocal(existing) &&
		((setupCfg != nil && len(setupCfg.Questions) > 0) || len(conflicts) > 0)

	// Load journal for pending-deploy banner
	statePath := fmt.Sprintf("%s/.devbox/deploy/state.yml", baseDir)
	state, err := journal.Load(statePath)
	var pending *journal.PendingApply
	if err == nil && state != nil {
		pending = state.Pending
	}

	// Render pending banner if any
	if pending != nil {
		fmt.Fprintln(cmd.OutOrStdout(), ui.RenderPendingBanner(pending))
	}

	// Get menu choice from the user (or stub in tests)
	if selectMenuItemFn == nil {
		selectMenuItemFn = selectMenuItemInteractive
	}
	choice, err := selectMenuItemFn(ctx, cmd, pending)
	if err != nil {
		if errors.Is(err, setup.ErrWizardCanceled) {
			return nil
		}
		return err
	}

	// Dispatch based on menu choice
	switch choice {
	case menuRun:
		opts := deployRunOpts{
			ServiceName:    "",
			NonInteractive: false,
			SkipPreflight:  false,
		}
		return runDeployRunFn(ctx, cmd, flags, opts)

	case menuRunService:
		// Prompt for service selection
		enabledServices := getEnabledServices(cfg)
		if len(enabledServices) == 0 {
			return errors.New("no enabled services")
		}
		serviceName, err := selectService(ctx, cmd, enabledServices)
		if err != nil {
			return err
		}
		opts := deployRunOpts{
			ServiceName:    serviceName,
			NonInteractive: false,
			SkipPreflight:  false,
		}
		return runDeployRunFn(ctx, cmd, flags, opts)

	case menuPlan:
		opts := deployPlanOpts{
			ServiceName: "",
			Format:      "table",
		}
		return runDeployPlanFn(ctx, cmd, flags, opts)

	case menuPlanService:
		enabledServices := getEnabledServices(cfg)
		if len(enabledServices) == 0 {
			return errors.New("no enabled services")
		}
		serviceName, err := selectService(ctx, cmd, enabledServices)
		if err != nil {
			return err
		}
		opts := deployPlanOpts{
			ServiceName: serviceName,
			Format:      "table",
		}
		return runDeployPlanFn(ctx, cmd, flags, opts)

	case menuWizard:
		if !showWizard {
			return errors.New("wizard not available")
		}

		// Assemble wizard dependencies
		var questions []setup.Question
		if setupCfg != nil {
			questions = setupCfg.Questions
		}
		askQuestions, askPortOverrides := newHuhAskerFn(cmd.OutOrStdout())
		deps := setup.WizardDeps{
			BaseDir:          baseDir,
			LocalPath:        localPath,
			Questions:        questions,
			PortConflicts:    conflicts,
			AskQuestions:     askQuestions,
			AskPortOverrides: askPortOverrides,
		}

		// Run wizard
		if err := runWizardFn(ctx, deps); err != nil {
			if errors.Is(err, setup.ErrWizardCanceled) {
				return nil
			}
			return fmt.Errorf("wizard failed: %w", err)
		}

		// Config reload + preflight + deploy
		opts := deployRunOpts{
			ServiceName:    "",
			NonInteractive: false,
			SkipPreflight:  false,
		}
		return runDeployRunFn(ctx, cmd, flags, opts)

	case menuExit:
		return nil

	default:
		return fmt.Errorf("unknown menu choice: %v", choice)
	}
}

// isEmptyLocal returns true if the local config is missing or empty.
func isEmptyLocal(m map[string]interface{}) bool {
	return len(m) == 0
}

// getEnabledServices returns the list of enabled service names.
func getEnabledServices(cfg *config.DevboxConfig) []string {
	var enabled []string
	for name, svcCfg := range cfg.Services {
		if svcCfg.Enabled {
			enabled = append(enabled, name)
		}
	}
	return enabled
}

// selectService prompts the user to choose a service from the list.
func selectService(ctx context.Context, cmd *cobra.Command, services []string) (string, error) {
	var selected string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select service").
				Options(
					func() []huh.Option[string] {
						var opts []huh.Option[string]
						for _, svc := range services {
							opts = append(opts, huh.NewOption(svc, svc))
						}
						return opts
					}()...,
				).
				Value(&selected),
		),
	).
		WithTheme(ui.Theme()).
		WithOutput(cmd.OutOrStdout())

	if err := ui.RunWithPromptHooks(func() error { return form.Run() }); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", setup.ErrWizardCanceled
		}
		return "", fmt.Errorf("service selection: %w", err)
	}

	return selected, nil
}

// selectMenuItemInteractive prompts the user to select a menu item.
func selectMenuItemInteractive(ctx context.Context, cmd *cobra.Command, pending *journal.PendingApply) (menuChoice, error) {
	var choice string

	options := []huh.Option[string]{
		huh.NewOption("Run (all)", string(menuRun)),
		huh.NewOption("Run service…", string(menuRunService)),
		huh.NewOption("Plan (all)", string(menuPlan)),
		huh.NewOption("Plan service…", string(menuPlanService)),
		huh.NewOption("Wizard", string(menuWizard)),
		huh.NewOption("Exit", string(menuExit)),
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Deploy").
				Options(options...).
				Value(&choice),
		),
	).
		WithTheme(ui.Theme()).
		WithOutput(cmd.OutOrStdout())

	if err := ui.RunWithPromptHooks(func() error { return form.Run() }); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", setup.ErrWizardCanceled
		}
		return "", fmt.Errorf("menu selection: %w", err)
	}

	return menuChoice(choice), nil
}

// usageError is a sentinel error type for usage/help errors (exit code 2).
type usageError string

func (u usageError) Error() string { return string(u) }
func (u usageError) ExitCode() int { return 2 }
