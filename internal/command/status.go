package command

import (
	"fmt"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"

	"github.com/spf13/cobra"
)

func newStatusCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show services and tools topology",
		Long: `Display a tree view of all services and optional tools defined in the project.

Shows which services and tools are enabled or disabled, along with
their compose file overlays and container assignments.`,
		Example:      "  devbox status",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			return runServices(render.Stdout(), cfg)
		},
	}
}
