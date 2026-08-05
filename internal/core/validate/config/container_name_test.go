package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	devconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func runContainerNameValidator(t *testing.T, root string) []validate.Diagnostic {
	t.Helper()
	cfg, err := devconfig.LoadConfig(filepath.Join(root, "workspace.yml"))
	require.NoError(t, err)
	return (&containerNameValidator{}).Run(validate.Context{
		ProjectRoot: root,
		ConfigPath:  filepath.Join(root, "workspace.yml"),
		Cfg:         cfg,
	})
}

// TestContainerNameValidator covers the divergence check: the resolved
// compose project name is always "dwe-shop" (composeNameWorkspaceYML:
// project.name=shop, prefix=dwe), so the derived name for service "app" is
// "dwe-shop-app".
func TestContainerNameValidator(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		compose    string
		wantWarn   bool
		wantMsgHas []string
	}{
		{
			name:       "divergent name warns",
			compose:    "services:\n  app:\n    image: busybox\n    container_name: myapp\n",
			wantWarn:   true,
			wantMsgHas: []string{"myapp", "dwe-shop-app"},
		},
		{
			// The defect is divergence, not casing: a lowercase name that
			// still does not match <project>-<service> is flagged too.
			name:       "lowercase but still divergent warns",
			compose:    "services:\n  app:\n    image: busybox\n    container_name: app\n",
			wantWarn:   true,
			wantMsgHas: []string{"\"app\"", "dwe-shop-app"},
		},
		{
			name:    "derived-matching name is silent",
			compose: "services:\n  app:\n    image: busybox\n    container_name: dwe-shop-app\n",
		},
		{
			name:    "no container_name is silent",
			compose: "services:\n  app:\n    image: busybox\n",
		},
		{
			name:    "interpolated container_name is silent",
			compose: "services:\n  app:\n    image: busybox\n    container_name: ${MY_CONTAINER}\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := writeComposeNameProject(t, composeNameWorkspaceYML, tc.compose, "")
			diags := runContainerNameValidator(t, root)
			if !tc.wantWarn {
				require.Empty(t, diags)
				return
			}
			d := hasDiag(t, diags, validate.SeverityWarning, "diverges from the name dwe derives")
			require.Equal(t, "config.container_name", d.Target)
			require.Equal(t, "docker-compose.yml", d.File)
			for _, s := range tc.wantMsgHas {
				require.Contains(t, d.Message, s)
			}
		})
	}
}

func TestContainerNameValidator_NilCfgIsSilent(t *testing.T) {
	t.Parallel()
	diags := (&containerNameValidator{}).Run(validate.Context{ProjectRoot: t.TempDir()})
	require.Empty(t, diags)
}
