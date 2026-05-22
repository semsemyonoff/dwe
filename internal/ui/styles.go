// Package ui provides Lipgloss-based styled rendering for the devbox CLI.
// It is separate from internal/render, which handles plain ANSI output used by
// deploy, docker, and passthrough commands.
package ui

import (
	"os"
	"strconv"

	huh "charm.land/huh/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"

	"devbox-cli/internal/config"
)

// Styles follow the Fang aesthetic: simple, high-contrast, terminal-friendly.
// All styles use ANSI 256-color palette codes for broad terminal compatibility.
var (
	// styleKey renders definition labels: bold bright-blue.
	styleKey = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)

	// styleSectionTitle renders section headers: bold cyan.
	styleSectionTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))

	// styleSubheader renders in-section subheaders: bold yellow.
	styleSubheader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))

	// styleMuted renders secondary / count text: dim gray.
	styleMuted = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	// styleWarn renders warning messages: yellow.
	styleWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))

	// styleInfoText renders info messages: bright blue / cyan.
	styleInfoText = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))

	// styleValue renders definition values: no styling (inherits terminal default).
	styleValue = lipgloss.NewStyle()

	// Semantic status styles.
	styleEnabled    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleDisabled   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleMandatory  = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	stylePartial    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleRunStopped = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))

	// Topology category styles.
	styleCatService = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleCatTool    = lipgloss.NewStyle().Foreground(lipgloss.Color("67"))
	styleCatInfra   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	// Table styles.
	styleTableBorder = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleTableHeader = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)

	// Command browser palette — stored as raw color strings so both lipgloss v1
	// and charm.land/lipgloss/v2 callers can consume the same source of truth
	// via the Color*() accessors. internal/ui/cmdbrowser/palette.go is the only
	// sanctioned bridge that wraps these into v2 styles.
	colorFocusBorder        = "12"
	colorDescription        = "8"
	colorTreeCount          = "8"
	colorTreeArrow          = "6"
	colorFilterMatch        = "12"
	colorPaginationActive   = "12"
	colorPaginationInactive = "8"

	// Semantic palette mirrors of the v1 styleKey / styleInfoText / styleEnabled
	// colors so cmdbrowser (v2 lipgloss) can pick them up through the shared
	// string-typed Color*() accessors. Driven from the same YAML fields
	// (Label / Info / Enabled) as the v1 styles so a single styles.yml update
	// keeps both versions in sync.
	colorKey     = "12"
	colorInfo    = "12"
	colorSuccess = "2"
)

// defSep is the delimiter used between a definition label and its value.
var defSep = "—"

