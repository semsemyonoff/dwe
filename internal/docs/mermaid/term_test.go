package mermaid

import (
	"os"
	"testing"
)

func TestCanInlineNoTmux(t *testing.T) {
	old := os.Getenv("TMUX")
	if err := os.Unsetenv("TMUX"); err != nil {
		t.Fatalf("unsetenv TMUX: %v", err)
	}
	t.Cleanup(func() {
		if old != "" {
			_ = os.Setenv("TMUX", old)
		}
	})
	_ = CanInline()
}

func TestCanInlineWithTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")

	if CanInline() {
		t.Errorf("CanInline should return false under tmux")
	}
}
