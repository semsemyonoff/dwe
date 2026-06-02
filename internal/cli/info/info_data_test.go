package info

import (
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// TestBuildAutoURLsData_ProxySchemePrecedence mirrors the table in
// internal/core/ui/render/info_auto_urls_test.go for the JSON data path —
// the two implementations live in sibling packages and must stay in sync.
// The cases here are the load-bearing ones for the proxy-scheme precedence
// contract; broader coverage is owned by the render-side table.
func TestBuildAutoURLsData_ProxySchemePrecedence(t *testing.T) {
	t.Parallel()

	type want struct {
		label string
		value string
	}

	tests := []struct {
		name string
		cfg  *config.DweConfig
		spec *config.AutoURLsSpec
		want []want
	}{
		{
			name: "routed info.scheme overrides proxied URL with mixed-scheme proxy",
			cfg: &config.DweConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports: map[string]config.ServicePortSpec{
							"http":  {Port: 80},
							"https": {Port: 443},
						},
					},
					"storefront": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Ports:   map[string]config.ServicePortSpec{},
						Hosts:   map[string]string{"web": "tbm.shop.local"},
						Info:    config.ServiceInfoBlock{Title: "Storefront", Scheme: "https"},
					},
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Ports:   map[string]config.ServicePortSpec{},
						Hosts:   map[string]string{"web": "tbm.local"},
						Info:    config.ServiceInfoBlock{Title: "Main"},
					},
				},
			},
			spec: &config.AutoURLsSpec{Include: []string{"app"}, PortVia: "nginx"},
			want: []want{
				{"Main", "http://tbm.local"},
				{"Storefront", "https://tbm.shop.local"},
			},
		},
		{
			// Regression: proxy.Info.Scheme must NOT leak onto routed URLs.
			name: "proxy Info.Scheme does not leak to proxied URL",
			cfg: &config.DweConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]config.ServicePortSpec{"http": {Port: 8080}},
						Info:    config.ServiceInfoBlock{Scheme: "https"},
					},
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Ports:   map[string]config.ServicePortSpec{},
						Hosts:   map[string]string{"web": "pilot.local"},
						Info:    config.ServiceInfoBlock{Title: "Main"},
					},
				},
			},
			spec: &config.AutoURLsSpec{Include: []string{"app"}, PortVia: "nginx"},
			want: []want{{"Main", "http://pilot.local:8080"}},
		},
		{
			// Explicit-but-missing PortVia must not silently fall back to
			// auto-detection (parity with renderAutoURLs).
			name: "explicit-but-missing PortVia yields no proxied URL",
			cfg: &config.DweConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]config.ServicePortSpec{"http": {Port: 80}},
					},
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Ports:   map[string]config.ServicePortSpec{},
						Hosts:   map[string]string{"web": "pilot.local"},
						Info:    config.ServiceInfoBlock{Title: "Main"},
					},
				},
			},
			spec: &config.AutoURLsSpec{Include: []string{"app"}, PortVia: "nginx-typo"},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildAutoURLsData(tt.cfg, tt.spec)
			if len(got) != len(tt.want) {
				t.Fatalf("buildAutoURLsData() len = %d, want %d; got=%v", len(got), len(tt.want), got)
			}
			for i, w := range tt.want {
				if got[i].Label != w.label || got[i].Value != w.value {
					t.Errorf("item[%d] = {%q, %q}, want {%q, %q}", i, got[i].Label, got[i].Value, w.label, w.value)
				}
			}
		})
	}
}
