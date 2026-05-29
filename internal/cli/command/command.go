package command

import (
	"errors"
	"fmt"
	"os/signal"
	"syscall"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/ui"
	"devbox-cli/internal/core/ui/ask"
	"devbox-cli/internal/core/ui/cmdbrowser"
	"devbox-cli/internal/core/usercommands"
	"devbox-cli/internal/shared/i18n"

	"github.com/spf13/cobra"
)

// Test seams — overridden in command_test.go. Subtests that override these
// MUST NOT call t.Parallel() (global state across goroutines).
var (
	runAsk         = ask.Run
	confirmRun     = ui.ConfirmRun
	runUserCommand = usercommands.RunCommand
	notifyContext  = signal.NotifyContext
)

// runOpts carries the per-invocation options for runCommandByID.
type runOpts struct {
	Inspect        bool
	Yes            bool // user-explicit --yes OR'd with TUI y-toggle at the call site
	ForceParamForm bool // TUI-only: user picked the item via the edit-params key
	SetValues      []string
	Silent         bool            // suppress end-of-command desktop notification
	Translator     i18n.Translator // for localized string lookups; nil-safe via TranslatorOrNop
	Locale         string          // active locale code (e.g. "ru", "en")
}

// NewCmd builds the `devbox commands` command tree.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	var (
		setFlags    []string
		skipConfirm bool
		inspectFlag bool
		silent      bool
	)

	cmd := &cobra.Command{
		Use:     "commands [id]",
		Aliases: []string{"cmd"},
		GroupID: groupID,
		Short:   "Run, inspect, and list devbox commands",
		Long: `Manage declarative commands defined in devbox/commands/.

Commands are YAML-defined operations organized into groups (e.g. db, app, services.main).
They can be shell commands, scripts, service exec/run operations, or multi-step workflows.

Without an id, an interactive selector lists public commands. With a group prefix
(e.g. services.main), the selector is filtered. With a full command id, it runs
(or inspects with -i) directly without a selector.`,
		Example: `  devbox commands
  devbox commands list
  devbox commands db.up
  devbox commands db.up --set env=local
  devbox commands -i db.up
  devbox cmd db.up --yes`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			// Cobra parses flags before invoking ValidArgsFunction, so --inspect
			// is available even though PersistentPreRunE is bypassed in the
			// __complete path.
			inspect, _ := cmd.Flags().GetBool("inspect")
			return registryIDCompletion(flags, inspect)(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
			if err != nil {
				return err
			}

			// Inspect route: exact id required; private allowed; cfg load errors tolerated.
			if inspectFlag {
				if len(args) == 0 {
					return errors.New("id required with --inspect")
				}
				if flags.Output == "json" {
					def, err := reg.Get(args[0])
					if err != nil {
						return err
					}
					translator := i18n.TranslatorOrNop(flags.I18n)
					data := buildCommandInspectJSON(def, translator, flags.Locale)
					return cmdctx.WriteData(flags, cmd, data, func(commandInspectJSON) string { return "" })
				}
				cfg, _ := config.LoadConfig(flags.ConfigPath)
				return runCommandByID(
					cmd.Context(),
					cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
					cfg, reg, flags.ProjectRoot(), args[0],
					runOpts{
						Inspect:    true,
						Translator: i18n.TranslatorOrNop(flags.I18n),
						Locale:     flags.Locale,
					},
				)
			}

			// Run route: existing selector behavior.
			cfg, err := config.LoadConfig(flags.ConfigPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			ctx, stop := notifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			var (
				skipConfirmFromTUI bool
				forceFormFromTUI   bool
			)
			selector := makeBrowserSelector(cfg, reg, cmdbrowser.ModeRun, false, &skipConfirmFromTUI, &forceFormFromTUI, i18n.TranslatorOrNop(flags.I18n), flags.Locale)
			if !ui.IsInteractiveFn(cmd.InOrStdin()) {
				selector = func(_ []*usercommands.CommandDef, _ string) (string, error) {
					return "", fmt.Errorf("no exact command ID given; pass a full command ID or run in an interactive terminal")
				}
			}
			id, err := resolveCommandID(reg, args, false, cfg.Project.Name, selector)
			if err != nil {
				if errors.Is(err, ui.ErrCancelled) {
					return nil
				}
				return err
			}
			return runCommandByID(
				ctx,
				cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
				cfg, reg, flags.ProjectRoot(), id,
				runOpts{
					Yes:            skipConfirm || skipConfirmFromTUI,
					ForceParamForm: forceFormFromTUI,
					SetValues:      setFlags,
					Silent:         silent,
					Translator:     i18n.TranslatorOrNop(flags.I18n),
					Locale:         flags.Locale,
				},
			)
		},
	}
	cmd.Flags().StringArrayVar(&setFlags, "set", nil, "Set a param value (key=value)")
	_ = cmd.RegisterFlagCompletionFunc("set", daemonSetCompletion(flags))
	cmd.Flags().BoolVarP(&skipConfirm, "yes", "y", false, "Skip confirmation prompts; intended for non-interactive use such as scripts and nested command runs")
	cmd.Flags().BoolVarP(&inspectFlag, "inspect", "i", false, "Show the full definition of the given command id instead of running it")
	cmdctx.AddSilent(cmd, &silent)
	cmd.MarkFlagsMutuallyExclusive("inspect", "set")
	cmd.MarkFlagsMutuallyExclusive("inspect", "yes")
	cmd.MarkFlagsMutuallyExclusive("inspect", "silent")

	cmd.AddCommand(newCommandListCmd(flags))
	return cmd
}
