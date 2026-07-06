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
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

// tabsLoadedMsg is emitted when buildTabsCmd completes and carries the
// loaded tabs, timestamp, and a generation number for stale-message filtering.
type tabsLoadedMsg struct {
	gen             uint64
	tabs            []tab
	anchors         [][]int // per-tab sub-table line offsets (aligned with tabs)
	loadedAt        time.Time
	healthIndicator string
}

// Package-level seams for testability. Tests override these to avoid slow
// docker/git calls. Restoration via t.Cleanup is required because these are
// package-level globals (not t.Parallel() safe).
var (
	collectDaemonsFn      = stack.CollectDaemons
	collectGitWorkspaceFn = stack.CollectGitWorkspace
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

// renderGitTab renders the Git Workspace section by calling collectGitWorkspaceFn
// and render.GitWorkspace. Returns a placeholder when no repos are tracked,
// and prepends a warning prefix if rows contain errors.
func renderGitTab(ctx context.Context, d Deps) string {
	title := render.SectionTitle("Git Workspace")
	rows := collectGitWorkspaceFn(ctx, d.Cfg, d.ProjectRoot)
	if len(rows) == 0 {
		return joinNonEmpty(title, "no git workspace tracked")
	}

	// Count rows with errors and prepend a warning if any.
	errCount := 0
	for _, row := range rows {
		if row.Err != nil {
			errCount++
		}
	}

	body := render.GitWorkspace(rows)
	return joinNonEmpty(title, warningPrefix(errCount), body)
}

// buildTabs executes all five Render* functions serially and returns the
// composed tabs, a per-tab list of sub-table line anchors (aligned with the
// tabs slice; nil for tabs without jumpable sub-tables), and a cached health
// indicator string. Each renderer returns (body, errs) — body strings are
// joined with joinNonEmpty, errs trigger a warning prefix.
func buildTabs(ctx context.Context, d Deps) ([]tab, [][]int, string) {
	in := stack.StatusInput{
		Cfg:        d.Cfg,
		IsRunning:  d.IsRunning,
		Topo:       d.Topo,
		TopoStatus: d.TopoStatus,
		State:      d.State,
		SvcDeploys: d.SvcDeploys,
		Tracked:    d.Tracked,
	}

	// Services (Apps + Tools + Infra combined). Each sub-table starts an anchor so
	// ] / [ hop between them; the warning prefix is not an anchor.
	appsBody, appsErrs := stack.RenderApps(in)
	toolsBody, toolsErrs := stack.RenderTools(in)
	infraBody, infraErrs := stack.RenderInfra(in)
	serviceWarnings := warningPrefix(len(appsErrs) + len(toolsErrs) + len(infraErrs))
	services, serviceAnchors := joinSectionsWithAnchors([]sectionPart{
		{serviceWarnings, false},
		{appsBody, true},
		{toolsBody, true},
		{infraBody, true},
	})
	if services == "" {
		services = "no services configured"
		serviceAnchors = nil
	}

	// Deploy Status
	deploy := stack.DeployStatus(in)
	if d.State != nil {
		pendingBanner := render.PendingBanner(d.State.Pending)
		deploy = joinNonEmpty(pendingBanner, deploy)
	}
	if deploy == "" {
		deploy = "no deploy status"
	}

	// Topology
	topology := stack.RenderTopology(in)
	if topology == "" {
		topology = "no topology data"
	}

	// Git Workspace
	git := renderGitTab(ctx, d)

	// Daemons
	daemonRows, daemonErrs := collectDaemonsFn(ctx, d.Cfg, normaliseDocker(d.DockerCfg), d.ProjectRoot)
	daemonsBody, renderErrs := stack.RenderDaemons(daemonRows)
	allDaemonErrs := make([]error, 0, len(daemonErrs)+len(renderErrs))
	allDaemonErrs = append(allDaemonErrs, daemonErrs...)
	allDaemonErrs = append(allDaemonErrs, renderErrs...)
	daemons := joinNonEmpty(warningPrefix(len(allDaemonErrs)), daemonsBody)
	if daemons == "" {
		daemons = "no daemons running"
	}

	tabs := []tab{
		{"Services", services},
		{"Deploy", deploy},
		{"Topology", topology},
		{"Git", git},
		{"Daemons", daemons},
	}
	// Anchors align with tabs by index and are sized off len(tabs) so the two can
	// never drift. Only Services (index 0) stacks multiple sub-tables today; the
	// rest stay nil → jumpSection no-ops there.
	anchors := make([][]int, len(tabs))
	anchors[0] = serviceAnchors
	return tabs, anchors, stack.HealthIndicator(in)
}

// buildTabsCmd returns a bubbletea command that calls buildTabs and emits
// the result as a tabsLoadedMsg. The gen parameter is captured in the closure
// and returned in the message for stale-message filtering.
func buildTabsCmd(ctx context.Context, d Deps, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tabs, anchors, health := buildTabs(ctx, d)
		return tabsLoadedMsg{
			gen:             gen,
			anchors:         anchors,
			tabs:            tabs,
			loadedAt:        time.Now(),
			healthIndicator: health,
		}
	}
}
