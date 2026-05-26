package render

import (
	"bytes"
	"regexp"
)

var fenceStartRE = regexp.MustCompile("^```(\\w*)")

// MermaidPlaceholder defines a mermaid block placeholder and the error it represents.
type MermaidPlaceholder struct {
	Text string
	Err  string // empty if no error
}

// PlaceholderFunc returns the placeholder text/error for a diagram based on its renderer state.
type PlaceholderFunc func(index int) MermaidPlaceholder

// PreprocessMermaid scans markdown and replaces mermaid fenced blocks with placeholders.
// Returns the modified markdown, a list of extracted diagrams, and any non-mermaid processing errors.
// Edge cases:
// - Nested fences are NOT handled specially; only the first ```mermaid opens a block.
// - A block without a closing ``` consumes lines to EOF and emits a placeholder + warning.
func PreprocessMermaid(input []byte, placeholderFunc PlaceholderFunc) (output []byte, diagrams []DiagramRef, err error) {
	lines := bytes.Split(input, []byte("\n"))
	var result [][]byte
	var foundDiagrams []DiagramRef

	diagramIndex := 0
	i := 0

	for i < len(lines) {
		line := lines[i]
		matches := fenceStartRE.FindSubmatch(line)

		// Check if this is a fence start
		if matches != nil {
			lang := string(matches[1])

			// If it's a mermaid block, process it
			if lang == "mermaid" {
				// Capture the mermaid source
				var sourceLines [][]byte
				startIdx := i + 1
				closingIdx := -1

				// Find the closing ``` fence
				for j := startIdx; j < len(lines); j++ {
					if fenceStartRE.Match(lines[j]) {
						// Found a closing fence (any language)
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

				// Get placeholder text
				placeholder := placeholderFunc(diagramIndex)

				// Add diagram reference (line number will be set later after flattening)
				foundDiagrams = append(foundDiagrams, DiagramRef{
					Source: source,
					Index:  diagramIndex,
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

	// Now populate LineInRendered in the diagrams
	for diagIdx := range foundDiagrams {
		// Find the line where this diagram's placeholder appears
		// Count through result to find the diagram's occurrence
		diagramsSeen := 0
		for lineIdx, resultLine := range result {
			placeholder := placeholderFunc(diagIdx)
			if diagramsSeen == diagIdx && bytes.Contains(resultLine, []byte(placeholder.Text)) {
				foundDiagrams[diagIdx].LineInRendered = lineIdx
				break
			}
			// Count placeholders we've seen
			for _, innerDiag := range foundDiagrams[:diagIdx] {
				innerPlaceholder := placeholderFunc(innerDiag.Index)
				if bytes.Contains(resultLine, []byte(innerPlaceholder.Text)) {
					diagramsSeen++
				}
			}
		}
	}

	output = bytes.Join(result, []byte("\n"))
	diagrams = foundDiagrams
	return
}
