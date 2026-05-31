// Package completion hosts the install / uninstall subcommands attached under
// cobra's built-in `completion` command, plus shared shell-name helpers.
package completion

import (
	"errors"
	"slices"

	"github.com/semsemyonoff/devbox/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

// ErrUnsupportedShell is returned when a shell name is not one of the four
// supported values: bash, zsh, fish, powershell.
var ErrUnsupportedShell = errors.New("unsupported shell")

// completionExitError carries a fixed exit code for main.go's ExitCode() dispatch.
type completionExitError struct {
	msg  string
	code int
}

func (e *completionExitError) Error() string { return e.msg }
func (e *completionExitError) ExitCode() int { return e.code }

// supportedShells is the canonical list accepted by install/uninstall.
var supportedShells = []string{"bash", "zsh", "fish", "powershell"}

func isSupportedShell(s string) bool {
	return slices.Contains(supportedShells, s)
}

// AttachInstallUninstall wires the install + uninstall subcommands under the
// parent completion command (cobra's built-in, returned by InitDefaultCompletionCmd).
func AttachInstallUninstall(parent *cobra.Command, _ *cmdctx.RootFlags) {
	parent.AddCommand(newInstallCompletionCmd())
	parent.AddCommand(newUninstallCompletionCmd())
}
