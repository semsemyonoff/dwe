package runtime

import (
	"errors"
	"fmt"
	"os"

	"devbox-cli/internal/core/ui"
	"devbox-cli/internal/shared/i18n"
	"devbox-cli/internal/shared/render"
	"devbox-cli/internal/shared/tpl"
)

// commandAbortedError is returned when the user explicitly declines a
// confirmation prompt. Notifications are suppressed for this error —
// cancellation is intentional, not a failure. ExitCode returns 0 so
// fang suppresses the "Error:" line.
type commandAbortedError struct{}

func (e *commandAbortedError) Error() string { return "aborted by user" }
func (e *commandAbortedError) ExitCode() int { return 0 }

// runConfirm is the package-level wrapper for ui.RunConfirm; swappable in tests.
var runConfirm = ui.RunConfirm

// ConfirmCommand prompts before running confirmation-enabled commands.
func ConfirmCommand(ctx RunContext) error {
	if ctx.Cmd == nil || !ctx.Cmd.Confirmation {
		return nil
	}

	if ctx.SkipConfirm || ctx.NonInteractive {
		return nil
	}

	if ctx.UnderParallel {
		return fmt.Errorf("%w: command %q requires confirmation", ErrConfirmInsideParallel, ctx.Cmd.ID)
	}

	// Ensure Translator is never nil for downstream use.
	if ctx.Translator == nil {
		ctx.Translator = i18n.NopTranslator{}
	}

	message := ctx.Translator.CommandConfirmationText(ctx.Locale, ctx.Cmd.ID, ctx.Cmd.EffectiveConfirmationText())
	if ctx.Render != nil {
		rendered, err := tpl.RenderCommand(message, ctx.Render)
		if err != nil {
			return fmt.Errorf("render confirmation_text: %w", err)
		}
		message = rendered
	}

	stdin := ctx.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}

	if ui.IsInteractiveFn(stdin) {
		confirmed, err := runConfirm(message, "Yes", "No")
		if err != nil {
			if errors.Is(err, ui.ErrCancelled) {
				return &commandAbortedError{}
			}
			return err
		}
		if !confirmed {
			return &commandAbortedError{}
		}
		return nil
	}

	if render.NewWriter(stdout(ctx)).Confirm(message, stdin) {
		return nil
	}
	return &commandAbortedError{}
}
