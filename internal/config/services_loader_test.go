package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeServiceFolder creates a single service folder at
// <baseDir>/devbox/services/<name>/service.yml with the given content.
func writeServiceFolder(t *testing.T, baseDir, name, content string) {
	t.Helper()
	dir := filepath.Join(baseDir, "devbox", "services", name)
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
mandatory: true
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

// TestLoadServices_missingDir verifies absent devbox/services/ returns empty map (not error).
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

// TestLoadServices_emptyDir verifies devbox/services/ with no subdirs returns empty map.
func TestLoadServices_emptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "devbox", "services"), 0755); err != nil {
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
	svcDir := filepath.Join(dir, "devbox", "services")
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

	child.Ports["http"] = 9999
	child.Hosts["main"] = "mutated.localhost"
	if parent.Port("http") != 8080 {
		t.Errorf("parent.Ports corrupted: %v", parent.Ports)
	}
	if parent.Host("main") != "base.localhost" {
		t.Errorf("parent.Hosts corrupted: %v", parent.Hosts)
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
	if got := s.DisplayIcon(); got != "⚙️" {
		t.Errorf("got %q, want %q", got, "⚙️")
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
