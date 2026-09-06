package deploy

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy"

	"github.com/spf13/cobra"
)

// runEject drives `dwe deploy eject` the way the root command would, returning
// stdout, stderr and the command error.
func runEject(t *testing.T, root string, output string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	flags := &cmdctx.RootFlags{
		ConfigPath: filepath.Join(root, "workspace.yml"),
		Root:       root,
		Output:     output,
	}
	cmd := newDeployEjectCmd(flags)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// codedError extracts the typed envelope so a test can pin the error code
// rather than a substring of the message.
func codedError(t *testing.T, err error) *cmdctx.CodedError {
	t.Helper()
	var coded *cmdctx.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("err = %v (%T), want *cmdctx.CodedError", err, err)
	}
	return coded
}

func TestDeployEject_Stdout(t *testing.T) {
	root := t.TempDir()
	stdout, stderr, err := runEject(t, root, "text")
	if err != nil {
		t.Fatalf("eject: %v", err)
	}
	if stdout != string(deploy.DefaultDeployYAML()) {
		t.Fatalf("stdout does not match the asset verbatim:\n%s", stdout)
	}
	for _, want := range []string{"name: services", "name: start", "name: post-deploy", "# workspace/deploy.yml"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty on the stdout path", stderr)
	}
}

func TestDeployEject_OutDashIsStdoutAndCreatesNoFile(t *testing.T) {
	root := t.TempDir()
	stdout, _, err := runEject(t, root, "text", "--out", "-")
	if err != nil {
		t.Fatalf("eject: %v", err)
	}
	if stdout != string(deploy.DefaultDeployYAML()) {
		t.Fatalf("stdout does not match the asset verbatim:\n%s", stdout)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("eject --out - created %d entries, want none", len(entries))
	}
}

func TestDeployEject_EmptyOutRejected(t *testing.T) {
	root := t.TempDir()
	_, _, err := runEject(t, root, "text", "--out", "")
	if err == nil {
		t.Fatal("err = nil, want a rejection of an empty --out")
	}
	if got := codedError(t, err).Code; got != "deploy_eject_path_invalid" {
		t.Fatalf("code = %q, want deploy_eject_path_invalid", got)
	}
}

func TestDeployEject_WritesLoadableFile(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "workspace", "deploy.yml")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stdout, stderr, err := runEject(t, root, "text", "--out", dst)
	if err != nil {
		t.Fatalf("eject: %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty on the --out path", stdout)
	}
	if !strings.Contains(stderr, dst) {
		t.Fatalf("stderr does not name the written path: %q", stderr)
	}

	// The load-bearing assertion: the emitted file goes back through the real
	// strict loader and yields the built-in default.
	loaded, err := config.LoadProjectDeployConfig(dst)
	if err != nil {
		t.Fatalf("load written deploy.yml: %v", err)
	}
	want := deploy.DefaultDeployConfig()
	if len(loaded.Phases) != len(want.Phases) {
		t.Fatalf("phases = %d, want %d", len(loaded.Phases), len(want.Phases))
	}
	if !loaded.LogEnabled() {
		t.Fatal("LogEnabled() = false, want true")
	}
}

