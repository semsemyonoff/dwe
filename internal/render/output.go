package render

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

const defaultLineWidth = 76

// Writer handles formatted terminal output, mirroring the legacy make functions:
// ok, err, warn, inf, dfn, line_text, table_header, table_subheader, spc.
type Writer struct {
	w         io.Writer
	lineWidth int // 0 means use defaultLineWidth
}

// NewWriter creates a Writer that writes to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// Stdout returns a Writer that writes to os.Stdout.
func Stdout() *Writer {
	return NewWriter(os.Stdout)
}

// SetLineWidth sets the line width used for headers and text wrapping.
// Values ≤ 0 are ignored (keeps the current/default width).
func (w *Writer) SetLineWidth(n int) {
	if n > 0 {
		w.lineWidth = n
	}
}

// getLineWidth returns the effective line width.
func (w *Writer) getLineWidth() int {
	if w.lineWidth > 0 {
		return w.lineWidth
	}
	return defaultLineWidth
}

// Success prints a green success message.
func (w *Writer) Success(msg string) {
	_, _ = fmt.Fprintf(w.w, "%s%s%s\n", Green, msg, Reset)
}

// Error prints a red error message.
func (w *Writer) Error(msg string) {
	_, _ = fmt.Fprintf(w.w, "%s%s%s\n", Red, msg, Reset)
}

// Warning prints a yellow warning message, wrapping at word boundaries to fit the
// configured line width.
func (w *Writer) Warning(msg string) {
	for _, line := range wordWrap(msg, w.getLineWidth()+2) {
		_, _ = fmt.Fprintf(w.w, "%s%s%s\n", Yellow, line, Reset)
	}
}

// Info prints a blue info message, wrapping at word boundaries to fit the
// configured line width.
func (w *Writer) Info(msg string) {
	for _, line := range wordWrap(msg, w.getLineWidth()+2) {
		_, _ = fmt.Fprintf(w.w, "%s%s%s\n", Blue, line, Reset)
	}
}

// Definition prints a definition entry with the name in blue:
//
//	[icon ]name — first line of value
//	            continuation aligned under the value
//
// Long values are wrapped at word boundaries; each continuation line is
// indented to align with the start of the value on the first line.
// indent controls leading whitespace; icon is an optional symbol before the
// name (empty = no icon); delim defaults to "—".
func (w *Writer) Definition(name, desc string, indent int, icon string, delim ...string) {
	d := "—"
	if len(delim) > 0 && delim[0] != "" {
		d = delim[0]
	}

	iconPrefix := ""
	iconWidth := 0
	if icon != "" {
		iconWidth = utf8.RuneCountInString(icon) + 1 // icon + space
		iconPrefix = icon + " "
	}

	// Visible overhead on the first line: indent + icon + name + " " + delim + " "
	overhead := indent + iconWidth + utf8.RuneCountInString(name) + 1 + utf8.RuneCountInString(d) + 1
	maxDesc := w.getLineWidth() + 2 - overhead

	lines := wordWrap(desc, maxDesc)

	prefix := strings.Repeat(" ", indent)
	_, _ = fmt.Fprintf(w.w, "%s%s%s%s%s %s %s\n", prefix, iconPrefix, Blue, name, Reset, d, lines[0])

	// Continuation lines are aligned under the value (not the name).
	contPrefix := strings.Repeat(" ", overhead)
	for _, l := range lines[1:] {
		_, _ = fmt.Fprintf(w.w, "%s%s\n", contPrefix, l)
	}
}

// LineText prints a line with centered text, following the legacy line_text macro:
//
//	output = lead + padding(sym) + text + padding(sym) + lead
//
// Parameters:
//   - color: ANSI color constant (empty → Reset)
//   - lead:  border character (default "-")
//   - total: width used to compute padding (0 → writer's line width)
//   - sym:   padding fill character (default "-")
func (w *Writer) LineText(text, color, lead string, total int, sym string) {
	if color == "" {
		color = Reset
	}
	if lead == "" {
		lead = "-"
	}
	if total == 0 {
		total = w.getLineWidth()
	}
	if sym == "" {
		sym = "-"
	}

	textLen := utf8.RuneCountInString(text)
	padding := max((total-textLen)/2, 0)
	pad := strings.Repeat(sym, padding)

	_, _ = fmt.Fprintf(w.w, "%s%s%s%s%s%s%s\n", color, lead, pad, text, pad, lead, Reset)
}

// TableHeader prints a green "+---Title---+" line using the configured line width.
// Empty text produces a closing footer line.
func (w *Writer) TableHeader(text string) {
	w.LineText(text, Green, "+", w.getLineWidth(), "-")
}

// TableSubheader prints a yellow "---Title---" line using the configured line width.
func (w *Writer) TableSubheader(text string) {
	w.LineText(text, Yellow, "-", w.getLineWidth(), "-")
}

// Println prints text followed by a newline.
func (w *Writer) Println(s string) {
	_, _ = fmt.Fprintln(w.w, s)
}

// wordWrap splits text into lines where each line is at most width runes wide.
// Breaks at the last space within positions [1, width] (inclusive) when possible,
// otherwise hard-breaks at width. Always returns at least one element.
func wordWrap(text string, width int) []string {
	if width <= 0 || utf8.RuneCountInString(text) <= width {
		return []string{text}
	}

	runes := []rune(text)
	var lines []string

	for len(runes) > 0 {
		if len(runes) <= width {
			lines = append(lines, string(runes))
			break
		}

		// Search from position width down to 1 for a space to break on.
		// Searching from width (not width-1) lets "hello world" (exactly width=11)
		// break at the trailing space rather than inside the word.
		cut := -1
		for i := width; i >= 1; i-- {
			if runes[i] == ' ' {
				cut = i
				break
			}
		}

		if cut < 0 {
			// No space found — hard-break at width.
			lines = append(lines, string(runes[:width]))
			runes = runes[width:]
		} else {
			lines = append(lines, string(runes[:cut]))
			runes = runes[cut+1:] // skip the space
		}
	}

	return lines
}
