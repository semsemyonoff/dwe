package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"devbox-cli/internal/config"
	"devbox-cli/internal/daemon"
	"devbox-cli/internal/i18n"
	"devbox-cli/internal/render"
	"devbox-cli/internal/tpl"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/ui/ask"
	"devbox-cli/internal/ui/cmdbrowser"
	"devbox-cli/internal/usercommands"
	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/usercommands/resolve"

	huh "charm.land/huh/v2"
	"github.com/spf13/cobra"
)

// Test seams — overridden in command_cmd_test.go. Subtests that override these
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

func newCommandCmd(flags *rootFlags) *cobra.Command {
	var (
		setFlags    []string
		skipConfirm bool
		inspectFlag bool
		silent      bool
	)

	cmd := &cobra.Command{
		Use:     "commands [id]",
		Aliases: []string{"cmd"},
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
			reg, err := loadCommandRegistry(flags.configPath)
			if err != nil {
				return err
			}

			// Inspect route: exact id required; private allowed; cfg load errors tolerated.
			if inspectFlag {
				if len(args) == 0 {
					return errors.New("id required with --inspect")
				}
				cfg, _ := config.LoadConfig(flags.configPath)
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
			cfg, err := config.LoadConfig(flags.configPath)
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
	addSilentFlag(cmd, &silent)
	cmd.MarkFlagsMutuallyExclusive("inspect", "set")
	cmd.MarkFlagsMutuallyExclusive("inspect", "yes")
	cmd.MarkFlagsMutuallyExclusive("inspect", "silent")

	cmd.AddCommand(newCommandListCmd(flags))
	return cmd
}

// runCommandByID is the single execution path for both `devbox commands <id>`
// and the TUI run flow. It handles inspect routing, param prompting,
// confirmation summary, and dispatch to the runner.
func runCommandByID(
	ctx context.Context,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	cfg *config.DevboxConfig,
	reg *usercommands.Registry,
	projectRoot string,
	id string,
	opts runOpts,
) error {
	def, err := reg.Get(id)
	if err != nil {
		return err
	}

	// Normalize Translator to never be nil; tests or edge cases might not set it.
	if opts.Translator == nil {
		opts.Translator = i18n.NopTranslator{}
	}

	// Inspect route — write the formatted definition and stop.
	if opts.Inspect {
		printInspect(stdout, def, cfg, reg, opts.Translator, opts.Locale)
		return nil
	}

	// Private guard: only block direct run; inspect already returned above.
	if def.Private {
		return fmt.Errorf("command %q is private and cannot be run directly", id)
	}

	provided, err := parseSetFlags(opts.SetValues)
	if err != nil {
		return err
	}

	nonInteractiveEnv := os.Getenv("DEVBOX_NONINTERACTIVE") == "1" || os.Getenv("DEVBOX_NONINTERACTIVE") == "true"
	skipPrompts := opts.Yes || nonInteractiveEnv
	canPromptHuh := ui.IsInteractiveFn(stdin) && !skipPrompts

	prefilled := resolve.ParamDefaults(def.Params, provided, cfg)

	// Resolve options for all select/multiselect params and validate membership
	// (--set must be in options if non-empty; defaults must be in non-empty options).
	resolvedOpts := make(map[string][]model.OptionItem)
	for name, p := range def.Params {
		if p.Options != nil && (p.EffectiveWidget() == model.WidgetSelect || p.EffectiveWidget() == model.WidgetMultiselect) {
			items, rerr := resolve.Options(p.Options, cfg.Raw)
			if rerr != nil {
				return fmt.Errorf("resolving options for param %q: %w", name, rerr)
			}
			resolvedOpts[name] = items

			// Membership-check rule:
			// - --set name=value: if options non-empty AND value ∉ options → error.
			//   Empty options + --set → bypass, trust user.
			// - default_from/default: if options non-empty AND value ∉ options → error.
			//   Empty options + default/default_from → error (config bug).
			if len(items) > 0 {
				value := prefilled[name]
				if value != "" {
					// For multiselect, validate each selected item individually.
					candidates := []string{value}
					if p.EffectiveWidget() == model.WidgetMultiselect {
						sep := p.Separator
						if sep == "" {
							sep = " "
						}
						candidates = strings.Split(value, sep)
					}
					optionSet := make(map[string]bool, len(items))
					for _, opt := range items {
						optionSet[opt.Value] = true
					}
					for _, candidate := range candidates {
						if !optionSet[candidate] {
							if provided[name] != "" {
								// --set value not in options.
								return fmt.Errorf("param %q: value %q not in options (valid: %s)", name, candidate, joinOptionValues(items))
							}
							// default_from or default not in options.
							return fmt.Errorf("param %q: default value %q not in options", name, candidate)
						}
					}
				}
			} else if prefilled[name] != "" && provided[name] == "" {
				// Empty options but a default/default_from exists → error.
				if p.DefaultFrom != "" {
					return fmt.Errorf("options for param %q resolved empty, but has default_from %q", name, p.DefaultFrom)
				} else if p.Default != "" {
					return fmt.Errorf("options for param %q resolved empty, but has default %q", name, p.Default)
				}
			}
		}
	}

	// Skip the form when every required param already has a value (from --set
	// or a declared Default / DefaultFrom) — pressing Enter on a form full of
	// pre-filled values is just friction. The TUI exposes the EditParams key
	// for the "I want to tweak the defaults" case, which sets ForceParamForm.
	showForm := canPromptHuh && len(def.Params) > 0 &&
		(opts.ForceParamForm || !allRequiredSatisfied(def.Params, prefilled))

	// Build the form values: either via huh form (canPromptHuh) or from prefilled.
	values := prefilled
	if showForm {
		fields, ferr := buildAskFields(def, prefilled, provided, opts.Translator, opts.Locale, resolvedOpts)
		if ferr != nil {
			return ferr
		}
		res, ferr := runAsk(ctx, "devbox commands › "+def.ID, fields, ask.RunOptions{Input: stdin, Output: stdout})
		if ferr != nil {
			if errors.Is(ferr, huh.ErrUserAborted) {
				return nil
			}
			return ferr
		}
		values = mergeAnswers(res, def.Params, prefilled)
	} else if !canPromptHuh {
		// Non-interactive (pipe or skip-prompts): pre-flight missing-required check
		// so the user sees a clear error instead of the runtime "param required" surfaced
		// later by resolve.Params.
		var missing []string
		for name, p := range def.Params {
			if p.Required && prefilled[name] == "" {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return fmt.Errorf("missing required params: %s", strings.Join(missing, ", "))
		}
	}

	// Build the run context (final resolve.Params runs here — validates required,
	// pattern, and type coercion). The form already ran per-field Validate, so any
	// error here is a safety net.
	with := make(map[string]any, len(values))
	for k, v := range values {
		with[k] = v
	}
	rctx, err := usercommands.BuildRunContext(cfg, reg, def, with, projectRoot)
	if err != nil {
		return fmt.Errorf("building run context: %w", err)
	}

	rctx.Stdin = stdin
	rctx.Stdout = stdout
	rctx.Stderr = stderr
	rctx.SkipNotify = opts.Silent
	rctx.Translator = opts.Translator
	rctx.Locale = opts.Locale

	if def.Confirmation && canPromptHuh {
		title := opts.Translator.CommandConfirmationText(opts.Locale, def.ID, def.EffectiveConfirmationText())
		if rctx.Render != nil {
			rendered, rerr := tpl.RenderCommand(title, rctx.Render)
			if rerr != nil {
				return fmt.Errorf("render confirmation_text: %w", rerr)
			}
			title = rendered
		}
		// Summary built from normalized rctx.Params (post-resolve) so the user
		// sees what the command actually receives, not raw form input.
		summary := stringifyParams(rctx.Params)
		ok, cerr := confirmRun(title, summary)
		if cerr != nil {
			if errors.Is(cerr, ui.ErrCancelled) {
				return nil
			}
			return cerr
		}
		if !ok {
			return nil
		}
		// Prevent runtime ConfirmCommand from re-prompting.
		rctx.SkipConfirm = true
	} else {
		rctx.SkipConfirm = skipPrompts
		rctx.NonInteractive = skipPrompts
	}

	printRunHeader(stdout, def, opts.Translator, opts.Locale)

	if err := runUserCommand(ctx, rctx); err != nil {
		return fmt.Errorf("running command %q: %w", id, err)
	}
	return nil
}

// printRunHeader writes a one-line banner identifying the command about to
// execute so the user has context for the runner output that follows.
// Format: `▶ <id>  [<type>]  <description>`. Type and description are omitted
// when empty.
func printRunHeader(w io.Writer, def *usercommands.CommandDef, translator i18n.Translator, locale string) {
	parts := []string{"▶ " + ui.StyleKey(def.ID)}
	if def.Type != "" {
		parts = append(parts, ui.StyleMuted("["+string(def.Type)+"]"))
	}
	desc := translator.CommandDescription(locale, def.ID, def.Description)
	if desc != "" {
		parts = append(parts, ui.StyleMuted(desc))
	}
	_, _ = fmt.Fprintln(w, strings.Join(parts, "  "))
}

// buildAskFields converts a command's params into ordered ask.Field values.
// It applies the empty-options rule: when a select/multiselect param has
// len(resolvedOpts[name]) == 0:
//   - prefilled via --set → skip field, keep explicit value
//   - no prefill AND required → error
//   - no prefill AND optional → skip field
func buildAskFields(def *usercommands.CommandDef, prefilled, provided map[string]string, translator i18n.Translator, locale string, resolvedOpts map[string][]model.OptionItem) ([]ask.Field, error) {
	names := make([]string, 0, len(def.Params))
	for name := range def.Params {
		names = append(names, name)
	}
	sort.Strings(names)

	var fields []ask.Field
	for _, name := range names {
		p := def.Params[name]
		paramDesc := translator.ParamDescription(locale, def.ID, name, p.Description)
		widget := p.EffectiveWidget()

		// Empty-options rule for select/multiselect.
		if widget == model.WidgetSelect || widget == model.WidgetMultiselect {
			opts := resolvedOpts[name]
			if len(opts) == 0 {
				// No options available.
				if prefilled[name] != "" && provided[name] != "" {
					// Prefilled via --set: skip field, keep value (escape hatch).
					continue
				}
				if prefilled[name] == "" && p.Required {
					// Required but no options: error.
					if p.Options != nil && p.Options.From != "" {
						return nil, fmt.Errorf("no options for param %q: ${%s} is empty", name, p.Options.From)
					}
					return nil, fmt.Errorf("no options for param %q", name)
				}
				// Optional with no options: skip field.
				continue
			}
		}

		// Build the ask.Field.
		title := name
		if p.Required {
			title += " *"
		}
		if prefilled[name] != "" && provided[name] == "" {
			title += " (default)"
		}

		field := ask.Field{
			Key:         name,
			Title:       title,
			Description: paramDesc,
			Required:    p.Required,
			Kind:        widgetToFieldKind(widget),
		}

		// Set defaults and options based on widget type.
		switch widget {
		case model.WidgetInput, model.WidgetConfirm:
			field.Default = prefilled[name]
			if widget == model.WidgetInput && p.Pattern != "" {
				pat, perr := regexp.Compile(p.Pattern)
				if perr == nil {
					field.Validate = func(s string) error {
						if !pat.MatchString(s) {
							return fmt.Errorf("value must match pattern %s", p.Pattern)
						}
						return nil
					}
				}
			}

		case model.WidgetSelect:
			field.Default = prefilled[name]
			field.Options = optionsToAskOptions(resolvedOpts[name], translator, locale, def.ID, name)

		case model.WidgetMultiselect:
			// Split Default by separator to get initial selections.
			sep := p.Separator
			if sep == "" {
				sep = " "
			}
			if prefilled[name] != "" {
				field.Defaults = strings.Split(prefilled[name], sep)
			}
			field.Default = prefilled[name] // for display/info
			field.Options = optionsToAskOptions(resolvedOpts[name], translator, locale, def.ID, name)
		}

		fields = append(fields, field)
	}

	return fields, nil
}

// widgetToFieldKind converts a ParamWidget to an ask.FieldKind.
func widgetToFieldKind(w model.ParamWidget) ask.FieldKind {
	switch w {
	case model.WidgetInput:
		return ask.FieldInput
	case model.WidgetSelect:
		return ask.FieldSelect
	case model.WidgetMultiselect:
		return ask.FieldMultiselect
	case model.WidgetConfirm:
		return ask.FieldConfirm
	default:
		return ask.FieldInput
	}
}

// optionsToAskOptions converts model.OptionItem to ask.Option with optional translation.
func optionsToAskOptions(items []model.OptionItem, translator i18n.Translator, locale, commandID, paramName string) []ask.Option {
	opts := make([]ask.Option, len(items))
	for i, item := range items {
		label := translator.ParamOptionLabel(locale, commandID, paramName, item.Value, item.Label)
		opts[i] = ask.Option{
			Value: item.Value,
			Label: label,
		}
	}
	return opts
}

// joinOptionValues returns a comma-separated list of option values for error messages.
func joinOptionValues(items []model.OptionItem) string {
	vals := make([]string, len(items))
	for i, item := range items {
		vals[i] = item.Value
	}
	return strings.Join(vals, ", ")
}

// mergeAnswers merges form results back into the values map.
// Input/select → string; multiselect → joined by separator; confirm → "true"/"false".
func mergeAnswers(res ask.Result, defs map[string]model.ParamDef, prevValues map[string]string) map[string]string {
	out := make(map[string]string, len(prevValues))
	// Start with previous values (unfilled fields will be kept as-is).
	maps.Copy(out, prevValues)

	// Update with form results — only keys that were actually in the form.
	// Fields skipped by buildAskFields (empty-options escape hatch, optional skip)
	// are absent from res; omitting them preserves the prevValues copy above.
	for name, def := range defs {
		if !res.Has(name) {
			continue
		}
		widget := def.EffectiveWidget()
		switch widget {
		case model.WidgetInput, model.WidgetSelect:
			out[name] = res.String(name)

		case model.WidgetMultiselect:
			values := res.Strings(name)
			sep := def.Separator
			if sep == "" {
				sep = " "
			}
			out[name] = strings.Join(values, sep)

		case model.WidgetConfirm:
			b := res.Bool(name)
			out[name] = fmt.Sprintf("%v", b)
		}
	}

	return out
}

// allRequiredSatisfied reports whether every required param already has a
// non-empty value in prefilled (which is the merge of provided ∪ DefaultFrom
// ∪ Default). Optional params are satisfied regardless of value — an empty
// optional param is a valid input that resolve.Params will accept.
func allRequiredSatisfied(defs map[string]model.ParamDef, prefilled map[string]string) bool {
	for name, p := range defs {
		if p.Required && prefilled[name] == "" {
			return false
		}
	}
	return true
}

// stringifyParams converts resolved params (map[string]any) into the
// string map ConfirmRun consumes for its summary.
func stringifyParams(params map[string]any) map[string]string {
	out := make(map[string]string, len(params))
	for k, v := range params {
		out[k] = fmt.Sprintf("%v", v)
	}
	return out
}

func newCommandListCmd(flags *rootFlags) *cobra.Command {
	var showAll bool

	cmd := &cobra.Command{
		Use:   "list [group]",
		Short: "List available commands",
		Long: `List all available declarative commands from devbox/commands/.

An optional group filter narrows the output to a specific command group (e.g. 'db', 'services.main').
Use --all to include private commands.`,
		Example: `  devbox commands list
  devbox commands list db
  devbox commands list --all`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			groupFilter := ""
			if len(args) > 0 {
				groupFilter = args[0]
			}
			reg, err := loadCommandRegistry(flags.configPath)
			if err != nil {
				return err
			}
			root := reg.Groups()
			nodes := buildTreeNodes(root, groupFilter, showAll, i18n.TranslatorOrNop(flags.I18n), flags.Locale)
			if len(nodes) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No commands found.")
				return nil
			}
			printTreeNodes(cmd.OutOrStdout(), nodes)
			return nil
		},
		SilenceUsage: true,
	}
	cmd.Flags().BoolVar(&showAll, "all", false, "Include private commands")
	return cmd
}

// loadCommandRegistry loads the command registry from devbox/commands/ relative
// to the config file. Returns an empty registry when the directory does not exist.
func loadCommandRegistry(configPath string) (*usercommands.Registry, error) {
	return usercommands.LoadRegistryFromConfigPath(configPath)
}

// selectCommandFn is the function signature for interactive command selection.
// It receives a slice of CommandDefs and a display title, and returns the chosen ID.
type selectCommandFn func(defs []*usercommands.CommandDef, title string) (string, error)

// makeBrowserSelector returns a selectCommandFn that drives the cmdbrowser
// TUI. The returned closure captures cfg (for resolving ui.commands.*
// defaults via the nil-safe accessors), mode, the includePrivate flag, and
// (run-site only) pointers to bools that receive Result.SkipConfirm and
// Result.ForceParamForm.
//
// For ModeInspect the skipConfirmOut / forceFormOut pointers are unused
// (their key bindings are disabled in inspect mode); pass nil.
func makeBrowserSelector(cfg *config.DevboxConfig, reg *usercommands.Registry, mode cmdbrowser.Mode, includePrivate bool, skipConfirmOut, forceFormOut *bool, translator i18n.Translator, locale string) selectCommandFn {
	return func(defs []*usercommands.CommandDef, title string) (string, error) {
		items := make([]cmdbrowser.Item, len(defs))
		for i, d := range defs {
			curDef := d
			items[i] = cmdbrowser.Item{
				ID:          d.ID,
				Description: translator.CommandDescription(locale, d.ID, d.Description),
				Type:        string(d.Type),
				Private:     d.Private,
				ParamCount:  len(d.Params),
				Inspect: func(width int) string {
					var buf bytes.Buffer
					printInspectAt(&buf, curDef, cfg, reg, width, translator, locale)
					return buf.String()
				},
			}
		}
		opts := cmdbrowser.Options{
			DefaultExpandedDepth: config.UICommandsDefaultDepth(cfg),
			AutoCollapseEmpty:    config.UICommandsAutoCollapseEmpty(cfg),
			ShowTypeBadges:       config.UICommandsShowTypeBadges(cfg),
			IncludePrivate:       includePrivate,
			Mode:                 mode,
		}
		res, err := cmdbrowser.Run(title, items, opts)
		if err != nil {
			return "", err
		}
		if skipConfirmOut != nil && res.SkipConfirm {
			*skipConfirmOut = true
		}
		if forceFormOut != nil && res.ForceParamForm {
			*forceFormOut = true
		}
		if res.Idx < 0 || res.Idx >= len(defs) {
			return "", fmt.Errorf("cmdbrowser: result index %d out of range [0, %d)", res.Idx, len(defs))
		}
		return defs[res.Idx].ID, nil
	}
}

// resolveCommandID determines the target command ID from optional positional args.
//
//   - No args: calls selector with all public (or all when includePrivate is true) usercommands.
//   - One arg that is a full command ID (registry.Get succeeds): returns it directly.
//   - One arg that is a group prefix (registry.List returns results): calls selector
//     filtered to that group.
//   - One arg that is neither: returns an error.
//
// projectName, when non-empty, is prepended to the selector title as
// "<project> · Select command [...]" so the TUI header makes clear which
// devbox project is active.
func resolveCommandID(reg *usercommands.Registry, args []string, includePrivate bool, projectName string, selector selectCommandFn) (string, error) {
	if len(args) == 1 {
		arg := args[0]
		// Exact command ID — use directly without selector.
		if _, err := reg.Get(arg); err == nil {
			return arg, nil
		}
		// Try as a group prefix.
		var defs []*usercommands.CommandDef
		if includePrivate {
			defs = reg.ListAll(arg)
		} else {
			defs = reg.List(arg)
		}
		if len(defs) == 0 {
			return "", fmt.Errorf("command %q not found", arg)
		}
		return selector(defs, selectorTitle(projectName, "Commands ("+arg+")"))
	}
	// No arg — show full list.
	var defs []*usercommands.CommandDef
	if includePrivate {
		defs = reg.ListAll("")
	} else {
		defs = reg.List("")
	}
	if len(defs) == 0 {
		return "", fmt.Errorf("no commands available")
	}
	return selector(defs, selectorTitle(projectName, "Commands"))
}

// selectorTitle composes the selector header from a fixed "Devbox" prefix,
// the project name (when set), and the base title, joined with middots. The
// "Devbox" prefix is always present so the TUI advertises which tool owns the
// window regardless of project context.
func selectorTitle(projectName, base string) string {
	parts := []string{"Devbox"}
	if projectName != "" {
		parts = append(parts, projectName)
	}
	parts = append(parts, base)
	return strings.Join(parts, " · ")
}

// parseSetFlags parses --set key=value flags into a map.
func parseSetFlags(flags []string) (map[string]string, error) {
	result := make(map[string]string, len(flags))
	for _, f := range flags {
		k, v, found := strings.Cut(f, "=")
		if !found {
			return nil, fmt.Errorf("--set %q: expected key=value format", f)
		}
		if k == "" {
			return nil, fmt.Errorf("--set %q: key must not be empty", f)
		}
		result[k] = v
	}
	return result, nil
}

// buildTreeNodes converts a GroupNode tree to render.TreeNode slices.
// When groupFilter is non-empty, only the matching sub-tree is rendered.
// Private commands are excluded when includePrivate is false.
func buildTreeNodes(root *usercommands.GroupNode, groupFilter string, includePrivate bool, translator i18n.Translator, locale string) []*render.TreeNode {
	if groupFilter != "" {
		target := findGroupNode(root, groupFilter)
		if target == nil {
			return nil
		}
		return groupNodeToChildren(target, includePrivate, translator, locale)
	}
	return groupNodeToChildren(root, includePrivate, translator, locale)
}

// findGroupNode searches the tree for a node with the given dot-separated ID.
func findGroupNode(node *usercommands.GroupNode, id string) *usercommands.GroupNode {
	if node.ID == id {
		return node
	}
	for _, child := range node.Children {
		if found := findGroupNode(child, id); found != nil {
			return found
		}
	}
	return nil
}

// groupNodeToChildren converts a GroupNode's contents into render.TreeNode slices,
// adding sub-groups and commands as children. Sub-groups without visible content
// are omitted when includePrivate is false.
func groupNodeToChildren(gn *usercommands.GroupNode, includePrivate bool, translator i18n.Translator, locale string) []*render.TreeNode {
	var nodes []*render.TreeNode
	for _, child := range gn.Children {
		childNode := groupNodeToSingleNode(child, includePrivate, translator, locale)
		if childNode != nil {
			nodes = append(nodes, childNode)
		}
	}
	for _, cmd := range gn.Commands {
		if !includePrivate && cmd.Private {
			continue
		}
		nodes = append(nodes, commandDefToTreeNode(cmd, translator, locale))
	}
	return nodes
}

// groupNodeToSingleNode converts a GroupNode into a single render.TreeNode.
// Returns nil when the group has no visible content (after private filtering).
func groupNodeToSingleNode(gn *usercommands.GroupNode, includePrivate bool, translator i18n.Translator, locale string) *render.TreeNode {
	children := groupNodeToChildren(gn, includePrivate, translator, locale)
	if !includePrivate && len(children) == 0 {
		return nil
	}
	desc := translator.GroupDescription(locale, gn.ID, gn.Meta.Description)
	node := &render.TreeNode{
		Label:    translator.GroupTitle(locale, gn.ID, gn.Name),
		Desc:     desc,
		Children: children,
	}
	return node
}

// commandDefToTreeNode converts a CommandDef into a leaf render.TreeNode.
func commandDefToTreeNode(cmd *usercommands.CommandDef, translator i18n.Translator, locale string) *render.TreeNode {
	var tags []string
	if cmd.Private {
		tags = append(tags, "private")
	}
	tags = append(tags, string(cmd.Type))
	desc := translator.CommandDescription(locale, cmd.ID, cmd.Description)
	return &render.TreeNode{
		Label: cmd.ID,
		Tags:  tags,
		Desc:  desc,
	}
}

// registryIDCompletion returns a ValidArgsFunction that completes command IDs
// from the registry. When includePrivate is true, private command IDs are also
// returned (useful for `inspect`). When false, only public IDs are returned
// (useful for `run`).
func registryIDCompletion(flags *rootFlags, includePrivate bool) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// Only complete the first positional argument.
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		configPath, _, err := completionConfigPath(flags, cmd)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		reg, err := loadCommandRegistry(configPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var defs []*usercommands.CommandDef
		if includePrivate {
			defs = reg.ListAll("")
		} else {
			defs = reg.List("")
		}
		completions := make([]string, 0, len(defs)+1)
		if !includePrivate && len(defs) > 0 {
			// Active Help: hint for run subcommand.
			completions = cobra.AppendActiveHelp(completions, "Use 'devbox commands --inspect <id>' to see command details")
		}
		translator := i18n.TranslatorOrNop(flags.I18n)
		for _, d := range defs {
			entry := d.ID
			desc := translator.CommandDescription(flags.Locale, d.ID, d.Description)
			if desc != "" {
				entry = cobra.CompletionWithDesc(d.ID, desc)
			}
			completions = append(completions, entry)
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
}

// inspectStepDescription returns the localized description for the command
// referenced by a workflow step, formatted with a leading em-dash separator so
// it can be concatenated onto a definition value. Returns "" when the command
// is unknown or carries no description.
func inspectStepDescription(reg *usercommands.Registry, translator i18n.Translator, locale, commandID string) string {
	if reg == nil || commandID == "" {
		return ""
	}
	target, err := reg.Get(commandID)
	if err != nil {
		return ""
	}
	desc := translator.CommandDescription(locale, target.ID, target.Description)
	if desc == "" {
		return ""
	}
	return " — " + desc
}

// printInspect writes a detailed view of a command definition using Lipgloss styles.
// cfg may be nil at call sites that exercise the renderer purely structurally
// (tests); the resolved container-name block is then omitted.
//
// Word-wrap follows the terminal width. For renderings into a fixed sub-region
// (e.g. an inspect viewport narrower than the terminal), use
// [printInspectAt] with the explicit width — otherwise values wrap to the
// terminal and get silently clipped when the viewport renders.
func printInspect(w io.Writer, def *usercommands.CommandDef, cfg *config.DevboxConfig, reg *usercommands.Registry, translator i18n.Translator, locale string) {
	printInspectAt(w, def, cfg, reg, 0, translator, locale)
}

// printInspectAt is [printInspect] with an explicit wrap width. maxWidth == 0
// falls back to the terminal width.
func printInspectAt(w io.Writer, def *usercommands.CommandDef, cfg *config.DevboxConfig, reg *usercommands.Registry, maxWidth int, translator i18n.Translator, locale string) {
	def2 := func(name, value string, indent int) {
		_, _ = fmt.Fprintln(w, ui.RenderDefinitionAt(name, value, indent, "", maxWidth))
	}
	sub := func(title string) {
		_, _ = fmt.Fprintln(w, ui.RenderSubheader("  "+title))
	}

	_, _ = fmt.Fprintln(w, ui.RenderSectionTitle(def.ID))
	def2("type", string(def.Type), 2)
	if def.DerivedFromDaemon != "" {
		def2("derived from", "daemon "+def.DerivedFromDaemon, 2)
	}
	desc := translator.CommandDescription(locale, def.ID, def.Description)
	if desc != "" {
		def2("description", desc, 2)
	}
	if def.Private {
		def2("private", "true", 2)
	}
	if def.Confirmation {
		def2("confirmation", "true", 2)
		confirmText := translator.CommandConfirmationText(locale, def.ID, def.EffectiveConfirmationText())
		def2("confirmation_text", confirmText, 2)
	}
	if def.Messages.Success != "" || def.Messages.Error != "" {
		sub("Messages")
		if def.Messages.Success != "" {
			def2("success", def.Messages.Success, 4)
		}
		if def.Messages.Error != "" {
			def2("error", def.Messages.Error, 4)
		}
	}

	switch def.Type {
	case usercommands.CommandTypeDevbox:
		if def.Cmd != "" {
			def2("cmd", def.Cmd, 2)
		}
	case usercommands.CommandTypeShell:
		if def.Cmd != "" {
			def2("cmd", def.Cmd, 2)
		}
		if len(def.Argv) > 0 {
			def2("argv", strings.Join(def.Argv, " "), 2)
		}
		if def.Workdir != "" {
			def2("workdir", def.Workdir, 2)
		}
	case usercommands.CommandTypeServiceExec, usercommands.CommandTypeServiceRun:
		if def.Service != "" {
			def2("service", def.Service, 2)
		}
		if def.Runner != nil && def.Runner.Service != "" {
			def2("service (runner)", def.Runner.Service, 2)
		}
		if def.User != "" {
			def2("user", string(def.User), 2)
		}
		if def.Workdir != "" {
			def2("workdir", def.Workdir, 2)
		}
		if def.WorkdirFrom != "" {
			def2("workdir_from", def.WorkdirFrom, 2)
		}
		if def.Mode != "" {
			def2("mode", string(def.Mode), 2)
		}
		if len(def.ComposeArgs) > 0 {
			def2("compose_args", strings.Join(def.ComposeArgs, " "), 2)
		}
		if def.Cmd != "" {
			def2("cmd", def.Cmd, 2)
		}
		if len(def.Argv) > 0 {
			def2("argv", strings.Join(def.Argv, " "), 2)
		}
	case usercommands.CommandTypeScript:
		if def.Script != nil {
			shell := def.Script.Shell
			if shell == "" {
				shell = "sh"
			}
			def2("script.shell", shell, 2)
			if def.Script.Path != "" {
				def2("script.path", def.Script.Path, 2)
			}
			if def.Script.Plan != "" {
				def2("script.plan", def.Script.Plan, 2)
			}
			if def.Script.Run != "" {
				def2("script.run", def.Script.Run, 2)
			}
			if def.Script.Cleanup != "" {
				def2("script.cleanup", def.Script.Cleanup, 2)
			}
		}
		if def.Workdir != "" {
			def2("workdir", def.Workdir, 2)
		}
	case usercommands.CommandTypeBuiltin:
		if def.Cmd != "" {
			def2("builtin", def.Cmd, 2)
		}
		if len(def.With) > 0 {
			sub("With")
			var keys []string
			for k := range def.With {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				def2(k, fmt.Sprintf("%v", def.With[k]), 4)
			}
		}
	case usercommands.CommandTypeWorkflow:
		sub("Steps")
		for i, step := range def.Steps {
			switch {
			case step.Confirm != "":
				def2(fmt.Sprintf("[%d] confirm", i), step.Confirm, 4)
			case step.Parallel != nil:
				p := step.Parallel
				label := fmt.Sprintf("[%d] parallel", i)
				var meta []string
				if p.MaxConcurrent > 0 {
					meta = append(meta, fmt.Sprintf("max_concurrent=%d", p.MaxConcurrent))
				}
				if p.FailFast != nil {
					meta = append(meta, fmt.Sprintf("fail_fast=%v", *p.FailFast))
				}
				desc := fmt.Sprintf("%d sub-steps", len(p.Steps))
				if len(meta) > 0 {
					desc += "  " + strings.Join(meta, ", ")
				}
				if step.When != "" {
					desc += "  when: " + step.When
				}
				def2(label, desc, 4)
				for j, sub := range p.Steps {
					subDesc := sub.Command + inspectStepDescription(reg, translator, locale, sub.Command)
					if sub.When != "" {
						subDesc += "  when: " + sub.When
					}
					if sub.ContinueOnError {
						subDesc += "  (continue_on_error)"
					}
					def2(fmt.Sprintf("  [%d.%d]", i, j), subDesc, 6)
				}
			default:
				label := fmt.Sprintf("[%d]", i)
				desc := step.Command + inspectStepDescription(reg, translator, locale, step.Command)
				if len(step.With) > 0 {
					var pairs []string
					for k, v := range step.With {
						pairs = append(pairs, k+"="+v)
					}
					sort.Strings(pairs)
					desc += "  with: " + strings.Join(pairs, ", ")
				}
				if step.When != "" {
					desc += "  when: " + step.When
				}
				if step.ContinueOnError {
					desc += "  (continue_on_error)"
				}
				def2(label, desc, 4)
			}
		}
	}

	if def.DerivedFromDaemon != "" && def.SourceDaemon != nil {
		sub("Daemon")
		ds := def.SourceDaemon
		def2("container_template", ds.ContainerTemplate, 4)
		if ds.OnAlreadyRunning != "" {
			def2("on_already_running", ds.OnAlreadyRunning, 4)
		}
		if ds.AutoRemove != nil {
			def2("auto_remove", fmt.Sprintf("%v", *ds.AutoRemove), 4)
		}
		if ds.StopTimeout != "" {
			def2("stop_timeout", ds.StopTimeout, 4)
		}
		// Execution fields live in def.With for synthetic commands (registry
		// expansion packs Service/User/Workdir/Argv/etc into the rendered map).
		if def.With != nil {
			withStr := func(key string) string {
				if v, ok := def.With[key]; ok {
					if s, ok := v.(string); ok {
						return s
					}
				}
				return ""
			}
			if s := withStr("service"); s != "" {
				def2("service", s, 4)
			}
			if s := withStr("user"); s != "" {
				def2("user", s, 4)
			}
			if s := withStr("workdir"); s != "" {
				def2("workdir", s, 4)
			}
			if s := withStr("workdir_from"); s != "" {
				def2("workdir_from", s, 4)
			}
			if argv, ok := def.With["argv"].([]any); ok && len(argv) > 0 {
				parts := make([]string, 0, len(argv))
				for _, a := range argv {
					if s, ok := a.(string); ok {
						parts = append(parts, s)
					}
				}
				if len(parts) > 0 {
					def2("argv", strings.Join(parts, " "), 4)
				}
			}
		}
		if cfg != nil {
			sub("Container")
			defaults := make(map[string]any, len(def.Params))
			for name, p := range def.Params {
				defaults[name] = p.Default
			}
			rendered, err := tpl.RenderCommand(ds.ContainerTemplate, &tpl.RenderContext{
				Raw:    cfg.Raw,
				Params: defaults,
			})
			if err == nil {
				name, err := daemon.ResolveContainerName(cfg.Project.FullName(), rendered)
				if err == nil {
					def2("resolved (with default params)", name, 4)
				}
			}
		}
	}

	if len(def.Params) > 0 {
		sub("Params")
		var names []string
		for name := range def.Params {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			p := def.Params[name]
			desc := string(p.Type)
			paramDesc := translator.ParamDescription(locale, def.ID, name, p.Description)
			if paramDesc != "" {
				desc = paramDesc + " (" + string(p.Type) + ")"
			}
			if p.Required {
				desc += " [required]"
			}
			if p.Default != "" {
				desc += " [default: " + p.Default + "]"
			}
			if p.DefaultFrom != "" {
				desc += " [default_from: " + p.DefaultFrom + "]"
			}
			def2(name, desc, 4)
		}
	}

	if len(def.Context) > 0 {
		sub("Context")
		var names []string
		for name := range def.Context {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			c := def.Context[name]
			desc := "from: " + c.From
			if c.Required {
				desc += " [required]"
			}
			if c.Env != "" {
				desc += " [env: " + c.Env + "]"
			}
			def2(name, desc, 4)
		}
	}

	if len(def.Env) > 0 {
		sub("Env")
		var keys []string
		for k := range def.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			def2(k, def.Env[k], 4)
		}
	}

	if len(def.Files) > 0 {
		sub("Files")
		var fids []string
		for fid := range def.Files {
			fids = append(fids, fid)
		}
		sort.Strings(fids)
		for _, fid := range fids {
			f := def.Files[fid]
			desc := string(f.Access)
			if f.Path != "" {
				desc += "  path: " + f.Path
			} else if len(f.Candidates) > 0 {
				desc += fmt.Sprintf("  candidates: %d", len(f.Candidates))
			}
			if f.Env != "" {
				desc += "  env: " + f.Env
			}
			var flags []string
			if f.Required {
				flags = append(flags, "required")
			}
			if f.Mkdir {
				flags = append(flags, "mkdir")
			}
			if f.Overwrite {
				flags = append(flags, "overwrite")
			}
			if f.OnError != "" {
				flags = append(flags, "on_error: "+string(f.OnError))
			}
			if len(flags) > 0 {
				desc += "  [" + strings.Join(flags, ", ") + "]"
			}
			def2(fid, desc, 4)
		}
	}

	_, _ = fmt.Fprintln(w, ui.RenderSectionTitle(""))
}

// printTreeNodes renders a flat list of tree nodes to w using Lipgloss styles.
func printTreeNodes(w io.Writer, nodes []*render.TreeNode) {
	for _, node := range nodes {
		printTreeNode(w, node, 0)
	}
}

// printTreeNode renders a single tree node and its children recursively.
// Group nodes (those with children) use the group/section style; leaf nodes use the key style.
func printTreeNode(w io.Writer, node *render.TreeNode, depth int) {
	indent := strings.Repeat("  ", depth)
	var sb strings.Builder
	sb.WriteString(indent)

	if len(node.Children) > 0 {
		sb.WriteString(ui.StyleGroup(node.Label))
	} else {
		sb.WriteString(ui.StyleKey(node.Label))
		if len(node.Tags) > 0 {
			sb.WriteString("  ")
			sb.WriteString(ui.StyleMuted("[" + strings.Join(node.Tags, ", ") + "]"))
		}
	}

	if node.Desc != "" {
		sb.WriteString("  ")
		sb.WriteString(ui.StyleMuted("—"))
		sb.WriteString(" ")
		sb.WriteString(node.Desc)
	}

	_, _ = fmt.Fprintln(w, sb.String())

	for _, child := range node.Children {
		printTreeNode(w, child, depth+1)
	}
}
