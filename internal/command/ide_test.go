package command

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/pathsafe"
	"devbox-cli/internal/render"
)

// Minimal inline templates used by renderIDETemplateFile unit tests.
const minimalDevcontainerTpl = `{"name":"{{ .Project.Name }}","service":"{{ .ServiceCfg.Container }}","workspaceFolder":"{{ .ServiceCfg.DirInternal }}","forwardPorts":[{{ .Runtime.Ports.app }}]}`
const minimalVscodeLaunchTpl = `{"type":"php","pathMappings":{"{{ .ServiceCfg.WorkDirInternal }}":"${workspaceFolder}/src"}}`
const minimalVscodeSettingsTpl = `{"php.validate.executablePath":"/usr/local/bin/php","editor.formatOnSave":true}`

// setupIDEPackTemplates writes an IDE template pack at <dir>/devbox/templates/ide/<packName>/
// and populates it with a directory structure of .tmpl files.
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
			Ports: config.RuntimePorts{"app": 80},
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
			Ports: config.RuntimePorts{"app": 8080},
		},
	}
	projectRoot := t.TempDir()
	absRoot, _ := filepath.Abs(projectRoot)
	absDir, _ := filepath.Abs(projectRoot)

	// Write template file
	srcPath := filepath.Join(projectRoot, "devcontainer.json.tmpl")
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

	srcPath := filepath.Join(projectRoot, "template.tmpl")
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

	srcPath := filepath.Join(projectRoot, "template.tmpl")
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

	srcPath := filepath.Join(projectRoot, "template.tmpl")
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

	srcPath := filepath.Join(projectRoot, "template.tmpl")
	if err := os.WriteFile(srcPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	dest := filepath.Join(svcDir, ".devcontainer", "devcontainer.json")
	err := renderIDETemplateFile(srcPath, data, dest, absDir, absRoot)
	if err == nil {
		t.Fatal("expected error when destination dir is a symlink outside project root")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink error, got: %v", err)
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

	srcPath := filepath.Join(projectRoot, "template.tmpl")
	if err := os.WriteFile(srcPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	err := renderIDETemplateFile(srcPath, data, dest, absDir, absRoot)
	if err == nil {
		t.Fatal("expected error when destination file is a symlink")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink error, got: %v", err)
	}
}

// TestResolveIDETemplatePack_explicit verifies explicit pack resolution.
func TestResolveIDETemplatePack_explicit(t *testing.T) {
	projectRoot := t.TempDir()

	// Create packs
	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".devcontainer/devcontainer.json.tmpl": "default-dc",
	})
	setupIDEPackTemplates(t, projectRoot, "custom", map[string]string{
		".devcontainer/devcontainer.json.tmpl": "custom-dc",
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
		".devcontainer/devcontainer.json.tmpl": "default-dc",
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

// TestResolveIDETemplatePack_allMissing verifies that an error is returned when
// neither the service-name pack nor the default pack exists.
func TestResolveIDETemplatePack_allMissing(t *testing.T) {
	projectRoot := t.TempDir()
	// No packs set up at all

	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		IDE:     config.ServiceIDEConfig{},
	}

	_, err := resolveIDETemplatePack(svc, projectRoot, "myservice")
	if err == nil {
		t.Fatal("expected error when no packs exist, got nil")
	}
}

// TestResolveIDETemplatePack_implicitPriority verifies that a service-name pack
// takes precedence over the default pack when both exist on disk.
func TestResolveIDETemplatePack_implicitPriority(t *testing.T) {
	projectRoot := t.TempDir()

	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".vscode/settings.json.tmpl": `{"source":"default"}`,
	})
	setupIDEPackTemplates(t, projectRoot, "main", map[string]string{
		".vscode/settings.json.tmpl": `{"source":"main"}`,
	})

	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		IDE:     config.ServiceIDEConfig{},
	}

	pack, err := resolveIDETemplatePack(svc, projectRoot, "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(pack, "main") {
		t.Errorf("want pack ending with 'main', got %q (service-name pack should beat default)", pack)
	}
}

