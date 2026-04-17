package builtin

import (
	"fmt"

	"devbox-cli/internal/tpl"
)

// validMessageLevels lists the accepted level values for the message builtin.
var validMessageLevels = map[string]bool{
	"info":    true,
	"success": true,
	"warning": true,
	"error":   true,
}

type messageBuiltin struct{}

func (messageBuiltin) Validate(with map[string]any) error {
	level := getStringParam(with, "level", "")
	if level == "" {
		return fmt.Errorf("builtin message: missing required param 'level'")
	}
	if !validMessageLevels[level] {
		return fmt.Errorf("builtin message: invalid level %q (valid: info, success, warning, error)", level)
	}
	text := getStringParam(with, "text", "")
	if text == "" {
		return fmt.Errorf("builtin message: missing required param 'text'")
	}
	return nil
}

func (messageBuiltin) Describe(with map[string]any) string {
	level := getStringParam(with, "level", "")
	text := getStringParam(with, "text", "")
	return fmt.Sprintf("builtin: message(level=%s, text=%s)", level, text)
}

func (messageBuiltin) Run(with map[string]any, ctx ExecContext) error {
	level := getStringParam(with, "level", "")
	rawText := getStringParam(with, "text", "")

	text, err := tpl.Render(rawText, ctx.Config)
	if err != nil {
		return fmt.Errorf("builtin message: template error: %w", err)
	}

	switch level {
	case "info":
		ctx.Output.Info(text)
	case "success":
		ctx.Output.Success(text)
	case "warning":
		ctx.Output.Warning(text)
	case "error":
		ctx.Output.Error(text)
	default:
		return fmt.Errorf("builtin message: invalid level %q", level)
	}
	return nil
}
