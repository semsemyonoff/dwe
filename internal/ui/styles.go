// Package ui provides Lipgloss-based styled rendering for the devbox CLI.
// It is separate from internal/render, which handles plain ANSI output used by
// deploy, docker, and passthrough commands.
package ui

import (
	"os"
	"strings"

	huh "charm.land/huh/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"

	"devbox-cli/internal/config"
)

// hexPair holds a light-mode and dark-mode hex string for a single semantic
// token. ApplyStyles resolves the active mode once via
// lipgloss.HasDarkBackground() and stores the chosen hex on the package-level
// resolved* vars. A non-empty user override in StylesColors applies to both
// modes (no separate light/dark override surface).
type hexPair struct{ Light, Dark string }

// Built-in light/dark defaults for the 7 semantic tokens. These are plain hex
// strings (not lipgloss.AdaptiveColor) because the package-level Color*()
// accessors must return raw hex — they bridge v1 lipgloss (this package), v2
// lipgloss (internal/ui/cmdbrowser), and Fang's ColorScheme.
var (
	defaultAccent  = hexPair{Light: "#0EA5E9", Dark: "#2EC3EB"}
	defaultSuccess = hexPair{Light: "#16A34A", Dark: "#22C55E"}
	defaultWarning = hexPair{Light: "#D97706", Dark: "#F59E0B"}
	defaultDanger  = hexPair{Light: "#DC2626", Dark: "#EF4444"}
	defaultMuted   = hexPair{Light: "#64748B", Dark: "#9AA3BB"}
	defaultBorder  = hexPair{Light: "#CBD5E1", Dark: "#334155"}
	// defaultText is intentionally empty: text token falls back to
	// lipgloss.NoColor{} so the terminal's default foreground is preserved
	// unless the user explicitly sets `text:` in styles.yml.
	defaultText = hexPair{Light: "", Dark: ""}
)

// Resolved hex strings for the 7 semantic tokens. ApplyStyles writes these
// once per call from the user config + light/dark defaults; Color*() accessors
// read them.
var (
	resolvedAccent  string
	resolvedSuccess string
	resolvedWarning string
	resolvedDanger  string
	resolvedMuted   string
	resolvedBorder  string
	resolvedText    string
)

// v1 lipgloss styles for the 7 semantic tokens. Built once per ApplyStyles
// call from the resolved hex values. Used directly by internal/ui consumers.
var (
	styleAccent  lipgloss.Style
	styleSuccess lipgloss.Style
	styleWarning lipgloss.Style
	styleDanger  lipgloss.Style
	styleMuted   lipgloss.Style
	styleBorder  lipgloss.Style
	styleText    lipgloss.Style
)

// Legacy v1 style aliases — Task 3 migrates internal/ui consumers off these
// and removes the aliases. Each alias is reassigned in rebuildSemanticStyles
// alongside the canonical 7 tokens.
var (
	styleKey          lipgloss.Style
	styleSectionTitle lipgloss.Style
	styleSubheader    lipgloss.Style
	styleInfoText     lipgloss.Style
	styleValue        lipgloss.Style
	styleEnabled      lipgloss.Style
	styleDisabled     lipgloss.Style
	styleMandatory    lipgloss.Style
	stylePartial      lipgloss.Style
	styleRunStopped   lipgloss.Style
	styleWarn         lipgloss.Style
	styleCatService   lipgloss.Style
	styleCatTool      lipgloss.Style
	styleCatInfra     lipgloss.Style
	styleTableBorder  lipgloss.Style
	styleTableHeader  lipgloss.Style
)

// Legacy palette color aliases — Task 3 migrates cmdbrowser/palette.go off
// these and removes the aliases.
var (
	colorFocusBorder        string
	colorDescription        string
	colorTreeCount          string
	colorTreeArrow          string
	colorFilterMatch        string
	colorPaginationActive   string
	colorPaginationInactive string
	colorKey                string
	colorInfo               string
	colorSuccess            string
)

// defSep is the delimiter used between a definition label and its value.
var defSep = "—"

func init() {
	// Establish a sane initial palette before any ApplyStyles call so package
	// consumers that read styles at init / first-use don't see zero values.
	rebuildSemanticStyles(config.StylesColors{})
}

// resolveHex picks the light or dark default for a token, honouring a user
// override that applies to both modes. dark indicates the resolved mode.
func resolveHex(override string, def hexPair, dark bool) string {
	if override != "" {
		return override
	}
	if dark {
		return def.Dark
	}
	return def.Light
}

// foreground builds a v1 lipgloss style with the given hex as foreground, or
// with lipgloss.NoColor{} when hex is empty (preserves terminal default text
// color). Used exclusively to construct semantic token styles.
func foreground(hex string) lipgloss.Style {
	if hex == "" {
		return lipgloss.NewStyle().Foreground(lipgloss.NoColor{})
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hex))
}

