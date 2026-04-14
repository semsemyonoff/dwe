// Package command wires up the devbox-cli cobra command tree.
package command

import (
	"github.com/spf13/cobra"
)

// rootFlags holds flags shared across all commands.
type rootFlags struct {
	configPath string
}

// NewRootCmd builds and returns the root cobra.Command.
func NewRootCmd() *cobra.Command {
	flags := &rootFlags{}

	root := &cobra.Command{
		Use:   "devbox",
		Short: "devbox-cli — local development environment toolkit",
		Long: `devbox-cli is the core engine for the devbox local development environment.

It provides config validation, rendering, topology inspection, and project info display.
Run 'devbox info' to display the current project status.`,
		// Running 'devbox' with no subcommand shows project info.
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInfo(flags)
		},
		// Suppress cobra's default "Run 'devbox --help' for more information" footer.
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVarP(
		&flags.configPath,
		"config", "c",
		"devbox.yml",
		"path to devbox.yml",
	)

	root.AddCommand(newInfoCmd(flags))
	root.AddCommand(newPrintCmd())
	root.AddCommand(newRenderCmd(flags))
	root.AddCommand(newComposeCmd(flags))
	root.AddCommand(newServicesCmd(flags))
	root.AddCommand(newServiceCmd(flags))
	root.AddCommand(newToolCmd(flags))
	root.AddCommand(newDeployCmd(flags))
	root.AddCommand(newCommandCmd(flags))

	return root
}
