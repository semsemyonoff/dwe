package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeServiceFolder creates a single service folder at
// <baseDir>/workspace/services/<name>/service.yml with the given content.
func writeServiceFolder(t *testing.T, baseDir, name, content string) {
	t.Helper()
	dir := filepath.Join(baseDir, "workspace", "services", name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("writeServiceFolder mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "service.yml"), []byte(content), 0644); err != nil {
		t.Fatalf("writeServiceFolder write: %v", err)
	}
}

// TestLoadServices_happyPath verifies three services (app, tool, infra) load correctly.
func TestLoadServices_happyPath(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "web", `
type: app
container: app-web
required: true
dir: ./services/web
dir_internal: /workspace
ports:
  http: 8080
  grpc: 9090
hosts:
  main: web.localhost
`)
	writeServiceFolder(t, dir, "adminer", `
type: tool
container: adminer
ports:
  web: 8080
`)
	writeServiceFolder(t, dir, "db", `
type: infra
container: postgres
ports:
  postgres: 5432
`)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	if len(services) != 3 {
		t.Fatalf("expected 3 services, got %d", len(services))
	}
	web := services["web"]
	if !web.IsApp() {
		t.Errorf("web type = %s, want app", web.Type)
	}
	if web.Port("http") != 8080 || web.Port("grpc") != 9090 {
		t.Errorf("web ports = %v", web.Ports)
	}
	if web.Host("main") != "web.localhost" {
		t.Errorf("web hosts = %v", web.Hosts)
	}
	adm := services["adminer"]
	if !adm.IsTool() {
		t.Errorf("adminer type = %s, want tool", adm.Type)
	}
	db := services["db"]
	if !db.IsInfra() {
		t.Errorf("db type = %s, want infra", db.Type)
	}
}

// TestLoadServices_missingDir verifies absent workspace/services/ returns empty map (not error).
func TestLoadServices_missingDir(t *testing.T) {
	dir := t.TempDir()
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("expected nil error for missing dir, got %v", err)
	}
	if len(services) != 0 {
		t.Fatalf("expected empty map, got %v", services)
	}
}

// TestLoadServices_emptyDir verifies workspace/services/ with no subdirs returns empty map.
func TestLoadServices_emptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "workspace", "services"), 0755); err != nil {
		t.Fatal(err)
	}
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("expected nil error for empty dir, got %v", err)
	}
	if len(services) != 0 {
		t.Fatalf("expected empty map, got %v", services)
	}
}

// TestLoadServices_nonDirEntriesIgnored verifies non-directory entries are skipped.
func TestLoadServices_nonDirEntriesIgnored(t *testing.T) {
	dir := t.TempDir()
	svcDir := filepath.Join(dir, "workspace", "services")
	if err := os.MkdirAll(svcDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Create a file (not a dir) in the services dir.
	if err := os.WriteFile(filepath.Join(svcDir, "README.md"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	writeServiceFolder(t, dir, "redis", `
type: infra
container: redis
`)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service (non-dir ignored), got %d: %v", len(services), services)
	}
}

// TestLoadServices_unknownFieldRejected verifies unknown fields return an error.
func TestLoadServices_unknownFieldRejected(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "web", `
type: app
container: app-web
dir: ./services/web
unknown_field: bad
`)
	_, err := LoadServices(dir)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

// TestLoadServices_missingType verifies missing type returns ErrServiceTypeMissing.
func TestLoadServices_missingType(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "web", `
container: app-web
`)
	_, err := LoadServices(dir)
	if err == nil {
		t.Fatal("expected error for missing type, got nil")
	}
	if !errors.Is(err, ErrServiceTypeMissing) {
		t.Errorf("err = %v, want errors.Is ErrServiceTypeMissing", err)
	}
}

// TestLoadServices_unknownType verifies unknown type returns ErrServiceTypeUnknown.
func TestLoadServices_unknownType(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "web", `
type: widget
container: app-web
`)
	_, err := LoadServices(dir)
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
	if !errors.Is(err, ErrServiceTypeUnknown) {
		t.Errorf("err = %v, want errors.Is ErrServiceTypeUnknown", err)
	}
}

// TestLoadServices_fieldNotAllowed verifies disallowed fields return ErrServiceFieldNotAllowed.
func TestLoadServices_fieldNotAllowed(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "redis", `
type: tool
container: redis
dir: ./not-allowed
`)
	_, err := LoadServices(dir)
	if err == nil {
		t.Fatal("expected error for disallowed field, got nil")
	}
	if !errors.Is(err, ErrServiceFieldNotAllowed) {
		t.Errorf("err = %v, want errors.Is ErrServiceFieldNotAllowed", err)
	}
}

