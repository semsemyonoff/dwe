package snapshot

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"devbox-cli/internal/core/workflow/snapshot/meta"
)

func writeManifest(t *testing.T, dir, name string, m *meta.Manifest) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := meta.SaveManifest(filepath.Join(dir, name, meta.ManifestFileName), m); err != nil {
		t.Fatalf("save: %v", err)
	}
}

func TestListSnapshots(t *testing.T) {
	base := t.TempDir()
	snapsDir := filepath.Join(base, "snapshots")

	t.Run("empty when no dir", func(t *testing.T) {
		entries, err := ListSnapshots(base, nil)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("want 0 entries, got %d", len(entries))
		}
	})

	older := time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC)

	writeManifest(t, snapsDir, "alpha", &meta.Manifest{
		Name:      "alpha",
		CreatedAt: older,
		Artifacts: []meta.ArtifactInfo{{Path: "a", Size: 100}, {Path: "b", Size: 200}},
	})
	writeManifest(t, snapsDir, "beta", &meta.Manifest{
		Name:      "beta",
		CreatedAt: newer,
	})

	// Add a corrupt entry (directory exists, manifest unreadable).
	if err := os.MkdirAll(filepath.Join(snapsDir, "corrupt"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// And a hidden / staging directory that must be skipped.
	if err := os.MkdirAll(filepath.Join(snapsDir, ".unpack-xyz"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	entries, err := ListSnapshots(base, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}

	// Newer should come first, then older, then corrupt (nil manifest sorts last).
	wantOrder := []string{"beta", "alpha", "corrupt"}
	for i, want := range wantOrder {
		got := filepath.Base(entries[i].Dir)
		if got != want {
			t.Errorf("entries[%d]: got %q want %q", i, got, want)
		}
	}

	// Corrupt entry has nil manifest.
	if entries[2].Manifest != nil {
		t.Errorf("expected nil manifest for corrupt entry")
	}

	// Alpha's total size sums artifact sizes.
	if entries[1].TotalSize != 300 {
		t.Errorf("alpha total size: got %d want 300", entries[1].TotalSize)
	}
}
