package render

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

func writeWorkspaceFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupWorkspacePack(t *testing.T, root, name string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		writeWorkspaceFixture(t, root, filepath.Join("workspace", "templates", "workspace", name, rel), content)
	}
}

func workspaceRenderProject(t *testing.T, extra string) string {
	t.Helper()
	root := t.TempDir()
	writeWorkspaceFixture(t, root, "workspace.yml", "schema_version: \"2\"\nproject:\n  name: test-project\n"+extra)
	return root
}

func TestNewWorkspaceCmd_selection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		args   []string
		want   []string
	}{
		{name: "default list", config: "render:\n  workspace: [first, second]\n", want: []string{"first", "second"}},
		{name: "explicit overrides default", config: "render:\n  workspace: [missing]\n", args: []string{"second"}, want: []string{"second"}},
		{name: "explicit multiple", args: []string{"second", "first"}, want: []string{"second", "first"}},
		{name: "explicit with empty list", config: "render:\n  workspace: []\n", args: []string{"first"}, want: []string{"first"}},
		{name: "empty list", config: "render:\n  workspace: []\n"},
		{name: "absent list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := workspaceRenderProject(t, tc.config)
			for _, name := range []string{"first", "second"} {
				setupWorkspacePack(t, root, name, map[string]string{
					"manifest.yml": "render:\n  - from: root.tmpl\n    to: " + name + ".txt\n",
					"root.tmpl":    "{{ .Project.Name }} service={{ .Service }} resolved={{ .Resolved }} container={{ .ServiceCfg.Container }}\n",
				})
			}
			cmd := newWorkspaceCmd(&cmdctx.RootFlags{ConfigPath: filepath.Join(root, "workspace.yml")})
			out, err := captureStdout(t, func() error { return cmd.RunE(cmd, tc.args) })
			if err != nil {
				t.Fatalf("RunE: %v", err)
			}
			for _, name := range []string{"first", "second"} {
				path := filepath.Join(root, name+".txt")
				if slices.Contains(tc.want, name) {
					assertRenderedIndex(t, path, "test-project service= resolved= container=\n")
					if !strings.Contains(unwrapWriterOutput(out), "workspace ["+name+"] → "+name+".txt") {
						t.Errorf("missing file result: %s", out)
					}
				} else if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("unexpected output %s: %v", path, err)
				}
			}
			if len(tc.want) == 0 && !strings.Contains(out, "no workspace packs configured in render.workspace") {
				t.Errorf("missing empty-list message: %s", out)
			}
		})
	}
}

func TestNewWorkspaceCmd_errorsLeaveDestinationsUntouched(t *testing.T) {
	for _, tc := range []struct {
		name string
		dest string
		args []string
		want string
	}{
		{name: "listed pack missing", want: "stat workspace pack \"second\""},
		{name: "explicit pack missing", args: []string{"first", "missing"}, want: "stat workspace pack \"missing\""},
		{name: "protected path", dest: "workspace.yml", want: "protected"},
		{name: "duplicate packs", args: []string{"first", "first"}, want: "duplicated"},
		{name: "invalid pack", args: []string{"../outside"}, want: "identifier-safe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := workspaceRenderProject(t, "render:\n  workspace: [first, second]\n")
			original, err := os.ReadFile(filepath.Join(root, "workspace.yml"))
			if err != nil {
				t.Fatal(err)
			}
			setupWorkspacePack(t, root, "first", map[string]string{
				"manifest.yml": "render:\n  - from: root.tmpl\n    to: first.txt\n",
				"root.tmpl":    "new content",
			})
			writeWorkspaceFixture(t, root, "first.txt", "existing content")
			if tc.dest != "" {
				setupWorkspacePack(t, root, "second", map[string]string{
					"manifest.yml": "render:\n  - from: root.tmpl\n    to: " + tc.dest + "\n",
					"root.tmpl":    "bad overwrite",
				})
			}
			cmd := newWorkspaceCmd(&cmdctx.RootFlags{ConfigPath: filepath.Join(root, "workspace.yml")})
			out, err := captureStdout(t, func() error { return cmd.RunE(cmd, tc.args) })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RunE = %v, want %q", err, tc.want)
			}
			if out != "" {
				t.Errorf("failed plan reported output: %s", out)
			}
			assertRenderedIndex(t, filepath.Join(root, "first.txt"), "existing content")
			data, err := os.ReadFile(filepath.Join(root, "workspace.yml"))
			if err != nil || !bytes.Equal(data, original) {
				t.Errorf("workspace.yml changed: %q, %v", data, err)
			}
		})
	}
}

func TestNewWorkspaceCmd_commandIndex(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "declared commands", true: "broken commands"}[broken], func(t *testing.T) {
			root := workspaceRenderProject(t, "render:\n  workspace: [first, second]\n")
			commands := "group:\n  description: Tools\ncommands:\n  lint:\n    type: shell\n    cmd: echo lint\n"
			wantIndex := "commands=1 groups=1"
			if broken {
				commands = "commands: [unclosed\n"
				wantIndex = "commands=0 groups=0"
			}
			writeWorkspaceFixture(t, root, "workspace/commands/tools.yml", commands)
			for _, name := range []string{"first", "second"} {
				setupWorkspacePack(t, root, name, map[string]string{
					"manifest.yml": "render:\n  - from: root.tmpl\n    to: " + name + ".txt\n",
					"root.tmpl":    "commands={{ len .Commands }} groups={{ len .CommandGroups }}\n",
				})
			}
			cmd := newWorkspaceCmd(&cmdctx.RootFlags{ConfigPath: filepath.Join(root, "workspace.yml")})
			out, err := captureStdout(t, func() error { return cmd.RunE(cmd, nil) })
			if err != nil {
				t.Fatal(err)
			}
			wantWarnings := 0
			if broken {
				wantWarnings = 1
			}
			if got := strings.Count(out, commandIndexWarning); got != wantWarnings {
				t.Errorf("warnings = %d, want %d: %s", got, wantWarnings, out)
			}
			for _, name := range []string{"first", "second"} {
				assertRenderedIndex(t, filepath.Join(root, name+".txt"), wantIndex)
			}
		})
	}
}

