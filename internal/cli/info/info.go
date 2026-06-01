// Package info provides the `devbox info` command.
package info

import (
	"fmt"
	"path/filepath"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/shared/version"

	"github.com/spf13/cobra"
)

// infoJSON is the JSON output shape for `devbox info --output json`.
type infoJSON struct {
	Title    string        `json:"title"`
	Sections []infoSection `json:"sections"`
}

// infoSection is a section in the info JSON output.
type infoSection struct {
	ID    string     `json:"id,omitempty"`
	Title string     `json:"title,omitempty"`
	Items []infoItem `json:"items"`
}

// infoItem is a single structured element within a section.
// Type values: "definition", "info", "warning", "url", "host", "subgroup".
type infoItem struct {
	Type  string `json:"type"`
	Label string `json:"label,omitempty"`
	Value string `json:"value,omitempty"`
}

// NewCmd builds the `devbox info` command.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Display project info dashboard (URLs, hosts, services, tools)",
		Long: `Display a styled project dashboard loaded from devbox/info.yml.

Shows project name, URLs, hosts, services, tools, and runtime details.
The dashboard is driven by Go templates evaluated against the merged devbox config.`,
		Example:      "  devbox info",
		Args:         cobra.NoArgs,
		GroupID:      groupID,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return Run(cmd, flags)
		},
	}
}

// Run executes the info dashboard render, writing to cmd.OutOrStdout.
// Exported so lifecycle commands can chain the info display after a successful
// `devbox run` / `devbox restart`.
func Run(cmd *cobra.Command, flags *cmdctx.RootFlags) error {
	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return cmdctx.ErrWrap("project_invalid_config", err)
	}

	infoPath := filepath.Join(flags.ProjectRoot(), "devbox", "info.yml")
	infoCfg, err := config.LoadInfoConfig(infoPath)
	if err != nil {
		return cmdctx.ErrWrap("project_invalid_config", err)
	}

	if flags.Output == "json" {
		data, err := buildInfoData(cfg, infoCfg)
		if err != nil {
			return cmdctx.ErrWrap("info_render_failed", err)
		}
		return cmdctx.WriteData(flags, cmd, data, func(infoJSON) string { return "" })
	}

	stylesCfg := flags.StylesCfg

	// Always render the branded identity line; the ASCII art block inside the
	// helper is gated by header.lines.
	header := render.Brand{
		Project: cfg.Project.FullName(),
		Version: version.Version,
	}
	if stylesCfg != nil {
		header.Tagline = stylesCfg.Header.Tagline
		header.Lines = stylesCfg.Header.Lines
		header.Font = stylesCfg.Header.Font
	}
	_, _ = fmt.Fprint(cmd.OutOrStdout(), render.BrandHeader(header))
	_, _ = fmt.Fprintln(cmd.OutOrStdout())

	out, err := render.Info(cfg, infoCfg)
	if err != nil {
		return fmt.Errorf("rendering info: %w", err)
	}

	_, _ = fmt.Fprint(cmd.OutOrStdout(), out)
	return nil
}
