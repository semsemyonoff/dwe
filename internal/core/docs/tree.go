package docs

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// Node represents a file or directory in the documentation tree.
type Node struct {
	Name     string    // Base name (file or directory)
	Path     string    // Relative path from the root (with forward slashes)
	IsDir    bool      // True if this is a directory
	Children []*Node   // Nil for files; non-nil (possibly empty) for directories
	Title    string    // First H1 from the file, if any. Empty for directories.
	Headings []Heading // H2/H3 headings parsed from the file, in document order.
}

// BuildTree walks the given documentation root and builds a hierarchical
// tree. It filters out non-markdown files and sorts children stably
// (directories before files, alphabetical). The result is the canonical
// view — every section under the root is included. UI projections (e.g.
// hiding internals, re-rooting at reference/) are the caller's job; doing
// it here would silently drop content for non-TUI consumers like
// `devbox docs export --include-internals`.
func BuildTree(root DocRoot) (*Node, error) {
	rootNode := &Node{
		Name:     root.Name,
		Path:     "",
		IsDir:    true,
		Children: []*Node{},
	}

	if err := walkFS(root.FS, ".", "", rootNode, root); err != nil {
		return nil, fmt.Errorf("failed to build tree for %s: %w", root.Name, err)
	}

	sortChildren(rootNode)
	return rootNode, nil
}

func walkFS(fsys fs.FS, dir string, relPath string, parent *Node, root DocRoot) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		name := entry.Name()
		entryRelPath := filepath.Join(relPath, name)
		entryRelPathForward := filepath.ToSlash(entryRelPath)

		if entry.IsDir() {
			// Skip the i18n directory (translations are handled separately at content resolution time).
			if name == "i18n" {
				continue
			}

			// Create a directory node.
			dirNode := &Node{
				Name:     name,
				Path:     entryRelPathForward,
				IsDir:    true,
				Children: []*Node{},
			}
			parent.Children = append(parent.Children, dirNode)

			// Recurse into the directory.
			subPath := filepath.Join(dir, name)
			if err := walkFS(fsys, subPath, entryRelPath, dirNode, root); err != nil {
				return err
			}
		} else if strings.HasSuffix(name, ".md") {
			// Only include markdown files. Read the file once to extract the
			// first H1 (used as the tree label) and the H2/H3 headings (used
			// as expandable sub-rows). Read failures are non-fatal: the node
			// is still added with an empty title and the tree falls back to
			// the filename.
			fileNode := &Node{
				Name:  name,
				Path:  entryRelPathForward,
				IsDir: false,
			}
			if content, err := fs.ReadFile(fsys, filepath.Join(dir, name)); err == nil {
				fileNode.Title, fileNode.Headings = ParseDoc(content)
			}
			parent.Children = append(parent.Children, fileNode)
		}
	}

	return nil
}

// sortChildren sorts the children of a node:
// - Directories come before files.
// - Within each group, sort alphabetically by Name.
func sortChildren(node *Node) {
	sort.Slice(node.Children, func(i, j int) bool {
		ni, nj := node.Children[i], node.Children[j]

		// Directories before files.
		if ni.IsDir != nj.IsDir {
			return ni.IsDir
		}

		// Alphabetically.
		return ni.Name < nj.Name
	})

	// Recursively sort children's children.
	for _, child := range node.Children {
		if child.IsDir {
			sortChildren(child)
		}
	}
}
