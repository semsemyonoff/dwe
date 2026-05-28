package render

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/shared/pathsafe"
	"devbox-cli/internal/shared/render"
	"devbox-cli/internal/templates/ide"
	"devbox-cli/internal/templates/manifest"
)

// Minimal inline templates used by RenderTemplateFile unit tests.
const minimalDevcontainerTpl = `{"name":"{{ .Project.Name }}","service":"{{ .ServiceCfg.Container }}","workspaceFolder":"{{ .ServiceCfg.DirInternal }}","forwardPorts":[{{ .ServiceCfg.Port "http" }}]}`
const minimalVscodeLaunchTpl = `{"type":"php","pathMappings":{"{{ .ServiceCfg.WorkDirInternal }}":"${workspaceFolder}/src"}}`
const minimalVscodeSettingsTpl = `{"php.validate.executablePath":"/usr/local/bin/php","editor.formatOnSave":true}`

// setupIDEPackTemplates writes an IDE template pack at <dir>/devbox/templates/ide/<packName>/.
// Every key is treated as a "from" path (typically ending in .tmpl); a manifest.yml is
// auto-generated whose `to` paths are the same path with any trailing .tmpl stripped.
// This matches the legacy walk-based behavior, so tests written against the old layout
// continue to exercise the manifest-driven renderer.
func setupIDEPackTemplates(t *testing.T, dir, packName string, files map[string]string) {
	t.Helper()
	packDir := filepath.Join(dir, "devbox", "templates", "ide", packName)
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("create pack dir: %v", err)
	}
	var keys []string
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var manifestBody strings.Builder
	if len(keys) > 0 {
		manifestBody.WriteString("render:\n")
		for _, rel := range keys {
			to := strings.TrimSuffix(rel, ".tmpl")
			manifestBody.WriteString("  - {from: ")
			manifestBody.WriteString(rel)
			manifestBody.WriteString(", to: ")
			manifestBody.WriteString(to)
			manifestBody.WriteString("}\n")
		}
	}
	for _, rel := range keys {
		path := filepath.Join(packDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create template dir for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(files[rel]), 0o644); err != nil {
			t.Fatalf("write template %s: %v", rel, err)
		}
	}
	if manifestBody.Len() > 0 {
		manifestPath := filepath.Join(packDir, "manifest.yml")
		if err := os.WriteFile(manifestPath, []byte(manifestBody.String()), 0o644); err != nil {
			t.Fatalf("write manifest.yml: %v", err)
		}
	}
}

// makeIDECfg returns a DevboxConfig configured for IDE rendering tests.
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
				Ports:           map[string]int{"http": 80},
			},
		},
		Raw: map[string]any{},
	}
}

// writeIDEPackFile writes a single file into <projectRoot>/devbox/templates/ide/test/<rel>.
func writeIDEPackFile(t *testing.T, projectRoot, rel, content string) {
	t.Helper()
	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide", "test")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("mkdir pack: %v", err)
	}
	full := filepath.Join(packDir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir parent of %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestRenderIDETemplateFile_devcontainer verifies template substitution.
func TestRenderIDETemplateFile_devcontainer(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "main")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatalf("create hub dir: %v", err)
	}
	writeIDEPackFile(t, projectRoot, "devcontainer.json.tmpl", minimalDevcontainerTpl)

	data := ide.TemplateData{
		Project: config.ProjectConfig{Name: "myapp"},
		Service: "main",
		ServiceCfg: config.ServiceConfig{
			Container:       "app-main",
			DirInternal:     "/workspace",
			WorkDirInternal: "/workspace/src",
			Ports:           map[string]int{"http": 8080},
		},
		Cfg: &config.DevboxConfig{Raw: map[string]any{}},
	}

	if _, err := ide.RenderTemplateFile(projectRoot, "test", "devcontainer.json.tmpl", data, "devcontainer.json", hubDir, projectRoot); err != nil {
		t.Fatalf("RenderTemplateFile: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(hubDir, "devcontainer.json"))
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	content := string(got)

	checks := []struct{ want, label string }{
		{`"name":"myapp"`, "project name"},
		{`"service":"app-main"`, "container name"},
		{`"workspaceFolder":"/workspace"`, "workspaceFolder"},
		{`8080`, "port"},
	}
	for _, c := range checks {
		if !strings.Contains(content, c.want) {
			t.Errorf("output missing %s (%q)\ngot:\n%s", c.label, c.want, content)
		}
	}
}

// TestRenderIDETemplateFile_createsParentDirs verifies parent directories are created.
func TestRenderIDETemplateFile_createsParentDirs(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "main")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatalf("create hub dir: %v", err)
	}
	writeIDEPackFile(t, projectRoot, "launch.json.tmpl", minimalVscodeLaunchTpl)

	data := ide.TemplateData{
		ServiceCfg: config.ServiceConfig{
			DirInternal:     "/workspace",
			WorkDirInternal: "/workspace/src",
		},
		Cfg: &config.DevboxConfig{Raw: map[string]any{}},
	}
	dest := filepath.Join("nested", "deep", "file.json")
	if _, err := ide.RenderTemplateFile(projectRoot, "test", "launch.json.tmpl", data, dest, hubDir, projectRoot); err != nil {
		t.Fatalf("RenderTemplateFile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hubDir, dest)); err != nil {
		t.Errorf("expected nested file: %v", err)
	}
}

