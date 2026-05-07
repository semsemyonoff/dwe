package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

// Minimal inline templates used by renderIDETemplateFile unit tests.
const minimalDevcontainerTpl = `{"name":"{{ .Project.Name }}","service":"{{ .ServiceCfg.Container }}","workspaceFolder":"{{ .ServiceCfg.DirInternal }}","forwardPorts":[{{ .Runtime.Ports.App }}]}`
const minimalVscodeLaunchTpl = `{"type":"php","pathMappings":{"{{ .ServiceCfg.WorkDirInternal }}":"${workspaceFolder}/src"}}`
const minimalVscodeSettingsTpl = `{"php.validate.executablePath":"/usr/local/bin/php","editor.formatOnSave":true}`

// setupIDEPackTemplates writes an IDE template pack at <dir>/devbox/templates/ide/<packName>/
// and populates it with a directory structure of .tpl files.
func setupIDEPackTemplates(t *testing.T, dir, packName string, files map[string]string) {
	t.Helper()
	packDir := filepath.Join(dir, "devbox", "templates", "ide", packName)
	// Ensure pack directory exists even if empty
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create pack dir: %v", err)
	}
	for relPath, content := range files {
		path := filepath.Join(packDir, relPath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create template dir for %s: %v", relPath, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write template %s: %v", relPath, err)
		}
	}
}

// makeIDECfg returns a DevboxConfig configured for IDE rendering tests.
// (No longer sets IDE fields; those are now pack-driven.)
func makeIDECfg(name string) *config.DevboxConfig {
	return &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "laravel", Prefix: "devbox"},
		Services: map[string]config.ServiceConfig{
			name: {
				Type:            "app",
				Enabled:         true,
				Dir:             filepath.Join("services", name),
				Container:       "app-" + name,
				DirInternal:     "/workspace",
				WorkDirInternal: "/workspace/src",
			},
		},
		Runtime: config.RuntimeConfig{
			Ports: config.RuntimePorts{App: 80},
		},
		Raw: map[string]any{},
	}
}

