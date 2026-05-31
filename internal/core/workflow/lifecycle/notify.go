package lifecycle

import (
	"context"

	"github.com/semsemyonoff/devbox/internal/core/notify"
	userpkg "github.com/semsemyonoff/devbox/internal/core/project/user"
)

// notifier is the consumer-local interface declared per the plan's
// testability pattern. Tests swap in a recording fake by overriding
// newNotifier.
type notifier interface {
	Notify(ctx context.Context, ev notify.Event)
}

// newNotifier is the package-level seam: production code constructs a
// real *notify.Notifier from the userconfig; tests override the var to
// record events.
var newNotifier = func(cfg *userpkg.Config) notifier {
	return notify.New(cfg)
}
