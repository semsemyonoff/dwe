package docs

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

// TestParseHeadingSlugLabel_SlugKeepsUnderscoresLabelDoesNot pins the asymmetry
// the whole agreement rests on: the slug comes from the raw heading text (where
// Slugify preserves `_`), the label from the stripped one (where stripEmphasis
// eats it). Slugging the label produced `servicedirsensure` — an anchor
// `docs show --anchors` advertised and `docs show topic#anchor` then rejected.
func TestParseHeadingSlugLabel_SlugKeepsUnderscoresLabelDoesNot(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantLevel int
		wantSlug  string
		wantLabel string
	}{
		{
			name:      "code span with underscores",
			line:      "## `service_dirs_ensure`",
			wantLevel: 2,
			wantSlug:  "service_dirs_ensure",
			wantLabel: "service_dirs_ensure",
		},
		{
			name:      "bare underscored identifier",
			line:      "### docker_wait_healthy and friends",
			wantLevel: 3,
			wantSlug:  "docker_wait_healthy-and-friends",
			wantLabel: "docker_wait_healthy and friends",
		},
		{
			name:      "emphasis is dropped from both",
			line:      "## **Bold** heading",
			wantLevel: 2,
			wantSlug:  "bold-heading",
			wantLabel: "Bold heading",
		},
		{
			name:      "link is flattened in both",
			line:      "## See [the `env_file` guide](x.md)",
			wantLevel: 2,
			wantSlug:  "see-the-env_file-guide",
			wantLabel: "See the env_file guide",
		},
		{
			name:      "not a heading",
			line:      "plain text",
			wantLevel: 0,
			wantSlug:  "",
			wantLabel: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lvl, slug, label := parseHeadingSlugLabel(tt.line)
			if lvl != tt.wantLevel {
				t.Errorf("level = %d, want %d", lvl, tt.wantLevel)
			}
			if slug != tt.wantSlug {
				t.Errorf("slug = %q, want %q", slug, tt.wantSlug)
			}
			if label != tt.wantLabel {
				t.Errorf("label = %q, want %q", label, tt.wantLabel)
			}
		})
	}
}

