package snapshot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/workflow/snapshot/archive"
	"devbox-cli/internal/core/workflow/snapshot/meta"
)

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// snapshotUnpackProject builds a minimal devbox project root so
// loadSnapshotConfigOrNil and lock.AcquireProjectLocks succeed in tests.
func snapshotUnpackProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte("schema_version: \"2\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devboxDir, "defaults.yml"), []byte("project:\n  name: testproj\n  prefix: testproj\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// devbox/services/ empty directory (no services defined).
	if err := os.MkdirAll(filepath.Join(devboxDir, "services"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "snapshots"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// buildFixtureTarGz creates a snapshot under <baseA>/snapshots/<name>/ with a
// single artifact recorded in the manifest, then Packs it to outPath.
func buildFixtureTarGz(t *testing.T, baseA, name, outPath string) {
	t.Helper()
	snapsRoot := filepath.Join(baseA, "snapshots")
	snapDir := filepath.Join(snapsRoot, name)
	if err := os.MkdirAll(filepath.Join(snapDir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("hello world")
	if err := os.WriteFile(filepath.Join(snapDir, "data", "a.txt"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	m := &meta.Manifest{
		Name:      name,
		CreatedAt: time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC),
		Project:   meta.ProjectInfo{Name: "testproj"},
		Artifacts: []meta.ArtifactInfo{{Path: "data/a.txt", Size: int64(len(body)), Sha256: sha256Hex(body)}},
	}
	if err := meta.SaveManifest(filepath.Join(snapDir, meta.ManifestFileName), m); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Pack(snapsRoot, snapDir, name, outPath, nil); err != nil {
		t.Fatalf("Pack: %v", err)
	}
}

func TestSnapshotUnpack_VerifiedSummary(t *testing.T) {
	srcBase := snapshotUnpackProject(t)
	tarPath := filepath.Join(t.TempDir(), "fix.tar.gz")
	buildFixtureTarGz(t, srcBase, "fix", tarPath)

	dstBase := snapshotUnpackProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dstBase, "devbox.yml"), Root: dstBase}

	cmd := newSnapshotUnpackCmd(flags)
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{tarPath, "--as=imported", "-y"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "snapshot \"imported\" unpacked into") {
		t.Errorf("missing unpack confirmation line: %q", out)
	}
	if !strings.Contains(out, "(verified)") {
		t.Errorf("expected (verified) summary, got: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dstBase, "snapshots", "imported", "manifest.yml")); err != nil {
		t.Errorf("expected manifest in final dir: %v", err)
	}
}

func TestSnapshotUnpack_NoVerifyFlag(t *testing.T) {
	srcBase := snapshotUnpackProject(t)
	tarPath := filepath.Join(t.TempDir(), "fix.tar.gz")
	buildFixtureTarGz(t, srcBase, "fix", tarPath)

	dstBase := snapshotUnpackProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dstBase, "devbox.yml"), Root: dstBase}

	cmd := newSnapshotUnpackCmd(flags)
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{tarPath, "--as=imported", "-y", "--no-verify"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "(verification skipped)") {
		t.Errorf("expected (verification skipped) summary, got: %q", out)
	}
	if !strings.Contains(out, "skipping artifact verification") {
		t.Errorf("expected explicit no-verify warning on stderr, got: %q", out)
	}
}

func TestSnapshotUnpack_VerifiedWithWarnings(t *testing.T) {
	// Build a fixture archive whose manifest lists an extra artifact path that
	// is not present in the data dir → verification reports Missing.
	srcBase := snapshotUnpackProject(t)
	snapsRoot := filepath.Join(srcBase, "snapshots")
	name := "fix"
	snapDir := filepath.Join(snapsRoot, name)
	if err := os.MkdirAll(filepath.Join(snapDir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("hi")
	if err := os.WriteFile(filepath.Join(snapDir, "data", "a.txt"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	m := &meta.Manifest{
		Name:      name,
		CreatedAt: time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC),
		Project:   meta.ProjectInfo{Name: "testproj"},
		Artifacts: []meta.ArtifactInfo{
			{Path: "data/a.txt", Size: int64(len(body)), Sha256: sha256Hex(body)},
			{Path: "data/missing.txt", Size: 1, Sha256: "deadbeef"},
		},
	}
	if err := meta.SaveManifest(filepath.Join(snapDir, meta.ManifestFileName), m); err != nil {
		t.Fatal(err)
	}
	tarPath := filepath.Join(t.TempDir(), "fix.tar.gz")
	if _, err := archive.Pack(snapsRoot, snapDir, name, tarPath, nil); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	dstBase := snapshotUnpackProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dstBase, "devbox.yml"), Root: dstBase}

	cmd := newSnapshotUnpackCmd(flags)
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{tarPath, "--as=imported", "-y"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "(verified with 1 warnings)") {
		t.Errorf("expected (verified with 1 warnings) summary, got: %q", out)
	}
}
