package trace

import (
	"bytes"
	"context"
	"sync"
	"testing"
)

// capturePrinter is a concurrency-safe LinePrinter that records lines.
type capturePrinter struct {
	mu    sync.Mutex
	lines []string
}

func (c *capturePrinter) PrintLine(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, s)
}

func (c *capturePrinter) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.lines))
	copy(out, c.lines)
	return out
}

// reset returns trace to a known clean state for the next test.
func reset(t *testing.T) {
	t.Helper()
	mu.Lock()
	printers = nil
	fallback = nil
	redactor = nil
	mu.Unlock()
	level.Store(int32(LevelOff))
}

func TestEnabled(t *testing.T) {
	tests := []struct {
		name    string
		set     Level
		query   Level
		enabled bool
	}{
		{"off-query-verbose", LevelOff, LevelVerbose, false},
		{"off-query-debug", LevelOff, LevelDebug, false},
		{"verbose-query-verbose", LevelVerbose, LevelVerbose, true},
		{"verbose-query-debug", LevelVerbose, LevelDebug, false},
		{"debug-query-verbose", LevelDebug, LevelVerbose, true},
		{"debug-query-debug", LevelDebug, LevelDebug, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
			Configure(&bytes.Buffer{}, tt.set)
			if got := Enabled(tt.query); got != tt.enabled {
				t.Fatalf("Enabled(%d) at level %d = %v, want %v", tt.query, tt.set, got, tt.enabled)
			}
		})
	}
}

func TestEmitLevelGating(t *testing.T) {
	tests := []struct {
		name      string
		level     Level
		emit      func(ctx context.Context)
		wantLines []string
	}{
		{
			name:      "command-off-silent",
			level:     LevelOff,
			emit:      func(ctx context.Context) { Command(ctx, "docker", "ps") },
			wantLines: nil,
		},
		{
			name:      "command-verbose-echoes",
			level:     LevelVerbose,
			emit:      func(ctx context.Context) { Command(ctx, "docker", "ps") },
			wantLines: []string{"$ docker ps"},
		},
		{
			name:      "command-debug-echoes",
			level:     LevelDebug,
			emit:      func(ctx context.Context) { Command(ctx, "docker", "ps") },
			wantLines: []string{"$ docker ps"},
		},
		{
			name:      "decision-off-silent",
			level:     LevelOff,
			emit:      func(ctx context.Context) { Decision(ctx, "skip %s", "build") },
			wantLines: nil,
		},
		{
			name:      "decision-verbose-emits",
			level:     LevelVerbose,
			emit:      func(ctx context.Context) { Decision(ctx, "skip %s", "build") },
			wantLines: []string{"skip build"},
		},
		{
			name:      "debugf-verbose-silent",
			level:     LevelVerbose,
			emit:      func(ctx context.Context) { Debugf(ctx, "took %dms", 12) },
			wantLines: nil,
		},
		{
			name:      "debugf-debug-emits",
			level:     LevelDebug,
			emit:      func(ctx context.Context) { Debugf(ctx, "took %dms", 12) },
			wantLines: []string{"took 12ms"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset(t)
			p := &capturePrinter{}
			Configure(nil, tt.level)
			restore := SetPrinter(p)
			defer restore()
			tt.emit(context.Background())
			got := p.snapshot()
			if len(got) != len(tt.wantLines) {
				t.Fatalf("lines = %v, want %v", got, tt.wantLines)
			}
			for i := range got {
				if got[i] != tt.wantLines[i] {
					t.Fatalf("line[%d] = %q, want %q", i, got[i], tt.wantLines[i])
				}
			}
		})
	}
}

func TestPrinterPrecedence(t *testing.T) {
	reset(t)
	Configure(nil, LevelVerbose)

	global := &capturePrinter{}
	restore := SetPrinter(global)
	defer restore()

	ctxPrinter := &capturePrinter{}
	ctx := WithLinePrinter(context.Background(), ctxPrinter)

	// ctx printer overrides the global printer.
	Decision(ctx, "via-ctx")
	// no ctx printer → global printer.
	Decision(context.Background(), "via-global")

	if got := ctxPrinter.snapshot(); len(got) != 1 || got[0] != "via-ctx" {
		t.Fatalf("ctx printer lines = %v, want [via-ctx]", got)
	}
	if got := global.snapshot(); len(got) != 1 || got[0] != "via-global" {
		t.Fatalf("global printer lines = %v, want [via-global]", got)
	}
}

func TestFallbackWriterWhenNoPrinter(t *testing.T) {
	reset(t)
	var buf bytes.Buffer
	Configure(&buf, LevelVerbose)

	Command(context.Background(), "git", "status")

	if got, want := buf.String(), "$ git status\n"; got != want {
		t.Fatalf("fallback = %q, want %q", got, want)
	}
}

func TestSetPrinterSaveRestore(t *testing.T) {
	reset(t)
	Configure(nil, LevelVerbose)

	base := &capturePrinter{}
	restoreBase := SetPrinter(base)

	nested := &capturePrinter{}
	restoreNested := SetPrinter(nested)

	// Top of stack is nested.
	Decision(context.Background(), "to-nested")
	restoreNested()

	// Back to base.
	Decision(context.Background(), "to-base")
	restoreBase()

	// No printer → falls through (no fallback writer set → no-op, no panic).
	Decision(context.Background(), "to-nowhere")

	if got := nested.snapshot(); len(got) != 1 || got[0] != "to-nested" {
		t.Fatalf("nested = %v, want [to-nested]", got)
	}
	if got := base.snapshot(); len(got) != 1 || got[0] != "to-base" {
		t.Fatalf("base = %v, want [to-base]", got)
	}
}

