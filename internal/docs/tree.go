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
	Name     string  // Base name (file or directory)
	Path     string  // Relative path from the root (with forward slashes)
	IsDir    bool    // True if this is a directory
	Children []*Node // Nil for files; non-nil (possibly empty) for directories
}

// BuildTree walks the given documentation root and builds a hierarchical tree.
// It filters out non-markdown files and sorts children stably (directories before files, alphabetical).
func BuildTree(root DocRoot) (*Node, error) {
	rootNode := &Node{
		Name:     root.Name,
		Path:     "",
		IsDir:    true,
		Children: []*Node{},
	}

	if err := walkFS(root.FS, ".", "", rootNode); err != nil {
		return nil, fmt.Errorf("failed to build tree for %s: %w", root.Name, err)
	}

	// Sort the root's children.
	sortChildren(rootNode)

	return rootNode, nil
}

func walkFS(fsys fs.FS, dir string, relPath string, parent *Node) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		name := entry.Name()
		entryRelPath := filepath.Join(relPath, name)
		entryRelPathForward := filepath.ToSlash(entryRelPath)

		if entry.IsDir() {
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
			if err := walkFS(fsys, subPath, entryRelPath, dirNode); err != nil {
				return err
			}
		} else if strings.HasSuffix(name, ".md") {
			// Only include markdown files.
			fileNode := &Node{
				Name:  name,
				Path:  entryRelPathForward,
				IsDir: false,
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
