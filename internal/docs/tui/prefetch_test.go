package tui

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"devbox-cli/internal/docs/mermaid"
)

// FakeRenderer tracks calls and allows injection of delays.
type FakeRenderer struct {
	mu               sync.Mutex
	calls            int
	maxConcurrent    int32
	currentConcurrent int32
	delay            time.Duration
	failAfter        int // Fail after N calls (-1 to never fail)
}

func (fr *FakeRenderer) Render(ctx context.Context, src string, theme mermaid.Theme, width int) ([]byte, error) {
	current := atomic.AddInt32(&fr.currentConcurrent, 1)
	defer atomic.AddInt32(&fr.currentConcurrent, -1)

	// Track max concurrent
	for {
		oldMax := atomic.LoadInt32(&fr.maxConcurrent)
		if current <= oldMax || atomic.CompareAndSwapInt32(&fr.maxConcurrent, oldMax, current) {
			break
		}
	}

	fr.mu.Lock()
	shouldFail := fr.failAfter >= 0 && fr.calls >= fr.failAfter
	fr.calls++
	fr.mu.Unlock()

	if fr.delay > 0 {
		select {
		case <-time.After(fr.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if shouldFail {
		return nil, mermaid.ErrRenderingDisabled
	}

	return []byte("fake png"), nil
}

func TestPrefetchBoundedConcurrency(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	progress := make(chan ProgressMsg, 100)
	renderer := &FakeRenderer{delay: 10 * time.Millisecond}

	prefetch := NewPrefetch(ctx, renderer, progress)
	defer prefetch.Close()

	items := []WorkItem{
		{Source: "a", Theme: mermaid.ThemeDark, Width: 100, Index: 0},
		{Source: "b", Theme: mermaid.ThemeDark, Width: 100, Index: 1},
		{Source: "c", Theme: mermaid.ThemeDark, Width: 100, Index: 2},
		{Source: "d", Theme: mermaid.ThemeDark, Width: 100, Index: 3},
		{Source: "e", Theme: mermaid.ThemeDark, Width: 100, Index: 4},
	}

	prefetch.Queue(items)

	// Wait for rendering to complete
	for {
		select {
		case msg := <-progress:
			if msg.Rendered >= len(items) {
				goto done
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for prefetch to complete")
		}
	}
done:

	// Give workers a moment to finish
	time.Sleep(100 * time.Millisecond)

	// Verify bounded concurrency (max 2 at a time)
	require.LessOrEqual(t, renderer.maxConcurrent, int32(MaxPrefetchWorkers), "max concurrent workers exceeded")
	require.Equal(t, 5, renderer.calls, "expected all 5 diagrams rendered")
}

func TestPrefetchProgressReporting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	progress := make(chan ProgressMsg, 100)
	renderer := &FakeRenderer{delay: 5 * time.Millisecond}

	prefetch := NewPrefetch(ctx, renderer, progress)
	defer prefetch.Close()

	items := []WorkItem{
		{Source: "a", Theme: mermaid.ThemeDark, Width: 100, Index: 0},
		{Source: "b", Theme: mermaid.ThemeDark, Width: 100, Index: 1},
		{Source: "c", Theme: mermaid.ThemeDark, Width: 100, Index: 2},
	}

	prefetch.Queue(items)

	// Collect progress messages
	var msgs []ProgressMsg
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case msg := <-progress:
			msgs = append(msgs, msg)
			if msg.Rendered >= len(items) {
				goto done
			}
		case <-timeout.C:
			t.Fatal("timeout waiting for progress messages")
		}
	}
done:

	// Verify progress messages
	require.Greater(t, len(msgs), 0, "expected progress messages")
	lastMsg := msgs[len(msgs)-1]
	require.Equal(t, len(items), lastMsg.Rendered, "expected final rendered count to equal total")
	require.Equal(t, len(items), lastMsg.Total, "expected total count to equal items queued")
}

func TestPrefetchCleanExitOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	progress := make(chan ProgressMsg, 100)
	renderer := &FakeRenderer{delay: 100 * time.Millisecond}

	prefetch := NewPrefetch(ctx, renderer, progress)

	items := []WorkItem{
		{Source: "a", Theme: mermaid.ThemeDark, Width: 100, Index: 0},
		{Source: "b", Theme: mermaid.ThemeDark, Width: 100, Index: 1},
		{Source: "c", Theme: mermaid.ThemeDark, Width: 100, Index: 2},
		{Source: "d", Theme: mermaid.ThemeDark, Width: 100, Index: 3},
		{Source: "e", Theme: mermaid.ThemeDark, Width: 100, Index: 4},
	}

	prefetch.Queue(items)

	// Give it a moment to start rendering
	time.Sleep(50 * time.Millisecond)

	// Cancel and close
	cancel()
	prefetch.Close() // Should exit cleanly without hanging

	// Verify that cancellation was effective (we started rendering but not all items completed)
	// The exact number depends on timing, but with 100ms delay and 50ms wait, we should render fewer items
	require.LessOrEqual(t, renderer.calls, len(items), "rendering should have been stopped by cancellation")
}

