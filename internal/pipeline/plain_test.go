package pipeline

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"
)

// fixedTime is the clock injected into the test reporter so timestamp
// prefixes are deterministic.
var fixedTime = time.Date(2026, 5, 14, 22, 36, 36, 0, time.UTC)

// newBufReporter returns a PlainReporter backed by a buffer for assertions.
// The reporter uses a fixed clock so timestamp prefixes are deterministic.
func newBufReporter() (*PlainReporter, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	w := render.NewWriter(buf)
	// termOut=io.Discard means LiveLine is disabled so byte-for-byte
	// assertions on `buf` still hold. logFile=nil means side-writes no-op.
	r := NewPlainReporter(w, nil, io.Discard)
	r.now = func() time.Time { return fixedTime }
	return r, buf
}

// stripANSI removes ANSI escape sequences from s.
func stripANSI(s string) string {
	out := s
	for {
		start := strings.Index(out, "\033[")
		if start == -1 {
			break
		}
		end := strings.Index(out[start:], "m")
		if end == -1 {
			break
		}
		out = out[:start] + out[start+end+1:]
	}
	return out
}

// timestampPrefixRe matches the "[YY-MM-DD HH:MM:SS] " line prefix emitted
// by PlainReporter (post-stripANSI).
var timestampPrefixRe = regexp.MustCompile(`(?m)^\[\d{2}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\] `)

// stripTimestamps removes the "[YY-MM-DD HH:MM:SS] " prefix from every line in s.
// Use after stripANSI for content assertions that don't care about the clock.
func stripTimestamps(s string) string {
	return timestampPrefixRe.ReplaceAllString(s, "")
}

// clean strips ANSI escapes and timestamp prefixes — the canonical
// pre-assertion pipeline for content tests.
func clean(s string) string {
	return stripTimestamps(stripANSI(s))
}

// lines splits s into non-empty trimmed lines for readable assertions.
func lines(s string) []string {
	var result []string
	for l := range strings.SplitSeq(s, "\n") {
		if l != "" {
			result = append(result, l)
		}
	}
	return result
}

// --- StartPipeline ---

func TestPlainReporter_StartPipeline_NoOutput(t *testing.T) {
	r, buf := newBufReporter()
	r.StartPipeline("deploy", 5)
	if buf.Len() != 0 {
		t.Errorf("StartPipeline should produce no output, got: %q", buf.String())
	}
}

// --- EnterPhase ---

func TestPlainReporter_EnterPhase_WithDescription(t *testing.T) {
	r, buf := newBufReporter()
	phase := config.DeployPhase{Name: "init", Description: "Initialization"}
	r.EnterPhase("init", phase)
	got := clean(buf.String())
	want := "Phase: init: Initialization\n"
	if got != want {
		t.Errorf("EnterPhase with description: got %q, want %q", got, want)
	}
}

func TestPlainReporter_EnterPhase_NoDescription(t *testing.T) {
	r, buf := newBufReporter()
	phase := config.DeployPhase{Name: "deploy"}
	r.EnterPhase("deploy", phase)
	got := clean(buf.String())
	want := "Phase: deploy\n"
	if got != want {
		t.Errorf("EnterPhase no description: got %q, want %q", got, want)
	}
}

func TestPlainReporter_EnterPhase_ServicePrefix(t *testing.T) {
	r, buf := newBufReporter()
	phase := config.DeployPhase{Name: "setup", Description: "Service setup"}
	r.EnterPhase("main/setup", phase)
	got := clean(buf.String())
	want := "Phase: main/setup: Service setup\n"
	if got != want {
		t.Errorf("EnterPhase with service prefix: got %q, want %q", got, want)
	}
}

// --- SkipPhase ---

func TestPlainReporter_SkipPhase(t *testing.T) {
	r, buf := newBufReporter()
	phase := config.DeployPhase{Name: "deploy"}
	r.SkipPhase("deploy", phase, "when: dir-empty services/main")
	got := clean(buf.String())
	want := "  Skipping phase deploy (when: dir-empty services/main)\n"
	if got != want {
		t.Errorf("SkipPhase: got %q, want %q", got, want)
	}
}

// --- StartStep ---

func TestPlainReporter_StartStep_WithDescription(t *testing.T) {
	r, buf := newBufReporter()
	step := config.DeployStep{Name: "render-env", Description: "Generate .env from config"}
	r.StartStep("init/render-env", step, 1, 5)
	got := clean(buf.String())
	want := "  · [1/5] init/render-env: Generate .env from config\n"
	if got != want {
		t.Errorf("StartStep with description: got %q, want %q", got, want)
	}
}

func TestPlainReporter_StartStep_NoDescription(t *testing.T) {
	r, buf := newBufReporter()
	step := config.DeployStep{Name: "migrate"}
	r.StartStep("main/setup/migrate", step, 3, 7)
	got := clean(buf.String())
	want := "  · [3/7] main/setup/migrate\n"
	if got != want {
		t.Errorf("StartStep no description: got %q, want %q", got, want)
	}
}

// --- SkipStep ---

func TestPlainReporter_SkipStep_WhenCondition(t *testing.T) {
	r, buf := newBufReporter()
	step := config.DeployStep{Name: "migrate"}
	r.SkipStep("init/migrate", step, 2, 4, "when: dir-empty services/main/src")
	got := clean(buf.String())
	want := "  ◎ [2/4] Skipped: init/migrate (when: dir-empty services/main/src)\n"
	if got != want {
		t.Errorf("SkipStep: got %q, want %q", got, want)
	}
}

func TestPlainReporter_SkipStep_PhaseWhenCondition(t *testing.T) {
	r, buf := newBufReporter()
	step := config.DeployStep{Name: "key-gen"}
	r.SkipStep("main/setup/key-gen", step, 3, 5, "phase when: cmd: check")
	got := clean(buf.String())
	want := "  ◎ [3/5] Skipped: main/setup/key-gen (phase when: cmd: check)\n"
	if got != want {
		t.Errorf("SkipStep with phase when: got %q, want %q", got, want)
	}
}

// --- FinishStep ---

func TestPlainReporter_FinishStep(t *testing.T) {
	r, buf := newBufReporter()
	step := config.DeployStep{Name: "render-env"}
	r.FinishStep("init/render-env", step, 1, 5)
	got := clean(buf.String())
	want := "  ✓ [1/5] Done: init/render-env\n"
	if got != want {
		t.Errorf("FinishStep: got %q, want %q", got, want)
	}
}

// --- FailStep ---

func TestPlainReporter_FailStep(t *testing.T) {
	r, buf := newBufReporter()
	r.StartPipeline("deploy", 7)
	step := config.DeployStep{Name: "migrate"}
	r.FailStep("main/setup/migrate", step, 4, 7, errors.New("exit status 1"))
	got := clean(buf.String())
	wantLines := []string{
		`✗ Deploy failed at step "main/setup/migrate"`,
		"  exit status 1",
	}
	gotLines := lines(got)
	if len(gotLines) != len(wantLines) {
		t.Fatalf("FailStep: got %d lines, want %d; output: %q", len(gotLines), len(wantLines), got)
	}
	for i, want := range wantLines {
		if gotLines[i] != want {
			t.Errorf("FailStep line %d: got %q, want %q", i, gotLines[i], want)
		}
	}
}

