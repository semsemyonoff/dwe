package docs

import (
	"os"
	"testing"

	"devbox-cli/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

// buildDocsTestRoot constructs a minimal cobra root that wires the docs
// subtree without dragging the cli/ package into the docs/ package's import
// graph (which would create a cycle).
func buildDocsTestRoot(flags *cmdctx.RootFlags) *cobra.Command {
	root := &cobra.Command{Use: "devbox"}
	root.AddGroup(&cobra.Group{ID: "advanced", Title: "Advanced"})
	root.AddCommand(NewCmd("advanced", flags))
	return root
}

// --- genCLIDocs ---

func TestGenCLIDocs_Markdown(t *testing.T) {
	dir := t.TempDir()
	root := buildDocsTestRoot(&cmdctx.RootFlags{})
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
	root := buildDocsTestRoot(&cmdctx.RootFlags{})
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
	root := buildDocsTestRoot(&cmdctx.RootFlags{})
	if err := genCLIDocs(root, dir, "xml"); err == nil {
		t.Fatal("expected error for unknown format")
	}
}

// --- genHiddenCLI* and walkAllCommands ---

func TestWalkAllCommands_VisitsAll(t *testing.T) {
	root := buildDocsTestRoot(&cmdctx.RootFlags{})
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
	root := buildDocsTestRoot(&cmdctx.RootFlags{})
	if err := genHiddenCLIMarkdown(root, dir); err != nil {
		t.Fatalf("genHiddenCLIMarkdown: %v", err)
	}
}

func TestGenHiddenCLIYaml_NoHidden(t *testing.T) {
	dir := t.TempDir()
	root := buildDocsTestRoot(&cmdctx.RootFlags{})
	if err := genHiddenCLIYaml(root, dir); err != nil {
		t.Fatalf("genHiddenCLIYaml: %v", err)
	}
}

func TestGenHiddenCLIMan_NoHidden(t *testing.T) {
	dir := t.TempDir()
	root := buildDocsTestRoot(&cmdctx.RootFlags{})
	if err := genHiddenCLIMan(root, dir); err != nil {
		t.Fatalf("genHiddenCLIMan: %v", err)
	}
}

// --- walkAllCommands error propagation ---

func TestWalkAllCommands_PropagatesError(t *testing.T) {
	root := buildDocsTestRoot(&cmdctx.RootFlags{})
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
	root := buildDocsTestRoot(&cmdctx.RootFlags{})
	if err := genCLIDocs(root, dir, "man"); err != nil {
		t.Fatalf("genCLIDocs man: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatal("expected man page files to be written")
	}
}
