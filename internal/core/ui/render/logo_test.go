package render

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"devbox-cli/internal/core/ui/styles"
)

// stripANSI removes ANSI escape sequences from s for plain-text comparison.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if inEsc {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
			continue
		}
		if r == 0x1b {
			inEsc = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestLogoMarkPlainNoEscape(t *testing.T) {
	got := LogoMarkPlain()
	if got != "{▪}" {
		t.Fatalf("LogoMarkPlain = %q, want %q", got, "{▪}")
	}
	if strings.ContainsRune(got, 0x1b) {
		t.Fatalf("LogoMarkPlain contains escape codes: %q", got)
	}
}

func TestLogoMarkStripsToPlain(t *testing.T) {
	// Force a color profile so LogoMark emits escapes even in tests.
	saved := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(saved) })

	styles.ApplyStyles(nil)
	mark := LogoMark()
	if stripped := stripANSI(mark); stripped != "{▪}" {
		t.Fatalf("stripANSI(LogoMark) = %q, want %q", stripped, "{▪}")
	}
	if stripANSI(LogoMarkPlain()) != stripANSI(LogoMark()) {
		t.Fatalf("LogoMark and LogoMarkPlain disagree on plain glyph")
	}
}

func TestLogoMarkContainsAccentWhenColorProfile(t *testing.T) {
	saved := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(saved) })

	styles.ApplyStyles(nil)
	mark := LogoMark()
	if !strings.ContainsRune(mark, 0x1b) {
		t.Fatalf("LogoMark under ANSI256 profile has no escape codes: %q", mark)
	}
}
