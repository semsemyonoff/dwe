package command

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"devbox-cli/internal/usercommands"

	cobradoc "github.com/spf13/cobra/doc"

	"github.com/spf13/cobra"
)

type docsFlags struct {
	output         string
	format         string
	scope          string
	includeHidden  bool
	includePrivate bool
}

func newDocsCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Generate documentation for devbox commands",
		Long: `Generate reference documentation for the devbox CLI and declarative command registry.

Documentation is written to the output directory (default: docs/reference).
Use --scope to limit output to CLI commands, registry commands, or both.`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newDocsGenerateCmd(flags))
	return cmd
}

func newDocsGenerateCmd(flags *rootFlags) *cobra.Command {
	df := &docsFlags{}

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate reference documentation",
		Long: `Generate reference documentation for the devbox CLI command tree and/or the
declarative command registry (devbox/commands/).

Supported formats: markdown, yaml, man, all
Supported scopes:  all, cli, commands`,
		Example: `  devbox docs generate
  devbox docs generate --format markdown --scope cli --output docs/reference
  devbox docs generate --format all --scope all --include-private`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDocsGenerate(cmd, flags, df)
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVarP(&df.output, "output", "o", "docs/reference", "Output directory for generated docs")
	cmd.Flags().StringVar(&df.format, "format", "markdown", "Output format: markdown, yaml, man, all")
	cmd.Flags().StringVar(&df.scope, "scope", "all", "Scope: all, cli, commands")
	cmd.Flags().BoolVar(&df.includeHidden, "include-hidden", false, "Include hidden CLI commands")
	cmd.Flags().BoolVar(&df.includePrivate, "include-private", false, "Include private registry commands")

	return cmd
}

func runDocsGenerate(cmd *cobra.Command, rflags *rootFlags, df *docsFlags) error {
	if err := validateDocsFlags(df); err != nil {
		return err
	}

	// Resolve output dir relative to project root.
	projectRoot := rflags.ProjectRoot()
	if projectRoot == "" {
		// No project found — only --scope cli is allowed without a project.
		requestedScopes := resolveScopes(df.scope)
		if requestedScopes["commands"] {
			return fmt.Errorf("commands scope requires a devbox project; use --scope cli to generate CLI reference docs without a project")
		}
		var cwdErr error
		projectRoot, cwdErr = os.Getwd()
		if cwdErr != nil {
			return fmt.Errorf("getwd: %w", cwdErr)
		}
	}
	outDir := df.output
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(projectRoot, outDir)
	}

	formats := resolveFormats(df.format)
	scopes := resolveScopes(df.scope)

	if scopes["cli"] {
		cliDir := filepath.Join(outDir, "cli")
		if err := os.MkdirAll(cliDir, 0o755); err != nil {
			return fmt.Errorf("creating cli output dir: %w", err)
		}
		root := cmd.Root()
		// cobra/doc already skips Hidden commands; no pre-processing needed.
		for _, fmt_ := range formats {
			if err := genCLIDocs(root, cliDir, fmt_); err != nil {
				return fmt.Errorf("generating cli docs (%s): %w", fmt_, err)
			}
			// cobra's stock generators skip hidden commands; when --include-hidden
			// is set, generate their pages explicitly so index links are not broken.
			if df.includeHidden {
				switch fmt_ {
				case "markdown":
					if err := genHiddenCLIMarkdown(root, cliDir); err != nil {
						return fmt.Errorf("generating hidden cli docs: %w", err)
					}
				case "yaml":
					if err := genHiddenCLIYaml(root, cliDir); err != nil {
						return fmt.Errorf("generating hidden cli docs (yaml): %w", err)
					}
				case "man":
					if err := genHiddenCLIMan(root, cliDir); err != nil {
						return fmt.Errorf("generating hidden cli docs (man): %w", err)
					}
				}
			}
		}
		// The CLI index is a markdown file — only generate it when markdown output
		// is included in the requested formats. For yaml/man-only runs the index
		// would link to .md files that were never produced.
		if slices.Contains(formats, "markdown") {
			if err := genCLIIndex(root, cliDir, df.includeHidden); err != nil {
				return fmt.Errorf("generating cli index: %w", err)
			}
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "CLI docs written to %s\n", cliDir)
	}

	if scopes["commands"] {
		reg, err := loadCommandRegistry(rflags.configPath)
		if err != nil {
			return err
		}
		commandsDir := filepath.Join(outDir, "commands")
		if err := os.MkdirAll(commandsDir, 0o755); err != nil {
			return fmt.Errorf("creating commands output dir: %w", err)
		}
		// Registry docs only support markdown. When yaml/man are also in the format
		// list (e.g. --format all), inform the user that those formats are skipped
		// rather than silently producing nothing.
		if slices.Contains(formats, "yaml") || slices.Contains(formats, "man") {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "note: registry docs only support markdown; yaml/man formats skipped for commands scope\n")
		}
		for _, fmt_ := range formats {
			if err := genRegistryDocs(reg, commandsDir, fmt_, df.includePrivate); err != nil {
				return fmt.Errorf("generating commands docs (%s): %w", fmt_, err)
			}
		}
		if err := genCommandsIndex(reg, commandsDir, df.includePrivate); err != nil {
			return fmt.Errorf("generating commands index: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Command docs written to %s\n", commandsDir)
	}

	// Top-level index.
	if err := genTopLevelIndex(outDir, scopes); err != nil {
		return fmt.Errorf("generating top-level index: %w", err)
	}

	return nil
}

