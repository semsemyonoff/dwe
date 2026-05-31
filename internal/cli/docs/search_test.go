package docs

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/semsemyonoff/devbox/internal/cli/cmdctx"

	"github.com/stretchr/testify/require"
)

func TestDocsSearchCommand(t *testing.T) {
	flags := &cmdctx.RootFlags{Locale: "en"}
	cmd := newDocsSearchCmd(flags)
	require.NotNil(t, cmd)
	require.Equal(t, "search", cmd.Name())
	require.NotEmpty(t, cmd.Short)
}

func TestDocsSearchFlags(t *testing.T) {
	flags := &cmdctx.RootFlags{Locale: "en"}
	cmd := newDocsSearchCmd(flags)
	require.NotNil(t, cmd.Flag("lang"))
	require.NotNil(t, cmd.Flag("source"))
	require.NotNil(t, cmd.Flag("limit"))
}

// TestDocsSearchTSV verifies the default tab-separated output: source\tpath#anchor\tcount.
func TestDocsSearchTSV(t *testing.T) {
	flags := &cmdctx.RootFlags{Locale: "en"}
	cmd := newDocsSearchCmd(flags)
	var outBuf strings.Builder
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"services", "--limit", "3"})

	require.NoError(t, cmd.Execute())
	output := outBuf.String()
	require.NotEmpty(t, output, "search 'services' should match at least one section")

	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		require.Len(t, parts, 3, "each TSV row must have exactly 3 fields: %q", line)
	}
}

// TestDocsSearchJSON verifies --output json emits a parseable array of
// {source, path, anchor, count} records. Field names are an agent-facing
// contract — renames must be deliberate.
func TestDocsSearchJSON(t *testing.T) {
	flags := &cmdctx.RootFlags{Output: "json", Locale: "en"}
	cmd := newDocsSearchCmd(flags)
	var outBuf strings.Builder
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"services", "--limit", "3"})

	require.NoError(t, cmd.Execute())

	var results []map[string]any
	require.NoError(t, json.Unmarshal([]byte(outBuf.String()), &results), "output must be valid JSON array")
	require.NotEmpty(t, results, "search 'services' should produce at least one hit")

	first := results[0]
	require.Contains(t, first, "source", "JSON contract: source field")
	require.Contains(t, first, "path", "JSON contract: path field")
	require.Contains(t, first, "anchor", "JSON contract: anchor field")
	require.Contains(t, first, "count", "JSON contract: count field")
	require.Len(t, first, 4, "JSON contract: exactly four fields per entry")
}

// TestDocsSearchEmptyOutput is the regression guard for the empty-output
// contract: zero hits must produce zero bytes in text mode and `[]` in JSON mode.
func TestDocsSearchEmptyOutput(t *testing.T) {
	t.Run("text mode emits zero bytes when no hits", func(t *testing.T) {
		flags := &cmdctx.RootFlags{Locale: "en"}
		cmd := newDocsSearchCmd(flags)
		var outBuf strings.Builder
		cmd.SetOut(&outBuf)
		cmd.SetErr(&outBuf)
		cmd.SetArgs([]string{"zzzzzz_no_such_substring_anywhere"})

		require.NoError(t, cmd.Execute())
		require.Empty(t, outBuf.String(), "text mode must emit zero bytes for empty result")
	})

	t.Run("json mode emits empty array", func(t *testing.T) {
		flags := &cmdctx.RootFlags{Output: "json", Locale: "en"}
		cmd := newDocsSearchCmd(flags)
		var outBuf strings.Builder
		cmd.SetOut(&outBuf)
		cmd.SetErr(&outBuf)
		cmd.SetArgs([]string{"zzzzzz_no_such_substring_anywhere"})

		require.NoError(t, cmd.Execute())
		require.Equal(t, "[]\n", outBuf.String(), "json mode must emit `[]` for empty result")
	})
}
