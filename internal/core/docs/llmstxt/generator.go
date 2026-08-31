// Package llmstxt generates an llms.txt document for AI agent orientation.
// See https://llmstxt.org/ for the format specification.
package llmstxt

import (
	"strings"

	coredocs "github.com/semsemyonoff/dwe/internal/core/docs"
)

// CommandSummary is a single user command entry for the Commands section.
type CommandSummary struct {
	ID          string
	Description string
}

// ServiceSummary is a single service entry for the Project section.
type ServiceSummary struct {
	Name  string
	Type  string
	Title string
}

// InfoSummary holds extracted project-info data for the Project section.
type InfoSummary struct {
	URLs  []string
	Hosts []string
}

// BuiltinSummary is one entry of the `type: builtin` registry (step bodies and
// `check:` positions). Kind is the display label ("action", "predicate",
// "internal") — the docs subsystem must not import the execution layer, so the
// caller flattens builtin.Kind to a string before passing it in.
type BuiltinSummary struct {
	Name    string
	Kind    string
	Summary string
}

// ConditionSummary is one predicate verb of the `when:` condition registry —
// a different, smaller set than BuiltinSummary despite the shared `builtin`
// spelling. Args is the verb's argument shape, e.g. "<path>".
type ConditionSummary struct {
	Name    string
	Args    string
	Summary string
}

// Opts configures document generation.
type Opts struct {
	ProjectRoot   string
	ProjectName   string
	IncludeIntern bool
	DocTopics     []coredocs.TopicEntry
	Commands      []CommandSummary
	Services      []ServiceSummary
	InfoSnapshot  *InfoSummary
	// Builtins and Conditions are collected in the CLI layer and passed in;
	// core/docs must not import internal/core/execution.
	Builtins   []BuiltinSummary
	Conditions []ConditionSummary
	// ReservedEnvNames are the env variable names the renderer always emits
	// itself (config.ReservedExportNames), likewise passed in from the CLI.
	ReservedEnvNames []string
}

// sectionItem is rendered as `- [Label](URL) — Description`.
type sectionItem struct {
	Label       string
	URL         string
	Description string
}

// Generate produces an llms.txt document from opts.
// It never returns an error in the current implementation; the signature is
// retained for future extension (e.g. template rendering).
func Generate(opts Opts) (string, error) {
	var b strings.Builder

	if opts.ProjectRoot == "" {
		// Project-agnostic output.
		writeTitle(&b, "DWE")
		writeBlockquote(&b, "DWE (Dev Workspace Engine) is a CLI for managing Docker-based local development environments. "+
			"Use `dwe --help` for a full command list.")
		writeCommandsSection(&b, opts.Commands)
		writeBriefingSections(&b, opts)
		writeDocumentationSection(&b, opts.DocTopics, opts.IncludeIntern)
		writeQuickStart(&b)
		return b.String(), nil
	}

	// Project-aware output.
	name := opts.ProjectName
	if name == "" {
		name = "DWE project"
	}
	writeTitle(&b, name)
	writeBlockquote(&b, "DWE (Dev Workspace Engine) project environment. "+
		"Use `dwe status` to see running services, `dwe validate` to check project health, "+
		"and `dwe docs llms-txt` to regenerate this index.")

	writeProjectSection(&b, opts)
	writeCommandsSection(&b, opts.Commands)
	writeBriefingSections(&b, opts)
	writeDocumentationSection(&b, opts.DocTopics, opts.IncludeIntern)
	writeQuickStart(&b)

	return b.String(), nil
}

// writeTitle writes the H1 document title.
func writeTitle(b *strings.Builder, title string) {
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n")
}

// writeBlockquote writes the optional blockquote summary.
func writeBlockquote(b *strings.Builder, text string) {
	if text == "" {
		return
	}
	b.WriteString("> ")
	b.WriteString(text)
	b.WriteString("\n\n")
}