func validateDocsFlags(df *docsFlags) error {
	validFormats := map[string]bool{"markdown": true, "yaml": true, "man": true, "all": true}
	if !validFormats[df.format] {
		return fmt.Errorf("--format %q is not valid; must be one of: markdown, yaml, man, all", df.format)
	}
	validScopes := map[string]bool{"all": true, "cli": true, "commands": true}
	if !validScopes[df.scope] {
		return fmt.Errorf("--scope %q is not valid; must be one of: all, cli, commands", df.scope)
	}
	// Registry docs only support markdown; reject non-markdown when the scope
	// would exclusively generate registry output (scope=commands).
	if df.scope == "commands" && df.format != "markdown" && df.format != "all" {
		return fmt.Errorf("--format %q is not supported for --scope commands; registry docs only support markdown (use --format markdown or --format all)", df.format)
	}
	return nil
}

// resolveFormats expands "all" to the full list; otherwise returns the single format.
func resolveFormats(format string) []string {
	if format == "all" {
		return []string{"markdown", "yaml", "man"}
	}
	return []string{format}
}

// resolveScopes returns a set of active scopes.
func resolveScopes(scope string) map[string]bool {
	if scope == "all" {
		return map[string]bool{"cli": true, "commands": true}
	}
	return map[string]bool{scope: true}
}

// genCLIDocs generates docs for the CLI command tree in the given format.
func genCLIDocs(root *cobra.Command, dir, format string) error {
	switch format {
	case "markdown":
		return cobradoc.GenMarkdownTree(root, dir)
	case "yaml":
		return cobradoc.GenYamlTree(root, dir)
	case "man":
		header := &cobradoc.GenManHeader{
			Title:   "DEVBOX",
			Section: "1",
		}
		return cobradoc.GenManTree(root, header, dir)
	default:
		return fmt.Errorf("unknown format %q", format)
	}
}

