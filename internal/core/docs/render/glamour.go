package render

import (
	"fmt"
	"os"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
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
