package workspace_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/semsemyonoff/dwe/internal/core/execution/templates/manifest"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/packcommon"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/workspace"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

func TestResolvePack(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"ralphex", "root-agents", "Pack_2"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			want := filepath.Join(root, "workspace", "templates", "workspace", name)
			mkdir(t, want)
			got, err := workspace.ResolvePack(root, name)
			if err != nil || got != want {
				t.Fatalf("ResolvePack = %q, %v; want %q", got, err, want)
			}
		})
	}
}

func TestResolvePackErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(*testing.T, string)
		want  string
	}{
		{name: "missing", want: "no such file"},
		{name: "file", setup: func(t *testing.T, dir string) { writeFile(t, dir, "file") }, want: "not a directory"},
		{name: "pack symlink", setup: func(t *testing.T, dir string) {
			mkdir(t, filepath.Dir(dir))
			symlink(t, t.TempDir(), dir)
		}, want: "symlink"},
		{name: "parent symlink", setup: func(t *testing.T, dir string) {
			parent := filepath.Dir(dir)
			mkdir(t, filepath.Dir(parent))
			outside := t.TempDir()
			mkdir(t, filepath.Join(outside, "test"))
			symlink(t, outside, parent)
		}, want: "symlink"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "workspace", "templates", "workspace", "test")
			if tt.setup != nil {
				tt.setup(t, dir)
			}
			_, err := workspace.ResolvePack(root, "test")
			requireError(t, err, tt.want)
			if tt.name == "missing" && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing pack error must wrap os.ErrNotExist: %v", err)
			}
		})
	}
	_, err := workspace.ResolvePack("", "test")
	requireError(t, err, "project root is required")
}

func TestValidatePacks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		names []string
		want  string
	}{
		{name: "empty list"},
		{name: "valid list", names: []string{"ralphex", "Root_2"}},
		{name: "duplicate", names: []string{"test", "other", "test"}, want: "duplicated"},
		{name: "empty name", names: []string{""}, want: "empty"},
		{name: "escape", names: []string{"../test"}, want: "identifier-safe"},
		{name: "absolute", names: []string{"/test"}, want: "identifier-safe"},
		{name: "shadow name", names: []string{"test.local"}, want: "identifier-safe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := workspace.ValidatePacks(tt.names)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			requireError(t, err, tt.want)
			_, err = workspace.ResolvePack(t.TempDir(), tt.names[0])
			if tt.name != "duplicate" {
				requireError(t, err, tt.want)
			}
		})
	}
}

