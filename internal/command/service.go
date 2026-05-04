package command

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/envfile"
	"devbox-cli/internal/localconfig"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"

	"github.com/spf13/cobra"
)

func newServiceCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "services",
		Short: "Manage application services",
		Long: `List, enable, or disable application services defined in the project config.

Mandatory services are always active and cannot be toggled.
Optional services can be enabled or disabled; the change is written to devbox/local.yml.

Use 'services status' to display a read-only table of all services and their current state.
Use 'services list' for an interactive toggle form (TTY) or the same table (non-TTY).`,
		Example: `  devbox services status
  devbox services list
  devbox services enable second
  devbox services disable second`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newServiceStatusCmd(flags))
	cmd.AddCommand(newServiceListCmd(flags))
	cmd.AddCommand(newServiceEnableCmd(flags))
	cmd.AddCommand(newServiceDisableCmd(flags))
	return cmd
}

func newServiceStatusCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   "Show all services and their current state (read-only table)",
		Long:    `Show all services defined in the project with their container names, enabled state, and running status.`,
		Example: "  devbox services status",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyStyles(flags.ProjectRoot(), cmd.ErrOrStderr())
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			projectName, err := resolveProjectName(flags.configPath, cfg)
			if err != nil {
				return err
			}
			dockerBin := config.DockerBin(cfg)
			isRunning := func(_, container string) bool {
				return containerRunning(projectName, container, dockerBin)
			}
			return runServiceList(render.Stdout(), cfg, isRunning)
		},
		SilenceUsage: true,
	}
}

func newServiceListCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Toggle services interactively (TTY) or show status table (non-TTY)",
		Long: `Toggle optional services on or off using an interactive multi-select form.

Mandatory services are always active and are shown above the form as "Always on".
On submit, newly-checked services are enabled and newly-unchecked services are
disabled in devbox/local.yml; .env is regenerated once.

In non-TTY mode (piped stdin or no terminal) the command falls back to printing
the read-only status table.`,
		Example: `  devbox services list
  devbox services list | cat   # non-TTY: prints the table`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyStyles(flags.ProjectRoot(), cmd.ErrOrStderr())
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			// Non-TTY: fall back to the read-only table.
			if !ui.IsInteractiveFn(cmd.InOrStdin()) {
				projectName, err := resolveProjectName(flags.configPath, cfg)
				if err != nil {
					return err
				}
				dockerBin := config.DockerBin(cfg)
				isRunning := func(_, container string) bool {
					return containerRunning(projectName, container, dockerBin)
				}
				return runServiceList(render.Stdout(), cfg, isRunning)
			}

			// TTY: build multi-select items from service rows.
			rows := buildServiceRows(cfg)
			items := make([]ui.MultiSelectItem, len(rows))
			var lockedNames []string
			for i, row := range rows {
				items[i] = ui.MultiSelectItem{
					Key:         row.Name,
					Label:       row.Name,
					Description: row.Container,
					Locked:      row.Mandatory,
					Selected:    row.Enabled,
				}
				if row.Mandatory {
					lockedNames = append(lockedNames, row.Name)
				}
			}

			// Print always-on header before the form.
			if len(lockedNames) > 0 {
				w := render.Stdout()
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

			// Print one-line summary of all changes.
			var parts []string
			if len(toEnable) > 0 {
				parts = append(parts, "enabled: "+strings.Join(toEnable, ", "))
			}
			if len(toDisable) > 0 {
				parts = append(parts, "disabled: "+strings.Join(toDisable, ", "))
			}
			render.Stdout().Success(strings.Join(parts, "; "))

			envPath, err := envfile.Regenerate(flags.configPath)
			if err != nil {
				return err
			}
			render.Stdout().Info(fmt.Sprintf(".env regenerated → %s", envPath))
			return nil
		},
		SilenceUsage: true,
	}
}

// resolveProjectName returns the compose project name from docker.yml.
// If docker.yml does not exist, the config default is returned (nil error).
// Any other load/parse/template error is returned so the caller can fail fast
// rather than silently querying the wrong container names.
func resolveProjectName(configPath string, cfg *config.DevboxConfig) (string, error) {
	baseDir := filepath.Dir(configPath)
	dockerCfg, err := config.LoadDockerConfig(baseDir, cfg)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg.Project.FullName(), nil
		}
		return "", fmt.Errorf("loading docker config: %w", err)
	}
	if dockerCfg.ProjectName != "" {
		return dockerCfg.ProjectName, nil
	}
	return cfg.Project.FullName(), nil
}

// containerCheckFn checks whether a container with the given name is running.
type containerCheckFn func(projectFullName, containerName string) bool

// containerRunning checks if a Docker container is running by full container name.
// Uses docker inspect to get an exact name match (docker ps name filter uses substring
// matching against the full /name path which is not portable across Docker versions).
// dockerBin is the Docker-compatible binary (e.g. "docker", "podman").
func containerRunning(projectFullName, containerName, dockerBin string) bool {
	fullName := projectFullName + "-" + containerName
	out, err := exec.Command(
		dockerBin, "inspect", //nolint:gosec
		"--format", "{{.State.Status}}",
		fullName,
	).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "running"
}

