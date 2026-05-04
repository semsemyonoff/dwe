package pipeline

import (
	"bytes"
	"strings"
	"testing"
)

func TestAnsiStripper_Write_StripsEscapes(t *testing.T) {
	var buf bytes.Buffer
	s := &ansiStripper{w: &buf}
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

func TestAnsiStripper_Write_PlainText(t *testing.T) {
	var buf bytes.Buffer
	s := &ansiStripper{w: &buf}
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
