package mermaid

import (
	"os/exec"
	"runtime"
)

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
