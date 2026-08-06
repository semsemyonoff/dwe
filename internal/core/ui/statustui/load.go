package statustui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/stack"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/statusview"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// tabSnapshot is the pure data buildTabs collects: every Docker- or
// git-backed probe (IsRunning, collectDaemonsFn, collectGitWorkspaceFn) has
// already run by the time a tabSnapshot exists. renderTab composes it into a
// tab's body at render time, at whatever width the caller passes — nothing
// here is pre-rendered into a table, so the same snapshot can be rendered at
// any width without re-probing Docker or git.
type tabSnapshot struct {
	apps  stack.ServiceSection
	tools stack.ServiceSection
	infra stack.ServiceSection
	// serviceErrs is the total count of custom-column render errors across
	// apps/tools/infra, driving the Services tab's warning prefix.
	serviceErrs int

	deployRows []render.DeployStatusRow
	// pendingBanner is pre-rendered: render.PendingBanner is pure and
	// width-independent, so there is nothing to gain by deferring it.
	pendingBanner string

	// topology is pre-rendered: stack.RenderTopology has no width parameter
	// (Topology is a tree diagram, not a table) and no Docker probe of its
	// own, so there is nothing to defer.
	topology string

	gitRows []statusview.GitWorkspaceRow

	daemonRows []statusview.DaemonRow
	// daemonErrs is the count of parse errors from collectDaemonsFn.
	daemonErrs int
}

// tabsLoadedMsg is emitted when buildTabsCmd completes and carries the
// collected snapshot, timestamp, and a generation number for stale-message
// filtering.
type tabsLoadedMsg struct {
	gen             uint64
	snap            tabSnapshot
	loadedAt        time.Time
	healthIndicator string
}

// Package-level seams for testability. Tests override these to avoid slow
// docker/git calls. Restoration via t.Cleanup is required because these are
// package-level globals (not t.Parallel() safe).
var (
	collectDaemonsFn      = stack.CollectDaemons
	collectGitWorkspaceFn = stack.CollectGitWorkspace
	// renderTabFn is the seam over renderTab itself. Production always uses
	// renderTab; tests that only care about frame/tab-strip mechanics (not
	// real render output) override this to return canned bodies without
	// needing a realistic snapshot.
	renderTabFn = renderTab
)

// joinNonEmpty drops empty/whitespace-only strings and joins the rest with a
// single newline. Returns "" when all inputs are empty.
func joinNonEmpty(parts ...string) string {
	var nonEmpty []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	if len(nonEmpty) == 0 {
		return ""
	}
	return strings.Join(nonEmpty, "\n")
}

// sectionPart is one piece fed to joinSectionsWithAnchors: its text plus whether
// its start is a jump anchor (a stacked sub-table heading) rather than incidental
// content like a warning prefix.
type sectionPart struct {
	text   string
	anchor bool
}

// joinSectionsWithAnchors joins non-empty parts with a single newline (like
// joinNonEmpty) and returns the joined string plus the 0-based starting line
// offset of every part flagged as an anchor. Anchors let ] / [ jump the viewport
// between stacked sub-tables. Empty/whitespace-only parts are dropped and never
// produce an anchor.
func joinSectionsWithAnchors(parts []sectionPart) (content string, anchors []int) {
	var kept []string
	line := 0
	for _, p := range parts {
		if strings.TrimSpace(p.text) == "" {
			continue
		}
		if p.anchor {
			anchors = append(anchors, line)
		}
		kept = append(kept, p.text)
		line += strings.Count(p.text, "\n") + 1
	}
	return strings.Join(kept, "\n"), anchors
}

// warningPrefix returns a styled warning line "⚠ N expression(s) failed"
// when n > 0, or "" when n == 0. Uses the canonical warning style from render.
func warningPrefix(n int) string {
	if n == 0 {
		return ""
	}
	if n == 1 {
		return styles.StyleWarning("⚠ 1 expression failed")
	}
	return styles.StyleWarning(fmt.Sprintf("⚠ %d expression(s) failed", n))
}

// normaliseDocker returns d when non-nil, or &config.DockerConfig{} when nil.
// Mirrors the pattern in internal/cli/status/status.go (normalisedDockerCfg).
func normaliseDocker(d *config.DockerConfig) *config.DockerConfig {
	if d != nil {
		return d
	}
	return &config.DockerConfig{}
}

