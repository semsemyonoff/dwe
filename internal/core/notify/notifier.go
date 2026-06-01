package notify

import (
	"context"

	userpkg "github.com/semsemyonoff/dwe/internal/core/project/user"
)

// Notifier is the concrete handle returned by New. A nil receiver is
// safe to call — Notify short-circuits — so hookpoints can store the
// return of New without nil-guarding.
type Notifier struct {
	cfg     *userpkg.Config
	backend backend
	enabled bool
}

// Notify dispatches the event when the factory-level enabled bit is
// set and the per-op gate on the userconfig matches. Errors from the
// backend never surface — best-effort by design.
func (n *Notifier) Notify(ctx context.Context, ev Event) {
	if n == nil || !n.enabled {
		return
	}
	if !n.cfg.NotifyEnabledFor(ev.Kind.configKey()) {
		return
	}
	n.backend.notify(ctx, ev)
}
