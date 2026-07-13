package envtest

import (
	"reflect"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

func remapTestConfig() *config.DweConfig {
	return &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"main":  {Enabled: true}, // enabled but declares no host port
			"db":    {Enabled: true, Ports: map[string]config.ServicePortSpec{"mysql": {Port: 13306}}},
			"nginx": {Enabled: true, Ports: map[string]config.ServicePortSpec{"http": {Port: 80}, "https": {Port: 443, Scheme: "https"}}},
			"minio": {Enabled: false, Ports: map[string]config.ServicePortSpec{"api": {Port: 9010}}},
			"bad":   {Enabled: true, Ports: map[string]config.ServicePortSpec{"x": {Port: 0}}}, // out of range, skipped
		},
	}
}

func TestEnabledHostPortKeys(t *testing.T) {
	cfg := remapTestConfig()
	tests := []struct {
		name string
		scn  *Scenario
		want []hostPortKey
	}{
		{
			name: "enabled+valid ports only, sorted; disabled/portless/out-of-range skipped",
			scn:  &Scenario{},
			want: []hostPortKey{{"db", "mysql"}, {"nginx", "http"}, {"nginx", "https"}},
		},
		{
			name: "scenario disable drops a service's ports",
			scn:  &Scenario{Env: ScenarioEnv{Services: ScenarioServices{Disable: []string{"nginx"}}}},
			want: []hostPortKey{{"db", "mysql"}},
		},
		{
			name: "scenario enable brings in an off-by-default service",
			scn:  &Scenario{Env: ScenarioEnv{Services: ScenarioServices{Enable: []string{"minio"}}}},
			want: []hostPortKey{{"db", "mysql"}, {"minio", "api"}, {"nginx", "http"}, {"nginx", "https"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := enabledHostPortKeys(cfg, tt.scn)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("enabledHostPortKeys = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnabledHostPortKeys_NilConfig(t *testing.T) {
	if got := enabledHostPortKeys(nil, &Scenario{}); got != nil {
		t.Fatalf("enabledHostPortKeys(nil) = %v, want nil", got)
	}
}

func TestBuildHostPortOverrides_PreservesScheme(t *testing.T) {
	cfg := remapTestConfig()
	keys := []hostPortKey{{"db", "mysql"}, {"nginx", "https"}}
	got := buildHostPortOverrides(cfg, keys, []int{20001, 20002})
	want := []HostPortOverride{
		{Service: "db", PortName: "mysql", Port: 20001, Scheme: ""},
		{Service: "nginx", PortName: "https", Port: 20002, Scheme: "https"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildHostPortOverrides = %+v, want %+v", got, want)
	}
}

func TestApplyHostPortOverrides(t *testing.T) {
	// overlay already carries a services.nginx.enabled toggle — the remap must
	// coexist with it, not clobber it.
	overlay := map[string]any{
		"services": map[string]any{
			"nginx": map[string]any{"enabled": true},
		},
	}
	ApplyHostPortOverrides(overlay, []HostPortOverride{
		{Service: "db", PortName: "mysql", Port: 20001},
		{Service: "nginx", PortName: "http", Port: 20002},
		{Service: "nginx", PortName: "https", Port: 20003, Scheme: "https"},
	})

	services := overlay["services"].(map[string]any)

	// pre-existing toggle preserved
	if nginx := services["nginx"].(map[string]any); nginx["enabled"] != true {
		t.Fatalf("services.nginx.enabled was clobbered: %+v", nginx)
	}

	// bare int for a scheme-less port
	db := services["db"].(map[string]any)["ports"].(map[string]any)
	if db["mysql"] != 20001 {
		t.Fatalf("services.db.ports.mysql = %v, want 20001", db["mysql"])
	}

	nginxPorts := services["nginx"].(map[string]any)["ports"].(map[string]any)
	if nginxPorts["http"] != 20002 {
		t.Fatalf("services.nginx.ports.http = %v (%T), want bare int 20002", nginxPorts["http"], nginxPorts["http"])
	}
	// {port, scheme} mapping preserves the original scheme
	https, ok := nginxPorts["https"].(map[string]any)
	if !ok || https["port"] != 20003 || https["scheme"] != "https" {
		t.Fatalf("services.nginx.ports.https = %v, want {port:20003, scheme:https}", nginxPorts["https"])
	}
}

func TestApplyHostPortOverrides_Empty(t *testing.T) {
	overlay := map[string]any{"vars": map[string]any{"a": 1}}
	ApplyHostPortOverrides(overlay, nil)
	if _, has := overlay["services"]; has {
		t.Fatalf("ApplyHostPortOverrides(nil) added a services key: %+v", overlay)
	}
}
