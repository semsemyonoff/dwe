package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"strconv"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/validate/env"

	"charm.land/bubbles/v2/key"
	huh "charm.land/huh/v2"
)

// Form rendering tested manually; see plan Post-Completion.
// The pure coercion helpers (coerceInputAnswers, coercePortOverrides) are
// unit-tested; "no huh tests" means we don't drive form.Run() from go test.

// NewHuhAsker returns two callback functions for asking questions and port overrides.
// The returned functions are wired to the provided io.Writer for output.
// In production, out is cmd.OutOrStdout(); in tests, it can be redirected.
func NewHuhAsker(out io.Writer) (
	askQuestions func(ctx context.Context, questions []Question) (map[string]any, error),
	askPortOverrides func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error),
	askServiceToggles func(ctx context.Context, toggles []ServiceToggle) (map[string]bool, error),
) {
	askQuestions = func(ctx context.Context, questions []Question) (map[string]any, error) {
		if len(questions) == 0 {
			return nil, nil
		}

		// Build ask.Field objects from questions. Input questions are tracked
		// separately for post-form coercion.
		var fields []ask.Field
		var inputQuestions []Question

		for _, q := range questions {
			field := ask.Field{
				Key:         q.ID,
				Title:       q.Title,
				Description: q.Description,
				Required:    q.Required,
			}

			switch q.Type {
			case TypeInput:
				inputQuestions = append(inputQuestions, q)
				field.Kind = ask.FieldInput
				field.Validate = buildInputValidator(q)

			case TypeSelect:
				field.Kind = ask.FieldSelect
				field.Validate = buildInputValidator(q)
				for _, opt := range q.Options {
					field.Options = append(field.Options, ask.Option{
						Value: opt.Value,
						Label: opt.Label,
					})
				}

			case TypeMultiselect:
				field.Kind = ask.FieldMultiselect
				for _, opt := range q.Options {
					field.Options = append(field.Options, ask.Option{
						Value: opt.Value,
						Label: opt.Label,
					})
				}

			case TypeConfirm:
				field.Kind = ask.FieldConfirm
			}

			fields = append(fields, field)
		}

		// Run the ask form. styles.Theme() and SetHuhHooks are handled by ask.Run.
		result, err := ask.Run(ctx, "Setup", fields, ask.RunOptions{Output: out})
		if err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return nil, ErrWizardCanceled
			}
			return nil, fmt.Errorf("wizard: %w", err)
		}

		// Collect raw input values for coercion.
		inputRaws := make(map[string]string)
		for _, q := range inputQuestions {
			val := result.String(q.ID)
			inputRaws[q.ID] = val
		}

		// Coerce input answers to their proper types.
		coercedInputs, err := coerceInputAnswers(inputQuestions, inputRaws)
		if err != nil {
			return nil, err
		}

		// Assemble final answers map.
		answers := make(map[string]any)
		maps.Copy(answers, coercedInputs)

		// Add select/multiselect/confirm answers from result.
		for _, q := range questions {
			if q.Type == TypeInput {
				continue // Already handled via coercion
			}

			switch q.Type {
			case TypeSelect:
				answers[q.ID] = result.String(q.ID)
			case TypeMultiselect:
				answers[q.ID] = result.Strings(q.ID)
			case TypeConfirm:
				answers[q.ID] = result.Bool(q.ID)
			}
		}

		return answers, nil
	}

	askPortOverrides = func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
		if len(conflicts) == 0 {
			return nil, nil
		}

		// NOTE: This prompt remains a direct huh.NewForm call (not migrated to ask.Run)
		// because it uses custom keymaps to hijack the AcceptSuggestion binding for
		// showing "esc cancel" in the help line. The ask.Field API does not support
		// per-field keymaps; all keymap customization goes through widgets.SetHuhHooks which
		// applies globally. Port-override needs per-prompt keymap overrides that can't
		// be expressed via ask.Field, so it stays as a raw huh form.

		// Build one input field per conflict for port override.
		var huhFields []huh.Field
		portBindings := make(map[string]*string) // PortKey.Service/PortKey.PortName → *string

		for _, conflict := range conflicts {
			val := strconv.Itoa(conflict.RequestedPort)
			ptr := &val
			key := fmt.Sprintf("%s/%s", conflict.Service, conflict.PortName)
			portBindings[key] = ptr

			title := fmt.Sprintf("Port for %s.%s (currently used by %s)",
				conflict.Service, conflict.PortName, conflict.OccupiedBy)

			field := huh.NewInput().
				Title(title).
				Value(ptr).
				Validate(buildPortValidator()).
				// Enable ShowSuggestions so huh.Input.KeyBinds() exposes the
				// AcceptSuggestion binding, which we hijack below to show
				// "esc cancel" in the help line. The func returns nil so no
				// actual suggestions are presented to the user.
				SuggestionsFunc(func() []string { return []string{" "} }, nil)

			huhFields = append(huhFields, field)
		}

		keymap := huh.NewDefaultKeyMap()
		keymap.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "cancel"))
		// Hijack AcceptSuggestion's help slot to surface ESC in the bottom
		// help line. The actual key press is caught by the form-level Quit
		// handler before the field sees it.
		keymap.Input.AcceptSuggestion = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))

		form := huh.NewForm(
			huh.NewGroup(huhFields...).
				Title("Port Overrides"),
		).
			WithTheme(styles.Theme()).
			WithKeyMap(keymap).
			WithShowHelp(true).
			WithOutput(out)

		err := widgets.RunWithPromptHooks(func() error {
			return form.Run()
		})

		if err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return nil, ErrWizardCanceled
			}
			return nil, fmt.Errorf("wizard: %w", err)
		}

		// Coerce port strings to ints.
		portRaws := make(map[string]string)
		for _, conflict := range conflicts {
			key := fmt.Sprintf("%s/%s", conflict.Service, conflict.PortName)
			if ptr, ok := portBindings[key]; ok {
				portRaws[key] = *ptr
			}
		}

		coercedPorts, err := coercePortOverrides(conflicts, portRaws)
		if err != nil {
			return nil, err
		}

		return coercedPorts, nil
	}

	askServiceToggles = func(ctx context.Context, toggles []ServiceToggle) (map[string]bool, error) {
		if len(toggles) == 0 {
			return map[string]bool{}, nil
		}

		// NOTE: This prompt remains a direct huh.NewForm call (not migrated to ask.Run)
		// because it uses custom keymaps (hijacking the Filter binding for "esc cancel")
		// and also sets Filterable(false) to prevent user filtering. The ask.Field API
		// does not support these per-field customizations. Additionally, it pre-prints
		// the "Always on:" mandatory services line before the form opens, which is a
		// prompt-specific affordance not expressible via ask.Field.

		// Build a huh multi-select. Mandatory rows are shown but locked: huh
		// has no native "disabled option" concept, so we leave them out of
		// the form and surface them above the prompt as an "Always on" line.
		// The returned map includes BOTH the user's optional picks AND every
		// mandatory service, so the caller can write a complete picture.
		var optionLines []huh.Option[string]
		var initial []string
		var mandatoryLabels []string
		nameToToggle := make(map[string]ServiceToggle, len(toggles))
		for _, t := range toggles {
			nameToToggle[t.Name] = t
			if t.Mandatory {
				mandatoryLabels = append(mandatoryLabels, formatServiceToggleRow(t))
				continue
			}
			optionLines = append(optionLines, huh.NewOption(formatServiceToggleRow(t), t.Name))
			if t.Enabled {
				initial = append(initial, t.Name)
			}
		}

		if len(optionLines) == 0 {
			// All mandatory — nothing to select. Still report mandatory as kept.
			result := make(map[string]bool, len(toggles))
			for _, t := range toggles {
				if t.Mandatory {
					result[t.Name] = true
				}
			}
			return result, nil
		}

		if len(mandatoryLabels) > 0 {
			_, _ = fmt.Fprintln(out, styles.StyleSubheader("Always on: ")+styles.StyleMuted(strings.Join(mandatoryLabels, ", ")))
		}

		picked := initial
		field := huh.NewMultiSelect[string]().
			Title("Services").
			Options(applyMultiSelectInitial(optionLines, initial)...).
			Value(&picked).
			Filterable(false)

		keymap := huh.NewDefaultKeyMap()
		keymap.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "cancel"))
		keymap.MultiSelect.Filter = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))

		form := huh.NewForm(huh.NewGroup(field).Title("Services")).
			WithTheme(styles.Theme()).
			WithKeyMap(keymap).
			WithShowHelp(true).
			WithOutput(out)

		err := widgets.RunWithPromptHooks(func() error { return form.Run() })
		if err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return nil, ErrWizardCanceled
			}
			return nil, fmt.Errorf("wizard: %w", err)
		}

		result := make(map[string]bool, len(toggles))
		for _, name := range picked {
			result[name] = true
		}
		// Mandatory services are always kept.
		for _, t := range toggles {
			if t.Mandatory {
				result[t.Name] = true
			}
		}
		return result, nil
	}

	return askQuestions, askPortOverrides, askServiceToggles
}

