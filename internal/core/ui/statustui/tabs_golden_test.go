package statustui

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/stack"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/statusview"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
)

// tabsGoldenNames are the five tab bodies, in tabTitles order, characterized
// by TestTabs_CharacterizationGolden.
var tabsGoldenNames = [...]string{"services", "deploy", "topology", "git", "daemons"}

// characterizationDeps builds a fully deterministic Deps — no docker/git
// calls needed once collectDaemonsFn/collectGitWorkspaceFn are stubbed —
// exercising every non-empty tab: a running app, an enabled tool, a tracked
// deploy-status row, a topology edge, a git row, and a daemon row.
func characterizationDeps() Deps {
	cfg := &config.DweConfig{
		Project: config.ProjectConfig{Name: "demo"},
		Services: map[string]config.ServiceConfig{
			"web":   {Type: config.ServiceTypeApp, Container: "web", Required: true},
			"cache": {Type: config.ServiceTypeTool, Container: "cache", Enabled: true},
		},
	}
	state := &journal.ProjectState{
		SchemaVersion: "1",
		Project:       &journal.ProjectLevelState{},
		Services:      map[string]*journal.ServiceState{},
	}

	return Deps{
		Cfg:         cfg,
		IsRunning:   func(container string) bool { return container == "web" },
		Topo:        map[string][]string{"web": {"cache"}, "cache": nil},
		TopoStatus:  map[string]render.NodeStatus{"web": render.NodeRunning, "cache": render.NodeStopped},
		State:       state,
		Tracked:     []string{"web"},
		ProjectRoot: "/tmp/demo",
	}
}

// stubCollectors overrides collectDaemonsFn/collectGitWorkspaceFn with
// deterministic canned data and restores them via t.Cleanup.
func stubCollectors(t *testing.T) {
	t.Helper()
	origDaemons := collectDaemonsFn
	origGit := collectGitWorkspaceFn
	t.Cleanup(func() {
		collectDaemonsFn = origDaemons
		collectGitWorkspaceFn = origGit
	})

	collectDaemonsFn = func(_ context.Context, _ *config.DweConfig, _ *config.DockerConfig, _ string) ([]statusview.DaemonRow, []error) {
		return []statusview.DaemonRow{
			{ID: "web.migrate", Container: "web-migrate-1", Uptime: 90 * time.Second},
		}, nil
	}
	collectGitWorkspaceFn = func(_ context.Context, _ *config.DweConfig, _ string) []statusview.GitWorkspaceRow {
		return []statusview.GitWorkspaceRow{
			{Service: "web", Dir: "services/web", Branch: "main", SHA: "abc1234"},
		}
	}
}

// TestTabs_CharacterizationGolden pins byte-exact bodies for all five tabs,
// built from a deterministic snapshot via buildTabs + renderTab at width 0
// (the only width statustui rendered at before width-awareness was added).
// It exists specifically so a later change to renderTab's composition (or to
// the width it is threaded) has a regression pin proving the pre-refactor
// output is unchanged.
//
// Regenerate with:
//
//	UPDATE_GOLDEN=1 go test ./internal/core/ui/statustui/... -run TestTabs_CharacterizationGolden
func TestTabs_CharacterizationGolden(t *testing.T) {
	stubCollectors(t)
	deps := characterizationDeps()

	snap, health := buildTabs(context.Background(), deps)
	if health == "" {
		t.Fatalf("buildTabs() health indicator = empty, want non-empty")
	}

	for i, name := range tabsGoldenNames {
		body, _ := renderTab(snap, i, 0)
		assertGolden(t, "tabs_"+name+".golden", body)
	}
}