// rebuildSemanticStyles is the single source of truth for the 7 token styles
// AND the legacy aliases. It resolves user/light/dark per token, then assigns
// every package-level style var.
func rebuildSemanticStyles(c config.StylesColors) {
	dark := lipgloss.HasDarkBackground()
	resolvedAccent = resolveHex(c.Accent, defaultAccent, dark)
	resolvedSuccess = resolveHex(c.Success, defaultSuccess, dark)
	resolvedWarning = resolveHex(c.Warning, defaultWarning, dark)
	resolvedDanger = resolveHex(c.Danger, defaultDanger, dark)
	resolvedMuted = resolveHex(c.Muted, defaultMuted, dark)
	resolvedBorder = resolveHex(c.Border, defaultBorder, dark)
	resolvedText = resolveHex(c.Text, defaultText, dark)

	styleAccent = foreground(resolvedAccent)
	styleSuccess = foreground(resolvedSuccess)
	styleWarning = foreground(resolvedWarning)
	styleDanger = foreground(resolvedDanger)
	styleMuted = foreground(resolvedMuted)
	styleBorder = foreground(resolvedBorder)
	styleText = foreground(resolvedText)

	// Legacy aliases — collapse old per-purpose styles onto the 7 semantic
	// tokens. Task 3 removes these along with their consumers.
	bold := styleAccent.Bold(true)
	styleKey = bold
	styleSectionTitle = bold
	styleSubheader = bold
	styleInfoText = styleAccent
	styleValue = styleText
	styleEnabled = styleSuccess
	styleDisabled = styleMuted
	styleMandatory = bold
	stylePartial = styleWarning
	styleRunStopped = styleDanger
	styleWarn = styleWarning
	styleCatService = styleAccent
	styleCatTool = styleText
	styleCatInfra = styleMuted
	styleTableBorder = styleBorder
	styleTableHeader = bold

	colorFocusBorder = resolvedAccent
	colorDescription = resolvedMuted
	colorTreeCount = resolvedMuted
	colorTreeArrow = resolvedAccent
	colorFilterMatch = resolvedAccent
	colorPaginationActive = resolvedAccent
	colorPaginationInactive = resolvedMuted
	colorKey = resolvedAccent
	colorInfo = resolvedAccent
	colorSuccess = resolvedSuccess
}

// ApplyStyles rebuilds package-level style vars from the provided StylesConfig.
// User-provided non-empty hex strings override the built-in light/dark
// defaults; missing/empty fields fall back to the default for the terminal's
// resolved background mode.
func ApplyStyles(cfg *config.StylesConfig) {
	if cfg == nil {
		rebuildSemanticStyles(config.StylesColors{})
		return
	}
	rebuildSemanticStyles(cfg.Colors)
	if cfg.Separator != "" {
		defSep = cfg.Separator
	}
	apply := buildPaletteApplier(&cfg.Colors)
	huhTheme = huh.ThemeFunc(func(isDark bool) *huh.Styles {
		s := huh.ThemeBase(isDark)
		applyFormGlyphs(s)
		apply(s)
		return s
	})
}

// RenderEnabled applies the enabled/running style to s.
func RenderEnabled(s string) string { return styleSuccess.Render(s) }

// RenderPartial applies the partial style to s.
func RenderPartial(s string) string { return styleWarning.Render(s) }

// RenderStopped applies the alert style to s. Used for "stopped" stack state
// to draw attention.
func RenderStopped(s string) string { return styleDanger.Render(s) }

// StyleKey renders s with the accent style, bold.
// Used for definition labels, command names, and leaf nodes in tree views.
func StyleKey(s string) string { return styleAccent.Bold(true).Render(s) }

// StyleGroup renders s with the accent style, bold. Used for group headers in
// tree views.
func StyleGroup(s string) string { return styleAccent.Bold(true).Render(s) }

// StyleSectionTitle renders s with the accent style, bold. Used for
// pipeline/dashboard top-level headers.
func StyleSectionTitle(s string) string { return styleAccent.Bold(true).Render(s) }

// StyleSubheader renders s with the accent style, bold. Used for phase labels
// and in-section sub-headers.
func StyleSubheader(s string) string { return styleAccent.Bold(true).Render(s) }

// StyleMuted renders s with the muted style.
// Used for tags, separators, and secondary information.
func StyleMuted(s string) string { return styleMuted.Render(s) }

// StyleInfo renders s with the accent style. Used for informational callouts.
func StyleInfo(s string) string { return styleAccent.Render(s) }

// StyleFailed renders s with the danger style. Used for failure icons in step
// history.
func StyleFailed(s string) string { return styleDanger.Render(s) }

