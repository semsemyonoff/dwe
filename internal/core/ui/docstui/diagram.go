package docstui

import (
	"github.com/semsemyonoff/dwe/internal/core/docs/render"
)

// DiagramState tracks diagrams for the current document.
type DiagramState struct {
	Diagrams []render.DiagramRef
	Current  int // Index into Diagrams; -1 if none
}

// NewDiagramState creates a new diagram state.
func NewDiagramState(diagrams []render.DiagramRef) *DiagramState {
	current := -1
	if len(diagrams) > 0 {
		current = 0
	}
	return &DiagramState{
		Diagrams: diagrams,
		Current:  current,
	}
}

// Next moves to the next diagram.
func (ds *DiagramState) Next() {
	if len(ds.Diagrams) == 0 {
		return
	}
	ds.Current = (ds.Current + 1) % len(ds.Diagrams)
}

// Prev moves to the previous diagram.
func (ds *DiagramState) Prev() {
	if len(ds.Diagrams) == 0 {
		return
	}
	ds.Current = (ds.Current - 1 + len(ds.Diagrams)) % len(ds.Diagrams)
}

// CurrentDiagram returns the current diagram, or nil if none.
func (ds *DiagramState) CurrentDiagram() *render.DiagramRef {
	if ds.Current < 0 || ds.Current >= len(ds.Diagrams) {
		return nil
	}
	return &ds.Diagrams[ds.Current]
}

// Update replaces diagrams with a new set.
func (ds *DiagramState) Update(diagrams []render.DiagramRef) {
	ds.Diagrams = diagrams
	if len(diagrams) == 0 {
		ds.Current = -1
	} else {
		ds.Current = 0
	}
}
