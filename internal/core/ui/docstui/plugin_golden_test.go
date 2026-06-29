package docstui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// goldenFrameHeight is the terminal height the full-frame goldens render at.
// Width varies across the buckets; height is fixed so goldens differ only by
// width and content, not vertical geometry.
const goldenFrameHeight = 24

// goldenRunOpts are the tui.RunOptions shared across golden frame tests.
var goldenRunOpts = tui.RunOptions{
	Brand:      "dwe",
	Mouse:      true,
	Translator: i18n.NopTranslator{},
	Locale:     "en",
}

// goldenDocs returns the in-memory docs used for golden frame tests.
// flatFS (scroll_test.go) supports ReadFile so loadTopic produces real content.
func goldenDocs() map[string]string {
	return map[string]string{
		"index.md":  "# DWE Documentation\n\nWelcome to the DWE documentation.\n",
		"config.md": "# Configuration\n\nConfigure your DWE workspace.\n",
		"guides.md": "# Guides\n\nTask-oriented guides for DWE.\n",
	}
}

// newGoldenBrowser builds a deterministic browser for golden frame tests:
//   - the mermaid-theme seam is overridden to "dark" so the
//     auto→HasDarkBackground probe is bypassed (Decision #11);
//   - a nil renderer means the Lookuper check fails and prefetch is skipped
//     (no goroutine leaks, no async timing in goldens);
//   - an empty ProjectRoot means no watcher is created;
//   - viewport content is seeded synchronously by running the initial topic
//     load Cmd before returning, so tui.RenderFrame's discarded WindowSizeMsg
//     Cmd (testsupport.go:44) does not leave the viewport empty.
func newGoldenBrowser(t *testing.T, termW, termH int) *browser {
	t.Helper()

	orig := mermaidThemeResolverFn
	t.Cleanup(func() { mermaidThemeResolverFn = orig })
	mermaidThemeResolverFn = func(_ string) string { return "dark" }

	roots := []docs.DocRoot{{Name: "dwe", FS: flatFS{files: goldenDocs()}}}
	m, err := NewModel(
		context.Background(),
		roots,
		"en",
		i18n.NopTranslator{},
		nil,    // nil renderer → no Lookuper → no prefetch
		0, 0,   // Frame supplies geometry
		"",     // no ProjectRoot → no watcher
		"dwe",
		"auto", // overridden by seam above to "dark"
	)
	if err != nil {
		t.Fatalf("newGoldenBrowser: NewModel: %v", err)
	}

	b := newBrowser(context.Background(), m)

	// Seed viewport content at termW. The first non-zero WindowSizeMsg fires
	// loadTopic; run the returned Cmd synchronously and apply the result so
	// tui.RenderFrame sees rendered content (RenderFrame discards its own Cmd).
	loadCmd := b.Update(tea.WindowSizeMsg{Width: termW, Height: termH})
	if loadCmd != nil {
		if msg := loadCmd(); msg != nil {
			b.Update(msg)
		}
	}

	return b
}

// assertGolden compares got against testdata/<name>, writing the file when
// UPDATE_GOLDEN is set. Mirrors the cmdbrowser golden helper.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("creating testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
		t.Logf("updated golden: %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s (run UPDATE_GOLDEN=1 to create): %v", path, err)
	}
	if got != string(want) {
		t.Errorf("golden %s mismatch:\ngot:\n%s\n\nwant:\n%s", name, got, want)
	}
}

// widthStr formats an int as a decimal string (avoids importing strconv).
func widthStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// TestDocs_FullFrameGolden pins the full-frame plugin render at width buckets
// 60/79/80/99/100 (odd+even) with the tree panel focused (the default initial
// state) via the exported tui.RenderFrame harness. It asserts the frame fills
// the terminal exactly (all rows have the expected width), the status line
// appears in the final row, and the byte-stable layout matches the golden.
//
// Regenerate with:
//
//	make embedded-docs && UPDATE_GOLDEN=1 go test ./internal/core/ui/docstui/...
func TestDocs_FullFrameGolden(t *testing.T) {
	for _, w := range []int{60, 79, 80, 99, 100} {
		t.Run("width_"+widthStr(w), func(t *testing.T) {
			b := newGoldenBrowser(t, w, goldenFrameHeight)
			content, err := tui.RenderFrame(b, goldenRunOpts, w, goldenFrameHeight)
			if err != nil {
				t.Fatalf("RenderFrame: %v", err)
			}
			plain := stripANSI(content)

			rows := strings.Split(plain, "\n")
			if len(rows) != goldenFrameHeight {
				t.Errorf("row count = %d, want terminal height %d", len(rows), goldenFrameHeight)
			}
			for i, row := range rows {
				if got := lipgloss.Width(row); got != w {
					t.Errorf("row %d width = %d, want frame width %d: %q", i, got, w, row)
				}
			}
			// The final row is the Frame-owned status line ("? help" hint).
			if last := rows[len(rows)-1]; !strings.Contains(last, "? help") {
				t.Errorf("final row is not the status line: %q", last)
			}

			assertGolden(t, "frame_tree_"+widthStr(w)+".golden", plain)
		})
	}
}

