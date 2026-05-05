package builtin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/config"
)

func TestServiceConfigsCheckBuiltin_Validate(t *testing.T) {
	tests := []struct {
		name    string
		with    map[string]any
		wantErr bool
		errMsg  string
	}{
		{
			name:    "missing service param",
			with:    map[string]any{},
			wantErr: true,
			errMsg:  "missing required param 'service'",
		},
		{
			name:    "empty service param",
			with:    map[string]any{"service": ""},
			wantErr: true,
			errMsg:  "missing required param 'service'",
		},
		{
			name:    "valid service param",
			with:    map[string]any{"service": "main"},
			wantErr: false,
		},
	}

	builtin := serviceConfigsCheckBuiltin{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := builtin.Validate(tt.with)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil && tt.errMsg != "" {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %v, want to contain %q", err, tt.errMsg)
				}
			}
		})
	}
}

func TestServiceConfigsCheckBuiltin_Describe(t *testing.T) {
	builtin := serviceConfigsCheckBuiltin{}
	result := builtin.Describe(map[string]any{"service": "main"})
	if !strings.Contains(result, "service_configs_check") || !strings.Contains(result, "main") {
		t.Errorf("Describe() = %q, want to contain 'service_configs_check' and 'main'", result)
	}
}

func TestServiceConfigsCheckBuiltin_Run(t *testing.T) {
	tests := []struct {
		name       string
		service    string
		setup      func(t *testing.T, tmpDir string) *config.DevboxConfig
		wantErr    bool
		errContain string
	}{
		{
			name:    "service not found",
			service: "unknown",
			setup: func(t *testing.T, tmpDir string) *config.DevboxConfig {
				return &config.DevboxConfig{Services: map[string]config.ServiceConfig{}}
			},
			wantErr:    true,
			errContain: "not found in config",
		},
		{
			name:    "no configs declared",
			service: "main",
			setup: func(t *testing.T, tmpDir string) *config.DevboxConfig {
				return &config.DevboxConfig{
					Services: map[string]config.ServiceConfig{
						"main": {
							Dir:     "services/main",
							Configs: nil,
						},
					},
				}
			},
			wantErr: false,
		},
		{
			name:    "all configs exist",
			service: "main",
			setup: func(t *testing.T, tmpDir string) *config.DevboxConfig {
				svcDir := filepath.Join(tmpDir, "services", "main", "configs")
				if err := os.MkdirAll(svcDir, 0o755); err != nil {
					t.Fatalf("failed to create dir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(svcDir, "app.env"), []byte("KEY=value"), 0o644); err != nil {
					t.Fatalf("failed to write file: %v", err)
				}
				if err := os.WriteFile(filepath.Join(svcDir, "db.env"), []byte("DB_HOST=localhost"), 0o644); err != nil {
					t.Fatalf("failed to write file: %v", err)
				}

				return &config.DevboxConfig{
					Services: map[string]config.ServiceConfig{
						"main": {
							Dir: "services/main",
							Configs: []config.ServiceConfigEntry{
								{File: "app.env"},
								{File: "db.env"},
							},
						},
					},
				}
			},
			wantErr: false,
		},
		{
			name:    "some configs missing",
			service: "main",
			setup: func(t *testing.T, tmpDir string) *config.DevboxConfig {
				svcDir := filepath.Join(tmpDir, "services", "main", "configs")
				if err := os.MkdirAll(svcDir, 0o755); err != nil {
					t.Fatalf("failed to create dir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(svcDir, "app.env"), []byte("KEY=value"), 0o644); err != nil {
					t.Fatalf("failed to write file: %v", err)
				}

				return &config.DevboxConfig{
					Services: map[string]config.ServiceConfig{
						"main": {
							Dir: "services/main",
							Configs: []config.ServiceConfigEntry{
								{File: "app.env"},
								{File: "db.env"},
							},
						},
					},
				}
			},
			wantErr:    true,
			errContain: "missing config files",
		},
		{
			name:    "missing directory",
			service: "main",
			setup: func(t *testing.T, tmpDir string) *config.DevboxConfig {
				return &config.DevboxConfig{
					Services: map[string]config.ServiceConfig{
						"main": {
							Dir: "services/main",
							Configs: []config.ServiceConfigEntry{
								{File: "app.env"},
							},
						},
					},
				}
			},
			wantErr:    true,
			errContain: "missing config files",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cfg := tt.setup(t, tmpDir)

			builtin := serviceConfigsCheckBuiltin{}
			ctx := ExecContext{
				Config:      cfg,
				ProjectRoot: tmpDir,
				Output:      nil,
			}

			err := builtin.Run(map[string]any{"service": tt.service}, ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil && tt.errContain != "" {
				if !strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("Run() error = %v, want to contain %q", err, tt.errContain)
				}
			}
		})
	}
}
