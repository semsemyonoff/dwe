package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNotificationIcon_PNGMagicBytes(t *testing.T) {
	if !bytes.HasPrefix(notificationIcon, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("embedded notificationIcon does not have PNG magic bytes; len=%d first=%x", len(notificationIcon), notificationIcon[:min(8, len(notificationIcon))])
	}
}

// withOSNotify swaps osNotify for the duration of a subtest.
func withOSNotify(t *testing.T, fn func(title, body string, icon any) error) {
	t.Helper()
	prev := osNotify
	osNotify = fn
	t.Cleanup(func() { osNotify = prev })
}

func TestFormatEvent_Success(t *testing.T) {
	title, body := formatEvent(Event{
		Kind:      OpDeploy,
		Operation: "deploy",
		Outcome:   OutcomeSuccess,
		Duration:  2500 * time.Millisecond,
		Project:   "myproj",
	})
	if title != "✓ DWE · myproj: deploy succeeded" {
		t.Fatalf("title=%q", title)
	}
	if body != "2.5s" {
		t.Fatalf("body=%q", body)
	}
}

func TestFormatEvent_SuccessNoProject(t *testing.T) {
	title, body := formatEvent(Event{
		Kind:      OpDeploy,
		Operation: "deploy",
		Outcome:   OutcomeSuccess,
		Duration:  2500 * time.Millisecond,
	})
	if title != "✓ DWE: deploy succeeded" {
		t.Fatalf("title=%q", title)
	}
	if body != "2.5s" {
		t.Fatalf("body=%q", body)
	}
}

func TestFormatEvent_FailureWithErr(t *testing.T) {
	title, body := formatEvent(Event{
		Operation: "run",
		Outcome:   OutcomeFailure,
		Duration:  500 * time.Millisecond,
		Project:   "p",
		Err:       errors.New("connection refused"),
	})
	if title != "✗ DWE · p: run failed" {
		t.Fatalf("title=%q", title)
	}
	if body != "500ms\nconnection refused" {
		t.Fatalf("body=%q", body)
	}
}

func TestFormatEvent_FailureTruncatesLongErr(t *testing.T) {
	long := strings.Repeat("x", 300)
	_, body := formatEvent(Event{
		Operation: "run",
		Outcome:   OutcomeFailure,
		Project:   "p",
		Err:       errors.New(long),
	})
	if !strings.HasSuffix(body, "…") {
		t.Fatalf("expected truncation marker, body=%q", body)
	}
	// 200 chars + "…"
	bodyLines := strings.Split(body, "\n")
	last := bodyLines[len(bodyLines)-1]
	if len([]rune(last)) != 201 {
		t.Fatalf("truncated len=%d, want 201", len([]rune(last)))
	}
}

func TestFormatEvent_FailureKeepsFirstLineOnly(t *testing.T) {
	_, body := formatEvent(Event{
		Operation: "run",
		Outcome:   OutcomeFailure,
		Project:   "p",
		Err:       errors.New("first line\nsecond line"),
	})
	if !strings.Contains(body, "first line") || strings.Contains(body, "second line") {
		t.Fatalf("expected only first line; got %q", body)
	}
}

func TestFormatEvent_OutcomeUnknown_ReturnsEmpty(t *testing.T) {
	title, body := formatEvent(Event{
		Operation: "deploy",
		Outcome:   OutcomeUnknown,
		Project:   "p",
	})
	if title != "" || body != "" {
		t.Fatalf("expected empty strings for OutcomeUnknown, got title=%q body=%q", title, body)
	}
}

func TestFormatEvent_MultibyteTruncation(t *testing.T) {
	long := strings.Repeat("日", 300)
	_, body := formatEvent(Event{
		Operation: "run",
		Outcome:   OutcomeFailure,
		Project:   "p",
		Err:       errors.New(long),
	})
	if !strings.HasSuffix(body, "…") {
		t.Fatalf("expected truncation marker, body=%q", body)
	}
	bodyLines := strings.Split(body, "\n")
	last := bodyLines[len(bodyLines)-1]
	if len([]rune(last)) != 201 {
		t.Fatalf("truncated rune len=%d, want 201", len([]rune(last)))
	}
}

func TestHumaniseDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0ms"},
		{500 * time.Millisecond, "500ms"},
		{2*time.Second + 500*time.Millisecond, "2.5s"},
		{90 * time.Second, "1m 30s"},
		{2*time.Hour + 15*time.Minute, "2h 15m"},
		{-5 * time.Second, "0ms"},
	}
	for _, tc := range cases {
		if got := humaniseDuration(tc.d); got != tc.want {
			t.Errorf("humaniseDuration(%v)=%q want %q", tc.d, got, tc.want)
		}
	}
}

