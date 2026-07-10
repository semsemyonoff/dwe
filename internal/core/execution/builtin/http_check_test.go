package builtin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"
)

func TestHTTPCheckValidate(t *testing.T) {
	t.Parallel()
	b := HTTPCheck{}
	cases := []struct {
		name    string
		with    map[string]any
		wantErr string
	}{
		{"ok minimal", map[string]any{"url": "http://localhost:8080/health"}, ""},
		{"ok https", map[string]any{"url": "https://example.com"}, ""},
		{"ok full", map[string]any{"url": "http://x/y", "status": 204, "contains": "ok", "retries": 3, "interval": "500ms", "timeout": "2s"}, ""},
		{"missing url", map[string]any{"status": 200}, "missing required param 'url'"},
		{"empty url", map[string]any{"url": ""}, "missing required param 'url'"},
		{"bad scheme", map[string]any{"url": "ftp://x/y"}, "must be http or https"},
		{"no host", map[string]any{"url": "http://"}, "missing host"},
		{"bad status type", map[string]any{"url": "http://x", "status": "abc"}, "invalid integer"},
		{"negative retries", map[string]any{"url": "http://x", "retries": -1}, "must be >= 0"},
		{"bad retries type", map[string]any{"url": "http://x", "retries": "abc"}, "invalid integer"},
		{"negative interval", map[string]any{"url": "http://x", "interval": "-1s"}, "param 'interval': must be >= 0"},
		{"bad interval", map[string]any{"url": "http://x", "interval": "nope"}, "invalid duration"},
		{"bad timeout", map[string]any{"url": "http://x", "timeout": "nope"}, "invalid duration"},
		{"zero timeout", map[string]any{"url": "http://x", "timeout": "0s"}, "must be > 0"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := b.Validate(tt.with)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("want %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestHTTPCheckDescribe(t *testing.T) {
	t.Parallel()
	got := HTTPCheck{}.Describe(map[string]any{"url": "http://h/x", "status": 204})
	if !strings.Contains(got, "http://h/x") || !strings.Contains(got, "204") {
		t.Fatalf("describe: %q", got)
	}
}

func TestHTTPCheckRun_OK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	b := HTTPCheck{}
	if err := b.Run(context.Background(), map[string]any{"url": srv.URL}, spec.ExecContext{}); err != nil {
		t.Fatalf("want ok, got %v", err)
	}
}

func TestHTTPCheckRun_WrongStatus(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := HTTPCheck{}
	err := b.Run(context.Background(), map[string]any{"url": srv.URL}, spec.ExecContext{})
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "expected status 200, got 500") {
		t.Fatalf("want status mismatch message, got %v", err)
	}
}

func TestHTTPCheckRun_CustomStatus(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	b := HTTPCheck{}
	if err := b.Run(context.Background(), map[string]any{"url": srv.URL, "status": 204}, spec.ExecContext{}); err != nil {
		t.Fatalf("want ok, got %v", err)
	}
}

func TestHTTPCheckRun_ContainsMatch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	}))
	defer srv.Close()

	b := HTTPCheck{}
	if err := b.Run(context.Background(), map[string]any{"url": srv.URL, "contains": "healthy"}, spec.ExecContext{}); err != nil {
		t.Fatalf("want ok, got %v", err)
	}
}

func TestHTTPCheckRun_ContainsMismatch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	b := HTTPCheck{}
	err := b.Run(context.Background(), map[string]any{"url": srv.URL, "contains": "healthy"}, spec.ExecContext{})
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), `does not contain "healthy"`) {
		t.Fatalf("want contains-mismatch message, got %v", err)
	}
}

func TestHTTPCheckRun_RetriesThenSucceed(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	b := HTTPCheck{}
	err := b.Run(context.Background(), map[string]any{
		"url":      srv.URL,
		"retries":  3,
		"interval": "1ms",
	}, spec.ExecContext{})
	if err != nil {
		t.Fatalf("want ok after retries, got %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("want 3 calls, got %d", got)
	}
}

func TestHTTPCheckRun_RetriesExhausted(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	b := HTTPCheck{}
	err := b.Run(context.Background(), map[string]any{
		"url":      srv.URL,
		"retries":  2,
		"interval": "1ms",
	}, spec.ExecContext{})
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "after 3 attempts") {
		t.Fatalf("want attempt-count message, got %v", err)
	}
}

func TestHTTPCheckRun_CanceledDuringRetryWait(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Fail every attempt, and cancel the parent context on the first one so
		// Run is sleeping in the inter-attempt select when cancellation lands.
		if calls.Add(1) == 1 {
			cancel()
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	b := HTTPCheck{}
	// A long interval guarantees the return is driven by cancellation, not by the
	// timer elapsing.
	err := b.Run(ctx, map[string]any{
		"url":      srv.URL,
		"retries":  5,
		"interval": "30s",
	}, spec.ExecContext{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("want exactly 1 attempt before cancellation, got %d", got)
	}
}

func TestHTTPCheckRun_Timeout(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = w.Write([]byte("late"))
	}))
	// Order matters: release must close before srv.Close() so the blocked
	// handler can return; otherwise srv.Close() waits on it forever (LIFO defers).
	defer srv.Close()
	defer close(release)

	b := HTTPCheck{}
	start := time.Now()
	err := b.Run(context.Background(), map[string]any{"url": srv.URL, "timeout": "50ms"}, spec.ExecContext{})
	if err == nil {
		t.Fatal("want timeout error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
}

func TestHTTPCheckRun_ConnectionRefused(t *testing.T) {
	t.Parallel()
	b := HTTPCheck{}
	// Port 1 on loopback: nothing listens.
	err := b.Run(context.Background(), map[string]any{"url": "http://127.0.0.1:1/", "timeout": "200ms"}, spec.ExecContext{})
	if err == nil {
		t.Fatal("want connection error")
	}
}
