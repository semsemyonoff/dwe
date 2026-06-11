package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeBridgeOverlayFixture drops a placeholder generated overlay at the
// canonical path under dir. Content is irrelevant — composeFiles only stats.
func writeBridgeOverlayFixture(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(BridgeOverlayRelPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir .dwe: %v", err)
	}
	if err := os.WriteFile(path, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("write bridge overlay: %v", err)
	}
}

func TestComposeFiles_bridgeOverlayChainPosition(t *testing.T) {
	dir := t.TempDir()
	cfg := &DweConfig{
		Compose: ComposeConfig{Base: "compose.yaml", Extra: []string{"compose.local.yml"}},
		Services: map[string]ServiceConfig{
			"main": {Type: ServiceTypeApp, Enabled: true,
				Compose:           []string{"compose/main.yml"},
				LocalComposeExtra: []string{"compose/main.local.yml"}},
			"redis": {Type: ServiceTypeInfra, Enabled: true,
				Compose: []string{"compose/redis.yml"}},
		},
		Raw: map[string]any{"__configPath": filepath.Join(dir, "workspace.yml")},
	}

	// Overlay absent on disk → chain untouched.
	want := []string{
		"compose.yaml",
		"compose/redis.yml",
		"compose/main.yml",
		"compose/main.local.yml",
		"compose.local.yml",
	}
	if got := cfg.ComposeFiles(); !slices.Equal(got, want) {
		t.Errorf("ComposeFiles without overlay = %v, want %v", got, want)
	}

	// Overlay present → inserted after the service groups and BEFORE the
	// project-wide local.yml overlays, so local.yml keeps the last word.
	writeBridgeOverlayFixture(t, dir)
	want = []string{
		"compose.yaml",
		"compose/redis.yml",
		"compose/main.yml",
		"compose/main.local.yml",
		BridgeOverlayRelPath,
		"compose.local.yml",
	}
	if got := cfg.ComposeFiles(); !slices.Equal(got, want) {
		t.Errorf("ComposeFiles with overlay = %v, want %v", got, want)
	}
	if got := cfg.ComposeFilesAll(); !slices.Equal(got, want) {
		t.Errorf("ComposeFilesAll with overlay = %v, want %v", got, want)
	}
}

func TestComposeFiles_bridgeOverlayRequiresConfigPath(t *testing.T) {
	// Configs built without LoadConfig carry no __configPath — the overlay
	// existence check has no root to probe and must stay silent.
	cfg := &DweConfig{
		Compose: ComposeConfig{Base: "compose.yaml"},
		Services: map[string]ServiceConfig{
			"main": {Type: ServiceTypeApp, Enabled: true, Compose: []string{"compose/main.yml"}},
		},
	}
	want := []string{"compose.yaml", "compose/main.yml"}
	if got := cfg.ComposeFiles(); !slices.Equal(got, want) {
		t.Errorf("ComposeFiles = %v, want %v", got, want)
	}
}

func TestComposeFiles_bridgeOverlayAfterLoadConfig(t *testing.T) {
	// End-to-end through LoadConfig: __configPath is injected by the loader,
	// so an overlay created AFTER load (the bridge prepare hook runs between
	// LoadConfig and compose-up) still enters the chain.
	customTools := `
services:
  mailpit:
    type: tool
    container: mailpit
    compose:
      - compose/tools/mailpit.yml
`
	defaultsWithCompose := `
schema_version: "1"
services:
  mailpit:
    enabled: true
runtime:
  use_https: false
  spx:
    path: ""
compose:
  base: compose.yaml
`
	path := writeFullFixture(t, sampleWorkspaceYML, defaultsWithCompose, "", "", customTools)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	want := []string{"compose.yaml", "compose/tools/mailpit.yml"}
	if got := cfg.ComposeFiles(); !slices.Equal(got, want) {
		t.Fatalf("ComposeFiles before overlay = %v, want %v", got, want)
	}

	writeBridgeOverlayFixture(t, filepath.Dir(path))
	want = append(want, BridgeOverlayRelPath)
	if got := cfg.ComposeFiles(); !slices.Equal(got, want) {
		t.Errorf("ComposeFiles after overlay = %v, want %v", got, want)
	}
}