// TestRenderIDETemplateFile_serviceDirContainment rejects dest escaping hub.
func TestRenderIDETemplateFile_serviceDirContainment(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "main")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatalf("create svc dir: %v", err)
	}
	writeIDEPackFile(t, projectRoot, "x.tmpl", "{}")

	dest := "../sibling/file.json"
	_, err := ide.RenderTemplateFile(projectRoot, "test", "x.tmpl", ide.TemplateData{Cfg: &config.DevboxConfig{Raw: map[string]any{}}}, dest, hubDir, projectRoot)
	if err == nil {
		t.Fatal("expected error when dest escapes service dir")
	}
	if !strings.Contains(err.Error(), "escape") {
		t.Errorf("expected escape error, got: %v", err)
	}
}

// TestRenderIDETemplateFile_symlinkDir rejects symlinked intermediate dir.
func TestRenderIDETemplateFile_symlinkDir(t *testing.T) {
	projectRoot := t.TempDir()
	outside := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "main")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(hubDir, ".devcontainer")); err != nil {
		t.Fatal(err)
	}
	writeIDEPackFile(t, projectRoot, "x.tmpl", "{}")

	_, err := ide.RenderTemplateFile(projectRoot, "test", "x.tmpl", ide.TemplateData{Cfg: &config.DevboxConfig{Raw: map[string]any{}}}, ".devcontainer/devcontainer.json", hubDir, projectRoot)
	if err == nil {
		t.Fatal("expected error when destination dir is a symlink outside project root")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink error, got: %v", err)
	}
}

// TestRenderIDETemplateFile_symlinkFile rejects symlinked dest file.
func TestRenderIDETemplateFile_symlinkFile(t *testing.T) {
	projectRoot := t.TempDir()
	outside := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "main", ".devcontainer")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "evil.json")
	if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	absHubDir := filepath.Join(projectRoot, "services", "main")
	dest := filepath.Join(absHubDir, ".devcontainer", "devcontainer.json")
	if err := os.Symlink(target, dest); err != nil {
		t.Fatal(err)
	}
	writeIDEPackFile(t, projectRoot, "x.tmpl", "{}")

	_, err := ide.RenderTemplateFile(projectRoot, "test", "x.tmpl", ide.TemplateData{Cfg: &config.DevboxConfig{Raw: map[string]any{}}}, ".devcontainer/devcontainer.json", absHubDir, projectRoot)
	if err == nil {
		t.Fatal("expected error when destination file is a symlink")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink error, got: %v", err)
	}
}

