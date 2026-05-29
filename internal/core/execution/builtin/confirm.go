package builtin

import (
	"context"
	"errors"
	"fmt"
	"os"

	"devbox-cli/internal/core/execution/builtin/spec"

	"devbox-cli/internal/core/ui"
)

// runConfirm is the package-level wrapper for ui.RunConfirm; swappable in tests.
var runConfirm = ui.RunConfirm

type confirmBuiltin struct{}

func (confirmBuiltin) Validate(with map[string]any) error {
	return nil
}

func (confirmBuiltin) Describe(with map[string]any) string {
	msg := spec.GetStringParam(with, "message", "Are you sure?")
	return fmt.Sprintf("builtin: confirm(message=%q)", msg)
}

func (confirmBuiltin) Run(_ context.Context, with map[string]any, ectx spec.ExecContext) error {
	if ectx.SkipConfirm {
		return nil
	}
	msg := spec.GetStringParam(with, "message", "Are you sure?")
	okMsg := spec.GetStringParam(with, "ok_msg", "Continuing")
	stopMsg := spec.GetStringParam(with, "stop_msg", "Aborted")

	// Injected confirmation callback (e.g. in tests); suppress ectx.Output writes.
	if ectx.ConfirmFunc != nil {
		confirmed, err := ectx.ConfirmFunc(msg, okMsg, stopMsg)
		if err != nil {
			return err
		}
		if !confirmed {
			return fmt.Errorf("aborted by user")
		}
		return nil
	}

	stdin := ectx.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}

	// Interactive TTY path: use huh.Confirm.
	if ui.IsInteractiveFn(stdin) {
		confirmed, err := runConfirm(msg, okMsg, stopMsg)
		if err != nil {
			if errors.Is(err, ui.ErrCancelled) {
				ectx.Output.Error(stopMsg)
				return fmt.Errorf("aborted by user")
			}
			return err
		}
		if !confirmed {
			ectx.Output.Error(stopMsg)
			return fmt.Errorf("aborted by user")
		}
		ectx.Output.Success(okMsg)
		return nil
	}

	// Non-TTY fallback: plain stdin Y/n.
	if ectx.Output.Confirm(msg, stdin) {
		ectx.Output.Success(okMsg)
		return nil
	}
	ectx.Output.Error(stopMsg)
	return fmt.Errorf("aborted by user")
}
