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
// layers cannot redeclare structural definition fields (container, dir,
// configs, compose, extends, etc.). The error must attribute the offending
// layer's path and the service name. Per-developer-overridable knobs
// (ports/hosts) have their own positive-path tests below.
func TestValidateServicesOverlay_rejectsDefinitionField(t *testing.T) {
	declared := map[string]ServiceConfig{"adminer": {Type: ServiceTypeTool, Container: "adminer"}}
	for _, layer := range []string{"workspace.yml", "workspace/defaults.yml", "workspace/local.yml"} {
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

// TestValidateServicesOverlay_acceptsPortsHosts confirms that per-developer
// port/host overrides are permitted in overlay layers — a core dwe
// feature so a developer can resolve port clashes locally without touching
// shared workspace/services.yml.
func TestValidateServicesOverlay_acceptsPortsHosts(t *testing.T) {
	declared := map[string]ServiceConfig{"adminer": {Type: ServiceTypeTool, Container: "adminer"}}
	raw := map[string]any{
		"services": map[string]any{
			"adminer": map[string]any{
				"enabled": true,
				"ports":   map[string]any{"http": 9027},
				"hosts":   map[string]any{"web": "dev.db.local"},
			},
		},
	}
	if err := validateServicesOverlay("workspace/local.yml", raw, declared); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateServicesOverlay_rejectsBadPortsShape covers the validator's
// shape checks: ports must be a map, every port value must be an integer
// in 1..65535.
func TestValidateServicesOverlay_rejectsBadPortsShape(t *testing.T) {
	declared := map[string]ServiceConfig{"adminer": {Type: ServiceTypeTool}}
	cases := []struct {
		name string
		raw  any
		want string
	}{
		{"not_a_map", "8080", "must be a map"},
		{"bad_value", map[string]any{"http": "8080"}, "not an integer"},
		{"out_of_range_low", map[string]any{"http": 0}, "out of range"},
		{"out_of_range_high", map[string]any{"http": 70000}, "out of range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{"services": map[string]any{"adminer": map[string]any{"ports": tc.raw}}}
			err := validateServicesOverlay("workspace/local.yml", raw, declared)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should contain %q", err, tc.want)
			}
		})
	}
}

// TestValidateServicesOverlay_rejectsBadHostsShape covers the validator's
// shape checks for hosts: must be a map of name -> string.
func TestValidateServicesOverlay_rejectsBadHostsShape(t *testing.T) {
	declared := map[string]ServiceConfig{"adminer": {Type: ServiceTypeTool}}
	cases := []struct {
		name string
		raw  any
		want string
	}{
		{"not_a_map", "x.localhost", "must be a map"},
		{"non_string_value", map[string]any{"web": 42}, "not a string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{"services": map[string]any{"adminer": map[string]any{"hosts": tc.raw}}}
			err := validateServicesOverlay("workspace/local.yml", raw, declared)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should contain %q", err, tc.want)
			}
		})
	}
}

// TestValidateServicesOverlay_rejectsUnknownService confirms that overlays
// cannot reference services that are not declared in workspace/services.yml —
// the canonical "unknown service in overlay" case the merge-after-validate
// ordering catches.
func TestValidateServicesOverlay_rejectsUnknownService(t *testing.T) {
	declared := map[string]ServiceConfig{"adminer": {Type: ServiceTypeTool}}
	raw := map[string]any{
		"services": map[string]any{
			"ghost": map[string]any{"enabled": true},
		},
	}
	err := validateServicesOverlay("workspace/local.yml", raw, declared)
	if err == nil {
		t.Fatal("expected error for unknown service name")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error %q should mention the unknown service name", err)
	}
}

