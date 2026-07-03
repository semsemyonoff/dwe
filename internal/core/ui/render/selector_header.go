package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
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

// BrandedTitleForConfig composes the branded full-screen-TUI title for a loaded
// config: `{▪} DWE · <project> · <base>`, using the **bare project name**
// (`cfg.Project.Name`, nil-safe) as the middot segment. It is the single source
// the status / docs / commands TUIs share for their status-line brand, so they
// cannot drift — in particular it takes the *config*, not a pre-resolved name
// string, so the compose project name (e.g. `dwe_tbm`) can never leak into a
// title where the display name (`tbm`) belongs.
func BrandedTitleForConfig(cfg *config.DweConfig, base string) string {
	name := ""
	if cfg != nil {
		name = cfg.Project.Name
	}
	return BrandedSelectorTitle(name, base)
}

// PrintSelectorHeader writes a styled subheader line containing the branded
// SelectorTitle for the given project and base, terminated with a newline.
// Centralizes the styling so every selector and TUI advertises the project
// consistently.
func PrintSelectorHeader(w io.Writer, projectName, base string) {
	_, _ = fmt.Fprintln(w, styles.StyleSubheader(BrandedSelectorTitle(projectName, base)))
}