// formatServiceToggleRow renders a single line for the wizard's service
// multi-select: name colored by type, type badge, container in muted color —
// mirroring the look of `dwe services`.
func formatServiceToggleRow(t ServiceToggle) string {
	typeText := t.Type
	if typeText == "" {
		typeText = "-"
	}
	container := t.Container
	if container == "" {
		container = "-"
	}
	return styles.IconPrefix(t.Icon) + styles.StyleServiceOptionName(t.Type, t.Name) + "  " +
		styles.StyleServiceOptionType(t.Type, "["+typeText+"]") + " " +
		styles.StyleServiceOptionContainer(container)
}

// applyMultiSelectInitial marks the given option values as pre-selected so the
// huh.MultiSelect form opens with them already checked.
func applyMultiSelectInitial(opts []huh.Option[string], initial []string) []huh.Option[string] {
	if len(initial) == 0 {
		return opts
	}
	set := make(map[string]bool, len(initial))
	for _, k := range initial {
		set[k] = true
	}
	for i, o := range opts {
		if set[o.Value] {
			opts[i] = o.Selected(true)
		}
	}
	return opts
}

// buildInputValidator returns a validator function for input fields that applies the question's validation.
func buildInputValidator(q Question) func(string) error {
	return func(s string) error {
		// Check required constraint.
		if q.Required && strings.TrimSpace(s) == "" {
			return fmt.Errorf("required")
		}

		// If empty and not required, pass.
		if s == "" {
			return nil
		}

		// Apply preset or regex validation.
		if q.Validate != nil {
			if q.Validate.Preset != "" {
				// Try to validate via ValidateAndCoerce.
				_, err := ValidateAndCoerce(q, s)
				if err != nil {
					return err
				}
			} else if q.Validate.Regex != "" {
				_, err := ValidateAndCoerce(q, s)
				if err != nil {
					return err
				}
			}
		}

		return nil
	}
}

