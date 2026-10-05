package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

func TestWorkspaceValidator(t *testing.T) {
	t.Parallel()
	const validManifest = "render:\n  - {from: config.tmpl, to: .tool/config}\n  - {from: task.md, to: .tool/task.md}\nsymlinks:\n  - {link: TOOL.md, to: .tool/task.md}\n"
	tests := []struct {
		name         string
		packs        []string
		manifest     string
		template     string
		nilConfig    bool
		destDir      bool
		wantSeverity validate.Severity
		wantMessage  string
	}{
		{name: "nil config", nilConfig: true, wantSeverity: validate.SeverityInfo, wantMessage: "requires successful main config load; skipped"},
		{name: "empty list", wantSeverity: validate.SeverityInfo, wantMessage: "no workspace packs configured"},
		{name: "clean pack with verbatim placeholder", packs: []string{"tool"}, manifest: validManifest, template: "{{ .Project.Name }} {{ .Runtime.UseHTTPS }} {{ .Services.main.Container }} {{ .Cfg.Vars.value }}{{ if or .Service .Resolved .ServiceCfg.Container }}{{ .UnexpectedService }}{{ end }}"},
		{name: "missing pack", packs: []string{"missing"}, wantSeverity: validate.SeverityError, wantMessage: "stat workspace pack \"missing\""},
		{name: "protected path", packs: []string{"tool"}, manifest: "render: [{from: config.tmpl, to: .env}]\n", template: "valid", wantSeverity: validate.SeverityError, wantMessage: "is protected"},
		{name: "protected symlink", packs: []string{"tool"}, manifest: "render: [{from: config.tmpl, to: .tool/config}]\nsymlinks: [{link: .GIT/hooks, to: .tool/config}]\n", template: "valid", wantSeverity: validate.SeverityError, wantMessage: "is protected"},
		{name: "template execution error", packs: []string{"tool"}, manifest: validManifest, template: "{{ .Cfg.Vars.missing }}", wantSeverity: validate.SeverityError, wantMessage: "map has no entry for key \"missing\""},
		{name: "template parse error", packs: []string{"tool"}, manifest: validManifest, template: "{{ if }}", wantSeverity: validate.SeverityError, wantMessage: "parse template"},
		{name: "missing source", packs: []string{"tool"}, manifest: "render: [{from: absent.tmpl, to: .tool/config}]\n", wantSeverity: validate.SeverityError, wantMessage: "absent.tmpl"},
		{name: "duplicate pack", packs: []string{"tool", "tool"}, manifest: validManifest, template: "valid", wantSeverity: validate.SeverityError, wantMessage: "is duplicated"},
		{name: "cross-pack collision", packs: []string{"tool", "other"}, manifest: validManifest, template: "valid", wantSeverity: validate.SeverityError, wantMessage: "collides"},
		{name: "destination directory", packs: []string{"tool"}, manifest: validManifest, template: "valid", destDir: true, wantSeverity: validate.SeverityError, wantMessage: "is a directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for _, name := range tt.packs {
				if tt.manifest != "" {
					pack := filepath.Join(root, "workspace", "templates", "workspace", name)
					writeFileAt(t, filepath.Join(pack, "manifest.yml"), tt.manifest)
					writeFileAt(t, filepath.Join(pack, "config.tmpl"), tt.template)
					writeFileAt(t, filepath.Join(pack, "task.md"), "Review {{PLAN_FILE}} verbatim.\n")
				}
			}
			if tt.destDir {
				if err := os.MkdirAll(filepath.Join(root, ".tool", "config"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			cfg := &config.DweConfig{
				Project:  config.ProjectConfig{Name: "test"},
				Runtime:  config.RuntimeConfig{UseHTTPS: true},
				Services: map[string]config.ServiceConfig{"main": {Container: "app"}},
				Vars:     map[string]any{"value": "configured"},
				Render:   config.RenderConfig{Workspace: tt.packs},
			}
			if tt.nilConfig {
				cfg = nil
			}
			diags := (&WorkspaceValidator{}).Run(validate.Context{ProjectRoot: root, Cfg: cfg})
			if tt.wantMessage == "" {
				if len(diags) != 0 {
					t.Fatalf("clean pack diagnostics: %+v", diags)
				}
			} else {
				if len(diags) != 1 {
					t.Fatalf("got diagnostics %+v, want one", diags)
				}
				d := diags[0]
				if d.Severity != tt.wantSeverity || d.Domain != "templates" || d.Target != "templates.workspace" || !strings.Contains(d.Message, tt.wantMessage) {
					t.Fatalf("unexpected diagnostic: %+v, want severity %v and message containing %q", d, tt.wantSeverity, tt.wantMessage)
				}
			}
			for _, rel := range []string{".env", ".git", "TOOL.md", ".tool/task.md"} {
				if _, err := os.Lstat(filepath.Join(root, rel)); !os.IsNotExist(err) {
					t.Errorf("validation changed destination %s: %v", rel, err)
				}
			}
			if !tt.destDir {
				if _, err := os.Lstat(filepath.Join(root, ".tool")); !os.IsNotExist(err) {
					t.Errorf("validation created destination directory: %v", err)
				}
			}
		})
	}
}

func TestWorkspaceValidator_PlanSeesCommandIndex(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	pack := filepath.Join(root, "workspace", "templates", "workspace", "tool")
	writeFileAt(t, filepath.Join(pack, "manifest.yml"), "render: [{from: config.tmpl, to: .tool/config}]\n")
	writeFileAt(t, filepath.Join(pack, "config.tmpl"), "{{ if not .Commands }}{{ .CommandIndexMissing }}{{ end }}{{ if not .CommandGroups }}{{ .GroupsMissing }}{{ end }}{{ if ne (index .Commands 0).ID \"tools.lint\" }}{{ .WrongCommand }}{{ end }}")
	configPath := filepath.Join(root, "workspace.yml")
	writeFileAt(t, configPath, "schema_version: \"2\"\nproject:\n  name: test\n")
	writeFileAt(t, filepath.Join(root, "workspace", "commands", "tools.yml"), "group:\n  description: Tools\ncommands:\n  lint:\n    type: shell\n    cmd: echo lint\n")
	reg, err := usercommands.LoadRegistryFromConfigPath(configPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := validate.Context{
		ProjectRoot: root,
		Cfg: &config.DweConfig{
			Render: config.RenderConfig{Workspace: []string{"tool"}},
		},
		CommandRegistry: reg,
	}
	v := &WorkspaceValidator{}
	if diags := v.Run(ctx); len(diags) != 0 {
		t.Fatalf("command index did not reach template: %+v", diags)
	}
	ctx.CommandRegistry = nil
	if diags := v.Run(ctx); findDiag(diags, validate.SeverityError, "templates.workspace") == nil {
		t.Fatalf("template should fail without a command index: %+v", diags)
	}
}

func TestWorkspaceValidator_PlanSeesSanitizedConfig(t *testing.T) {
	root := t.TempDir()
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(secrets.EnvKey, id.Export())
	t.Setenv(secrets.EnvKeyFile, "")
	marker, err := secrets.Encrypt("s3cr3t-value", id.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "workspace.yml")
	writeFileAt(t, configPath, "schema_version: \"2\"\nproject:\n  name: test\nsecrets:\n  recipient: "+id.Recipient()+"\nvars:\n  token: "+marker+"\nrender:\n  workspace: [tool]\n")
	pack := filepath.Join(root, "workspace", "templates", "workspace", "tool")
	writeFileAt(t, filepath.Join(pack, "manifest.yml"), "render: [{from: config.tmpl, to: .tool/config}]\n")
	writeFileAt(t, filepath.Join(pack, "config.tmpl"), "{{ if ne .Cfg.Vars.token \""+marker+"\" }}{{ .PlaintextExposed }}{{ end }}")
	loaded, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Vars["token"] != "s3cr3t-value" {
		t.Fatalf("fixture did not decrypt: %v", loaded.Vars["token"])
	}
	v := &WorkspaceValidator{}
	ctx := validate.Context{ProjectRoot: root, ConfigPath: configPath, Cfg: loaded}
	if diags := v.Run(ctx); len(diags) != 0 {
		t.Fatalf("template did not see the encrypted marker: %+v", diags)
	}
	ctx.ConfigPath = ""
	if diags := v.Run(ctx); findDiag(diags, validate.SeverityError, "templates.workspace") == nil {
		t.Fatalf("control template should fail with plaintext config: %+v", diags)
	}
}
