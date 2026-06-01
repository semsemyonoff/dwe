package service

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	localpkg "github.com/semsemyonoff/dwe/internal/core/project/local"
	"github.com/semsemyonoff/dwe/internal/core/project/services"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
)

// NewCmd builds the `devbox services` command tree: bare-form opens a
// multi-select toggle; subcommands enable / disable handle individual services.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	var apply, printPlan, skipHooks bool
	cmd := &cobra.Command{
		GroupID: groupID,
		Use:     "services",
		Short:   "Toggle optional services (interactive) or list / enable / disable",
		Long: `Open an interactive multi-select form to enable or disable optional services.
Required services (including required infra) are always active and shown
pre-checked / locked. Optional infra services (required: false) appear
alongside apps and tools.

On submit, changes are written to devbox/local.yml and .env is regenerated.

Use --print-plan to preview what lifecycle steps will run after the selection
without making any changes (you will still use the interactive selector).
Use --apply to execute the plan non-interactively after writing local.yml.

When stdin is not a TTY (piped or non-interactive) or when --output json is
set, the command renders a read-only listing of every service and its status
instead of opening the toggle. For a richer view including topology, deploy
state, and daemons, run 'devbox status'.`,
		Example: `  devbox services
  devbox services --print-plan
  devbox services enable adminer
  devbox services disable second`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServicesToggle(cmd, flags, singleToggleFlags{
				apply:     apply,
				printPlan: printPlan,
				skipHooks: skipHooks,
			})
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "execute the plan non-interactively after writing local.yml")
	cmd.Flags().BoolVar(&printPlan, "print-plan", false, "preview what would happen without making any changes")
	cmd.Flags().BoolVar(&skipHooks, "skip-hooks", false, "skip before/after hook commands when applying")
	cmd.AddCommand(newServiceEnableCmd(flags))
	cmd.AddCommand(newServiceDisableCmd(flags))
	return cmd
}

