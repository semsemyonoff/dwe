package render

import (
	"bytes"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	sharedrender "github.com/semsemyonoff/dwe/internal/shared/render"
)

// Brand is the input to BrandHeader. Project and Version drive the
// always-emitted identity line; Tagline and Lines are optional decorations.
type Brand struct {
	Project string
	Version string
	Tagline string
	Lines   []string
	Font    string
}

// BrandHeader returns the branded header block:
//
//   - "Devbox · <project> · <version>" identity line (always emitted)
//   - optional tagline in muted color
//   - optional ASCII art block in accent color, preceded by a blank line
//
// The returned string ends with a single trailing newline so callers can write
// it directly without re-adding spacing.
func BrandHeader(h Brand) string {
	var sb strings.Builder

	parts := []string{LogoMark() + " " + styles.AccentStyle().Bold(true).Render("Devbox")}
	if h.Project != "" {
		parts = append(parts, styles.TextStyle().Render(h.Project))
	}
	if h.Version != "" {
		parts = append(parts, styles.MutedStyle().Render(h.Version))
	}
	sep := " " + styles.MutedStyle().Render("·") + " "
	sb.WriteString(strings.Join(parts, sep))
	sb.WriteByte('\n')

	if h.Tagline != "" {
		sb.WriteString(styles.MutedStyle().Render(h.Tagline))
		sb.WriteByte('\n')
	}

	if len(h.Lines) > 0 {
		var buf bytes.Buffer
		w := sharedrender.NewWriter(&buf)
		if err := w.ASCII(h.Lines, h.Font); err == nil && buf.Len() > 0 {
			sb.WriteByte('\n')
			sb.WriteString(styles.AccentStyle().Render(strings.TrimRight(buf.String(), "\n")))
			sb.WriteByte('\n')
		}
	}

	return sb.String()
}
