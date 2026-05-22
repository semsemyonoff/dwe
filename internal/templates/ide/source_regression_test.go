package ide_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSourceTemplates_noStaleAccessors walks every template-source location
// in the repo and asserts that the deleted accessors (.Tools, .Runtime.Ports,
// .Runtime.Hosts) and dot-paths (runtime.ports, runtime.hosts) no longer
// appear. Mirrors the Task 13 acceptance grep, scoped to template-source
// paths — catches stale source that happens not to be exercised by any
// rendering fixture.
//
// Excludes the active plan and completed plans, which intentionally
// enumerate the old names.
func TestSourceTemplates_noStaleAccessors(t *testing.T) {
	repoRoot := findRepoRoot(t)

	stalePatterns := []string{
		".Tools",
		".Runtime.Ports",
		".Runtime.Hosts",
		"runtime.ports",
		"runtime.hosts",
	}

	dirs := []string{
		filepath.Join(repoRoot, "internal", "templates"),
		filepath.Join(repoRoot, "next"),
	}
	exts := map[string]bool{
		".tmpl":    true,
		".gotmpl":  true,
		".yml":     true,
		".yaml":    true,
	}
	skipSuffixes := []string{
		"_test.go",
		"docs/plans/2026-05-22-unified-services-schema.md",
	}

	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if strings.Contains(path, "docs/plans/completed") {
					return filepath.SkipDir
				}
				return nil
			}
			if !exts[filepath.Ext(path)] {
				return nil
			}
			for _, s := range skipSuffixes {
				if strings.HasSuffix(path, s) {
					return nil
				}
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			content := string(data)
			for _, pat := range stalePatterns {
				if strings.Contains(content, pat) {
					t.Errorf("stale token %q found in %s (Task 8 migration guard)", pat, path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
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
