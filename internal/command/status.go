package command

import (
	"fmt"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/render"
	"devbox-cli/internal/stack"
	"devbox-cli/internal/ui"

	"github.com/spf13/cobra"
)

func newStatusCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show stack health and services/tools status",
		Long: `Display the running status of the entire devbox stack.

Shows a health indicator (running/partial/stopped), a services table,
a tools table with live container status, and a compose topology tree.`,
		Example:      "  devbox status",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyStyles(flags.ProjectRoot(), cmd.ErrOrStderr())
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			projectName, dockerCfg, err := stack.ResolveProjectAndDocker(flags.configPath, cfg)
			if err != nil {
				return err
			}
			composeFiles := cfg.ComposeFiles()

			var processEnv []string
			if dockerCfg != nil {
				processEnv = docker.MergeEnv(dockerCfg.ProcessEnv)
			}

			dockerBin := config.DockerBin(cfg)
			topo := stack.FetchComposeTopology(composeFiles, projectName, processEnv, dockerBin)
			var topoStatus map[string]ui.NodeStatus
			if topo == nil {
				topo = stack.ParseTopologyFromFiles(composeFiles)
			} else {
				topoStatus = stack.ComposeNodeStatuses(composeFiles, projectName, processEnv, dockerBin)
				if topoStatus != nil {
					for name := range topo {
						if _, ok := topoStatus[name]; !ok {
							topoStatus[name] = ui.NodeStopped
						}
					}
				}
			}

			topo, topoStatus = stack.AugmentWithDisabled(cfg, topo, topoStatus)

			if dockerCfg != nil && len(dockerCfg.Topology.Hidden) > 0 {
				topo, topoStatus = stack.RemoveHiddenNodes(topo, topoStatus, dockerCfg.Topology.Hidden)
			}

			isRunning := func(_, container string) bool {
				return containerRunning(projectName, container, dockerBin)
			}
			return stack.RunStatus(render.Stdout(), cfg, isRunning, topo, topoStatus)
		},
	}
}
