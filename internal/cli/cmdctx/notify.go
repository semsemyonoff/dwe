package cmdctx

import (
	"context"

	"github.com/semsemyonoff/dwe/internal/core/notify"
	userpkg "github.com/semsemyonoff/dwe/internal/core/project/user"
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
