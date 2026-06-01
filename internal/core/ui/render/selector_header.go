package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// SelectorTitle composes an interactive-prompt header from a fixed "DWE"
// prefix, the project name (when set), and a base label, joined with
// middots. Used by selectors and TUI titles to advertise which dwe project
// owns the prompt window regardless of project context.
func SelectorTitle(projectName, base string) string {
	parts := []string{"DWE"}
	if projectName != "" {
		parts = append(parts, projectName)
	}
	parts = append(parts, base)
	return strings.Join(parts, " · ")
}

// BrandedSelectorTitle returns SelectorTitle prefixed with the plain DWE
// logomark — `{▪} DWE · <project> · <base>`. The plain (unstyled) logomark is
// used so a single outer style (e.g. styles.StyleSubheader for non-TUI
// surfaces, lipgloss accent+bold for TUI title bars) colors the whole line
// uniformly without conflicting escape codes.
func BrandedSelectorTitle(projectName, base string) string {
	return LogoMarkPlain() + " " + SelectorTitle(projectName, base)
}

// PrintSelectorHeader writes a styled subheader line containing the branded
// SelectorTitle for the given project and base, terminated with a newline.
// Centralizes the styling so every selector and TUI advertises the project
// consistently.
func PrintSelectorHeader(w io.Writer, projectName, base string) {
	_, _ = fmt.Fprintln(w, styles.StyleSubheader(BrandedSelectorTitle(projectName, base)))
}
