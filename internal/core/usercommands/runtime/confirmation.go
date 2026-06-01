package runtime

import (
	"errors"
	"fmt"
	"os"

	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/internal/runio"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
	"github.com/semsemyonoff/dwe/internal/shared/render"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// commandAbortedError is returned when the user explicitly declines a
// confirmation prompt. Notifications are suppressed for this error —
// cancellation is intentional, not a failure. ExitCode returns 0 so
// fang suppresses the "Error:" line.
type commandAbortedError struct{}

func (e *commandAbortedError) Error() string { return "aborted by user" }
func (e *commandAbortedError) ExitCode() int { return 0 }

// runConfirm is the package-level wrapper for widgets.RunConfirm; swappable in tests.
var runConfirm = widgets.RunConfirm

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

	if widgets.IsInteractiveFn(stdin) {
		confirmed, err := runConfirm(message, "Yes", "No")
		if err != nil {
			if errors.Is(err, widgets.ErrCancelled) {
				return &commandAbortedError{}
			}
			return err
		}
		if !confirmed {
			return &commandAbortedError{}
		}
		return nil
	}

	if render.NewWriter(runio.StdoutOf(ctx)).Confirm(message, stdin) {
		return nil
	}
	return &commandAbortedError{}
}
