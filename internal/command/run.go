package command

import (
	"devbox-cli/internal/lifecycle"

	"github.com/spf13/cobra"
)

func newRunCmd(flags *rootFlags) *cobra.Command {
	var noUpdate bool
	var updateMode string
	var yes bool
	var skipPreflight bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start the project (full lifecycle: update probe → up → wait → info)",
		Long: `Run the full project lifecycle driven by devbox/lifecycle.yml.

Execution order: optional git update probe → before-run hooks → docker up → docker wait
→ after-run hooks → optional info display → final ready message.

Use 'devbox docker up' for a bare Docker Compose start without hooks or the update probe.`,
		Example: `  devbox run
  devbox run --no-update
  devbox run --update auto`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return lifecycle.RunRun(lifecycle.RunContext{
				Ctx:           cmd.Context(),
				ConfigPath:    flags.configPath,
				NoUpdate:      noUpdate,
				UpdateMode:    updateMode,
				Yes:           yes,
				ShowInfo:      func() error { return runInfo(cmd, flags) },
				SkipPreflight: skipPreflight,
				ErrOut:        cmd.ErrOrStderr(),
			})
		},
	}

	cmd.Flags().BoolVar(&noUpdate, "no-update", false, "disable git update probe regardless of lifecycle.yml config")
	cmd.Flags().StringVar(&updateMode, "update", "", "override update probe mode (prompt|auto|check|off)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompts inside hook steps")
	addSkipPreflightFlag(cmd, &skipPreflight)
	return cmd
}
