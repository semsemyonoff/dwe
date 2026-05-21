package runtime

import (
	"context"

	"devbox-cli/internal/notify"
	"devbox-cli/internal/userconfig"
)

// notifier is the consumer-local interface declared per the plan's
// testability pattern. Tests override newNotifier to capture events.
type notifier interface {
	Notify(ctx context.Context, ev notify.Event)
}

// newNotifier is the package-level seam: production constructs a real
// *notify.Notifier; tests override to record events.
var newNotifier = func(cfg *userconfig.Config) notifier {
	return notify.New(cfg)
}

// userconfigLoadFunc is the package-level seam for userconfig loading.
// Tests override to count invocations / inject failures without touching disk.
var userconfigLoadFunc = userconfig.Load

// TestSnapshotRC is a test-only hook fired at the top of RunCommand with
// the inbound RunContext. Tests in other packages set this to capture
// the RunContext built by an upstream caller (e.g. pipeline executor) and
// assert propagation of fields like SkipNotify. Production code leaves
// this nil; the snapshot call short-circuits when nil.
var TestSnapshotRC func(RunContext)
