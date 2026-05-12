package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/config"
)

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

// TestEnsureRelativeSymlink_escapeLink rejects link escaping hub.
func TestEnsureRelativeSymlink_escapeLink(t *testing.T) {
	hubDir := t.TempDir()
	projectRoot := filepath.Dir(hubDir)

	_, err := ensureRelativeSymlink("../escape.md", "AGENTS.md", hubDir, projectRoot)
	if err == nil {
		t.Fatal("expected error for escaping link")
	}
	if !strings.Contains(err.Error(), "escape") {
		t.Errorf("error should mention escape: %v", err)
	}
}

// TestEnsureRelativeSymlink_escapeTarget rejects target escaping hub.
func TestEnsureRelativeSymlink_escapeTarget(t *testing.T) {
	hubDir := t.TempDir()
	projectRoot := filepath.Dir(hubDir)

	_, err := ensureRelativeSymlink("CLAUDE.md", "../escape.md", hubDir, projectRoot)
	if err == nil {
		t.Fatal("expected error for escaping target")
	}
	if !strings.Contains(err.Error(), "escape") {
		t.Errorf("error should mention escape: %v", err)
	}
}
