package docstui

import (
	"path"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/semsemyonoff/dwe/internal/core/docs"
)

// linkRegion is one OSC-8 hyperlink occurrence projected onto a single rendered
// row. A link whose text word-wraps across N rows yields N regions (the OSC-8
// open marker sits only on the first row and the close only on the last, so the
// parser carries the open href across line boundaries). colStart/colEnd are
// VISIBLE cell columns within the row, matching PanelClickMsg.X and the cursor's
// column space.
type linkRegion struct {
	line     int
	colStart int
	colEnd   int
	href     string
}

// parseLinkRegions scans the displayed document (post-inlineDiagrams — the exact
// string handed to Viewport.SetContent) for glamour's OSC-8 hyperlinks and
// returns one linkRegion per (rendered row × link).
//
// glamour emits hyperlinks as `ESC ]8;id=<n>;<RAW-HREF> BEL  text  ESC ]8;; BEL`
// (BEL-terminated, NOT ST; the href is the raw, unresolved markdown target since
// dwe sets no glamour BaseURL). The scan is a document-wide state machine: it
// tracks the open href across `\n` because word-wrap can split a link's text
// over several rows while the open/close markers bracket the whole span.
func parseLinkRegions(content string) []linkRegion {
	var regions []linkRegion
	href := "" // currently-open hyperlink href; "" means none open
	colStart := 0
	for li, line := range strings.Split(content, "\n") {
		col := 0
		i := 0
		for i < len(line) {
			if line[i] == 0x1b && i+1 < len(line) && line[i+1] == ']' {
				payload, next := scanOSC(line, i)
				i = next
				if uri, ok := hyperlinkURI(payload); ok {
					if uri == "" {
						// Close: emit the region for the portion on this row.
						regions = append(regions, linkRegion{line: li, colStart: colStart, colEnd: col, href: href})
						href = ""
					} else {
						// Open: remember the href and where its text starts.
						href = uri
						colStart = col
					}
				}
				continue
			}
			if line[i] == 0x1b {
				i = scanEscape(line, i)
				continue
			}
			r, sz := utf8.DecodeRuneInString(line[i:])
			if sz == 0 {
				i++
				continue
			}
			col += ansi.StringWidth(string(r))
			i += sz
		}
		// A still-open link spans into the next row: record this row's slice and
		// reset the continuation start to the left margin.
		if href != "" {
			regions = append(regions, linkRegion{line: li, colStart: colStart, colEnd: col, href: href})
			colStart = 0
		}
	}
	return regions
}

// scanOSC returns the OSC payload (the bytes between `ESC ]` and the terminator)
// and the index just past the terminator. It accepts BEL or ST (`ESC \`) as the
// terminator. i must point at the `ESC` of an `ESC ]` sequence.
func scanOSC(s string, i int) (payload string, next int) {
	j := i + 2
	for j < len(s) {
		if s[j] == 0x07 { // BEL
			return s[i+2 : j], j + 1
		}
		if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' { // ST
			return s[i+2 : j], j + 2
		}
		j++
	}
	return s[i+2:], len(s) // unterminated; consume the rest
}

// hyperlinkURI parses an OSC-8 payload of the form `8;<params>;<uri>` and returns
// the uri. The bool is false when the payload is not an OSC-8 hyperlink (some
// other OSC command). A close marker is `8;;` → uri == "".
func hyperlinkURI(payload string) (string, bool) {
	rest, ok := strings.CutPrefix(payload, "8;")
	if !ok {
		return "", false
	}
	// rest == "<params>;<uri>"; the uri is everything after the first ';' (params
	// never contains ';', but a uri theoretically could).
	if _, uri, ok := strings.Cut(rest, ";"); ok {
		return uri, true
	}
	return "", true
}

// scanEscape returns the index just past a non-OSC escape sequence beginning at
// s[i] (s[i] == ESC). CSI (`ESC [ … final`) is consumed up to its final byte
// (0x40–0x7e); any other escape is treated as a two-byte sequence.
func scanEscape(s string, i int) int {
	if i+1 < len(s) && s[i+1] == '[' {
		j := i + 2
		for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
			j++
		}
		return j + 1 // consume the final byte
	}
	return i + 2 // two-byte escape (or trailing lone ESC)
}

// isExternalHref reports whether href points outside the docs tree — an absolute
// URL (any `scheme://`) or a `mailto:`/`tel:` link. The terminal owns OSC-8
// activation for these, so the browser leaves them alone.
func isExternalHref(href string) bool {
	h := strings.ToLower(strings.TrimSpace(href))
	return strings.Contains(h, "://") || strings.HasPrefix(h, "mailto:") || strings.HasPrefix(h, "tel:")
}