// TestDocs_FullFrameGoldenViewportFocused pins the frame render with the
// viewport panel focused (tab pressed after the initial WindowSizeMsg).
// Uses tui.RenderFrameAfterSetup to inject the Tab key before snapshotting.
// Tested at widths 80 and 99 (representative pair) to keep golden count bounded.
func TestDocs_FullFrameGoldenViewportFocused(t *testing.T) {
	tab := tea.KeyPressMsg{Code: tea.KeyTab}
	for _, w := range []int{80, 99} {
		t.Run("width_"+widthStr(w), func(t *testing.T) {
			b := newGoldenBrowser(t, w, goldenFrameHeight)
			content, err := tui.RenderFrameAfterSetup(b, goldenRunOpts, w, goldenFrameHeight, tab)
			if err != nil {
				t.Fatalf("RenderFrameAfterSetup: %v", err)
			}
			plain := stripANSI(content)

			rows := strings.Split(plain, "\n")
			if len(rows) != goldenFrameHeight {
				t.Errorf("row count = %d, want %d", len(rows), goldenFrameHeight)
			}
			for i, row := range rows {
				if got := lipgloss.Width(row); got != w {
					t.Errorf("row %d width = %d, want %d: %q", i, got, w, row)
				}
			}

			assertGolden(t, "frame_viewport_"+widthStr(w)+".golden", plain)
		})
	}
}

// TestDocs_FullFrameGoldenFilterOpen pins the frame render while the inline
// filter is active. The tree panel shows the query header ("/ █ 3 matches")
// and the filtered visible set. Tested at width 80.
func TestDocs_FullFrameGoldenFilterOpen(t *testing.T) {
	b := newGoldenBrowser(t, 80, goldenFrameHeight)
	b.enterFilter()

	content, err := tui.RenderFrame(b, goldenRunOpts, 80, goldenFrameHeight)
	if err != nil {
		t.Fatalf("RenderFrame (filter open): %v", err)
	}
	plain := stripANSI(content)

	rows := strings.Split(plain, "\n")
	if len(rows) != goldenFrameHeight {
		t.Errorf("row count = %d, want %d", len(rows), goldenFrameHeight)
	}
	for i, row := range rows {
		if got := lipgloss.Width(row); got != 80 {
			t.Errorf("row %d width = %d, want 80: %q", i, got, row)
		}
	}

	// The tree panel must contain the filter prompt character.
	if !strings.Contains(plain, "/") {
		t.Errorf("filter-open frame missing '/' prompt in tree panel")
	}

	assertGolden(t, "frame_filter_80.golden", plain)
}

// TestDocs_HelpModalGolden pins the registry-generated ?-modal help at width
// 100×40 via the exported tui.BuildHelp harness, locking the Diagrams and
// Locales sections that are docs-browser-specific.
func TestDocs_HelpModalGolden(t *testing.T) {
	b := newGoldenBrowser(t, 100, goldenFrameHeight)
	store, err := i18n.Load("")
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}

	ov, err := tui.BuildHelp(b, store, "en", 100, 40)
	if err != nil {
		t.Fatalf("BuildHelp: %v", err)
	}
	plain := stripANSI(ov.Content)

	for _, want := range []string{"Diagrams", "Locales", "Navigation"} {
		if !strings.Contains(plain, want) {
			t.Errorf("help modal missing section %q:\n%s", want, plain)
		}
	}
	for _, want := range []string{"Previous diagram", "Next diagram", "Open diagram", "Copy diagram source"} {
		if !strings.Contains(plain, want) {
			t.Errorf("help modal missing diagram action %q:\n%s", want, plain)
		}
	}
	for _, want := range []string{"Cycle language", "Show English"} {
		if !strings.Contains(plain, want) {
			t.Errorf("help modal missing locale action %q:\n%s", want, plain)
		}
	}

	assertGolden(t, "help.golden", plain)
}

