package render

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

const commandIndexWarning = "command index unavailable"

// captureStdout runs fn with os.Stdout redirected to a pipe. render.Stdout()
// resolves os.Stdout at call time, so the swap reaches the command's writer.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan []byte)
	go func() {
		data, _ := io.ReadAll(r)
		done <- data
	}()
	runErr := fn()
	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return string(out), runErr
}

// writeCommandIndexRenderProject builds a two-service project carrying an ai,
// ide and git pack whose single template prints the index sizes, plus the
// given workspace/commands/tools.yml body.
func writeCommandIndexRenderProject(t *testing.T, commandFile string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "workspace.yml"), []byte("schema_version: \"2\"\nproject:\n  name: p\nservices:\n  api:\n    enabled: true\n  web:\n    enabled: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setupServicesConfig(t, root, `
services:
  api:
    type: app
    dir: services/api
    container: p-api
  web:
    type: app
    dir: services/web
    container: p-web
`)
	const tmpl = "commands={{ len .Commands }} groups={{ len .CommandGroups }}\n"
	setupAgentsPackTemplates(t, root, "default", map[string]string{
		"manifest.yml":   "render:\n  - from: AGENTS.md.tmpl\n    to: AGENTS.md\n",
		"AGENTS.md.tmpl": tmpl,
	})
	setupIDEPackTemplates(t, root, "default", map[string]string{
		"manifest.yml":   "render:\n  - from: index.txt.tmpl\n    to: index.txt\n",
		"index.txt.tmpl": tmpl,
	})
	setupGitPack(t, root, "default", map[string]string{
		"manifest.yml":    "render:\n  - from: pre-commit.tmpl\n    to: pre-commit\n",
		"pre-commit.tmpl": tmpl,
	})
	for _, svc := range []string{"api", "web"} {
		mkGitDir(t, filepath.Join(root, "services", svc))
	}
	dir := filepath.Join(root, "workspace", "commands")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools.yml"), []byte(commandFile), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

var commandIndexRenderCmds = []struct {
	name    string
	newCmd  func(*cmdctx.RootFlags) *cobra.Command
	outputs []string // relative to the service dir
}{
	{name: "ai", newCmd: newAICmd, outputs: []string{"AGENTS.md"}},
	{name: "ide", newCmd: newIDECmd, outputs: []string{"index.txt"}},
	{name: "git", newCmd: newGitCmd, outputs: []string{filepath.Join("src", ".git", "hooks", "pre-commit")}},
}

// TestRenderCommands_BrokenCommandFileWarnsOnce pins the render failure policy:
// an unloadable command file does not fail ai/ide/git, warns exactly once per
// invocation (not once per service) on stdout naming the file, and renders
// with an empty index.
func TestRenderCommands_BrokenCommandFileWarnsOnce(t *testing.T) {
	for _, tc := range commandIndexRenderCmds {
		t.Run(tc.name, func(t *testing.T) {
			root := writeCommandIndexRenderProject(t, "commands: [unclosed\n")
			flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(root, "workspace.yml")}
			cmd := tc.newCmd(flags)

			out, err := captureStdout(t, func() error { return cmd.RunE(cmd, nil) })
			if err != nil {
				t.Fatalf("RunE: %v", err)
			}
			if n := strings.Count(out, commandIndexWarning); n != 1 {
				t.Fatalf("warning count = %d, want 1; stdout:\n%s", n, out)
			}
			// Writer.Warning hard-wraps words longer than the line (the absolute
			// temp path is), so match against the output with the wrap undone.
			if !strings.Contains(unwrapWriterOutput(out), "workspace/commands/tools.yml") {
				t.Errorf("warning does not name the command file; stdout:\n%s", out)
			}
			for _, svc := range []string{"api", "web"} {
				for _, rel := range tc.outputs {
					assertRenderedIndex(t, filepath.Join(root, "services", svc, rel), "commands=0 groups=0")
				}
			}
		})
	}
}

// TestRenderCommands_ThreadCommandIndex is the positive control for the test
// above: with a loadable command file every service's render sees the index,
// so "commands=0" there is the failure policy and not a missing thread.
func TestRenderCommands_ThreadCommandIndex(t *testing.T) {
	const commands = "group:\n  description: Tools\ncommands:\n  lint:\n    type: shell\n    cmd: echo lint\n  test:\n    type: shell\n    cmd: echo test\n"
	for _, tc := range commandIndexRenderCmds {
		t.Run(tc.name, func(t *testing.T) {
			root := writeCommandIndexRenderProject(t, commands)
			flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(root, "workspace.yml")}
			cmd := tc.newCmd(flags)

			out, err := captureStdout(t, func() error { return cmd.RunE(cmd, nil) })
			if err != nil {
				t.Fatalf("RunE: %v", err)
			}
			if strings.Contains(out, commandIndexWarning) {
				t.Errorf("unexpected warning; stdout:\n%s", out)
			}
			for _, svc := range []string{"api", "web"} {
				for _, rel := range tc.outputs {
					assertRenderedIndex(t, filepath.Join(root, "services", svc, rel), "commands=2 groups=1")
				}
			}
		})
	}
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func unwrapWriterOutput(s string) string {
	return strings.ReplaceAll(ansiEscape.ReplaceAllString(s, ""), "\n", "")
}

func assertRenderedIndex(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.Contains(data, []byte(want)) {
		t.Errorf("%s = %q, want it to contain %q", path, data, want)
	}
}
