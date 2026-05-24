package snapshot

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// -- helpers ------------------------------------------------------------------

func validManifestBytes() []byte {
	return []byte("name: x\ncreated_at: 2026-05-24T00:00:00Z\nproject:\n  name: fixture\n  config_hash: \"\"\n")
}

func writeChecksumSidecar(t *testing.T, tarPath string) {
	t.Helper()
	f, err := os.Open(tarPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatalf("hash: %v", err)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	line := sum + "  " + filepath.Base(tarPath) + "\n"
	if err := os.WriteFile(tarPath+".sha256", []byte(line), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
}

// -- pack/unpack roundtrip ----------------------------------------------------

func TestPackUnpack_Roundtrip(t *testing.T) {
	tmp := t.TempDir()

	snapName := "rt"
	snapDir := filepath.Join(tmp, "snapshots", snapName)
	if err := os.MkdirAll(filepath.Join(snapDir, "data"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	m := &Manifest{Name: snapName, CreatedAt: time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)}
	if err := SaveManifest(filepath.Join(snapDir, ManifestFileName), m); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(snapDir, "data", "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(snapDir, "data", "b.tmp"), []byte("skip me"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := Pack(filepath.Join(tmp, "snapshots"), snapDir, snapName, "", []string{"**/*.tmp"})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if _, err := os.Stat(res.OutPath); err != nil {
		t.Fatalf("out missing: %v", err)
	}
	if _, err := os.Stat(res.OutPath + ".sha256"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected no sidecar next to %s, got stat err %v", res.OutPath, err)
	}
	if res.Sha256 == "" {
		t.Errorf("expected in-memory sha256 to be populated")
	}
	if len(res.SkippedEntries) == 0 {
		t.Errorf("expected at least one skipped entry (b.tmp)")
	}

	dst := t.TempDir()
	ur, err := Unpack(res.OutPath, filepath.Join(dst, "snapshots"), "rt", true, nil)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	_ = ur
	if _, err := os.Stat(filepath.Join(ur.SnapshotDir, "data", "a.txt")); err != nil {
		t.Errorf("a.txt missing after unpack: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ur.SnapshotDir, "data", "b.tmp")); err == nil {
		t.Errorf("b.tmp should have been excluded from archive")
	}
}

func TestUnpack_RejectsSidecarMismatch(t *testing.T) {
	tmp := t.TempDir()
	tarPath := filepath.Join(tmp, "snap.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: ManifestFileName, typeflag: tar.TypeReg, body: validManifestBytes()},
	})
	if err := os.WriteFile(tarPath+".sha256",
		[]byte("0000000000000000000000000000000000000000000000000000000000000000  snap.tar.gz\n"),
		0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	root := filepath.Join(tmp, "snapshots")
	_, err := Unpack(tarPath, root, "x", true, nil)
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected sha256 mismatch error, got %v", err)
	}
	if dents, _ := os.ReadDir(root); len(dents) != 0 {
		t.Errorf("expected no files under %s, got %d entries", root, len(dents))
	}
}

// -- malicious tar fixtures ---------------------------------------------------

func TestUnpack_RejectsEscapePath(t *testing.T) {
	tarPath := filepath.Join(t.TempDir(), "escape.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: "../etc/passwd", typeflag: tar.TypeReg, body: []byte("x")},
	})
	root := filepath.Join(t.TempDir(), "snapshots")
	_, err := Unpack(tarPath, root, "x", true, nil)
	assertRejected(t, err, "escape")
	assertNoFinalDirContents(t, root)
}

func TestUnpack_RejectsAbsolutePath(t *testing.T) {
	tarPath := filepath.Join(t.TempDir(), "absolute.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: "/etc/passwd", typeflag: tar.TypeReg, body: []byte("x")},
	})
	root := filepath.Join(t.TempDir(), "snapshots")
	_, err := Unpack(tarPath, root, "x", true, nil)
	assertRejected(t, err, "absolute")
	assertNoFinalDirContents(t, root)
}

func TestUnpack_RejectsSymlink(t *testing.T) {
	tarPath := filepath.Join(t.TempDir(), "symlink.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: "link", typeflag: tar.TypeSymlink, linkname: "/tmp/whatever"},
	})
	root := filepath.Join(t.TempDir(), "snapshots")
	_, err := Unpack(tarPath, root, "x", true, nil)
	assertRejected(t, err, "symlink")
}

func TestUnpack_RejectsHardlink(t *testing.T) {
	tarPath := filepath.Join(t.TempDir(), "hardlink.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: "link", typeflag: tar.TypeLink, linkname: "x"},
	})
	root := filepath.Join(t.TempDir(), "snapshots")
	_, err := Unpack(tarPath, root, "x", true, nil)
	assertRejected(t, err, "hardlink")
}

func TestUnpack_RejectsDevice(t *testing.T) {
	tarPath := filepath.Join(t.TempDir(), "device.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: "dev", typeflag: tar.TypeChar},
	})
	root := filepath.Join(t.TempDir(), "snapshots")
	_, err := Unpack(tarPath, root, "x", true, nil)
	assertRejected(t, err, "device")
}

func TestUnpack_RejectsFileCountOverflow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping count-overflow under -short")
	}
	tarPath := filepath.Join(t.TempDir(), "bomb.tar.gz")
	entries := make([]tarEntry, 0, maxUnpackFiles+1)
	for i := 0; i <= maxUnpackFiles; i++ {
		entries = append(entries, tarEntry{
			name: fmtName(i), typeflag: tar.TypeReg,
		})
	}
	writeTarGz(t, tarPath, entries)
	root := filepath.Join(t.TempDir(), "snapshots")
	_, err := Unpack(tarPath, root, "x", true, nil)
	if err == nil || !strings.Contains(err.Error(), "file count") {
		t.Fatalf("expected file-count error, got %v", err)
	}
}

func TestUnpack_AcceptsTrustedArchive(t *testing.T) {
	tarPath := filepath.Join(t.TempDir(), "good.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: "data/", typeflag: tar.TypeDir},
		{name: "data/x.txt", typeflag: tar.TypeReg, body: []byte("trusted")},
		{name: ManifestFileName, typeflag: tar.TypeReg, body: validManifestBytes()},
	})
	writeChecksumSidecar(t, tarPath)

	root := filepath.Join(t.TempDir(), "snapshots")
	ur, err := Unpack(tarPath, root, "good", true, nil)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if !ur.VerifiedChecksum {
		t.Errorf("expected VerifiedChecksum=true")
	}
	body, err := os.ReadFile(filepath.Join(ur.SnapshotDir, "data", "x.txt"))
	if err != nil || string(body) != "trusted" {
		t.Errorf("body=%q err=%v", string(body), err)
	}
}

func TestGlobToRegexp(t *testing.T) {
	cases := []struct {
		glob, path string
		want       bool
	}{
		{"**/*.tmp", "a/b/c.tmp", true},
		{"**/*.tmp", "c.tmp", true},
		{"**/*.tmp", "c.txt", false},
		{".cache/**", ".cache/x/y", true},
		{".cache/**", ".cache", true},
		{".cache/**", "data/x", false},
		{"foo.txt", "foo.txt", true},
		{"foo.txt", "bar/foo.txt", false},
		// **/name: must match the exact name segment, not files that merely end with the name.
		{"**/dump.sql", "dump.sql", true},
		{"**/dump.sql", "a/dump.sql", true},
		{"**/dump.sql", "a/b/dump.sql", true},
		{"**/dump.sql", "xdump.sql", false},
		{"**/dump.sql", "a/xdump.sql", false},
		// middle-position /**/: separator before suffix is required; "ab" must not match "a/**/b".
		{"data/**/secret.key", "data/secret.key", true},
		{"data/**/secret.key", "data/sub/secret.key", true},
		{"data/**/secret.key", "data/sub/dir/secret.key", true},
		{"data/**/secret.key", "datasecret.key", false},
		{"data/**/secret.key", "data/subsecret.key", false},
	}
	for _, tc := range cases {
		re, err := globToRegexp(tc.glob)
		if err != nil {
			t.Errorf("compile %q: %v", tc.glob, err)
			continue
		}
		got := re.MatchString(tc.path)
		if got != tc.want {
			t.Errorf("glob %q vs %q = %v want %v (regex=%q)", tc.glob, tc.path, got, tc.want, re.String())
		}
	}
}

// -- assertions --------------------------------------------------------------

func assertRejected(t *testing.T, err error, marker string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected rejection, got nil")
	}
	if rej, ok := errors.AsType[*rejectedTarEntryError](err); ok {
		if !strings.Contains(strings.ToLower(rej.Reason), marker) {
			t.Fatalf("expected reason mentioning %q, got: %s", marker, rej.Reason)
		}
		return
	}
	if !strings.Contains(strings.ToLower(err.Error()), marker) {
		t.Fatalf("expected error mentioning %q, got: %v", marker, err)
	}
}

func assertNoFinalDirContents(t *testing.T, root string) {
	t.Helper()
	dents, _ := os.ReadDir(root)
	for _, d := range dents {
		if strings.HasPrefix(d.Name(), ".unpack-") {
			t.Errorf("unexpected staging dir survived: %s", d.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(root, "x")); err == nil {
		t.Errorf("final target dir should not exist on rejection")
	}
}

func fmtName(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var b [12]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = digits[i%10]
		i /= 10
	}
	return string(b[n:])
}
