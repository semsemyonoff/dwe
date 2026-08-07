package docs

import (
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

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
	cmd.SetArgs([]string{"--raw", "reference/config/workspace"})

	err := cmd.Execute()
	require.NoError(t, err, "expected built-in topic to render without error")
	require.NotEmpty(t, outBuf.String(), "expected non-empty output for built-in topic")
}

// execDocsShow executes `docs show` with the given args and returns stdout and
// stderr separately — the long-doc hint is a stderr-only side channel, so a
// test that merged the two could not tell it from document body.
func execDocsShow(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()

	flags := &cmdctx.RootFlags{Locale: "en"}
	cmd := newDocsShowCmd(flags)
	var outBuf, errBuf strings.Builder
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	// --lang en pins the locale: without it the resolved language follows the
	// developer's $LANG, and an anchor from the English source does not exist
	// in the Russian mirror.
	cmd.SetArgs(append([]string{"--lang", "en"}, args...))

	require.NoError(t, cmd.Execute())
	return outBuf.String(), errBuf.String()
}

// TestDocsShowLongDocHint pins the point-of-use nudge toward --toc/--anchors.
// Stdout is not a terminal under `go test`, which is exactly the piped shape
// the hint targets.
func TestDocsShowLongDocHint(t *testing.T) {
	t.Run("fires on a long whole document", func(t *testing.T) {
		stdout, stderr := execDocsShow(t, "--raw", "reference/config/workspace")
		require.NotEmpty(t, stdout)
		require.Contains(t, stderr, "--toc")
		require.Contains(t, stderr, "sections")
		require.NotContains(t, stdout, "--toc` lists them",
			"the hint must not contaminate stdout — a caller parsing the markdown would ingest it")
	})

	t.Run("silent when a section was requested", func(t *testing.T) {
		// Asking for an anchor is already the behaviour the hint teaches.
		_, stderr := execDocsShow(t, "--raw", "reference/config/workspace#merge-overview")
		require.NotContains(t, stderr, "--toc")
	})

	t.Run("silent for --anchors and --toc themselves", func(t *testing.T) {
		_, stderr := execDocsShow(t, "reference/config/workspace", "--anchors")
		require.Empty(t, stderr)

		_, stderr = execDocsShow(t, "reference/config/workspace", "--toc")
		require.Empty(t, stderr)
	})
}