// TestRenderIDETemplateFile_overrideHit verifies sibling .local override wins.
func TestRenderIDETemplateFile_overrideHit(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "main")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeIDEPackFile(t, projectRoot, "settings.json.tmpl", `{"src":"canonical"}`)
	overrideDir := filepath.Join(projectRoot, "devbox", "templates", "ide", "test.local")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overrideDir, "settings.json.tmpl"), []byte(`{"src":"override"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	fromOverride, err := ide.RenderTemplateFile(projectRoot, "test", "settings.json.tmpl", ide.TemplateData{Cfg: &config.DevboxConfig{Raw: map[string]any{}}}, "settings.json", hubDir, projectRoot)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !fromOverride {
		t.Error("expected fromOverride=true")
	}
	got, err := os.ReadFile(filepath.Join(hubDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "override") {
		t.Errorf("expected override content, got %q", got)
	}
}

// TestResolveIDETemplatePack_explicit verifies explicit pack resolution.
func TestResolveIDETemplatePack_explicit(t *testing.T) {
	projectRoot := t.TempDir()
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
		{"explicit pack resolves", "custom", "custom", false},
		{"explicit pack missing - error", "missing", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := config.ServiceConfig{
				Type:    "app",
				Enabled: true,
				Dir:     "services/main",
				Render:  config.ServiceRenderConfig{IDE: config.ServiceIDEConfig{Template: tt.template}},
			}
			pack, packName, found, err := ide.ResolveTemplatePack(svc, projectRoot, "main")
			if (err != nil) != tt.wantError {
				t.Errorf("want error=%v, got %v", tt.wantError, err)
			}
			if !tt.wantError {
				if !found {
					t.Errorf("want found=true, got found=false")
				}
				if !strings.Contains(pack, tt.wantPack) {
					t.Errorf("want pack containing %q, got %q", tt.wantPack, pack)
				}
				if packName != tt.wantPack {
					t.Errorf("want packName %q, got %q", tt.wantPack, packName)
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

	svc := config.ServiceConfig{Type: "app", Enabled: true, Dir: "services/main"}
	pack, packName, found, err := ide.ResolveTemplatePack(svc, projectRoot, "unknown")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !found {
		t.Fatalf("want found=true, got found=false")
	}
	if !strings.HasSuffix(pack, "default") || packName != "default" {
		t.Errorf("want default pack, got pack=%q packName=%q", pack, packName)
	}
}

// TestResolveIDETemplatePack_allMissing verifies found=false when no packs exist.
func TestResolveIDETemplatePack_allMissing(t *testing.T) {
	projectRoot := t.TempDir()
	svc := config.ServiceConfig{Type: "app", Enabled: true, Dir: "services/main"}
	_, _, found, err := ide.ResolveTemplatePack(svc, projectRoot, "myservice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected found=false when no packs exist")
	}
}

// TestResolveIDETemplatePack_implicitPriority verifies service-name beats default.
func TestResolveIDETemplatePack_implicitPriority(t *testing.T) {
	projectRoot := t.TempDir()
	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".vscode/settings.json.tmpl": `{"source":"default"}`,
	})
	setupIDEPackTemplates(t, projectRoot, "main", map[string]string{
		".vscode/settings.json.tmpl": `{"source":"main"}`,
	})

	svc := config.ServiceConfig{Type: "app", Enabled: true, Dir: "services/main"}
	pack, packName, found, err := ide.ResolveTemplatePack(svc, projectRoot, "main")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !found {
		t.Fatalf("want found=true, got found=false")
	}
	if !strings.HasSuffix(pack, "main") || packName != "main" {
		t.Errorf("want main pack, got pack=%q packName=%q", pack, packName)
	}
}

// TestResolveIDETemplatePack_explicitStrictSemantics verifies explicit does NOT fall back.
func TestResolveIDETemplatePack_explicitStrictSemantics(t *testing.T) {
	projectRoot := t.TempDir()
	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".devcontainer/devcontainer.json.tmpl": "default-dc",
	})
	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		Render:  config.ServiceRenderConfig{IDE: config.ServiceIDEConfig{Template: "main-deubg"}},
	}
	_, _, found, err := ide.ResolveTemplatePack(svc, projectRoot, "main")
	if err == nil {
		t.Fatal("expected error for typo")
	}
	if found {
		t.Fatal("expected found=false when error occurs")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found', got: %v", err)
	}
}

// TestResolveIDETemplatePack_packIsSymlink verifies symlinked pack root rejected.
func TestResolveIDETemplatePack_packIsSymlink(t *testing.T) {
	projectRoot := t.TempDir()
	realPack := filepath.Join(projectRoot, "real-pack")
	if err := os.MkdirAll(realPack, 0o755); err != nil {
		t.Fatal(err)
	}
	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realPack, filepath.Join(packDir, "custom")); err != nil {
		t.Fatal(err)
	}
	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		Render:  config.ServiceRenderConfig{IDE: config.ServiceIDEConfig{Template: "custom"}},
	}
	_, _, found, err := ide.ResolveTemplatePack(svc, projectRoot, "main")
	if err == nil {
		t.Fatal("expected error for symlinked pack")
	}
	if found {
		t.Fatal("expected found=false when error occurs")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink error, got: %v", err)
	}
}

// TestResolveIDETemplatePack_invalidExplicitTemplateKey rejects path separators.
func TestResolveIDETemplatePack_invalidExplicitTemplateKey(t *testing.T) {
	projectRoot := t.TempDir()
	svc := config.ServiceConfig{
		Type:    "app",
		Enabled: true,
		Dir:     "services/main",
		Render:  config.ServiceRenderConfig{IDE: config.ServiceIDEConfig{Template: "foo/bar"}},
	}
	_, _, found, err := ide.ResolveTemplatePack(svc, projectRoot, "main")
	if err == nil {
		t.Fatal("want error for invalid render.ide.template")
	}
	if found {
		t.Fatal("expected found=false when error occurs")
	}
	if !strings.Contains(err.Error(), "invalid render.ide.template") {
		t.Errorf("got %q", err.Error())
	}
}

// TestResolveIDETemplatePack_invalidServiceName silently skips an
// identifier-unsafe service name as an implicit pack candidate; with no
// default pack the resolver returns found=false.
func TestResolveIDETemplatePack_invalidServiceName(t *testing.T) {
	projectRoot := t.TempDir()
	svc := config.ServiceConfig{Type: "app", Enabled: true, Dir: "services/main"}
	_, _, found, err := ide.ResolveTemplatePack(svc, projectRoot, "foo/bar")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("want found=false (no pack found)")
	}
}

// TestResolveIDETemplatePack_emptyServiceName silently skips empty service name;
// with no default pack it returns found=false.
func TestResolveIDETemplatePack_emptyServiceName(t *testing.T) {
	projectRoot := t.TempDir()
	svc := config.ServiceConfig{Type: "app", Enabled: true, Dir: "services/main"}
	_, _, found, err := ide.ResolveTemplatePack(svc, projectRoot, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("want found=false (no pack found)")
	}
}

// TestResolveIDETemplatePack_leadingDotServiceName allows leading-dot service names
// but returns found=false when no pack is present.
func TestResolveIDETemplatePack_leadingDotServiceName(t *testing.T) {
	projectRoot := t.TempDir()
	svc := config.ServiceConfig{Type: "app", Enabled: true, Dir: "services/hidden"}
	_, _, found, err := ide.ResolveTemplatePack(svc, projectRoot, ".hidden")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("want found=false (no pack found)")
	}
}

// TestLoadIDEManifest_missing verifies missing manifest produces ErrManifestMissing.
func TestLoadIDEManifest_missing(t *testing.T) {
	projectRoot := t.TempDir()
	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide", "default")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "x.tmpl"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ide.LoadManifest(packDir)
	if err == nil {
		t.Fatal("expected error for missing manifest")
	}
	if !errors.Is(err, manifest.ErrManifestMissing) {
		t.Errorf("want ErrManifestMissing in chain, got %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want os.ErrNotExist in chain, got %v", err)
	}
}

// TestRenderIDEConfigs_missingManifest verifies friendly migration error.
func TestRenderIDEConfigs_missingManifest(t *testing.T) {
	projectRoot := t.TempDir()
	packDir := filepath.Join(projectRoot, "devbox", "templates", "ide", "default")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write some .tmpl files but no manifest.yml.
	if err := os.WriteFile(filepath.Join(packDir, "settings.json.tmpl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := makeIDECfg("main")
	svc := cfg.Services["main"]

	var buf strings.Builder
	w := render.NewWriter(&buf)
	err := renderIDEConfigs(projectRoot, "main", svc, cfg, w)
	if err == nil {
		t.Fatal("expected error for missing manifest.yml")
	}
	if !strings.Contains(err.Error(), "manifest") {
		t.Errorf("expected manifest error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "manifest.yml") || !strings.Contains(err.Error(), "migration") {
		t.Errorf("expected migration hint, got: %v", err)
	}
}

// TestRenderIDEConfigs_packResolution verifies pack resolution and rendering.
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
	for _, rel := range []string{".devcontainer/devcontainer.json", ".vscode/settings.json"} {
		if _, err := os.Stat(filepath.Join(projectRoot, "services", "main", rel)); err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
		}
	}
}

// TestRenderIDEConfigs_packNotFound verifies error when no pack found.
func TestRenderIDEConfigs_packNotFound(t *testing.T) {
	projectRoot := t.TempDir()
	cfg := makeIDECfg("main")
	svc := cfg.Services["main"]

	var buf strings.Builder
	w := render.NewWriter(&buf)
	err := renderIDEConfigs(projectRoot, "main", svc, cfg, w)
	if err != nil {
		t.Fatalf("expected implicit missing pack to warn and skip, got error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "skipped") || !strings.Contains(output, "no template pack found") {
		t.Errorf("expected warning about missing template pack in output, got: %s", output)
	}
}

// TestRenderIDEConfigs_explicitPackNotFound verifies that an explicit template reference that doesn't exist produces an error.
func TestRenderIDEConfigs_explicitPackNotFound(t *testing.T) {
	projectRoot := t.TempDir()
	cfg := makeIDECfg("main")
	svc := cfg.Services["main"]
	svc.Render.IDE.Template = "nonexistent"

	var buf strings.Builder
	w := render.NewWriter(&buf)
	err := renderIDEConfigs(projectRoot, "main", svc, cfg, w)
	if err == nil {
		t.Fatal("expected error for explicit missing template pack")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

// TestRenderIDEConfigs_dotDirRejected verifies a service with dir "." is rejected.
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
		t.Fatal("expected error for dir '.'")
	}
	if !strings.Contains(err.Error(), "escapes project root") {
		t.Errorf("expected 'escapes project root', got: %v", err)
	}
}

// TestRenderIDEConfigs_perServiceOverride verifies explicit template selection.
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
	svc.Render.IDE.Template = "main-debug"

	var buf strings.Builder
	w := render.NewWriter(&buf)
	if err := renderIDEConfigs(projectRoot, "main", svc, cfg, w); err != nil {
		t.Fatalf("renderIDEConfigs: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(projectRoot, "services", "main", ".vscode", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "debug") {
		t.Errorf("expected debug pack, got: %s", content)
	}
}

// TestRenderIDEConfigs_serviceNameFallback verifies service-name pack beats default.
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

	var buf strings.Builder
	w := render.NewWriter(&buf)
	if err := renderIDEConfigs(projectRoot, "main", svc, cfg, w); err != nil {
		t.Fatalf("renderIDEConfigs: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(projectRoot, "services", "main", ".vscode", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"source": "main"`) {
		t.Errorf("expected main pack, got: %s", content)
	}
}