// TestTabs_CharacterizationGolden_AnchorsStable pins the Services tab's
// sub-table anchors (Apps/Tools/Infra jump offsets) separately from the body
// text, since assertGolden only compares the body string.
func TestTabs_CharacterizationGolden_AnchorsStable(t *testing.T) {
	stubCollectors(t)
	deps := characterizationDeps()

	snap, _ := buildTabs(context.Background(), deps)
	_, anchors := renderTab(snap, 0, 0)

	if len(anchors) != 2 {
		t.Fatalf("Services tab anchors = %v, want 2 (Apps + Tools; Infra empty)", anchors)
	}
	if anchors[0] != 0 {
		t.Errorf("first anchor = %d, want 0", anchors[0])
	}
	if anchors[1] <= anchors[0] {
		t.Errorf("anchors not strictly increasing: %v", anchors)
	}
}

// goldenPath mirrors assertGolden's path resolution, exposed here so
// TestTabs_CharacterizationGolden's UPDATE_GOLDEN doc comment matches an
// actual on-disk path readers can find (testdata/tabs_*.golden).
func goldenPath(name string) string {
	return filepath.Join("testdata", name)
}

// TestTabs_CharacterizationGolden_FilesExist is a light guard that the
// tabs_*.golden fixtures actually landed under testdata/.
func TestTabs_CharacterizationGolden_FilesExist(t *testing.T) {
	for _, name := range tabsGoldenNames {
		p := goldenPath("tabs_" + name + ".golden")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("golden file missing: %s (%v)", p, err)
		}
	}
}

// widthDependentTabIndex is tabsGoldenNames' index of every tab whose body
// depends on the width renderTab is given. Topology is excluded: it is
// pre-rendered by stack.RenderTopology, which has no width parameter (see
// buildTabs' tabSnapshot.topology doc comment).
var widthDependentTabIndex = map[int]string{0: "services", 1: "deploy", 3: "git", 4: "daemons"}

// wideTabSnapshot builds a tabSnapshot with deliberately long values in
// every column that has a Wrap function (DIR, CONTAINER on ServicesTable,
// BRANCH, PARAMS, LAST FAILED) so rendering at a narrow width actually
// exercises shrink/record mode rather than trivially fitting. Columns
// declared unbreakable by design (DaemonTable's ID/CONTAINER, GitWorkspace's
// SHA) are deliberately kept short — a value longer than the budget in one
// of those is expected to overflow (Cols[i].Wrap == nil never wraps, and the
// renderers overflow rather than drop data), so including one here would
// make TestTabs_RenderedWidthNeverExceedsBudget assert the wrong thing.
// Built directly (not via buildTabs) so the test needs no docker/git stubs.
func wideTabSnapshot() tabSnapshot {
	longDir := strings.Repeat("services/web/very/deep/nested/path/", 2) + "src"
	longContainer := "demo-web-" + strings.Repeat("x", 40) + "-1"
	longBranch := "feature/" + strings.Repeat("very-long-branch-segment-", 3) + "end"
	longParams := strings.Repeat("key=value ", 12)
	longStep := strings.Repeat("very-long-failed-step-name-", 3)

	return tabSnapshot{
		apps: stack.ServiceSection{
			Rows: []render.ServiceTableRow{
				{
					Name: "web", Dir: longDir, Container: longContainer,
					Hosts: map[string]string{"h": "web.local"}, Ports: map[string]int{"p": 80},
					Mandatory: true, Running: true,
				},
			},
		},
		tools: stack.ServiceSection{
			Rows: []render.ServiceTableRow{
				{Name: "cache", Container: longContainer, Enabled: true, Running: false},
			},
		},
		deployRows: []render.DeployStatusRow{
			{
				Service: "web", Status: "deployed", ConfigDelta: "changed",
				PrevHashShort: "abc1234", CurrHashShort: "def5678", LastFailedStep: longStep,
			},
		},
		gitRows: []statusview.GitWorkspaceRow{
			{Service: "web", Dir: longDir, Branch: longBranch, SHA: "abc1234"},
		},
		daemonRows: []statusview.DaemonRow{
			// ID and Container are DaemonTable's unbreakable columns (Cols[0]
			// and Cols[2] have no Wrap) — kept short deliberately; only Params
			// (Flex+wrapText) is made long here.
			{ID: "web.migrate", Container: "web-migrate-1", Params: longParams},
		},
	}
}

