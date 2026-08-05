package command

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
)

// setupListProject scaffolds a minimal project with two command groups and
// returns the workspace.yml path.
func setupListProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(cfgPath, []byte("schema_version: \"2\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmdDir := filepath.Join(dir, "workspace", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "db.yml"), []byte("commands:\n  up:\n    type: shell\n    cmd: echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "app.yml"), []byte("commands:\n  build:\n    type: shell\n    cmd: echo build\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

// stubInteractive overrides widgets.IsInteractiveFn for the test duration.
// Tests using it must not call t.Parallel() (global seam).
func stubInteractive(t *testing.T, interactive bool) {
	t.Helper()
	orig := widgets.IsInteractiveFn
	t.Cleanup(func() { widgets.IsInteractiveFn = orig })
	widgets.IsInteractiveFn = func(io.Reader) bool { return interactive }
}

// runBare invokes the `dwe commands` RunE with the given args and returns
// captured stdout and the error.
func runBare(t *testing.T, flags *cmdctx.RootFlags, args []string) (string, error) {
	t.Helper()
	cmd := NewCmd("", flags)
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.RunE(cmd, args)
	return out.String(), err
}

// TestCommandsGroupPrefix_nonTTY_refusesPassThrough closes the other half of
// the silent-discard hole commandIDArgs' `near == 0` guard opens on.
//
// A group prefix is not an exact id, so the run route reaches the selector; the
// non-interactive fallback (CI pipe, or any container — the bridge daemon
// force-sets DWE_NONINTERACTIVE=1) used to print the command list and return
// nil, dropping the caller's arguments and exiting 0.
//
// This goes through cobra's own parse rather than calling RunE directly:
// ArgsLenAtDash() is only populated by a real Execute, and it is the split the
// whole guard depends on.
func TestCommandsGroupPrefix_nonTTY_refusesPassThrough(t *testing.T) {
	cfgPath := setupListProject(t)
	stubInteractive(t, false)

	cmd := NewCmd("", &cmdctx.RootFlags{ConfigPath: cfgPath})
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"db", "--", "--run", "x.ts"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("a group prefix with pass-through args must fail, not list and exit 0")
	}
	for _, want := range []string{"exact command id", `"db"`, "--run x.ts", "dwe commands list db"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}
	if strings.Contains(out.String(), "db.up") {
		t.Errorf("the command list must not be printed instead of the error:\n%s", out.String())
	}
}

// A group prefix WITHOUT pass-through args keeps listing — the guard must not
// break bare `dwe cmd <group>` in CI.
func TestCommandsGroupPrefix_nonTTY_stillListsWithoutPassThrough(t *testing.T) {
	cfgPath := setupListProject(t)
	stubInteractive(t, false)

	out, err := runBare(t, &cmdctx.RootFlags{ConfigPath: cfgPath}, []string{"db"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "db.up") {
		t.Errorf("expected the group listing, got:\n%s", out)
	}
}

func TestCommandsBare_nonTTY_printsList(t *testing.T) {
	cfgPath := setupListProject(t)
	stubInteractive(t, false)

	out, err := runBare(t, &cmdctx.RootFlags{ConfigPath: cfgPath}, nil)
	if err != nil {
		t.Fatalf("bare `dwe commands` without a TTY must list, not error: %v", err)
	}
	for _, want := range []string{"db.up", "app.build"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q\n---\n%s", want, out)
		}
	}
}

func TestCommandsBare_nonInteractiveEnv_printsList(t *testing.T) {
	cfgPath := setupListProject(t)

	for _, val := range []string{"1", "true"} {
		t.Run("DWE_NONINTERACTIVE="+val, func(t *testing.T) {
			// TTY present, but a truthy DWE_NONINTERACTIVE (the bridge daemon
			// force-sets it) must route to the list fallback all the same.
			stubInteractive(t, true)
			t.Setenv("DWE_NONINTERACTIVE", val)

			out, err := runBare(t, &cmdctx.RootFlags{ConfigPath: cfgPath}, nil)
			if err != nil {
				t.Fatalf("bare `dwe commands` with DWE_NONINTERACTIVE=%s must list, not error: %v", val, err)
			}
			if !strings.Contains(out, "db.up") {
				t.Errorf("list output missing db.up\n---\n%s", out)
			}
		})
	}
}

func TestCommandsBare_nonTTY_jsonList(t *testing.T) {
	cfgPath := setupListProject(t)
	stubInteractive(t, false)

	out, err := runBare(t, &cmdctx.RootFlags{ConfigPath: cfgPath, Output: "json"}, nil)
	if err != nil {
		t.Fatalf("bare `dwe commands --output json` must list, not error: %v", err)
	}
	var data struct {
		Commands []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"commands"`
	}
	if uerr := json.Unmarshal([]byte(out), &data); uerr != nil {
		t.Fatalf("stdout is not the commands list JSON: %v\n---\n%s", uerr, out)
	}
	ids := make([]string, 0, len(data.Commands))
	for _, c := range data.Commands {
		ids = append(ids, c.ID)
	}
	for _, want := range []string{"db.up", "app.build"} {
		if !strings.Contains(strings.Join(ids, " "), want) {
			t.Errorf("JSON list missing %q, got %v", want, ids)
		}
	}
}

func TestCommandsGroupPrefix_nonTTY_printsFilteredList(t *testing.T) {
	cfgPath := setupListProject(t)
	stubInteractive(t, false)

	out, err := runBare(t, &cmdctx.RootFlags{ConfigPath: cfgPath}, []string{"db"})
	if err != nil {
		t.Fatalf("group-prefix `dwe commands db` without a TTY must list, not error: %v", err)
	}
	if !strings.Contains(out, "db.up") {
		t.Errorf("filtered list missing db.up\n---\n%s", out)
	}
	if strings.Contains(out, "app.build") {
		t.Errorf("filtered list must not contain other groups\n---\n%s", out)
	}
}

func TestCommandsExactID_nonInteractiveEnv_stillRuns(t *testing.T) {
	cfgPath := setupListProject(t)
	stubInteractive(t, false)
	t.Setenv("DWE_NONINTERACTIVE", "1")

	origRun := runUserCommand
	t.Cleanup(func() { runUserCommand = origRun })
	ran := ""
	runUserCommand = func(_ context.Context, rc usercommands.RunContext) error {
		ran = rc.Cmd.ID
		return nil
	}

	out, err := runBare(t, &cmdctx.RootFlags{ConfigPath: cfgPath}, []string{"db.up"})
	if err != nil {
		t.Fatalf("exact ID must run in non-interactive mode: %v", err)
	}
	if ran != "db.up" {
		t.Errorf("expected db.up to run, got %q", ran)
	}
	if strings.Contains(out, "app.build") {
		t.Errorf("exact-ID run must not print the command list\n---\n%s", out)
	}
}

func TestNonInteractiveEnv_table(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"yes", false}, // only the documented {"1","true"} set is truthy
		{"1", true},
		{"true", true},
	}
	for _, tc := range cases {
		t.Run("val="+tc.val, func(t *testing.T) {
			t.Setenv("DWE_NONINTERACTIVE", tc.val)
			if got := nonInteractiveEnv(); got != tc.want {
				t.Errorf("nonInteractiveEnv() with %q = %v, want %v", tc.val, got, tc.want)
			}
		})
	}
}