// TestLoadServices_bridgeBlock verifies the bridge block decodes on every
// service type and that omitting it leaves the tristate nil with type-based
// accessor defaults.
func TestLoadServices_bridgeBlock(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "web", `
type: app
container: app-web
dir: ./services/web
bridge:
  enabled: false
  shim_path: /opt/dwe/bin/dwe
  on_unreachable: warn
`)
	writeServiceFolder(t, dir, "worker", `
type: infra
container: worker
bridge:
  enabled: true
`)
	writeServiceFolder(t, dir, "adminer", `
type: tool
container: adminer
`)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}

	web := services["web"]
	if web.Bridge.Enabled == nil || *web.Bridge.Enabled != false {
		t.Errorf("web Bridge.Enabled = %v, want explicit false", web.Bridge.Enabled)
	}
	if web.BridgeShimPath() != "/opt/dwe/bin/dwe" {
		t.Errorf("web BridgeShimPath() = %q, want /opt/dwe/bin/dwe", web.BridgeShimPath())
	}
	if web.BridgeOnUnreachable() != BridgeOnUnreachableWarn {
		t.Errorf("web BridgeOnUnreachable() = %q, want warn", web.BridgeOnUnreachable())
	}

	worker := services["worker"]
	if !worker.BridgeEnabled() {
		t.Error("worker (infra) with explicit enabled: true should report BridgeEnabled() true")
	}

	adminer := services["adminer"]
	if adminer.Bridge.Enabled != nil {
		t.Errorf("adminer Bridge.Enabled should be nil when omitted, got %v", *adminer.Bridge.Enabled)
	}
	if adminer.BridgeEnabled() {
		t.Error("adminer (tool, omitted) should default BridgeEnabled() false")
	}
	if adminer.BridgeShimPath() != DefaultBridgeShimPath {
		t.Errorf("adminer BridgeShimPath() = %q, want default %q", adminer.BridgeShimPath(), DefaultBridgeShimPath)
	}
	if adminer.BridgeOnUnreachable() != BridgeOnUnreachableFail {
		t.Errorf("adminer BridgeOnUnreachable() = %q, want fail", adminer.BridgeOnUnreachable())
	}
}

// TestLoadServices_bridgeUnknownSubField verifies the strict KnownFields decode
// rejects unknown keys inside the bridge block.
func TestLoadServices_bridgeUnknownSubField(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "web", `
type: app
container: app-web
dir: ./services/web
bridge:
  enabled: true
  bogus: 1
`)
	_, err := LoadServices(dir)
	if err == nil {
		t.Fatal("expected error for unknown bridge sub-field, got nil")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("err = %v, want mention of unknown field %q", err, "bogus")
	}
}

// TestLoadServices_infraWithExtends verifies infra+extends returns ErrServiceExtendsCrossType.
func TestLoadServices_infraWithExtends(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "db", `
type: infra
container: db
extends: other
`)
	_, err := LoadServices(dir)
	if err == nil {
		t.Fatal("expected error for infra with extends, got nil")
	}
	if !errors.Is(err, ErrServiceExtendsCrossType) {
		t.Errorf("err = %v, want errors.Is ErrServiceExtendsCrossType", err)
	}
}

// TestLoadServices_portsScalar verifies scalar ports returns ErrServicePortsShape.
func TestLoadServices_portsScalar(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "web", `
type: app
container: app-web
dir: ./services/web
ports: 8080
`)
	_, err := LoadServices(dir)
	if err == nil {
		t.Fatal("expected error for scalar ports, got nil")
	}
	if !errors.Is(err, ErrServicePortsShape) {
		t.Errorf("err = %v, want errors.Is ErrServicePortsShape", err)
	}
}

// TestLoadServices_hostsScalar verifies scalar hosts returns ErrServiceHostsShape.
func TestLoadServices_hostsScalar(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "web", `
type: app
container: app-web
dir: ./services/web
hosts: web.localhost
`)
	_, err := LoadServices(dir)
	if err == nil {
		t.Fatal("expected error for scalar hosts, got nil")
	}
	if !errors.Is(err, ErrServiceHostsShape) {
		t.Errorf("err = %v, want errors.Is ErrServiceHostsShape", err)
	}
}

// TestLoadServices_portOutOfRange verifies out-of-range port returns ErrServicePortOutOfRange.
func TestLoadServices_portOutOfRange(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "web", `
type: app
container: app-web
dir: ./services/web
ports:
  http: 70000
`)
	_, err := LoadServices(dir)
	if err == nil {
		t.Fatal("expected error for port out of range, got nil")
	}
	if !errors.Is(err, ErrServicePortOutOfRange) {
		t.Errorf("err = %v, want errors.Is ErrServicePortOutOfRange", err)
	}
}

// TestLoadServices_multiErrorAggregation verifies multiple broken folders aggregate errors.
func TestLoadServices_multiErrorAggregation(t *testing.T) {
	dir := t.TempDir()
	// tool with dir → ErrServiceFieldNotAllowed
	writeServiceFolder(t, dir, "one", `
type: tool
container: one
dir: ./not-allowed-for-tool
`)
	// infra with extends → ErrServiceExtendsCrossType
	writeServiceFolder(t, dir, "two", `
type: infra
container: two
extends: one
`)
	// app with port out of range → ErrServicePortOutOfRange
	writeServiceFolder(t, dir, "three", `
type: app
container: app-three
dir: ./services/three
ports:
  bad: 70000
`)
	_, err := LoadServices(dir)
	if err == nil {
		t.Fatal("expected joined error, got nil")
	}
	for _, sentinel := range []error{
		ErrServiceFieldNotAllowed,
		ErrServiceExtendsCrossType,
		ErrServicePortOutOfRange,
	} {
		if !errors.Is(err, sentinel) {
			t.Errorf("err missing sentinel %v: %v", sentinel, err)
		}
	}
}

// TestLoadServices_extendsToposort verifies 3-level extends chain resolves in any dir order.
func TestLoadServices_extendsToposort(t *testing.T) {
	dir := t.TempDir()
	// grandparent → parent → child
	writeServiceFolder(t, dir, "grandparent", `
type: app
container: app-gp
dir: ./services/gp
`)
	writeServiceFolder(t, dir, "parent", `
type: app
container: app-parent
extends: grandparent
`)
	writeServiceFolder(t, dir, "child", `
type: app
container: app-child
extends: parent
`)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	// child should inherit dir from grandparent
	child := services["child"]
	if child.Dir != "./services/gp" {
		t.Errorf("child.Dir = %q, want ./services/gp (inherited from grandparent)", child.Dir)
	}
}

