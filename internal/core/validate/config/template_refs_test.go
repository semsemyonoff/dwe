package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	devconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func runTemplateRefsValidator(t *testing.T, root string) []validate.Diagnostic {
	t.Helper()
	cfg, err := devconfig.LoadConfig(filepath.Join(root, "workspace.yml"))
	require.NoError(t, err)
	return (&templateRefsValidator{}).Run(validate.Context{
		ProjectRoot: root,
		ConfigPath:  filepath.Join(root, "workspace.yml"),
		Cfg:         cfg,
	})
}

// TestTemplateRefsValidator_Typo pins the motivating case: a service deploy.yml
// referencing ${vars.opechatka} (a typo — never declared under vars:) beside a
// valid ${vars.source.repo}. Exactly one warning, naming the file/step/field.
func TestTemplateRefsValidator_Typo(t *testing.T) {
	t.Parallel()
	root := filepath.Join("testdata", "template_ref_typo")
	diags := runTemplateRefsValidator(t, root)

	d := hasDiag(t, diags, validate.SeverityWarning, "vars.opechatka")
	require.Equal(t, "config.template_refs", d.Target)
	require.Equal(t, "workspace/services/app/deploy.yml", d.File)
	require.Equal(t, 6, d.Line)
	require.Contains(t, d.Message, "does not resolve")

	// The valid reference must not also warn.
	for _, diag := range diags {
		require.NotContains(t, diag.Message, "vars.source.repo")
	}
	require.Len(t, diags, 1)
}

func TestTemplateRefsValidator_NegativeCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		workspace  string
		deployBody string
	}{
		{
			name: "unknown head is silent",
			workspace: `project:
  name: test
`,
			deployBody: `phases:
  - name: setup
    steps:
      - name: step
        type: shell
        cmd: "echo ${HOME}"
`,
		},
		{
			name: "param head is silent",
			workspace: `project:
  name: test
`,
			deployBody: `phases:
  - name: setup
    steps:
      - name: step
        type: shell
        cmd: "echo ${param.name}"
`,
		},
		{
			name: "generated head is silent",
			workspace: `project:
  name: test
`,
			deployBody: `phases:
  - name: setup
    steps:
      - name: step
        type: shell
        cmd: "echo ${generated.app_key}"
`,
		},
		{
			// A head-only ${state} / ${vars} / ${stop} is a lowercase shell
			// variable that happens to collide with a root-key name;
			// CompileVarSyntax leaves it literal (tpl.IsVarNamespaceRef needs a
			// tail), so warning that it "does not resolve" would flag exactly
			// what the whitelist keeps out of the engine.
			name: "head-only known head is silent",
			workspace: `project:
  name: test
`,
			deployBody: `phases:
  - name: setup
    steps:
      - name: step
        type: shell
        cmd: "for state in a b; do echo ${state} ${vars} ${stop}; done"
`,
		},
		{
			name: "resolvable reference is silent",
			workspace: `project:
  name: test
vars:
  source:
    repo: https://example.com/app.git
`,
			deployBody: `phases:
  - name: setup
    steps:
      - name: step
        type: shell
        cmd: "git clone ${vars.source.repo}"
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte(tc.workspace), 0o644))
			require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "workspace", "deploy.yml"), []byte(tc.deployBody), 0o644))

			diags := runTemplateRefsValidator(t, root)
			require.Empty(t, diags)
		})
	}
}

// TestTemplateRefsValidator_HeadBlockAbsent pins that the gate is the root-key
// allowlist, not "head present in cfg.Raw". A project that never declared a
// vars: block is the most common shape (the scaffold ships none), and every
// ${vars.*} reference in it is unresolvable — a Raw-presence probe would treat
// the whole class as none of this validator's business and stay silent.
func TestTemplateRefsValidator_HeadBlockAbsent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace.yml"), []byte("project:\n  name: test\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspace"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace", "deploy.yml"), []byte(`phases:
  - name: setup
    steps:
      - name: clone
        type: shell
        cmd: "git clone ${vars.source.repo}"
`), 0o644))

	diags := runTemplateRefsValidator(t, root)
	d := hasDiag(t, diags, validate.SeverityWarning, "vars.source.repo")
	require.Equal(t, "config.template_refs", d.Target)
	require.Equal(t, "workspace/deploy.yml", d.File)
	require.Contains(t, d.Message, "does not resolve")
	require.Len(t, diags, 1)
}

func TestTemplateRefsValidator_NilCfgIsSilent(t *testing.T) {
	t.Parallel()
	diags := (&templateRefsValidator{}).Run(validate.Context{ProjectRoot: t.TempDir()})
	require.Empty(t, diags)
}
