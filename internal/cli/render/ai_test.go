package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/devbox/internal/cli/cmdctx"
	aipkg "github.com/semsemyonoff/devbox/internal/core/execution/templates/ai"
	"github.com/semsemyonoff/devbox/internal/core/project/config"

	yamlPkg "gopkg.in/yaml.v3"
)

// setupServicesConfig writes per-folder service files from a `services:` wrapped YAML fragment.
func setupServicesConfig(t *testing.T, dir, servicesYML string) {
	t.Helper()
	type wrap struct {
		Services map[string]any `yaml:"services"`
	}
	var w wrap
	if err := yamlPkg.Unmarshal([]byte(servicesYML), &w); err != nil {
		t.Fatalf("setupServicesConfig parse: %v", err)
	}
	for name, svc := range w.Services {
		svcDir := filepath.Join(dir, "devbox", "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatalf("setupServicesConfig mkdir %s: %v", name, err)
		}
		data, err := yamlPkg.Marshal(svc)
		if err != nil {
			t.Fatalf("setupServicesConfig marshal %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), data, 0o644); err != nil {
			t.Fatalf("setupServicesConfig write %s: %v", name, err)
		}
	}
}

// setupAIPack creates an empty pack at <projectRoot>/devbox/templates/ai/test/
// for ValidateManifest/RenderTemplateFile tests that need a real packroot
// layout. Callers populate packDir with fixtures and call the new API with
// (projectRoot, "test", projectRoot).
func setupAIPack(t *testing.T) (projectRoot, packDir string) {
	t.Helper()
	projectRoot = t.TempDir()
	packDir = filepath.Join(projectRoot, "devbox", "templates", "ai", "test")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("setup ai pack: %v", err)
	}
	return projectRoot, packDir
}

