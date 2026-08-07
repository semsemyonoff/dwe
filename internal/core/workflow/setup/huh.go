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
)

// Form rendering tested manually; see plan Post-Completion. The pure
// coercion helpers (coerceInputAnswers, coercePortOverrides) are
// unit-tested; form construction routes through ask.Run/ask.Build, tested
// in internal/core/ui/ask.

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
			return nil, mapWizardCancel(err)
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

		result, err := ask.Run(ctx, "Port Overrides", buildPortOverrideFields(conflicts), ask.RunOptions{
			Output: out,
			Quit:   portOverridesQuit,
		})
		if err != nil {
			return nil, mapWizardCancel(err)
		}

		coercedPorts, err := coercePortOverrides(conflicts, portOverrideRaws(conflicts, result))
		if err != nil {
			return nil, err
		}

		return coercedPorts, nil
	}

	askServiceToggles = func(ctx context.Context, toggles []ServiceToggle) (map[string]bool, error) {
		if len(toggles) == 0 {
			return map[string]bool{}, nil
		}

		field, mandatoryLabels := buildServiceTogglesField(toggles)
		if field == nil {
			// All mandatory — nothing to select. Still report mandatory as kept.
			return mandatoryToggles(toggles), nil
		}

		if len(mandatoryLabels) > 0 {
			_, _ = fmt.Fprintln(out, styles.StyleSubheader("Always on: ")+styles.StyleMuted(strings.Join(mandatoryLabels, ", ")))
		}

		result, err := ask.Run(ctx, "Services", []ask.Field{*field}, ask.RunOptions{
			Output: out,
			Quit:   serviceTogglesQuit,
		})
		if err != nil {
			return nil, mapWizardCancel(err)
		}

		return mergeMandatoryToggles(result.Strings(serviceTogglesKey), toggles), nil
	}

	return askQuestions, askPortOverrides, askServiceToggles
}

// mapWizardCancel translates the ask cancel error into the wizard's own
// sentinel; any other error is wrapped.
func mapWizardCancel(err error) error {
	if errors.Is(err, widgets.ErrCancelled) {
		return ErrWizardCanceled
	}
	return fmt.Errorf("wizard: %w", err)
}

// portOverridesQuit is the declarative quit binding shared by the port
// override and service-toggle prompts (esc/ctrl+c cancel the wizard step).
var portOverridesQuit = &ask.QuitSpec{Keys: []string{"esc", "ctrl+c"}, Help: "cancel"}

// portOverrideKey is the ask.Field/Result key for one conflict: joins the
// service name and port name so keys stay unique across conflicts.
func portOverrideKey(conflict env.PortConflict) string {
	return fmt.Sprintf("%s/%s", conflict.Service, conflict.PortName)
}

// buildPortOverrideFields builds one ask.Field{Kind: FieldInput} per
// conflict, defaulted to the originally requested port and validated inline
// via buildPortValidator.
func buildPortOverrideFields(conflicts []env.PortConflict) []ask.Field {
	fields := make([]ask.Field, 0, len(conflicts))
	for _, conflict := range conflicts {
		fields = append(fields, ask.Field{
			Key:      portOverrideKey(conflict),
			Title:    fmt.Sprintf("Port for %s.%s (currently used by %s)", conflict.Service, conflict.PortName, conflict.OccupiedBy),
			Kind:     ask.FieldInput,
			Default:  strconv.Itoa(conflict.RequestedPort),
			Validate: buildPortValidator(),
		})
	}
	return fields
}

// portOverrideRaws harvests each conflict's raw string answer from a
// completed ask.Result, keyed for coercePortOverrides.
func portOverrideRaws(conflicts []env.PortConflict, result ask.Result) map[string]string {
	raws := make(map[string]string, len(conflicts))
	for _, conflict := range conflicts {
		raws[portOverrideKey(conflict)] = result.String(portOverrideKey(conflict))
	}
	return raws
}

// serviceTogglesKey is the ask.Field/Result key for the service-toggles
// multi-select.
const serviceTogglesKey = "services"

// serviceTogglesQuit is the declarative quit binding for the service-toggles
// prompt. The "esc cancel" hint does not render (Filterable: false hides the
// hijacked slot — see design decision 3 in the stage-6 plan); esc still
// cancels via the form-level Quit binding.
var serviceTogglesQuit = &ask.QuitSpec{Keys: []string{"esc", "ctrl+c"}, Help: "cancel"}

// buildServiceTogglesField splits toggles into the mandatory (always-on,
// shown above the prompt) and optional (selectable) sets, returning the
// ask.Field for the optional set and the mandatory rows' display labels. Nil
// field means every toggle is mandatory — nothing to select.
func buildServiceTogglesField(toggles []ServiceToggle) (field *ask.Field, mandatoryLabels []string) {
	var options []ask.Option
	var initial []string
	for _, t := range toggles {
		if t.Mandatory {
			mandatoryLabels = append(mandatoryLabels, formatServiceToggleRow(t))
			continue
		}
		options = append(options, ask.Option{Value: t.Name, Label: formatServiceToggleRow(t)})
		if t.Enabled {
			initial = append(initial, t.Name)
		}
	}

	if len(options) == 0 {
		return nil, mandatoryLabels
	}

	notFilterable := false
	return &ask.Field{
		Key:        serviceTogglesKey,
		Title:      "Services",
		Kind:       ask.FieldMultiselect,
		Options:    options,
		Defaults:   initial,
		Filterable: &notFilterable,
	}, mandatoryLabels
}

// mandatoryToggles returns a map with every mandatory toggle set to true and
// every optional toggle omitted — used when there is nothing left to select.
func mandatoryToggles(toggles []ServiceToggle) map[string]bool {
	result := make(map[string]bool, len(toggles))
	for _, t := range toggles {
		if t.Mandatory {
			result[t.Name] = true
		}
	}
	return result
}

// mergeMandatoryToggles combines the user's picked (optional) service names
// with every mandatory service, which is always kept regardless of the
// picker's outcome.
func mergeMandatoryToggles(picked []string, toggles []ServiceToggle) map[string]bool {
	result := make(map[string]bool, len(toggles))
	for _, name := range picked {
		result[name] = true
	}
	for _, t := range toggles {
		if t.Mandatory {
			result[t.Name] = true
		}
	}
	return result
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
