package ui

import (
	"strings"
	"testing"

	"devbox-cli/internal/config"
)

func TestRenderAutoURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     *config.DevboxConfig
		spec    *config.AutoURLsSpec
		wantOut string
	}{
		{
			name: "app behind proxy with multiple paths",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"http": 80},
						Hosts:   map[string]string{},
					},
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Icon:    "📦",
						Ports:   map[string]int{},
						Hosts:   map[string]string{"web": "pilot.local"},
						Info: config.ServiceInfoBlock{
							Title:   "Main",
							HostKey: "web",
							PortKey: "http",
							Paths: []config.ServiceInfoPath{
								{Name: "API specification", Path: "/api/docs", Icon: "📖"},
								{Name: "Clockwork", Path: "/__clockwork", Icon: ""},
								{Name: "SPX profiler", Path: "/?SPX_KEY=dev", Icon: "⚡"},
							},
						},
					},
				},
			},
			spec: &config.AutoURLsSpec{
				Include: []string{"app", "tool"},
			},
			wantOut: strings.Join([]string{
				"",
				"Main",
				"  📦 Main  — http://pilot.local",
				"     📖 API specification  — http://pilot.local/api/docs",
				"     🔗 Clockwork  — http://pilot.local/__clockwork",
				"     ⚡ SPX profiler  — http://pilot.local/?SPX_KEY=dev",
			}, "\n"),
		},
		{
			name: "app with no paths",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"http": 80},
					},
					"catalog": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Ports:   map[string]int{},
						Hosts:   map[string]string{"web": "pilot.catalog.local"},
						Info: config.ServiceInfoBlock{
							Title: "Catalog",
						},
					},
				},
			},
			spec: &config.AutoURLsSpec{
				Include: []string{"app"},
			},
			wantOut: strings.Join([]string{
				"",
				"Catalog",
				"  📦 Catalog  — http://pilot.catalog.local",
			}, "\n"),
		},
		{
			name: "tool with both hosts and ports - proxied and direct",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"http": 80},
					},
					"adminer": {
						Type:    config.ServiceTypeTool,
						Enabled: true,
						Icon:    "⚙️",
						Ports:   map[string]int{"http": 8027},
						Hosts:   map[string]string{"web": "pilot.db.local"},
						Info: config.ServiceInfoBlock{
							Title: "Adminer",
						},
					},
				},
			},
			spec: &config.AutoURLsSpec{
				Include: []string{"tool"},
			},
			wantOut: strings.Join([]string{
				"",
				"Adminer",
				"  ⚙️ Adminer  — http://pilot.db.local | http://localhost:8027",
			}, "\n"),
		},
		{
			name: "tool with only ports - localhost URL",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"redis": {
						Type:    config.ServiceTypeTool,
						Enabled: true,
						Icon:    "⚙️",
						Ports:   map[string]int{"http": 6379},
						Hosts:   map[string]string{},
						Info: config.ServiceInfoBlock{
							Title: "Redis",
						},
					},
				},
			},
			spec: &config.AutoURLsSpec{
				Include: []string{"tool"},
			},
			wantOut: strings.Join([]string{
				"",
				"Redis",
				"  ⚙️ Redis  — http://localhost:6379",
			}, "\n"),
		},
		{
			name: "service with neither host nor port and no paths - silently omitted",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Ports:   map[string]int{},
						Hosts:   map[string]string{},
					},
				},
			},
			spec: &config.AutoURLsSpec{
				Include: []string{"app"},
			},
			wantOut: "",
		},
		{
			name: "hide excludes services",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"http": 80},
					},
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Ports:   map[string]int{},
						Hosts:   map[string]string{"web": "pilot.local"},
						Info: config.ServiceInfoBlock{
							Title: "Main",
						},
					},
					"catalog": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Ports:   map[string]int{},
						Hosts:   map[string]string{"web": "pilot.catalog.local"},
						Info: config.ServiceInfoBlock{
							Title: "Catalog",
						},
					},
				},
			},
			spec: &config.AutoURLsSpec{
				Include: []string{"app"},
				Hide:    []string{"catalog"},
			},
			wantOut: strings.Join([]string{
				"",
				"Main",
				"  📦 Main  — http://pilot.local",
			}, "\n"),
		},
		{
			name: "hide_paths excludes individual paths",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"http": 80},
					},
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Ports:   map[string]int{},
						Hosts:   map[string]string{"web": "pilot.local"},
						Info: config.ServiceInfoBlock{
							Title: "Main",
							Paths: []config.ServiceInfoPath{
								{Name: "API specification", Path: "/api/docs"},
								{Name: "SPX profiler", Path: "/?SPX_KEY=dev"},
							},
						},
					},
				},
			},
			spec: &config.AutoURLsSpec{
				Include: []string{"app"},
				HidePaths: map[string][]string{
					"main": {"SPX profiler"},
				},
			},
			wantOut: strings.Join([]string{
				"",
				"Main",
				"  📦 Main  — http://pilot.local",
				"     🔗 API specification  — http://pilot.local/api/docs",
			}, "\n"),
		},
		{
			name: "explicit port_via",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"http": 8080, "admin": 9000},
					},
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Ports:   map[string]int{},
						Hosts:   map[string]string{"web": "pilot.local"},
						Info: config.ServiceInfoBlock{
							Title: "Main",
						},
					},
				},
			},
			spec: &config.AutoURLsSpec{
				Include: []string{"app"},
				PortVia: "nginx",
			},
			wantOut: strings.Join([]string{
				"",
				"Main",
				"  📦 Main  — http://pilot.local:8080",
			}, "\n"),
		},
		{
			name: "auto-detect port_via picks single infra with ports.http == 80",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"http": 80},
					},
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Ports:   map[string]int{},
						Hosts:   map[string]string{"web": "pilot.local"},
						Info: config.ServiceInfoBlock{
							Title: "Main",
						},
					},
				},
			},
			spec: &config.AutoURLsSpec{
				Include: []string{"app"},
				// PortVia empty, should auto-detect
			},
			wantOut: strings.Join([]string{
				"",
				"Main",
				"  📦 Main  — http://pilot.local",
			}, "\n"),
		},
		{
			name: "auto-detect port_via declines when extra infra with non-80 http",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"http": 80},
					},
					"opensearch": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"http": 9200},
					},
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Ports:   map[string]int{},
						Hosts:   map[string]string{"web": "pilot.local"},
						Info: config.ServiceInfoBlock{
							Title: "Main",
						},
					},
				},
			},
			spec: &config.AutoURLsSpec{
				Include: []string{"app"},
			},
			// Should still detect nginx and use it, opensearch is not a candidate
			wantOut: strings.Join([]string{
				"",
				"Main",
				"  📦 Main  — http://pilot.local",
			}, "\n"),
		},
		{
			name: "auto-detect port_via declines when 0 candidates",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"opensearch": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"http": 9200},
					},
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Ports:   map[string]int{},
						Hosts:   map[string]string{"web": "pilot.local"},
						Info: config.ServiceInfoBlock{
							Title: "Main",
						},
					},
				},
			},
			spec: &config.AutoURLsSpec{
				Include: []string{"app"},
			},
			// No port_via, and no host-based URL since portVia is nil
			wantOut: "",
		},
		{
			name: "service with custom host_key and port_key",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"http": 80},
					},
					"minio": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Icon:    "⚙️",
						Ports:   map[string]int{"api": 9010, "console": 9011},
						Hosts:   map[string]string{"s3": "s3.local", "console": "minio.local"},
						Info: config.ServiceInfoBlock{
							Title:   "MinIO Console",
							HostKey: "console",
							PortKey: "console",
						},
					},
				},
			},
			spec: &config.AutoURLsSpec{
				Include: []string{"infra"},
				Hide:    []string{"nginx"},
			},
			wantOut: strings.Join([]string{
				"",
				"MinIO Console",
				"  ⚙️ MinIO Console  — http://minio.local | http://localhost:9011",
			}, "\n"),
		},
		{
			name: "disabled service is skipped",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"http": 80},
					},
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: false,
						Ports:   map[string]int{},
						Hosts:   map[string]string{"web": "pilot.local"},
						Info: config.ServiceInfoBlock{
							Title: "Main",
						},
					},
				},
			},
			spec: &config.AutoURLsSpec{
				Include: []string{"app"},
			},
			wantOut: "",
		},
		{
			name: "host-only with paths and no portVia - paths suppressed, service omitted",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts:   map[string]string{"web": "pilot.local"},
						Ports:   map[string]int{},
						Info: config.ServiceInfoBlock{
							Title: "Main",
							Paths: []config.ServiceInfoPath{
								{Name: "Docs", Path: "/docs"},
							},
						},
					},
				},
			},
			spec:    &config.AutoURLsSpec{Include: []string{"app"}},
			wantOut: "",
		},
		{
			name: "auto-detect portVia with use_https true and proxy has only ports.http - host-only https URL",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: true},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"http": 80},
					},
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts:   map[string]string{"web": "pilot.local"},
						Ports:   map[string]int{},
						Info: config.ServiceInfoBlock{
							Title: "Main",
						},
					},
				},
			},
			spec: &config.AutoURLsSpec{Include: []string{"app"}},
			// use_https=true but proxy only has ports.http; re-selecting ports.https → 0 → host-only
			wantOut: strings.Join([]string{
				"",
				"Main",
				"  📦 Main  — https://pilot.local",
			}, "\n"),
		},
		{
			name: "auto-detect portVia with use_https false and proxy has only ports.https - host-only http URL",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"https": 443},
					},
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Hosts:   map[string]string{"web": "pilot.local"},
						Ports:   map[string]int{},
						Info: config.ServiceInfoBlock{
							Title: "Main",
						},
					},
				},
			},
			spec: &config.AutoURLsSpec{Include: []string{"app"}},
			// use_https=false but proxy only has ports.https; re-selecting ports.http → 0 → host-only
			wantOut: strings.Join([]string{
				"",
				"Main",
				"  📦 Main  — http://pilot.local",
			}, "\n"),
		},
		{
			name: "service without info block renders main URL using defaults",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"http": 80},
					},
					"myapp": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Ports:   map[string]int{},
						Hosts:   map[string]string{"web": "myapp.local"},
						// No Info block at all
					},
				},
			},
			spec: &config.AutoURLsSpec{
				Include: []string{"app"},
			},
			wantOut: strings.Join([]string{
				"",
				"Myapp",
				"  📦 Myapp  — http://myapp.local",
			}, "\n"),
		},
		{
			name:    "nil cfg returns empty string",
			cfg:     nil,
			spec:    &config.AutoURLsSpec{},
			wantOut: "",
		},
		{
			name: "nil spec returns empty string",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Ports:   map[string]int{},
						Hosts:   map[string]string{"web": "pilot.local"},
					},
				},
			},
			spec:    nil,
			wantOut: "",
		},
		{
			name: "empty cfg.Services returns empty string",
			cfg: &config.DevboxConfig{
				Runtime:  config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{},
			},
			spec: &config.AutoURLsSpec{
				Include: []string{"app"},
			},
			wantOut: "",
		},
		{
			name: "default include when empty",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: false},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"http": 80},
					},
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Ports:   map[string]int{},
						Hosts:   map[string]string{"web": "pilot.local"},
						Info: config.ServiceInfoBlock{
							Title: "Main",
						},
					},
					"tool1": {
						Type:    config.ServiceTypeTool,
						Enabled: true,
						Ports:   map[string]int{"http": 9000},
						Hosts:   map[string]string{},
						Info: config.ServiceInfoBlock{
							Title: "Tool1",
						},
					},
					"infra1": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"http": 5000},
						Hosts:   map[string]string{},
						Info: config.ServiceInfoBlock{
							Title: "Infra1",
						},
					},
				},
			},
			spec: &config.AutoURLsSpec{
				// Include empty, should default to ["app", "tool"]
			},
			wantOut: strings.Join([]string{
				"",
				"Main",
				"  📦 Main  — http://pilot.local",
				"",
				"Tool1",
				"  ⚙️ Tool1  — http://localhost:9000",
			}, "\n"),
		},
		{
			name: "https scheme applied correctly",
			cfg: &config.DevboxConfig{
				Runtime: config.RuntimeConfig{UseHTTPS: true},
				Services: map[string]config.ServiceConfig{
					"nginx": {
						Type:    config.ServiceTypeInfra,
						Enabled: true,
						Ports:   map[string]int{"https": 443},
					},
					"main": {
						Type:    config.ServiceTypeApp,
						Enabled: true,
						Ports:   map[string]int{},
						Hosts:   map[string]string{"web": "pilot.local"},
						Info: config.ServiceInfoBlock{
							Title: "Main",
						},
					},
				},
			},
			spec: &config.AutoURLsSpec{
				Include: []string{"app"},
			},
			wantOut: strings.Join([]string{
				"",
				"Main",
				"  📦 Main  — https://pilot.local",
			}, "\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := renderAutoURLs(tt.cfg, tt.spec)
			if got != tt.wantOut {
				t.Errorf("renderAutoURLs() = %q, want %q", got, tt.wantOut)
			}
		})
	}
}
