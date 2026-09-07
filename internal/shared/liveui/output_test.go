package liveui

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

// syncBuf is a concurrency-safe bytes.Buffer wrapper for tests that share a
// writer across parallel goroutines.
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

func TestAnsiOnlyStripper_Write_StripsEscapes(t *testing.T) {
	var buf bytes.Buffer
	s := &ANSIOnlyStripper{W: &buf}
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
	s := &ANSIOnlyStripper{W: &buf}
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

// TestAnsiOnlyRe_PrivateModeCsi strips DEC private-mode sequences that
// contain `?` in the parameter (e.g. hide/show cursor, synchronized output).
func TestAnsiOnlyRe_PrivateModeCsi(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"hide cursor", "before\x1b[?25lafter", "beforeafter"},
		{"show cursor", "before\x1b[?25hafter", "beforeafter"},
		{"synchronized output on", "a\x1b[?2026hb", "ab"},
		{"synchronized output off", "a\x1b[?2026lb", "ab"},
		{"alt screen on", "a\x1b[?1049hb", "ab"},
		{"alt screen off", "a\x1b[?1049lb", "ab"},
		{"multi-param", "a\x1b[1;2;3mb", "ab"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(ANSIOnlyRe.ReplaceAll([]byte(tc.input), nil))
			if got != tc.want {
				t.Errorf("ANSIOnlyRe on %q: got %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestAnsiOnlyRe_OscBelTerminated strips OSC sequences terminated by BEL.
func TestAnsiOnlyRe_OscBelTerminated(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"window title", "a\x1b]0;My Window\x07b", "ab"},
		{"hyperlink", "a\x1b]8;;http://example.com\x07b", "ab"},
		{"empty osc", "a\x1b]0;\x07b", "ab"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(ANSIOnlyRe.ReplaceAll([]byte(tc.input), nil))
			if got != tc.want {
				t.Errorf("ANSIOnlyRe OSC on %q: got %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestAnsiOnlyRe_OscStTerminated strips OSC sequences terminated by ST (ESC \).
// OSC 8 hyperlinks from curl, git, and ls commonly use ST rather than BEL.
func TestAnsiOnlyRe_OscStTerminated(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"hyperlink st", "a\x1b]8;;http://example.com\x1b\\link\x1b]8;;\x1b\\b", "alinkb"},
		{"window title st", "a\x1b]0;My Window\x1b\\b", "ab"},
		{"empty osc st", "a\x1b]0;\x1b\\b", "ab"},
		{"st after bel-osc", "a\x1b]0;t\x07b\x1b]8;;u\x1b\\c", "abc"},
		// Regression: ST-terminated OSC followed by visible text followed by
		// BEL-terminated OSC. The old BEL branch [^\x07]*\x07 allowed ESC in
		// its content and would greedily match from the first ESC] all the way
		// to the trailing BEL, consuming the visible text between them.
		{"mixed terminator preserves link text", "before\x1b]8;;http://example.com\x1b\\VISIBLE\x1b]0;title\x07after", "beforeVISIBLEafter"},
		{"st-bel-st does not over-match", "\x1b]8;;u\x1b\\LINK\x1b]0;t\x07END", "LINKEND"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(ANSIOnlyRe.ReplaceAll([]byte(tc.input), nil))
			if got != tc.want {
				t.Errorf("ANSIOnlyRe ST-OSC on %q: got %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestAnsiOnlyRe_TwoByteSequences verifies that the expanded Fp/Fe/Fs branch
// strips common two-byte and intermediate-byte escape sequences that the old
// [a-zA-Z]-only branch missed: ESC 7 / ESC 8 (save/restore cursor from
// tput/ncurses), ESC = / ESC > (keypad modes), ESC ( B (charset designation),
// ESC # 8 (DEC screen alignment with intermediate byte).  Also verifies that
// ESC [ (CSI introducer), ESC ] (OSC introducer), and ESC \ (ST terminator)
// are NOT consumed by this branch so that split-sequence handling in the
// lineTee buffer is not disrupted.
func TestAnsiOnlyRe_TwoByteSequences(t *testing.T) {
	strip := func(in string) string {
		return string(ANSIOnlyRe.ReplaceAll([]byte(in), nil))
	}
	cases := []struct {
		name  string
		input string
		want  string
	}{
		// ESC 7 / ESC 8 — save/restore cursor (Fp class, final byte 0x37/0x38)
		{"ESC 7 save cursor", "before\x1b7after", "beforeafter"},
		{"ESC 8 restore cursor", "before\x1b8after", "beforeafter"},
		// ESC = / ESC > — application/normal keypad (Fp, 0x3D/0x3E)
		{"ESC = app keypad", "a\x1b=b", "ab"},
		{"ESC > normal keypad", "a\x1b>b", "ab"},
		// ESC M — reverse linefeed (Fe, 0x4D) — already covered; regression guard
		{"ESC M reverse linefeed", "a\x1bMb", "ab"},
		// ESC ( B — designate G0 charset to ASCII (with intermediate byte 0x28)
		{"ESC ( B charset", "a\x1b(Bb", "ab"},
		// ESC # 8 — DEC screen alignment test (with intermediate byte 0x23)
		{"ESC # 8 dec align", "a\x1b#8b", "ab"},
		// Excluded: an incomplete ESC [ (no final byte) must NOT be consumed
		// by the Fe branch. (A complete CSI like \x1b[b IS consumed by the CSI
		// branch — that is correct; the exclusion prevents the Fe branch from
		// eating just the \x1b[ introducer before the final byte arrives.)
		{"incomplete CSI not consumed by Fe", "a\x1b[", "a\x1b["},
		// Excluded: an incomplete ESC ] (no terminator) must NOT be consumed
		// by the Fe branch. (Complete OSC is handled by the OSC branch.)
		{"incomplete OSC not consumed by Fe", "a\x1b]", "a\x1b]"},
		// Excluded: ESC \ (ST terminator) must NOT be consumed per-Write so
		// that the ST in chunk 2 of a split "ESC]…ESC\" survives the per-Write
		// pass and the lineTee buffer can assemble the complete sequence.
		{"ESC \\ ST not consumed", "a\x1b\\", "a\x1b\\"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strip(tc.input)
			if got != tc.want {
				t.Errorf("ANSIOnlyRe on %q: got %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestSubStepLog_RoutedViaLineTee_SplitOSCClean pins the LineTee double-strip
// itself: routing log writes through the callback (not a stateless
// LogSanitizer branch) strips OSC sequences that were split across PTY read
// boundaries, because the tee assembles the full sequence in its buffer before
// emitting the frame. It does not pin any caller's frame policy — the pipeline
// sub-step log writes only committed frames (see FrameLogWriter and
// executor.go's parallel branch); this test writes both so the assertion is
// about escape stripping alone.
func TestSubStepLog_RoutedViaLineTee_SplitOSCClean(t *testing.T) {
	var logBuf bytes.Buffer
	// Write every assembled frame regardless of `final` — the sequence under
	// test spans frames, and the callback's own filtering is not the subject.
	tee := NewLineTee(func(frame string, final bool) {
		_, _ = fmt.Fprintln(&logBuf, frame)
	})
	// Simulate split PTY reads: OSC 8 hyperlink opener in chunk 1, ST
	// terminator + visible text + hyperlink closer in chunk 2.
	_, _ = tee.Write([]byte("\x1b]8;;https://example.com"))   // chunk 1
	_, _ = tee.Write([]byte("\x1b\\VISIBLE\x1b]8;;\x1b\\\n")) // chunk 2
	got := logBuf.String()
	if strings.Contains(got, "\x1b") {
		t.Errorf("sub-step log via lineTee: split OSC leaked ESC bytes: %q", got)
	}
	if !strings.Contains(got, "VISIBLE") {
		t.Errorf("sub-step log via lineTee: visible text missing from log: %q", got)
	}
}

// TestAnsiOnlyRe_PreservesCR ensures the regex used by the tee path leaves
// `\r` bytes intact (precondition for LineTee frame parsing).
func TestAnsiOnlyRe_PreservesCR(t *testing.T) {
	in := []byte("\x1b[32m50%\r100%\x1b[0m\n")
	got := ANSIOnlyRe.ReplaceAll(in, nil)
	want := "50%\r100%\n"
	if string(got) != want {
		t.Errorf("ANSIOnlyRe.ReplaceAll = %q, want %q (must preserve \\r)", got, want)
	}
}

// TestLogSanitizer_ProgressFrames_BecomeSeparateLines pins LogSanitizer's own
// choice, which survives for the reporter status-line path (plain.go): frames
// that overwrite each other on a real terminal land on separate lines. The
// pipeline log itself no longer goes through this writer — it collapses a
// redraw run to its last frame via FrameLogWriter, whose opposite choice is
// pinned in logframe_test.go.
func TestLogSanitizer_ProgressFrames_BecomeSeparateLines(t *testing.T) {
	var buf bytes.Buffer
	s := &LogSanitizer{W: &buf}
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
		t.Errorf("LogSanitizer left bare \\r in output: %q", got)
	}
	if strings.Contains(got, "50%100%") {
		t.Errorf("frames concatenated instead of separating: %q", got)
	}
}

func TestLogSanitizer_LoneCR_BecomesNewline(t *testing.T) {
	var buf bytes.Buffer
	s := &LogSanitizer{W: &buf}
	if _, err := s.Write([]byte("partial\r")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if buf.String() != "partial\n" {
		t.Errorf("lone \\r: got %q, want %q", buf.String(), "partial\n")
	}
}

// TestLogSanitizer_CRLFInOneWrite_CollapsesToOneNewline pins the
// PTY-friendly behaviour: `\r\n` within a single Write collapses to one
// `\n`. PTYs in cooked mode apply ONLCR and translate every `\n` from
// the child into `\r\n`, so without this collapse the log would have a
// blank line after every real line.
func TestLogSanitizer_CRLFInOneWrite_CollapsesToOneNewline(t *testing.T) {
	var buf bytes.Buffer
	s := &LogSanitizer{W: &buf}
	if _, err := s.Write([]byte("a\r\nb\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if buf.String() != "a\nb\n" {
		t.Errorf("CRLF: got %q, want %q", buf.String(), "a\nb\n")
	}
}

// TestLogSanitizer_SplitWrite_CRThenLF documents that a `\r` and `\n`
// arriving in separate Writes produce two newlines because the writer is
// stateless: the first Write turns `\r` into `\n` (committed), the second
// commits another `\n`. PTY output rarely splits CRLF this way, so the
// extra blank line is an accepted trade-off for keeping the writer
// stateless and lock-free.
func TestLogSanitizer_SplitWrite_CRThenLF(t *testing.T) {
	var buf bytes.Buffer
	s := &LogSanitizer{W: &buf}
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
	s := &LogSanitizer{W: &buf}
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
	tee := NewLineTee(cb)
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
	tee := NewLineTee(cb)
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
	tee := NewLineTee(cb)
	_, _ = tee.Write([]byte("hello\r\nworld\r\n"))
	want := []teeFrame{{"hello", true}, {"world", true}}
	if !equalFrames(*got, want) {
		t.Errorf("got %v, want %v", *got, want)
	}
}

// TestLineTee_FrameParsing_Mixed combines `\r`, `\n`, and CRLF in one stream.
func TestLineTee_FrameParsing_Mixed(t *testing.T) {
	got, cb := collectFrames()
	tee := NewLineTee(cb)
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
	tee := NewLineTee(cb)
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
	tee := NewLineTee(cb)
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
	tee := NewLineTee(cb)
	tee.Flush()
	if len(*got) != 0 {
		t.Errorf("expected no callbacks for empty stream, got %v", *got)
	}
}

// TestLineTee_FrameParsing_MultipleConsecutiveCRs verifies a run of `\r`
// produces empty in-progress frames.
func TestLineTee_FrameParsing_MultipleConsecutiveCRs(t *testing.T) {
	got, cb := collectFrames()
	tee := NewLineTee(cb)
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
	tee := NewLineTee(cb)
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

// TestLineTee_SplitWriteOSC_StrippedAtFrameEmit is the regression test for the
// split-write OSC leak. PTY reads can deliver an OSC sequence in two separate
// Write calls (e.g. the URL in one read and the ST terminator in the next).
// Neither partial matches the regex alone, but the assembled line in the buffer
// contains the complete sequence and must be stripped at frame-emit time.
func TestLineTee_SplitWriteOSC_StrippedAtFrameEmit(t *testing.T) {
	got, cb := collectFrames()
	tee := NewLineTee(cb)
	// Simulate a split PTY read: the OSC 8 hyperlink opener arrives in write 1,
	// the ST terminator + visible text + closer arrive in write 2.
	_, _ = tee.Write([]byte("\x1b]8;;https://example.com"))
	_, _ = tee.Write([]byte("\x1b\\VISIBLE\x1b]8;;\x1b\\\n"))
	want := []teeFrame{{"VISIBLE", true}}
	if !equalFrames(*got, want) {
		t.Errorf("split-write OSC: got %v, want %v", *got, want)
	}
	for _, f := range *got {
		if strings.Contains(f.frame, "\x1b") {
			t.Errorf("ANSI leaked into frame after split-write: %q", f.frame)
		}
	}
}

// TestLineTee_SplitWriteOSC_Flush_StrippedAtEmit verifies that trailing
// split-write OSC bytes in the buffer are stripped when Flush emits the tail.
func TestLineTee_SplitWriteOSC_Flush_StrippedAtEmit(t *testing.T) {
	got, cb := collectFrames()
	tee := NewLineTee(cb)
	_, _ = tee.Write([]byte("text\x1b]8;;https://example.com"))
	_, _ = tee.Write([]byte("\x1b\\more"))
	tee.Flush()
	if len(*got) != 1 {
		t.Fatalf("expected 1 frame, got %v", *got)
	}
	if strings.Contains((*got)[0].frame, "\x1b") {
		t.Errorf("ANSI leaked into flushed tail: %q", (*got)[0].frame)
	}
	if !strings.Contains((*got)[0].frame, "text") || !strings.Contains((*got)[0].frame, "more") {
		t.Errorf("visible text missing from flushed tail: %q", (*got)[0].frame)
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

func TestJoinWriters_AllNil_ReturnsDiscard(t *testing.T) {
	w := JoinWriters(nil, nil)
	if w != io.Discard {
		t.Errorf("JoinWriters(nil, nil) = %v, want io.Discard", w)
	}
}

func TestJoinWriters_SingleNonNil_ReturnsIt(t *testing.T) {
	var buf bytes.Buffer
	w := JoinWriters(nil, &buf, nil)
	if w != io.Writer(&buf) {
		t.Errorf("JoinWriters with single non-nil: got %v, want &buf", w)
	}
}

func TestJoinWriters_NilFiltering_DoesNotPanicOnMultiWriter(t *testing.T) {
	// io.MultiWriter cannot tolerate nil entries — it panics on first Write
	// when one is present. JoinWriters must filter nils before constructing
	// the MultiWriter. Regression: JoinWriters(nil, nil, &buf) returned the
	// buffer directly; this case has two non-nil entries plus a nil and
	// must still survive.
	var a, b bytes.Buffer
	w := JoinWriters(nil, &a, nil, &b)
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
	tee := NewLineTee(func(line string, _ bool) { got = line })
	w := JoinWriters(nil, nil, tee)
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
	tee := NewLineTee(func(line string, _ bool) {
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
