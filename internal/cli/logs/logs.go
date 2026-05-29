// Package logs provides the `devbox logs <service>` command.
package logs

import (
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/project/services"
	"devbox-cli/internal/shared/daemon"
	"devbox-cli/internal/shared/docker"

	"github.com/spf13/cobra"
)

type logsOptions struct {
	tail   int
	since  string
	follow bool
}

// NewCmd builds the `devbox logs <service>` command.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	var opts logsOptions

	cmd := &cobra.Command{
		Use:   "logs <service>",
		Short: "Stream container logs for a service",
		Long: `Stream Docker container logs for a named project service.

In text mode (default), output is passed through unchanged from docker logs.
With --output json, emits NDJSON: one {"ts","stream","msg"} object per line.`,
		Example:      "  devbox logs myapp\n  devbox logs myapp --tail 100 --follow\n  devbox logs myapp --output json --since 5m",
		SilenceUsage: true,
		GroupID:      groupID,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(cmd, flags, args, opts)
		},
	}

	cmd.Flags().IntVar(&opts.tail, "tail", 50, "number of trailing log lines to show (0 = all)")
	cmd.Flags().StringVar(&opts.since, "since", "", "show logs since duration (e.g. 5m, 1h) or RFC3339 timestamp")
	cmd.Flags().BoolVarP(&opts.follow, "follow", "f", false, "stream new log lines as they arrive")

	return cmd
}

// ResolveLogsTarget loads the project config, validates the service name, and
// returns the resolved Docker container name and loaded config.
func ResolveLogsTarget(flags *cmdctx.RootFlags, serviceName string) (containerName string, cfg *config.DevboxConfig, err error) {
	cfg, err = config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return "", nil, cmdctx.ErrWrap("project_invalid_config", err)
	}
	svc, ok := cfg.Services[serviceName]
	if !ok {
		known := services.SortedNames(cfg.Services)
		return "", cfg, cmdctx.Err("service_unknown", fmt.Sprintf("no service %q in project", serviceName)).
			WithHint("available: " + strings.Join(known, ", ")).
			WithDetail("requested", serviceName)
	}
	containerName, err = daemon.ResolveContainerName(cfg.Project.FullName(), svc.Container)
	if err != nil {
		return "", cfg, cmdctx.ErrWrap("container_name_invalid", err)
	}
	return containerName, cfg, nil
}

func buildDockerLogsArgs(containerName string, opts logsOptions) []string {
	args := []string{"logs"}
	if opts.tail == 0 {
		args = append(args, "--tail", "all")
	} else {
		args = append(args, "--tail", strconv.Itoa(opts.tail))
	}
	if opts.since != "" {
		args = append(args, "--since", opts.since)
	}
	if opts.follow {
		args = append(args, "--follow")
	}
	args = append(args, containerName)
	return args
}

func runLogs(cmd *cobra.Command, flags *cmdctx.RootFlags, args []string, opts logsOptions) error {
	containerName, cfg, err := ResolveLogsTarget(flags, args[0])
	if err != nil {
		return err
	}

	dockerArgs := buildDockerLogsArgs(containerName, opts)
	dockerCmd := exec.CommandContext(cmd.Context(), config.DockerBin(cfg), dockerArgs...) //nolint:gosec
	dockerCmd.Stdout = cmd.OutOrStdout()

	// Tee stderr: stream it to the caller's stderr AND capture it so we can
	// detect "No such container" after the process exits.
	var stderrBuf strings.Builder
	dockerCmd.Stderr = io.MultiWriter(cmd.ErrOrStderr(), &stderrBuf)

	if runErr := dockerCmd.Run(); runErr != nil {
		if docker.IsNoSuchContainerErr(stderrBuf.String()) {
			return cmdctx.Err("container_not_found",
				fmt.Sprintf("container for service %q not found — is the project deployed?", args[0])).
				WithDetail("container", containerName)
		}
		return fmt.Errorf("docker logs: %w", runErr)
	}
	return nil
}
