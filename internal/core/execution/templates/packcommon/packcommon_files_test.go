package packcommon_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/templates/ai"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/ide"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/packcommon"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

var destLabels = packcommon.DestLabels{
	Path: "destination", Boundary: "hub directory", ResolveBoundary: "hub dir",
}

func TestPrepareFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		content        string
		renderTemplate bool
		sourceMode     os.FileMode
		override       bool
		want           string
		wantMode       os.FileMode
	}{
		{
			name: "template", content: "project={{ .Project.Name }}",
			renderTemplate: true, sourceMode: 0o600, want: "project=demo", wantMode: 0o644,
		},
		{
			name: "verbatim", content: "{{PLAN_FILE}}\r\n{{ .Project.Name }}\x00\xff",
			sourceMode: 0o644, want: "{{PLAN_FILE}}\r\n{{ .Project.Name }}\x00\xff", wantMode: 0o644,
		},
		{
			name: "executable", content: "#!/bin/sh\n", sourceMode: 0o755,
			want: "#!/bin/sh\n", wantMode: 0o755,
		},
		{
			name: "group execute bit", content: "script", sourceMode: 0o610,
			want: "script", wantMode: 0o755,
		},
		{
			name: "other execute bit", content: "script", sourceMode: 0o601,
			want: "script", wantMode: 0o755,
		},
		{
			name: "override template", content: "override={{ .Project.Name }}",
			renderTemplate: true, sourceMode: 0o755, override: true, want: "override=demo", wantMode: 0o755,
		},
		{
			name: "override verbatim", content: "{{PLAN_FILE}}", sourceMode: 0o755,
			override: true, want: "{{PLAN_FILE}}", wantMode: 0o755,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeSource(t, root, "workspace", "test", "source", "canonical", 0o644)
			pack := "test"
			if tt.override {
				pack += ".local"
			}
			writeSource(t, root, "workspace", pack, "source", tt.content, tt.sourceMode)
			data := packcommon.TemplateData{Project: config.ProjectConfig{Name: "demo"}}
			file, err := packcommon.PrepareFile("workspace", root, "test", "source", data, tt.renderTemplate)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(file.Data, []byte(tt.want)) {
				t.Errorf("bytes = %q, want %q", file.Data, tt.want)
			}
			if file.Mode != tt.wantMode || file.FromOverride != tt.override {
				t.Errorf("mode/override = %o/%v, want %o/%v", file.Mode, file.FromOverride, tt.wantMode, tt.override)
			}
			if _, err := os.Stat(filepath.Join(root, ".ralphex")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("preparation created a destination: %v", err)
			}
		})
	}
}

func TestPrepareFileErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		missing bool
		wantErr string
	}{
		{name: "missing source", missing: true, wantErr: "resolve template"},
		{name: "invalid template", content: "{{", wantErr: "parse template"},
		{name: "missing key", content: "{{ .Cfg.Raw.absent }}", wantErr: "render template"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if !tt.missing {
				writeSource(t, root, "workspace", "test", "source", tt.content, 0o644)
			}
			data := packcommon.TemplateData{Cfg: &config.DweConfig{Raw: map[string]any{}}}
			_, err := packcommon.PrepareFile("workspace", root, "test", "source", data, true)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
			if tt.missing && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing source error does not wrap os.ErrNotExist: %v", err)
			}
		})
	}
}

func TestCheckDestMissingDirectories(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	hub := filepath.Join(root, "services", "app")
	dest := "nested/deeper/output"
	absDest, err := packcommon.CheckDest(dest, hub, root, destLabels)
	if err != nil {
		t.Fatal(err)
	}
	if absDest != filepath.Join(hub, dest) {
		t.Fatalf("destination = %q", absDest)
	}
	if _, err := os.Stat(filepath.Join(root, "services")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("CheckDest created directories: %v", err)
	}
}

func TestCheckDestErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		setup   func(t *testing.T, root, hub, outside string) (string, string)
		wantErr string
	}{
		{
			name: "escapes hub",
			setup: func(_ *testing.T, _, hub, _ string) (string, string) {
				return "../escape/output", hub
			},
			wantErr: "escapes hub directory",
		},
		{
			name: "absolute path",
			setup: func(_ *testing.T, _, hub, outside string) (string, string) {
				return filepath.Join(outside, "output"), hub
			},
			wantErr: "path is absolute",
		},
		{
			name: "symlink destination",
			setup: func(t *testing.T, _, hub, outside string) (string, string) {
				if err := os.Symlink(filepath.Join(outside, "output"), filepath.Join(hub, "output")); err != nil {
					t.Fatal(err)
				}
				return "output", hub
			},
			wantErr: "is a symlink",
		},
		{
			name: "directory destination",
			setup: func(t *testing.T, _, hub, _ string) (string, string) {
				if err := os.Mkdir(filepath.Join(hub, "output"), 0o755); err != nil {
					t.Fatal(err)
				}
				return "output", hub
			},
			wantErr: "is a directory",
		},
		{
			name: "symlink parent",
			setup: func(t *testing.T, _, hub, outside string) (string, string) {
				if err := os.Symlink(outside, filepath.Join(hub, "nested")); err != nil {
					t.Fatal(err)
				}
				return "nested/missing/output", hub
			},
			wantErr: "contains symlink",
		},
		{
			name: "symlink hub",
			setup: func(t *testing.T, root, _, outside string) (string, string) {
				link := filepath.Join(root, "linked")
				if err := os.Symlink(outside, link); err != nil {
					t.Fatal(err)
				}
				return "output", link
			},
			wantErr: "contains symlink",
		},
		{
			name: "hub outside project",
			setup: func(_ *testing.T, _, _, outside string) (string, string) {
				return "output", outside
			},
			wantErr: "not under root",
		},
		{
			name: "parent is a file",
			setup: func(t *testing.T, _, hub, _ string) (string, string) {
				writeFile(t, filepath.Join(hub, "nested"), "keep", 0o644)
				return "nested/missing/output", hub
			},
			wantErr: "not a directory",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root, outside := t.TempDir(), t.TempDir()
			hub := filepath.Join(root, "hub")
			if err := os.Mkdir(hub, 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(outside, "output"), "keep", 0o644)
			dest, destRoot := tt.setup(t, root, hub, outside)
			_, err := packcommon.CheckDest(dest, destRoot, root, destLabels)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("CheckDest error = %v, want %q", err, tt.wantErr)
			}
			file := packcommon.PreparedFile{Data: []byte("replace"), Mode: 0o755}
			if err := packcommon.WriteFile(file, dest, destRoot, root, destLabels, true); err == nil {
				t.Fatal("WriteFile accepted an unsafe destination")
			}
			assertFile(t, filepath.Join(outside, "output"), "keep", 0o644)
		})
	}
}

