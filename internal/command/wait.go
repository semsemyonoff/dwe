package command

import (
	"fmt"
	"time"

	"devbox-cli/internal/docker"
	"devbox-cli/internal/render"

	"github.com/spf13/cobra"
)

func newWaitCmd(flags *rootFlags) *cobra.Command {
	var timeout time.Duration
	var interval time.Duration

	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Wait for all compose containers to become healthy",
		Long: `Poll all running compose containers until they report a healthy status or the timeout elapses.

Containers without a healthcheck are considered healthy immediately.
Use '--timeout' and '--interval' to control polling behavior.`,
		Example: `  devbox wait
  devbox wait --timeout 120s --interval 5s`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newDockerPipeline(flags, "wait")
			if err != nil {
				return err
			}

			ids, err := p.compose.ContainerIDs()
			if err != nil {
				return fmt.Errorf("getting container IDs: %w", err)
			}
			if len(ids) == 0 {
				render.Stdout().Warning("no containers found")
				return nil
			}

			if interval <= 0 {
				return fmt.Errorf("--interval must be greater than zero")
			}
			attempts := max(int(timeout/interval), 1)

			return docker.WaitContainersHealthy(ids, p.compose.HealthStatus, attempts, interval, render.Stdout())
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "total wait timeout")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "poll interval")
	return cmd
}
