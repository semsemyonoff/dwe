package ui

import (
	"bytes"
	"strings"

	"devbox-cli/internal/shared/render"
)

// BrandHeader is the input to RenderBrandHeader. Project and Version drive the
// always-emitted identity line; Tagline and Lines are optional decorations.
type BrandHeader struct {
	Project string
	Version string
	Tagline string
	Lines   []string
	Font    string
}

// RenderBrandHeader returns the branded header block:
//
//   - "Devbox · <project> · <version>" identity line (always emitted)
//   - optional tagline in muted color
//   - optional ASCII art block in accent color, preceded by a blank line
//
// The returned string ends with a single trailing newline so callers can write
// it directly without re-adding spacing.
func RenderBrandHeader(h BrandHeader) string {
	var sb strings.Builder

	parts := []string{LogoMark() + " " + styleAccent.Bold(true).Render("Devbox")}
	if h.Project != "" {
		parts = append(parts, styleText.Render(h.Project))
	}
	if h.Version != "" {
		parts = append(parts, styleMuted.Render(h.Version))
	}
	sep := " " + styleMuted.Render("·") + " "
	sb.WriteString(strings.Join(parts, sep))
	sb.WriteByte('\n')

	if h.Tagline != "" {
		sb.WriteString(styleMuted.Render(h.Tagline))
		sb.WriteByte('\n')
	}

	if len(h.Lines) > 0 {
		var buf bytes.Buffer
		w := render.NewWriter(&buf)
		if err := w.ASCII(h.Lines, h.Font); err == nil && buf.Len() > 0 {
			sb.WriteByte('\n')
			sb.WriteString(styleAccent.Render(strings.TrimRight(buf.String(), "\n")))
			sb.WriteByte('\n')
		}
	}

	return sb.String()
}