func TestNewWorkspaceCmd_overrideAndSymlinkOutput(t *testing.T) {
	root := workspaceRenderProject(t, "")
	setupWorkspacePack(t, root, "root-agents", map[string]string{
		"manifest.yml":   "render:\n  - from: AGENTS.md.tmpl\n    to: AGENTS.md\nsymlinks:\n  - link: CLAUDE.md\n    to: AGENTS.md\n",
		"AGENTS.md.tmpl": "canonical",
	})
	setupWorkspacePack(t, root, "root-agents.local", map[string]string{"AGENTS.md.tmpl": "local"})
	cmd := newWorkspaceCmd(&cmdctx.RootFlags{ConfigPath: filepath.Join(root, "workspace.yml")})
	out, err := captureStdout(t, func() error { return cmd.RunE(cmd, []string{"root-agents"}) })
	if err != nil {
		t.Fatal(err)
	}
	assertRenderedIndex(t, filepath.Join(root, "AGENTS.md"), "local")
	if !strings.Contains(out, "using local override") || !strings.Contains(out, "CLAUDE.md (symlink)") {
		t.Errorf("missing override or symlink output: %s", out)
	}
	if target, err := os.Readlink(filepath.Join(root, "CLAUDE.md")); err != nil || target != "AGENTS.md" {
		t.Errorf("symlink = %q, %v", target, err)
	}
}

func TestNewWorkspaceCmd_completion(t *testing.T) {
	root := workspaceRenderProject(t, "render:\n  workspace: [missing]\n")
	for _, name := range []string{"zeta", "alpha", "alpha.local", ".hidden", "invalid.name"} {
		setupWorkspacePack(t, root, name, map[string]string{"manifest.yml": "render: []\n"})
	}
	packDir := filepath.Join(root, "workspace", "templates", "workspace")
	writeWorkspaceFixture(t, packDir, "file", "not a pack")
	if err := os.Symlink(filepath.Join(packDir, "alpha"), filepath.Join(packDir, "linked")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		prefix   string
		args     []string
		explicit bool
		want     []string
	}{
		{name: "discover from subdirectory", want: []string{"alpha", "zeta"}},
		{name: "filter prefix", prefix: "a", want: []string{"alpha"}},
		{name: "exclude selected", args: []string{"alpha"}, want: []string{"zeta"}},
		{name: "explicit config outside project", explicit: true, want: []string{"alpha", "zeta"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd := filepath.Join(root, "workspace")
			if tc.explicit {
				cwd = t.TempDir()
			}
			t.Chdir(cwd)
			flags := &cmdctx.RootFlags{}
			parent := &cobra.Command{Use: "dwe"}
			parent.PersistentFlags().StringVar(&flags.ConfigPath, "config", "", "")
			if tc.explicit {
				if err := parent.PersistentFlags().Set("config", filepath.Join(root, "workspace.yml")); err != nil {
					t.Fatal(err)
				}
			}
			cmd := newWorkspaceCmd(flags)
			parent.AddCommand(cmd)
			var got []string
			var directive cobra.ShellCompDirective
			out, err := captureStdout(t, func() error {
				got, directive = cmd.ValidArgsFunction(cmd, tc.args, tc.prefix)
				return nil
			})
			if err != nil || out != "" || directive != cobra.ShellCompDirectiveNoFileComp || !slices.Equal(got, tc.want) {
				t.Errorf("completion = %v, %v, stdout=%q, err=%v; want %v", got, directive, out, err, tc.want)
			}
		})
	}
}

func TestNewWorkspaceCmd_completionErrorsAreSilent(t *testing.T) {
	for _, reason := range []string{"no project", "invalid config", "missing directory", "symlinked directory"} {
		t.Run(reason, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			if reason != "no project" {
				body := "schema_version: \"2\"\nproject:\n  name: test-project\n"
				if reason == "invalid config" {
					body += "unknown_key: true\n"
				}
				writeWorkspaceFixture(t, root, "workspace.yml", body)
			}
			if reason == "symlinked directory" {
				writeWorkspaceFixture(t, root, "workspace/templates/placeholder", "")
				if err := os.Symlink(t.TempDir(), filepath.Join(root, "workspace", "templates", "workspace")); err != nil {
					t.Fatal(err)
				}
			}
			cmd := newWorkspaceCmd(&cmdctx.RootFlags{})
			out, err := captureStdout(t, func() error {
				got, directive := cmd.ValidArgsFunction(cmd, nil, "")
				if len(got) != 0 || directive != cobra.ShellCompDirectiveNoFileComp {
					t.Errorf("completion = %v, %v", got, directive)
				}
				return nil
			})
			if out != "" || err != nil {
				t.Errorf("completion emitted %q, err=%v", out, err)
			}
		})
	}
}

func TestRenderCmd_registersWorkspace(t *testing.T) {
	cmd := NewCmd("", &cmdctx.RootFlags{})
	child, _, err := cmd.Find([]string{"workspace"})
	if err != nil || child.Name() != "workspace" || child.RunE == nil {
		t.Fatalf("workspace command missing: %v", err)
	}
}