// buildPortValidator returns a validator for port override inputs. The
// validator parses + range-checks the value, then probes whether the chosen
// port is currently free on localhost. A still-occupied port is rejected
// inline so the user has to pick a different one before the form submits.
func buildPortValidator() func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("port required")
		}

		port, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("must be a number")
		}

		if port < minPort || port > maxPort {
			return fmt.Errorf("port out of range (1..65535)")
		}

		if !env.IsPortAvailable(port) {
			return fmt.Errorf("port %d is still in use; pick another", port)
		}

		return nil
	}
}

// coerceInputAnswers converts raw string answers from huh inputs to their properly typed values.
// It handles port presets and regex validation, returning typed answers (int for port, string for others).
func coerceInputAnswers(questions []Question, raws map[string]string) (map[string]any, error) {
	result := make(map[string]any)

	for _, q := range questions {
		if q.Type != TypeInput {
			continue // Only process input questions here.
		}

		raw, ok := raws[q.ID]
		if !ok {
			// Question not in raws (unexpected, but handle gracefully).
			raw = ""
		}

		// Optional questions with a blank answer are omitted from the result so that
		// validateAnswers skips them (missing optional → no-op) and BuildOverlay
		// writes nothing to the overlay (preserving any existing local.yml value).
		if !q.Required && strings.TrimSpace(raw) == "" {
			continue
		}

		// Use ValidateAndCoerce to get the typed value.
		typed, err := ValidateAndCoerce(q, raw)
		if err != nil {
			return nil, fmt.Errorf("question %q: %w", q.ID, err)
		}

		result[q.ID] = typed
	}

	return result, nil
}

// coercePortOverrides converts raw port strings to their integer values.
// Validates that each port is in range 1..65535.
func coercePortOverrides(conflicts []env.PortConflict, raws map[string]string) (map[PortKey]int, error) {
	result := make(map[PortKey]int)

	for _, conflict := range conflicts {
		key := fmt.Sprintf("%s/%s", conflict.Service, conflict.PortName)
		raw, ok := raws[key]
		if !ok {
			// This shouldn't happen if form binding was correct.
			continue
		}

		port, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("port %s/%s: must be a number", conflict.Service, conflict.PortName)
		}

		if port < minPort || port > maxPort {
			return nil, fmt.Errorf("port %s/%s: %d out of range (1..65535)", conflict.Service, conflict.PortName, port)
		}

		result[PortKey{
			Service:  conflict.Service,
			PortName: conflict.PortName,
		}] = port
	}

	return result, nil
}
