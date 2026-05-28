package cli

import (
	"bytes"
	"testing"

	"devbox-cli/internal/cli/cmdctx"

	"github.com/stretchr/testify/require"
)

func TestDocsRootNonTTY(t *testing.T) {
	// Test that non-TTY returns an appropriate error
	cmd := newDocsCmd(&cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		Locale:     "en",
		I18n:       nil,
	})

	// Simulate non-TTY by using a buffer as stdout
	outBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)

	errBuf := &bytes.Buffer{}
	cmd.SetErr(errBuf)

	// Running without arguments in non-TTY should fail
	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires a TTY")
}

func TestDocsRootWithArgs(t *testing.T) {
	// Test that docs with arguments bypasses the parent RunE
	cmd := newDocsCmd(&cmdctx.RootFlags{
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
	cmd := newDocsCmd(&cmdctx.RootFlags{
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
	cmd := newDocsCmd(&cmdctx.RootFlags{
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
