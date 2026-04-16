// Package ui provides Lipgloss-based styled rendering for the devbox CLI.
// It is separate from internal/render, which handles plain ANSI output used by
// deploy, docker, and passthrough commands.
package ui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
)

// Styles follow the Fang aesthetic: simple, high-contrast, terminal-friendly.
// All styles use ANSI 256-color palette codes for broad terminal compatibility.
var (
	// styleKey renders definition labels: bold bright-blue.
	styleKey = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)

	// styleSectionTitle renders section headers: bold cyan.
	styleSectionTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))

	// styleSubheader renders in-section subheaders: bold yellow.
	styleSubheader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))

	// styleMuted renders secondary / count text: dim gray.
	styleMuted = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	// styleWarn renders warning messages: yellow.
	styleWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))

	// styleInfoText renders info messages: bright blue / cyan.
	styleInfoText = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))

	// styleValue renders definition values: no styling (inherits terminal default).
	styleValue = lipgloss.NewStyle()
)

// defSep is the delimiter used between a definition label and its value.
const defSep = "—"

// TermWidth returns the current terminal width, falling back to 80 when the
// output is not a terminal or the size cannot be determined.
func TermWidth() int {
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w <= 0 {
		return 80
	}
	return w
}
