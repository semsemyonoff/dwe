package command

import (
	"errors"
	"os/signal"
	"syscall"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/cmdbrowser"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"

	"github.com/spf13/cobra"
)

// Test seams — overridden in command_test.go. Subtests that override these
// MUST NOT call t.Parallel() (global state across goroutines).
var (
	runAsk         = ask.Run
	confirmRun     = widgets.ConfirmRun
	runUserCommand = usercommands.RunCommand
	notifyContext  = signal.NotifyContext
)

// errCommandsListed is the sentinel returned by the non-interactive selector
// fallback after it printed the command list instead of selecting — the run
// route treats it as a successful no-op.
var errCommandsListed = errors.New("command list printed instead of interactive selection")

// nonInteractiveEnv reports whether DWE_NONINTERACTIVE is truthy — thin alias
// over the shared cmdctx.NonInteractiveEnv (also consumed by the bare
// `dwe docs` list fallback).
func nonInteractiveEnv() bool {
	return cmdctx.NonInteractiveEnv()
}

// runOpts carries the per-invocation options for runCommandByID.
type runOpts struct {
	Inspect        bool
	Yes            bool // user-explicit --yes OR'd with TUI y-toggle at the call site
	ForceParamForm bool // TUI-only: user picked the item via the edit-params key
	SetValues      []string
	Silent         bool            // suppress end-of-command desktop notification
	Translator     i18n.Translator // for localized string lookups; nil-safe via TranslatorOrNop
	Locale         string          // active locale code (e.g. "ru", "en")
	// PrefilledParams carries param values harvested by the in-TUI param-form
	// overlay (cmdbrowser Result.Values). When non-nil, runCommandByID skips its
	// own huh form entirely and uses these values directly. nil = build/prompt
	// the form here as usual (every non-browser path leaves it nil).
	PrefilledParams map[string]string
	// PassThroughArgs carries everything the caller wrote after `--`
	// (`dwe cmd site.test -- --run x.test.ts`). It reaches the command only
	// through ${args}; a command that does not reference it rejects a non-empty
	// slice — see checkPassThroughArgs.
	PassThroughArgs []string
}

