package pipeline

import (
	"fmt"
	"io"
	"os"

	"devbox-cli/internal/render"

	"github.com/charmbracelet/x/term"
)

// UIMode controls which reporter implementation is used for pipeline output.
type UIMode string

const (
	// UIModeAuto uses TUI if the terminal is capable, else falls back to plain.
	UIModeAuto UIMode = "auto"
	// UIModePlain always uses PlainReporter.
	UIModePlain UIMode = "plain"
	// UIModeTUI uses TUI if the terminal is capable, else warns and falls back to plain.
	UIModeTUI UIMode = "tui"
)

// ParseUIMode parses s as a UIMode. Returns an error if s is not one of auto, plain, or tui.
func ParseUIMode(s string) (UIMode, error) {
	switch UIMode(s) {
	case UIModeAuto, UIModePlain, UIModeTUI:
		return UIMode(s), nil
	default:
		return "", fmt.Errorf("invalid --ui mode %q: must be auto, plain, or tui", s)
	}
}

// IsCapableTTY reports whether the current terminal can support TUI output.
// Returns false if any of: stdout/stderr/stdin are not TTYs, TERM=dumb, or
// common CI environment variables are set (CI, GITHUB_ACTIONS, JENKINS_URL,
// BUILDKITE, GITLAB_CI).
func IsCapableTTY() bool {
	return isCapableTTYWith(term.IsTerminal, os.Getenv)
}

// isCapableTTYWith is the testable implementation of IsCapableTTY.
// isTTY checks whether a file descriptor is a terminal.
// getenv retrieves an environment variable value.
func isCapableTTYWith(isTTY func(fd uintptr) bool, getenv func(string) string) bool {
	if !isTTY(os.Stdin.Fd()) || !isTTY(os.Stdout.Fd()) || !isTTY(os.Stderr.Fd()) {
		return false
	}
	if getenv("TERM") == "dumb" {
		return false
	}
	for _, v := range []string{"CI", "GITHUB_ACTIONS", "JENKINS_URL", "BUILDKITE", "GITLAB_CI"} {
		if getenv(v) != "" {
			return false
		}
	}
	return true
}

// NewReporter creates a Reporter appropriate for mode.
//   - plain: always PlainReporter
//   - auto:  TUIReporter if terminal is capable, else PlainReporter (silent fallback)
//   - tui:   TUIReporter if terminal is capable, else PlainReporter (warns if not capable)
//
// logWriter, if non-nil, receives plain-text lifecycle events from TUIReporter
// so log files match PlainReporter format without escape sequences.
func NewReporter(mode UIMode, w *render.Writer, logWriter io.Writer) Reporter {
	switch mode {
	case UIModeTUI:
		if !IsCapableTTY() {
			w.Warning("TUI mode requested but terminal is not capable; falling back to plain output")
			return NewPlainReporter(w)
		}
		return NewTUIReporter(logWriter)
	case UIModeAuto:
		if IsCapableTTY() {
			return NewTUIReporter(logWriter)
		}
		return NewPlainReporter(w)
	default: // UIModePlain
		return NewPlainReporter(w)
	}
}
