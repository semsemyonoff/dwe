package render

import (
	"fmt"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/varsusage"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"

	"gopkg.in/yaml.v3"
)

// VarListItem is one row in the `dwe vars list` table: a leaf dot-path, its
// effective value, and the layer badge naming where that value originates
// ("local" / "default" / "" when unresolved).
type VarListItem struct {
	Path  string
	Value any
	Layer string
}

// VarInspect carries the per-layer resolution and static usages for a single
// var, as produced by config.ResolveLayeredPath + varsusage.ScanUsages. The
// renderer is pure formatting; callers supply already-resolved data.
type VarInspect struct {
	Path        string
	Author      any
	AuthorOK    bool
	Local       any
	LocalOK     bool
	Effective   any
	EffectiveOK bool
	// Origin is the (display-ready) file path supplying the effective value.
	Origin string
	Usages []varsusage.Usage
}

// usageCaveat is appended to inspect output: the static scan cannot follow
// dynamically-built dot-paths or Go-template field access (.Vars.x).
const usageCaveat = "Note: dynamically-built var paths are not tracked."

// RenderVarValue formats a single var's effective value for `dwe vars get`. A
// scalar (string/number/bool/null) renders as a bare line suitable for piping;
// a namespace subtree (map or sequence) renders as indented YAML. Returns an
// error only when a composite value fails to marshal.
func RenderVarValue(value any) (string, error) {
	switch value.(type) {
	case map[string]any, []any:
		out, err := yaml.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("marshal var value: %w", err)
		}
		return strings.TrimRight(string(out), "\n"), nil
	default:
		return scalarString(value), nil
	}
}

// RenderVarsList formats a flat, styled list of var leaves. When namespace is
// non-empty, only leaves at or under that namespace (exact path or a real dot
// boundary below it) are shown. Returns an empty string when nothing matches.
func RenderVarsList(items []VarListItem, namespace string) string {
	filtered := make([]VarListItem, 0, len(items))
	for _, it := range items {
		if namespaceMatches(it.Path, namespace) {
			filtered = append(filtered, it)
		}
	}
	if len(filtered) == 0 {
		return ""
	}

	// Align the value column under the widest path.
	pathWidth := 0
	for _, it := range filtered {
		if n := len(it.Path); n > pathWidth {
			pathWidth = n
		}
	}

	var sb strings.Builder
	for _, it := range filtered {
		pad := strings.Repeat(" ", pathWidth-len(it.Path))
		sb.WriteString(styles.StyleKey(it.Path))
		sb.WriteString(pad)
		sb.WriteString("  ")
		sb.WriteString(styles.TextStyle().Render(inlineValue(it.Value)))
		if it.Layer != "" {
			sb.WriteString("  ")
			sb.WriteString(styles.MutedStyle().Render("[" + it.Layer + "]"))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// RenderVarInspect formats the per-layer block, origin, and grouped usages for
// `dwe vars inspect`. width bounds usage-line wrapping (<=0 → terminal width).
func RenderVarInspect(in VarInspect, width int) string {
	if width <= 0 {
		width = styles.TermWidth()
	}

	var sb strings.Builder
	sb.WriteString(renderSectionTitle(in.Path))
	sb.WriteByte('\n')

	sb.WriteString(layerLine("Author", in.Author, in.AuthorOK))
	sb.WriteString(layerLine("Local", in.Local, in.LocalOK))
	sb.WriteString(layerLine("Effective", in.Effective, in.EffectiveOK))

	if in.Origin != "" {
		sb.WriteString("  ")
		sb.WriteString(styles.MutedStyle().Render("Origin"))
		sb.WriteString(": ")
		sb.WriteString(styles.TextStyle().Render(in.Origin))
		sb.WriteByte('\n')
	}

	sb.WriteByte('\n')
	if len(in.Usages) == 0 {
		sb.WriteString(styles.MutedStyle().Render("Usages: none found"))
		sb.WriteByte('\n')
	} else {
		sb.WriteString(styles.StyleSubheader(fmt.Sprintf("Usages (%d):", len(in.Usages))))
		sb.WriteByte('\n')
		for _, u := range in.Usages {
			loc := fmt.Sprintf("%s:%d", u.File, u.Line)
			sb.WriteString("  ")
			sb.WriteString(styles.StyleKey(loc))
			sb.WriteString("  ")
			sb.WriteString(styles.MutedStyle().Render(u.Kind))
			sb.WriteByte('\n')
			for _, line := range wordWrap(u.Text, max(width-4, 20)) {
				sb.WriteString("    ")
				sb.WriteString(accentMatch(line, in.Path))
				sb.WriteByte('\n')
			}
		}
	}

	sb.WriteByte('\n')
	sb.WriteString(styles.MutedStyle().Render(usageCaveat))
	sb.WriteByte('\n')
	return sb.String()
}

// layerLine renders one "  Label    — value" row, padded to a fixed label
// width; an unresolved layer shows a muted "(not set)".
func layerLine(label string, value any, ok bool) string {
	const labelWidth = 10
	pad := strings.Repeat(" ", max(labelWidth-len(label), 0))
	var rhs string
	if !ok {
		rhs = styles.MutedStyle().Render("(not set)")
	} else {
		rhs = styles.TextStyle().Render(inlineValue(value))
	}
	return "  " + styles.AccentStyle().Render(label) + pad + styles.MutedStyle().Render(styles.DefSep) + " " + rhs + "\n"
}

// accentMatch bolds occurrences of the queried path (and the bare vars head)
// inside a source line so the reference stands out.
func accentMatch(line, path string) string {
	if path != "" && strings.Contains(line, path) {
		return styles.MutedStyle().Render(line[:strings.Index(line, path)]) +
			styles.StyleKey(path) +
			styles.MutedStyle().Render(line[strings.Index(line, path)+len(path):])
	}
	return styles.MutedStyle().Render(line)
}

// namespaceMatches reports whether path is at or under namespace. An empty
// namespace matches everything; otherwise the match is exact or at a real dot
// boundary (so "vars.db" matches "vars.db" and "vars.db.host" but not
// "vars.dbx").
func namespaceMatches(path, namespace string) bool {
	if namespace == "" {
		return true
	}
	return path == namespace || strings.HasPrefix(path, namespace+".")
}

// inlineValue renders a value on a single line for list/layer rows: scalars
// verbatim, composites as compact flow YAML.
func inlineValue(value any) string {
	switch value.(type) {
	case map[string]any, []any:
		out, err := yaml.Marshal(value)
		if err != nil {
			return fmt.Sprintf("%v", value)
		}
		return strings.Join(strings.Fields(string(out)), " ")
	default:
		return scalarString(value)
	}
}

// scalarString renders a scalar value for display: null for nil, the empty
// string shown as "" so an empty value is visible, everything else via %v.
func scalarString(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		if v == "" {
			return `""`
		}
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}
