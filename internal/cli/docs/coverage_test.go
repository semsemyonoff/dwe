package docs

import (
	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

// buildDocsTestRoot constructs a minimal cobra root that wires the docs
// subtree without dragging the cli/ package into the docs/ package's import
// graph (which would create a cycle).
func buildDocsTestRoot(flags *cmdctx.RootFlags) *cobra.Command {
	root := &cobra.Command{Use: "dwe"}
	root.AddGroup(&cobra.Group{ID: "advanced", Title: "Advanced"})
	root.AddCommand(NewCmd("advanced", flags))
	return root
}
