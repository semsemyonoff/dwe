package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gen2brain/beeep"
)

// beeepNotify is the seam for tests to intercept the OS notifier call.
var beeepNotify = func(title, body, icon string) error {
	return beeep.Notify(title, body, icon)
}

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

	// Snapshot the seam before spawning so tests that swap beeepNotify
	// and then restore it on cleanup cannot race with the goroutine read.
	beeepFn := beeepNotify
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				<-b.sem // release before signalling so a concurrent notify() sees the slot as free
				slog.Debug("notify backend panic recovered", "recover", r)
				done <- fmt.Errorf("panic: %v", r)
				return
			}
			<-b.sem
		}()
		done <- beeepFn(title, body, "")
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
// Format strings and the failure-body truncation length are locked by
// the plan's Technical Details § notify package internals.
func formatEvent(ev Event) (title, body string) {
	op := ev.Operation
	if op == "" {
		op = "operation"
	}
	dur := humaniseDuration(ev.Duration)
	project := ev.Project
	if project == "" {
		project = "—"
	}
	switch ev.Outcome {
	case OutcomeFailure:
		title = fmt.Sprintf("✗ Devbox: %s failed", op)
		body = fmt.Sprintf("%s · %s", project, dur)
		if ev.Err != nil && ev.Err.Error() != "" {
			body += "\n" + truncateErr(ev.Err.Error(), failBodyMaxErrLen)
		}
	case OutcomeSuccess:
		title = fmt.Sprintf("✓ Devbox: %s succeeded", op)
		body = fmt.Sprintf("%s · %s", project, dur)
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