// TestLoadServices_extendsInheritsFields verifies Compose, Ports, Hosts, Configs inherit.
func TestLoadServices_extendsInheritsFields(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "base", `
type: app
container: app-base
dir: ./services/base
dir_internal: /workspace
work_dir_internal: /workspace/src
configs:
  - .env
compose:
  - compose/services/base/base.yml
ports:
  http: 8080
hosts:
  main: base.localhost
`)
	writeServiceFolder(t, dir, "child", `
type: app
container: app-child
extends: base
`)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	child := services["child"]
	if child.Dir != "./services/base" {
		t.Errorf("child.Dir = %q, want inherited ./services/base", child.Dir)
	}
	if len(child.Configs) != 1 || child.Configs[0].File != ".env" {
		t.Errorf("child.Configs = %v, want inherited [.env]", child.Configs)
	}
	if len(child.Compose) != 1 || child.Compose[0] != "compose/services/base/base.yml" {
		t.Errorf("child.Compose = %v, want inherited", child.Compose)
	}
	if child.Port("http") != 8080 {
		t.Errorf("child.Port(http) = %d, want inherited 8080", child.Port("http"))
	}
	if child.Host("main") != "base.localhost" {
		t.Errorf("child.Host(main) = %q, want inherited base.localhost", child.Host("main"))
	}
}

// TestLoadServices_extendsDefensiveCopy verifies mutating child's slice/map doesn't corrupt parent.
func TestLoadServices_extendsDefensiveCopy(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "base", `
type: app
container: app-base
dir: ./services/base
configs:
  - .env
compose:
  - compose/services/base/base.yml
ports:
  http: 8080
hosts:
  main: base.localhost
`)
	writeServiceFolder(t, dir, "child", `
type: app
container: app-child
extends: base
`)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	parent := services["base"]
	child := services["child"]

	child.Configs[0].File = "MUTATED"
	child.Compose[0] = "MUTATED.yml"
	if parent.Configs[0].File != ".env" {
		t.Errorf("parent.Configs corrupted: %v", parent.Configs)
	}
	if parent.Compose[0] != "compose/services/base/base.yml" {
		t.Errorf("parent.Compose corrupted: %v", parent.Compose)
	}

	child.Ports["http"] = ServicePortSpec{Port: 9999}
	child.Hosts["main"] = "mutated.localhost"
	if parent.Port("http") != 8080 {
		t.Errorf("parent.Ports corrupted: %v", parent.Ports)
	}
	if parent.Host("main") != "base.localhost" {
		t.Errorf("parent.Hosts corrupted: %v", parent.Hosts)
	}
}

// TestLoadServices_extendsInheritsRenderConfigAndGenerated verifies a child app
// service inherits the parent's render.config pin and generated: declarations
// (mirroring render.ide/ai/git inheritance). Without this, a child loses the
// parent's config pack and its generated-key safety checks (run-render skip,
// generated-missing predicate, validator cross-check) silently break.
func TestLoadServices_extendsInheritsRenderConfigAndGenerated(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "base", `
type: app
container: app-base
dir: ./services/base
render:
  config:
    template: laravel
generated:
  app_key:
    file: src/.env
    pattern: '^APP_KEY=(.*)$'
`)
	writeServiceFolder(t, dir, "child", `
type: app
container: app-child
extends: base
`)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	child := services["child"]

	if child.Render.Config == nil || child.Render.Config.Template != "laravel" {
		t.Fatalf("child.Render.Config = %+v, want inherited template laravel", child.Render.Config)
	}
	gen, ok := child.Generated["app_key"]
	if !ok {
		t.Fatalf("child.Generated missing inherited app_key: %v", child.Generated)
	}
	if gen.File != "src/.env" || gen.Pattern != "^APP_KEY=(.*)$" {
		t.Errorf("child.Generated[app_key] = %+v, want inherited file/pattern", gen)
	}

	// Defensive copy: mutating the child must not corrupt the parent.
	parent := services["base"]
	child.Render.Config.Template = "MUTATED"
	child.Generated["app_key"] = GeneratedField{File: "MUTATED", Pattern: "MUTATED"}
	if parent.Render.Config.Template != "laravel" {
		t.Errorf("parent.Render.Config corrupted: %+v", parent.Render.Config)
	}
	if parent.Generated["app_key"].File != "src/.env" {
		t.Errorf("parent.Generated corrupted: %+v", parent.Generated["app_key"])
	}

	// A child that redeclares either field keeps its own value (no overwrite).
	writeServiceFolder(t, dir, "child2", `
type: app
container: app-child2
extends: base
render:
  config:
    template: symfony
generated:
  own_key:
    file: src/own.env
    pattern: '^OWN=(.*)$'
`)
	services2, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices (child2): %v", err)
	}
	child2 := services2["child2"]
	if child2.Render.Config == nil || child2.Render.Config.Template != "symfony" {
		t.Errorf("child2.Render.Config = %+v, want own symfony", child2.Render.Config)
	}
	if _, ok := child2.Generated["own_key"]; !ok {
		t.Errorf("child2.Generated lost its own declaration: %v", child2.Generated)
	}
	if _, ok := child2.Generated["app_key"]; ok {
		t.Errorf("child2.Generated should not merge parent keys when it declares its own: %v", child2.Generated)
	}

	// An explicitly empty `generated: {}` is the child declaring its own (empty)
	// map and must wholly replace the parent's — NOT inherit it. Conflating this
	// with an omitted key (len()==0) would make a child that intentionally
	// cleared its generated declarations silently harvest/replay parent fields.
	writeServiceFolder(t, dir, "child3", `
type: app
container: app-child3
extends: base
generated: {}
`)
	services3, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices (child3): %v", err)
	}
	child3 := services3["child3"]
	if len(child3.Generated) != 0 {
		t.Errorf("child3.Generated = %v, want empty (explicit generated: {} wholly replaces parent)", child3.Generated)
	}
}

