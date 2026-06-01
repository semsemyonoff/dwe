package meta

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNewManifest(t *testing.T) {
	fixed := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	m := NewManifest("snap", func() time.Time { return fixed })
	if m.Name != "snap" {
		t.Fatalf("name: %q", m.Name)
	}
	if !m.CreatedAt.Equal(fixed) {
		t.Fatalf("createdAt: got %v want %v", m.CreatedAt, fixed)
	}
}

func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yml")
	fixed := time.Date(2026, 5, 24, 11, 2, 0, 0, time.UTC)
	m := NewManifest("feature-x", func() time.Time { return fixed })
	m.Description = "WIP"
	m.Project = ProjectInfo{Name: "tbm-next", ConfigHash: "abc123"}
	m.DweVersion = "0.42.0"
	m.Variant = "db-only"
	m.Artifacts = []ArtifactInfo{
		{Path: "db/main.sql.gz", Size: 1234, Sha256: "deadbeef"},
	}
	m.WorkspaceFiles = WorkspaceFiles{
		LocalYML:    "workspace/local.yml",
		DeployState: "workspace/deploy-state.yml",
	}
	m.LastCreate = &LastCreate{At: fixed, Status: StatusOk}

	if err := SaveManifest(path, m); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Name != m.Name || got.Description != m.Description {
		t.Errorf("name/description mismatch: %+v", got)
	}
	if !reflect.DeepEqual(got.Project, m.Project) {
		t.Errorf("project mismatch: got %+v want %+v", got.Project, m.Project)
	}
	if got.Variant != "db-only" || got.DweVersion != "0.42.0" {
		t.Errorf("variant/version mismatch: %+v", got)
	}
	if len(got.Artifacts) != 1 || got.Artifacts[0].Sha256 != "deadbeef" || got.Artifacts[0].Size != 1234 {
		t.Errorf("artifacts mismatch: %+v", got.Artifacts)
	}
	if got.LastCreate == nil || got.LastCreate.Status != StatusOk || !got.LastCreate.At.Equal(fixed) {
		t.Errorf("last_create mismatch: %+v", got.LastCreate)
	}
}

func TestSaveManifest_atomicNoLeftoverOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yml")
	if err := SaveManifest(path, NewManifest("snap", func() time.Time { return time.Unix(0, 0).UTC() })); err != nil {
		t.Fatalf("save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.Name() == "manifest.yml" {
			continue
		}
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file: %q", e.Name())
		}
	}
}

func TestSaveManifest_nil(t *testing.T) {
	dir := t.TempDir()
	if err := SaveManifest(filepath.Join(dir, "manifest.yml"), nil); err == nil {
		t.Fatal("expected error for nil manifest")
	}
}

func TestSaveManifest_createsParent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested/sub/manifest.yml")
	if err := SaveManifest(path, NewManifest("snap", func() time.Time { return time.Unix(0, 0).UTC() })); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat: %v", err)
	}
}

func TestManifestServiceSnapshotRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		svcs []ServiceSnapshot
	}{
		{name: "zero", svcs: nil},
		{
			name: "mixed-enabled",
			svcs: []ServiceSnapshot{
				{Name: "cdn", Enabled: false},
				{Name: "db", Enabled: true},
				{Name: "main", Enabled: true},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "manifest.yml")
			m := NewManifest("snap", func() time.Time { return time.Unix(0, 0).UTC() })
			m.Project = ProjectInfo{Name: "p", Services: tc.svcs}
			if err := SaveManifest(path, m); err != nil {
				t.Fatalf("save: %v", err)
			}
			got, err := LoadManifest(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if !reflect.DeepEqual(got.Project.Services, tc.svcs) {
				t.Fatalf("services: got %+v want %+v", got.Project.Services, tc.svcs)
			}
		})
	}
}

func TestLoadManifest_missingFile(t *testing.T) {
	_, err := LoadManifest(filepath.Join(t.TempDir(), "missing.yml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
