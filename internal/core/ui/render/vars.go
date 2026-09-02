package render

import (
	"fmt"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/varsusage"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"

	"gopkg.in/yaml.v3"
)

// VarListItem is one row in the `dwe vars list` table: a leaf dot-path, its
// effective value, and the layer badge naming where that value originates
// ("local" / "default" / "" when unresolved). Encrypted marks a leaf whose
// origin layer holds an ENC[age:…] marker that could not be decrypted — the
// value renders as EncryptedPlaceholder and the badge carries a suffix.
type VarListItem struct {
	Path      string
	Value     any
	Layer     string
	Encrypted bool
}

// VarInspect carries the per-layer resolution and static usages for a single
// var, as produced by config.ResolveLayeredPath + varsusage.ScanUsages. The
// renderer is pure formatting; callers supply already-resolved data.
type VarInspect struct {
	Path      string
	Default   any
	DefaultOK bool
	Local     any
	LocalOK   bool
	Current   any
	CurrentOK bool
	// Origin is the (display-ready) file path supplying the current value.
	Origin string
	// Secret is the one-line encrypted-secret note ("decrypted via keyfile
	// (…)" / "unresolved — no identity for age1…"), empty for a var that is
	// not an encrypted secret at its origin layer.
	Secret string
	Usages []varsusage.Usage
}

// usageCaveat is appended to inspect output: the static scan cannot follow
// dynamically-built dot-paths or Go-template field access (.Vars.x).
const usageCaveat = "Note: dynamically-built var paths are not tracked."

// EncryptedPlaceholder replaces an undecrypted ENC[age:…] marker everywhere
// the `dwe vars` surfaces would otherwise print it. The ciphertext itself is
// committed and harmless, but showing it as a value is noise the user cannot
// act on — the placeholder plus `dwe secrets status` is the actionable form.
const EncryptedPlaceholder = "<encrypted>"

// MaskSecretValue replaces every undecrypted marker inside a var value with
// EncryptedPlaceholder, recursing through mappings and sequences so a
// namespace subtree is masked leaf by leaf. It reports whether anything was
// masked. Composites are copied only when they actually contain a marker, so
// the common (no secrets) path returns the original value untouched.
func MaskSecretValue(value any) (any, bool) {
	switch t := value.(type) {
	case string:
		if secrets.IsMarker(t) {
			return EncryptedPlaceholder, true
		}
		return t, false
	case map[string]any:
		out := make(map[string]any, len(t))
		masked := false
		for k, v := range t {
			mv, m := MaskSecretValue(v)
			out[k] = mv
			masked = masked || m
		}
		if !masked {
			return t, false
		}
		return out, true
	case []any:
		out := make([]any, len(t))
		masked := false
		for i, v := range t {
			mv, m := MaskSecretValue(v)
			out[i] = mv
			masked = masked || m
		}
		if !masked {
			return t, false
		}
		return out, true
	default:
		return value, false
	}
}

// VarLayerBadge is the plain-text layer badge for a var row: the origin layer
// name with an " (encrypted)" suffix when that layer supplies an undecrypted
// secret. Shared by the list renderer and the TUI browser's type badge so both
// spell the state the same way.
func VarLayerBadge(layer string, encrypted bool) string {
	if !encrypted {
		return layer
	}
	if layer == "" {
		return "encrypted"
	}
	return layer + " (encrypted)"
}

// DisplayVarPath strips the leading `vars.` namespace for DISPLAY only — under
// `dwe vars` every path is a var, so the prefix is noise on screen. Storage,
// JSON, completion, and source-line matching keep the canonical full path; this
// is purely cosmetic for the text/TUI surfaces.
func DisplayVarPath(path string) string {
	return strings.TrimPrefix(path, "vars.")
}

// VarValue formats a single var's effective value for `dwe vars get`. A
// scalar (string/number/bool/null) renders as a bare line suitable for piping;
// a namespace subtree (map or sequence) renders as indented YAML. Returns an
// error only when a composite value fails to marshal. This is the UNSTYLED form
// — used for JSON reuse, the set-form description, and as the substrate for the
// styled variants. Color stripping for pipes is handled by the renderer.
func VarValue(value any) (string, error) {
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

// VarValueStyled is the colored form of VarValue for `dwe vars get`'s human
// output: a scalar — the sole thing the user asked for — is accented so it
// stands out; a namespace subtree (a partial-path match) has its YAML keys
// accented and values themed so it matches the rest of the vars surface. On a
// pipe every style collapses to a no-op, so the text stays byte-identical to the
// unstyled VarValue.
func VarValueStyled(value any) (string, error) {
	raw, err := VarValue(value)
	if err != nil {
		return "", err
	}
	switch value.(type) {
	case map[string]any, []any:
		return styleYAMLBlock(raw), nil
	default:
		return styles.StyleInfo(raw), nil
	}
}

// styleYAMLBlock colors a marshalled YAML subtree line by line: mapping keys in
// the accent color, the `:` / `-` punctuation muted, and scalar values themed.
// When the palette is in no-color mode every Render is identity, so the output
// is byte-identical to the input (keeps `dwe vars get vars.db | …` pipe-clean).
func styleYAMLBlock(raw string) string {
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		lines[i] = styleYAMLLine(line)
	}
	return strings.Join(lines, "\n")
}

