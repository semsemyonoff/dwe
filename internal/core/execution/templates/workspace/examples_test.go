package workspace_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/templates/packcommon"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/workspace"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	commandvalidate "github.com/semsemyonoff/dwe/internal/core/validate/commands"
)

func TestExamplePacks(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"ralphex", "root-agents"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := installExample(t, name, "")
			cfgPath := filepath.Join(root, "workspace.yml")
			cfg, err := config.LoadConfigSanitized(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			reg, err := usercommands.LoadRegistryFromConfigPath(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, diag := range (&commandvalidate.Validator{}).Run(validate.Context{
				ProjectRoot: root, ConfigPath: cfgPath, Cfg: cfg, CommandRegistry: reg,
			}) {
				if diag.Severity == validate.SeverityError || diag.Severity == validate.SeverityWarning {
					t.Errorf("example command diagnostic: %+v", diag)
				}
			}
			planned, err := workspace.Plan(root, cfg.Render.Workspace, exampleTemplateData(cfg))
			if err != nil {
				t.Fatal(err)
			}
			if len(planned) != 1 || planned[0].Name != name {
				t.Fatalf("unexpected packs: %+v", planned)
			}
			for _, file := range planned[0].Files {
				source := readExampleFile(t, filepath.Join(root, "workspace/templates/workspace", name, file.From))
				if !strings.HasSuffix(file.From, ".tmpl") && !bytes.Equal(source, file.File.Data) {
					t.Errorf("verbatim file changed: %s", file.From)
				}
				switch file.To {
				case ".ralphex/config":
					for _, setting := range []string{
						"default_branch = main", "vcs_command = .ralphex/scripts/ws-git",
						"use_worktree = false", "finalize_enabled = false",
					} {
						if !bytes.Contains(file.File.Data, []byte(setting)) {
							t.Errorf("config missing %q: %s", setting, file.File.Data)
						}
					}
				case ".ralphex/scripts/ws-git":
					if file.File.Mode != 0o755 {
						t.Errorf("planned ws-git mode = %o", file.File.Mode)
					}
				case "AGENTS.md":
					for _, want := range []string{"# example workspace", "api (app): services/api", "database (infra)"} {
						if !bytes.Contains(file.File.Data, []byte(want)) {
							t.Errorf("agent template missing %q: %s", want, file.File.Data)
						}
					}
				case "workspace-notes.txt":
					if !bytes.Contains(file.File.Data, []byte("{{PLAN_FILE}}")) {
						t.Error("literal placeholder lost")
					}
				}
			}
			if name == "ralphex" {
				if len(reg.List("ralphex")) != 2 {
					t.Fatalf("expected two ralphex commands: %+v", reg.List("ralphex"))
				}
				scope, err := reg.Get("ralphex.scope")
				if err != nil {
					t.Fatal(err)
				}
				for _, param := range []string{"repos", "base", "branch"} {
					if !scope.Params[param].Required {
						t.Errorf("scope param %s is not required", param)
					}
				}
				// Optional params must not open the interactive form when omitted.
				if p := scope.Params["prepare"]; p.Required || p.Default != "false" || p.Type != usercommands.ParamTypeBool {
					t.Errorf("prepare param must be an optional bool defaulting to false: %+v", p)
				}
				if p := scope.Params["plan"]; p.Required || p.Default != "" || p.DefaultFrom != "" ||
					(p.Type != usercommands.ParamTypeString && p.Type != "") {
					t.Errorf("plan param must be an optional string without default: %+v", p)
				}
				resolved, err := usercommands.ResolveParams(scope.Params,
					map[string]string{"repos": ".", "base": "base", "branch": "task"}, cfg)
				if err != nil || resolved["prepare"] != false || resolved["plan"] != "" {
					t.Errorf("omitted optional params resolved to %+v, %v", resolved, err)
				}
				for name, want := range map[string]string{
					"RALPHEX_PREPARE": "${param.prepare}", "RALPHEX_PLAN": "${param.plan}",
				} {
					if got := scope.Env[name]; got != want {
						t.Errorf("scope env %s = %q, want %q", name, got, want)
					}
				}
				prompts, err := reg.Get("ralphex.prompts")
				if err != nil {
					t.Fatal(err)
				}
				if prompts.Params["check"].Default != "false" || prompts.Params["check"].Type != usercommands.ParamTypeBool {
					t.Fatalf("check param must default to false: %+v", prompts.Params["check"])
				}
			}
		})
	}
}

func TestExampleRalphex_DefaultBranch(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		vars string
		want string
	}{
		{name: "absent", want: "main"},
		{name: "unrelated vars", vars: "vars: {other: true}\n", want: "main"},
		{name: "empty ralphex", vars: "vars: {ralphex: {}}\n", want: "main"},
		{name: "empty branch", vars: "vars: {ralphex: {default_branch: ''}}\n", want: "main"},
		{name: "configured", vars: "vars: {ralphex: {default_branch: trunk}}\n", want: "trunk"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := installExample(t, "ralphex", "render:\n  workspace: [ralphex]\n"+tt.vars)
			cfg, err := config.LoadConfigSanitized(filepath.Join(root, "workspace.yml"))
			if err != nil {
				t.Fatal(err)
			}
			planned, err := workspace.Plan(root, cfg.Render.Workspace, exampleTemplateData(cfg))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(planned[0].Files[0].File.Data, []byte("default_branch = "+tt.want+"\n")) {
				t.Fatalf("wrong default branch: %s", planned[0].Files[0].File.Data)
			}
		})
	}
}

func installExample(t *testing.T, name, activation string) string {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(findRepoRoot(t), "examples/workspace-packs", name)
	copyExampleTree(t, filepath.Join(source, "workspace"), filepath.Join(root, "workspace"))
	if activation == "" {
		snippet, err := os.ReadFile(filepath.Join(source, "workspace.yml.snippet"))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		activation = string(snippet)
		if len(snippet) == 0 {
			activation = "render:\n  workspace: [" + name + "]\n"
		}
	}
	writeFile(t, filepath.Join(root, "workspace.yml"),
		"schema_version: \"2\"\nproject:\n  name: example\n"+activation)
	writeFile(t, filepath.Join(root, "workspace/services/api/service.yml"),
		"type: app\ncontainer: api\ndir: services/api\n")
	writeFile(t, filepath.Join(root, "workspace/services/database/service.yml"),
		"type: infra\ncontainer: database\n")
	return root
}

func copyExampleTree(t *testing.T, source, dest string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func exampleTemplateData(cfg *config.DweConfig) packcommon.TemplateData {
	return packcommon.TemplateData{Project: cfg.Project, Runtime: cfg.Runtime, Services: cfg.Services, Cfg: cfg}
}

func readExampleFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}
