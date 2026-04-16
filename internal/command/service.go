package command

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newServiceCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "services",
		Short: "Manage application services",
		Long: `List, enable, or disable application services defined in the project config.

Mandatory services are always active and cannot be toggled.
Optional services can be enabled or disabled; the change is written to devbox/local.yml.`,
		Example: `  devbox services list
  devbox services enable second
  devbox services disable second`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newServiceListCmd(flags))
	cmd.AddCommand(newServiceEnableCmd(flags))
	cmd.AddCommand(newServiceDisableCmd(flags))
	return cmd
}

func newServiceListCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List all services and their status",
		Long:    `Show all services defined in the project with their container names, enabled state, and running status.`,
		Example: "  devbox services list",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load and apply styles (graceful — missing styles.yml uses defaults).
			stylesPath := filepath.Join(filepath.Dir(flags.configPath), "devbox", "styles.yml")
			stylesCfg, _ := config.LoadStylesConfig(stylesPath)
			ui.ApplyStyles(stylesCfg)

			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			return runServiceList(render.Stdout(), cfg, containerRunning)
		},
		SilenceUsage: true,
	}
}

// containerCheckFn checks whether a container with the given name is running.
type containerCheckFn func(projectFullName, containerName string) bool

// containerRunning checks if a Docker container is running by full container name.
func containerRunning(projectFullName, containerName string) bool {
	fullName := projectFullName + "-" + containerName
	out, err := exec.Command(
		"docker", "ps", "-q",
		"--filter", fmt.Sprintf("name=^%s$", fullName),
		"--filter", "status=running",
	).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
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

func newServiceEnableCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "enable <service>",
		Short: "Enable an optional service (writes to devbox/local.yml)",
		Long: `Enable an optional service by writing services.<name>.enabled = true to devbox/local.yml.

The .env file is regenerated automatically after the change.
Use 'devbox up' to start the newly enabled service.`,
		Example:           "  devbox services enable second",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: optionalServiceNameCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			return setServiceEnabled(flags.configPath, cfg, args[0], true)
		},
		SilenceUsage: true,
	}
}

func newServiceDisableCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "disable <service>",
		Short: "Disable an optional service (writes to devbox/local.yml)",
		Long: `Disable an optional service by writing services.<name>.enabled = false to devbox/local.yml.

The .env file is regenerated automatically after the change.
Use 'devbox stop <container>' or 'devbox down' to stop the service.`,
		Example:           "  devbox services disable second",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: optionalServiceNameCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			return setServiceEnabled(flags.configPath, cfg, args[0], false)
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
		cfg, err := config.LoadConfig(flags.configPath)
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

// setServiceEnabled writes services.<name>.enabled = value to devbox/local.yml.
func setServiceEnabled(configPath string, cfg *config.DevboxConfig, name string, enabled bool) error {
	svc, ok := cfg.Services[name]
	if !ok {
		return fmt.Errorf("service %q not found", name)
	}
	if svc.Mandatory {
		return fmt.Errorf("service %q is mandatory and cannot be %s", name, disableWord(enabled))
	}

	baseDir := filepath.Dir(configPath)
	localPath := filepath.Join(baseDir, "devbox", "local.yml")

	// Load existing local.yml or start with empty map.
	local := make(map[string]any)
	if data, err := os.ReadFile(localPath); err == nil {
		if err := yaml.Unmarshal(data, &local); err != nil {
			return fmt.Errorf("parse %s: %w", localPath, err)
		}
		if local == nil {
			local = make(map[string]any)
		}
	}

	// Set services.<name>.enabled.
	svcMap, ok := local["services"].(map[string]any)
	if !ok {
		svcMap = make(map[string]any)
		local["services"] = svcMap
	}
	entry, ok := svcMap[name].(map[string]any)
	if !ok {
		entry = make(map[string]any)
		svcMap[name] = entry
	}
	entry["enabled"] = enabled

	data, err := yaml.Marshal(local)
	if err != nil {
		return fmt.Errorf("marshal local config: %w", err)
	}
	if err := os.WriteFile(localPath, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", localPath, err)
	}

	w := render.Stdout()
	if enabled {
		w.Success(fmt.Sprintf("service %q enabled (written to %s)", name, localPath))
	} else {
		w.Success(fmt.Sprintf("service %q disabled (written to %s)", name, localPath))
	}
	return regenEnv(configPath, baseDir)
}

func disableWord(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}