// runServicesToggle opens the interactive multi-select toggle form. When
// --output json is set or stdin is not a TTY, dispatches to the read-only
// list renderer. All-mandatory short-circuits.
func runServicesToggle(cmd *cobra.Command, flags *cmdctx.RootFlags, opts singleToggleFlags) error {
	if flags.Output == "json" || !widgets.IsInteractiveFn(cmd.InOrStdin()) {
		return runServicesList(cmd, flags)
	}

	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	rows := services.BuildRows(cfg)
	togglable := 0
	for _, row := range rows {
		if !row.Mandatory {
			togglable++
		}
	}
	if togglable == 0 {
		return fmt.Errorf("nothing to toggle, see 'devbox status'")
	}

	items := make([]widgets.MultiSelectItem, len(rows))
	var lockedNames []string
	for i, row := range rows {
		items[i] = widgets.MultiSelectItem{
			Key:         row.Name,
			Label:       formatServiceToggleOptionLabel(row),
			Description: formatServiceToggleOptionDescription(row),
			Locked:      row.Mandatory,
			Selected:    row.Enabled,
		}
		if row.Mandatory {
			lockedNames = append(lockedNames, formatServiceLockedLabel(row))
		}
	}

	if len(lockedNames) > 0 {
		w := render.NewWriter(cmd.OutOrStdout())
		_, _ = fmt.Fprintln(w.Writer(), styles.StyleSubheader("Always on: ")+styles.StyleMuted(strings.Join(lockedNames, ", ")))
	}

	result, err := runMultiSelect("Toggle services:", items)
	if err != nil {
		if errors.Is(err, widgets.ErrCancelled) {
			return nil
		}
		return err
	}

	selections := make([]localpkg.ServiceSelection, len(rows))
	for i, row := range rows {
		selections[i] = localpkg.ServiceSelection{Name: row.Name, Enabled: row.Enabled, Mandatory: row.Mandatory}
	}
	toEnable, toDisable := localpkg.DiffServiceSelection(selections, result.Kept)
	if len(toEnable) == 0 && len(toDisable) == 0 {
		return nil
	}

	baseDir := flags.ProjectRoot()
	reg, regErr := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
	if regErr != nil {
		return fmt.Errorf("loading command registry: %w", regErr)
	}

	svcDeploys, err := config.LoadServiceDeployConfigs(baseDir, cfg.Services)
	if err != nil {
		return fmt.Errorf("loading service deploy configs: %w", err)
	}

	statePath := filepath.Join(baseDir, journal.DefaultRelPath)
	deployedServices, journalErr := loadDeployedServices(statePath)
	warnDeployedServicesLoad(cmd.ErrOrStderr(), journalErr)

	// --print-plan: build plan in-memory from the current (pre-mutation) config,
	// render to stdout, then return without any filesystem mutations.
	if opts.printPlan {
		var toggles []ToggleAction
		for _, name := range toEnable {
			toggles = append(toggles, ToggleAction{Service: name, Direction: DirectionEnable})
		}
		for _, name := range toDisable {
			toggles = append(toggles, ToggleAction{Service: name, Direction: DirectionDisable})
		}
		plan, err := buildTogglePlan(cfg, reg, svcDeploys, toggles, deployedServices)
		if err != nil {
			return err
		}
		renderTogglePlan(cmd.OutOrStdout(), plan)
		return nil
	}

	localPath := filepath.Join(baseDir, "workspace", "local.yml")
	envPath := filepath.Join(baseDir, ".env")

	stackRunning := probeStackOrWarn(cmd.ErrOrStderr(), cfg, baseDir)

	plan, contributors, cfgNew, err := mutateAndPlanBatch(
		cmd.OutOrStdout(),
		baseDir, flags.ConfigPath, localPath, envPath, statePath,
		cfg, reg, svcDeploys, deployedServices,
		toEnable, toDisable,
	)
	if err != nil {
		return err
	}

	deps := ExecuteDeps{
		Cmd:        cmd,
		Flags:      flags,
		BaseDir:    baseDir,
		StatePath:  statePath,
		Cfg:        cfgNew,
		CmdReg:     reg,
		RunDeploy:  multiToggleRunDeploy,
		RunRestart: multiToggleRunRestart,
		RunUserCmd: multiToggleRunUserCmd,
	}
	execOpts := ExecuteOptions{
		SkipHooks:    opts.skipHooks,
		Contributors: contributors,
	}

	// Explicit --apply always executes (even when stack probe reports stopped).
	if opts.apply {
		return executeTogglePlan(cmd.Context(), deps, plan, execOpts)
	}

	if len(plan.ApplySteps) == 0 && len(plan.BeforeSteps) == 0 && len(plan.AfterSteps) == 0 {
		return nil
	}

	// Stack not running and no --apply: pending already recorded; defer apply.
	if !stackRunning {
		warnStackStopped(cmd.OutOrStdout(), plan)
		return nil
	}

	if len(plan.ApplySteps) == 0 {
		// Hooks exist but no apply step — execute immediately without prompting.
		return executeTogglePlan(cmd.Context(), deps, plan, execOpts)
	}

	// We're always in TTY here (checked at the top), so prompt the user.
	ok, err := confirmApplyPrompt()
	if err != nil {
		if errors.Is(err, widgets.ErrCancelled) {
			return nil
		}
		return err
	}
	if ok {
		return executeTogglePlan(cmd.Context(), deps, plan, execOpts)
	}
	return nil
}

func formatServiceToggleLabel(row services.Row) string {
	return styles.IconPrefix(row.Icon) + styles.StyleServiceName(row.Type, row.Name, rowActive(row))
}

func formatServiceToggleOptionLabel(row services.Row) string {
	return styles.IconPrefix(row.Icon) + styles.StyleServiceOptionName(row.Type, row.Name)
}

func formatServiceToggleDescription(row services.Row) string {
	active := rowActive(row)
	typeBadge := styles.StyleServiceType(row.Type, "["+row.Type+"]", active)
	container := styles.StyleServiceContainer(displayContainer(row.Container), active)
	return typeBadge + " " + container
}

