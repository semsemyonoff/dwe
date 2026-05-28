package ui

import (
	"fmt"
	"strings"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/ui/statusview"
)

// RenderSummary returns a compact project summary string.
// It shows the project name, state, and enabled service/tool counts.
// When deploySummary is provided, also shows deploy status (N/M deployed).
// The returned string contains no trailing newline on the last line.
func RenderSummary(cfg *config.DevboxConfig, deploySummary *statusview.DeploySummary) string {
	var lines []string

	// Project identity now lives in the branded header (ui.RenderBrandHeader);
	// the summary only carries state (when set) and counts.
	if cfg.State != "" {
		lines = append(lines, styleMuted.Render("state")+" "+defSep+" "+cfg.State)
	}

	// Service and tool counts, plus deploy status if available.
	enabledSvcs, totalSvcs := countServices(cfg)
	enabledTools := countTools(cfg)

	var line2Parts []string
	line2Parts = append(line2Parts, styleMuted.Render(fmt.Sprintf("services %d/%d enabled", enabledSvcs, totalSvcs)))
	line2Parts = append(line2Parts, styleMuted.Render(fmt.Sprintf("tools %d enabled", enabledTools)))

	if deploySummary != nil && deploySummary.Total > 0 {
		deployedStr := fmt.Sprintf("services %d/%d deployed", deploySummary.Deployed, deploySummary.Total)
		line2Parts = append(line2Parts, styleMuted.Render(deployedStr))
	}

	lines = append(lines, strings.Join(line2Parts, "  "))

	return strings.Join(lines, "\n")
}

// countServices returns (enabled, total) counts for app-type services only.
// Tools have their own counter; infra is excluded because it's mostly mandatory
// stack plumbing (db, redis, …) that would skew the user-visible "enabled"
// ratio without adding signal.
func countServices(cfg *config.DevboxConfig) (int, int) {
	total := 0
	enabled := 0
	for _, svc := range cfg.Services {
		if !svc.IsApp() {
			continue
		}
		total++
		if svc.Enabled || svc.Required {
			enabled++
		}
	}
	return enabled, total
}

// countTools returns the number of enabled services of type "tool".
func countTools(cfg *config.DevboxConfig) int {
	n := 0
	for _, svc := range cfg.Services {
		if svc.IsTool() && svc.Enabled {
			n++
		}
	}
	return n
}
