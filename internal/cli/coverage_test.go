package cli

import (
	"os"
	"testing"

	"github.com/spf13/cobra"
)

// --- genCLIDocs ---

func TestGenCLIDocs_Markdown(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	if err := genCLIDocs(root, dir, "markdown"); err != nil {
		t.Fatalf("genCLIDocs markdown: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatal("expected markdown files to be written")
	}
}

func TestGenCLIDocs_Yaml(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	if err := genCLIDocs(root, dir, "yaml"); err != nil {
		t.Fatalf("genCLIDocs yaml: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatal("expected yaml files to be written")
	}
}

func TestGenCLIDocs_UnknownFormat(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	if err := genCLIDocs(root, dir, "xml"); err == nil {
		t.Fatal("expected error for unknown format")
	}
}

// --- genHiddenCLI* and walkAllCommands ---

func TestWalkAllCommands_VisitsAll(t *testing.T) {
	root := NewRootCmd()
	visited := 0
	err := walkAllCommands(root, func(cmd *cobra.Command) error {
		visited++
		return nil
	})
	if err != nil {
		t.Fatalf("walkAllCommands: %v", err)
	}
	if visited < 5 {
		t.Errorf("expected at least 5 commands visited, got %d", visited)
	}
}

func TestGenHiddenCLIMarkdown_NoHidden(t *testing.T) {
	dir := t.TempDir()
	// Build a simple command with no hidden subusercommands.
	root := NewRootCmd()
	if err := genHiddenCLIMarkdown(root, dir); err != nil {
		t.Fatalf("genHiddenCLIMarkdown: %v", err)
	}
}

func TestGenHiddenCLIYaml_NoHidden(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	if err := genHiddenCLIYaml(root, dir); err != nil {
		t.Fatalf("genHiddenCLIYaml: %v", err)
	}
}

func TestGenHiddenCLIMan_NoHidden(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	if err := genHiddenCLIMan(root, dir); err != nil {
		t.Fatalf("genHiddenCLIMan: %v", err)
	}
}

// --- walkAllCommands error propagation ---

func TestWalkAllCommands_PropagatesError(t *testing.T) {
	root := NewRootCmd()
	sentinel := os.ErrNotExist
	err := walkAllCommands(root, func(cmd *cobra.Command) error {
		return sentinel
	})
	if err != sentinel {
		t.Errorf("expected sentinel error, got: %v", err)
	}
}

// --- genCLIDocs man format ---

func TestGenCLIDocs_Man(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	if err := genCLIDocs(root, dir, "man"); err != nil {
		t.Fatalf("genCLIDocs man: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatal("expected man page files to be written")
	}
}

// --- genHiddenCLI* with actual hidden commands ---

func TestGenHiddenCLIMarkdown_WithHiddenCommands(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	// The 'print' subgroup contains hidden commands (print success/warning/etc.).
	// genHiddenCLIMarkdown should write .md for each hidden command.
	if err := genHiddenCLIMarkdown(root, dir); err != nil {
		t.Fatalf("genHiddenCLIMarkdown: %v", err)
	}
	// At least some files should be written if there are hidden usercommands.
	entries, _ := os.ReadDir(dir)
	t.Logf("written %d files for hidden markdown", len(entries))
}

func TestGenHiddenCLIYaml_WithHiddenCommands(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	if err := genHiddenCLIYaml(root, dir); err != nil {
		t.Fatalf("genHiddenCLIYaml: %v", err)
	}
}

func TestGenHiddenCLIMan_WithHiddenCommands(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	if err := genHiddenCLIMan(root, dir); err != nil {
		t.Fatalf("genHiddenCLIMan: %v", err)
	}
}