// TestDocs_AsyncMsgPreservationThroughFrame verifies that async plugin messages
// (FileChangedMsg, ProgressMsg) survive the Frame update loop — i.e. the Frame
// forwards them to plugin.Update without swallowing or transforming them.
//
// FileChangedMsg: injected via RenderFrameAfterSetup; requires a real ProjectRoot
// so the path-matching branch fires and loadGen increments.
// ProgressMsg: injected via RenderFrameAfterSetup; the plugin must not crash on
// a mismatched generation (drops silently), confirming the message reached Update.
func TestDocs_AsyncMsgPreservationThroughFrame(t *testing.T) {
	t.Run("FileChangedMsg", func(t *testing.T) {
		// Use a tmpDir as ProjectRoot so the FileChangedMsg path-matching fires.
		tmpDir := t.TempDir()
		docsDir := filepath.Join(tmpDir, "docs")
		if err := os.MkdirAll(docsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}

		orig := mermaidThemeResolverFn
		mermaidThemeResolverFn = func(_ string) string { return "dark" }
		t.Cleanup(func() { mermaidThemeResolverFn = orig })

		roots := []docs.DocRoot{{Name: "dwe", FS: flatFS{files: goldenDocs()}}}
		m, err := NewModel(context.Background(), roots, "en", i18n.NopTranslator{},
			nil, 0, 0, tmpDir, "dwe", "auto")
		if err != nil {
			t.Fatalf("NewModel: %v", err)
		}
		b := newBrowser(context.Background(), m)
		defer func() { _ = b.Close() }()

		loadCmd := b.Update(tea.WindowSizeMsg{Width: 80, Height: goldenFrameHeight})
		if loadCmd != nil {
			if msg := loadCmd(); msg != nil {
				b.Update(msg)
			}
		}

		if b.CurrentTopic == nil || b.CurrentTopic.Node == nil {
			t.Skip("browser has no current topic to test FileChangedMsg")
		}

		beforeGen := b.loadGen
		matchingPath := filepath.Join(docsDir, b.CurrentTopic.Node.Path)

		// Inject FileChangedMsg through the Frame. The Frame forwards unknown
		// message types to plugin.Update via the default case (frame.go:295).
		_, err = tui.RenderFrameAfterSetup(b, goldenRunOpts, 80, goldenFrameHeight,
			FileChangedMsg{Path: matchingPath})
		if err != nil {
			t.Fatalf("RenderFrameAfterSetup: %v", err)
		}

		if b.loadGen <= beforeGen {
			t.Errorf("FileChangedMsg via Frame did not trigger reload: loadGen=%d want >%d",
				b.loadGen, beforeGen)
		}
	})

	t.Run("ProgressMsg", func(t *testing.T) {
		b := newGoldenBrowser(t, 80, goldenFrameHeight)

		// A ProgressMsg must reach plugin.Update via the Frame's default dispatch
		// (frame.go:295). With no Prefetch (nil renderer → nil Prefetch), the
		// generation check is skipped and the message is applied directly.
		// Verify the Frame forwards it without error and the plugin applies it.
		msg := ProgressMsg{Rendered: 2, Total: 5, Generation: 0}
		_, err := tui.RenderFrameAfterSetup(b, goldenRunOpts, 80, goldenFrameHeight, msg)
		if err != nil {
			t.Fatalf("RenderFrameAfterSetup with ProgressMsg: %v", err)
		}
		// Message survived the Frame loop and was applied by the plugin.
		if b.PrefetchProgress.Rendered != 2 {
			t.Errorf("ProgressMsg via Frame: PrefetchProgress.Rendered=%d, want 2",
				b.PrefetchProgress.Rendered)
		}
	})
}