func TestPlan(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	createPack(t, root, "ralphex", manifest.File{
		Render: []manifest.RenderEntry{
			{From: "config.tmpl", To: ".ralphex/config"},
			{From: "task.md", To: ".ralphex/prompts/task.md"},
			{From: "ws-git", To: ".ralphex/scripts/ws-git"},
		},
	}, map[string]string{
		"config.tmpl": "canonical {{ .Project.Name }}",
		"task.md":     "{{PLAN_FILE}}\r\n{{ .Project.Name }}\x00\xff",
		"ws-git":      "#!/bin/sh\n",
	})
	override := filepath.Join(root, "workspace", "templates", "workspace", "ralphex.local", "config.tmpl")
	writeFile(t, override, "override {{ .Project.Name }}")
	script := filepath.Join(root, "workspace", "templates", "workspace", "ralphex", "ws-git")
	if err := os.Chmod(script, 0o751); err != nil {
		t.Fatal(err)
	}
	createPack(t, root, "agents", manifest.File{
		Render:   []manifest.RenderEntry{{From: "AGENTS.md.tmpl", To: "AGENTS.md"}},
		Symlinks: []manifest.SymlinkEntry{{Link: "agent/CLAUDE.md", To: "AGENTS.md"}},
	}, map[string]string{"AGENTS.md.tmpl": "{{ .Project.Name }}/{{ .Service }}/{{ .Resolved }}"})
	data := packcommon.TemplateData{Project: config.ProjectConfig{Name: "demo"}}
	before := snapshot(t, root)
	planned, err := workspace.Plan(root, []string{"ralphex", "agents"}, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(planned) != 2 || planned[0].Name != "ralphex" || planned[1].Name != "agents" {
		t.Fatalf("unexpected packs: %+v", planned)
	}
	files := planned[0].Files
	if len(files) != 3 {
		t.Fatalf("files = %d, want 3", len(files))
	}
	for i, want := range []struct {
		from, to, content string
		mode              os.FileMode
		override          bool
	}{
		{from: "config.tmpl", to: ".ralphex/config", content: "override demo", mode: 0o644, override: true},
		{from: "task.md", to: ".ralphex/prompts/task.md", content: "{{PLAN_FILE}}\r\n{{ .Project.Name }}\x00\xff", mode: 0o644},
		{from: "ws-git", to: ".ralphex/scripts/ws-git", content: "#!/bin/sh\n", mode: 0o755},
	} {
		got := files[i]
		if got.From != want.from || got.To != want.to || string(got.File.Data) != want.content {
			t.Errorf("file[%d] = %+v (data %q), want %+v", i, got, got.File.Data, want)
		}
		if got.File.Mode != want.mode || got.File.FromOverride != want.override {
			t.Errorf("file[%d] mode/override = %o/%v, want %o/%v",
				i, got.File.Mode, got.File.FromOverride, want.mode, want.override)
		}
	}
	if string(planned[1].Files[0].File.Data) != "demo//" || len(planned[1].Symlinks) != 1 {
		t.Fatalf("unexpected agents pack: %+v", planned[1])
	}
	assertUnchanged(t, root, before)
}

func TestPlanProtectedDestinations(t *testing.T) {
	t.Parallel()
	paths := []string{
		"workspace.yml", "workspace", "services", ".dwe", ".git", ".env",
		"Workspace.yml", ".GIT/hooks", "WoRkSpAcE/templates/new", "SERVICES/app",
		".DwE/state", ".ENV/child", "workspace.yml/child", "safe/../.git/hooks",
	}
	for _, path := range paths {
		for _, kind := range []string{"render", "symlink"} {
			t.Run(kind+"/"+path, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				m := manifest.File{Render: []manifest.RenderEntry{{From: "source", To: path}}}
				if kind == "symlink" {
					m.Render[0].To = "safe/output"
					m.Symlinks = []manifest.SymlinkEntry{{Link: path, To: "safe/output"}}
				}
				createPack(t, root, "test", m, map[string]string{"source": "content"})
				before := snapshot(t, root)
				planned, err := workspace.Plan(root, []string{"test"}, packcommon.TemplateData{})
				requireError(t, err, "protected")
				if planned != nil {
					t.Fatal("error returned a partial plan")
				}
				assertUnchanged(t, root, before)
			})
		}
	}
}

func TestPlanOverrideOnlySource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	createPack(t, root, "test", manifest.File{
		Render: []manifest.RenderEntry{{From: "prompt", To: "prompt.tmpl"}},
	}, nil)
	source := filepath.Join(root, "workspace", "templates", "workspace", "test.local", "prompt")
	writeFile(t, source, "{{PLAN_FILE}}")
	if err := os.Chmod(source, 0o700); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, root)
	planned, err := workspace.Plan(root, []string{"test"}, packcommon.TemplateData{})
	if err != nil {
		t.Fatal(err)
	}
	file := planned[0].Files[0].File
	if string(file.Data) != "{{PLAN_FILE}}" || file.Mode != 0o755 || !file.FromOverride {
		t.Fatalf("override-only source = %+v (data %q)", file, file.Data)
	}
	assertUnchanged(t, root, before)
}

