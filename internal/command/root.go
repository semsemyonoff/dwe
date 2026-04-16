// Package command wires up the devbox-cli cobra command tree.
package command

import (
	"errors"
	"fmt"
	"os"
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
Run 'devbox' with no arguments to display a compact project summary and available commands.
Run 'devbox info' for the full info dashboard.`,
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
	addCmd(root, groupAdvanced, newDocsCmd(flags))

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
	// Load and apply styles (graceful — missing styles.yml uses defaults).
	stylesPath := filepath.Join(filepath.Dir(flags.configPath), "devbox", "styles.yml")
	stylesCfg, _ := config.LoadStylesConfig(stylesPath)
	ui.ApplyStyles(stylesCfg)

	cfg, err := config.LoadConfig(flags.configPath)
	switch {
	case err == nil:
		// Render ASCII art header from styles config if lines are set.
		if stylesCfg != nil && len(stylesCfg.Header.Lines) > 0 {
			r := render.NewWriter(cmd.OutOrStdout())
			if asciiErr := r.ASCII(stylesCfg.Header.Lines, stylesCfg.Header.Font, stylesCfg.Header.Color); asciiErr == nil {
				_, _ = fmt.Fprintln(cmd.OutOrStdout())
			}
		}
		// Print compact project summary followed by a blank separator line.
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), ui.RenderSummary(cfg))
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
	case errors.Is(err, os.ErrNotExist):
		// Config file not found — not an error, just skip the summary.
	default:
		// Config exists but could not be parsed — surface the error.
		return err
	}

	// Always show help regardless of whether config loaded.
	return cmd.Help()
}
