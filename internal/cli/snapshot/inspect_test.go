package snapshot

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	snapshotpkg "github.com/semsemyonoff/dwe/internal/core/workflow/snapshot"
	"github.com/semsemyonoff/dwe/internal/core/workflow/snapshot/meta"

	"github.com/spf13/cobra"
)

// snapshotInspectProject builds a devbox project with the given service map
// declared via devbox/{services,defaults}.yml so config.LoadConfig produces a
// non-empty cfg.Services and runSnapshotInspect can diff against it.
func snapshotInspectProject(t *testing.T, services map[string]bool) string {
	t.Helper()
	dir := t.TempDir()
	devboxDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte("schema_version: \"2\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var defaults strings.Builder
	defaults.WriteString("project:\n  name: testproj\n  prefix: testproj\n")
	if len(services) > 0 {
		defaults.WriteString("services:\n")
		for name, enabled := range services {
			defaults.WriteString("  ")
			defaults.WriteString(name)
			defaults.WriteString(":\n    enabled: ")
			if enabled {
				defaults.WriteString("true\n")
			} else {
				defaults.WriteString("false\n")
			}
			// Write per-folder service file.
			svcDir := filepath.Join(devboxDir, "services", name)
			if err := os.MkdirAll(svcDir, 0o755); err != nil {
				t.Fatal(err)
			}
			content := "type: app\ncontainer: app-" + name + "\n"
			if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(devboxDir, "defaults.yml"), []byte(defaults.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "snapshots"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeSnapshotWithServices(t *testing.T, base, name string, captured []meta.ServiceSnapshot) {
	t.Helper()
	dir := filepath.Join(base, "snapshots", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := &meta.Manifest{
		Name:      name,
		CreatedAt: time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC),
		Project: meta.ProjectInfo{
			Name:     "testproj",
			Services: captured,
		},
	}
	if err := meta.SaveManifest(filepath.Join(dir, meta.ManifestFileName), m); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotInspect_ServicesDiff(t *testing.T) {
	captured := []meta.ServiceSnapshot{
		{Name: "db", Enabled: true},
		{Name: "main", Enabled: true},
	}

	tests := []struct {
		name     string
		services map[string]bool
		captured []meta.ServiceSnapshot
		wantText []string
		dontWant []string
	}{
		{
			name:     "in sync",
			services: map[string]bool{"db": true, "main": true},
			captured: captured,
			wantText: []string{"services:", "in sync", "2 captured"},
		},
		{
			name:     "only in snapshot",
			services: map[string]bool{"main": true},
			captured: captured,
			wantText: []string{"services:", "only in snapshot: db"},
			dontWant: []string{"in sync"},
		},
		{
			name:     "only local",
			services: map[string]bool{"db": true, "main": true, "search": true},
			captured: captured,
			wantText: []string{"services:", "only local: search"},
		},
		{
			name:     "enabled flipped",
			services: map[string]bool{"db": false, "main": true},
			captured: captured,
			wantText: []string{"services:", "enabled differs: db"},
		},
		{
			name:     "no services captured",
			services: map[string]bool{"db": true},
			captured: nil,
			dontWant: []string{"services:"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := snapshotInspectProject(t, tc.services)
			writeSnapshotWithServices(t, base, "snap", tc.captured)
			flags := &cmdctx.RootFlags{
				ConfigPath: filepath.Join(base, "workspace.yml"),
				Root:       base,
			}
			var out bytes.Buffer
			cmd := &cobra.Command{Use: "test"}
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			if err := runSnapshotInspect(flags, cmd, "snap"); err != nil {
				t.Fatalf("err: %v", err)
			}
			s := out.String()
			for _, want := range tc.wantText {
				if !strings.Contains(s, want) {
					t.Errorf("want %q in:\n%s", want, s)
				}
			}
			for _, dont := range tc.dontWant {
				if strings.Contains(s, dont) {
					t.Errorf("did not want %q in:\n%s", dont, s)
				}
			}
		})
	}
}

func TestSnapshotInspect_ServicesDiff_JSON(t *testing.T) {
	base := snapshotInspectProject(t, map[string]bool{"main": true})
	writeSnapshotWithServices(t, base, "snap", []meta.ServiceSnapshot{
		{Name: "db", Enabled: true},
		{Name: "main", Enabled: true},
	})
	flags := &cmdctx.RootFlags{
		ConfigPath: filepath.Join(base, "workspace.yml"),
		Root:       base,
		Output:     "json",
	}
	var out bytes.Buffer
	cmd := &cobra.Command{Use: "test"}
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := runSnapshotInspect(flags, cmd, "snap"); err != nil {
		t.Fatalf("err: %v", err)
	}
	var payload struct {
		ServicesDiff *snapshotpkg.ServicesDiff `json:"services_diff"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if payload.ServicesDiff == nil {
		t.Fatalf("expected services_diff in payload: %s", out.String())
	}
	if len(payload.ServicesDiff.OnlyInSnapshot) != 1 || payload.ServicesDiff.OnlyInSnapshot[0] != "db" {
		t.Errorf("OnlyInSnapshot = %+v, want [db]", payload.ServicesDiff.OnlyInSnapshot)
	}
}
