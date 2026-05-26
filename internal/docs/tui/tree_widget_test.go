package tui

import (
	"io/fs"
	"testing"

	"devbox-cli/internal/docs"
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

	tw, err := NewTreeWidget(roots)
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

	tw, err := NewTreeWidget(roots)
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
