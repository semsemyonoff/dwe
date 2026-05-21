package notify

import "context"

// backend is the unexported dispatch interface. Two implementations
// today: noopBackend (silent) and nativeBackend (OS notifier, wired in
// Task 3). New implementations live alongside; Notifier picks one at
// construction time based on the resolved channel list.
type backend interface {
	notify(ctx context.Context, ev Event)
}

// noopBackend silently drops every event. Used when notifications are
// disabled (master switch off, non-interactive environment, no usable
// channels).
type noopBackend struct{}

func (noopBackend) notify(_ context.Context, _ Event) {}

// nativeBackend is a placeholder in Task 2 — Task 3 wires beeep here.
// Defining the type now keeps the factory switch in factory.go honest
// about what it returns.
type nativeBackend struct{}

func (nativeBackend) notify(_ context.Context, _ Event) {}

// Compile-time interface checks (golang-structs-interfaces).
var (
	_ backend = noopBackend{}
	_ backend = nativeBackend{}
)
