package command

import (
	"fmt"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/render"
	"devbox-cli/internal/stack"
	"devbox-cli/internal/usercommands"

	"github.com/spf13/cobra"
)

func newStatusCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status [service]",
		Short: "Show stack health and services/tools status",
		Long: `Display the running status of the entire devbox stack.

Shows a health indicator (running/partial/stopped), a services table,
a tools table with live container status, a compose topology tree,
and deploy status per service.

If a service name is provided, shows per-phase/step deploy breakdown for that service.`,
		Example:      "  devbox status\n  devbox status main",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyStyles(flags.ProjectRoot(), cmd.ErrOrStderr())

			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			statePath := filepath.Join(flags.ProjectRoot(), journal.DefaultRelPath)
			state, err := journal.Load(statePath)
			if err != nil {
				return fmt.Errorf("loading deploy state: %w", err)
			}

			reg, err := usercommands.LoadRegistryFromConfigPath(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading command registry: %w", err)
			}

			tracked, svcDeploys, err := deploy.LoadTrackedServices(cfg, reg, flags.ProjectRoot())
			if err != nil {
				return fmt.Errorf("loading tracked services: %w", err)
			}

			if len(args) > 0 {
				return stack.RenderServiceDeployDetail(cmd.OutOrStdout(), state, tracked, args[0])
			}

			projectName, dockerCfg, err := stack.ResolveProjectAndDocker(flags.configPath, cfg)
			if err != nil {
				return err
			}

			topo, topoStatus := stack.ResolveTopology(cfg, dockerCfg, projectName)
			dockerBin := config.DockerBin(cfg)
			isRunning := func(_, container string) bool {
				return stack.ContainerRunning(projectName, container, dockerBin)
			}

			return stack.RunStatus(render.NewWriter(cmd.OutOrStdout()), stack.StatusInput{
				Cfg:        cfg,
				IsRunning:  isRunning,
				Topo:       topo,
				TopoStatus: topoStatus,
				State:      state,
				SvcDeploys: svcDeploys,
				Tracked:    tracked,
			})
		},
	}
}