// TestLoadServices_extendsCrossType verifies app extends infra returns ErrServiceExtendsCrossType.
func TestLoadServices_extendsCrossType(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "infrasvc", `
type: infra
container: infra-ctr
`)
	writeServiceFolder(t, dir, "appsvc", `
type: app
container: app-ctr
dir: ./services/app
extends: infrasvc
`)
	_, err := LoadServices(dir)
	if err == nil {
		t.Fatal("expected ErrServiceExtendsCrossType for app extends infra, got nil")
	}
	if !errors.Is(err, ErrServiceExtendsCrossType) {
		t.Errorf("err = %v, want errors.Is ErrServiceExtendsCrossType", err)
	}
}

// TestLoadServiceFolder_happyPath verifies loading a single service folder.
func TestLoadServiceFolder_happyPath(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "web", `
type: app
container: app-web
dir: ./services/web
ports:
  http: 8080
`)
	svc, err := LoadServiceFolder(dir, "web")
	if err != nil {
		t.Fatalf("LoadServiceFolder: %v", err)
	}
	if !svc.IsApp() {
		t.Errorf("svc.Type = %s, want app", svc.Type)
	}
	if svc.Container != "app-web" {
		t.Errorf("svc.Container = %q, want app-web", svc.Container)
	}
	if svc.Port("http") != 8080 {
		t.Errorf("svc.Port(http) = %d, want 8080", svc.Port("http"))
	}
}

// TestLoadServiceFolder_missingFile verifies file not found returns error.
func TestLoadServiceFolder_missingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadServiceFolder(dir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing service folder, got nil")
	}
}

// TestLoadServiceFolder_preValidation verifies validation runs for LoadServiceFolder.
func TestLoadServiceFolder_preValidation(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "redis", `
type: tool
container: redis
dir: ./not-allowed-for-tool
`)
	_, err := LoadServiceFolder(dir, "redis")
	if err == nil {
		t.Fatal("expected error for disallowed field, got nil")
	}
	if !errors.Is(err, ErrServiceFieldNotAllowed) {
		t.Errorf("err = %v, want errors.Is ErrServiceFieldNotAllowed", err)
	}
}

// TestToggleRequires_IsKnown verifies all recognized values.
func TestToggleRequires_IsKnown(t *testing.T) {
	for _, r := range []ToggleRequires{RequiresUnspecified, RequiresNone, RequiresRestart, RequiresDeploy} {
		if !r.IsKnown() {
			t.Errorf("IsKnown(%q) = false, want true", r)
		}
	}
	if ToggleRequires("rstart").IsKnown() {
		t.Error("IsKnown(rstart) = true, want false")
	}
}

// TestToggleRequires_OrDefault verifies unspecified resolves to restart.
func TestToggleRequires_OrDefault(t *testing.T) {
	if got := RequiresUnspecified.OrDefault(); got != RequiresRestart {
		t.Errorf("OrDefault(unspecified) = %q, want restart", got)
	}
	if got := RequiresNone.OrDefault(); got != RequiresNone {
		t.Errorf("OrDefault(none) = %q, want none", got)
	}
	if got := RequiresDeploy.OrDefault(); got != RequiresDeploy {
		t.Errorf("OrDefault(deploy) = %q, want deploy", got)
	}
}

// TestToggleRequires_Resolve verifies the deploy-or-restart collapse rule and
// that other values pass through unchanged.
func TestToggleRequires_Resolve(t *testing.T) {
	cases := []struct {
		in       ToggleRequires
		deployed bool
		want     ToggleRequires
	}{
		{RequiresDeployOrRestart, false, RequiresDeploy},
		{RequiresDeployOrRestart, true, RequiresRestart},
		{RequiresRestart, false, RequiresRestart},
		{RequiresRestart, true, RequiresRestart},
		{RequiresDeploy, false, RequiresDeploy},
		{RequiresDeploy, true, RequiresDeploy},
		{RequiresNone, true, RequiresNone},
		{RequiresUnspecified, true, RequiresUnspecified},
	}
	for _, c := range cases {
		if got := c.in.Resolve(c.deployed); got != c.want {
			t.Errorf("%q.Resolve(%v) = %q, want %q", c.in, c.deployed, got, c.want)
		}
	}
}