// ApplyStyles rebuilds package-level style vars from the provided StylesConfig.
// Fields with empty color strings are skipped — the existing defaults are preserved.
func ApplyStyles(cfg *config.StylesConfig) {
	if cfg == nil {
		return
	}
	c := cfg.Colors
	if c.Label != "" {
		styleKey = lipgloss.NewStyle().Foreground(lipgloss.Color(c.Label)).Bold(true)
		colorKey = c.Label
	}
	if c.SectionTitle != "" {
		styleSectionTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(c.SectionTitle))
	}
	if c.SubHeader != "" {
		styleSubheader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(c.SubHeader))
	}
	if c.Muted != "" {
		styleMuted = lipgloss.NewStyle().Foreground(lipgloss.Color(c.Muted))
	}
	if c.Warning != "" {
		styleWarn = lipgloss.NewStyle().Foreground(lipgloss.Color(c.Warning))
	}
	if c.Info != "" {
		styleInfoText = lipgloss.NewStyle().Foreground(lipgloss.Color(c.Info))
		colorInfo = c.Info
	}
	if c.Enabled != "" {
		styleEnabled = lipgloss.NewStyle().Foreground(lipgloss.Color(c.Enabled))
		colorSuccess = c.Enabled
	}
	if c.Disabled != "" {
		styleDisabled = lipgloss.NewStyle().Foreground(lipgloss.Color(c.Disabled))
	}
	if c.Mandatory != "" {
		styleMandatory = lipgloss.NewStyle().Foreground(lipgloss.Color(c.Mandatory)).Bold(true)
	}
	if c.Partial != "" {
		stylePartial = lipgloss.NewStyle().Foreground(lipgloss.Color(c.Partial))
	}
	if c.TableBorder != "" {
		styleTableBorder = lipgloss.NewStyle().Foreground(lipgloss.Color(c.TableBorder))
	}
	if c.TableHeader != "" {
		styleTableHeader = lipgloss.NewStyle().Foreground(lipgloss.Color(c.TableHeader)).Bold(true)
	}
	if c.FocusBorder != "" {
		colorFocusBorder = c.FocusBorder
	}
	if c.Description != "" {
		colorDescription = c.Description
	}
	if c.TreeCount != "" {
		colorTreeCount = c.TreeCount
	}
	if c.TreeArrow != "" {
		colorTreeArrow = c.TreeArrow
	}
	if c.FilterMatch != "" {
		colorFilterMatch = c.FilterMatch
	}
	if c.PaginationActive != "" {
		colorPaginationActive = c.PaginationActive
	}
	if c.PaginationInactive != "" {
		colorPaginationInactive = c.PaginationInactive
	}
	if cfg.Separator != "" {
		defSep = cfg.Separator
	}
	apply := buildPaletteApplier(&c)
	huhTheme = huh.ThemeFunc(func(isDark bool) *huh.Styles {
		s := huh.ThemeBase(isDark)
		applyFormGlyphs(s)
		apply(s)
		return s
	})
}

// RenderEnabled applies the enabled/running style to s.
func RenderEnabled(s string) string { return styleEnabled.Render(s) }

// RenderPartial applies the partial style to s.
func RenderPartial(s string) string { return stylePartial.Render(s) }

// RenderStopped applies the mandatory/alert style to s.
// Used for "stopped" stack state to draw attention (coral red, bold).
func RenderStopped(s string) string { return styleMandatory.Render(s) }

// StyleKey renders s with the key/label style (bright-blue, bold).
// Used for definition labels, command names, and leaf nodes in tree views.
func StyleKey(s string) string { return styleKey.Render(s) }

// StyleGroup renders s with the group/section-title style (cyan, bold).
// Used for group headers in tree views.
func StyleGroup(s string) string { return styleSectionTitle.Render(s) }

// StyleSectionTitle renders s with the section title style (cyan, bold).
// Used for pipeline/dashboard top-level headers.
func StyleSectionTitle(s string) string { return styleSectionTitle.Render(s) }

// StyleSubheader renders s with the subheader style (yellow, bold).
// Used for phase labels and in-section sub-headers.
func StyleSubheader(s string) string { return styleSubheader.Render(s) }

// StyleMuted renders s with the muted/dim style.
// Used for tags, separators, and secondary information.
func StyleMuted(s string) string { return styleMuted.Render(s) }

// StyleInfo renders s with the info style (bright blue/cyan).
// Used for informational callouts.
func StyleInfo(s string) string { return styleInfoText.Render(s) }

// StyleFailed renders s with the failed/error style (red).
// Used for failure icons in step history.
func StyleFailed(s string) string { return styleRunStopped.Render(s) }

// StyleWarning renders s with the warning style (yellow).
// Used for warning icons and cautionary prompts.
func StyleWarning(s string) string { return styleWarn.Render(s) }

// StyleServiceName renders a service name with the color assigned to its type.
// Inactive services are dimmed and intentionally not bold.
func StyleServiceName(serviceType, s string, active bool) string {
	if !active {
		return styleInactiveService(serviceType).Render(s)
	}
	return serviceTypeStyle(serviceType).Bold(true).Render(s)
}

// StyleServiceType renders a service type badge with its type color.
func StyleServiceType(serviceType, s string, active bool) string {
	if !active {
		return styleInactiveService(serviceType).Render(s)
	}
	return serviceTypeStyle(serviceType).Render(s)
}

