package docs

import (
	"strings"
	"testing"
	"testing/fstest"
	"unicode/utf8"
)

func TestSearch_AttributesToNearestSection(t *testing.T) {
	doc := []byte("" +
		"# Title\n\n" +
		"Lead paragraph mentions depends_on once.\n\n" +
		"## Alpha\n\n" +
		"Alpha body has depends_on twice: depends_on.\n\n" +
		"```yaml\n" +
		"services:\n" +
		"  depends_on:\n" +
		"    - redis\n" +
		"```\n\n" +
		"## Beta\n\n" +
		"Beta does not mention the keyword.\n",
	)
	roots := []DocRoot{{
		Name: "dwe",
		FS:   fstest.MapFS{"config/services.md": &fstest.MapFile{Data: doc}},
	}}

	hits := Search(roots, "depends_on", "en", SearchOptions{})
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits (lead + alpha), got %d: %+v", len(hits), hits)
	}

	// Highest count first: alpha has 3 (body twice + fenced YAML once).
	if hits[0].Section != "alpha" || hits[0].Count != 3 {
		t.Errorf("hit[0] = {Section: %q, Count: %d}, want {alpha, 3}", hits[0].Section, hits[0].Count)
	}
	// Lead paragraph has no section attribution.
	if hits[1].Section != "" || hits[1].Count != 1 {
		t.Errorf("hit[1] = {Section: %q, Count: %d}, want {<empty>, 1}", hits[1].Section, hits[1].Count)
	}
}

