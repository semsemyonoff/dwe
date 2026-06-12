package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func runGeneratedValidator(t *testing.T, root string, cfg *config.DweConfig) []validate.Diagnostic {
	t.Helper()
	return (&generatedValidator{}).Run(validate.Context{ProjectRoot: root, Cfg: cfg})
}

// countSeverity tallies diagnostics by severity.
func countSeverity(diags []validate.Diagnostic, sev validate.Severity) int {
	n := 0
	for _, d := range diags {
		if d.Severity == sev {
			n++
		}
	}
	return n
}

func cfgWithServices(svcs map[string]config.ServiceConfig) *config.DweConfig {
	return &config.DweConfig{Services: svcs}
}

func TestGeneratedValidator_Fields(t *testing.T) {
	tests := []struct {
		name       string
		generated  map[string]config.GeneratedField
		wantErrors int
		wantMsgHas string
	}{
		{
			name: "valid declaration — no diagnostics",
			generated: map[string]config.GeneratedField{
				"app_key": {File: "configs/.env", Pattern: `^APP_KEY=(.*)$`},
			},
			wantErrors: 0,
		},
		{
			name: "missing pattern",
			generated: map[string]config.GeneratedField{
				"app_key": {File: "configs/.env"},
			},
			wantErrors: 1,
			wantMsgHas: "pattern is required",
		},
		{
			name: "invalid regex",
			generated: map[string]config.GeneratedField{
				"app_key": {File: "configs/.env", Pattern: `^APP_KEY=(.*$`},
			},
			wantErrors: 1,
			wantMsgHas: "does not compile",
		},
		{
			name: "no capture group",
			generated: map[string]config.GeneratedField{
				"app_key": {File: "configs/.env", Pattern: `^APP_KEY=.*$`},
			},
			wantErrors: 1,
			wantMsgHas: "no capture group",
		},
		{
			name: "missing file",
			generated: map[string]config.GeneratedField{
				"app_key": {Pattern: `^APP_KEY=(.*)$`},
			},
			wantErrors: 1,
			wantMsgHas: "file is required",
		},
		{
			name: "file escapes service dir",
			generated: map[string]config.GeneratedField{
				"app_key": {File: "../../etc/passwd", Pattern: `^APP_KEY=(.*)$`},
			},
			wantErrors: 1,
			wantMsgHas: "must be a relative path",
		},
		{
			name: "absolute file path",
			generated: map[string]config.GeneratedField{
				"app_key": {File: "/etc/passwd", Pattern: `^APP_KEY=(.*)$`},
			},
			wantErrors: 1,
			wantMsgHas: "must be a relative path",
		},
		{
			name: "invalid field name",
			generated: map[string]config.GeneratedField{
				"app.key": {File: "configs/.env", Pattern: `^APP_KEY=(.*)$`},
			},
			wantErrors: 1,
			wantMsgHas: "invalid name",
		},
		{
			name: "multi-field — one bad one good",
			generated: map[string]config.GeneratedField{
				"app_key":   {File: "configs/.env", Pattern: `^APP_KEY=(.*)$`},
				"crypt_key": {File: "configs/env.php", Pattern: `crypt.*key`},
			},
			wantErrors: 1,
			wantMsgHas: "no capture group",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := cfgWithServices(map[string]config.ServiceConfig{
				"main": {Type: config.ServiceTypeApp, Dir: "services/main/src", Generated: tt.generated},
			})
			diags := runGeneratedValidator(t, root, cfg)
			require.Equal(t, tt.wantErrors, countSeverity(diags, validate.SeverityError),
				"diags: %+v", diags)
			if tt.wantMsgHas != "" {
				found := false
				for _, d := range diags {
					if strings.Contains(d.Message, tt.wantMsgHas) {
						found = true
					}
				}
				require.True(t, found, "no diagnostic contained %q; got %+v", tt.wantMsgHas, diags)
			}
		})
	}
}

func TestGeneratedValidator_NoServices(t *testing.T) {
	root := t.TempDir()
	require.Empty(t, runGeneratedValidator(t, root, nil))
	require.Empty(t, runGeneratedValidator(t, root, cfgWithServices(nil)))
}

