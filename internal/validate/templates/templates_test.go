package templates

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"devbox-cli/internal/config"
	"devbox-cli/internal/validate"
)

func writeGitPack(t *testing.T, root, packName string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, "devbox", "templates", "git", packName, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
}

func writeFileAt(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func severityCount(diags []validate.Diagnostic, sev validate.Severity) int {
	n := 0
	for _, d := range diags {
		if d.Severity == sev {
			n++
		}
	}
	return n
}

func findDiag(diags []validate.Diagnostic, sev validate.Severity, target string) *validate.Diagnostic {
	for i := range diags {
		if diags[i].Severity == sev && diags[i].Target == target {
			return &diags[i]
		}
	}
	return nil
}

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

func TestTemplateValidatorsIgnoreNonAppServices(t *testing.T) {
	tr := true
	ctx := validate.Context{
		ProjectRoot: t.TempDir(),
		Cfg: &config.DevboxConfig{
			Services: map[string]config.ServiceConfig{
				"adminer": {
					Enabled: true,
					Type:    config.ServiceTypeTool,
					Dir:     "services/adminer",
					Render: config.ServiceRenderConfig{
						IDE: config.ServiceIDEConfig{Enabled: &tr},
						AI:  config.ServiceAIConfig{Enabled: &tr},
						Git: config.ServiceGitHooksConfig{Enabled: &tr},
					},
				},
				"db": {
					Enabled: true,
					Type:    config.ServiceTypeInfra,
					Dir:     "services/db",
					Render: config.ServiceRenderConfig{
						IDE: config.ServiceIDEConfig{Enabled: &tr},
						AI:  config.ServiceAIConfig{Enabled: &tr},
						Git: config.ServiceGitHooksConfig{Enabled: &tr},
					},
				},
			},
		},
	}

	tests := []struct {
		name      string
		validator validate.Validator
		target    string
	}{
		{name: "ide", validator: &IDEValidator{}, target: "templates.ide"},
		{name: "ai", validator: &AIValidator{}, target: "templates.ai"},
		{name: "git", validator: &GitValidator{}, target: "templates.git"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := tt.validator.Run(ctx)
			require.Len(t, diags, 1)
			require.Equal(t, validate.SeverityOK, diags[0].Severity)
			require.Equal(t, tt.target, diags[0].Target)
			for _, d := range diags {
				require.NotContains(t, d.Target, "adminer")
				require.NotContains(t, d.Target, "db")
			}
		})
	}
}

func TestAllFunction(t *testing.T) {
	validators := All()
	require.Len(t, validators, 3)

	ids := make(map[string]bool)
	for _, v := range validators {
		ids[v.ID()] = true
	}
	require.True(t, ids["ide"], "IDE validator should be present")
	require.True(t, ids["ai"], "AI validator should be present")
	require.True(t, ids["git"], "Git validator should be present")
}

func ideSvc(dir string) config.ServiceConfig {
	tr := true
	return config.ServiceConfig{
		Enabled: true,
		Type:    "app",
		Dir:     dir,
		Render:  config.ServiceRenderConfig{IDE: config.ServiceIDEConfig{Enabled: &tr}},
	}
}

func aiSvc(dir string) config.ServiceConfig {
	tr := true
	return config.ServiceConfig{
		Enabled: true,
		Type:    "app",
		Dir:     dir,
		Render:  config.ServiceRenderConfig{AI: config.ServiceAIConfig{Enabled: &tr}},
	}
}

func TestIDEValidator_ImplicitMissingPackEmitsWarning(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "services", "main"), 0o755))

	v := &IDEValidator{}
	diags := v.Run(validate.Context{
		ProjectRoot: root,
		Cfg: &config.DevboxConfig{
			Services: map[string]config.ServiceConfig{"main": ideSvc("services/main")},
		},
	})

	warnDiag := findDiag(diags, validate.SeverityWarning, "templates.ide:main")
	require.NotNil(t, warnDiag)
	require.Contains(t, warnDiag.Message, "template pack not found")
}

