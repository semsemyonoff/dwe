package interaction

import (
	"context"
	"fmt"

	"devbox-cli/internal/core/execution/builtin/spec"

	"devbox-cli/internal/shared/tpl"
)

// validMessageLevels lists the accepted level values for the message builtin.
var validMessageLevels = map[string]bool{
	"info":    true,
	"success": true,
	"warning": true,
	"error":   true,
}

// Message prints a styled line to the pipeline output writer.
type Message struct{}

// Validate ensures the required level and text params are present and valid.
func (Message) Validate(with map[string]any) error {
	level := spec.GetStringParam(with, "level", "")
	if level == "" {
		return fmt.Errorf("builtin message: missing required param 'level'")
	}
	if !validMessageLevels[level] {
		return fmt.Errorf("builtin message: invalid level %q (valid: info, success, warning, error)", level)
	}
	text := spec.GetStringParam(with, "text", "")
	if text == "" {
		return fmt.Errorf("builtin message: missing required param 'text'")
	}
	return nil
}

// Describe returns a plan-line summary of the message builtin invocation.
func (Message) Describe(with map[string]any) string {
	level := spec.GetStringParam(with, "level", "")
	text := spec.GetStringParam(with, "text", "")
	return fmt.Sprintf("builtin: message(level=%s, text=%s)", level, text)
}

// Run renders the text through the project template engine and writes it at the configured level.
func (Message) Run(_ context.Context, with map[string]any, ectx spec.ExecContext) error {
	level := spec.GetStringParam(with, "level", "")
	rawText := spec.GetStringParam(with, "text", "")

	text, err := tpl.Render(rawText, ectx.Config)
	if err != nil {
		return fmt.Errorf("builtin message: template error: %w", err)
	}

	switch level {
	case "info":
		ectx.Output.Info(text)
	case "success":
		ectx.Output.Success(text)
	case "warning":
		ectx.Output.Warning(text)
	case "error":
		ectx.Output.Error(text)
	default:
		return fmt.Errorf("builtin message: invalid level %q", level)
	}
	return nil
}
