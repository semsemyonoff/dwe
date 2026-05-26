package render

import (
	"os"
	"regexp"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"
)

// ThemeFromBackground selects a glamour style based on the terminal background.
// Returns "dark" if the terminal has a dark background, "light" otherwise.
func ThemeFromBackground() string {
	if lipgloss.HasDarkBackground(os.Stdin, os.Stdout) {
		return "dark"
	}
	return "light"
}

// Render renders markdown to formatted text using glamour.
// Mermaid blocks are preprocessed and replaced with placeholders.
// The placeholderFunc determines the text/error for each diagram.
func Render(input []byte, opts RenderOpts, placeholderFunc PlaceholderFunc) (RenderResult, error) {
	if opts.Width <= 0 {
		opts.Width = 100
	}

	// Preprocess mermaid blocks
	preprocessed, diagrams, err := PreprocessMermaid(input, placeholderFunc)
	if err != nil {
		return RenderResult{}, err
	}

	// Render with glamour
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStylePath(opts.Theme),
		glamour.WithWordWrap(opts.Width),
	)
	if err != nil {
		// Fall back to a default style if theme selection fails
		renderer, _ = glamour.NewTermRenderer(
			glamour.WithWordWrap(opts.Width),
		)
	}

	output, err := renderer.Render(string(preprocessed))
	if err != nil {
		return RenderResult{}, err
	}

	return RenderResult{
		Output:   []byte(output),
		Diagrams: diagrams,
	}, nil
}

// RawMarkdown returns the input as-is without rendering.
// Useful for non-TTY output or when --raw flag is set.
func RawMarkdown(input []byte) RenderResult {
	return RenderResult{
		Output:   input,
		Diagrams: []DiagramRef{},
	}
}

// PlaceholderForDisabled returns a placeholder for disabled mermaid rendering.
func PlaceholderForDisabled(index int) MermaidPlaceholder {
	return MermaidPlaceholder{
		Text: "<📊 [diagrams disabled]>",
	}
}

// PlaceholderForUnavailable returns a placeholder when mmdc is not available.
func PlaceholderForUnavailable(index int) MermaidPlaceholder {
	return MermaidPlaceholder{
		Text: "<📊 [mmdc not installed — Y to copy]>",
	}
}

// PlaceholderForRendering returns a placeholder while rendering is in progress.
func PlaceholderForRendering(index int) MermaidPlaceholder {
	return MermaidPlaceholder{
		Text: "<📊 [rendering...]>",
	}
}

// PlaceholderForFailed returns a placeholder when rendering failed.
func PlaceholderForFailed(index int) MermaidPlaceholder {
	return MermaidPlaceholder{
		Text: "<📊 [render failed — Y to copy]>",
	}
}

// ExtractMermaidBlocks extracts all mermaid blocks from markdown without rendering.
// Returns a list of (source, startLine, endLine) tuples.
func ExtractMermaidBlocks(input []byte) []struct {
	Source    string
	StartLine int
	EndLine   int
} {
	lines := string(input)
	re := regexp.MustCompile("(?m)^```mermaid$([\\s\\S]*?)^```$")
	matches := re.FindAllStringSubmatchIndex(lines, -1)

	var blocks []struct {
		Source    string
		StartLine int
		EndLine   int
	}

	for _, match := range matches {
		if len(match) >= 4 {
			source := input[match[2]:match[3]]
			startLine := regexp.MustCompile("\n").FindAllIndex(input[:match[0]], -1)
			startLineNum := len(startLine)

			endLine := regexp.MustCompile("\n").FindAllIndex(input[:match[1]], -1)
			endLineNum := len(endLine)

			blocks = append(blocks, struct {
				Source    string
				StartLine int
				EndLine   int
			}{
				Source:    string(source),
				StartLine: startLineNum,
				EndLine:   endLineNum,
			})
		}
	}

	return blocks
}
