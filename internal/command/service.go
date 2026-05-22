package command

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/envfile"
	"devbox-cli/internal/localconfig"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"

	"github.com/spf13/cobra"
)

// ErrInteractiveRequired is returned when a mutating command needs a TTY but
// stdin is not interactive. Commands return it so cobra yields a non-zero exit.
var ErrInteractiveRequired = errors.New("interactive terminal required")

func newServiceCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "services",
		Short: "Toggle app/tool services (interactive) or enable/disable individually",
		Long: `Open an interactive multi-select form to enable or disable app/tool services.
Infra services are config-managed and are intentionally not shown here.

Mandatory services are always active and shown pre-checked / locked.
On submit, changes are written to devbox/local.yml and .env is regenerated.

For a read-only view, run 'devbox status' or one of 'devbox status apps / tools / infra'.`,
		Example: `  devbox services
  devbox services enable adminer
  devbox services disable second`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServicesToggle(cmd, flags)
		},
	}
	cmd.AddCommand(newServiceEnableCmd(flags))
	cmd.AddCommand(newServiceDisableCmd(flags))
	return cmd
}

// runServicesToggle opens the interactive multi-select toggle form. Non-TTY
// returns ErrInteractiveRequired with a hint. All-mandatory short-circuits.
func runServicesToggle(cmd *cobra.Command, flags *rootFlags) error {
	if !ui.IsInteractiveFn(cmd.InOrStdin()) {
		return fmt.Errorf("%w: services: interactive toggle requires a TTY; use 'devbox status' for read-only view", ErrInteractiveRequired)
	}

	cfg, err := config.LoadConfig(flags.configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	rows := buildServiceRows(cfg)
	togglable := 0
	for _, row := range rows {
		if !row.Mandatory {
			togglable++
		}
	}
	if togglable == 0 {
		return fmt.Errorf("nothing to toggle, see 'devbox status'")
	}

	items := make([]ui.MultiSelectItem, len(rows))
	var lockedNames []string
	for i, row := range rows {
		items[i] = ui.MultiSelectItem{
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
		_, _ = fmt.Fprintln(w.Writer(), ui.StyleSubheader("Always on: ")+ui.StyleMuted(strings.Join(lockedNames, ", ")))
	}

	result, err := runMultiSelect("Toggle services:", items)
	if err != nil {
		if errors.Is(err, ui.ErrCancelled) {
			return nil
		}
		return err
	}

	selections := make([]localconfig.ServiceSelection, len(rows))
	for i, row := range rows {
		selections[i] = localconfig.ServiceSelection{Name: row.Name, Enabled: row.Enabled, Mandatory: row.Mandatory}
	}
	toEnable, toDisable := localconfig.DiffServiceSelection(selections, result.Kept)
	if len(toEnable) == 0 && len(toDisable) == 0 {
		return nil
	}

	if err := applyServiceTogglesBatch(flags.configPath, cfg, toEnable, toDisable); err != nil {
		return err
	}

	var parts []string
	if len(toEnable) > 0 {
		parts = append(parts, "enabled: "+strings.Join(toEnable, ", "))
	}
	if len(toDisable) > 0 {
		parts = append(parts, "disabled: "+strings.Join(toDisable, ", "))
	}
	render.NewWriter(cmd.OutOrStdout()).Success(strings.Join(parts, "; "))

	envPath, err := envfile.Regenerate(flags.configPath)
	if err != nil {
		return err
	}
	render.NewWriter(cmd.OutOrStdout()).Info(fmt.Sprintf(".env regenerated → %s", envPath))
	return nil
}

func formatServiceToggleLabel(row serviceRow) string {
	return ui.StyleServiceName(row.Type, row.Name, rowActive(row))
}

func formatServiceToggleOptionLabel(row serviceRow) string {
	return ui.StyleServiceOptionName(row.Type, row.Name)
}

func formatServiceToggleDescription(row serviceRow) string {
	active := rowActive(row)
	typeBadge := ui.StyleServiceType(row.Type, "["+row.Type+"]", active)
	container := ui.StyleServiceContainer(displayContainer(row.Container), active)
	return typeBadge + " " + container
}

func formatServiceToggleOptionDescription(row serviceRow) string {
	typeBadge := ui.StyleServiceOptionType(row.Type, "["+row.Type+"]")
	container := ui.StyleServiceOptionContainer(displayContainer(row.Container))
	return typeBadge + " " + container
}

func formatServiceLockedLabel(row serviceRow) string {
	return formatServiceToggleLabel(row) + " " + formatServiceToggleDescription(row)
}

func displayContainer(container string) string {
	if container == "" {
		return "-"
	}
	return container
}

func rowActive(row serviceRow) bool {
	return row.Mandatory || row.Enabled
}

// selectToggleFn is a function type for interactive service selection used by
// enable/disable commands when no service argument is provided.
type selectToggleFn func(title string, items []ui.SelectorItem) (int, error)

// defaultSelectToggle calls ui.RunSelector.
var defaultSelectToggle selectToggleFn = ui.RunSelector

// pickServiceToEnable returns the name of a disabled non-mandatory service to enable.
func pickServiceToEnable(cfg *config.DevboxConfig, selector selectToggleFn) (string, error) {
	var candidates []string
	for _, name := range sortedServiceNames(cfg.Services) {
		svc := cfg.Services[name]
		if isServiceManageable(svc) && !svc.Mandatory && !svc.Enabled {
			candidates = append(candidates, name)
		}
	}
	return pickToggleCandidates(cfg, candidates, "disabled", "Select a service to enable:", selector)
}

// pickServiceToDisable returns the name of an enabled non-mandatory service to disable.
func pickServiceToDisable(cfg *config.DevboxConfig, selector selectToggleFn) (string, error) {
	var candidates []string
	for _, name := range sortedServiceNames(cfg.Services) {
		svc := cfg.Services[name]
		if isServiceManageable(svc) && !svc.Mandatory && svc.Enabled {
			candidates = append(candidates, name)
		}
	}
	return pickToggleCandidates(cfg, candidates, "enabled", "Select a service to disable:", selector)
}

func pickToggleCandidates(cfg *config.DevboxConfig, names []string, statusLabel, title string, selector selectToggleFn) (string, error) {
	if len(names) == 0 {
		return "", fmt.Errorf("no %s optional services found", statusLabel)
	}
	items := make([]ui.SelectorItem, len(names))
	for i, name := range names {
		svc := cfg.Services[name]
		row := serviceRow{
			Name:      name,
			Type:      string(svc.Type),
			Container: svc.Container,
			Mandatory: svc.Mandatory,
			Enabled:   svc.Enabled,
		}
		items[i] = ui.SelectorItem{
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

func newServiceEnableCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "enable [service]",
		Short: "Enable an optional service (writes to devbox/local.yml)",
		Long: `Enable an optional service by writing services.<name>.enabled = true to devbox/local.yml.

The .env file is regenerated automatically after the change.
Use 'devbox run' to start the newly enabled service.

When no service name is given, an interactive selector shows all currently
disabled optional services.`,
		Example:           "  devbox services enable second",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: serviceCompletion(flags, completeDisabledOptional),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
				if svc, ok := cfg.Services[name]; ok && !isServiceManageable(svc) {
					return fmt.Errorf("cannot enable infra service %q; infra services are managed by config, not devbox services", name)
				}
				// Tightened semantics: mandatory enable is a no-op + warning.
				if svc, ok := cfg.Services[name]; ok && svc.Mandatory {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: service %q is already mandatory; nothing to do\n", name)
					return nil
				}
			} else {
				if !ui.IsInteractiveFn(cmd.InOrStdin()) {
					return fmt.Errorf("no service name given; pass a service name or run in an interactive terminal")
				}
				name, err = pickServiceToEnable(cfg, defaultSelectToggle)
				if err != nil {
					if errors.Is(err, ui.ErrCancelled) {
						return nil
					}
					return err
				}
			}
			return setServiceEnabled(cmd.OutOrStdout(), flags.configPath, cfg, name, true)
		},
		SilenceUsage: true,
	}
}

func newServiceDisableCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "disable [service]",
		Short: "Disable an optional service (writes to devbox/local.yml)",
		Long: `Disable an optional service by writing services.<name>.enabled = false to devbox/local.yml.

The .env file is regenerated automatically after the change.
Use 'devbox stop' to stop the service.

When no service name is given, an interactive selector shows all currently
enabled optional services.`,
		Example:           "  devbox services disable second",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: serviceCompletion(flags, completeEnabledOptional),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
				if svc, ok := cfg.Services[name]; ok && !isServiceManageable(svc) {
					return fmt.Errorf("cannot disable infra service %q; infra services are managed by config, not devbox services", name)
				}
				// Tightened semantics: cannot disable mandatory.
				if svc, ok := cfg.Services[name]; ok && svc.Mandatory {
					return fmt.Errorf("cannot disable mandatory service %q", name)
				}
			} else {
				if !ui.IsInteractiveFn(cmd.InOrStdin()) {
					return fmt.Errorf("no service name given; pass a service name or run in an interactive terminal")
				}
				name, err = pickServiceToDisable(cfg, defaultSelectToggle)
				if err != nil {
					if errors.Is(err, ui.ErrCancelled) {
						return nil
					}
					return err
				}
			}
			return setServiceEnabled(cmd.OutOrStdout(), flags.configPath, cfg, name, false)
		},
		SilenceUsage: true,
	}
}