// TestServiceToggleHooks_parseFullBlock verifies full hooks block parses for each toggleable type.
func TestServiceToggleHooks_parseFullBlock(t *testing.T) {
	for _, svcType := range []string{"app", "tool", "infra"} {
		t.Run(svcType, func(t *testing.T) {
			dir := t.TempDir()
			var yaml string
			switch svcType {
			case "app":
				yaml = `
type: app
container: ctr
dir: ./svc
on_enable:
  requires: deploy
  before:
    - cmd-before
  after:
    - cmd-after
on_disable:
  requires: restart
  before:
    - cmd-disable-before
notes:
  enable: "Run migrations after enabling"
  disable: "Data will be preserved"
`
			case "tool":
				yaml = `
type: tool
container: ctr
on_enable:
  requires: restart
on_disable:
  requires: none
notes:
  enable: "Tool enable note"
`
			case "infra":
				yaml = `
type: infra
container: ctr
on_enable:
  requires: deploy
  after:
    - post-infra-cmd
on_disable:
  requires: restart
notes:
  disable: "Infra disable note"
`
			}
			writeServiceFolder(t, dir, "svc", yaml)
			svc, err := LoadServiceFolder(dir, "svc")
			if err != nil {
				t.Fatalf("LoadServiceFolder(%s): %v", svcType, err)
			}
			if svc.OnEnable == nil {
				t.Fatal("OnEnable is nil")
			}
			if svc.OnDisable == nil {
				t.Fatal("OnDisable is nil")
			}
			if svc.Notes == nil {
				t.Fatal("Notes is nil")
			}
		})
	}
}

// TestServiceToggleHooks_parsePartialBlock verifies partial hooks block (requires only) parses.
func TestServiceToggleHooks_parsePartialBlock(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "svc", `
type: app
container: ctr
dir: ./svc
on_enable:
  requires: deploy
`)
	svc, err := LoadServiceFolder(dir, "svc")
	if err != nil {
		t.Fatalf("LoadServiceFolder: %v", err)
	}
	if svc.OnEnable == nil {
		t.Fatal("OnEnable is nil")
	}
	if svc.OnEnable.Requires != RequiresDeploy {
		t.Errorf("OnEnable.Requires = %q, want deploy", svc.OnEnable.Requires)
	}
	if len(svc.OnEnable.Before) != 0 {
		t.Errorf("OnEnable.Before = %v, want empty", svc.OnEnable.Before)
	}
	if svc.OnDisable != nil {
		t.Errorf("OnDisable = %v, want nil", svc.OnDisable)
	}
}

// TestServiceToggleHooks_parseAbsent verifies absent hooks produce nil pointers.
func TestServiceToggleHooks_parseAbsent(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "svc", `
type: app
container: ctr
dir: ./svc
`)
	svc, err := LoadServiceFolder(dir, "svc")
	if err != nil {
		t.Fatalf("LoadServiceFolder: %v", err)
	}
	if svc.OnEnable != nil {
		t.Errorf("OnEnable = %v, want nil", svc.OnEnable)
	}
	if svc.OnDisable != nil {
		t.Errorf("OnDisable = %v, want nil", svc.OnDisable)
	}
	if svc.Notes != nil {
		t.Errorf("Notes = %v, want nil", svc.Notes)
	}
}

// TestServiceToggleHooks_allowlistRegression verifies on_enable is NOT rejected by the field allowlist.
func TestServiceToggleHooks_allowlistRegression(t *testing.T) {
	for _, svcType := range []string{"app", "tool", "infra"} {
		t.Run(svcType, func(t *testing.T) {
			dir := t.TempDir()
			var content string
			switch svcType {
			case "app":
				content = "type: app\ncontainer: ctr\ndir: ./svc\non_enable:\n  requires: restart\n"
			case "tool":
				content = "type: tool\ncontainer: ctr\non_enable:\n  requires: none\n"
			case "infra":
				content = "type: infra\ncontainer: ctr\non_enable:\n  requires: restart\n"
			}
			writeServiceFolder(t, dir, "svc", content)
			_, err := LoadServiceFolder(dir, "svc")
			if err != nil {
				t.Errorf("LoadServiceFolder(%s) with on_enable: %v (expected ErrServiceFieldNotAllowed fix to let this through)", svcType, err)
			}
		})
	}
}

// TestServiceToggleHooks_unknownFieldRejected verifies typo inside on_enable is rejected.
func TestServiceToggleHooks_unknownFieldRejected(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "svc", `
type: app
container: ctr
dir: ./svc
on_enable:
  requirss: restart
`)
	_, err := LoadServiceFolder(dir, "svc")
	if err == nil {
		t.Fatal("expected error for typo inside on_enable, got nil")
	}
}

// TestDisplayIcon_appDefault verifies app type returns default icon.
func TestDisplayIcon_appDefault(t *testing.T) {
	t.Parallel()
	s := ServiceConfig{Type: ServiceTypeApp}
	if got := s.DisplayIcon(); got != "📦" {
		t.Errorf("got %q, want %q", got, "📦")
	}
}

// TestDisplayIcon_toolDefault verifies tool type returns default icon.
func TestDisplayIcon_toolDefault(t *testing.T) {
	t.Parallel()
	s := ServiceConfig{Type: ServiceTypeTool}
	if got := s.DisplayIcon(); got != "🔧" {
		t.Errorf("got %q, want %q", got, "🔧")
	}
}

// TestDisplayIcon_infraDefault verifies infra type returns default icon.
func TestDisplayIcon_infraDefault(t *testing.T) {
	t.Parallel()
	s := ServiceConfig{Type: ServiceTypeInfra}
	if got := s.DisplayIcon(); got != "🧱" {
		t.Errorf("got %q, want %q", got, "🧱")
	}
}

// TestDisplayIcon_explicitOverride verifies explicit icon overrides type default.
func TestDisplayIcon_explicitOverride(t *testing.T) {
	t.Parallel()
	s := ServiceConfig{Type: ServiceTypeApp, Icon: "🔧"}
	if got := s.DisplayIcon(); got != "🔧" {
		t.Errorf("got %q, want %q", got, "🔧")
	}
}