func TestPlainReporter_FailStep_Reset(t *testing.T) {
	r, buf := newBufReporter()
	r.StartPipeline("reset", 3)
	step := config.DeployStep{Name: "stop"}
	r.FailStep("cleanup/stop", step, 2, 3, errors.New("exit status 2"))
	got := clean(buf.String())
	wantLines := []string{
		`✗ Reset failed at step "cleanup/stop"`,
		"  exit status 2",
	}
	gotLines := lines(got)
	if len(gotLines) != len(wantLines) {
		t.Fatalf("FailStep reset: got %d lines, want %d; output: %q", len(gotLines), len(wantLines), got)
	}
	for i, want := range wantLines {
		if gotLines[i] != want {
			t.Errorf("FailStep reset line %d: got %q, want %q", i, gotLines[i], want)
		}
	}
}

func TestPlainReporter_FailStep_NoStartPipeline(t *testing.T) {
	r, buf := newBufReporter()
	step := config.DeployStep{Name: "migrate"}
	r.FailStep("init/migrate", step, 1, 1, errors.New("timeout"))
	got := clean(buf.String())
	wantLines := []string{
		`✗ Pipeline failed at step "init/migrate"`,
		"  timeout",
	}
	gotLines := lines(got)
	if len(gotLines) != len(wantLines) {
		t.Fatalf("FailStep no name: got %d lines, want %d; output: %q", len(gotLines), len(wantLines), got)
	}
	for i, want := range wantLines {
		if gotLines[i] != want {
			t.Errorf("FailStep no name line %d: got %q, want %q", i, gotLines[i], want)
		}
	}
}

// --- FinishPipeline ---

func TestPlainReporter_FinishPipeline_Success_PrintsDone(t *testing.T) {
	r, buf := newBufReporter()
	r.StartPipeline("deploy", 3)
	r.FinishPipeline(true)
	got := clean(buf.String())
	// StartPipeline produces no output; only FinishPipeline does.
	gotLines := lines(got)
	if len(gotLines) != 1 {
		t.Fatalf("FinishPipeline(true) should produce 1 line, got %d: %q", len(gotLines), got)
	}
	if !strings.Contains(gotLines[0], "✓ Done") {
		t.Errorf("FinishPipeline(true) line should contain '✓ Done', got: %q", gotLines[0])
	}
	if !strings.Contains(gotLines[0], "s)") {
		t.Errorf("FinishPipeline(true) line should contain elapsed time ending in 's)', got: %q", gotLines[0])
	}
}

func TestPlainReporter_FinishPipeline_Failure_NoOutput(t *testing.T) {
	r, buf := newBufReporter()
	r.StartPipeline("deploy", 3)
	r.FinishPipeline(false)
	// Only StartPipeline output (none) — FinishPipeline(false) is silent.
	if buf.Len() != 0 {
		t.Errorf("FinishPipeline(false) should produce no output, got: %q", buf.String())
	}
}

func TestPlainReporter_FinishPipeline_NoStartPipeline(t *testing.T) {
	r, buf := newBufReporter()
	// Even without StartPipeline, success still prints Done (startTime is zero → large elapsed, but valid).
	r.FinishPipeline(true)
	got := clean(buf.String())
	if !strings.Contains(got, "✓ Done") {
		t.Errorf("FinishPipeline without StartPipeline should still print Done, got: %q", got)
	}
}

// --- Full event sequence ---

func TestPlainReporter_FullEventSequence(t *testing.T) {
	r, buf := newBufReporter()

	phase1 := config.DeployPhase{Name: "env", Description: "Environment"}
	phase2 := config.DeployPhase{Name: "setup"}
	phase3 := config.DeployPhase{Name: "post-deploy"}

	step1 := config.DeployStep{Name: "render-env", Description: "Generate .env from config"}
	step2 := config.DeployStep{Name: "dirs-ensure"}
	step3 := config.DeployStep{Name: "migrate"}
	step4 := config.DeployStep{Name: "success"}

	r.StartPipeline("deploy", 4)

	r.EnterPhase("env", phase1)
	r.StartStep("env/render-env", step1, 1, 4)
	r.FinishStep("env/render-env", step1, 1, 4)

	r.EnterPhase("main/setup", phase2)
	r.StartStep("main/setup/dirs-ensure", step2, 2, 4)
	r.FinishStep("main/setup/dirs-ensure", step2, 2, 4)
	r.StartStep("main/setup/migrate", step3, 3, 4)
	r.SkipStep("main/setup/migrate", step3, 3, 4, "when: dir-not-empty services/main/src")

	r.EnterPhase("post-deploy", phase3)
	r.StartStep("post-deploy/success", step4, 4, 4)
	r.FinishStep("post-deploy/success", step4, 4, 4)

	r.FinishPipeline(true)

	got := clean(buf.String())
	wantLines := []string{
		"Phase: env: Environment",
		"  · [1/4] env/render-env: Generate .env from config",
		"  ✓ [1/4] Done: env/render-env",
		"Phase: main/setup",
		"  · [2/4] main/setup/dirs-ensure",
		"  ✓ [2/4] Done: main/setup/dirs-ensure",
		"  · [3/4] main/setup/migrate",
		"  ◎ [3/4] Skipped: main/setup/migrate (when: dir-not-empty services/main/src)",
		"Phase: post-deploy",
		"  · [4/4] post-deploy/success",
		"  ✓ [4/4] Done: post-deploy/success",
	}
	gotLines := lines(got)
	// Last line is the "✓ Done (Xs)" summary — check it separately.
	if len(gotLines) < len(wantLines)+1 {
		t.Fatalf("FullEventSequence: got %d lines, want at least %d\ngot:\n%s",
			len(gotLines), len(wantLines)+1,
			strings.Join(gotLines, "\n"),
		)
	}
	for i, want := range wantLines {
		if gotLines[i] != want {
			t.Errorf("line %d: got %q, want %q", i, gotLines[i], want)
		}
	}
	doneLine := gotLines[len(gotLines)-1]
	if !strings.Contains(doneLine, "✓ Done") {
		t.Errorf("last line should be Done summary, got: %q", doneLine)
	}
}

// --- Untracked suppression ---

func TestPlainReporter_EnterPhase_Untracked_Silent(t *testing.T) {
	r, buf := newBufReporter()
	phase := config.DeployPhase{Name: "post-deploy", Description: "Post-deploy tasks", Untracked: true}
	r.EnterPhase("post-deploy", phase)
	if buf.Len() != 0 {
		t.Errorf("EnterPhase untracked should produce no output, got: %q", buf.String())
	}
}

func TestPlainReporter_SkipPhase_Untracked_Silent(t *testing.T) {
	r, buf := newBufReporter()
	phase := config.DeployPhase{Name: "post-deploy", Untracked: true}
	r.SkipPhase("post-deploy", phase, "when: skip")
	if buf.Len() != 0 {
		t.Errorf("SkipPhase untracked should produce no output, got: %q", buf.String())
	}
}

func TestPlainReporter_StartStep_Untracked_Silent(t *testing.T) {
	r, buf := newBufReporter()
	step := config.DeployStep{Name: "notify", Description: "Send notification"}
	r.StartStep("post-deploy/notify", step, 0, 0)
	if buf.Len() != 0 {
		t.Errorf("StartStep untracked (0,0) should produce no output, got: %q", buf.String())
	}
}

func TestPlainReporter_FinishStep_Untracked_Silent(t *testing.T) {
	r, buf := newBufReporter()
	step := config.DeployStep{Name: "notify"}
	r.FinishStep("post-deploy/notify", step, 0, 0)
	if buf.Len() != 0 {
		t.Errorf("FinishStep untracked (0,0) should produce no output, got: %q", buf.String())
	}
}

func TestPlainReporter_SkipStep_Untracked_Silent(t *testing.T) {
	r, buf := newBufReporter()
	step := config.DeployStep{Name: "notify"}
	r.SkipStep("post-deploy/notify", step, 0, 0, "when: skip")
	if buf.Len() != 0 {
		t.Errorf("SkipStep untracked (0,0) should produce no output, got: %q", buf.String())
	}
}

