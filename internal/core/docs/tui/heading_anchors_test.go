package tui

import (
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/docs/render"
)

// TestHeadingMarkersMapToCorrectLine guards the section-navigation fix:
// substring matching on heading text picked up body prose that referenced
// the heading words ("Build Commands" mentioned in the intro of a file
// whose H2 is "Build Commands"), so the viewport "scrolled by one line"
// instead of jumping to the heading. Marker injection bypasses substring
// ambiguity — each heading owns a unique anchor and the rendered line is
// looked up directly. This test fails fast if a future glamour upgrade
// breaks marker survival (e.g. splits the token like the underscore form
// did during development).
func TestHeadingMarkersMapToCorrectLine(t *testing.T) {
	md := []byte("# Title\n\nIntro mentioning Build Commands and Project Structure.\n\n" +
		"## Build Commands\n\nBody.\n\n" +
		"## Project Structure\n\nBody.\n\n" +
		"### Sub\n\nBody.\n")

	marked := preprocessHeadings(md)
	if !strings.Contains(string(marked), headingMarker(0)) {
		t.Fatalf("marker 0 missing from preprocessed output: %s", marked)
	}
	if !strings.Contains(string(marked), headingMarker(2)) {
		t.Fatalf("marker 2 missing from preprocessed output: %s", marked)
	}

	result, err := render.Render(marked, render.Opts{Theme: "dark", Width: 100}, func(int) render.MermaidPlaceholder {
		return render.MermaidPlaceholder{Text: ""}
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	out, lineByIdx := stripHeadingMarkers(string(result.Output))

	if len(lineByIdx) != 3 {
		t.Fatalf("expected 3 heading lines, got %d (%v)", len(lineByIdx), lineByIdx)
	}
	for i, line := range lineByIdx {
		if line < 0 {
			t.Fatalf("heading %d: marker not found in rendered output", i)
		}
	}
	if lineByIdx[0] >= lineByIdx[1] || lineByIdx[1] >= lineByIdx[2] {
		t.Errorf("heading lines not strictly increasing: %v", lineByIdx)
	}

	// Markers must be invisible in the final output — users would otherwise
	// see "‡DBXHDR0‡Build Commands" on the heading line.
	if strings.Contains(out, "DBXHDR") {
		t.Errorf("marker leaked into stripped output: %q", out)
	}
	if strings.Contains(out, "‡") {
		t.Errorf("double-dagger leaked into stripped output: %q", out)
	}
}

func TestPreprocessHeadingsSkipsFencedCodeBlocks(t *testing.T) {
	md := []byte("## Real Heading\n\n```sh\n## not a heading inside fence\n```\n\n## Another\n")
	marked := preprocessHeadings(md)
	// Two markers expected (0 for "Real Heading", 1 for "Another"); the
	// `## not a heading` inside the fence must NOT receive a marker because
	// it's source code, not a markdown heading.
	if !strings.Contains(string(marked), headingMarker(0)) {
		t.Errorf("expected marker 0 in: %s", marked)
	}
	if !strings.Contains(string(marked), headingMarker(1)) {
		t.Errorf("expected marker 1 in: %s", marked)
	}
	if strings.Contains(string(marked), headingMarker(2)) {
		t.Errorf("did not expect a third marker (fenced ## should be skipped): %s", marked)
	}
}
