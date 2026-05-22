package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateServicesOverlay_acceptsEnabledOnly confirms that an overlay
// block carrying only services.<name>.enabled passes against a declared
// service set.
func TestValidateServicesOverlay_acceptsEnabledOnly(t *testing.T) {
	declared := map[string]ServiceConfig{"adminer": {Type: ServiceTypeTool, Container: "adminer"}}
	raw := map[string]any{
		"services": map[string]any{
			"adminer": map[string]any{"enabled": true},
		},
	}
	if err := validateServicesOverlay("path/to/defaults.yml", raw, declared); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateServicesOverlay_rejectsDefinitionField confirms that overlay
// layers cannot redeclare structural fields (ports, container, dir, etc.).
// The error must attribute the offending layer's path and the service name.
func TestValidateServicesOverlay_rejectsDefinitionField(t *testing.T) {
	declared := map[string]ServiceConfig{"adminer": {Type: ServiceTypeTool, Container: "adminer"}}
	for _, layer := range []string{"devbox.yml", "devbox/defaults.yml", "devbox/local.yml"} {
		raw := map[string]any{
			"services": map[string]any{
				"adminer": map[string]any{"container": "stale"},
			},
		}
		err := validateServicesOverlay(layer, raw, declared)
		if err == nil {
			t.Errorf("%s: expected error for stale definition field", layer)
			continue
		}
		if !strings.Contains(err.Error(), layer) || !strings.Contains(err.Error(), "adminer") {
			t.Errorf("%s: error %q should mention layer and service name", layer, err)
		}
	}
}

// TestValidateServicesOverlay_rejectsUnknownService confirms that overlays
// cannot reference services that are not declared in devbox/services.yml —
// the canonical "unknown service in overlay" case the merge-after-validate
// ordering catches.
func TestValidateServicesOverlay_rejectsUnknownService(t *testing.T) {
	declared := map[string]ServiceConfig{"adminer": {Type: ServiceTypeTool}}
	raw := map[string]any{
		"services": map[string]any{
			"ghost": map[string]any{"enabled": true},
		},
	}
	err := validateServicesOverlay("devbox/local.yml", raw, declared)
	if err == nil {
		t.Fatal("expected error for unknown service name")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error %q should mention the unknown service name", err)
	}
}

func TestValidateServicesOverlay_noServicesBlock(t *testing.T) {
	declared := map[string]ServiceConfig{"adminer": {Type: ServiceTypeTool}}
	if err := validateServicesOverlay("devbox.yml", map[string]any{"project": "x"}, declared); err != nil {
		t.Errorf("missing services block should not error: %v", err)
	}
}

// TestLoadConfig_overlaySequencing_unknownServiceRejected is the canonical
// sequencing test for Task 4: an overlay layer declaring a brand-new
// services.<name> block (one that does NOT appear in services.yml) must be
// rejected — even when the YAML is otherwise well-formed. This guards the
// merge-after-validate ordering of LoadConfig.
func TestLoadConfig_overlaySequencing_unknownServiceRejected(t *testing.T) {
	dir := t.TempDir()
	devboxYML := `schema_version: "2"
project:
  name: test
  prefix: devbox
`
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte(devboxYML), 0o644); err != nil {
		t.Fatal(err)
	}
	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	servicesYML := `services:
  adminer:
    type: tool
    container: adminer
`
	if err := os.WriteFile(filepath.Join(devboxDir, "services.yml"), []byte(servicesYML), 0o644); err != nil {
		t.Fatal(err)
	}
	defaultsYML := `services:
  brand_new:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(devboxDir, "defaults.yml"), []byte(defaultsYML), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(filepath.Join(dir, "devbox.yml"))
	if err == nil {
		t.Fatal("expected overlay validation to reject brand_new service")
	}
	if !strings.Contains(err.Error(), "brand_new") || !strings.Contains(err.Error(), "unknown service") {
		t.Errorf("error %q should mention brand_new and 'unknown service'", err)
	}
}

// TestInjectServicesIntoRaw_portsHostsRoundTrip verifies that ports/hosts
// are mirrored into Raw when populated, and intentionally absent when not
// populated. This protects existence-check templates like
// `{{ if (index .Services "X").Ports }}…` from being misled by empty maps.
func TestInjectServicesIntoRaw_portsHostsRoundTrip(t *testing.T) {
	raw := map[string]any{}
	services := map[string]ServiceConfig{
		"with_ports": {
			Type:      ServiceTypeTool,
			Container: "wp",
			Ports:     map[string]int{"http": 80},
			Hosts:     map[string]string{"main": "wp.localhost"},
		},
		"without_ports": {
			Type:      ServiceTypeTool,
			Container: "np",
		},
	}
	injectServicesIntoRaw(raw, services)

	svcMap, ok := raw["services"].(map[string]any)
	if !ok {
		t.Fatal("services key missing from raw")
	}

	wp, _ := svcMap["with_ports"].(map[string]any)
	if wp == nil {
		t.Fatal("with_ports entry missing")
	}
	ports, ok := wp["ports"].(map[string]any)
	if !ok {
		t.Fatal("ports key missing on with_ports")
	}
	if ports["http"] != 80 {
		t.Errorf("ports.http = %v, want 80", ports["http"])
	}
	if _, ok := wp["hosts"]; !ok {
		t.Error("hosts key missing on with_ports")
	}

	np, _ := svcMap["without_ports"].(map[string]any)
	if np == nil {
		t.Fatal("without_ports entry missing")
	}
	if _, present := np["ports"]; present {
		t.Error("ports key should be absent when Ports is empty")
	}
	if _, present := np["hosts"]; present {
		t.Error("hosts key should be absent when Hosts is empty")
	}
}

// TestInjectServicesIntoRaw_dotPathResolution exercises ResolvePath against
// the populated map — proves the new shape is reachable from tpl.Render.
func TestInjectServicesIntoRaw_dotPathResolution(t *testing.T) {
	raw := map[string]any{}
	services := map[string]ServiceConfig{
		"db": {
			Type:      ServiceTypeInfra,
			Container: "db",
			Ports:     map[string]int{"mysql": 3306},
			Hosts:     map[string]string{"primary": "db.localhost"},
		},
	}
	injectServicesIntoRaw(raw, services)

	if v, ok := ResolvePath(raw, "services.db.ports.mysql"); !ok || v != 3306 {
		t.Errorf("services.db.ports.mysql = (%v, %v), want (3306, true)", v, ok)
	}
	if v, ok := ResolvePath(raw, "services.db.hosts.primary"); !ok || v != "db.localhost" {
		t.Errorf("services.db.hosts.primary = (%v, %v), want (db.localhost, true)", v, ok)
	}
}