func TestPlainReporter_FailStep_Untracked_StillPrints(t *testing.T) {
	r, buf := newBufReporter()
	r.StartPipeline("deploy", 0)
	step := config.DeployStep{Name: "notify"}
	r.FailStep("post-deploy/notify", step, 0, 0, errors.New("network error"))
	got := clean(buf.String())
	if !strings.Contains(got, "failed at step") {
		t.Errorf("FailStep untracked should still print failure, got: %q", got)
	}
}

func TestPlainReporter_FullEventSequence_WithUntracked(t *testing.T) {
	r, buf := newBufReporter()

	tracked := config.DeployPhase{Name: "setup", Description: "Setup"}
	untracked := config.DeployPhase{Name: "post-deploy", Untracked: true}

	step1 := config.DeployStep{Name: "migrate"}
	step2 := config.DeployStep{Name: "notify"}

	r.StartPipeline("deploy", 1)

	r.EnterPhase("setup", tracked)
	r.StartStep("setup/migrate", step1, 1, 1)
	r.FinishStep("setup/migrate", step1, 1, 1)

	// Untracked phase: all system output suppressed
	r.EnterPhase("post-deploy", untracked)
	r.StartStep("post-deploy/notify", step2, 0, 0)
	r.FinishStep("post-deploy/notify", step2, 0, 0)

	r.FinishPipeline(true)

	got := clean(buf.String())
	wantLines := []string{
		"Phase: setup: Setup",
		"  · [1/1] setup/migrate",
		"  ✓ [1/1] Done: setup/migrate",
	}
	gotLines := lines(got)
	// Last line is the "✓ Done (Xs)" summary — check at least wantLines + 1 lines total.
	if len(gotLines) < len(wantLines)+1 {
		t.Fatalf("FullEventSequence with untracked: got %d lines, want at least %d\ngot:\n%s",
			len(gotLines), len(wantLines)+1,
			strings.Join(gotLines, "\n"),
		)
	}
	for i, want := range wantLines {
		if gotLines[i] != want {
			t.Errorf("line %d: got %q, want %q", i, gotLines[i], want)
		}
	}
	doneLine := gotLines[len(gotLines)-1]
	if !strings.Contains(doneLine, "✓ Done") {
		t.Errorf("last line should be Done summary, got: %q", doneLine)
	}
}

// --- formatElapsed ---

func TestFormatElapsed_Seconds(t *testing.T) {
	got := formatElapsed(45 * time.Second)
	if got != "45s" {
		t.Errorf("formatElapsed(45s): got %q, want %q", got, "45s")
	}
}

func TestFormatElapsed_MinutesAndSeconds(t *testing.T) {
	got := formatElapsed(83 * time.Second)
	if got != "1m 23s" {
		t.Errorf("formatElapsed(83s): got %q, want %q", got, "1m 23s")
	}
}

func TestFormatElapsed_HoursAndMinutes(t *testing.T) {
	got := formatElapsed(2*time.Hour + 5*time.Minute + 30*time.Second)
	if got != "2h 5m" {
		t.Errorf("formatElapsed(2h5m30s): got %q, want %q", got, "2h 5m")
	}
}

// --- Timestamp prefix ---

// TestPlainReporter_TimestampPrefix_OnEveryLine asserts that every emitted
// step output line carries the gray "[YY-MM-DD HH:MM:SS] " prefix, including
// the per-step lines, skip lines, fail lines and the closing Done summary.
func TestPlainReporter_TimestampPrefix_OnEveryLine(t *testing.T) {
	r, buf := newBufReporter()
	wantPrefix := "[26-05-14 22:36:36] "

	r.StartPipeline("deploy", 2)
	r.EnterPhase("env", config.DeployPhase{Name: "env"})
	r.StartStep("env/render", config.DeployStep{Name: "render"}, 1, 2)
	r.FinishStep("env/render", config.DeployStep{Name: "render"}, 1, 2)
	r.SkipStep("env/skip", config.DeployStep{Name: "skip"}, 2, 2, "when: false")
	r.FailStep("env/fail", config.DeployStep{Name: "fail"}, 2, 2, errors.New("boom"))
	r.FinishPipeline(true)

	got := stripANSI(buf.String())
	for i, line := range lines(got) {
		if !strings.HasPrefix(line, wantPrefix) {
			t.Errorf("line %d missing timestamp prefix %q: %q", i, wantPrefix, line)
		}
	}
}

// TestPlainReporter_TimestampPrefix_GrayColor asserts that the timestamp is
// wrapped in the gray ANSI color codes — the visual differentiation from the
// colored body matters.
func TestPlainReporter_TimestampPrefix_GrayColor(t *testing.T) {
	r, buf := newBufReporter()
	r.EnterPhase("env", config.DeployPhase{Name: "env"})

	got := buf.String()
	wantPrefix := render.Gray + "[26-05-14 22:36:36]" + render.Reset + " "
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("output should start with gray-wrapped timestamp; got %q", got)
	}
}

// --- Group event stubs ---

func TestPlainReporter_StartGroup_PrintsHeader(t *testing.T) {
	r, buf := newBufReporter()
	r.StartGroup("init/dumps", config.DeployStep{Name: "dumps", Description: "download dumps"}, []int{1, 2, 3}, 5)
	got := stripTimestamps(stripANSI(buf.String()))
	want := "  · Parallel group: init/dumps: download dumps (3 steps)\n"
	if got != want {
		t.Errorf("StartGroup output\n got:  %q\n want: %q", got, want)
	}
}

func TestPlainReporter_FinishGroup_Success(t *testing.T) {
	r, buf := newBufReporter()
	r.FinishGroup("init/dumps", config.DeployStep{Name: "dumps"}, true)
	got := stripTimestamps(stripANSI(buf.String()))
	want := "  ✓ Parallel group done: init/dumps\n"
	if got != want {
		t.Errorf("FinishGroup(success) output\n got:  %q\n want: %q", got, want)
	}
}

func TestPlainReporter_FinishGroup_Failure(t *testing.T) {
	r, buf := newBufReporter()
	r.FinishGroup("init/dumps", config.DeployStep{Name: "dumps"}, false)
	got := stripTimestamps(stripANSI(buf.String()))
	want := "  ✗ Parallel group failed: init/dumps\n"
	if got != want {
		t.Errorf("FinishGroup(fail) output\n got:  %q\n want: %q", got, want)
	}
}

// Parallel sub-step StepOutput must buffer (not write directly) — the buffer
// is dumped between separator bars on FinishStep / FailStep.
func TestPlainReporter_StepOutput_ParallelSubStep_Buffers(t *testing.T) {
	r, buf := newBufReporter()
	group := parallelGroup("dumps", "main")
	r.StartGroup("init/dumps", group, []int{1}, 1)
	// After StartGroup, "init/dumps/main" is registered with a groupAddr; the
	// frame is buffered, not written directly.
	pre := buf.Len()
	r.StepOutput("init/main", "some line", true)
	if buf.Len() != pre {
		t.Errorf("parallel sub-step StepOutput must buffer, not write; new bytes: %q", buf.String()[pre:])
	}
}

// Sequential StepOutput writes the final frame directly to the screen so the
// user sees real-time output (in Task 7 this routes through LiveLine.Println).
func TestPlainReporter_StepOutput_Sequential_WritesDirectly(t *testing.T) {
	r, buf := newBufReporter()
	r.StepOutput("p/s", "line one", true)
	out := buf.String()
	if !strings.Contains(out, "line one") {
		t.Errorf("sequential final StepOutput should write to screen; got %q", out)
	}
}

