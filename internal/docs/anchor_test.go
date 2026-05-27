package docs

import (
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"Load behavior", "load-behavior"},
		{"`binaries` block", "binaries-block"},
		{"Toggle plan and `--apply`", "toggle-plan-and---apply"},
		{"`on_enable` and `on_disable` schema", "on_enable-and-on_disable-schema"},
		{"Field reference", "field-reference"},
		{"Contents", "contents"},
		{"  Padded  ", "padded"},
		{"", ""},
	}
	for _, tt := range tests {
		got := Slugify(tt.in)
		if got != tt.want {
			t.Errorf("Slugify(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

const anchorDoc = "# Title\n" +
	"\n" +
	"Intro paragraph.\n" +
	"\n" +
	"## Alpha\n" +
	"\n" +
	"Alpha body.\n" +
	"\n" +
	"### Alpha child\n" +
	"\n" +
	"Child body.\n" +
	"\n" +
	"## `binaries` block\n" +
	"\n" +
	"Binaries body line 1.\n" +
	"Binaries body line 2.\n" +
	"\n" +
	"```sh\n" +
	"# not a heading: lives inside a fence\n" +
	"echo hi\n" +
	"```\n" +
	"\n" +
	"## Gamma\n" +
	"\n" +
	"Gamma body.\n"

func TestSliceByAnchor_ExactSlug(t *testing.T) {
	sliced, slug, _, ok := SliceByAnchor([]byte(anchorDoc), "binaries-block")
	if !ok {
		t.Fatalf("expected match")
	}
	if slug != "binaries-block" {
		t.Errorf("slug = %q, want binaries-block", slug)
	}
	got := string(sliced)
	if !strings.HasPrefix(got, "## `binaries` block\n") {
		t.Errorf("slice should start with the matched heading, got: %q", first80(got))
	}
	if !strings.Contains(got, "Binaries body line 2.") {
		t.Errorf("slice should contain section body")
	}
	if strings.Contains(got, "## Gamma") {
		t.Errorf("slice should stop before the next H2; got %q", got)
	}
	// Fenced content with `#` lines must be preserved verbatim inside the section.
	if !strings.Contains(got, "# not a heading: lives inside a fence") {
		t.Errorf("fenced code block contents missing from slice")
	}
}

func TestSliceByAnchor_PrefixFallback(t *testing.T) {
	// "binaries" is not a literal slug, but "binaries-block" starts with
	// "binaries-" — tier 3 should resolve it uniquely.
	sliced, slug, _, ok := SliceByAnchor([]byte(anchorDoc), "binaries")
	if !ok {
		t.Fatalf("expected prefix match")
	}
	if slug != "binaries-block" {
		t.Errorf("slug = %q, want binaries-block", slug)
	}
	if !strings.HasPrefix(string(sliced), "## `binaries` block\n") {
		t.Errorf("unexpected slice start: %q", first80(string(sliced)))
	}
}

func TestSliceByAnchor_H3StopsAtSiblingH2(t *testing.T) {
	sliced, _, _, ok := SliceByAnchor([]byte(anchorDoc), "alpha-child")
	if !ok {
		t.Fatalf("expected match for alpha-child")
	}
	got := string(sliced)
	if !strings.HasPrefix(got, "### Alpha child\n") {
		t.Errorf("slice should start with the H3 heading, got: %q", first80(got))
	}
	if !strings.Contains(got, "Child body.") {
		t.Errorf("slice missing child body")
	}
	// Must stop before the next H2 (which is shallower).
	if strings.Contains(got, "`binaries` block") {
		t.Errorf("H3 slice leaked into following H2")
	}
}

func TestSliceByAnchor_NotFoundReturnsCandidates(t *testing.T) {
	_, _, candidates, ok := SliceByAnchor([]byte(anchorDoc), "missing")
	if ok {
		t.Fatalf("expected no match")
	}
	if len(candidates) == 0 {
		t.Fatalf("expected candidate slugs to be returned for diagnostics")
	}
	// Sanity: known slugs should be present in candidates.
	want := map[string]bool{"alpha": false, "binaries-block": false, "gamma": false, "alpha-child": false}
	for _, c := range candidates {
		if _, tracked := want[c]; tracked {
			want[c] = true
		}
	}
	for slug, seen := range want {
		if !seen {
			t.Errorf("candidate slug %q missing", slug)
		}
	}
}

func TestParseHeadingSlugs(t *testing.T) {
	got := ParseHeadingSlugs([]byte(anchorDoc))
	if len(got) != 4 {
		t.Fatalf("expected 4 H2/H3 headings, got %d: %+v", len(got), got)
	}
	// Verify level + slug + text for each.
	want := []HeadingInfo{
		{Level: 2, Slug: "alpha", Text: "Alpha"},
		{Level: 3, Slug: "alpha-child", Text: "Alpha child"},
		{Level: 2, Slug: "binaries-block", Text: "binaries block"},
		{Level: 2, Slug: "gamma", Text: "Gamma"},
	}
	for i, h := range got {
		if h != want[i] {
			t.Errorf("headings[%d] = %+v, want %+v", i, h, want[i])
		}
	}
}

func TestParseHeadingSlugs_SkipsFencedHashes(t *testing.T) {
	// `# inside fence` lines must NOT register as H1 — and the in-fence H2
	// underneath must be ignored too.
	doc := []byte("# Title\n\n## Real\n\n```\n# fake heading\n## fake h2\n```\n\n## Other\n")
	got := ParseHeadingSlugs(doc)
	if len(got) != 2 {
		t.Fatalf("expected 2 headings (Real, Other), got %d: %+v", len(got), got)
	}
	if got[0].Slug != "real" || got[1].Slug != "other" {
		t.Errorf("slugs = %v, want [real, other]", []string{got[0].Slug, got[1].Slug})
	}
}

func first80(s string) string {
	if len(s) <= 80 {
		return s
	}
	return s[:80] + "..."
}
