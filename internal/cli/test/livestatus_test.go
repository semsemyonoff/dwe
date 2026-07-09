package test

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/workflow/envtest"
	"github.com/semsemyonoff/dwe/internal/shared/liveui"
)

var ansiTestRe = regexp.MustCompile("\x1b\\[[0-9;?]*[a-zA-Z]")

// stripANSI drops CSI escape sequences so tests can assert on the visible text
// of a mixed cursor-control + content stream.
func stripANSI(s string) string { return ansiTestRe.ReplaceAllString(s, "") }

// lastLineContaining returns the last line of text that contains sub, or "".
// The redraw stream repaints rows repeatedly; the last occurrence is the most
// recent frame's rendering of that row.
func lastLineContaining(text, sub string) string {
	var found string
	for line := range strings.SplitSeq(text, "\n") {
		if strings.Contains(line, sub) {
			found = line
		}
	}
	return found
}

// newTestDisplay builds a TTY-mode display with deterministic width/height and
// the no-ticker LiveLine hook, writing frames to `out` and warnings to `diag`.
func newTestDisplay(t *testing.T, names []string, height int) (*runLiveStatus, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, diag bytes.Buffer
	termSize := func() (int, int) { return 80, height }
	d := newRunLiveStatus(names, true, termSize, &out, &diag)
	d.live.SetTestHooks(true, func() int { return 80 })
	d.start()
	return d, &out, &diag
}

func TestRunLiveStatus_InitialPendingRows(t *testing.T) {
	d, out, _ := newTestDisplay(t, []string{"alpha", "beta"}, 24)
	defer d.Close()

	text := stripANSI(out.String())
	if !strings.Contains(text, "alpha") || !strings.Contains(text, "beta") {
		t.Fatalf("expected both scenario names seeded as pending rows, got:\n%s", text)
	}
	// The pending row lines carry the gray dot glyph and NO elapsed bracket
	// (the footer legitimately has its own stopwatch bracket — check the rows).
	for _, name := range []string{"alpha", "beta"} {
		row := lastLineContaining(text, name)
		if !strings.Contains(row, "·") {
			t.Errorf("expected pending dot glyph on row %q, got %q", name, row)
		}
		if strings.Contains(row, "[") {
			t.Errorf("pending row %q must not show an elapsed bracket, got %q", name, row)
		}
	}
	// Footer counter seeded at 0 running.
	if !strings.Contains(text, "running 0/2 scenarios") {
		t.Errorf("expected initial footer counter, got:\n%s", text)
	}
}

func TestRunLiveStatus_PhaseTransitionRelabel(t *testing.T) {
	d, out, _ := newTestDisplay(t, []string{"alpha"}, 24)
	defer d.Close()

	d.Started(0)
	out.Reset()
	d.Phase(0, envtest.PhaseDeploying)

	text := stripANSI(out.String())
	if !strings.Contains(text, "alpha") || !strings.Contains(text, "deploying…") {
		t.Errorf("expected row relabeled to the deploying phase, got:\n%s", text)
	}
	// Running rows show the elapsed bracket (stopwatch started).
	if !strings.Contains(text, "[") {
		t.Errorf("expected an elapsed bracket on a running row, got:\n%s", text)
	}
}

func TestRunLiveStatus_Finalization(t *testing.T) {
	d, out, _ := newTestDisplay(t, []string{"pass-me", "fail-me"}, 24)
	defer d.Close()

	d.Started(0)
	d.Started(1)
	out.Reset()
	d.Finished(0, scenarioOutcome{Name: "pass-me", Status: envtest.StatusPassed})
	d.Finished(1, scenarioOutcome{Name: "fail-me", Status: envtest.StatusFailed, FailedStep: "app answers"})

	text := stripANSI(out.String())
	if !strings.Contains(text, "✓") {
		t.Errorf("expected a done glyph for the passed scenario, got:\n%s", text)
	}
	if !strings.Contains(text, "pass-me  passed") {
		t.Errorf("expected passed label, got:\n%s", text)
	}
	if !strings.Contains(text, "✗") {
		t.Errorf("expected a failed glyph for the failed scenario, got:\n%s", text)
	}
	if !strings.Contains(text, `fail-me  failed — step "app answers"`) {
		t.Errorf("expected failed-step label, got:\n%s", text)
	}
}

