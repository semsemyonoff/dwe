package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"devbox-cli/internal/config"
	"devbox-cli/internal/tpl"
)

// defaultIndent is the leading-space indent applied to definition items that
// do not specify an explicit indent value.
const defaultIndent = 2

// RenderInfo builds and returns the full styled info dashboard string for the
// given devbox config and info configuration. It replaces the legacy
// table-header / definition rendering in internal/command/info.go.
//
// Returns an error if any Go template expression in text/value/when fields
// fails to evaluate.
func RenderInfo(cfg *config.DevboxConfig, infoCfg *config.InfoConfig) (string, error) {
	var sb strings.Builder
	renderedAnySection := false

	for _, section := range infoCfg.Sections {
		// First pass: evaluate when: conditions for all items and collect survivors.
		// Items without when: always survive. This ensures decorative items (warning,
		// info, subheader) without when: are always counted as content, making a section
		// with such items never "empty".
		survivors := []config.InfoItem{}
		for _, item := range section.Items {
			show, err := tpl.EvalCondition(item.When, cfg)
			if err != nil {
				return "", fmt.Errorf("section %q item %q when: %w", section.ID, item.Type, err)
			}
			if show {
				survivors = append(survivors, item)
			}
		}

		// If no items survived and the section is marked hide_on_empty, skip it entirely.
		if len(survivors) == 0 && section.HideOnEmpty {
			continue
		}

		// Section is rendered: render title if present, then all surviving items.
		if section.Title != "" {
			sb.WriteString(renderSectionTitle(section.Title))
			sb.WriteByte('\n')
		}

		for _, item := range survivors {
			rendered, err := renderInfoItem(cfg, item)
			if err != nil {
				return "", fmt.Errorf("section %q: %w", section.ID, err)
			}
			sb.WriteString(rendered)
			sb.WriteByte('\n')
		}

		renderedAnySection = true
	}

	// Footer is rendered only if at least one section was rendered.
	if infoCfg.Footer && renderedAnySection {
		sb.WriteString(renderSectionTitle(""))
		sb.WriteByte('\n')
	}

	return sb.String(), nil
}

// RenderSectionTitle renders a section header line using Lipgloss styling.
// Empty text renders a closing separator line.
func RenderSectionTitle(text string) string {
	return renderSectionTitle(text)
}

// RenderSubheader renders a bold yellow in-section subheader.
// Used for grouping sections within a larger block (e.g. Steps, Params).
func RenderSubheader(text string) string {
	return styleSubheader.Render(text)
}

// RenderDefinition renders a styled "key — value" definition line.
// Wraps the internal renderDefinition helper for use outside the ui package.
func RenderDefinition(name, value string, indent int, icon string) string {
	return renderDefinition(name, value, indent, icon)
}

// renderSectionTitle is the internal implementation of RenderSectionTitle.
func renderSectionTitle(text string) string {
	width := min(TermWidth(), 100)

	if text == "" {
		return styleMuted.Render(strings.Repeat("─", width))
	}

	// Build: ── Title ──────...
	label := styleSectionTitle.Render(" " + text + " ")
	// Strip ANSI for width calculation.
	labelVisible := " " + text + " "
	labelWidth := utf8.RuneCountInString(labelVisible)

	remaining := max(width-4-labelWidth, 0)
	leftDash := styleMuted.Render("──")
	rightDash := styleMuted.Render(strings.Repeat("─", remaining+2))

	return leftDash + label + rightDash
}

// renderInfoItem renders a single info item to a string (without trailing newline).
func renderInfoItem(cfg *config.DevboxConfig, item config.InfoItem) (string, error) {
	switch item.Type {
	case "subheader":
		text, err := tpl.Render(item.Text, cfg)
		if err != nil {
			return "", err
		}
		return styleSubheader.Render(text), nil

	case "definition":
		value, err := tpl.Render(item.Value, cfg)
		if err != nil {
			return "", err
		}
		indent := defaultIndent
		if item.Indent.IsSet() {
			indent = item.Indent.Value()
		}
		return renderDefinition(item.Name, value, indent, item.Icon), nil

	case "warning":
		text, err := tpl.Render(item.Text, cfg)
		if err != nil {
			return "", err
		}
		return styleWarn.Render(text), nil

	case "info":
		text, err := tpl.Render(item.Text, cfg)
		if err != nil {
			return "", err
		}
		prefix := strings.Repeat(" ", item.Indent.Value())
		return styleInfoText.Render(prefix + text), nil

	case "separator":
		return "", nil

	default:
		// Unknown types are silently skipped for forward compatibility.
		return "", nil
	}
}

// renderDefinition formats a definition line, wrapping long values at word
// boundaries to fit the terminal width. Continuation lines are aligned under
// the start of the value on the first line.
//
//	<indent>[icon ]<key> — first line of value
//	                       continuation aligned here
func renderDefinition(name, value string, indent int, icon string) string {
	iconWidth := 0
	iconPrefix := ""
	if icon != "" {
		iconWidth = utf8.RuneCountInString(icon) + 1
		iconPrefix = icon + " "
	}

	sep := defSep
	// Visible overhead: indent + icon + name + " " + sep + " "
	overhead := indent + iconWidth + utf8.RuneCountInString(name) + 1 + utf8.RuneCountInString(sep) + 1
	maxValue := max(TermWidth()-overhead, 20)

	lines := wordWrap(value, maxValue)

	prefix := strings.Repeat(" ", indent)
	contPrefix := strings.Repeat(" ", overhead)

	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteString(iconPrefix)
	sb.WriteString(styleKey.Render(name))
	sb.WriteString(" ")
	sb.WriteString(styleMuted.Render(sep))
	sb.WriteString(" ")
	sb.WriteString(styleValue.Render(lines[0]))
	for _, l := range lines[1:] {
		sb.WriteByte('\n')
		sb.WriteString(contPrefix)
		sb.WriteString(styleValue.Render(l))
	}

	return sb.String()
}

// wordWrap splits text into lines of at most width runes, breaking at word
// boundaries. Always returns at least one element.
func wordWrap(text string, width int) []string {
	if width <= 0 || utf8.RuneCountInString(text) <= width {
		return []string{text}
	}

	runes := []rune(text)
	var lines []string

	for len(runes) > 0 {
		if len(runes) <= width {
			lines = append(lines, string(runes))
			break
		}
		cut := -1
		for i := width; i >= 1; i-- {
			if runes[i] == ' ' {
				cut = i
				break
			}
		}
		if cut < 0 {
			lines = append(lines, string(runes[:width]))
			runes = runes[width:]
		} else {
			lines = append(lines, string(runes[:cut]))
			runes = runes[cut+1:]
		}
	}

	return lines
}
