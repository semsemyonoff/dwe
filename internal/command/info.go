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
		Short: "Display project info, URLs, credentials and available commands",
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

	infoPath := filepath.Join(filepath.Dir(flags.configPath), "devbox", "info.yml")
	infoCfg, err := config.LoadInfoConfig(infoPath)
	if err != nil {
		return fmt.Errorf("loading devbox/info.yml: %w", err)
	}

	w := render.NewWriter(cmd.OutOrStdout())

	// ASCII art header
	if len(infoCfg.Header.ASCII.Lines) > 0 {
		if err := w.ASCII(infoCfg.Header.ASCII.Lines, infoCfg.Header.ASCII.Font, infoCfg.Header.ASCII.Color); err != nil {
			return fmt.Errorf("header ASCII: %w", err)
		}
	}

	out, err := ui.RenderInfo(cfg, infoCfg)
	if err != nil {
		return fmt.Errorf("rendering info: %w", err)
	}

	_, _ = fmt.Fprint(cmd.OutOrStdout(), out)
	return nil
}
