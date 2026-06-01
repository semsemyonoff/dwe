package snapshot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/workflow/snapshot/meta"

	"github.com/spf13/cobra"
)

// snapshotTestProject sets up an empty devbox project (workspace.yml + snapshots/
// dir) and returns the project root. The on-disk workspace.yml is minimal but
// loadable by config.LoadConfig.
func snapshotTestProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfg := []byte("schema_version: 1\nproject:\n  name: testproj\n")
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), cfg, 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "snapshots"), 0o755); err != nil {
		t.Fatalf("mkdir snapshots: %v", err)
	}
	return dir
}

func writeTestSnapshot(t *testing.T, base, name string, m *meta.Manifest) {
	t.Helper()
	dir := filepath.Join(base, "snapshots", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := meta.SaveManifest(filepath.Join(dir, meta.ManifestFileName), m); err != nil {
		t.Fatalf("save: %v", err)
	}
}

// makeTestCmd builds a minimal cobra.Command with captured output/error buffers.
func makeTestCmd(t *testing.T) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errW bytes.Buffer
	cmd := &cobra.Command{Use: "test"}
	cmd.SetOut(&out)
	cmd.SetErr(&errW)
	return cmd, &out, &errW
}

// normalizeSnapshotPaths replaces absolute snapshot directory paths with a
// stable placeholder so golden files are portable across test runs.
func normalizeSnapshotPaths(s, base string) string {
	s = strings.ReplaceAll(s, base, "<BASE>")
	// Also normalize any remaining absolute paths that look like snapshot dirs.
	re := regexp.MustCompile(`"dir":"[^"]*"`)
	return re.ReplaceAllString(s, `"dir":"<DIR>"`)
}

// normalizeSnapshotSource replaces a source path (manifest file path) with a
// stable placeholder.
func normalizeSnapshotSource(s, base string) string {
	return strings.ReplaceAll(s, base, "<BASE>")
}

// loadOrUpdateGolden reads the golden file at path and compares it to got.
// When UPDATE_GOLDEN=1 is set, it writes got to the golden file instead.
func loadOrUpdateGolden(t *testing.T, path, got string) {
	t.Helper()
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run UPDATE_GOLDEN=1 to create)", path, err)
	}
	if string(want) != got {
		t.Errorf("golden mismatch for %s:\nwant: %s\n got: %s", path, want, got)
	}
}

func TestSnapshotList_Empty(t *testing.T) {
	base := snapshotTestProject(t)
	flags := &cmdctx.RootFlags{
		ConfigPath: filepath.Join(base, "workspace.yml"),
		Root:       base,
	}
	cmd, out, errW := makeTestCmd(t)
	if err := runSnapshotList(flags, cmd); err != nil {
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
	flags := &cmdctx.RootFlags{
		ConfigPath: filepath.Join(base, "workspace.yml"),
		Root:       base,
	}
	older := time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC)
	writeTestSnapshot(t, base, "alpha", &meta.Manifest{
		Name:      "alpha",
		CreatedAt: older,
		Artifacts: []meta.ArtifactInfo{{Path: "x", Size: 2048}},
	})
	writeTestSnapshot(t, base, "beta", &meta.Manifest{
		Name:        "beta",
		CreatedAt:   newer,
		Description: "WIP",
	})

	// Set current pointer to beta.
	if err := meta.WriteCurrent(base, "beta"); err != nil {
		t.Fatalf("write current: %v", err)
	}

	t.Run("table", func(t *testing.T) {
		cmd, out, _ := makeTestCmd(t)
		if err := runSnapshotList(flags, cmd); err != nil {
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
		flagsJSON := &cmdctx.RootFlags{
			ConfigPath: flags.ConfigPath,
			Root:       flags.Root,
			Output:     "json",
		}
		cmd, out, _ := makeTestCmd(t)
		if err := runSnapshotList(flagsJSON, cmd); err != nil {
			t.Fatalf("err: %v", err)
		}
		var got snapshotListJSON
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("decode json: %v\n%s", err, out.String())
		}
		if len(got.Snapshots) != 2 {
			t.Fatalf("entries = %d, want 2", len(got.Snapshots))
		}
		if got.Snapshots[0].Name != "beta" || !got.Snapshots[0].Current {
			t.Errorf("expected beta first + current=true, got %+v", got.Snapshots[0])
		}
		if got.Snapshots[1].Name != "alpha" || got.Snapshots[1].TotalSize != 2048 {
			t.Errorf("expected alpha second with size 2048, got %+v", got.Snapshots[1])
		}
	})

	t.Run("golden", func(t *testing.T) {
		flagsJSON := &cmdctx.RootFlags{
			ConfigPath: flags.ConfigPath,
			Root:       flags.Root,
			Output:     "json",
		}
		cmd, out, _ := makeTestCmd(t)
		if err := runSnapshotList(flagsJSON, cmd); err != nil {
			t.Fatalf("err: %v", err)
		}
		got := normalizeSnapshotPaths(out.String(), base)
		loadOrUpdateGolden(t, "testdata/list.json.golden", got)
	})
}