func TestPlanCollisions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, first, second string
		symlink, firstLink  bool
	}{
		{name: "equal", first: "output", second: "output"},
		{name: "normalized equal", first: "dir/../output", second: "output"},
		{name: "prefix", first: ".ralphex", second: ".ralphex/config"},
		{name: "reverse prefix", first: ".ralphex/config", second: ".ralphex"},
		{name: "case equal", first: "AGENTS.md", second: "agents.md"},
		{name: "case prefix", first: "Prompts", second: "prompts/task.md"},
		{name: "file and link", first: "CLAUDE.md", second: "CLAUDE.md", symlink: true},
		{name: "link parent", first: "agent", second: "agent/CLAUDE.md", symlink: true},
		{name: "link and file", first: "CLAUDE.md", second: "CLAUDE.md", firstLink: true},
		{name: "two links", first: "link", second: "link/child", symlink: true, firstLink: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			firstManifest := manifest.File{
				Render: []manifest.RenderEntry{{From: "source", To: tt.first}},
			}
			if tt.firstLink {
				firstManifest.Render[0].To = "first-target"
				firstManifest.Symlinks = []manifest.SymlinkEntry{{Link: tt.first, To: "first-target"}}
			}
			createPack(t, root, "first", firstManifest, map[string]string{"source": "first"})
			m := manifest.File{Render: []manifest.RenderEntry{{From: "source", To: tt.second}}}
			if tt.symlink {
				m.Render[0].To = "target"
				m.Symlinks = []manifest.SymlinkEntry{{Link: tt.second, To: "target"}}
			}
			createPack(t, root, "second", m, map[string]string{"source": "second"})
			before := snapshot(t, root)
			planned, err := workspace.Plan(root, []string{"first", "second"}, packcommon.TemplateData{})
			requireError(t, err, "collides")
			if !strings.Contains(err.Error(), "first") || !strings.Contains(err.Error(), "second") {
				t.Errorf("collision should name both packs: %v", err)
			}
			if planned != nil {
				t.Fatal("error returned a partial plan")
			}
			assertUnchanged(t, root, before)
		})
	}
}

func TestPlanErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(*testing.T, string, string)
		want  string
	}{
		{name: "missing source", setup: func(t *testing.T, _, dir string) {
			if err := os.Remove(filepath.Join(dir, "source.tmpl")); err != nil {
				t.Fatal(err)
			}
		}, want: "not found"},
		{name: "missing key", setup: func(t *testing.T, _, dir string) {
			writeFile(t, filepath.Join(dir, "source.tmpl"), "{{ .Cfg.Raw.absent }}")
		}, want: "map has no entry for key"},
		{name: "parse error", setup: func(t *testing.T, _, dir string) {
			writeFile(t, filepath.Join(dir, "source.tmpl"), "{{")
		}, want: "parse template"},
		{name: "destination directory", setup: func(t *testing.T, root, _ string) {
			mkdir(t, filepath.Join(root, "out", "file"))
		}, want: "is a directory"},
		{name: "destination symlink", setup: func(t *testing.T, root, _ string) {
			mkdir(t, filepath.Join(root, "out"))
			symlink(t, "missing", filepath.Join(root, "out", "file"))
		}, want: "is a symlink"},
		{name: "destination parent symlink", setup: func(t *testing.T, root, _ string) {
			symlink(t, t.TempDir(), filepath.Join(root, "out"))
		}, want: "symlink"},
		{name: "destination parent file", setup: func(t *testing.T, root, _ string) {
			writeFile(t, filepath.Join(root, "out"), "existing")
		}, want: "not a directory"},
		{name: "source symlink", setup: func(t *testing.T, _, dir string) {
			if err := os.Remove(filepath.Join(dir, "source.tmpl")); err != nil {
				t.Fatal(err)
			}
			symlink(t, "missing", filepath.Join(dir, "source.tmpl"))
		}, want: "symlink"},
		{name: "bad override", setup: func(t *testing.T, _, dir string) {
			mkdir(t, filepath.Join(dir+".local", "source.tmpl"))
		}, want: "not a regular file"},
		{name: "unknown manifest field", setup: func(t *testing.T, _, dir string) {
			writeFile(t, filepath.Join(dir, "manifest.yml"), "render: []\nunknown: true\n")
		}, want: "unknown field"},
		{name: "missing manifest", setup: func(t *testing.T, _, dir string) {
			if err := os.Remove(filepath.Join(dir, "manifest.yml")); err != nil {
				t.Fatal(err)
			}
		}, want: "manifest file not found"},
		{name: "manifest symlink", setup: func(t *testing.T, _, dir string) {
			if err := os.Remove(filepath.Join(dir, "manifest.yml")); err != nil {
				t.Fatal(err)
			}
			symlink(t, "source.tmpl", filepath.Join(dir, "manifest.yml"))
		}, want: "symlink"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			createPack(t, root, "first", manifest.File{
				Render: []manifest.RenderEntry{{From: "source", To: "first/output"}},
			}, map[string]string{"source": "first"})
			writeFile(t, filepath.Join(root, "first", "output"), "keep existing bytes")
			dir := createPack(t, root, "second", manifest.File{
				Render: []manifest.RenderEntry{{From: "source.tmpl", To: "out/file"}},
			}, map[string]string{"source.tmpl": "{{ .Project.Name }}"})
			tt.setup(t, root, dir)
			before := snapshot(t, root)
			planned, err := workspace.Plan(root, []string{"first", "second"}, packcommon.TemplateData{
				Cfg: &config.DweConfig{Raw: map[string]any{}},
			})
			requireError(t, err, tt.want)
			if planned != nil {
				t.Fatal("error returned a partial plan")
			}
			assertUnchanged(t, root, before)
		})
	}
}

