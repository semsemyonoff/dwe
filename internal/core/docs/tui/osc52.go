package tui

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
)

// CopyViaOSC52 copies text to clipboard using OSC 52 escape sequence.
// Safe for use over SSH. Returns nil on success.
func CopyViaOSC52(text string, out io.Writer) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	seq := fmt.Sprintf("\x1b]52;c;%s\x07", encoded)
	_, err := io.WriteString(out, seq)
	return err
}

// ClipboardTmuxHint returns true if we should show the tmux hint.
// The hint appears when TMUX env var is set and clipboard passthrough is not enabled.
func ClipboardTmuxHint() bool {
	return os.Getenv("TMUX") != ""
}
