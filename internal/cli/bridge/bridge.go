// Package bridge provides the `dwe bridge` command subtree: the host bridge
// daemon lifecycle and diagnostics, plus the hidden `bridge daemon`
// foreground entry that the lifecycle ensure spawns.
package bridge

import (
	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

// NewCmd builds the `dwe bridge` command tree: the user-facing
// start/stop/status/logs subcommands plus the hidden `bridge daemon`
// foreground entry that the lifecycle ensure spawns. From inside containers
// only `bridge status` is reachable (design D9 — `bridge stop` would be
// suicide for the bridge itself).
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		GroupID: groupID,
		Use:     "bridge",
		Short:   "Manage the host bridge daemon for dev containers",
		Long: `Manage the host bridge daemon that lets dev containers run dwe commands.

The bridge mounts a small shim binary into bridge-enabled service containers
as ` + "`dwe`" + `; the shim forwards every invocation to a host-side daemon over the
project's ` + "`.dwe/bridge`" + ` transports, and the daemon forks the real dwe on the
host. Lifecycle commands manage the daemon automatically — this subtree is
for manual control and diagnostics.`,
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newStartCmd(flags))
	cmd.AddCommand(newStopCmd(flags))
	cmd.AddCommand(newStatusCmd(flags))
	cmd.AddCommand(newLogsCmd(flags))
	cmd.AddCommand(newDaemonCmd(flags))
	return cmd
}
