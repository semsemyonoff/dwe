package ui

import (
	"fmt"
	"strings"

	"devbox-cli/internal/config"
)

// RenderSummary returns a compact two-line project summary string.
// It shows the project name, state, and enabled service/tool counts.
// The returned string contains no trailing newline on the last line.
func RenderSummary(cfg *config.DevboxConfig) string {
	var lines []string

	// Line 1: project name and state.
	var parts []string

	name := cfg.Project.FullName()
	parts = append(parts, styleKey.Render("project")+" "+defSep+" "+name)

	if cfg.State != "" {
		parts = append(parts, styleMuted.Render("state")+" "+defSep+" "+cfg.State)
	}

	lines = append(lines, strings.Join(parts, "  "))

	// Line 2: service and tool counts.
	enabledSvcs, totalSvcs := countServices(cfg)
	enabledTools := countTools(cfg)

	line2 := styleMuted.Render(fmt.Sprintf("services %d/%d enabled", enabledSvcs, totalSvcs)) +
		"  " +
		styleMuted.Render(fmt.Sprintf("tools %d enabled", enabledTools))
	lines = append(lines, line2)

	return strings.Join(lines, "\n")
}

// countServices returns (enabled, total) service counts from the config.
func countServices(cfg *config.DevboxConfig) (int, int) {
	total := len(cfg.Services)
	enabled := 0
	for _, svc := range cfg.Services {
		if svc.Enabled || svc.Mandatory {
			enabled++
		}
	}
	return enabled, total
}

// countTools returns the number of enabled tools.
func countTools(cfg *config.DevboxConfig) int {
	n := 0
	for _, t := range cfg.Tools {
		if t.Enabled {
			n++
		}
	}
	return n
}
