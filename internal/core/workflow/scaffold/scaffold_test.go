package scaffold

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "update golden fixtures")

func scaffoldOptions(target string) Options {
	o := newTestOptions()
	o.TargetDir = target
	return o
}

// snapshotTree serializes the directory tree at root into a deterministic, golden-
// friendly blob: a sorted list of entries, files rendered with their content and
// symlinks rendered with their target. Temp-file artifacts must never appear.
func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var entries []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if strings.Contains(filepath.Base(path), ".scaffold-") {
			t.Errorf("leftover temp file in tree: %s", rel)
		}
		switch {
		case d.Type()&os.ModeSymlink != 0:
			tgt, err := os.Readlink(path)
			if err != nil {
				return err
			}
			entries = append(entries, "SYMLINK "+rel+" -> "+filepath.ToSlash(tgt))
		case d.IsDir():
			entries = append(entries, "DIR "+rel)
		default:
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entries = append(entries, "=== "+rel+" ===\n"+string(data))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(entries)
	return strings.Join(entries, "\n--------\n")
}

func goldenCompare(t *testing.T, name, got string) {
	t.Helper()
	goldenPath := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", goldenPath, err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s (run with -update to create): %v", goldenPath, err)
	}
	if got != string(want) {
		t.Errorf("scaffold tree mismatch for %s.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestScaffold_GoldenTree(t *testing.T) {
	dir := t.TempDir()
	res, err := Scaffold(scaffoldOptions(dir))
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if res.Target != dir {
		// dir from t.TempDir() is already absolute; Target should match.
		abs, _ := filepath.Abs(dir)
		if res.Target != abs {
			t.Errorf("Target = %q, want %q", res.Target, abs)
		}
	}
	if len(res.Skipped) != 0 {
		t.Errorf("fresh scaffold reported skipped files: %v", res.Skipped)
	}
	goldenCompare(t, "golden_default.txt", snapshotTree(t, dir))
}

func TestScaffold_ReportsCreated(t *testing.T) {
	dir := t.TempDir()
	res, err := Scaffold(scaffoldOptions(dir))
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	for _, want := range []string{
		"workspace.yml",
		"compose.yaml",
		".gitignore",
		".editorconfig",
		"AGENTS.md",
		"CLAUDE.md",
		".dwe/config",
		"workspace/defaults.yml",
		"workspace/styles.yml",
		"workspace/deploy.yml",
		"workspace/lifecycle.yml",
		"workspace/info.yml",
		"workspace/docker.yml",
		"workspace/services/app/service.yml",
	} {
		if !contains(res.Created, want) {
			t.Errorf("Created missing %q; got %v", want, res.Created)
		}
	}
}

func TestScaffold_Idempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := Scaffold(scaffoldOptions(dir)); err != nil {
		t.Fatalf("first Scaffold: %v", err)
	}
	before := snapshotTree(t, dir)

	res, err := Scaffold(scaffoldOptions(dir))
	if err != nil {
		t.Fatalf("second Scaffold: %v", err)
	}
	if len(res.Created) != 0 {
		t.Errorf("re-run created files: %v", res.Created)
	}
	if after := snapshotTree(t, dir); after != before {
		t.Errorf("re-run mutated the tree.\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

func TestScaffold_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	if _, err := Scaffold(scaffoldOptions(dir)); err != nil {
		t.Fatalf("first Scaffold: %v", err)
	}
	wsPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(wsPath, []byte("garbage: true\n"), 0o644); err != nil {
		t.Fatalf("clobber workspace.yml: %v", err)
	}

	// Without force: file is left as-is (skipped).
	res, err := Scaffold(scaffoldOptions(dir))
	if err != nil {
		t.Fatalf("Scaffold (no force): %v", err)
	}
	if !contains(res.Skipped, "workspace.yml") {
		t.Errorf("expected workspace.yml skipped without force; Skipped=%v", res.Skipped)
	}
	if data, _ := os.ReadFile(wsPath); string(data) != "garbage: true\n" {
		t.Errorf("workspace.yml overwritten without force: %s", data)
	}

	// With force: file is restored from template.
	opts := scaffoldOptions(dir)
	opts.Force = true
	res, err = Scaffold(opts)
	if err != nil {
		t.Fatalf("Scaffold (force): %v", err)
	}
	if !contains(res.Created, "workspace.yml") {
		t.Errorf("expected workspace.yml created with force; Created=%v", res.Created)
	}
	if data, _ := os.ReadFile(wsPath); strings.Contains(string(data), "garbage") {
		t.Errorf("workspace.yml not overwritten by force: %s", data)
	}
}

func TestScaffold_NamedTargetCreatesSubdir(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "myproj")
	res, err := Scaffold(scaffoldOptions(target))
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if res.Target != target {
		t.Errorf("Target = %q, want %q", res.Target, target)
	}
	if _, err := os.Stat(filepath.Join(target, "workspace.yml")); err != nil {
		t.Errorf("workspace.yml not created under named target: %v", err)
	}
}

func TestScaffold_EmptyServiceOmitsFolder(t *testing.T) {
	dir := t.TempDir()
	opts := scaffoldOptions(dir)
	opts.Service = ""
	res, err := Scaffold(opts)
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "workspace", "services")); !os.IsNotExist(err) {
		t.Errorf("workspace/services should not exist with empty Service; stat err=%v", err)
	}
	for _, c := range res.Created {
		if strings.HasPrefix(c, "workspace/services/") {
			t.Errorf("Created contains a service path with empty Service: %q", c)
		}
	}
}

