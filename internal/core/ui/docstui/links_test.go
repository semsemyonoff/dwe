package docstui

import (
	"strings"
	"testing"
	"testing/fstest"

	"charm.land/glamour/v2"

	"github.com/semsemyonoff/dwe/internal/core/docs"
)

func TestParseLinkRegions_FromGlamour(t *testing.T) {
	md := "See [services](../services/index.md#vars) and [home](https://example.com).\n"
	r, err := glamour.NewTermRenderer(glamour.WithStylePath("dark"), glamour.WithWordWrap(80))
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	out, err := r.Render(md)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	regions := parseLinkRegions(out)
	if len(regions) == 0 {
		t.Fatal("expected OSC-8 link regions, got none")
	}

	var sawInternal, sawExternal bool
	for _, reg := range regions {
		if reg.colEnd <= reg.colStart {
			t.Errorf("region has non-positive width: %+v", reg)
		}
		switch reg.href {
		case "../services/index.md#vars":
			sawInternal = true
		case "https://example.com":
			sawExternal = true
		}
	}
	if !sawInternal {
		t.Errorf("internal href not parsed; regions=%+v", regions)
	}
	if !sawExternal {
		t.Errorf("external href not parsed; regions=%+v", regions)
	}
}

func TestParseLinkRegions_WrappedLinkSpansRows(t *testing.T) {
	// A long link text at a narrow width wraps; the OSC-8 open sits on the first
	// row and the close on the last, so the region must appear on every row.
	md := "[this is a very long link label that will certainly wrap across lines](../a/b.md)\n"
	r, err := glamour.NewTermRenderer(glamour.WithStylePath("dark"), glamour.WithWordWrap(24))
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	out, err := r.Render(md)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	regions := parseLinkRegions(out)
	rows := map[int]bool{}
	for _, reg := range regions {
		if reg.href == "../a/b.md" {
			rows[reg.line] = true
		}
	}
	if len(rows) < 2 {
		t.Errorf("expected the wrapped link on multiple rows, got rows=%v regions=%+v", rows, regions)
	}
}

// TestFollowLinkUnfoldsTreeToTarget guards the bug where navigating an internal
// link expanded the target's ancestors' flags but never rebuilt the visible set,
// so the tree stayed collapsed and the target file was not revealed.
func TestFollowLinkUnfoldsTreeToTarget(t *testing.T) {
	fsys := fstest.MapFS{
		"top.md":         {Data: []byte("# Top\n\nSee [page](sub/page.md).\n")},
		"sub/page.md":    {Data: []byte("# Page\n\nBody.\n")},
		"sub/sibling.md": {Data: []byte("# Sibling\n\nBody.\n")},
	}
	tw, err := NewTreeWidget([]docs.DocRoot{{Name: "dwe", FS: fsys}}, "en")
	if err != nil {
		t.Fatalf("NewTreeWidget: %v", err)
	}

	b := newTestBrowser(t)
	b.Tree = tw
	b.CurrentTopic = tw.VisibleNodes()[0] // root-level node carries RootName "dwe"
	b.currentlyLoadedPath = "top.md"

	// The target lives under the "sub" directory; collapse it so the page is not
	// yet visible.
	sub := tw.findNode(func(n *TreeNode) bool {
		return n.Node != nil && n.Node.IsDir && n.Node.Name == "sub"
	})
	if sub == nil {
		t.Fatal("fixture: expected a sub/ directory node")
	}
	tw.SetExpanded(sub, false)
	tw.recomputeVisible()
	if visibleContains(tw, "sub/page") {
		t.Fatal("precondition: sub/page should be hidden while sub/ is collapsed")
	}

	b.followLink("sub/page.md")

	if !visibleContains(tw, "sub/page") {
		t.Errorf("after followLink the target file is still hidden; visible=%v", visibleLabels(tw.VisibleNodes()))
	}
	if cur := tw.Cursor(); cur == nil || !contentPathEq(cur, "sub/page") {
		t.Errorf("cursor not on the target; got %q", nodeLabel(cur))
	}
}

func visibleContains(tw *TreeWidget, topicPath string) bool {
	for _, n := range tw.VisibleNodes() {
		if contentPathEq(n, topicPath) {
			return true
		}
	}
	return false
}

func TestIsExternalHref(t *testing.T) {
	cases := map[string]bool{
		"https://x.io":         true,
		"http://x.io":          true,
		"mailto:a@b.c":         true,
		"tel:+100":             true,
		"../services/index.md": false,
		"workspace.md#vars":    false,
		"index":                false,
		"FTP://x":              true,
	}
	for href, want := range cases {
		if got := isExternalHref(href); got != want {
			t.Errorf("isExternalHref(%q) = %v, want %v", href, got, want)
		}
	}
}

func TestHeadingIndexForAnchor(t *testing.T) {
	// Built through docs.ParseDoc rather than by hand: the anchor a link names
	// is docs.Heading.Slug, and a hand-filled fixture can populate Text without
	// it — a state production cannot produce, and one that hid the bug where
	// this function slugged Text itself and so never matched an underscore.
	_, headings := docs.ParseDoc([]byte(strings.Join([]string{
		"# Title",
		"## Getting Started",
		"### Vars block",
		"## Advanced Usage",
		"### `service_dirs_ensure`",
	}, "\n")))

	cases := map[string]int{
		"getting-started":     0,
		"vars-block":          1,
		"advanced-usage":      2,
		"VARS-BLOCK":          1, // case-insensitive tier
		"advanced":            2, // slug-prefix tier ("advanced-usage")
		"service_dirs_ensure": 3, // underscores survive into the anchor
		"missing":             -1,
	}
	for anchor, want := range cases {
		if got := headingIndexForAnchor(headings, anchor); got != want {
			t.Errorf("headingIndexForAnchor(%q) = %d, want %d", anchor, got, want)
		}
	}
}
