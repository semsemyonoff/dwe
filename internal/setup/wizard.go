package setup

import (
	"context"
	"fmt"

	"devbox-cli/internal/localconfig"
	"devbox-cli/internal/validate/env"
)

// WizardDeps contains all required collaborators for the wizard executor.
// The caller loads questions and port conflicts externally and passes them in.
type WizardDeps struct {
	BaseDir string
	LocalPath string
	Questions []Question
	PortConflicts []env.PortConflict
	// AskQuestions is called to collect answers for all questions.
	// Returns a map of question ID to answer (typed per question type).
	// If the user cancels, returns ErrWizardCanceled.
	AskQuestions func(ctx context.Context, questions []Question) (map[string]any, error)
	// AskPortOverrides is called to collect port override choices.
	// Returns a map of PortKey to chosen port number.
	// If the user cancels, returns ErrWizardCanceled.
	AskPortOverrides func(ctx context.Context, conflicts []env.PortConflict) (map[PortKey]int, error)
}

// Run executes the wizard flow: probe port conflicts, ask questions, validate answers,
// merge overlays, and write local.yml atomically.
// On user cancel (Ctrl-C / Esc), returns ErrWizardCanceled and leaves local.yml untouched.
// On validation error or write error, returns a wrapped error and leaves local.yml untouched.
func Run(ctx context.Context, deps WizardDeps) error {
	var portOverrides map[PortKey]int
	var answers map[string]any

	// If there are port conflicts, ask the user to override them.
	if len(deps.PortConflicts) > 0 {
		var err error
		portOverrides, err = deps.AskPortOverrides(ctx, deps.PortConflicts)
		if err != nil {
			if isErrWizardCanceled(err) {
				return ErrWizardCanceled
			}
			return fmt.Errorf("ask port overrides: %w", err)
		}
	}

	// If there are questions, ask the user to answer them.
	if len(deps.Questions) > 0 {
		var err error
		answers, err = deps.AskQuestions(ctx, deps.Questions)
		if err != nil {
			if isErrWizardCanceled(err) {
				return ErrWizardCanceled
			}
			return fmt.Errorf("ask questions: %w", err)
		}
	}

	// Validate and enforce required answers, type assertions, and semantic constraints.
	if err := validateAnswers(deps.Questions, answers); err != nil {
		return fmt.Errorf("validate answers: %w", err)
	}

	// Build overlays from answers and port overrides.
	qOverlay, err := BuildOverlay(deps.Questions, answers)
	if err != nil {
		return fmt.Errorf("build question overlay: %w", err)
	}

	pOverlay, err := BuildPortOverlay(portOverrides)
	if err != nil {
		return fmt.Errorf("build port overlay: %w", err)
	}

	// Load existing local.yml (if any).
	existing, err := localconfig.LoadLocalYAML(deps.LocalPath)
	if err != nil {
		return fmt.Errorf("read existing local.yml: %w", err)
	}

	// Merge overlays sequentially.
	merged, err := MergeIntoLocal(existing, qOverlay)
	if err != nil {
		return fmt.Errorf("merge question overlay: %w", err)
	}

	merged, err = MergeIntoLocal(merged, pOverlay)
	if err != nil {
		return fmt.Errorf("merge port overlay: %w", err)
	}

	// Write atomically.
	if err := localconfig.WriteLocalYAML(deps.LocalPath, merged); err != nil {
		return fmt.Errorf("write local.yml: %w", err)
	}

	return nil
}

// validateAnswers enforces type assertions, semantic re-validation for input types,
// and required-answer checks.
func validateAnswers(questions []Question, answers map[string]any) error {
	if answers == nil {
		answers = make(map[string]any)
	}

	for _, q := range questions {
		answer, exists := answers[q.ID]
		if !exists {
			// Question has no answer. Check if it's required.
			if q.Required {
				return fmt.Errorf("question %q (required): no answer provided", q.ID)
			}
			continue
		}

		// Type assertions and semantic validation per question type.
		if err := validateAnswerForQuestion(q, answer); err != nil {
			return err
		}
	}

	return nil
}

