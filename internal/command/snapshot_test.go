package command

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"devbox-cli/internal/snapshot"

	"github.com/spf13/cobra"
)

// snapshotTestProject sets up an empty devbox project (devbox.yml + snapshots/
// dir) and returns the project root. The on-disk devbox.yml is minimal but
// loadable by config.LoadConfig.
func snapshotTestProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfg := []byte("schema_version: 1\nproject:\n  name: testproj\n")
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), cfg, 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "snapshots"), 0o755); err != nil {
		t.Fatalf("mkdir snapshots: %v", err)
	}
	return dir
}

func writeTestSnapshot(t *testing.T, base, name string, m *snapshot.Manifest) {
	t.Helper()
	dir := filepath.Join(base, "snapshots", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := snapshot.SaveManifest(filepath.Join(dir, snapshot.ManifestFileName), m); err != nil {
		t.Fatalf("save: %v", err)
	}
}

func TestSnapshotList_Empty(t *testing.T) {
	base := snapshotTestProject(t)
	flags := &rootFlags{
		configPath:  filepath.Join(base, "devbox.yml"),
		projectRoot: base,
	}
	var out, errW bytes.Buffer
	if err := runSnapshotList(flags, &out, &errW, false); err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected empty stdout, got %q", out.String())
	}
	if !strings.Contains(errW.String(), "no snapshots found") {
		t.Errorf("missing empty-state hint on stderr: %q", errW.String())
	}
}

func TestSnapshotList_TableAndJSON(t *testing.T) {
	base := snapshotTestProject(t)
	flags := &rootFlags{
		configPath:  filepath.Join(base, "devbox.yml"),
		projectRoot: base,
	}
	older := time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC)
	writeTestSnapshot(t, base, "alpha", &snapshot.Manifest{
		Name:      "alpha",
		CreatedAt: older,
		Artifacts: []snapshot.ArtifactInfo{{Path: "x", Size: 2048}},
	})
	writeTestSnapshot(t, base, "beta", &snapshot.Manifest{
		Name:        "beta",
		CreatedAt:   newer,
		Description: "WIP",
	})

	// Set current pointer to beta.
	if err := snapshot.WriteCurrent(base, "beta"); err != nil {
		t.Fatalf("write current: %v", err)
	}

	t.Run("table", func(t *testing.T) {
		var out, errW bytes.Buffer
		if err := runSnapshotList(flags, &out, &errW, false); err != nil {
			t.Fatalf("err: %v", err)
		}
		s := out.String()
		// Beta first (newer), Alpha second.
		bIdx := strings.Index(s, "beta")
		aIdx := strings.Index(s, "alpha")
		if bIdx < 0 || aIdx < 0 || bIdx > aIdx {
			t.Errorf("unexpected ordering / missing entries:\n%s", s)
		}
		if !strings.Contains(s, "beta *") {
			t.Errorf("expected current marker on beta:\n%s", s)
		}
		if !strings.Contains(s, "WIP") {
			t.Errorf("expected description in table:\n%s", s)
		}
	})

	t.Run("json", func(t *testing.T) {
		var out, errW bytes.Buffer
		if err := runSnapshotList(flags, &out, &errW, true); err != nil {
			t.Fatalf("err: %v", err)
		}
		var got []snapshotListJSONEntry
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("decode json: %v\n%s", err, out.String())
		}
		if len(got) != 2 {
			t.Fatalf("entries = %d, want 2", len(got))
		}
		if got[0].Name != "beta" || !got[0].Current {
			t.Errorf("expected beta first + current=true, got %+v", got[0])
		}
		if got[1].Name != "alpha" || got[1].TotalSize != 2048 {
			t.Errorf("expected alpha second with size 2048, got %+v", got[1])
		}
	})
}

