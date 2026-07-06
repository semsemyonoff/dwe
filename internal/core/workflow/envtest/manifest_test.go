package envtest

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifests", "redis-off-abc123.yml")

	want := &Manifest{
		Scenario:       "redis-off",
		RunID:          "abc123",
		ComposeProject: "myapp-t-redis-off-abc123",
		CopyPath:       filepath.Join(dir, "runs", "redis-off"),
		BridgeDir:      filepath.Join(dir, "runs", "redis-off", ".dwe", "bridge"),
		ReportDir:      filepath.Join(dir, "reports", "redis-off"),
		CreatedAt:      time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC),
	}

	if err := WriteManifest(path, want); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	got, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if *got != *want {
		t.Errorf("LoadManifest() = %+v, want %+v", *got, *want)
	}
}

func TestWriteManifestNoTempLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run-abc123.yml")

	if err := WriteManifest(path, &Manifest{Scenario: "smoke", RunID: "abc123"}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "run-abc123.yml" {
		t.Fatalf("dir contents = %v, want exactly [run-abc123.yml]", entries)
	}
}

func TestWriteManifestNil(t *testing.T) {
	dir := t.TempDir()
	if err := WriteManifest(filepath.Join(dir, "m.yml"), nil); err == nil {
		t.Fatal("WriteManifest(nil) = nil error, want error")
	}
}

func TestLoadManifestMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadManifest(filepath.Join(dir, "does-not-exist.yml"))
	if err == nil {
		t.Fatal("LoadManifest() = nil error, want error for missing file")
	}
	if !os.IsNotExist(err) {
		t.Errorf("LoadManifest() error = %v, want os.IsNotExist", err)
	}
}

func TestLoadManifestCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.yml")
	if err := os.WriteFile(path, []byte("scenario: [this is not a manifest"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := LoadManifest(path); err == nil {
		t.Fatal("LoadManifest() = nil error, want error for corrupt file")
	}
}

func TestDeleteManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.yml")
	if err := WriteManifest(path, &Manifest{Scenario: "smoke", RunID: "abc123"}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	if err := DeleteManifest(path); err != nil {
		t.Fatalf("DeleteManifest: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still exists after DeleteManifest, stat err = %v", err)
	}
	// Idempotent: deleting again is not an error.
	if err := DeleteManifest(path); err != nil {
		t.Errorf("DeleteManifest (second call): %v", err)
	}
}
