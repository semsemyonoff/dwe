package render

import (
	"fmt"
	"strings"
	"unicode/utf8"

	figure "github.com/common-nighthawk/go-figure"
)

// asciiBlock holds the prepared output lines and the left padding for one text entry.
type asciiBlock struct {
	lines  []string
	prefix string
}

// prepareASCII renders text as plain ASCII art and computes the centering prefix.
// Callers wrap the output in lipgloss styling externally.
func prepareASCII(text, font string, totalWidth int) asciiBlock {
	plain := strings.TrimRight(figure.NewFigure(text, font, true).String(), "\n")
	lines := strings.Split(plain, "\n")

	maxWidth := 0
	for _, l := range lines {
		if n := utf8.RuneCountInString(l); n > maxWidth {
			maxWidth = n
		}
	}
	leftPad := max((totalWidth-maxWidth)/2, 0)

	return asciiBlock{
		lines:  lines,
		prefix: strings.Repeat(" ", leftPad),
	}
}

// ASCII generates centered plain ASCII art for each entry in lines using go-figure.
// Centering is relative to the configured line width. font defaults to "standard".
// Callers apply foreground color (e.g. accent) externally with lipgloss.
func (w *Writer) ASCII(lines []string, font string) error {
	if font == "" {
		font = "standard"
	}
	totalWidth := w.getLineWidth() + 2

	for _, text := range lines {
		block := prepareASCII(text, font, totalWidth)
		for _, l := range block.lines {
			_, _ = fmt.Fprintf(w.w, "%s%s\n", block.prefix, l)
		}
	}
	return nil
}
