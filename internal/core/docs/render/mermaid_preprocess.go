package render

import (
	"bytes"
	"regexp"
)

var fenceStartRE = regexp.MustCompile("^```(\\w*)")
var fenceCloseRE = regexp.MustCompile("^```\\s*$")

// MermaidPlaceholder defines a mermaid block placeholder and the error it represents.
type MermaidPlaceholder struct {
	Text string
	Err  string // empty if no error
}

// PlaceholderFunc returns the placeholder text/error for a diagram based on its renderer state.
type PlaceholderFunc func(index int) MermaidPlaceholder

// PreprocessMermaid scans markdown and replaces mermaid fenced blocks with placeholders.
// Returns the modified markdown, a list of extracted diagrams, and any non-mermaid processing errors.
// When placeholderFunc is nil, mermaid blocks are left verbatim (raw code blocks pass through to glamour).
// Edge cases:
// - Nested fences are NOT handled specially; only the first ```mermaid opens a block.
// - A block without a closing ``` consumes lines to EOF and emits a placeholder + warning.
func PreprocessMermaid(input []byte, placeholderFunc PlaceholderFunc) (output []byte, diagrams []DiagramRef, err error) {
	if placeholderFunc == nil {
		return input, nil, nil
	}
	lines := bytes.Split(input, []byte("\n"))
	var result [][]byte
	var foundDiagrams []DiagramRef

	diagramIndex := 0
	i := 0

	for i < len(lines) {
		line := lines[i]
		matches := fenceStartRE.FindSubmatch(line)

		if matches != nil {
			lang := string(matches[1])

			if lang == "mermaid" {
				// Capture the mermaid source
				var sourceLines [][]byte
				startIdx := i + 1
				closingIdx := -1

				// Find the closing ``` fence (bare backticks only, no language tag).
				// A fence opener like ```go does NOT close a mermaid block.
				for j := startIdx; j < len(lines); j++ {
					if fenceCloseRE.Match(lines[j]) {
						closingIdx = j
						break
					}
					sourceLines = append(sourceLines, lines[j])
				}

				// If no closing fence, consume to EOF
				if closingIdx == -1 {
					closingIdx = len(lines) - 1
				}

				source := string(bytes.Join(sourceLines, []byte("\n")))

				placeholder := placeholderFunc(diagramIndex)

				// Record position now (len(result) is the index the placeholder will occupy).
				foundDiagrams = append(foundDiagrams, DiagramRef{
					Source:         source,
					Index:          diagramIndex,
					LineInRendered: len(result),
				})

				// Add the placeholder as a single line
				result = append(result, []byte(placeholder.Text))
				if placeholder.Err != "" {
					result = append(result, []byte("<!-- "+placeholder.Err+" -->"))
				}

				diagramIndex++

				// Skip to after the closing fence
				i = closingIdx + 1
				continue
			}
		}

		// Not a mermaid block; keep the line as-is
		result = append(result, line)
		i++
	}

	output = bytes.Join(result, []byte("\n"))
	diagrams = foundDiagrams
	return
}