// genHiddenCLIMarkdown generates markdown pages for hidden commands that cobra's
// stock GenMarkdownTree skips. Called only when --include-hidden is set so that
// every entry written to cli/index.md has a corresponding file.
func genHiddenCLIMarkdown(root *cobra.Command, dir string) error {
	return walkAllCommands(root, func(cmd *cobra.Command) error {
		if !cmd.Hidden {
			return nil
		}
		filename := strings.ReplaceAll(cmd.CommandPath(), " ", "_") + ".md"
		f, err := os.Create(filepath.Join(dir, filename))
		if err != nil {
			return fmt.Errorf("creating doc file for %s: %w", cmd.CommandPath(), err)
		}
		if err := cobradoc.GenMarkdown(cmd, f); err != nil {
			_ = f.Close()
			return err
		}
		return f.Close()
	})
}

// genHiddenCLIYaml generates yaml docs for hidden commands that cobra's
// stock GenYamlTree skips. Called only when --include-hidden is set.
func genHiddenCLIYaml(root *cobra.Command, dir string) error {
	return walkAllCommands(root, func(cmd *cobra.Command) error {
		if !cmd.Hidden {
			return nil
		}
		filename := strings.ReplaceAll(cmd.CommandPath(), " ", "_") + ".yaml"
		f, err := os.Create(filepath.Join(dir, filename))
		if err != nil {
			return fmt.Errorf("creating yaml doc file for %s: %w", cmd.CommandPath(), err)
		}
		if err := cobradoc.GenYaml(cmd, f); err != nil {
			_ = f.Close()
			return err
		}
		return f.Close()
	})
}

// genHiddenCLIMan generates man pages for hidden commands that cobra's
// stock GenManTree skips. Called only when --include-hidden is set.
func genHiddenCLIMan(root *cobra.Command, dir string) error {
	header := &cobradoc.GenManHeader{
		Title:   "DEVBOX",
		Section: "1",
	}
	return walkAllCommands(root, func(cmd *cobra.Command) error {
		if !cmd.Hidden {
			return nil
		}
		basename := strings.ReplaceAll(cmd.CommandPath(), " ", "-")
		filename := basename + "." + header.Section
		f, err := os.Create(filepath.Join(dir, filename))
		if err != nil {
			return fmt.Errorf("creating man doc file for %s: %w", cmd.CommandPath(), err)
		}
		if err := cobradoc.GenMan(cmd, header, f); err != nil {
			_ = f.Close()
			return err
		}
		return f.Close()
	})
}

// walkAllCommands visits every command in the tree, including hidden ones.
func walkAllCommands(cmd *cobra.Command, fn func(*cobra.Command) error) error {
	if err := fn(cmd); err != nil {
		return err
	}
	for _, sub := range cmd.Commands() {
		if err := walkAllCommands(sub, fn); err != nil {
			return err
		}
	}
	return nil
}

// genCLIIndex writes a markdown index of all CLI usercommands.
func genCLIIndex(root *cobra.Command, dir string, includeHidden bool) error {
	var sb strings.Builder
	sb.WriteString("# CLI Reference\n\n")
	sb.WriteString("Generated reference for the `devbox` command tree.\n\n")
	sb.WriteString("## Commands\n\n")

	writeCLIIndexEntries(&sb, root, includeHidden, 0)

	indexPath := filepath.Join(dir, "index.md")
	return os.WriteFile(indexPath, []byte(sb.String()), 0o644)
}

func writeCLIIndexEntries(sb *strings.Builder, cmd *cobra.Command, includeHidden bool, depth int) {
	if cmd.Hidden && !includeHidden {
		return
	}
	// Skip cobra's built-in help subcommand — cobradoc.GenMarkdownTree does not
	// generate a file for it, so linking to it would produce a broken reference.
	if cmd.Name() == "help" {
		return
	}
	if depth > 0 {
		indent := strings.Repeat("  ", depth-1)
		name := cmd.CommandPath()
		// Link to the generated markdown file: cobra uses underscores for spaces.
		slug := strings.ReplaceAll(name, " ", "_") + ".md"
		short := cmd.Short
		if short == "" {
			short = name
		}
		fmt.Fprintf(sb, "%s- [%s](%s) — %s\n", indent, name, slug, short)
	}
	for _, sub := range cmd.Commands() {
		writeCLIIndexEntries(sb, sub, includeHidden, depth+1)
	}
}

