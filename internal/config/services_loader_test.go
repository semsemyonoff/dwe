package config

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestLoadServicesConfig_happyPathByType confirms each canonical type loads
// without error and that key per-type fields decode through cleanly.
func TestLoadServicesConfig_happyPathByType(t *testing.T) {
	tests := []struct {
		name   string
		file   string
		assert func(t *testing.T, services map[string]ServiceConfig)
	}{
		{
			name: "app",
			file: "happy_app.yml",
			assert: func(t *testing.T, s map[string]ServiceConfig) {
				web := s["web"]
				if !web.IsApp() {
					t.Fatalf("web type = %s, want app", web.Type)
				}
				if web.Port("http") != 8080 || web.Port("grpc") != 9090 {
					t.Fatalf("web ports = %v", web.Ports)
				}
				if web.Host("main") != "web.localhost" {
					t.Fatalf("web hosts = %v", web.Hosts)
				}
			},
		},
		{
			name: "tool",
			file: "happy_tool.yml",
			assert: func(t *testing.T, s map[string]ServiceConfig) {
				adm := s["adminer"]
				if !adm.IsTool() {
					t.Fatalf("adminer type = %s, want tool", adm.Type)
				}
				if adm.Port("web") != 8080 {
					t.Fatalf("adminer ports = %v", adm.Ports)
				}
			},
		},
		{
			name: "infra",
			file: "happy_infra.yml",
			assert: func(t *testing.T, s map[string]ServiceConfig) {
				db := s["db"]
				cache := s["cache"]
				if !db.IsInfra() || !cache.IsInfra() {
					t.Fatalf("expected infra types: db=%s cache=%s", db.Type, cache.Type)
				}
				if len(cache.DependsOn) != 1 || cache.DependsOn[0] != "db" {
					t.Fatalf("cache.DependsOn = %v", cache.DependsOn)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			services, err := LoadServicesConfig(filepath.Join("testdata", "services", tc.file))
			if err != nil {
				t.Fatalf("LoadServicesConfig: %v", err)
			}
			tc.assert(t, services)
		})
	}
}

// TestLoadServicesConfig_sentinelErrors covers each sentinel-error path with a
// minimal fixture.
func TestLoadServicesConfig_sentinelErrors(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		sentinel error
	}{
		{"missing type", "missing_type.yml", ErrServiceTypeMissing},
		{"unknown type", "unknown_type.yml", ErrServiceTypeUnknown},
		{"tool with dir", "tool_with_dir.yml", ErrServiceFieldNotAllowed},
		{"infra with extends", "infra_with_extends.yml", ErrServiceExtendsCrossType},
		{"ports scalar", "ports_scalar.yml", ErrServicePortsShape},
		{"hosts scalar", "hosts_scalar.yml", ErrServiceHostsShape},
		{"port out of range", "port_out_of_range.yml", ErrServicePortOutOfRange},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadServicesConfig(filepath.Join("testdata", "services", tc.file))
			if err == nil {
				t.Fatalf("expected error matching %v, got nil", tc.sentinel)
			}
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("err = %v, want errors.Is %v", err, tc.sentinel)
			}
		})
	}
}

// TestLoadServicesConfig_multiErrorAggregation confirms a single file with
// multiple violations produces a joined error matching every sentinel.
func TestLoadServicesConfig_multiErrorAggregation(t *testing.T) {
	_, err := LoadServicesConfig(filepath.Join("testdata", "services", "multi_error.yml"))
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

// TestLoadServicesConfig_extendsInheritsNewFields proves Compose, Ports, Hosts,
// and Configs inherit from parent when child leaves them empty.
func TestLoadServicesConfig_extendsInheritsNewFields(t *testing.T) {
	services, err := LoadServicesConfig(filepath.Join("testdata", "services", "app_extends.yml"))
	if err != nil {
		t.Fatalf("LoadServicesConfig: %v", err)
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

// TestLoadServicesConfig_extendsDefensiveCopy confirms mutating the child's
// inherited slice/map does not corrupt the parent.
func TestLoadServicesConfig_extendsDefensiveCopy(t *testing.T) {
	services, err := LoadServicesConfig(filepath.Join("testdata", "services", "app_extends.yml"))
	if err != nil {
		t.Fatalf("LoadServicesConfig: %v", err)
	}
	parent := services["base"]
	child := services["child"]

	// Slice mutation on child must not affect parent backing storage.
	child.Configs[0].File = "MUTATED"
	child.Compose[0] = "MUTATED.yml"
	if parent.Configs[0].File != ".env" {
		t.Errorf("parent.Configs corrupted: %v", parent.Configs)
	}
	if parent.Compose[0] != "compose/services/base/base.yml" {
		t.Errorf("parent.Compose corrupted: %v", parent.Compose)
	}

	// Map mutation on child must not affect parent.
	child.Ports["http"] = 9999
	child.Hosts["main"] = "mutated.localhost"
	if parent.Port("http") != 8080 {
		t.Errorf("parent.Ports corrupted: %v", parent.Ports)
	}
	if parent.Host("main") != "base.localhost" {
		t.Errorf("parent.Hosts corrupted: %v", parent.Hosts)
	}
}
