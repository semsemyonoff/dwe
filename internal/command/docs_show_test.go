package command

import (
	"strings"
	"testing"

	"devbox-cli/internal/command/cmdctx"

	"github.com/stretchr/testify/require"
)

func TestDocsShowExactMatch(t *testing.T) {
	flags := &cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		I18n:       nil,
		Locale:     "en",
	}

	cmd := newDocsShowCmd(flags)
	require.NotNil(t, cmd)
	require.Equal(t, "show", cmd.Name())
	require.NotEmpty(t, cmd.Short)
}

func TestDocsShowRawMode(t *testing.T) {
	flags := &cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		I18n:       nil,
		Locale:     "en",
	}

	cmd := newDocsShowCmd(flags)
	require.NotNil(t, cmd.Flag("raw"))
	require.NotNil(t, cmd.Flag("lang"))
	require.NotNil(t, cmd.Flag("source"))
}

func TestGetTermWidth(t *testing.T) {
	width := getTermWidth()
	require.Greater(t, width, 0)
}

func TestDocsShowMissingTopic(t *testing.T) {
	flags := &cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		I18n:       nil,
		Locale:     "en",
	}

	cmd := newDocsShowCmd(flags)
	var errBuf strings.Builder
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"nonexistent/topic/that/does/not/exist"})

	err := cmd.Execute()
	require.Error(t, err, "expected error for unknown topic")
}

func TestDocsShowBuiltinTopic(t *testing.T) {
	flags := &cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		I18n:       nil,
		Locale:     "en",
	}

	cmd := newDocsShowCmd(flags)
	var outBuf strings.Builder
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	// Pass --raw to skip TTY detection and get plain text output.
	cmd.SetArgs([]string{"--raw", "reference/config/devbox"})

	err := cmd.Execute()
	require.NoError(t, err, "expected built-in topic to render without error")
	require.NotEmpty(t, outBuf.String(), "expected non-empty output for built-in topic")
}
