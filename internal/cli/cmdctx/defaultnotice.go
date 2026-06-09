package cmdctx

import (
	"context"
	"fmt"

	"github.com/semsemyonoff/dwe/internal/shared/render"
	"github.com/semsemyonoff/dwe/internal/shared/trace"

	"github.com/spf13/cobra"
)

// EmitDefaultNotice writes a single info line announcing that a built-in
// default pipeline was used. No-op when output is JSON.
//
// The notice is also mirrored to the Debug firehose so the lifecycle-meta
// decision is visible even in JSON mode (where the user-facing line is
// suppressed): diagnostics go to stderr only and never touch the JSON stdout.
func EmitDefaultNotice(cmd *cobra.Command, flags *RootFlags, pipeline string, file string) {
	trace.Debugf(context.Background(), "lifecycle: using built-in default %s pipeline (no workspace/%s.yml)", pipeline, file)
	if flags.Output == "json" {
		return
	}
	render.NewWriter(cmd.ErrOrStderr()).Info(
		fmt.Sprintf("Using built-in default %s pipeline (override with workspace/%s.yml).", pipeline, file),
	)
}
