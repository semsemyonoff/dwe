package config

import (
	"errors"
	"testing"
)

func TestServiceType_Validate(t *testing.T) {
	tests := []struct {
		name    string
		input   ServiceType
		wantErr error
	}{
		{"app", ServiceTypeApp, nil},
		{"tool", ServiceTypeTool, nil},
		{"infra", ServiceTypeInfra, nil},
		{"empty", ServiceType(""), ErrServiceTypeMissing},
		{"unknown", ServiceType("worker"), ErrServiceTypeUnknown},
		{"mixed-case", ServiceType("App"), ErrServiceTypeUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.input.Validate()
			switch {
			case tc.wantErr == nil && err != nil:
				t.Fatalf("Validate(%q) = %v, want nil", tc.input, err)
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Fatalf("Validate(%q) = %v, want errors.Is %v", tc.input, err, tc.wantErr)
			}
		})
	}
}

func TestServiceType_IsValidAndPredicates(t *testing.T) {
	if !ServiceTypeApp.IsValid() || !ServiceTypeTool.IsValid() || !ServiceTypeInfra.IsValid() {
		t.Fatal("known types must report IsValid")
	}
	if ServiceType("worker").IsValid() {
		t.Fatal("unknown type must not report IsValid")
	}
	if !ServiceTypeApp.IsApp() || ServiceTypeApp.IsTool() || ServiceTypeApp.IsInfra() {
		t.Fatal("IsApp predicate mismatch")
	}
	if !ServiceTypeTool.IsTool() || ServiceTypeTool.IsApp() || ServiceTypeTool.IsInfra() {
		t.Fatal("IsTool predicate mismatch")
	}
	if !ServiceTypeInfra.IsInfra() || ServiceTypeInfra.IsApp() || ServiceTypeInfra.IsTool() {
		t.Fatal("IsInfra predicate mismatch")
	}
}

func TestServiceConfig_TypeForwarders(t *testing.T) {
	s := ServiceConfig{Type: ServiceTypeApp}
	if !s.IsApp() || s.IsTool() || s.IsInfra() {
		t.Fatal("ServiceConfig.IsApp forwarder broken")
	}
	s.Type = ServiceTypeTool
	if !s.IsTool() {
		t.Fatal("ServiceConfig.IsTool forwarder broken")
	}
	s.Type = ServiceTypeInfra
	if !s.IsInfra() {
		t.Fatal("ServiceConfig.IsInfra forwarder broken")
	}
}

func TestServiceConfig_PortHostHelpers(t *testing.T) {
	s := ServiceConfig{
		Ports: map[string]int{"http": 8080},
		Hosts: map[string]string{"main": "example.test"},
	}
	if got := s.Port("http"); got != 8080 {
		t.Fatalf("Port(http) = %d, want 8080", got)
	}
	if got := s.Port("missing"); got != 0 {
		t.Fatalf("Port(missing) = %d, want 0", got)
	}
	if got := s.Host("main"); got != "example.test" {
		t.Fatalf("Host(main) = %q", got)
	}
	if got := s.Host("missing"); got != "" {
		t.Fatalf("Host(missing) = %q, want empty", got)
	}
	// Zero-value receiver safety: nil maps must not panic.
	var zero ServiceConfig
	if zero.Port("x") != 0 || zero.Host("x") != "" {
		t.Fatal("zero-value receiver must return zero values")
	}
}

func TestAllowedFieldsFor(t *testing.T) {
	commonFields := []string{"type", "container", "required", "compose", "ports", "hosts", "status"}

	appOnly := []string{"dir", "dir_internal", "work_dir_internal", "configs", "dirs", "extends", "cli", "render"}
	dependsOn := "depends_on"

	tests := []struct {
		name     string
		t        ServiceType
		mustHave []string
		mustLack []string
	}{
		{
			name:     "app allows everything",
			t:        ServiceTypeApp,
			mustHave: append(append([]string{}, commonFields...), append(appOnly, dependsOn)...),
			mustLack: nil,
		},
		{
			name:     "infra allows depends_on but not app-only",
			t:        ServiceTypeInfra,
			mustHave: append(append([]string{}, commonFields...), dependsOn),
			mustLack: appOnly,
		},
		{
			name:     "tool rejects depends_on and app-only",
			t:        ServiceTypeTool,
			mustHave: commonFields,
			mustLack: append([]string{dependsOn}, appOnly...),
		},
		{
			name:     "unknown type returns empty",
			t:        ServiceType("worker"),
			mustHave: nil,
			mustLack: append(append([]string{}, commonFields...), dependsOn),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := allowedFieldsFor(tc.t)
			for _, f := range tc.mustHave {
				if !got[f] {
					t.Errorf("expected field %q to be allowed for %s", f, tc.t)
				}
			}
			for _, f := range tc.mustLack {
				if got[f] {
					t.Errorf("expected field %q to be rejected for %s", f, tc.t)
				}
			}
		})
	}

	// Confirm fresh-map semantics: mutating the returned map must not affect the next call.
	m := allowedFieldsFor(ServiceTypeApp)
	m["nonsense"] = true
	if allowedFieldsFor(ServiceTypeApp)["nonsense"] {
		t.Fatal("allowedFieldsFor must return a fresh map per call")
	}
}