func TestSearch_CaseInsensitive(t *testing.T) {
	doc := []byte("# T\n\n## Section\n\nFooBar foobar FOOBAR\n")
	roots := []DocRoot{{
		Name: "dwe",
		FS:   fstest.MapFS{"x.md": &fstest.MapFile{Data: doc}},
	}}
	hits := Search(roots, "foobar", "en", SearchOptions{})
	if len(hits) != 1 || hits[0].Count != 3 {
		t.Fatalf("expected 1 hit with Count=3, got %+v", hits)
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	roots := []DocRoot{{
		Name: "dwe",
		FS:   fstest.MapFS{"x.md": &fstest.MapFile{Data: []byte("# T\n\nbody\n")}},
	}}
	if hits := Search(roots, "", "en", SearchOptions{}); hits != nil {
		t.Errorf("empty query should return nil, got %+v", hits)
	}
	if hits := Search(roots, "   ", "en", SearchOptions{}); hits != nil {
		t.Errorf("whitespace-only query should return nil, got %+v", hits)
	}
	if hits := Search(roots, "", "en", SearchOptions{Literal: true}); hits != nil {
		t.Errorf("empty literal query should return nil, got %+v", hits)
	}
	// A literal query is not quoting: an all-whitespace one must find nothing
	// rather than search for a run of spaces and match every indented line.
	if hits := Search(roots, "   ", "en", SearchOptions{Literal: true}); hits != nil {
		t.Errorf("whitespace-only literal query should return nil, got %+v", hits)
	}
}

// TestSearch_SnippetCapIncludesEllipsis pins the documented contract: a snippet
// is at most snippetMaxLen bytes INCLUDING the ellipsis, cut on a rune and word
// boundary, so a TSV row can never gain a fifth field.
func TestSearch_SnippetCapIncludesEllipsis(t *testing.T) {
	long := strings.Repeat("configuration référence ", 40)
	roots := []DocRoot{{
		Name: "dwe",
		FS:   fstest.MapFS{"x.md": &fstest.MapFile{Data: []byte("# T\n\n## A\n\n" + long + "\n")}},
	}}
	hits := Search(roots, "configuration", "en", SearchOptions{})
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	snippet := hits[0].Snippet
	if len(snippet) > snippetMaxLen {
		t.Errorf("snippet is %d bytes, want <= %d: %q", len(snippet), snippetMaxLen, snippet)
	}
	if !strings.HasSuffix(snippet, "…") {
		t.Errorf("truncated snippet should end with an ellipsis, got %q", snippet)
	}
	if !utf8.ValidString(snippet) {
		t.Errorf("snippet cut mid-rune: %q", snippet)
	}
	if strings.HasSuffix(strings.TrimSuffix(snippet, "…"), " ") {
		t.Errorf("snippet should not keep a trailing space before the ellipsis: %q", snippet)
	}
}

func TestSearch_SortByCountThenPath(t *testing.T) {
	roots := []DocRoot{{
		Name: "dwe",
		FS: fstest.MapFS{
			"b.md": &fstest.MapFile{Data: []byte("# T\n\n## A\n\nneedle needle needle\n")},
			"a.md": &fstest.MapFile{Data: []byte("# T\n\n## A\n\nneedle needle needle\n")},
			"c.md": &fstest.MapFile{Data: []byte("# T\n\n## A\n\nneedle\n")},
		},
	}}
	hits := Search(roots, "needle", "en", SearchOptions{})
	if len(hits) != 3 {
		t.Fatalf("expected 3 hits, got %d", len(hits))
	}
	// Ties broken by path ascending.
	if hits[0].Path != "a" || hits[1].Path != "b" || hits[2].Path != "c" {
		t.Errorf("unexpected order: %+v", hits)
	}
}

// TestSearch_SingleTokenUnchanged pins the pre-tokenization behaviour for a
// one-word identifier query: same hits, same counts, same order. All the
// historical tests are single-token, so the AND change is low-risk — but a
// tokenizer that silently altered identifier search would be a regression
// nobody notices until an agent's `depends_on:` lookup goes quiet.
func TestSearch_SingleTokenUnchanged(t *testing.T) {
	roots := []DocRoot{{
		Name: "dwe",
		FS: fstest.MapFS{
			"a.md": &fstest.MapFile{Data: []byte("# T\n\n## One\n\ndepends_on: redis\ndepends_on: db\n\n## Two\n\ndepends_on: web\n")},
			"b.md": &fstest.MapFile{Data: []byte("# T\n\n## One\n\nnothing here\n")},
		},
	}}
	want := []SearchHit{
		{Source: "dwe", Path: "a", Section: "one", Count: 2, Snippet: "depends_on: redis"},
		{Source: "dwe", Path: "a", Section: "two", Count: 1, Snippet: "depends_on: web"},
	}
	hits := Search(roots, "depends_on:", "en", SearchOptions{})
	if len(hits) != len(want) {
		t.Fatalf("expected %d hits, got %d: %+v", len(want), len(hits), hits)
	}
	for i := range want {
		if hits[i] != want[i] {
			t.Errorf("hit[%d] = %+v, want %+v", i, hits[i], want[i])
		}
	}
}

// TestSearch_AllTokensRequired is the core of the tokenization change: a
// section carrying only one of the two words is not a hit.
func TestSearch_AllTokensRequired(t *testing.T) {
	roots := []DocRoot{{
		Name: "dwe",
		FS: fstest.MapFS{
			"a.md": &fstest.MapFile{Data: []byte("" +
				"# T\n\n" +
				"## Both\n\nInterpolation of vars happens here.\n\n" +
				"## OnlyOne\n\nvars vars vars vars vars\n")},
		},
	}}
	hits := Search(roots, "interpolation vars", "en", SearchOptions{})
	if len(hits) != 1 {
		t.Fatalf("expected exactly 1 hit (the section with both tokens), got %+v", hits)
	}
	if hits[0].Section != "both" {
		t.Errorf("hit attributed to %q, want %q", hits[0].Section, "both")
	}
}

// TestSearch_SubstringTradeOff pins the decision that tokens keep matching as
// substrings rather than whole words: it is what makes `depends_on:` and
// `RunContext.Render` work, and the price is that a short token matches inside
// a longer word. Documented as a known trade-off, not a surprise.
func TestSearch_SubstringTradeOff(t *testing.T) {
	roots := []DocRoot{{
		Name: "dwe",
		FS:   fstest.MapFS{"a.md": &fstest.MapFile{Data: []byte("# T\n\n## S\n\nSee the guides for environment setup.\n")}},
	}}
	if hits := Search(roots, "uid env", "en", SearchOptions{}); len(hits) != 1 {
		t.Fatalf("substring matching must let 'uid' hit 'guides' and 'env' hit 'environment', got %+v", hits)
	}
	if hits := Search(roots, "depends_on", "en", SearchOptions{}); len(hits) != 0 {
		t.Fatalf("unexpected hit: %+v", hits)
	}
}

// TestSearch_DoubleSpaceQuery guards the strings.Fields choice: splitting on
// " " would produce an empty token, which matches nothing, and the AND gate
// would then zero out every result.
func TestSearch_DoubleSpaceQuery(t *testing.T) {
	roots := []DocRoot{{
		Name: "dwe",
		FS:   fstest.MapFS{"a.md": &fstest.MapFile{Data: []byte("# T\n\n## S\n\nalpha and beta\n")}},
	}}
	hits := Search(roots, "alpha  beta", "en", SearchOptions{})
	if len(hits) != 1 {
		t.Fatalf("double-space query must behave like a single space, got %+v", hits)
	}
}

// TestSearch_MinRankingBeatsSum is the test the single-token regression cannot
// make: with sum ranking the section repeating the common word 40 times wins;
// with min ranking the section actually about the pair does.
func TestSearch_MinRankingBeatsSum(t *testing.T) {
	noisy := "# T\n\n## Noisy\n\ninterpolation\n" + strings.Repeat("vars vars vars vars\n", 10)
	focused := "# T\n\n## Focused\n\ninterpolation of vars\ninterpolation of vars\n"
	roots := []DocRoot{{
		Name: "dwe",
		FS: fstest.MapFS{
			"noisy.md":   &fstest.MapFile{Data: []byte(noisy)},
			"focused.md": &fstest.MapFile{Data: []byte(focused)},
		},
	}}
	hits := Search(roots, "interpolation vars", "en", SearchOptions{})
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %+v", hits)
	}
	if hits[0].Path != "focused" {
		t.Errorf("top hit = %q (count %d), want focused — min ranking must not be a sum", hits[0].Path, hits[0].Count)
	}
	if hits[0].Count != 2 || hits[1].Count != 1 {
		t.Errorf("counts = %d, %d; want the rarest token's count (2, 1)", hits[0].Count, hits[1].Count)
	}
}

