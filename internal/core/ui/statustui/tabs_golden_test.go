package statustui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
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
// tabs_*.golden fixtures actually landed under testdata/, matching the Files
// list in the responsive-tables plan.
func TestTabs_CharacterizationGolden_FilesExist(t *testing.T) {
	for _, name := range tabsGoldenNames {
		p := goldenPath("tabs_" + name + ".golden")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("golden file missing: %s (%v)", p, err)
		}
	}
}
