package pipeline

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// TestPlainReporterRoutesTraceThroughLiveView asserts that while a pipeline is
// active, trace emits route through the reporter's LiveLine (the safe screen
// path) rather than the configured fallback writer, and that FinishPipeline
// pops the printer so later emits fall back to stderr.
func TestPlainReporterRoutesTraceThroughLiveView(t *testing.T) {
	var fallback bytes.Buffer
	trace.Configure(&fallback, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	r, buf := newBufReporter()
	r.StartPipeline("deploy", 0)

	trace.Command(context.Background(), "docker", "ps")

	if fallback.Len() != 0 {
		t.Fatalf("trace leaked to fallback while pipeline active: %q", fallback.String())
	}
	if got := stripANSI(buf.String()); !strings.Contains(got, "$ docker ps") {
		t.Fatalf("trace did not route through live view: %q", got)
	}

	// FinishPipeline pops the printer; later emits fall back to stderr.
	r.FinishPipeline(true)
	buf.Reset()
	fallback.Reset()
	trace.Decision(context.Background(), "after pipeline")
	if buf.Len() != 0 {
		t.Fatalf("trace still routed to live view after finish: %q", buf.String())
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

	r, buf := newBufReporter()
	r.StartPipeline("deploy", 0)
	r.FinishPipeline(false)

	buf.Reset()
	trace.Decision(context.Background(), "post-failure")
	if buf.Len() != 0 {
		t.Fatalf("trace still routed to live view after failed finish: %q", buf.String())
	}
	if !strings.Contains(fallback.String(), "post-failure") {
		t.Fatalf("trace did not fall back after failed finish: %q", fallback.String())
	}
}

// TestPlainReporterNestedTraceRestore asserts nested pipelines save/restore the
// global trace printer correctly: inner emits route to the inner reporter, and
// after the inner pipeline finishes, outer emits route back to the outer one.
func TestPlainReporterNestedTraceRestore(t *testing.T) {
	var fallback bytes.Buffer
	trace.Configure(&fallback, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	rA, bufA := newBufReporter()
	rB, bufB := newBufReporter()

	rA.StartPipeline("deploy", 0)
	rB.StartPipeline("nested", 0)

	trace.Decision(context.Background(), "inner")
	if !strings.Contains(stripANSI(bufB.String()), "inner") {
		t.Fatalf("inner emit not routed to nested reporter: %q", bufB.String())
	}
	if strings.Contains(bufA.String(), "inner") {
		t.Fatalf("inner emit leaked to outer reporter: %q", bufA.String())
	}

	rB.FinishPipeline(true)
	bufA.Reset()
	bufB.Reset()
	trace.Decision(context.Background(), "outer")
	if !strings.Contains(stripANSI(bufA.String()), "outer") {
		t.Fatalf("outer emit not routed to outer reporter after nested finish: %q", bufA.String())
	}
	if strings.Contains(bufB.String(), "outer") {
		t.Fatalf("outer emit leaked to nested reporter: %q", bufB.String())
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