// TestRenderIDETemplateFile_devcontainer verifies that the template
// substitutes project name, container, workspaceFolder, and port.
func TestRenderIDETemplateFile_devcontainer(t *testing.T) {
	data := ideTemplateData{
		Project: config.ProjectConfig{Name: "myapp"},
		Service: "main",
		ServiceCfg: config.ServiceConfig{
			Container:       "app-main",
			DirInternal:     "/workspace",
			WorkDirInternal: "/workspace/src",
		},
		Runtime: config.RuntimeConfig{
			Ports: config.RuntimePorts{App: 8080},
		},
	}
	projectRoot := t.TempDir()
	absRoot, _ := filepath.Abs(projectRoot)
	absDir, _ := filepath.Abs(projectRoot)

	// Write template file
	srcPath := filepath.Join(projectRoot, "devcontainer.json.tpl")
	if err := os.WriteFile(srcPath, []byte(minimalDevcontainerTpl), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	dest := filepath.Join(projectRoot, "devcontainer.json")
	if err := renderIDETemplateFile(srcPath, data, dest, absDir, absRoot); err != nil {
		t.Fatalf("renderIDETemplateFile: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	content := string(got)

	checks := []struct{ want, label string }{
		{`"name":"myapp"`, "project name"},
		{`"service":"app-main"`, "container name"},
		{`"workspaceFolder":"/workspace"`, "workspaceFolder (hub dir)"},
		{`8080`, "port"},
	}
	for _, c := range checks {
		if !strings.Contains(content, c.want) {
			t.Errorf("devcontainer.json missing %s (%q)\ngot:\n%s", c.label, c.want, content)
		}
	}
}

// TestRenderIDETemplateFile_createsParentDirs verifies parent directories are created.
func TestRenderIDETemplateFile_createsParentDirs(t *testing.T) {
	data := ideTemplateData{
		ServiceCfg: config.ServiceConfig{
			DirInternal:     "/workspace",
			WorkDirInternal: "/workspace/src",
		},
	}
	projectRoot := t.TempDir()
	absRoot, _ := filepath.Abs(projectRoot)
	absDir, _ := filepath.Abs(projectRoot)

	srcPath := filepath.Join(projectRoot, "template.tpl")
	if err := os.WriteFile(srcPath, []byte(minimalVscodeLaunchTpl), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	dest := filepath.Join(projectRoot, "nested", "deep", "file.json")
	if err := renderIDETemplateFile(srcPath, data, dest, absDir, absRoot); err != nil {
		t.Fatalf("renderIDETemplateFile should create parent dirs: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("expected file to exist at %s: %v", dest, err)
	}
}

// TestRenderIDETemplateFile_serviceDirContainment verifies that dest
// must be contained within absDir (the service directory).
func TestRenderIDETemplateFile_serviceDirContainment(t *testing.T) {
	data := ideTemplateData{}
	projectRoot := t.TempDir()
	absRoot, _ := filepath.Abs(projectRoot)
	svcDir := filepath.Join(projectRoot, "services", "main")
	absDir, _ := filepath.Abs(svcDir)

	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatalf("create svc dir: %v", err)
	}

	srcPath := filepath.Join(projectRoot, "template.tpl")
	if err := os.WriteFile(srcPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	// Try to write outside the service dir but inside the project root
	dest := filepath.Join(projectRoot, "services", "sibling", "file.json")

	err := renderIDETemplateFile(srcPath, data, dest, absDir, absRoot)
	if err == nil {
		t.Fatal("expected error when dest escapes service dir")
	}
	if !strings.Contains(err.Error(), "escapes service dir") {
		t.Errorf("expected escapes error, got: %v", err)
	}
}

// TestRenderIDETemplateFile_siblingPrefixAttack verifies that a naive
// HasPrefix check would fail (main2 has prefix main).
func TestRenderIDETemplateFile_siblingPrefixAttack(t *testing.T) {
	data := ideTemplateData{}
	projectRoot := t.TempDir()
	absRoot, _ := filepath.Abs(projectRoot)

	// Service dir for "main"
	mainDir := filepath.Join(projectRoot, "services", "main")
	absDir, _ := filepath.Abs(mainDir)
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatalf("create main dir: %v", err)
	}

	// Create sibling "main2" and try to escape into it
	main2Dir := filepath.Join(projectRoot, "services", "main2")
	if err := os.MkdirAll(main2Dir, 0o755); err != nil {
		t.Fatalf("create main2 dir: %v", err)
	}

	srcPath := filepath.Join(projectRoot, "template.tpl")
	if err := os.WriteFile(srcPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	// dest resolves to ../main2/leak, which is outside mainDir
	dest := filepath.Join(mainDir, "..", "main2", "leak")

	err := renderIDETemplateFile(srcPath, data, dest, absDir, absRoot)
	if err == nil {
		t.Fatal("expected error when dest escapes to sibling service dir")
	}
	if !strings.Contains(err.Error(), "escapes service dir") {
		t.Errorf("expected escapes error, got: %v", err)
	}
}

// TestRenderIDETemplateFile_symlinkDir verifies that a symlinked intermediate
// directory pointing outside the project root is rejected.
func TestRenderIDETemplateFile_symlinkDir(t *testing.T) {
	data := ideTemplateData{}
	projectRoot := t.TempDir()
	outside := t.TempDir()

	absRoot, _ := filepath.Abs(projectRoot)
	svcDir := filepath.Join(projectRoot, "services", "main")
	absDir, _ := filepath.Abs(svcDir)

	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// .devcontainer -> outside the project root
	if err := os.Symlink(outside, filepath.Join(svcDir, ".devcontainer")); err != nil {
		t.Fatal(err)
	}

	srcPath := filepath.Join(projectRoot, "template.tpl")
	if err := os.WriteFile(srcPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	dest := filepath.Join(svcDir, ".devcontainer", "devcontainer.json")
	err := renderIDETemplateFile(srcPath, data, dest, absDir, absRoot)
	if err == nil {
		t.Fatal("expected error when destination dir is a symlink outside project root")
	}
}

// TestRenderIDETemplateFile_symlinkFile verifies that a symlinked destination
// file is rejected even when the parent directory is safe.
func TestRenderIDETemplateFile_symlinkFile(t *testing.T) {
	data := ideTemplateData{}
	projectRoot := t.TempDir()
	outside := t.TempDir()

	absRoot, _ := filepath.Abs(projectRoot)
	svcDir := filepath.Join(projectRoot, "services", "main", ".devcontainer")
	absDir, _ := filepath.Abs(filepath.Join(projectRoot, "services", "main"))

	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// devcontainer.json -> file outside the project root
	target := filepath.Join(outside, "evil.json")
	if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(svcDir, "devcontainer.json")
	if err := os.Symlink(target, dest); err != nil {
		t.Fatal(err)
	}

	srcPath := filepath.Join(projectRoot, "template.tpl")
	if err := os.WriteFile(srcPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	err := renderIDETemplateFile(srcPath, data, dest, absDir, absRoot)
	if err == nil {
		t.Fatal("expected error when destination file is a symlink")
	}
}

// TestResolveIDETemplatePack_explicit verifies explicit pack resolution.
func TestResolveIDETemplatePack_explicit(t *testing.T) {
	projectRoot := t.TempDir()

	// Create packs
	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".devcontainer/devcontainer.json.tpl": "default-dc",
	})
	setupIDEPackTemplates(t, projectRoot, "custom", map[string]string{
		".devcontainer/devcontainer.json.tpl": "custom-dc",
	})

	tests := []struct {
		name      string
		template  string
		wantPack  string
		wantError bool
	}{
		{
			name:     "explicit pack resolves",
			template: "custom",
			wantPack: "custom",
		},
		{
			name:      "explicit pack missing - error",
			template:  "missing",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := config.ServiceConfig{
				Type:    "app",
				Enabled: true,
				Dir:     "services/main",
				IDE:     config.ServiceIDEConfig{Template: tt.template},
			}

			pack, err := resolveIDETemplatePack(svc, projectRoot, "main")
			if (err != nil) != tt.wantError {
				t.Errorf("want error=%v, got %v", tt.wantError, err)
			}
			if !tt.wantError {
				if !strings.Contains(pack, tt.wantPack) {
					t.Errorf("want pack containing %q, got %q", tt.wantPack, pack)
				}
			}
		})
	}
}

// TestResolveIDETemplatePack_implicit verifies implicit fallback chain.
func TestResolveIDETemplatePack_implicit(t *testing.T) {
	projectRoot := t.TempDir()

	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".devcontainer/devcontainer.json.tpl": "default-dc",
	})

	tests := []struct {
		name        string
		serviceName string
		wantPack    string
		wantError   bool
	}{
		{
			name:        "default pack used when service pack missing",
			serviceName: "unknown",
			wantPack:    "default",
		},
		{
			name:        "service pack wins over default",
			serviceName: "default", // there's a pack named "default"
			wantPack:    "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := config.ServiceConfig{
				Type:    "app",
				Enabled: true,
				Dir:     "services/main",
				IDE:     config.ServiceIDEConfig{}, // empty template = implicit
			}

			pack, err := resolveIDETemplatePack(svc, projectRoot, tt.serviceName)
			if (err != nil) != tt.wantError {
				t.Errorf("want error=%v, got %v", tt.wantError, err)
			}
			if !tt.wantError {
				if !strings.Contains(pack, tt.wantPack) {
					t.Errorf("want pack containing %q, got %q", tt.wantPack, pack)
				}
			}
		})
	}
}

