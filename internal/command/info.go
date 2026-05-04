package command

import (
	"fmt"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"

	"github.com/spf13/cobra"
)

func newInfoCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Display project info dashboard (URLs, hosts, services, tools)",
		Long: `Display a styled project dashboard loaded from devbox/info.yml.

Shows project name, URLs, hosts, services, tools, and runtime details.
The dashboard is driven by Go templates evaluated against the merged devbox config.`,
		Example: "  devbox info",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInfo(cmd, flags)
		},
		SilenceUsage: true,
	}
}

func runInfo(cmd *cobra.Command, flags *rootFlags) error {
	cfg, err := config.LoadConfig(flags.configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	infoPath := filepath.Join(flags.ProjectRoot(), "devbox", "info.yml")
	infoCfg, err := config.LoadInfoConfig(infoPath)
	if err != nil {
		return fmt.Errorf("loading devbox/info.yml: %w", err)
	}

	stylesCfg := applyStyles(flags.ProjectRoot(), cmd.ErrOrStderr())

	// Render ASCII art header from styles config if lines are set.
	if stylesCfg != nil && len(stylesCfg.Header.Lines) > 0 {
		r := render.NewWriter(cmd.OutOrStdout())
		if asciiErr := r.ASCII(stylesCfg.Header.Lines, stylesCfg.Header.Font, stylesCfg.Header.Color); asciiErr == nil {
			_, _ = fmt.Fprintln(cmd.OutOrStdout())
		}
	}

	out, err := ui.RenderInfo(cfg, infoCfg)
	if err != nil {
		return fmt.Errorf("rendering info: %w", err)
	}

	_, _ = fmt.Fprint(cmd.OutOrStdout(), out)
	return nil
}
