package builtin

import (
	"errors"
	"fmt"
	"os"

	"devbox-cli/internal/ui"
)

// runConfirm is the package-level wrapper for ui.RunConfirm; swappable in tests.
var runConfirm = ui.RunConfirm

type confirmBuiltin struct{}

func (confirmBuiltin) Validate(with map[string]any) error {
	return nil
}

func (confirmBuiltin) Describe(with map[string]any) string {
	msg := getStringParam(with, "message", "Are you sure?")
	return fmt.Sprintf("builtin: confirm(message=%q)", msg)
}

func (confirmBuiltin) Run(with map[string]any, ctx ExecContext) error {
	if ctx.SkipConfirm {
		return nil
	}
	msg := getStringParam(with, "message", "Are you sure?")
	okMsg := getStringParam(with, "ok_msg", "Continuing")
	stopMsg := getStringParam(with, "stop_msg", "Aborted")

	// Injected confirmation callback (e.g. in tests); suppress ctx.Output writes.
	if ctx.ConfirmFunc != nil {
		confirmed, err := ctx.ConfirmFunc(msg, okMsg, stopMsg)
		if err != nil {
			return err
		}
		if !confirmed {
			return fmt.Errorf("aborted by user")
		}
		return nil
	}

	stdin := ctx.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}

	// Interactive TTY path: use huh.Confirm.
	if ui.IsInteractiveFn(stdin) {
		confirmed, err := runConfirm(msg, okMsg, stopMsg)
		if err != nil {
			if errors.Is(err, ui.ErrCancelled) {
				ctx.Output.Error(stopMsg)
				return fmt.Errorf("aborted by user")
			}
			return err
		}
		if !confirmed {
			ctx.Output.Error(stopMsg)
			return fmt.Errorf("aborted by user")
		}
		ctx.Output.Success(okMsg)
		return nil
	}

	// Non-TTY fallback: plain stdin Y/n.
	if ctx.Output.Confirm(msg, stdin) {
		ctx.Output.Success(okMsg)
		return nil
	}
	ctx.Output.Error(stopMsg)
	return fmt.Errorf("aborted by user")
}