// TestResolveIDETemplatePack_explicitStrictSemantics verifies that explicit
// template does not fall back to default even if default exists.
func TestResolveIDETemplatePack_explicitStrictSemantics(t *testing.T) {
	projectRoot := t.TempDir()

	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".devcontainer/devcontainer.json.tpl": "default-dc",
	})

	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		IDE:     config.ServiceIDEConfig{Template: "main-deubg"}, // typo
	}

	_, err := resolveIDETemplatePack(svc, projectRoot, "main")
	if err == nil {
		t.Fatal("expected error for typo in explicit template")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

// TestResolveIDETemplatePack_packIsSymlink verifies that pack roots
// that are symlinks are rejected.
func TestResolveIDETemplatePack_packIsSymlink(t *testing.T) {
	projectRoot := t.TempDir()
	realPack := filepath.Join(projectRoot, "real-pack")
	if err := os.MkdirAll(realPack, 0o755); err != nil {
		t.Fatalf("create real pack: %v", err)
	}

	// Create a symlink to the pack
	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create pack dir: %v", err)
	}
	symlinkPack := filepath.Join(packDir, "custom")
	if err := os.Symlink(realPack, symlinkPack); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		IDE:     config.ServiceIDEConfig{Template: "custom"},
	}

	_, err := resolveIDETemplatePack(svc, projectRoot, "main")
	if err == nil {
		t.Fatal("expected error when pack root is a symlink")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink error, got: %v", err)
	}
}

// TestWalkIDEPack_emptyPack verifies that an empty pack returns no entries.
func TestWalkIDEPack_emptyPack(t *testing.T) {
	projectRoot := t.TempDir()
	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{})

	packPath := filepath.Join(projectRoot, "devbox", "templates", "ide", "default")
	entries, err := walkIDEPack(packPath)
	if err != nil {
		t.Fatalf("walkIDEPack: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("empty pack should return no entries, got %d", len(entries))
	}
}

