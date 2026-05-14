package templates

import (
	"testing"

	"github.com/stretchr/testify/require"

	"devbox-cli/internal/config"
	"devbox-cli/internal/validate"
)

func TestIDEValidator(t *testing.T) {
	tests := []struct {
		name      string
		buildCtx  func() validate.Context
		checkDiag func(*testing.T, []validate.Diagnostic)
	}{
		{
			name: "nil_config",
			buildCtx: func() validate.Context {
				return validate.Context{
					ProjectRoot: t.TempDir(),
					Cfg:         nil,
				}
			},
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				require.Len(t, diags, 1)
				require.Equal(t, validate.SeverityInfo, diags[0].Severity)
				require.Equal(t, "templates.ide", diags[0].Target)
			},
		},
		{
			name: "no_services",
			buildCtx: func() validate.Context {
				return validate.Context{
					ProjectRoot: t.TempDir(),
					Cfg: &config.DevboxConfig{
						Services: make(map[string]config.ServiceConfig),
					},
				}
			},
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				require.Len(t, diags, 1)
				require.Equal(t, validate.SeverityOK, diags[0].Severity)
				require.Equal(t, "templates.ide", diags[0].Target)
			},
		},
		{
			name: "disabled_service",
			buildCtx: func() validate.Context {
				return validate.Context{
					ProjectRoot: t.TempDir(),
					Cfg: &config.DevboxConfig{
						Services: map[string]config.ServiceConfig{
							"main": {
								Enabled: false,
								Dir:     "services/main",
							},
						},
					},
				}
			},
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				require.Len(t, diags, 1)
				require.Equal(t, validate.SeverityOK, diags[0].Severity)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &IDEValidator{}
			ctx := tt.buildCtx()
			diags := v.Run(ctx)
			tt.checkDiag(t, diags)
		})
	}
}

func TestAIValidator(t *testing.T) {
	tests := []struct {
		name      string
		buildCtx  func() validate.Context
		checkDiag func(*testing.T, []validate.Diagnostic)
	}{
		{
			name: "nil_config",
			buildCtx: func() validate.Context {
				return validate.Context{
					ProjectRoot: t.TempDir(),
					Cfg:         nil,
				}
			},
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				require.Len(t, diags, 1)
				require.Equal(t, validate.SeverityInfo, diags[0].Severity)
				require.Equal(t, "templates.ai", diags[0].Target)
			},
		},
		{
			name: "no_services",
			buildCtx: func() validate.Context {
				return validate.Context{
					ProjectRoot: t.TempDir(),
					Cfg: &config.DevboxConfig{
						Services: make(map[string]config.ServiceConfig),
					},
				}
			},
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				require.Len(t, diags, 1)
				require.Equal(t, validate.SeverityOK, diags[0].Severity)
				require.Equal(t, "templates.ai", diags[0].Target)
			},
		},
		{
			name: "disabled_service",
			buildCtx: func() validate.Context {
				return validate.Context{
					ProjectRoot: t.TempDir(),
					Cfg: &config.DevboxConfig{
						Services: map[string]config.ServiceConfig{
							"main": {
								Enabled: false,
								Dir:     "services/main",
							},
						},
					},
				}
			},
			checkDiag: func(t *testing.T, diags []validate.Diagnostic) {
				require.Len(t, diags, 1)
				require.Equal(t, validate.SeverityOK, diags[0].Severity)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &AIValidator{}
			ctx := tt.buildCtx()
			diags := v.Run(ctx)
			tt.checkDiag(t, diags)
		})
	}
}

func TestTemplateValidatorIDs(t *testing.T) {
	ide := &IDEValidator{}
	require.Equal(t, "ide", ide.ID())
	require.Equal(t, "templates", ide.Domain())

	ai := &AIValidator{}
	require.Equal(t, "ai", ai.ID())
	require.Equal(t, "templates", ai.Domain())
}

func TestAllFunction(t *testing.T) {
	validators := All()
	require.Len(t, validators, 2)

	// Check that both validators are present
	ids := make(map[string]bool)
	for _, v := range validators {
		ids[v.ID()] = true
	}
	require.True(t, ids["ide"], "IDE validator should be present")
	require.True(t, ids["ai"], "AI validator should be present")
}
