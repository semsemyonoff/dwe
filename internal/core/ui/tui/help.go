package tui

import (
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"

	"charm.land/lipgloss/v2"
)

// help.go builds the ?-modal help overlay from the action registry. The modal
// lists every registered binding grouped by [Section] (label · keys ·
// description rows) and is width-aware so it always fits inside the body region
// the overlay manager centres it over.
//
// i18n key namespace (shipped in internal/shared/i18n/translations/en.yml and
// allowlisted in i18n.KnownUIKeys):
//
//	tui.help.title              → the modal title ("Help")
//	tui.help.section.<name>     → a section label (fallback: the English name)
//	tui.help.action.<actionID>  → a binding description (fallback: Binding.Desc)
//
// Every lookup carries a code-level English fallback, so an unregistered key
// still renders. (Note: the ui: unknown-key validator at
// internal/core/validate/config/ui.go only warns on ui.commands keys in
// workspace.yml and is unrelated to this tui.help.* namespace.)
const (
	helpKeyTitle       = "tui.help.title"
	helpKeySectionPfx  = "tui.help.section."
	helpKeyActionPfx   = "tui.help.action."
	helpDefaultTitle   = "Help"
	helpKeysSeparator  = ", " // joins a binding's multiple keys for display
	helpColGap         = "  " // gap between the keys column and the description
	helpRowIndent      = "  " // indent of a binding row under its section header
	helpBorderPadCells = 4    // border (2) + horizontal padding (2) the box adds
	helpBorderRows     = 2    // top + bottom border rows the box adds (vPadding is 0)
)

// buildHelpOverlay renders the registry's sections and bindings into a bordered
// modal [Overlay], resolving the title, section labels, and descriptions through
// tr with English fallbacks. width and height are the body region dimensions the
// modal must fit within; the content is clamped on BOTH axes (MaxWidth /
// MaxHeight) so the returned overlay never exceeds the body region. This keeps
// the modal looking right (a correctly-sized box rather than a trimmed one):
// [Composite] does clamp an oversized overlay to the body as a last-resort
// safety net, but that truncates the box edge, so sizing here is what produces
// good output at small-but-permitted sizes (tooNarrow only floors height at
// minHeight). locale is required because [i18n.Translator.T] takes it.
//
// Only [Binding.Keys] are rendered in the modal; [Binding.Aliases] are
// intentionally excluded — they dispatch (Match resolves them) but are hidden
// from the help modal to avoid cluttering the display.
func buildHelpOverlay(reg *Registry, tr i18n.Translator, locale string, width, height int) Overlay {
	if tr == nil {
		tr = i18n.NopTranslator{}
	}

	sections := reg.Sections()

	// Pass 1: resolve every display string and find the keys-column width so the
	// descriptions align across all sections.
	type row struct {
		keys string
		desc string
	}
	rendered := make([][]row, len(sections))
	labels := make([]string, len(sections))
	keyCol := 0
	for si, sec := range sections {
		labels[si] = tr.T(locale, helpKeySectionPfx+strings.ToLower(sec.Name), sec.Name)
		rows := make([]row, 0, len(sec.Entries))
		for _, e := range sec.Entries {
			keys := strings.Join(e.Binding.Keys, helpKeysSeparator)
			desc := tr.T(locale, helpKeyActionPfx+string(e.Action), e.Binding.Desc)
			if w := lipgloss.Width(keys); w > keyCol {
				keyCol = w
			}
			rows = append(rows, row{keys: keys, desc: desc})
		}
		rendered[si] = rows
	}

	// Pass 2: assemble the content lines (title, then per-section header + rows).
	var lines []string
	title := tr.T(locale, helpKeyTitle, helpDefaultTitle)
	lines = append(lines, lipgloss.NewStyle().Bold(true).Render(title), "")
	for si := range sections {
		if si > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(styles.ColorAccent())).
			Render(labels[si]))
		for _, r := range rendered[si] {
			pad := strings.Repeat(" ", max(0, keyCol-lipgloss.Width(r.keys)))
			lines = append(lines, helpRowIndent+r.keys+pad+helpColGap+r.desc)
		}
	}

	content := strings.Join(lines, "\n")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(styles.ColorBorder())).
		Padding(0, 1)
	// Width-aware: clamp the inner content so the bordered box never exceeds the
	// body region width. MaxWidth truncates each line to the available inner
	// width (width minus the border + padding cells the box adds).
	if inner := width - helpBorderPadCells; inner > 0 {
		box = box.MaxWidth(width)
		content = lipgloss.NewStyle().MaxWidth(inner).Render(content)
	}
	// Height-aware: clamp the row count so the bordered box never exceeds the
	// body region height. Without this, a help modal taller than a small (but
	// permitted) terminal grows the composited frame past the screen, since
	// Composite does not clip vertically.
	if innerH := height - helpBorderRows; innerH > 0 {
		box = box.MaxHeight(height)
		content = lipgloss.NewStyle().MaxHeight(innerH).Render(content)
	}

	out := box.Render(content)
	return Overlay{
		Content: out,
		Width:   lipgloss.Width(out),
		Height:  lipgloss.Height(out),
	}
}
