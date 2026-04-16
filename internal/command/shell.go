package command

import (
	"fmt"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"

	"github.com/spf13/cobra"
)

func newShellCmd(flags *rootFlags) *cobra.Command {
	var asRoot bool

	cmd := &cobra.Command{
		Use:   "shell [service]",
		Short: "Open a shell in a service container",
		Long: `Open an interactive shell in the specified service container.

If the container is running, connects via 'docker exec'.
If the container does not exist, starts a new one via 'docker compose run --rm'.
If the container is stopped (exited), an error is returned.

Shell, user, and working directory defaults are read from the cli: section
in devbox/services.yml and can be overridden with flags.

When only one service is defined, the service argument may be omitted.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			return runServicesCLI(cfg, compose, serviceName, asRoot)
		},
	}

	cmd.Flags().BoolVar(&asRoot, "root", false, "run as root user")
	return cmd
}