// buildTabs runs every Docker- or git-backed probe (service IsRunning checks,
// collectDaemonsFn, collectGitWorkspaceFn) and returns the result as a pure
// tabSnapshot, plus the cached health indicator string. No table is rendered
// here — renderTab does that at render time, at whatever width it is given.
func buildTabs(ctx context.Context, d Deps) (tabSnapshot, string) {
	in := stack.StatusInput{
		Cfg:        d.Cfg,
		IsRunning:  d.IsRunning,
		Topo:       d.Topo,
		TopoStatus: d.TopoStatus,
		State:      d.State,
		SvcDeploys: d.SvcDeploys,
		Tracked:    d.Tracked,
	}

	apps, appsErrs := stack.CollectApps(in)
	tools, toolsErrs := stack.CollectTools(in)
	infra, infraErrs := stack.CollectInfra(in)

	deployRows := stack.CollectDeployStatus(in)
	var pendingBanner string
	if d.State != nil {
		pendingBanner = render.PendingBanner(d.State.Pending)
	}

	topology := stack.RenderTopology(in)

	gitRows := collectGitWorkspaceFn(ctx, d.Cfg, d.ProjectRoot)

	daemonRows, daemonErrs := collectDaemonsFn(ctx, d.Cfg, normaliseDocker(d.DockerCfg), d.ProjectRoot)

	snap := tabSnapshot{
		apps:          apps,
		tools:         tools,
		infra:         infra,
		serviceErrs:   len(appsErrs) + len(toolsErrs) + len(infraErrs),
		deployRows:    deployRows,
		pendingBanner: pendingBanner,
		topology:      topology,
		gitRows:       gitRows,
		daemonRows:    daemonRows,
		daemonErrs:    len(daemonErrs),
	}
	return snap, stack.HealthIndicator(in)
}

// buildTabsCmd returns a bubbletea command that calls buildTabs and emits
// the result as a tabsLoadedMsg. The gen parameter is captured in the closure
// and returned in the message for stale-message filtering.
func buildTabsCmd(ctx context.Context, d Deps, gen uint64) tea.Cmd {
	return func() tea.Msg {
		snap, health := buildTabs(ctx, d)
		return tabsLoadedMsg{
			gen:             gen,
			snap:            snap,
			loadedAt:        time.Now(),
			healthIndicator: health,
		}
	}
}

// renderTab composes the body and jump-anchors of tab index (0=Services,
// 1=Deploy, 2=Topology, 3=Git, 4=Daemons) from snap, at the given width. It
// is pure: no Docker or git probe, no side effect, safe to call once per
// render. Titles, warning prefixes, and empty-section placeholders are
// composed here, matching what buildTabs used to do before rendering moved
// to render time.
func renderTab(snap tabSnapshot, index, width int) (body string, anchors []int) {
	switch index {
	case 0:
		return renderServicesTab(snap, width)
	case 1:
		return renderDeployTab(snap, width), nil
	case 2:
		return renderTopologyTab(snap), nil
	case 3:
		return renderGitTab(snap, width), nil
	case 4:
		return renderDaemonsTab(snap, width), nil
	default:
		return "", nil
	}
}

// renderServicesTab composes the Apps/Tools/Infra sub-tables into the
// Services tab. Each sub-table starts an anchor so ] / [ hop between them;
// the warning prefix is not an anchor.
func renderServicesTab(snap tabSnapshot, width int) (string, []int) {
	appsBody := stack.RenderAppsRows(snap.apps, width)
	toolsBody := stack.RenderToolsRows(snap.tools, width)
	infraBody := stack.RenderInfraRows(snap.infra, width)
	services, anchors := joinSectionsWithAnchors([]sectionPart{
		{warningPrefix(snap.serviceErrs), false},
		{appsBody, true},
		{toolsBody, true},
		{infraBody, true},
	})
	if services == "" {
		return "no services configured", nil
	}
	return services, anchors
}

// renderDeployTab composes the pending-apply banner (if any) and the deploy
// status table.
func renderDeployTab(snap tabSnapshot, width int) string {
	deploy := joinNonEmpty(snap.pendingBanner, stack.RenderDeployStatusRows(snap.deployRows, width))
	if deploy == "" {
		return "no deploy status"
	}
	return deploy
}

// renderTopologyTab returns the pre-rendered topology diagram, or the
// placeholder when there is none.
func renderTopologyTab(snap tabSnapshot) string {
	if snap.topology == "" {
		return "no topology data"
	}
	return snap.topology
}

// renderGitTab composes the Git Workspace section title, warning prefix (for
// rows with a per-repo error), and table.
func renderGitTab(snap tabSnapshot, width int) string {
	title := render.SectionTitleAt("Git Workspace", width)
	if len(snap.gitRows) == 0 {
		return joinNonEmpty(title, "no git workspace tracked")
	}
	errCount := 0
	for _, row := range snap.gitRows {
		if row.Err != nil {
			errCount++
		}
	}
	return joinNonEmpty(title, warningPrefix(errCount), render.GitWorkspaceAt(snap.gitRows, width))
}

// renderDaemonsTab composes the Daemons warning prefix (parse errors from
// collectDaemonsFn) and table.
func renderDaemonsTab(snap tabSnapshot, width int) string {
	daemonsBody, renderErrs := stack.RenderDaemonsAt(snap.daemonRows, width)
	daemons := joinNonEmpty(warningPrefix(snap.daemonErrs+len(renderErrs)), daemonsBody)
	if daemons == "" {
		return "no daemons running"
	}
	return daemons
}
