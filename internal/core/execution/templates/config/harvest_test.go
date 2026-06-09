package config

import (
	"os"
	"path/filepath"
	"testing"

	projectconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/generatedstore"
)

// writeServiceFile writes content to <root>/<dir>/<rel>, creating parents.
func writeServiceFile(t *testing.T, root, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(root, dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// cfgWithGenerated builds a single-service cfg whose service declares the given
// generated fields.
func cfgWithGenerated(svcName, dir string, generated map[string]projectconfig.GeneratedField) *projectconfig.DweConfig {
	return &projectconfig.DweConfig{
		Raw: map[string]any{},
		Services: map[string]projectconfig.ServiceConfig{
			svcName: {
				Type:      projectconfig.ServiceTypeApp,
				Enabled:   true,
				Dir:       dir,
				Generated: generated,
			},
		},
	}
}

func TestHarvestGenerated_dotenv(t *testing.T) {
	root := t.TempDir()
	writeServiceFile(t, root, "services/main", "configs/.env",
		"APP_NAME=demo\nAPP_KEY=base64:Xa3secret==\nDB_HOST=db\n")

	cfg := cfgWithGenerated("main", "services/main", map[string]projectconfig.GeneratedField{
		"app_key": {File: "configs/.env", Pattern: `^APP_KEY=(.*)$`},
	})
	store := generatedstore.New()

	res, err := HarvestGenerated(root, cfg, "main", store)
	if err != nil {
		t.Fatalf("HarvestGenerated: %v", err)
	}
	if len(res.Fields) != 1 || res.Fields[0].Field != "app_key" || !res.Fields[0].Wrote {
		t.Fatalf("unexpected result: %+v", res)
	}
	if got := store.Get("main", "app_key"); got != "base64:Xa3secret==" {
		t.Errorf("store value = %q, want base64:Xa3secret==", got)
	}
	// Store was saved to disk.
	if _, err := os.Stat(filepath.Join(root, generatedstore.DefaultRelPath)); err != nil {
		t.Errorf("expected store file written: %v", err)
	}
}

func TestHarvestGenerated_crlfLineEndings(t *testing.T) {
	root := t.TempDir()
	// Windows (CRLF) line endings must not leak a trailing \r into the captured
	// secret — an anchored `(.*)$` would otherwise grab the carriage return.
	writeServiceFile(t, root, "services/main", "configs/.env",
		"APP_NAME=demo\r\nAPP_KEY=base64:Xa3secret==\r\nDB_HOST=db\r\n")

	cfg := cfgWithGenerated("main", "services/main", map[string]projectconfig.GeneratedField{
		"app_key": {File: "configs/.env", Pattern: `^APP_KEY=(.*)$`},
	})
	store := generatedstore.New()

	if _, err := HarvestGenerated(root, cfg, "main", store); err != nil {
		t.Fatalf("HarvestGenerated: %v", err)
	}
	if got := store.Get("main", "app_key"); got != "base64:Xa3secret==" {
		t.Errorf("store value = %q, want base64:Xa3secret== (no trailing \\r)", got)
	}
}

func TestHarvestGenerated_phpArray(t *testing.T) {
	root := t.TempDir()
	writeServiceFile(t, root, "services/magento", "configs/env.php",
		"<?php\nreturn [\n  'crypt' => [\n    'key' => '241f4fa60be8f69638343cacc5a1a192',\n  ],\n];\n")

	cfg := cfgWithGenerated("magento", "services/magento", map[string]projectconfig.GeneratedField{
		"crypt_key": {File: "configs/env.php", Pattern: `'key'\s*=>\s*'([^']*)'`},
	})
	store := generatedstore.New()

	if _, err := HarvestGenerated(root, cfg, "magento", store); err != nil {
		t.Fatalf("HarvestGenerated: %v", err)
	}
	if got := store.Get("magento", "crypt_key"); got != "241f4fa60be8f69638343cacc5a1a192" {
		t.Errorf("store value = %q", got)
	}
}

func TestHarvestGenerated_writeIfAbsent(t *testing.T) {
	root := t.TempDir()
	writeServiceFile(t, root, "services/main", "configs/.env", "APP_KEY=fresh-from-disk\n")

	cfg := cfgWithGenerated("main", "services/main", map[string]projectconfig.GeneratedField{
		"app_key": {File: "configs/.env", Pattern: `^APP_KEY=(.*)$`},
	})
	store := generatedstore.New()
	store.SetIfAbsent("main", "app_key", "existing-value")

	res, err := HarvestGenerated(root, cfg, "main", store)
	if err != nil {
		t.Fatalf("HarvestGenerated: %v", err)
	}
	if res.Fields[0].Wrote {
		t.Errorf("expected Wrote=false (value preserved), got %+v", res.Fields[0])
	}
	if got := store.Get("main", "app_key"); got != "existing-value" {
		t.Errorf("store value = %q, want preserved existing-value", got)
	}
}

func TestHarvestGenerated_multiField(t *testing.T) {
	root := t.TempDir()
	writeServiceFile(t, root, "services/main", "configs/.env",
		"APP_KEY=key-aaa\nJWT_SECRET=sec-bbb\n")

	cfg := cfgWithGenerated("main", "services/main", map[string]projectconfig.GeneratedField{
		"app_key":    {File: "configs/.env", Pattern: `^APP_KEY=(.*)$`},
		"jwt_secret": {File: "configs/.env", Pattern: `^JWT_SECRET=(.*)$`},
	})
	store := generatedstore.New()

	res, err := HarvestGenerated(root, cfg, "main", store)
	if err != nil {
		t.Fatalf("HarvestGenerated: %v", err)
	}
	if len(res.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(res.Fields))
	}
	// Deterministic sorted order: app_key before jwt_secret.
	if res.Fields[0].Field != "app_key" || res.Fields[1].Field != "jwt_secret" {
		t.Errorf("non-deterministic field order: %+v", res.Fields)
	}
	if store.Get("main", "app_key") != "key-aaa" || store.Get("main", "jwt_secret") != "sec-bbb" {
		t.Errorf("unexpected stored values: %v", store.Services)
	}
}

func TestHarvestGenerated_noGeneratedFields(t *testing.T) {
	root := t.TempDir()
	cfg := cfgWithGenerated("main", "services/main", nil)
	store := generatedstore.New()

	res, err := HarvestGenerated(root, cfg, "main", store)
	if err != nil {
		t.Fatalf("HarvestGenerated: %v", err)
	}
	if len(res.Fields) != 0 {
		t.Errorf("expected no fields, got %+v", res.Fields)
	}
	// No store should be written when nothing is declared.
	if _, err := os.Stat(filepath.Join(root, generatedstore.DefaultRelPath)); !os.IsNotExist(err) {
		t.Errorf("expected no store file, stat err = %v", err)
	}
}

func TestHarvestGenerated_missingFile(t *testing.T) {
	root := t.TempDir()
	cfg := cfgWithGenerated("main", "services/main", map[string]projectconfig.GeneratedField{
		"app_key": {File: "configs/.env", Pattern: `^APP_KEY=(.*)$`},
	})
	if _, err := HarvestGenerated(root, cfg, "main", generatedstore.New()); err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestHarvestGenerated_noMatch(t *testing.T) {
	root := t.TempDir()
	writeServiceFile(t, root, "services/main", "configs/.env", "APP_NAME=demo\n")
	cfg := cfgWithGenerated("main", "services/main", map[string]projectconfig.GeneratedField{
		"app_key": {File: "configs/.env", Pattern: `^APP_KEY=(.*)$`},
	})
	if _, err := HarvestGenerated(root, cfg, "main", generatedstore.New()); err == nil {
		t.Fatal("expected error for no match, got nil")
	}
}

func TestHarvestGenerated_noCaptureGroup(t *testing.T) {
	root := t.TempDir()
	writeServiceFile(t, root, "services/main", "configs/.env", "APP_KEY=abc\n")
	cfg := cfgWithGenerated("main", "services/main", map[string]projectconfig.GeneratedField{
		"app_key": {File: "configs/.env", Pattern: `^APP_KEY=.*$`},
	})
	if _, err := HarvestGenerated(root, cfg, "main", generatedstore.New()); err == nil {
		t.Fatal("expected error for pattern without a capture group, got nil")
	}
}

func TestHarvestGenerated_emptyCapture(t *testing.T) {
	root := t.TempDir()
	writeServiceFile(t, root, "services/main", "configs/.env", "APP_KEY=\n")
	cfg := cfgWithGenerated("main", "services/main", map[string]projectconfig.GeneratedField{
		"app_key": {File: "configs/.env", Pattern: `^APP_KEY=(.*)$`},
	})
	if _, err := HarvestGenerated(root, cfg, "main", generatedstore.New()); err == nil {
		t.Fatal("expected error for empty captured value, got nil")
	}
}

func TestHarvestGenerated_invalidPattern(t *testing.T) {
	root := t.TempDir()
	writeServiceFile(t, root, "services/main", "configs/.env", "APP_KEY=abc\n")
	cfg := cfgWithGenerated("main", "services/main", map[string]projectconfig.GeneratedField{
		"app_key": {File: "configs/.env", Pattern: `^APP_KEY=(.*$`},
	})
	if _, err := HarvestGenerated(root, cfg, "main", generatedstore.New()); err == nil {
		t.Fatal("expected error for invalid regex, got nil")
	}
}

func TestHarvestGenerated_pathEscapeRejected(t *testing.T) {
	root := t.TempDir()
	cfg := cfgWithGenerated("main", "services/main", map[string]projectconfig.GeneratedField{
		"app_key": {File: "../../etc/passwd", Pattern: `(.*)`},
	})
	if _, err := HarvestGenerated(root, cfg, "main", generatedstore.New()); err == nil {
		t.Fatal("expected error for path escape, got nil")
	}
}

func TestHarvestGenerated_unknownService(t *testing.T) {
	root := t.TempDir()
	cfg := cfgWithGenerated("main", "services/main", nil)
	if _, err := HarvestGenerated(root, cfg, "nope", generatedstore.New()); err == nil {
		t.Fatal("expected error for unknown service, got nil")
	}
}