func TestValidateServicesOverlay_noServicesBlock(t *testing.T) {
	declared := map[string]ServiceConfig{"adminer": {Type: ServiceTypeTool}}
	if err := validateServicesOverlay("workspace.yml", map[string]any{"project": "x"}, declared); err != nil {
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
	cfgYAML := `schema_version: "2"
project:
  name: test
  prefix: dwe
`
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceDir, "services", "adminer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "services", "adminer", "service.yml"), []byte("type: tool\ncontainer: adminer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defaultsYML := `services:
  brand_new:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "defaults.yml"), []byte(defaultsYML), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err == nil {
		t.Fatal("expected overlay validation to reject brand_new service")
	}
	if !strings.Contains(err.Error(), "brand_new") || !strings.Contains(err.Error(), "unknown service") {
		t.Errorf("error %q should mention brand_new and 'unknown service'", err)
	}
}

// TestLoadConfig_overlayPortsHostsMerged is the end-to-end positive-path
// test for per-developer port/host overrides: a local.yml entry overriding
// services.adminer.ports.http and adding services.main.hosts.api must (a)
// reach cfg.Services with the merged values and (b) be reflected in
// cfg.Raw so dot-path templates resolve against the overridden values.
func TestLoadConfig_overlayPortsHostsMerged(t *testing.T) {
	dir := t.TempDir()
	cfgYAML := `schema_version: "2"
project:
  name: test
  prefix: dwe
`
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"adminer": "type: tool\ncontainer: adminer\nports:\n  http: 8027\nhosts:\n  web: admin.local\n",
		"main":    "type: app\ncontainer: app-main\nrequired: true\ndir: ./services/main\nports:\n  http: 80\nhosts:\n  web: app.local\n",
	} {
		svcDir := filepath.Join(workspaceDir, "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	localYML := `services:
  adminer:
    ports:
      http: 9027
  main:
    hosts:
      api: api.dev.local
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "local.yml"), []byte(localYML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Services["adminer"].Port("http"); got != 9027 {
		t.Errorf("adminer.ports.http = %d, want 9027 (overlay override)", got)
	}
	if got := cfg.Services["adminer"].Host("web"); got != "admin.local" {
		t.Errorf("adminer.hosts.web = %q, want %q (declared value preserved)", got, "admin.local")
	}
	if got := cfg.Services["main"].Host("api"); got != "api.dev.local" {
		t.Errorf("main.hosts.api = %q, want %q (overlay-added entry)", got, "api.dev.local")
	}
	if got := cfg.Services["main"].Host("web"); got != "app.local" {
		t.Errorf("main.hosts.web = %q, want %q (declared value preserved)", got, "app.local")
	}
	// Raw mirror — proves dot-path templates resolve through the overlay.
	if v, ok := ResolvePath(cfg.Raw, "services.adminer.ports.http"); !ok || v != 9027 {
		t.Errorf("services.adminer.ports.http via Raw = (%v, %v), want (9027, true)", v, ok)
	}
	if v, ok := ResolvePath(cfg.Raw, "services.main.hosts.api"); !ok || v != "api.dev.local" {
		t.Errorf("services.main.hosts.api via Raw = (%v, %v), want (api.dev.local, true)", v, ok)
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

// TestLoadConfig_extendsInheritsOverlaidParentHosts is a regression guard for
// the bug where `auto-hosts` (and any consumer of cfg.Services[*].Hosts on a
// child service) saw a parent's pre-overlay host because `extends:`
// inheritance ran inside LoadServices BEFORE the per-service overlay loop in
// LoadConfig got to apply local.yml overrides on the parent.
//
// Concrete scenario: `main-debug` extends `main`. `main` declares
// `hosts.web: tbm.local` in service.yml; `local.yml` overrides it to
// `tbm.localhost`. After LoadConfig, BOTH services must report
// `hosts.web == "tbm.localhost"` — children inherit the OVERLAID parent.
//
// Loader contract this pins down: per-service overlay merges
// (applyOverlayPorts / applyOverlayHosts) MUST run before
// ResolveServiceExtends so children clone the post-overlay parent maps.
func TestLoadConfig_extendsInheritsOverlaidParentHosts(t *testing.T) {
	dir := t.TempDir()

	cfgYAML := `
schema_version: "1"
project:
  name: tbm
  prefix: dwe
`
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace/: %v", err)
	}

	// local.yml overrides the parent's hosts.web only. main-debug has no
	// entry — the regression was that it stayed at the pre-overlay default.
	localYML := `
services:
  main:
    hosts:
      web: tbm.localhost
  main-debug:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "local.yml"), []byte(localYML), 0o644); err != nil {
		t.Fatalf("write local.yml: %v", err)
	}

	mainYML := `
type: app
container: app-main
required: true
dir: ./services/main
hosts:
  web: tbm.local
`
	debugYML := `
type: app
container: app-main-debug
required: false
extends: main
`
	writeServiceFolder(t, dir, "main", mainYML)
	writeServiceFolder(t, dir, "main-debug", debugYML)

	cfg, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got := cfg.Services["main"].Hosts["web"]; got != "tbm.localhost" {
		t.Errorf("main.hosts.web = %q, want tbm.localhost (overlay)", got)
	}
	if got := cfg.Services["main-debug"].Hosts["web"]; got != "tbm.localhost" {
		t.Errorf("main-debug.hosts.web = %q, want tbm.localhost — child must inherit overlaid parent host, not the pre-overlay service.yml default", got)
	}
}

// TestLoadConfig_extendsInheritsOverlaidParentPorts is the ports counterpart
// of [TestLoadConfig_extendsInheritsOverlaidParentHosts]. The same loader
// ordering bug would let children pin a pre-overlay parent port.
func TestLoadConfig_extendsInheritsOverlaidParentPorts(t *testing.T) {
	dir := t.TempDir()

	cfgYAML := `
schema_version: "1"
project:
  name: tbm
  prefix: dwe
`
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace/: %v", err)
	}

	localYML := `
services:
  parent:
    ports:
      http: 9090
  child:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "local.yml"), []byte(localYML), 0o644); err != nil {
		t.Fatalf("write local.yml: %v", err)
	}

	parentYML := `
type: app
container: app-parent
required: true
dir: ./services/parent
ports:
  http: 8080
`
	childYML := `
type: app
container: app-child
required: false
extends: parent
`
	writeServiceFolder(t, dir, "parent", parentYML)
	writeServiceFolder(t, dir, "child", childYML)

	cfg, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got := cfg.Services["parent"].Ports["http"]; got != 9090 {
		t.Errorf("parent.ports.http = %d, want 9090 (overlay)", got)
	}
	if got := cfg.Services["child"].Ports["http"]; got != 9090 {
		t.Errorf("child.ports.http = %d, want 9090 — child must inherit overlaid parent port, not the pre-overlay service.yml default", got)
	}
}

