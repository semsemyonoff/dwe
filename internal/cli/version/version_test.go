package version_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/semsemyonoff/devbox/internal/cli/cmdctx"
	"github.com/semsemyonoff/devbox/internal/cli/version"
	versioninfo "github.com/semsemyonoff/devbox/internal/shared/version"

	"github.com/spf13/cobra"
)

// setTestVersion pins the version variables to fixed values for the test and
// restores originals via t.Cleanup.
func setTestVersion(t *testing.T) {
	t.Helper()
	origV, origC, origD, origB := versioninfo.Version, versioninfo.Commit, versioninfo.Date, versioninfo.BuiltBy
	versioninfo.Version = "1.2.3"
	versioninfo.Commit = "abc1234"
	versioninfo.Date = "2026-01-01T00:00:00Z"
	versioninfo.BuiltBy = "make"
	t.Cleanup(func() {
		versioninfo.Version = origV
		versioninfo.Commit = origC
		versioninfo.Date = origD
		versioninfo.BuiltBy = origB
	})
}

// runVersionCmd creates a fresh root+version command pair, sets args, and
// returns stdout output.
func runVersionCmd(t *testing.T, flags *cmdctx.RootFlags, args ...string) string {
	t.Helper()
	root := &cobra.Command{Use: "devbox", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(version.NewCmd("", flags))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return out.String()
}

func TestVersionCmd_TextMode(t *testing.T) {
	setTestVersion(t)
	flags := &cmdctx.RootFlags{Output: "text"}
	got := runVersionCmd(t, flags, "version")
	if !strings.Contains(got, "Devbox v1.2.3") {
		t.Errorf("text output should contain version; got: %q", got)
	}
	if !strings.Contains(got, "abc1234") {
		t.Errorf("text output should contain commit; got: %q", got)
	}
}

func TestVersionCmd_JSONMode_Golden(t *testing.T) {
	setTestVersion(t)
	flags := &cmdctx.RootFlags{Output: "json"}
	got := strings.TrimRight(runVersionCmd(t, flags, "version"), "\n")

	goldenPath := "testdata/version.json.golden"
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(goldenPath, []byte(got+"\n"), 0644); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
		t.Logf("updated golden: %s", goldenPath)
		return
	}

	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden %s: %v", goldenPath, err)
	}
	want := strings.TrimRight(string(raw), "\n")
	if got != want {
		t.Errorf("JSON output mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestVersionCmd_JSONMode_PrettyFlag(t *testing.T) {
	setTestVersion(t)
	flags := &cmdctx.RootFlags{Output: "json", Pretty: true}
	got := runVersionCmd(t, flags, "version")
	// Pretty output must be multi-line and contain indented fields.
	if !strings.Contains(got, "\n  ") {
		t.Errorf("pretty JSON should contain indented lines; got: %q", got)
	}
	if !strings.Contains(got, `"version": "1.2.3"`) {
		t.Errorf("pretty JSON should contain version field; got: %q", got)
	}
}
