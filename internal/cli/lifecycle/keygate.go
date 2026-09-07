package lifecycle

import (
	"context"
	"io"

	"github.com/semsemyonoff/dwe/internal/core/ui/secretsprompt"
	"github.com/semsemyonoff/dwe/internal/core/workflow/keygate"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"

	"github.com/spf13/cobra"
)

// keyPrompt and keyConfirm wrap the two interactive halves of the age-identity
// gate into the function values RunContext carries. They live here rather than
// in core/workflow/lifecycle because the forms are core/ui (§ Dependency Rules,
// ui-is-sink) and because the cobra streams are only known at this layer.
//
// Every caller that does NOT build them — the service toggle executor, tests —
// leaves the fields nil, and a nil hook makes the gate non-interactive.
func keyPrompt(cmd *cobra.Command) keygate.PromptFunc {
	in, out := cmdStreams(cmd)
	return func(ctx context.Context, recipient string) (secrets.Identity, error) {
		return secretsprompt.PromptIdentity(ctx, recipient, in, out)
	}
}

func keyConfirm(cmd *cobra.Command) keygate.ConfirmFunc {
	in, out := cmdStreams(cmd)
	return func(ctx context.Context, explanation string) (bool, error) {
		return secretsprompt.ConfirmImport(ctx, explanation, in, out)
	}
}

func cmdStreams(cmd *cobra.Command) (io.Reader, io.Writer) {
	return cmd.InOrStdin(), cmd.OutOrStdout()
}
