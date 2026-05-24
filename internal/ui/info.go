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
		out, rendered, err := renderBlock(cfg, section.Items, section.HideOnEmpty, section.Title, true, section.ID, "")
		if err != nil {
			return "", err
		}
		if rendered {
			sb.WriteString(out)
			sb.WriteByte('\n')
		}
	}

	// Footer is rendered only if at least one section produced output.
	if infoCfg.Footer && sb.Len() > 0 {
		sb.WriteString(renderSectionTitle(""))
		sb.WriteByte('\n')
	}

	return sb.String(), nil
}

// renderBlock renders a section or a subgroup uniformly, recursively handling
// nested subgroups. It evaluates when: conditions, counts content vs. decorative
// items, and applies hide_on_empty.
//
// Parameters:
//   - cfg: the DevboxConfig for template evaluation
//   - items: the items to render (section items or subgroup items)
//   - hideOnEmpty: section.HideOnEmpty for sections, item.SubgroupHideOnEmpty() for subgroups
//   - title: section.Title for sections, item.Title for subgroups (NOT item.Text)
//     Subgroup title should be pre-rendered via tpl.Render before passing in.
//   - asSection: true → renderSectionTitle(title); false → styleAccent.Bold(true).Render(title)
//   - sectionID: section ID for error messages (e.g., "tools"); passed through recursion
//   - itemPath: dot-separated path for nested items (e.g., "" → "items[0]" → "items[0].items[1]")
//
// Returns:
//   - out: the rendered text (empty iff the block produced nothing)
//   - rendered: biconditional with `out != ""` — true iff `out` is non-empty.
//     Parent uses this to decide whether to append `out`. Does NOT imply "counts as content".
//   - err: any template/when/recursion error, fully propagated
func renderBlock(
	cfg *config.DevboxConfig,
	items []config.InfoItem,
	hideOnEmpty bool,
	title string,
	asSection bool,
	sectionID string,
	itemPath string,
) (out string, rendered bool, err error) {
	var survivors []string
	contentCount := 0

	for idx, item := range items {
		// Build path for error reporting
		var currentPath string
		if itemPath == "" {
			currentPath = fmt.Sprintf("items[%d]", idx)
		} else {
			currentPath = fmt.Sprintf("%s.items[%d]", itemPath, idx)
		}

		// Evaluate when: condition
		show, err := tpl.EvalCondition(item.When, cfg)
		if err != nil {
			return "", false, fmt.Errorf("section %q %s when: %w", sectionID, currentPath, err)
		}
		if !show {
			continue
		}

		// Handle subgroup items specially (recursive)
		if item.Type == "subgroup" {
			// Render the subgroup title
			renderedTitle, err := tpl.Render(item.Title, cfg)
			if err != nil {
				return "", false, fmt.Errorf("section %q %s (subgroup) title: %w", sectionID, currentPath, err)
			}

			// Recursively render the subgroup
			subOut, subRendered, err := renderBlock(
				cfg,
				item.Items,
				item.SubgroupHideOnEmpty(),
				renderedTitle,
				false, // subgroup, not section
				sectionID,
				currentPath,
			)
			if err != nil {
				return "", false, err
			}

			// Only add to survivors if the subgroup rendered
			if subRendered {
				survivors = append(survivors, subOut)
				if !item.IsDecorative() {
					contentCount++
				}
			}
		} else {
			// Non-subgroup item: render via renderInfoItem
			itemOut, err := renderInfoItem(cfg, item)
			if err != nil {
				return "", false, fmt.Errorf("section %q %s (%s): %w", sectionID, currentPath, item.Type, err)
			}
			survivors = append(survivors, itemOut)
			if !item.IsDecorative() {
				contentCount++
			}
		}
	}

	// If no content and hide_on_empty, return empty
	if contentCount == 0 && hideOnEmpty {
		return "", false, nil
	}

	// Build output
	var outSB strings.Builder
	if title != "" {
		var head string
		if asSection {
			head = renderSectionTitle(title)
		} else {
			head = styleAccent.Bold(true).Render(title)
		}
		outSB.WriteString(head)
		outSB.WriteByte('\n')
	}

	// Write each survivor with trailing newline (even for empty separators)
	for _, s := range survivors {
		outSB.WriteString(s)
		outSB.WriteByte('\n')
	}

	// Enforce rendered ⇔ out != "" biconditional.
	// The hideOnEmpty short-circuit above already handled contentCount==0 &&
	// hideOnEmpty. Here we only catch the title-less / no-survivors /
	// hide_on_empty:false corner where nothing was actually emitted.
	// Decorative survivors (e.g. separators) produce "\n" via the per-item
	// newline write, so result != "" and rendered=true for them — intentional.
	result := outSB.String()
	if result == "" {
		return "", false, nil
	}

	return result, true, nil
}