// TestSearch_DuplicateTokensHarmless — `vars vars` must behave like `vars`.
func TestSearch_DuplicateTokensHarmless(t *testing.T) {
	roots := []DocRoot{{
		Name: "dwe",
		FS:   fstest.MapFS{"a.md": &fstest.MapFile{Data: []byte("# T\n\n## S\n\nvars vars vars\n")}},
	}}
	one := Search(roots, "vars", "en", SearchOptions{})
	two := Search(roots, "vars vars", "en", SearchOptions{})
	if len(one) != 1 || len(two) != 1 || one[0] != two[0] {
		t.Fatalf("duplicate token changed the result: %+v vs %+v", one, two)
	}
}

// TestSearch_DocumentLevelSecondTier covers the case that motivated the tier at
// all: a page explaining two concepts in two adjacent sections, which the
// section-level AND misses entirely.
func TestSearch_DocumentLevelSecondTier(t *testing.T) {
	split := "# T\n\n## Interpolation\n\nHow interpolation works.\n\n## Values\n\nvars are declared here.\n"
	exact := "# T\n\n## Pair\n\ninterpolation of vars\n"
	roots := []DocRoot{{
		Name: "dwe",
		FS: fstest.MapFS{
			"split.md": &fstest.MapFile{Data: []byte(split)},
			"exact.md": &fstest.MapFile{Data: []byte(exact)},
		},
	}}

	hits := Search(roots, "interpolation vars", "en", SearchOptions{})
	if len(hits) != 2 {
		t.Fatalf("expected the exact section hit plus one doc-level hit, got %+v", hits)
	}
	if hits[0].Path != "exact" {
		t.Errorf("tier-1 hit must sort first, got %q", hits[0].Path)
	}
	if hits[1].Path != "split" || hits[1].Section == "" {
		t.Errorf("doc-level hit = {%q, %q}, want split anchored at a section", hits[1].Path, hits[1].Section)
	}
	if hits[1].Snippet == "" {
		t.Error("doc-level hit must carry a snippet too")
	}
}

// TestSearch_DocLevelTierIsPerDocument: the fallback is evaluated per document,
// not globally. A document whose sections never hold every token must still be
// reported even when some OTHER document has a clean section match — that is the
// difference between finding the page about the pair and finding noise.
func TestSearch_DocLevelTierIsPerDocument(t *testing.T) {
	roots := []DocRoot{{
		Name: "dwe",
		FS: fstest.MapFS{
			"clean.md": &fstest.MapFile{Data: []byte("# T\n\n## S\n\nalpha beta\n")},
			"split.md": &fstest.MapFile{Data: []byte("# T\n\n## A\n\nalpha\n\n## B\n\nbeta\n")},
		},
	}}
	hits := Search(roots, "alpha beta", "en", SearchOptions{})
	if len(hits) != 2 {
		t.Fatalf("expected both documents, got %+v", hits)
	}
	if hits[1].Path != "split" {
		t.Errorf("second hit = %q, want split", hits[1].Path)
	}
}

