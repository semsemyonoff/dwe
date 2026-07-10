package envtest

import (
	"bytes"
	"testing"
)

func TestStripANSI_WholeSequences(t *testing.T) {
	var buf bytes.Buffer
	w := stripANSI(&buf)
	in := "\x1b[38;2;239;68;68mDWE checked\x1b[0m\nplain line\n"
	n, err := w.Write([]byte(in))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(in) {
		t.Fatalf("Write returned n=%d, want %d", n, len(in))
	}
	if got, want := buf.String(), "DWE checked\nplain line\n"; got != want {
		t.Fatalf("stripped = %q, want %q", got, want)
	}
}

func TestStripANSI_SplitSequenceAcrossWrites(t *testing.T) {
	var buf bytes.Buffer
	w := stripANSI(&buf)
	// A CSI sequence split mid-way across two writes must not leak bytes.
	if _, err := w.Write([]byte("hello \x1b[38")); err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	// The incomplete "\x1b[38" is held back — only "hello " is emitted so far.
	if got := buf.String(); got != "hello " {
		t.Fatalf("after write 1 = %q, want %q", got, "hello ")
	}
	if _, err := w.Write([]byte(";2;0;0;0mworld\x1b[0m")); err != nil {
		t.Fatalf("Write 2: %v", err)
	}
	if got, want := buf.String(), "hello world"; got != want {
		t.Fatalf("stripped = %q, want %q", got, want)
	}
}

func TestStripANSI_OSCSplit(t *testing.T) {
	var buf bytes.Buffer
	w := stripANSI(&buf)
	// OSC terminated by ST (ESC \) split across writes.
	_, _ = w.Write([]byte("a\x1b]11;?"))
	if got := buf.String(); got != "a" {
		t.Fatalf("after osc write 1 = %q, want %q", got, "a")
	}
	_, _ = w.Write([]byte("\x1b\\b"))
	if got, want := buf.String(), "ab"; got != want {
		t.Fatalf("stripped = %q, want %q", got, want)
	}
}

func TestStripANSI_PlainPassthrough(t *testing.T) {
	var buf bytes.Buffer
	w := stripANSI(&buf)
	_, _ = w.Write([]byte("no escapes here\n"))
	if got, want := buf.String(), "no escapes here\n"; got != want {
		t.Fatalf("plain = %q, want %q", got, want)
	}
}