func styleYAMLLine(line string) string {
	indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
	rest := line[len(indent):]
	if rest == "" {
		return line
	}

	// Sequence item marker (`- `), possibly followed by a scalar or a key:value.
	dash := ""
	switch {
	case rest == "-":
		return indent + styles.MutedStyle().Render("-")
	case strings.HasPrefix(rest, "- "):
		dash = styles.MutedStyle().Render("- ")
		rest = rest[2:]
	}

	// `key: value` — split on the first ": " (YAML's key/value boundary; values
	// that themselves contain a colon keep theirs because Cut takes the first).
	if key, val, ok := strings.Cut(rest, ": "); ok {
		return indent + dash + styles.AccentStyle().Render(key) +
			styles.MutedStyle().Render(": ") + styles.TextStyle().Render(val)
	}
	// `key:` — a parent mapping key with no inline value.
	if k, ok := strings.CutSuffix(rest, ":"); ok {
		return indent + dash + styles.AccentStyle().Render(k) + styles.MutedStyle().Render(":")
	}
	// Bare scalar (e.g. a sequence element).
	return indent + dash + styles.TextStyle().Render(rest)
}

// VarSetConfirmation is the styled one-line confirmation printed after `dwe vars
// set` writes: a green check, the var path as an accented key, and the new
// value. Mirrors the palette used by list/inspect so the command family reads
// consistently.
func VarSetConfirmation(path, value string) string {
	return styles.SuccessStyle().Render("✓") + " " +
		styles.MutedStyle().Render("set") + " " +
		styles.StyleKey(DisplayVarPath(path)) + " " +
		styles.MutedStyle().Render("=") + " " +
		styles.TextStyle().Render(value)
}

// VarsList formats a flat, styled list of var leaves. When namespace is
// non-empty, only leaves at or under that namespace (exact path or a real dot
// boundary below it) are shown. Returns an empty string when nothing matches.
func VarsList(items []VarListItem, namespace string) string {
	filtered := make([]VarListItem, 0, len(items))
	for _, it := range items {
		if namespaceMatches(it.Path, namespace) {
			filtered = append(filtered, it)
		}
	}
	if len(filtered) == 0 {
		return ""
	}

	// Align the value column under the widest (display) path.
	pathWidth := 0
	for _, it := range filtered {
		if n := len(DisplayVarPath(it.Path)); n > pathWidth {
			pathWidth = n
		}
	}

	var sb strings.Builder
	for _, it := range filtered {
		disp := DisplayVarPath(it.Path)
		pad := strings.Repeat(" ", pathWidth-len(disp))
		sb.WriteString(styles.StyleKey(disp))
		sb.WriteString(pad)
		sb.WriteString("  ")
		// Masking is value-driven here as well as flag-driven: the renderer is
		// the last stop before the terminal, so a caller that forgets the flag
		// still cannot print ciphertext.
		masked, _ := MaskSecretValue(it.Value)
		sb.WriteString(styles.TextStyle().Render(inlineValue(masked)))
		if badge := VarLayerBadge(it.Layer, it.Encrypted); badge != "" {
			sb.WriteString("  ")
			sb.WriteString(styleLayerBadge(badge))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// styleLayerBadge colors a list row's layer badge: a `local` override is the
// notable case (the dev has diverged from the team default), so it gets the
// accent color; `default` stays muted to recede.
func styleLayerBadge(layer string) string {
	label := "[" + layer + "]"
	if strings.HasPrefix(layer, "local") {
		return styles.AccentStyle().Render(label)
	}
	return styles.MutedStyle().Render(label)
}

// VarInspectView formats the per-layer block, origin, and grouped usages for
// `dwe vars inspect`. width bounds usage-line wrapping (<=0 → terminal width).
func VarInspectView(in VarInspect, width int) string {
	if width <= 0 {
		width = styles.TermWidth()
	}

	var sb strings.Builder
	sb.WriteString(renderSectionTitleAt(DisplayVarPath(in.Path), width))
	sb.WriteByte('\n')

	sb.WriteString(layerLine("Default", in.Default, in.DefaultOK))
	sb.WriteString(layerLine("Local", in.Local, in.LocalOK))
	sb.WriteString(layerLine("Current", in.Current, in.CurrentOK))

	if in.Origin != "" {
		sb.WriteString("  ")
		sb.WriteString(styles.MutedStyle().Render("Origin"))
		sb.WriteString(": ")
		sb.WriteString(styles.TextStyle().Render(in.Origin))
		sb.WriteByte('\n')
	}
	if in.Secret != "" {
		sb.WriteString("  ")
		sb.WriteString(styles.MutedStyle().Render("Secret"))
		sb.WriteString(": ")
		sb.WriteString(styles.TextStyle().Render(in.Secret))
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
		masked, _ := MaskSecretValue(value)
		rhs = styles.TextStyle().Render(inlineValue(masked))
	}
	return "  " + styles.AccentStyle().Render(label) + pad + styles.MutedStyle().Render(styles.DefSep) + " " + rhs + "\n"
}

// accentMatch renders a source line bright (TextStyle) so the code stands out,
// with occurrences of the queried path accented (StyleKey) on top so the
// reference itself pops within the line.
func accentMatch(line, path string) string {
	if before, after, found := strings.Cut(line, path); path != "" && found {
		return styles.TextStyle().Render(before) +
			styles.StyleKey(path) +
			styles.TextStyle().Render(after)
	}
	return styles.TextStyle().Render(line)
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
