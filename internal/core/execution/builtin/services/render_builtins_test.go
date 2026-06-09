package services

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/generatedstore"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

// writeConfigPack writes a config template pack (manifest + sources) under
// workspace/templates/config/<name>/ within root.
func writeConfigPack(t *testing.T, root, name, manifest string, sources map[string]string) {
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

// renderExecCtx builds an ExecContext rooted at root with a single enabled app
// service ("main") whose hub dir is services/main, plus the given Raw map and
// generated fields. The Output buffer is returned for assertions.
func renderExecCtx(t *testing.T, root string, raw map[string]any, generated map[string]config.GeneratedField) (spec.ExecContext, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	return spec.ExecContext{
		Config: &config.DweConfig{
			Raw: raw,
			Services: map[string]config.ServiceConfig{
				"main": {
					Type:      config.ServiceTypeApp,
					Enabled:   true,
					Dir:       "services/main",
					Generated: generated,
				},
			},
		},
		ProjectRoot: root,
		Output:      render.NewWriter(buf),
	}, buf
}

// --- ConfigsRender ---

func TestServiceConfigsRender_Validate(t *testing.T) {
	b := ConfigsRender{}
	if err := b.Validate(nil); err == nil {
		t.Fatal("expected error for missing service")
	}
	if err := b.Validate(map[string]any{"service": "main"}); err != nil {
		t.Fatalf("default mode should be valid: %v", err)
	}
	if err := b.Validate(map[string]any{"service": "main", "mode": "replace"}); err != nil {
		t.Fatalf("replace mode should be valid: %v", err)
	}
	if err := b.Validate(map[string]any{"service": "main", "mode": "update"}); err == nil {
		t.Fatal("expected error for unsupported mode 'update'")
	}
}

func TestServiceConfigsRender_Describe(t *testing.T) {
	desc := ConfigsRender{}.Describe(map[string]any{"service": "main"})
	if !strings.Contains(desc, "main") || !strings.Contains(desc, "replace") {
		t.Errorf("unexpected describe: %q", desc)
	}
}

func TestServiceConfigsRender_Run_RendersAndReplays(t *testing.T) {
	root := t.TempDir()
	writeConfigPack(t, root, "default",
		"render:\n  - from: env.tmpl\n    to: src/.env\n",
		map[string]string{"env.tmpl": "DB=${databases.magento}\nAPP_KEY=${generated.app_key}\n"})

	// Seed the store so render replays the harvested value.
	store := generatedstore.New()
	store.SetIfAbsent("main", "app_key", "base64:secret==")
	if err := generatedstore.Save(filepath.Join(root, generatedstore.DefaultRelPath), store); err != nil {
		t.Fatal(err)
	}

	ectx, _ := renderExecCtx(t, root, map[string]any{"databases": map[string]any{"magento": "pgsql"}}, nil)
	if err := (ConfigsRender{}).Run(context.Background(), map[string]any{"service": "main"}, ectx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := mustRead(t, filepath.Join(root, "services", "main", "src", ".env"))
	if got != "DB=pgsql\nAPP_KEY=base64:secret==\n" {
		t.Errorf("rendered content = %q", got)
	}
}

func TestServiceConfigsRender_Run_NoPack_SkipsCleanly(t *testing.T) {
	root := t.TempDir()
	ectx, buf := renderExecCtx(t, root, map[string]any{}, nil)
	if err := (ConfigsRender{}).Run(context.Background(), map[string]any{"service": "main"}, ectx); err != nil {
		t.Fatalf("Run with no pack should succeed: %v", err)
	}
	if !strings.Contains(buf.String(), "no config pack") {
		t.Errorf("expected skip notice, got: %q", buf.String())
	}
}

// --- ConfigsRenderCheck ---

func TestServiceConfigsRenderCheck_Validate(t *testing.T) {
	b := ConfigsRenderCheck{}
	if err := b.Validate(nil); err == nil {
		t.Fatal("expected error for missing service")
	}
	if err := b.Validate(map[string]any{"service": "main"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestServiceConfigsRenderCheck_Run_PassesAfterRender is the motivating test:
// once render writes the targets, the paired check passes; deleting a target
// makes it fail. The check's presence is what forces render to re-run every
// deploy (the hasCheck → Run lever exercised in journal/decision_test.go).
func TestServiceConfigsRenderCheck_Run_PassesAfterRender(t *testing.T) {
	root := t.TempDir()
	writeConfigPack(t, root, "default",
		"render:\n  - from: env.tmpl\n    to: src/.env\n",
		map[string]string{"env.tmpl": "ok\n"})
	ectx, _ := renderExecCtx(t, root, map[string]any{}, nil)

	// Before render: target missing → check fails.
	if err := (ConfigsRenderCheck{}).Run(context.Background(), map[string]any{"service": "main"}, ectx); err == nil {
		t.Fatal("expected check to fail before render")
	}

	// Render, then check passes.
	if err := (ConfigsRender{}).Run(context.Background(), map[string]any{"service": "main"}, ectx); err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := (ConfigsRenderCheck{}).Run(context.Background(), map[string]any{"service": "main"}, ectx); err != nil {
		t.Fatalf("expected check to pass after render: %v", err)
	}

	// Remove the rendered file → check fails again (drives a re-render).
	if err := os.Remove(filepath.Join(root, "services", "main", "src", ".env")); err != nil {
		t.Fatal(err)
	}
	if err := (ConfigsRenderCheck{}).Run(context.Background(), map[string]any{"service": "main"}, ectx); err == nil {
		t.Fatal("expected check to fail after target removed")
	}
}

func TestServiceConfigsRenderCheck_Run_NoPack_NoOp(t *testing.T) {
	root := t.TempDir()
	ectx, _ := renderExecCtx(t, root, map[string]any{}, nil)
	if err := (ConfigsRenderCheck{}).Run(context.Background(), map[string]any{"service": "main"}, ectx); err != nil {
		t.Fatalf("check with no pack should be a no-op: %v", err)
	}
}

// --- GeneratedHarvest ---

func TestServiceGeneratedHarvest_Validate(t *testing.T) {
	b := GeneratedHarvest{}
	if err := b.Validate(nil); err == nil {
		t.Fatal("expected error for missing service")
	}
	if err := b.Validate(map[string]any{"service": "main"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServiceGeneratedHarvest_Run_HarvestsIntoStore(t *testing.T) {
	root := t.TempDir()
	// The service's own generator wrote APP_KEY into src/.env.
	envPath := filepath.Join(root, "services", "main", "src", ".env")
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte("APP_KEY=base64:minted==\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	generated := map[string]config.GeneratedField{
		"app_key": {File: "src/.env", Pattern: `^APP_KEY=(.*)$`},
	}
	ectx, _ := renderExecCtx(t, root, map[string]any{}, generated)
	if err := (GeneratedHarvest{}).Run(context.Background(), map[string]any{"service": "main"}, ectx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	store, err := generatedstore.Load(filepath.Join(root, generatedstore.DefaultRelPath))
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Get("main", "app_key"); got != "base64:minted==" {
		t.Errorf("harvested value = %q, want base64:minted==", got)
	}
}

func TestServiceGeneratedHarvest_Run_WriteIfAbsent(t *testing.T) {
	root := t.TempDir()
	envPath := filepath.Join(root, "services", "main", "src", ".env")
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte("APP_KEY=on-disk\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-existing store value must be preserved (no overwrite).
	store := generatedstore.New()
	store.SetIfAbsent("main", "app_key", "original")
	if err := generatedstore.Save(filepath.Join(root, generatedstore.DefaultRelPath), store); err != nil {
		t.Fatal(err)
	}

	generated := map[string]config.GeneratedField{
		"app_key": {File: "src/.env", Pattern: `^APP_KEY=(.*)$`},
	}
	ectx, _ := renderExecCtx(t, root, map[string]any{}, generated)
	if err := (GeneratedHarvest{}).Run(context.Background(), map[string]any{"service": "main"}, ectx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	reloaded, err := generatedstore.Load(filepath.Join(root, generatedstore.DefaultRelPath))
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Get("main", "app_key"); got != "original" {
		t.Errorf("write-if-absent violated: got %q, want original", got)
	}
}

func TestServiceGeneratedHarvest_Run_NoFields_NoOp(t *testing.T) {
	root := t.TempDir()
	ectx, _ := renderExecCtx(t, root, map[string]any{}, nil)
	if err := (GeneratedHarvest{}).Run(context.Background(), map[string]any{"service": "main"}, ectx); err != nil {
		t.Fatalf("no generated fields should be a no-op: %v", err)
	}
}

func TestServiceGeneratedHarvest_Run_MissingFile_Errors(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "services", "main", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	generated := map[string]config.GeneratedField{
		"app_key": {File: "src/.env", Pattern: `^APP_KEY=(.*)$`},
	}
	ectx, _ := renderExecCtx(t, root, map[string]any{}, generated)
	if err := (GeneratedHarvest{}).Run(context.Background(), map[string]any{"service": "main"}, ectx); err == nil {
		t.Fatal("expected error for missing generated file")
	}
}

// TestServiceGeneratedHarvest_Run_ConcurrentServices guards the lost-update
// race: two harvest steps for different services placed in the same parallel:
// group each load → mutate → save the whole store. Without serialization the
// last writer drops the other service's just-harvested secret. Both keys must
// survive. Run with -race to also catch the concurrent map access.
func TestServiceGeneratedHarvest_Run_ConcurrentServices(t *testing.T) {
	root := t.TempDir()

	svcNames := []string{"alpha", "beta"}
	services := map[string]config.ServiceConfig{}
	for _, name := range svcNames {
		envPath := filepath.Join(root, "services", name, "src", ".env")
		if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(envPath, []byte("APP_KEY=minted-"+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		services[name] = config.ServiceConfig{
			Type:    config.ServiceTypeApp,
			Enabled: true,
			Dir:     filepath.Join("services", name),
			Generated: map[string]config.GeneratedField{
				"app_key": {File: "src/.env", Pattern: `^APP_KEY=(.*)$`},
			},
		}
	}

	cfg := &config.DweConfig{Raw: map[string]any{}, Services: services}

	var wg sync.WaitGroup
	errs := make([]error, len(svcNames))
	for i, name := range svcNames {
		wg.Go(func() {
			ectx := spec.ExecContext{Config: cfg, ProjectRoot: root, Output: render.NewWriter(&bytes.Buffer{})}
			errs[i] = (GeneratedHarvest{}).Run(context.Background(), map[string]any{"service": name}, ectx)
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Run %s: %v", svcNames[i], err)
		}
	}

	store, err := generatedstore.Load(filepath.Join(root, generatedstore.DefaultRelPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range svcNames {
		if got := store.Get(name, "app_key"); got != "minted-"+name {
			t.Errorf("service %s lost its harvested value: got %q, want minted-%s", name, got, name)
		}
	}
}

// mustRead reads path or fails the test.
func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
