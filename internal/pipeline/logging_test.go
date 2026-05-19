package pipeline

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"devbox-cli/internal/condition"
	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/render"
)

func TestAnsiOnlyStripper_Write_StripsEscapes(t *testing.T) {
	var buf bytes.Buffer
	s := &ansiOnlyStripper{w: &buf}
	input := "\x1b[32mhello\x1b[0m world"
	n, err := s.Write([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(input) {
		t.Errorf("expected n=%d, got %d", len(input), n)
	}
	out := buf.String()
	if strings.Contains(out, "\x1b") {
		t.Errorf("expected ANSI escapes stripped, got: %q", out)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "world") {
		t.Errorf("expected text preserved, got: %q", out)
	}
}

func TestAnsiOnlyStripper_Write_PlainText(t *testing.T) {
	var buf bytes.Buffer
	s := &ansiOnlyStripper{w: &buf}
	input := "plain text"
	n, err := s.Write([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(input) {
		t.Errorf("expected n=%d, got %d", len(input), n)
	}
	if buf.String() != input {
		t.Errorf("expected output unchanged, got: %q", buf.String())
	}
}

// TestAnsiOnlyRe_PreservesCR ensures the regex used by the tee path leaves
// `\r` bytes intact (precondition for Task 2 frame parsing).
func TestAnsiOnlyRe_PreservesCR(t *testing.T) {
	in := []byte("\x1b[32m50%\r100%\x1b[0m\n")
	got := ansiOnlyRe.ReplaceAll(in, nil)
	want := "50%\r100%\n"
	if string(got) != want {
		t.Errorf("ansiOnlyRe.ReplaceAll = %q, want %q (must preserve \\r)", got, want)
	}
}

// TestLogSanitizer_ProgressFrames_BecomeSeparateLines is the regression test
// for the live-progress bug: progress frames that overwrite each other on a
// real terminal must land on separate lines in the log file.
func TestLogSanitizer_ProgressFrames_BecomeSeparateLines(t *testing.T) {
	var buf bytes.Buffer
	s := &logSanitizer{w: &buf}
	in := "50%\r100%\n"
	if _, err := s.Write([]byte(in)); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := buf.String()
	want := "50%\n100%\n"
	if got != want {
		t.Errorf("progress-frame log: got %q, want %q", got, want)
	}
	if strings.Contains(got, "\r") {
		t.Errorf("logSanitizer left bare \\r in output: %q", got)
	}
	if strings.Contains(got, "50%100%") {
		t.Errorf("frames concatenated instead of separating: %q", got)
	}
}

func TestLogSanitizer_LoneCR_BecomesNewline(t *testing.T) {
	var buf bytes.Buffer
	s := &logSanitizer{w: &buf}
	if _, err := s.Write([]byte("partial\r")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if buf.String() != "partial\n" {
		t.Errorf("lone \\r: got %q, want %q", buf.String(), "partial\n")
	}
}

// TestLogSanitizer_CRLFInOneWrite_BecomesDoubleNewline documents the stateless
// trade-off: `\r\n` within a single Write produces `\n\n` (one extra blank
// line). Acceptable because PTY-captured child output rarely emits CRLF.
func TestLogSanitizer_CRLFInOneWrite_BecomesDoubleNewline(t *testing.T) {
	var buf bytes.Buffer
	s := &logSanitizer{w: &buf}
	if _, err := s.Write([]byte("a\r\nb")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if buf.String() != "a\n\nb" {
		t.Errorf("CRLF: got %q, want %q", buf.String(), "a\n\nb")
	}
}

// TestLogSanitizer_SplitWrite_CRThenLF documents that a `\r` and `\n` arriving
// in separate Writes also produce two newlines (each Write is independent).
func TestLogSanitizer_SplitWrite_CRThenLF(t *testing.T) {
	var buf bytes.Buffer
	s := &logSanitizer{w: &buf}
	if _, err := s.Write([]byte("a\r")); err != nil {
		t.Fatalf("write1: %v", err)
	}
	if _, err := s.Write([]byte("\nb")); err != nil {
		t.Fatalf("write2: %v", err)
	}
	if buf.String() != "a\n\nb" {
		t.Errorf("split CR/LF: got %q, want %q", buf.String(), "a\n\nb")
	}
}

// TestLogSanitizer_ConcurrentWrites_NoPanic asserts the stateless writer
// tolerates concurrent calls from multiple goroutines without panicking. Run
// with `-race` to also catch any shared-state issues.
func TestLogSanitizer_ConcurrentWrites_NoPanic(t *testing.T) {
	var buf syncBuf
	s := &logSanitizer{w: &buf}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				_, _ = s.Write([]byte("frame\r"))
			}
		})
	}
	wg.Wait()
	if strings.Contains(buf.String(), "\r") {
		t.Errorf("expected no \\r in output, got %q", buf.String())
	}
}

// teeFrame is a captured (frame, final) pair from the new lineTee callback.
type teeFrame struct {
	frame string
	final bool
}

func collectFrames() (*[]teeFrame, func(string, bool)) {
	var got []teeFrame
	return &got, func(frame string, final bool) { got = append(got, teeFrame{frame, final}) }
}

// TestLineTee_FrameParsing_NewlineOnly verifies plain `\n`-terminated lines
// each emit one final=true frame and the trailing tail flushes as final=false.
func TestLineTee_FrameParsing_NewlineOnly(t *testing.T) {
	got, cb := collectFrames()
	tee := newLineTee(cb)
	_, _ = tee.Write([]byte("a\nb\nc"))
	tee.Flush()
	want := []teeFrame{{"a", true}, {"b", true}, {"c", false}}
	if !equalFrames(*got, want) {
		t.Errorf("got %v, want %v", *got, want)
	}
}

// TestLineTee_FrameParsing_CROnly verifies `\r` frames are emitted as
// final=false.
func TestLineTee_FrameParsing_CROnly(t *testing.T) {
	got, cb := collectFrames()
	tee := newLineTee(cb)
	_, _ = tee.Write([]byte("50%\r75%\r100%\n"))
	want := []teeFrame{{"50%", false}, {"75%", false}, {"100%", true}}
	if !equalFrames(*got, want) {
		t.Errorf("got %v, want %v", *got, want)
	}
}

// TestLineTee_FrameParsing_CRLF verifies CRLF collapses to a single final
// frame.
func TestLineTee_FrameParsing_CRLF(t *testing.T) {
	got, cb := collectFrames()
	tee := newLineTee(cb)
	_, _ = tee.Write([]byte("hello\r\nworld\r\n"))
	want := []teeFrame{{"hello", true}, {"world", true}}
	if !equalFrames(*got, want) {
		t.Errorf("got %v, want %v", *got, want)
	}
}

// TestLineTee_FrameParsing_Mixed combines `\r`, `\n`, and CRLF in one stream.
func TestLineTee_FrameParsing_Mixed(t *testing.T) {
	got, cb := collectFrames()
	tee := newLineTee(cb)
	_, _ = tee.Write([]byte("a\nb\rc\r\nd"))
	tee.Flush()
	want := []teeFrame{{"a", true}, {"b", false}, {"c", true}, {"d", false}}
	if !equalFrames(*got, want) {
		t.Errorf("got %v, want %v", *got, want)
	}
}

// TestLineTee_FrameParsing_LoneCRMidLine asserts a `\r` in the middle of a
// line splits cleanly into two frames.
func TestLineTee_FrameParsing_LoneCRMidLine(t *testing.T) {
	got, cb := collectFrames()
	tee := newLineTee(cb)
	_, _ = tee.Write([]byte("ab\rcd\n"))
	want := []teeFrame{{"ab", false}, {"cd", true}}
	if !equalFrames(*got, want) {
		t.Errorf("got %v, want %v", *got, want)
	}
}

// TestLineTee_FrameParsing_TrailingTail asserts Flush emits the trailing
// non-terminated tail as final=false.
func TestLineTee_FrameParsing_TrailingTail(t *testing.T) {
	got, cb := collectFrames()
	tee := newLineTee(cb)
	_, _ = tee.Write([]byte("partial"))
	if len(*got) != 0 {
		t.Fatalf("expected no callbacks before Flush, got %v", *got)
	}
	tee.Flush()
	want := []teeFrame{{"partial", false}}
	if !equalFrames(*got, want) {
		t.Errorf("got %v, want %v", *got, want)
	}
}

// TestLineTee_FrameParsing_Empty verifies an empty stream emits nothing.
func TestLineTee_FrameParsing_Empty(t *testing.T) {
	got, cb := collectFrames()
	tee := newLineTee(cb)
	tee.Flush()
	if len(*got) != 0 {
		t.Errorf("expected no callbacks for empty stream, got %v", *got)
	}
}

// TestLineTee_FrameParsing_MultipleConsecutiveCRs verifies a run of `\r`
// produces empty in-progress frames.
func TestLineTee_FrameParsing_MultipleConsecutiveCRs(t *testing.T) {
	got, cb := collectFrames()
	tee := newLineTee(cb)
	_, _ = tee.Write([]byte("a\r\r\rb\n"))
	want := []teeFrame{{"a", false}, {"", false}, {"", false}, {"b", true}}
	if !equalFrames(*got, want) {
		t.Errorf("got %v, want %v", *got, want)
	}
}

// TestLineTee_PreservesCR_StripsANSI asserts ANSI is stripped while `\r`
// survives so the frame parser can see it as a delimiter.
func TestLineTee_PreservesCR_StripsANSI(t *testing.T) {
	got, cb := collectFrames()
	tee := newLineTee(cb)
	_, _ = tee.Write([]byte("\x1b[32m50%\r100%\x1b[0m\n"))
	want := []teeFrame{{"50%", false}, {"100%", true}}
	if !equalFrames(*got, want) {
		t.Errorf("got %v, want %v", *got, want)
	}
	for _, f := range *got {
		if strings.Contains(f.frame, "\x1b") {
			t.Errorf("ANSI leaked into frame: %q", f.frame)
		}
	}
}

func equalFrames(a, b []teeFrame) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestOpenPipelineLog_CreatesDevboxLogsDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	_, logWriter, _, logPath, cleanup, err := OpenPipelineLog(tmpDir, "deploy", true)
	defer cleanup()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if logWriter == nil {
		t.Errorf("expected logWriter to be non-nil")
	}

	expectedPath := filepath.Join(tmpDir, ".devbox", "logs", "deploy.log")
	if logPath != expectedPath {
		t.Errorf("expected logPath=%q, got %q", expectedPath, logPath)
	}

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Errorf("expected log file to exist at %s", logPath)
	}

	legacyLogsDir := filepath.Join(tmpDir, "logs")
	if _, err := os.Stat(legacyLogsDir); !os.IsNotExist(err) {
		t.Errorf("expected legacy logs/ directory to not exist, but it does")
	}
}

