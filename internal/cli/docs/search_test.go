package docs

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

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
	require.NotNil(t, cmd.Flag("literal"))
}

// TestDocsSearchTSV verifies the default tab-separated output:
// source\tpath#anchor\tcount\tsnippet. The snippet column is append-only — a
// consumer reading fields [0..2] keeps working — and the snippet itself is
// sanitized upstream, so a row can never gain a fifth field.
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
		require.Len(t, parts, 4, "each TSV row must have exactly 4 fields: %q", line)
		require.NotEmpty(t, parts[3], "every hit carries a snippet: %q", line)
	}
}

// TestDocsSearchJSON verifies --output json emits a parseable array of
// {source, path, anchor, count, snippet} records. Field names are an
// agent-facing contract — renames must be deliberate.
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
	require.Contains(t, first, "snippet", "JSON contract: snippet field")
	require.Len(t, first, 5, "JSON contract: exactly five fields per entry")
}

// runDocsSearchLines executes the command against the real embedded docs tree
// and returns the TSV rows. --lang en is explicit: the locale comes from
// i18n.ResolveLocale($LANG), not from RootFlags.Locale, so a developer machine
// with LANG=ru would otherwise search the translated mirror.
func runDocsSearchLines(t *testing.T, args ...string) []string {
	t.Helper()
	flags := &cmdctx.RootFlags{Locale: "en"}
	cmd := newDocsSearchCmd(flags)
	var outBuf, errBuf strings.Builder
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(append(args, "--lang", "en"))
	require.NoError(t, cmd.Execute())

	out := strings.TrimSpace(outBuf.String())
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// TestDocsSearchRelevance asserts RELEVANCE, not non-emptiness. Both queries are
// real ones an agent typed during the analysed sessions; both returned `[]`
// under the old whole-query substring matcher. A "returns hits" assertion would
// pass on pure noise — a naive AND does exactly that for the first query.
func TestDocsSearchRelevance(t *testing.T) {
	// The relevance claim is asserted over the WHOLE result set plus a bound on
	// its size, not over a near-boundary rank: a naive AND returns hundreds of
	// rows, so a small total IS the relevance signal, while pinning the target's
	// exact rank would turn any unrelated documentation edit into a failure here.
	cases := []struct {
		name    string
		query   string
		want    string
		maxHits int
	}{
		{
			name:    "two-word concept query finds the templates reference",
			query:   "interpolation vars",
			want:    "reference/templates",
			maxHits: 25,
		},
		{
			name:    "auto-injected env names find the exports.env schema",
			query:   "UID GID env",
			want:    "reference/config/workspace#exportsenv",
			maxHits: 40,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := runDocsSearchLines(t, tc.query, "--limit", "200")
			require.NotEmpty(t, lines, "query %q must not return an empty result", tc.query)
			require.LessOrEqual(t, len(lines), tc.maxHits,
				"query %q returned %d hits — the AND gate is no longer discriminating",
				tc.query, len(lines))

			found := false
			for _, line := range lines {
				if strings.Contains(line, tc.want) {
					found = true
					break
				}
			}
			require.True(t, found, "query %q must surface %q:\n%s",
				tc.query, tc.want, strings.Join(lines, "\n"))
		})
	}
}

// TestDocsSearchLiteral covers the flag that preserves the pre-tokenization
// behaviour. It must be a flag: `docs search` is ExactArgs(1) and the shell
// strips quotes, so "a b" and a b arrive identical.
func TestDocsSearchLiteral(t *testing.T) {
	const query = "vars interpolation" // reversed order — no doc says it verbatim

	tokenized := runDocsSearchLines(t, query, "--limit", "5")
	require.NotEmpty(t, tokenized, "tokenized search must find the pair")

	literal := runDocsSearchLines(t, query, "--limit", "5", "--literal")
	require.Empty(t, literal, "literal search must not match a phrase no document contains verbatim")
}

// TestDocsSearchLiteralNotice: the zero-result notice must name --literal when
// it is the reason nothing matched, and must never claim the default matcher is
// a whole-query literal substring — that stopped being true here.
func TestDocsSearchLiteralNotice(t *testing.T) {
	flags := &cmdctx.RootFlags{Locale: "en"}
	cmd := newDocsSearchCmd(flags)
	var outBuf, errBuf strings.Builder
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"vars interpolation", "--literal", "--lang", "en"})

	require.NoError(t, cmd.Execute())
	require.Empty(t, outBuf.String())
	require.Contains(t, errBuf.String(), "--literal", "the notice must name the flag that caused the empty result")
}

// TestDocsSearchEmptyOutput is the regression guard for the empty-output
// contract: zero hits must produce zero bytes of STDOUT in text mode and `[]` in
// JSON mode. stdout and stderr are asserted separately — the zero-bytes contract
// is about the machine-readable stream only, and a zero-result search also emits
// a human-facing notice on stderr so an agent can tell "no matches" apart from a
// command that silently did nothing.
func TestDocsSearchEmptyOutput(t *testing.T) {
	const missing = "zzzzzz_no_such_substring_anywhere"

	t.Run("text mode emits zero stdout bytes and a stderr notice when no hits", func(t *testing.T) {
		flags := &cmdctx.RootFlags{Locale: "en"}
		cmd := newDocsSearchCmd(flags)
		var outBuf, errBuf strings.Builder
		cmd.SetOut(&outBuf)
		cmd.SetErr(&errBuf)
		cmd.SetArgs([]string{missing})

		require.NoError(t, cmd.Execute())
		require.Empty(t, outBuf.String(), "text mode must emit zero stdout bytes for empty result")
		require.Contains(t, errBuf.String(), missing,
			"zero-result search must name the query on stderr")
		require.Contains(t, errBuf.String(), "--source=all",
			"notice must name the source filter that can cause a false empty result")
	})

	t.Run("text mode stays silent on stderr when there are hits", func(t *testing.T) {
		flags := &cmdctx.RootFlags{Locale: "en"}
		cmd := newDocsSearchCmd(flags)
		var outBuf, errBuf strings.Builder
		cmd.SetOut(&outBuf)
		cmd.SetErr(&errBuf)
		cmd.SetArgs([]string{"services"})

		require.NoError(t, cmd.Execute())
		require.NotEmpty(t, outBuf.String(), "a matching query must produce rows")
		require.Empty(t, errBuf.String(), "the no-matches notice must not fire when there are hits")
	})

	t.Run("json mode emits empty array and no stderr notice", func(t *testing.T) {
		flags := &cmdctx.RootFlags{Output: "json", Locale: "en"}
		cmd := newDocsSearchCmd(flags)
		var outBuf, errBuf strings.Builder
		cmd.SetOut(&outBuf)
		cmd.SetErr(&errBuf)
		cmd.SetArgs([]string{missing})

		require.NoError(t, cmd.Execute())
		require.Equal(t, "[]\n", outBuf.String(), "json mode must emit `[]` for empty result")
		require.Empty(t, errBuf.String(),
			"json consumers get an unambiguous [] — the notice would be noise")
	})
}