func TestIDEValidator_ExplicitMissingPackEmitsError(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "services", "main"), 0o755))

	tr := true
	svc := config.ServiceConfig{
		Enabled: true,
		Type:    "app",
		Dir:     "services/main",
		Render: config.ServiceRenderConfig{
			IDE: config.ServiceIDEConfig{Enabled: &tr, Template: "custom"},
		},
	}

	v := &IDEValidator{}
	diags := v.Run(validate.Context{
		ProjectRoot: root,
		Cfg: &config.DevboxConfig{
			Services: map[string]config.ServiceConfig{"main": svc},
		},
	})

	errDiag := findDiag(diags, validate.SeverityError, "templates.ide:main")
	require.NotNil(t, errDiag)
	require.Contains(t, errDiag.Message, "failed to resolve template pack")
	require.Contains(t, errDiag.Message, "custom")
}

func TestAIValidator_ImplicitMissingPackEmitsWarning(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "services", "main"), 0o755))

	v := &AIValidator{}
	diags := v.Run(validate.Context{
		ProjectRoot: root,
		Cfg: &config.DevboxConfig{
			Services: map[string]config.ServiceConfig{"main": aiSvc("services/main")},
		},
	})

	warnDiag := findDiag(diags, validate.SeverityWarning, "templates.ai:main")
	require.NotNil(t, warnDiag)
	require.Contains(t, warnDiag.Message, "template pack not found")
}

func TestAIValidator_ExplicitMissingPackEmitsError(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "services", "main"), 0o755))

	tr := true
	svc := config.ServiceConfig{
		Enabled: true,
		Type:    "app",
		Dir:     "services/main",
		Render: config.ServiceRenderConfig{
			AI: config.ServiceAIConfig{Enabled: &tr, Template: "custom"},
		},
	}

	v := &AIValidator{}
	diags := v.Run(validate.Context{
		ProjectRoot: root,
		Cfg: &config.DevboxConfig{
			Services: map[string]config.ServiceConfig{"main": svc},
		},
	})

	errDiag := findDiag(diags, validate.SeverityError, "templates.ai:main")
	require.NotNil(t, errDiag)
	require.Contains(t, errDiag.Message, "failed to resolve template pack")
	require.Contains(t, errDiag.Message, "custom")
}

func TestGitValidator_BasicID(t *testing.T) {
	v := &GitValidator{}
	require.Equal(t, "git", v.ID())
	require.Equal(t, "templates", v.Domain())
}

func TestGitValidator_NilConfig(t *testing.T) {
	v := &GitValidator{}
	diags := v.Run(validate.Context{ProjectRoot: t.TempDir(), Cfg: nil})
	require.Len(t, diags, 1)
	require.Equal(t, validate.SeverityInfo, diags[0].Severity)
	require.Equal(t, "templates.git", diags[0].Target)
}

func TestGitValidator_NoServices(t *testing.T) {
	v := &GitValidator{}
	diags := v.Run(validate.Context{
		ProjectRoot: t.TempDir(),
		Cfg:         &config.DevboxConfig{Services: map[string]config.ServiceConfig{}},
	})
	require.Len(t, diags, 1)
	require.Equal(t, validate.SeverityOK, diags[0].Severity)
}

func gitSvc(dir string) config.ServiceConfig {
	tr := true
	return config.ServiceConfig{
		Enabled: true,
		Type:    "app",
		Dir:     dir,
		Render:  config.ServiceRenderConfig{Git: config.ServiceGitHooksConfig{Enabled: &tr}},
	}
}

func TestGitValidator_InvalidToEmitsErrorEvenWhenSrcGitMissing(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "services", "main"), 0o755))
	// No src/.git directory exists for service.
	writeGitPack(t, root, "default", map[string]string{
		"manifest.yml":    "render:\n  - {from: pre-commit.tmpl, to: hooks/pre-commit}\n",
		"pre-commit.tmpl": "#!/bin/sh\n",
	})

	v := &GitValidator{}
	diags := v.Run(validate.Context{
		ProjectRoot: root,
		Cfg: &config.DevboxConfig{
			Services: map[string]config.ServiceConfig{"main": gitSvc("services/main")},
		},
	})

	// Must emit error even though src/.git missing.
	require.GreaterOrEqual(t, severityCount(diags, validate.SeverityError), 1,
		"expected error diagnostic for invalid `to: hooks/pre-commit` regardless of missing src/.git")
	errDiag := findDiag(diags, validate.SeverityError, "templates.git:main")
	require.NotNil(t, errDiag)
	require.Contains(t, errDiag.Message, "invalid manifest")
}

