package bridge

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// autoStopDone runs RunAutoStop in the background and returns its result
// channel, so tests can drive events while it runs.
func autoStopDone(ctx context.Context, cfg AutoStopConfig) <-chan error {
	done := make(chan error, 1)
	go func() { done <- RunAutoStop(ctx, cfg) }()
	return done
}

func waitAutoStop(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("RunAutoStop did not return in time")
		return nil
	}
}

// countScript scripts CountRunning results: each call pops the next entry,
// the last entry repeats once the script is exhausted. A negative count
// scripts an error result.
type countScript struct {
	mu     sync.Mutex
	counts []int
	calls  []time.Time
}

func (s *countScript) next(context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, time.Now())
	n := s.counts[0]
	if len(s.counts) > 1 {
		s.counts = s.counts[1:]
	}
	if n < 0 {
		return 0, fmt.Errorf("scripted docker failure %d", n)
	}
	return n, nil
}

func (s *countScript) callTimes() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

func TestAutoStopRequiresCountProbe(t *testing.T) {
	err := RunAutoStop(context.Background(), AutoStopConfig{})
	if err == nil || !strings.Contains(err.Error(), "CountRunning") {
		t.Fatalf("err = %v, want CountRunning requirement", err)
	}
}

func TestAutoStopWaitsGraceBeforeFirstCheck(t *testing.T) {
	const grace = 80 * time.Millisecond
	script := &countScript{counts: []int{0}}
	start := time.Now()

	done := autoStopDone(context.Background(), AutoStopConfig{
		CountRunning: script.next,
		Grace:        grace,
		PollInterval: time.Hour,
	})
	if err := waitAutoStop(t, done); err != nil {
		t.Fatalf("RunAutoStop: %v", err)
	}
	calls := script.callTimes()
	if len(calls) != 1 {
		t.Fatalf("count called %d times, want 1", len(calls))
	}
	if elapsed := calls[0].Sub(start); elapsed < grace {
		t.Errorf("first check after %v, want >= startup grace %v", elapsed, grace)
	}
}

func TestAutoStopEventDriven(t *testing.T) {
	// Scripted stack state: alive at the post-grace check, alive after the
	// first event, gone after the second.
	script := &countScript{counts: []int{1, 1, 0}}
	events := make(chan struct{}, 2)
	events <- struct{}{}
	events <- struct{}{}

	done := autoStopDone(context.Background(), AutoStopConfig{
		Subscribe: func(context.Context) (<-chan struct{}, error) {
			return events, nil
		},
		CountRunning: script.next,
		Grace:        time.Millisecond,
		PollInterval: time.Hour, // events, not polling, must drive this test
	})
	if err := waitAutoStop(t, done); err != nil {
		t.Fatalf("RunAutoStop: %v", err)
	}
	if calls := len(script.callTimes()); calls != 3 {
		t.Errorf("count called %d times, want 3", calls)
	}
}

func TestAutoStopEventsDropFallsBackToPolling(t *testing.T) {
	script := &countScript{counts: []int{1, 0}}
	events := make(chan struct{})
	close(events) // the stream drops right away

	var logMu sync.Mutex
	var logLines []string
	done := autoStopDone(context.Background(), AutoStopConfig{
		Subscribe: func(context.Context) (<-chan struct{}, error) {
			return events, nil
		},
		CountRunning: script.next,
		Grace:        time.Millisecond,
		PollInterval: 30 * time.Millisecond,
		Logf: func(format string, args ...any) {
			logMu.Lock()
			defer logMu.Unlock()
			logLines = append(logLines, fmt.Sprintf(format, args...))
		},
	})
	if err := waitAutoStop(t, done); err != nil {
		t.Fatalf("RunAutoStop: %v", err)
	}
	logMu.Lock()
	defer logMu.Unlock()
	if !slices.ContainsFunc(logLines, func(s string) bool {
		return strings.Contains(s, "falling back")
	}) {
		t.Errorf("log lines %q missing the poll-fallback notice", logLines)
	}
}

func TestAutoStopSubscribeErrorDegradesToPolling(t *testing.T) {
	script := &countScript{counts: []int{1, 0}}

	done := autoStopDone(context.Background(), AutoStopConfig{
		Subscribe: func(context.Context) (<-chan struct{}, error) {
			return nil, errors.New("docker events unavailable")
		},
		CountRunning: script.next,
		Grace:        time.Millisecond,
		PollInterval: 30 * time.Millisecond,
	})
	if err := waitAutoStop(t, done); err != nil {
		t.Fatalf("RunAutoStop: %v", err)
	}
}

func TestAutoStopCountErrorIsRetriedNextWakeup(t *testing.T) {
	// First check errors (scripted as -1); the daemon must survive the
	// docker hiccup and stop on the next successful zero-count poll.
	script := &countScript{counts: []int{-1, 0}}

	done := autoStopDone(context.Background(), AutoStopConfig{
		CountRunning: script.next,
		Grace:        time.Millisecond,
		PollInterval: 30 * time.Millisecond,
	})
	if err := waitAutoStop(t, done); err != nil {
		t.Fatalf("RunAutoStop: %v", err)
	}
	if calls := len(script.callTimes()); calls < 2 {
		t.Errorf("count called %d times, want >= 2", calls)
	}
}

func TestAutoStopStopsOnContextCancel(t *testing.T) {
	script := &countScript{counts: []int{1}} // stack stays up forever
	ctx, cancel := context.WithCancel(context.Background())

	done := autoStopDone(ctx, AutoStopConfig{
		CountRunning: script.next,
		Grace:        time.Millisecond,
		PollInterval: 20 * time.Millisecond,
	})
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := waitAutoStop(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestAutoStopCancelDuringGrace(t *testing.T) {
	script := &countScript{counts: []int{0}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RunAutoStop(ctx, AutoStopConfig{
		CountRunning: script.next,
		Grace:        time.Hour,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls := len(script.callTimes()); calls != 0 {
		t.Errorf("count called %d times during grace, want 0", calls)
	}
}

func TestDockerEventsArgs(t *testing.T) {
	got := dockerEventsArgs("acme-shop")
	want := []string{
		"events",
		"--filter", "type=container",
		"--filter", "label=com.docker.compose.project=acme-shop",
		"--format", "{{.Status}}",
	}
	if !slices.Equal(got, want) {
		t.Errorf("dockerEventsArgs = %q, want %q", got, want)
	}
}

func TestDockerPSArgs(t *testing.T) {
	got := dockerPSArgs("acme-shop")
	want := []string{
		"ps",
		"--quiet",
		"--filter", "status=running",
		"--filter", "label=com.docker.compose.project=acme-shop",
	}
	if !slices.Equal(got, want) {
		t.Errorf("dockerPSArgs = %q, want %q", got, want)
	}
}
