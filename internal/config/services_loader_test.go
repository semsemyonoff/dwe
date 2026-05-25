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