// TestRenderIDEConfigs_defaultOnly verifies default pack used when only it exists.
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
	content, err := os.ReadFile(filepath.Join(projectRoot, "services", "unknown", ".vscode", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"source": "default"`) {
		t.Errorf("expected default pack, got: %s", content)
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
	content, err := os.ReadFile(filepath.Join(projectRoot, "services", "main", ".devcontainer", "devcontainer.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(content)
	checks := []struct{ want, label string }{
		{`"name":"laravel"`, "project name"},
		{`"service":"app-main"`, "container name"},
		{`"workspaceFolder":"/workspace"`, "workspace folder"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.want) {
			t.Errorf("output missing %s (%q)", c.label, c.want)
		}
	}
}

// TestRenderIDEConfigs_overrideEmitsInfo verifies the override info line.
func TestRenderIDEConfigs_overrideEmitsInfo(t *testing.T) {
	projectRoot := t.TempDir()
	setupIDEPackTemplates(t, projectRoot, "default", map[string]string{
		".vscode/settings.json.tmpl": `{"src":"canonical"}`,
	})
	overrideDir := filepath.Join(projectRoot, "devbox", "templates", "ide", "default.local", ".vscode")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overrideDir, "settings.json.tmpl"), []byte(`{"src":"override"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := makeIDECfg("main")
	svc := cfg.Services["main"]

	var buf strings.Builder
	w := render.NewWriter(&buf)
	if err := renderIDEConfigs(projectRoot, "main", svc, cfg, w); err != nil {
		t.Fatalf("renderIDEConfigs: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "local override") {
		t.Errorf("expected info line about local override, got: %s", out)
	}
	content, err := os.ReadFile(filepath.Join(projectRoot, "services", "main", ".vscode", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "override") {
		t.Errorf("expected override content, got: %s", content)
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
			name:      "no extends",
			services:  map[string]config.ServiceConfig{"main": {Type: "app"}},
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
			name:      "unknown service - treated as depth 0",
			services:  map[string]config.ServiceConfig{"main": {Type: "app"}},
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
			depth, capped := ide.ExtendsDepth(tt.services, tt.svcName)
			if depth != tt.wantDepth {
				t.Errorf("depth: want %d, got %d", tt.wantDepth, depth)
			}
			if capped != tt.wantCapped {
				t.Errorf("capped: want %v, got %v", tt.wantCapped, capped)
			}
		})
	}
}

// TestValidateIDETemplateKey tests template key validation.
func TestValidateIDETemplateKey(t *testing.T) {
	tests := []struct {
		key     string
		wantErr bool
	}{
		{"", false},
		{"main", false},
		{"main-debug", false},
		{"foo/bar", true},
		{"foo\\bar", true},
		{"/abs/path", true},
		{".hidden", true},
		{"..", true},
		{"foo/../bar", true},
		{"../escape", true},
		{"./foo", true},
		{".", true},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			err := ide.ValidateTemplateKey(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("key %q: want err=%v, got %v", tt.key, tt.wantErr, err)
			}
		})
	}
}

