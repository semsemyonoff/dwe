//go:build windows

package mermaid

import (
	"os/exec"
)

// configureCommand is a no-op on Windows.
// Windows relies on exec.CommandContext's default kill behavior.
// Note: mmdc's chrome subprocess may survive on timeout (acceptable trade-off).
func configureCommand(cmd *exec.Cmd) {
	// Nothing to do.
}