// StyleServiceContainer renders service container metadata as secondary text.
func StyleServiceContainer(s string, active bool) string {
	if !active {
		return styleMuted.Faint(true).Render(s)
	}
	return styleMuted.Render(s)
}

// StyleServiceOptionName renders a service option segment without a full ANSI
// reset, so huh can still apply dynamic selected/unselected bold/faint styles
// around the full option line.
func StyleServiceOptionName(serviceType, s string) string {
	return renderFGOnly(serviceTypeColor(serviceType), s)
}

// StyleServiceOptionType renders a service type badge for huh option text.
func StyleServiceOptionType(serviceType, s string) string {
	return renderFGOnly(serviceTypeColor(serviceType), s)
}

// StyleServiceOptionContainer renders container metadata for huh option text.
func StyleServiceOptionContainer(s string) string {
	return renderFGOnly(colorDescription, s)
}

func styleInactiveService(serviceType string) lipgloss.Style {
	return serviceTypeStyle(serviceType).Bold(false).Faint(true)
}

func serviceTypeStyle(serviceType string) lipgloss.Style {
	switch serviceType {
	case "app":
		return styleCatService
	case "tool":
		return styleCatTool
	case "infra":
		return styleCatInfra
	default:
		return styleMuted
	}
}

func serviceTypeColor(serviceType string) string {
	switch serviceType {
	case "app":
		return "6"
	case "tool":
		return "67"
	case "infra":
		return "8"
	default:
		return colorDescription
	}
}

func renderFGOnly(color, s string) string {
	if s == "" {
		return ""
	}
	if _, err := strconv.Atoi(color); err != nil {
		return s
	}
	return "\x1b[38;5;" + color + "m" + s + "\x1b[39m"
}

// ColorFocusBorder returns the raw color string used by the command browser
// for focused-panel borders. The value is consumable by both lipgloss v1 and
// charm.land/lipgloss/v2 via their respective lipgloss.Color(s) constructors.
func ColorFocusBorder() string { return colorFocusBorder }

// ColorDescription returns the raw color string for secondary description text
// (item subtitles, faint tree captions).
func ColorDescription() string { return colorDescription }

// ColorTreeCount returns the raw color string for "(N)" counters in the left
// tree of the command browser.
func ColorTreeCount() string { return colorTreeCount }

// ColorTreeArrow returns the raw color string for tree disclosure glyphs (▸/▾).
func ColorTreeArrow() string { return colorTreeArrow }

// ColorFilterMatch returns the raw color string used to highlight characters
// matched by the active filter inside the command list.
func ColorFilterMatch() string { return colorFilterMatch }

// ColorPaginationActive returns the raw color string for the active pagination
// dot in the bubbles list.
func ColorPaginationActive() string { return colorPaginationActive }

// ColorPaginationInactive returns the raw color string for inactive pagination
// dots.
func ColorPaginationInactive() string { return colorPaginationInactive }

// ColorKey returns the raw color string for high-prominence labels (titles,
// breadcrumbs, focused emphasis). Mirrors the v1 styleKey color and is driven
// by the StylesColors.Label YAML field.
func ColorKey() string { return colorKey }

// ColorInfo returns the raw color string for informational accents. Mirrors
// the v1 styleInfoText color and is driven by the StylesColors.Info YAML
// field.
func ColorInfo() string { return colorInfo }

// ColorSuccess returns the raw color string for success / enabled accents
// (e.g. the cmdbrowser "[--yes ON]" toggle). Mirrors the v1 styleEnabled color
// and is driven by the StylesColors.Enabled YAML field.
func ColorSuccess() string { return colorSuccess }

// TermWidth returns the current terminal width, falling back to 80 when the
// output is not a terminal or the size cannot be determined.
func TermWidth() int {
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w <= 0 {
		return 80
	}
	return w
}
