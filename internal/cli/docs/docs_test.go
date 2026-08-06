package docs

import (
	"bytes"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/spf13/cobra"

	"github.com/stretchr/testify/require"
)

func TestDocsRootNonTTY(t *testing.T) {
	// Bare `dwe docs` without a TTY falls back to the `docs list` output
	// instead of erroring. DWE_NONINTERACTIVE pins the non-interactive branch
	// deterministically — the ambient stdout is usually a pipe under
	// `go test`, but the test must not depend on how it was invoked.
	t.Setenv("DWE_NONINTERACTIVE", "1")
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

// TestDocsTopicWithoutShow pins the recovery path for the most common wrong
// guess — `dwe docs <topic>` instead of `dwe docs show <topic>`. dwe's own
// scaffolded AGENTS.md advertised the wrong form, so this is the shape agents
// actually type. The `--lang` variant is the load-bearing case: without a --lang
// flag on the parent, cobra fails flag parsing first and reports "Unknown flag:
// --lang", blaming the flag instead of the missing subcommand.
func TestDocsTopicWithoutShow(t *testing.T) {
	newCmd := func() *cobra.Command {
		return NewCmd("", &cmdctx.RootFlags{Locale: "en"})
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"bare topic", []string{"config/workspace"}},
		{"topic with --lang", []string{"config/workspace", "--lang", "en"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(tc.args)

			err := cmd.Execute()

			require.Error(t, err)
			require.Contains(t, err.Error(), `unknown docs subcommand "config/workspace"`)
			require.Contains(t, err.Error(), "dwe docs show config/workspace",
				"the error must name the exact command that works")
			require.Contains(t, err.Error(), "dwe docs list")
			require.NotContains(t, err.Error(), "Unknown flag",
				"the flag must not preempt the subcommand diagnosis")
		})
	}

	t.Run("lists real subcommands so a typo is recoverable too", func(t *testing.T) {
		cmd := newCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"serch"})

		err := cmd.Execute()

		require.Error(t, err)
		for _, name := range []string{"show", "list", "search", "llms-txt"} {
			require.Contains(t, err.Error(), name)
		}
	})
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
	// Args is docsNoTopicArgs — a NoArgs equivalent that diagnoses a bare
	// `dwe docs <topic>` in the user's terms (see TestDocsTopicWithoutShow).
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