// TestResolveIDETemplatePack_explicitStrictSemantics verifies that explicit
// template does not fall back to default even if default exists.
func TestResolveIDETemplatePack_explicitStrictSemantics(t *testing.T) {
	projectRoot := t.TempDir()

	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".devcontainer/devcontainer.json.tmpl": "default-dc",
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

// TestResolveIDETemplatePack_explicitPackIsFile verifies that an explicit pack that
// is a regular file (not a directory) is rejected.
func TestResolveIDETemplatePack_explicitPackIsFile(t *testing.T) {
	projectRoot := t.TempDir()

	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create ide dir: %v", err)
	}
	// Create a regular file where a pack directory is expected
	if err := os.WriteFile(filepath.Join(packDir, "custom"), []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		IDE:     config.ServiceIDEConfig{Template: "custom"},
	}

	_, err := resolveIDETemplatePack(svc, projectRoot, "main")
	if err == nil {
		t.Fatal("expected error when explicit pack is a regular file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("expected 'not a directory' error, got: %v", err)
	}
}

// TestResolveIDETemplatePack_implicitCandidateIsFile verifies that when a service-name
// candidate is a regular file (not a directory), it is rejected — no fallthrough to default.
func TestResolveIDETemplatePack_implicitCandidateIsFile(t *testing.T) {
	projectRoot := t.TempDir()

	// Set up default pack
	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".devcontainer/devcontainer.json.tmpl": "default-dc",
	})

	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide")
	// Create a regular file where "main" pack directory is expected
	if err := os.WriteFile(filepath.Join(packDir, "main"), []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		IDE:     config.ServiceIDEConfig{},
	}

	_, err := resolveIDETemplatePack(svc, projectRoot, "main")
	if err == nil {
		t.Fatal("expected error when service-name candidate is a regular file (no fallthrough)")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("expected 'not a directory' error, got: %v", err)
	}
}

// TestResolveIDETemplatePack_implicitCandidateIsSymlink verifies that when a service-name
// candidate is a symlink to a directory, it is rejected — no fallthrough to default.
func TestResolveIDETemplatePack_implicitCandidateIsSymlink(t *testing.T) {
	projectRoot := t.TempDir()

	// Set up default pack and a real directory to link to
	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".devcontainer/devcontainer.json.tmpl": "default-dc",
	})
	realDir := t.TempDir()

	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide")
	symlinkPath := filepath.Join(packDir, "main")
	if err := os.Symlink(realDir, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		IDE:     config.ServiceIDEConfig{},
	}

	_, err := resolveIDETemplatePack(svc, projectRoot, "main")
	if err == nil {
		t.Fatal("expected error when service-name candidate is a symlink")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink error, got: %v", err)
	}
}

// TestResolveIDETemplatePack_defaultIsFile verifies that when default/ is a regular
// file it is rejected as a hard error.
func TestResolveIDETemplatePack_defaultIsFile(t *testing.T) {
	projectRoot := t.TempDir()

	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create ide dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "default"), []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		IDE:     config.ServiceIDEConfig{},
	}

	_, err := resolveIDETemplatePack(svc, projectRoot, "unknown-service")
	if err == nil {
		t.Fatal("expected error when default/ is a regular file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("expected 'not a directory' error, got: %v", err)
	}
}

// TestResolveIDETemplatePack_defaultIsSymlink verifies that when default/ is a symlink
// to a directory it is rejected as a hard error.
func TestResolveIDETemplatePack_defaultIsSymlink(t *testing.T) {
	projectRoot := t.TempDir()
	realDir := t.TempDir()

	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create ide dir: %v", err)
	}
	if err := os.Symlink(realDir, filepath.Join(packDir, "default")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		IDE:     config.ServiceIDEConfig{},
	}

	_, err := resolveIDETemplatePack(svc, projectRoot, "unknown-service")
	if err == nil {
		t.Fatal("expected error when default/ is a symlink")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink error, got: %v", err)
	}
}

// TestResolveIDETemplatePack_relativeProjectRoot verifies that passing a relative
// projectRoot still produces an absolute pack path.
func TestResolveIDETemplatePack_relativeProjectRoot(t *testing.T) {
	projectRoot := t.TempDir()

	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".devcontainer/devcontainer.json.tmpl": "dc",
	})

	// Compute a relative path to projectRoot from cwd
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	relRoot, err := filepath.Rel(cwd, projectRoot)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}

	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		IDE:     config.ServiceIDEConfig{},
	}

	pack, err := resolveIDETemplatePack(svc, relRoot, "unknown")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(pack) {
		t.Errorf("want absolute pack path, got %q", pack)
	}
}

