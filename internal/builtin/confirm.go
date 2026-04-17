package builtin

import (
	"fmt"
	"os"
)

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

	// TUI mode: delegate to the native Bubble Tea confirmation prompt.
	// Visual feedback is handled by the TUI model; suppress ctx.Output writes.
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

	// Plain/stdin fallback.
	if ctx.Output.Confirm(msg, os.Stdin) {
		ctx.Output.Success(okMsg)
		return nil
	}
	ctx.Output.Error(stopMsg)
	return fmt.Errorf("aborted by user")
}
