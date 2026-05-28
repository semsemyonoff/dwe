package ui

import (
	"strings"
	"testing"

	"devbox-cli/internal/core/project/config"
)

func TestRenderAutoHosts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     *config.DevboxConfig
		spec    *config.AutoHostsSpec
		wantOut string
	}{
		{
			name: "dedup across services",
			cfg: &config.DevboxConfig{
				Services: map[string]config.ServiceConfig{
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts: map[string]string{
							"web": "pilot.local",
						},
					},
					"catalog": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts: map[string]string{
							"web": "pilot.catalog.local",
						},
					},
				},
			},
			spec: &config.AutoHostsSpec{
				Include: []string{"app"},
				IP:      "127.0.0.1",
			},
			// Services are sorted alphabetically: catalog < main
			wantOut: strings.Join([]string{
				"127.0.0.1\tpilot.catalog.local",
				"127.0.0.1\tpilot.local",
			}, "\n"),
		},
		{
			name: ".localhost suffix filtered",
			cfg: &config.DevboxConfig{
				Services: map[string]config.ServiceConfig{
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts: map[string]string{
							"dev": "app.localhost",
							"web": "pilot.local",
						},
					},
				},
			},
			spec: &config.AutoHostsSpec{
				Include: []string{"app"},
				IP:      "127.0.0.1",
			},
			// app.localhost is auto-resolved by browsers/resolvers; no /etc/hosts entry needed.
			wantOut: "127.0.0.1\tpilot.local",
		},
		{
			name: "localhost filtered",
			cfg: &config.DevboxConfig{
				Services: map[string]config.ServiceConfig{
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts: map[string]string{
							"web":  "pilot.local",
							"self": "localhost",
						},
					},
				},
			},
			spec: &config.AutoHostsSpec{
				Include: []string{"app"},
				IP:      "127.0.0.1",
			},
			wantOut: "127.0.0.1\tpilot.local",
		},
		{
			name: "hide works",
			cfg: &config.DevboxConfig{
				Services: map[string]config.ServiceConfig{
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts: map[string]string{
							"web": "pilot.local",
						},
					},
					"catalog": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts: map[string]string{
							"web": "pilot.catalog.local",
						},
					},
				},
			},
			spec: &config.AutoHostsSpec{
				Include: []string{"app"},
				IP:      "127.0.0.1",
				Hide:    []string{"catalog"},
			},
			wantOut: "127.0.0.1\tpilot.local",
		},
		{
			name: "deploy order preserved",
			cfg: &config.DevboxConfig{
				Services: map[string]config.ServiceConfig{
					"catalog": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts: map[string]string{
							"web": "pilot.catalog.local",
						},
					},
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts: map[string]string{
							"web": "pilot.local",
						},
						DependsOn: []string{"catalog"},
					},
				},
			},
			spec: &config.AutoHostsSpec{
				Include: []string{"app"},
				IP:      "127.0.0.1",
			},
			// catalog should come first in deployment order
			wantOut: strings.Join([]string{
				"127.0.0.1\tpilot.catalog.local",
				"127.0.0.1\tpilot.local",
			}, "\n"),
		},
		{
			name: "custom IP applied",
			cfg: &config.DevboxConfig{
				Services: map[string]config.ServiceConfig{
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts: map[string]string{
							"web": "pilot.local",
						},
					},
				},
			},
			spec: &config.AutoHostsSpec{
				Include: []string{"app"},
				IP:      "192.168.1.100",
			},
			wantOut: "192.168.1.100\tpilot.local",
		},
		{
			name: "empty result returns empty string",
			cfg: &config.DevboxConfig{
				Services: map[string]config.ServiceConfig{
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts:   map[string]string{},
					},
				},
			},
			spec: &config.AutoHostsSpec{
				Include: []string{"app"},
				IP:      "127.0.0.1",
			},
			wantOut: "",
		},
		{
			name: "disabled service skipped",
			cfg: &config.DevboxConfig{
				Services: map[string]config.ServiceConfig{
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: false,
						Hosts: map[string]string{
							"web": "pilot.local",
						},
					},
				},
			},
			spec: &config.AutoHostsSpec{
				Include: []string{"app"},
				IP:      "127.0.0.1",
			},
			wantOut: "",
		},
		{
			name: "default include when empty",
			cfg: &config.DevboxConfig{
				Services: map[string]config.ServiceConfig{
					"app1": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts: map[string]string{
							"web": "app1.local",
						},
					},
					"tool1": {
						Type:    config.ServiceTypeTool,
						Enabled: true,
						Hosts: map[string]string{
							"web": "tool1.local",
						},
					},
					"infra1": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Hosts: map[string]string{
							"web": "infra1.local",
						},
					},
				},
			},
			spec: &config.AutoHostsSpec{
				// Include empty, should default to ["app", "tool", "infra"]
				IP: "127.0.0.1",
			},
			wantOut: strings.Join([]string{
				"127.0.0.1\tapp1.local",
				"127.0.0.1\ttool1.local",
				"127.0.0.1\tinfra1.local",
			}, "\n"),
		},
		{
			name: "default IP when empty",
			cfg: &config.DevboxConfig{
				Services: map[string]config.ServiceConfig{
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts: map[string]string{
							"web": "pilot.local",
						},
					},
				},
			},
			spec: &config.AutoHostsSpec{
				Include: []string{"app"},
				// IP empty, should default to 127.0.0.1
			},
			wantOut: "127.0.0.1\tpilot.local",
		},
		{
			name: "nil cfg returns empty string",
			cfg:  nil,
			spec: &config.AutoHostsSpec{
				Include: []string{"app"},
				IP:      "127.0.0.1",
			},
			wantOut: "",
		},
		{
			name: "nil spec returns empty string",
			cfg: &config.DevboxConfig{
				Services: map[string]config.ServiceConfig{
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts: map[string]string{
							"web": "pilot.local",
						},
					},
				},
			},
			spec:    nil,
			wantOut: "",
		},
		{
			name: "empty cfg.Services returns empty string",
			cfg: &config.DevboxConfig{
				Services: map[string]config.ServiceConfig{},
			},
			spec: &config.AutoHostsSpec{
				Include: []string{"app"},
				IP:      "127.0.0.1",
			},
			wantOut: "",
		},
		{
			name: "first-seen order preserved on dedup",
			cfg: &config.DevboxConfig{
				Services: map[string]config.ServiceConfig{
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts: map[string]string{
							"web": "pilot.local",
							"alt": "pilot-alt.local",
						},
					},
					"catalog": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts: map[string]string{
							"web":     "pilot.catalog.local",
							"dup_alt": "pilot-alt.local", // duplicate
						},
					},
				},
			},
			spec: &config.AutoHostsSpec{
				Include: []string{"app"},
				IP:      "127.0.0.1",
			},
			// Services are sorted alphabetically: catalog < main
			// catalog hosts: pilot.catalog.local, pilot-alt.local (dup)
			// main hosts: pilot-alt.local (already seen), pilot.local
			wantOut: strings.Join([]string{
				"127.0.0.1\tpilot-alt.local",
				"127.0.0.1\tpilot.catalog.local",
				"127.0.0.1\tpilot.local",
			}, "\n"),
		},
		{
			name: "empty strings dropped",
			cfg: &config.DevboxConfig{
				Services: map[string]config.ServiceConfig{
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts: map[string]string{
							"web":   "pilot.local",
							"empty": "",
						},
					},
				},
			},
			spec: &config.AutoHostsSpec{
				Include: []string{"app"},
				IP:      "127.0.0.1",
			},
			wantOut: "127.0.0.1\tpilot.local",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := renderAutoHosts(tt.cfg, tt.spec)
			if got != tt.wantOut {
				t.Errorf("renderAutoHosts() = %q, want %q", got, tt.wantOut)
			}
		})
	}
}