// TestResolveIDETemplatePack_byServiceOnly verifies that when only the service-name
// pack exists (no default/), it is resolved without error.
func TestResolveIDETemplatePack_byServiceOnly(t *testing.T) {
	projectRoot := t.TempDir()

	// Set up only the service-name pack, no default/
	setupIDEPackTemplates(t, projectRoot, "main", map[string]string{
		".vscode/settings.json.tmpl": `{"source":"main"}`,
	})

	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		IDE:     config.ServiceIDEConfig{},
	}

	pack, err := resolveIDETemplatePack(svc, projectRoot, "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(pack, "main") {
		t.Errorf("want pack ending with 'main', got %q", pack)
	}
}

// TestResolveIDETemplatePack_invalidServiceName verifies that an invalid service name
// (e.g. containing a path separator) causes an error at the resolver level.
func TestResolveIDETemplatePack_invalidServiceName(t *testing.T) {
	projectRoot := t.TempDir()

	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		IDE:     config.ServiceIDEConfig{},
	}

	_, err := resolveIDETemplatePack(svc, projectRoot, "foo/bar")
	if err == nil {
		t.Fatal("want error for invalid service name, got nil")
	}
	if !strings.Contains(err.Error(), "cannot be used as implicit template pack key") {
		t.Errorf("want error mentioning template pack key, got %q", err.Error())
	}
}

// TestResolveIDETemplatePack_emptyServiceName verifies that an empty service name
// is rejected at the resolver level (not silently collapsed by filepath.Join).
func TestResolveIDETemplatePack_emptyServiceName(t *testing.T) {
	projectRoot := t.TempDir()

	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		IDE:     config.ServiceIDEConfig{},
	}

	_, err := resolveIDETemplatePack(svc, projectRoot, "")
	if err == nil {
		t.Fatal("want error for empty service name, got nil")
	}
	if !strings.Contains(err.Error(), "service name is empty") {
		t.Errorf("want error mentioning empty service name, got %q", err.Error())
	}
}

// TestResolveIDETemplatePack_leadingDotServiceName verifies that a service name with a
// leading dot is allowed (leading dots are valid YAML map keys, unlike ide.template values).
func TestResolveIDETemplatePack_leadingDotServiceName(t *testing.T) {
	projectRoot := t.TempDir()

	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/hidden",
		IDE:     config.ServiceIDEConfig{},
	}

	// No packs exist; we expect "not found" wrapping os.ErrNotExist, not a validation error.
	_, err := resolveIDETemplatePack(svc, projectRoot, ".hidden")
	if err == nil {
		t.Fatal("want error (no pack found), got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want os.ErrNotExist for missing pack, got %v", err)
	}
}

// TestResolveIDETemplatePack_invalidExplicitTemplateKey verifies that an explicit
// ide.template value containing a path separator is rejected before any filesystem lookup.
func TestResolveIDETemplatePack_invalidExplicitTemplateKey(t *testing.T) {
	projectRoot := t.TempDir()

	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		IDE:     config.ServiceIDEConfig{Template: "foo/bar"},
	}

	_, err := resolveIDETemplatePack(svc, projectRoot, "main")
	if err == nil {
		t.Fatal("want error for invalid ide.template key, got nil")
	}
	if !strings.Contains(err.Error(), "invalid ide.template") {
		t.Errorf("want error mentioning 'invalid ide.template', got %q", err.Error())
	}
}

// TestResolveIDETemplatePack_explicitOnlyPack verifies that when only the explicit pack
// exists (no service-name or default pack), it resolves correctly.
func TestResolveIDETemplatePack_explicitOnlyPack(t *testing.T) {
	projectRoot := t.TempDir()

	// Only set up the explicit pack; no service-name pack, no default pack.
	setupIDEPackTemplates(t, projectRoot, "custom", map[string]string{
		".vscode/settings.json.tmpl": `{"source":"custom"}`,
	})

	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		IDE:     config.ServiceIDEConfig{Template: "custom"},
	}

	pack, err := resolveIDETemplatePack(svc, projectRoot, "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(pack, "custom") {
		t.Errorf("want pack ending with 'custom', got %q", pack)
	}
}

