package scaffold

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"text/template"
)

// templatesFS holds the project-template tree, mirroring the generated output
// layout. Dotfiles are stored with a `dot-` prefix on the relevant path segment
// because go:embed silently skips names beginning with `.` or `_`; mapEmbedPath
// reverses the rename. We deliberately do NOT use the `all:` embed prefix — it
// would also pull in `_`-prefixed files and editor junk.
//
//go:embed templates
var templatesFS embed.FS

// templateSuffix marks files that are rendered through text/template; the suffix
// is stripped from the output path.
const templateSuffix = ".tmpl"

// dotPrefix is the stand-in for a leading "." on an embedded path segment.
const dotPrefix = "dot-"

// renderPlan walks the embedded template FS and returns the full file plan:
// project-relative output path → rendered (or verbatim) bytes.
func renderPlan(opts Options) (map[string][]byte, error) {
	sub, err := fs.Sub(templatesFS, "templates")
	if err != nil {
		return nil, fmt.Errorf("scaffold: open templates: %w", err)
	}
	return renderPlanFS(sub, opts)
}

// renderPlanFS is the FS-injectable core of renderPlan, so the walker/renderer
// can be exercised against an in-memory FS in tests without depending on the
// embedded content.
func renderPlanFS(fsys fs.FS, opts Options) (map[string][]byte, error) {
	plan := make(map[string][]byte)
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("scaffold: read %s: %w", path, err)
		}

		outPath := mapEmbedPath(path)
		if strings.HasSuffix(path, templateSuffix) {
			rendered, err := renderTemplate(path, data, opts)
			if err != nil {
				return err
			}
			data = rendered
		}
		plan[outPath] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return plan, nil
}

// mapEmbedPath converts an embedded template path to its output path: each
// `dot-`-prefixed segment becomes a `.`-prefixed segment, and a trailing
// `.tmpl` suffix is removed.
func mapEmbedPath(path string) string {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		if after, ok := strings.CutPrefix(seg, dotPrefix); ok {
			segments[i] = "." + after
		}
	}
	out := strings.Join(segments, "/")
	return strings.TrimSuffix(out, templateSuffix)
}

// yamlEsc escapes a string for safe interpolation inside a YAML double-quoted
// scalar. It handles backslashes, double-quotes, C0 controls (U+0000–U+001F),
// DEL (U+007F), and C1 controls (U+0080–U+009F) that yaml.v3 rejects as
// illegal control characters.
func yamlEsc(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\x00':
			b.WriteString(`\0`)
		default:
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// renderTemplate parses and executes a single template with custom [[ ]]
// delimiters, so literal Go-template syntax ({{ .Project.Name }}) inside inert
// reference files is never interpreted.
func renderTemplate(name string, data []byte, opts Options) ([]byte, error) {
	tmpl, err := template.New(name).
		Funcs(template.FuncMap{"yamlEsc": yamlEsc}).
		Delims("[[", "]]").
		Option("missingkey=error").
		Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("scaffold: parse template %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, opts); err != nil {
		return nil, fmt.Errorf("scaffold: render template %s: %w", name, err)
	}
	return buf.Bytes(), nil
}