// TestSelectIDEServices tests selection and collision resolution.
func TestSelectIDEServices(t *testing.T) {
	trueVal := true
	falseVal := false
	tests := []struct {
		name           string
		services       map[string]config.ServiceConfig
		wantSelected   []string
		wantSkippedMap map[string]ide.SkippedService
	}{
		{
			name: "all enabled distinct dirs",
			services: map[string]config.ServiceConfig{
				"svc1": {Type: "app", Enabled: true, Dir: "./services/svc1"},
				"svc2": {Type: "app", Enabled: true, Dir: "./services/svc2"},
			},
			wantSelected: []string{"svc1", "svc2"},
		},
		{
			name: "IDERenderEnabled=false dropped",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app", Enabled: true, Dir: "./services/main"},
				"db":   {Type: "db", Enabled: true, Dir: "./services/db"},
			},
			wantSelected: []string{"main"},
			wantSkippedMap: map[string]ide.SkippedService{
				"db": {Name: "db", Reason: "ide-policy"},
			},
		},
		{
			name: "Enabled=false dropped",
			services: map[string]config.ServiceConfig{
				"app":  {Type: "app", Enabled: false, Dir: "./services/app"},
				"main": {Type: "app", Enabled: true, Dir: "./services/main"},
			},
			wantSelected: []string{"main"},
			wantSkippedMap: map[string]ide.SkippedService{
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
					Render:  config.ServiceRenderConfig{IDE: config.ServiceIDEConfig{Enabled: &trueVal}},
				},
			},
			wantSelected: []string{"db"},
		},
		{
			name: "explicit Render.IDE.Enabled=false overrides type",
			services: map[string]config.ServiceConfig{
				"main": {
					Type:    "app",
					Enabled: true,
					Dir:     "./services/main",
					Render:  config.ServiceRenderConfig{IDE: config.ServiceIDEConfig{Enabled: &falseVal}},
				},
			},
			wantSkippedMap: map[string]ide.SkippedService{
				"main": {Name: "main", Reason: "ide-disabled"},
			},
		},
		{
			name: "child extends parent - child wins",
			services: map[string]config.ServiceConfig{
				"main":       {Type: "app", Enabled: true, Dir: "./services/main"},
				"main-debug": {Type: "app", Enabled: true, Dir: "./services/main", Extends: "main"},
			},
			wantSelected: []string{"main-debug"},
			wantSkippedMap: map[string]ide.SkippedService{
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
			selected, skipped := ide.SelectServices(tt.services)
			if len(selected) != len(tt.wantSelected) {
				t.Errorf("selected count: want %d, got %d (%v)", len(tt.wantSelected), len(selected), selected)
			}
			for i, name := range selected {
				if i < len(tt.wantSelected) && name != tt.wantSelected[i] {
					t.Errorf("selected[%d]: want %q, got %q", i, tt.wantSelected[i], name)
				}
			}
			skippedMap := make(map[string]ide.SkippedService)
			for _, s := range skipped {
				skippedMap[s.Name] = s
			}
			if len(skippedMap) != len(tt.wantSkippedMap) {
				t.Errorf("skipped count: want %d, got %d (%v)", len(tt.wantSkippedMap), len(skippedMap), skippedMap)
			}
			for name, want := range tt.wantSkippedMap {
				got, ok := skippedMap[name]
				if !ok {
					t.Errorf("skipped[%q] not found", name)
					continue
				}
				if got.Reason != want.Reason {
					t.Errorf("skipped[%q].Reason: want %q, got %q", name, want.Reason, got.Reason)
				}
				if want.Reason == "lost-collision" {
					if got.Dir != want.Dir || got.Winner != want.Winner {
						t.Errorf("skipped[%q]: want %+v, got %+v", name, want, got)
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
		t.Fatal(err)
	}
	if err := pathsafe.CheckNoSymlinks(root, filepath.Join(root, "nonexistent", "path"), "test"); err != nil {
		t.Errorf("non-existent: %v", err)
	}
	if err := pathsafe.CheckNoSymlinks(root, realSub, "test"); err != nil {
		t.Errorf("real path: %v", err)
	}
	linkPath := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "real"), linkPath); err != nil {
		t.Fatal(err)
	}
	if err := pathsafe.CheckNoSymlinks(root, filepath.Join(linkPath, "sub"), "test"); err == nil {
		t.Errorf("path through symlink: expected error")
	}
	if err := pathsafe.CheckNoSymlinks(root, linkPath, "test"); err == nil {
		t.Errorf("symlink as target: expected error")
	}
	if err := pathsafe.CheckNoSymlinks(root, filepath.Dir(root), "test"); err == nil {
		t.Errorf("path outside root: expected error")
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
			name:        "valid",
			serviceName: "main",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app", Enabled: true, Dir: "./services/main"},
			},
		},
		{
			name:        "unknown",
			serviceName: "missing",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app", Enabled: true, Dir: "./services/main"},
			},
			wantErrMsg: `service "missing" not found in config`,
		},
		{
			name:        "disabled",
			serviceName: "main",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app", Enabled: false, Dir: "./services/main"},
			},
			wantErrMsg: `service "main" is disabled at the project level`,
		},
		{
			name:        "ide.enabled: false",
			serviceName: "main",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app", Enabled: true, Dir: "./services/main", Render: config.ServiceRenderConfig{IDE: config.ServiceIDEConfig{Enabled: &falseVal}}},
			},
			wantErrMsg: `service "main" has render.ide.enabled: false`,
		},
		{
			name:        "non-app type no explicit",
			serviceName: "db",
			services: map[string]config.ServiceConfig{
				"db": {Type: "db", Enabled: true, Dir: "./services/db"},
			},
			wantErrMsg: `does not participate in IDE rendering by default`,
		},
		{
			name:        "no dir",
			serviceName: "main",
			services: map[string]config.ServiceConfig{
				"main": {Type: "app", Enabled: true, Render: config.ServiceRenderConfig{IDE: config.ServiceIDEConfig{Enabled: &trueVal}}},
			},
			wantErrMsg: `service "main" has no dir`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExplicitIDEArg(tt.serviceName, tt.services)
			if tt.wantErrMsg == "" {
				if err != nil {
					t.Errorf("unexpected: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErrMsg)
			}
			if !strings.Contains(err.Error(), tt.wantErrMsg) {
				t.Errorf("error %q should contain %q", err.Error(), tt.wantErrMsg)
			}
		})
	}
}

