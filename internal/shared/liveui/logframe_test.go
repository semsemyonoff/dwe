package liveui

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
)

// TestFrameLogWriter_FrameSemantics pins the frame table from the plan: a
// committed line reaches the log once, a redraw run collapses to its last
// frame, and a run left un-terminated survives only through Flush.
func TestFrameLogWriter_FrameSemantics(t *testing.T) {
	cases := []struct {
		name  string
		write string
		flush bool
		want  string
	}{
		{name: "progress run ends with newline", write: "10%\r50%\r100%\n", want: "100%\n"},
		{name: "progress run ends with bare CR", write: "10%\r50%\r", flush: true, want: "50%\n"},
		{name: "progress run without flush loses tail", write: "10%\r50%\r", want: ""},
		{name: "plain lines", write: "a\nb\n", want: "a\nb\n"},
		{name: "crlf lines", write: "a\r\nb\n", want: "a\nb\n"},
		{name: "shorter overwrite records last frame only", write: "abc\rX\n", want: "X\n"},
		{name: "empty input", write: "", flush: true, want: ""},
		{name: "blank line is preserved", write: "a\n\nb\n", want: "a\n\nb\n"},
		{name: "unterminated tail without CR", write: "tail", flush: true, want: "tail\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := NewFrameLogWriter(&buf)
			if tc.write != "" {
				n, err := w.Write([]byte(tc.write))
				if err != nil {
					t.Fatalf("write: %v", err)
				}
				if n != len(tc.write) {
					t.Fatalf("Write returned %d, want %d", n, len(tc.write))
				}
			}
			if tc.flush {
				if err := w.Flush(); err != nil {
					t.Fatalf("flush: %v", err)
				}
			}
			if got := buf.String(); got != tc.want {
				t.Errorf("log contents = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFrameLogWriter_SplitWrites covers frames that straddle a PTY read
// boundary: a frame split mid-text, and a CRLF whose `\r` and `\n` land in
// different Writes (the case LogSanitizer turns into a spurious blank line).
func TestFrameLogWriter_SplitWrites(t *testing.T) {
	cases := []struct {
		name   string
		writes []string
		want   string
	}{
		{name: "frame split mid text", writes: []string{"hel", "lo\n"}, want: "hello\n"},
		{name: "crlf split across writes", writes: []string{"a\r", "\nb\n"}, want: "a\nb\n"},
		{name: "redraw split across writes", writes: []string{"10", "%\r100%", "\n"}, want: "100%\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := NewFrameLogWriter(&buf)
			for _, chunk := range tc.writes {
				if _, err := w.Write([]byte(chunk)); err != nil {
					t.Fatalf("write %q: %v", chunk, err)
				}
			}
			if got := buf.String(); got != tc.want {
				t.Errorf("log contents = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFrameLogWriter_SplitANSISequence pins the double-strip inherited from
// LineTee: an escape sequence split across two Writes matches neither half on
// the per-write pass but is complete in the buffer at frame-emit time. The
// stateless LogSanitizer cannot do this.
func TestFrameLogWriter_SplitANSISequence(t *testing.T) {
	var buf bytes.Buffer
	w := NewFrameLogWriter(&buf)
	_, _ = w.Write([]byte("\x1b]8;;https://example.com"))
	_, _ = w.Write([]byte("\x1b\\VISIBLE\x1b]8;;\x1b\\\n"))
	got := buf.String()
	if strings.Contains(got, "\x1b") {
		t.Errorf("split OSC leaked ESC bytes into the log: %q", got)
	}
	if got != "VISIBLE\n" {
		t.Errorf("log contents = %q, want %q", got, "VISIBLE\n")
	}
}

func TestFrameLogWriter_FlushIsIdempotent(t *testing.T) {
	var buf bytes.Buffer
	w := NewFrameLogWriter(&buf)
	if _, err := w.Write([]byte("50%\r")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("second flush: %v", err)
	}
	if got := buf.String(); got != "50%\n" {
		t.Errorf("double Flush emitted %q, want %q", got, "50%\n")
	}
}

func TestFrameLogWriter_FlushOnEmptyWriterEmitsNothing(t *testing.T) {
	var buf bytes.Buffer
	w := NewFrameLogWriter(&buf)
	if err := w.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := buf.String(); got != "" {
		t.Errorf("Flush on an untouched writer emitted %q, want nothing", got)
	}
}

// shortWriter reports fewer bytes than it was given, which is what makes
// io.MultiWriter raise io.ErrShortWrite.
type shortWriter struct{ buf bytes.Buffer }

func (s *shortWriter) Write(p []byte) (int, error) {
	s.buf.Write(p)
	return len(p) - 1, nil
}

type failingWriter struct{ err error }

func (f *failingWriter) Write([]byte) (int, error) { return 0, f.err }

// TestFrameLogWriter_WriteReportsFullLength guards the io.MultiWriter contract:
// stepWriter fans out to stdout and the log, so anything but len(p) fails the
// step with io.ErrShortWrite.
func TestFrameLogWriter_WriteReportsFullLength(t *testing.T) {
	w := NewFrameLogWriter(&shortWriter{})
	in := []byte("line\n")
	n, err := w.Write(in)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != len(in) {
		t.Errorf("Write returned %d, want %d", n, len(in))
	}
}

// TestFrameLogWriter_WriteSurfacesUnderlyingError checks the failure is
// reported rather than swallowed — the byte count stays len(p) either way.
func TestFrameLogWriter_WriteSurfacesUnderlyingError(t *testing.T) {
	sentinel := errors.New("disk full")
	w := NewFrameLogWriter(&failingWriter{err: sentinel})
	in := []byte("line\n")
	n, err := w.Write(in)
	if !errors.Is(err, sentinel) {
		t.Errorf("Write error = %v, want %v", err, sentinel)
	}
	if n != len(in) {
		t.Errorf("Write returned %d, want %d", n, len(in))
	}
}

// TestFrameLogWriter_FlushSurfacesUnderlyingError covers the one frame no
// caller can see fail any other way: a step whose whole output is an
// un-terminated redraw run writes nothing until Flush, so without this return
// a dead log sink would be silent end to end.
func TestFrameLogWriter_FlushSurfacesUnderlyingError(t *testing.T) {
	sentinel := errors.New("disk full")
	w := NewFrameLogWriter(&failingWriter{err: sentinel})
	// Ends on a bare `\r`: nothing is committed, so Write itself never touches
	// the destination and returns nil.
	if _, err := w.Write([]byte("50%\r")); err != nil {
		t.Fatalf("write on an uncommitted frame returned %v, want nil", err)
	}
	if err := w.Flush(); !errors.Is(err, sentinel) {
		t.Errorf("Flush error = %v, want %v", err, sentinel)
	}
	// Idempotency survives the failure: the pending frame is cleared either way,
	// so a second Flush has nothing left to fail on.
	if err := w.Flush(); err != nil {
		t.Errorf("second Flush error = %v, want nil", err)
	}
}

// TestFrameLogWriter_ConcurrentWrites_NoPanic mirrors
// TestLogSanitizer_ConcurrentWrites_NoPanic: stepWriter also reaches builtins
// as ActionContext.StepWriter, so the pending-frame state must survive
// concurrent writers. Aimed at `make test-race`.
func TestFrameLogWriter_ConcurrentWrites_NoPanic(t *testing.T) {
	var buf syncBuf
	w := NewFrameLogWriter(&buf)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				_, _ = w.Write([]byte("frame\r"))
				_, _ = w.Write([]byte("done\n"))
			}
		})
	}
	wg.Wait()
	if err := w.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "\r") {
		t.Errorf("expected no \\r in output, got %q", out)
	}
	if strings.Contains(out, "frame") {
		t.Errorf("non-final frames must be evicted by the committed line, got %q", out)
	}
	if n := strings.Count(out, "done\n"); n != 400 {
		t.Errorf("committed lines = %d, want 400", n)
	}
}
