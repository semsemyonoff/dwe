package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/resolve"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"

	huh "charm.land/huh/v2"
)

// runCommandByID is the single execution path for both `dwe commands <id>`
// and the TUI run flow. It handles inspect routing, param prompting,
// confirmation summary, and dispatch to the runner.
func runCommandByID(
	ctx context.Context,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	cfg *config.DweConfig,
	reg *usercommands.Registry,
	projectRoot string,
	id string,
	opts runOpts,
) error {
	def, err := reg.Get(id)
	if err != nil {
		return cmdctx.ErrWrap("command_unknown", err).WithDetail("id", id)
	}

	// Bridge guard sits BEFORE the inspect route: unlike Hidden (where
	// inspect is the debug aid for "why did my command disappear"), the
	// container surface is a hard gate — inspect is rejected too.
	if err := bridgeGuard(def); err != nil {
		return err
	}

	// Normalize Translator to never be nil; tests or edge cases might not set it.
	if opts.Translator == nil {
		opts.Translator = i18n.NopTranslator{}
	}

	// Inspect route — write the formatted definition and stop.
	// Inspect is allowed on hidden commands (informational) so users can
	// debug why a command disappeared from listings.
	if opts.Inspect {
		printInspect(stdout, def, cfg, reg, opts.Translator, opts.Locale, projectRoot)
		return nil
	}

	// Hidden guard: hide is a runtime condition, so the user-facing error
	// distinguishes it from Private (developer intent) and points at the
	// remediation — usually "the condition turned true; check `hide:` or
	// enable the underlying service".
	if def.Hidden {
		err := cmdctx.Err("command_unknown",
			fmt.Sprintf("command %q is hidden by its hide: condition", id)).
			WithDetail("id", id).
			WithHint("Run `dwe commands -i " + id + "` to see the hide expression.")
		return err
	}

	// Private guard: only block direct run; inspect already returned above.
	if def.Private {
		return fmt.Errorf("command %q is private and cannot be run directly", id)
	}

	provided, err := parseSetFlags(opts.SetValues)
	if err != nil {
		return err
	}

	skipPrompts := opts.Yes || nonInteractiveEnv()
	canPromptHuh := widgets.IsInteractiveFn(stdin) && !skipPrompts

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
		res, ferr := runAsk(ctx, "dwe commands › "+def.ID, fields, ask.RunOptions{Input: stdin, Output: stdout})
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
			if errors.Is(cerr, widgets.ErrCancelled) {
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
	parts := []string{"▶ " + styles.StyleKey(def.ID)}
	if def.Type != "" {
		parts = append(parts, styles.StyleMuted("["+string(def.Type)+"]"))
	}
	desc := translator.CommandDescription(locale, def.ID, def.Description)
	if desc != "" {
		parts = append(parts, styles.StyleMuted(desc))
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
