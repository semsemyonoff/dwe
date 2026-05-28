package tui

import (
	"testing"

	"devbox-cli/internal/core/docs/render"
)

func TestNewDiagramState(t *testing.T) {
	diagrams := []render.DiagramRef{
		{Source: "graph TD; A --> B", Index: 0, LineInRendered: 5},
		{Source: "sequenceDiagram; A->>B: Hello", Index: 1, LineInRendered: 10},
	}

	ds := NewDiagramState(diagrams)
	if len(ds.Diagrams) != 2 {
		t.Errorf("expected 2 diagrams, got %d", len(ds.Diagrams))
	}
	if ds.Current != 0 {
		t.Errorf("expected current to be 0, got %d", ds.Current)
	}
}

func TestDiagramStateEmpty(t *testing.T) {
	ds := NewDiagramState(nil)
	if ds.Current != -1 {
		t.Errorf("expected current to be -1 for empty diagrams, got %d", ds.Current)
	}

	diagram := ds.CurrentDiagram()
	if diagram != nil {
		t.Errorf("expected CurrentDiagram to be nil for empty diagrams")
	}
}

func TestDiagramStateNavigation(t *testing.T) {
	diagrams := []render.DiagramRef{
		{Source: "1", Index: 0},
		{Source: "2", Index: 1},
		{Source: "3", Index: 2},
	}

	ds := NewDiagramState(diagrams)

	// Test Next
	ds.Next()
	if ds.Current != 1 {
		t.Errorf("expected current to be 1 after Next(), got %d", ds.Current)
	}

	ds.Next()
	if ds.Current != 2 {
		t.Errorf("expected current to be 2, got %d", ds.Current)
	}

	// Test wrapping
	ds.Next()
	if ds.Current != 0 {
		t.Errorf("expected current to wrap to 0, got %d", ds.Current)
	}

	// Test Prev
	ds.Prev()
	if ds.Current != 2 {
		t.Errorf("expected current to be 2 after Prev(), got %d", ds.Current)
	}

	ds.Prev()
	if ds.Current != 1 {
		t.Errorf("expected current to be 1, got %d", ds.Current)
	}

	// Test CurrentDiagram
	diagram := ds.CurrentDiagram()
	if diagram == nil {
		t.Fatalf("expected CurrentDiagram to not be nil")
	}
	if diagram.Source != "2" {
		t.Errorf("expected source '2', got '%s'", diagram.Source)
	}
}

func TestDiagramStateUpdate(t *testing.T) {
	ds := NewDiagramState(nil)

	// Update with new diagrams
	newDiagrams := []render.DiagramRef{
		{Source: "new1", Index: 0},
		{Source: "new2", Index: 1},
	}

	ds.Update(newDiagrams)
	if ds.Current != 0 {
		t.Errorf("expected current to reset to 0 after Update(), got %d", ds.Current)
	}
	if len(ds.Diagrams) != 2 {
		t.Errorf("expected 2 diagrams after Update(), got %d", len(ds.Diagrams))
	}

	// Update to empty
	ds.Update(nil)
	if ds.Current != -1 {
		t.Errorf("expected current to be -1 after Update(nil), got %d", ds.Current)
	}
}

func TestDiagramStateEmptyNavigation(t *testing.T) {
	ds := NewDiagramState(nil)

	// Navigation on empty should not crash
	ds.Next()
	if ds.Current != -1 {
		t.Errorf("expected current to stay -1 on Next() with no diagrams")
	}

	ds.Prev()
	if ds.Current != -1 {
		t.Errorf("expected current to stay -1 on Prev() with no diagrams")
	}
}
