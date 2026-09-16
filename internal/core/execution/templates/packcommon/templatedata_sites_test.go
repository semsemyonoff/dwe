package packcommon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// templateDataSiteRoots are the trees that construct TemplateData: the three
// render commands, the three dry-run validators and the git renderer.
var templateDataSiteRoots = []string{
	"internal/cli/render",
	"internal/core/validate/templates",
	"internal/core/execution/templates",
}

// TestTemplateDataSitesSetCommandIndex walks every non-test TemplateData
// composite literal and requires it to set Commands and CommandGroups. A site
// that forgets them compiles fine and renders a pack that silently claims the
// project declares no commands.
//
// Blind spot: only composite literals are seen. A future site that declares
// `var d TemplateData` and assigns fields afterwards is not caught.
func TestTemplateDataSitesSetCommandIndex(t *testing.T) {
	root := moduleRoot(t)
	var sites []string
	for _, rel := range templateDataSiteRoots {
		err := filepath.WalkDir(filepath.Join(root, rel), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok || !isTemplateDataType(lit.Type) {
					return true
				}
				pos := fset.Position(lit.Pos())
				relPath, _ := filepath.Rel(root, pos.Filename)
				site := filepath.ToSlash(relPath)
				sites = append(sites, site)
				keys := literalKeys(lit)
				for _, want := range []string{"Commands", "CommandGroups"} {
					if !slices.Contains(keys, want) {
						t.Errorf("%s:%d: TemplateData literal does not set %s", site, pos.Line, want)
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", rel, err)
		}
	}

	// Guards against a vacuous pass (renamed type, moved tree): the six known
	// construction sites must all be found.
	want := []string{
		"internal/cli/render/ai.go",
		"internal/cli/render/ide.go",
		"internal/core/execution/templates/git/git.go",
		"internal/core/validate/templates/ai.go",
		"internal/core/validate/templates/git.go",
		"internal/core/validate/templates/ide.go",
	}
	for _, w := range want {
		if !slices.Contains(sites, w) {
			t.Errorf("no TemplateData literal found in %s; found sites: %v", w, sites)
		}
	}
}

func isTemplateDataType(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name == "TemplateData"
	case *ast.SelectorExpr:
		return e.Sel.Name == "TemplateData"
	}
	return false
}

func literalKeys(lit *ast.CompositeLit) []string {
	var keys []string
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id, ok := kv.Key.(*ast.Ident); ok {
			keys = append(keys, id.Name)
		}
	}
	return keys
}

// moduleRoot walks up from the package directory to the directory holding
// go.mod, so the test survives a package move.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from the package directory")
		}
		dir = parent
	}
}