// TestAdvertisedAnchorsResolve is the regression guard for the whole class:
// every anchor any surface hands out must be one SliceByAnchor accepts.
//
// It runs over the real embedded tree rather than a fixture on purpose. The
// defect it guards was invisible to unit tests because each side was tested
// against its own expectation — the two were never compared, and the docs that
// break are precisely the ones whose headings are builtin names.
func TestAdvertisedAnchorsResolve(t *testing.T) {
	files := embeddedMarkdownFiles(t)

	for _, path := range files {
		content, err := fs.ReadFile(BuiltinFS, path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		// Surface 1: `docs show --anchors` / `--toc`.
		for _, h := range ParseHeadingSlugs(content) {
			if _, matched, _, ok := SliceByAnchor(content, h.Slug); !ok {
				t.Errorf("%s: --anchors advertises %q, SliceByAnchor rejects it", path, h.Slug)
			} else if matched != h.Slug {
				t.Errorf("%s: anchor %q resolved to a different heading %q", path, h.Slug, matched)
			}
		}

		// Surface 2: the docs TUI, which jumps by docs.Heading.Slug.
		_, headings := ParseDoc(content)
		for _, h := range headings {
			if h.Slug == "" {
				continue
			}
			if _, _, _, ok := SliceByAnchor(content, h.Slug); !ok {
				t.Errorf("%s: ParseDoc yields Slug %q, SliceByAnchor rejects it", path, h.Slug)
			}
		}
	}
}

// TestSearchSectionAnchorsResolve covers the third surface: the `<path>#<anchor>`
// reference in every `dwe docs search` row. A hit is only actionable if the
// anchor it names can be opened, and search attributes hits by slugging heading
// lines itself.
func TestSearchSectionAnchorsResolve(t *testing.T) {
	roots := Sources("")
	if len(roots) == 0 {
		t.Skip("no doc roots available")
	}

	// A query whose matches live under underscored headings — the exact shape
	// that used to emit an unusable anchor.
	hits := Search(roots, "service_dirs_ensure", "en", SearchOptions{})
	if len(hits) == 0 {
		t.Fatal("no hits for service_dirs_ensure; the embedded tree is likely not synced (run `make embedded-docs`)")
	}

	checked := 0
	for _, h := range hits {
		if h.Section == "" {
			// Lead text before the first H2/H3 has no anchor by design.
			continue
		}
		root, ok := rootBySource(roots, h.Source)
		if !ok {
			t.Fatalf("hit names unknown source %q", h.Source)
		}
		content, _, _, err := ResolveContent(root, h.Path+".md", "en")
		if err != nil {
			t.Fatalf("read %s: %v", h.Path, err)
		}
		if _, _, _, ok := SliceByAnchor(content, h.Section); !ok {
			t.Errorf("search row %s#%s names an anchor SliceByAnchor rejects", h.Path, h.Section)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("every hit was lead text; the test asserted nothing")
	}
}

func rootBySource(roots []DocRoot, source string) (DocRoot, bool) {
	for _, r := range roots {
		if r.Name == source {
			return r, true
		}
	}
	return DocRoot{}, false
}

// linkWithAnchorRE matches an inline markdown link whose target carries an
// `#anchor`. Targets never contain whitespace or parentheses in this doc set.
var linkWithAnchorRE = regexp.MustCompile(`\[[^\]]*\]\(([^()\s]+#[^()\s]+)\)`)

var (
	fenceLineRE = regexp.MustCompile("^(```|~~~)")
	codeSpanRE  = regexp.MustCompile("`[^`\n]*`")
	urlSchemeRE = regexp.MustCompile(`^[a-z][a-z0-9+.-]*:`)
)

// TestDocumentLinkAnchorsResolve covers the fourth surface, and the one the
// other three cannot see: the `#anchor` a document *links to*. The sibling
// tests prove dwe resolves the anchors it advertises — which stayed true while
// the docs linked to something else entirely.
//
// Two defect classes hid in that gap. The RU mirror translates headings but
// kept the English anchors, so 140 links resolved to nothing; and `Slugify`
// trimmed a heading's own leading hyphens, so `docs/reference/config/tests.md`
// linked its own `#--parallel-n` while the resolver answered to `parallel-n`.
//
// A link to a missing *file* is deliberately not an error here — `web/` already
// degrades those to plain text with a build warning, by design.
func TestDocumentLinkAnchorsResolve(t *testing.T) {
	files := embeddedMarkdownFiles(t)

	checked := 0
	for _, docPath := range files {
		content, err := fs.ReadFile(BuiltinFS, docPath)
		if err != nil {
			t.Fatalf("read %s: %v", docPath, err)
		}
		for _, m := range linkWithAnchorRE.FindAllStringSubmatch(stripCodeForLinkScan(string(content)), -1) {
			target := m[1]
			if urlSchemeRE.MatchString(target) {
				continue
			}
			relPath, anchor, _ := strings.Cut(target, "#")

			targetPath := docPath
			if relPath != "" {
				if !strings.HasSuffix(relPath, ".md") {
					continue
				}
				targetPath = path.Join(path.Dir(docPath), relPath)
				if strings.HasPrefix(targetPath, "..") {
					continue // escapes the embedded tree — not ours to resolve
				}
			}

			targetContent, err := fs.ReadFile(BuiltinFS, targetPath)
			if err != nil {
				continue // missing file: see the doc comment
			}
			checked++
			if _, _, _, ok := SliceByAnchor(targetContent, anchor); !ok {
				t.Errorf("%s links to %q, but %s has no such anchor", docPath, target, targetPath)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no anchor links found; the embedded tree is likely not synced (run `make embedded-docs`)")
	}
}

// stripCodeForLinkScan removes fenced blocks and inline code spans so prose
// *about* link syntax is not scanned as a link — `[x](#frag)` in tui-keymap.md
// and Go generics like `errors.AsType[*lock.HeldError](err)` in packages.md both
// parse as one otherwise. Never use it for slugs: a code span's text belongs to
// the heading's anchor.
func stripCodeForLinkScan(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inFence := false
	for line := range strings.SplitSeq(s, "\n") {
		if fenceLineRE.MatchString(strings.TrimSpace(line)) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		b.WriteString(codeSpanRE.ReplaceAllString(line, ""))
		b.WriteByte('\n')
	}
	return b.String()
}

func embeddedMarkdownFiles(t *testing.T) []string {
	t.Helper()

	var out []string
	err := fs.WalkDir(BuiltinFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded docs: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no embedded markdown found; run `make embedded-docs` (or use `make test`)")
	}
	return out
}