func TestRunLiveStatus_ErrorStatusLabel(t *testing.T) {
	// A prep StatusError (e.g. flock held / kept prior run) must render as
	// "error" in the block row, matching the flat/overflow line and the final
	// text report — never collapse to "failed".
	d, out, _ := newTestDisplay(t, []string{"prep-fail"}, 24)
	defer d.Close()

	d.Started(0)
	out.Reset()
	d.Finished(0, scenarioOutcome{Name: "prep-fail", Status: envtest.StatusError, Message: "flock held"})

	text := stripANSI(out.String())
	if !strings.Contains(text, "prep-fail  error") {
		t.Errorf("expected error label in the block row, got:\n%s", text)
	}
	if strings.Contains(text, "prep-fail  failed") {
		t.Errorf("StatusError must not render as failed, got:\n%s", text)
	}

	// The overflow/flat path (isRow false) already surfaces the raw status; keep
	// the two in agreement.
	if kind, label := finalStatusLabel(scenarioOutcome{Name: "x", Status: envtest.StatusError}); kind != liveui.BlockRowFailed || label != "x  error" {
		t.Errorf("finalStatusLabel(error) = %v, %q; want BlockRowFailed, \"x  error\"", kind, label)
	}
}

func TestRunLiveStatus_FooterCounter(t *testing.T) {
	d, out, _ := newTestDisplay(t, []string{"a", "b"}, 24)
	defer d.Close()

	d.Started(0)
	out.Reset()
	d.Started(1)
	if text := stripANSI(out.String()); !strings.Contains(text, "running 2/2 scenarios") {
		t.Errorf("expected 2 running after both started, got:\n%s", text)
	}

	out.Reset()
	d.Finished(0, scenarioOutcome{Name: "a", Status: envtest.StatusPassed})
	if text := stripANSI(out.String()); !strings.Contains(text, "running 1/2 scenarios") {
		t.Errorf("expected 1 running after one finished, got:\n%s", text)
	}
}

func TestRunLiveStatus_HeightClampOverflowFramed(t *testing.T) {
	// height 6 → visibleRows = min(5, 6-3) = 3, so scenarios d,e overflow.
	names := []string{"a", "b", "c", "d", "e"}
	d, out, _ := newTestDisplay(t, names, 6)
	defer d.Close()

	if d.visibleRows != 3 {
		t.Fatalf("visibleRows = %d, want 3", d.visibleRows)
	}

	out.Reset()
	d.Started(3) // overflow → framed Println line
	if text := stripANSI(out.String()); !strings.Contains(text, "scenario d: started") {
		t.Errorf("expected framed start line for overflow scenario, got:\n%s", text)
	}

	out.Reset()
	d.Finished(4, scenarioOutcome{Name: "e", Status: envtest.StatusPassed})
	if text := stripANSI(out.String()); !strings.Contains(text, "scenario e: passed") {
		t.Errorf("expected framed final line for overflow scenario, got:\n%s", text)
	}
}

func TestRunLiveStatus_NonTTYFlatLinesEveryScenario(t *testing.T) {
	var out, diag bytes.Buffer
	termSize := func() (int, int) { return 80, 24 }
	// isTTY=false → disabled LiveLine; every scenario must still emit flat lines.
	d := newRunLiveStatus([]string{"a", "b"}, false, termSize, &out, &diag)
	d.start()
	defer d.Close()

	d.Started(0)
	d.Started(1)
	d.Phase(0, envtest.PhaseDeploying) // must produce nothing in disabled mode
	d.Finished(0, scenarioOutcome{Name: "a", Status: envtest.StatusPassed})
	d.Finished(1, scenarioOutcome{Name: "b", Status: envtest.StatusFailed, FailedStep: "x"})

	got := out.String()
	for _, want := range []string{
		"scenario a: started\n",
		"scenario b: started\n",
		"scenario a: passed\n",
		"scenario b: failed\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("disabled mode missing flat line %q, got:\n%s", want, got)
		}
	}
	// No per-phase noise in the flat fallback.
	if strings.Contains(got, "deploying") {
		t.Errorf("disabled mode must not emit per-phase lines, got:\n%s", got)
	}
}

func TestRunLiveStatus_WarnRoutesToDiag(t *testing.T) {
	d, out, diag := newTestDisplay(t, []string{"alpha"}, 24)
	defer d.Close()

	d.Started(0)
	out.Reset()
	d.Warn(0, "something odd")

	if !strings.Contains(diag.String(), "[alpha] warning: something odd") {
		t.Errorf("expected warning on the diag writer, got diag:\n%s", diag.String())
	}
	if strings.Contains(stripANSI(out.String()), "warning") {
		t.Errorf("warning bytes must not land on the screen writer, got out:\n%s", stripANSI(out.String()))
	}
}

func TestPhaseLabel_FallbackForUnknown(t *testing.T) {
	if got := phaseLabel(envtest.PhaseValidating); got != "validating…" {
		t.Errorf("phaseLabel(validating) = %q", got)
	}
	if got := phaseLabel(envtest.ProgressPhase("mystery")); got != "mystery" {
		t.Errorf("unknown phase must fall back to the raw enum, got %q", got)
	}
}
