package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/version"

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
	missingInfo := false
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("loading devbox/info.yml: %w", err)
		}
		missingInfo = true
	}

	stylesCfg := flags.stylesCfg

	// Always render the branded identity line; the ASCII art block inside the
	// helper is gated by header.lines.
	header := ui.BrandHeader{
		Project: cfg.Project.FullName(),
		Version: version.Version,
	}
	if stylesCfg != nil {
		header.Tagline = stylesCfg.Header.Tagline
		header.Lines = stylesCfg.Header.Lines
		header.Font = stylesCfg.Header.Font
	}
	_, _ = fmt.Fprint(cmd.OutOrStdout(), ui.RenderBrandHeader(header))
	_, _ = fmt.Fprintln(cmd.OutOrStdout())

	if missingInfo {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), ui.RenderSummary(cfg, nil))
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
		render.NewWriter(cmd.ErrOrStderr()).Warning("devbox/info.yml not found — showing minimal summary. Run `devbox validate config info` for details.")
		return nil
	}

	out, err := ui.RenderInfo(cfg, infoCfg)
	if err != nil {
		return fmt.Errorf("rendering info: %w", err)
	}

	_, _ = fmt.Fprint(cmd.OutOrStdout(), out)
	return nil
}
