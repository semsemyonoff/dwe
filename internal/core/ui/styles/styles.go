// Package styles provides Lipgloss-based theme primitives for the dwe CLI:
// the 7-token semantic palette (accent/success/warning/danger/muted/border/text),
// rendering helpers, icon classification, and huh.Theme construction.
//
// It is separate from internal/shared/render, which handles plain ANSI output
// used by deploy, docker, and passthrough commands.
package styles

import (
	"os"
	"strings"

	huh "charm.land/huh/v2"
	huhlip "charm.land/lipgloss/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
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
// lipgloss (internal/core/ui/cmdbrowser), and Fang's ColorScheme.
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
// call from the resolved hex values. Used directly by sibling packages via
// the AccentStyle()/MutedStyle()/... accessor functions below.
var (
	styleAccent  lipgloss.Style
	styleSuccess lipgloss.Style
	styleWarning lipgloss.Style
	styleDanger  lipgloss.Style
	styleMuted   lipgloss.Style
	styleBorder  lipgloss.Style
	styleText    lipgloss.Style
)

// DefSep is the delimiter used between a definition label and its value.
// ApplyStyles mutates this from StylesConfig.Separator when non-empty.
var DefSep = "—"

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
		DefSep = cfg.Separator
	}
	apply := BuildPaletteApplier()
	HuhTheme = huh.ThemeFunc(func(isDark bool) *huh.Styles {
		s := huh.ThemeBase(isDark)
		ApplyFormGlyphs(s)
		apply(s)
		return s
	})
}

// AccentStyle returns the current lipgloss.Style for the accent token. The
// returned value is a copy; mutating it does not affect the package palette.
// Used by sibling packages (render, widgets) that need to chain .Bold(true) /
// .Render(text) without going through the StyleX(string) wrapper.
func AccentStyle() lipgloss.Style { return styleAccent }

// MutedStyle returns the current lipgloss.Style for the muted token.
func MutedStyle() lipgloss.Style { return styleMuted }

// SuccessStyle returns the current lipgloss.Style for the success token.
func SuccessStyle() lipgloss.Style { return styleSuccess }

// WarningStyle returns the current lipgloss.Style for the warning token.
func WarningStyle() lipgloss.Style { return styleWarning }

// DangerStyle returns the current lipgloss.Style for the danger token.
func DangerStyle() lipgloss.Style { return styleDanger }

// BorderStyle returns the current lipgloss.Style for the border token.
func BorderStyle() lipgloss.Style { return styleBorder }

// TextStyle returns the current lipgloss.Style for the text token.
func TextStyle() lipgloss.Style { return styleText }

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
	return ServiceTypeStyle(serviceType).Bold(true).Render(s)
}

