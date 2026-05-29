package snapshot

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"devbox-cli/internal/core/workflow/snapshot/meta"
)

// -- helpers ------------------------------------------------------------------

func validManifestBytes() []byte {
	return []byte("name: x\ncreated_at: 2026-05-24T00:00:00Z\nproject:\n  name: fixture\n  config_hash: \"\"\n")
}

// silentOpts builds UnpackOptions with a discarding Stderr writer and the two
// confirmation callbacks defaulting to "yes" so tests focused on path-safety /
// signature behavior don't have to wire prompts.
func silentOpts() UnpackOptions {
	return UnpackOptions{
		AssumeYes:        true,
		Stderr:           io.Discard,
		ConfirmOverwrite: func() (bool, error) { return true, nil },
		ConfirmVerify:    func(string) (bool, error) { return true, nil },
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
	m := &meta.Manifest{Name: snapName, CreatedAt: time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)}
	if err := meta.SaveManifest(filepath.Join(snapDir, meta.ManifestFileName), m); err != nil {
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
	ur, err := Unpack(res.OutPath, filepath.Join(dst, "snapshots"), "rt", silentOpts())
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ur.SnapshotDir, "data", "a.txt")); err != nil {
		t.Errorf("a.txt missing after unpack: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ur.SnapshotDir, "data", "b.tmp")); err == nil {
		t.Errorf("b.tmp should have been excluded from archive")
	}
	// Manifest in this fixture has no artifacts list, so data/a.txt shows up
	// as Extra (info-only, no prompt). Verification still ran.
	if ur.Verification != VerificationWarned {
		t.Errorf("Verification = %v, want VerificationWarned (extras only)", ur.Verification)
	}
}

// -- malicious tar fixtures ---------------------------------------------------

func TestUnpack_RejectsEscapePath(t *testing.T) {
	tarPath := filepath.Join(t.TempDir(), "escape.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: "../etc/passwd", typeflag: tar.TypeReg, body: []byte("x")},
	})
	root := filepath.Join(t.TempDir(), "snapshots")
	_, err := Unpack(tarPath, root, "x", silentOpts())
	assertRejected(t, err, "escape")
	assertNoFinalDirContents(t, root)
}

func TestUnpack_RejectsAbsolutePath(t *testing.T) {
	tarPath := filepath.Join(t.TempDir(), "absolute.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: "/etc/passwd", typeflag: tar.TypeReg, body: []byte("x")},
	})
	root := filepath.Join(t.TempDir(), "snapshots")
	_, err := Unpack(tarPath, root, "x", silentOpts())
	assertRejected(t, err, "absolute")
	assertNoFinalDirContents(t, root)
}

func TestUnpack_RejectsSymlink(t *testing.T) {
	tarPath := filepath.Join(t.TempDir(), "symlink.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: "link", typeflag: tar.TypeSymlink, linkname: "/tmp/whatever"},
	})
	root := filepath.Join(t.TempDir(), "snapshots")
	_, err := Unpack(tarPath, root, "x", silentOpts())
	assertRejected(t, err, "symlink")
}

func TestUnpack_RejectsHardlink(t *testing.T) {
	tarPath := filepath.Join(t.TempDir(), "hardlink.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: "link", typeflag: tar.TypeLink, linkname: "x"},
	})
	root := filepath.Join(t.TempDir(), "snapshots")
	_, err := Unpack(tarPath, root, "x", silentOpts())
	assertRejected(t, err, "hardlink")
}

func TestUnpack_RejectsDevice(t *testing.T) {
	tarPath := filepath.Join(t.TempDir(), "device.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: "dev", typeflag: tar.TypeChar},
	})
	root := filepath.Join(t.TempDir(), "snapshots")
	_, err := Unpack(tarPath, root, "x", silentOpts())
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
	_, err := Unpack(tarPath, root, "x", silentOpts())
	if err == nil || !strings.Contains(err.Error(), "file count") {
		t.Fatalf("expected file-count error, got %v", err)
	}
}