// runServiceList prints the service list as a styled Lipgloss table.
func runServiceList(w *render.Writer, cfg *config.DevboxConfig, isRunning containerCheckFn) error {
	names := sortedKeys(cfg.Services)
	projectFull := cfg.Project.FullName()

	rows := make([]ui.ServiceTableRow, 0, len(names))
	for _, name := range names {
		svc := cfg.Services[name]
		running := false
		if svc.Mandatory || svc.Enabled {
			running = isRunning(projectFull, svc.Container)
		}
		rows = append(rows, ui.ServiceTableRow{
			Name:      name,
			Container: svc.Container,
			Mandatory: svc.Mandatory,
			Enabled:   svc.Enabled,
			Running:   running,
		})
	}

	_, _ = fmt.Fprintln(w.Writer(), ui.RenderServiceTable(rows))
	return nil
}

// selectToggleFn is a function type for interactive service selection used by
// enable/disable commands when no service argument is provided.
type selectToggleFn func(title string, items []ui.SelectorItem) (int, error)

// defaultSelectToggle calls ui.RunSelector.
var defaultSelectToggle selectToggleFn = ui.RunSelector

// pickServiceToEnable returns the name of a disabled non-mandatory service to enable.
// If no disabled optional services exist, returns an error.
// Otherwise the selector is called with all disabled optional services.
func pickServiceToEnable(cfg *config.DevboxConfig, selector selectToggleFn) (string, error) {
	var candidates []string
	for _, name := range sortedKeys(cfg.Services) {
		svc := cfg.Services[name]
		if !svc.Mandatory && !svc.Enabled {
			candidates = append(candidates, name)
		}
	}
	return pickToggleCandidates(cfg, candidates, "disabled", "Select a service to enable:", selector)
}

// pickServiceToDisable returns the name of an enabled non-mandatory service to disable.
// If no enabled optional services exist, returns an error.
// Otherwise the selector is called with all enabled optional services.
func pickServiceToDisable(cfg *config.DevboxConfig, selector selectToggleFn) (string, error) {
	var candidates []string
	for _, name := range sortedKeys(cfg.Services) {
		svc := cfg.Services[name]
		if !svc.Mandatory && svc.Enabled {
			candidates = append(candidates, name)
		}
	}
	return pickToggleCandidates(cfg, candidates, "enabled", "Select a service to disable:", selector)
}

// pickToggleCandidates resolves a service name from a candidate list.
// - Empty list → error mentioning statusLabel.
// - One or more → selector is always invoked.
func pickToggleCandidates(cfg *config.DevboxConfig, names []string, statusLabel, title string, selector selectToggleFn) (string, error) {
	if len(names) == 0 {
		return "", fmt.Errorf("no %s optional services found", statusLabel)
	}
	items := make([]ui.SelectorItem, len(names))
	for i, name := range names {
		svc := cfg.Services[name]
		items[i] = ui.SelectorItem{
			Label:       name,
			Description: svc.Container,
			Status:      statusLabel,
		}
	}
	idx, err := selector(title, items)
	if err != nil {
		return "", err
	}
	return names[idx], nil
}

func newServiceEnableCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "enable [service]",
		Short: "Enable an optional service (writes to devbox/local.yml)",
		Long: `Enable an optional service by writing services.<name>.enabled = true to devbox/local.yml.

The .env file is regenerated automatically after the change.
Use 'devbox up' to start the newly enabled service.

When no service name is given, an interactive selector shows all currently
disabled optional services.`,
		Example:           "  devbox services enable second",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: optionalServiceNameCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
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
			return setServiceEnabled(flags.configPath, cfg, name, true)
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
Use 'devbox stop <container>' or 'devbox down' to stop the service.

When no service name is given, an interactive selector shows all currently
enabled optional services.`,
		Example:           "  devbox services disable second",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: optionalServiceNameCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
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
			return setServiceEnabled(flags.configPath, cfg, name, false)
		},
		SilenceUsage: true,
	}
}

// optionalServiceNameCompletion completes non-mandatory service names (those
// that can be enabled or disabled).
func optionalServiceNameCompletion(flags *rootFlags) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
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
			return nil, cobra.ShellCompDirectiveError
		}
		var names []string
		for name, svc := range cfg.Services {
			if !svc.Mandatory {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

// applyServiceTogglesBatch loads devbox/local.yml once, validates and applies all
// toggles in-memory, then writes the file once. Either every change is persisted
// or none are — eliminating partial-state risk if a later validation fails after
// earlier ones already wrote to disk.
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
func setServiceEnabled(configPath string, cfg *config.DevboxConfig, name string, enabled bool) error {
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
	w := render.Stdout()
	if enabled {
		w.Success(fmt.Sprintf("service %q enabled (written to %s)", name, localPath))
	} else {
		w.Success(fmt.Sprintf("service %q disabled (written to %s)", name, localPath))
	}
	envPath, err := envfile.Regenerate(configPath)
	if err != nil {
		return err
	}
	render.Stdout().Info(fmt.Sprintf(".env regenerated → %s", envPath))
	return nil
}