func TestGeneratedValidator_RenderConfigTemplatePin(t *testing.T) {
	t.Run("unresolved pin warns", func(t *testing.T) {
		root := t.TempDir()
		tmpl := "laravel"
		cfg := cfgWithServices(map[string]config.ServiceConfig{
			"main": {
				Type:   config.ServiceTypeApp,
				Dir:    "services/main/src",
				Render: config.ServiceRenderConfig{Config: &config.RenderConfigSection{Template: tmpl}},
			},
		})
		diags := runGeneratedValidator(t, root, cfg)
		require.Equal(t, 1, countSeverity(diags, validate.SeverityWarning), "diags: %+v", diags)
		require.Equal(t, 0, countSeverity(diags, validate.SeverityError))
	})

	t.Run("resolved pin clean", func(t *testing.T) {
		root := t.TempDir()
		tmpl := "laravel"
		packDir := filepath.Join(root, "workspace", "templates", "config", tmpl)
		require.NoError(t, os.MkdirAll(packDir, 0o755))
		cfg := cfgWithServices(map[string]config.ServiceConfig{
			"main": {
				Type:   config.ServiceTypeApp,
				Dir:    "services/main/src",
				Render: config.ServiceRenderConfig{Config: &config.RenderConfigSection{Template: tmpl}},
			},
		})
		diags := runGeneratedValidator(t, root, cfg)
		require.Equal(t, 0, countSeverity(diags, validate.SeverityWarning), "diags: %+v", diags)
	})

	t.Run("no pin — no warning", func(t *testing.T) {
		root := t.TempDir()
		cfg := cfgWithServices(map[string]config.ServiceConfig{
			"main": {Type: config.ServiceTypeApp, Dir: "services/main/src"},
		})
		require.Empty(t, runGeneratedValidator(t, root, cfg))
	})
}

func TestGeneratedValidator_PredicateCrossCheck(t *testing.T) {
	mkBuiltin := func(cmd string) *condition.Condition {
		return &condition.Condition{Type: condition.TypeBuiltin, Cmd: cmd}
	}

	baseSvcs := func() map[string]config.ServiceConfig {
		return map[string]config.ServiceConfig{
			"main": {
				Type: config.ServiceTypeApp,
				Dir:  "services/main/src",
				Generated: map[string]config.GeneratedField{
					"app_key": {File: "configs/.env", Pattern: `^APP_KEY=(.*)$`},
				},
			},
		}
	}

	tests := []struct {
		name       string
		when       *condition.Condition
		wantErrors int
		wantMsgHas string
	}{
		{
			name:       "references declared field — clean",
			when:       mkBuiltin("generated-missing main app_key"),
			wantErrors: 0,
		},
		{
			name:       "unknown service",
			when:       mkBuiltin("generated-missing other app_key"),
			wantErrors: 1,
			wantMsgHas: "unknown service",
		},
		{
			name:       "undeclared field",
			when:       mkBuiltin("generated-missing main nope"),
			wantErrors: 1,
			wantMsgHas: "undeclared generated field",
		},
		{
			name:       "non-generated predicate ignored",
			when:       mkBuiltin("dir-empty services/main/src"),
			wantErrors: 0,
		},
		{
			name:       "bad arity left to runtime",
			when:       mkBuiltin("generated-missing main"),
			wantErrors: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := cfgWithServices(baseSvcs())
			cfg.Deploy = &config.ProjectDeployConfig{Phases: []config.DeployPhase{
				{Name: "p", Steps: []config.DeployStep{{Name: "s", When: tt.when}}},
			}}
			diags := runGeneratedValidator(t, root, cfg)
			require.Equal(t, tt.wantErrors, countSeverity(diags, validate.SeverityError), "diags: %+v", diags)
			if tt.wantMsgHas != "" {
				found := false
				for _, d := range diags {
					if strings.Contains(d.Message, tt.wantMsgHas) {
						found = true
					}
				}
				require.True(t, found, "no diagnostic contained %q; got %+v", tt.wantMsgHas, diags)
			}
		})
	}
}

func TestGeneratedValidator_PredicateInPhaseWhen(t *testing.T) {
	root := t.TempDir()
	cfg := cfgWithServices(map[string]config.ServiceConfig{
		"main": {Type: config.ServiceTypeApp, Dir: "services/main/src"},
	})
	cfg.Deploy = &config.ProjectDeployConfig{Phases: []config.DeployPhase{
		{
			Name: "p",
			When: &condition.Condition{Type: condition.TypeBuiltin, Cmd: "generated-missing main app_key"},
		},
	}}
	diags := runGeneratedValidator(t, root, cfg)
	require.Equal(t, 1, countSeverity(diags, validate.SeverityError), "diags: %+v", diags)
	require.Contains(t, diags[0].Message, "undeclared generated field")
}
