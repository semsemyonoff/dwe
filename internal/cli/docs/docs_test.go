package docs

import (
	"bytes"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/stretchr/testify/require"
)

func TestDocsRootNonTTY(t *testing.T) {
	// Without a TTY (the test process stdout is a pipe under `go test`) bare
	// `dwe docs` falls back to the `docs list` output instead of erroring.
	cmd := NewCmd("", &cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		Locale:     "en",
		I18n:       nil,
	})

	outBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)

	errBuf := &bytes.Buffer{}
	cmd.SetErr(errBuf)

	require.NoError(t, cmd.Execute(), "bare docs without a TTY must fall back to list")
	require.NotEmpty(t, outBuf.String(), "fallback must print the docs list")
}

func TestDocsRootWithArgs(t *testing.T) {
	// Test that docs with arguments bypasses the parent RunE
	cmd := NewCmd("", &cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		Locale:     "en",
		I18n:       nil,
	})

	// Adding subcommands should work
	require.NotNil(t, cmd)
	require.Equal(t, "docs", cmd.Use)

	// Verify subcommands are registered
	subcommands := []string{"show", "list", "export", "cache", "generate"}
	for _, subcmd := range subcommands {
		found := false
		for _, cmd := range cmd.Commands() {
			if cmd.Name() == subcmd {
				found = true
				break
			}
		}
		require.True(t, found, "subcommand %s not found", subcmd)
	}
}

func TestDocsRootStructure(t *testing.T) {
	// Test that the docs command is properly configured
	cmd := NewCmd("", &cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		Locale:     "en",
		I18n:       nil,
	})

	require.NotNil(t, cmd)
	require.Equal(t, "docs", cmd.Use)
	require.NotNil(t, cmd.RunE)
	// Verify Args are set to NoArgs by checking that the command accepts no arguments
	require.NotNil(t, cmd.Args)
}

func TestDocsShowRegressionCheck(t *testing.T) {
	// Regression test: ensure existing docs generate subcommand still works
	cmd := NewCmd("", &cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		Locale:     "en",
		I18n:       nil,
	})

	generateCmd := cmd.Commands()
	found := false
	for _, subcmd := range generateCmd {
		if subcmd.Name() == "generate" {
			found = true
			require.NotNil(t, subcmd.RunE, "docs generate should have RunE")
			break
		}
	}
	require.True(t, found, "docs generate command not found")
}

// TestDocsBare_NonInteractiveFallsBackToList: bare `dwe docs` without a TTY
// for the browser — here forced via DWE_NONINTERACTIVE=1, which the bridge
// daemon sets for every container invocation — prints the `docs list` output
// instead of erroring, mirroring bare `dwe commands`.
func TestDocsBare_NonInteractiveFallsBackToList(t *testing.T) {
	t.Setenv("DWE_NONINTERACTIVE", "1")

	cmd := NewCmd("", &cmdctx.RootFlags{Locale: "en"})
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{})

	require.NoError(t, cmd.Execute(), "bare docs must fall back to list, not error")

	output := strings.TrimSpace(out.String())
	require.NotEmpty(t, output, "fallback must print the docs list")
	for line := range strings.SplitSeq(output, "\n") {
		if line == "" {
			continue
		}
		require.Len(t, strings.Split(line, "\t"), 3,
			"fallback output must be the tab-separated list format: %q", line)
	}
}