func TestPlanSymlinkDestinations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(*testing.T, string)
		want  string
	}{
		{name: "missing parent"},
		{name: "existing link", setup: func(t *testing.T, root string) {
			mkdir(t, filepath.Join(root, "links"))
			symlink(t, "../old-target", filepath.Join(root, "links", "CLAUDE.md"))
		}},
		{name: "regular file", setup: func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, "links", "CLAUDE.md"), "keep")
		}, want: "non-symlink"},
		{name: "directory", setup: func(t *testing.T, root string) {
			mkdir(t, filepath.Join(root, "links", "CLAUDE.md"))
		}, want: "non-symlink"},
		{name: "parent symlink", setup: func(t *testing.T, root string) {
			symlink(t, t.TempDir(), filepath.Join(root, "links"))
		}, want: "symlink parent dir contains symlink"},
		{name: "parent file", setup: func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, "links"), "keep")
		}, want: "not a directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			createPack(t, root, "agents", manifest.File{
				Render:   []manifest.RenderEntry{{From: "source", To: "AGENTS.md"}},
				Symlinks: []manifest.SymlinkEntry{{Link: "links/CLAUDE.md", To: "AGENTS.md"}},
			}, map[string]string{"source": "agents"})
			if tt.setup != nil {
				tt.setup(t, root)
			}
			before := snapshot(t, root)
			_, err := workspace.Plan(root, []string{"agents"}, packcommon.TemplateData{})
			if tt.want != "" {
				requireError(t, err, tt.want)
			} else if err != nil {
				t.Fatal(err)
			}
			assertUnchanged(t, root, before)
		})
	}
}

func TestPlanShapeAndSelection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, manifest string
		names          []string
		want           string
	}{
		{name: "missing pack", names: []string{"missing"}, want: "stat workspace pack"},
		{name: "duplicate pack", names: []string{"test", "test"}, want: "duplicated"},
		{name: "invalid pack", names: []string{"../test"}, want: "identifier-safe"},
		{name: "empty list", names: []string{}},
		{name: "empty manifest", manifest: "# empty", want: "manifest is empty"},
		{name: "escape", manifest: "render: [{from: source, to: ../outside}]", want: "escapes dest root"},
		{name: "absolute", manifest: "render: [{from: source, to: /outside}]", want: "must be relative"},
		{name: "prefix in same pack", manifest: "render: [{from: source, to: file}, {from: source, to: file/child}]", want: "collides"},
		{name: "link missing target", manifest: "render: [{from: source, to: file}]\nsymlinks: [{link: link, to: other}]", want: "does not reference"},
		{name: "distinct component", manifest: "render: [{from: source, to: file}, {from: source, to: filename/child}]"},
		{name: "unprotected prefix", manifest: "render: [{from: source, to: workspace-backup}, {from: source, to: .env.example}]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "workspace", "templates", "workspace", "test")
			writeFile(t, filepath.Join(dir, "source"), "content")
			writeFile(t, filepath.Join(dir, "manifest.yml"), tt.manifest)
			names := tt.names
			if names == nil {
				names = []string{"test"}
			}
			before := snapshot(t, root)
			_, err := workspace.Plan(root, names, packcommon.TemplateData{})
			if tt.want != "" {
				requireError(t, err, tt.want)
			} else if err != nil {
				t.Fatal(err)
			}
			assertUnchanged(t, root, before)
		})
	}
}

