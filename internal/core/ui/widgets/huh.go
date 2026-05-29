package widgets

import (
	"sync"
)

// huhHooksMu guards huhBeforeHook and huhAfterHook. The two hooks are written
// together under Lock so callers always observe a consistent pair via
// snapshotHuhHooks. Callers (RunConfirm / RunSelector / RunMultiSelect) snapshot
// the pair once at entry and use the snapshotted after-hook in a defer so that
// SetHuhHooks / ClearHuhHooks calls during a prompt cannot break pairing.
var (
	huhHooksMu    sync.RWMutex
	huhBeforeHook func()
	huhAfterHook  func()
)

// SetHuhHooks installs hooks invoked before/after every huh-based prompt
// (RunConfirm, RunSelector, RunMultiSelect). Both hooks are written together so
// snapshotHuhHooks always returns a consistent pair. Pass nil for either to
// disable that side; ClearHuhHooks is the canonical way to remove both at once.
//
// Only one PlainReporter is expected to be active per process; nested deploys
// are not supported by the global hook design.
func SetHuhHooks(before, after func()) {
	huhHooksMu.Lock()
	huhBeforeHook = before
	huhAfterHook = after
	huhHooksMu.Unlock()
}

// ClearHuhHooks removes the package-level prompt hooks. Safe to call when no
// hooks are installed.
func ClearHuhHooks() {
	huhHooksMu.Lock()
	huhBeforeHook = nil
	huhAfterHook = nil
	huhHooksMu.Unlock()
}

// snapshotHuhHooks returns the current (before, after) pair under a single
// RLock so callers do not re-read the globals between before() and after().
func snapshotHuhHooks() (before, after func()) {
	huhHooksMu.RLock()
	before = huhBeforeHook
	after = huhAfterHook
	huhHooksMu.RUnlock()
	return before, after
}

// SnapshotHuhHooks exposes the current hook pair. Used by cross-package tests
// (e.g. internal/core/execution/pipeline) to assert hook installation; production callers
// should use snapshotHuhHooks via the prompt entry points instead.
func SnapshotHuhHooks() (before, after func()) {
	return snapshotHuhHooks()
}

// RunWithPromptHooks runs fn wrapped by the snapshotted before/after hook pair.
// It is the canonical entry point for full-screen prompt-like UI (e.g. the
// command browser TUI) outside of internal/core/ui's huh-backed primitives: it
// snapshots the current (before, after) pair once, calls before(), defers
// after(), then invokes fn(). The after hook still fires when fn returns an
// error so a paused LiveLine always resumes.
//
// Production callers should prefer this over SnapshotHuhHooks (which is
// scoped to cross-package tests per its docstring).
func RunWithPromptHooks(fn func() error) error {
	before, after := snapshotHuhHooks()
	if before != nil {
		before()
	}
	if after != nil {
		defer after()
	}
	return fn()
}