// TestWalkIDEPack_nested verifies that nested directory structures are handled.
func TestWalkIDEPack_nested(t *testing.T) {
	projectRoot := t.TempDir()
	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".devcontainer/devcontainer.json.tpl":    "dc",
		".vscode/settings.json.tpl":              "vs-settings",
		".vscode/launch.json.tpl":                "vs-launch",
		".idea/custom.xml.tpl":                   "idea",
		".zed/settings.json.tpl":                 "zed-settings",
	})

	packPath := filepath.Join(projectRoot, "devbox", "templates", "ide", "default")
	entries, err := walkIDEPack(packPath)
	if err != nil {
		t.Fatalf("walkIDEPack: %v", err)
	}

	if len(entries) != 5 {
		t.Errorf("want 5 entries, got %d", len(entries))
	}

	// Check ordering is lexicographic by RelPath
	wantOrder := []string{
		filepath.Join(".devcontainer", "devcontainer.json"),
		filepath.Join(".idea", "custom.xml"),
		filepath.Join(".vscode", "launch.json"),
		filepath.Join(".vscode", "settings.json"),
		filepath.Join(".zed", "settings.json"),
	}
	for i, want := range wantOrder {
		if i >= len(entries) {
			break
		}
		if entries[i].RelPath != want {
			t.Errorf("entry[%d]: want %q, got %q", i, want, entries[i].RelPath)
		}
	}
}

// TestWalkIDEPack_nonTplFilesSkipped verifies that non-.tpl files are silently skipped.
func TestWalkIDEPack_nonTplFilesSkipped(t *testing.T) {
	projectRoot := t.TempDir()
	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide", "default")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create pack dir: %v", err)
	}

	// Create both .tpl and non-.tpl files
	if err := os.WriteFile(filepath.Join(packDir, "file.tpl"), []byte("tpl"), 0o644); err != nil {
		t.Fatalf("write .tpl: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "file.txt"), []byte("txt"), 0o644); err != nil {
		t.Fatalf("write .txt: %v", err)
	}

	entries, err := walkIDEPack(packDir)
	if err != nil {
		t.Fatalf("walkIDEPack: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("want 1 entry (only .tpl), got %d", len(entries))
	}
	if entries[0].RelPath != "file" {
		t.Errorf("want RelPath=file, got %q", entries[0].RelPath)
	}
}

// TestWalkIDEPack_symlinkFileRejected verifies that a symlinked .tpl file
// is rejected (symlink check runs before suffix filter).
func TestWalkIDEPack_symlinkFileRejected(t *testing.T) {
	projectRoot := t.TempDir()
	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide", "default")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create pack dir: %v", err)
	}

	// Create a real file outside the pack
	outside := t.TempDir()
	realFile := filepath.Join(outside, "real.tpl")
	if err := os.WriteFile(realFile, []byte("real"), 0o644); err != nil {
		t.Fatalf("write real file: %v", err)
	}

	// Symlink it inside the pack
	symlinkFile := filepath.Join(packDir, "file.tpl")
	if err := os.Symlink(realFile, symlinkFile); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	_, err := walkIDEPack(packDir)
	if err == nil {
		t.Fatal("expected error when pack contains symlinked .tpl file")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink error, got: %v", err)
	}
}

// TestWalkIDEPack_bareTPLRejected verifies that a bare ".tpl" file is rejected.
func TestWalkIDEPack_bareTPLRejected(t *testing.T) {
	projectRoot := t.TempDir()
	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide", "default")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create pack dir: %v", err)
	}

	// Create a bare ".tpl" file at pack root
	if err := os.WriteFile(filepath.Join(packDir, ".tpl"), []byte("bad"), 0o644); err != nil {
		t.Fatalf("write .tpl: %v", err)
	}

	_, err := walkIDEPack(packDir)
	if err == nil {
		t.Fatal("expected error for bare .tpl file")
	}
	if !strings.Contains(err.Error(), "bare .tpl") {
		t.Errorf("expected 'bare .tpl' error, got: %v", err)
	}
}

// TestWalkIDEPack_nestedBareTplRejected verifies that "dir/.tpl" is rejected.
func TestWalkIDEPack_nestedBareTplRejected(t *testing.T) {
	projectRoot := t.TempDir()
	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide", "default")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create pack dir: %v", err)
	}

	// Create a nested ".tpl" file: subdir/.tpl
	subDir := filepath.Join(packDir, "subdir")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, ".tpl"), []byte("bad"), 0o644); err != nil {
		t.Fatalf("write nested .tpl: %v", err)
	}

	_, err := walkIDEPack(packDir)
	if err == nil {
		t.Fatal("expected error for nested .tpl file")
	}
	if !strings.Contains(err.Error(), "bare .tpl") {
		t.Errorf("expected 'bare .tpl' error, got: %v", err)
	}
}

