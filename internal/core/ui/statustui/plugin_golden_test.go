package statustui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// goldenFrameHeight is the terminal height the full-frame goldens render at.
// Width varies across the buckets; height is fixed so goldens differ only by
// width and content, not vertical geometry.
const goldenFrameHeight = 24

// goldenRunOpts are the tui.RunOptions shared across golden frame tests. Brand
// is composed exactly as statustui.Run does (render.BrandedSelectorTitle) so the
// goldens exercise the real status-line branding, with an empty Project.
var goldenRunOpts = tui.RunOptions{
	Brand:      render.BrandedSelectorTitle("demo", statusTitleBase),
	Mouse:      true,
	Translator: i18n.NopTranslator{},
	Locale:     "en",
}

// goldenBodies is deterministic canned content for the five tabs, indexed by
// tab position. Golden frame tests wire it through a renderTabFn stub
// (below) rather than a realistic tabSnapshot — these tests pin frame/chrome
// layout, not real table-rendering output, so plain canned text keeps them
// independent of the render package's table shape.
func goldenBodies() [5]string {
	return [5]string{
		"Services\n\nweb    running\ndb     running",
		"Deploy Status\n\nlast run: success",
		"Topology\n\nweb -> db",
		"Git Workspace\n\nno git workspace tracked",
		"Daemons\n\nno daemons running",
	}
}

// stubRenderTab returns a renderTabFn replacement that ignores the snapshot
// entirely and returns canned bodies[index] with no anchors.
func stubRenderTab(bodies [5]string) func(tabSnapshot, int, int) (string, []int) {
	return func(_ tabSnapshot, index, _ int) (string, []int) {
		if index < 0 || index >= len(bodies) {
			return "", nil
		}
		return bodies[index], nil
	}
}

// newGoldenPlugin builds a deterministic plugin for golden frame tests: a
// snapshot is assigned directly (bypassing buildTabsCmd) and renderTabFn is
// stubbed to canned bodies, so rendering is fully deterministic with no
// docker/git calls and no dependency on real table-rendering output.
func newGoldenPlugin(t *testing.T, loading bool) *plugin {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	origRenderTab := renderTabFn
	t.Cleanup(func() { renderTabFn = origRenderTab })
	renderTabFn = stubRenderTab(goldenBodies())

	m := newModel(Deps{Cfg: &config.DweConfig{Project: config.ProjectConfig{Name: "demo"}}}, ctx)
	m.loading = loading
	if !loading {
		m.snap = tabSnapshot{}
		m.loaded = true
		m.sectionAnchors = make([][]int, len(tabTitles))
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
		snap:            tabSnapshot{},
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
	if !p.m.loaded {
		t.Errorf("loaded after tabsLoadedMsg via Frame = false, want true")
	}
	if !strings.Contains(plain, "Services") {
		t.Errorf("frame missing loaded tab content after tabsLoadedMsg via Frame:\n%s", plain)
	}
}

// TestStatus_ReloadRestoresYOffsetThroughFrame drives a reload end-to-end
// through the Frame — HandleAction(ActionReload) followed by the
// tabsLoadedMsg it implies arriving via the Frame's Update loop — and
// asserts the restored scroll position through the RENDERED frame text
// (which lines are visible), not by inspecting m.viewport.YOffset()
// directly. This is the render-time counterpart to
// TestPlugin_Update_PreservesYOffsetOnReload_SameTab, which asserts the same
// invariant against the internal field.
func TestStatus_ReloadRestoresYOffsetThroughFrame(t *testing.T) {
	p := newGoldenPlugin(t, false)

	var tallLines []string
	for i := range 100 {
		tallLines = append(tallLines, fmt.Sprintf("line%02d", i))
	}
	tallContent := strings.Join(tallLines, "\n")
	renderTabFn = func(_ tabSnapshot, _, _ int) (string, []int) { return tallContent, nil }

	// First render sizes the viewport against real frame geometry so a
	// later SetYOffset clamps against the real content height.
	if _, err := tui.RenderFrame(p, goldenRunOpts, 80, goldenFrameHeight); err != nil {
		t.Fatalf("RenderFrame (initial): %v", err)
	}
	p.m.viewport.SetYOffset(5)
	p.m.loadGen = 1

	cmd, handled := p.HandleAction(tui.ActionReload)
	if !handled || cmd == nil {
		t.Fatalf("HandleAction(ActionReload) = (%v, %v), want (non-nil, true)", cmd, handled)
	}
	gen := p.m.loadGen

	msg := tabsLoadedMsg{gen: gen, snap: tabSnapshot{}, loadedAt: time.Now(), healthIndicator: "●"}
	content, err := tui.RenderFrameAfterSetup(p, goldenRunOpts, 80, goldenFrameHeight, msg)
	if err != nil {
		t.Fatalf("RenderFrameAfterSetup: %v", err)
	}
	plain := ansi.Strip(content)

	if !strings.Contains(plain, "line05") {
		t.Errorf("frame after reload missing %q — YOffset was not restored:\n%s", "line05", plain)
	}
	if strings.Contains(plain, "line00") {
		t.Errorf("frame after reload unexpectedly shows %q — YOffset was reset to top instead of restored:\n%s", "line00", plain)
	}
}

// TestStatus_ViewPanelNeverProbesIsRunning is a spy test proving IsRunning
// (the Docker probe) is never called from renderTab / ViewPanel — only from
// buildTabs, which runs once per load/reload in the async goroutine. If a
// later change accidentally moved the probe into the render path, this test
// catches it via a call-count assertion rather than relying on inspection.
func TestStatus_ViewPanelNeverProbesIsRunning(t *testing.T) {
	stubCollectors(t)
	calls := 0
	deps := characterizationDeps()
	deps.IsRunning = func(container string) bool {
		calls++
		return container == "web"
	}

	snap, health := buildTabs(context.Background(), deps)
	if calls == 0 {
		t.Fatalf("buildTabs() made 0 IsRunning calls, want at least 1 (sanity check on the spy)")
	}
	afterLoad := calls

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m := newModel(deps, ctx)
	m.loading = false
	m.loaded = true
	m.snap = snap
	m.healthIndicator = health
	m.sectionAnchors = make([][]int, len(tabTitles))
	p := newPlugin(m, cancel)

	for range 5 {
		p.ViewPanel(panelMain, tui.Region{Width: 80, Height: 24})
	}
	for i := range len(tabTitles) {
		p.m.active = i
		p.ViewPanel(panelMain, tui.Region{Width: 80, Height: 24})
	}

	if calls != afterLoad {
		t.Errorf("IsRunning calls after %d ViewPanel renders = %d, want unchanged %d (renderTab/ViewPanel must never probe Docker)", 5+len(tabTitles), calls, afterLoad)
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
