package vars

import (
	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/varsusage"
	uirender "github.com/semsemyonoff/dwe/internal/core/ui/render"

	"github.com/spf13/cobra"
)

// varGetJSON is the JSON shape for `dwe vars get --output json`.
type varGetJSON struct {
	Var   string `json:"var"`
	Value any    `json:"value"`
}

func newVarsGetCmd(flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <var>",
		Short: "Print a var's effective value",
		Long: `Print the effective value of a single var (what ${vars.x} resolves to).

A leaf path prints the scalar; a namespace path (e.g. vars.db) prints the
subtree as YAML.`,
		Example: `  dwe vars get vars.db.host
  dwe vars get vars.db
  dwe vars get vars.db.host --output json`,
		Args:              cobra.ExactArgs(1),
		SilenceUsage:      true,
		ValidArgsFunction: leafCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			// Confine reads to the vars.* sandbox: a non-vars path is reported as
			// not-found rather than resolved against the full project config (which
			// `vars` reachability from a container would otherwise leak).
			if !isVarsPath(path) {
				return notFoundError(path)
			}
			cfg, err := loadConfigForVars(flags)
			if err != nil {
				return err
			}
			value, ok := varsusage.ResolveVar(cfg, path)
			if !ok {
				return notFoundError(path)
			}
			rendered, rerr := uirender.VarValue(value)
			if rerr != nil && flags.Output != "json" {
				// Composite marshal failure is vanishingly unlikely for a
				// yaml-decoded value; surface it rather than print nothing.
				return cmdctx.ErrWrap("internal_error", rerr)
			}
			data := varGetJSON{Var: path, Value: value}
			return cmdctx.WriteData(flags, cmd, data, func(varGetJSON) string {
				return rendered
			})
		},
	}
	return cmd
}