// sectionTitleBarRe matches the decorative "── Title ──…" rule line
// render.SectionTitleAt (used by stack's wrapSection and by renderGitTab)
// renders above every table. It is asserted on, not skipped: the bar is sized
// from the same panel width the table body is fitted to, so a regression that
// drops the width on the title path — leaving it to fall back to the ambient
// styles.TermWidth() probe — must fail here. The regexp only guards that such
// a line still exists in the rendered tabs, so the assertion below cannot
// silently degrade to "no titles were rendered at all".
var sectionTitleBarRe = regexp.MustCompile(`^── .+ ─+$`)

// TestTabs_RenderedWidthNeverExceedsBudget verifies every rendered line —
// section title bars included — of every width-dependent tab stays within the
// given budget at the narrow buckets the status TUI panel can realistically
// end up at (panel inner width = outer − 4 per Frame.renderBody). This is the
// render-time counterpart to the render/ package's own fitRows tests: it
// confirms renderTab actually threads the width down to
// stack.RenderAppsRows / RenderDeployStatusRows / render.GitWorkspaceAt /
// stack.RenderDaemonsAt rather than dropping it.
func TestTabs_RenderedWidthNeverExceedsBudget(t *testing.T) {
	snap := wideTabSnapshot()
	sawTitleBar := false
	for _, w := range []int{60, 79, 80} {
		for idx, name := range widthDependentTabIndex {
			body, _ := renderTab(snap, idx, w)
			for lineNo, line := range strings.Split(body, "\n") {
				if sectionTitleBarRe.MatchString(ansi.Strip(line)) {
					sawTitleBar = true
				}
				if got := lipgloss.Width(line); got > w {
					t.Errorf("tab %s width=%d line %d exceeds budget: got %d\nline: %q", name, w, lineNo, got, line)
				}
			}
		}
	}
	if !sawTitleBar {
		t.Error("no section title bar found in any rendered tab — the width assertion above no longer covers the title path")
	}
}

// TestTabs_AnchorsAtNarrowWidthLandOnHeadings verifies the Services tab's
// jump anchors still point at the Apps/Tools sub-table headings once
// rendering happens at a narrow width — anchors are line offsets into the
// wrapped body (joinSectionsWithAnchors), so a change to how many lines a
// table wraps into must not desynchronize them from the headings ]/[ jump
// between.
func TestTabs_AnchorsAtNarrowWidthLandOnHeadings(t *testing.T) {
	snap := wideTabSnapshot()
	body, anchors := renderTab(snap, 0, 60)

	wantTitles := []string{"Apps", "Tools"}
	if len(anchors) != len(wantTitles) {
		t.Fatalf("anchors = %v, want %d entries (Apps, Tools; Infra empty)", anchors, len(wantTitles))
	}
	lines := strings.Split(body, "\n")
	for i, a := range anchors {
		if a < 0 || a >= len(lines) {
			t.Fatalf("anchor[%d] = %d out of range (body has %d lines)", i, a, len(lines))
		}
		if !strings.Contains(lines[a], wantTitles[i]) {
			t.Errorf("anchor[%d] = line %d %q, want it to contain %q", i, a, lines[a], wantTitles[i])
		}
	}
}

// TestTabs_CharacterizationGolden_Width60 pins byte-exact bodies for the four
// width-dependent tabs at panel width 60, built from the same deterministic
// snapshot as TestTabs_CharacterizationGolden. The width-0 goldens above stay
// unbounded; these pin the narrow-panel rendering the status TUI actually
// uses.
//
// Regenerate with:
//
//	UPDATE_GOLDEN=1 go test ./internal/core/ui/statustui/... -run TestTabs_CharacterizationGolden_Width60
func TestTabs_CharacterizationGolden_Width60(t *testing.T) {
	stubCollectors(t)
	deps := characterizationDeps()

	snap, _ := buildTabs(context.Background(), deps)
	for idx, name := range widthDependentTabIndex {
		body, _ := renderTab(snap, idx, 60)
		assertGolden(t, "tabs_"+name+"_w60.golden", body)
	}
}