// Sequential non-final frame is held in inProgress (display state only) and
// not committed to the screen until commitTrailingTail (via FinishStep) runs.
func TestPlainReporter_StepOutput_Sequential_NonFinalDeferred(t *testing.T) {
	r, buf := newBufReporter()
	r.StepOutput("p/s", "spin1", false)
	if buf.Len() != 0 {
		t.Errorf("non-final sequential StepOutput must not write directly; got %q", buf.String())
	}
	// FinishStep should flush the tail via commitTrailingTail.
	r.FinishStep("p/s", config.DeployStep{Name: "s"}, 1, 1)
	if !strings.Contains(buf.String(), "spin1") {
		t.Errorf("commitTrailingTail should flush leftover tail on FinishStep; got %q", buf.String())
	}
}

// TestPlainReporter_StepOutput_NonFinalFrames_NotCommitted verifies that
// in-progress `\r` frames update inProgress state but are NOT appended to the
// per-sub-step buffer. Only the final `\n`-terminated frame lands in the dump.
func TestPlainReporter_StepOutput_NonFinalFrames_NotCommitted(t *testing.T) {
	r, buf := newBufReporter()
	group := parallelGroup("dumps", "main")
	r.StartGroup("init/dumps", group, []int{1}, 1)

	// Progressive `\r` frames coalesce — only the final frame is committed.
	r.StepOutput("init/main", "0%", false)
	r.StepOutput("init/main", "50%", false)
	r.StepOutput("init/main", "100%", true)
	r.FinishStep("init/main", config.DeployStep{Name: "main"}, 1, 1)

	got := clean(buf.String())
	bodyLines := lines(got)
	// Locate "100%" as an exact body line; ensure "0%" and "50%" are absent.
	saw100, sawIntermediate := false, false
	for _, l := range bodyLines {
		switch l {
		case "100%":
			saw100 = true
		case "0%", "50%":
			sawIntermediate = true
		}
	}
	if !saw100 {
		t.Errorf("expected final frame '100%%' in dump:\n%s", got)
	}
	if sawIntermediate {
		t.Errorf("non-final frames should not be committed; got:\n%s", got)
	}
}

// TestPlainReporter_StepOutput_CommitTheRightFrame is the regression test for
// the commit-the-right-frame bug: feed `50%\r100%\n` semantics as two
// StepOutput calls — non-final 50%, then final 100% — and assert the buffer
// dump contains exactly `100%`, NOT `50%`.
func TestPlainReporter_StepOutput_CommitTheRightFrame(t *testing.T) {
	r, buf := newBufReporter()
	group := parallelGroup("dumps", "main")
	r.StartGroup("init/dumps", group, []int{1}, 1)
	r.StepOutput("init/main", "50%", false)
	r.StepOutput("init/main", "100%", true)
	r.FinishStep("init/main", config.DeployStep{Name: "main"}, 1, 1)

	got := clean(buf.String())
	bodyLines := lines(got)
	saw100, saw50 := false, false
	for _, l := range bodyLines {
		switch l {
		case "100%":
			saw100 = true
		case "50%":
			saw50 = true
		}
	}
	if !saw100 {
		t.Errorf("expected '100%%' in dump:\n%s", got)
	}
	if saw50 {
		t.Errorf("regression: committed the wrong frame; got:\n%s", got)
	}
}

// TestPlainReporter_FlushOutput_CommitsBodyTailBeforeCheckOutput is the
// regression test for the P1 finding: a body's trailing non-newline-terminated
// tail (stored as inProgress after tee.Flush) must survive into the output
// even when a subsequent check: step emits a final=true line that would
// otherwise wipe inProgress before commitTrailingTail sees it.
//
// Sequence mirrors what executeStepBody does:
//  1. body emits trailing tail → StepOutput(addr, tail, false)
//  2. tee.Flush implicit → FlushOutput(addr) commits the tail
//  3. check emits a committed line → StepOutput(addr, line, true)
//  4. FinishStep closes the step
func TestPlainReporter_FlushOutput_CommitsBodyTailBeforeCheckOutput(t *testing.T) {
	r, buf := newBufReporter()

	// Sequential step (no StartGroup → groupAddr == "").
	r.StepOutput("setup/build", "body-tail", false) // body trailing tail
	if buf.Len() != 0 {
		t.Fatalf("non-final frame must not write to screen immediately; got %q", buf.String())
	}

	r.FlushOutput("setup/build") // executor calls this between body and check
	if !strings.Contains(clean(buf.String()), "body-tail") {
		t.Errorf("FlushOutput must commit the body tail to screen; got %q", buf.String())
	}
	bufBeforeCheck := buf.String()

	// Check step emits a final line. Without the fix this would clear
	// inProgress before commitTrailingTail could save "body-tail".
	r.StepOutput("setup/build", "check-line", true)
	r.FinishStep("setup/build", config.DeployStep{Name: "build"}, 1, 1)

	got := clean(buf.String())
	if !strings.Contains(got, "body-tail") {
		t.Errorf("body-tail must still be in output after check runs; got %q", got)
	}
	if !strings.Contains(got, "check-line") {
		t.Errorf("check-line must appear in output; got %q", got)
	}
	_ = bufBeforeCheck
}

// TestPlainReporter_FlushOutput_NoopWhenEmpty verifies that FlushOutput is a
// no-op when inProgress is empty (e.g. called for a step with no trailing tail).
func TestPlainReporter_FlushOutput_NoopWhenEmpty(t *testing.T) {
	r, buf := newBufReporter()
	r.StepOutput("setup/build", "clean-line", true) // final, so inProgress=""
	lenAfterFinal := buf.Len()
	r.FlushOutput("setup/build") // must be a no-op
	if buf.Len() != lenAfterFinal {
		t.Errorf("FlushOutput on empty inProgress must not write anything extra; got %q", buf.String())
	}
}

// --- Task 8: non-TTY parallel sub-step buffering and dump ---

// helper: build a parallel-group DeployStep with the named sub-steps so
// StartGroup pre-registers the matching sub-addr buffers.
func parallelGroup(name string, subNames ...string) config.DeployStep {
	subs := make([]config.DeployStep, len(subNames))
	for i, n := range subNames {
		subs[i] = config.DeployStep{Name: n}
	}
	return config.DeployStep{
		Name:     name,
		Parallel: &config.ParallelGroup{Steps: subs},
	}
}

func TestPlainReporter_NonTTY_FinishStep_DumpsBufferedOutput(t *testing.T) {
	r, buf := newBufReporter()

	group := parallelGroup("dumps", "main", "stock")
	r.StartGroup("init/dumps", group, []int{1, 2}, 2)
	r.StartStep("init/main", config.DeployStep{Name: "main"}, 1, 2)
	r.StepOutput("init/main", "downloading…", true)
	r.StepOutput("init/main", "done", true)
	r.FinishStep("init/main", config.DeployStep{Name: "main"}, 1, 2)

	got := clean(buf.String())
	wantLines := []string{
		"  · Parallel group: init/dumps (2 steps)",
		"  · [1/2] init/main",
		"  ✓ [1/2] Done: init/main",
		"  ───── output ─────",
		"downloading…",
		"done",
		"  ──────────────────",
	}
	gotLines := lines(got)
	if len(gotLines) != len(wantLines) {
		t.Fatalf("FinishStep dump: got %d lines, want %d\ngot:\n%s",
			len(gotLines), len(wantLines), strings.Join(gotLines, "\n"))
	}
	for i, want := range wantLines {
		if gotLines[i] != want {
			t.Errorf("line %d:\n got:  %q\n want: %q", i, gotLines[i], want)
		}
	}
}