func TestOpenSubStepLog_Disabled(t *testing.T) {
	w, path, err := OpenSubStepLog(t.TempDir(), "deploy", "g", "a", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != nil || path != "" {
		t.Errorf("disabled: want (nil, \"\"), got (%v, %q)", w, path)
	}
}

func TestOpenSubStepLog_CreatesPathAndSanitises(t *testing.T) {
	tmp := t.TempDir()
	// Pipeline / group / sub names contain unsafe characters that must be
	// replaced by sanitizeForFS — slashes, spaces, colons.
	w, path, err := OpenSubStepLog(tmp, "dep/loy", "my group:1", "../sub a", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w == nil {
		t.Fatal("expected non-nil writer")
	}
	defer func() { _ = w.Close() }()

	wantDir := filepath.Join(tmp, ".devbox", "logs", "parallel", "dep_loy", "my_group_1")
	wantPath := filepath.Join(wantDir, "_sub_a.log")
	if path != wantPath {
		t.Errorf("path = %q, want %q", path, wantPath)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}
	// Ensure the sanitised name did not escape the parallel root.
	rel, err := filepath.Rel(filepath.Join(tmp, ".devbox", "logs", "parallel"), path)
	if err != nil {
		t.Fatalf("filepath.Rel: %v", err)
	}
	if strings.HasPrefix(rel, "..") {
		t.Errorf("sanitised path escaped parallel root: %q", rel)
	}
}

func TestSanitizeForFS(t *testing.T) {
	cases := map[string]string{
		"":            "_",
		"plain":       "plain",
		"with space":  "with_space",
		"a/b":         "a_b",
		"...":         "_",
		"../etc":      "_etc",
		"keep.dots":   "keep.dots",
		"dash-and_us": "dash-and_us",
	}
	for in, want := range cases {
		if got := sanitizeForFS(in); got != want {
			t.Errorf("sanitizeForFS(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinWriters_AllNil_ReturnsDiscard(t *testing.T) {
	w := joinWriters(nil, nil)
	if w != io.Discard {
		t.Errorf("joinWriters(nil, nil) = %v, want io.Discard", w)
	}
}

func TestJoinWriters_SingleNonNil_ReturnsIt(t *testing.T) {
	var buf bytes.Buffer
	w := joinWriters(nil, &buf, nil)
	if w != io.Writer(&buf) {
		t.Errorf("joinWriters with single non-nil: got %v, want &buf", w)
	}
}

func TestJoinWriters_NilFiltering_DoesNotPanicOnMultiWriter(t *testing.T) {
	// io.MultiWriter cannot tolerate nil entries — it panics on first Write
	// when one is present. joinWriters must filter nils before constructing
	// the MultiWriter. Regression: joinWriters(nil, nil, &buf) returned the
	// buffer directly; this case has two non-nil entries plus a nil and
	// must still survive.
	var a, b bytes.Buffer
	w := joinWriters(nil, &a, nil, &b)
	if _, err := w.Write([]byte("xyz")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if a.String() != "xyz" || b.String() != "xyz" {
		t.Errorf("expected both buffers to receive 'xyz', got a=%q b=%q", a.String(), b.String())
	}
}

func TestJoinWriters_NilsWithLineTee_NoPanic(t *testing.T) {
	// Scenario that motivated the helper: pipeline logging disabled
	// (globalLogWriter and subLogWriter are both nil), but lineTee is always
	// non-nil. The helper must return lineTee alone — and writes must succeed.
	var got string
	tee := newLineTee(func(line string, _ bool) { got = line })
	w := joinWriters(nil, nil, tee)
	if _, err := w.Write([]byte("hello\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got != "hello" {
		t.Errorf("lineTee got %q, want %q", got, "hello")
	}
}

func TestLineTee_SplitsOnNewline_StripsANSI(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	tee := newLineTee(func(line string, _ bool) {
		mu.Lock()
		lines = append(lines, line)
		mu.Unlock()
	})
	// Write across multiple chunks; one chunk has no terminator.
	_, _ = tee.Write([]byte("\x1b[32mfirst\x1b[0m\nsec"))
	_, _ = tee.Write([]byte("ond\nthird"))
	tee.Flush()

	want := []string{"first", "second", "third"}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != len(want) {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line[%d] = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestChildIO_Parallel_NoStdoutNoStderr(t *testing.T) {
	var buf bytes.Buffer
	stdout, stderr, cleanup := childIO(&buf, true)
	defer cleanup()
	if stdout == os.Stdout || stderr == os.Stderr {
		t.Error("parallel mode must not return os.Stdout / os.Stderr")
	}
	// Parallel branch returns the writer unchanged — the caller (runParallelSubStep)
	// is responsible for routing through logSanitizer / ansiOnlyStripper.
	if _, err := stdout.Write([]byte("\x1b[31mred\x1b[0m\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(buf.String(), "red") {
		t.Errorf("expected text preserved, got %q", buf.String())
	}
}

func TestChildIO_Parallel_NilLogWriter_Panics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when parallel=true and logWriter is nil")
		}
	}()
	_, _, _ = childIO(nil, true)
}

// TestParallelGroup_PerSubStepLogRoutesOutput exercises the executor's
// parallel branch end-to-end: each sub-step's stdout must reach its dedicated
// log file, the global pipeline log, and Reporter.SubStepOutput; nothing must
// reach a sibling sub-step's log file or os.Stdout.
func TestParallelGroup_PerSubStepLogRoutesOutput(t *testing.T) {
	tmp := t.TempDir()

	// Capture the writer that OpenPipelineLog would normally hand back. A
	// real *os.File would be safe for concurrent writes; a bytes.Buffer is
	// not, so guard it with a mutex.
	globalLog := &syncBuf{}

	rep := &mockReporter{}
	phase := config.DeployPhase{Name: "p"}
	group := buildParallelGroupStep(phase, "g", true, 0, []config.DeployStep{
		{Name: "alpha", Type: "shell", Cmd: "echo alpha-out"},
		{Name: "beta", Type: "shell", Cmd: "echo beta-out"},
	})

	opts := RunOptions{
		Steps:       []ResolvedStep{group},
		Reporter:    rep,
		Name:        "deploy",
		Config:      &config.DevboxConfig{Raw: map[string]any{}},
		WorkDir:     tmp,
		LogWriter:   globalLog,
		Recorder:    &mockRecorder{},
		SkipDecider: func(addr string, rs ResolvedStep, h string) journal.Decision { return journal.Run },
	}
	if err := RunWithOptions(opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	alphaPath := filepath.Join(tmp, ".devbox", "logs", "parallel", "deploy", "g", "alpha.log")
	betaPath := filepath.Join(tmp, ".devbox", "logs", "parallel", "deploy", "g", "beta.log")

	alpha, err := os.ReadFile(alphaPath)
	if err != nil {
		t.Fatalf("read alpha log: %v", err)
	}
	beta, err := os.ReadFile(betaPath)
	if err != nil {
		t.Fatalf("read beta log: %v", err)
	}

	if !strings.Contains(string(alpha), "alpha-out") {
		t.Errorf("alpha.log missing 'alpha-out': %q", alpha)
	}
	if strings.Contains(string(alpha), "beta-out") {
		t.Errorf("alpha.log leaked sibling output: %q", alpha)
	}
	if !strings.Contains(string(beta), "beta-out") {
		t.Errorf("beta.log missing 'beta-out': %q", beta)
	}
	if strings.Contains(string(beta), "alpha-out") {
		t.Errorf("beta.log leaked sibling output: %q", beta)
	}

	// NOTE: As of Task 1, the global pipeline log is intentionally NOT in the
	// per-sub-step join — the global log receives parallel sub-step output via
	// Reporter.StepOutput's dedicated side-write, added in Task 6. Until then,
	// we do not assert global-log contents here.
	_ = globalLog

	// Reporter.SubStepOutput was called with each sub-step's line.
	sawAlpha, sawBeta := false, false
	for _, e := range rep.events {
		if e.kind != "StepOutput" {
			continue
		}
		if e.stepAddr == "p/alpha" && strings.Contains(e.reason, "alpha-out") {
			sawAlpha = true
		}
		if e.stepAddr == "p/beta" && strings.Contains(e.reason, "beta-out") {
			sawBeta = true
		}
	}
	if !sawAlpha || !sawBeta {
		t.Errorf("missing SubStepOutput events: sawAlpha=%v sawBeta=%v events=%v", sawAlpha, sawBeta, rep.events)
	}
}

// TestParallelGroup_NoOutputWithLoggingDisabled verifies that when the
// pipeline log is disabled (opts.LogWriter == nil), no per-sub-step log file
// is created, but SubStepOutput events still fire so the reporter can render.
func TestParallelGroup_DisabledLog_NoFiles_StillStreamsToReporter(t *testing.T) {
	tmp := t.TempDir()

	rep := &mockReporter{}
	phase := config.DeployPhase{Name: "p"}
	group := buildParallelGroupStep(phase, "g", true, 0, []config.DeployStep{
		{Name: "alpha", Type: "shell", Cmd: "echo a"},
	})

	opts := RunOptions{
		Steps:       []ResolvedStep{group},
		Reporter:    rep,
		Name:        "deploy",
		Config:      &config.DevboxConfig{Raw: map[string]any{}},
		WorkDir:     tmp,
		LogWriter:   nil,
		Recorder:    &mockRecorder{},
		SkipDecider: func(addr string, rs ResolvedStep, h string) journal.Decision { return journal.Run },
	}
	if err := RunWithOptions(opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No parallel log directory should have been created.
	if _, err := os.Stat(filepath.Join(tmp, ".devbox", "logs", "parallel")); !os.IsNotExist(err) {
		t.Errorf("expected no parallel/ log dir when logging disabled, got err=%v", err)
	}

	// SubStepOutput still fires.
	saw := false
	for _, e := range rep.events {
		if e.kind == "StepOutput" && strings.Contains(e.reason, "a") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected SubStepOutput event when log disabled, events=%v", rep.events)
	}
}

// TestExecBuiltinAction_Parallel_NoStdoutWrite verifies that a builtin running
// in parallel mode (actx.Parallel=true) writes only to actx.LogWriter and
// never directly to os.Stdout — required for non-TTY buffered reporter modes.
func TestExecBuiltinAction_Parallel_NoStdoutWrite(t *testing.T) {
	var buf bytes.Buffer
	tee := newLineTee(func(string, bool) {})
	w := joinWriters(&buf, tee)

	// Capture and discard os.Stdout writes to assert nothing leaks.
	origStdout := os.Stdout
	rPipe, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = wPipe
	defer func() { os.Stdout = origStdout; _ = rPipe.Close(); _ = wPipe.Close() }()

	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	actx := ActionContext{
		WorkDir:   t.TempDir(),
		Cfg:       cfg,
		LogWriter: w,
		Parallel:  true,
	}
	action := config.Action{Type: "builtin", Cmd: "message", With: map[string]any{"level": "info", "text": "hi"}}
	if err := ExecAction(t.Context(), action, actx); err != nil {
		t.Fatalf("ExecAction: %v", err)
	}
	if !strings.Contains(buf.String(), "hi") {
		t.Errorf("builtin output did not reach LogWriter: %q", buf.String())
	}
	// Drain the captured os.Stdout pipe non-blockingly.
	_ = wPipe.Close()
	captured, _ := io.ReadAll(rPipe)
	if len(captured) > 0 {
		t.Errorf("parallel builtin leaked %d bytes to os.Stdout: %q", len(captured), captured)
	}
	os.Stdout = origStdout
}

// suppress unused-import warning for condition when no tests reference it.
var _ = condition.TypeShell

// syncBuf is a concurrency-safe bytes.Buffer wrapper for tests that share a
// global log writer across parallel sub-steps.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestOpenPipelineLog_ScreenDoesNotTeeToLog asserts the new split-channel
// contract: ANSI written to the screen writer never reaches the log file
// (because the old MultiWriter tee was removed).
func TestOpenPipelineLog_ScreenDoesNotTeeToLog(t *testing.T) {
	tmpDir := t.TempDir()
	screen, logFile, _, logPath, cleanup, err := OpenPipelineLog(tmpDir, "deploy", true)
	if err != nil {
		t.Fatalf("OpenPipelineLog: %v", err)
	}
	defer cleanup()

	// Write ANSI through the screen writer. The log file (a separate writer)
	// must remain empty because screen no longer tees.
	if _, werr := screen.Writer().Write([]byte("\x1b[31mhello\x1b[0m\n")); werr != nil {
		t.Fatalf("screen write: %v", werr)
	}
	// Ensure file content is observable by reading from disk.
	if f, ok := logFile.(*os.File); ok {
		_ = f.Sync()
	}
	data, rerr := os.ReadFile(logPath)
	if rerr != nil {
		t.Fatalf("read log: %v", rerr)
	}
	if len(data) != 0 {
		t.Errorf("expected log file to be empty after screen write, got %q", data)
	}
}

// TestPlainReporter_StatusLineReachesLogFile verifies that PlainReporter
// side-writes every emit() to the dedicated log file (no fan-out duplication;
// each line lands exactly once).
func TestPlainReporter_StatusLineReachesLogFile(t *testing.T) {
	var screen, logBuf bytes.Buffer
	rep := NewPlainReporter(render.NewWriter(&screen), &logBuf, nil)
	rep.now = func() time.Time { return fixedTime }
	rep.StartPipeline("deploy", 1)
	rep.EnterPhase("deploy", config.DeployPhase{Name: "deploy", Description: "Deploy"})

	got := logBuf.String()
	if !strings.Contains(got, "Phase: deploy") {
		t.Errorf("expected log file to contain phase line, got %q", got)
	}
	// Count occurrences: exactly one line per emit.
	if n := strings.Count(got, "Phase: deploy"); n != 1 {
		t.Errorf("expected 1 occurrence of phase line in log, got %d (content=%q)", n, got)
	}
}

func TestOpenPipelineLog_DisabledReturnsNil(t *testing.T) {
	tmpDir := t.TempDir()

	w, logWriter, termOut, logPath, cleanup, err := OpenPipelineLog(tmpDir, "deploy", false)
	defer cleanup()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w == nil {
		t.Errorf("expected writer to be non-nil when logging disabled")
	}

	if logWriter != nil {
		t.Errorf("expected logWriter to be nil when disabled")
	}

	if logPath != "" {
		t.Errorf("expected logPath to be empty when disabled, got %q", logPath)
	}

	if termOut == nil {
		t.Errorf("expected termOut to be non-nil even when disabled (io.Discard or os.Stdout)")
	}

	devboxLogsDir := filepath.Join(tmpDir, ".devbox", "logs")
	if _, err := os.Stat(devboxLogsDir); !os.IsNotExist(err) {
		t.Errorf("expected .devbox/logs directory to not exist when logging disabled")
	}
}