// StyleWarning renders s with the warning style. Used for warning icons and
// cautionary prompts.
func StyleWarning(s string) string { return styleWarning.Render(s) }

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
	return renderFGOnly(resolvedMuted, s)
}

func styleInactiveService(serviceType string) lipgloss.Style {
	return serviceTypeStyle(serviceType).Bold(false).Faint(true)
}

func serviceTypeStyle(serviceType string) lipgloss.Style {
	switch serviceType {
	case "app":
		return styleAccent
	case "tool":
		return styleSuccess
	case "infra":
		return styleMuted
	default:
		return styleMuted
	}
}

func serviceTypeColor(serviceType string) string {
	switch serviceType {
	case "app":
		return resolvedAccent
	case "tool":
		return resolvedSuccess
	case "infra":
		return resolvedMuted
	default:
		return resolvedMuted
	}
}

// renderFGOnly wraps s with a foreground-only ANSI escape so callers (huh
// option rendering) can compose bold/faint around the same line without a
// full reset cancelling the surrounding style. Supports both hex (#RRGGBB,
// emitted as truecolor 38;2;R;G;B) and legacy 256-color numeric strings.
func renderFGOnly(color, s string) string {
	if s == "" || color == "" {
		return s
	}
	if strings.HasPrefix(color, "#") {
		r, g, b, ok := parseHex(color)
		if !ok {
			return s
		}
		return ansiTruecolorFG(r, g, b) + s + "\x1b[39m"
	}
	// Numeric 256-color path (legacy ANSI index).
	for _, c := range color {
		if c < '0' || c > '9' {
			return s
		}
	}
	return "\x1b[38;5;" + color + "m" + s + "\x1b[39m"
}

func parseHex(h string) (r, g, b int, ok bool) {
	if len(h) != 7 || h[0] != '#' {
		return 0, 0, 0, false
	}
	v, err := strconvParseHex(h[1:])
	if err != nil {
		return 0, 0, 0, false
	}
	return (v >> 16) & 0xff, (v >> 8) & 0xff, v & 0xff, true
}

func strconvParseHex(s string) (int, error) {
	var v int
	for _, c := range s {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= int(c - '0')
		case c >= 'a' && c <= 'f':
			v |= int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v |= int(c-'A') + 10
		default:
			return 0, os.ErrInvalid
		}
	}
	return v, nil
}

func ansiTruecolorFG(r, g, b int) string {
	var sb strings.Builder
	sb.WriteString("\x1b[38;2;")
	writeInt(&sb, r)
	sb.WriteByte(';')
	writeInt(&sb, g)
	sb.WriteByte(';')
	writeInt(&sb, b)
	sb.WriteByte('m')
	return sb.String()
}

func writeInt(sb *strings.Builder, v int) {
	if v == 0 {
		sb.WriteByte('0')
		return
	}
	var buf [4]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	sb.Write(buf[i:])
}

// ColorAccent returns the resolved hex string for the accent token. Consumed
// by cmdbrowser (v2 lipgloss) via lipgloss.Color(ColorAccent()) and by Fang's
// ColorScheme. Re-resolved on every ApplyStyles call.
func ColorAccent() string { return resolvedAccent }

// ColorSuccess returns the resolved hex string for the success token.
func ColorSuccess() string { return resolvedSuccess }

// ColorWarning returns the resolved hex string for the warning token.
func ColorWarning() string { return resolvedWarning }

// ColorDanger returns the resolved hex string for the danger token.
func ColorDanger() string { return resolvedDanger }

// ColorMuted returns the resolved hex string for the muted token.
func ColorMuted() string { return resolvedMuted }

// ColorBorder returns the resolved hex string for the border token.
func ColorBorder() string { return resolvedBorder }

// ColorText returns the resolved hex string for the text token. May be empty
// when the user has not overridden the default, in which case callers must
// treat it as lipgloss.NoColor{} (terminal default foreground).
func ColorText() string { return resolvedText }

// Legacy accessors — Task 3 migrates callers (cmdbrowser/palette.go,
// cmd/devbox/main.go loadHelpColorScheme) onto the 7 semantic accessors and
// removes these wrappers.
func ColorFocusBorder() string        { return resolvedAccent }
func ColorDescription() string        { return resolvedMuted }
func ColorTreeCount() string          { return resolvedMuted }
func ColorTreeArrow() string          { return resolvedAccent }
func ColorFilterMatch() string        { return resolvedAccent }
func ColorPaginationActive() string   { return resolvedAccent }
func ColorPaginationInactive() string { return resolvedMuted }
func ColorKey() string                { return resolvedAccent }
func ColorInfo() string               { return resolvedAccent }

// TermWidth returns the current terminal width, falling back to 80 when the
// output is not a terminal or the size cannot be determined.
func TermWidth() int {
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w <= 0 {
		return 80
	}
	return w
}