func TestRender(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	createPack(t, root, "ralphex", manifest.File{
		Render: []manifest.RenderEntry{
			{From: "config.tmpl", To: ".ralphex/config"},
			{From: "task.md", To: ".ralphex/prompts/task.md"},
			{From: "ws-git", To: ".ralphex/scripts/ws-git"},
		},
	}, map[string]string{
		"config.tmpl": "canonical {{ .Project.Name }}",
		"task.md":     "{{PLAN_FILE}}\r\n{{ .Project.Name }}\x00\xff",
		"ws-git":      "#!/bin/sh\n",
	})
	override := filepath.Join(root, "workspace", "templates", "workspace", "ralphex.local", "config.tmpl")
	writeFile(t, override, "override {{ .Project.Name }}")
	script := filepath.Join(root, "workspace", "templates", "workspace", "ralphex", "ws-git")
	chmod(t, script, 0o755)
	writeFile(t, filepath.Join(root, ".ralphex", "config"), "old config")
	createPack(t, root, "agents", manifest.File{
		Render:   []manifest.RenderEntry{{From: "AGENTS.md.tmpl", To: "AGENTS.md"}},
		Symlinks: []manifest.SymlinkEntry{{Link: "agent/CLAUDE.md", To: "AGENTS.md"}},
	}, map[string]string{"AGENTS.md.tmpl": "agents for {{ .Project.Name }}"})
	data := packcommon.TemplateData{Project: config.ProjectConfig{Name: "demo"}}
	results, err := workspace.Render(root, []string{"ralphex", "agents"}, data)
	if err != nil {
		t.Fatal(err)
	}
	want := []workspace.RenderResult{
		{
			Name:         "ralphex",
			Files:        []string{".ralphex/config", ".ralphex/prompts/task.md", ".ralphex/scripts/ws-git"},
			OverrideHits: []string{".ralphex/config"},
			Symlinks:     []string{},
		},
		{
			Name:         "agents",
			Files:        []string{"AGENTS.md"},
			OverrideHits: []string{},
			Symlinks:     []string{"agent/CLAUDE.md"},
		},
	}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("Render results = %+v, want %+v", results, want)
	}
	for _, file := range []struct {
		path, content string
		mode          os.FileMode
	}{
		{path: ".ralphex/config", content: "override demo", mode: 0o644},
		{path: ".ralphex/prompts/task.md", content: "{{PLAN_FILE}}\r\n{{ .Project.Name }}\x00\xff", mode: 0o644},
		{path: ".ralphex/scripts/ws-git", content: "#!/bin/sh\n", mode: 0o755},
		{path: "AGENTS.md", content: "agents for demo", mode: 0o644},
	} {
		path := filepath.Join(root, file.path)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != file.content {
			t.Errorf("%s = %q, want %q", file.path, got, file.content)
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != file.mode {
			t.Errorf("%s mode = %o, want %o", file.path, fi.Mode().Perm(), file.mode)
		}
	}
	link := filepath.Join(root, "agent", "CLAUDE.md")
	target, err := os.Readlink(link)
	if err != nil || target != "../AGENTS.md" {
		t.Fatalf("symlink = %q, %v; want ../AGENTS.md", target, err)
	}
	before := snapshot(t, root)
	if _, err := workspace.Render(root, []string{"ralphex", "agents"}, data); err != nil {
		t.Fatal(err)
	}
	assertUnchanged(t, root, before)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	symlink(t, "../old-target", link)
	if _, err := workspace.Render(root, []string{"agents"}, data); err != nil {
		t.Fatal(err)
	}
	target, err = os.Readlink(link)
	if err != nil || target != "../AGENTS.md" {
		t.Fatalf("retargeted symlink = %q, %v; want ../AGENTS.md", target, err)
	}
}

