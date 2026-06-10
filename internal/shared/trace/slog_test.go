package trace

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestSlogHandler_RoutesThroughFallback(t *testing.T) {
	reset(t)
	var buf bytes.Buffer
	Configure(&buf, LevelDebug)

	logger := slog.New(NewSlogHandler())
	logger.Debug("loading config", "path", "/tmp/workspace.yml", "services", 3)

	got := strings.TrimSpace(buf.String())
	for _, want := range []string{"DEBUG", "loading config", "path=/tmp/workspace.yml", "services=3"} {
		if !strings.Contains(got, want) {
			t.Errorf("record %q missing %q", got, want)
		}
	}
}

func TestSlogHandler_RoutesWarnAndError(t *testing.T) {
	reset(t)
	var buf bytes.Buffer
	Configure(&buf, LevelDebug)

	logger := slog.New(NewSlogHandler())
	logger.Warn("heads up", "err", "boom")
	logger.Error("broke", "code", 7)

	out := buf.String()
	if !strings.Contains(out, "WARN heads up err=boom") {
		t.Errorf("warn line missing/garbled: %q", out)
	}
	if !strings.Contains(out, "ERROR broke code=7") {
		t.Errorf("error line missing/garbled: %q", out)
	}
}

func TestSlogHandler_EnabledAlwaysTrue(t *testing.T) {
	reset(t)
	h := NewSlogHandler()
	for _, lvl := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		if !h.Enabled(context.Background(), lvl) {
			t.Errorf("Enabled(%v) = false, want true", lvl)
		}
	}
}

func TestSlogHandler_WithAttrsAndGroup(t *testing.T) {
	reset(t)
	var buf bytes.Buffer
	Configure(&buf, LevelDebug)

	logger := slog.New(NewSlogHandler()).
		With("component", "deploy").
		WithGroup("svc").
		With("name", "api")
	logger.Debug("step", "phase", "build")

	got := strings.TrimSpace(buf.String())
	for _, want := range []string{"component=deploy", "svc.name=api", "svc.phase=build"} {
		if !strings.Contains(got, want) {
			t.Errorf("record %q missing %q", got, want)
		}
	}
}

func TestSlogHandler_RoutesThroughCtxPrinter(t *testing.T) {
	reset(t)
	Configure(&bytes.Buffer{}, LevelDebug) // fallback should NOT receive the line
	p := &capturePrinter{}
	ctx := WithLinePrinter(context.Background(), p)

	logger := slog.New(NewSlogHandler())
	logger.DebugContext(ctx, "ctx routed", "k", "v")

	lines := p.snapshot()
	if len(lines) != 1 || !strings.Contains(lines[0], "ctx routed") {
		t.Fatalf("expected ctx printer to receive the record, got %v", lines)
	}
}

func TestSlogHandler_RoutesThroughGlobalPrinter(t *testing.T) {
	reset(t)
	Configure(&bytes.Buffer{}, LevelDebug)
	p := &capturePrinter{}
	restore := SetPrinter(p)
	defer restore()

	logger := slog.New(NewSlogHandler())
	logger.Debug("global routed")

	lines := p.snapshot()
	if len(lines) != 1 || !strings.Contains(lines[0], "global routed") {
		t.Fatalf("expected global printer to receive the record, got %v", lines)
	}
}