func TestRestoreOutOfOrderRemovesOnlyOwnEntry(t *testing.T) {
	reset(t)
	Configure(nil, LevelVerbose)

	a := &capturePrinter{}
	restoreA := SetPrinter(a)
	b := &capturePrinter{}
	restoreB := SetPrinter(b)

	// Restoring a out of order removes only a's entry; b stays active so its
	// still-open pipeline keeps receiving diagnostics.
	restoreA()
	Decision(context.Background(), "to-b")
	// restoreA is idempotent — a second call finds no matching entry.
	restoreA()

	restoreB()
	// Both gone → falls through (no fallback writer set → no-op, no panic).
	Decision(context.Background(), "to-nowhere")

	if got := b.snapshot(); len(got) != 1 || got[0] != "to-b" {
		t.Fatalf("b = %v, want [to-b]", got)
	}
	if got := a.snapshot(); len(got) != 0 {
		t.Fatalf("a should have received nothing after its restore, got %v", got)
	}
}

func TestFormatCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"simple", []string{"docker", "ps"}, "docker ps"},
		{"empty-arg", []string{"echo", ""}, "echo ''"},
		{"space", []string{"sh", "-c", "echo hi"}, "sh -c 'echo hi'"},
		{"single-quote", []string{"echo", "it's"}, `echo 'it'\''s'`},
		{"special-chars", []string{"sh", "-c", "a|b&c"}, "sh -c 'a|b&c'"},
		{"dollar", []string{"echo", "$HOME"}, "echo '$HOME'"},
		{"equals", []string{"env", "A=B"}, "env 'A=B'"},
		{"empty-list", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatCommand(tt.args); got != tt.want {
				t.Fatalf("FormatCommand(%q) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestNilWriterAndNilPrinterSafe(t *testing.T) {
	reset(t)
	Configure(nil, LevelDebug)
	// No printer, nil writer: must not panic.
	Command(context.Background(), "docker", "ps")
	Decision(context.Background(), "x")
	Debugf(context.Background(), "y")

	// nil ctx must not panic in printerFrom. A typed-nil variable exercises the
	// ctx == nil branch without tripping staticcheck SA1012 (nil-literal ctx).
	var nilCtx context.Context
	if p := printerFrom(nilCtx); p != nil {
		t.Fatalf("expected nil printer, got %v", p)
	}
}

func TestConcurrentEmitWithCtxPrinter(t *testing.T) {
	reset(t)
	Configure(nil, LevelDebug)

	global := &capturePrinter{}
	restore := SetPrinter(global)
	defer restore()

	const goroutines = 16
	const perG = 50
	var wg sync.WaitGroup
	printers := make([]*capturePrinter, goroutines)
	for i := range goroutines {
		printers[i] = &capturePrinter{}
		p := printers[i]
		wg.Go(func() {
			ctx := WithLinePrinter(context.Background(), p)
			for range perG {
				Command(ctx, "docker", "ps")
				Debugf(ctx, "tick")
			}
		})
	}
	// Concurrently emit to the global printer too.
	wg.Go(func() {
		for range perG {
			Decision(context.Background(), "global-tick")
		}
	})
	wg.Wait()

	for i, p := range printers {
		if got := len(p.snapshot()); got != perG*2 {
			t.Fatalf("printer[%d] got %d lines, want %d", i, got, perG*2)
		}
	}
	if got := len(global.snapshot()); got != perG {
		t.Fatalf("global got %d lines, want %d", got, perG)
	}
}

// TestConcurrentSetPrinterNoFallthrough models parallel test scenarios: each
// registers its own global printer, emits, then restores in an arbitrary order.
// A sibling's restore must never drop another sibling's still-active printer, so
// no line may leak to the fallback writer while any printer is registered.
func TestConcurrentSetPrinterNoFallthrough(t *testing.T) {
	reset(t)
	var leaked bytes.Buffer
	Configure(&safeWriter{w: &leaked}, LevelVerbose)

	const goroutines = 16
	const perG = 50
	var wg sync.WaitGroup
	captured := make([]*capturePrinter, goroutines)
	for i := range goroutines {
		captured[i] = &capturePrinter{}
		p := captured[i]
		wg.Go(func() {
			restore := SetPrinter(p)
			for range perG {
				Decision(context.Background(), "tick")
			}
			restore()
		})
	}
	wg.Wait()

	// Every emitted line must have landed on some registered printer, never the
	// fallback: an out-of-order restore that truncated the stack would leak here.
	if got := leaked.Len(); got != 0 {
		t.Fatalf("fallback writer received %d bytes; a sibling printer was dropped", got)
	}
	total := 0
	for _, p := range captured {
		total += len(p.snapshot())
	}
	if total != goroutines*perG {
		t.Fatalf("captured %d lines total, want %d", total, goroutines*perG)
	}
}

// safeWriter serializes concurrent writes to the wrapped buffer so the
// fallback-leak assertion above is race-free.
type safeWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (s *safeWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