// TestWalkIDEPack_absoluteSourcePath verifies that SourcePath is absolute
// even when packDir is relative.
func TestWalkIDEPack_absoluteSourcePath(t *testing.T) {
	projectRoot := t.TempDir()
	packName := "default"
	setupIDEPackTemplates(t, projectRoot, packName, map[string]string{
		"file.tpl": "content",
	})

	// Change to a temp directory and use relative path
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(oldCwd)

	if err := os.Chdir(projectRoot); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// Use relative path
	relativePack := filepath.Join("devbox", "templates", "ide", packName)
	entries, err := walkIDEPack(relativePack)
	if err != nil {
		t.Fatalf("walkIDEPack: %v", err)
	}

	if len(entries) == 0 {
		t.Fatalf("no entries found")
	}

	// Check that SourcePath is absolute
	if !filepath.IsAbs(entries[0].SourcePath) {
		t.Errorf("SourcePath should be absolute, got %q", entries[0].SourcePath)
	}
}

// TestRenderIDEConfigs_packResolution verifies that renderIDEConfigs
// resolves the pack and renders all entries.
func TestRenderIDEConfigs_packResolution(t *testing.T) {
	projectRoot := t.TempDir()

	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".devcontainer/devcontainer.json.tpl": minimalDevcontainerTpl,
		".vscode/settings.json.tpl":           minimalVscodeSettingsTpl,
	})

	cfg := makeIDECfg("main")
	svc := cfg.Services["main"]

	var buf strings.Builder
	w := render.NewWriter(&buf)

	if err := renderIDEConfigs(projectRoot, "main", svc, cfg, w); err != nil {
		t.Fatalf("renderIDEConfigs: %v", err)
	}

	// Check that both files were created
	devcontainerPath := filepath.Join(projectRoot, "services", "main", ".devcontainer", "devcontainer.json")
	if _, err := os.Stat(devcontainerPath); err != nil {
		t.Errorf("expected devcontainer.json to exist: %v", err)
	}

	settingsPath := filepath.Join(projectRoot, "services", "main", ".vscode", "settings.json")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Errorf("expected settings.json to exist: %v", err)
	}
}

// TestRenderIDEConfigs_emptyPack verifies that an empty pack produces no files.
func TestRenderIDEConfigs_emptyPack(t *testing.T) {
	projectRoot := t.TempDir()
	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{})

	cfg := makeIDECfg("main")
	svc := cfg.Services["main"]

	var buf strings.Builder
	w := render.NewWriter(&buf)

	if err := renderIDEConfigs(projectRoot, "main", svc, cfg, w); err != nil {
		t.Fatalf("renderIDEConfigs: %v", err)
	}

	// Verify no IDE files were created
	serviceDir := filepath.Join(projectRoot, "services", "main")
	if _, err := os.Stat(serviceDir); err == nil {
		// Dir exists; verify IDE files don't
		for _, rel := range []string{".devcontainer/devcontainer.json", ".vscode/settings.json"} {
			if _, err := os.Stat(filepath.Join(serviceDir, rel)); err == nil {
				t.Errorf("file %s should not be created for empty pack", rel)
			}
		}
	}
}

// TestRenderIDEConfigs_packNotFound verifies clear error when pack is missing.
func TestRenderIDEConfigs_packNotFound(t *testing.T) {
	projectRoot := t.TempDir()
	// Don't create any packs

	cfg := makeIDECfg("main")
	svc := cfg.Services["main"]

	var buf strings.Builder
	w := render.NewWriter(&buf)

	err := renderIDEConfigs(projectRoot, "main", svc, cfg, w)
	if err == nil {
		t.Fatal("expected error when no pack found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

// TestRenderIDEConfigs_perServiceOverride verifies per-service pack override.
func TestRenderIDEConfigs_perServiceOverride(t *testing.T) {
	projectRoot := t.TempDir()

	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".vscode/settings.json.tpl": `{"default": true}`,
	})
	setupIDEPackTemplates(t, projectRoot, "main-debug", map[string]string{
		".vscode/settings.json.tpl": `{"debug": true}`,
	})

	cfg := makeIDECfg("main")
	svc := cfg.Services["main"]
	// Override to use main-debug pack
	svc.IDE.Template = "main-debug"

	var buf strings.Builder
	w := render.NewWriter(&buf)

	if err := renderIDEConfigs(projectRoot, "main", svc, cfg, w); err != nil {
		t.Fatalf("renderIDEConfigs: %v", err)
	}

	// Check that debug pack was used
	settingsPath := filepath.Join(projectRoot, "services", "main", ".vscode", "settings.json")
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	if !strings.Contains(string(content), "debug") {
		t.Errorf("expected debug pack content, got: %s", string(content))
	}
}

// TestRenderIDEConfigs_serviceNameFallback verifies that service-name pack wins over default.
func TestRenderIDEConfigs_serviceNameFallback(t *testing.T) {
	projectRoot := t.TempDir()

	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".vscode/settings.json.tpl": `{"source": "default"}`,
	})
	setupIDEPackTemplates(t, projectRoot, "main", map[string]string{
		".vscode/settings.json.tpl": `{"source": "main"}`,
	})

	cfg := makeIDECfg("main")
	svc := cfg.Services["main"]
	// No explicit template; should use main pack over default

	var buf strings.Builder
	w := render.NewWriter(&buf)

	if err := renderIDEConfigs(projectRoot, "main", svc, cfg, w); err != nil {
		t.Fatalf("renderIDEConfigs: %v", err)
	}

	settingsPath := filepath.Join(projectRoot, "services", "main", ".vscode", "settings.json")
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	if !strings.Contains(string(content), `"source": "main"`) {
		t.Errorf("expected main pack, got: %s", string(content))
	}
}