func TestSnapshotCurrent(t *testing.T) {
	base := snapshotTestProject(t)
	flags := &cmdctx.RootFlags{
		ConfigPath: filepath.Join(base, "workspace.yml"),
		Root:       base,
	}

	t.Run("cleared", func(t *testing.T) {
		cmd, out, errW := makeTestCmd(t)
		if err := runSnapshotCurrent(flags, cmd); err != nil {
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
		writeTestSnapshot(t, base, "feature-x", &meta.Manifest{
			Name:        "feature-x",
			CreatedAt:   time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC),
			Description: "wip",
		})
		if err := meta.WriteCurrent(base, "feature-x"); err != nil {
			t.Fatalf("write current: %v", err)
		}
		cmd, out, _ := makeTestCmd(t)
		if err := runSnapshotCurrent(flags, cmd); err != nil {
			t.Fatalf("err: %v", err)
		}
		if !strings.Contains(out.String(), "feature-x") {
			t.Errorf("missing name on stdout: %q", out.String())
		}
		if !strings.Contains(out.String(), "wip") {
			t.Errorf("missing description on stdout: %q", out.String())
		}
	})

	t.Run("json_none", func(t *testing.T) {
		// Start fresh — no current pointer set in this subtest.
		base2 := snapshotTestProject(t)
		flags2 := &cmdctx.RootFlags{
			ConfigPath: filepath.Join(base2, "workspace.yml"),
			Root:       base2,
			Output:     "json",
		}
		cmd, out, _ := makeTestCmd(t)
		if err := runSnapshotCurrent(flags2, cmd); err != nil {
			t.Fatalf("err: %v", err)
		}
		var got snapshotCurrentWrapper
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("decode json: %v\n%s", err, out.String())
		}
		if got.Current != nil {
			t.Errorf("expected current=null, got %+v", got.Current)
		}
	})

	t.Run("golden", func(t *testing.T) {
		// Create a fresh base with a known current snapshot for the golden.
		base2 := snapshotTestProject(t)
		flags2 := &cmdctx.RootFlags{
			ConfigPath: filepath.Join(base2, "workspace.yml"),
			Root:       base2,
			Output:     "json",
		}
		writeTestSnapshot(t, base2, "release-1", &meta.Manifest{
			Name:        "release-1",
			CreatedAt:   time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC),
			Description: "stable",
		})
		if err := meta.WriteCurrent(base2, "release-1"); err != nil {
			t.Fatalf("write current: %v", err)
		}
		cmd, out, _ := makeTestCmd(t)
		if err := runSnapshotCurrent(flags2, cmd); err != nil {
			t.Fatalf("err: %v", err)
		}
		got := normalizeSnapshotPaths(out.String(), base2)
		loadOrUpdateGolden(t, "testdata/current.json.golden", got)
	})
}

