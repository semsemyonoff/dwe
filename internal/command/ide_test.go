package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

// Minimal inline templates used by renderIDETemplate unit tests.
const minimalDevcontainerTpl = `{"name":"{{ .Project.Name }}","service":"{{ .ServiceCfg.Container }}","workspaceFolder":"{{ .ServiceCfg.DirInternal }}","forwardPorts":[{{ .Runtime.Ports.App }}]}`
const minimalVscodeLaunchTpl = `{"type":"php","pathMappings":{"{{ .ServiceCfg.WorkDirInternal }}":"${workspaceFolder}/src"}}`
const minimalVscodeSettingsTpl = `{"php.validate.executablePath":"/usr/local/bin/php","editor.formatOnSave":true}`

// setupIDETemplates writes the three IDE template files into <dir>/devbox/templates/ide/
// so that renderIDEConfigs can load them during tests.
func setupIDETemplates(t *testing.T, dir string) {
	t.Helper()
	tplDir := filepath.Join(dir, "devbox", "templates", "ide")
	if err := os.MkdirAll(tplDir, 0o755); err != nil {
		t.Fatalf("create template dir: %v", err)
	}
	files := map[string]string{
		"devcontainer.json.tpl":    minimalDevcontainerTpl,
		"vscode_launch.json.tpl":   minimalVscodeLaunchTpl,
		"vscode_settings.json.tpl": minimalVscodeSettingsTpl,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(tplDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write template %s: %v", name, err)
		}
	}
}

