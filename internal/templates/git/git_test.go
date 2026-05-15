package git

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/templates/manifest"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ptrBool(b bool) *bool { v := b; return &v } //nolint:newexpr

func mkPack(t *testing.T, root, packName string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		writeFile(t, filepath.Join(root, "devbox", "templates", "git", packName, rel), content)
	}
}

func TestResolveTemplatePack(t *testing.T) {
	t.Run("explicit hit", func(t *testing.T) {
		root := t.TempDir()
		mkPack(t, root, "myhooks", map[string]string{"manifest.yml": "render: []\n"})
		svc := config.ServiceConfig{Git: config.ServiceGitHooksConfig{Template: "myhooks"}}
		packDir, packName, err := ResolveTemplatePack(svc, root, "main")
		if err != nil {
			t.Fatal(err)
		}
		if packName != "myhooks" {
			t.Errorf("packName=%q want myhooks", packName)
		}
		if !strings.HasSuffix(packDir, filepath.Join("git", "myhooks")) {
			t.Errorf("packDir=%q", packDir)
		}
	})
	t.Run("explicit missing hard error", func(t *testing.T) {
		root := t.TempDir()
		svc := config.ServiceConfig{Git: config.ServiceGitHooksConfig{Template: "nope"}}
		_, _, err := ResolveTemplatePack(svc, root, "main")
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("implicit service-name fallback", func(t *testing.T) {
		root := t.TempDir()
		mkPack(t, root, "main", map[string]string{"manifest.yml": ""})
		packDir, packName, err := ResolveTemplatePack(config.ServiceConfig{}, root, "main")
		if err != nil {
			t.Fatal(err)
		}
		if packName != "main" {
			t.Errorf("packName=%q", packName)
		}
		if filepath.Base(packDir) != "main" {
			t.Errorf("packDir=%q", packDir)
		}
	})
	t.Run("implicit default fallback", func(t *testing.T) {
		root := t.TempDir()
		mkPack(t, root, "default", map[string]string{"manifest.yml": ""})
		_, packName, err := ResolveTemplatePack(config.ServiceConfig{}, root, "main")
		if err != nil {
			t.Fatal(err)
		}
		if packName != "default" {
			t.Errorf("packName=%q", packName)
		}
	})
	t.Run("none found", func(t *testing.T) {
		root := t.TempDir()
		_, _, err := ResolveTemplatePack(config.ServiceConfig{}, root, "main")
		if err == nil {
			t.Fatal("expected error")
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("expected ErrNotExist, got %v", err)
		}
	})
}

func TestSelectServices(t *testing.T) {
	t.Run("disabled and git-disabled dropped", func(t *testing.T) {
		svcs := map[string]config.ServiceConfig{
			"a": {Enabled: false, Type: "app", Dir: "services/a"},
			"b": {Enabled: true, Type: "app", Dir: "services/b", Git: config.ServiceGitHooksConfig{Enabled: ptrBool(false)}},
			"c": {Enabled: true, Type: "app", Dir: "services/c"},
		}
		selected, skipped := SelectServices(svcs)
		if len(selected) != 1 || selected[0] != "c" {
			t.Errorf("selected=%v", selected)
		}
		if len(skipped) != 2 {
			t.Errorf("skipped=%v", skipped)
		}
	})
	t.Run("empty dir dropped", func(t *testing.T) {
		svcs := map[string]config.ServiceConfig{
			"a": {Enabled: true, Type: "app", Dir: ""},
		}
		selected, _ := SelectServices(svcs)
		if len(selected) != 0 {
			t.Errorf("selected=%v", selected)
		}
	})
	t.Run("deepest extends wins", func(t *testing.T) {
		svcs := map[string]config.ServiceConfig{
			"parent": {Enabled: true, Type: "app", Dir: "services/main"},
			"child":  {Enabled: true, Type: "app", Dir: "services/main", Extends: "parent"},
		}
		selected, _ := SelectServices(svcs)
		if len(selected) != 1 || selected[0] != "child" {
			t.Errorf("selected=%v", selected)
		}
	})
}

func TestPrepareHub(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "services", "main"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("valid", func(t *testing.T) {
		absHub, err := PrepareHub(root, "main", config.ServiceConfig{Dir: "services/main"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(absHub, filepath.Join("services", "main")) {
			t.Errorf("absHub=%q", absHub)
		}
	})
	t.Run("escaping rejected", func(t *testing.T) {
		_, err := PrepareHub(root, "main", config.ServiceConfig{Dir: "../outside"})
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("empty dir rejected", func(t *testing.T) {
		_, err := PrepareHub(root, "main", config.ServiceConfig{Dir: ""})
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("symlink hub rejected", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink semantics differ on windows")
		}
		// Create a symlink at services/link → services/main
		linkPath := filepath.Join(root, "services", "link")
		if err := os.Symlink(filepath.Join(root, "services", "main"), linkPath); err != nil {
			t.Fatal(err)
		}
		_, err := PrepareHub(root, "link", config.ServiceConfig{Dir: "services/link"})
		if err == nil {
			t.Fatal("expected symlink rejection")
		}
	})
}

func TestResolveGitHooksDir(t *testing.T) {
	t.Run("dir ok", func(t *testing.T) {
		hub := t.TempDir()
		if err := os.MkdirAll(filepath.Join(hub, "src", ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		hooks, status, err := ResolveGitHooksDir(hub)
		if err != nil {
			t.Fatal(err)
		}
		if status != DirOK {
			t.Errorf("status=%v", status)
		}
		if !strings.HasSuffix(hooks, filepath.Join(".git", "hooks")) {
			t.Errorf("hooks=%q", hooks)
		}
	})
	t.Run("missing", func(t *testing.T) {
		hub := t.TempDir()
		_, status, err := ResolveGitHooksDir(hub)
		if err != nil {
			t.Fatal(err)
		}
		if status != DirMissing {
			t.Errorf("status=%v", status)
		}
	})
	t.Run("worktree", func(t *testing.T) {
		hub := t.TempDir()
		if err := os.MkdirAll(filepath.Join(hub, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(hub, "src", ".git"), "gitdir: /tmp/somewhere\n")
		_, status, err := ResolveGitHooksDir(hub)
		if err != nil {
			t.Fatal(err)
		}
		if status != DirWorktree {
			t.Errorf("status=%v", status)
		}
	})
	t.Run("symlinked .git rejected", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip()
		}
		hub := t.TempDir()
		if err := os.MkdirAll(filepath.Join(hub, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		target := t.TempDir()
		if err := os.Symlink(target, filepath.Join(hub, "src", ".git")); err != nil {
			t.Fatal(err)
		}
		_, _, err := ResolveGitHooksDir(hub)
		if err == nil {
			t.Fatal("expected symlink rejection")
		}
	})
}

func TestLoadManifest(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "manifest.yml"),
			"render:\n  - {from: pre-commit.tmpl, to: pre-commit}\n")
		m, err := LoadManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Render) != 1 {
			t.Errorf("len=%d", len(m.Render))
		}
	})
	t.Run("missing", func(t *testing.T) {
		_, err := LoadManifest(t.TempDir())
		if err == nil {
			t.Fatal("expected error")
		}
		if !errors.Is(err, manifest.ErrManifestMissing) {
			t.Errorf("expected ErrManifestMissing, got %v", err)
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("expected os.ErrNotExist, got %v", err)
		}
	})
	t.Run("unknown field strict", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "manifest.yml"),
			"render:\n  - {from: a.tmpl, to: a}\nbogus: 1\n")
		_, err := LoadManifest(dir)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestValidateManifest(t *testing.T) {
	root := t.TempDir()
	// canonical pack with pre-commit.tmpl
	mkPack(t, root, "default", map[string]string{
		"manifest.yml":     "render:\n  - {from: pre-commit.tmpl, to: pre-commit}\n",
		"pre-commit.tmpl":  "#!/bin/sh\n",
		"prepare-msg.tmpl": "#!/bin/sh\n",
		"sub.tmpl":         "#!/bin/sh\n",
	})
	hooksDir := filepath.Join(root, "services", "main", "src", ".git", "hooks")

	t.Run("valid", func(t *testing.T) {
		m, err := LoadManifest(filepath.Join(root, "devbox", "templates", "git", "default"))
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateManifest(m, root, "default", hooksDir); err != nil {
			t.Fatalf("unexpected: %v", err)
		}
	})
	t.Run("empty manifest rejected", func(t *testing.T) {
		m := &manifest.File{}
		if err := ValidateManifest(m, root, "default", hooksDir); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("duplicate to rejected", func(t *testing.T) {
		m := &manifest.File{Render: []manifest.RenderEntry{
			{From: "pre-commit.tmpl", To: "pre-commit"},
			{From: "prepare-msg.tmpl", To: "pre-commit"},
		}}
		err := ValidateManifest(m, root, "default", hooksDir)
		if err == nil || !strings.Contains(err.Error(), "duplicat") {
			t.Errorf("expected dup error: %v", err)
		}
	})
	t.Run("to with slash rejected", func(t *testing.T) {
		m := &manifest.File{Render: []manifest.RenderEntry{
			{From: "sub.tmpl", To: "sub/hook"},
		}}
		err := ValidateManifest(m, root, "default", hooksDir)
		if err == nil || !strings.Contains(err.Error(), "basename") {
			t.Errorf("expected basename error: %v", err)
		}
	})
	t.Run("to with traversal rejected", func(t *testing.T) {
		m := &manifest.File{Render: []manifest.RenderEntry{
			{From: "sub.tmpl", To: ".."},
		}}
		// `..` won't pass containment in ValidateShape — but git-specific should also flag it.
		err := ValidateManifest(m, root, "default", hooksDir)
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("symlinks rejected", func(t *testing.T) {
		m := &manifest.File{
			Render:   []manifest.RenderEntry{{From: "pre-commit.tmpl", To: "pre-commit"}},
			Symlinks: []manifest.SymlinkEntry{{Link: "x", To: "pre-commit"}},
		}
		err := ValidateManifest(m, root, "default", hooksDir)
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Errorf("expected symlinks-rejected error: %v", err)
		}
	})
	t.Run("missing from rejected", func(t *testing.T) {
		m := &manifest.File{Render: []manifest.RenderEntry{
			{From: "absent.tmpl", To: "pre-commit"},
		}}
		err := ValidateManifest(m, root, "default", hooksDir)
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("from only in override passes", func(t *testing.T) {
		// Pack with a manifest entry whose canonical from is absent but the
		// override directory has it.
		root2 := t.TempDir()
		mkPack(t, root2, "default", map[string]string{
			"manifest.yml": "render:\n  - {from: pre-commit.tmpl, to: pre-commit}\n",
		})
		writeFile(t, filepath.Join(root2, "devbox", "templates", "git", "default.local", "pre-commit.tmpl"), "#!/bin/sh\n")
		hooksDir2 := filepath.Join(root2, "services", "main", "src", ".git", "hooks")
		m, err := LoadManifest(filepath.Join(root2, "devbox", "templates", "git", "default"))
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateManifest(m, root2, "default", hooksDir2); err != nil {
			t.Fatalf("override should satisfy: %v", err)
		}
	})
}

func TestRenderHooks(t *testing.T) {
	setup := func(t *testing.T) (root, hub, hooks string, cfg *config.DevboxConfig, m *manifest.File) {
		t.Helper()
		root = t.TempDir()
		mkPack(t, root, "default", map[string]string{
			"manifest.yml":    "render:\n  - {from: pre-commit.tmpl, to: pre-commit}\n",
			"pre-commit.tmpl": "#!/bin/sh\necho {{.Service}}\n",
		})
		hub = filepath.Join(root, "services", "main")
		if err := os.MkdirAll(filepath.Join(hub, "src", ".git", "hooks"), 0o755); err != nil {
			t.Fatal(err)
		}
		hooks = filepath.Join(hub, "src", ".git", "hooks")
		cfg = &config.DevboxConfig{}
		var err error
		m, err = LoadManifest(filepath.Join(root, "devbox", "templates", "git", "default"))
		if err != nil {
			t.Fatal(err)
		}
		return
	}

	t.Run("golden", func(t *testing.T) {
		root, hub, hooks, cfg, m := setup(t)
		ctx := Context{
			ProjectRoot: root, Cfg: cfg, Service: "main",
			ServiceCfg: config.ServiceConfig{Dir: "services/main"},
			PackName:   "default", Manifest: m,
			HooksDir: hooks, HubDir: hub,
		}
		if err := RenderHooks(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(hooks, "pre-commit"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), "echo main") {
			t.Errorf("content=%q", got)
		}
		fi, err := os.Stat(filepath.Join(hooks, "pre-commit"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o755 {
			t.Errorf("mode=%v", fi.Mode().Perm())
		}
	})

	t.Run("override file used", func(t *testing.T) {
		root, hub, hooks, cfg, m := setup(t)
		writeFile(t, filepath.Join(root, "devbox", "templates", "git", "default.local", "pre-commit.tmpl"),
			"#!/bin/sh\necho OVERRIDE-{{.Service}}\n")
		ctx := Context{
			ProjectRoot: root, Cfg: cfg, Service: "main",
			ServiceCfg: config.ServiceConfig{Dir: "services/main"},
			PackName:   "default", Manifest: m,
			HooksDir: hooks, HubDir: hub,
		}
		if err := RenderHooks(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(hooks, "pre-commit"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), "OVERRIDE-main") {
			t.Errorf("override not used: %q", got)
		}
	})

	t.Run("missingkey error", func(t *testing.T) {
		root, hub, hooks, cfg, _ := setup(t)
		// Replace template with one that references a missing key.
		writeFile(t, filepath.Join(root, "devbox", "templates", "git", "default", "pre-commit.tmpl"),
			"#!/bin/sh\necho {{.Nope}}\n")
		m, err := LoadManifest(filepath.Join(root, "devbox", "templates", "git", "default"))
		if err != nil {
			t.Fatal(err)
		}
		ctx := Context{
			ProjectRoot: root, Cfg: cfg, Service: "main",
			ServiceCfg: config.ServiceConfig{Dir: "services/main"},
			PackName:   "default", Manifest: m,
			HooksDir: hooks, HubDir: hub,
		}
		if err := RenderHooks(ctx); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("destination symlink rejected", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip()
		}
		root, hub, hooks, cfg, m := setup(t)
		target := filepath.Join(t.TempDir(), "evil")
		writeFile(t, target, "evil")
		if err := os.Symlink(target, filepath.Join(hooks, "pre-commit")); err != nil {
			t.Fatal(err)
		}
		ctx := Context{
			ProjectRoot: root, Cfg: cfg, Service: "main",
			ServiceCfg: config.ServiceConfig{Dir: "services/main"},
			PackName:   "default", Manifest: m,
			HooksDir: hooks, HubDir: hub,
		}
		err := RenderHooks(ctx)
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("expected symlink rejection: %v", err)
		}
		// Target must not have been overwritten
		got, _ := os.ReadFile(target)
		if string(got) != "evil" {
			t.Errorf("target was followed: %q", got)
		}
	})

	t.Run("chmod on rewrite", func(t *testing.T) {
		root, hub, hooks, cfg, m := setup(t)
		// Pre-create the hook as 0644.
		if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
		ctx := Context{
			ProjectRoot: root, Cfg: cfg, Service: "main",
			ServiceCfg: config.ServiceConfig{Dir: "services/main"},
			PackName:   "default", Manifest: m,
			HooksDir: hooks, HubDir: hub,
		}
		if err := RenderHooks(ctx); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(filepath.Join(hooks, "pre-commit"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o755 {
			t.Errorf("mode after rewrite=%v want 0755", fi.Mode().Perm())
		}
	})

	t.Run("multiple entries closed cleanly", func(t *testing.T) {
		// File-handle accounting: render many entries and ensure subsequent
		// writes still succeed (no descriptor exhaustion via leaked defers).
		root := t.TempDir()
		var lines []string
		entries := map[string]string{"manifest.yml": ""}
		var manifestYaml strings.Builder
		manifestYaml.WriteString("render:\n")
		for i := range 20 {
			name := "hook" + string(rune('a'+i)) + ".tmpl"
			to := "hook" + string(rune('a'+i))
			entries[name] = "#!/bin/sh\n"
			manifestYaml.WriteString("  - {from: " + name + ", to: " + to + "}\n")
			lines = append(lines, to)
		}
		entries["manifest.yml"] = manifestYaml.String()
		mkPack(t, root, "default", entries)
		hub := filepath.Join(root, "services", "main")
		hooks := filepath.Join(hub, "src", ".git", "hooks")
		if err := os.MkdirAll(hooks, 0o755); err != nil {
			t.Fatal(err)
		}
		m, err := LoadManifest(filepath.Join(root, "devbox", "templates", "git", "default"))
		if err != nil {
			t.Fatal(err)
		}
		ctx := Context{
			ProjectRoot: root, Cfg: &config.DevboxConfig{}, Service: "main",
			ServiceCfg: config.ServiceConfig{Dir: "services/main"},
			PackName:   "default", Manifest: m,
			HooksDir: hooks, HubDir: hub,
		}
		if err := RenderHooks(ctx); err != nil {
			t.Fatal(err)
		}
		for _, name := range lines {
			if _, err := os.Stat(filepath.Join(hooks, name)); err != nil {
				t.Errorf("missing %s: %v", name, err)
			}
		}
	})
}
