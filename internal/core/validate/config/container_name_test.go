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

// TestContainerNameValidator_LastFileWins pins compose's `-f` merge semantics:
// container_name is a scalar field, so the last file declaring it is the only
// one that takes effect. Warning per file would flag a base value an overlay
// already corrected, and stay silent about an overlay that broke a correct base.
func TestContainerNameValidator_LastFileWins(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		base     string
		extra    string
		wantWarn string // "" means silent
	}{
		{
			name:  "overlay corrects a divergent base",
			base:  "services:\n  app:\n    image: busybox\n    container_name: myapp\n",
			extra: "services:\n  app:\n    container_name: dwe-shop-app\n",
		},
		{
			name:     "overlay diverges from a correct base",
			base:     "services:\n  app:\n    image: busybox\n    container_name: dwe-shop-app\n",
			extra:    "services:\n  app:\n    container_name: myapp\n",
			wantWarn: "myapp",
		},
		{
			name:     "overlay leaving container_name alone keeps the base finding",
			base:     "services:\n  app:\n    image: busybox\n    container_name: myapp\n",
			extra:    "services:\n  app:\n    environment:\n      FOO: bar\n",
			wantWarn: "myapp",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := writeComposeNameProjectExtra(t, tc.base, tc.extra)
			diags := runContainerNameValidator(t, root)
			if tc.wantWarn == "" {
				require.Empty(t, diags)
				return
			}
			require.Len(t, diags, 1)
			require.Contains(t, diags[0].Message, tc.wantWarn)
		})
	}
}

// TestContainerNameValidator_ResetClearsBaseFinding pins compose's `!reset`
// merge tag (Compose v2.24+): an overlay clearing container_name leaves the
// merged stack with none at all, so neither the base declaration nor the
// clearing overlay may warn. yaml.v3 leaves an unknown tag unresolved, so
// `!reset null` arrives as the raw scalar text "null" — decoding it straight
// into a string would report a container literally named "null".
func TestContainerNameValidator_ResetClearsBaseFinding(t *testing.T) {
	t.Parallel()
	base := "services:\n  app:\n    image: busybox\n    container_name: myapp\n"
	for _, extra := range []string{
		"services:\n  app:\n    container_name: !reset null\n",
		"services:\n  app:\n    container_name: !reset ''\n",
	} {
		root := writeComposeNameProjectExtra(t, base, extra)
		diags := runContainerNameValidator(t, root)
		require.Empty(t, diags, "extra=%q", extra)
	}
}

// TestContainerNameValidator_OverrideTagIsAPlainValue pins the other compose
// merge tag: `!override` replaces the merged value rather than clearing it, so
// the overlay's value is what takes effect and what gets compared.
func TestContainerNameValidator_OverrideTagIsAPlainValue(t *testing.T) {
	t.Parallel()
	root := writeComposeNameProjectExtra(t,
		"services:\n  app:\n    image: busybox\n    container_name: dwe-shop-app\n",
		"services:\n  app:\n    container_name: !override myapp\n",
	)
	diags := runContainerNameValidator(t, root)
	require.Len(t, diags, 1)
	require.Contains(t, diags[0].Message, "myapp")
}

func TestContainerNameValidator_NilCfgIsSilent(t *testing.T) {
	t.Parallel()
	diags := (&containerNameValidator{}).Run(validate.Context{ProjectRoot: t.TempDir()})
	require.Empty(t, diags)
}
