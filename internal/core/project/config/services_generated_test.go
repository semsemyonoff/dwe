package config

import (
	"testing"
)

// TestLoadServices_renderConfigAndGenerated verifies that the new render.config
// pin and generated: block decode into the typed structs.
func TestLoadServices_renderConfigAndGenerated(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "main", `
type: app
container: app-main
dir: ./services/main
render:
  config:
    template: laravel
generated:
  app_key:
    file: configs/.env
    pattern: '^APP_KEY=(.*)$'
  other_secret:
    file: configs/secret.php
    pattern: "'value' => '(.*)'"
`)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	main := services["main"]
	if main.Render.Config == nil {
		t.Fatal("render.config is nil, want decoded section")
	}
	if main.Render.Config.Template != "laravel" {
		t.Errorf("render.config.template = %q, want laravel", main.Render.Config.Template)
	}
	if len(main.Generated) != 2 {
		t.Fatalf("generated has %d fields, want 2: %v", len(main.Generated), main.Generated)
	}
	appKey := main.Generated["app_key"]
	if appKey.File != "configs/.env" {
		t.Errorf("generated.app_key.file = %q, want configs/.env", appKey.File)
	}
	if appKey.Pattern != "^APP_KEY=(.*)$" {
		t.Errorf("generated.app_key.pattern = %q, want ^APP_KEY=(.*)$", appKey.Pattern)
	}
	if got := main.Generated["other_secret"].File; got != "configs/secret.php" {
		t.Errorf("generated.other_secret.file = %q, want configs/secret.php", got)
	}
}

// TestLoadServices_renderConfigAbsent verifies render.config stays nil when the
// service does not declare it (so callers can detect an explicit pin).
func TestLoadServices_renderConfigAbsent(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "main", `
type: app
container: app-main
dir: ./services/main
render:
  ide:
    template: goland
`)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	if services["main"].Render.Config != nil {
		t.Errorf("render.config = %+v, want nil when absent", services["main"].Render.Config)
	}
}

// TestLoadServices_deprecatedConfigsStillDecodes verifies the legacy configs:
// block keeps parsing alongside the new fields (transitional compatibility).
func TestLoadServices_deprecatedConfigsStillDecodes(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "main", `
type: app
container: app-main
dir: ./services/main
configs:
  - .env
  - file: src/config.php
    mountpoint: src/config.php
generated:
  app_key:
    file: configs/.env
    pattern: '^APP_KEY=(.*)$'
`)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	main := services["main"]
	if len(main.Configs) != 2 {
		t.Fatalf("configs has %d entries, want 2: %v", len(main.Configs), main.Configs)
	}
	if main.Configs[0].File != ".env" {
		t.Errorf("configs[0].file = %q, want .env", main.Configs[0].File)
	}
	if main.Configs[1].Mountpoint != "src/config.php" {
		t.Errorf("configs[1].mountpoint = %q, want src/config.php", main.Configs[1].Mountpoint)
	}
	if main.Generated["app_key"].File != "configs/.env" {
		t.Errorf("generated.app_key.file = %q, want configs/.env", main.Generated["app_key"].File)
	}
}

// TestLoadServices_unknownGeneratedSubfieldRejected verifies the strict decoder
// still hard-errors on an unknown sibling inside a generated field.
func TestLoadServices_unknownGeneratedSubfieldRejected(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "main", `
type: app
container: app-main
dir: ./services/main
generated:
  app_key:
    file: configs/.env
    pattern: '^APP_KEY=(.*)$'
    bogus: nope
`)
	_, err := LoadServices(dir)
	if err == nil {
		t.Fatal("expected error for unknown generated subfield, got nil")
	}
}

// TestLoadServices_unknownRenderConfigSubfieldRejected verifies an unknown
// sibling inside render.config is rejected by the strict decoder.
func TestLoadServices_unknownRenderConfigSubfieldRejected(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "main", `
type: app
container: app-main
dir: ./services/main
render:
  config:
    template: laravel
    bogus: nope
`)
	_, err := LoadServices(dir)
	if err == nil {
		t.Fatal("expected error for unknown render.config subfield, got nil")
	}
}