func TestUnpack_AcceptsTrustedArchive(t *testing.T) {
	tarPath := filepath.Join(t.TempDir(), "good.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: "data/", typeflag: tar.TypeDir},
		{name: "data/x.txt", typeflag: tar.TypeReg, body: []byte("trusted")},
		{name: meta.ManifestFileName, typeflag: tar.TypeReg, body: validManifestBytes()},
	})

	root := filepath.Join(t.TempDir(), "snapshots")
	ur, err := Unpack(tarPath, root, "good", silentOpts())
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	// Manifest has no artifacts list, but data/x.txt exists on disk — should
	// show up as Extra and trigger VerificationWarned (no prompt since no
	// Missing/HashMismatch).
	if ur.Verification != VerificationWarned {
		t.Errorf("Verification = %v, want VerificationWarned (extra-only)", ur.Verification)
	}
	if len(ur.VerifyReport.Extra) == 0 {
		t.Errorf("expected Extra to include data/x.txt, got %+v", ur.VerifyReport)
	}
	body, err := os.ReadFile(filepath.Join(ur.SnapshotDir, "data", "x.txt"))
	if err != nil || string(body) != "trusted" {
		t.Errorf("body=%q err=%v", string(body), err)
	}
}

// -- artifact verification ----------------------------------------------------

// manifestWithArtifactsBytes builds a manifest yaml document declaring the
// given artifacts (path, sha256, size taken from len(body)).
func manifestWithArtifactsBytes(t *testing.T, artifacts []meta.ArtifactInfo) []byte {
	t.Helper()
	m := &meta.Manifest{
		Name:      "v",
		CreatedAt: time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC),
		Artifacts: artifacts,
	}
	var buf bytes.Buffer
	if err := writeManifestTo(&buf, m); err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return buf.Bytes()
}

// writeManifestTo round-trips through SaveManifest's marshaller via a tempdir.
func writeManifestTo(w io.Writer, m *meta.Manifest) error {
	dir, err := os.MkdirTemp("", "manifest-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "manifest.yml")
	if err := meta.SaveManifest(path, m); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func computeSha256Hex(body []byte) string {
	h := sha256.New()
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func TestUnpack_VerificationHappyPath(t *testing.T) {
	body := []byte("payload")
	mBytes := manifestWithArtifactsBytes(t, []meta.ArtifactInfo{
		{Path: "data/a.txt", Size: int64(len(body)), Sha256: computeSha256Hex(body)},
	})
	tarPath := filepath.Join(t.TempDir(), "ok.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: meta.ManifestFileName, typeflag: tar.TypeReg, body: mBytes},
		{name: "data/a.txt", typeflag: tar.TypeReg, body: body},
	})
	root := filepath.Join(t.TempDir(), "snapshots")
	var stderr bytes.Buffer
	opts := silentOpts()
	opts.Stderr = &stderr
	ur, err := Unpack(tarPath, root, "v", opts)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if ur.Verification != VerificationClean {
		t.Errorf("Verification = %v, want VerificationClean", ur.Verification)
	}
	if !ur.VerifyReport.Empty() {
		t.Errorf("expected empty report, got %+v", ur.VerifyReport)
	}
	if stderr.Len() != 0 {
		t.Errorf("expected no stderr output, got %q", stderr.String())
	}
}

func TestUnpack_VerificationMissingDeclined(t *testing.T) {
	body := []byte("payload")
	mBytes := manifestWithArtifactsBytes(t, []meta.ArtifactInfo{
		{Path: "data/a.txt", Size: int64(len(body)), Sha256: computeSha256Hex(body)},
		{Path: "data/missing.txt", Size: 3, Sha256: computeSha256Hex([]byte("xyz"))},
	})
	tarPath := filepath.Join(t.TempDir(), "missing.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: meta.ManifestFileName, typeflag: tar.TypeReg, body: mBytes},
		{name: "data/a.txt", typeflag: tar.TypeReg, body: body},
	})
	root := filepath.Join(t.TempDir(), "snapshots")
	var stderr bytes.Buffer
	opts := UnpackOptions{
		AssumeYes:        false,
		Stderr:           &stderr,
		ConfirmOverwrite: func() (bool, error) { return true, nil },
		ConfirmVerify:    func(string) (bool, error) { return false, nil },
	}
	_, err := Unpack(tarPath, root, "v", opts)
	if err == nil {
		t.Fatalf("expected verify-declined error")
	}
	var declined *UnpackVerifyDeclinedError
	if !errors.As(err, &declined) {
		t.Fatalf("expected *UnpackVerifyDeclinedError, got %T: %v", err, err)
	}
	if len(declined.Report.Missing) != 1 || declined.Report.Missing[0] != "data/missing.txt" {
		t.Errorf("Missing = %+v", declined.Report.Missing)
	}
	if !strings.Contains(stderr.String(), `warning: artifact "data/missing.txt"`) {
		t.Errorf("stderr missing expected warning, got %q", stderr.String())
	}
	// Final dir must not exist (staging cleaned up).
	if _, statErr := os.Stat(filepath.Join(root, "v")); statErr == nil {
		t.Errorf("final dir should not exist after declined verification")
	}
}

func TestUnpack_VerificationMissingAccepted(t *testing.T) {
	body := []byte("payload")
	mBytes := manifestWithArtifactsBytes(t, []meta.ArtifactInfo{
		{Path: "data/a.txt", Size: int64(len(body)), Sha256: computeSha256Hex(body)},
		{Path: "data/missing.txt", Size: 3, Sha256: computeSha256Hex([]byte("xyz"))},
	})
	tarPath := filepath.Join(t.TempDir(), "missing-ok.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: meta.ManifestFileName, typeflag: tar.TypeReg, body: mBytes},
		{name: "data/a.txt", typeflag: tar.TypeReg, body: body},
	})
	root := filepath.Join(t.TempDir(), "snapshots")
	var stderr bytes.Buffer
	opts := UnpackOptions{
		AssumeYes:        false,
		Stderr:           &stderr,
		ConfirmOverwrite: func() (bool, error) { return true, nil },
		ConfirmVerify:    func(string) (bool, error) { return true, nil },
	}
	ur, err := Unpack(tarPath, root, "v", opts)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if ur.Verification != VerificationWarned {
		t.Errorf("Verification = %v, want VerificationWarned", ur.Verification)
	}
	if _, statErr := os.Stat(filepath.Join(root, "v")); statErr != nil {
		t.Errorf("final dir should exist after accepted verification: %v", statErr)
	}
}

