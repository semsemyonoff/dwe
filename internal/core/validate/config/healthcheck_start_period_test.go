package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	devconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func TestHealthcheckStartPeriodValidator(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		compose string
		want    []string
	}{
		{
			name:    "healthcheck without start_period is noted",
			compose: "services:\n  db:\n    image: postgres:16\n    healthcheck:\n      test: [\"CMD\", \"pg_isready\"]\n",
			want:    []string{"compose service db has a healthcheck without start_period"},
		},
		{
			name:    "start_period present is silent",
			compose: "services:\n  db:\n    healthcheck:\n      test: [\"CMD\", \"pg_isready\"]\n      start_period: 60s\n",
		},
		{
			name:    "disabled healthcheck is silent",
			compose: "services:\n  db:\n    healthcheck:\n      disable: true\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := writeComposeNameProject(t, composeNameWorkspaceYML, tt.compose, "")
			cfg, err := devconfig.LoadConfig(filepath.Join(root, "workspace.yml"))
			require.NoError(t, err)
			diags := (&healthcheckStartPeriodValidator{}).Run(validate.Context{ProjectRoot: root, Cfg: cfg})

			require.Len(t, diags, len(tt.want))
			for i, want := range tt.want {
				require.Equal(t, validate.SeverityInfo, diags[i].Severity)
				require.Equal(t, "config.healthcheck_start_period", diags[i].Target)
				require.Equal(t, "docker-compose.yml", diags[i].File)
				require.Equal(t, want, diags[i].Message)
			}
		})
	}
}

// TestHealthcheckStartPeriodValidator_DisabledServiceOverlay pins that only the
// active chain is scanned: a disabled service's overlay never reaches
// `docker compose up --wait`.
func TestHealthcheckStartPeriodValidator_DisabledServiceOverlay(t *testing.T) {
	t.Parallel()
	root := writeComposeNameProject(t, composeNameWorkspaceYML, "services: {}\n", "")
	svcDir := filepath.Join(root, "workspace", "services", "adminer")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte("type: tool\ncompose: [adminer.yml]\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "adminer.yml"),
		[]byte("services:\n  adminer:\n    healthcheck:\n      test: [\"CMD\", \"true\"]\n"), 0o644))

	cfg, err := devconfig.LoadConfig(filepath.Join(root, "workspace.yml"))
	require.NoError(t, err)
	require.False(t, cfg.Services["adminer"].Enabled)
	require.Empty(t, (&healthcheckStartPeriodValidator{}).Run(validate.Context{ProjectRoot: root, Cfg: cfg}))
}