func formatServiceToggleOptionDescription(row services.Row) string {
	typeBadge := styles.StyleServiceOptionType(row.Type, "["+row.Type+"]")
	container := styles.StyleServiceOptionContainer(displayContainer(row.Container))
	return typeBadge + " " + container
}

func formatServiceLockedLabel(row services.Row) string {
	return formatServiceToggleLabel(row) + " " + formatServiceToggleDescription(row)
}

func displayContainer(container string) string {
	if container == "" {
		return "-"
	}
	return container
}

func rowActive(row services.Row) bool {
	return row.Mandatory || row.Enabled
}

// selectToggleFn is a function type for interactive service selection used by
// enable/disable commands when no service argument is provided.
type selectToggleFn func(title string, items []widgets.SelectorItem) (int, error)

// defaultSelectToggle calls widgets.RunSelector.
var defaultSelectToggle selectToggleFn = widgets.RunSelector

// pickServiceToEnable returns the name of a disabled non-required service to enable.
func pickServiceToEnable(cfg *config.DweConfig, selector selectToggleFn) (string, error) {
	var candidates []string
	for _, name := range services.SortedNames(cfg.Services) {
		svc := cfg.Services[name]
		if services.IsManageable(svc) && !svc.Required && !svc.Enabled {
			candidates = append(candidates, name)
		}
	}
	return pickToggleCandidates(cfg, candidates, "disabled", "Select a service to enable:", selector)
}

// pickServiceToDisable returns the name of an enabled non-required service to disable.
func pickServiceToDisable(cfg *config.DweConfig, selector selectToggleFn) (string, error) {
	var candidates []string
	for _, name := range services.SortedNames(cfg.Services) {
		svc := cfg.Services[name]
		if services.IsManageable(svc) && !svc.Required && svc.Enabled {
			candidates = append(candidates, name)
		}
	}
	return pickToggleCandidates(cfg, candidates, "enabled", "Select a service to disable:", selector)
}

func pickToggleCandidates(cfg *config.DweConfig, names []string, statusLabel, title string, selector selectToggleFn) (string, error) {
	if len(names) == 0 {
		return "", fmt.Errorf("no %s optional services found", statusLabel)
	}
	items := make([]widgets.SelectorItem, len(names))
	for i, name := range names {
		svc := cfg.Services[name]
		row := services.Row{
			Name:      name,
			Type:      string(svc.Type),
			Icon:      svc.DisplayIcon(),
			Container: svc.Container,
			Mandatory: svc.Required,
			Enabled:   svc.Enabled,
		}
		items[i] = widgets.SelectorItem{
			Label:       formatServiceToggleLabel(row),
			Description: formatServiceToggleDescription(row),
			Status:      statusLabel,
		}
	}
	idx, err := selector(title, items)
	if err != nil {
		return "", err
	}
	if idx < 0 || idx >= len(names) {
		return "", fmt.Errorf("selector returned invalid index %d for %d candidates", idx, len(names))
	}
	return names[idx], nil
}

func newServiceEnableCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var apply, printPlan, skipHooks bool
	cmd := &cobra.Command{
		Use:   "enable [service]",
		Short: "Enable an optional service (writes to devbox/local.yml)",
		Long: `Enable an optional service by writing services.<name>.enabled = true to devbox/local.yml.

The .env file is regenerated automatically after the change.
Lifecycle hooks defined in on_enable are planned and optionally executed.

Use --print-plan to preview what will happen without making any changes.
Use --apply to execute the plan non-interactively (useful in CI/scripts).

When no service name is given, an interactive selector shows all currently
disabled optional services.`,
		Example:           "  devbox services enable second",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: serviceCompletion(flags, completeDisabledOptional),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.ConfigPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
				// Required enable is a no-op + warning.
				if svc, ok := cfg.Services[name]; ok && svc.Required {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: service %q is already required; nothing to do\n", name)
					return nil
				}
				// Already-enabled optional service: no-op to avoid spurious pending ops.
				if svc, ok := cfg.Services[name]; ok && !svc.Required && svc.Enabled {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "service %q is already enabled\n", name)
					return nil
				}
			} else {
				if !widgets.IsInteractiveFn(cmd.InOrStdin()) {
					return fmt.Errorf("no service name given; pass a service name or run in an interactive terminal")
				}
				name, err = pickServiceToEnable(cfg, defaultSelectToggle)
				if err != nil {
					if errors.Is(err, widgets.ErrCancelled) {
						return nil
					}
					return err
				}
			}
			return runSingleServiceToggle(cmd.Context(), cmd, flags, name, DirectionEnable, singleToggleFlags{
				apply:     apply,
				printPlan: printPlan,
				skipHooks: skipHooks,
			})
		},
		SilenceUsage: true,
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "execute the plan non-interactively after writing local.yml")
	cmd.Flags().BoolVar(&printPlan, "print-plan", false, "preview what would happen without making any changes")
	cmd.Flags().BoolVar(&skipHooks, "skip-hooks", false, "skip before/after hook commands when applying")
	return cmd
}

func newServiceDisableCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var apply, printPlan, skipHooks bool
	cmd := &cobra.Command{
		Use:   "disable [service]",
		Short: "Disable an optional service (writes to devbox/local.yml)",
		Long: `Disable an optional service by writing services.<name>.enabled = false to devbox/local.yml.

The .env file is regenerated automatically after the change.
Lifecycle hooks defined in on_disable are planned and optionally executed.

Use --print-plan to preview what will happen without making any changes.
Use --apply to execute the plan non-interactively (useful in CI/scripts).

When no service name is given, an interactive selector shows all currently
enabled optional services.`,
		Example:           "  devbox services disable second",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: serviceCompletion(flags, completeEnabledOptional),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.ConfigPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
				// Cannot disable required.
				if svc, ok := cfg.Services[name]; ok && svc.Required {
					return fmt.Errorf("cannot disable required service %q", name)
				}
				// Already-disabled optional service: no-op to avoid spurious pending ops.
				if svc, ok := cfg.Services[name]; ok && !svc.Required && !svc.Enabled {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "service %q is already disabled\n", name)
					return nil
				}
			} else {
				if !widgets.IsInteractiveFn(cmd.InOrStdin()) {
					return fmt.Errorf("no service name given; pass a service name or run in an interactive terminal")
				}
				name, err = pickServiceToDisable(cfg, defaultSelectToggle)
				if err != nil {
					if errors.Is(err, widgets.ErrCancelled) {
						return nil
					}
					return err
				}
			}
			return runSingleServiceToggle(cmd.Context(), cmd, flags, name, DirectionDisable, singleToggleFlags{
				apply:     apply,
				printPlan: printPlan,
				skipHooks: skipHooks,
			})
		},
		SilenceUsage: true,
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "execute the plan non-interactively after writing local.yml")
	cmd.Flags().BoolVar(&printPlan, "print-plan", false, "preview what would happen without making any changes")
	cmd.Flags().BoolVar(&skipHooks, "skip-hooks", false, "skip before/after hook commands when applying")
	return cmd
}

// service completion filters.
type serviceFilter int

const (
	completeDisabledOptional serviceFilter = iota
	completeEnabledOptional
)

func serviceCompletion(flags *cmdctx.RootFlags, filter serviceFilter) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		configPath, _, err := cmdctx.CompletionConfigPath(flags, cmd)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for _, name := range services.SortedNames(cfg.Services) {
			svc := cfg.Services[name]
			if svc.Required || !services.IsManageable(svc) {
				continue
			}
			switch filter {
			case completeDisabledOptional:
				if !svc.Enabled {
					names = append(names, name)
				}
			case completeEnabledOptional:
				if svc.Enabled {
					names = append(names, name)
				}
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}