func TestUnpack_VerificationHashMismatch(t *testing.T) {
	body := []byte("payload")
	mBytes := manifestWithArtifactsBytes(t, []meta.ArtifactInfo{
		{Path: "data/a.txt", Size: int64(len(body)), Sha256: computeSha256Hex([]byte("different"))},
	})
	tarPath := filepath.Join(t.TempDir(), "hash.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: meta.ManifestFileName, typeflag: tar.TypeReg, body: mBytes},
		{name: "data/a.txt", typeflag: tar.TypeReg, body: body},
	})
	root := filepath.Join(t.TempDir(), "snapshots")
	var stderr bytes.Buffer
	opts := UnpackOptions{
		AssumeYes:        false,
		Stderr:           &stderr,
		ConfirmOverwrite: func() (bool, error) { return true, nil },
		ConfirmVerify:    func(string) (bool, error) { return false, nil },
	}
	_, err := Unpack(tarPath, root, "v", opts)
	var declined *UnpackVerifyDeclinedError
	if !errors.As(err, &declined) {
		t.Fatalf("expected *UnpackVerifyDeclinedError, got %v", err)
	}
	if len(declined.Report.HashMismatch) != 1 {
		t.Errorf("HashMismatch len = %d", len(declined.Report.HashMismatch))
	}
	if !strings.Contains(stderr.String(), "sha256 mismatch") {
		t.Errorf("stderr missing sha256 mismatch warning, got %q", stderr.String())
	}
}

func TestUnpack_VerificationExtraOnly(t *testing.T) {
	body := []byte("payload")
	mBytes := manifestWithArtifactsBytes(t, []meta.ArtifactInfo{
		{Path: "data/a.txt", Size: int64(len(body)), Sha256: computeSha256Hex(body)},
	})
	tarPath := filepath.Join(t.TempDir(), "extra.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: meta.ManifestFileName, typeflag: tar.TypeReg, body: mBytes},
		{name: "data/a.txt", typeflag: tar.TypeReg, body: body},
		{name: "data/stowaway.txt", typeflag: tar.TypeReg, body: []byte("uninvited")},
	})
	root := filepath.Join(t.TempDir(), "snapshots")
	var stderr bytes.Buffer
	// AssumeYes=false but ConfirmVerify will not be called for extras-only.
	opts := UnpackOptions{
		AssumeYes:        false,
		Stderr:           &stderr,
		ConfirmOverwrite: func() (bool, error) { return true, nil },
		ConfirmVerify: func(string) (bool, error) {
			t.Fatalf("ConfirmVerify must not be called for extras-only")
			return false, nil
		},
	}
	ur, err := Unpack(tarPath, root, "v", opts)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if ur.Verification != VerificationWarned {
		t.Errorf("Verification = %v, want VerificationWarned", ur.Verification)
	}
	if len(ur.VerifyReport.Extra) != 1 {
		t.Errorf("Extra = %+v", ur.VerifyReport.Extra)
	}
	if !strings.Contains(stderr.String(), "info: archive contains") {
		t.Errorf("stderr missing extras info, got %q", stderr.String())
	}
}

