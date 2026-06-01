package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// writeServiceYML creates devbox/services/<name>/service.yml with the given content.
func writeServiceYML(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, "devbox", "services", name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "service.yml"), []byte(content), 0o644))
}

// writeDeployYML creates devbox/services/<name>/deploy.yml.
func writeDeployYML(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, "devbox", "services", name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deploy.yml"), []byte("phases: []\n"), 0o644))
}

func runHooksValidator(t *testing.T, root string, cfg *config.DevboxConfig, reg *usercommands.Registry) []validate.Diagnostic {
	t.Helper()
	ctx := validate.Context{
		ProjectRoot:     root,
		Cfg:             cfg,
		CommandRegistry: reg,
	}
	return (&serviceHooksValidator{}).Run(ctx)
}

func TestServiceHooksValidator(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(root string)
		cfg        func(root string) *config.DevboxConfig
		reg        func() *usercommands.Registry
		wantErrors int
		wantWarns  int
		wantMsgHas []string
		wantNoMsg  []string
	}{
		{
			name:  "no services — no diagnostics",
			setup: func(root string) {},
			cfg:   func(root string) *config.DevboxConfig { return nil },
			reg:   usercommands.NewEmptyRegistry,
		},
		{
			name: "service without hooks — no diagnostics",
			setup: func(root string) {
				writeServiceYML(t, root, "redis", "type: tool\ncontainer: redis\n")
			},
			cfg: func(root string) *config.DevboxConfig { return nil },
			reg: usercommands.NewEmptyRegistry,
		},
		{
			name: "on_disable.requires deploy — error",
			cfg: func(root string) *config.DevboxConfig {
				return &config.DevboxConfig{
					Services: map[string]config.ServiceConfig{
						"myapp": {
							Type:      config.ServiceTypeApp,
							Container: "myapp",
							OnDisable: &config.ServiceToggleHooks{Requires: config.RequiresDeploy},
						},
					},
				}
			},
			setup: func(root string) {
				writeServiceYML(t, root, "myapp", "type: app\ncontainer: myapp\n")
			},
			reg:        usercommands.NewEmptyRegistry,
			wantErrors: 1,
			wantMsgHas: []string{"on_disable", "deploy", "not allowed"},
		},
		{
			name: "on_enable.requires unknown value — error",
			cfg: func(root string) *config.DevboxConfig {
				return &config.DevboxConfig{
					Services: map[string]config.ServiceConfig{
						"myapp": {
							Type:      config.ServiceTypeApp,
							Container: "myapp",
							OnEnable:  &config.ServiceToggleHooks{Requires: "rstart"},
						},
					},
				}
			},
			setup: func(root string) {
				writeServiceYML(t, root, "myapp", "type: app\ncontainer: myapp\n")
			},
			reg:        usercommands.NewEmptyRegistry,
			wantErrors: 1,
			wantMsgHas: []string{"unknown value", "rstart"},
		},
		{
			name: "on_disable.requires unknown value — deploy omitted from valid list",
			cfg: func(root string) *config.DevboxConfig {
				return &config.DevboxConfig{
					Services: map[string]config.ServiceConfig{
						"myapp": {
							Type:      config.ServiceTypeApp,
							Container: "myapp",
							OnDisable: &config.ServiceToggleHooks{Requires: "rstart"},
						},
					},
				}
			},
			setup: func(root string) {
				writeServiceYML(t, root, "myapp", "type: app\ncontainer: myapp\n")
			},
			reg:        usercommands.NewEmptyRegistry,
			wantErrors: 1,
			wantMsgHas: []string{"unknown value", "rstart", "none, restart"},
			wantNoMsg:  []string{"deploy"},
		},
		{
			name: "on_enable.requires deploy — no deploy.yml — error",
			cfg: func(root string) *config.DevboxConfig {
				return &config.DevboxConfig{
					Services: map[string]config.ServiceConfig{
						"myapp": {
							Type:      config.ServiceTypeApp,
							Container: "myapp",
							OnEnable:  &config.ServiceToggleHooks{Requires: config.RequiresDeploy},
						},
					},
				}
			},
			setup: func(root string) {
				writeServiceYML(t, root, "myapp", "type: app\ncontainer: myapp\n")
				// no deploy.yml
			},
			reg:        usercommands.NewEmptyRegistry,
			wantErrors: 1,
			wantMsgHas: []string{"myapp", "on_enable.requires: deploy", "no deploy.yml"},
		},
		{
			name: "on_enable.requires deploy — deploy.yml present — no error",
			cfg: func(root string) *config.DevboxConfig {
				return &config.DevboxConfig{
					Services: map[string]config.ServiceConfig{
						"myapp": {
							Type:      config.ServiceTypeApp,
							Container: "myapp",
							OnEnable:  &config.ServiceToggleHooks{Requires: config.RequiresDeploy},
						},
					},
				}
			},
			setup: func(root string) {
				writeServiceYML(t, root, "myapp", "type: app\ncontainer: myapp\n")
				writeDeployYML(t, root, "myapp")
			},
			reg:        usercommands.NewEmptyRegistry,
			wantErrors: 0,
		},
		{
			name: "before references unknown command — error",
			cfg: func(root string) *config.DevboxConfig {
				return &config.DevboxConfig{
					Services: map[string]config.ServiceConfig{
						"myapp": {
							Type:      config.ServiceTypeApp,
							Container: "myapp",
							OnEnable:  &config.ServiceToggleHooks{Before: []string{"does-not-exist"}},
						},
					},
				}
			},
			setup: func(root string) {
				writeServiceYML(t, root, "myapp", "type: app\ncontainer: myapp\n")
			},
			reg:        usercommands.NewEmptyRegistry,
			wantErrors: 1,
			wantMsgHas: []string{"unknown command", "does-not-exist"},
		},
		{
			name: "after references command with unsupported type — error",
			cfg: func(root string) *config.DevboxConfig {
				return &config.DevboxConfig{
					Services: map[string]config.ServiceConfig{
						"myapp": {
							Type:      config.ServiceTypeApp,
							Container: "myapp",
							OnEnable:  &config.ServiceToggleHooks{After: []string{"my:workflow"}},
						},
					},
				}
			},
			setup: func(root string) {
				writeServiceYML(t, root, "myapp", "type: app\ncontainer: myapp\n")
			},
			reg: func() *usercommands.Registry {
				reg := usercommands.NewEmptyRegistry()
				reg.AddCommandForTest(&usercommands.CommandDef{
					ID:   "my:workflow",
					Type: usercommands.CommandTypeWorkflow,
				})
				return reg
			},
			wantErrors: 1,
			wantMsgHas: []string{"my:workflow", "workflow", "only shell/script"},
		},
		{
			name: "before references shell command — no error",
			cfg: func(root string) *config.DevboxConfig {
				return &config.DevboxConfig{
					Services: map[string]config.ServiceConfig{
						"myapp": {
							Type:      config.ServiceTypeApp,
							Container: "myapp",
							OnEnable:  &config.ServiceToggleHooks{Before: []string{"my:setup"}},
						},
					},
				}
			},
			setup: func(root string) {
				writeServiceYML(t, root, "myapp", "type: app\ncontainer: myapp\n")
			},
			reg: func() *usercommands.Registry {
				reg := usercommands.NewEmptyRegistry()
				reg.AddCommandForTest(&usercommands.CommandDef{
					ID:   "my:setup",
					Type: usercommands.CommandTypeShell,
				})
				return reg
			},
			wantErrors: 0,
		},
		{
			name: "after references script command — no error",
			cfg: func(root string) *config.DevboxConfig {
				return &config.DevboxConfig{
					Services: map[string]config.ServiceConfig{
						"myapp": {
							Type:      config.ServiceTypeApp,
							Container: "myapp",
							OnDisable: &config.ServiceToggleHooks{After: []string{"my:teardown"}},
						},
					},
				}
			},
			setup: func(root string) {
				writeServiceYML(t, root, "myapp", "type: app\ncontainer: myapp\n")
			},
			reg: func() *usercommands.Registry {
				reg := usercommands.NewEmptyRegistry()
				reg.AddCommandForTest(&usercommands.CommandDef{
					ID:   "my:teardown",
					Type: usercommands.CommandTypeScript,
				})
				return reg
			},
			wantErrors: 0,
		},
		{
			name: "mandatory service with on_enable hook — warning",
			cfg: func(root string) *config.DevboxConfig {
				return &config.DevboxConfig{
					Services: map[string]config.ServiceConfig{
						"mandatory-svc": {
							Type:      config.ServiceTypeApp,
							Container: "mandatory-svc",
							Required:  true,
							OnEnable:  &config.ServiceToggleHooks{Requires: config.RequiresRestart},
						},
					},
				}
			},
			setup: func(root string) {
				writeServiceYML(t, root, "mandatory-svc", "type: app\ncontainer: mandatory-svc\nrequired: true\n")
			},
			reg:        usercommands.NewEmptyRegistry,
			wantWarns:  1,
			wantMsgHas: []string{"mandatory", "will never fire"},
		},
		{
			name: "mandatory service with on_disable hook — warning",
			cfg: func(root string) *config.DevboxConfig {
				return &config.DevboxConfig{
					Services: map[string]config.ServiceConfig{
						"mandatory-svc": {
							Type:      config.ServiceTypeApp,
							Container: "mandatory-svc",
							Required:  true,
							OnDisable: &config.ServiceToggleHooks{Requires: config.RequiresRestart},
						},
					},
				}
			},
			setup: func(root string) {
				writeServiceYML(t, root, "mandatory-svc", "type: app\ncontainer: mandatory-svc\nrequired: true\n")
			},
			reg:        usercommands.NewEmptyRegistry,
			wantWarns:  1,
			wantMsgHas: []string{"mandatory", "will never fire"},
		},
		{
			name: "no registry — ref validation skipped — no error",
			cfg: func(root string) *config.DevboxConfig {
				return &config.DevboxConfig{
					Services: map[string]config.ServiceConfig{
						"myapp": {
							Type:      config.ServiceTypeApp,
							Container: "myapp",
							OnEnable:  &config.ServiceToggleHooks{Before: []string{"some:cmd"}},
						},
					},
				}
			},
			setup: func(root string) {
				writeServiceYML(t, root, "myapp", "type: app\ncontainer: myapp\n")
			},
			reg:        func() *usercommands.Registry { return nil },
			wantErrors: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "devbox"), 0o755))
			tt.setup(root)

			var cfg *config.DevboxConfig
			if tt.cfg != nil {
				cfg = tt.cfg(root)
			}
			var reg *usercommands.Registry
			if tt.reg != nil {
				reg = tt.reg()
			}

			diags := runHooksValidator(t, root, cfg, reg)

			var errCount, warnCount int
			for _, d := range diags {
				switch d.Severity {
				case validate.SeverityError:
					errCount++
				case validate.SeverityWarning:
					warnCount++
				}
			}
			require.Equal(t, tt.wantErrors, errCount, "error count mismatch: diags=%v", diags)
			require.Equal(t, tt.wantWarns, warnCount, "warning count mismatch: diags=%v", diags)

			for _, substr := range tt.wantMsgHas {
				found := false
				for _, d := range diags {
					if strings.Contains(d.Message, substr) || strings.Contains(d.Hint, substr) {
						found = true
						break
					}
				}
				require.True(t, found, "expected message containing %q in %v", substr, diags)
			}

			for _, substr := range tt.wantNoMsg {
				for _, d := range diags {
					require.NotContains(t, d.Message, substr, "unexpected %q in message %q", substr, d.Message)
				}
			}
		})
	}
}