func TestGitValidator_ValidPackEmitsInfoForMissingSrcGit(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "services", "main"), 0o755))
	writeGitPack(t, root, "default", map[string]string{
		"manifest.yml":    "render:\n  - {from: pre-commit.tmpl, to: pre-commit}\n",
		"pre-commit.tmpl": "#!/bin/sh\n",
	})

	v := &GitValidator{}
	diags := v.Run(validate.Context{
		ProjectRoot: root,
		Cfg: &config.DevboxConfig{
			Services: map[string]config.ServiceConfig{"main": gitSvc("services/main")},
		},
	})

	require.Equal(t, 0, severityCount(diags, validate.SeverityError))
	infoDiag := findDiag(diags, validate.SeverityInfo, "templates.git:main")
	require.NotNil(t, infoDiag, "expected info diagnostic for missing src/.git")
	require.Contains(t, infoDiag.Message, "no src/.git")
}

func TestGitValidator_ImplicitMissingPackEmitsWarning(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "services", "main"), 0o755))
	// No pack on disk at all (implicit missing).

	v := &GitValidator{}
	diags := v.Run(validate.Context{
		ProjectRoot: root,
		Cfg: &config.DevboxConfig{
			Services: map[string]config.ServiceConfig{"main": gitSvc("services/main")},
		},
	})

	warnDiag := findDiag(diags, validate.SeverityWarning, "templates.git:main")
	require.NotNil(t, warnDiag)
	require.Contains(t, warnDiag.Message, "template pack not found")
}

func TestGitValidator_ExplicitMissingPackEmitsError(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "services", "main"), 0o755))
	// Explicit template set but pack does not exist.

	tr := true
	svc := config.ServiceConfig{
		Enabled: true,
		Type:    "app",
		Dir:     "services/main",
		Render: config.ServiceRenderConfig{
			Git: config.ServiceGitHooksConfig{Enabled: &tr, Template: "custom"},
		},
	}

	v := &GitValidator{}
	diags := v.Run(validate.Context{
		ProjectRoot: root,
		Cfg: &config.DevboxConfig{
			Services: map[string]config.ServiceConfig{"main": svc},
		},
	})

	errDiag := findDiag(diags, validate.SeverityError, "templates.git:main")
	require.NotNil(t, errDiag)
	require.Contains(t, errDiag.Message, "failed to resolve template pack")
	require.Contains(t, errDiag.Message, "custom")
}

func TestGitValidator_MissingManifestEmitsError(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "services", "main"), 0o755))
	// Pack dir exists but no manifest.yml inside.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "devbox", "templates", "git", "default"), 0o755))
	writeFileAt(t, filepath.Join(root, "devbox", "templates", "git", "default", "pre-commit.tmpl"), "#!/bin/sh\n")

	v := &GitValidator{}
	diags := v.Run(validate.Context{
		ProjectRoot: root,
		Cfg: &config.DevboxConfig{
			Services: map[string]config.ServiceConfig{"main": gitSvc("services/main")},
		},
	})

	errDiag := findDiag(diags, validate.SeverityError, "templates.git:main")
	require.NotNil(t, errDiag)
	require.Contains(t, errDiag.Message, "failed to load manifest")
}

func TestGitValidator_MissingFromFileEmitsError(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "services", "main"), 0o755))
	// Manifest declares pre-commit.tmpl but it does not exist on disk.
	writeGitPack(t, root, "default", map[string]string{
		"manifest.yml": "render:\n  - {from: pre-commit.tmpl, to: pre-commit}\n",
	})

	v := &GitValidator{}
	diags := v.Run(validate.Context{
		ProjectRoot: root,
		Cfg: &config.DevboxConfig{
			Services: map[string]config.ServiceConfig{"main": gitSvc("services/main")},
		},
	})

	errDiag := findDiag(diags, validate.SeverityError, "templates.git:main")
	require.NotNil(t, errDiag)
	require.Contains(t, errDiag.Message, "invalid manifest")
}

