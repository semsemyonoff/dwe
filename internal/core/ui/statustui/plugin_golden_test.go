package statustui

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
	Project:    "demo",
	Mouse:      true,
	Translator: i18n.NopTranslator{},
	Locale:     "en",
}

// goldenTabs is a deterministic tab set used by golden tests, avoiding any
// docker/git calls that buildTabsCmd would otherwise perform.
func goldenTabs() []tab {
	return []tab{
		{"Services", "Services\n\nweb    running\ndb     running"},
		{"Deploy", "Deploy Status\n\nlast run: success"},
		{"Topology", "Topology\n\nweb -> db"},
		{"Git", "Git Workspace\n\nno git workspace tracked"},
		{"Daemons", "Daemons\n\nno daemons running"},
	}
}

// newGoldenPlugin builds a deterministic plugin for golden frame tests: tabs
// are assigned directly (bypassing buildTabsCmd) and loading is cleared, so
// rendering is fully deterministic with no docker/git calls.
func newGoldenPlugin(t *testing.T, loading bool) *plugin {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	m := newModel(Deps{ProjectName: "demo"}, ctx, 0, 0)
	m.loading = loading
	if !loading {
		m.tabs = goldenTabs()
		m.viewport.SetContent(m.tabs[m.active].content)
	}
	return newPlugin(m, cancel)
}

// assertGolden compares got against testdata/<name>, writing the file when
// UPDATE_GOLDEN is set. Mirrors the docstui golden helper.
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

// TestStatus_FullFrameGolden pins the full-frame plugin render (normal, tabs
// loaded) at width buckets 60/79/80/99/100 (odd+even) via the exported
// tui.RenderFrame harness.
//
// Regenerate with:
//
//	make embedded-docs && UPDATE_GOLDEN=1 go test ./internal/core/ui/statustui/...
func TestStatus_FullFrameGolden(t *testing.T) {
	for _, w := range []int{60, 79, 80, 99, 100} {
		t.Run("width_"+strconv.Itoa(w), func(t *testing.T) {
			p := newGoldenPlugin(t, false)
			content, err := tui.RenderFrame(p, goldenRunOpts, w, goldenFrameHeight)
			if err != nil {
				t.Fatalf("RenderFrame: %v", err)
			}
			plain := ansi.Strip(content)

			rows := strings.Split(plain, "\n")
			if len(rows) != goldenFrameHeight {
				t.Errorf("row count = %d, want terminal height %d", len(rows), goldenFrameHeight)
			}
			for i, row := range rows {
				if got := lipgloss.Width(row); got != w {
					t.Errorf("row %d width = %d, want frame width %d: %q", i, got, w, row)
				}
			}
			if last := rows[len(rows)-1]; !strings.Contains(last, "? help") {
				t.Errorf("final row is not the status line: %q", last)
			}
			if !strings.Contains(plain, "Services") {
				t.Errorf("frame missing active tab title %q", "Services")
			}

			assertGolden(t, "frame_normal_"+strconv.Itoa(w)+".golden", plain)
		})
	}
}

// TestStatus_LoadingFrameGolden pins the full-frame plugin render while the
// initial tab load is in flight (spinner, no tab strip) at width 80.
func TestStatus_LoadingFrameGolden(t *testing.T) {
	p := newGoldenPlugin(t, true)
	content, err := tui.RenderFrame(p, goldenRunOpts, 80, goldenFrameHeight)
	if err != nil {
		t.Fatalf("RenderFrame: %v", err)
	}
	plain := ansi.Strip(content)

	rows := strings.Split(plain, "\n")
	if len(rows) != goldenFrameHeight {
		t.Errorf("row count = %d, want %d", len(rows), goldenFrameHeight)
	}
	for i, row := range rows {
		if got := lipgloss.Width(row); got != 80 {
			t.Errorf("row %d width = %d, want 80: %q", i, got, row)
		}
	}

	assertGolden(t, "frame_loading_80.golden", plain)
}

// TestStatus_HelpModalGolden pins the registry-generated ?-modal help at
// width 100×40 via the exported tui.BuildHelp harness, locking the Tabs
// section (prev/next/jumps) and the ctrl+r reload binding — and confirming
// the legacy "r" reload key never resurfaces in the rendered modal.
func TestStatus_HelpModalGolden(t *testing.T) {
	p := newGoldenPlugin(t, false)

	ov, err := tui.BuildHelp(p, i18n.NopTranslator{}, "en", 100, 40)
	if err != nil {
		t.Fatalf("BuildHelp: %v", err)
	}
	plain := ansi.Strip(ov.Content)

	for _, want := range []string{sectionTabs, "Previous tab", "Next tab", "Services", "Daemons"} {
		if !strings.Contains(plain, want) {
			t.Errorf("help modal missing %q:\n%s", want, plain)
		}
	}
	for _, want := range []string{"ctrl+r", "Reload"} {
		if !strings.Contains(plain, want) {
			t.Errorf("help modal missing reload binding %q:\n%s", want, plain)
		}
	}
	for line := range strings.SplitSeq(plain, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "r" || strings.HasPrefix(trimmed, "r ") {
			t.Errorf("help modal unexpectedly contains legacy %q binding: %q", "r", line)
		}
	}

	assertGolden(t, "help.golden", plain)
}

// TestStatus_AsyncTabsLoadedMsgPreservationThroughFrame verifies that an
// async tabsLoadedMsg (the message buildTabsCmd's goroutine delivers on
// completion) survives the Frame's Update loop — i.e. the Frame forwards
// unmatched message types to plugin.Update without swallowing or
// transforming them, so the reload/loadGen state machine driven by
// plugin.Update keeps working once the plugin is hosted inside the Frame.
func TestStatus_AsyncTabsLoadedMsgPreservationThroughFrame(t *testing.T) {
	p := newGoldenPlugin(t, true)
	p.m.loadGen = 1

	msg := tabsLoadedMsg{
		gen:             1,
		tabs:            goldenTabs(),
		loadedAt:        time.Now(),
		healthIndicator: "●",
	}

	content, err := tui.RenderFrameAfterSetup(p, goldenRunOpts, 80, goldenFrameHeight, msg)
	if err != nil {
		t.Fatalf("RenderFrameAfterSetup: %v", err)
	}
	plain := ansi.Strip(content)

	if p.m.loading {
		t.Errorf("loading after tabsLoadedMsg via Frame = true, want false")
	}
	if len(p.m.tabs) != len(goldenTabs()) {
		t.Errorf("tabs after tabsLoadedMsg via Frame = %d, want %d", len(p.m.tabs), len(goldenTabs()))
	}
	if !strings.Contains(plain, "Services") {
		t.Errorf("frame missing loaded tab content after tabsLoadedMsg via Frame:\n%s", plain)
	}
}

// TestStatus_FrameWidthInvariant verifies that every row of the rendered
// frame at each width bucket has exactly the expected width (the frame fills
// the terminal with no overflow). Kept separate from the goldens so it runs
// even while UPDATE_GOLDEN is set.
func TestStatus_FrameWidthInvariant(t *testing.T) {
	for _, w := range []int{60, 79, 80, 99, 100} {
		t.Run("width_"+strconv.Itoa(w), func(t *testing.T) {
			p := newGoldenPlugin(t, false)
			content, err := tui.RenderFrame(p, goldenRunOpts, w, goldenFrameHeight)
			if err != nil {
				t.Fatalf("RenderFrame: %v", err)
			}
			plain := ansi.Strip(content)
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
