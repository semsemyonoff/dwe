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

func TestRemappedHostPortServices(t *testing.T) {
	cfg := remapTestConfig()
	cfg.Services["core"] = config.ServiceConfig{
		Enabled: true, Required: true,
		Ports: map[string]config.ServicePortSpec{"http": {Port: 8000}},
	}
	tests := []struct {
		name string
		cfg  *config.DweConfig
		scn  *Scenario
		want map[string]bool
	}{
		{
			name: "enabled services with an in-range port only",
			cfg:  cfg,
			scn:  &Scenario{},
			want: map[string]bool{"core": true, "db": true, "nginx": true},
		},
		{
			name: "scenario enable and disable move membership",
			cfg:  cfg,
			scn:  &Scenario{Env: ScenarioEnv{Services: ScenarioServices{Enable: []string{"minio"}, Disable: []string{"db"}}}},
			want: map[string]bool{"core": true, "minio": true, "nginx": true},
		},
		{
			// The loader keeps a required service enabled, but the remap skips
			// it — membership follows the remap, not Enabled.
			name: "required service disabled by the scenario is not remapped",
			cfg:  cfg,
			scn:  &Scenario{Env: ScenarioEnv{Services: ScenarioServices{Disable: []string{"core"}}}},
			want: map[string]bool{"db": true, "nginx": true},
		},
		{
			name: "nil config",
			cfg:  nil,
			scn:  &Scenario{},
			want: map[string]bool{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RemappedHostPortServices(tt.cfg, tt.scn); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("RemappedHostPortServices = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScenarioAutoPortVarPaths(t *testing.T) {
	scn := &Scenario{Env: ScenarioEnv{Vars: map[string]any{
		"ports.web":    AutoPortSentinel,
		"ports.api":    AutoPortSentinel,
		"feature.flag": "on",
		"ports.fixed":  8080,
	}}}
	if got, want := scn.AutoPortVarPaths(), []string{"ports.api", "ports.web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("AutoPortVarPaths = %v, want %v", got, want)
	}
	var nilScn *Scenario
	if got := nilScn.AutoPortVarPaths(); got != nil {
		t.Fatalf("nil scenario AutoPortVarPaths = %v, want nil", got)
	}
}

func TestCoversInterpolatedHostPort(t *testing.T) {
	cfg := remapTestConfig()
	auto := &Scenario{Env: ScenarioEnv{Vars: map[string]any{"ports.valkey": AutoPortSentinel}}}
	interp := func(varPath, source string) config.IsolationFinding {
		return config.IsolationFinding{Kind: config.KindInterpolatedHostPort, VarPath: varPath, SourceService: source}
	}
	tests := []struct {
		name string
		scn  *Scenario
		f    config.IsolationFinding
		want bool
	}{
		{"vars path set to auto", auto, interp("ports.valkey", ""), true},
		{"vars path without auto", &Scenario{}, interp("ports.valkey", ""), false},
		{"other vars path set to auto", auto, interp("ports.redis", ""), false},
		{"source service remapped", &Scenario{}, interp("", "db"), true},
		{"source service disabled by the scenario", &Scenario{Env: ScenarioEnv{Services: ScenarioServices{Disable: []string{"db"}}}}, interp("", "db"), false},
		{"source service off by default", &Scenario{}, interp("", "minio"), false},
		{"neither field", auto, interp("", ""), false},
		{"nil scenario", nil, interp("ports.valkey", ""), false},
		{"other kind never covered", auto, config.IsolationFinding{Kind: config.KindRawHostPort, VarPath: "ports.valkey"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CoversInterpolatedHostPort(cfg, tt.scn, tt.f); got != tt.want {
				t.Fatalf("CoversInterpolatedHostPort = %v, want %v", got, tt.want)
			}
		})
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