func TestWriteFileModes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dest := "nested/output"
	for _, mode := range []os.FileMode{0o644, 0o755, 0o644} {
		file := packcommon.PreparedFile{Data: []byte("rendered"), Mode: mode}
		if err := packcommon.WriteFile(file, dest, root, root, destLabels, true); err != nil {
			t.Fatal(err)
		}
		assertFile(t, filepath.Join(root, dest), "rendered", mode)
	}
}

func TestLegacyRenderFileModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind   string
		render func(string, string, string, packcommon.TemplateData, string, string, string) (bool, error)
	}{
		{kind: "ai", render: ai.RenderTemplateFile},
		{kind: "ide", render: ide.RenderTemplateFile},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeSource(t, root, tt.kind, "test", "source", "{{ .Project.Name }}", 0o755)
			hub := filepath.Join(root, "hub")
			data := packcommon.TemplateData{Project: config.ProjectConfig{Name: "demo"}, Cfg: &config.DweConfig{}}
			if _, err := tt.render(root, "test", "source", data, "output", hub, root); err != nil {
				t.Fatal(err)
			}
			assertFile(t, filepath.Join(hub, "output"), "demo", 0o644)
			if err := os.Chmod(filepath.Join(hub, "output"), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := tt.render(root, "test", "source", data, "output", hub, root); err != nil {
				t.Fatal(err)
			}
			assertFile(t, filepath.Join(hub, "output"), "demo", 0o755)
			_, err := tt.render(root, "test", "source", packcommon.TemplateData{}, "output", hub, root)
			if err == nil || err.Error() != tt.kind+": nil cfg" {
				t.Fatalf("nil config error = %v", err)
			}
		})
	}
}

func TestEnsureRelativeSymlink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	hub := filepath.Join(root, "hub")
	for _, target := range []string{"AGENTS.md", "AGENTS.md", "OTHER.md"} {
		if err := packcommon.EnsureRelativeSymlink("nested/link", target, hub, root, "hub", ""); err != nil {
			t.Fatal(err)
		}
		got, err := os.Readlink(filepath.Join(hub, "nested/link"))
		if err != nil || got != "../"+target {
			t.Fatalf("symlink = %q, %v; want ../%s", got, err, target)
		}
	}
	writeFile(t, filepath.Join(hub, "regular"), "keep", 0o644)
	err := packcommon.EnsureRelativeSymlink("regular", "AGENTS.md", hub, root, "hub", "disable rendering")
	if err == nil || !strings.Contains(err.Error(), "; disable rendering") {
		t.Fatalf("non-symlink refusal lacks hint: %v", err)
	}
	assertFile(t, filepath.Join(hub, "regular"), "keep", 0o644)
	for _, kind := range []string{"ai", "ide"} {
		var err error
		if kind == "ai" {
			err = ai.EnsureRelativeSymlink("regular", "AGENTS.md", hub, root)
		} else {
			err = ide.EnsureRelativeSymlink("regular", "AGENTS.md", hub, root)
		}
		want := "refuse to overwrite non-symlink file at regular"
		if kind == "ai" {
			want += "; remove it or disable via render.ai.enabled: false"
		}
		if err == nil || err.Error() != want {
			t.Errorf("%s refusal = %v, want %q", kind, err, want)
		}
	}
}

func TestEnsureRelativeSymlinkErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		link    string
		target  string
		wantErr string
	}{
		{name: "link escapes", link: "../escape", target: "output", wantErr: "link \"../escape\" escapes project directory"},
		{name: "target escapes", link: "output", target: "../escape", wantErr: "target \"../escape\" escapes project directory"},
		{name: "parent symlink", link: "linked/missing/output", target: "output", wantErr: "symlink parent dir contains symlink"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root, outside := t.TempDir(), t.TempDir()
			hub := filepath.Join(root, "hub")
			if err := os.Mkdir(hub, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(hub, "linked")); err != nil {
				t.Fatal(err)
			}
			err := packcommon.EnsureRelativeSymlink(tt.link, tt.target, hub, root, "project", "")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatalf("symlink helper wrote outside the boundary: %v, %v", entries, err)
			}
		})
	}
}

func writeSource(t *testing.T, root, kind, pack, rel, content string, mode os.FileMode) {
	t.Helper()
	writeFile(t, filepath.Join(root, "workspace/templates", kind, pack, rel), content, mode)
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("file content = %q, want %q", got, content)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != mode {
		t.Errorf("file mode = %o, want %o", fi.Mode().Perm(), mode)
	}
}
