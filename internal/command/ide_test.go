package command

import (
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

	if err := renderIDETemplate(minimalDevcontainerTpl, "devcontainer.json", data, dest); err != nil {
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

	if err := renderIDETemplate(minimalVscodeLaunchTpl, "launch.json", data, dest); err != nil {
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

	if err := renderIDETemplate(minimalVscodeSettingsTpl, "settings.json", data, dest); err != nil {
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

	if err := renderIDETemplate(minimalVscodeLaunchTpl, "file.json", data, dest); err != nil {
		t.Fatalf("renderIDETemplate should create parent dirs: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("expected file to exist at %s: %v", dest, err)
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
				"main": {Type: "app"},
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
				"db": {Name: "db", Reason: "disabled-by-policy"},
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
				"app": {Name: "app", Reason: "disabled-by-policy"},
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
				"main": {Name: "main", Reason: "disabled-by-policy"},
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