// TestDisplayIcon_unknownType verifies unknown type returns empty string.
func TestDisplayIcon_unknownType(t *testing.T) {
	t.Parallel()
	s := ServiceConfig{Type: ServiceType("unknown")}
	if got := s.DisplayIcon(); got != "" {
		t.Errorf("got %q, want %q", got, "")
	}
}

// TestDisplayIcon_zeroValue verifies zero-value ServiceConfig returns empty string.
func TestDisplayIcon_zeroValue(t *testing.T) {
	t.Parallel()
	s := ServiceConfig{}
	if got := s.DisplayIcon(); got != "" {
		t.Errorf("got %q, want %q", got, "")
	}
}

// TestLoadServiceFolder_iconField verifies icon field loads cleanly via LoadServiceFolder.
func TestLoadServiceFolder_iconField(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeServiceFolder(t, dir, "web", `
type: app
container: app-web
dir: ./services/web
icon: "🔧"
`)
	svc, err := LoadServiceFolder(dir, "web")
	if err != nil {
		t.Fatalf("LoadServiceFolder: %v", err)
	}
	if svc.Icon != "🔧" {
		t.Errorf("Icon = %q, want %q", svc.Icon, "🔧")
	}
}

// TestLoadServiceFolder_iconMissingAllowed verifies icon is optional and defaults to empty.
func TestLoadServiceFolder_iconMissingAllowed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeServiceFolder(t, dir, "web", `
type: app
container: app-web
dir: ./services/web
`)
	svc, err := LoadServiceFolder(dir, "web")
	if err != nil {
		t.Fatalf("LoadServiceFolder: %v", err)
	}
	if svc.Icon != "" {
		t.Errorf("Icon = %q, want empty", svc.Icon)
	}
}

// TestLoadServiceFolder_containerDefaultToFolderName verifies container defaults to folder name.
func TestLoadServiceFolder_containerDefaultToFolderName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeServiceFolder(t, dir, "myservice", `
type: tool
`)
	svc, err := LoadServiceFolder(dir, "myservice")
	if err != nil {
		t.Fatalf("LoadServiceFolder: %v", err)
	}
	if svc.Container != "myservice" {
		t.Errorf("Container = %q, want %q", svc.Container, "myservice")
	}
}

// TestLoadServiceFolder_containerExplicitWins verifies explicit container overrides folder default.
func TestLoadServiceFolder_containerExplicitWins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeServiceFolder(t, dir, "myservice", `
type: tool
container: custom-name
`)
	svc, err := LoadServiceFolder(dir, "myservice")
	if err != nil {
		t.Fatalf("LoadServiceFolder: %v", err)
	}
	if svc.Container != "custom-name" {
		t.Errorf("Container = %q, want %q", svc.Container, "custom-name")
	}
}

// TestLoadServices_containerInheritance verifies folder-name default is applied before extends-merge.
func TestLoadServices_containerInheritance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Parent with explicit container name
	writeServiceFolder(t, dir, "parent", `
type: app
container: parent-explicit
dir: ./services/parent
`)
	// Child without container — should get folder name "child", not inherit parent's
	writeServiceFolder(t, dir, "child", `
type: app
extends: parent
dir: ./services/child
`)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	child := services["child"]
	if child.Container != "child" {
		t.Errorf("child Container = %q, want %q (folder-name default, not inherited)", child.Container, "child")
	}
}

// TestLoadServices_richPortFormAndScheme verifies the new port-spec union
// decode (bare int + rich {port, scheme} object) and the EffectiveScheme
// precedence: per-port → info.scheme → runtime fallback.
func TestLoadServices_richPortFormAndScheme(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "vite", `
type: app
container: vite
dir: ./services/vite
ports:
  http: 5173
info:
  scheme: https
`)
	writeServiceFolder(t, dir, "api", `
type: app
container: api
dir: ./services/api
ports:
  http: 3000
  admin:
    port: 9443
    scheme: https
`)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}

	vite := services["vite"]
	if got := vite.Port("http"); got != 5173 {
		t.Errorf("vite.Port(http) = %d, want 5173", got)
	}
	if got := vite.Info.Scheme; got != "https" {
		t.Errorf("vite.Info.Scheme = %q, want https", got)
	}
	// Service-level info.scheme wins over the runtime fallback for any port
	// that has no per-port override.
	if got := vite.EffectiveScheme("http", false); got != "https" {
		t.Errorf("vite.EffectiveScheme(http, false) = %q, want https (info.scheme override)", got)
	}

	api := services["api"]
	if got := api.Port("admin"); got != 9443 {
		t.Errorf("api.Port(admin) = %d, want 9443", got)
	}
	if got := api.PortScheme("admin"); got != "https" {
		t.Errorf("api.PortScheme(admin) = %q, want https", got)
	}
	// Bare-int port falls through to runtime (api has no info.scheme).
	if got := api.EffectiveScheme("http", false); got != "http" {
		t.Errorf("api.EffectiveScheme(http, false) = %q, want http (runtime fallback)", got)
	}
	if got := api.EffectiveScheme("http", true); got != "https" {
		t.Errorf("api.EffectiveScheme(http, true) = %q, want https (runtime fallback)", got)
	}
	// Per-port override wins regardless of runtime.
	if got := api.EffectiveScheme("admin", false); got != "https" {
		t.Errorf("api.EffectiveScheme(admin, false) = %q, want https (per-port override)", got)
	}
}

