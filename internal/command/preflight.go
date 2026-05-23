package command

import (
	"devbox-cli/internal/preflight"

	"github.com/spf13/cobra"
)

// preflightRun is a package-level variable so tests can stub preflight
// (deploy invokes it directly, not via lifecycle.PreflightFunc).
var preflightRun = preflight.Run

// addSkipPreflightFlag registers a local --skip-preflight flag on a lifecycle
// command. It is intentionally NOT on rootFlags — it would be meaningless on
// validate, status, docs, etc.
func addSkipPreflightFlag(cmd *cobra.Command, target *bool) {
	cmd.Flags().BoolVar(target, "skip-preflight", false,
		"bypass environment probes and project checks before running")
}
