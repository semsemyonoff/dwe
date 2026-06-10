package bridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	devconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// appSvc returns a fully-valid bridged app service (the bridge is strictly
// opt-in, so the toggle is explicit); tests mutate single fields to isolate
// one diagnostic each.
func appSvc(mut func(*devconfig.ServiceConfig)) devconfig.ServiceConfig {
	svc := devconfig.ServiceConfig{
		Type:        devconfig.ServiceTypeApp,
		Container:   "app-main",
		Dir:         "./services/main",
		DirInternal: "/workspace",
		Bridge:      devconfig.ServiceBridgeConfig{Enabled: new(true)},
	}
	if mut != nil {
		mut(&svc)
	}
	return svc
}

func runValidator(t *testing.T, services map[string]devconfig.ServiceConfig) []validate.Diagnostic {
	t.Helper()
	ctx := validate.Context{
		ProjectRoot: t.TempDir(),
		Cfg:         &devconfig.DweConfig{Services: services},
	}
	return (&servicesValidator{}).Run(ctx)
}

func TestServicesValidator_Table(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		svc          devconfig.ServiceConfig
		wantSeverity validate.Severity
		wantMessage  string
		wantHint     string
	}{
		{
			name: "invalid on_unreachable enum",
			svc: appSvc(func(s *devconfig.ServiceConfig) {
				s.Bridge.OnUnreachable = "sometimes"
			}),
			wantSeverity: validate.SeverityError,
			wantMessage:  `bridge.on_unreachable: unknown value "sometimes"; valid: fail, warn`,
			wantHint:     "on_unreachable: fail",
		},
		{
			name: "invalid enum still flagged when bridge disabled",
			svc: appSvc(func(s *devconfig.ServiceConfig) {
				s.Bridge.Enabled = new(false)
				s.Bridge.OnUnreachable = "wran"
			}),
			wantSeverity: validate.SeverityError,
			wantMessage:  `unknown value "wran"`,
		},
		{
			name: "relative shim_path",
			svc: appSvc(func(s *devconfig.ServiceConfig) {
				s.Bridge.ShimPath = "usr/local/bin/dwe"
			}),
			wantSeverity: validate.SeverityError,
			wantMessage:  `bridge.shim_path: "usr/local/bin/dwe" must be an absolute container path`,
			wantHint:     "/usr/local/bin/dwe",
		},
		{
			name: "bridge enabled without container target",
			svc: appSvc(func(s *devconfig.ServiceConfig) {
				s.Container = ""
			}),
			wantSeverity: validate.SeverityWarning,
			wantMessage:  "no container target",
			wantHint:     "declare container:",
		},
		{
			name: "app without workspace mapping",
			svc: appSvc(func(s *devconfig.ServiceConfig) {
				s.Dir = ""
				s.DirInternal = ""
			}),
			wantSeverity: validate.SeverityWarning,
			wantMessage:  "no dir/dir_internal workspace mapping",
			wantHint:     "declare both dir and dir_internal",
		},
		{
			name: "app with half a workspace mapping",
			svc: appSvc(func(s *devconfig.ServiceConfig) {
				s.DirInternal = ""
			}),
			wantSeverity: validate.SeverityWarning,
			wantMessage:  "no dir/dir_internal workspace mapping",
		},
		{
			name: "tool explicitly bridged has non-app hint",
			svc: devconfig.ServiceConfig{
				Type:      devconfig.ServiceTypeTool,
				Container: "adminer",
				Bridge:    devconfig.ServiceBridgeConfig{Enabled: new(true)},
			},
			wantSeverity: validate.SeverityWarning,
			wantMessage:  "no dir/dir_internal workspace mapping",
			wantHint:     "set bridge.enabled: false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			diags := runValidator(t, map[string]devconfig.ServiceConfig{"svc": tt.svc})
			require.Len(t, diags, 1, "diag dump: %+v", diags)

			d := diags[0]
			assert.Equal(t, tt.wantSeverity, d.Severity)
			assert.Equal(t, "bridge", d.Domain)
			assert.Equal(t, "bridge.services:svc", d.Target)
			assert.Equal(t, filepath.Join("workspace", "services", "svc", "service.yml"), d.File)
			assert.Contains(t, d.Message, `service "svc"`)
			assert.Contains(t, d.Message, tt.wantMessage)
			if tt.wantHint != "" {
				assert.Contains(t, d.Hint, tt.wantHint)
			}
		})
	}
}

