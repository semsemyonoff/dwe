package cmdctx

import (
	"fmt"

	"devbox-cli/internal/shared/render"

	"github.com/spf13/cobra"
)

// EmitDefaultNotice writes a single info line announcing that a built-in
// default pipeline was used. No-op when output is JSON.
func EmitDefaultNotice(cmd *cobra.Command, flags *RootFlags, pipeline string, file string) {
	if flags.Output == "json" {
		return
	}
	render.NewWriter(cmd.ErrOrStderr()).Info(
		fmt.Sprintf("Using built-in default %s pipeline (override with devbox/%s.yml).", pipeline, file),
	)
}
