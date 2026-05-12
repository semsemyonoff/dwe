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
