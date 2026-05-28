package cmdctx

import (
	"context"

	"devbox-cli/internal/core/notify"
	"devbox-cli/internal/userconfig"
)

// Notifier is the minimal interface consumers depend on. Tests override
// NewNotifier to swap in a recording fake.
type Notifier interface {
	Notify(ctx context.Context, ev notify.Event)
}

// NewNotifier constructs the production notifier; tests override it.
var NewNotifier = func(cfg *userconfig.Config) Notifier {
	return notify.New(cfg)
}
