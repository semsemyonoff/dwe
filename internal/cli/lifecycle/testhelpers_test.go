package lifecycle

import (
	"devbox-cli/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

const (
	groupEnvironment = "environment"
	groupPipelines   = "pipelines"
)

// buildLifecycleTestRoot constructs a minimal cobra root that mirrors the
// composition root for lifecycle commands. It registers the environment +
// pipelines groups, attaches all four lifecycle subcommands, and exposes the
// persistent `--config` flag bound to flags.ConfigPath.
//
// Tests that previously called cli.NewRootCmd() use this helper to avoid the
// (now-cyclic) import of the cli root package.
func buildLifecycleTestRoot(flags *cmdctx.RootFlags) *cobra.Command {
	root := &cobra.Command{Use: "devbox"}
	root.PersistentFlags().StringVarP(&flags.ConfigPath, "config", "c", "", "path to devbox.yml")
	root.AddGroup(
		&cobra.Group{ID: groupEnvironment, Title: "Environment Commands:"},
		&cobra.Group{ID: groupPipelines, Title: "Pipeline Commands:"},
	)
	root.AddCommand(NewRunCmd(groupEnvironment, flags))
	root.AddCommand(NewStopCmd(groupEnvironment, flags))
	root.AddCommand(NewRestartCmd(groupEnvironment, flags))
	root.AddCommand(NewResetCmd(groupPipelines, flags))
	return root
}
