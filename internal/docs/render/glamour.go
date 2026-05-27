package render

import (
	"fmt"
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
func Render(input []byte, opts Opts, placeholderFunc PlaceholderFunc) (Result, error) {
	if opts.Width <= 0 {
		opts.Width = 100
	}

	// Preprocess mermaid blocks
	preprocessed, diagrams, err := PreprocessMermaid(input, placeholderFunc)
	if err != nil {
		return Result{}, err
	}

	// Render with glamour
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStylePath(opts.Theme),
		glamour.WithWordWrap(opts.Width),
	)
	if err != nil {
		// Fall back to a default style if theme selection fails
		var fallbackErr error
		renderer, fallbackErr = glamour.NewTermRenderer(
			glamour.WithWordWrap(opts.Width),
		)
		if fallbackErr != nil {
			return Result{}, fmt.Errorf("glamour: create renderer: %w (fallback: %v)", err, fallbackErr)
		}
	}

	output, err := renderer.Render(string(preprocessed))
	if err != nil {
		return Result{}, err
	}

	return Result{
		Output:   []byte(output),
		Diagrams: diagrams,
	}, nil
}

// RawMarkdown returns the input as-is without rendering.
// Useful for non-TTY output or when --raw flag is set.
func RawMarkdown(input []byte) Result {
	return Result{
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

var (
	mermaidBlockRE = regexp.MustCompile("(?m)^```mermaid$([\\s\\S]*?)^```$")
	newlineRE      = regexp.MustCompile("\n")
)

// ExtractMermaidBlocks extracts all mermaid blocks from markdown without rendering.
// Returns a list of (source, startLine, endLine) tuples.
func ExtractMermaidBlocks(input []byte) []struct {
	Source    string
	StartLine int
	EndLine   int
} {
	lines := string(input)
	matches := mermaidBlockRE.FindAllStringSubmatchIndex(lines, -1)

	var blocks []struct {
		Source    string
		StartLine int
		EndLine   int
	}

	for _, match := range matches {
		if len(match) >= 4 {
			source := input[match[2]:match[3]]
			startLineNum := len(newlineRE.FindAllIndex(input[:match[0]], -1))
			endLineNum := len(newlineRE.FindAllIndex(input[:match[1]], -1))

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
