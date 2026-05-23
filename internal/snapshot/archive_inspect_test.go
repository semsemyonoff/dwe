package snapshot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTarGz writes the given entries (typeflag + name + body) to path. Empty
// body is allowed for directory/symlink entries.
type tarEntry struct {
	name     string
	typeflag byte
	body     []byte
	linkname string
}

func writeTarGz(t *testing.T, path string, entries []tarEntry) {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     0o644,
			Size:     int64(len(e.body)),
			Typeflag: e.typeflag,
			Linkname: e.linkname,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if len(e.body) > 0 {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatalf("write body: %v", err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gz: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func TestReadManifestFromTar(t *testing.T) {
	dir := t.TempDir()

	manifest := []byte("name: feature-x\ncreated_at: 2026-05-24T11:02:00Z\nproject:\n  name: tbm\n  config_hash: abc\n")

	t.Run("happy path", func(t *testing.T) {
		p := filepath.Join(dir, "ok.tar.gz")
		writeTarGz(t, p, []tarEntry{
			{name: "feature-x/", typeflag: tar.TypeDir},
			{name: "feature-x/manifest.yml", typeflag: tar.TypeReg, body: manifest},
			{name: "feature-x/db/main.sql.gz", typeflag: tar.TypeReg, body: []byte("payload")},
		})
		m, err := ReadManifestFromTar(p)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if m.Name != "feature-x" {
			t.Errorf("name = %q", m.Name)
		}
		if m.Project.ConfigHash != "abc" {
			t.Errorf("config_hash = %q", m.Project.ConfigHash)
		}
	})

	t.Run("rejects symlink", func(t *testing.T) {
		p := filepath.Join(dir, "symlink.tar.gz")
		writeTarGz(t, p, []tarEntry{
			{name: "feature-x/evil", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
		})
		_, err := ReadManifestFromTar(p)
		if err == nil || !strings.Contains(err.Error(), "rejected") {
			t.Fatalf("expected rejection, got %v", err)
		}
	})

	t.Run("rejects hardlink", func(t *testing.T) {
		p := filepath.Join(dir, "hardlink.tar.gz")
		writeTarGz(t, p, []tarEntry{
			{name: "feature-x/link", typeflag: tar.TypeLink, linkname: "manifest.yml"},
		})
		_, err := ReadManifestFromTar(p)
		if err == nil || !strings.Contains(err.Error(), "rejected") {
			t.Fatalf("expected rejection, got %v", err)
		}
	})

	t.Run("no manifest entry", func(t *testing.T) {
		p := filepath.Join(dir, "no-manifest.tar.gz")
		writeTarGz(t, p, []tarEntry{
			{name: "feature-x/db.sql", typeflag: tar.TypeReg, body: []byte("x")},
		})
		_, err := ReadManifestFromTar(p)
		if err == nil || !strings.Contains(err.Error(), "no manifest.yml") {
			t.Fatalf("expected no-manifest error, got %v", err)
		}
	})

	t.Run("rejects oversize manifest", func(t *testing.T) {
		big := bytes.Repeat([]byte("a"), maxInspectManifestBytes+1)
		p := filepath.Join(dir, "huge.tar.gz")
		writeTarGz(t, p, []tarEntry{
			{name: "feature-x/manifest.yml", typeflag: tar.TypeReg, body: big},
		})
		_, err := ReadManifestFromTar(p)
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("expected size cap error, got %v", err)
		}
	})

	t.Run("non-gzip input", func(t *testing.T) {
		p := filepath.Join(dir, "plain.tar.gz")
		if err := os.WriteFile(p, []byte("not a gzip"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := ReadManifestFromTar(p); err == nil {
			t.Fatalf("expected gzip error")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		if _, err := ReadManifestFromTar(filepath.Join(dir, "nope.tar.gz")); err == nil {
			t.Fatalf("expected error")
		}
	})
}