// TestLoadServices_richPortFormUnknownField verifies the rich-form port
// decoder rejects fields other than {port, scheme} via the loader's strict
// pre-decode validator.
func TestLoadServices_richPortFormUnknownField(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "bad", `
type: app
container: bad
dir: ./services/bad
ports:
  http:
    port: 3000
    bogus: nope
`)
	if _, err := LoadServices(dir); err == nil {
		t.Fatal("expected error for unknown rich-port field, got nil")
	}
}

// TestLoadServices_invalidSchemeRejected verifies validatePortObject rejects
// scheme values outside {http, https}.
func TestLoadServices_invalidSchemeRejected(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "bad", `
type: app
container: bad
dir: ./services/bad
ports:
  http:
    port: 3000
    scheme: gopher
`)
	if _, err := LoadServices(dir); err == nil {
		t.Fatal("expected error for invalid port scheme, got nil")
	}
}

// TestLoadServices_richPortRequiresPortInServiceYml verifies the loader's
// pre-validator rejects rich-form ports in service.yml that omit `port:` —
// service definitions must declare their own port number. The overlay layer
// is allowed to omit port (handled by validateOverlayPorts), but service.yml
// is not.
func TestLoadServices_richPortRequiresPortInServiceYml(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "bad", `
type: app
container: bad
dir: ./services/bad
ports:
  http:
    scheme: https
`)
	if _, err := LoadServices(dir); err == nil {
		t.Fatal("expected error when service.yml omits port: in rich form")
	}
}

// TestLoadServices_richPortRejectsNullPort verifies UnmarshalYAML rejects
// `port: null` explicitly rather than silently coercing to zero.
func TestLoadServices_richPortRejectsNullPort(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "bad", `
type: app
container: bad
dir: ./services/bad
ports:
  http:
    port: null
    scheme: https
`)
	if _, err := LoadServices(dir); err == nil {
		t.Fatal("expected error for null port, got nil")
	}
}

// TestLoadConfig_overlaySchemeOnlyOverridesScheme verifies the overlay layer
// accepts rich-form `{scheme: https}` without `port:` and only overrides
// the scheme of an inherited port.
func TestLoadConfig_overlaySchemeOnlyOverridesScheme(t *testing.T) {
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
  api:
    ports:
      http:
        scheme: https
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "local.yml"), []byte(localYML), 0o644); err != nil {
		t.Fatalf("write local.yml: %v", err)
	}
	writeServiceFolder(t, dir, "api", `
type: app
container: api
required: true
dir: ./services/api
ports:
  http: 3000
`)

	cfg, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	api := cfg.Services["api"]
	if got := api.Port("http"); got != 3000 {
		t.Errorf("api.Port(http) = %d, want 3000 (preserved from service.yml)", got)
	}
	if got := api.PortScheme("http"); got != "https" {
		t.Errorf("api.PortScheme(http) = %q, want https (overlay)", got)
	}
}

// TestLoadConfig_overlayEmptyRichFormRejected verifies an overlay rich-form
// entry with neither port nor scheme is rejected with a clear diagnostic
// (not silently accepted as a no-op).
func TestLoadConfig_overlayEmptyRichFormRejected(t *testing.T) {
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
  api:
    ports:
      http: {}
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "local.yml"), []byte(localYML), 0o644); err != nil {
		t.Fatalf("write local.yml: %v", err)
	}
	writeServiceFolder(t, dir, "api", `
type: app
container: api
required: true
dir: ./services/api
ports:
  http: 3000
`)
	if _, err := LoadConfig(filepath.Join(dir, "workspace.yml")); err == nil {
		t.Fatal("expected error for empty rich-form overlay, got nil")
	}
}

// TestLoadConfig_overlaySchemeOnlyForUndeclaredPortRejected verifies that
// applyDeferredOverlaySchemes rejects a scheme-only overlay targeting a port
// that is neither declared in service.yml nor inherited via extends.
// Without this guard, a typo like `services.api.ports.htt: {scheme: https}`
// would silently materialise a `{Port: 0, Scheme: "https"}` entry, leaking
// an invalid port number into status JSON and env conflict probes.
func TestLoadConfig_overlaySchemeOnlyForUndeclaredPortRejected(t *testing.T) {
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
  api:
    ports:
      htt:
        scheme: https
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "local.yml"), []byte(localYML), 0o644); err != nil {
		t.Fatalf("write local.yml: %v", err)
	}
	writeServiceFolder(t, dir, "api", `
type: app
container: api
required: true
dir: ./services/api
ports:
  http: 3000
`)
	_, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err == nil {
		t.Fatal("expected error for scheme-only overlay on undeclared port, got nil")
	}
	if !strings.Contains(err.Error(), "scheme-only overlay") {
		t.Errorf("error %q should mention scheme-only overlay", err.Error())
	}
}

// TestLoadConfig_overlaySchemeOnlyOnInheritedPortApplies verifies that a
// scheme-only overlay on a port the child inherits from its parent via
// `extends:` is correctly applied AFTER inheritance — the child should end
// up with the parent's port number plus the overlay's scheme override,
// without losing the inherited port to a `Port: 0` overlay collision.
func TestLoadConfig_overlaySchemeOnlyOnInheritedPortApplies(t *testing.T) {
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
  child:
    ports:
      http:
        scheme: https
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "local.yml"), []byte(localYML), 0o644); err != nil {
		t.Fatalf("write local.yml: %v", err)
	}
	writeServiceFolder(t, dir, "parent", `
type: app
container: parent
required: true
dir: ./services/parent
ports:
  http: 3000
`)
	writeServiceFolder(t, dir, "child", `
type: app
container: child
required: true
extends: parent
dir: ./services/child
`)
	cfg, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	child := cfg.Services["child"]
	if got := child.Port("http"); got != 3000 {
		t.Errorf("child.Port(http) = %d, want 3000 (inherited from parent)", got)
	}
	if got := child.PortScheme("http"); got != "https" {
		t.Errorf("child.PortScheme(http) = %q, want https (overlay)", got)
	}
}