func TestUnpack_NoVerifyBypass(t *testing.T) {
	body := []byte("payload")
	mBytes := manifestWithArtifactsBytes(t, []meta.ArtifactInfo{
		{Path: "data/a.txt", Size: int64(len(body)), Sha256: computeSha256Hex([]byte("different"))},
	})
	tarPath := filepath.Join(t.TempDir(), "noverify.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: meta.ManifestFileName, typeflag: tar.TypeReg, body: mBytes},
		{name: "data/a.txt", typeflag: tar.TypeReg, body: body},
	})
	root := filepath.Join(t.TempDir(), "snapshots")
	var stderr bytes.Buffer
	opts := UnpackOptions{
		NoVerify:         true,
		AssumeYes:        true,
		Stderr:           &stderr,
		ConfirmOverwrite: func() (bool, error) { return true, nil },
		ConfirmVerify:    func(string) (bool, error) { return false, nil },
	}
	ur, err := Unpack(tarPath, root, "v", opts)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if ur.Verification != VerificationSkipped {
		t.Errorf("Verification = %v, want VerificationSkipped", ur.Verification)
	}
	if !strings.Contains(stderr.String(), "skipping artifact verification") {
		t.Errorf("expected skip-verification warning, got %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "sha256 mismatch") {
		t.Errorf("expected no artifact-mismatch warnings with NoVerify, got %q", stderr.String())
	}
}

func TestUnpack_AssumeYesAcceptsAll(t *testing.T) {
	bodyA := []byte("payload")
	mBytes := manifestWithArtifactsBytes(t, []meta.ArtifactInfo{
		{Path: "data/a.txt", Size: int64(len(bodyA)), Sha256: computeSha256Hex([]byte("nope"))},
		{Path: "data/missing.txt", Size: 1, Sha256: computeSha256Hex([]byte("x"))},
	})
	tarPath := filepath.Join(t.TempDir(), "yes.tar.gz")
	writeTarGz(t, tarPath, []tarEntry{
		{name: meta.ManifestFileName, typeflag: tar.TypeReg, body: mBytes},
		{name: "data/a.txt", typeflag: tar.TypeReg, body: bodyA},
		{name: "data/stowaway.txt", typeflag: tar.TypeReg, body: []byte("uninvited")},
	})
	root := filepath.Join(t.TempDir(), "snapshots")
	var stderr bytes.Buffer
	opts := UnpackOptions{
		AssumeYes:        true,
		Stderr:           &stderr,
		ConfirmOverwrite: func() (bool, error) { return true, nil },
		ConfirmVerify: func(string) (bool, error) {
			t.Fatalf("ConfirmVerify must not be called with AssumeYes")
			return false, nil
		},
	}
	ur, err := Unpack(tarPath, root, "v", opts)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if ur.Verification != VerificationWarned {
		t.Errorf("Verification = %v, want VerificationWarned", ur.Verification)
	}
	out := stderr.String()
	if !strings.Contains(out, "missing from archive") || !strings.Contains(out, "sha256 mismatch") || !strings.Contains(out, "info: archive contains") {
		t.Errorf("expected all three warning lines, got %q", out)
	}
}

// -- VerifyExtractedArtifacts path-safety -----------------------------------

func TestVerifyExtractedArtifacts_RejectsEscapePath(t *testing.T) {
	staging := t.TempDir()
	m := &meta.Manifest{
		Artifacts: []meta.ArtifactInfo{
			{Path: "../escape", Size: 1, Sha256: "x"},
		},
	}
	_, err := VerifyExtractedArtifacts(staging, m)
	if err == nil || !strings.Contains(err.Error(), "escapes staging") {
		t.Fatalf("expected escape rejection, got %v", err)
	}
}

func TestVerifyExtractedArtifacts_RejectsAbsolutePath(t *testing.T) {
	staging := t.TempDir()
	m := &meta.Manifest{
		Artifacts: []meta.ArtifactInfo{
			{Path: "/etc/passwd", Size: 1, Sha256: "x"},
		},
	}
	_, err := VerifyExtractedArtifacts(staging, m)
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected absolute-path rejection, got %v", err)
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
		// **<literal> without separator: matches any path ending with the suffix.
		{"**.sql", "dump.sql", true},
		{"**.sql", "backup/dump.sql", true},
		{"**.sql", ".sql", true},
		{"**.sql", "dump.txt", false},
		{"**.gz", "archive.tar.gz", true},
		{"**.gz", "a/b/archive.tar.gz", true},
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
