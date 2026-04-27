// Package ui provides Lipgloss-based styled rendering for the devbox CLI.
// It is separate from internal/render, which handles plain ANSI output used by
// deploy, docker, and passthrough commands.
package ui

import (
	"os"

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
	styleCatTool    = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	styleCatInfra   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	// Table styles.
	styleTableBorder = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleTableHeader = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
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
	}
	if c.Enabled != "" {
		styleEnabled = lipgloss.NewStyle().Foreground(lipgloss.Color(c.Enabled))
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

// TermWidth returns the current terminal width, falling back to 80 when the
// output is not a terminal or the size cannot be determined.
func TermWidth() int {
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w <= 0 {
		return 80
	}
	return w
}
