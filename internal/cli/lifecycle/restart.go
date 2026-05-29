package lifecycle

import (
	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/cli/info"
	lifecyclepkg "devbox-cli/internal/core/workflow/lifecycle"

	"github.com/spf13/cobra"
)

// NewRestartCmd builds the `devbox restart` cobra command.
func NewRestartCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	var yes bool
	var skipPreflight bool

	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the project (stop, then run --no-update)",
		Long: `Restart the project by running the full stop lifecycle then the full run lifecycle.

The run leg always skips the git update probe (equivalent to 'devbox run --no-update').

Use 'devbox docker restart' for the low-level compose restart passthrough.`,
		Example:      `  devbox restart`,
		Args:         cobra.NoArgs,
		GroupID:      groupID,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return lifecyclepkg.RunRestart(lifecyclepkg.RunContext{
				Ctx:        cmd.Context(),
				ConfigPath: flags.ConfigPath,
				Yes:        yes,
				// lifecycle commands are not migrated to JSON output; suppress
				// the chained info display in JSON mode to avoid a mixed
				// text+JSON stream on stdout.
				ShowInfo: func() error {
					if flags.Output == "json" {
						return nil
					}
					return info.Run(cmd, flags)
				},
				SkipPreflight: skipPreflight,
				ErrOut:        cmd.ErrOrStderr(),
				Translator:    flags.I18n,
				Locale:        flags.Locale,
				OnDefaultUsed: func(p lifecyclepkg.DefaultedPipeline) {
					cmdctx.EmitDefaultNotice(cmd, flags, string(p), "lifecycle")
				},
			})
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompts inside hook steps")
	cmdctx.AddSkipPreflight(cmd, &skipPreflight)
	return cmd
}
