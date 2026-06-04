package tui

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/docs"
)

// headingMarkerRE matches the unique anchor we inject before every H2/H3
// heading text in the markdown source. The double-dagger (U+2021) is rare
// enough in user docs to be safe, and the all-letters-then-digits-then-
// daggers shape is glamour-stable: glamour wraps on word boundaries and
// also re-applies styling around tokens containing word-class breaks like
// underscores, so the marker deliberately avoids `_`. With this shape the
// whole token stays intact in the rendered output.
var headingMarkerRE = regexp.MustCompile(`\x{2021}DBXHDR(\d+)\x{2021}`)

func headingMarker(idx int) string {
	return fmt.Sprintf("‡DBXHDR%d‡", idx)
}

// preprocessHeadings injects a unique anchor token before each H2/H3
// heading's text so the rendered output carries a reliable per-heading
// position. The previous substring-on-heading-text approach is fragile:
// body prose routinely references the same words as the heading, and the
// first match wins (typically the intro paragraph, not the heading
// itself), so the viewport "scrolls" by only a few lines instead of
// jumping to the section.
//
// Anchors are inserted between the leading `#`s and the heading text so
// that glamour keeps them inside the styled heading line. Fenced code
// blocks are skipped so `#` comments inside shell snippets are not
// mistaken for headings. The returned byte slice is the modified markdown
// ready to feed to glamour.Render.
func preprocessHeadings(input []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(input) + 64)
	scanner := bufio.NewScanner(bytes.NewReader(input))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	inFence := false
	idx := 0
	for scanner.Scan() {
		line := scanner.Text()
		trim := strings.TrimSpace(line)
		if docs.IsFenceLine(trim) {
			inFence = !inFence
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}
		if !inFence {
			i := 0
			for i < len(line) && line[i] == '#' {
				i++
			}
			if (i == 2 || i == 3) && i < len(line) && (line[i] == ' ' || line[i] == '\t') {
				out.WriteString(line[:i+1])
				out.WriteString(headingMarker(idx))
				out.WriteString(line[i+1:])
				out.WriteByte('\n')
				idx++
				continue
			}
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// stripHeadingMarkers walks the rendered output, records the rendered
// line number where each marker appears (keyed by source heading index),
// and returns the output with markers removed so the user never sees them.
//
// stripANSI is applied per line before the regex match so glamour's
// styling around the marker doesn't break detection; the visible line is
// scrubbed with a literal regex replace afterwards. lineByIdx[i] holds the
// rendered line of the i-th source heading; -1 means the marker was lost
// (shouldn't happen for well-formed input — kept defensive so the index
// stays aligned with the source).
func stripHeadingMarkers(rendered string) (string, []int) {
	lines := strings.Split(rendered, "\n")
	var lineByIdx []int
	for i := range lines {
		plain := stripANSI(lines[i])
		for _, m := range headingMarkerRE.FindAllStringSubmatchIndex(plain, -1) {
			idx, _ := strconv.Atoi(plain[m[2]:m[3]])
			for len(lineByIdx) <= idx {
				lineByIdx = append(lineByIdx, -1)
			}
			if lineByIdx[idx] == -1 {
				lineByIdx[idx] = i
			}
		}
		lines[i] = headingMarkerRE.ReplaceAllString(lines[i], "")
	}
	return strings.Join(lines, "\n"), lineByIdx
}
