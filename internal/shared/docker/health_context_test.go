package docker

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestWaitContainersHealthyContext_CancelAborts verifies that cancelling the
// context aborts the polling loop within one interval, returning ctx.Err().
func TestWaitContainersHealthyContext_CancelAborts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after 50ms — well before the second poll tick.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	// Health stays "starting" forever — only cancellation can end the loop.
	getHealth := func(string) (string, error) { return "starting", nil }

	start := time.Now()
	err := WaitContainersHealthyContext(ctx, []string{"id1"}, getHealth, 100, 100*time.Millisecond, nil)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("WaitContainersHealthyContext did not abort promptly: %v", elapsed)
	}
}

// TestWaitContainersHealthyContext_DeadlineExceeded verifies behaviour under a
// context deadline.
func TestWaitContainersHealthyContext_DeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	getHealth := func(string) (string, error) { return "starting", nil }

	err := WaitContainersHealthyContext(ctx, []string{"id1"}, getHealth, 100, 100*time.Millisecond, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}
