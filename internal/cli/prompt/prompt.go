// Package prompt provides the `dwe prompt` command.
package prompt

import (
	pipeline "github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	promptpkg "github.com/semsemyonoff/dwe/internal/shared/prompt"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

// NewCmd builds the `dwe prompt` command.
func NewCmd(groupID string, _ *cmdctx.RootFlags) *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "prompt",
		Short: "Print a compact shell-prompt segment for the current project",
		Long: `Print a compact, prompt-ready segment describing the current dwe project.

Designed for shell-prompt integration (e.g. starship). The output is a single
line of the form:

    {▪} <project> [<service>] <deploy-icon> <stack-icon>

Segments:
  - {▪}            DWE logomark, colored with the project's accent token.
  - <project>      project.name from workspace.yml.
  - [<service>]    optional: present only when cwd is under
                   workspace/services/<name>/...; the surrounding brackets are
                   plain, the inner name is sanitized.
  - <deploy-icon>  optional: ✓/⟳/⚠/✗ reflecting the deploy-state journal at
                   .dwe/deploy/state.yml (success/pending/partial/failed).
  - <stack-icon>   optional: ●/◐/○ reflecting live container state
                   (running/partial/stopped). Backed by a stale-while-revalidate
                   cache at .dwe/prompt-cache.yml (TTL 2 min); on stale cache
                   prompt shells out to 'docker ps' with a 150 ms hard timeout.
                   Authoritative writers (lifecycle commands, dwe status) keep
                   the cache accurate; the prompt itself never downgrades a
                   cached "running" to "stopped".

Only the '{▪} <project>' prefix is a stability guarantee — every other segment
is optional and may be absent.

The hot path bypasses cobra and dispatches directly from main; this command
exists primarily for --help discoverability and shell completion. Exits 0
inside a dwe project and 1 outside (or on any silent failure).`,
		Example: "  dwe prompt\n  dwe prompt --check",
		Args:    cobra.NoArgs,
		GroupID: groupID,
		Hidden:  true,
		// Prompt output is consumed by shells — never let cobra print usage or
		// error banners that would corrupt the rendered prompt line.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var passthrough []string
			if check {
				passthrough = []string{"--check"}
			}
			if promptpkg.Run(cmd.OutOrStdout(), passthrough) != 0 {
				return pipeline.ErrSilent
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "exit-only mode for shell when-predicates; no output")
	return cmd
}
