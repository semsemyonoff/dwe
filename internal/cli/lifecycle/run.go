package lifecycle

import (
	"github.com/semsemyonoff/devbox/internal/cli/cmdctx"
	"github.com/semsemyonoff/devbox/internal/cli/info"
	lifecyclepkg "github.com/semsemyonoff/devbox/internal/core/workflow/lifecycle"

	"github.com/spf13/cobra"
)

// NewRunCmd builds the `devbox run` cobra command.
func NewRunCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	var noUpdate bool
	var updateMode string
	var yes bool
	var skipPreflight bool
	var silent bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start the project (full lifecycle: update probe → up → wait → info)",
		Long: `Run the full project lifecycle driven by devbox/lifecycle.yml.

Execution order: optional git update probe → before-run hooks → docker up → docker wait
→ after-run hooks → optional info display → final ready message.

Use 'devbox docker up' for a bare Docker Compose start without hooks or the update probe.`,
		Example: `  devbox run
  devbox run --no-update
  devbox run --update on`,
		Args:         cobra.NoArgs,
		GroupID:      groupID,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return lifecyclepkg.RunRun(lifecyclepkg.RunContext{
				Ctx:        cmd.Context(),
				ConfigPath: flags.ConfigPath,
				NoUpdate:   noUpdate,
				UpdateMode: updateMode,
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
				SkipNotify:    silent,
				ErrOut:        cmd.ErrOrStderr(),
				Translator:    flags.I18n,
				Locale:        flags.Locale,
				OnDefaultUsed: func(p lifecyclepkg.DefaultedPipeline) {
					cmdctx.EmitDefaultNotice(cmd, flags, string(p), "lifecycle")
				},
			})
		},
	}

	cmd.Flags().BoolVar(&noUpdate, "no-update", false, "disable git update probe regardless of lifecycle.yml config")
	cmd.Flags().StringVar(&updateMode, "update", "", "override update probe mode (on|off)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompts inside hook steps")
	cmdctx.AddSkipPreflight(cmd, &skipPreflight)
	cmdctx.AddSilent(cmd, &silent)
	return cmd
}
