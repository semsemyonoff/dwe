package notify

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

//go:embed assets/icon.png
var notificationIcon []byte

// osNotify is the seam for tests to intercept the OS notifier call.
// Its concrete value is set in a platform-specific file: on darwin we
// drive `terminal-notifier` directly so the icon actually renders on
// modern macOS; on other platforms we delegate to beeep. icon is `any`
// to mirror beeep.Notify (path string or raw []byte).
//
// See native_darwin.go and native_other.go for the platform bindings.

// nativeBackendTimeout bounds how long Notify waits on the OS notifier
// before logging and giving up. The goroutine running beeep continues
// in the background until beeep returns (the semaphore slot stays
// occupied), which bounds in-flight goroutines to at most one per
// process.
const nativeBackendTimeout = 2 * time.Second

const failBodyMaxErrLen = 200

// nativeBackend dispatches notifications via gen2brain/beeep. The
// single-slot semaphore enforces "at most one in-flight beeep call per
// process": back-to-back notify() invocations whose predecessor hasn't
// completed are silently dropped with a debug log. The slot is released
// from inside the worker goroutine — not the caller — so a hung beeep
// call keeps the slot occupied forever, structurally bounding goroutine
// leaks to one even when the OS notifier daemon stalls.
type nativeBackend struct {
	sem chan struct{}
}

func newNativeBackend() *nativeBackend {
	return &nativeBackend{sem: make(chan struct{}, 1)}
}

func (b *nativeBackend) notify(ctx context.Context, ev Event) {
	title, body := formatEvent(ev)
	if title == "" {
		return
	}

	select {
	case b.sem <- struct{}{}:
	default:
		slog.Debug("notify backend busy, dropping event", "kind", ev.Kind)
		return
	}

	// Snapshot the seam before spawning so tests that swap osNotify and
	// then restore it on cleanup cannot race with the goroutine read.
	notifyFn := osNotify
	done := make(chan error, 1)
	go func() {
		var result error
		defer func() {
			if r := recover(); r != nil {
				slog.Debug("notify backend panic recovered", "recover", r)
				result = fmt.Errorf("panic: %v", r)
			}
			<-b.sem // always release before signalling so a concurrent notify() sees the slot as free
			done <- result
		}()
		result = notifyFn(title, body, notificationIcon)
	}()

	timer := time.NewTimer(nativeBackendTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			slog.Debug("notify backend failed", "err", err)
		}
	case <-timer.C:
		slog.Debug("notify backend timed out; slot remains occupied until beeep returns", "kind", ev.Kind)
	case <-ctx.Done():
		slog.Debug("notify backend cancelled by ctx; slot remains occupied until beeep returns", "err", ctx.Err())
	}
}

// formatEvent renders the title and body strings for a notification.
// The exact format strings and failBodyMaxErrLen are pinned by
// native_test.go — changing either breaks those assertions.
func formatEvent(ev Event) (title, body string) {
	op := ev.Operation
	if op == "" {
		op = "operation"
	}
	dur := humaniseDuration(ev.Duration)
	prefix := "DWE"
	if ev.Project != "" {
		prefix = "DWE · " + ev.Project
	}
	switch ev.Outcome {
	case OutcomeFailure:
		title = fmt.Sprintf("✗ %s: %s failed", prefix, op)
		body = dur
		if ev.Err != nil && ev.Err.Error() != "" {
			body += "\n" + truncateErr(ev.Err.Error(), failBodyMaxErrLen)
		}
	case OutcomeSuccess:
		title = fmt.Sprintf("✓ %s: %s succeeded", prefix, op)
		body = dur
	default:
		slog.Debug("notify: unexpected zero-value Outcome; dropping event", "kind", ev.Kind)
		return "", ""
	}
	return title, body
}

// truncateErr keeps the first line of an error message and clips it to
// max runes so the notification body stays readable.
func truncateErr(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// humaniseDuration formats a duration for display in the notification
// body. Buckets: <1s → "Xms"; <60s → "X.Xs"; <1h → "Xm Ys"; ≥1h →
// "Xh Ym".
func humaniseDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		h := int(d / time.Hour)
		m := int((d % time.Hour) / time.Minute)
		return fmt.Sprintf("%dh %dm", h, m)
	}
}

var _ backend = (*nativeBackend)(nil)