// service completion filters.
type serviceFilter int

const (
	completeDisabledOptional serviceFilter = iota
	completeEnabledOptional
)

func serviceCompletion(flags *rootFlags, filter serviceFilter) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		configPath, _, err := completionConfigPath(flags, cmd)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for _, name := range sortedServiceNames(cfg.Services) {
			svc := cfg.Services[name]
			if svc.Mandatory || !isServiceManageable(svc) {
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

// applyServiceTogglesBatch loads devbox/local.yml once, validates and applies all
// toggles in-memory, then writes the file once.
func applyServiceTogglesBatch(configPath string, cfg *config.DevboxConfig, toEnable, toDisable []string) error {
	baseDir := filepath.Dir(configPath)
	localPath := filepath.Join(baseDir, "devbox", "local.yml")

	local, err := localconfig.LoadLocalYAML(localPath)
	if err != nil {
		return err
	}

	if err := localconfig.ApplyServiceTogglesToYAML(cfg, local, toEnable, toDisable); err != nil {
		return err
	}

	return localconfig.WriteLocalYAML(localPath, local)
}

// setServiceEnabled writes services.<name>.enabled = value to devbox/local.yml,
// prints a confirmation, and regenerates .env.
func setServiceEnabled(out io.Writer, configPath string, cfg *config.DevboxConfig, name string, enabled bool) error {
	var toEnable, toDisable []string
	if enabled {
		toEnable = []string{name}
	} else {
		toDisable = []string{name}
	}
	if err := applyServiceTogglesBatch(configPath, cfg, toEnable, toDisable); err != nil {
		return err
	}

	baseDir := filepath.Dir(configPath)
	localPath := filepath.Join(baseDir, "devbox", "local.yml")
	w := render.NewWriter(out)
	typeLabel := ""
	if svc, ok := cfg.Services[name]; ok && svc.Type != "" {
		typeLabel = fmt.Sprintf(" [%s]", svc.Type)
	}
	if enabled {
		w.Success(fmt.Sprintf("service %q%s enabled (written to %s)", name, typeLabel, localPath))
	} else {
		w.Success(fmt.Sprintf("service %q%s disabled (written to %s)", name, typeLabel, localPath))
	}
	envPath, err := envfile.Regenerate(configPath)
	if err != nil {
		return err
	}
	w.Info(fmt.Sprintf(".env regenerated → %s", envPath))
	return nil
}
