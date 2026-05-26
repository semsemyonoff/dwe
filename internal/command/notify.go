package command

import (
	"context"

	"devbox-cli/internal/notify"
	"devbox-cli/internal/userconfig"

	"github.com/spf13/cobra"
)

// addSilentFlag registers a local --silent flag that suppresses the
// end-of-operation desktop notification for the command. Cobra renders one
// help line per command, so help text stays focused; the flag is intentionally
// not on rootFlags — most commands never fire notifications.
func addSilentFlag(cmd *cobra.Command, target *bool) {
	cmd.Flags().BoolVar(target, "silent", false,
		"suppress the end-of-run desktop notification")
}

// notifier is the consumer-local interface declared per the plan's
// testability pattern. Each hookpoint in this package depends only on
// the single method it needs; tests swap in a recording fake by
// overriding newNotifier.
type notifier interface {
	Notify(ctx context.Context, ev notify.Event)
}

// newNotifier is the package-level seam: production code constructs a
// real *notify.Notifier from the userconfig; tests override the var to
// record events.
var newNotifier = func(cfg *userconfig.Config) notifier {
	return notify.New(cfg)
}
