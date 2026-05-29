// Package llmstxt generates an llms.txt document for AI agent orientation.
// See https://llmstxt.org/ for the format specification.
package llmstxt

import (
	"strings"

	coredocs "devbox-cli/internal/core/docs"
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

// Opts configures document generation.
type Opts struct {
	ProjectRoot   string
	ProjectName   string
	IncludeIntern bool
	DocTopics     []coredocs.TopicEntry
	Commands      []CommandSummary
	Services      []ServiceSummary
	InfoSnapshot  *InfoSummary
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
		writeTitle(&b, "devbox")
		writeBlockquote(&b, "devbox is a developer tool for local development environments running on Docker. "+
			"Use `devbox --help` for a full command list.")
		writeCommandsSection(&b, opts.Commands)
		writeDocumentationSection(&b, opts.DocTopics, opts.IncludeIntern)
		writeQuickStart(&b)
		return b.String(), nil
	}

	// Project-aware output.
	name := opts.ProjectName
	if name == "" {
		name = "devbox project"
	}
	writeTitle(&b, name)
	writeBlockquote(&b, "devbox project environment. "+
		"Use `devbox status` to see running services, `devbox validate` to check project health, "+
		"and `devbox docs llms-txt` to regenerate this index.")

	writeProjectSection(&b, opts)
	writeCommandsSection(&b, opts.Commands)
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
	b.WriteString("## ")
	b.WriteString(heading)
	b.WriteString("\n\n")
	for _, line := range lines {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
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
			URL:   "devbox-docs://" + t.Path,
		})
	}
	if len(items) == 0 {
		return
	}
	writeSection(b, "Documentation", items)
}

func writeQuickStart(b *strings.Builder) {
	lines := []string{
		"`devbox status` — see what's running",
		"`devbox validate` — check project health",
		"`devbox info` — project overview",
		"`devbox docs llms-txt` — regenerate this index",
		"`devbox --help` — full command list",
	}
	writeSectionLines(b, "Quick start", lines)
}
