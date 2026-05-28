package cmdctx

import (
	"context"

	"devbox-cli/internal/core/notify"
	userpkg "devbox-cli/internal/core/project/user"
)

// Notifier is the minimal interface consumers depend on. Tests override
// NewNotifier to swap in a recording fake.
type Notifier interface {
	Notify(ctx context.Context, ev notify.Event)
}

// NewNotifier constructs the production notifier; tests override it.
var NewNotifier = func(cfg *userpkg.Config) Notifier {
	return notify.New(cfg)
}