// TestResolveIDEHubAnchor verifies hub-anchor resolution.
func TestResolveIDEHubAnchor(t *testing.T) {
	falseVal := false
	tests := []struct {
		name     string
		input    string
		services map[string]config.ServiceConfig
		want     string
	}{
		{
			name:  "no siblings",
			input: "solo",
			services: map[string]config.ServiceConfig{
				"solo": {Type: "app", Enabled: true, Dir: "./services/solo"},
			},
			want: "solo",
		},
		{
			name:  "child wins",
			input: "main",
			services: map[string]config.ServiceConfig{
				"main":       {Type: "app", Enabled: true, Dir: "./services/main"},
				"main-debug": {Type: "app", Enabled: true, Dir: "./services/main", Extends: "main"},
			},
			want: "main-debug",
		},
		{
			name:  "variant disabled - parent wins",
			input: "main",
			services: map[string]config.ServiceConfig{
				"main":       {Type: "app", Enabled: true, Dir: "./services/main"},
				"main-debug": {Type: "app", Enabled: false, Dir: "./services/main", Extends: "main"},
			},
			want: "main",
		},
		{
			name:  "variant ide.enabled=false - parent wins",
			input: "main",
			services: map[string]config.ServiceConfig{
				"main":       {Type: "app", Enabled: true, Dir: "./services/main"},
				"main-debug": {Type: "app", Enabled: true, Dir: "./services/main", Extends: "main", Render: config.ServiceRenderConfig{IDE: config.ServiceIDEConfig{Enabled: &falseVal}}},
			},
			want: "main",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveIDEHubAnchor(tt.input, tt.services)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRenderIDETemplateFile_cfgRawDotAccess verifies .Cfg.Raw dot syntax.
func TestRenderIDETemplateFile_cfgRawDotAccess(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "main")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeIDEPackFile(t, projectRoot, "settings.json.tmpl",
		`{"prefix":"{{ .Cfg.Raw.git.project_prefix }}","hook":"{{ index (index .Cfg.Raw.git.hooks .Service) "pre_commit" }}"}`)

	cfg := &config.DevboxConfig{Raw: map[string]any{
		"git": map[string]any{
			"project_prefix": "PRJ",
			"hooks": map[string]any{
				"main": map[string]any{"pre_commit": "echo hi"},
			},
		},
	}}
	data := ide.TemplateData{Service: "main", Cfg: cfg}
	if _, err := ide.RenderTemplateFile(projectRoot, "test", "settings.json.tmpl", data, "settings.json", hubDir, projectRoot); err != nil {
		t.Fatalf("render: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(hubDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "PRJ") || !strings.Contains(string(got), "echo hi") {
		t.Errorf("content=%q", got)
	}
}

// TestRenderIDETemplateFile_cfgRawNonIdentifierKey verifies index escape hatch.
func TestRenderIDETemplateFile_cfgRawNonIdentifierKey(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "main")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeIDEPackFile(t, projectRoot, "settings.json.tmpl",
		`{"token":"{{ index .Cfg.Raw "my-tool" "api-key" }}"}`)

	cfg := &config.DevboxConfig{Raw: map[string]any{
		"my-tool": map[string]any{"api-key": "VALUE"},
	}}
	data := ide.TemplateData{Service: "main", Cfg: cfg}
	if _, err := ide.RenderTemplateFile(projectRoot, "test", "settings.json.tmpl", data, "settings.json", hubDir, projectRoot); err != nil {
		t.Fatalf("render: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(hubDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "VALUE") {
		t.Errorf("content=%q", got)
	}
}

// TestRenderIDETemplateFile_cfgRawMissingKey verifies missingkey=error surfaces typos.
func TestRenderIDETemplateFile_cfgRawMissingKey(t *testing.T) {
	projectRoot := t.TempDir()
	hubDir := filepath.Join(projectRoot, "services", "main")
	if err := os.MkdirAll(hubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeIDEPackFile(t, projectRoot, "settings.json.tmpl",
		`{"prefix":"{{ .Cfg.Raw.git.project_prefix }}"}`)

	cfg := &config.DevboxConfig{Raw: map[string]any{}}
	data := ide.TemplateData{Service: "main", Cfg: cfg}
	_, err := ide.RenderTemplateFile(projectRoot, "test", "settings.json.tmpl", data, "settings.json", hubDir, projectRoot)
	if err == nil {
		t.Fatal("expected missingkey error")
	}
	if !strings.Contains(err.Error(), "git") {
		t.Errorf("expected error to mention 'git', got: %v", err)
	}
}

// TestRenderIDETemplateFile_backwardCompat verifies output byte-identical when
// templates do not reference .Cfg.
func TestRenderIDETemplateFile_backwardCompat(t *testing.T) {
	render := func(cfg *config.DevboxConfig) []byte {
		projectRoot := t.TempDir()
		hubDir := filepath.Join(projectRoot, "services", "main")
		if err := os.MkdirAll(hubDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeIDEPackFile(t, projectRoot, "settings.json.tmpl",
			`{"name":"{{ .Project.Name }}","port":{{ .ServiceCfg.Port "http" }}}`)
		data := ide.TemplateData{
			Project:    config.ProjectConfig{Name: "myapp"},
			Service:    "main",
			ServiceCfg: config.ServiceConfig{Ports: map[string]int{"http": 8080}},
			Cfg:        cfg,
		}
		if _, err := ide.RenderTemplateFile(projectRoot, "test", "settings.json.tmpl", data, "settings.json", hubDir, projectRoot); err != nil {
			t.Fatalf("render: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(hubDir, "settings.json"))
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
