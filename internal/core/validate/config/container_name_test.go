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
		{
			// Compose lowercases the project name, so a container_name that
			// differs from the derived one only in casing IS a divergence:
			// docker never creates a container called "DWE-Shop-App".
			name:       "case-only divergence warns",
			compose:    "services:\n  app:\n    image: busybox\n    container_name: DWE-Shop-App\n",
			wantWarn:   true,
			wantMsgHas: []string{"DWE-Shop-App", "dwe-shop-app"},
		},
		{
			// Named volumes and networks are isolation findings of other kinds
			// in the same scan. Without the KindContainerName filter each one
			// would be reported as a service setting container_name: "".
			name: "named volumes and networks are not container_name findings",
			compose: "services:\n  app:\n    image: busybox\n    container_name: dwe-shop-app\n" +
				"volumes:\n  data:\n    name: my-vol\nnetworks:\n  back:\n    name: my-net\n",
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
			require.Len(t, diags, 1, "exactly one diagnostic per divergent service")
			d := hasDiag(t, diags, validate.SeverityWarning, "diverges from the conventional")
			require.Equal(t, "config.container_name", d.Target)
			require.Equal(t, "docker-compose.yml", d.File)
			for _, s := range tc.wantMsgHas {
				require.Contains(t, d.Message, s)
			}
			// The hint must not claim dwe's own commands break, and must not
			// offer "drop it" as an equivalent remedy — compose then names the
			// container <derived>-1, which is a different name again.
			require.Contains(t, d.Hint, "compose labels")
			require.Contains(t, d.Hint, "dwe-shop-app-1")
			require.NotContains(t, d.Hint, "compose's own naming applies")
		})
	}
}

func TestContainerNameValidator_NilCfgIsSilent(t *testing.T) {
	t.Parallel()
	diags := (&containerNameValidator{}).Run(validate.Context{ProjectRoot: t.TempDir()})
	require.Empty(t, diags)
}
