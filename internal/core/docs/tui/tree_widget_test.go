package tui

import (
	"io/fs"
	"strings"
	"testing"

	"devbox-cli/internal/core/docs"
)

func TestTreeWidget(t *testing.T) {
	// Create a simple in-memory filesystem for testing
	fsys := &testFS{
		files: map[string]string{
			"config.md": "config content",
			"readme.md": "readme content",
		},
	}

	roots := []docs.DocRoot{
		{
			Name: "devbox",
			FS:   fsys,
		},
	}

	tw, err := NewTreeWidget(roots, "en")
	if err != nil {
		t.Fatalf("NewTreeWidget failed: %v", err)
	}

	if tw.Cursor() == nil {
		t.Error("Cursor should not be nil after initialization")
	}

	if len(tw.VisibleNodes()) == 0 {
		t.Error("VisibleNodes should not be empty")
	}
}

func TestTreeWidgetNavigation(t *testing.T) {
	fsys := &testFS{
		files: map[string]string{
			"a.md": "a",
			"b.md": "b",
			"c.md": "c",
		},
	}

	roots := []docs.DocRoot{
		{
			Name: "devbox",
			FS:   fsys,
		},
	}

	tw, err := NewTreeWidget(roots, "en")
	if err != nil {
		t.Fatalf("NewTreeWidget failed: %v", err)
	}

	initial := tw.Cursor()

	tw.MoveDown()
	if tw.Cursor() == initial {
		t.Error("MoveDown should change cursor")
	}

	tw.MoveUp()
	if tw.Cursor() != initial {
		t.Error("MoveUp should return to initial cursor")
	}
}

type testFS struct {
	files map[string]string
}

func (f *testFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func (f *testFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name != "." && name != "" {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	// Return test files
	entries := make([]fs.DirEntry, 0)
	for fileName := range f.files {
		entries = append(entries, &testDirEntry{name: fileName})
	}
	return entries, nil
}

type testDirEntry struct {
	name string
}

func (e *testDirEntry) Name() string               { return e.name }
func (e *testDirEntry) IsDir() bool                { return false }
func (e *testDirEntry) Type() fs.FileMode          { return 0 }
func (e *testDirEntry) Info() (fs.FileInfo, error) { return nil, nil }

// TestFilterShowsParentOfMatchingHeading guards a bug where typing a
// query that only matched H2/H3 headings (not their parent file or
// directory) produced an empty visible list — the user saw a blank tree
// panel after the second character because the previous algorithm only
// added a node when one of its own ancestors had already been added in
// the same pass, but no ancestor matched the query.
func TestFilterShowsParentOfMatchingHeading(t *testing.T) {
	roots := []docs.DocRoot{{Name: "devbox", FS: filterFixtureFS{}}}
	tw, err := NewTreeWidget(roots, "en")
	if err != nil {
		t.Fatalf("NewTreeWidget: %v", err)
	}

	f := NewTreeFilter()
	f.Open()
	for _, r := range "uniq" { // matches only the H2 "Uniq Heading"
		f.Append(r)
	}
	tw.ApplyFilter(f)

	vis := tw.VisibleNodes()
	if len(vis) == 0 {
		t.Fatalf("filter on a heading-only match returned no visible nodes — parent file was dropped")
	}

	var sawFile, sawHeading bool
	for _, n := range vis {
		if n.Heading != nil && strings.Contains(strings.ToLower(n.Heading.Text), "uniq") {
			sawHeading = true
		}
		if n.Node != nil && !n.Node.IsDir && n.Heading == nil {
			sawFile = true
		}
	}
	if !sawFile {
		t.Errorf("expected the matching heading's parent file to appear in visible: %v", visibleLabels(vis))
	}
	if !sawHeading {
		t.Errorf("expected the matching heading itself to appear in visible: %v", visibleLabels(vis))
	}
}

func visibleLabels(vis []*TreeNode) []string {
	out := make([]string, len(vis))
	for i, n := range vis {
		out[i] = nodeLabel(n)
	}
	return out
}

type filterFixtureFS struct{}

func (filterFixtureFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func (filterFixtureFS) ReadDir(name string) ([]fs.DirEntry, error) {
	switch name {
	case ".", "":
		return []fs.DirEntry{filterDirEntry{name: "guide.md"}}, nil
	}
	return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
}

func (filterFixtureFS) ReadFile(name string) ([]byte, error) {
	if name == "guide.md" {
		return []byte("# Guide\n\nIntro.\n\n## Uniq Heading\n\nBody.\n"), nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func (filterFixtureFS) Stat(name string) (fs.FileInfo, error) {
	return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
}

type filterDirEntry struct {
	name string
}

func (e filterDirEntry) Name() string               { return e.name }
func (e filterDirEntry) IsDir() bool                { return false }
func (e filterDirEntry) Type() fs.FileMode          { return 0 }
func (e filterDirEntry) Info() (fs.FileInfo, error) { return nil, nil }