// TestLoadConfig_overlayPortNullRejected verifies that an overlay with
// `port: null` is rejected outright. Without this guard, a typo would
// manufacture a `Port: 0` entry that bypasses Phase 2's scheme-only handling
// AND blocks parent port inheritance via the `len(svc.Ports) == 0` check.
func TestLoadConfig_overlayPortNullRejected(t *testing.T) {
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
  child:
    ports:
      http:
        port: null
        scheme: https
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "local.yml"), []byte(localYML), 0o644); err != nil {
		t.Fatalf("write local.yml: %v", err)
	}
	writeServiceFolder(t, dir, "parent", `
type: app
container: parent
required: true
dir: ./services/parent
ports:
  http: 3000
`)
	writeServiceFolder(t, dir, "child", `
type: app
container: child
required: true
extends: parent
dir: ./services/child
`)
	_, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err == nil {
		t.Fatal("expected error for port: null overlay, got nil")
	}
	if !strings.Contains(err.Error(), "cannot be null") {
		t.Errorf("error %q should mention null rejection", err.Error())
	}
}

// TestLoadServices_invalidInfoSchemeRejected verifies the loader rejects
// info.scheme values outside {"", "http", "https"} at load time, not only
// in the validate subsystem — EffectiveScheme trusts the field directly.
func TestLoadServices_invalidInfoSchemeRejected(t *testing.T) {
	dir := t.TempDir()
	writeServiceFolder(t, dir, "bad", `
type: app
container: bad
dir: ./services/bad
info:
  scheme: ftp
ports:
  http: 3000
`)
	if _, err := LoadServices(dir); err == nil {
		t.Fatal("expected error for info.scheme: ftp, got nil")
	}
}

// TestLoadConfig_extendsInheritsInfoScheme verifies that a child extending a
// parent inherits Info.Scheme when the child does not declare its own.
// EffectiveScheme depends on this for child services to render URLs with
// the parent's documented scheme.
func TestLoadConfig_extendsInheritsInfoScheme(t *testing.T) {
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
	writeServiceFolder(t, dir, "parent", `
type: app
container: parent
required: true
dir: ./services/parent
ports:
  http: 5173
info:
  scheme: https
`)
	writeServiceFolder(t, dir, "child", `
type: app
container: child
required: true
extends: parent
dir: ./services/child
`)
	cfg, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	child := cfg.Services["child"]
	if got := child.Port("http"); got != 5173 {
		t.Errorf("child.Port(http) = %d, want 5173 (inherited)", got)
	}
	if got := child.Info.Scheme; got != "https" {
		t.Errorf("child.Info.Scheme = %q, want https (inherited from parent)", got)
	}
	if got := child.EffectiveScheme("http", false); got != "https" {
		t.Errorf("child.EffectiveScheme(http, false) = %q, want https (inherited from parent)", got)
	}
}

// TestLoadConfig_extendsChildInfoSchemeOverridesParent verifies that the
// child's own Info.Scheme takes precedence over the parent's during extends.
func TestLoadConfig_extendsChildInfoSchemeOverridesParent(t *testing.T) {
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
	writeServiceFolder(t, dir, "parent", `
type: app
container: parent
required: true
dir: ./services/parent
ports:
  http: 5173
info:
  scheme: https
`)
	writeServiceFolder(t, dir, "child", `
type: app
container: child
required: true
extends: parent
dir: ./services/child
info:
  scheme: http
`)
	cfg, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Services["child"].Info.Scheme; got != "http" {
		t.Errorf("child.Info.Scheme = %q, want http (child override wins)", got)
	}
}

// TestLoadConfig_overlaySchemeOnlyOnParentPropagatesToChild verifies that a
// scheme-only overlay applied to a parent's own declared port is visible to
// children that extend the parent — i.e. extends inherits the overlaid
// scheme, not the pre-overlay one. This was Codex's round-3 finding: applying
// scheme-only overlays only in Phase 2 (after extends) would clone the
// parent's pre-overlay Ports map into the child and then mutate only the
// parent, losing the override on the child side.
func TestLoadConfig_overlaySchemeOnlyOnParentPropagatesToChild(t *testing.T) {
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
      http:
        scheme: https
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "local.yml"), []byte(localYML), 0o644); err != nil {
		t.Fatalf("write local.yml: %v", err)
	}
	writeServiceFolder(t, dir, "parent", `
type: app
container: parent
required: true
dir: ./services/parent
ports:
  http: 3000
`)
	writeServiceFolder(t, dir, "child", `
type: app
container: child
required: true
extends: parent
dir: ./services/child
`)
	cfg, err := LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	parent := cfg.Services["parent"]
	child := cfg.Services["child"]
	if got := parent.PortScheme("http"); got != "https" {
		t.Errorf("parent.PortScheme(http) = %q, want https", got)
	}
	if got := child.Port("http"); got != 3000 {
		t.Errorf("child.Port(http) = %d, want 3000 (inherited)", got)
	}
	if got := child.PortScheme("http"); got != "https" {
		t.Errorf("child.PortScheme(http) = %q, want https (parent overlay propagated through extends)", got)
	}
	if got := child.EffectiveScheme("http", false); got != "https" {
		t.Errorf("child.EffectiveScheme(http, false) = %q, want https", got)
	}
}
