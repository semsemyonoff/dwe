// Package command wires up the devbox-cli cobra command tree.
package command

import (
	"fmt"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"

	"github.com/spf13/cobra"
)

// Command group IDs.
const (
	groupCore          = "core"
	groupEnvironment   = "environment"
	groupConfiguration = "configuration"
	groupPipelines     = "pipelines"
	groupAdvanced      = "advanced"
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
		// Running 'devbox' with no subcommand shows project summary + help.
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRoot(cmd, flags)
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

	// Define command groups for organized help output.
	root.AddGroup(
		&cobra.Group{ID: groupCore, Title: "Core Commands:"},
		&cobra.Group{ID: groupEnvironment, Title: "Environment Commands:"},
		&cobra.Group{ID: groupConfiguration, Title: "Configuration Commands:"},
		&cobra.Group{ID: groupPipelines, Title: "Pipeline Commands:"},
		&cobra.Group{ID: groupAdvanced, Title: "Advanced Commands:"},
	)

	// Core group: project info and version.
	addCmd(root, groupCore, newInfoCmd(flags))
	addCmd(root, groupCore, newVersionCmd())

	// Environment group: lifecycle and shell access.
	addCmd(root, groupEnvironment, newUpCmd(flags))
	addCmd(root, groupEnvironment, newDownCmd(flags))
	addCmd(root, groupEnvironment, newStopCmd(flags))
	addCmd(root, groupEnvironment, newRestartCmd(flags))
	addCmd(root, groupEnvironment, newLogsCmd(flags))
	addCmd(root, groupEnvironment, newPsCmd(flags))
	addCmd(root, groupEnvironment, newWaitCmd(flags))
	addCmd(root, groupEnvironment, newShellCmd(flags))
	addCmd(root, groupEnvironment, newStatusCmd(flags))

	// Configuration group: services, tools, rendering.
	addCmd(root, groupConfiguration, newServiceCmd(flags))
	addCmd(root, groupConfiguration, newToolCmd(flags))
	addCmd(root, groupConfiguration, newRenderCmd(flags))

	// Pipelines group: deploy and reset.
	addCmd(root, groupPipelines, newDeployCmd(flags))
	addCmd(root, groupPipelines, newResetCmd(flags))

	// Advanced group: low-level and diagnostic commands.
	addCmd(root, groupAdvanced, newCommandCmd(flags))
	addCmd(root, groupAdvanced, newDockerCmd(flags))
	addCmd(root, groupAdvanced, newComposeCmd(flags))

	// Add the built-in Cobra completion command to the Advanced group.
	root.InitDefaultCompletionCmd()
	if completionCmd, _, err := root.Find([]string{"completion"}); err == nil && completionCmd != nil {
		completionCmd.GroupID = groupAdvanced
	}

	// Internal: hidden Make-compatibility command.
	printCmd := newPrintCmd()
	printCmd.Hidden = true
	root.AddCommand(printCmd)

	return root
}

// addCmd assigns a group ID to cmd and adds it to parent.
func addCmd(parent *cobra.Command, groupID string, cmd *cobra.Command) {
	cmd.GroupID = groupID
	parent.AddCommand(cmd)
}

// runRoot is the handler for `devbox` with no subcommand.
// It prints an ASCII header and compact project summary (when a config is
// found), followed by the Cobra/Fang help output.
func runRoot(cmd *cobra.Command, flags *rootFlags) error {
	cfg, err := config.LoadConfig(flags.configPath)
	if err == nil {
		// Print ASCII art header from info.yml when available.
		infoPath := filepath.Join(filepath.Dir(flags.configPath), "devbox", "info.yml")
		if infoCfg, infoErr := config.LoadInfoConfig(infoPath); infoErr == nil {
			if len(infoCfg.Header.ASCII.Lines) > 0 {
				w := render.NewWriter(cmd.OutOrStdout())
				// Header is cosmetic — skip on render error.
				_ = w.ASCII(infoCfg.Header.ASCII.Lines, infoCfg.Header.ASCII.Font, infoCfg.Header.ASCII.Color)
			}
		}

		// Print compact project summary followed by a blank separator line.
		fmt.Fprintln(cmd.OutOrStdout(), ui.RenderSummary(cfg))
		fmt.Fprintln(cmd.OutOrStdout())
	}

	// Always show help regardless of whether config loaded.
	return cmd.Help()
}
