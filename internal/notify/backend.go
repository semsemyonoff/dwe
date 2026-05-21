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

// Compile-time interface check for the in-package no-op backend.
// nativeBackend's check lives in native.go.
var _ backend = noopBackend{}