// makeIDECfg returns a DevboxConfig configured for IDE rendering tests.
func makeIDECfg(vscodeEnabled, devcontainerEnabled bool) *config.DevboxConfig {
	return &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "laravel", Prefix: "devbox"},
		Services: map[string]config.ServiceConfig{
			"main": {
				Type:            "app",
				Enabled:         true,
				Dir:             "./services/main",
				Container:       "app-main",
				DirInternal:     "/workspace",
				WorkDirInternal: "/workspace/src",
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
// substitutes project name, container, workspaceFolder (hub dir), and port.
func TestRenderIDETemplate_devcontainer(t *testing.T) {
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
	dir := t.TempDir()
	dest := filepath.Join(dir, "devcontainer.json")

	if err := renderIDETemplate(minimalDevcontainerTpl, "devcontainer.json", data, dest, dir); err != nil {
		t.Fatalf("renderIDETemplate: %v", err)
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

// TestRenderIDETemplate_vscodeLaunch verifies path mappings in launch.json use WorkDirInternal.
func TestRenderIDETemplate_vscodeLaunch(t *testing.T) {
	data := ideTemplateData{
		ServiceCfg: config.ServiceConfig{
			DirInternal:     "/workspace",
			WorkDirInternal: "/workspace/src",
		},
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "launch.json")

	if err := renderIDETemplate(minimalVscodeLaunchTpl, "launch.json", data, dest, dir); err != nil {
		t.Fatalf("renderIDETemplate: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	content := string(got)

	if !strings.Contains(content, `"/workspace/src":"${workspaceFolder}/src"`) {
		t.Errorf("launch.json missing correct pathMappings, got:\n%s", content)
	}
	if !strings.Contains(content, `"type":"php"`) {
		t.Errorf("launch.json missing type: php, got:\n%s", content)
	}
}

// TestRenderIDETemplate_vscodeSettings verifies settings.json content.
func TestRenderIDETemplate_vscodeSettings(t *testing.T) {
	data := ideTemplateData{}
	dir := t.TempDir()
	dest := filepath.Join(dir, "settings.json")

	if err := renderIDETemplate(minimalVscodeSettingsTpl, "settings.json", data, dest, dir); err != nil {
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
	if !strings.Contains(content, `"editor.formatOnSave":true`) {
		t.Errorf("settings.json missing editor.formatOnSave, got:\n%s", content)
	}
}

// TestRenderIDETemplate_createsParentDirs verifies parent directories are created.
func TestRenderIDETemplate_createsParentDirs(t *testing.T) {
	data := ideTemplateData{
		ServiceCfg: config.ServiceConfig{
			DirInternal:     "/workspace",
			WorkDirInternal: "/workspace/src",
		},
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "nested", "deep", "file.json")

	if err := renderIDETemplate(minimalVscodeLaunchTpl, "file.json", data, dest, dir); err != nil {
		t.Fatalf("renderIDETemplate should create parent dirs: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("expected file to exist at %s: %v", dest, err)
	}
}

// TestRenderIDETemplate_symlinkDir verifies that a symlinked intermediate directory
// that points outside the project root is rejected.
func TestRenderIDETemplate_symlinkDir(t *testing.T) {
	projectRoot := t.TempDir()
	outside := t.TempDir()

	svcDir := filepath.Join(projectRoot, "services", "main")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// .devcontainer -> outside the project root
	if err := os.Symlink(outside, filepath.Join(svcDir, ".devcontainer")); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(svcDir, ".devcontainer", "devcontainer.json")
	absRoot, _ := filepath.Abs(projectRoot)
	err := renderIDETemplate("{}", "devcontainer.json", ideTemplateData{}, dest, absRoot)
	if err == nil {
		t.Fatal("expected error when destination dir is a symlink outside project root")
	}
}

// TestRenderIDETemplate_symlinkFile verifies that a symlinked destination file
// is rejected even when the parent directory is safe.
func TestRenderIDETemplate_symlinkFile(t *testing.T) {
	projectRoot := t.TempDir()
	outside := t.TempDir()

	svcDir := filepath.Join(projectRoot, "services", "main", ".devcontainer")
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

	absRoot, _ := filepath.Abs(projectRoot)
	err := renderIDETemplate("{}", "devcontainer.json", ideTemplateData{}, dest, absRoot)
	if err == nil {
		t.Fatal("expected error when destination file is a symlink")
	}
}

// TestRenderIDEConfigs_devcontainerOnly verifies only devcontainer files generated.
func TestRenderIDEConfigs_devcontainerOnly(t *testing.T) {
	projectRoot := t.TempDir()
	setupIDETemplates(t, projectRoot)

	cfg := makeIDECfg(false, true) // vscode=off, devcontainer=on
	svc := cfg.Services["main"]

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
	projectRoot := t.TempDir()
	setupIDETemplates(t, projectRoot)

	cfg := makeIDECfg(true, false) // vscode=on, devcontainer=off
	svc := cfg.Services["main"]

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
	projectRoot := t.TempDir()
	setupIDETemplates(t, projectRoot)

	cfg := makeIDECfg(true, true) // vscode=on, devcontainer=on
	svc := cfg.Services["main"]

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
	projectRoot := t.TempDir()
	setupIDETemplates(t, projectRoot)

	cfg := makeIDECfg(false, false) // vscode=off, devcontainer=off
	svc := cfg.Services["main"]

	var buf strings.Builder
	w := render.NewWriter(&buf)

	if err := renderIDEConfigs(projectRoot, "main", svc, cfg, w); err != nil {
		t.Fatalf("renderIDEConfigs: %v", err)
	}

	serviceDir := filepath.Join(projectRoot, "services", "main")
	if _, err := os.Stat(serviceDir); err == nil {
		for _, rel := range []string{".devcontainer/devcontainer.json", ".vscode/launch.json"} {
			if _, err := os.Stat(filepath.Join(serviceDir, rel)); err == nil {
				t.Errorf("file %s should not be created when all editors disabled", rel)
			}
		}
	}
}

// TestRenderIDEConfigs_devcontainerSubstitutesValues checks that the rendered
// devcontainer.json has correct project name, container, and hub workspace folder.
func TestRenderIDEConfigs_devcontainerSubstitutesValues(t *testing.T) {
	projectRoot := t.TempDir()
	setupIDETemplates(t, projectRoot)

	cfg := makeIDECfg(false, true)
	svc := cfg.Services["main"]

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

	if !strings.Contains(s, `"name":"laravel"`) {
		t.Errorf("devcontainer.json should contain project name, got:\n%s", s)
	}
	if !strings.Contains(s, `"service":"app-main"`) {
		t.Errorf("devcontainer.json should contain container name, got:\n%s", s)
	}
	// workspaceFolder should be the hub dir, not the src dir
	if !strings.Contains(s, `"workspaceFolder":"/workspace"`) {
		t.Errorf("devcontainer.json should contain hub workspaceFolder, got:\n%s", s)
	}
}

// TestRenderIDEConfigs_missingTemplates verifies that missing project-level
// templates emit a warning and are skipped rather than returning an error.
func TestRenderIDEConfigs_missingTemplates(t *testing.T) {
	projectRoot := t.TempDir()
	// Intentionally do NOT call setupIDETemplates.

	cfg := makeIDECfg(false, true) // devcontainer=on
	svc := cfg.Services["main"]

	var buf strings.Builder
	w := render.NewWriter(&buf)

	if err := renderIDEConfigs(projectRoot, "main", svc, cfg, w); err != nil {
		t.Fatalf("expected warn-and-skip for missing templates, got error: %v", err)
	}
	if !strings.Contains(buf.String(), "skipping") {
		t.Errorf("expected warning about missing template, got: %s", buf.String())
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

// TestResolveIDETemplate tests the 3-step template fallback resolution.
func TestResolveIDETemplate(t *testing.T) {
	projectRoot := t.TempDir()

	// Create template directory structure
	tplDir := filepath.Join(projectRoot, "devbox", "templates", "ide")
	if err := os.MkdirAll(tplDir, 0o755); err != nil {
		t.Fatalf("create template dir: %v", err)
	}

	// Create test templates
	templates := map[string]string{
		// Global templates
		filepath.Join(tplDir, "devcontainer.json.tpl"):  "global-devcontainer",
		filepath.Join(tplDir, "vscode_launch.json.tpl"): "global-vscode-launch",
		// By-name templates
		filepath.Join(tplDir, "main", "devcontainer.json.tpl"):       "main-devcontainer",
		filepath.Join(tplDir, "main-debug", "devcontainer.json.tpl"): "main-debug-devcontainer",
		// Explicit override templates
		filepath.Join(tplDir, "custom", "devcontainer.json.tpl"): "custom-devcontainer",
	}

	for path, content := range templates {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create dir for %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write template %s: %v", path, err)
		}
	}

	trueVal := true

	tests := []struct {
		name         string
		svc          config.ServiceConfig
		serviceName  string
		fileBase     string
		wantContent  string
		wantErrType  error
		wantErrInMsg string
	}{
		{
			name: "only global exists - used",
			svc: config.ServiceConfig{
				Type:    "app",
				Enabled: true,
				IDE:     config.ServiceIDEConfig{Enabled: &trueVal},
			},
			serviceName: "unknown",
			fileBase:    "vscode_launch.json",
			wantContent: "global-vscode-launch",
		},
		{
			name: "only by-name exists - used",
			svc: config.ServiceConfig{
				Type:    "app",
				Enabled: true,
				IDE:     config.ServiceIDEConfig{Enabled: &trueVal},
			},
			serviceName: "main",
			fileBase:    "devcontainer.json",
			wantContent: "main-devcontainer",
		},
		{
			name: "only explicit template exists - used",
			svc: config.ServiceConfig{
				Type:    "app",
				Enabled: true,
				IDE:     config.ServiceIDEConfig{Enabled: &trueVal, Template: "custom"},
			},
			serviceName: "anything",
			fileBase:    "devcontainer.json",
			wantContent: "custom-devcontainer",
		},
		{
			name: "explicit beats by-name beats global precedence",
			svc: config.ServiceConfig{
				Type:    "app",
				Enabled: true,
				IDE:     config.ServiceIDEConfig{Enabled: &trueVal, Template: "custom"},
			},
			serviceName: "main",
			fileBase:    "devcontainer.json",
			wantContent: "custom-devcontainer",
		},
		{
			name: "by-name beats global",
			svc: config.ServiceConfig{
				Type:    "app",
				Enabled: true,
				IDE:     config.ServiceIDEConfig{Enabled: &trueVal},
			},
			serviceName: "main-debug",
			fileBase:    "devcontainer.json",
			wantContent: "main-debug-devcontainer",
		},
		{
			name: "none exist - returns wrapped ErrNotExist",
			svc: config.ServiceConfig{
				Type:    "app",
				Enabled: true,
				IDE:     config.ServiceIDEConfig{Enabled: &trueVal},
			},
			serviceName:  "nonexistent",
			fileBase:     "unknown.json",
			wantErrType:  os.ErrNotExist,
			wantErrInMsg: "ide template for unknown.json",
		},
		{
			name: "empty ide.template skips step 1",
			svc: config.ServiceConfig{
				Type:    "app",
				Enabled: true,
				IDE:     config.ServiceIDEConfig{Enabled: &trueVal, Template: ""},
			},
			serviceName: "main",
			fileBase:    "devcontainer.json",
			wantContent: "main-devcontainer",
		},
		{
			name: "invalid ide.template (with slash) - non-ErrNotExist error",
			svc: config.ServiceConfig{
				Type:    "app",
				Enabled: true,
				IDE:     config.ServiceIDEConfig{Enabled: &trueVal, Template: "foo/bar"},
			},
			serviceName:  "main",
			fileBase:     "devcontainer.json",
			wantErrInMsg: "path separator",
		},
		{
			name: "invalid ide.template (with ..) - non-ErrNotExist error",
			svc: config.ServiceConfig{
				Type:    "app",
				Enabled: true,
				IDE:     config.ServiceIDEConfig{Enabled: &trueVal, Template: ".."},
			},
			serviceName:  "main",
			fileBase:     "devcontainer.json",
			wantErrInMsg: ".. segment",
		},
		{
			name: "invalid ide.template (leading dot) - non-ErrNotExist error",
			svc: config.ServiceConfig{
				Type:    "app",
				Enabled: true,
				IDE:     config.ServiceIDEConfig{Enabled: &trueVal, Template: ".hidden"},
			},
			serviceName:  "main",
			fileBase:     "devcontainer.json",
			wantErrInMsg: "starts with dot",
		},
		{
			name: "invalid service name - non-ErrNotExist error",
			svc: config.ServiceConfig{
				Type:    "app",
				Enabled: true,
				IDE:     config.ServiceIDEConfig{Enabled: &trueVal},
			},
			serviceName:  "a/b",
			fileBase:     "devcontainer.json",
			wantErrInMsg: "path separator",
		},
		{
			name: "invalid service name with .. - non-ErrNotExist error",
			svc: config.ServiceConfig{
				Type:    "app",
				Enabled: true,
				IDE:     config.ServiceIDEConfig{Enabled: &trueVal},
			},
			serviceName:  "..",
			fileBase:     "devcontainer.json",
			wantErrInMsg: ".. segment",
		},
		{
			name: "explicit template absent - falls through to by-name",
			svc: config.ServiceConfig{
				Type:    "app",
				Enabled: true,
				IDE:     config.ServiceIDEConfig{Enabled: &trueVal, Template: "nonexistent-custom"},
			},
			serviceName: "main",
			fileBase:    "devcontainer.json",
			wantContent: "main-devcontainer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, content, err := resolveIDETemplate(projectRoot, tt.svc, tt.serviceName, tt.fileBase)

			if tt.wantErrType != nil || tt.wantErrInMsg != "" {
				if err == nil {
					t.Fatalf("want error, got nil")
				}
				if tt.wantErrInMsg != "" && !strings.Contains(err.Error(), tt.wantErrInMsg) {
					t.Errorf("error message: want to contain %q, got %q", tt.wantErrInMsg, err.Error())
				}
				if tt.wantErrType != nil && !errors.Is(err, tt.wantErrType) {
					t.Errorf("error type: want %v, got %v", tt.wantErrType, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("want no error, got %v", err)
			}
			if string(content) != tt.wantContent {
				t.Errorf("content: want %q, got %q", tt.wantContent, string(content))
			}
			if path == "" {
				t.Errorf("resolveIDETemplate returned empty path for successful lookup")
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
		wantSkippedMap map[string]skippedService // name -> expected skipped entry
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
					// IDE.Enabled is nil, Type="app", so IDERenderEnabled()=true
				},
				"db": {
					Type:    "db",
					Enabled: true,
					Dir:     "./services/db",
					// IDE.Enabled is nil, Type="db", so IDERenderEnabled()=false
				},
			},
			wantSelected: []string{"main"},
			wantSkippedMap: map[string]skippedService{
				"db": {Name: "db", Reason: "ide-policy"},
			},
		},
		{
			name: "service with Enabled=false dropped (even if IDERenderEnabled=true)",
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
		{
			name: "dir normalization: ./services/main vs services/main",
			services: map[string]config.ServiceConfig{
				"svc-a": {
					Type:    "app",
					Enabled: true,
					Dir:     "./services/main",
				},
				"svc-b": {
					Type:    "app",
					Enabled: true,
					Dir:     "services/main",
					Extends: "svc-a",
				},
			},
			wantSelected: []string{"svc-b"},
			wantSkippedMap: map[string]skippedService{
				"svc-a": {
					Name:   "svc-a",
					Reason: "lost-collision",
					Dir:    filepath.Join(".", "services", "main"),
					Winner: "svc-b",
				},
			},
		},
		{
			name: "three-level extends chain - c extends b extends a",
			services: map[string]config.ServiceConfig{
				"a": {
					Type:    "app",
					Enabled: true,
					Dir:     "./services/main",
				},
				"b": {
					Type:    "app",
					Enabled: true,
					Dir:     "./services/main",
					Extends: "a",
				},
				"c": {
					Type:    "app",
					Enabled: true,
					Dir:     "./services/main",
					Extends: "b",
				},
			},
			wantSelected: []string{"c"},
			wantSkippedMap: map[string]skippedService{
				"a": {
					Name:   "a",
					Reason: "lost-collision",
					Dir:    filepath.Join(".", "services", "main"),
					Winner: "c",
				},
				"b": {
					Name:   "b",
					Reason: "lost-collision",
					Dir:    filepath.Join(".", "services", "main"),
					Winner: "c",
				},
			},
		},
		{
			name: "tie on equal depth - lexicographic winner",
			services: map[string]config.ServiceConfig{
				"zebra": {
					Type:    "app",
					Enabled: true,
					Dir:     "./services/shared",
				},
				"apple": {
					Type:    "app",
					Enabled: true,
					Dir:     "./services/shared",
				},
			},
			wantSelected: []string{"apple"},
			wantSkippedMap: map[string]skippedService{
				"zebra": {
					Name:   "zebra",
					Reason: "lost-collision",
					Dir:    filepath.Join(".", "services", "shared"),
					Winner: "apple",
				},
			},
		},
		{
			name: "empty Dir dropped",
			services: map[string]config.ServiceConfig{
				"main": {
					Type:    "app",
					Enabled: true,
					Dir:     "",
				},
				"other": {
					Type:    "app",
					Enabled: true,
					Dir:     "./services/other",
				},
			},
			wantSelected: []string{"other"},
			wantSkippedMap: map[string]skippedService{
				"main": {Name: "main", Reason: "empty-dir"},
			},
		},
		{
			name: "whitespace-only Dir treated as empty",
			services: map[string]config.ServiceConfig{
				"main": {
					Type:    "app",
					Enabled: true,
					Dir:     "   ",
				},
				"other": {
					Type:    "app",
					Enabled: true,
					Dir:     "./services/other",
				},
			},
			wantSelected: []string{"other"},
			wantSkippedMap: map[string]skippedService{
				"main": {Name: "main", Reason: "empty-dir"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected, skipped := selectIDEServices(tt.services)

			// Check selected
			if len(selected) != len(tt.wantSelected) {
				t.Errorf("selected count: want %d, got %d (%v)", len(tt.wantSelected), len(selected), selected)
			}
			for i, name := range selected {
				if i < len(tt.wantSelected) && name != tt.wantSelected[i] {
					t.Errorf("selected[%d]: want %q, got %q", i, tt.wantSelected[i], name)
				}
			}

			// Check skipped
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

// TestRenderIDEConfigs_collisionResolution verifies that when two services share a dir,
// the most-derived (deepest extends chain) wins and renders, while the other is skipped.
func TestRenderIDEConfigs_collisionResolution(t *testing.T) {
	projectRoot := t.TempDir()
	setupIDETemplates(t, projectRoot)

	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "laravel", Prefix: "devbox"},
		Services: map[string]config.ServiceConfig{
			"main": {
				Type:            "app",
				Enabled:         true,
				Dir:             "./services/main",
				Container:       "app-main",
				DirInternal:     "/workspace",
				WorkDirInternal: "/workspace/src",
			},
			"main-debug": {
				Type:            "app",
				Enabled:         true,
				Extends:         "main",
				Dir:             "./services/main",
				Container:       "app-main-debug",
				DirInternal:     "/workspace",
				WorkDirInternal: "/workspace/src",
			},
		},
		Runtime: config.RuntimeConfig{
			Ports: config.RuntimePorts{App: 80},
		},
		IDE: config.IDEConfig{
			VSCode:       config.IDEEditorConfig{Enabled: false},
			Devcontainer: config.IDEEditorConfig{Enabled: true},
		},
		Raw: map[string]any{},
	}

	var output strings.Builder
	w := render.NewWriter(&output)

	// Select services and render
	selected, skipped := selectIDEServices(cfg.Services)
	if len(selected) != 1 || selected[0] != "main-debug" {
		t.Errorf("expected only main-debug selected, got %v", selected)
	}
	if len(skipped) != 1 || skipped[0].Name != "main" {
		t.Errorf("expected main to be skipped, got %v", skipped)
	}

	// Render the selected service
	if err := renderIDEConfigs(projectRoot, "main-debug", cfg.Services["main-debug"], cfg, w); err != nil {
		t.Fatalf("renderIDEConfigs: %v", err)
	}

	// Check that devcontainer was written for main-debug
	devcontainerPath := filepath.Join(projectRoot, "services", "main", ".devcontainer", "devcontainer.json")
	content, err := os.ReadFile(devcontainerPath)
	if err != nil {
		t.Fatalf("read devcontainer.json: %v", err)
	}
	s := string(content)

	// Should contain main-debug's container name
	if !strings.Contains(s, "app-main-debug") {
		t.Errorf("devcontainer.json should contain main-debug container, got:\n%s", s)
	}
}

// TestRenderIDECmd_explicitArgErrors verifies the four error cases when an explicit
// service argument is provided to the render ide command. It exercises the same
// validation logic as RunE's explicit-arg branch.
func TestRenderIDECmd_explicitArgErrors(t *testing.T) {
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
			err := simulateExplicitArgValidation(tt.serviceName, tt.services)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErrMsg)
			}
			if !strings.Contains(err.Error(), tt.wantErrMsg) {
				t.Errorf("error %q should contain %q", err.Error(), tt.wantErrMsg)
			}
		})
	}
}

// simulateExplicitArgValidation replicates the explicit-arg validation logic from RunE.
func simulateExplicitArgValidation(name string, services map[string]config.ServiceConfig) error {
	svc, ok := services[name]
	if !ok {
		return fmt.Errorf("service %q not found in config", name)
	}
	if !svc.Enabled {
		return fmt.Errorf("service %q is disabled at the project level", name)
	}
	if strings.TrimSpace(svc.Dir) == "" {
		return fmt.Errorf("service %q has no dir; cannot render IDE files", name)
	}
	enabled, explicit := svc.IDERenderEnabledExplicit()
	if !enabled {
		if explicit {
			return fmt.Errorf("service %q has ide.enabled: false", name)
		}
		return fmt.Errorf("service %q (type: %s) does not participate in IDE rendering by default; set ide.enabled: true to opt in", name, svc.Type)
	}
	return nil
}

// TestRenderIDECmd_collisionResolutionWithDisable verifies behavior when one
// service in a collision is disabled.
func TestRenderIDECmd_collisionResolutionWithDisable(t *testing.T) {
	projectRoot := t.TempDir()
	setupIDETemplates(t, projectRoot)

	falseVal := false
	cfg := &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "laravel", Prefix: "devbox"},
		Services: map[string]config.ServiceConfig{
			"main": {
				Type:            "app",
				Enabled:         true,
				Dir:             "./services/main",
				Container:       "app-main",
				DirInternal:     "/workspace",
				WorkDirInternal: "/workspace/src",
			},
			"main-debug": {
				Type:            "app",
				Enabled:         true,
				Extends:         "main",
				Dir:             "./services/main",
				Container:       "app-main-debug",
				DirInternal:     "/workspace",
				WorkDirInternal: "/workspace/src",
				IDE:             config.ServiceIDEConfig{Enabled: &falseVal},
			},
		},
		Runtime: config.RuntimeConfig{
			Ports: config.RuntimePorts{App: 80},
		},
		IDE: config.IDEConfig{
			VSCode:       config.IDEEditorConfig{Enabled: false},
			Devcontainer: config.IDEEditorConfig{Enabled: true},
		},
		Raw: map[string]any{},
	}

	var output strings.Builder
	w := render.NewWriter(&output)

	// Select services
	selected, skipped := selectIDEServices(cfg.Services)
	if len(selected) != 1 || selected[0] != "main" {
		t.Errorf("expected only main selected, got %v", selected)
	}

	// Verify main-debug was skipped by policy, not collision
	skippedByName := make(map[string]skippedService)
	for _, s := range skipped {
		skippedByName[s.Name] = s
	}
	if debugSkip, ok := skippedByName["main-debug"]; !ok || debugSkip.Reason != "ide-disabled" {
		t.Errorf("expected main-debug skipped with ide-disabled reason, got %v", skippedByName)
	}

	// Render the selected service
	if err := renderIDEConfigs(projectRoot, "main", cfg.Services["main"], cfg, w); err != nil {
		t.Fatalf("renderIDEConfigs: %v", err)
	}

	// Check that devcontainer was written for main
	devcontainerPath := filepath.Join(projectRoot, "services", "main", ".devcontainer", "devcontainer.json")
	content, err := os.ReadFile(devcontainerPath)
	if err != nil {
		t.Fatalf("read devcontainer.json: %v", err)
	}
	s := string(content)

	// Should contain main's container name (not main-debug)
	if !strings.Contains(s, "app-main") || strings.Contains(s, "app-main-debug") {
		t.Errorf("devcontainer.json should contain main (not debug) container, got:\n%s", s)
	}
}

// TestCheckNoSymlinks verifies the symlink detection helper.
func TestCheckNoSymlinks(t *testing.T) {
	root := t.TempDir()

	// Create a real subdirectory: root/real/sub
	realSub := filepath.Join(root, "real", "sub")
	if err := os.MkdirAll(realSub, 0o755); err != nil {
		t.Fatalf("create real dir: %v", err)
	}

	// Non-existent path should be fine (no symlinks possible in non-existent path)
	if err := checkNoSymlinks(root, filepath.Join(root, "nonexistent", "path")); err != nil {
		t.Errorf("non-existent path: unexpected error: %v", err)
	}

	// Real existing path should be fine
	if err := checkNoSymlinks(root, realSub); err != nil {
		t.Errorf("real path: unexpected error: %v", err)
	}

	// Create a symlink component: root/link -> root/real
	linkPath := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "real"), linkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	// Path through symlink should be rejected
	if err := checkNoSymlinks(root, filepath.Join(linkPath, "sub")); err == nil {
		t.Errorf("path through symlink: expected error, got nil")
	}

	// Symlink itself as target should be rejected
	if err := checkNoSymlinks(root, linkPath); err == nil {
		t.Errorf("symlink as target: expected error, got nil")
	}
}

// TestRenderIDEConfigs_symlink verifies that renderIDEConfigs rejects service dirs
// containing symlink components, even when the lexical containment check passes.
func TestRenderIDEConfigs_symlink(t *testing.T) {
	projectRoot := t.TempDir()
	setupIDETemplates(t, projectRoot)

	// Create a real dir outside project root
	outside := t.TempDir()

	// Create a symlink inside project root pointing outside
	symlinkDir := filepath.Join(projectRoot, "services")
	if err := os.Symlink(outside, symlinkDir); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	cfg := makeIDECfg(false, true)
	svc := cfg.Services["main"] // Dir: ./services/main — goes through the symlink

	var buf strings.Builder
	w := render.NewWriter(&buf)

	err := renderIDEConfigs(projectRoot, "main", svc, cfg, w)
	if err == nil {
		t.Fatal("expected error for symlinked service dir, got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected error mentioning symlink, got: %v", err)
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
			// IDE.Enabled is nil, Type="db" → default false
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