// TestRenderIDEConfigs_defaultOnly verifies default pack is used when only it exists.
func TestRenderIDEConfigs_defaultOnly(t *testing.T) {
	projectRoot := t.TempDir()

	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".vscode/settings.json.tpl": `{"source": "default"}`,
	})

	cfg := makeIDECfg("unknown")
	svc := cfg.Services["unknown"]

	var buf strings.Builder
	w := render.NewWriter(&buf)

	if err := renderIDEConfigs(projectRoot, "unknown", svc, cfg, w); err != nil {
		t.Fatalf("renderIDEConfigs: %v", err)
	}

	settingsPath := filepath.Join(projectRoot, "services", "unknown", ".vscode", "settings.json")
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	if !strings.Contains(string(content), `"source": "default"`) {
		t.Errorf("expected default pack, got: %s", string(content))
	}
}

// TestRenderIDEConfigs_substitutesTemplateValues verifies template rendering.
func TestRenderIDEConfigs_substitutesTemplateValues(t *testing.T) {
	projectRoot := t.TempDir()

	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".devcontainer/devcontainer.json.tpl": minimalDevcontainerTpl,
	})

	cfg := makeIDECfg("main")
	svc := cfg.Services["main"]

	var buf strings.Builder
	w := render.NewWriter(&buf)

	if err := renderIDEConfigs(projectRoot, "main", svc, cfg, w); err != nil {
		t.Fatalf("renderIDEConfigs: %v", err)
	}

	devcontainerPath := filepath.Join(projectRoot, "services", "main", ".devcontainer", "devcontainer.json")
	content, err := os.ReadFile(devcontainerPath)
	if err != nil {
		t.Fatalf("read devcontainer.json: %v", err)
	}
	s := string(content)

	checks := []struct{ want, label string }{
		{`"name":"laravel"`, "project name"},
		{`"service":"app-main"`, "container name"},
		{`"workspaceFolder":"/workspace"`, "workspace folder"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.want) {
			t.Errorf("devcontainer.json missing %s (%q)", c.label, c.want)
		}
	}
}

// TestExtendsDepth tests the extends chain depth computation.
func TestExtendsDepth(t *testing.T) {
	tests := []struct {
		name       string
		services   map[string]config.ServiceConfig
		svcName    string
		wantDepth  int
		wantCapped bool
	}{
		{
			name: "no extends",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app"},
			},
			svcName:   "main",
			wantDepth: 0,
		},
		{
			name: "one level",
			services: map[string]config.ServiceConfig{
				"main":  {Type: "app"},
				"debug": {Type: "app", Extends: "main"},
			},
			svcName:   "debug",
			wantDepth: 1,
		},
		{
			name: "three-level chain",
			services: map[string]config.ServiceConfig{
				"a": {Type: "app"},
				"b": {Type: "app", Extends: "a"},
				"c": {Type: "app", Extends: "b"},
			},
			svcName:   "c",
			wantDepth: 2,
		},
		{
			name: "unknown service - treated as depth 0",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app"},
			},
			svcName:   "unknown",
			wantDepth: 0,
		},
		{
			name: "cycle - capped at 32",
			services: map[string]config.ServiceConfig{
				"a": {Type: "app", Extends: "b"},
				"b": {Type: "app", Extends: "a"},
			},
			svcName:    "a",
			wantDepth:  32,
			wantCapped: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			depth, capped := extendsDepth(tt.services, tt.svcName)
			if depth != tt.wantDepth {
				t.Errorf("depth: want %d, got %d", tt.wantDepth, depth)
			}
			if capped != tt.wantCapped {
				t.Errorf("capped: want %v, got %v", tt.wantCapped, capped)
			}
		})
	}
}