func TestDeployEject_RefusesExistingFile(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantNote string
	}{
		{
			name:     "all-comment file is inert",
			content:  "# nothing active here\n",
			wantNote: "no active content",
		},
		{
			name:     "log-only file is inert too",
			content:  "log: false\n",
			wantNote: "declares no phases",
		},
		{
			name:     "authored file gets no inert note",
			content:  "phases:\n  - name: build\n    steps:\n      - name: noop\n        type: shell\n        cmd: \"true\"\n",
			wantNote: "",
		},
		{
			name: "unparseable file still refuses as already-here",
			// A file that does not load must never surface its parse error as if
			// it were a write failure.
			content:  "phases: [\n",
			wantNote: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			// The project's own workspace/deploy.yml: the only target an inert
			// note may describe (see existingDeployNote).
			dst := filepath.Join(root, "workspace", "deploy.yml")
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(dst, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			_, _, err := runEject(t, root, "text", "--out", dst)
			if err == nil {
				t.Fatal("err = nil, want a refusal")
			}
			coded := codedError(t, err)
			if coded.Code != "deploy_eject_output_exists" {
				t.Fatalf("code = %q, want deploy_eject_output_exists", coded.Code)
			}
			if !strings.Contains(coded.Message, dst) {
				t.Fatalf("message does not name the path: %q", coded.Message)
			}
			if tc.wantNote == "" {
				if strings.Contains(coded.Message, "—") {
					t.Fatalf("message carries an inert note it should not: %q", coded.Message)
				}
			} else if !strings.Contains(coded.Message, tc.wantNote) {
				t.Fatalf("message = %q, want it to contain %q", coded.Message, tc.wantNote)
			}
			if coded.Hint == "" {
				t.Fatal("hint is empty, want the --force suggestion")
			}

			after, readErr := os.ReadFile(dst)
			if readErr != nil {
				t.Fatalf("read back: %v", readErr)
			}
			if string(after) != tc.content {
				t.Fatalf("existing file changed:\n%s", after)
			}
		})
	}
}

// TestDeployEject_NoInertNoteOffCanonicalPath pins the note's scope: it claims
// the built-in default "is what runs today", so it may only describe the
// project's own workspace/deploy.yml. An inert scratch file elsewhere is still
// refused — just without a sentence about a pipeline dwe never reads there.
func TestDeployEject_NoInertNoteOffCanonicalPath(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "scratch.yml")
	if err := os.WriteFile(dst, []byte("# nothing active here\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, _, err := runEject(t, root, "text", "--out", dst)
	if err == nil {
		t.Fatal("err = nil, want a refusal")
	}
	coded := codedError(t, err)
	if coded.Code != "deploy_eject_output_exists" {
		t.Fatalf("code = %q, want deploy_eject_output_exists", coded.Code)
	}
	if strings.Contains(coded.Message, "—") {
		t.Fatalf("a non-canonical target must carry no inert note: %q", coded.Message)
	}
}

func TestDeployEject_ForceOverwrites(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "deploy.yml")
	if err := os.WriteFile(dst, []byte("# inert\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, _, err := runEject(t, root, "text", "--out", dst, "--force"); err != nil {
		t.Fatalf("eject --force: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(deploy.DefaultDeployYAML()) {
		t.Fatalf("file was not overwritten with the asset:\n%s", got)
	}
}

func TestDeployEject_JSONStdoutHasNoEnvelope(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{nil, {"--out", "-"}} {
		stdout, stderr, err := runEject(t, root, "json", args...)
		if err != nil {
			t.Fatalf("eject %v: %v", args, err)
		}
		// The document is the payload in json mode too: no envelope, no wrapping.
		if stdout != string(deploy.DefaultDeployYAML()) {
			t.Fatalf("eject %v: stdout is not the raw document:\n%s", args, stdout)
		}
		if stderr != "" {
			t.Fatalf("eject %v: stderr = %q, want empty", args, stderr)
		}
	}
}

func TestDeployEject_JSONWriteEmitsPayload(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "deploy.yml")
	stdout, stderr, err := runEject(t, root, "json", "--out", dst)
	if err != nil {
		t.Fatalf("eject: %v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want the confirmation suppressed in json mode", stderr)
	}
	var payload ejectJSON
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("decode %q: %v", stdout, err)
	}
	if payload.Path != dst || payload.Pipeline != "deploy" {
		t.Fatalf("payload = %+v, want {Path:%s Pipeline:deploy}", payload, dst)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("stat written file: %v", err)
	}
}

func TestDeployEject_RegisteredOnDeployCommand(t *testing.T) {
	cmd := NewCmd("", &cmdctx.RootFlags{})
	var found *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Name() == "eject" {
			found = sub
		}
	}
	if found == nil {
		t.Fatal("`dwe deploy eject` is not registered")
	}
	if found.Args == nil {
		t.Fatal("eject accepts positional args, want cobra.NoArgs")
	}
	if !strings.Contains(found.Long, "built-in default") {
		t.Fatalf("Long does not state the built-in-default scope: %q", found.Long)
	}
}
