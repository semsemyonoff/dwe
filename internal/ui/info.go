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

	for _, section := range infoCfg.Sections {
		if section.Title != "" {
			sb.WriteString(renderSectionTitle(section.Title))
			sb.WriteByte('\n')
		}

		for _, item := range section.Items {
			show, err := tpl.EvalCondition(item.When, cfg)
			if err != nil {
				return "", fmt.Errorf("section %q item %q when: %w", section.ID, item.Type, err)
			}
			if !show {
				continue
			}

			rendered, err := renderInfoItem(cfg, item)
			if err != nil {
				return "", fmt.Errorf("section %q: %w", section.ID, err)
			}
			sb.WriteString(rendered)
			sb.WriteByte('\n')
		}
	}

	if infoCfg.Footer {
		sb.WriteString(renderSectionTitle(""))
		sb.WriteByte('\n')
	}

	return sb.String(), nil
}

// renderSectionTitle renders a section header line using Lipgloss styling.
// Empty text renders a closing separator line.
func renderSectionTitle(text string) string {
	width := TermWidth()
	if width > 100 {
		width = 100
	}

	if text == "" {
		return styleMuted.Render(strings.Repeat("─", width))
	}

	// Build: ── Title ──────...
	label := styleSectionTitle.Render(" " + text + " ")
	// Strip ANSI for width calculation.
	labelVisible := " " + text + " "
	labelWidth := utf8.RuneCountInString(labelVisible)

	remaining := width - 4 - labelWidth
	if remaining < 0 {
		remaining = 0
	}
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

// renderDefinition formats a single definition line:
//
//	<indent>[icon ]<key> — <value>
func renderDefinition(name, value string, indent int, icon string) string {
	prefix := strings.Repeat(" ", indent)

	var sb strings.Builder
	sb.WriteString(prefix)

	if icon != "" {
		sb.WriteString(icon)
		sb.WriteByte(' ')
	}

	sb.WriteString(styleKey.Render(name))
	sb.WriteString(" ")
	sb.WriteString(styleMuted.Render(defSep))
	sb.WriteString(" ")
	sb.WriteString(styleValue.Render(value))

	return sb.String()
}
