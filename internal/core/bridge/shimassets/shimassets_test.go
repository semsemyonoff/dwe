package shimassets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func testFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, data := range files {
		m["bin/"+name] = &fstest.MapFile{Data: []byte(data)}
	}
	return m
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

func TestMaterializeCreatesShims(t *testing.T) {
	fsys := testFS(map[string]string{
		".gitkeep":         "",
		"shim-linux-amd64": "amd64-payload",
		"shim-linux-arm64": "arm64-payload",
	})
	baseDir := t.TempDir()

	paths, err := materializeFS(fsys, baseDir)
	if err != nil {
		t.Fatalf("materializeFS: %v", err)
	}

	bridgeDir := filepath.Join(baseDir, ".dwe", "bridge")
	want := []string{
		filepath.Join(bridgeDir, "shim-linux-amd64"),
		filepath.Join(bridgeDir, "shim-linux-arm64"),
	}
	if len(paths) != len(want) {
		t.Fatalf("got %d paths %v, want %d", len(paths), paths, len(want))
	}
	for i, p := range want {
		if paths[i] != p {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], p)
		}
	}

	for p, content := range map[string]string{
		want[0]: "amd64-payload",
		want[1]: "arm64-payload",
	} {
		if got := readFile(t, p); got != content {
			t.Errorf("%s content = %q, want %q", p, got, content)
		}
		if mode := fileMode(t, p); mode != 0o755 {
			t.Errorf("%s mode = %o, want 0755", p, mode)
		}
	}

	if _, err := os.Stat(filepath.Join(bridgeDir, ".gitkeep")); !os.IsNotExist(err) {
		t.Errorf(".gitkeep was materialized (stat err = %v), want skipped", err)
	}
	if mode := fileMode(t, bridgeDir); mode != 0o700 {
		t.Errorf("bridge dir mode = %o, want 0700", mode)
	}
}

func TestMaterializeIdempotentNoRewrite(t *testing.T) {
	fsys := testFS(map[string]string{"shim-linux-amd64": "payload"})
	baseDir := t.TempDir()

	paths, err := materializeFS(fsys, baseDir)
	if err != nil {
		t.Fatalf("first materializeFS: %v", err)
	}
	shim := paths[0]

	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(shim, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if _, err := materializeFS(fsys, baseDir); err != nil {
		t.Fatalf("second materializeFS: %v", err)
	}

	info, err := os.Stat(shim)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.ModTime().Equal(past) {
		t.Errorf("unchanged shim was rewritten: mtime %v, want %v", info.ModTime(), past)
	}
}

func TestMaterializeOverwritesOnContentChange(t *testing.T) {
	baseDir := t.TempDir()

	if _, err := materializeFS(testFS(map[string]string{"shim-linux-amd64": "old"}), baseDir); err != nil {
		t.Fatalf("first materializeFS: %v", err)
	}

	paths, err := materializeFS(testFS(map[string]string{"shim-linux-amd64": "new"}), baseDir)
	if err != nil {
		t.Fatalf("second materializeFS: %v", err)
	}
	if got := readFile(t, paths[0]); got != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
	if mode := fileMode(t, paths[0]); mode != 0o755 {
		t.Errorf("mode = %o, want 0755", mode)
	}
}

func TestMaterializeRepairsMode(t *testing.T) {
	fsys := testFS(map[string]string{"shim-linux-amd64": "payload"})
	baseDir := t.TempDir()

	paths, err := materializeFS(fsys, baseDir)
	if err != nil {
		t.Fatalf("first materializeFS: %v", err)
	}
	if err := os.Chmod(paths[0], 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if _, err := materializeFS(fsys, baseDir); err != nil {
		t.Fatalf("second materializeFS: %v", err)
	}
	if mode := fileMode(t, paths[0]); mode != 0o755 {
		t.Errorf("mode after repair = %o, want 0755", mode)
	}
}

func TestMaterializePlaceholderOnlyIsNoop(t *testing.T) {
	fsys := testFS(map[string]string{".gitkeep": ""})
	baseDir := t.TempDir()

	paths, err := materializeFS(fsys, baseDir)
	if err != nil {
		t.Fatalf("materializeFS: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("got paths %v, want none", paths)
	}
}

// TestMaterializeEmbedded exercises the real embed tree. It must pass both on
// a fresh checkout (placeholder only — zero shims) and after `make shims`
// (two linux shims), so it asserts shape, not count.
func TestMaterializeEmbedded(t *testing.T) {
	baseDir := t.TempDir()

	paths, err := Materialize(baseDir)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	for _, p := range paths {
		if !strings.HasPrefix(filepath.Base(p), "shim-") {
			t.Errorf("materialized non-shim entry %s", p)
		}
		if mode := fileMode(t, p); mode != 0o755 {
			t.Errorf("%s mode = %o, want 0755", p, mode)
		}
	}
}