func TestRenderModeConverges(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name                 string
		sourceMode, destMode os.FileMode
		want                 os.FileMode
		override             bool
	}{
		{name: "add executable bit", sourceMode: 0o751, destMode: 0o644, want: 0o755},
		{name: "remove executable bit", sourceMode: 0o640, destMode: 0o755, want: 0o644},
		{name: "override adds executable bit", sourceMode: 0o700, destMode: 0o644, want: 0o755, override: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := createPack(t, root, "test", manifest.File{
				Render: []manifest.RenderEntry{{From: "script", To: "scripts/run"}},
			}, map[string]string{"script": "#!/bin/sh\n"})
			source := filepath.Join(dir, "script")
			if tt.override {
				source = filepath.Join(root, "workspace", "templates", "workspace", "test.local", "script")
				writeFile(t, source, "override script")
			}
			chmod(t, source, tt.sourceMode)
			dest := filepath.Join(root, "scripts", "run")
			writeFile(t, dest, "old script")
			for range 2 {
				chmod(t, dest, tt.destMode)
				if _, err := workspace.Render(root, []string{"test"}, packcommon.TemplateData{}); err != nil {
					t.Fatal(err)
				}
				fi, err := os.Stat(dest)
				if err != nil {
					t.Fatal(err)
				}
				if fi.Mode().Perm() != tt.want {
					t.Fatalf("mode = %o, want %o", fi.Mode().Perm(), tt.want)
				}
			}
		})
	}
}

func TestRenderPlanningFailureWritesNothing(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, content, dest, want string
		directory                 bool
	}{
		{name: "bad template", content: "{{ .Cfg.Raw.absent }}", dest: "second/output", want: "map has no entry"},
		{name: "protected path", content: "second", dest: ".GIT/hooks", want: "protected"},
		{name: "destination directory", content: "second", dest: "second/output", want: "is a directory", directory: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			createPack(t, root, "first", manifest.File{
				Render: []manifest.RenderEntry{
					{From: "source", To: "first/existing"},
					{From: "source", To: "new/output"},
				},
				Symlinks: []manifest.SymlinkEntry{{Link: "links/agents", To: "first/existing"}},
			}, map[string]string{"source": "replacement"})
			existing := filepath.Join(root, "first", "existing")
			writeFile(t, existing, "keep existing bytes")
			chmod(t, existing, 0o751)
			mkdir(t, filepath.Join(root, "links"))
			symlink(t, "../old-target", filepath.Join(root, "links", "agents"))
			createPack(t, root, "second", manifest.File{
				Render: []manifest.RenderEntry{{From: "source.tmpl", To: tt.dest}},
			}, map[string]string{"source.tmpl": tt.content})
			if tt.directory {
				mkdir(t, filepath.Join(root, tt.dest))
			}
			before := snapshot(t, root)
			results, err := workspace.Render(root, []string{"first", "second"}, packcommon.TemplateData{
				Cfg: &config.DweConfig{Raw: map[string]any{}},
			})
			requireError(t, err, tt.want)
			if !strings.Contains(err.Error(), "second") {
				t.Errorf("error should name failing pack: %v", err)
			}
			if results != nil {
				t.Fatal("failure returned partial results")
			}
			assertUnchanged(t, root, before)
		})
	}
}

func TestRenderEmptySelection(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	before := snapshot(t, root)
	results, err := workspace.Render(root, nil, packcommon.TemplateData{})
	if err != nil || len(results) != 0 {
		t.Fatalf("Render = %+v, %v; want no results", results, err)
	}
	assertUnchanged(t, root, before)
}

func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func createPack(t *testing.T, root, name string, m manifest.File, sources map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, "workspace", "templates", "workspace", name)
	data, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "manifest.yml"), string(data))
	for path, content := range sources {
		writeFile(t, filepath.Join(dir, path), content)
	}
	return dir
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func requireError(t *testing.T, err error, substring string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), substring) {
		t.Fatalf("error = %v, want %q", err, substring)
	}
}

type entrySnapshot struct {
	mode os.FileMode
	data string
}

func snapshot(t *testing.T, root string) map[string]entrySnapshot {
	t.Helper()
	out := make(map[string]entrySnapshot)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := entry.Info()
		if err != nil {
			return err
		}
		item := entrySnapshot{mode: fi.Mode()}
		if fi.Mode()&os.ModeSymlink != 0 {
			item.data, err = os.Readlink(path)
		} else if fi.Mode().IsRegular() {
			var data []byte
			data, err = os.ReadFile(path)
			item.data = string(data)
		}
		out[path] = item
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertUnchanged(t *testing.T, root string, before map[string]entrySnapshot) {
	t.Helper()
	if after := snapshot(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("planning changed project files, directories, permissions or symlinks")
	}
}
