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
	if err := validateServicesOverlay("path/to/defaults.yml", raw, declared, false); err != nil {
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
		err := validateServicesOverlay(layer, raw, declared, layer == "workspace/local.yml")
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
	if err := validateServicesOverlay("workspace/local.yml", raw, declared, true); err != nil {
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
		{"bad_value", map[string]any{"http": "8080"}, "must be an integer or a mapping"},
		{"out_of_range_low", map[string]any{"http": 0}, "out of range"},
		{"out_of_range_high", map[string]any{"http": 70000}, "out of range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{"services": map[string]any{"adminer": map[string]any{"ports": tc.raw}}}
			err := validateServicesOverlay("workspace/local.yml", raw, declared, true)
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
			err := validateServicesOverlay("workspace/local.yml", raw, declared, true)
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
	err := validateServicesOverlay("workspace/local.yml", raw, declared, true)
	if err == nil {
		t.Fatal("expected error for unknown service name")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error %q should mention the unknown service name", err)
	}
}

func TestValidateServicesOverlay_noServicesBlock(t *testing.T) {
	declared := map[string]ServiceConfig{"adminer": {Type: ServiceTypeTool}}
	if err := validateServicesOverlay("workspace.yml", map[string]any{"project": "x"}, declared, false); err != nil {
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
			Ports:     map[string]ServicePortSpec{"http": {Port: 80}},
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

	if got := cfg.Services["parent"].Port("http"); got != 9090 {
		t.Errorf("parent.ports.http = %d, want 9090 (overlay)", got)
	}
	if got := cfg.Services["child"].Port("http"); got != 9090 {
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
			Ports:     map[string]ServicePortSpec{"mysql": {Port: 3306}},
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

// TestValidateServicesOverlay_acceptsLocalCompose confirms that
// services.<name>.compose.extra is accepted in workspace/local.yml.
func TestValidateServicesOverlay_acceptsLocalCompose(t *testing.T) {
	declared := map[string]ServiceConfig{"dev": {Type: ServiceTypeApp, Container: "dev"}}
	raw := map[string]any{
		"services": map[string]any{
			"dev": map[string]any{
				"compose": map[string]any{
					"extra": []any{"compose.local.yml", "compose/dev.local.yml"},
				},
			},
		},
	}
	if err := validateServicesOverlay("workspace/local.yml", raw, declared, true); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateServicesOverlay_rejectsNonLocalCompose confirms that
// services.<name>.compose is rejected in non-local layers (defaults.yml,
// workspace.yml) with the existing "service definitions belong in
// workspace/services/<name>/service.yml" hint.
func TestValidateServicesOverlay_rejectsNonLocalCompose(t *testing.T) {
	declared := map[string]ServiceConfig{"dev": {Type: ServiceTypeApp, Container: "dev"}}
	for _, layer := range []string{"workspace.yml", "workspace/defaults.yml"} {
		raw := map[string]any{
			"services": map[string]any{
				"dev": map[string]any{
					"compose": map[string]any{"extra": []any{"x.yml"}},
				},
			},
		}
		err := validateServicesOverlay(layer, raw, declared, false)
		if err == nil {
			t.Errorf("%s: expected error for compose in non-local layer", layer)
			continue
		}
		if !strings.Contains(err.Error(), "service definitions belong in workspace/services") {
			t.Errorf("%s: error %q should reference service.yml hint", layer, err)
		}
	}
}

// TestValidateOverlayCompose covers the per-service compose-block shape
// checker in workspace/local.yml.
func TestValidateOverlayCompose(t *testing.T) {
	declared := map[string]ServiceConfig{"dev": {Type: ServiceTypeApp, Container: "dev"}}
	cases := []struct {
		name string
		raw  any
		want string
	}{
		{"not_a_map", "x.yml", "must be a mapping"},
		{"unknown_subkey", map[string]any{"foo": "bar"}, "unknown field"},
		{"extra_not_a_list", map[string]any{"extra": "x.yml"}, "must be a list of strings"},
		{"non_string_entry", map[string]any{"extra": []any{123}}, "must be a string"},
		{"empty_string_entry", map[string]any{"extra": []any{""}}, "must be a non-empty string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{
				"services": map[string]any{
					"dev": map[string]any{"compose": tc.raw},
				},
			}
			err := validateServicesOverlay("workspace/local.yml", raw, declared, true)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should contain %q", err, tc.want)
			}
		})
	}
}

// TestValidateLocalCompose covers the project-wide compose.extra shape check
// in workspace/local.yml. Other top-level keys (state:, runtime:) must remain
// accepted — only the shape of `compose:` is validated.
func TestValidateLocalCompose(t *testing.T) {
	t.Run("accepts_extra_list", func(t *testing.T) {
		raw := map[string]any{
			"compose": map[string]any{
				"extra": []any{"compose.local.yml"},
			},
		}
		if err := validateLocalCompose("workspace/local.yml", raw); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("accepts_unknown_top_level_keys", func(t *testing.T) {
		// local.yml legitimately carries other convention keys (state, runtime, …).
		raw := map[string]any{
			"state":   map[string]any{"foo": "bar"},
			"runtime": map[string]any{"x": "y"},
		}
		if err := validateLocalCompose("workspace/local.yml", raw); err != nil {
			t.Errorf("unexpected error for unknown top-level keys: %v", err)
		}
	})

	t.Run("rejects_unknown_subkey", func(t *testing.T) {
		raw := map[string]any{
			"compose": map[string]any{"foo": "bar"},
		}
		err := validateLocalCompose("workspace/local.yml", raw)
		if err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Errorf("expected 'unknown field' error, got: %v", err)
		}
	})

	t.Run("rejects_base_in_local", func(t *testing.T) {
		// compose.base belongs in workspace.yml.
		raw := map[string]any{
			"compose": map[string]any{"base": "compose.yml"},
		}
		err := validateLocalCompose("workspace/local.yml", raw)
		if err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Errorf("expected 'unknown field' for compose.base in local.yml, got: %v", err)
		}
	})

	t.Run("rejects_extra_non_list", func(t *testing.T) {
		raw := map[string]any{
			"compose": map[string]any{"extra": "x.yml"},
		}
		err := validateLocalCompose("workspace/local.yml", raw)
		if err == nil || !strings.Contains(err.Error(), "must be a list of strings") {
			t.Errorf("expected 'must be a list of strings' error, got: %v", err)
		}
	})

	t.Run("accepts_no_compose_block", func(t *testing.T) {
		if err := validateLocalCompose("workspace/local.yml", map[string]any{}); err != nil {
			t.Errorf("missing compose block should not error: %v", err)
		}
	})
}

// TestValidateNonLocalCompose confirms that compose.extra in non-local
// layers is rejected with a hint pointing to workspace/local.yml, while
// compose.base remains accepted (unaffected).
func TestValidateNonLocalCompose(t *testing.T) {
	t.Run("rejects_extra_in_workspace_yml", func(t *testing.T) {
		raw := map[string]any{
			"compose": map[string]any{"extra": []any{"x.yml"}},
		}
		err := validateNonLocalCompose("workspace.yml", raw)
		if err == nil {
			t.Fatal("expected error for compose.extra in workspace.yml")
		}
		if !strings.Contains(err.Error(), "workspace/local.yml") {
			t.Errorf("error %q should point to workspace/local.yml", err)
		}
	})

	t.Run("rejects_extra_in_defaults", func(t *testing.T) {
		raw := map[string]any{
			"compose": map[string]any{"extra": []any{"x.yml"}},
		}
		err := validateNonLocalCompose("workspace/defaults.yml", raw)
		if err == nil {
			t.Fatal("expected error for compose.extra in defaults.yml")
		}
	})

	t.Run("accepts_compose_base", func(t *testing.T) {
		raw := map[string]any{
			"compose": map[string]any{"base": "compose.yml"},
		}
		if err := validateNonLocalCompose("workspace.yml", raw); err != nil {
			t.Errorf("compose.base in workspace.yml should be accepted: %v", err)
		}
	})

	t.Run("accepts_no_compose", func(t *testing.T) {
		if err := validateNonLocalCompose("workspace.yml", map[string]any{}); err != nil {
			t.Errorf("missing compose block should not error: %v", err)
		}
	})
}

// TestLoadConfig_localComposeExtraInjection verifies that local.yml entries
// for both project-wide (compose.extra) and per-service
// (services.<name>.compose.extra) reach cfg.Compose.Extra and
// cfg.Services[name].LocalComposeExtra respectively.
func TestLoadConfig_localComposeExtraInjection(t *testing.T) {
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
	writeServiceFolder(t, dir, "dev", "type: app\ncontainer: app-dev\nrequired: true\ndir: ./services/dev\n")
	writeServiceFolder(t, dir, "adminer", "type: tool\ncontainer: adminer\n")

	// Touch the overlay files so the existence check in validateLocalComposeExtraPaths passes.
	for _, f := range []string{"compose.local.yml", "compose/dev.local.yml", "tools.local.yml"} {
		full := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("services: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	localYML := `compose:
  extra:
    - compose.local.yml
services:
  dev:
    compose:
      extra:
        - compose/dev.local.yml
  adminer:
    enabled: true
    compose:
      extra:
        - tools.local.yml
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "local.yml"), []byte(localYML), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got, want := cfg.Compose.Extra, []string{"compose.local.yml"}; !equalStrings(got, want) {
		t.Errorf("cfg.Compose.Extra = %v, want %v", got, want)
	}
	if got, want := cfg.Services["dev"].LocalComposeExtra, []string{"compose/dev.local.yml"}; !equalStrings(got, want) {
		t.Errorf("dev.LocalComposeExtra = %v, want %v", got, want)
	}
	if got, want := cfg.Services["adminer"].LocalComposeExtra, []string{"tools.local.yml"}; !equalStrings(got, want) {
		t.Errorf("adminer.LocalComposeExtra = %v, want %v", got, want)
	}
}

// TestLoadConfig_backwardCompatNoComposeBlock confirms that an existing
// local.yml with only enabled/ports/hosts (no compose: block) loads cleanly
// and leaves Compose.Extra / LocalComposeExtra empty.
func TestLoadConfig_backwardCompatNoComposeBlock(t *testing.T) {
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
	writeServiceFolder(t, dir, "adminer", "type: tool\ncontainer: adminer\n")
	localYML := `services:
  adminer:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "local.yml"), []byte(localYML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Compose.Extra) != 0 {
		t.Errorf("cfg.Compose.Extra should be empty, got %v", cfg.Compose.Extra)
	}
	if len(cfg.Services["adminer"].LocalComposeExtra) != 0 {
		t.Errorf("adminer.LocalComposeExtra should be empty, got %v", cfg.Services["adminer"].LocalComposeExtra)
	}
}

// TestLoadConfig_extendsInheritsParentLocalComposeExtra is the inheritance
// counterpart for LocalComposeExtra: a child service with extends: but no
// compose.extra of its own MUST inherit the parent's LocalComposeExtra. When
// the child has its own non-empty LocalComposeExtra, child's wins (no merge).
func TestLoadConfig_extendsInheritsParentLocalComposeExtra(t *testing.T) {
	dir := t.TempDir()
	cfgYAML := `schema_version: "1"
project:
  name: tbm
  prefix: dwe
`
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"parent.local.yml", "child.local.yml"} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte("services: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeServiceFolder(t, dir, "parent", `
type: app
container: app-parent
required: true
dir: ./services/parent
`)
	writeServiceFolder(t, dir, "inheritor", `
type: app
container: app-inheritor
required: false
extends: parent
`)
	writeServiceFolder(t, dir, "overrider", `
type: app
container: app-overrider
required: false
extends: parent
`)
	localYML := `services:
  parent:
    compose:
      extra:
        - parent.local.yml
  inheritor:
    enabled: true
  overrider:
    enabled: true
    compose:
      extra:
        - child.local.yml
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "local.yml"), []byte(localYML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got, want := cfg.Services["parent"].LocalComposeExtra, []string{"parent.local.yml"}; !equalStrings(got, want) {
		t.Errorf("parent.LocalComposeExtra = %v, want %v", got, want)
	}
	if got, want := cfg.Services["inheritor"].LocalComposeExtra, []string{"parent.local.yml"}; !equalStrings(got, want) {
		t.Errorf("inheritor.LocalComposeExtra = %v, want %v (must inherit from parent)", got, want)
	}
	if got, want := cfg.Services["overrider"].LocalComposeExtra, []string{"child.local.yml"}; !equalStrings(got, want) {
		t.Errorf("overrider.LocalComposeExtra = %v, want %v (child wins)", got, want)
	}
}

// TestApplyLocalComposeExtra_sourceGating is the defense-in-depth assertion
// that injection ONLY reads from the local.yml raw map. A non-local layer that
// somehow carries services.<name>.compose.extra (bypassing the pre-merge
// validator in this test) MUST NOT populate LocalComposeExtra.
func TestApplyLocalComposeExtra_sourceGating(t *testing.T) {
	svc := ServiceConfig{Type: ServiceTypeTool, Container: "adminer"}
	// Pass nil localRaw — simulates "no local.yml present". Even if a stray
	// non-local layer carried the field, applyLocalComposeExtra never sees it.
	applyLocalComposeExtra(nil, "adminer", &svc)
	if len(svc.LocalComposeExtra) != 0 {
		t.Errorf("LocalComposeExtra should remain empty when localRaw is nil; got %v", svc.LocalComposeExtra)
	}
	// And when localRaw exists but lacks the entry: also no-op.
	applyLocalComposeExtra(map[string]any{"services": map[string]any{"other": map[string]any{"compose": map[string]any{"extra": []any{"x.yml"}}}}}, "adminer", &svc)
	if len(svc.LocalComposeExtra) != 0 {
		t.Errorf("LocalComposeExtra should remain empty for unrelated service; got %v", svc.LocalComposeExtra)
	}
}

// TestLoadConfig_localComposeExtraPathValidation covers Task 4: every overlay
// path declared in workspace/local.yml is checked in three stages —
// absolute-rejection, containment under the project root, and existence.
// Paths are stored as-written (relative form) on the typed config so docker
// compose resolves them via cmd.Dir = baseDir, matching ServiceConfig.Compose.
func TestLoadConfig_localComposeExtraPathValidation(t *testing.T) {
	tests := []struct {
		name      string
		localYML  string
		wantErr   string // substring
		setupFile string // relative path to touch before LoadConfig (so existence passes for that fixture)
	}{
		{
			name: "relative_path_inside_root_ok",
			localYML: `compose:
  extra:
    - compose.local.yml
`,
			setupFile: "compose.local.yml",
		},
		{
			name: "absolute_path_rejected",
			localYML: `compose:
  extra:
    - /etc/passwd
`,
			wantErr: "absolute paths are not permitted",
		},
		{
			name: "escape_rejected",
			localYML: `compose:
  extra:
    - ../escape.yml
`,
			wantErr: "escapes project root",
		},
		{
			name: "missing_file_rejected",
			localYML: `compose:
  extra:
    - missing.yml
`,
			wantErr: "file not found",
		},
		{
			name: "per_service_absolute_rejected",
			localYML: `services:
  adminer:
    enabled: true
    compose:
      extra:
        - /tmp/x.yml
`,
			wantErr: "absolute paths are not permitted",
		},
		{
			name: "per_service_escape_rejected",
			localYML: `services:
  adminer:
    enabled: true
    compose:
      extra:
        - ../escape.yml
`,
			wantErr: "escapes project root",
		},
		{
			name: "per_service_missing_rejected",
			localYML: `services:
  adminer:
    enabled: true
    compose:
      extra:
        - gone.yml
`,
			wantErr: "file not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			workspaceYML := `schema_version: "2"
project:
  name: test
  prefix: dwe
`
			if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(workspaceYML), 0o644); err != nil {
				t.Fatal(err)
			}
			workspaceDir := filepath.Join(dir, "workspace")
			if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
				t.Fatal(err)
			}
			writeServiceFolder(t, dir, "adminer", "type: tool\ncontainer: adminer\n")
			if tt.setupFile != "" {
				full := filepath.Join(dir, tt.setupFile)
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte("services: {}\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(workspaceDir, "local.yml"), []byte(tt.localYML), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("LoadConfig: unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("LoadConfig: expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("LoadConfig error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

// TestLoadConfig_localComposeExtraDuplicatePathsAllowed confirms that the same
// path declared in both project-wide compose.extra and per-service
// compose.extra is NOT deduped (docker compose tolerates duplicates).
func TestLoadConfig_localComposeExtraDuplicatePathsAllowed(t *testing.T) {
	dir := t.TempDir()
	workspaceYML := `schema_version: "2"
project:
  name: test
  prefix: dwe
`
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(workspaceYML), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeServiceFolder(t, dir, "adminer", "type: tool\ncontainer: adminer\n")
	if err := os.WriteFile(filepath.Join(dir, "shared.local.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	localYML := `compose:
  extra:
    - shared.local.yml
services:
  adminer:
    enabled: true
    compose:
      extra:
        - shared.local.yml
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "local.yml"), []byte(localYML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got, want := cfg.Compose.Extra, []string{"shared.local.yml"}; !equalStrings(got, want) {
		t.Errorf("cfg.Compose.Extra = %v, want %v", got, want)
	}
	if got, want := cfg.Services["adminer"].LocalComposeExtra, []string{"shared.local.yml"}; !equalStrings(got, want) {
		t.Errorf("adminer.LocalComposeExtra = %v, want %v", got, want)
	}
}

// TestLoadConfig_localComposeExtraPathsPreservedAsWritten asserts that paths
// are stored RELATIVE on the typed config (matching ServiceConfig.Compose
// semantics — docker.Compose resolves via cmd.Dir = baseDir).
func TestLoadConfig_localComposeExtraPathsPreservedAsWritten(t *testing.T) {
	dir := t.TempDir()
	workspaceYML := `schema_version: "2"
project:
  name: test
  prefix: dwe
`
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(workspaceYML), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeServiceFolder(t, dir, "adminer", "type: tool\ncontainer: adminer\n")
	if err := os.MkdirAll(filepath.Join(dir, "compose"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"top.yml", "compose/svc.yml"} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte("services: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	localYML := `compose:
  extra:
    - top.yml
services:
  adminer:
    enabled: true
    compose:
      extra:
        - compose/svc.yml
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "local.yml"), []byte(localYML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// Stored as written: relative path, no baseDir prefix, no Clean rewrite.
	if got, want := cfg.Compose.Extra, []string{"top.yml"}; !equalStrings(got, want) {
		t.Errorf("cfg.Compose.Extra = %v, want %v (must be stored as-written)", got, want)
	}
	if got, want := cfg.Services["adminer"].LocalComposeExtra, []string{"compose/svc.yml"}; !equalStrings(got, want) {
		t.Errorf("adminer.LocalComposeExtra = %v, want %v (must be stored as-written)", got, want)
	}
	// Sanity: paths must NOT have been absolutized.
	for _, p := range cfg.Compose.Extra {
		if filepath.IsAbs(p) {
			t.Errorf("project-wide path was absolutized: %q", p)
		}
	}
	for _, p := range cfg.Services["adminer"].LocalComposeExtra {
		if filepath.IsAbs(p) {
			t.Errorf("per-service path was absolutized: %q", p)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
