package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

// makeIDECfg returns a DevboxConfig configured for IDE rendering tests.
func makeIDECfg(vscodeEnabled, devcontainerEnabled bool) *config.DevboxConfig {
	return &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "laravel", Prefix: "devbox"},
		Services: map[string]config.ServiceConfig{
			"main": {
				Type:        "app",
				Dir:         "./services/main",
				Container:   "app-main",
				DirInternal: "/var/www/app",
			},
		},
		Runtime: config.RuntimeConfig{
			Ports: config.RuntimePorts{App: 80},
		},
		IDE: config.IDEConfig{
			VSCode:       config.IDEEditorConfig{Enabled: vscodeEnabled},
			Devcontainer: config.IDEEditorConfig{Enabled: devcontainerEnabled},
		},
		Raw: map[string]any{},
	}
}

// TestRenderIDETemplate_devcontainer verifies that the devcontainer template
// substitutes project name, container, workspaceFolder, and port.
func TestRenderIDETemplate_devcontainer(t *testing.T) {
	data := ideTemplateData{
		Project: config.ProjectConfig{Name: "myapp"},
		Service: "main",
		ServiceCfg: config.ServiceConfig{
			Container:   "app-main",
			DirInternal: "/var/www/app",
		},
		Runtime: config.RuntimeConfig{
			Ports: config.RuntimePorts{App: 8080},
		},
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "devcontainer.json")

	if err := renderIDETemplate(devcontainerTpl, "devcontainer.json", data, dest); err != nil {
		t.Fatalf("renderIDETemplate: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	content := string(got)

	checks := []struct{ want, label string }{
		{`"name": "myapp"`, "project name"},
		{`"service": "app-main"`, "container name"},
		{`"workspaceFolder": "/var/www/app"`, "workspaceFolder"},
		{`8080`, "port"},
	}
	for _, c := range checks {
		if !strings.Contains(content, c.want) {
			t.Errorf("devcontainer.json missing %s (%q)\ngot:\n%s", c.label, c.want, content)
		}
	}
}

// TestRenderIDETemplate_vscodeLaunch verifies path mappings in launch.json.
func TestRenderIDETemplate_vscodeLaunch(t *testing.T) {
	data := ideTemplateData{
		ServiceCfg: config.ServiceConfig{DirInternal: "/var/www/app"},
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "launch.json")

	if err := renderIDETemplate(vscodeLaunchTpl, "launch.json", data, dest); err != nil {
		t.Fatalf("renderIDETemplate: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	content := string(got)

	if !strings.Contains(content, `"/var/www/app": "${workspaceFolder}"`) {
		t.Errorf("launch.json missing pathMappings, got:\n%s", content)
	}
	if !strings.Contains(content, `"type": "php"`) {
		t.Errorf("launch.json missing type: php, got:\n%s", content)
	}
}

// TestRenderIDETemplate_vscodeSettings verifies settings.json content.
func TestRenderIDETemplate_vscodeSettings(t *testing.T) {
	data := ideTemplateData{}
	dir := t.TempDir()
	dest := filepath.Join(dir, "settings.json")

	if err := renderIDETemplate(vscodeSettingsTpl, "settings.json", data, dest); err != nil {
		t.Fatalf("renderIDETemplate: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	content := string(got)

	if !strings.Contains(content, `"php.validate.executablePath"`) {
		t.Errorf("settings.json missing php.validate.executablePath, got:\n%s", content)
	}
	if !strings.Contains(content, `"editor.formatOnSave": true`) {
		t.Errorf("settings.json missing editor.formatOnSave, got:\n%s", content)
	}
}

// TestRenderIDETemplate_createsParentDirs verifies parent directories are created.
func TestRenderIDETemplate_createsParentDirs(t *testing.T) {
	data := ideTemplateData{
		ServiceCfg: config.ServiceConfig{DirInternal: "/var/www/app"},
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "nested", "deep", "file.json")

	if err := renderIDETemplate(vscodeLaunchTpl, "file.json", data, dest); err != nil {
		t.Fatalf("renderIDETemplate should create parent dirs: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("expected file to exist at %s: %v", dest, err)
	}
}

// TestRenderIDEConfigs_devcontainerOnly verifies only devcontainer files generated.
func TestRenderIDEConfigs_devcontainerOnly(t *testing.T) {
	cfg := makeIDECfg(false, true) // vscode=off, devcontainer=on
	svc := cfg.Services["main"]
	projectRoot := t.TempDir()

	var buf strings.Builder
	w := render.NewWriter(&buf)

	if err := renderIDEConfigs(projectRoot, "main", svc, cfg, w); err != nil {
		t.Fatalf("renderIDEConfigs: %v", err)
	}

	devcontainerPath := filepath.Join(projectRoot, "services", "main", ".devcontainer", "devcontainer.json")
	if _, err := os.Stat(devcontainerPath); err != nil {
		t.Errorf("expected devcontainer.json to exist: %v", err)
	}

	// VSCode files should NOT be created.
	launchPath := filepath.Join(projectRoot, "services", "main", ".vscode", "launch.json")
	if _, err := os.Stat(launchPath); err == nil {
		t.Errorf("launch.json should not be created when vscode disabled")
	}
}

// TestRenderIDEConfigs_vscodeOnly verifies only vscode files generated.
func TestRenderIDEConfigs_vscodeOnly(t *testing.T) {
	cfg := makeIDECfg(true, false) // vscode=on, devcontainer=off
	svc := cfg.Services["main"]
	projectRoot := t.TempDir()

	var buf strings.Builder
	w := render.NewWriter(&buf)

	if err := renderIDEConfigs(projectRoot, "main", svc, cfg, w); err != nil {
		t.Fatalf("renderIDEConfigs: %v", err)
	}

	launchPath := filepath.Join(projectRoot, "services", "main", ".vscode", "launch.json")
	if _, err := os.Stat(launchPath); err != nil {
		t.Errorf("expected launch.json to exist: %v", err)
	}

	settingsPath := filepath.Join(projectRoot, "services", "main", ".vscode", "settings.json")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Errorf("expected settings.json to exist: %v", err)
	}

	// Devcontainer should NOT be created.
	devcontainerPath := filepath.Join(projectRoot, "services", "main", ".devcontainer", "devcontainer.json")
	if _, err := os.Stat(devcontainerPath); err == nil {
		t.Errorf("devcontainer.json should not be created when devcontainer disabled")
	}
}

// TestRenderIDEConfigs_bothEnabled verifies all IDE files generated when both enabled.
func TestRenderIDEConfigs_bothEnabled(t *testing.T) {
	cfg := makeIDECfg(true, true) // vscode=on, devcontainer=on
	svc := cfg.Services["main"]
	projectRoot := t.TempDir()

	var buf strings.Builder
	w := render.NewWriter(&buf)

	if err := renderIDEConfigs(projectRoot, "main", svc, cfg, w); err != nil {
		t.Fatalf("renderIDEConfigs: %v", err)
	}

	expected := []string{
		filepath.Join(projectRoot, "services", "main", ".devcontainer", "devcontainer.json"),
		filepath.Join(projectRoot, "services", "main", ".vscode", "launch.json"),
		filepath.Join(projectRoot, "services", "main", ".vscode", "settings.json"),
	}
	for _, p := range expected {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected file to exist: %s: %v", p, err)
		}
	}
}

// TestRenderIDEConfigs_neitherEnabled verifies nothing generated when both disabled.
func TestRenderIDEConfigs_neitherEnabled(t *testing.T) {
	cfg := makeIDECfg(false, false) // vscode=off, devcontainer=off
	svc := cfg.Services["main"]
	projectRoot := t.TempDir()

	var buf strings.Builder
	w := render.NewWriter(&buf)

	if err := renderIDEConfigs(projectRoot, "main", svc, cfg, w); err != nil {
		t.Fatalf("renderIDEConfigs: %v", err)
	}

	serviceDir := filepath.Join(projectRoot, "services", "main")
	if _, err := os.Stat(serviceDir); err == nil {
		// The directory may not exist since nothing was written.
		// Check specific files.
		for _, rel := range []string{".devcontainer/devcontainer.json", ".vscode/launch.json"} {
			if _, err := os.Stat(filepath.Join(serviceDir, rel)); err == nil {
				t.Errorf("file %s should not be created when all editors disabled", rel)
			}
		}
	}
}

// TestRenderIDEConfigs_devcontainerSubstitutesValues checks that the rendered
// devcontainer.json has correct project name and container values.
func TestRenderIDEConfigs_devcontainerSubstitutesValues(t *testing.T) {
	cfg := makeIDECfg(false, true)
	svc := cfg.Services["main"]
	projectRoot := t.TempDir()

	var buf strings.Builder
	w := render.NewWriter(&buf)

	if err := renderIDEConfigs(projectRoot, "main", svc, cfg, w); err != nil {
		t.Fatalf("renderIDEConfigs: %v", err)
	}

	dest := filepath.Join(projectRoot, "services", "main", ".devcontainer", "devcontainer.json")
	content, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read devcontainer.json: %v", err)
	}
	s := string(content)

	if !strings.Contains(s, `"name": "laravel"`) {
		t.Errorf("devcontainer.json should contain project name, got:\n%s", s)
	}
	if !strings.Contains(s, `"service": "app-main"`) {
		t.Errorf("devcontainer.json should contain container name, got:\n%s", s)
	}
	if !strings.Contains(s, `"workspaceFolder": "/var/www/app"`) {
		t.Errorf("devcontainer.json should contain workspaceFolder, got:\n%s", s)
	}
}