func TestNativeBackend_NotifyCallsBeeep(t *testing.T) {
	var calls atomic.Int32
	var gotTitle, gotBody string
	var gotIcon any
	done := make(chan struct{})
	withOSNotify(t, func(title, body string, icon any) error {
		gotTitle, gotBody = title, body
		gotIcon = icon
		calls.Add(1)
		close(done)
		return nil
	})
	b := newNativeBackend()
	b.notify(context.Background(), Event{Operation: "deploy", Outcome: OutcomeSuccess, Project: "p"})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("osNotify was not invoked")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d want 1", calls.Load())
	}
	if !strings.Contains(gotTitle, "succeeded") {
		t.Fatalf("title=%q", gotTitle)
	}
	// Project name is now in the title, not the body.
	if !strings.Contains(gotTitle, "p") {
		t.Fatalf("title missing project: %q", gotTitle)
	}
	if strings.Contains(gotBody, "p") {
		t.Fatalf("body should not contain project name: %q", gotBody)
	}
	iconBytes, ok := gotIcon.([]byte)
	if !ok {
		t.Fatalf("icon is %T, want []byte", gotIcon)
	}
	if !bytes.HasPrefix(iconBytes, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("icon does not have PNG magic bytes; first=%x", iconBytes[:min(8, len(iconBytes))])
	}
}

func TestNativeBackend_SwallowsBeeepError(t *testing.T) {
	done := make(chan struct{})
	withOSNotify(t, func(_, _ string, _ any) error {
		defer close(done)
		return fmt.Errorf("boom")
	})
	b := newNativeBackend()
	b.notify(context.Background(), Event{Operation: "deploy", Outcome: OutcomeSuccess})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("osNotify not called")
	}
	// No panic, no error returned — pass.
}

func TestNativeBackend_TimeoutDoesNotBlock(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	withOSNotify(t, func(_, _ string, _ any) error {
		<-block
		return nil
	})

	// Shorten the timeout for this test by temporarily replacing the
	// constant via a small wrapper: we can't change the const, so we
	// just assert the call returns within timeout + slack.
	b := newNativeBackend()
	start := time.Now()
	b.notify(context.Background(), Event{Operation: "deploy", Outcome: OutcomeSuccess})
	elapsed := time.Since(start)
	// Allow some slack on slow CI.
	if elapsed > nativeBackendTimeout+500*time.Millisecond {
		t.Fatalf("notify took %v; expected return within ~%v", elapsed, nativeBackendTimeout)
	}
}

func TestNativeBackend_CtxCancelReturnsImmediately(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	withOSNotify(t, func(_, _ string, _ any) error {
		<-block
		return nil
	})

	b := newNativeBackend()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	b.notify(ctx, Event{Operation: "deploy", Outcome: OutcomeSuccess})
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("cancelled notify took %v; expected near-immediate return", elapsed)
	}
}

func TestNativeBackend_DropOnBusy(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	var firstCalls atomic.Int32
	withOSNotify(t, func(_, _ string, _ any) error {
		firstCalls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
		return nil
	})
	b := newNativeBackend()

	// First call: cancelled ctx so the caller returns once the worker
	// has been launched. Wait for the worker to actually be inside
	// osNotify before we attempt the second call.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.notify(ctx, Event{Operation: "deploy", Outcome: OutcomeSuccess})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("worker did not start")
	}

	// Slot should still be occupied. Second call must drop without
	// invoking osNotify again.
	b.notify(ctx, Event{Operation: "deploy", Outcome: OutcomeSuccess})
	if got := firstCalls.Load(); got != 1 {
		t.Fatalf("firstCalls=%d want 1 (second call should drop)", got)
	}

	// Release the worker, give it a moment to release the slot.
	close(block)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(b.sem) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(b.sem) != 0 {
		t.Fatalf("slot not released after worker finished")
	}

	// Third call should proceed (using fresh ctx so it actually waits).
	// Use a separate counter so any stragglers from prior stages cannot
	// inflate the third-call assertion.
	var thirdCalls atomic.Int32
	freshDone := make(chan struct{})
	withOSNotify(t, func(_, _ string, _ any) error {
		thirdCalls.Add(1)
		close(freshDone)
		return nil
	})
	b.notify(context.Background(), Event{Operation: "deploy", Outcome: OutcomeSuccess})
	select {
	case <-freshDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("third call did not invoke osNotify")
	}
	if got := thirdCalls.Load(); got != 1 {
		t.Fatalf("thirdCalls=%d want 1", got)
	}
}

func TestNew_UnknownChannelsOnly_FallsBackToNoop(t *testing.T) {
	// Already covered by TestNew_OnlyUnknownChannels_ReturnsNoop in
	// notifier_test.go, but pin the *native.go* contract: pickBackend
	// returns nil for unknown-only.
	withInteractive(t, true)
	cfg := enabledCfg()
	cfg.NotifyChannels = []string{"telegram"}
	n := New(cfg)
	if n.enabled {
		t.Fatalf("expected disabled")
	}
}
