package tui

import (
	"bytes"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func TestCopyViaOSC52(t *testing.T) {
	text := "Hello, World!"
	buf := &bytes.Buffer{}

	err := CopyViaOSC52(text, buf)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	output := buf.String()
	// OSC 52 format: \x1b]52;c;<base64>\x07
	expected := base64.StdEncoding.EncodeToString([]byte(text))
	if !strings.Contains(output, expected) {
		t.Errorf("expected output to contain '%s', got '%s'", expected, output)
	}

	if !strings.HasPrefix(output, "\x1b]52;c;") {
		t.Errorf("expected output to start with OSC 52 prefix")
	}
	if !strings.HasSuffix(output, "\x07") {
		t.Errorf("expected output to end with BEL character")
	}
}

func TestCopyViaOSC52Encoding(t *testing.T) {
	// Test with special characters
	text := "Line 1\nLine 2\t\t\rSpecial: é"
	buf := &bytes.Buffer{}

	err := CopyViaOSC52(text, buf)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	output := buf.String()
	expected := base64.StdEncoding.EncodeToString([]byte(text))

	if !strings.Contains(output, expected) {
		t.Errorf("expected output to contain base64-encoded text")
	}
}

func TestClipboardTmuxHint(t *testing.T) {
	// Save original env
	originalTmux := os.Getenv("TMUX")
	defer func() {
		if originalTmux != "" {
			os.Setenv("TMUX", originalTmux)
		} else {
			os.Unsetenv("TMUX")
		}
	}()

	// Test with TMUX set
	os.Setenv("TMUX", "/tmp/tmux-1000/default")
	if !ClipboardTmuxHint() {
		t.Errorf("expected ClipboardTmuxHint to return true when TMUX is set")
	}

	// Test without TMUX set
	os.Unsetenv("TMUX")
	if ClipboardTmuxHint() {
		t.Errorf("expected ClipboardTmuxHint to return false when TMUX is unset")
	}
}
