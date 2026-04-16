package command

import (
	"errors"
	"fmt"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/ui"

	"github.com/spf13/cobra"
)

// validModes contains the allowed values for the --mode flag.
var validModes = map[string]bool{"auto": true, "exec": true, "run": true}

// selectServiceFn is the function signature for interactive service selection.
// It receives the sorted list of enabled service names and returns the chosen name.
type selectServiceFn func(cfg *config.DevboxConfig, names []string) (string, error)

// defaultSelectService shows an interactive selector via ui.RunSelector.
func defaultSelectService(cfg *config.DevboxConfig, names []string) (string, error) {
	items := make([]ui.SelectorItem, len(names))
	for i, name := range names {
		svc := cfg.Services[name]
		items[i] = ui.SelectorItem{
			Label:       name,
			Description: svc.Container,
		}
	}
	idx, err := ui.RunSelector("Select a service:", items)
	if err != nil {
		return "", err
	}
	return names[idx], nil
}

// pickService resolves which service name to use for the shell command.
//   - If serviceName is non-empty, it is returned directly.
//   - If exactly one enabled service exists, it is auto-selected.
//   - If multiple enabled services exist, the selector function is called.
//   - If no enabled services exist, an error is returned.
//
// "Enabled" means mandatory or explicitly enabled in the current config.
func pickService(cfg *config.DevboxConfig, serviceName string, selector selectServiceFn) (string, error) {
	if serviceName != "" {
		return serviceName, nil
	}

	// Collect enabled services in sorted order.
	var enabled []string
	for _, name := range sortedKeys(cfg.Services) {
		svc := cfg.Services[name]
		if svc.Mandatory || svc.Enabled {
			enabled = append(enabled, name)
		}
	}

	switch len(enabled) {
	case 0:
		return "", fmt.Errorf("no enabled services — enable a service with 'devbox services enable <name>'")
	case 1:
		return enabled[0], nil
	default:
		return selector(cfg, enabled)
	}
}

func newShellCmd(flags *rootFlags) *cobra.Command {
	var asRoot bool
	var flagMode string
	var flagShell string
	var flagUser string
	var flagWorkDir string
	var flagEnvVars []string

	cmd := &cobra.Command{
		Use:   "shell [service]",
		Short: "Open a shell in a service container",
		Long: `Open an interactive shell in the specified service container.

Mode controls how the shell is opened (--mode auto|exec|run):
  auto  — connect via 'docker exec' if running, 'compose run' if absent, error if stopped (default)
  exec  — always use 'docker exec'; error if container is not running
  run   — always start a new container via 'docker compose run --rm'

Shell, user, working directory, and env defaults are read from the service
cli config block in devbox/services.yml and can be overridden with flags.

When no service argument is given, the command auto-selects if only one enabled
service exists, or shows an interactive selector when multiple services are enabled.`,
		Example: `  devbox shell
  devbox shell main
  devbox shell main --root
  devbox shell main --mode run --shell sh
  devbox shell main --user deploy --workdir /app`,
		Args:              cobra.MaximumNArgs(1),
		SilenceUsage:      true,
		ValidArgsFunction: serviceNameCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate mutual exclusion: --root and --user cannot both be set.
			if asRoot && flagUser != "" {
				return fmt.Errorf("--root and --user are mutually exclusive")
			}
			// Validate --mode value.
			if flagMode != "" && !validModes[flagMode] {
				return fmt.Errorf("--mode must be one of: auto, exec, run (got %q)", flagMode)
			}

			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			baseDir := filepath.Dir(flags.configPath)
			dockerCfg, err := config.LoadDockerConfig(baseDir, cfg)
			if err != nil {
				return fmt.Errorf("loading docker config: %w", err)
			}
			compose := docker.NewCompose(cfg, dockerCfg)

			argName := ""
			if len(args) > 0 {
				argName = args[0]
			}
			serviceName, err := pickService(cfg, argName, defaultSelectService)
			if err != nil {
				if errors.Is(err, ui.ErrCancelled) {
					return nil
				}
				return err
			}

			shellFlags := shellCLIFlags{
				asRoot:  asRoot,
				mode:    flagMode,
				shell:   flagShell,
				user:    flagUser,
				workDir: flagWorkDir,
				envVars: flagEnvVars,
			}
			processEnv := compose.BuildEnv()
			stateFn := func(name string) (string, error) {
				return containerStateStatus(name, processEnv)
			}
			execFn := func(containerName, shell, u, workDir string, env map[string]string) error {
				return dockerExecCLI(containerName, shell, u, workDir, env, processEnv)
			}
			return runServicesCLI(cfg, compose, serviceName, shellFlags, stateFn, execFn, composeRunCLI)
		},
	}

	cmd.Flags().BoolVar(&asRoot, "root", false, "run as root user")
	cmd.Flags().StringVar(&flagMode, "mode", "", "shell mode: auto, exec, or run")
	cmd.Flags().StringVar(&flagShell, "shell", "", "shell binary to use (e.g. bash, sh, zsh)")
	cmd.Flags().StringVar(&flagUser, "user", "", "user to run as inside the container")
	cmd.Flags().StringVar(&flagWorkDir, "workdir", "", "working directory inside the container")
	cmd.Flags().StringArrayVar(&flagEnvVars, "env", nil, "set an environment variable (KEY=VALUE); overrides service cli.env config")
	return cmd
}

// shellCLIFlags holds the flag values passed to the shell command.
type shellCLIFlags struct {
	asRoot  bool
	mode    string
	shell   string
	user    string
	workDir string
	envVars []string // KEY=VALUE pairs from --env flags; override service cli.env config
}

// serviceNameCompletion returns a ValidArgsFunction that completes service names
// from the loaded devbox config.
func serviceNameCompletion(flags *rootFlags) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cfg, err := config.LoadConfig(flags.configPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		names := sortedKeys(cfg.Services)
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}
