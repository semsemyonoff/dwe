package docs

import (
	"testing"
	"testing/fstest"
)

func TestBuildTree(t *testing.T) {
	// Create a test FS with a known structure.
	testFS := fstest.MapFS{
		"index.md":            &fstest.MapFile{Data: []byte("# Index")},
		"config/services.md":  &fstest.MapFile{Data: []byte("# Services")},
		"config/workspace.md": &fstest.MapFile{Data: []byte("# DWE")},
		"lifecycle/deploy.md": &fstest.MapFile{Data: []byte("# Deploy")},
		"lifecycle/run.md":    &fstest.MapFile{Data: []byte("# Run")},
		"ignored.txt":         &fstest.MapFile{Data: []byte("ignored")}, // Should be filtered out
		"config/.hidden":      &fstest.MapFile{Data: []byte("hidden")},  // No .md, filtered
	}

	root := DocRoot{
		Name: "test",
		FS:   testFS,
	}

	tree, err := BuildTree(root, "en")
	if err != nil {
		t.Fatalf("BuildTree failed: %v", err)
	}

	if tree.Name != "test" {
		t.Errorf("tree.Name = %q, want %q", tree.Name, "test")
	}

	if !tree.IsDir {
		t.Error("root should be a directory")
	}

	// Verify children count: 3 items at root (index.md, config/, lifecycle/)
	if len(tree.Children) != 3 {
		t.Errorf("root has %d children, want 3; got: %v", len(tree.Children), childNames(tree.Children))
	}

	// Verify ordering: directories before files, alphabetical.
	// Expected: config/ (dir), lifecycle/ (dir), index.md (file)
	expectedOrder := []string{"config", "lifecycle", "index.md"}
	for i, expected := range expectedOrder {
		if i >= len(tree.Children) {
			t.Errorf("not enough children; expected %s at index %d", expected, i)
			continue
		}
		if tree.Children[i].Name != expected {
			t.Errorf("child[%d]: got name %q, want %q", i, tree.Children[i].Name, expected)
		}
	}

	// Verify config directory contents.
	configNode := findChild(tree, "config")
	if configNode == nil {
		t.Fatal("config directory not found")
	}
	if len(configNode.Children) != 2 {
		t.Errorf("config has %d children, want 2; got: %v", len(configNode.Children), childNames(configNode.Children))
	}
	// Alphabetical order: services.md, workspace.md
	if configNode.Children[0].Name != "services.md" {
		t.Errorf("config[0]: got %q, want %q", configNode.Children[0].Name, "services.md")
	}
	if configNode.Children[1].Name != "workspace.md" {
		t.Errorf("config[1]: got %q, want %q", configNode.Children[1].Name, "workspace.md")
	}

	// Verify lifecycle directory contents.
	lifecycleNode := findChild(tree, "lifecycle")
	if lifecycleNode == nil {
		t.Fatal("lifecycle directory not found")
	}
	if len(lifecycleNode.Children) != 2 {
		t.Errorf("lifecycle has %d children, want 2; got: %v", len(lifecycleNode.Children), childNames(lifecycleNode.Children))
	}
	// Alphabetical order: deploy.md, run.md
	if lifecycleNode.Children[0].Name != "deploy.md" {
		t.Errorf("lifecycle[0]: got %q, want %q", lifecycleNode.Children[0].Name, "deploy.md")
	}
	if lifecycleNode.Children[1].Name != "run.md" {
		t.Errorf("lifecycle[1]: got %q, want %q", lifecycleNode.Children[1].Name, "run.md")
	}

	// Verify paths use forward slashes.
	for _, child := range tree.Children {
		if child.Name == "config" {
			if child.Path != "config" {
				t.Errorf("config path: got %q, want %q", child.Path, "config")
			}
			for _, subchild := range child.Children {
				expected := "config/" + subchild.Name
				if subchild.Path != expected {
					t.Errorf("path: got %q, want %q", subchild.Path, expected)
				}
			}
		}
	}
}

func TestBuildTreeFiltersNonMarkdown(t *testing.T) {
	testFS := fstest.MapFS{
		"readme.md":       &fstest.MapFile{Data: []byte("# README")},
		"notes.txt":       &fstest.MapFile{Data: []byte("notes")},
		"config.yaml":     &fstest.MapFile{Data: []byte("config")},
		"folder/.gitkeep": &fstest.MapFile{Data: []byte("")},
	}

	root := DocRoot{Name: "test", FS: testFS}
	tree, err := BuildTree(root, "en")
	if err != nil {
		t.Fatalf("BuildTree failed: %v", err)
	}

	// Should only have readme.md and folder directory
	if len(tree.Children) != 2 {
		t.Errorf("expected 2 top-level children (readme.md and folder), got %d: %v", len(tree.Children), childNames(tree.Children))
	}

	// Verify folder is empty (no .gitkeep in children)
	folderNode := findChild(tree, "folder")
	if folderNode == nil {
		t.Fatal("folder directory not found")
	}
	if len(folderNode.Children) != 0 {
		t.Errorf("folder should be empty, but has %d children: %v", len(folderNode.Children), childNames(folderNode.Children))
	}
}

func TestBuildTreeEmptyRoot(t *testing.T) {
	testFS := fstest.MapFS{}
	root := DocRoot{Name: "test", FS: testFS}
	tree, err := BuildTree(root, "en")
	if err != nil {
		t.Fatalf("BuildTree failed: %v", err)
	}

	if len(tree.Children) != 0 {
		t.Errorf("empty FS should have 0 children, got %d", len(tree.Children))
	}
}

// Helper functions for testing.

func childNames(nodes []*Node) []string {
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	return names
}

func findChild(node *Node, name string) *Node {
	for _, child := range node.Children {
		if child.Name == name {
			return child
		}
	}
	return nil
}