// RenderSectionTitle renders a section header line using Lipgloss styling.
// Empty text renders a closing separator line.
func RenderSectionTitle(text string) string {
	return renderSectionTitle(text)
}

// RenderBrandedSectionTitle renders a section header prefixed with the Devbox
// logomark. Used by the status command's section titles only — other callers
// of RenderSectionTitle (inspect, pipeline phase headers) must NOT carry the
// logomark per the exclusion list in logo.go.
func RenderBrandedSectionTitle(text string) string {
	if text == "" {
		return renderSectionTitle(text)
	}
	return renderSectionTitle(LogoMarkPlain() + " " + text)
}

// RenderSubheader renders a bold yellow in-section subheader.
// Used for grouping sections within a larger block (e.g. Steps, Params).
func RenderSubheader(text string) string {
	return styleAccent.Bold(true).Render(text)
}

// RenderDefinition renders a styled "key — value" definition line, word-wrapping
// the value to TermWidth(). For callers that render into a fixed-width context
// (e.g. an inspect viewport narrower than the terminal), use
// [RenderDefinitionAt] with the explicit width instead — otherwise values are
// wrapped to the terminal and silently truncated when the viewport renders.
func RenderDefinition(name, value string, indent int, icon string) string {
	return renderDefinition(name, value, indent, icon, 0)
}

// RenderDefinitionAt is [RenderDefinition] with an explicit wrap width.
// maxWidth == 0 falls back to TermWidth(); pass the viewport's content width
// when rendering for a sub-region.
func RenderDefinitionAt(name, value string, indent int, icon string, maxWidth int) string {
	return renderDefinition(name, value, indent, icon, maxWidth)
}

// renderSectionTitle is the internal implementation of RenderSectionTitle.
func renderSectionTitle(text string) string {
	width := min(TermWidth(), 100)

	if text == "" {
		return styleMuted.Render(strings.Repeat("─", width))
	}

	// Build: ── Title ──────...
	label := styleAccent.Bold(true).Render(" " + text + " ")
	// Strip ANSI for width calculation.
	labelVisible := " " + text + " "
	labelWidth := utf8.RuneCountInString(labelVisible)

	remaining := max(width-4-labelWidth, 0)
	leftDash := styleMuted.Render("──")
	rightDash := styleMuted.Render(strings.Repeat("─", remaining+2))

	return leftDash + label + rightDash
}

// renderInfoItem renders a single info item to a string (without trailing newline).
// Subgroups are handled in renderBlock before reaching here.
func renderInfoItem(cfg *config.DevboxConfig, item config.InfoItem) (string, error) {
	switch item.Type {
	case "definition":
		value, err := tpl.Render(item.Value, cfg)
		if err != nil {
			return "", err
		}
		indent := defaultIndent
		if item.Indent.IsSet() {
			indent = item.Indent.Value()
		}
		return renderDefinition(item.Name, value, indent, item.Icon, 0), nil

	case "warning":
		text, err := tpl.Render(item.Text, cfg)
		if err != nil {
			return "", err
		}
		return styleWarning.Render(text), nil

	case "info":
		text, err := tpl.Render(item.Text, cfg)
		if err != nil {
			return "", err
		}
		prefix := strings.Repeat(" ", item.Indent.Value())
		return styleAccent.Render(prefix + text), nil

	case "separator":
		return "", nil

	case "subgroup":
		return "", fmt.Errorf("subgroup must be handled in renderBlock, not renderInfoItem")

	default:
		return "", fmt.Errorf("unknown item type %q", item.Type)
	}
}

// renderDefinition formats a definition line, wrapping long values at word
// boundaries to fit the terminal width. Continuation lines are aligned under
// the start of the value on the first line.
//
//	<indent>[icon ]<key> — first line of value
//	                       continuation aligned here
func renderDefinition(name, value string, indent int, icon string, maxWidth int) string {
	iconWidth := 0
	iconPrefix := ""
	if icon != "" {
		iconWidth = utf8.RuneCountInString(icon) + 1
		iconPrefix = icon + " "
	}

	sep := defSep
	// Visible overhead: indent + icon + name + " " + sep + " "
	overhead := indent + iconWidth + utf8.RuneCountInString(name) + 1 + utf8.RuneCountInString(sep) + 1
	if maxWidth <= 0 {
		maxWidth = TermWidth()
	}
	maxValue := max(maxWidth-overhead, 20)

	lines := wordWrap(value, maxValue)

	prefix := strings.Repeat(" ", indent)
	contPrefix := strings.Repeat(" ", overhead)

	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteString(iconPrefix)
	sb.WriteString(styleAccent.Bold(true).Render(name))
	sb.WriteString(" ")
	sb.WriteString(styleMuted.Render(sep))
	sb.WriteString(" ")
	sb.WriteString(styleText.Render(lines[0]))
	for _, l := range lines[1:] {
		sb.WriteByte('\n')
		sb.WriteString(contPrefix)
		sb.WriteString(styleText.Render(l))
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
