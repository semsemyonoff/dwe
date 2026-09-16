package templates

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// commandIndexValidators lists the three dry-run validators with the pack
// layout and service shape each one needs to reach its DryRunRender call.
func commandIndexValidators() []struct {
	name string
	v    validate.Validator
	kind string
	file string
	svc  func(dir string) config.ServiceConfig
} {
	return []struct {
		name string
		v    validate.Validator
		kind string
		file string
		svc  func(dir string) config.ServiceConfig
	}{
		{name: "ai", v: &AIValidator{}, kind: "ai", file: "AGENTS.md", svc: aiSvc},
		{name: "ide", v: &IDEValidator{}, kind: "ide", file: "settings.json", svc: ideSvc},
		{name: "git", v: &GitValidator{}, kind: "git", file: "pre-commit", svc: gitSvc},
	}
}

func writeCommandIndexProject(t *testing.T, kind, file, tmpl, commandFile string) string {
	t.Helper()
	root := t.TempDir()
	writeFileAt(t, filepath.Join(root, "services", "main", "src", ".git", "hooks", ".keep"), "")
	pack := filepath.Join(root, "workspace", "templates", kind, "default")
	writeFileAt(t, filepath.Join(pack, "manifest.yml"), "render:\n  - {from: "+file+".tmpl, to: "+file+"}\n")
	writeFileAt(t, filepath.Join(pack, file+".tmpl"), tmpl)
	writeFileAt(t, filepath.Join(root, "workspace.yml"), "schema_version: \"2\"\nproject:\n  name: p\n")
	if commandFile != "" {
		writeFileAt(t, filepath.Join(root, "workspace", "commands", "tools.yml"), commandFile)
	}
	return root
}

// TestTemplateValidators_DryRunSeesCommandIndex pins that the index built from
// ctx.CommandRegistry reaches the dry-run TemplateData: the template reads a
// missing field only when .Commands is empty, so a validator that drops the
// index reports a render error.
func TestTemplateValidators_DryRunSeesCommandIndex(t *testing.T) {
	const tmpl = "{{ if not .Commands }}{{ .CommandIndexMissing }}{{ end }}{{ len .CommandGroups }}\n"
	const commands = "group:\n  description: Tools\ncommands:\n  lint:\n    type: shell\n    cmd: echo lint\n"

	for _, tc := range commandIndexValidators() {
		t.Run(tc.name, func(t *testing.T) {
			root := writeCommandIndexProject(t, tc.kind, tc.file, tmpl, commands)
			reg, err := usercommands.LoadRegistryFromConfigPath(filepath.Join(root, "workspace.yml"))
			require.NoError(t, err)

			diags := tc.v.Run(validate.Context{
				ProjectRoot:     root,
				CommandRegistry: reg,
				Cfg: &config.DweConfig{
					Services: map[string]config.ServiceConfig{"main": tc.svc("services/main")},
				},
			})
			require.Equal(t, 0, severityCount(diags, validate.SeverityError), "diags: %+v", diags)

			// Control: without a registry the same template must fail, or
			// the assertion above proves nothing.
			diags = tc.v.Run(validate.Context{
				ProjectRoot: root,
				Cfg: &config.DweConfig{
					Services: map[string]config.ServiceConfig{"main": tc.svc("services/main")},
				},
			})
			require.NotNil(t, findDiag(diags, validate.SeverityError, "templates."+tc.kind+":main"), "diags: %+v", diags)
		})
	}
}

// TestTemplateValidators_BrokenCommandFileIsSilent pins the failure policy on
// the validate side: `dwe validate` leaves CommandRegistry nil when a command
// file does not load, the commands domain reports it, and the templates
// domain must not report it a second time.
func TestTemplateValidators_BrokenCommandFileIsSilent(t *testing.T) {
	const tmpl = "commands={{ len .Commands }} groups={{ len .CommandGroups }}\n"

	for _, tc := range commandIndexValidators() {
		t.Run(tc.name, func(t *testing.T) {
			root := writeCommandIndexProject(t, tc.kind, tc.file, tmpl, "commands: [unclosed\n")
			_, err := usercommands.LoadRegistryFromConfigPath(filepath.Join(root, "workspace.yml"))
			require.Error(t, err, "fixture must carry an unloadable command file")
			_, statErr := os.Stat(filepath.Join(root, "workspace", "commands", "tools.yml"))
			require.NoError(t, statErr)

			diags := tc.v.Run(validate.Context{
				ProjectRoot: root,
				Cfg: &config.DweConfig{
					Services: map[string]config.ServiceConfig{"main": tc.svc("services/main")},
				},
			})
			for _, d := range diags {
				require.NotContains(t, []validate.Severity{validate.SeverityError, validate.SeverityWarning}, d.Severity, "unexpected diagnostic: %+v", d)
			}
		})
	}
}
