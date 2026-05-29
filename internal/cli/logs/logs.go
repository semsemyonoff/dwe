// Package logs provides the `devbox logs <service>` command.
package logs

import (
	"devbox-cli/internal/cli/cmdctx"

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

func runLogs(cmd *cobra.Command, flags *cmdctx.RootFlags, args []string, opts logsOptions) error {
	_ = opts
	_ = args
	_ = cmd
	_ = flags
	// Implemented in subsequent tasks.
	return nil
}
