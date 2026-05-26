package render

// Opts specifies rendering options for markdown content.
type Opts struct {
	// Theme is the glamour style (e.g., "dark", "light", "auto").
	Theme string
	// Width is the terminal width in characters. Defaults to 100 if <= 0.
	Width int
}

// DiagramRef is a reference to a mermaid diagram in the rendered output.
type DiagramRef struct {
	// LineInRendered is the line number in the rendered output where the placeholder appears.
	LineInRendered int
	// Source is the original mermaid source code.
	Source string
	// Index is the diagram's order among all diagrams in this render (0-indexed).
	Index int
}

// Result is the output of a render operation.
type Result struct {
	// Output is the rendered markdown (as bytes, potentially with ANSI codes).
	Output []byte
	// Diagrams is a list of mermaid diagrams found and preprocessed.
	Diagrams []DiagramRef
}