// setupAgentsPackTemplates writes an agents template pack at <dir>/devbox/templates/ai/<packName>/
// and populates it with a directory structure of files.
func setupAgentsPackTemplates(t *testing.T, dir, packName string, files map[string]string) {
	t.Helper()
	packDir := filepath.Join(dir, "devbox", "templates", "ai", packName)
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

// TestResolveAgentsTemplatePack_explicitPackFound verifies explicit pack resolution.
func TestResolveAgentsTemplatePack_explicitPackFound(t *testing.T) {
	projectRoot := t.TempDir()
	setupAgentsPackTemplates(t, projectRoot, "custom", map[string]string{
		"AGENTS.md.tmpl": "test",
	})

	svc := config.ServiceConfig{
		Render: config.ServiceRenderConfig{AI: config.ServiceAIConfig{
			Template: "custom",
		}},
	}

	pack, _, found, err := aipkg.ResolveTemplatePack(svc, projectRoot, "myservice")
	if err != nil {
		t.Fatalf("resolveAgentsTemplatePack: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}

	expected := filepath.Join(projectRoot, "devbox", "templates", "ai", "custom")
	if pack != expected {
		t.Errorf("expected %q, got %q", expected, pack)
	}
}

// TestResolveAgentsTemplatePack_explicitPackMissing verifies explicit missing pack is a hard error.
func TestResolveAgentsTemplatePack_explicitPackMissing(t *testing.T) {
	projectRoot := t.TempDir()

	svc := config.ServiceConfig{
		Render: config.ServiceRenderConfig{AI: config.ServiceAIConfig{
			Template: "missing",
		}},
	}

	_, _, found, err := aipkg.ResolveTemplatePack(svc, projectRoot, "myservice")
	if err == nil {
		t.Fatal("expected error for missing explicit pack")
	}
	if found {
		t.Fatal("expected found=false when error occurs")
	}
	if !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should mention not found and pack name: %v", err)
	}
}

// TestResolveAgentsTemplatePack_implicitServiceName verifies service-name fallthrough.
func TestResolveAgentsTemplatePack_implicitServiceName(t *testing.T) {
	projectRoot := t.TempDir()
	setupAgentsPackTemplates(t, projectRoot, "api", map[string]string{
		"AGENTS.md.tmpl": "test",
	})

	svc := config.ServiceConfig{
		Render: config.ServiceRenderConfig{AI: config.ServiceAIConfig{
			Template: "",
		}},
	}

	pack, _, found, err := aipkg.ResolveTemplatePack(svc, projectRoot, "api")
	if err != nil {
		t.Fatalf("resolveAgentsTemplatePack: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}

	expected := filepath.Join(projectRoot, "devbox", "templates", "ai", "api")
	if pack != expected {
		t.Errorf("expected %q, got %q", expected, pack)
	}
}

// TestResolveAgentsTemplatePack_implicitFallbackToDefault verifies default fallback.
func TestResolveAgentsTemplatePack_implicitFallbackToDefault(t *testing.T) {
	projectRoot := t.TempDir()
	setupAgentsPackTemplates(t, projectRoot, "default", map[string]string{
		"AGENTS.md.tmpl": "test",
	})

	svc := config.ServiceConfig{
		Render: config.ServiceRenderConfig{AI: config.ServiceAIConfig{
			Template: "",
		}},
	}

	pack, _, found, err := aipkg.ResolveTemplatePack(svc, projectRoot, "notfound")
	if err != nil {
		t.Fatalf("resolveAgentsTemplatePack: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}

	expected := filepath.Join(projectRoot, "devbox", "templates", "ai", "default")
	if pack != expected {
		t.Errorf("expected %q, got %q", expected, pack)
	}
}

// TestResolveAgentsTemplatePack_implicitBothMissing verifies error when both candidates missing.
func TestResolveAgentsTemplatePack_implicitBothMissing(t *testing.T) {
	projectRoot := t.TempDir()

	svc := config.ServiceConfig{
		Render: config.ServiceRenderConfig{AI: config.ServiceAIConfig{
			Template: "",
		}},
	}

	_, _, found, err := aipkg.ResolveTemplatePack(svc, projectRoot, "myservice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected found=false when both candidates missing")
	}
}

// TestResolveAgentsTemplatePack_symlinkedPackRejected verifies symlinks are rejected.
func TestResolveAgentsTemplatePack_symlinkedPackRejected(t *testing.T) {
	projectRoot := t.TempDir()
	realPack := filepath.Join(projectRoot, "real_agents")
	if err := os.MkdirAll(realPack, 0o755); err != nil {
		t.Fatalf("create real pack: %v", err)
	}

	templatesDir := filepath.Join(projectRoot, "devbox", "templates", "ai")
	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		t.Fatalf("create templates dir: %v", err)
	}

	symlinkPath := filepath.Join(templatesDir, "linked")
	if err := os.Symlink(realPack, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	svc := config.ServiceConfig{
		Render: config.ServiceRenderConfig{AI: config.ServiceAIConfig{
			Template: "linked",
		}},
	}

	_, _, found, err := aipkg.ResolveTemplatePack(svc, projectRoot, "myservice")
	if err == nil {
		t.Fatal("expected error for symlinked pack")
	}
	if found {
		t.Fatal("expected found=false when error occurs")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink: %v", err)
	}
}

// TestResolveAgentsTemplatePack_nonDirPackRejected verifies non-directories are rejected.
func TestResolveAgentsTemplatePack_nonDirPackRejected(t *testing.T) {
	projectRoot := t.TempDir()
	templatesDir := filepath.Join(projectRoot, "devbox", "templates", "ai")
	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		t.Fatalf("create templates dir: %v", err)
	}

	filePath := filepath.Join(templatesDir, "file")
	if err := os.WriteFile(filePath, []byte("test"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	svc := config.ServiceConfig{
		Render: config.ServiceRenderConfig{AI: config.ServiceAIConfig{
			Template: "file",
		}},
	}

	_, _, found, err := aipkg.ResolveTemplatePack(svc, projectRoot, "myservice")
	if err == nil {
		t.Fatal("expected error for non-dir pack")
	}
	if found {
		t.Fatal("expected found=false when error occurs")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error should mention not a directory: %v", err)
	}
}

// TestResolveAgentsTemplatePack_invalidTemplateKey verifies invalid key rejection.
func TestResolveAgentsTemplatePack_invalidTemplateKey(t *testing.T) {
	projectRoot := t.TempDir()

	tests := []struct {
		template string
		label    string
	}{
		{"path/separator", "path separator"},
		{".hidden", "leading dot"},
		{"../escape", "path escape"},
	}

	for _, test := range tests {
		t.Run(test.label, func(t *testing.T) {
			svc := config.ServiceConfig{
				Render: config.ServiceRenderConfig{AI: config.ServiceAIConfig{
					Template: test.template,
				}},
			}

			_, _, found, err := aipkg.ResolveTemplatePack(svc, projectRoot, "myservice")
			if err == nil {
				t.Fatalf("expected error for %s", test.label)
			}
			if found {
				t.Fatal("expected found=false when error occurs")
			}
		})
	}
}

// TestResolveAgentsTemplatePack_invalidServiceName verifies invalid service name rejection.
func TestResolveAgentsTemplatePack_invalidServiceName(t *testing.T) {
	projectRoot := t.TempDir()

	tests := []struct {
		serviceName string
		label       string
	}{
		{"path/sep", "path separator"},
		{"..evil", "path escape"},
		{"../evil", "path escape prefix"},
	}

	for _, test := range tests {
		t.Run(test.label, func(t *testing.T) {
			svc := config.ServiceConfig{
				Render: config.ServiceRenderConfig{AI: config.ServiceAIConfig{
					Template: "",
				}},
			}

			_, _, found, err := aipkg.ResolveTemplatePack(svc, projectRoot, test.serviceName)
			if err != nil {
				t.Fatalf("unexpected error for %s: %v", test.label, err)
			}
			if found {
				t.Fatalf("expected found=false for %s", test.label)
			}
		})
	}
}

// TestResolveAgentsTemplatePack_implicitChainPreference verifies service-name is preferred over default.
func TestResolveAgentsTemplatePack_implicitChainPreference(t *testing.T) {
	projectRoot := t.TempDir()
	// Set up both service-name and default packs
	setupAgentsPackTemplates(t, projectRoot, "myapi", map[string]string{
		"AGENTS.md.tmpl": "api-specific",
	})
	setupAgentsPackTemplates(t, projectRoot, "default", map[string]string{
		"AGENTS.md.tmpl": "default-content",
	})

	svc := config.ServiceConfig{
		Render: config.ServiceRenderConfig{AI: config.ServiceAIConfig{
			Template: "",
		}},
	}

	pack, _, found, err := aipkg.ResolveTemplatePack(svc, projectRoot, "myapi")
	if err != nil {
		t.Fatalf("resolveAgentsTemplatePack: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}

	// Should pick the service-name pack, not the default
	expected := filepath.Join(projectRoot, "devbox", "templates", "ai", "myapi")
	if pack != expected {
		t.Errorf("expected %q (service-name), got %q", expected, pack)
	}
}

// TestLoadAgentsManifest_valid loads a valid manifest successfully.
func TestLoadAgentsManifest_valid(t *testing.T) {
	packDir := t.TempDir()
	manifestContent := `render:
  - from: AGENTS.md.tmpl
    to: AGENTS.md
symlinks:
  - link: CLAUDE.md
    to: AGENTS.md
`
	manifestPath := filepath.Join(packDir, "manifest.yml")
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	m, err := aipkg.LoadManifest(packDir)
	if err != nil {
		t.Fatalf("loadAgentsManifest: %v", err)
	}

	if len(m.Render) != 1 || m.Render[0].From != "AGENTS.md.tmpl" || m.Render[0].To != "AGENTS.md" {
		t.Errorf("unexpected render entry: %+v", m.Render)
	}

	if len(m.Symlinks) != 1 || m.Symlinks[0].Link != "CLAUDE.md" || m.Symlinks[0].To != "AGENTS.md" {
		t.Errorf("unexpected symlink entry: %+v", m.Symlinks)
	}
}

// TestLoadAgentsManifest_missing reports error when manifest missing.
func TestLoadAgentsManifest_missing(t *testing.T) {
	packDir := t.TempDir()

	_, err := aipkg.LoadManifest(packDir)
	if err == nil {
		t.Fatal("expected error for missing manifest")
	}
	if !strings.Contains(err.Error(), "manifest") {
		t.Errorf("error should mention manifest: %v", err)
	}
}

// TestLoadAgentsManifest_unknownField rejects unknown YAML fields.
func TestLoadAgentsManifest_unknownField(t *testing.T) {
	packDir := t.TempDir()
	manifestContent := `render:
  - from: test.tmpl
    to: test
unknown_field: value
`
	manifestPath := filepath.Join(packDir, "manifest.yml")
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, err := aipkg.LoadManifest(packDir)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

// TestValidateAgentsManifest_empty rejects empty manifest.
func TestValidateAgentsManifest_empty(t *testing.T) {
	projectRoot, _ := setupAIPack(t)
	m := &aipkg.Manifest{}
	err := aipkg.ValidateManifest(m, projectRoot, "test", projectRoot)
	if err == nil {
		t.Fatal("expected error for empty manifest")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty: %v", err)
	}
}

// TestValidateAgentsManifest_fromEscaping rejects `from` escaping pack dir.
func TestValidateAgentsManifest_fromEscaping(t *testing.T) {
	projectRoot, packDir := setupAIPack(t)
	outsideFile := filepath.Join(filepath.Dir(packDir), "outside.tmpl")
	if err := os.WriteFile(outsideFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	m := &aipkg.Manifest{
		Render: []aipkg.RenderEntry{
			{From: "../outside.tmpl", To: "test"},
		},
	}

	err := aipkg.ValidateManifest(m, projectRoot, "test", projectRoot)
	if err == nil {
		t.Fatal("expected error for escaping from")
	}
	if !strings.Contains(err.Error(), "escape") {
		t.Errorf("error should mention escape: %v", err)
	}
}

// TestValidateAgentsManifest_fromNoTmplSuffix rejects `from` not ending in .tmpl.
func TestValidateAgentsManifest_fromNoTmplSuffix(t *testing.T) {
	projectRoot, packDir := setupAIPack(t)
	file := filepath.Join(packDir, "test.txt")
	if err := os.WriteFile(file, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	m := &aipkg.Manifest{
		Render: []aipkg.RenderEntry{
			{From: "test.txt", To: "test"},
		},
	}

	err := aipkg.ValidateManifest(m, projectRoot, "test", projectRoot)
	if err == nil {
		t.Fatal("expected error for non-.tmpl from")
	}
	if !strings.Contains(err.Error(), ".tmpl") {
		t.Errorf("error should mention .tmpl: %v", err)
	}
}

// TestValidateAgentsManifest_fromNotExist rejects non-existent `from` file.
func TestValidateAgentsManifest_fromNotExist(t *testing.T) {
	projectRoot, _ := setupAIPack(t)
	m := &aipkg.Manifest{
		Render: []aipkg.RenderEntry{
			{From: "missing.tmpl", To: "test"},
		},
	}

	err := aipkg.ValidateManifest(m, projectRoot, "test", projectRoot)
	if err == nil {
		t.Fatal("expected error for missing from file")
	}
	if !strings.Contains(err.Error(), "not exist") {
		t.Errorf("error should mention not exist: %v", err)
	}
}

// TestValidateAgentsManifest_fromIsSymlink rejects `from` being a symlink.
func TestValidateAgentsManifest_fromIsSymlink(t *testing.T) {
	projectRoot, packDir := setupAIPack(t)
	targetFile := filepath.Join(packDir, "target.tmpl")
	if err := os.WriteFile(targetFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	linkPath := filepath.Join(packDir, "link.tmpl")
	if err := os.Symlink(targetFile, linkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	m := &aipkg.Manifest{
		Render: []aipkg.RenderEntry{
			{From: "link.tmpl", To: "test"},
		},
	}

	err := aipkg.ValidateManifest(m, projectRoot, "test", projectRoot)
	if err == nil {
		t.Fatal("expected error for symlink from")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink: %v", err)
	}
}

// TestValidateAgentsManifest_fromSymlinkedParent rejects `from` with symlinked parent.
func TestValidateAgentsManifest_fromSymlinkedParent(t *testing.T) {
	projectRoot, packDir := setupAIPack(t)
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "evil.tmpl")
	if err := os.WriteFile(outsideFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	symDir := filepath.Join(packDir, "symdir")
	if err := os.Symlink(outsideDir, symDir); err != nil {
		t.Fatalf("create symlink dir: %v", err)
	}

	m := &aipkg.Manifest{
		Render: []aipkg.RenderEntry{
			{From: "symdir/evil.tmpl", To: "test"},
		},
	}

	err := aipkg.ValidateManifest(m, projectRoot, "test", projectRoot)
	if err == nil {
		t.Fatal("expected error for symlinked parent directory")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink: %v", err)
	}
}

// TestValidateAgentsManifest_fromIsDirectory rejects `from` being a directory.
func TestValidateAgentsManifest_fromIsDirectory(t *testing.T) {
	projectRoot, packDir := setupAIPack(t)
	subdir := filepath.Join(packDir, "subdir.tmpl")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("create subdir: %v", err)
	}

	m := &aipkg.Manifest{
		Render: []aipkg.RenderEntry{
			{From: "subdir.tmpl", To: "test"},
		},
	}

	err := aipkg.ValidateManifest(m, projectRoot, "test", projectRoot)
	if err == nil {
		t.Fatal("expected error for directory from")
	}
	if !strings.Contains(err.Error(), "regular file") {
		t.Errorf("error should mention regular file: %v", err)
	}
}

// TestValidateAgentsManifest_toEscaping rejects `to` escaping hub dir.
func TestValidateAgentsManifest_toEscaping(t *testing.T) {
	projectRoot, packDir := setupAIPack(t)
	file := filepath.Join(packDir, "test.tmpl")
	if err := os.WriteFile(file, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	destRoot := filepath.Join(projectRoot, "hub")
	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		t.Fatalf("create destRoot: %v", err)
	}

	m := &aipkg.Manifest{
		Render: []aipkg.RenderEntry{
			{From: "test.tmpl", To: "../escape"},
		},
	}

	err := aipkg.ValidateManifest(m, projectRoot, "test", destRoot)
	if err == nil {
		t.Fatal("expected error for escaping to")
	}
	if !strings.Contains(err.Error(), "escape") {
		t.Errorf("error should mention escape: %v", err)
	}
}

// TestValidateAgentsManifest_symlinkToNotMatching rejects symlink `to` not matching render.
func TestValidateAgentsManifest_symlinkToNotMatching(t *testing.T) {
	projectRoot, packDir := setupAIPack(t)
	file := filepath.Join(packDir, "test.tmpl")
	if err := os.WriteFile(file, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	m := &aipkg.Manifest{
		Render: []aipkg.RenderEntry{
			{From: "test.tmpl", To: "AGENTS.md"},
		},
		Symlinks: []aipkg.SymlinkEntry{
			{Link: "CLAUDE.md", To: "nonexistent.md"},
		},
	}

	err := aipkg.ValidateManifest(m, projectRoot, "test", projectRoot)
	if err == nil {
		t.Fatal("expected error for symlink to not matching render")
	}
	if !strings.Contains(err.Error(), "does not reference") {
		t.Errorf("error should mention not referencing render destination: %v", err)
	}
}

// TestValidateAgentsManifest_duplicateRenderDest rejects duplicate render destinations.
func TestValidateAgentsManifest_duplicateRenderDest(t *testing.T) {
	projectRoot, packDir := setupAIPack(t)
	file1 := filepath.Join(packDir, "file1.tmpl")
	file2 := filepath.Join(packDir, "file2.tmpl")
	if err := os.WriteFile(file1, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file2: %v", err)
	}

	m := &aipkg.Manifest{
		Render: []aipkg.RenderEntry{
			{From: "file1.tmpl", To: "AGENTS.md"},
			{From: "file2.tmpl", To: "AGENTS.md"},
		},
	}

	err := aipkg.ValidateManifest(m, projectRoot, "test", projectRoot)
	if err == nil {
		t.Fatal("expected error for duplicate render destination")
	}
	if !strings.Contains(err.Error(), "duplicate") && !strings.Contains(err.Error(), "duplicated") {
		t.Errorf("error should mention duplicate: %v", err)
	}
}

// TestValidateAgentsManifest_duplicateSymlinkLink rejects duplicate symlink links.
func TestValidateAgentsManifest_duplicateSymlinkLink(t *testing.T) {
	projectRoot, packDir := setupAIPack(t)
	file := filepath.Join(packDir, "test.tmpl")
	if err := os.WriteFile(file, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	m := &aipkg.Manifest{
		Render: []aipkg.RenderEntry{
			{From: "test.tmpl", To: "AGENTS.md"},
		},
		Symlinks: []aipkg.SymlinkEntry{
			{Link: "CLAUDE.md", To: "AGENTS.md"},
			{Link: "CLAUDE.md", To: "AGENTS.md"},
		},
	}

	err := aipkg.ValidateManifest(m, projectRoot, "test", projectRoot)
	if err == nil {
		t.Fatal("expected error for duplicate symlink link")
	}
	if !strings.Contains(err.Error(), "duplicate") && !strings.Contains(err.Error(), "duplicated") {
		t.Errorf("error should mention duplicate: %v", err)
	}
}

// TestValidateAgentsManifest_nestedPaths allows nested `to`/`link` paths.
func TestValidateAgentsManifest_nestedPaths(t *testing.T) {
	projectRoot, packDir := setupAIPack(t)
	file := filepath.Join(packDir, "test.tmpl")
	if err := os.WriteFile(file, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	m := &aipkg.Manifest{
		Render: []aipkg.RenderEntry{
			{From: "test.tmpl", To: ".claude/AGENTS.md"},
		},
		Symlinks: []aipkg.SymlinkEntry{
			{Link: ".claude/CLAUDE.md", To: ".claude/AGENTS.md"},
		},
	}

	err := aipkg.ValidateManifest(m, projectRoot, "test", projectRoot)
	if err != nil {
		t.Errorf("nested paths should be allowed: %v", err)
	}
}

// TestValidateAgentsManifest_validFull tests a complete valid manifest.
func TestValidateAgentsManifest_validFull(t *testing.T) {
	projectRoot, packDir := setupAIPack(t)
	file1 := filepath.Join(packDir, "agents.tmpl")
	file2 := filepath.Join(packDir, "claude.tmpl")
	if err := os.WriteFile(file1, []byte("agents"), 0o644); err != nil {
		t.Fatalf("write file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte("claude"), 0o644); err != nil {
		t.Fatalf("write file2: %v", err)
	}

	m := &aipkg.Manifest{
		Render: []aipkg.RenderEntry{
			{From: "agents.tmpl", To: "AGENTS.md"},
			{From: "claude.tmpl", To: "CLAUDE.md"},
		},
		Symlinks: []aipkg.SymlinkEntry{
			{Link: ".claude/AGENTS.md", To: "AGENTS.md"},
		},
	}

	err := aipkg.ValidateManifest(m, projectRoot, "test", projectRoot)
	if err != nil {
		t.Errorf("valid manifest should not error: %v", err)
	}
}

// TestValidateAgentsManifest_overrideSatisfiesMissingFrom verifies a sibling
// <pack>.local/ override can supply a `from` file that is absent from the
// canonical pack. Without resolver-aware validation this test fails.
func TestValidateAgentsManifest_overrideSatisfiesMissingFrom(t *testing.T) {
	projectRoot, _ := setupAIPack(t)
	// Canonical pack intentionally has NO foo.tmpl; place it only in the override pack.
	overrideDir := filepath.Join(projectRoot, "devbox", "templates", "ai", "test.local")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir override: %v", err)
	}
	if err := os.WriteFile(filepath.Join(overrideDir, "foo.tmpl"), []byte("override"), 0o644); err != nil {
		t.Fatalf("write override file: %v", err)
	}

	m := &aipkg.Manifest{
		Render: []aipkg.RenderEntry{
			{From: "foo.tmpl", To: "AGENTS.md"},
		},
	}
	if err := aipkg.ValidateManifest(m, projectRoot, "test", projectRoot); err != nil {
		t.Errorf("override should satisfy from existence: %v", err)
	}
}

// writeAIPackTmpl writes a single .tmpl into <projectRoot>/devbox/templates/ai/test/<rel>.
func writeAIPackTmpl(t *testing.T, projectRoot, rel, content string) {
	t.Helper()
	packDir := filepath.Join(projectRoot, "devbox", "templates", "ai", "test")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("mkdir pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, rel), []byte(content), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
}

// TestRenderAgentsTemplateFile_fresh writes a fresh template file.
func TestRenderAgentsTemplateFile_fresh(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "api")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatalf("create hub dir: %v", err)
	}

	writeAIPackTmpl(t, projectRoot, "template.tmpl", "Service: {{ .Service }}, Project: {{ .Project.Name }}")

	data := aipkg.TemplateData{
		Project: config.ProjectConfig{Name: "myproject"},
		Service: "api",
		Cfg:     &config.DevboxConfig{Raw: map[string]any{}},
	}

	dest := "AGENTS.md"
	if _, err := aipkg.RenderTemplateFile(projectRoot, "test", "template.tmpl", data, dest, hubDir, projectRoot); err != nil {
		t.Fatalf("renderAgentsTemplateFile: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(hubDir, dest))
	if err != nil {
		t.Fatalf("read rendered file: %v", err)
	}
	expected := "Service: api, Project: myproject"
	if string(content) != expected {
		t.Errorf("expected %q, got %q", expected, string(content))
	}
}

// TestRenderAgentsTemplateFile_idempotent re-renders the same file.
func TestRenderAgentsTemplateFile_idempotent(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "api")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatalf("create hub dir: %v", err)
	}

	writeAIPackTmpl(t, projectRoot, "template.tmpl", "Content: {{ .Service }}")
	data := aipkg.TemplateData{Service: "api", Cfg: &config.DevboxConfig{Raw: map[string]any{}}}
	dest := "AGENTS.md"

	if _, err := aipkg.RenderTemplateFile(projectRoot, "test", "template.tmpl", data, dest, hubDir, projectRoot); err != nil {
		t.Fatalf("first render: %v", err)
	}
	resultPath := filepath.Join(hubDir, dest)
	info1, err := os.Stat(resultPath)
	if err != nil {
		t.Fatalf("stat after first render: %v", err)
	}

	if _, err := aipkg.RenderTemplateFile(projectRoot, "test", "template.tmpl", data, dest, hubDir, projectRoot); err != nil {
		t.Fatalf("second render: %v", err)
	}
	info2, err := os.Stat(resultPath)
	if err != nil {
		t.Fatalf("stat after second render: %v", err)
	}

	if !info1.Mode().IsRegular() || !info2.Mode().IsRegular() {
		t.Error("expected regular files")
	}
	got, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read after second render: %v", err)
	}
	if string(got) != "Content: api" {
		t.Errorf("expected %q, got %q", "Content: api", string(got))
	}
}

// TestRenderAgentsTemplateFile_nestedPath renders to a nested destination.
func TestRenderAgentsTemplateFile_nestedPath(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "api")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatalf("create hub dir: %v", err)
	}

	writeAIPackTmpl(t, projectRoot, "template.tmpl", "Nested")
	data := aipkg.TemplateData{Cfg: &config.DevboxConfig{Raw: map[string]any{}}}
	dest := ".claude/AGENTS.md"

	if _, err := aipkg.RenderTemplateFile(projectRoot, "test", "template.tmpl", data, dest, hubDir, projectRoot); err != nil {
		t.Fatalf("renderAgentsTemplateFile: %v", err)
	}

	if _, err := os.Stat(filepath.Join(hubDir, dest)); err != nil {
		t.Fatalf("nested file not created: %v", err)
	}
}

// TestRenderAgentsTemplateFile_escapingDest rejects destination escaping hub.
func TestRenderAgentsTemplateFile_escapingDest(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "api")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatalf("create hub dir: %v", err)
	}

	writeAIPackTmpl(t, projectRoot, "template.tmpl", "test")
	data := aipkg.TemplateData{Cfg: &config.DevboxConfig{Raw: map[string]any{}}}
	dest := "../escape.md"

	_, err := aipkg.RenderTemplateFile(projectRoot, "test", "template.tmpl", data, dest, hubDir, projectRoot)
	if err == nil {
		t.Fatal("expected error for escaping destination")
	}
	if !strings.Contains(err.Error(), "escape") {
		t.Errorf("error should mention escape: %v", err)
	}
}

// TestRenderAgentsTemplateFile_symlinkInDestDir rejects a destination whose
// directory path contains a symlink component.
func TestRenderAgentsTemplateFile_symlinkInDestDir(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "api")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatalf("create hub dir: %v", err)
	}

	symlinkDir := filepath.Join(hubDir, ".claude")
	realTarget := t.TempDir()
	if err := os.Symlink(realTarget, symlinkDir); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	writeAIPackTmpl(t, projectRoot, "template.tmpl", "test")
	data := aipkg.TemplateData{Cfg: &config.DevboxConfig{Raw: map[string]any{}}}
	dest := ".claude/AGENTS.md"

	_, err := aipkg.RenderTemplateFile(projectRoot, "test", "template.tmpl", data, dest, hubDir, projectRoot)
	if err == nil {
		t.Fatal("expected error when destination dir contains a symlink component")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink: %v", err)
	}
}

// TestRenderAgentsTemplateFile_overrideHit verifies a sibling <pack>.local
// override is preferred over the canonical pack and is reported via fromOverride.
func TestRenderAgentsTemplateFile_overrideHit(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "api")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatalf("create hub dir: %v", err)
	}
	writeAIPackTmpl(t, projectRoot, "foo.tmpl", "canonical")
	overrideDir := filepath.Join(projectRoot, "devbox", "templates", "ai", "test.local")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir override: %v", err)
	}
	if err := os.WriteFile(filepath.Join(overrideDir, "foo.tmpl"), []byte("override"), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}

	fromOverride, err := aipkg.RenderTemplateFile(projectRoot, "test", "foo.tmpl", aipkg.TemplateData{Cfg: &config.DevboxConfig{Raw: map[string]any{}}}, "AGENTS.md", hubDir, projectRoot)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !fromOverride {
		t.Error("expected fromOverride=true when override pack supplies the file")
	}
	got, err := os.ReadFile(filepath.Join(hubDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "override" {
		t.Errorf("expected override content, got %q", got)
	}
}

// TestEnsureRelativeSymlink_fresh creates a new symlink.
func TestEnsureRelativeSymlink_fresh(t *testing.T) {
	hubDir := t.TempDir()
	projectRoot := filepath.Dir(hubDir)

	if err := aipkg.EnsureRelativeSymlink("CLAUDE.md", "AGENTS.md", hubDir, projectRoot); err != nil {
		t.Fatalf("EnsureRelativeSymlink: %v", err)
	}

	// Verify symlink exists
	linkPath := filepath.Join(hubDir, "CLAUDE.md")
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "AGENTS.md" {
		t.Errorf("expected target %q, got %q", "AGENTS.md", target)
	}
}

// TestEnsureRelativeSymlink_idempotent verifies that calling EnsureRelativeSymlink twice is safe.
func TestEnsureRelativeSymlink_idempotent(t *testing.T) {
	hubDir := t.TempDir()
	projectRoot := filepath.Dir(hubDir)

	// First creation
	if err := aipkg.EnsureRelativeSymlink("CLAUDE.md", "AGENTS.md", hubDir, projectRoot); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Second call (idempotent - should not error)
	if err := aipkg.EnsureRelativeSymlink("CLAUDE.md", "AGENTS.md", hubDir, projectRoot); err != nil {
		t.Fatalf("second call: %v", err)
	}

	// Verify symlink still correct
	linkPath := filepath.Join(hubDir, "CLAUDE.md")
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "AGENTS.md" {
		t.Errorf("expected target %q, got %q", "AGENTS.md", target)
	}
}

// TestEnsureRelativeSymlink_targetChanged replaces when target changes.
func TestEnsureRelativeSymlink_targetChanged(t *testing.T) {
	hubDir := t.TempDir()
	projectRoot := filepath.Dir(hubDir)

	// Create initial symlink
	if err := aipkg.EnsureRelativeSymlink("LINK.md", "OLD.md", hubDir, projectRoot); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Change target
	if err := aipkg.EnsureRelativeSymlink("LINK.md", "NEW.md", hubDir, projectRoot); err != nil {
		t.Fatalf("second call: %v", err)
	}

	linkPath := filepath.Join(hubDir, "LINK.md")
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "NEW.md" {
		t.Errorf("expected NEW.md, got %q", target)
	}
}

// TestEnsureRelativeSymlink_regularFileExists rejects regular file at link path.
func TestEnsureRelativeSymlink_regularFileExists(t *testing.T) {
	hubDir := t.TempDir()
	projectRoot := filepath.Dir(hubDir)

	// Create a regular file at the link path
	linkPath := filepath.Join(hubDir, "CLAUDE.md")
	if err := os.WriteFile(linkPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	err := aipkg.EnsureRelativeSymlink("CLAUDE.md", "AGENTS.md", hubDir, projectRoot)
	if err == nil {
		t.Fatal("expected error for existing regular file")
	}
	if !strings.Contains(err.Error(), "refuse") {
		t.Errorf("error should mention refuse: %v", err)
	}
}

// TestEnsureRelativeSymlink_nestedPath creates symlink in nested directory.
func TestEnsureRelativeSymlink_nestedPath(t *testing.T) {
	hubDir := t.TempDir()
	projectRoot := filepath.Dir(hubDir)

	if err := aipkg.EnsureRelativeSymlink(".claude/CLAUDE.md", "AGENTS.md", hubDir, projectRoot); err != nil {
		t.Fatalf("EnsureRelativeSymlink: %v", err)
	}

	linkPath := filepath.Join(hubDir, ".claude", "CLAUDE.md")
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "../AGENTS.md" {
		t.Errorf("expected relative target ../AGENTS.md, got %q", target)
	}
}

func TestEnsureRelativeSymlink_escapeLink(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "api")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatalf("create hub dir: %v", err)
	}

	// Write a target file inside the hub so the target path is valid
	if err := os.WriteFile(filepath.Join(hubDir, "AGENTS.md"), []byte("content"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	// linkPath escapes the hub directory
	err := aipkg.EnsureRelativeSymlink("../escape.md", "AGENTS.md", hubDir, projectRoot)
	if err == nil {
		t.Fatal("expected error for link escaping hub, got nil")
	}
}

func TestEnsureRelativeSymlink_escapeTarget(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "api")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatalf("create hub dir: %v", err)
	}

	// targetWithinHub escapes the hub directory
	err := aipkg.EnsureRelativeSymlink("CLAUDE.md", "../outside.md", hubDir, projectRoot)
	if err == nil {
		t.Fatal("expected error for target escaping hub, got nil")
	}
}

// TestNewAICmd_happyPath tests the full command flow with a single service.
func TestNewAICmd_happyPath(t *testing.T) {
	projectRoot := t.TempDir()

	// Setup devbox.yml
	devboxYAML := `schema_version: "2"
project:
  name: test-project
services:
  api:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(projectRoot, "devbox.yml"), []byte(devboxYAML), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}

	// Setup services.yml with service details
	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    dir: services/api
    container: test-api
`)

	// Create template pack with manifest and template file
	setupAgentsPackTemplates(t, projectRoot, "default", map[string]string{
		"manifest.yml":   "render:\n  - from: AGENTS.md.tmpl\n    to: AGENTS.md\nsymlinks:\n  - link: CLAUDE.md\n    to: AGENTS.md",
		"AGENTS.md.tmpl": "# Agents for {{ .Service }}\nProject: {{ .Project.Name }}",
	})

	// Create service directory
	hubDir := filepath.Join(projectRoot, "services", "api")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatalf("create service dir: %v", err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newAICmd(flags)

	if err := cmd.RunE(cmd, []string{"api"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	// Verify AGENTS.md was created
	agentsPath := filepath.Join(hubDir, "AGENTS.md")
	content, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if !strings.Contains(string(content), "Agents for api") {
		t.Errorf("AGENTS.md missing expected content: %s", content)
	}

	// Verify symlink was created
	claudePath := filepath.Join(hubDir, "CLAUDE.md")
	link, err := os.Readlink(claudePath)
	if err != nil {
		t.Fatalf("readlink CLAUDE.md: %v", err)
	}
	if link != "AGENTS.md" {
		t.Errorf("expected symlink to AGENTS.md, got %q", link)
	}
}

// TestNewAICmd_explicitServiceAIDisabled tests error when ai.enabled: false.
func TestNewAICmd_explicitServiceAIDisabled(t *testing.T) {
	projectRoot := t.TempDir()

	devboxYAML := `schema_version: "2"
project:
  name: test-project
services:
  api:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(projectRoot, "devbox.yml"), []byte(devboxYAML), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}

	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    dir: services/api
    container: test-api
    render:
      ai:
        enabled: false
`)

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newAICmd(flags)

	err := cmd.RunE(cmd, []string{"api"})
	if err == nil {
		t.Fatal("expected error for ai.enabled: false")
	}
	if !strings.Contains(err.Error(), "ai.enabled") {
		t.Errorf("error should mention 'ai.enabled': %v", err)
	}
}

// TestNewAICmd_noArgAutoSelection tests auto-selection without explicit service.
func TestNewAICmd_noArgAutoSelection(t *testing.T) {
	projectRoot := t.TempDir()

	devboxYAML := `schema_version: "2"
project:
  name: test-project
services:
  enabled-svc:
    enabled: true
  disabled-svc:
    enabled: false
  ai-disabled-svc:
    enabled: true
  no-dir-svc:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(projectRoot, "devbox.yml"), []byte(devboxYAML), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}

	setupServicesConfig(t, projectRoot, `
services:
  enabled-svc:
    type: app
    dir: services/enabled
    container: test-enabled
  disabled-svc:
    type: app
    dir: services/disabled
    container: test-disabled
  ai-disabled-svc:
    type: app
    dir: services/ai-disabled
    container: test-ai-disabled
    render:
      ai:
        enabled: false
  no-dir-svc:
    type: app
    container: test-no-dir
`)

	// Create template pack
	setupAgentsPackTemplates(t, projectRoot, "default", map[string]string{
		"manifest.yml":   "render:\n  - from: AGENTS.md.tmpl\n    to: AGENTS.md\nsymlinks:\n  - link: CLAUDE.md\n    to: AGENTS.md",
		"AGENTS.md.tmpl": "# Agents for {{ .Service }}",
	})

	// Create service directories (no-dir-svc has no directory by design)
	for _, dir := range []string{"services/enabled", "services/disabled", "services/ai-disabled"} {
		if err := os.MkdirAll(filepath.Join(projectRoot, dir), 0o755); err != nil {
			t.Fatalf("create dir %s: %v", dir, err)
		}
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newAICmd(flags)

	// Command should succeed even though no-dir-svc is skipped with a warning
	if err := cmd.RunE(cmd, []string{}); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	// Only enabled-svc should have rendered files
	enabledPath := filepath.Join(projectRoot, "services", "enabled", "AGENTS.md")
	if _, err := os.Stat(enabledPath); err != nil {
		t.Fatalf("expected AGENTS.md in enabled service: %v", err)
	}

	// ai-disabled-svc should not have files (ai.enabled: false)
	aiDisabledPath := filepath.Join(projectRoot, "services", "ai-disabled", "AGENTS.md")
	if _, err := os.Stat(aiDisabledPath); err == nil {
		t.Fatal("expected no AGENTS.md in ai-disabled service")
	}

	// Disabled services should not have files
	disabledPath := filepath.Join(projectRoot, "services", "disabled", "AGENTS.md")
	if _, err := os.Stat(disabledPath); err == nil {
		t.Fatal("expected no AGENTS.md in disabled service")
	}

	// no-dir-svc should not have rendered files (skipped due to empty dir, warning emitted)
	noDirPath := filepath.Join(projectRoot, "no-dir-svc", "AGENTS.md")
	if _, err := os.Stat(noDirPath); err == nil {
		t.Fatal("expected no AGENTS.md for no-dir-svc")
	}
}

// TestNewAICmd_explicitServiceNotFound tests error handling for non-existent service.
func TestNewAICmd_explicitServiceNotFound(t *testing.T) {
	projectRoot := t.TempDir()

	devboxYAML := `schema_version: "2"
project:
  name: test-project
services:
  api:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(projectRoot, "devbox.yml"), []byte(devboxYAML), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}

	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    dir: services/api
    container: test-api
`)

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newAICmd(flags)

	err := cmd.RunE(cmd, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found': %v", err)
	}
}

// TestNewAICmd_explicitServiceDisabled tests error for disabled service.
func TestNewAICmd_explicitServiceDisabled(t *testing.T) {
	projectRoot := t.TempDir()

	devboxYAML := `schema_version: "2"
project:
  name: test-project
services:
  api:
    enabled: false
`
	if err := os.WriteFile(filepath.Join(projectRoot, "devbox.yml"), []byte(devboxYAML), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}

	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    dir: services/api
    container: test-api
`)

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newAICmd(flags)

	err := cmd.RunE(cmd, []string{"api"})
	if err == nil {
		t.Fatal("expected error for disabled service")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error should mention 'disabled': %v", err)
	}
}

// TestNewAICmd_missingPack tests error when pack not found.
func TestNewAICmd_missingPack(t *testing.T) {
	projectRoot := t.TempDir()

	devboxYAML := `schema_version: "2"
project:
  name: test-project
services:
  api:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(projectRoot, "devbox.yml"), []byte(devboxYAML), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}

	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    dir: services/api
    container: test-api
`)

	// Create service directory but no template pack
	if err := os.MkdirAll(filepath.Join(projectRoot, "services", "api"), 0o755); err != nil {
		t.Fatalf("create service dir: %v", err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newAICmd(flags)

	err := cmd.RunE(cmd, []string{"api"})
	if err != nil {
		t.Fatalf("expected implicit missing pack to warn and skip, got error: %v", err)
	}
}

// TestNewAICmd_explicitPackMissing tests that an explicit template reference that doesn't exist produces an error.
func TestNewAICmd_explicitPackMissing(t *testing.T) {
	projectRoot := t.TempDir()

	devboxYAML := `schema_version: "2"
project:
  name: test-project
services:
  api:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(projectRoot, "devbox.yml"), []byte(devboxYAML), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}

	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    dir: services/api
    container: test-api
    render:
      ai:
        template: nonexistent
`)

	// Create service directory but no template pack
	if err := os.MkdirAll(filepath.Join(projectRoot, "services", "api"), 0o755); err != nil {
		t.Fatalf("create service dir: %v", err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newAICmd(flags)

	err := cmd.RunE(cmd, []string{"api"})
	if err == nil {
		t.Fatal("expected error for explicit missing template pack")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found': %v", err)
	}
}

// TestNewAICmd_explicitServiceNoDir tests error when service has no dir configured.
func TestNewAICmd_explicitServiceNoDir(t *testing.T) {
	projectRoot := t.TempDir()

	devboxYAML := `schema_version: "2"
project:
  name: test-project
services:
  api:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(projectRoot, "devbox.yml"), []byte(devboxYAML), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}

	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    container: test-api
`)

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newAICmd(flags)

	err := cmd.RunE(cmd, []string{"api"})
	if err == nil {
		t.Fatal("expected error for service with no dir")
	}
	if !strings.Contains(err.Error(), "no dir") {
		t.Errorf("error should mention 'no dir': %v", err)
	}
}

// TestSelectAgentsServices tests the service selection and collision resolution logic.
func TestSelectAgentsServices(t *testing.T) {
	falseVal := false
	trueVal := true

	tests := []struct {
		name           string
		services       map[string]config.ServiceConfig
		wantSelected   []string
		wantSkippedMap map[string]aipkg.SkippedService
	}{
		{
			name: "all enabled distinct dirs - all kept",
			services: map[string]config.ServiceConfig{
				"svc1": {Type: "app", Enabled: true, Dir: "./services/svc1"},
				"svc2": {Type: "db", Enabled: true, Dir: "./services/svc2"},
			},
			wantSelected: []string{"svc1", "svc2"},
		},
		{
			name: "service with Enabled=false dropped as service-disabled",
			services: map[string]config.ServiceConfig{
				"off": {Type: "app", Enabled: false, Dir: "./services/off"},
				"on":  {Type: "app", Enabled: true, Dir: "./services/on"},
			},
			wantSelected: []string{"on"},
			wantSkippedMap: map[string]aipkg.SkippedService{
				"off": {Name: "off", Reason: "service-disabled"},
			},
		},
		{
			name: "explicit ai.enabled=false drops service as ai-disabled",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app", Enabled: true, Dir: "./services/main"},
				"aux":  {Type: "app", Enabled: true, Dir: "./services/aux", Render: config.ServiceRenderConfig{AI: config.ServiceAIConfig{Enabled: &falseVal}}},
			},
			wantSelected: []string{"main"},
			wantSkippedMap: map[string]aipkg.SkippedService{
				"aux": {Name: "aux", Reason: "ai-disabled"},
			},
		},
		{
			name: "ai.enabled=true (explicit) keeps service",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app", Enabled: true, Dir: "./services/main", Render: config.ServiceRenderConfig{AI: config.ServiceAIConfig{Enabled: &trueVal}}},
			},
			wantSelected: []string{"main"},
		},
		{
			name: "service with empty dir dropped as empty-dir",
			services: map[string]config.ServiceConfig{
				"nodir": {Type: "app", Enabled: true, Dir: ""},
				"main":  {Type: "app", Enabled: true, Dir: "./services/main"},
			},
			wantSelected: []string{"main"},
			wantSkippedMap: map[string]aipkg.SkippedService{
				"nodir": {Name: "nodir", Reason: "empty-dir"},
			},
		},
		{
			name: "two services share dir - child extends parent - parent wins (canonical hub)",
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
			wantSelected: []string{"main"},
			wantSkippedMap: map[string]aipkg.SkippedService{
				"main-debug": {
					Name:   "main-debug",
					Reason: "lost-collision",
					Dir:    filepath.Join(".", "services", "main"),
					Winner: "main",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected, skipped := aipkg.SelectServices(tt.services)

			if len(selected) != len(tt.wantSelected) {
				t.Errorf("selected count: want %d, got %d (%v)", len(tt.wantSelected), len(selected), selected)
			}
			for i, name := range selected {
				if i < len(tt.wantSelected) && name != tt.wantSelected[i] {
					t.Errorf("selected[%d]: want %q, got %q", i, tt.wantSelected[i], name)
				}
			}

			skippedMap := make(map[string]aipkg.SkippedService)
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

// TestNewAICmd_existingRegularFile tests error when regular file exists at symlink target.
func TestNewAICmd_existingRegularFile(t *testing.T) {
	projectRoot := t.TempDir()

	devboxYAML := `schema_version: "2"
project:
  name: test-project
services:
  api:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(projectRoot, "devbox.yml"), []byte(devboxYAML), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}

	setupServicesConfig(t, projectRoot, `
services:
  api:
    type: app
    dir: services/api
    container: test-api
`)

	// Create template pack
	setupAgentsPackTemplates(t, projectRoot, "default", map[string]string{
		"manifest.yml":   "render:\n  - from: AGENTS.md.tmpl\n    to: AGENTS.md\nsymlinks:\n  - link: CLAUDE.md\n    to: AGENTS.md",
		"AGENTS.md.tmpl": "# Agents for {{ .Service }}",
	})

	// Create service directory with existing CLAUDE.md file
	hubDir := filepath.Join(projectRoot, "services", "api")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatalf("create service dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hubDir, "CLAUDE.md"), []byte("manual content"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newAICmd(flags)

	err := cmd.RunE(cmd, []string{"api"})
	if err == nil {
		t.Fatal("expected error for existing regular file")
	}
	if !strings.Contains(err.Error(), "refuse to overwrite") {
		t.Errorf("error should mention 'refuse to overwrite': %v", err)
	}
}

// TestResolveAIHubAnchor verifies hub-anchor resolution for agent docs: an
// explicit service name is treated as a hub anchor, and the AI-docs
// collision-policy winner among services sharing its dir (shallowest extends
// wins — canonical hub owner) is returned.
func TestResolveAIHubAnchor(t *testing.T) {
	falseVal := false

	tests := []struct {
		name     string
		input    string
		services map[string]config.ServiceConfig
		want     string
	}{
		{
			name:  "no siblings: input returned unchanged",
			input: "solo",
			services: map[string]config.ServiceConfig{
				"solo": {Type: "app", Enabled: true, Dir: "./services/solo"},
			},
			want: "solo",
		},
		{
			name:  "parent and child share dir, both enabled: parent (shallowest) wins",
			input: "main",
			services: map[string]config.ServiceConfig{
				"main":       {Type: "app", Enabled: true, Dir: "./services/main"},
				"main-debug": {Type: "app", Enabled: true, Dir: "./services/main", Extends: "main"},
			},
			want: "main",
		},
		{
			name:  "passing the variant name still resolves to the parent (canonical hub)",
			input: "main-debug",
			services: map[string]config.ServiceConfig{
				"main":       {Type: "app", Enabled: true, Dir: "./services/main"},
				"main-debug": {Type: "app", Enabled: true, Dir: "./services/main", Extends: "main"},
			},
			want: "main",
		},
		{
			name:  "parent disabled: variant becomes the only candidate",
			input: "main-debug",
			services: map[string]config.ServiceConfig{
				"main":       {Type: "app", Enabled: false, Dir: "./services/main"},
				"main-debug": {Type: "app", Enabled: true, Dir: "./services/main", Extends: "main"},
			},
			want: "main-debug",
		},
		{
			name:  "parent has ai.enabled=false: variant wins",
			input: "main-debug",
			services: map[string]config.ServiceConfig{
				"main":       {Type: "app", Enabled: true, Dir: "./services/main", Render: config.ServiceRenderConfig{AI: config.ServiceAIConfig{Enabled: &falseVal}}},
				"main-debug": {Type: "app", Enabled: true, Dir: "./services/main", Extends: "main"},
			},
			want: "main-debug",
		},
		{
			name:  "siblings in another dir do not affect resolution",
			input: "main",
			services: map[string]config.ServiceConfig{
				"main":   {Type: "app", Enabled: true, Dir: "./services/main"},
				"second": {Type: "app", Enabled: true, Dir: "./services/second"},
			},
			want: "main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAIHubAnchor(tt.input, tt.services)
			if got != tt.want {
				t.Errorf("resolveAIHubAnchor(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestRenderAgentsTemplateFile_cfgRawDotAccess verifies .Cfg.Raw dot syntax.
func TestRenderAgentsTemplateFile_cfgRawDotAccess(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "main")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAIPackTmpl(t, projectRoot, "template.tmpl",
		`prefix={{ .Cfg.Raw.git.project_prefix }};hook={{ index (index .Cfg.Raw.git.hooks .Service) "pre_commit" }}`)

	cfg := &config.DevboxConfig{Raw: map[string]any{
		"git": map[string]any{
			"project_prefix": "PRJ",
			"hooks": map[string]any{
				"main": map[string]any{"pre_commit": "echo hi"},
			},
		},
	}}
	data := aipkg.TemplateData{Service: "main", Cfg: cfg}
	if _, err := aipkg.RenderTemplateFile(projectRoot, "test", "template.tmpl", data, "AGENTS.md", hubDir, projectRoot); err != nil {
		t.Fatalf("render: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(hubDir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "PRJ") || !strings.Contains(string(got), "echo hi") {
		t.Errorf("content=%q", got)
	}
}

// TestRenderAgentsTemplateFile_cfgRawNonIdentifierKey verifies index escape hatch.
func TestRenderAgentsTemplateFile_cfgRawNonIdentifierKey(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "main")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAIPackTmpl(t, projectRoot, "template.tmpl",
		`token={{ index .Cfg.Raw "my-tool" "api-key" }}`)

	cfg := &config.DevboxConfig{Raw: map[string]any{
		"my-tool": map[string]any{"api-key": "VALUE"},
	}}
	data := aipkg.TemplateData{Service: "main", Cfg: cfg}
	if _, err := aipkg.RenderTemplateFile(projectRoot, "test", "template.tmpl", data, "AGENTS.md", hubDir, projectRoot); err != nil {
		t.Fatalf("render: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(hubDir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "VALUE") {
		t.Errorf("content=%q", got)
	}
}

// TestRenderAgentsTemplateFile_cfgRawMissingKey verifies missingkey=error surfaces typos.
func TestRenderAgentsTemplateFile_cfgRawMissingKey(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "main")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAIPackTmpl(t, projectRoot, "template.tmpl",
		`prefix={{ .Cfg.Raw.git.project_prefix }}`)

	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	data := aipkg.TemplateData{Service: "main", Cfg: cfg}
	_, err := aipkg.RenderTemplateFile(projectRoot, "test", "template.tmpl", data, "AGENTS.md", hubDir, projectRoot)
	if err == nil {
		t.Fatal("expected missingkey error")
	}
	if !strings.Contains(err.Error(), "git") {
		t.Errorf("expected error to mention 'git', got: %v", err)
	}
}

// TestRenderAgentsTemplateFile_backwardCompat verifies output byte-identical when
// templates do not reference .Cfg.
func TestRenderAgentsTemplateFile_backwardCompat(t *testing.T) {
	render := func(cfg *config.DevboxConfig) []byte {
		projectRoot := t.TempDir()
		hubDir := filepath.Join(projectRoot, "services", "main")
		if err := os.MkdirAll(hubDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeAIPackTmpl(t, projectRoot, "template.tmpl",
			`name={{ .Project.Name }};svc={{ .Service }}`)
		data := aipkg.TemplateData{
			Project: config.ProjectConfig{Name: "myapp"},
			Service: "main",
			Cfg:     cfg,
		}
		if _, err := aipkg.RenderTemplateFile(projectRoot, "test", "template.tmpl", data, "AGENTS.md", hubDir, projectRoot); err != nil {
			t.Fatalf("render: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(hubDir, "AGENTS.md"))
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	empty := render(&config.DevboxConfig{})
	populated := render(&config.DevboxConfig{Raw: map[string]any{"git": map[string]any{"project_prefix": "PRJ"}}})
	if string(empty) != string(populated) {
		t.Errorf("output diverged when template does not reference .Cfg:\nempty=%q\npopulated=%q", empty, populated)
	}
}
