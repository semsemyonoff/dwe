package runtime

import (
	"errors"
	"fmt"
	"os"

	"devbox-cli/internal/render"
	"devbox-cli/internal/tpl"
	"devbox-cli/internal/ui"
)

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

	message := ctx.Cmd.EffectiveConfirmationText()
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
				return fmt.Errorf("aborted by user")
			}
			return err
		}
		if !confirmed {
			return fmt.Errorf("aborted by user")
		}
		return nil
	}

	if render.NewWriter(stdout(ctx)).Confirm(message, stdin) {
		return nil
	}
	return fmt.Errorf("aborted by user")
}