// validateAnswerForQuestion validates that the answer for a given question
// matches the expected type and satisfies semantic constraints.
func validateAnswerForQuestion(q Question, answer any) error {
	switch q.Type {
	case TypeConfirm:
		// confirm must produce a bool.
		if _, ok := answer.(bool); !ok {
			return fmt.Errorf("question %q (type: confirm): expected bool, got %T", q.ID, answer)
		}
		// confirm answers are never treated as "empty"; Required is a no-op.
		return nil

	case TypeInput:
		// input must produce a string (or int for port preset).
		// First, handle port preset specially.
		if q.Validate != nil && q.Validate.Preset == PresetPort {
			// Port input must return int.
			port, ok := answer.(int)
			if !ok {
				return fmt.Errorf("question %q (type: input, preset: port): expected int, got %T", q.ID, answer)
			}
			// Defensive range check (should already have been validated by coercion).
			if port < 1 || port > 65535 {
				return fmt.Errorf("question %q (type: input, preset: port): port %d out of range (1..65535)", q.ID, port)
			}
			// No further re-validation for port; the coercion pass already enforced it.
			return nil
		}

		// For other input types, expect string.
		str, ok := answer.(string)
		if !ok {
			return fmt.Errorf("question %q (type: input): expected string, got %T", q.ID, answer)
		}

		// Semantic re-validation for presets and regex.
		if q.Validate != nil {
			if q.Validate.Preset != "" {
				// Re-run the preset coercion to validate.
				_, err := ValidateAndCoerce(q, str)
				if err != nil {
					return fmt.Errorf("question %q (type: input, preset: %s): %w", q.ID, q.Validate.Preset, err)
				}
			} else if q.Validate.Regex != "" {
				// Re-run regex validation.
				_, err := ValidateAndCoerce(q, str)
				if err != nil {
					return fmt.Errorf("question %q (type: input): %w", q.ID, err)
				}
			}
		}

		// Check required constraint for string inputs.
		if q.Required && str == "" {
			return fmt.Errorf("question %q (type: input, required): value cannot be empty", q.ID)
		}

		return nil

	case TypeSelect:
		// select must produce a string matching one of the declared options.
		str, ok := answer.(string)
		if !ok {
			return fmt.Errorf("question %q (type: select): expected string, got %T", q.ID, answer)
		}

		// Check that the value is in the options.
		validOption := false
		for _, opt := range q.Options {
			if opt.Value == str {
				validOption = true
				break
			}
		}
		if !validOption {
			return fmt.Errorf("question %q (type: select): value %q not in declared options", q.ID, str)
		}

		// Check required constraint.
		if q.Required && str == "" {
			return fmt.Errorf("question %q (type: select, required): value cannot be empty", q.ID)
		}

		return nil

	case TypeMultiselect:
		// multiselect must produce []string with all values in declared options.
		strs, ok := answer.([]string)
		if !ok {
			return fmt.Errorf("question %q (type: multiselect): expected []string, got %T", q.ID, answer)
		}

		// Check that all values are in the options.
		optionValues := make(map[string]bool)
		for _, opt := range q.Options {
			optionValues[opt.Value] = true
		}
		for _, s := range strs {
			if !optionValues[s] {
				return fmt.Errorf("question %q (type: multiselect): value %q not in declared options", q.ID, s)
			}
		}

		// Check required constraint (non-empty slice).
		if q.Required && len(strs) == 0 {
			return fmt.Errorf("question %q (type: multiselect, required): must select at least one option", q.ID)
		}

		return nil

	default:
		// Unknown question type should have been caught by validators.
		// Be defensive and accept the answer as-is.
		return nil
	}
}

// isErrWizardCanceled checks if an error is ErrWizardCanceled.
func isErrWizardCanceled(err error) bool {
	return err != nil && err.Error() == ErrWizardCanceled.Error()
}