// linkAt returns the href of the link covering the given absolute rendered row
// and visible column, if any.
func (b *browser) linkAt(absLine, col int) (string, bool) {
	for _, r := range b.currentLinks {
		if r.line == absLine && col >= r.colStart && col < r.colEnd {
			return r.href, true
		}
	}
	return "", false
}

// firstInternalLinkOnRow returns the first internal (navigable) link href on the
// given rendered row — used by Enter on the viewport cursor row.
func (b *browser) firstInternalLinkOnRow(line int) (string, bool) {
	for _, r := range b.currentLinks {
		if r.line == line && !isExternalHref(r.href) {
			return r.href, true
		}
	}
	return "", false
}

// followLink navigates to an internal markdown link. External links are ignored
// (the terminal handles them). The target file/dir is resolved relative to the
// current document, selected in the tree, and loaded; an optional `#anchor`
// scrolls to the matching H2/H3 heading once the content is in place. Mirrors
// selectCursor's already-loaded / async-load / heading-scroll structure but
// drives the heading index from the link anchor rather than a heading tree row.
func (b *browser) followLink(href string) tea.Cmd {
	if href == "" || isExternalHref(href) {
		return nil
	}
	node, headingIdx, ok := b.resolveInternalLink(href)
	if !ok || node == nil || b.Tree == nil {
		return nil
	}
	b.Tree.SetCursor(node)
	b.Tree.expandAncestors(node)
	// expandAncestors only flips expansion flags; rebuild the visible set so the
	// newly-revealed path to the target actually unfolds in the tree, then scroll
	// the focused row into view.
	b.Tree.recomputeVisible()
	b.Tree.eng.EnsureFocusVisible(b.treeInner.Height)
	b.CurrentTopic = node

	cn := contentNodeFor(node)
	if cn == nil {
		cmd, _ := b.loadTopic(node)
		return tea.Batch(cmd, focusCmd(panelViewport))
	}

	if b.currentlyLoadedPath == cn.Path && b.currentlyLoadedLocale == b.Locale {
		if headingIdx >= 0 {
			b.scrollToHeading(headingIdx)
		} else {
			b.Viewport.ScrollToLine(0)
		}
		b.viewportCursor = b.clampCursor(b.Viewport.YOffset())
		b.syncActiveDiagram()
		return focusCmd(panelViewport)
	}

	b.pendingHeadingIdx = headingIdx
	cmd, _ := b.loadTopic(node)
	return tea.Batch(cmd, focusCmd(panelViewport))
}

// resolveInternalLink resolves an internal href to its tree node and, when the
// href carries a `#anchor`, the source index of the matching H2/H3 heading
// (−1 when none). The href is resolved relative to the directory of the
// currently-loaded document; links that escape the docs root are rejected.
func (b *browser) resolveInternalLink(href string) (*TreeNode, int, bool) {
	pathPart, anchor, err := docs.ParseTopic(href)
	if err != nil {
		return nil, -1, false
	}
	base := path.Dir(b.currentlyLoadedPath) // currentlyLoadedPath carries the .md
	target := path.Clean(path.Join(base, pathPart))
	if target == "." || target == "" || strings.HasPrefix(target, "..") {
		return nil, -1, false
	}
	root := ""
	if b.CurrentTopic != nil {
		root = b.CurrentTopic.RootName
	}
	node := b.Tree.findByContentPath(root, target)
	if node == nil {
		return nil, -1, false
	}
	headingIdx := -1
	if anchor != "" {
		if cn := contentNodeFor(node); cn != nil {
			headingIdx = headingIndexForAnchor(cn.Headings, anchor)
		}
	}
	return node, headingIdx, true
}

// headingIndexForAnchor maps a GitHub-style anchor slug to the index of the
// matching H2/H3 heading, mirroring docs.SliceByAnchor's tiers: exact slug,
// then case-insensitive, then slug-prefix (`anchor-…`). Returns −1 on no match.
// The index is the source H2/H3 order, which aligns with currentHeadingLines.
func headingIndexForAnchor(headings []docs.Heading, anchor string) int {
	if anchor == "" {
		return -1
	}
	for i, h := range headings {
		if docs.Slugify(h.Text) == anchor {
			return i
		}
	}
	for i, h := range headings {
		if strings.EqualFold(docs.Slugify(h.Text), anchor) {
			return i
		}
	}
	al := strings.ToLower(anchor)
	for i, h := range headings {
		if strings.HasPrefix(strings.ToLower(docs.Slugify(h.Text)), al+"-") {
			return i
		}
	}
	return -1
}