func TestGitValidator_ShadowOverrideResolvesMissingFrom(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "services", "main"), 0o755))
	// Canonical manifest declares pre-commit.tmpl, canonical file ABSENT.
	writeGitPack(t, root, "default", map[string]string{
		"manifest.yml": "render:\n  - {from: pre-commit.tmpl, to: pre-commit}\n",
	})
	// Override supplies the missing file.
	writeFileAt(t,
		filepath.Join(root, "devbox", "templates", "git", "default.local", "pre-commit.tmpl"),
		"#!/bin/sh\necho override\n")

	v := &GitValidator{}
	diags := v.Run(validate.Context{
		ProjectRoot: root,
		Cfg: &config.DevboxConfig{
			Services: map[string]config.ServiceConfig{"main": gitSvc("services/main")},
		},
	})

	require.Equal(t, 0, severityCount(diags, validate.SeverityError),
		"shadow override should satisfy from-file existence; got: %+v", diags)
}

func TestGitValidator_NoOverrides_NoOverrideInfoDiag(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "services", "main"), 0o755))
	writeGitPack(t, root, "default", map[string]string{
		"manifest.yml":    "render:\n  - {from: pre-commit.tmpl, to: pre-commit}\n",
		"pre-commit.tmpl": "#!/bin/sh\n",
	})

	v := &GitValidator{}
	diags := v.Run(validate.Context{
		ProjectRoot: root,
		Cfg: &config.DevboxConfig{
			Services: map[string]config.ServiceConfig{"main": gitSvc("services/main")},
		},
	})

	for _, d := range diags {
		require.NotContains(t, d.Message, "local override", "no override info expected when no overrides applied")
	}
}

func TestGitValidator_OneOverride_EmitsInfoDiag(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "services", "main"), 0o755))
	writeGitPack(t, root, "default", map[string]string{
		"manifest.yml":    "render:\n  - {from: pre-commit.tmpl, to: pre-commit}\n",
		"pre-commit.tmpl": "#!/bin/sh\n",
	})
	writeFileAt(t,
		filepath.Join(root, "devbox", "templates", "git", "default.local", "pre-commit.tmpl"),
		"#!/bin/sh\necho override\n")

	v := &GitValidator{}
	diags := v.Run(validate.Context{
		ProjectRoot: root,
		Cfg: &config.DevboxConfig{
			Services: map[string]config.ServiceConfig{"main": gitSvc("services/main")},
		},
	})

	infoDiag := findOverrideDiag(diags, "templates.git:main")
	require.NotNil(t, infoDiag, "expected one override info diag; got: %+v", diags)
	require.Contains(t, infoDiag.Message, "1 local override")
	require.Contains(t, infoDiag.Message, "pre-commit.tmpl")
}

func TestGitValidator_ManyOverrides_TruncatedListing(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "services", "main"), 0o755))
	// Manifest with 7 entries; all 7 overridden.
	var b strings.Builder
	b.WriteString("render:\n")
	files := map[string]string{}
	for i := 1; i <= 7; i++ {
		name := fmt.Sprintf("h%d.tmpl", i)
		fmt.Fprintf(&b, "  - {from: %s, to: hook%d}\n", name, i)
		files[name] = "#!/bin/sh\n"
		writeFileAt(t,
			filepath.Join(root, "devbox", "templates", "git", "default.local", name),
			"#!/bin/sh\necho override\n")
	}
	files["manifest.yml"] = b.String()
	writeGitPack(t, root, "default", files)

	v := &GitValidator{}
	diags := v.Run(validate.Context{
		ProjectRoot: root,
		Cfg: &config.DevboxConfig{
			Services: map[string]config.ServiceConfig{"main": gitSvc("services/main")},
		},
	})

	infoDiag := findOverrideDiag(diags, "templates.git:main")
	require.NotNil(t, infoDiag)
	require.Contains(t, infoDiag.Message, "7 local override")
	require.Contains(t, infoDiag.Message, "...")
}

// findOverrideDiag returns an info diagnostic whose message starts with "using N local override".
func findOverrideDiag(diags []validate.Diagnostic, target string) *validate.Diagnostic {
	for i := range diags {
		if diags[i].Severity == validate.SeverityInfo && diags[i].Target == target &&
			strings.Contains(diags[i].Message, "local override") {
			return &diags[i]
		}
	}
	return nil
}