// TestValidateIDETemplateKey tests the template key validation logic.
func TestValidateIDETemplateKey(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{
			name:    "empty key is valid",
			key:     "",
			wantErr: false,
		},
		{
			name:    "simple key",
			key:     "main",
			wantErr: false,
		},
		{
			name:    "alphanumeric with dash",
			key:     "main-debug",
			wantErr: false,
		},
		{
			name:    "forward slash - rejected",
			key:     "foo/bar",
			wantErr: true,
		},
		{
			name:    "backslash - rejected",
			key:     "foo\\bar",
			wantErr: true,
		},
		{
			name:    "absolute path - rejected",
			key:     "/abs/path",
			wantErr: true,
		},
		{
			name:    "dot at start - rejected",
			key:     ".hidden",
			wantErr: true,
		},
		{
			name:    "double dot - rejected",
			key:     "..",
			wantErr: true,
		},
		{
			name:    "double dot in path - rejected",
			key:     "foo/../bar",
			wantErr: true,
		},
		{
			name:    "double dot segment - rejected",
			key:     "../escape",
			wantErr: true,
		},
		{
			name:    "dot slash - rejected",
			key:     "./foo",
			wantErr: true,
		},
		{
			name:    "single dot - rejected",
			key:     ".",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateIDETemplateKey(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateIDETemplateKey(%q): want err=%v, got %v", tt.key, tt.wantErr, err)
			}
		})
	}
}

// TestSelectIDEServices tests the service selection and collision resolution logic.
func TestSelectIDEServices(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name           string
		services       map[string]config.ServiceConfig
		wantSelected   []string
		wantSkippedMap map[string]skippedService
	}{
		{
			name: "all enabled distinct dirs - all kept",
			services: map[string]config.ServiceConfig{
				"svc1": {
					Type:    "app",
					Enabled: true,
					Dir:     "./services/svc1",
				},
				"svc2": {
					Type:    "app",
					Enabled: true,
					Dir:     "./services/svc2",
				},
			},
			wantSelected: []string{"svc1", "svc2"},
		},
		{
			name: "service with IDERenderEnabled=false dropped",
			services: map[string]config.ServiceConfig{
				"main": {
					Type:    "app",
					Enabled: true,
					Dir:     "./services/main",
				},
				"db": {
					Type:    "db",
					Enabled: true,
					Dir:     "./services/db",
				},
			},
			wantSelected: []string{"main"},
			wantSkippedMap: map[string]skippedService{
				"db": {Name: "db", Reason: "ide-policy"},
			},
		},
		{
			name: "service with Enabled=false dropped",
			services: map[string]config.ServiceConfig{
				"app": {
					Type:    "app",
					Enabled: false,
					Dir:     "./services/app",
				},
				"main": {
					Type:    "app",
					Enabled: true,
					Dir:     "./services/main",
				},
			},
			wantSelected: []string{"main"},
			wantSkippedMap: map[string]skippedService{
				"app": {Name: "app", Reason: "service-disabled"},
			},
		},
		{
			name: "explicit IDE.Enabled=true overrides default",
			services: map[string]config.ServiceConfig{
				"db": {
					Type:    "db",
					Enabled: true,
					Dir:     "./services/db",
					IDE:     config.ServiceIDEConfig{Enabled: &trueVal},
				},
			},
			wantSelected: []string{"db"},
		},
		{
			name: "explicit IDE.Enabled=false overrides type-based default",
			services: map[string]config.ServiceConfig{
				"main": {
					Type:    "app",
					Enabled: true,
					Dir:     "./services/main",
					IDE:     config.ServiceIDEConfig{Enabled: &falseVal},
				},
			},
			wantSkippedMap: map[string]skippedService{
				"main": {Name: "main", Reason: "ide-disabled"},
			},
		},
		{
			name: "two services share dir - child extends parent - child wins",
			services: map[string]config.ServiceConfig{
				"main": {
					Type:    "app",
					Enabled: true,
					Dir:     "./services/main",
				},
				"main-debug": {
					Type:    "app",
					Enabled: true,
					Dir:     "./services/main",
					Extends: "main",
				},
			},
			wantSelected: []string{"main-debug"},
			wantSkippedMap: map[string]skippedService{
				"main": {
					Name:   "main",
					Reason: "lost-collision",
					Dir:    filepath.Join(".", "services", "main"),
					Winner: "main-debug",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected, skipped := selectIDEServices(tt.services)

			if len(selected) != len(tt.wantSelected) {
				t.Errorf("selected count: want %d, got %d (%v)", len(tt.wantSelected), len(selected), selected)
			}
			for i, name := range selected {
				if i < len(tt.wantSelected) && name != tt.wantSelected[i] {
					t.Errorf("selected[%d]: want %q, got %q", i, tt.wantSelected[i], name)
				}
			}

			skippedMap := make(map[string]skippedService)
			for _, s := range skipped {
				skippedMap[s.Name] = s
			}
			if len(skippedMap) != len(tt.wantSkippedMap) {
				t.Errorf("skipped count: want %d, got %d (%v)", len(tt.wantSkippedMap), len(skippedMap), skippedMap)
			}
			for name, want := range tt.wantSkippedMap {
				got, ok := skippedMap[name]
				if !ok {
					t.Errorf("skipped[%q]: expected but not found", name)
					continue
				}
				if got.Reason != want.Reason {
					t.Errorf("skipped[%q].Reason: want %q, got %q", name, want.Reason, got.Reason)
				}
				if want.Reason == "lost-collision" {
					if got.Dir != want.Dir {
						t.Errorf("skipped[%q].Dir: want %q, got %q", name, want.Dir, got.Dir)
					}
					if got.Winner != want.Winner {
						t.Errorf("skipped[%q].Winner: want %q, got %q", name, want.Winner, got.Winner)
					}
				}
			}
		})
	}
}

