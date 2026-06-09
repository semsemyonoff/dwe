package pipeline

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// newDiagReporter returns a buffer-backed reporter whose diagnostics writer is
// captured separately from the screen, so trace-routing tests can assert that
// diagnostic lines land on the diag (stderr) channel and never on the screen
// (stdout).
func newDiagReporter() (r *PlainReporter, screen, diag *bytes.Buffer) {
	r, screen = newBufReporter()
	diag = &bytes.Buffer{}
	r.live.SetDiagWriter(diag)
	return r, screen, diag
}

// TestPlainReporterRoutesTraceThroughLiveView asserts that while a pipeline is
// active, trace emits route through the reporter's LiveLine framing onto the
// DIAGNOSTICS writer (stderr), never the screen (stdout) and never the
// configured fallback writer, and that FinishPipeline pops the printer so later
// emits fall back to stderr.
func TestPlainReporterRoutesTraceThroughLiveView(t *testing.T) {
	var fallback bytes.Buffer
	trace.Configure(&fallback, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	r, screen, diag := newDiagReporter()
	r.StartPipeline("deploy", 0)

	trace.Command(context.Background(), "docker", "ps")

	if fallback.Len() != 0 {
		t.Fatalf("trace leaked to fallback while pipeline active: %q", fallback.String())
	}
	if screen.Len() != 0 {
		t.Fatalf("trace leaked to the screen (stdout) while pipeline active: %q", screen.String())
	}
	if got := stripANSI(diag.String()); !strings.Contains(got, "$ docker ps") {
		t.Fatalf("trace did not route to the diagnostics writer: %q", got)
	}

	// FinishPipeline pops the printer; later emits fall back to stderr.
	r.FinishPipeline(true)
	diag.Reset()
	fallback.Reset()
	trace.Decision(context.Background(), "after pipeline")
	if diag.Len() != 0 {
		t.Fatalf("trace still routed to live view after finish: %q", diag.String())
	}
	if !strings.Contains(fallback.String(), "after pipeline") {
		t.Fatalf("trace did not fall back to configured writer: %q", fallback.String())
	}
}

// TestPlainReporterFinishPipelineFailureRestoresTrace asserts the trace printer
// is popped even on the failure path (FinishPipeline returns early on !success).
func TestPlainReporterFinishPipelineFailureRestoresTrace(t *testing.T) {
	var fallback bytes.Buffer
	trace.Configure(&fallback, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	r, _, diag := newDiagReporter()
	r.StartPipeline("deploy", 0)
	r.FinishPipeline(false)

	diag.Reset()
	trace.Decision(context.Background(), "post-failure")
	if diag.Len() != 0 {
		t.Fatalf("trace still routed to live view after failed finish: %q", diag.String())
	}
	if !strings.Contains(fallback.String(), "post-failure") {
		t.Fatalf("trace did not fall back after failed finish: %q", fallback.String())
	}
}

// TestPlainReporterNestedTraceRestore asserts nested pipelines save/restore the
// global trace printer correctly: inner emits route to the inner reporter's
// diagnostics writer, and after the inner pipeline finishes, outer emits route
// back to the outer one.
func TestPlainReporterNestedTraceRestore(t *testing.T) {
	var fallback bytes.Buffer
	trace.Configure(&fallback, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	rA, _, diagA := newDiagReporter()
	rB, _, diagB := newDiagReporter()

	rA.StartPipeline("deploy", 0)
	rB.StartPipeline("nested", 0)

	trace.Decision(context.Background(), "inner")
	if !strings.Contains(stripANSI(diagB.String()), "inner") {
		t.Fatalf("inner emit not routed to nested reporter: %q", diagB.String())
	}
	if strings.Contains(diagA.String(), "inner") {
		t.Fatalf("inner emit leaked to outer reporter: %q", diagA.String())
	}

	rB.FinishPipeline(true)
	diagA.Reset()
	diagB.Reset()
	trace.Decision(context.Background(), "outer")
	if !strings.Contains(stripANSI(diagA.String()), "outer") {
		t.Fatalf("outer emit not routed to outer reporter after nested finish: %q", diagA.String())
	}
	if strings.Contains(diagB.String(), "outer") {
		t.Fatalf("outer emit leaked to nested reporter: %q", diagB.String())
	}

	rA.FinishPipeline(true)
}

// TestWriterLinePrinterCtxAttribution asserts that a per-sub-step ctx printer
// (the mechanism executeStepBody attaches in parallel mode) wins over the
// global pipeline printer and the configured fallback.
func TestWriterLinePrinterCtxAttribution(t *testing.T) {
	var fallback bytes.Buffer
	trace.Configure(&fallback, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	var global bytes.Buffer
	restore := trace.SetPrinter(writerLinePrinter{w: &global})
	defer restore()

	var sub bytes.Buffer
	ctx := trace.WithLinePrinter(context.Background(), writerLinePrinter{w: &sub})

	trace.Command(ctx, "docker", "stop", "web")

	if !strings.Contains(sub.String(), "$ docker stop web") {
		t.Fatalf("ctx printer did not receive emit: %q", sub.String())
	}
	if global.Len() != 0 {
		t.Fatalf("emit leaked to global printer despite ctx override: %q", global.String())
	}
	if fallback.Len() != 0 {
		t.Fatalf("emit leaked to fallback despite ctx override: %q", fallback.String())
	}
}

// TestWriterLinePrinterParallelAttribution exercises concurrent sub-steps: each
// goroutine carries its own ctx printer, so emits attribute to the right writer
// with no interleaving. Run under -race to catch shared-state bugs.
func TestWriterLinePrinterParallelAttribution(t *testing.T) {
	trace.Configure(nil, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	// A global printer baseline that should never be hit when ctx is present.
	var global bytes.Buffer
	var globalMu sync.Mutex
	restore := trace.SetPrinter(lockedPrinter{b: &global, mu: &globalMu})
	defer restore()

	const subs = 8
	bufs := make([]*bytes.Buffer, subs)
	var wg sync.WaitGroup
	for i := range subs {
		bufs[i] = &bytes.Buffer{}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx := trace.WithLinePrinter(context.Background(), writerLinePrinter{w: bufs[idx]})
			trace.Command(ctx, "echo", "sub", string(rune('a'+idx)))
		}(i)
	}
	wg.Wait()

	for i := range subs {
		want := "$ echo sub " + string(rune('a'+i))
		if !strings.Contains(bufs[i].String(), want) {
			t.Fatalf("sub %d missing its own echo: %q", i, bufs[i].String())
		}
	}
	if global.Len() != 0 {
		t.Fatalf("ctx-attributed emits leaked to the global printer: %q", global.String())
	}
}

// lockedPrinter is a concurrency-safe capture printer for the parallel test's
// global baseline (which must stay empty).
type lockedPrinter struct {
	b  *bytes.Buffer
	mu *sync.Mutex
}

func (p lockedPrinter) PrintLine(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.b.WriteString(s + "\n")
}