// TestSearch_SingleTokenNeverProducesDocLevelHit — with one token a document
// match is always a section match, so the tier must not double-report.
func TestSearch_SingleTokenNeverProducesDocLevelHit(t *testing.T) {
	roots := []DocRoot{{
		Name: "dwe",
		FS:   fstest.MapFS{"a.md": &fstest.MapFile{Data: []byte("# T\n\n## A\n\nalpha\n\n## B\n\nalpha\n")}},
	}}
	hits := Search(roots, "alpha", "en", SearchOptions{})
	if len(hits) != 2 {
		t.Fatalf("expected one hit per section, got %+v", hits)
	}
}

// TestSearch_LiteralMode keeps the whole query as one substring — the only way
// to ask for the old behaviour, since the shell strips quotes before cobra sees
// the single argument.
func TestSearch_LiteralMode(t *testing.T) {
	roots := []DocRoot{{
		Name: "dwe",
		FS: fstest.MapFS{
			"a.md": &fstest.MapFile{Data: []byte("# T\n\n## S\n\nrun dwe deploy run now\n")},
			"b.md": &fstest.MapFile{Data: []byte("# T\n\n## S\n\ndeploy is a noun, run is a verb\n")},
		},
	}}

	literal := Search(roots, "deploy run", "en", SearchOptions{Literal: true})
	if len(literal) != 1 || literal[0].Path != "a" {
		t.Fatalf("literal search must match the exact phrase only, got %+v", literal)
	}
	tokenized := Search(roots, "deploy run", "en", SearchOptions{})
	if len(tokenized) != 2 {
		t.Fatalf("tokenized search must match both documents, got %+v", tokenized)
	}
}

// TestSearch_SnippetIsDensestLineAndSanitized: the snippet is the line carrying
// the most distinct tokens (not the first line carrying any), and it can never
// break a tab-separated row.
func TestSearch_SnippetIsDensestLineAndSanitized(t *testing.T) {
	doc := "# T\n\n## S\n\nalpha alone here\n| alpha\t| beta |   spaced |\nbeta alone\n"
	roots := []DocRoot{{
		Name: "dwe",
		FS:   fstest.MapFS{"a.md": &fstest.MapFile{Data: []byte(doc)}},
	}}
	hits := Search(roots, "alpha beta", "en", SearchOptions{})
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %+v", hits)
	}
	got := hits[0].Snippet
	if got != "| alpha | beta | spaced |" {
		t.Errorf("snippet = %q, want the densest line with whitespace collapsed", got)
	}
	if strings.ContainsAny(got, "\t\n") {
		t.Errorf("snippet must never contain a tab or newline: %q", got)
	}
}

// TestSearch_SnippetStripsControlCharacters: the snippet is the only channel
// through which document content reaches stdout, and a doc tree can be
// untrusted (`--source project` inside a cloned repo). An ESC/BEL/OSC sequence
// embedded in a page must not survive to the terminal, where it would clear the
// screen, recolor it, or set the window title.
func TestSearch_SnippetStripsControlCharacters(t *testing.T) {
	doc := "# T\n\n## S\n\nalpha \x1b[2J\x1b[31mbeta\x1b]0;pwned\x07 \x00nul\n"
	roots := []DocRoot{{
		Name: "dwe",
		FS:   fstest.MapFS{"a.md": &fstest.MapFile{Data: []byte(doc)}},
	}}
	hits := Search(roots, "alpha beta", "en", SearchOptions{})
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %+v", hits)
	}
	got := hits[0].Snippet
	if strings.ContainsAny(got, "\x1b\x07\x00") {
		t.Errorf("snippet must not carry control characters: %q", got)
	}
	if got != "alpha [2J[31mbeta]0;pwned nul" {
		t.Errorf("snippet = %q, want the printable remainder only", got)
	}
}

func TestSearch_SnippetTruncated(t *testing.T) {
	long := strings.Repeat("alpha beta ", 60)
	roots := []DocRoot{{
		Name: "dwe",
		FS:   fstest.MapFS{"a.md": &fstest.MapFile{Data: []byte("# T\n\n## S\n\n" + long + "\n")}},
	}}
	hits := Search(roots, "alpha beta", "en", SearchOptions{})
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %+v", hits)
	}
	if len(hits[0].Snippet) > snippetMaxLen+len("…") {
		t.Errorf("snippet not truncated: %d bytes", len(hits[0].Snippet))
	}
	if !strings.HasSuffix(hits[0].Snippet, "…") {
		t.Errorf("truncated snippet must be marked: %q", hits[0].Snippet)
	}
}
