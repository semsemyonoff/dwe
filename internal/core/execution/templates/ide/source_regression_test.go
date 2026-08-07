package ide_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Accessors removed by the services-as-map migration. They are gone from the
// type system — TemplateData has no Tools field (only the ToolServices method)
// and RuntimeConfig carries only UseHTTPS/SPX — so a template naming one fails
// at execution. This guard exists to catch such a template in *shipped source*
// before a user renders it.
var (
	// staleTemplateExprs must only ever be matched INSIDE a {{ … }} action.
	// Matching them on raw text produced false positives on ordinary Go code
	// (`data.Tools` in cli/status/json.go) and on prose that documents the
	// removal (docs/reference/render/ide.md says the namespace does not exist).
	staleTemplateExprs = []string{".Tools", ".Runtime.Ports", ".Runtime.Hosts"}

	// staleDotPaths are YAML `from:` values, not template syntax, so they are
	// matched on raw text and only in YAML.
	staleDotPaths = []string{"runtime.ports", "runtime.hosts"}
)

// scannedTemplateDirs are the trees holding template SOURCE. Both are checked
// in and rendered by tests, so this guard is a second line of defence — its
// value is naming the offending file instead of failing inside a render.
var scannedTemplateDirs = [][]string{
	// Starter project shipped by `dwe init`; .tmpl/.yml rendered into the
	// user's tree, so a stale accessor here reaches users directly.
	{"internal", "core", "workflow", "scaffold", "templates"},
	// Render packs (ide/ai/git/config). Templates live in Go string literals
	// here, which is why the walk cannot filter by file extension.
	{"internal", "core", "execution", "templates"},
}

// TestSourceTemplates_noStaleAccessors scans the template-source trees for
// accessors the services-as-map migration removed, catching stale source that
// no rendering fixture happens to exercise.
//
// A missing directory is a hard failure, not a skip: the previous version
// pointed at internal/templates and next/, and when neither survived a package
// move it silently passed for every subsequent commit. If this fails after a
// refactor, repoint scannedTemplateDirs — do not restore the skip.
func TestSourceTemplates_noStaleAccessors(t *testing.T) {
	repoRoot := findRepoRoot(t)

	for _, parts := range scannedTemplateDirs {
		dir := filepath.Join(repoRoot, filepath.Join(parts...))
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("scanned template dir is gone: %s (%v) — repoint scannedTemplateDirs", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("scanned template path is not a directory: %s", dir)
		}

		err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			content := string(data)
			rel, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				rel = path
			}

			for _, action := range templateActions(content) {
				for _, pat := range staleTemplateExprs {
					if strings.Contains(action, pat) {
						t.Errorf("removed accessor %q in template action %q (%s)", pat, action, rel)
					}
				}
			}

			if ext := filepath.Ext(path); ext == ".yml" || ext == ".yaml" {
				for _, pat := range staleDotPaths {
					if strings.Contains(content, pat) {
						t.Errorf("removed dot-path %q in %s", pat, rel)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}

// TestSourceTemplates_guardCanFire pins the scanner itself. Without it the
// guard's only observable behaviour is "passes", which is exactly what the
// inert version did for months.
func TestSourceTemplates_guardCanFire(t *testing.T) {
	t.Run("matches inside an action", func(t *testing.T) {
		actions := templateActions(`hosts: {{ .Runtime.Hosts.main }}`)
		if len(actions) != 1 || !strings.Contains(actions[0], ".Runtime.Hosts") {
			t.Fatalf("expected the stale accessor to be extracted, got %q", actions)
		}
	})

	t.Run("ignores the same text outside an action", func(t *testing.T) {
		// The shape that made the naive raw-text scan unusable.
		if got := templateActions(`data.Tools = build()`); len(got) != 0 {
			t.Fatalf("expected no actions in plain Go source, got %q", got)
		}
	})

	t.Run("handles trim markers and several actions per line", func(t *testing.T) {
		got := templateActions(`{{- .A -}}{{ .B }}`)
		if len(got) != 2 {
			t.Fatalf("expected 2 actions, got %d (%q)", len(got), got)
		}
	})
}

// templateActions returns the body of every {{ … }} action in src. An unclosed
// action is ignored — this is a lint over source, not a template parser.
func templateActions(src string) []string {
	var out []string
	for rest := src; ; {
		open := strings.Index(rest, "{{")
		if open < 0 {
			return out
		}
		rest = rest[open+2:]
		closeIdx := strings.Index(rest, "}}")
		if closeIdx < 0 {
			return out
		}
		out = append(out, strings.TrimSpace(rest[:closeIdx]))
		rest = rest[closeIdx+2:]
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for cur := cwd; cur != "/" && cur != "."; cur = filepath.Dir(cur) {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}
	}
	t.Fatalf("could not find repo root from %s", cwd)
	return ""
}