func TestPlainReporter_NonTTY_InterleavedCompletion_KeepsBuffersDistinct(t *testing.T) {
	r, buf := newBufReporter()

	group := parallelGroup("dumps", "alpha", "beta")
	r.StartGroup("init/dumps", group, []int{1, 2}, 2)
	r.StartStep("init/alpha", config.DeployStep{Name: "alpha"}, 1, 2)
	r.StartStep("init/beta", config.DeployStep{Name: "beta"}, 2, 2)
	r.StepOutput("init/alpha", "alpha-1", true)
	r.StepOutput("init/beta", "beta-1", true)
	r.StepOutput("init/alpha", "alpha-2", true)
	// beta finishes first
	r.FinishStep("init/beta", config.DeployStep{Name: "beta"}, 2, 2)
	r.FinishStep("init/alpha", config.DeployStep{Name: "alpha"}, 1, 2)

	got := clean(buf.String())
	// beta's block must precede alpha's block and contain only beta-1.
	betaIdx := strings.Index(got, "✓ [2/2] Done: init/beta")
	alphaIdx := strings.Index(got, "✓ [1/2] Done: init/alpha")
	if betaIdx == -1 || alphaIdx == -1 || betaIdx >= alphaIdx {
		t.Fatalf("expected beta block before alpha block; got:\n%s", got)
	}
	betaBlock := got[betaIdx:alphaIdx]
	alphaBlock := got[alphaIdx:]
	if !strings.Contains(betaBlock, "beta-1") {
		t.Errorf("beta block missing beta-1:\n%s", betaBlock)
	}
	if strings.Contains(betaBlock, "alpha-1") || strings.Contains(betaBlock, "alpha-2") {
		t.Errorf("beta block leaked alpha output:\n%s", betaBlock)
	}
	if !strings.Contains(alphaBlock, "alpha-1") || !strings.Contains(alphaBlock, "alpha-2") {
		t.Errorf("alpha block missing alpha lines:\n%s", alphaBlock)
	}
	if strings.Contains(alphaBlock, "beta-1") {
		t.Errorf("alpha block leaked beta output:\n%s", alphaBlock)
	}
}