// genRegistryDocs generates documentation for each registry command.
func genRegistryDocs(reg *usercommands.Registry, dir, format string, includePrivate bool) error {
	// We always use markdown for the registry; yaml/man are CLI-specific.
	// For non-markdown formats we skip (registry has no cobra representation).
	if format != "markdown" {
		return nil
	}
	return genRegistryMarkdown(reg, dir, includePrivate)
}

// genRegistryMarkdown writes one markdown file per command group and one file
// per command. Private commands are only written when includePrivate is true.
func genRegistryMarkdown(reg *usercommands.Registry, dir string, includePrivate bool) error {
	var all []*usercommands.CommandDef
	if includePrivate {
		all = reg.ListAll("")
	} else {
		all = reg.List("")
	}

	// Group by group ID.
	byGroup := make(map[string][]*usercommands.CommandDef)
	var groups []string
	for _, def := range all {
		g := def.Group
		if _, seen := byGroup[g]; !seen {
			groups = append(groups, g)
		}
		byGroup[g] = append(byGroup[g], def)
	}
	sort.Strings(groups)

	for _, group := range groups {
		defs := byGroup[group]
		groupDir := dir
		if group != "" {
			groupDir = filepath.Join(dir, filepath.FromSlash(strings.ReplaceAll(group, ".", "/")))
		}
		if err := os.MkdirAll(groupDir, 0o755); err != nil {
			return fmt.Errorf("creating group dir %s: %w", groupDir, err)
		}
		for _, def := range defs {
			if err := writeCommandMarkdown(def, groupDir); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeCommandMarkdown writes a single command's documentation to a markdown file.
func writeCommandMarkdown(def *usercommands.CommandDef, dir string) error {
	var sb strings.Builder

	fmt.Fprintf(&sb, "# %s\n\n", def.ID)

	if def.Description != "" {
		sb.WriteString(def.Description + "\n\n")
	}

	sb.WriteString("## Properties\n\n")
	sb.WriteString("| Property | Value |\n|---|---|\n")
	fmt.Fprintf(&sb, "| **ID** | `%s` |\n", def.ID)
	fmt.Fprintf(&sb, "| **Type** | `%s` |\n", def.Type)
	fmt.Fprintf(&sb, "| **Group** | `%s` |\n", def.Group)
	if def.Private {
		sb.WriteString("| **Private** | yes |\n")
	}
	if def.Confirmation {
		sb.WriteString("| **Confirmation** | yes |\n")
		fmt.Fprintf(&sb, "| **Confirmation text** | %s |\n", def.EffectiveConfirmationText())
	}
	if def.Messages.Success != "" {
		fmt.Fprintf(&sb, "| **Success message** | %s |\n", def.Messages.Success)
	}
	if def.Messages.Error != "" {
		fmt.Fprintf(&sb, "| **Error message** | %s |\n", def.Messages.Error)
	}
	sb.WriteString("\n")

	// Type-specific details.
	switch def.Type {
	case usercommands.CommandTypeCommand, usercommands.CommandTypeDevbox:
		if def.Run != "" {
			sb.WriteString("## Command\n\n```sh\n" + def.Run + "\n```\n\n")
		}
		if len(def.Argv) > 0 {
			sb.WriteString("## Argv\n\n```\n" + strings.Join(def.Argv, " ") + "\n```\n\n")
		}
		if def.Workdir != "" {
			fmt.Fprintf(&sb, "**Working directory:** `%s`\n\n", def.Workdir)
		}
	case usercommands.CommandTypeServiceExec, usercommands.CommandTypeServiceRun:
		if def.Service != "" {
			fmt.Fprintf(&sb, "**Service:** `%s`\n\n", def.Service)
		}
		if def.Run != "" {
			sb.WriteString("## Command\n\n```sh\n" + def.Run + "\n```\n\n")
		}
		if len(def.Argv) > 0 {
			sb.WriteString("## Argv\n\n```\n" + strings.Join(def.Argv, " ") + "\n```\n\n")
		}
		if len(def.ComposeArgs) > 0 {
			fmt.Fprintf(&sb, "**Compose args:** `%s`\n\n", strings.Join(def.ComposeArgs, " "))
		}
	case usercommands.CommandTypeScript:
		if def.Script != nil {
			shell := def.Script.Shell
			if shell == "" {
				shell = "sh"
			}
			fmt.Fprintf(&sb, "**Shell:** `%s`\n\n", shell)
			if def.Script.Path != "" {
				fmt.Fprintf(&sb, "**Script:** `%s`\n\n", def.Script.Path)
			}
			if def.Script.Run != "" {
				sb.WriteString("## Script\n\n```sh\n" + def.Script.Run + "\n```\n\n")
			}
		}
		if def.Workdir != "" {
			fmt.Fprintf(&sb, "**Workdir:** `%s`\n\n", def.Workdir)
		}
	case usercommands.CommandTypeWorkflow:
		if len(def.Steps) > 0 {
			sb.WriteString("## Steps\n\n")
			for i, step := range def.Steps {
				if step.Confirm != "" {
					fmt.Fprintf(&sb, "%d. **confirm:** %s\n", i+1, step.Confirm)
				} else {
					line := fmt.Sprintf("%d. `%s`", i+1, step.Command)
					if len(step.With) > 0 {
						var pairs []string
						for k, v := range step.With {
							pairs = append(pairs, k+"="+v)
						}
						sort.Strings(pairs)
						line += " (with: " + strings.Join(pairs, ", ") + ")"
					}
					if step.When != "" {
						line += " (when: " + step.When + ")"
					}
					if step.ContinueOnError {
						line += " (continue_on_error)"
					}
					sb.WriteString(line + "\n")
				}
			}
			sb.WriteString("\n")
		}
	}

	if len(def.Params) > 0 {
		sb.WriteString("## Parameters\n\n")
		sb.WriteString("| Name | Type | Required | Default | Description |\n|---|---|---|---|---|\n")
		var names []string
		for name := range def.Params {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			p := def.Params[name]
			required := ""
			if p.Required {
				required = "yes"
			}
			defVal := p.Default
			if defVal == "" && p.DefaultFrom != "" {
				defVal = fmt.Sprintf("from `%s`", p.DefaultFrom)
			}
			fmt.Fprintf(&sb, "| `%s` | `%s` | %s | %s | %s |\n",
				name, p.Type, required, defVal, p.Description)
		}
		sb.WriteString("\n")
	}

	if len(def.Context) > 0 {
		sb.WriteString("## Context\n\n")
		sb.WriteString("| Name | From | Required | Env |\n|---|---|---|---|\n")
		var names []string
		for name := range def.Context {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			c := def.Context[name]
			required := ""
			if c.Required {
				required = "yes"
			}
			fmt.Fprintf(&sb, "| `%s` | `%s` | %s | %s |\n", name, c.From, required, c.Env)
		}
		sb.WriteString("\n")
	}

	if len(def.Files) > 0 {
		sb.WriteString("## Files\n\n")
		var fileIDs []string
		for id := range def.Files {
			fileIDs = append(fileIDs, id)
		}
		sort.Strings(fileIDs)
		for _, id := range fileIDs {
			f := def.Files[id]
			attrs := string(f.Access)
			if f.Required {
				attrs += ", required"
			}
			fmt.Fprintf(&sb, "### `%s` (%s)\n\n", id, attrs)
			if f.Env != "" {
				fmt.Fprintf(&sb, "**Env:** `%s`\n\n", f.Env)
			}
			if f.Path != "" {
				fmt.Fprintf(&sb, "**Path:** `%s`\n\n", f.Path)
			}
			if len(f.Candidates) > 0 {
				sb.WriteString("**Candidates:**\n\n")
				for i, c := range f.Candidates {
					if c.Glob != "" {
						line := fmt.Sprintf("%d. glob: `%s`", i+1, c.Glob)
						if c.Match != "" {
							line += fmt.Sprintf(" (match: `%s`", c.Match)
							if c.Sort != "" {
								line += fmt.Sprintf(", sort: %s", string(c.Sort))
							}
							line += ")"
						} else if c.Sort != "" {
							line += fmt.Sprintf(" (sort: %s)", string(c.Sort))
						}
						sb.WriteString(line + "\n")
					} else if c.Path != "" {
						fmt.Fprintf(&sb, "%d. path: `%s`\n", i+1, c.Path)
					}
				}
				sb.WriteString("\n")
			}
		}
	}

	if len(def.Env) > 0 {
		sb.WriteString("## Environment Variables\n\n")
		sb.WriteString("| Name | Value |\n|---|---|\n")
		var keys []string
		for k := range def.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&sb, "| `%s` | `%s` |\n", k, def.Env[k])
		}
		sb.WriteString("\n")
	}

	filename := def.LocalName + ".md"
	return os.WriteFile(filepath.Join(dir, filename), []byte(sb.String()), 0o644)
}

// genCommandsIndex writes the commands reference index.
func genCommandsIndex(reg *usercommands.Registry, dir string, includePrivate bool) error {
	var defs []*usercommands.CommandDef
	if includePrivate {
		defs = reg.ListAll("")
	} else {
		defs = reg.List("")
	}

	var sb strings.Builder
	sb.WriteString("# Commands Reference\n\n")
	sb.WriteString("Reference for declarative commands defined in `devbox/commands/`.\n\n")

	if len(defs) == 0 {
		sb.WriteString("No commands defined.\n")
	} else {
		// Group by group.
		byGroup := make(map[string][]*usercommands.CommandDef)
		var groups []string
		for _, def := range defs {
			g := def.Group
			if _, seen := byGroup[g]; !seen {
				groups = append(groups, g)
			}
			byGroup[g] = append(byGroup[g], def)
		}
		sort.Strings(groups)

		for _, group := range groups {
			groupLabel := group
			if groupLabel == "" {
				groupLabel = "(root)"
			}
			fmt.Fprintf(&sb, "## %s\n\n", groupLabel)
			for _, def := range byGroup[group] {
				relPath := def.LocalName + ".md"
				if group != "" {
					relPath = strings.ReplaceAll(group, ".", "/") + "/" + relPath
				}
				private := ""
				if def.Private {
					private = " *(private)*"
				}
				desc := def.Description
				if desc == "" {
					desc = string(def.Type)
				}
				fmt.Fprintf(&sb, "- [%s](%s)%s — %s\n", def.ID, relPath, private, desc)
			}
			sb.WriteString("\n")
		}
	}

	indexPath := filepath.Join(dir, "index.md")
	return os.WriteFile(indexPath, []byte(sb.String()), 0o644)
}

// genTopLevelIndex writes a top-level docs/reference/index.md.
func genTopLevelIndex(outDir string, scopes map[string]bool) error {
	var sb strings.Builder
	sb.WriteString("# devbox Reference Documentation\n\n")
	sb.WriteString("Generated reference documentation for devbox.\n\n")
	sb.WriteString("## Sections\n\n")
	if scopes["cli"] {
		sb.WriteString("- [CLI Reference](cli/index.md) — `devbox` command tree\n")
	}
	if scopes["commands"] {
		sb.WriteString("- [Commands Reference](commands/index.md) — declarative command registry\n")
	}
	sb.WriteString("\n")

	indexPath := filepath.Join(outDir, "index.md")
	return os.WriteFile(indexPath, []byte(sb.String()), 0o644)
}