// NewCmd builds the `dwe commands` command tree.
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
		Short:   "Run, inspect, and list dwe commands",
		Long: `Manage declarative commands defined in workspace/commands/.

Commands are YAML-defined operations organized into groups (e.g. db, app, services.main).
They can be shell commands, scripts, service exec/run operations, or multi-step workflows.

Without an id, an interactive selector lists public commands. With a group prefix
(e.g. services.main), the selector is filtered. With a full command id, it runs
(or inspects with -i) directly without a selector.`,
		Example: `  dwe commands
  dwe commands list
  dwe commands db.up
  dwe commands db.up --set env=local
  dwe commands -i db.up
  dwe cmd db.up --yes`,
		// One positional id, plus anything after `--` for a command that
		// declares ${args}. Everything past the dash is the caller's, so the
		// count is only checked on the near side; the far side is validated
		// per-command by checkPassThroughArgs, which can name the command and
		// the fix. cobra's stock MaximumNArgs(1) reported "Accepts at most 1
		// arg(s), received 3" here and left the caller nowhere to go.
		Args:         commandIDArgs,
		SilenceUsage: true,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			// Cobra parses flags before invoking ValidArgsFunction, so --inspect
			// is available even though PersistentPreRunE is bypassed in the
			// __complete path.
			inspect, _ := cmd.Flags().GetBool("inspect")
			return registryIDCompletion(flags, inspect)(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Split at `--` immediately: everything past the dash belongs to the
			// target command, and every id/group/selector decision below counts
			// positional args. Leaving them merged would make `dwe cmd site.test
			// -- --run x` look like a three-argument invocation and fall through
			// to the interactive selector instead of running site.test.
			through := passThroughArgs(cmd, args)
			args = nearArgs(cmd, args)

			reg, err := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
			if err != nil {
				return cmdctx.ErrWrap("command_registry_invalid", err)
			}

			// Inspect route: exact id required; private allowed; cfg load errors tolerated.
			if inspectFlag {
				if len(args) == 0 {
					return cmdctx.Err("usage_error", "id required with --inspect")
				}
				// Inspect prints a definition, it does not run one, so there is
				// nothing for pass-through arguments to reach. Rejecting here
				// keeps the "extra arguments are opt-in and refused loudly"
				// contract whole — the run route enforces it per-command via
				// checkPassThroughArgs, which this route never reaches.
				if len(through) > 0 {
					return cmdctx.Err("usage_error",
						"--inspect prints a command's definition rather than running it, so it takes no arguments after `--`\n\n"+
							"Drop the `--` part to inspect:      dwe cmd -i "+args[0]+"\n"+
							"Or drop --inspect to run it:        dwe cmd "+args[0]+" -- ...")
				}
				// Best-effort cfg load — inspect tolerates malformed configs so users
				// can still introspect command definitions when the project is broken.
				// ApplyVisibility is fail-open: per-expression eval failures log a
				// warning and treat the command as visible, so a broken project never
				// blocks inspect.
				inspectCfg, _ := config.LoadConfig(flags.ConfigPath)
				_ = reg.ApplyVisibility(inspectCfg, flags.ProjectRoot())
				if flags.Output == "json" {
					def, err := reg.Get(args[0])
					if err != nil {
						return cmdctx.ErrWrap("command_unknown", err).WithDetail("id", args[0])
					}
					if err := bridgeGuard(def); err != nil {
						return err
					}
					translator := i18n.TranslatorOrNop(flags.I18n)
					data := buildCommandInspectJSON(def, translator, flags.Locale)
					return cmdctx.WriteJSON(flags, cmd, data)
				}
				return runCommandByID(
					cmd.Context(),
					cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
					inspectCfg, reg, flags.ProjectRoot(), args[0],
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
				return cmdctx.ErrWrap("project_invalid_config", err)
			}
			// ApplyVisibility is fail-open: a single bad hide expression
			// must never brick the entire `dwe commands` UX.
			_ = reg.ApplyVisibility(cfg, flags.ProjectRoot())

			ctx, stop := notifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			var (
				skipConfirmFromTUI bool
				forceFormFromTUI   bool
				prefilledFromTUI   map[string]string
			)
			selector := makeBrowserSelector(cfg, reg, cmdbrowser.ModeRun, false, setFlags, &skipConfirmFromTUI, &forceFormFromTUI, &prefilledFromTUI, i18n.TranslatorOrNop(flags.I18n), flags.Locale, flags.ProjectRoot())
			if !widgets.IsInteractiveFn(cmd.InOrStdin()) || nonInteractiveEnv() {
				// No TTY for the browser (CI pipe) or forced non-interactive
				// (DWE_NONINTERACTIVE=1 — the bridge daemon sets it for every
				// container invocation): print `commands list` output instead
				// of erroring, so bare `dwe commands` stays useful.
				selector = func(_ []*usercommands.CommandDef, _ string) (string, error) {
					groupFilter := ""
					if len(args) == 1 {
						groupFilter = args[0]
					}
					if err := writeCommandsList(cmd, flags, reg, groupFilter, false); err != nil {
						return "", err
					}
					return "", errCommandsListed
				}
			}
			id, err := resolveCommandID(reg, args, false, cfg, selector)
			if err != nil {
				if errors.Is(err, widgets.ErrCancelled) || errors.Is(err, errCommandsListed) {
					return nil
				}
				return err
			}
			return runCommandByID(
				ctx,
				cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
				cfg, reg, flags.ProjectRoot(), id,
				runOpts{
					Yes:             skipConfirm || skipConfirmFromTUI,
					ForceParamForm:  forceFormFromTUI,
					SetValues:       setFlags,
					Silent:          silent,
					Translator:      i18n.TranslatorOrNop(flags.I18n),
					Locale:          flags.Locale,
					PrefilledParams: prefilledFromTUI,
					PassThroughArgs: through,
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
