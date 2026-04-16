package command

import (
	"fmt"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"

	"github.com/spf13/cobra"
)

// validModes contains the allowed values for the --mode flag.
var validModes = map[string]bool{"auto": true, "exec": true, "run": true}

func newShellCmd(flags *rootFlags) *cobra.Command {
	var asRoot bool
	var flagMode string
	var flagShell string
	var flagUser string
	var flagWorkDir string

	cmd := &cobra.Command{
		Use:   "shell [service]",
		Short: "Open a shell in a service container",
		Long: `Open an interactive shell in the specified service container.

Mode controls how the shell is opened (--mode auto|exec|run):
  auto  — connect via 'docker exec' if running, 'compose run' if absent, error if stopped (default)
  exec  — always use 'docker exec'; error if container is not running
  run   — always start a new container via 'docker compose run --rm'

Shell, user, working directory, and env defaults are read from the service
cli config block in devbox/defaults.yml and can be overridden with flags.

When only one service is defined, the service argument may be omitted.`,
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

			serviceName := ""
			if len(args) > 0 {
				serviceName = args[0]
			} else {
				names := sortedKeys(cfg.Services)
				if len(names) == 0 {
					return fmt.Errorf("no services defined in config")
				}
				if len(names) > 1 {
					return fmt.Errorf("multiple services defined — specify a service name: %v", names)
				}
				serviceName = names[0]
			}

			shellFlags := shellCLIFlags{
				asRoot:  asRoot,
				mode:    flagMode,
				shell:   flagShell,
				user:    flagUser,
				workDir: flagWorkDir,
			}
			return runServicesCLI(cfg, compose, serviceName, shellFlags)
		},
	}

	cmd.Flags().BoolVar(&asRoot, "root", false, "run as root user")
	cmd.Flags().StringVar(&flagMode, "mode", "", "shell mode: auto, exec, or run")
	cmd.Flags().StringVar(&flagShell, "shell", "", "shell binary to use (e.g. bash, sh, zsh)")
	cmd.Flags().StringVar(&flagUser, "user", "", "user to run as inside the container")
	cmd.Flags().StringVar(&flagWorkDir, "workdir", "", "working directory inside the container")
	return cmd
}

// shellCLIFlags holds the flag values passed to the shell command.
type shellCLIFlags struct {
	asRoot  bool
	mode    string
	shell   string
	user    string
	workDir string
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
