package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/config"
)

// setupServicesConfig writes a devbox/services.yml file with the given YAML content.
func setupServicesConfig(t *testing.T, dir, yaml string) {
	t.Helper()
	servicesDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(servicesDir, 0o755); err != nil {
		t.Fatalf("create devbox dir: %v", err)
	}
	path := filepath.Join(servicesDir, "services.yml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write services.yml: %v", err)
	}
}

// setupAgentsPackTemplates writes an agents template pack at <dir>/devbox/templates/agents/<packName>/
// and populates it with a directory structure of files.
func setupAgentsPackTemplates(t *testing.T, dir, packName string, files map[string]string) {
	t.Helper()
	packDir := filepath.Join(dir, "devbox", "templates", "agents", packName)
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
		AIDocs: config.ServiceAIDocsConfig{
			Template: "custom",
		},
	}

	pack, err := resolveAgentsTemplatePack(svc, projectRoot, "myservice")
	if err != nil {
		t.Fatalf("resolveAgentsTemplatePack: %v", err)
	}

	expected := filepath.Join(projectRoot, "devbox", "templates", "agents", "custom")
	if pack != expected {
		t.Errorf("expected %q, got %q", expected, pack)
	}
}

// TestResolveAgentsTemplatePack_explicitPackMissing verifies explicit missing pack is a hard error.
func TestResolveAgentsTemplatePack_explicitPackMissing(t *testing.T) {
	projectRoot := t.TempDir()

	svc := config.ServiceConfig{
		AIDocs: config.ServiceAIDocsConfig{
			Template: "missing",
		},
	}

	_, err := resolveAgentsTemplatePack(svc, projectRoot, "myservice")
	if err == nil {
		t.Fatal("expected error for missing explicit pack")
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
		AIDocs: config.ServiceAIDocsConfig{
			Template: "",
		},
	}

	pack, err := resolveAgentsTemplatePack(svc, projectRoot, "api")
	if err != nil {
		t.Fatalf("resolveAgentsTemplatePack: %v", err)
	}

	expected := filepath.Join(projectRoot, "devbox", "templates", "agents", "api")
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
		AIDocs: config.ServiceAIDocsConfig{
			Template: "",
		},
	}

	pack, err := resolveAgentsTemplatePack(svc, projectRoot, "notfound")
	if err != nil {
		t.Fatalf("resolveAgentsTemplatePack: %v", err)
	}

	expected := filepath.Join(projectRoot, "devbox", "templates", "agents", "default")
	if pack != expected {
		t.Errorf("expected %q, got %q", expected, pack)
	}
}

// TestResolveAgentsTemplatePack_implicitBothMissing verifies error when both candidates missing.
func TestResolveAgentsTemplatePack_implicitBothMissing(t *testing.T) {
	projectRoot := t.TempDir()

	svc := config.ServiceConfig{
		AIDocs: config.ServiceAIDocsConfig{
			Template: "",
		},
	}

	_, err := resolveAgentsTemplatePack(svc, projectRoot, "myservice")
	if err == nil {
		t.Fatal("expected error when both candidates missing")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention not found: %v", err)
	}
}

// TestResolveAgentsTemplatePack_symlinkedPackRejected verifies symlinks are rejected.
func TestResolveAgentsTemplatePack_symlinkedPackRejected(t *testing.T) {
	projectRoot := t.TempDir()
	realPack := filepath.Join(projectRoot, "real_agents")
	if err := os.MkdirAll(realPack, 0o755); err != nil {
		t.Fatalf("create real pack: %v", err)
	}

	templatesDir := filepath.Join(projectRoot, "devbox", "templates", "agents")
	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		t.Fatalf("create templates dir: %v", err)
	}

	symlinkPath := filepath.Join(templatesDir, "linked")
	if err := os.Symlink(realPack, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	svc := config.ServiceConfig{
		AIDocs: config.ServiceAIDocsConfig{
			Template: "linked",
		},
	}

	_, err := resolveAgentsTemplatePack(svc, projectRoot, "myservice")
	if err == nil {
		t.Fatal("expected error for symlinked pack")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink: %v", err)
	}
}