func TestPlainReporter_NonTTY_FailStep_DumpsBufferThenError(t *testing.T) {
	r, buf := newBufReporter()
	r.StartPipeline("deploy", 2)

	group := parallelGroup("dumps", "main")
	r.StartGroup("init/dumps", group, []int{1}, 1)
	r.StartStep("init/main", config.DeployStep{Name: "main"}, 1, 1)
	r.StepOutput("init/main", "partial output", true)
	r.FailStep("init/main", config.DeployStep{Name: "main"}, 1, 1, errors.New("exit status 7"))

	got := clean(buf.String())
	gotLines := lines(got)
	wantOrder := []string{
		"  ✗ [1/1] Failed: init/main",
		"  ───── output ─────",
		"partial output",
		"  ──────────────────",
		"  exit status 7",
	}
	// Find the failed-line index and check the following block.
	startIdx := -1
	for i, l := range gotLines {
		if l == wantOrder[0] {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		t.Fatalf("missing failed line; output:\n%s", got)
	}
	if startIdx+len(wantOrder) > len(gotLines) {
		t.Fatalf("not enough trailing lines; output:\n%s", got)
	}
	for i, want := range wantOrder {
		if gotLines[startIdx+i] != want {
			t.Errorf("line %d:\n got:  %q\n want: %q", i, gotLines[startIdx+i], want)
		}
	}
}

func TestPlainReporter_NonTTY_SkipStep_NoBufferDump(t *testing.T) {
	r, buf := newBufReporter()

	group := parallelGroup("dumps", "main")
	r.StartGroup("init/dumps", group, []int{1}, 1)
	r.SkipStep("init/main", config.DeployStep{Name: "main"}, 1, 1, "when: false")

	got := clean(buf.String())
	if !strings.Contains(got, "◎ [1/1] Skipped: init/main (when: false)") {
		t.Errorf("missing skip line; got:\n%s", got)
	}
	if strings.Contains(got, "───── output ─────") {
		t.Errorf("skip must not dump output block; got:\n%s", got)
	}
}

func TestPlainReporter_NonTTY_FinishGroup_PrintsCountsAndElapsed(t *testing.T) {
	r, buf := newBufReporter()

	group := parallelGroup("dumps", "a", "b", "c")
	r.StartGroup("init/dumps", group, []int{1, 2, 3}, 3)
	r.FinishStep("init/a", config.DeployStep{Name: "a"}, 1, 3)
	r.FailStep("init/b", config.DeployStep{Name: "b"}, 2, 3, errors.New("nope"))
	r.SkipStep("init/c", config.DeployStep{Name: "c"}, 3, 3, "when: false")
	r.FinishGroup("init/dumps", config.DeployStep{Name: "dumps"}, false)

	got := clean(buf.String())
	want := "  ✗ Parallel group failed: init/dumps (1 ok, 1 failed, 1 skipped of 3, 0s)"
	if !strings.Contains(got, want) {
		t.Errorf("missing aggregate line %q; got:\n%s", want, got)
	}
}

func TestPlainReporter_NonTTY_FinishGroup_CancelledSubStepsIncludedInSummary(t *testing.T) {
	r, buf := newBufReporter()

	// Three sub-steps: "a" finishes OK, "b" fails, "c" is cancelled (FailFast)
	// and never receives a terminal event.
	group := parallelGroup("dumps", "a", "b", "c")
	r.StartGroup("init/dumps", group, []int{1, 2, 3}, 3)
	r.FinishStep("init/a", config.DeployStep{Name: "a"}, 1, 3)
	r.FailStep("init/b", config.DeployStep{Name: "b"}, 2, 3, errors.New("nope"))
	// "c" receives no terminal event — simulates FailFast cancellation.
	r.FinishGroup("init/dumps", config.DeployStep{Name: "dumps"}, false)

	got := clean(buf.String())
	want := "  ✗ Parallel group failed: init/dumps (1 ok, 1 failed, 0 skipped, 1 cancelled of 3, 0s)"
	if !strings.Contains(got, want) {
		t.Errorf("missing aggregate line %q; got:\n%s", want, got)
	}
}

func TestPlainReporter_NonTTY_SubStepOutput_LazyEntryWhenStartGroupSkipped(t *testing.T) {
	r, buf := newBufReporter()
	// No StartGroup; defensive lazy creation must not panic.
	r.StepOutput("orphan/sub", "stray line", true)
	r.FinishStep("orphan/sub", config.DeployStep{Name: "sub"}, 1, 1)

	got := clean(buf.String())
	if !strings.Contains(got, "stray line") {
		t.Errorf("expected lazy buffer to flush on FinishStep; got:\n%s", got)
	}
}

// --- Concurrency safety ---
//
// PlainReporter is invoked from N goroutines by the parallel-group executor.
// This test pounds every public method from 16 goroutines; running under -race
// is what actually guarantees the mutex placement is correct.
func TestPlainReporter_ConcurrentEvents_NoRace(t *testing.T) {
	r, _ := newBufReporter()
	r.StartPipeline("deploy", 0)

	const workers = 16
	const iters = 50

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := range workers {
		go func(w int) {
			defer wg.Done()
			step := config.DeployStep{Name: fmt.Sprintf("s%d", w)}
			group := config.DeployStep{Name: fmt.Sprintf("g%d", w)}
			phase := config.DeployPhase{Name: fmt.Sprintf("p%d", w)}
			for i := range iters {
				addr := fmt.Sprintf("p%d/s%d", w, i)
				r.EnterPhase(phase.Name, phase)
				r.StartStep(addr, step, 1, 100)
				r.StepOutput(addr, "line", true)
				r.FinishStep(addr, step, 1, 100)
				r.StartGroup(addr+"/g", group, []int{1, 2}, 100)
				r.StepOutput(addr+"/g/a", "out", true)
				r.FinishGroup(addr+"/g", group, true)
				r.SkipStep(addr, step, 1, 100, "reason")
				r.SkipPhase(phase.Name, phase, "reason")
				r.FailStep(addr, step, 1, 100, errSentinel)
			}
		}(w)
	}
	wg.Wait()

	r.FinishPipeline(true)
}

var errSentinel = errors.New("concurrent test")

// --- Interface compliance ---

// Verify PlainReporter satisfies the Reporter interface at compile time.
var _ Reporter = (*PlainReporter)(nil)

// --- Task 7: LiveLine integration (sequential lifecycle) ---

// newTTYReporter returns a PlainReporter wired to a termGrid as termOut so the
// LiveLine is enabled. The internal ticker is suppressed via testHooks so the
// tests don't race against a background goroutine; redraws happen synchronously
// on Println / SetText calls.
func newTTYReporter() (*PlainReporter, *termGrid) {
	grid := newTermGrid(24, 120)
	// Both the screen writer and termOut target the same grid, mirroring how
	// a real terminal consumes a single unified byte stream.
	w := render.NewWriter(grid)
	r := NewPlainReporter(w, nil, grid)
	r.live.testHooks = &liveLineTestHooks{noTicker: true, widthFn: func() int { return 80 }}
	r.now = func() time.Time { return fixedTime }
	return r, grid
}

func TestPlainReporter_StartPipeline_PaintsFooter(t *testing.T) {
	r, grid := newTTYReporter()
	r.StartPipeline("deploy", 3)
	defer r.live.Stop() // safety net even though FinishPipeline is not called

	// After Start, the footer is painted on row 0 and cursor lives on row 1.
	if !strings.Contains(grid.line(0), "Starting deploy...") {
		t.Errorf("footer should contain 'Starting deploy...'; got %q", grid.line(0))
	}
	if row, col := grid.cursor(); row != 1 || col != 0 {
		t.Errorf("cursor should be below footer at (1,0); got (%d,%d)", row, col)
	}
}

func TestPlainReporter_EnterPhase_UpdatesFooter(t *testing.T) {
	r, grid := newTTYReporter()
	r.StartPipeline("deploy", 1)
	r.EnterPhase("env", config.DeployPhase{Name: "env", Description: "Environment"})
	// Drive a tick so the new footer text actually paints.
	r.live.tick()
	defer r.live.Stop()

	// emit pushed the "Phase: env: Environment" line into scrollback (row 0),
	// and the redraw paints the new footer at the current footer row with
	// the SetText value.
	if !strings.Contains(grid.line(0), "Phase: env: Environment") {
		t.Errorf("scrollback row 0 should carry the phase line; got %q", grid.line(0))
	}
	// The footer row should mention the phase key + description (from SetText).
	footerRow := grid.line(1)
	if !strings.Contains(footerRow, "env: Environment") {
		t.Errorf("footer should reflect phase via SetText; got %q", footerRow)
	}
}

func TestPlainReporter_StartStep_UpdatesFooter(t *testing.T) {
	r, grid := newTTYReporter()
	r.StartPipeline("deploy", 1)
	step := config.DeployStep{Name: "render", Description: "Render env"}
	r.StartStep("env/render", step, 1, 2)
	r.live.tick()
	defer r.live.Stop()

	if !strings.Contains(grid.line(0), "env/render") {
		t.Errorf("scrollback row 0 should carry the step start line; got %q", grid.line(0))
	}
	if !strings.Contains(grid.line(1), "[1/2] env/render") {
		t.Errorf("footer should carry the [N/M] step prefix; got %q", grid.line(1))
	}
}

func TestPlainReporter_FinishPipeline_Success_StopsLiveLine(t *testing.T) {
	r, grid := newTTYReporter()
	r.StartPipeline("deploy", 1)
	r.FinishPipeline(true)

	// After Stop, the footer is erased. The "Done" line lands in scrollback;
	// the row where the footer used to sit is now blank.
	scrollback := grid.line(0) + " | " + grid.line(1) + " | " + grid.line(2)
	if !strings.Contains(scrollback, "✓ Done") {
		t.Errorf("expected '✓ Done' somewhere in grid; got %s", scrollback)
	}
	if r.live.stopped != true {
		t.Errorf("LiveLine should be stopped after FinishPipeline(true)")
	}
}

// Regression test: FinishPipeline(false) must still call live.Stop() — the
// previous early-return bug left the ticker running.
func TestPlainReporter_FinishPipeline_Failure_StopsLiveLine(t *testing.T) {
	r, _ := newTTYReporter()
	r.StartPipeline("deploy", 1)
	r.FinishPipeline(false)

	if !r.live.stopped {
		t.Fatalf("LiveLine must be stopped after FinishPipeline(false) — regression of the early-return bug")
	}
}

// TestPlainReporter_FinishPipeline_StopIdempotent guards against any future
// caller pattern that calls FinishPipeline followed by Close() (Task 10) by
// asserting Stop is safe to invoke twice via stopOnce.
func TestPlainReporter_FinishPipeline_StopIdempotent(t *testing.T) {
	r, _ := newTTYReporter()
	r.StartPipeline("deploy", 1)
	r.FinishPipeline(true)
	r.live.Stop() // double-stop must be a no-op via stopOnce
}

// TestPlainReporter_LogFile_StatusLines_ExactlyOnce verifies that every status
// line lands in the log file exactly once even when the LiveLine is enabled
// (i.e. the screen and log channels are independent — no double-emit).
func TestPlainReporter_LogFile_StatusLines_ExactlyOnce(t *testing.T) {
	var logBuf bytes.Buffer
	scr := &bytes.Buffer{}
	w := render.NewWriter(scr)
	grid := newTermGrid(24, 120)
	r := NewPlainReporter(w, &logBuf, grid)
	r.live.testHooks = &liveLineTestHooks{noTicker: true, widthFn: func() int { return 80 }}
	r.now = func() time.Time { return fixedTime }

	r.StartPipeline("deploy", 2)
	r.EnterPhase("env", config.DeployPhase{Name: "env"})
	r.StartStep("env/render", config.DeployStep{Name: "render"}, 1, 2)
	r.FinishStep("env/render", config.DeployStep{Name: "render"}, 1, 2)
	r.FinishPipeline(true)

	log := logBuf.String()
	checks := []string{
		"Phase: env",
		"[1/2] env/render",
		"Done: env/render",
		"✓ Done (",
	}
	for _, c := range checks {
		if n := strings.Count(log, c); n != 1 {
			t.Errorf("status line %q must appear exactly once in log; got %d in:\n%s", c, n, log)
		}
	}
}

// --- Task 9: parallel block integration ---

// TestPlainReporter_TTY_StartGroup_ReservesBlockRows verifies that StartGroup
// in TTY mode reserves N rows and paints the initial running rows for each
// sub-step with the running glyph `·`.
func TestPlainReporter_TTY_StartGroup_ReservesBlockRows(t *testing.T) {
	r, grid := newTTYReporter()
	r.StartPipeline("deploy", 3)
	group := parallelGroup("dumps", "alpha", "beta", "charlie")
	r.StartGroup("init/dumps", group, []int{1, 2, 3}, 3)
	r.live.tick()
	defer r.live.Stop()

	// Each block row should carry the running glyph and the sub-step name.
	rows := []string{grid.line(1), grid.line(2), grid.line(3)}
	want := []string{"alpha", "beta", "charlie"}
	for i, want := range want {
		if !strings.Contains(rows[i], want) {
			t.Errorf("block row %d should contain %q; got %q", i, want, rows[i])
		}
		if !strings.Contains(rows[i], iconRunning) {
			t.Errorf("block row %d should carry running glyph %q; got %q", i, iconRunning, rows[i])
		}
		// MUST NOT show finished/failed/skipped glyphs during execution.
		for _, bad := range []string{iconDone, iconFailed, iconSkipped} {
			if strings.Contains(rows[i], bad) {
				t.Errorf("block row %d unexpectedly carries terminal glyph %q during execution; got %q", i, bad, rows[i])
			}
		}
	}
}

// TestPlainReporter_TTY_StepOutput_UpdatesBlockRow_RunningGlyph is the glyph-
// discipline regression test: every StepOutput frame (final or not, repeated)
// must show the running glyph — never ✓/✗/◎ before FinishStep.
func TestPlainReporter_TTY_StepOutput_UpdatesBlockRow_RunningGlyph(t *testing.T) {
	r, grid := newTTYReporter()
	r.StartPipeline("deploy", 1)
	group := parallelGroup("dumps", "alpha")
	r.StartGroup("init/dumps", group, []int{1}, 1)

	// Feed multiple final=true frames pre-FinishStep; the row glyph must stay `·`.
	r.StepOutput("init/alpha", "10%", true)
	r.StepOutput("init/alpha", "50%", false)
	r.StepOutput("init/alpha", "99%", true)
	r.live.tick()
	defer r.live.Stop()

	row := grid.line(1)
	if !strings.Contains(row, iconRunning) {
		t.Errorf("row should carry running glyph during execution; got %q", row)
	}
	for _, bad := range []string{iconDone, iconFailed, iconSkipped} {
		if strings.Contains(row, bad) {
			t.Errorf("row must NOT carry terminal glyph %q before Finish/Fail/SkipStep; got %q", bad, row)
		}
	}
	if !strings.Contains(row, "99%") {
		t.Errorf("row should reflect latest frame '99%%'; got %q", row)
	}
}

// TestPlainReporter_TTY_FinishStep_SetsDoneGlyph verifies the row icon flips
// to ✓ after FinishStep.
func TestPlainReporter_TTY_FinishStep_SetsDoneGlyph(t *testing.T) {
	r, _ := newTTYReporter()
	r.StartPipeline("deploy", 1)
	group := parallelGroup("dumps", "alpha")
	r.StartGroup("init/dumps", group, []int{1}, 1)
	r.StepOutput("init/alpha", "done", true)
	r.FinishStep("init/alpha", config.DeployStep{Name: "alpha"}, 1, 1)
	defer r.live.Stop()

	// Block content is in r.live.blockContent[0].
	got := r.live.blockContent[0]
	if !strings.Contains(got, iconDone) {
		t.Errorf("block row should carry done glyph %q after FinishStep; got %q", iconDone, got)
	}
	if !strings.Contains(got, "Done: alpha") {
		t.Errorf("block row should contain 'Done: alpha'; got %q", got)
	}
}

// TestPlainReporter_TTY_FailStep_SetsFailedGlyph verifies the row icon flips
// to ✗ after FailStep.
func TestPlainReporter_TTY_FailStep_SetsFailedGlyph(t *testing.T) {
	r, _ := newTTYReporter()
	r.StartPipeline("deploy", 1)
	group := parallelGroup("dumps", "alpha")
	r.StartGroup("init/dumps", group, []int{1}, 1)
	r.FailStep("init/alpha", config.DeployStep{Name: "alpha"}, 1, 1, errors.New("boom"))
	defer r.live.Stop()

	got := r.live.blockContent[0]
	if !strings.Contains(got, iconFailed) {
		t.Errorf("block row should carry failed glyph %q after FailStep; got %q", iconFailed, got)
	}
	if !strings.Contains(got, "Failed: alpha") {
		t.Errorf("block row should contain 'Failed: alpha'; got %q", got)
	}
}

// TestPlainReporter_TTY_SkipStep_SetsSkippedGlyph verifies the row icon flips
// to ◎ after SkipStep.
func TestPlainReporter_TTY_SkipStep_SetsSkippedGlyph(t *testing.T) {
	r, _ := newTTYReporter()
	r.StartPipeline("deploy", 1)
	group := parallelGroup("dumps", "alpha")
	r.StartGroup("init/dumps", group, []int{1}, 1)
	r.SkipStep("init/alpha", config.DeployStep{Name: "alpha"}, 1, 1, "when: false")
	defer r.live.Stop()

	got := r.live.blockContent[0]
	if !strings.Contains(got, iconSkipped) {
		t.Errorf("block row should carry skipped glyph %q after SkipStep; got %q", iconSkipped, got)
	}
	if !strings.Contains(got, "Skipped: alpha") {
		t.Errorf("block row should contain 'Skipped: alpha'; got %q", got)
	}
	if !strings.Contains(got, "when: false") {
		t.Errorf("block row should carry skip reason; got %q", got)
	}
}

// TestPlainReporter_TTY_FinishGroup_EndsBlock verifies that FinishGroup ends
// the LiveLine block (blockRows returns to 0).
func TestPlainReporter_TTY_FinishGroup_EndsBlock(t *testing.T) {
	r, _ := newTTYReporter()
	r.StartPipeline("deploy", 1)
	group := parallelGroup("dumps", "alpha")
	r.StartGroup("init/dumps", group, []int{1}, 1)
	if r.live.blockRows != 1 {
		t.Fatalf("expected blockRows=1 after StartGroup; got %d", r.live.blockRows)
	}
	r.FinishStep("init/alpha", config.DeployStep{Name: "alpha"}, 1, 1)
	r.FinishGroup("init/dumps", group, true)
	defer r.live.Stop()

	if r.live.blockRows != 0 {
		t.Errorf("FinishGroup should EndBlock (blockRows=0); got %d", r.live.blockRows)
	}
}

// TestPlainReporter_TTY_FailedSubStep_DumpsBuffer verifies the TTY-failure
// policy: a failed sub-step ALWAYS dumps its buffer between bars even when a
// log path is set (user needs the full history to diagnose).
func TestPlainReporter_TTY_FailedSubStep_DumpsBuffer(t *testing.T) {
	scr := &bytes.Buffer{}
	w := render.NewWriter(scr)
	grid := newTermGrid(24, 120)
	r := NewPlainReporter(w, nil, grid)
	r.live.testHooks = &liveLineTestHooks{noTicker: true, widthFn: func() int { return 80 }}
	r.now = func() time.Time { return fixedTime }

	r.StartPipeline("deploy", 1)
	group := parallelGroup("dumps", "alpha")
	r.StartGroup("init/dumps", group, []int{1}, 1)
	// Even with a log path set, FAILED sub-steps must dump the buffer.
	r.SetSubStepLogPath("init/alpha", "/tmp/parallel/init/dumps/alpha.log")
	r.StepOutput("init/alpha", "partial output", true)
	r.FailStep("init/alpha", config.DeployStep{Name: "alpha"}, 1, 1, errors.New("boom"))
	defer r.live.Stop()

	got := clean(scr.String())
	if !strings.Contains(got, "───── output ─────") {
		t.Errorf("TTY+failure must dump output between bars; got:\n%s", got)
	}
	if !strings.Contains(got, "partial output") {
		t.Errorf("TTY+failure must include captured output line; got:\n%s", got)
	}
	if strings.Contains(got, "Full log:") {
		t.Errorf("TTY+failure must NOT emit 'Full log:' (dump policy); got:\n%s", got)
	}
}

// TestPlainReporter_TTY_SuccessWithLogPath_SuppressesDump verifies the TTY-
// success-log-enabled policy: when a successful sub-step has a known log
// path, the buffer dump is suppressed in favour of a "Full log:" pointer.
func TestPlainReporter_TTY_SuccessWithLogPath_SuppressesDump(t *testing.T) {
	scr := &bytes.Buffer{}
	w := render.NewWriter(scr)
	grid := newTermGrid(24, 120)
	r := NewPlainReporter(w, nil, grid)
	r.live.testHooks = &liveLineTestHooks{noTicker: true, widthFn: func() int { return 80 }}
	r.now = func() time.Time { return fixedTime }

	r.StartPipeline("deploy", 1)
	group := parallelGroup("dumps", "alpha")
	r.StartGroup("init/dumps", group, []int{1}, 1)
	r.SetSubStepLogPath("init/alpha", "/tmp/parallel/init/dumps/alpha.log")
	r.StepOutput("init/alpha", "downloading", true)
	r.StepOutput("init/alpha", "done", true)
	r.FinishStep("init/alpha", config.DeployStep{Name: "alpha"}, 1, 1)
	defer r.live.Stop()

	got := clean(scr.String())
	if !strings.Contains(got, "Full log: /tmp/parallel/init/dumps/alpha.log") {
		t.Errorf("TTY+success+logPath should emit 'Full log:' pointer; got:\n%s", got)
	}
	if strings.Contains(got, "───── output ─────") {
		t.Errorf("TTY+success+logPath must NOT dump between bars; got:\n%s", got)
	}
}

// TestPlainReporter_TTY_SuccessNoLogPath_DumpsBuffer verifies that a
// successful sub-step with NO log path falls back to dumping the buffer
// (otherwise the user has no on-screen record at all).
func TestPlainReporter_TTY_SuccessNoLogPath_DumpsBuffer(t *testing.T) {
	scr := &bytes.Buffer{}
	w := render.NewWriter(scr)
	grid := newTermGrid(24, 120)
	r := NewPlainReporter(w, nil, grid)
	r.live.testHooks = &liveLineTestHooks{noTicker: true, widthFn: func() int { return 80 }}
	r.now = func() time.Time { return fixedTime }

	r.StartPipeline("deploy", 1)
	group := parallelGroup("dumps", "alpha")
	r.StartGroup("init/dumps", group, []int{1}, 1)
	// No SetSubStepLogPath call → empty logPath.
	r.StepOutput("init/alpha", "downloading", true)
	r.FinishStep("init/alpha", config.DeployStep{Name: "alpha"}, 1, 1)
	defer r.live.Stop()

	got := clean(scr.String())
	if !strings.Contains(got, "───── output ─────") {
		t.Errorf("TTY+success+noLogPath must dump output between bars; got:\n%s", got)
	}
	if !strings.Contains(got, "downloading") {
		t.Errorf("TTY+success+noLogPath must include captured output; got:\n%s", got)
	}
	if strings.Contains(got, "Full log:") {
		t.Errorf("must NOT emit 'Full log:' when path is empty; got:\n%s", got)
	}
}

// TestPlainReporter_SetSubStepLogPath_EmptyPathIsNoOp verifies the convention
// in the runner: an empty path call from runParallelSubStep (log disabled)
// must not change reporter state.
func TestPlainReporter_SetSubStepLogPath_EmptyPathIsNoOp(t *testing.T) {
	r, _ := newBufReporter()
	r.SetSubStepLogPath("init/alpha", "")
	// No panic, no entry created.
	if entry, ok := r.subs["init/alpha"]; ok && entry.logPath != "" {
		t.Errorf("empty path must not set logPath; got %q", entry.logPath)
	}
}

// TestPlainReporter_TTY_FullParallelGroup_Integration drives a 3-sub-step
// parallel group end-to-end with the LiveLine block and verifies that all
// status lines appear in the screen output and the block is torn down.
func TestPlainReporter_TTY_FullParallelGroup_Integration(t *testing.T) {
	// Use a separate bytes.Buffer for screen so data lines are inspectable
	// without interleaving ANSI cursor sequences from termOut.
	scr := &bytes.Buffer{}
	w := render.NewWriter(scr)
	grid := newTermGrid(24, 120)
	r := NewPlainReporter(w, nil, grid)
	r.live.testHooks = &liveLineTestHooks{noTicker: true, widthFn: func() int { return 80 }}
	r.now = func() time.Time { return fixedTime }
	defer r.live.Stop()

	r.StartPipeline("deploy", 3)
	group := parallelGroup("dumps", "alpha", "beta", "charlie")
	r.StartGroup("init/dumps", group, []int{1, 2, 3}, 3)

	r.StepOutput("init/alpha", "progress", true)
	r.FinishStep("init/alpha", config.DeployStep{Name: "alpha"}, 1, 3)

	r.FailStep("init/beta", config.DeployStep{Name: "beta"}, 2, 3, errors.New("nope"))

	r.SkipStep("init/charlie", config.DeployStep{Name: "charlie"}, 3, 3, "when: false")

	r.FinishGroup("init/dumps", group, false)

	got := clean(scr.String())
	for _, want := range []string{
		"✓ [1/3] Done: init/alpha",
		"✗ [2/3] Failed: init/beta",
		"◎ [3/3] Skipped: init/charlie (when: false)",
		"✗ Parallel group failed: init/dumps",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing line %q; output:\n%s", want, got)
		}
	}
	// Block must be torn down after FinishGroup.
	if r.live.blockRows != 0 {
		t.Errorf("blockRows should be 0 after FinishGroup, got %d", r.live.blockRows)
	}
}

