package config

import (
	"os"
	"path/filepath"
	"testing"

	projectconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/generatedstore"
)

// writePack writes a canonical config pack (manifest + source files) under
// workspace/templates/config/<name>/.
func writePack(t *testing.T, root, name string, manifest string, sources map[string]string) {
	t.Helper()
	packDir := filepath.Join(root, "workspace", "templates", "config", name)
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("mkdir pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "manifest.yml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	for rel, content := range sources {
		p := filepath.Join(packDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir source dir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write source: %v", err)
		}
	}
}

// writeOverride writes a sibling <name>.local override source file.
func writeOverride(t *testing.T, root, name, rel, content string) {
	t.Helper()
	p := filepath.Join(root, "workspace", "templates", "config", name+".local", rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir override dir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}
}

// newCfg builds a minimal DweConfig with a single enabled app service and a
// Raw map for ${...} dot-path resolution.
func newCfg(svcName, dir string, raw map[string]any) *projectconfig.DweConfig {
	return &projectconfig.DweConfig{
		Raw: raw,
		Services: map[string]projectconfig.ServiceConfig{
			svcName: {
				Type:    projectconfig.ServiceTypeApp,
				Enabled: true,
				Dir:     dir,
			},
		},
	}
}

func TestRenderConfigs_writesUnderHubDir(t *testing.T) {
	root := t.TempDir()
	writePack(t, root, "default", "render:\n  - from: env.tmpl\n    to: src/.env\n", map[string]string{
		"env.tmpl": "DB=${vars.databases.magento}\n",
	})

	cfg := newCfg("main", "services/main", map[string]any{
		"vars": map[string]any{"databases": map[string]any{"magento": "pgsql"}},
	})
	store := generatedstore.New()

	res, err := RenderConfigs(root, cfg, "main", store)
	if err != nil {
		t.Fatalf("RenderConfigs: %v", err)
	}
	if !res.Found || res.Pack != "default" {
		t.Fatalf("unexpected result: %+v", res)
	}
	got := mustRead(t, filepath.Join(root, "services", "main", "src", ".env"))
	if got != "DB=pgsql\n" {
		t.Errorf("rendered content = %q, want %q", got, "DB=pgsql\n")
	}
}

func TestRenderConfigs_localOverride(t *testing.T) {
	root := t.TempDir()
	writePack(t, root, "default", "render:\n  - from: env.tmpl\n    to: src/.env\n", map[string]string{
		"env.tmpl": "canonical\n",
	})
	writeOverride(t, root, "default", "env.tmpl", "overridden\n")

	cfg := newCfg("main", "services/main", map[string]any{})
	res, err := RenderConfigs(root, cfg, "main", generatedstore.New())
	if err != nil {
		t.Fatalf("RenderConfigs: %v", err)
	}
	if len(res.Rendered) != 1 || !res.Rendered[0].FromOverride {
		t.Fatalf("expected fromOverride=true, got %+v", res.Rendered)
	}
	if got := mustRead(t, filepath.Join(root, "services", "main", "src", ".env")); got != "overridden\n" {
		t.Errorf("content = %q, want overridden", got)
	}
}

func TestRenderConfigs_generatedReplay(t *testing.T) {
	root := t.TempDir()
	writePack(t, root, "default", "render:\n  - from: env.tmpl\n    to: src/.env\n", map[string]string{
		"env.tmpl": "APP_KEY=${generated.app_key}\n",
	})

	cfg := newCfg("main", "services/main", map[string]any{})
	store := generatedstore.New()
	store.SetIfAbsent("main", "app_key", "base64:secret==")

	if _, err := RenderConfigs(root, cfg, "main", store); err != nil {
		t.Fatalf("RenderConfigs: %v", err)
	}
	if got := mustRead(t, filepath.Join(root, "services", "main", "src", ".env")); got != "APP_KEY=base64:secret==\n" {
		t.Errorf("content = %q, want replayed app_key", got)
	}
}

func TestRenderConfigs_generatedAbsentRendersEmpty(t *testing.T) {
	root := t.TempDir()
	writePack(t, root, "default", "render:\n  - from: env.tmpl\n    to: src/.env\n", map[string]string{
		"env.tmpl": "APP_KEY=${generated.app_key}\n",
	})

	cfg := newCfg("main", "services/main", map[string]any{})
	if _, err := RenderConfigs(root, cfg, "main", generatedstore.New()); err != nil {
		t.Fatalf("RenderConfigs: %v", err)
	}
	if got := mustRead(t, filepath.Join(root, "services", "main", "src", ".env")); got != "APP_KEY=\n" {
		t.Errorf("content = %q, want empty replay (lenient)", got)
	}
}

func TestRenderConfigs_replaceOverwrites(t *testing.T) {
	root := t.TempDir()
	writePack(t, root, "default", "render:\n  - from: env.tmpl\n    to: src/.env\n", map[string]string{
		"env.tmpl": "fresh\n",
	})
	dest := filepath.Join(root, "services", "main", "src", ".env")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := newCfg("main", "services/main", map[string]any{})
	if _, err := RenderConfigs(root, cfg, "main", generatedstore.New()); err != nil {
		t.Fatalf("RenderConfigs: %v", err)
	}
	if got := mustRead(t, dest); got != "fresh\n" {
		t.Errorf("content = %q, want fresh (replace overwrites)", got)
	}
}

func TestRenderConfigs_pathEscapeRejected(t *testing.T) {
	root := t.TempDir()
	writePack(t, root, "default", "render:\n  - from: env.tmpl\n    to: ../escape\n", map[string]string{
		"env.tmpl": "x\n",
	})

	cfg := newCfg("main", "services/main", map[string]any{})
	if _, err := RenderConfigs(root, cfg, "main", generatedstore.New()); err == nil {
		t.Fatal("expected path-escape error, got nil")
	}
}

func TestRenderConfigs_noPackFound(t *testing.T) {
	root := t.TempDir()
	cfg := newCfg("main", "services/main", map[string]any{})
	res, err := RenderConfigs(root, cfg, "main", generatedstore.New())
	if err != nil {
		t.Fatalf("RenderConfigs: %v", err)
	}
	if res.Found {
		t.Errorf("expected Found=false when no pack resolves, got %+v", res)
	}
}

func TestRenderConfigs_explicitPinNotFound(t *testing.T) {
	root := t.TempDir()
	cfg := newCfg("main", "services/main", map[string]any{})
	svc := cfg.Services["main"]
	svc.Render.Config = &projectconfig.RenderConfigSection{Template: "missing"}
	cfg.Services["main"] = svc

	if _, err := RenderConfigs(root, cfg, "main", generatedstore.New()); err == nil {
		t.Fatal("expected error for unresolvable explicit pin, got nil")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