// TestResolveAgentsTemplatePack_nonDirPackRejected verifies non-directories are rejected.
func TestResolveAgentsTemplatePack_nonDirPackRejected(t *testing.T) {
	projectRoot := t.TempDir()
	templatesDir := filepath.Join(projectRoot, "devbox", "templates", "agents")
	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		t.Fatalf("create templates dir: %v", err)
	}

	filePath := filepath.Join(templatesDir, "file")
	if err := os.WriteFile(filePath, []byte("test"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	svc := config.ServiceConfig{
		AIDocs: config.ServiceAIDocsConfig{
			Template: "file",
		},
	}

	_, err := resolveAgentsTemplatePack(svc, projectRoot, "myservice")
	if err == nil {
		t.Fatal("expected error for non-dir pack")
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
				AIDocs: config.ServiceAIDocsConfig{
					Template: test.template,
				},
			}

			_, err := resolveAgentsTemplatePack(svc, projectRoot, "myservice")
			if err == nil {
				t.Fatalf("expected error for %s", test.label)
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
				AIDocs: config.ServiceAIDocsConfig{
					Template: "",
				},
			}

			_, err := resolveAgentsTemplatePack(svc, projectRoot, test.serviceName)
			if err == nil {
				t.Fatalf("expected error for %s", test.label)
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
		AIDocs: config.ServiceAIDocsConfig{
			Template: "",
		},
	}

	pack, err := resolveAgentsTemplatePack(svc, projectRoot, "myapi")
	if err != nil {
		t.Fatalf("resolveAgentsTemplatePack: %v", err)
	}

	// Should pick the service-name pack, not the default
	expected := filepath.Join(projectRoot, "devbox", "templates", "agents", "myapi")
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

	m, err := loadAgentsManifest(packDir)
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

	_, err := loadAgentsManifest(packDir)
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

	_, err := loadAgentsManifest(packDir)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

// TestValidateAgentsManifest_empty rejects empty manifest.
func TestValidateAgentsManifest_empty(t *testing.T) {
	m := &agentsManifest{}
	err := validateAgentsManifest(m, t.TempDir())
	if err == nil {
		t.Fatal("expected error for empty manifest")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty: %v", err)
	}
}

// TestValidateAgentsManifest_fromEscaping rejects `from` escaping pack dir.
func TestValidateAgentsManifest_fromEscaping(t *testing.T) {
	packDir := t.TempDir()
	// Create a file outside the pack
	outsideDir := filepath.Dir(packDir)
	outsideFile := filepath.Join(outsideDir, "outside.tmpl")
	if err := os.WriteFile(outsideFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	m := &agentsManifest{
		Render: []agentsRenderEntry{
			{From: "../outside.tmpl", To: "test"},
		},
	}

	err := validateAgentsManifest(m, packDir)
	if err == nil {
		t.Fatal("expected error for escaping from")
	}
	if !strings.Contains(err.Error(), "escape") {
		t.Errorf("error should mention escape: %v", err)
	}
}

// TestValidateAgentsManifest_fromNoTmplSuffix rejects `from` not ending in .tmpl.
func TestValidateAgentsManifest_fromNoTmplSuffix(t *testing.T) {
	packDir := t.TempDir()
	file := filepath.Join(packDir, "test.txt")
	if err := os.WriteFile(file, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	m := &agentsManifest{
		Render: []agentsRenderEntry{
			{From: "test.txt", To: "test"},
		},
	}

	err := validateAgentsManifest(m, packDir)
	if err == nil {
		t.Fatal("expected error for non-.tmpl from")
	}
	if !strings.Contains(err.Error(), ".tmpl") {
		t.Errorf("error should mention .tmpl: %v", err)
	}
}

// TestValidateAgentsManifest_fromNotExist rejects non-existent `from` file.
func TestValidateAgentsManifest_fromNotExist(t *testing.T) {
	m := &agentsManifest{
		Render: []agentsRenderEntry{
			{From: "missing.tmpl", To: "test"},
		},
	}

	err := validateAgentsManifest(m, t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing from file")
	}
	if !strings.Contains(err.Error(), "not exist") {
		t.Errorf("error should mention not exist: %v", err)
	}
}

// TestValidateAgentsManifest_fromIsSymlink rejects `from` being a symlink.
func TestValidateAgentsManifest_fromIsSymlink(t *testing.T) {
	packDir := t.TempDir()
	targetFile := filepath.Join(packDir, "target.tmpl")
	if err := os.WriteFile(targetFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	linkPath := filepath.Join(packDir, "link.tmpl")
	if err := os.Symlink(targetFile, linkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	m := &agentsManifest{
		Render: []agentsRenderEntry{
			{From: "link.tmpl", To: "test"},
		},
	}

	err := validateAgentsManifest(m, packDir)
	if err == nil {
		t.Fatal("expected error for symlink from")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink: %v", err)
	}
}

// TestValidateAgentsManifest_fromSymlinkedParent rejects `from` with symlinked parent.
func TestValidateAgentsManifest_fromSymlinkedParent(t *testing.T) {
	packDir := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "evil.tmpl")
	if err := os.WriteFile(outsideFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	symDir := filepath.Join(packDir, "symdir")
	if err := os.Symlink(outsideDir, symDir); err != nil {
		t.Fatalf("create symlink dir: %v", err)
	}

	m := &agentsManifest{
		Render: []agentsRenderEntry{
			{From: "symdir/evil.tmpl", To: "test"},
		},
	}

	err := validateAgentsManifest(m, packDir)
	if err == nil {
		t.Fatal("expected error for symlinked parent directory")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink: %v", err)
	}
}

// TestValidateAgentsManifest_fromIsDirectory rejects `from` being a directory.
func TestValidateAgentsManifest_fromIsDirectory(t *testing.T) {
	packDir := t.TempDir()
	subdir := filepath.Join(packDir, "subdir.tmpl")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("create subdir: %v", err)
	}

	m := &agentsManifest{
		Render: []agentsRenderEntry{
			{From: "subdir.tmpl", To: "test"},
		},
	}

	err := validateAgentsManifest(m, packDir)
	if err == nil {
		t.Fatal("expected error for directory from")
	}
	if !strings.Contains(err.Error(), "regular file") {
		t.Errorf("error should mention regular file: %v", err)
	}
}

// TestValidateAgentsManifest_toEscaping rejects `to` escaping hub dir.
func TestValidateAgentsManifest_toEscaping(t *testing.T) {
	packDir := t.TempDir()
	file := filepath.Join(packDir, "test.tmpl")
	if err := os.WriteFile(file, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	m := &agentsManifest{
		Render: []agentsRenderEntry{
			{From: "test.tmpl", To: "../escape"},
		},
	}

	err := validateAgentsManifest(m, packDir)
	if err == nil {
		t.Fatal("expected error for escaping to")
	}
	if !strings.Contains(err.Error(), "escape") {
		t.Errorf("error should mention escape: %v", err)
	}
}

// TestValidateAgentsManifest_symlinkToNotMatching rejects symlink `to` not matching render.
func TestValidateAgentsManifest_symlinkToNotMatching(t *testing.T) {
	packDir := t.TempDir()
	file := filepath.Join(packDir, "test.tmpl")
	if err := os.WriteFile(file, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	m := &agentsManifest{
		Render: []agentsRenderEntry{
			{From: "test.tmpl", To: "AGENTS.md"},
		},
		Symlinks: []agentsSymlinkEntry{
			{Link: "CLAUDE.md", To: "nonexistent.md"},
		},
	}

	err := validateAgentsManifest(m, packDir)
	if err == nil {
		t.Fatal("expected error for symlink to not matching render")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("error should mention not matching: %v", err)
	}
}

// TestValidateAgentsManifest_duplicateRenderDest rejects duplicate render destinations.
func TestValidateAgentsManifest_duplicateRenderDest(t *testing.T) {
	packDir := t.TempDir()
	file1 := filepath.Join(packDir, "file1.tmpl")
	file2 := filepath.Join(packDir, "file2.tmpl")
	if err := os.WriteFile(file1, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file2: %v", err)
	}

	m := &agentsManifest{
		Render: []agentsRenderEntry{
			{From: "file1.tmpl", To: "AGENTS.md"},
			{From: "file2.tmpl", To: "AGENTS.md"},
		},
	}

	err := validateAgentsManifest(m, packDir)
	if err == nil {
		t.Fatal("expected error for duplicate render destination")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention duplicate: %v", err)
	}
}

// TestValidateAgentsManifest_duplicateSymlinkLink rejects duplicate symlink links.
func TestValidateAgentsManifest_duplicateSymlinkLink(t *testing.T) {
	packDir := t.TempDir()
	file := filepath.Join(packDir, "test.tmpl")
	if err := os.WriteFile(file, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	m := &agentsManifest{
		Render: []agentsRenderEntry{
			{From: "test.tmpl", To: "AGENTS.md"},
		},
		Symlinks: []agentsSymlinkEntry{
			{Link: "CLAUDE.md", To: "AGENTS.md"},
			{Link: "CLAUDE.md", To: "AGENTS.md"},
		},
	}

	err := validateAgentsManifest(m, packDir)
	if err == nil {
		t.Fatal("expected error for duplicate symlink link")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention duplicate: %v", err)
	}
}

// TestValidateAgentsManifest_nestedPaths allows nested `to`/`link` paths.
func TestValidateAgentsManifest_nestedPaths(t *testing.T) {
	packDir := t.TempDir()
	file := filepath.Join(packDir, "test.tmpl")
	if err := os.WriteFile(file, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	m := &agentsManifest{
		Render: []agentsRenderEntry{
			{From: "test.tmpl", To: ".claude/AGENTS.md"},
		},
		Symlinks: []agentsSymlinkEntry{
			{Link: ".claude/CLAUDE.md", To: ".claude/AGENTS.md"},
		},
	}

	err := validateAgentsManifest(m, packDir)
	if err != nil {
		t.Errorf("nested paths should be allowed: %v", err)
	}
}

// TestValidateAgentsManifest_validFull tests a complete valid manifest.
func TestValidateAgentsManifest_validFull(t *testing.T) {
	packDir := t.TempDir()
	file1 := filepath.Join(packDir, "agents.tmpl")
	file2 := filepath.Join(packDir, "claude.tmpl")
	if err := os.WriteFile(file1, []byte("agents"), 0o644); err != nil {
		t.Fatalf("write file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte("claude"), 0o644); err != nil {
		t.Fatalf("write file2: %v", err)
	}

	m := &agentsManifest{
		Render: []agentsRenderEntry{
			{From: "agents.tmpl", To: "AGENTS.md"},
			{From: "claude.tmpl", To: "CLAUDE.md"},
		},
		Symlinks: []agentsSymlinkEntry{
			{Link: ".claude/AGENTS.md", To: "AGENTS.md"},
		},
	}

	err := validateAgentsManifest(m, packDir)
	if err != nil {
		t.Errorf("valid manifest should not error: %v", err)
	}
}

// TestRenderAgentsTemplateFile_fresh writes a fresh template file.
func TestRenderAgentsTemplateFile_fresh(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "api")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatalf("create hub dir: %v", err)
	}

	// Create a template file
	templateContent := "Service: {{ .Service }}, Project: {{ .Project.Name }}"
	templatePath := filepath.Join(t.TempDir(), "template.tmpl")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	data := agentsTemplateData{
		Project: config.ProjectConfig{Name: "myproject"},
		Service: "api",
	}

	dest := "AGENTS.md"
	err := renderAgentsTemplateFile(templatePath, data, dest, hubDir, projectRoot)
	if err != nil {
		t.Fatalf("renderAgentsTemplateFile: %v", err)
	}

	// Verify file was written
	resultPath := filepath.Join(hubDir, dest)
	content, err := os.ReadFile(resultPath)
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

	templateContent := "Content: {{ .Service }}"
	templatePath := filepath.Join(t.TempDir(), "template.tmpl")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	data := agentsTemplateData{Service: "api"}
	dest := "AGENTS.md"

	// First render
	if err := renderAgentsTemplateFile(templatePath, data, dest, hubDir, projectRoot); err != nil {
		t.Fatalf("first render: %v", err)
	}

	resultPath := filepath.Join(hubDir, dest)
	info1, err := os.Stat(resultPath)
	if err != nil {
		t.Fatalf("stat after first render: %v", err)
	}

	// Second render (idempotent)
	if err := renderAgentsTemplateFile(templatePath, data, dest, hubDir, projectRoot); err != nil {
		t.Fatalf("second render: %v", err)
	}

	info2, err := os.Stat(resultPath)
	if err != nil {
		t.Fatalf("stat after second render: %v", err)
	}

	// File should exist and be overwritten
	if !info1.Mode().IsRegular() || !info2.Mode().IsRegular() {
		t.Error("expected regular files")
	}

	// Content should match template rendering
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

	templateContent := "Nested"
	templatePath := filepath.Join(t.TempDir(), "template.tmpl")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	data := agentsTemplateData{}
	dest := ".claude/AGENTS.md"

	err := renderAgentsTemplateFile(templatePath, data, dest, hubDir, projectRoot)
	if err != nil {
		t.Fatalf("renderAgentsTemplateFile: %v", err)
	}

	resultPath := filepath.Join(hubDir, dest)
	if _, err := os.Stat(resultPath); err != nil {
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

	templatePath := filepath.Join(t.TempDir(), "template.tmpl")
	if err := os.WriteFile(templatePath, []byte("test"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	data := agentsTemplateData{}
	dest := "../escape.md"

	err := renderAgentsTemplateFile(templatePath, data, dest, hubDir, projectRoot)
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

	// Create a symlink at services/api/.claude → /tmp/somewhere
	symlinkDir := filepath.Join(hubDir, ".claude")
	realTarget := t.TempDir()
	if err := os.Symlink(realTarget, symlinkDir); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	templatePath := filepath.Join(t.TempDir(), "template.tmpl")
	if err := os.WriteFile(templatePath, []byte("test"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	data := agentsTemplateData{}
	dest := ".claude/AGENTS.md"

	err := renderAgentsTemplateFile(templatePath, data, dest, hubDir, projectRoot)
	if err == nil {
		t.Fatal("expected error when destination dir contains a symlink component")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink: %v", err)
	}
}

// TestEnsureRelativeSymlink_fresh creates a new symlink.
func TestEnsureRelativeSymlink_fresh(t *testing.T) {
	hubDir := t.TempDir()
	projectRoot := filepath.Dir(hubDir)

	changed, err := ensureRelativeSymlink("CLAUDE.md", "AGENTS.md", hubDir, projectRoot)
	if err != nil {
		t.Fatalf("ensureRelativeSymlink: %v", err)
	}

	if !changed {
		t.Error("expected changed=true for new symlink")
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

// TestEnsureRelativeSymlink_idempotent returns false when symlink already correct.
func TestEnsureRelativeSymlink_idempotent(t *testing.T) {
	hubDir := t.TempDir()
	projectRoot := filepath.Dir(hubDir)

	// First creation
	changed1, err := ensureRelativeSymlink("CLAUDE.md", "AGENTS.md", hubDir, projectRoot)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !changed1 {
		t.Error("first call should return changed=true")
	}

	// Second call (idempotent)
	changed2, err := ensureRelativeSymlink("CLAUDE.md", "AGENTS.md", hubDir, projectRoot)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if changed2 {
		t.Error("second call should return changed=false")
	}
}

// TestEnsureRelativeSymlink_targetChanged replaces when target changes.
func TestEnsureRelativeSymlink_targetChanged(t *testing.T) {
	hubDir := t.TempDir()
	projectRoot := filepath.Dir(hubDir)

	// Create initial symlink
	if _, err := ensureRelativeSymlink("LINK.md", "OLD.md", hubDir, projectRoot); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Change target
	changed, err := ensureRelativeSymlink("LINK.md", "NEW.md", hubDir, projectRoot)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if !changed {
		t.Error("expected changed=true when target changed")
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

	_, err := ensureRelativeSymlink("CLAUDE.md", "AGENTS.md", hubDir, projectRoot)
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

	changed, err := ensureRelativeSymlink(".claude/CLAUDE.md", "AGENTS.md", hubDir, projectRoot)
	if err != nil {
		t.Fatalf("ensureRelativeSymlink: %v", err)
	}

	if !changed {
		t.Error("expected changed=true for new symlink")
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
	_, err := ensureRelativeSymlink("../escape.md", "AGENTS.md", hubDir, projectRoot)
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
	_, err := ensureRelativeSymlink("CLAUDE.md", "../outside.md", hubDir, projectRoot)
	if err == nil {
		t.Fatal("expected error for target escaping hub, got nil")
	}
}

// TestNewRenderAICmd_happyPath tests the full command flow with a single service.
func TestNewRenderAICmd_happyPath(t *testing.T) {
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

	flags := &rootFlags{configPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newRenderAICmd(flags)

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

// TestNewRenderAICmd_explicitServiceAIDocsDisabled tests error when ai_docs.enabled: false.
func TestNewRenderAICmd_explicitServiceAIDocsDisabled(t *testing.T) {
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
    ai_docs:
      enabled: false
`)

	flags := &rootFlags{configPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newRenderAICmd(flags)

	err := cmd.RunE(cmd, []string{"api"})
	if err == nil {
		t.Fatal("expected error for ai_docs.enabled: false")
	}
	if !strings.Contains(err.Error(), "ai_docs.enabled") {
		t.Errorf("error should mention 'ai_docs.enabled': %v", err)
	}
}

// TestNewRenderAICmd_noArgAutoSelection tests auto-selection without explicit service.
func TestNewRenderAICmd_noArgAutoSelection(t *testing.T) {
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
    ai_docs:
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

	flags := &rootFlags{configPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newRenderAICmd(flags)

	// Command should succeed even though no-dir-svc is skipped with a warning
	if err := cmd.RunE(cmd, []string{}); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	// Only enabled-svc should have rendered files
	enabledPath := filepath.Join(projectRoot, "services", "enabled", "AGENTS.md")
	if _, err := os.Stat(enabledPath); err != nil {
		t.Fatalf("expected AGENTS.md in enabled service: %v", err)
	}

	// ai-disabled-svc should not have files (ai_docs.enabled: false)
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

// TestNewRenderAICmd_explicitServiceNotFound tests error handling for non-existent service.
func TestNewRenderAICmd_explicitServiceNotFound(t *testing.T) {
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

	flags := &rootFlags{configPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newRenderAICmd(flags)

	err := cmd.RunE(cmd, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found': %v", err)
	}
}

// TestNewRenderAICmd_explicitServiceDisabled tests error for disabled service.
func TestNewRenderAICmd_explicitServiceDisabled(t *testing.T) {
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

	flags := &rootFlags{configPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newRenderAICmd(flags)

	err := cmd.RunE(cmd, []string{"api"})
	if err == nil {
		t.Fatal("expected error for disabled service")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error should mention 'disabled': %v", err)
	}
}

// TestNewRenderAICmd_missingPack tests error when pack not found.
func TestNewRenderAICmd_missingPack(t *testing.T) {
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

	flags := &rootFlags{configPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newRenderAICmd(flags)

	err := cmd.RunE(cmd, []string{"api"})
	if err == nil {
		t.Fatal("expected error for missing pack")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found': %v", err)
	}
}

// TestNewRenderAICmd_explicitServiceNoDir tests error when service has no dir configured.
func TestNewRenderAICmd_explicitServiceNoDir(t *testing.T) {
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

	flags := &rootFlags{configPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newRenderAICmd(flags)

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
		wantSkippedMap map[string]skippedService
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
			wantSkippedMap: map[string]skippedService{
				"off": {Name: "off", Reason: "service-disabled"},
			},
		},
		{
			name: "explicit ai_docs.enabled=false drops service as ai-disabled",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app", Enabled: true, Dir: "./services/main"},
				"aux":  {Type: "app", Enabled: true, Dir: "./services/aux", AIDocs: config.ServiceAIDocsConfig{Enabled: &falseVal}},
			},
			wantSelected: []string{"main"},
			wantSkippedMap: map[string]skippedService{
				"aux": {Name: "aux", Reason: "ai-disabled"},
			},
		},
		{
			name: "ai_docs.enabled=true (explicit) keeps service",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app", Enabled: true, Dir: "./services/main", AIDocs: config.ServiceAIDocsConfig{Enabled: &trueVal}},
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
			wantSkippedMap: map[string]skippedService{
				"nodir": {Name: "nodir", Reason: "empty-dir"},
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
			selected, skipped := selectAgentsServices(tt.services)

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

// TestNewRenderAICmd_existingRegularFile tests error when regular file exists at symlink target.
func TestNewRenderAICmd_existingRegularFile(t *testing.T) {
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

	flags := &rootFlags{configPath: filepath.Join(projectRoot, "devbox.yml")}
	cmd := newRenderAICmd(flags)

	err := cmd.RunE(cmd, []string{"api"})
	if err == nil {
		t.Fatal("expected error for existing regular file")
	}
	if !strings.Contains(err.Error(), "refuse to overwrite") {
		t.Errorf("error should mention 'refuse to overwrite': %v", err)
	}
}
