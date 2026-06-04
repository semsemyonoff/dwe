package render

import (
	"bytes"
	"testing"
)

func TestPreprocessMermaid(t *testing.T) {
	tests := []struct {
		name          string
		input         []byte
		expectedDiags int
		expectedLines int
		checkOutput   func(t *testing.T, output []byte, diagrams []DiagramRef)
	}{
		{
			name:          "no mermaid blocks",
			input:         []byte("# Heading\n\nSome text\n\nMore text"),
			expectedDiags: 0,
			expectedLines: 5,
		},
		{
			name:          "single mermaid block",
			input:         []byte("# Heading\n\n```mermaid\ngraph TD\n  A --> B\n```\n\nText after"),
			expectedDiags: 1,
			checkOutput: func(t *testing.T, output []byte, diagrams []DiagramRef) {
				if len(diagrams) != 1 {
					t.Fatalf("expected 1 diagram, got %d", len(diagrams))
				}
				if diagrams[0].Index != 0 {
					t.Errorf("expected index 0, got %d", diagrams[0].Index)
				}
				if !bytes.Contains([]byte(diagrams[0].Source), []byte("graph TD")) {
					t.Errorf("expected 'graph TD' in source, got: %s", diagrams[0].Source)
				}
				if !bytes.Contains(output, []byte("<📊")) {
					t.Errorf("expected placeholder in output")
				}
			},
		},
		{
			name:          "multiple mermaid blocks",
			input:         []byte("```mermaid\nA\n```\nText\n```mermaid\nB\n```"),
			expectedDiags: 2,
			checkOutput: func(t *testing.T, output []byte, diagrams []DiagramRef) {
				if len(diagrams) != 2 {
					t.Fatalf("expected 2 diagrams, got %d", len(diagrams))
				}
				if diagrams[0].Index != 0 || diagrams[1].Index != 1 {
					t.Errorf("expected indices 0 and 1")
				}
			},
		},
		{
			name:          "mermaid block without closing fence",
			input:         []byte("Text\n```mermaid\nUnclosed diagram"),
			expectedDiags: 1,
			checkOutput: func(t *testing.T, output []byte, diagrams []DiagramRef) {
				if len(diagrams) != 1 {
					t.Fatalf("expected 1 diagram, got %d", len(diagrams))
				}
				if !bytes.Contains([]byte(diagrams[0].Source), []byte("Unclosed diagram")) {
					t.Errorf("expected source to include 'Unclosed diagram'")
				}
			},
		},
		{
			name:          "non-mermaid code block preserved",
			input:         []byte("```python\nprint('hello')\n```\n\nText"),
			expectedDiags: 0,
			checkOutput: func(t *testing.T, output []byte, diagrams []DiagramRef) {
				if !bytes.Contains(output, []byte("python")) {
					t.Errorf("expected 'python' in output")
				}
				if !bytes.Contains(output, []byte("print")) {
					t.Errorf("expected 'print' in output")
				}
			},
		},
	}

	placeholderFunc := func(index int) MermaidPlaceholder {
		return MermaidPlaceholder{Text: "<📊 [placeholder]>"}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, diagrams, err := PreprocessMermaid(tt.input, placeholderFunc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(diagrams) != tt.expectedDiags {
				t.Errorf("expected %d diagrams, got %d", tt.expectedDiags, len(diagrams))
			}

			if tt.checkOutput != nil {
				tt.checkOutput(t, output, diagrams)
			}
		})
	}
}