// TestLoadConfig_extendsChildOwnHostBeatsParentOverlay verifies the
// inheritance precedence wasn't reversed by the loader reorder: when the
// child declares its OWN hosts.web in service.yml, it wins regardless of any
// overlay on the parent.
func TestLoadConfig_extendsChildOwnHostBeatsParentOverlay(t *testing.T) {
	dir := t.TempDir()

	cfgYAML := `
schema_version: "1"
project:
  name: tbm
  prefix: dwe
`
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}

	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace/: %v", err)
	}

	localYML := `
services:
  parent:
    hosts:
      web: parent.localhost
  child:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "local.yml"), []byte(localYML), 0o644); err != nil {
		t.Fatalf("write local.yml: %v", err)
	}

	parentYML := `
type: app
container: app-parent
required: true
dir: ./services/parent
hosts:
  web: parent.local
`
	childYML := `
type: app
container: app-child
required: false
extends: parent
hosts:
  web: child.local
`
	writeServiceFolder(t, dir, "parent", parentYML)
	writeServiceFolder(t, dir, "child", childYML)

	cfg, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got := cfg.Services["parent"].Hosts["web"]; got != "parent.localhost" {
		t.Errorf("parent.hosts.web = %q, want parent.localhost", got)
	}
	if got := cfg.Services["child"].Hosts["web"]; got != "child.local" {
		t.Errorf("child.hosts.web = %q, want child.local — child's own hosts.web must win over parent inheritance", got)
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
