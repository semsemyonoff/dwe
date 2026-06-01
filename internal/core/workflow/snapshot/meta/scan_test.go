package meta

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestScanArtifacts_basic(t *testing.T) {
	snap := t.TempDir()
	writeFile(t, filepath.Join(snap, "db/main.sql.gz"), []byte("dump-data"))
	writeFile(t, filepath.Join(snap, "search/idx.bin"), []byte("xx"))
	writeFile(t, filepath.Join(snap, "manifest.yml"), []byte("name: ignored"))
	writeFile(t, filepath.Join(snap, "workspace/local.yml"), []byte("ignored"))
	writeFile(t, filepath.Join(snap, "workspace/deploy-state.yml"), []byte("ignored"))

	out, err := ScanArtifacts(snap)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 artifacts, got %d: %+v", len(out), out)
	}
	// Sorted order: db/... then search/...
	if out[0].Path != "db/main.sql.gz" || out[1].Path != "search/idx.bin" {
		t.Errorf("ordering: %+v", out)
	}
	if out[0].Sha256 != sha256Hex([]byte("dump-data")) {
		t.Errorf("db sha256 mismatch: %s", out[0].Sha256)
	}
	if out[0].Size != int64(len("dump-data")) {
		t.Errorf("db size mismatch: %d", out[0].Size)
	}
}

func TestScanArtifacts_rejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	snap := t.TempDir()
	writeFile(t, filepath.Join(snap, "target.txt"), []byte("real"))
	if err := os.Symlink("target.txt", filepath.Join(snap, "link.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	_, err := ScanArtifacts(snap)
	if err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestScanArtifacts_emptyDir(t *testing.T) {
	out, err := ScanArtifacts(t.TempDir())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("want 0 artifacts, got %+v", out)
	}
}

func TestScanArtifacts_largeFileStreaming(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-file streaming test in -short mode")
	}
	snap := t.TempDir()
	path := filepath.Join(snap, "big.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 64 MiB of random data — well above any sensible inline buffer.
	const size = 64 * 1024 * 1024
	h := sha256.New()
	w := io.MultiWriter(f, h)
	if _, err := io.CopyN(w, rand.Reader, size); err != nil {
		_ = f.Close()
		t.Fatalf("write big: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	want := hex.EncodeToString(h.Sum(nil))

	out, err := ScanArtifacts(snap)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 artifact, got %d", len(out))
	}
	if out[0].Size != size {
		t.Fatalf("size: got %d want %d", out[0].Size, size)
	}
	if out[0].Sha256 != want {
		t.Fatalf("sha256: got %s want %s", out[0].Sha256, want)
	}
}