// TestResolveIDETemplatePack_explicitBeatsServiceNameAndDefault verifies that when all
// three packs exist (explicit, service-name, and default), the explicit pack wins.
func TestResolveIDETemplatePack_explicitBeatsServiceNameAndDefault(t *testing.T) {
	projectRoot := t.TempDir()

	setupIDEPackTemplates(t, projectRoot, "main", map[string]string{
		".vscode/settings.json.tmpl": `{"source":"main"}`,
	})
	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".vscode/settings.json.tmpl": `{"source":"default"}`,
	})
	setupIDEPackTemplates(t, projectRoot, "custom", map[string]string{
		".vscode/settings.json.tmpl": `{"source":"custom"}`,
	})

	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		IDE:     config.ServiceIDEConfig{Template: "custom"},
	}

	pack, err := resolveIDETemplatePack(svc, projectRoot, "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(pack, "custom") {
		t.Errorf("want explicit 'custom' pack, got %q", pack)
	}
}

// TestWalkIDEPack_noDuplicateRelPath verifies that a well-formed pack with unique
// RelPaths walks without error. A true RelPath collision cannot arise on a
// case-sensitive filesystem (two files cannot share the same name in the same dir),
// so this test confirms the duplicate guard doesn't break the happy path.
func TestWalkIDEPack_noDuplicateRelPath(t *testing.T) {
	packDir := t.TempDir()

	files := map[string]string{
		"foo.tmpl":     "a",
		"bar/baz.tmpl": "b",
	}
	for rel, content := range files {
		full := filepath.Join(packDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	entries, err := walkIDEPack(packDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("want 2 entries, got %d", len(entries))
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
		".devcontainer/devcontainer.json.tmpl": "dc",
		".vscode/settings.json.tmpl":           "vs-settings",
		".vscode/launch.json.tmpl":             "vs-launch",
		".idea/custom.xml.tmpl":                "idea",
		".zed/settings.json.tmpl":              "zed-settings",
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

// TestWalkIDEPack_nonTmplFilesSkipped verifies that non-.tmpl files are silently skipped.
func TestWalkIDEPack_nonTmplFilesSkipped(t *testing.T) {
	projectRoot := t.TempDir()
	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide", "default")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create pack dir: %v", err)
	}

	// Create both .tmpl and non-.tmpl files
	if err := os.WriteFile(filepath.Join(packDir, "file.tmpl"), []byte("tpl"), 0o644); err != nil {
		t.Fatalf("write .tmpl: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "file.txt"), []byte("txt"), 0o644); err != nil {
		t.Fatalf("write .txt: %v", err)
	}

	entries, err := walkIDEPack(packDir)
	if err != nil {
		t.Fatalf("walkIDEPack: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("want 1 entry (only .tmpl), got %d", len(entries))
	}
	if entries[0].RelPath != "file" {
		t.Errorf("want RelPath=file, got %q", entries[0].RelPath)
	}
}

// TestWalkIDEPack_symlinkFileRejected verifies that a symlinked .tmpl file
// is rejected (symlink check runs before suffix filter).
func TestWalkIDEPack_symlinkFileRejected(t *testing.T) {
	projectRoot := t.TempDir()
	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide", "default")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create pack dir: %v", err)
	}

	// Create a real file outside the pack
	outside := t.TempDir()
	realFile := filepath.Join(outside, "real.tmpl")
	if err := os.WriteFile(realFile, []byte("real"), 0o644); err != nil {
		t.Fatalf("write real file: %v", err)
	}

	// Symlink it inside the pack
	symlinkFile := filepath.Join(packDir, "file.tmpl")
	if err := os.Symlink(realFile, symlinkFile); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	_, err := walkIDEPack(packDir)
	if err == nil {
		t.Fatal("expected error when pack contains symlinked .tmpl file")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink error, got: %v", err)
	}
}

// TestWalkIDEPack_symlinkNonTmplFileRejected verifies that a symlinked non-.tmpl file
// is rejected, proving the symlink check runs before the suffix filter.
func TestWalkIDEPack_symlinkNonTmplFileRejected(t *testing.T) {
	projectRoot := t.TempDir()
	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide", "default")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create pack dir: %v", err)
	}

	outside := t.TempDir()
	realFile := filepath.Join(outside, "real.txt")
	if err := os.WriteFile(realFile, []byte("text"), 0o644); err != nil {
		t.Fatalf("write real file: %v", err)
	}

	// Symlink a non-.tmpl file inside the pack
	if err := os.Symlink(realFile, filepath.Join(packDir, "readme.txt")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	_, err := walkIDEPack(packDir)
	if err == nil {
		t.Fatal("expected error when pack contains symlinked non-.tmpl file")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink error, got: %v", err)
	}
}

// TestWalkIDEPack_symlinkDirRejected verifies that a symlinked directory inside
// the pack is rejected.
func TestWalkIDEPack_symlinkDirRejected(t *testing.T) {
	projectRoot := t.TempDir()
	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide", "default")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create pack dir: %v", err)
	}

	// Create a real directory outside the pack with a template file
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "settings.json.tmpl"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write tpl: %v", err)
	}

	// Symlink the outside directory inside the pack as a subdirectory
	if err := os.Symlink(outside, filepath.Join(packDir, ".vscode")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	_, err := walkIDEPack(packDir)
	if err == nil {
		t.Fatal("expected error when pack contains symlinked directory")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink error, got: %v", err)
	}
}

