package mermaid

import (
	"os"
	"os/exec"
	"runtime"

	"github.com/BourgeoisBear/rasterm"
)

// CanInline returns true if the terminal can display inline images (kitty graphics).
// Returns false under tmux (passthrough too unreliable).
// For other terminals, uses rasterm.IsKittyCapable().
// Note: this is a best-effort hint, not a guarantee.
func CanInline() bool {
	// tmux is unreliable; never inline in tmux.
	if os.Getenv("TMUX") != "" {
		return false
	}

	// Check for kitty-compatible terminals (kitty, ghostty, wezterm).
	return rasterm.IsKittyCapable()
}

// OpenSystem opens a file using the system's default viewer.
// On macOS: open
// On Linux/BSD: xdg-open
// On Windows: cmd /c start "" <path> (note: empty title is mandatory)
func OpenSystem(path string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default: // Linux, BSD, etc.
		cmd = exec.Command("xdg-open", path)
	}

	return cmd.Run()
}