func TestSnapshotCurrent(t *testing.T) {
	base := snapshotTestProject(t)
	flags := &rootFlags{
		configPath:  filepath.Join(base, "devbox.yml"),
		projectRoot: base,
	}

	t.Run("cleared", func(t *testing.T) {
		var out, errW bytes.Buffer
		if err := runSnapshotCurrent(flags, &out, &errW); err != nil {
			t.Fatalf("err: %v", err)
		}
		if out.Len() != 0 {
			t.Errorf("expected empty stdout, got %q", out.String())
		}
		if !strings.Contains(errW.String(), "no current snapshot") {
			t.Errorf("missing hint: %q", errW.String())
		}
	})

	t.Run("set", func(t *testing.T) {
		writeTestSnapshot(t, base, "feature-x", &snapshot.Manifest{
			Name:        "feature-x",
			CreatedAt:   time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC),
			Description: "wip",
		})
		if err := snapshot.WriteCurrent(base, "feature-x"); err != nil {
			t.Fatalf("write current: %v", err)
		}
		var out, errW bytes.Buffer
		if err := runSnapshotCurrent(flags, &out, &errW); err != nil {
			t.Fatalf("err: %v", err)
		}
		if !strings.Contains(out.String(), "feature-x") {
			t.Errorf("missing name on stdout: %q", out.String())
		}
		if !strings.Contains(out.String(), "wip") {
			t.Errorf("missing description on stdout: %q", out.String())
		}
	})
}

func TestSnapshotInspect_FromDir(t *testing.T) {
	base := snapshotTestProject(t)
	flags := &rootFlags{
		configPath:  filepath.Join(base, "devbox.yml"),
		projectRoot: base,
	}
	writeTestSnapshot(t, base, "feature-x", &snapshot.Manifest{
		Name:      "feature-x",
		CreatedAt: time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC),
		Project:   snapshot.ProjectInfo{Name: "testproj", ConfigHash: "abc"},
		Artifacts: []snapshot.ArtifactInfo{
			{Path: "db/main.sql.gz", Size: 1024, Sha256: strings.Repeat("a", 64)},
		},
	})

	t.Run("text", func(t *testing.T) {
		var out bytes.Buffer
		if err := runSnapshotInspect(flags, &out, "feature-x", false); err != nil {
			t.Fatalf("err: %v", err)
		}
		s := out.String()
		for _, want := range []string{"feature-x", "testproj", "db/main.sql.gz", "abc"} {
			if !strings.Contains(s, want) {
				t.Errorf("expected %q in:\n%s", want, s)
			}
		}
	})

	t.Run("json", func(t *testing.T) {
		var out bytes.Buffer
		if err := runSnapshotInspect(flags, &out, "feature-x", true); err != nil {
			t.Fatalf("err: %v", err)
		}
		var payload struct {
			Source             string             `json:"source"`
			Manifest           *snapshot.Manifest `json:"manifest"`
			CurrentConfigHash  string             `json:"current_config_hash"`
			ConfigHashDiverged bool               `json:"config_hash_diverged"`
		}
		if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
			t.Fatalf("decode: %v\n%s", err, out.String())
		}
		if payload.Manifest == nil || payload.Manifest.Name != "feature-x" {
			t.Errorf("unexpected manifest: %+v", payload.Manifest)
		}
		if payload.ConfigHashDiverged {
			t.Errorf("should not diverge with no deploy state")
		}
	})

	t.Run("invalid name", func(t *testing.T) {
		var out bytes.Buffer
		if err := runSnapshotInspect(flags, &out, "Bad Name!", false); err == nil {
			t.Fatalf("expected error for invalid name")
		}
	})
}