// --- Task 10: huh hook registration ---

func TestNewPlainReporter_RegistersHuhHooks(t *testing.T) {
	ui.ClearHuhHooks()
	t.Cleanup(ui.ClearHuhHooks)

	r, _ := newBufReporter()
	t.Cleanup(r.Close)

	before, after := ui.SnapshotHuhHooks()
	if before == nil || after == nil {
		t.Fatal("NewPlainReporter must install before+after huh hooks")
	}
}

func TestPlainReporter_Close_ClearsHooks(t *testing.T) {
	ui.ClearHuhHooks()
	t.Cleanup(ui.ClearHuhHooks)

	r, _ := newBufReporter()
	r.Close()

	before, after := ui.SnapshotHuhHooks()
	if before != nil || after != nil {
		t.Errorf("Close must clear huh hooks, got before=%v after=%v", before != nil, after != nil)
	}
}

func TestPlainReporter_Close_Idempotent(t *testing.T) {
	r, _ := newBufReporter()
	r.Close()
	r.Close() // must not panic
}

func TestPlainReporter_HuhHooksDriveLiveLinePauseResume(t *testing.T) {
	ui.ClearHuhHooks()
	t.Cleanup(ui.ClearHuhHooks)

	r, grid := newTTYReporter()
	t.Cleanup(r.Close)

	r.StartPipeline("deploy", 1)

	before, after := ui.SnapshotHuhHooks()
	if before == nil || after == nil {
		t.Fatal("hooks not installed")
	}

	// Invoke before (Pause): footer row should be cleared.
	before()
	// After Pause, the LiveLine should be marked paused.
	r.live.mu.Lock()
	paused := r.live.paused
	r.live.mu.Unlock()
	if !paused {
		t.Error("before hook should pause LiveLine")
	}

	// Invoke after (Resume): footer repaints.
	after()
	r.live.mu.Lock()
	paused = r.live.paused
	r.live.mu.Unlock()
	if paused {
		t.Error("after hook should resume LiveLine")
	}

	_ = grid // grid byte-level assertions are covered by liveline_test.go
}
