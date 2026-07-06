// Package test implements the `dwe test` command tree: running declarative
// integration-test scenarios (workspace/tests/*.yml) against a disposable
// copy of the project, and listing available scenarios. The heavy lifting
// (copy, isolation, teardown, subprocess orchestration) lives in
// internal/core/workflow/envtest; this package is the thin CLI composition
// root, following the same NewCmd(groupID, flags) pattern as every other
// command subtree.
package test

import (
	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

// NewCmd builds the `dwe test` command tree (run / list).
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		GroupID: groupID,
		Use:     "test",
		Short:   "Run integration test scenarios",
		Long: `Run declarative integration-test scenarios defined under workspace/tests/*.yml.

Each scenario runs against a disposable, isolated copy of the project: the copy
gets its own compose project, generated local.yml, and (when needed) freshly
allocated host ports, then goes through 'dwe validate' and 'dwe deploy run'
before the scenario's own steps execute. The copy and every Docker/bridge
resource it created are torn down afterwards unless --keep is passed.`,
		Example: `  dwe test run
  dwe test run redis-off
  dwe test run --keep smoke
  dwe test list`,
		SilenceUsage: true,
		Args:         cobra.NoArgs,
	}
	cmd.AddCommand(newTestRunCmd(flags))
	cmd.AddCommand(newTestListCmd(flags))
	return cmd
}