func TestSnapshotInspect_FromTar(t *testing.T) {
	base := snapshotTestProject(t)
	flags := &rootFlags{
		configPath:  filepath.Join(base, "devbox.yml"),
		projectRoot: base,
	}

	// Build a small .tar.gz with a manifest.yml.
	manifest := []byte("name: from-tar\ncreated_at: 2026-05-24T10:00:00Z\nproject:\n  name: testproj\n  config_hash: zzz\n")
	tarPath := filepath.Join(base, "ship.tar.gz")
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "from-tar/manifest.yml", Mode: 0o644, Size: int64(len(manifest)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("hdr: %v", err)
	}
	if _, err := tw.Write(manifest); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	if err := os.WriteFile(tarPath, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write tar: %v", err)
	}

	var out bytes.Buffer
	if err := runSnapshotInspect(flags, &out, tarPath, false); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(out.String(), "from-tar") {
		t.Errorf("expected name in output: %s", out.String())
	}
	if !strings.Contains(out.String(), tarPath) {
		t.Errorf("expected source path in output: %s", out.String())
	}
}

func TestSnapshotInspect_ConfigDiverged(t *testing.T) {
	base := snapshotTestProject(t)
	flags := &rootFlags{
		configPath:  filepath.Join(base, "devbox.yml"),
		projectRoot: base,
	}
	// Write a deploy state with a different config_hash.
	stateDir := filepath.Join(base, ".devbox", "deploy")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stateYAML := "schema_version: \"1\"\nproject:\n  status: deployed\n  config_hash: live-hash\n"
	if err := os.WriteFile(filepath.Join(stateDir, "state.yml"), []byte(stateYAML), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	writeTestSnapshot(t, base, "snap", &snapshot.Manifest{
		Name:      "snap",
		CreatedAt: time.Now().UTC(),
		Project:   snapshot.ProjectInfo{Name: "testproj", ConfigHash: "snap-hash"},
	})

	var out bytes.Buffer
	if err := runSnapshotInspect(flags, &out, "snap", false); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(out.String(), "DIVERGED") {
		t.Errorf("expected DIVERGED marker: %s", out.String())
	}
}

func TestSnapshotCmd_ArgsValidation(t *testing.T) {
	// Verify each subcommand declares its Args validator (no in-RunE arg parsing).
	root := newSnapshotCmd(&rootFlags{})
	cases := []struct {
		name        string
		args        []string
		wantSuccess bool
	}{
		{"list", []string{"list", "extra"}, false},
		{"current", []string{"current", "extra"}, false},
		{"inspect missing arg", []string{"inspect"}, false},
		{"inspect extra arg", []string{"inspect", "a", "b"}, false},
		{"restore missing arg", []string{"restore"}, false},
		{"restore extra arg", []string{"restore", "a", "b"}, false},
		{"rollback extra arg", []string{"rollback", "a"}, false},
		{"rollback no arg", []string{"rollback"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub, _, err := root.Find(tc.args)
			if err != nil {
				t.Fatalf("find: %v", err)
			}
			// Apply args after the subcommand name.
			tail := tc.args[1:]
			err = sub.Args(sub, tail)
			if (err == nil) != tc.wantSuccess {
				t.Errorf("Args validation err = %v, wantSuccess=%v", err, tc.wantSuccess)
			}
		})
	}
}

func TestSnapshotNameCompletion(t *testing.T) {
	base := snapshotTestProject(t)
	flags := &rootFlags{
		configPath:  filepath.Join(base, "devbox.yml"),
		projectRoot: base,
	}
	writeTestSnapshot(t, base, "alpha", &snapshot.Manifest{Name: "alpha", CreatedAt: time.Now().UTC()})
	writeTestSnapshot(t, base, "beta", &snapshot.Manifest{Name: "beta", CreatedAt: time.Now().UTC()})

	fn := snapshotNameCompletion(flags)
	// Pass a real cobra.Command — completion contract reads Lookup("config") off root.
	root := NewRootCmd()
	names, dir := fn(root, nil, "")
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v", dir)
	}
	got := strings.Join(names, ",")
	if !strings.Contains(got, "alpha") || !strings.Contains(got, "beta") {
		t.Errorf("expected both snapshots, got %q", got)
	}
}
