package mermaid

import (
	"os"
	"testing"
)

func TestCanInlineNoTmux(t *testing.T) {
	// Clear TMUX env if set.
	oldTmux := os.Getenv("TMUX")
	os.Unsetenv("TMUX")
	defer func() {
		if oldTmux != "" {
			os.Setenv("TMUX", oldTmux)
		}
	}()

	// CanInline should work (may be true or false depending on terminal capabilities).
	_ = CanInline()
}

func TestCanInlineWithTmux(t *testing.T) {
	// Set TMUX env.
	oldTmux := os.Getenv("TMUX")
	os.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	defer func() {
		if oldTmux != "" {
			os.Setenv("TMUX", oldTmux)
		} else {
			os.Unsetenv("TMUX")
		}
	}()

	// CanInline should return false under tmux.
	if CanInline() {
		t.Errorf("CanInline should return false under tmux")
	}
}
