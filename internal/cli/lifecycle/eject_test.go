package lifecycle

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
	"github.com/semsemyonoff/dwe/internal/core/workflow/reset"

	"github.com/spf13/cobra"
)

// runResetEjectCmd drives `dwe reset eject` the way the root command would,
// returning stdout, stderr and the command error.
func runResetEjectCmd(t *testing.T, root string, output string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	flags := &cmdctx.RootFlags{
		ConfigPath: filepath.Join(root, "workspace.yml"),
		Root:       root,
		Output:     output,
	}
	cmd := newResetEjectCmd(flags)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// ejectCodedError extracts the typed envelope so a test can pin the error code
// rather than a substring of the message.
func ejectCodedError(t *testing.T, err error) *cmdctx.CodedError {
	t.Helper()
	var coded *cmdctx.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("err = %v (%T), want *cmdctx.CodedError", err, err)
	}
	return coded
}

func TestResetEject_Stdout(t *testing.T) {
	root := t.TempDir()
	stdout, stderr, err := runResetEjectCmd(t, root, "text")
	if err != nil {
		t.Fatalf("eject: %v", err)
	}
	if stdout != string(reset.DefaultResetYAML()) {
		t.Fatalf("stdout does not match the asset verbatim:\n%s", stdout)
	}
	for _, want := range []string{"name: pre", "name: stop", "name: cleanup", "# workspace/reset.yml"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty on the stdout path", stderr)
	}
}

func TestResetEject_OutDashIsStdoutAndCreatesNoFile(t *testing.T) {
	root := t.TempDir()
	stdout, _, err := runResetEjectCmd(t, root, "text", "--out", "-")
	if err != nil {
		t.Fatalf("eject: %v", err)
	}
	if stdout != string(reset.DefaultResetYAML()) {
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

func TestResetEject_EmptyOutRejected(t *testing.T) {
	root := t.TempDir()
	_, _, err := runResetEjectCmd(t, root, "text", "--out", "")
	if err == nil {
		t.Fatal("err = nil, want a rejection of an empty --out")
	}
	if got := ejectCodedError(t, err).Code; got != "reset_eject_path_invalid" {
		t.Fatalf("code = %q, want reset_eject_path_invalid", got)
	}
}

func TestResetEject_WritesLoadableFile(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "workspace", "reset.yml")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stdout, stderr, err := runResetEjectCmd(t, root, "text", "--out", dst)
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
	loaded, err := config.LoadResetConfig(dst)
	if err != nil {
		t.Fatalf("load written reset.yml: %v", err)
	}
	want := reset.DefaultResetConfig()
	if len(loaded.Phases) != len(want.Phases) {
		t.Fatalf("phases = %d, want %d", len(loaded.Phases), len(want.Phases))
	}
}

// TestResetEject_WrittenFileHasLoggingOff pins what a user actually gets from an
// ejected reset pipeline: no .dwe/logs/reset.log.
//
// The value comes from the asset's explicit `log: false`, NOT from the loader's
// defaultLog — loadProjectDeployConfigDecode applies defaultLog only when
// cfg.Log == nil (workspace.go:3200-3203), and here it is not. That is exactly
// why the asset must keep the key: drop it and the emitted file's behaviour
// silently depends on which loader reads it (LoadProjectDeployConfig defaults
// log to true, LoadResetConfig to false).
func TestResetEject_WrittenFileHasLoggingOff(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "reset.yml")
	if _, _, err := runResetEjectCmd(t, root, "text", "--out", dst); err != nil {
		t.Fatalf("eject: %v", err)
	}
	if !strings.Contains(string(reset.DefaultResetYAML()), "log: false") {
		t.Fatal("the asset no longer declares log: false explicitly")
	}
	loaded, err := config.LoadResetConfig(dst)
	if err != nil {
		t.Fatalf("load written reset.yml: %v", err)
	}
	if loaded.LogEnabled() {
		t.Fatal("LogEnabled() = true, want false for an ejected reset pipeline")
	}
	// Read through the deploy-side loader, whose defaultLog is true: the key is
	// what keeps the answer the same either way.
	viaDeployLoader, err := config.LoadProjectDeployConfig(dst)
	if err != nil {
		t.Fatalf("load written reset.yml via the deploy loader: %v", err)
	}
	if viaDeployLoader.LogEnabled() {
		t.Fatal("LogEnabled() = true through the deploy loader; the explicit log: false key was lost")
	}
}

func TestResetEject_RefusesExistingFile(t *testing.T) {
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
			content:  "phases:\n  - name: teardown\n    steps:\n      - name: noop\n        type: shell\n        cmd: \"true\"\n",
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
			dst := filepath.Join(root, "reset.yml")
			if err := os.WriteFile(dst, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			_, _, err := runResetEjectCmd(t, root, "text", "--out", dst)
			if err == nil {
				t.Fatal("err = nil, want a refusal")
			}
			coded := ejectCodedError(t, err)
			if coded.Code != "reset_eject_output_exists" {
				t.Fatalf("code = %q, want reset_eject_output_exists", coded.Code)
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

func TestResetEject_ForceOverwrites(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "reset.yml")
	if err := os.WriteFile(dst, []byte("# inert\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, _, err := runResetEjectCmd(t, root, "text", "--out", dst, "--force"); err != nil {
		t.Fatalf("eject --force: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(reset.DefaultResetYAML()) {
		t.Fatalf("file was not overwritten with the asset:\n%s", got)
	}
}

func TestResetEject_JSONStdoutHasNoEnvelope(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{nil, {"--out", "-"}} {
		stdout, stderr, err := runResetEjectCmd(t, root, "json", args...)
		if err != nil {
			t.Fatalf("eject %v: %v", args, err)
		}
		// The document is the payload in json mode too: no envelope, no wrapping.
		if stdout != string(reset.DefaultResetYAML()) {
			t.Fatalf("eject %v: stdout is not the raw document:\n%s", args, stdout)
		}
		if stderr != "" {
			t.Fatalf("eject %v: stderr = %q, want empty", args, stderr)
		}
	}
}

func TestResetEject_JSONWriteEmitsPayload(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "reset.yml")
	stdout, stderr, err := runResetEjectCmd(t, root, "json", "--out", dst)
	if err != nil {
		t.Fatalf("eject: %v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want the confirmation suppressed in json mode", stderr)
	}
	var payload resetEjectJSON
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("decode %q: %v", stdout, err)
	}
	if payload.Path != dst || payload.Pipeline != "reset" {
		t.Fatalf("payload = %+v, want {Path:%s Pipeline:reset}", payload, dst)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("stat written file: %v", err)
	}
}

func TestResetEject_RegisteredOnResetCommand(t *testing.T) {
	cmd := NewResetCmd("", &cmdctx.RootFlags{})
	var found *cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Name() == "eject" {
			found = sub
		}
	}
	if found == nil {
		t.Fatal("`dwe reset eject` is not registered")
	}
	if found.Args == nil {
		t.Fatal("eject accepts positional args, want cobra.NoArgs")
	}
	if !strings.Contains(found.Long, "built-in default") {
		t.Fatalf("Long does not state the built-in-default scope: %q", found.Long)
	}
}