// StyleServiceType renders a service type badge with its type color.
func StyleServiceType(serviceType, s string, active bool) string {
	if !active {
		return styleInactiveService(serviceType).Render(s)
	}
	return ServiceTypeStyle(serviceType).Render(s)
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

// StyleOptionSuccess renders s in the success color, FG-only (composable with
// huh's bold/faint wrappers without a full ANSI reset).
func StyleOptionSuccess(s string) string {
	return renderFGOnly(resolvedSuccess, s)
}

// StyleOptionMuted renders s in the muted color, FG-only.
func StyleOptionMuted(s string) string {
	return renderFGOnly(resolvedMuted, s)
}

// StyleOptionWarning renders s in the warning color, FG-only.
func StyleOptionWarning(s string) string {
	return renderFGOnly(resolvedWarning, s)
}

func styleInactiveService(serviceType string) lipgloss.Style {
	return ServiceTypeStyle(serviceType).Bold(false).Faint(true)
}

// ServiceTypeStyle returns the lipgloss.Style assigned to a service type
// ("app", "tool", "infra"). Exposed so external renderers (e.g. the topology
// tree) keep the color mapping in one place.
func ServiceTypeStyle(serviceType string) lipgloss.Style {
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

// TermWidth returns the current terminal width, falling back to 80 when the
// output is not a terminal or the size cannot be determined.
func TermWidth() int {
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w <= 0 {
		return 80
	}
	return w
}

// Test seams for TermWidthOrZero, following the cmdbrowser/fallback.go
// convention: production code indirects through these package-level vars so
// tests can simulate TTY/non-TTY and specific widths without a real
// terminal. Tests reassign and restore via t.Cleanup.
var (
	termWidthIsTerminalFn = func(f *os.File) bool { return term.IsTerminal(f.Fd()) }
	termWidthGetSizeFn    = func(f *os.File) (w, h int, err error) { return term.GetSize(f.Fd()) }
)

// TermWidthOrZero returns f's terminal width, or 0 when f is not a terminal
// or its size is unknown. Unlike TermWidth it has no 80-column fallback:
// callers use 0 to mean "unbounded", which is the correct behavior for a
// pipe or file — TermWidth's fallback would otherwise silently push every
// piped run and every test into narrow mode.
func TermWidthOrZero(f *os.File) int {
	if !termWidthIsTerminalFn(f) {
		return 0
	}
	w, _, err := termWidthGetSizeFn(f)
	if err != nil || w <= 0 {
		return 0
	}
	return w
}

// HuhTheme is the package-level huh.Theme built from workspace/styles.yml.
// It defaults to ThemeBase + dwe glyph overrides (no project palette
// applied) until ApplyStyles is called.
var HuhTheme huh.Theme = huh.ThemeFunc(func(isDark bool) *huh.Styles {
	s := huh.ThemeBase(isDark)
	ApplyFormGlyphs(s)
	applyMultiSelectStateStyles(s, resolvedSuccess, resolvedMuted)
	return s
})

// ApplyFormGlyphs replaces the default huh prefix glyphs with the dwe look:
// "✓ " for selected items, "• " for unselected. Coloring is handled separately
// by BuildPaletteApplier so the glyphs always render even without a palette.
func ApplyFormGlyphs(s *huh.Styles) {
	s.Focused.SelectedPrefix = s.Focused.SelectedPrefix.SetString("✓ ")
	s.Focused.UnselectedPrefix = s.Focused.UnselectedPrefix.SetString("• ")
	s.Blurred.SelectedPrefix = s.Blurred.SelectedPrefix.SetString("✓ ")
	s.Blurred.UnselectedPrefix = s.Blurred.UnselectedPrefix.SetString("• ")
}

func applyMultiSelectStateStyles(s *huh.Styles, selectedColor, unselectedColor string) {
	selected := huhlip.Color(selectedColor)
	unselected := huhlip.Color(unselectedColor)

	s.Focused.SelectedOption = s.Focused.SelectedOption.Foreground(selected).Bold(true).Faint(false)
	s.Focused.SelectedPrefix = s.Focused.SelectedPrefix.Foreground(selected).Bold(true).Faint(false)
	s.Blurred.SelectedOption = s.Blurred.SelectedOption.Foreground(selected).Bold(true).Faint(false)
	s.Blurred.SelectedPrefix = s.Blurred.SelectedPrefix.Foreground(selected).Bold(true).Faint(false)

	s.Focused.UnselectedOption = s.Focused.UnselectedOption.Foreground(unselected).Bold(false).Faint(true)
	s.Focused.UnselectedPrefix = s.Focused.UnselectedPrefix.Foreground(unselected).Bold(false).Faint(true)
	s.Blurred.UnselectedOption = s.Blurred.UnselectedOption.Foreground(unselected).Bold(false).Faint(true)
	s.Blurred.UnselectedPrefix = s.Blurred.UnselectedPrefix.Foreground(unselected).Bold(false).Faint(true)
}

// Theme returns the current package-level huh.Theme.
// All huh form/field call sites should use .WithTheme(styles.Theme()) so they
// automatically pick up palette changes from styles.yml.
func Theme() huh.Theme {
	return HuhTheme
}

// BuildPaletteApplier returns a function that applies project palette colors
// to a *huh.Styles in place. The returned function is safe to call multiple
// times on different *huh.Styles values (no shared state).
//
// Palette mapping (7-token → *huh.Styles):
//   - accent  → Focused.Title, Group.Title, SelectSelector, MultiSelectSelector,
//     Option, TextInput.Prompt, NextIndicator, PrevIndicator
//   - muted   → Focused.Description, Group.Description, Blurred.Title,
//     Blurred.Description, UnselectedOption, TextInput.Placeholder
//   - success → Focused.SelectedOption, Focused.SelectedPrefix (multi-select checked)
//   - danger  → Focused.ErrorIndicator, Focused.ErrorMessage
//
// The applier reads from the resolved token values (resolvedAccent, etc.) so
// passing nil or an empty StylesColors still produces a fully-themed *huh.Styles
// — empty user overrides have already been resolved to the built-in defaults
// by rebuildSemanticStyles.
func BuildPaletteApplier() func(*huh.Styles) {
	return func(s *huh.Styles) {
		accent := huhlip.Color(resolvedAccent)
		muted := huhlip.Color(resolvedMuted)
		danger := huhlip.Color(resolvedDanger)

		s.Focused.Title = s.Focused.Title.Foreground(accent).Bold(true)
		s.Group.Title = s.Group.Title.Foreground(accent).Bold(true)

		s.Focused.Description = s.Focused.Description.Foreground(muted)
		s.Group.Description = s.Group.Description.Foreground(muted)

		s.Focused.SelectSelector = s.Focused.SelectSelector.Foreground(accent)
		s.Focused.MultiSelectSelector = s.Focused.MultiSelectSelector.Foreground(accent)
		s.Focused.Option = s.Focused.Option.Foreground(accent)

		s.Blurred.Title = s.Blurred.Title.Foreground(muted)
		s.Blurred.Description = s.Blurred.Description.Foreground(muted)
		s.Focused.TextInput.Placeholder = s.Focused.TextInput.Placeholder.Foreground(muted)

		s.Focused.ErrorIndicator = s.Focused.ErrorIndicator.Foreground(danger)
		s.Focused.ErrorMessage = s.Focused.ErrorMessage.Foreground(danger)

		s.Focused.TextInput.Prompt = s.Focused.TextInput.Prompt.Foreground(accent)
		s.Focused.NextIndicator = s.Focused.NextIndicator.Foreground(accent)
		s.Focused.PrevIndicator = s.Focused.PrevIndicator.Foreground(accent)

		applyMultiSelectStateStyles(s, resolvedSuccess, resolvedMuted)
	}
}
