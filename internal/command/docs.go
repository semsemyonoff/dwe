package command

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"devbox-cli/internal/commands"

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

	// Resolve output dir relative to project root (same dir as devbox.yml).
	projectRoot := filepath.Dir(rflags.configPath)
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
		if !df.includeHidden {
			hideHiddenForDocs(root)
		}
		for _, fmt_ := range formats {
			if err := genCLIDocs(root, cliDir, fmt_); err != nil {
				return fmt.Errorf("generating cli docs (%s): %w", fmt_, err)
			}
		}
		if err := genCLIIndex(root, cliDir, df.includeHidden); err != nil {
			return fmt.Errorf("generating cli index: %w", err)
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
		for _, fmt_ := range formats {
			if err := genRegistryDocs(reg, commandsDir, fmt_); err != nil {
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

// hideHiddenForDocs temporarily marks hidden commands so cobra/doc skips them.
// cobra/doc already skips commands where Hidden==true, so this is a no-op;
// included for explicitness.
func hideHiddenForDocs(_ *cobra.Command) {
	// cobra/doc already excludes Hidden commands. Nothing to do.
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

// genCLIIndex writes a markdown index of all CLI commands.
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
func genRegistryDocs(reg *commands.Registry, dir, format string) error {
	// We always use markdown for the registry; yaml/man are CLI-specific.
	// For non-markdown formats we skip (registry has no cobra representation).
	if format != "markdown" {
		return nil
	}
	return genRegistryMarkdown(reg, dir)
}

// genRegistryMarkdown writes one markdown file per command group and one file
// per private/public command.
func genRegistryMarkdown(reg *commands.Registry, dir string) error {
	all := reg.ListAll("")

	// Group by group ID.
	byGroup := make(map[string][]*commands.CommandDef)
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
func writeCommandMarkdown(def *commands.CommandDef, dir string) error {
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
	sb.WriteString("\n")

	// Type-specific details.
	switch def.Type {
	case commands.CommandTypeCommand, commands.CommandTypeDevbox:
		if def.Run != "" {
			sb.WriteString("## Command\n\n```sh\n" + def.Run + "\n```\n\n")
		}
		if len(def.Argv) > 0 {
			sb.WriteString("## Argv\n\n```\n" + strings.Join(def.Argv, " ") + "\n```\n\n")
		}
		if def.Cwd != "" {
			fmt.Fprintf(&sb, "**Working directory:** `%s`\n\n", def.Cwd)
		}
	case commands.CommandTypeServiceExec, commands.CommandTypeServiceRun:
		if def.Service != "" {
			fmt.Fprintf(&sb, "**Service:** `%s`\n\n", def.Service)
		}
		if def.Run != "" {
			sb.WriteString("## Command\n\n```sh\n" + def.Run + "\n```\n\n")
		}
		if len(def.Argv) > 0 {
			sb.WriteString("## Argv\n\n```\n" + strings.Join(def.Argv, " ") + "\n```\n\n")
		}
	case commands.CommandTypeScript:
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
	case commands.CommandTypeWorkflow:
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
func genCommandsIndex(reg *commands.Registry, dir string, includePrivate bool) error {
	var defs []*commands.CommandDef
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
		byGroup := make(map[string][]*commands.CommandDef)
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
