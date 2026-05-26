package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"strconv"
	"strings"

	"devbox-cli/internal/ui"
	"devbox-cli/internal/validate/env"

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
) {
	askQuestions = func(ctx context.Context, questions []Question) (map[string]any, error) {
		if len(questions) == 0 {
			return nil, nil
		}

		// Prepare answer bindings for each question type.
		type answerBinding struct {
			id          string
			qType       string
			stringVal   *string
			stringSlice *[]string
			boolVal     *bool
		}

		var bindings []answerBinding
		var inputQuestions []Question
		var huhFields []huh.Field

		for _, q := range questions {
			switch q.Type {
			case TypeInput:
				// Input questions: bind to *string, will coerce later.
				inputQuestions = append(inputQuestions, q)
				val := ""
				ptr := &val

				field := huh.NewInput().
					Title(q.Title).
					Description(q.Description).
					Value(ptr).
					Validate(buildInputValidator(q))

				bindings = append(bindings, answerBinding{
					id:        q.ID,
					qType:     TypeInput,
					stringVal: ptr,
				})
				huhFields = append(huhFields, field)

			case TypeSelect:
				// Select: huh returns the value directly, no coercion needed.
				val := ""
				ptr := &val
				opts := make([]huh.Option[string], len(q.Options))
				for i, opt := range q.Options {
					opts[i] = huh.NewOption(opt.Label, opt.Value)
				}

				field := huh.NewSelect[string]().
					Title(q.Title).
					Description(q.Description).
					Options(opts...).
					Value(ptr)

				bindings = append(bindings, answerBinding{
					id:        q.ID,
					qType:     TypeSelect,
					stringVal: ptr,
				})
				huhFields = append(huhFields, field)

			case TypeMultiselect:
				// Multiselect: huh returns []string directly.
				val := []string{}
				ptr := &val
				opts := make([]huh.Option[string], len(q.Options))
				for i, opt := range q.Options {
					opts[i] = huh.NewOption(opt.Label, opt.Value)
				}

				field := huh.NewMultiSelect[string]().
					Title(q.Title).
					Description(q.Description).
					Options(opts...).
					Value(ptr)

				bindings = append(bindings, answerBinding{
					id:          q.ID,
					qType:       TypeMultiselect,
					stringSlice: ptr,
				})
				huhFields = append(huhFields, field)

			case TypeConfirm:
				// Confirm: huh returns bool directly.
				val := false
				ptr := &val

				field := huh.NewConfirm().
					Title(q.Title).
					Description(q.Description).
					Value(ptr)

				bindings = append(bindings, answerBinding{
					id:      q.ID,
					qType:   TypeConfirm,
					boolVal: ptr,
				})
				huhFields = append(huhFields, field)
			}
		}

		keymap := huh.NewDefaultKeyMap()
		keymap.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "quit"))

		form := huh.NewForm(huh.NewGroup(huhFields...)).
			WithTheme(ui.Theme()).
			WithKeyMap(keymap).
			WithShowHelp(true).
			WithOutput(out)

		err := ui.RunWithPromptHooks(func() error {
			return form.Run()
		})

		if err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return nil, ErrWizardCanceled
			}
			return nil, fmt.Errorf("wizard: %w", err)
		}

		// Coerce input answers to their proper types.
		inputRaws := make(map[string]string)
		for _, b := range bindings {
			if b.qType == TypeInput && b.stringVal != nil {
				inputRaws[b.id] = *b.stringVal
			}
		}

		coercedInputs, err := coerceInputAnswers(inputQuestions, inputRaws)
		if err != nil {
			return nil, err
		}

		// Assemble final answers map from all bindings.
		answers := make(map[string]any)

		// Add coerced input answers.
		maps.Copy(answers, coercedInputs)

		// Add select/multiselect/confirm answers from huh bindings.
		for _, b := range bindings {
			switch b.qType {
			case TypeSelect:
				if b.stringVal != nil {
					answers[b.id] = *b.stringVal
				}
			case TypeMultiselect:
				if b.stringSlice != nil {
					answers[b.id] = *b.stringSlice
				}
			case TypeConfirm:
				if b.boolVal != nil {
					answers[b.id] = *b.boolVal
				}
			}
		}

		return answers, nil
	}

	askPortOverrides = func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error) {
		if len(conflicts) == 0 {
			return nil, nil
		}

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
				Validate(buildPortValidator())

			huhFields = append(huhFields, field)
		}

		keymap := huh.NewDefaultKeyMap()
		keymap.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "quit"))

		form := huh.NewForm(huh.NewGroup(huhFields...).Title("Port Overrides")).
			WithTheme(ui.Theme()).
			WithKeyMap(keymap).
			WithShowHelp(true).
			WithOutput(out)

		err := ui.RunWithPromptHooks(func() error {
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

	return askQuestions, askPortOverrides
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

// buildPortValidator returns a validator for port override inputs.
func buildPortValidator() func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("port required")
		}

		port, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("must be a number")
		}

		if port < 1 || port > 65535 {
			return fmt.Errorf("port out of range (1..65535)")
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

		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("port %s/%s: %d out of range (1..65535)", conflict.Service, conflict.PortName, port)
		}

		result[PortKey{
			Service:  conflict.Service,
			PortName: conflict.PortName,
		}] = port
	}

	return result, nil
}