func TestServicesValidator_NoDiagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		svc  devconfig.ServiceConfig
	}{
		{
			name: "valid bridged app",
			svc: appSvc(func(s *devconfig.ServiceConfig) {
				s.Bridge = devconfig.ServiceBridgeConfig{
					Enabled:       new(true),
					ShimPath:      "/opt/dwe/bin/dwe",
					OnUnreachable: devconfig.BridgeOnUnreachableWarn,
				}
			}),
		},
		{
			name: "app with bridge opted out and no mapping",
			svc: appSvc(func(s *devconfig.ServiceConfig) {
				s.Bridge.Enabled = new(false)
				s.Dir = ""
				s.DirInternal = ""
			}),
		},
		{
			name: "tool defaults to bridge off",
			svc:  devconfig.ServiceConfig{Type: devconfig.ServiceTypeTool, Container: "adminer"},
		},
		{
			name: "infra defaults to bridge off",
			svc:  devconfig.ServiceConfig{Type: devconfig.ServiceTypeInfra, Container: "db"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			diags := runValidator(t, map[string]devconfig.ServiceConfig{"svc": tt.svc})
			assert.Empty(t, diags, "diag dump: %+v", diags)
		})
	}
}

func TestServicesValidator_ContainerWarningSuppressesMappingWarning(t *testing.T) {
	t.Parallel()

	// No container target → the overlay skips the service entirely, so the
	// workspace-mapping warning would be noise on top.
	diags := runValidator(t, map[string]devconfig.ServiceConfig{
		"svc": appSvc(func(s *devconfig.ServiceConfig) {
			s.Container = ""
			s.Dir = ""
			s.DirInternal = ""
		}),
	})
	require.Len(t, diags, 1, "diag dump: %+v", diags)
	assert.Contains(t, diags[0].Message, "no container target")
}

func TestServicesValidator_MultipleServicesSortedAndIndependent(t *testing.T) {
	t.Parallel()

	diags := runValidator(t, map[string]devconfig.ServiceConfig{
		"zeta": appSvc(func(s *devconfig.ServiceConfig) {
			s.Bridge.OnUnreachable = "nope"
		}),
		"alpha": appSvc(func(s *devconfig.ServiceConfig) {
			s.Bridge.ShimPath = "relative/dwe"
		}),
		"good": appSvc(nil),
	})
	require.Len(t, diags, 2, "diag dump: %+v", diags)
	// Services iterate in sorted name order.
	assert.Equal(t, "bridge.services:alpha", diags[0].Target)
	assert.Equal(t, validate.SeverityError, diags[0].Severity)
	assert.Equal(t, "bridge.services:zeta", diags[1].Target)
	assert.Equal(t, validate.SeverityError, diags[1].Severity)
}

func TestServicesValidator_LoadsServicesFromDisk(t *testing.T) {
	t.Parallel()

	// nil Cfg → resolveServices falls back to LoadServices(projectRoot).
	root := t.TempDir()
	svcDir := filepath.Join(root, "workspace", "services", "web")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(
		"type: tool\nbridge:\n  enabled: true\n  on_unreachable: sometimes\n",
	), 0o644))

	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	require.Len(t, diags, 2, "diag dump: %+v", diags)

	assert.Equal(t, validate.SeverityError, diags[0].Severity)
	assert.Contains(t, diags[0].Message, `unknown value "sometimes"`)
	// Container defaulted to the folder name at load, so only the
	// workspace-mapping warning fires, never the container-target one.
	assert.Equal(t, validate.SeverityWarning, diags[1].Severity)
	assert.Contains(t, diags[1].Message, "no dir/dir_internal workspace mapping")
	for _, d := range diags {
		assert.Equal(t, "bridge.services:web", d.Target)
		assert.Equal(t, filepath.Join("workspace", "services", "web", "service.yml"), d.File)
	}
}

func TestServicesValidator_SkipsSilentlyOnLoadError(t *testing.T) {
	t.Parallel()

	// Strict decode rejects the unknown field → LoadServices errors → the
	// validator skips silently (the config domain owns the load error).
	root := t.TempDir()
	svcDir := filepath.Join(root, "workspace", "services", "web")
	require.NoError(t, os.MkdirAll(svcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(
		"type: app\nbogus_field: true\n",
	), 0o644))

	diags := (&servicesValidator{}).Run(validate.Context{ProjectRoot: root})
	assert.Empty(t, diags, "diag dump: %+v", diags)
}

func TestAll(t *testing.T) {
	t.Parallel()

	vs := All()
	require.Len(t, vs, 1)
	assert.Equal(t, "bridge", vs[0].Domain())
	assert.Equal(t, "services", vs[0].ID())
}