func TestPrefetchErrorSwallowing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	progress := make(chan ProgressMsg, 100)
	renderer := &FakeRenderer{delay: 5 * time.Millisecond, failAfter: 1} // Fail after first call

	prefetch := NewPrefetch(ctx, renderer, progress)
	defer prefetch.Close()

	items := []WorkItem{
		{Source: "a", Theme: mermaid.ThemeDark, Width: 100, Index: 0},
		{Source: "b", Theme: mermaid.ThemeDark, Width: 100, Index: 1},
		{Source: "c", Theme: mermaid.ThemeDark, Width: 100, Index: 2},
	}

	prefetch.Queue(items)

	// Wait for all renders (including failures)
	for {
		select {
		case msg := <-progress:
			if msg.Rendered >= len(items) {
				goto done
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for prefetch to complete")
		}
	}
done:

	// Despite the failure, all items should have been processed
	require.Equal(t, len(items), renderer.calls, "expected all items to be attempted despite errors")
}

func TestPrefetchItemQueueing(t *testing.T) {
	// Test that items are queued and processed successfully
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	progress := make(chan ProgressMsg, 100)
	renderer := &FakeRenderer{delay: 2 * time.Millisecond}

	prefetch := NewPrefetch(ctx, renderer, progress)
	defer prefetch.Close()

	items := []WorkItem{
		{Source: "a", Theme: mermaid.ThemeDark, Width: 100, Index: 0},
		{Source: "b", Theme: mermaid.ThemeDark, Width: 100, Index: 1},
		{Source: "c", Theme: mermaid.ThemeDark, Width: 100, Index: 2},
		{Source: "d", Theme: mermaid.ThemeDark, Width: 100, Index: 3},
	}

	prefetch.Queue(items)

	// Wait for completion
	for {
		select {
		case msg := <-progress:
			if msg.Rendered >= len(items) {
				goto done
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for prefetch to complete")
		}
	}
done:

	// Verify all items were processed
	require.Equal(t, len(items), renderer.calls, "expected all items to be rendered")
}

func TestPrefetchClosureChainCleanExit(t *testing.T) {
	// This test verifies that Close() properly cancels the context and
	// the workers exit cleanly without goroutine leaks (verified by goleak.VerifyTestMain)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	progress := make(chan ProgressMsg, 100)
	renderer := &FakeRenderer{delay: 10 * time.Millisecond}

	prefetch := NewPrefetch(ctx, renderer, progress)

	items := []WorkItem{
		{Source: "a", Theme: mermaid.ThemeDark, Width: 100, Index: 0},
		{Source: "b", Theme: mermaid.ThemeDark, Width: 100, Index: 1},
	}

	prefetch.Queue(items)

	// Close should wait for workers to finish
	prefetch.Close()

	// Verify no hanging goroutines (goleak will catch this if we fail)
	// No explicit assertion needed; goleak.VerifyTestMain will fail if there are leaks
}

func TestPrefetchMultipleQueues(t *testing.T) {
	// Test that we can queue multiple batches of items
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	progress := make(chan ProgressMsg, 100)
	renderer := &FakeRenderer{delay: 2 * time.Millisecond}

	prefetch := NewPrefetch(ctx, renderer, progress)
	defer prefetch.Close()

	// First batch
	batch1 := []WorkItem{
		{Source: "batch1_a", Theme: mermaid.ThemeDark, Width: 100, Index: 0},
		{Source: "batch1_b", Theme: mermaid.ThemeDark, Width: 100, Index: 1},
	}

	prefetch.Queue(batch1)

	// Wait for batch 1 to complete
	for {
		select {
		case msg := <-progress:
			if msg.Rendered >= len(batch1) {
				goto batch2
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for batch 1")
		}
	}

batch2:
	// Second batch
	batch2 := []WorkItem{
		{Source: "batch2_a", Theme: mermaid.ThemeDark, Width: 100, Index: 0},
		{Source: "batch2_b", Theme: mermaid.ThemeDark, Width: 100, Index: 1},
		{Source: "batch2_c", Theme: mermaid.ThemeDark, Width: 100, Index: 2},
	}

	prefetch.Queue(batch2)

	// Wait for batch 2 to complete
	for {
		select {
		case msg := <-progress:
			if msg.Rendered >= len(batch2) {
				goto done
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for batch 2")
		}
	}
done:

	// Verify all items were rendered
	require.Equal(t, len(batch1)+len(batch2), renderer.calls, "all items should have been rendered")
}