// TestWalkIDEPack_bareTmplRejected verifies that a bare ".tmpl" file is rejected.
func TestWalkIDEPack_bareTmplRejected(t *testing.T) {
	projectRoot := t.TempDir()
	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide", "default")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create pack dir: %v", err)
	}

	// Create a bare ".tmpl" file at pack root
	if err := os.WriteFile(filepath.Join(packDir, ".tmpl"), []byte("bad"), 0o644); err != nil {
		t.Fatalf("write .tmpl: %v", err)
	}

	_, err := walkIDEPack(packDir)
	if err == nil {
		t.Fatal("expected error for bare .tmpl file")
	}
	if !strings.Contains(err.Error(), "bare .tmpl") {
		t.Errorf("expected 'bare .tmpl' error, got: %v", err)
	}
}

// TestWalkIDEPack_nestedBareTmplRejected verifies that "dir/.tmpl" is rejected.
func TestWalkIDEPack_nestedBareTmplRejected(t *testing.T) {
	projectRoot := t.TempDir()
	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide", "default")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create pack dir: %v", err)
	}

	// Create a nested ".tmpl" file: subdir/.tmpl
	subDir := filepath.Join(packDir, "subdir")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, ".tmpl"), []byte("bad"), 0o644); err != nil {
		t.Fatalf("write nested .tmpl: %v", err)
	}

	_, err := walkIDEPack(packDir)
	if err == nil {
		t.Fatal("expected error for nested .tmpl file")
	}
	if !strings.Contains(err.Error(), "bare .tmpl") {
		t.Errorf("expected 'bare .tmpl' error, got: %v", err)
	}
}

// TestWalkIDEPack_absoluteSourcePath verifies that SourcePath is absolute
// even when packDir is relative.
func TestWalkIDEPack_absoluteSourcePath(t *testing.T) {
	projectRoot := t.TempDir()
	packName := "default"
	setupIDEPackTemplates(t, projectRoot, packName, map[string]string{
		"file.tmpl": "content",
	})

	// Change to a temp directory and use relative path
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		if err := os.Chdir(oldCwd); err != nil {
			t.Errorf("chdir back to original: %v", err)
		}
	}()

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
		".devcontainer/devcontainer.json.tmpl": minimalDevcontainerTpl,
		".vscode/settings.json.tmpl":           minimalVscodeSettingsTpl,
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

// TestRenderIDEConfigs_dotDirRejected verifies that a service with dir "." is rejected
// to prevent writing IDE files into the project root.
func TestRenderIDEConfigs_dotDirRejected(t *testing.T) {
	projectRoot := t.TempDir()

	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".devcontainer/devcontainer.json.tmpl": `{}`,
	})

	cfg := makeIDECfg("main")
	svc := cfg.Services["main"]
	svc.Dir = "."

	var buf strings.Builder
	w := render.NewWriter(&buf)

	err := renderIDEConfigs(projectRoot, "main", svc, cfg, w)
	if err == nil {
		t.Fatal("expected error for dir '.', got nil")
	}
	if !strings.Contains(err.Error(), "escapes project root") {
		t.Errorf("expected 'escapes project root' in error, got: %v", err)
	}
}

