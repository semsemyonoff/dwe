package docstui

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// indexFixtureFS is a nested in-memory docs root: a `config/` directory that
// holds an `index.md` plus a sibling `services.md`, and a top-level
// `guide.md`. It exercises the index.md folding logic in the tree widget.
type indexFixtureFS struct {
	dirs  map[string][]fs.DirEntry
	files map[string]string
}

func newIndexFixtureFS() indexFixtureFS {
	return indexFixtureFS{
		dirs: map[string][]fs.DirEntry{
			".":      {dirEnt{name: "config", dir: true}, dirEnt{name: "guide.md"}},
			"config": {dirEnt{name: "index.md"}, dirEnt{name: "services.md"}},
		},
		files: map[string]string{
			"guide.md":           "# Guide\n",
			"config/index.md":    "# Configuration\n\n## Overview\n\nBody.\n",
			"config/services.md": "# Services\n",
		},
	}
}

func (indexFixtureFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func (f indexFixtureFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if entries, ok := f.dirs[name]; ok {
		return entries, nil
	}
	return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
}

func (f indexFixtureFS) ReadFile(name string) ([]byte, error) {
	if content, ok := f.files[name]; ok {
		return []byte(content), nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

type dirEnt struct {
	name string
	dir  bool
}

func (e dirEnt) Name() string { return e.name }
func (e dirEnt) IsDir() bool  { return e.dir }
func (e dirEnt) Type() fs.FileMode {
	if e.dir {
		return fs.ModeDir
	}
	return 0
}
func (e dirEnt) Info() (fs.FileInfo, error) { return nil, nil }

// findDir returns the first directory TreeNode with the given Node.Name.
func findDir(node *TreeNode, name string) *TreeNode {
	if node.Node != nil && node.Node.IsDir && node.Node.Name == name && node.Heading == nil {
		return node
	}
	for _, c := range node.Children {
		if got := findDir(c, name); got != nil {
			return got
		}
	}
	return nil
}

func TestDirectoryFoldsIndexMd(t *testing.T) {
	roots := []docs.DocRoot{{Name: "dwe", FS: newIndexFixtureFS()}}
	tw, err := NewTreeWidget(roots, "en")
	if err != nil {
		t.Fatalf("NewTreeWidget: %v", err)
	}

	cfg := findDir(tw.root, "config")
	if cfg == nil {
		t.Fatal("config directory node not found")
	}

	// The directory borrows index.md's H1 as its label.
	if got := nodeLabel(cfg); got != "Configuration" {
		t.Errorf("nodeLabel(config) = %q, want %q", got, "Configuration")
	}

	// IndexNode is wired to config/index.md.
	if cfg.IndexNode == nil {
		t.Fatal("config.IndexNode is nil; index.md was not folded")
	}
	if cfg.IndexNode.Path != "config/index.md" {
		t.Errorf("IndexNode.Path = %q, want %q", cfg.IndexNode.Path, "config/index.md")
	}

	// Selecting the directory resolves to the index file's content.
	if cn := contentNodeFor(cfg); cn == nil || cn.Path != "config/index.md" {
		t.Errorf("contentNodeFor(config) = %v, want config/index.md", cn)
	}

	// index.md is NOT exposed as a separate child row; services.md still is.
	for _, c := range cfg.Children {
		if c.Node != nil && c.Node.Name == "index.md" {
			t.Error("index.md should be folded away, not shown as a child row")
		}
	}
	var sawServices bool
	for _, c := range cfg.Children {
		if c.Node != nil && c.Node.Name == "services.md" {
			sawServices = true
		}
	}
	if !sawServices {
		t.Error("expected services.md to remain a visible child of config")
	}
}

// TestSelectingIndexDirRendersIndexContent drives a Model end-to-end: the
// initial cursor lands on the folded `config` directory and the async topic
// load resolves and renders config/index.md into the viewport.
func TestSelectingIndexDirRendersIndexContent(t *testing.T) {
	roots := []docs.DocRoot{{Name: "dwe", FS: newIndexFixtureFS()}}
	m, err := NewModel(context.Background(), roots, "en", i18n.NopTranslator{}, &testRenderer{}, 120, 40, "", "DWE", "auto")
	if err != nil {
		t.Fatalf("NewModel: %v", err)
	}

	// Directories sort before files, so the first row is the folded config dir.
	if !m.Tree.IsDir(m.Tree.Cursor()) {
		t.Fatalf("expected initial cursor on the config directory, got %q", nodeLabel(m.Tree.Cursor()))
	}
	if m.initCmd == nil {
		t.Fatal("expected an initial async load cmd for the index directory")
	}

	msg := m.initCmd()
	loaded, ok := msg.(topicLoadedMsg)
	if !ok {
		t.Fatalf("expected topicLoadedMsg, got %T", msg)
	}
	if loaded.Err != nil {
		t.Fatalf("topic load returned an error: %v", loaded.Err)
	}
	m.Update(loaded)

	if m.currentlyLoadedPath != "config/index.md" {
		t.Errorf("currentlyLoadedPath = %q, want config/index.md", m.currentlyLoadedPath)
	}
	if got := stripANSI(m.Viewport.View()); !strings.Contains(got, "Configuration") {
		t.Errorf("viewport should render index.md's H1 'Configuration'; got:\n%s", got)
	}
}

// TestPlainDirectoryHasNoContent verifies a directory without an index.md
// still resolves to no content node (blank viewport), preserving the prior
// behavior.
func TestPlainDirectoryHasNoContent(t *testing.T) {
	fsys := indexFixtureFS{
		dirs: map[string][]fs.DirEntry{
			".":      {dirEnt{name: "guides", dir: true}},
			"guides": {dirEnt{name: "intro.md"}},
		},
		files: map[string]string{"guides/intro.md": "# Intro\n"},
	}
	roots := []docs.DocRoot{{Name: "dwe", FS: fsys}}
	tw, err := NewTreeWidget(roots, "en")
	if err != nil {
		t.Fatalf("NewTreeWidget: %v", err)
	}

	guides := findDir(tw.root, "guides")
	if guides == nil {
		t.Fatal("guides directory node not found")
	}
	if guides.IndexNode != nil {
		t.Error("plain directory should not have an IndexNode")
	}
	if nodeLabel(guides) != "guides" {
		t.Errorf("nodeLabel(guides) = %q, want %q", nodeLabel(guides), "guides")
	}
	if cn := contentNodeFor(guides); cn != nil {
		t.Errorf("contentNodeFor(plain dir) = %v, want nil", cn)
	}
}
