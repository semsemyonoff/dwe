package statustui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/project/stack"
	"devbox-cli/internal/core/ui"
	"devbox-cli/internal/core/ui/styles"
)

// tabsLoadedMsg is emitted when buildTabsCmd completes and carries the
// loaded tabs, timestamp, and a generation number for stale-message filtering.
type tabsLoadedMsg struct {
	gen             uint64
	tabs            []tab
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

// warningPrefix returns a styled warning line "⚠ N expression(s) failed"
// when n > 0, or "" when n == 0. Uses the canonical warning style from ui.
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
// and ui.RenderGitWorkspace. Returns a placeholder when no repos are tracked,
// and prepends a warning prefix if rows contain errors.
func renderGitTab(ctx context.Context, d Deps) string {
	title := ui.RenderSectionTitle("Git Workspace")
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

	body := ui.RenderGitWorkspace(rows)
	return joinNonEmpty(title, warningPrefix(errCount), body)
}

// buildTabs executes all five Render* functions serially and returns the
// composed tabs and a cached health indicator string. Each renderer returns
// (body, errs) — body strings are joined with joinNonEmpty, errs trigger a
// warning prefix.
func buildTabs(ctx context.Context, d Deps) ([]tab, string) {
	in := stack.StatusInput{
		Cfg:        d.Cfg,
		IsRunning:  d.IsRunning,
		Topo:       d.Topo,
		TopoStatus: d.TopoStatus,
		State:      d.State,
		SvcDeploys: d.SvcDeploys,
		Tracked:    d.Tracked,
	}

	// Services (Apps + Tools + Infra combined)
	appsBody, appsErrs := stack.RenderApps(in)
	toolsBody, toolsErrs := stack.RenderTools(in)
	infraBody, infraErrs := stack.RenderInfra(in)
	serviceWarnings := warningPrefix(len(appsErrs) + len(toolsErrs) + len(infraErrs))
	services := joinNonEmpty(serviceWarnings, appsBody, toolsBody, infraBody)
	if services == "" {
		services = "no services configured"
	}

	// Deploy Status
	deploy := stack.RenderDeployStatus(in)
	if d.State != nil {
		pendingBanner := ui.RenderPendingBanner(d.State.Pending)
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
	daemonRows, daemonErrs := collectDaemonsFn(ctx, d.Cfg, normaliseDocker(d.DockerCfg))
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
	return tabs, stack.HealthIndicator(in)
}

// buildTabsCmd returns a bubbletea command that calls buildTabs and emits
// the result as a tabsLoadedMsg. The gen parameter is captured in the closure
// and returned in the message for stale-message filtering.
func buildTabsCmd(ctx context.Context, d Deps, gen uint64) tea.Cmd {
	return func() tea.Msg {
		tabs, health := buildTabs(ctx, d)
		return tabsLoadedMsg{
			gen:             gen,
			tabs:            tabs,
			loadedAt:        time.Now(),
			healthIndicator: health,
		}
	}
}
