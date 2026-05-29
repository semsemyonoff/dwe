package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"devbox-cli/internal/core/ui/styles"
	"devbox-cli/internal/core/workflow/deploy/journal"
)

// DeployInfoRow describes one service's deploy timestamp for the interactive
// deploy menu's "last deploy" banner.
type DeployInfoRow struct {
	Name        string
	Type        string
	DeployedAt  time.Time
	Status      journal.Status
	NotDeployed bool
}

// RenderDeployInfo returns a multi-line summary of the last deploy state for
// display at the top of the interactive deploy menu. Returns "" when there is
// nothing meaningful to show (no project-level state and no per-service
// entries).
//
// Output shape:
//
//	Last deploy: 5m ago (deployed)
//	  ✓ main      app    5m ago
//	  ✓ adminer   tool   5m ago
//	  · worker    app    not deployed
func RenderDeployInfo(state *journal.ProjectState, now time.Time, rows []DeployInfoRow) string {
	if state == nil && len(rows) == 0 {
		return ""
	}

	var sb strings.Builder

	if state != nil && state.Project != nil {
		header := "Last deploy: "
		switch {
		case !state.Project.DeployedAt.IsZero():
			header += relativeTime(state.Project.DeployedAt, now)
		case state.Project.LastRun != nil && !state.Project.LastRun.FinishedAt.IsZero():
			header += relativeTime(state.Project.LastRun.FinishedAt, now)
		default:
			header += "never"
		}
		if state.Project.Status != "" {
			header += " " + styles.StyleMuted("("+string(state.Project.Status)+")")
		}
		fmt.Fprintln(&sb, styles.StyleKey(header))
	}

	if len(rows) == 0 {
		return strings.TrimRight(sb.String(), "\n") + "\n"
	}

	sort.SliceStable(rows, func(i, j int) bool {
		// Deployed (non-zero time) first, then alphabetical by name.
		ai, aj := rows[i].DeployedAt.IsZero(), rows[j].DeployedAt.IsZero()
		if ai != aj {
			return !ai
		}
		return rows[i].Name < rows[j].Name
	})

	nameW, typeW := 0, 0
	for _, r := range rows {
		if l := len([]rune(r.Name)); l > nameW {
			nameW = l
		}
		if l := len([]rune(r.Type)); l > typeW {
			typeW = l
		}
	}

	typeBadgeW := typeW + 2 // brackets

	for _, r := range rows {
		name := padRight(r.Name, nameW)
		typeText := r.Type
		if typeText == "" {
			typeText = "-"
		}
		typeBadge := padRight("["+typeText+"]", typeBadgeW)

		var icon, when string
		if r.DeployedAt.IsZero() {
			icon = styles.StyleOptionMuted("·")
			when = styles.StyleServiceOptionContainer("not deployed")
		} else {
			icon = styles.StyleOptionSuccess("✓")
			when = styles.StyleServiceOptionContainer(relativeTime(r.DeployedAt, now))
		}

		coloredName := styles.StyleServiceOptionName(r.Type, name)
		coloredType := styles.StyleServiceOptionType(r.Type, typeBadge)

		fmt.Fprintf(&sb, "  %s %s  %s  %s\n", icon, coloredName, coloredType, when)
	}

	return sb.String()
}

// FormatRelativeTime formats t relative to now ("5m ago", "2h ago", "3d ago",
// or an absolute date for older). Future times fall back to the absolute
// format.
func FormatRelativeTime(t, now time.Time) string {
	return relativeTime(t, now)
}

// padRight right-pads s with spaces to width w (counting runes, not bytes).
func padRight(s string, w int) string {
	if n := len([]rune(s)); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// relativeTime formats t relative to now ("5m ago", "2h ago", "3d ago", or an
// absolute date for older). Future times fall back to the absolute format.
func relativeTime(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	if d < 0 {
		return t.Format("2006-01-02 15:04")
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	default:
		return t.Format("2006-01-02")
	}
}
