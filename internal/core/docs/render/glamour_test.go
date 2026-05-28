package render

import (
	"bytes"
	"testing"
)

func TestRender(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		width           int
		expectedDefault int
		checkOutput     func(t *testing.T, result Result)
	}{
		{
			name:            "width defaults to 100 when zero",
			input:           "# Hello\n\nWorld",
			width:           0,
			expectedDefault: 100,
		},
		{
			name:  "width defaults to 100 when negative",
			input: "# Hello",
			width: -5,
		},
		{
			name:  "simple markdown renders",
			input: "# Heading\n\nParagraph text",
			width: 80,
			checkOutput: func(t *testing.T, result Result) {
				if len(result.Output) == 0 {
					t.Errorf("expected non-empty output")
				}
				// Glamour adds ANSI codes, so just check it's longer than input
				if len(result.Output) < len("# Heading") {
					t.Errorf("expected glamour to expand output, got: %s", result.Output)
				}
			},
		},
		{
			name:  "code blocks preserved",
			input: "```go\nfunc main() {}\n```",
			width: 80,
			checkOutput: func(t *testing.T, result Result) {
				if !bytes.Contains(result.Output, []byte("main")) {
					t.Errorf("expected 'main' in output")
				}
			},
		},
	}

	placeholderFunc := PlaceholderForRendering

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := Opts{
				Theme: "dark",
				Width: tt.width,
			}

			result, err := Render([]byte(tt.input), opts, placeholderFunc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(result.Output) == 0 {
				t.Errorf("expected non-empty output")
			}

			if tt.checkOutput != nil {
				tt.checkOutput(t, result)
			}
		})
	}
}

func TestRenderWithMermaid(t *testing.T) {
	input := "# Diagram\n\n```mermaid\ngraph TD\n  A --> B\n```\n\nDone"

	tests := []struct {
		name        string
		placeholder MermaidPlaceholder
		check       func(t *testing.T, result Result)
	}{
		{
			name:        "disabled",
			placeholder: MermaidPlaceholder{Text: "<📊 [disabled]>"},
			check: func(t *testing.T, result Result) {
				if !bytes.Contains(result.Output, []byte("disabled")) {
					t.Errorf("expected 'disabled' in output")
				}
			},
		},
		{
			name:        "unavailable",
			placeholder: MermaidPlaceholder{Text: "<📊 [unavailable]>"},
			check: func(t *testing.T, result Result) {
				if !bytes.Contains(result.Output, []byte("unavailable")) {
					t.Errorf("expected 'unavailable' in output")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			placeholderFunc := func(i int) MermaidPlaceholder {
				return tt.placeholder
			}

			opts := Opts{
				Theme: "dark",
				Width: 80,
			}

			result, err := Render([]byte(input), opts, placeholderFunc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(result.Diagrams) != 1 {
				t.Errorf("expected 1 diagram, got %d", len(result.Diagrams))
			}

			if len(result.Diagrams) > 0 {
				if !bytes.Contains([]byte(result.Diagrams[0].Source), []byte("graph TD")) {
					t.Errorf("expected 'graph TD' in diagram source")
				}
			}

			if tt.check != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestRawMarkdown(t *testing.T) {
	input := []byte("# Heading\n\n```mermaid\ndiagram\n```")
	result := RawMarkdown(input)

	if !bytes.Equal(result.Output, input) {
		t.Errorf("expected raw output to match input")
	}

	if len(result.Diagrams) != 0 {
		t.Errorf("expected no diagrams in raw markdown, got %d", len(result.Diagrams))
	}
}

func TestThemeFromBackground(t *testing.T) {
	theme := ThemeFromBackground()
	if theme != "dark" && theme != "light" {
		t.Errorf("expected 'dark' or 'light', got %q", theme)
	}
}