// TestRenderIDEConfigs_perServiceOverride verifies per-service pack override.
func TestRenderIDEConfigs_perServiceOverride(t *testing.T) {
	projectRoot := t.TempDir()

	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".vscode/settings.json.tmpl": `{"default": true}`,
	})
	setupIDEPackTemplates(t, projectRoot, "main-debug", map[string]string{
		".vscode/settings.json.tmpl": `{"debug": true}`,
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
		".vscode/settings.json.tmpl": `{"source": "default"}`,
	})
	setupIDEPackTemplates(t, projectRoot, "main", map[string]string{
		".vscode/settings.json.tmpl": `{"source": "main"}`,
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
		".vscode/settings.json.tmpl": `{"source": "default"}`,
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
		".devcontainer/devcontainer.json.tmpl": minimalDevcontainerTpl,
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

	if err := pathsafe.CheckNoSymlinks(root, filepath.Join(root, "nonexistent", "path"), "test path"); err != nil {
		t.Errorf("non-existent path: unexpected error: %v", err)
	}

	if err := pathsafe.CheckNoSymlinks(root, realSub, "test path"); err != nil {
		t.Errorf("real path: unexpected error: %v", err)
	}

	linkPath := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "real"), linkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	if err := pathsafe.CheckNoSymlinks(root, filepath.Join(linkPath, "sub"), "test path"); err == nil {
		t.Errorf("path through symlink: expected error, got nil")
	}

	if err := pathsafe.CheckNoSymlinks(root, linkPath, "test path"); err == nil {
		t.Errorf("symlink as target: expected error, got nil")
	}

	// Path outside root: CheckNoSymlinks should reject it
	if err := pathsafe.CheckNoSymlinks(root, filepath.Dir(root), "test path"); err == nil {
		t.Errorf("path outside root: expected error, got nil")
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
		wantErrMsg  string // empty means success expected
	}{
		{
			name:        "valid app service - success",
			serviceName: "main",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app", Enabled: true, Dir: "./services/main"},
			},
		},
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
		{
			name:        "service dir is dot - rejected",
			serviceName: "main",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app", Enabled: true, Dir: "."},
			},
			wantErrMsg: `service "main" has no dir`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExplicitIDEArg(tt.serviceName, tt.services)
			if tt.wantErrMsg == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
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

// TestResolveIDEHubAnchor verifies hub-anchor resolution: an explicit service
// name is treated as a hub anchor, and the IDE collision-policy winner among
// services sharing its dir (deepest extends wins) is returned.
func TestResolveIDEHubAnchor(t *testing.T) {
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
			name:  "parent and child share dir, both enabled: child (deepest) wins",
			input: "main",
			services: map[string]config.ServiceConfig{
				"main":       {Type: "app", Enabled: true, Dir: "./services/main"},
				"main-debug": {Type: "app", Enabled: true, Dir: "./services/main", Extends: "main"},
			},
			want: "main-debug",
		},
		{
			name:  "passing the variant name still resolves to the variant (it is the winner)",
			input: "main-debug",
			services: map[string]config.ServiceConfig{
				"main":       {Type: "app", Enabled: true, Dir: "./services/main"},
				"main-debug": {Type: "app", Enabled: true, Dir: "./services/main", Extends: "main"},
			},
			want: "main-debug",
		},
		{
			name:  "variant disabled: parent wins",
			input: "main",
			services: map[string]config.ServiceConfig{
				"main":       {Type: "app", Enabled: true, Dir: "./services/main"},
				"main-debug": {Type: "app", Enabled: false, Dir: "./services/main", Extends: "main"},
			},
			want: "main",
		},
		{
			name:  "variant has ide.enabled=false: parent wins",
			input: "main",
			services: map[string]config.ServiceConfig{
				"main":       {Type: "app", Enabled: true, Dir: "./services/main"},
				"main-debug": {Type: "app", Enabled: true, Dir: "./services/main", Extends: "main", IDE: config.ServiceIDEConfig{Enabled: &falseVal}},
			},
			want: "main",
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
			got := resolveIDEHubAnchor(tt.input, tt.services)
			if got != tt.want {
				t.Errorf("resolveIDEHubAnchor(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