func TestSnapshotInspect_FromDir(t *testing.T) {
	base := snapshotTestProject(t)
	flags := &cmdctx.RootFlags{
		ConfigPath: filepath.Join(base, "workspace.yml"),
		Root:       base,
	}
	writeTestSnapshot(t, base, "feature-x", &meta.Manifest{
		Name:      "feature-x",
		CreatedAt: time.Date(2026, 5, 24, 11, 0, 0, 0, time.UTC),
		Project:   meta.ProjectInfo{Name: "testproj", ConfigHash: "abc"},
		Artifacts: []meta.ArtifactInfo{
			{Path: "db/main.sql.gz", Size: 1024, Sha256: strings.Repeat("a", 64)},
		},
	})

	t.Run("text", func(t *testing.T) {
		cmd, out, _ := makeTestCmd(t)
		if err := runSnapshotInspect(flags, cmd, "feature-x"); err != nil {
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
		flagsJSON := &cmdctx.RootFlags{
			ConfigPath: flags.ConfigPath,
			Root:       flags.Root,
			Output:     "json",
		}
		cmd, out, _ := makeTestCmd(t)
		if err := runSnapshotInspect(flagsJSON, cmd, "feature-x"); err != nil {
			t.Fatalf("err: %v", err)
		}
		var payload snapshotInspectJSON
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
		cmd, _, _ := makeTestCmd(t)
		if err := runSnapshotInspect(flags, cmd, "Bad Name!"); err == nil {
			t.Fatalf("expected error for invalid name")
		}
	})

	t.Run("golden", func(t *testing.T) {
		flagsJSON := &cmdctx.RootFlags{
			ConfigPath: flags.ConfigPath,
			Root:       flags.Root,
			Output:     "json",
		}
		cmd, out, _ := makeTestCmd(t)
		if err := runSnapshotInspect(flagsJSON, cmd, "feature-x"); err != nil {
			t.Fatalf("err: %v", err)
		}
		got := normalizeSnapshotSource(out.String(), base)
		loadOrUpdateGolden(t, "testdata/inspect.json.golden", got)
	})
}

func TestSnapshotInspect_FromTar(t *testing.T) {
	base := snapshotTestProject(t)
	flags := &cmdctx.RootFlags{
		ConfigPath: filepath.Join(base, "workspace.yml"),
		Root:       base,
	}

	// Build a small .tar.gz with a manifest.yml.
	manifest := []byte("name: from-tar\ncreated_at: 2026-05-24T10:00:00Z\nproject:\n  name: testproj\n  config_hash: zzz\n")
	tarPath := filepath.Join(base, "ship.tar.gz")
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.yml", Mode: 0o644, Size: int64(len(manifest)), Typeflag: tar.TypeReg}); err != nil {
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

	cmd, out, _ := makeTestCmd(t)
	if err := runSnapshotInspect(flags, cmd, tarPath); err != nil {
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
	flags := &cmdctx.RootFlags{
		ConfigPath: filepath.Join(base, "workspace.yml"),
		Root:       base,
	}
	// Write a deploy state with a different config_hash.
	stateDir := filepath.Join(base, ".dwe", "deploy")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stateYAML := "schema_version: \"1\"\nproject:\n  status: deployed\n  config_hash: live-hash\n"
	if err := os.WriteFile(filepath.Join(stateDir, "state.yml"), []byte(stateYAML), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	writeTestSnapshot(t, base, "snap", &meta.Manifest{
		Name:      "snap",
		CreatedAt: time.Now().UTC(),
		Project:   meta.ProjectInfo{Name: "testproj", ConfigHash: "snap-hash"},
	})

	cmd, out, _ := makeTestCmd(t)
	if err := runSnapshotInspect(flags, cmd, "snap"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(out.String(), "DIVERGED") {
		t.Errorf("expected DIVERGED marker: %s", out.String())
	}
}

func TestSnapshotCmd_ArgsValidation(t *testing.T) {
	// Verify each subcommand declares its Args validator (no in-RunE arg parsing).
	root := NewCmd("", &cmdctx.RootFlags{})
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
	flags := &cmdctx.RootFlags{
		ConfigPath: filepath.Join(base, "workspace.yml"),
		Root:       base,
	}
	writeTestSnapshot(t, base, "alpha", &meta.Manifest{Name: "alpha", CreatedAt: time.Now().UTC()})
	writeTestSnapshot(t, base, "beta", &meta.Manifest{Name: "beta", CreatedAt: time.Now().UTC()})

	fn := snapshotNameCompletion(flags)
	// Pass a real cobra.Command — completion contract reads Lookup("config") off root.
	root := &cobra.Command{Use: "dwe"}
	root.PersistentFlags().StringVarP(&flags.ConfigPath, "config", "c", flags.ConfigPath, "")
	names, dir := fn(root, nil, "")
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v", dir)
	}
	got := strings.Join(names, ",")
	if !strings.Contains(got, "alpha") || !strings.Contains(got, "beta") {
		t.Errorf("expected both snapshots, got %q", got)
	}
}