// TestCheckNoSymlinks verifies the symlink detection helper.
func TestCheckNoSymlinks(t *testing.T) {
	root := t.TempDir()

	realSub := filepath.Join(root, "real", "sub")
	if err := os.MkdirAll(realSub, 0o755); err != nil {
		t.Fatalf("create real dir: %v", err)
	}

	if err := checkNoSymlinks(root, filepath.Join(root, "nonexistent", "path"), "test path"); err != nil {
		t.Errorf("non-existent path: unexpected error: %v", err)
	}

	if err := checkNoSymlinks(root, realSub, "test path"); err != nil {
		t.Errorf("real path: unexpected error: %v", err)
	}

	linkPath := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "real"), linkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	if err := checkNoSymlinks(root, filepath.Join(linkPath, "sub"), "test path"); err == nil {
		t.Errorf("path through symlink: expected error, got nil")
	}

	if err := checkNoSymlinks(root, linkPath, "test path"); err == nil {
		t.Errorf("symlink as target: expected error, got nil")
	}
}

// TestValidateExplicitIDEArg tests explicit service argument validation.
func TestValidateExplicitIDEArg(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name        string
		serviceName string
		services    map[string]config.ServiceConfig
		wantErrMsg  string
	}{
		{
			name:        "unknown service",
			serviceName: "missing",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app", Enabled: true, Dir: "./services/main"},
			},
			wantErrMsg: `service "missing" not found in config`,
		},
		{
			name:        "service disabled at project level",
			serviceName: "main",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app", Enabled: false, Dir: "./services/main"},
			},
			wantErrMsg: `service "main" is disabled at the project level`,
		},
		{
			name:        "ide.enabled: false explicitly set",
			serviceName: "main",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app", Enabled: true, Dir: "./services/main", IDE: config.ServiceIDEConfig{Enabled: &falseVal}},
			},
			wantErrMsg: `service "main" has ide.enabled: false`,
		},
		{
			name:        "non-app type with no explicit ide.enabled",
			serviceName: "db",
			services: map[string]config.ServiceConfig{
				"db": {Type: "db", Enabled: true, Dir: "./services/db"},
			},
			wantErrMsg: `does not participate in IDE rendering by default`,
		},
		{
			name:        "service has no dir",
			serviceName: "main",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app", Enabled: true, IDE: config.ServiceIDEConfig{Enabled: &trueVal}},
			},
			wantErrMsg: `service "main" has no dir`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExplicitIDEArg(tt.serviceName, tt.services)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErrMsg)
			}
			if !strings.Contains(err.Error(), tt.wantErrMsg) {
				t.Errorf("error %q should contain %q", err.Error(), tt.wantErrMsg)
			}
		})
	}
}

// TestSelectIDEServices_ideDisabledReason verifies that explicit ide.enabled:false
// produces "ide-disabled" reason, while default-by-type false produces "ide-policy".
func TestSelectIDEServices_ideDisabledReason(t *testing.T) {
	falseVal := false
	services := map[string]config.ServiceConfig{
		"explicit-false": {
			Type:    "app",
			Enabled: true,
			Dir:     "./services/a",
			IDE:     config.ServiceIDEConfig{Enabled: &falseVal},
		},
		"default-false": {
			Type:    "db",
			Enabled: true,
			Dir:     "./services/b",
		},
	}

	_, skipped := selectIDEServices(services)
	byName := make(map[string]skippedService)
	for _, s := range skipped {
		byName[s.Name] = s
	}

	if got := byName["explicit-false"].Reason; got != "ide-disabled" {
		t.Errorf("explicit false: want reason %q, got %q", "ide-disabled", got)
	}
	if got := byName["default-false"].Reason; got != "ide-policy" {
		t.Errorf("default false: want reason %q, got %q", "ide-policy", got)
	}
}