// TestDocs_MouseRoutingViaFrame verifies that tui.PanelClickMsg forwarded
// through the Frame's default dispatch path (frame.go:295) reaches plugin.Update
// and routes correctly: a tree click moves the cursor, a viewport click is a
// no-op. The test injects PanelClickMsg directly via RenderFrameAfterSetup,
// bypassing the Frame's geometry hit-test (which requires real mouse coordinates).
func TestDocs_MouseRoutingViaFrame(t *testing.T) {
	t.Run("TreeClickMovesCursor", func(t *testing.T) {
		b := newGoldenBrowser(t, 80, goldenFrameHeight)
		// Prime the tree panel dimensions so topIdx is calibrated.
		b.ViewPanel(panelTree, tui.Region{Width: 10, Height: 10})

		visible := b.Tree.VisibleNodes()
		if len(visible) < 2 {
			t.Skip("need at least 2 visible nodes")
		}
		// Ensure cursor starts at row 0.
		b.Tree.focusRow(0)
		before := b.Tree.Cursor()

		_, err := tui.RenderFrameAfterSetup(b, goldenRunOpts, 80, goldenFrameHeight,
			tui.PanelClickMsg{Panel: panelTree, X: 0, Y: 1})
		if err != nil {
			t.Fatalf("RenderFrameAfterSetup: %v", err)
		}
		if b.Tree.Cursor() == before {
			t.Error("PanelClickMsg(tree, row=1) via Frame did not move cursor")
		}
	})

	t.Run("ViewportClickIsNoop", func(t *testing.T) {
		b := newGoldenBrowser(t, 80, goldenFrameHeight)
		b.Viewport.SetContent("Line 1\nLine 2\nLine 3\n")
		before := b.Viewport.YOffset()

		_, err := tui.RenderFrameAfterSetup(b, goldenRunOpts, 80, goldenFrameHeight,
			tui.PanelClickMsg{Panel: panelViewport, X: 5, Y: 2})
		if err != nil {
			t.Fatalf("RenderFrameAfterSetup: %v", err)
		}
		if b.Viewport.YOffset() != before {
			t.Errorf("PanelClickMsg(viewport) via Frame changed YOffset: got %d, want %d",
				b.Viewport.YOffset(), before)
		}
	})
}

// TestDocs_FrameWidthInvariant verifies that every row of the rendered frame
// at each width bucket has exactly the expected width (frame fills the terminal
// with no overflow). This is an invariant test separate from the goldens so
// it runs even if UPDATE_GOLDEN is set (goldens may be in the process of being
// regenerated).
func TestDocs_FrameWidthInvariant(t *testing.T) {
	for _, w := range []int{60, 79, 80, 99, 100} {
		t.Run("width_"+widthStr(w), func(t *testing.T) {
			b := newGoldenBrowser(t, w, goldenFrameHeight)
			content, err := tui.RenderFrame(b, goldenRunOpts, w, goldenFrameHeight)
			if err != nil {
				t.Fatalf("RenderFrame: %v", err)
			}
			plain := stripANSI(content)
			rows := strings.Split(plain, "\n")
			if len(rows) != goldenFrameHeight {
				t.Errorf("row count = %d, want %d", len(rows), goldenFrameHeight)
			}
			for i, row := range rows {
				if got := lipgloss.Width(row); got != w {
					t.Errorf("row %d width = %d, want %d (term width %d)", i, got, w, w)
				}
			}
		})
	}
}

// TestDocs_HelpModalContentsViaBuildHelp verifies that tui.BuildHelp produces
// a help overlay for the docs plugin that includes all expected sections and
// action descriptions — this is the "Frame-level" coverage of the help-modal
// contents assertion (complements the plugin-level BuildHelp tests in plugin_test.go).
func TestDocs_HelpModalContentsViaBuildHelp(t *testing.T) {
	b := newGoldenBrowser(t, 100, goldenFrameHeight)
	ov, err := tui.BuildHelp(b, i18n.NopTranslator{}, "en", 100, 40)
	if err != nil {
		t.Fatalf("BuildHelp: %v", err)
	}
	plain := stripANSI(ov.Content)

	checks := []struct {
		label string
		want  string
	}{
		{"Diagrams section", "Diagrams"},
		{"Locales section", "Locales"},
		{"Navigation section", "Navigation"},
		{"diagram.prev action", "Previous diagram"},
		{"diagram.open action", "Open diagram"},
		{"locale.cycle action", "Cycle language"},
		{"locale.english action", "Show English"},
		{"reload action", "ctrl+r"},
	}
	for _, c := range checks {
		if !strings.Contains(plain, c.want) {
			t.Errorf("help modal missing %s (%q):\n%s", c.label, c.want, plain)
		}
	}
}