func TestScaffold_RenamedServiceFolder(t *testing.T) {
	dir := t.TempDir()
	opts := scaffoldOptions(dir)
	opts.Service = "web"
	if _, err := Scaffold(opts); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	svc := filepath.Join(dir, "workspace", "services", "web", "service.yml")
	data, err := os.ReadFile(svc)
	if err != nil {
		t.Fatalf("read renamed service.yml: %v", err)
	}
	if !strings.Contains(string(data), `container: "web"`) {
		t.Errorf("renamed service.yml missing container: web:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "workspace", "services", "app")); !os.IsNotExist(err) {
		t.Errorf("default app service folder should not exist when renamed; err=%v", err)
	}
}

func TestScaffold_NestedWarning(t *testing.T) {
	root := t.TempDir()
	// Outer project: a bare workspace.yml at root.
	if err := os.WriteFile(filepath.Join(root, "workspace.yml"), []byte("project:\n  name: outer\n"), 0o644); err != nil {
		t.Fatalf("seed outer workspace.yml: %v", err)
	}
	inner := filepath.Join(root, "nested")
	res, err := Scaffold(scaffoldOptions(inner))
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if !res.NestedWarning {
		t.Error("expected NestedWarning=true when an ancestor workspace.yml exists")
	}

	// A standalone project (no ancestor) must not warn.
	standalone := t.TempDir()
	res, err = Scaffold(scaffoldOptions(standalone))
	if err != nil {
		t.Fatalf("Scaffold standalone: %v", err)
	}
	if res.NestedWarning {
		t.Error("standalone project should not set NestedWarning")
	}
}

func TestScaffold_ClaudeSymlink(t *testing.T) {
	dir := t.TempDir()
	if _, err := Scaffold(scaffoldOptions(dir)); err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	info, err := os.Lstat(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("lstat CLAUDE.md: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("CLAUDE.md is not a symlink (mode %v)", info.Mode())
	}
	tgt, err := os.Readlink(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if tgt != "AGENTS.md" {
		t.Errorf("CLAUDE.md -> %q, want AGENTS.md", tgt)
	}
}

// TestScaffold_InvalidServiceRejected verifies that service names containing
// path separators, traversal components, or control characters are rejected
// before any disk write.
func TestScaffold_InvalidServiceRejected(t *testing.T) {
	bad := []string{
		"../etc", "../../root", "a/b", `a\b`, "..", ".",
		"api\nports:\n  http: 9999", "svc\x00null", "tab\there",
		// spaces: Docker container names cannot contain spaces
		"my app", "svc name",
		// C1 controls: yaml.v3 rejects them in quoted scalars
		"svc\u0080name",
		// YAML line-break runes break comment lines in service.yml.tmpl
		"svc\u0085name", "svc\u2028name", "svc\u2029name",
	}
	for _, svc := range bad {
		dir := t.TempDir()
		opts := scaffoldOptions(dir)
		opts.Service = svc
		_, err := Scaffold(opts)
		if err == nil {
			t.Errorf("service %q: expected error, got nil", svc)
		}
		// No files should have been written.
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Errorf("service %q: wrote files to disk despite invalid name: %v", svc, entries)
		}
	}
}

func contains(s []string, want string) bool {
	return slices.Contains(s, want)
}

func TestResolveTarget(t *testing.T) {
	dir := t.TempDir()
	abs, err := ResolveTarget(dir)
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if !filepath.IsAbs(abs) {
		t.Errorf("ResolveTarget(%q) = %q, want absolute path", dir, abs)
	}

	// Empty target resolves to the working directory.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ResolveTarget("")
	if err != nil {
		t.Fatalf("ResolveTarget(\"\"): %v", err)
	}
	if got != cwd {
		t.Errorf("ResolveTarget(\"\") = %q, want cwd %q", got, cwd)
	}
}

func TestHasProjectConfig(t *testing.T) {
	dir := t.TempDir()

	has, err := HasProjectConfig(dir)
	if err != nil {
		t.Fatalf("HasProjectConfig (absent): %v", err)
	}
	if has {
		t.Error("HasProjectConfig reported true for an empty directory")
	}

	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte("project: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	has, err = HasProjectConfig(dir)
	if err != nil {
		t.Fatalf("HasProjectConfig (present): %v", err)
	}
	if !has {
		t.Error("HasProjectConfig reported false despite workspace.yml present")
	}
}
