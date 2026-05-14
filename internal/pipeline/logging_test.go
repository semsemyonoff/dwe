package pipeline

import (
	"bytes"
	"os"
	"path/filepath"
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

func TestOpenPipelineLog_CreatesDevboxLogsDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	_, logWriter, logPath, cleanup, err := OpenPipelineLog(tmpDir, "deploy", true)
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

func TestOpenPipelineLog_DisabledReturnsNil(t *testing.T) {
	tmpDir := t.TempDir()

	w, logWriter, logPath, cleanup, err := OpenPipelineLog(tmpDir, "deploy", false)
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

	devboxLogsDir := filepath.Join(tmpDir, ".devbox", "logs")
	if _, err := os.Stat(devboxLogsDir); !os.IsNotExist(err) {
		t.Errorf("expected .devbox/logs directory to not exist when logging disabled")
	}
}
