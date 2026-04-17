package pipeline

import (
	"fmt"
	"io"

	"devbox-cli/internal/render"
)

// UIMode controls which reporter implementation is used for pipeline output.
//
// Deprecated: Only PlainReporter is supported. UIMode and ParseUIMode are kept
// temporarily for backward compatibility with the --ui flag in deploy/reset
// commands and will be removed in a subsequent task.
type UIMode string

const (
	// UIModeAuto is kept for backward compatibility; always produces PlainReporter.
	UIModeAuto UIMode = "auto"
	// UIModePlain always uses PlainReporter.
	UIModePlain UIMode = "plain"
	// UIModeTUI is kept for backward compatibility; falls back to PlainReporter.
	UIModeTUI UIMode = "tui"
)

// ParseUIMode parses s as a UIMode. Returns an error if s is not one of auto, plain, or tui.
//
// Deprecated: The --ui flag is being removed. This function remains only while
// deploy/reset commands still accept the flag.
func ParseUIMode(s string) (UIMode, error) {
	switch UIMode(s) {
	case UIModeAuto, UIModePlain, UIModeTUI:
		return UIMode(s), nil
	default:
		return "", fmt.Errorf("invalid --ui mode %q: must be auto, plain, or tui", s)
	}
}

// NewReporter creates a PlainReporter. The mode and logWriter parameters are
// ignored; they are kept only for backward compatibility while the --ui flag
// is being removed from deploy/reset commands.
//
// Deprecated: Call pipeline.NewPlainReporter directly.
func NewReporter(_ UIMode, w *render.Writer, _ io.Writer) Reporter {
	return NewPlainReporter(w)
}
