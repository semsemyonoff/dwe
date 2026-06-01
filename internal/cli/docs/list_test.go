package docs

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/stretchr/testify/require"
)

func TestDocsListCommand(t *testing.T) {
	flags := &cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		I18n:       nil,
		Locale:     "en",
	}

	cmd := newDocsListCmd(flags)
	require.NotNil(t, cmd)
	require.Equal(t, "list", cmd.Name())
	require.NotEmpty(t, cmd.Short)
}

func TestDocsListFlags(t *testing.T) {
	flags := &cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		I18n:       nil,
		Locale:     "en",
	}

	cmd := newDocsListCmd(flags)
	require.NotNil(t, cmd.Flag("lang"))
	require.NotNil(t, cmd.Flag("source"))
}

// TestDocsListOutput executes the list command and verifies tab-separated output format.
func TestDocsListOutput(t *testing.T) {
	flags := &cmdctx.RootFlags{
		ConfigPath: "",
		Root:       "",
		I18n:       nil,
		Locale:     "en",
	}

	cmd := newDocsListCmd(flags)
	var outBuf strings.Builder
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	require.NoError(t, err, "list should succeed with built-in docs")

	output := outBuf.String()
	require.NotEmpty(t, output, "list should produce output for built-in docs")

	// Each line must be tab-separated: source\tpath\tlang
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		require.Len(t, parts, 3, "each output line must have exactly 3 tab-separated fields: %q", line)
	}
}

// TestDocsListJSON verifies --output json emits a parseable array of
// {source, path, lang} records. Field names are an agent-facing contract; any
// rename would break downstream parsers and must be a deliberate API decision.
func TestDocsListJSON(t *testing.T) {
	flags := &cmdctx.RootFlags{
		Output: "json",
		Locale: "en",
	}

	cmd := newDocsListCmd(flags)
	var outBuf strings.Builder
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	require.NoError(t, err)

	var entries []map[string]any
	require.NoError(t, json.Unmarshal([]byte(outBuf.String()), &entries), "output must be valid JSON array")
	require.NotEmpty(t, entries, "built-in docs should produce at least one entry")

	first := entries[0]
	require.Contains(t, first, "source", "JSON contract: source field")
	require.Contains(t, first, "path", "JSON contract: path field")
	require.Contains(t, first, "lang", "JSON contract: lang field")
	require.Len(t, first, 3, "JSON contract: exactly three fields per entry")
}

// TestDocsListEmptyOutput is the regression guard for the cmdctx.WriteData
// empty-output contract: when no rows match, text mode must emit zero bytes
// (not a stray '\n') and JSON mode must emit `[]`.
func TestDocsListEmptyOutput(t *testing.T) {
	t.Run("text mode emits zero bytes when no match", func(t *testing.T) {
		flags := &cmdctx.RootFlags{Locale: "en"}
		cmd := newDocsListCmd(flags)
		var outBuf strings.Builder
		cmd.SetOut(&outBuf)
		cmd.SetErr(&outBuf)
		cmd.SetArgs([]string{"--match", "never/matches/this/**"})

		require.NoError(t, cmd.Execute())
		require.Empty(t, outBuf.String(), "text mode must emit zero bytes for empty result")
	})

	t.Run("json mode emits empty array", func(t *testing.T) {
		flags := &cmdctx.RootFlags{Output: "json", Locale: "en"}
		cmd := newDocsListCmd(flags)
		var outBuf strings.Builder
		cmd.SetOut(&outBuf)
		cmd.SetErr(&outBuf)
		cmd.SetArgs([]string{"--match", "never/matches/this/**"})

		require.NoError(t, cmd.Execute())
		require.Equal(t, "[]\n", outBuf.String(), "json mode must emit `[]` for empty result")
	})
}