// writeSection writes an H2 heading followed by a list of items.
func writeSection(b *strings.Builder, heading string, items []sectionItem) {
	b.WriteString("## ")
	b.WriteString(heading)
	b.WriteString("\n\n")
	for _, item := range items {
		b.WriteString("- ")
		if item.URL != "" {
			b.WriteString("[")
			b.WriteString(item.Label)
			b.WriteString("](")
			b.WriteString(item.URL)
			b.WriteString(")")
		} else {
			b.WriteString(item.Label)
		}
		if item.Description != "" {
			b.WriteString(" — ")
			b.WriteString(item.Description)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

// writeSectionLines writes an H2 heading with plain text lines (no link format).
func writeSectionLines(b *strings.Builder, heading string, lines []string) {
	writeHeading(b, heading)
	writeLines(b, lines)
}

func writeProjectSection(b *strings.Builder, opts Opts) {
	var lines []string

	for _, svc := range opts.Services {
		entry := "Service: " + svc.Name
		if svc.Type != "" {
			entry += " (type: " + svc.Type + ")"
		}
		if svc.Title != "" {
			entry += " — " + svc.Title
		}
		lines = append(lines, entry)
	}

	if opts.InfoSnapshot != nil {
		for _, u := range opts.InfoSnapshot.URLs {
			lines = append(lines, "URL: "+u)
		}
		for _, h := range opts.InfoSnapshot.Hosts {
			lines = append(lines, "Host: "+h)
		}
	}

	if len(lines) == 0 {
		return
	}
	writeSectionLines(b, "Project", lines)
}

func writeCommandsSection(b *strings.Builder, cmds []CommandSummary) {
	items := make([]sectionItem, 0, len(cmds))
	for _, c := range cmds {
		items = append(items, sectionItem{
			Label:       c.ID,
			Description: c.Description,
		})
	}
	if len(items) == 0 {
		return
	}
	writeSection(b, "Commands", items)
}

func writeDocumentationSection(b *strings.Builder, topics []coredocs.TopicEntry, includeInternals bool) {
	items := make([]sectionItem, 0, len(topics))
	for _, t := range topics {
		if strings.HasPrefix(t.Path, "internals/") && !includeInternals {
			continue
		}
		items = append(items, sectionItem{
			Label: t.DisplayName,
			URL:   "dwe-docs://" + t.Path,
		})
	}
	if len(items) == 0 {
		return
	}
	writeSection(b, "Documentation", items)
}

// writeBriefingSections writes the static knowledge sections — the parts an
// agent otherwise reverse-engineers from other repositories. They are identical
// inside and outside a project, since they describe dwe itself, not the project.
func writeBriefingSections(b *strings.Builder, opts Opts) {
	writeBuiltinsSection(b, opts.Builtins, opts.Conditions)
	writeTemplateSyntaxSection(b)
	writeDiagnosticsSection(b)
	writeReservedEnvSection(b, opts.ReservedEnvNames)
}

// writeBuiltinsSection lists both `type: builtin` registries. Naming them as
// two disjoint sets is the point of the section: a `when:` verb such as
// `dir-not-empty` is rejected in a `check:`, and vice versa.
func writeBuiltinsSection(b *strings.Builder, builtins []BuiltinSummary, conds []ConditionSummary) {
	if len(builtins) == 0 && len(conds) == 0 {
		return
	}
	writeHeading(b, "Builtins")
	writeParagraph(b, "Two disjoint registries share the word \"builtin\". A name from one is NOT accepted by the other.")

	if len(builtins) > 0 {
		writeParagraph(b, "1. Step builtins — `type: builtin` with `cmd: <name>`, usable in a step body and in `check:`. "+
			"Kinds: `action` runs work; `predicate` is a boolean check (as a step body it asserts — false fails the step); "+
			"`internal` is engine-only and rejected in user-authored YAML.")
		for _, e := range builtins {
			b.WriteString("- `")
			b.WriteString(e.Name)
			b.WriteString("` — ")
			b.WriteString(e.Kind)
			if e.Summary != "" {
				b.WriteString(" — ")
				b.WriteString(e.Summary)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(conds) > 0 {
		writeParagraph(b, "2. Condition predicates — the `when:` registry only. A pipeline `when:` is always a "+
			"mapping (`when: {type: builtin, cmd: \"<verb> <args>\"}`); the scalar shorthand is rejected at load. "+
			"This set is the whole of it:")
		for _, c := range conds {
			b.WriteString("- `")
			b.WriteString(c.Name)
			if c.Args != "" {
				b.WriteString(" ")
				b.WriteString(c.Args)
			}
			b.WriteString("` — ")
			b.WriteString(c.Summary)
			b.WriteString("\n")
		}
		b.WriteString("\n")
		writeParagraph(b, "A `when:` needing anything else uses `when: {type: shell, cmd: …}`; "+
			"a `check:` needing a filesystem test uses `check: {type: builtin, cmd: shell, with: {cmd: …}}` "+
			"(the `shell` builtin's param is `cmd`, not `command`) or the `check: auto` inverse of a shell `when:`.")
	}
}

// writeTemplateSyntaxSection maps each authoring site to the syntax evaluated
// there. Condensed from docs/reference/templates.md — the full table lives there.
func writeTemplateSyntaxSection(b *strings.Builder) {
	writeHeading(b, "Template syntax by site")
	lines := []string{
		"| Site | Syntax | Notes |",
		"|---|---|---|",
		"| pipeline `cmd`, `with` leaves, `check`, `timeout`, shell `when` | `${...}` only | rendered ONCE at plan-resolution time; only `${vars.*}` / `${project.*}` / `${services.*}` / other config roots + `${host.*}` resolve — `${param.*}`, `${context.*}`, `${files.*}`, `${generated.*}`, `${args}` fail the resolve here |",
		"| pipeline `when: {type: template, expr}` | `{{ ... }}` | evaluated at plan time against the resolved config |",
		"| `workspace/commands/**` — `cmd`, `argv`, `workdir`, `env`, `messages.*`, `files.*` | `${...}` and `{{ ... }}` | full command context: `${param.*}`, `${context.*}`, `${files.*}`, `${args}`, `${host.*}` |",
		"| `info.yml` — `text`, `value`, `title`; custom columns | `{{ ... }}` and `${...}` | resolved project config |",
		"| `message` builtin — `text` | `{{ ... }}` | resolved project config (`{{ .Project.Name }}`) |",
		"| `workspace/templates/config/**` | `${...}` | lenient (absent → `\"\"`); the only site where `${generated.<name>}` resolves |",
		"| `workspace/templates/{ide,ai,git}/**` | `{{ ... }}` | strict render packs; `${...}` is NOT interpreted here |",
		"| `docker.yml` — `project_name` | `${...}` | separate resolver: any `Raw` dot-path, and an unresolved path is a hard error |",
		"| `params.*.default_from`, `context.*.from` | — | plain dot-paths, no template expressions |",
	}
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	writeParagraph(b, "An unknown `${...}` head (`${HOME}`, a typo) is left literal, never blanked. "+
		"Full reference: `dwe docs show reference/templates`.")
}

// writeDiagnosticsSection names the flags that make dwe output parseable and
// debuggable. Session evidence: none of them were used across 94 invocations.
func writeDiagnosticsSection(b *strings.Builder) {
	writeHeading(b, "Diagnostics and machine-readable output")
	lines := []string{
		"`dwe validate --quiet` — hide the ok/info rows; only warnings and errors remain",
		"`dwe validate --level error,warning` — filter by severity instead of grepping the table",
		"`dwe validate -o json` — structured diagnostics (this command emits diagnostics-as-data even at severity=error)",
		"`-v` / `--debug` — echo executed commands, skip decisions, timings and exit codes to **stderr**; stdout (including `-o json`) stays clean, so `dwe run --debug 2>debug.log` captures them",
		"`dwe docs show <topic> --toc` / `--anchors` — headings or anchor ids instead of the whole document; use these instead of piping through `head`/`sed`",
		"`dwe docs search <query> -o json` — search hits with `source`, `path`, `anchor`, `count`, `snippet`. Every word of the query must appear in the same section (or, failing that, the same document); `--literal` matches the whole query as one substring",
		"`-o json` (+ `--pretty`) — every read-only command. Two exceptions: `dwe docs show` and `dwe docs llms-txt` always emit markdown and ignore `-o json`; to write llms-txt to a file use its own `--out PATH`",
	}
	writeLines(b, lines)
}

// writeReservedEnvSection names the env variables the renderer injects itself.
// One session reverse-engineered them from another project's rendered .env.
func writeReservedEnvSection(b *strings.Builder, names []string) {
	if len(names) == 0 {
		return
	}
	writeHeading(b, "Reserved env names")
	writeParagraph(b, "`dwe render env` always emits these itself, before any `exports.env` rule: `"+
		strings.Join(names, "`, `")+"`. "+
		"They are available in `compose.yaml` without being declared, and an `exports.env` rule may not redeclare them.")
}

// writeHeading writes an H2 heading with the trailing blank line.
func writeHeading(b *strings.Builder, heading string) {
	b.WriteString("## ")
	b.WriteString(heading)
	b.WriteString("\n\n")
}

// writeParagraph writes a text block followed by a blank line.
func writeParagraph(b *strings.Builder, text string) {
	b.WriteString(text)
	b.WriteString("\n\n")
}

// writeLines writes a bullet list followed by a blank line.
func writeLines(b *strings.Builder, lines []string) {
	for _, line := range lines {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func writeQuickStart(b *strings.Builder) {
	lines := []string{
		"`dwe status` — see what's running",
		"`dwe validate` — check project health",
		"`dwe info` — project overview",
		"`dwe docs llms-txt` — regenerate this index",
		"`dwe --help` — full command list",
	}
	writeSectionLines(b, "Quick start", lines)
}
