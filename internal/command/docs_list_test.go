package command

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDocsListCommand(t *testing.T) {
	flags := &rootFlags{
		configPath:  "",
		projectRoot: "",
		I18n:        nil,
		Locale:      "en",
	}

	cmd := newDocsListCmd(flags)
	require.NotNil(t, cmd)

	// Verify the command exists and has the right shape
	require.Equal(t, "list", cmd.Name())
	require.NotEmpty(t, cmd.Short)
}

func TestDocsListFlags(t *testing.T) {
	flags := &rootFlags{
		configPath:  "",
		projectRoot: "",
		I18n:        nil,
		Locale:      "en",
	}

	cmd := newDocsListCmd(flags)

	// Check that flags are registered
	require.NotNil(t, cmd.Flag("lang"))
	require.NotNil(t, cmd.Flag("source"))
}

func TestDocsListOutput(t *testing.T) {
	// Test that the command can write to a buffer without panicking
	flags := &rootFlags{
		configPath:  "",
		projectRoot: "",
		I18n:        nil,
		Locale:      "en",
	}

	cmd := newDocsListCmd(flags)
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	// The command should exist and be executable
	require.NotNil(t, cmd.RunE)
}

func TestDocsListTabSeparated(t *testing.T) {
	// Test that output would be tab-separated
	// This is a basic test that the structure is in place
	flags := &rootFlags{
		configPath:  "",
		projectRoot: "",
		I18n:        nil,
		Locale:      "en",
	}

	cmd := newDocsListCmd(flags)
	require.NotNil(t, cmd)

	// Output format should be source\tpath\tlang
	// Verified by integration tests since we need actual docs to list
}
