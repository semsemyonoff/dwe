package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/docs"
	"devbox-cli/internal/core/docs/mermaid"
	"devbox-cli/internal/core/docs/tui"
	"devbox-cli/internal/core/project/config"
	userpkg "devbox-cli/internal/core/project/user"
	"devbox-cli/internal/core/ui"
	"devbox-cli/internal/core/usercommands"
	"devbox-cli/internal/shared/i18n"

	cobradoc "github.com/spf13/cobra/doc"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/spf13/cobra"
)

type docsFlags struct {
	output         string
	format         string
	scope          string
	lang           string
	includeHidden  bool
	includePrivate bool
}

func newDocsCmd(flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Browse and manage documentation",
		Long: `Browse and manage devbox documentation.

View documentation interactively with a TUI browser or display specific topics.
Generate reference documentation for the CLI and command registry.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check if we're in a TTY
			if !term.IsTerminal(int(os.Stdout.Fd())) {
				return errors.New("devbox docs without arguments requires a TTY; use 'devbox docs show <topic>' or 'devbox docs list' for non-interactive use")
			}

			// Get terminal dimensions
			width, height, err := term.GetSize(int(os.Stdout.Fd()))
			if err != nil {
				return fmt.Errorf("failed to get terminal size: %w", err)
			}

			return runDocsTUI(cmd, flags, width, height)
		},
	}
	cmd.AddCommand(newDocsShowCmd(flags))
	cmd.AddCommand(newDocsListCmd(flags))
	cmd.AddCommand(newDocsSearchCmd(flags))
	cmd.AddCommand(newDocsExportCmd(flags))
	cmd.AddCommand(newDocsCacheCmd(flags))
	cmd.AddCommand(newDocsGenerateCmd(flags))
	return cmd
}

func newDocsGenerateCmd(flags *cmdctx.RootFlags) *cobra.Command {
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
	cmd.Flags().StringVar(&df.lang, "lang", "", "Language code (default: from userconfig / $LANG)")
	cmd.Flags().BoolVar(&df.includeHidden, "include-hidden", false, "Include hidden CLI commands")
	cmd.Flags().BoolVar(&df.includePrivate, "include-private", false, "Include private registry commands")

	return cmd
}

func runDocsGenerate(cmd *cobra.Command, rflags *cmdctx.RootFlags, df *docsFlags) error {
	if err := validateDocsFlags(df); err != nil {
		return err
	}

	// Resolve output dir relative to project root.
	projectRoot := rflags.ProjectRoot()
	if projectRoot == "" {
		// No project found — only --scope cli is allowed without a project.
		requestedScopes := resolveScopes(df.scope)
		if requestedScopes["commands"] {
			scopeLabel := "commands scope"
			if df.scope == "all" {
				scopeLabel = "all scope (which includes commands)"
			}
			return fmt.Errorf("%s requires a devbox project; use --scope cli to generate CLI reference docs without a project", scopeLabel)
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
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s CLI docs written to %s\n", ui.LogoMark(), cliDir)
	}

	if scopes["commands"] {
		reg, err := usercommands.LoadRegistryFromConfigPath(rflags.ConfigPath)
		if err != nil {
			return err
		}

		// Resolve locale: explicit --lang flag takes precedence, then fall back to rflags.Locale
		// (which is already clamped to available locales in root PersistentPreRunE).
		// Clamp explicit --lang too so an unavailable locale doesn't produce English content
		// under a foreign-language path (e.g. commands/ru/ when no ru.yml exists).
		resolvedLocale := rflags.Locale
		if df.lang != "" {
			resolvedLocale = rflags.I18n.ClampLocale(i18n.ResolveLocale(df.lang, "", ""))
		}

		// commandsDir now includes the language: commands/<lang>
		langDir := filepath.Join("commands", resolvedLocale)
		commandsDir := filepath.Join(outDir, langDir)
		if err := os.MkdirAll(commandsDir, 0o755); err != nil {
			return fmt.Errorf("creating commands output dir: %w", err)
		}
		// Registry docs only support markdown. When yaml/man are also in the format
		// list (e.g. --format all), inform the user that those formats are skipped
		// rather than silently producing nothing.
		if slices.Contains(formats, "yaml") || slices.Contains(formats, "man") {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s note: registry docs only support markdown; yaml/man formats skipped for commands scope\n", ui.LogoMark())
		}
		for _, fmt_ := range formats {
			if err := genRegistryDocs(reg, commandsDir, fmt_, df.includePrivate, rflags.I18n, resolvedLocale); err != nil {
				return fmt.Errorf("generating commands docs (%s): %w", fmt_, err)
			}
		}
		if err := genCommandsIndex(reg, commandsDir, df.includePrivate, rflags.I18n, resolvedLocale); err != nil {
			return fmt.Errorf("generating commands index: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s Command docs written to %s\n", ui.LogoMark(), commandsDir)
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
	// --lang all is reserved for future multi-locale generation; reject it now.
	if df.lang == "all" {
		return fmt.Errorf("--lang all is not supported; specify a single locale (e.g. --lang en)")
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
func genRegistryDocs(reg *usercommands.Registry, dir, format string, includePrivate bool, store *i18n.Store, locale string) error {
	// We always use markdown for the registry; yaml/man are CLI-specific.
	// For non-markdown formats we skip (registry has no cobra representation).
	if format != "markdown" {
		return nil
	}
	return genRegistryMarkdown(reg, dir, includePrivate, store, locale)
}

// genRegistryMarkdown writes one markdown file per command group and one file
// per command. Private commands are only written when includePrivate is true.
func genRegistryMarkdown(reg *usercommands.Registry, dir string, includePrivate bool, store *i18n.Store, locale string) error {
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
			if err := writeCommandMarkdown(def, groupDir, reg, store, locale); err != nil {
				return err
			}
		}
	}
	return nil
}

// stepCommandDescription returns the localized description for the command
// referenced by a workflow step, or "" when the command is unknown or has no
// description. Used by workflow rendering to annotate step IDs.
func stepCommandDescription(reg *usercommands.Registry, store *i18n.Store, locale, commandID string) string {
	if reg == nil || commandID == "" {
		return ""
	}
	target, err := reg.Get(commandID)
	if err != nil {
		return ""
	}
	return store.CommandDescription(locale, target.ID, target.Description)
}

// writeCommandMarkdown writes a single command's documentation to a markdown file.
func writeCommandMarkdown(def *usercommands.CommandDef, dir string, reg *usercommands.Registry, store *i18n.Store, locale string) error {
	var sb strings.Builder

	fmt.Fprintf(&sb, "# %s\n\n", def.ID)

	// Use i18n lookup for description
	description := store.CommandDescription(locale, def.ID, def.Description)
	if description != "" {
		sb.WriteString(description + "\n\n")
	}

	propertiesHeader := store.T(locale, "docs.section.properties", "Properties")
	sb.WriteString("## " + propertiesHeader + "\n\n")
	sb.WriteString("| Property | Value |\n|---|---|\n")
	fmt.Fprintf(&sb, "| **%s** | `%s` |\n", store.T(locale, "docs.property.id", "ID"), def.ID)
	fmt.Fprintf(&sb, "| **%s** | `%s` |\n", store.T(locale, "docs.property.type", "Type"), def.Type)
	fmt.Fprintf(&sb, "| **%s** | `%s` |\n", store.T(locale, "docs.property.group", "Group"), def.Group)
	if def.Private {
		sb.WriteString("| **" + store.T(locale, "docs.property.private", "Private") + "** | yes |\n")
	}
	if def.Confirmation {
		sb.WriteString("| **" + store.T(locale, "docs.property.confirmation", "Confirmation") + "** | yes |\n")
		confirmationText := store.CommandConfirmationText(locale, def.ID, def.EffectiveConfirmationText())
		fmt.Fprintf(&sb, "| **%s** | %s |\n", store.T(locale, "docs.property.confirmation_text", "Confirmation text"), confirmationText)
	}
	if def.Messages.Success != "" {
		fmt.Fprintf(&sb, "| **%s** | %s |\n", store.T(locale, "docs.property.success_message", "Success message"), def.Messages.Success)
	}
	if def.Messages.Error != "" {
		fmt.Fprintf(&sb, "| **%s** | %s |\n", store.T(locale, "docs.property.error_message", "Error message"), def.Messages.Error)
	}
	sb.WriteString("\n")

	commandHeader := store.T(locale, "docs.section.command", "Command")
	argvHeader := store.T(locale, "docs.section.argv", "Argv")
	scriptHeader := store.T(locale, "docs.section.script", "Script")
	withHeader := store.T(locale, "docs.section.with", "With")
	workdirLabel := store.T(locale, "docs.property.workdir", "Working directory")
	serviceLabel := store.T(locale, "docs.property.service", "Service")
	shellLabel := store.T(locale, "docs.property.shell", "Shell")
	composeArgsLabel := store.T(locale, "docs.property.compose_args", "Compose args")
	scriptLabel := store.T(locale, "docs.property.script", "Script")
	builtinLabel := store.T(locale, "docs.property.builtin", "Builtin")

	// Type-specific details.
	switch def.Type {
	case usercommands.CommandTypeShell, usercommands.CommandTypeDevbox:
		if def.Cmd != "" {
			fmt.Fprintf(&sb, "## %s\n\n```sh\n%s\n```\n\n", commandHeader, def.Cmd)
		}
		if len(def.Argv) > 0 {
			fmt.Fprintf(&sb, "## %s\n\n```\n%s\n```\n\n", argvHeader, strings.Join(def.Argv, " "))
		}
		if def.Workdir != "" {
			fmt.Fprintf(&sb, "**%s:** `%s`\n\n", workdirLabel, def.Workdir)
		}
	case usercommands.CommandTypeServiceExec, usercommands.CommandTypeServiceRun:
		if def.Service != "" {
			fmt.Fprintf(&sb, "**%s:** `%s`\n\n", serviceLabel, def.Service)
		}
		if def.Cmd != "" {
			fmt.Fprintf(&sb, "## %s\n\n```sh\n%s\n```\n\n", commandHeader, def.Cmd)
		}
		if len(def.Argv) > 0 {
			fmt.Fprintf(&sb, "## %s\n\n```\n%s\n```\n\n", argvHeader, strings.Join(def.Argv, " "))
		}
		if len(def.ComposeArgs) > 0 {
			fmt.Fprintf(&sb, "**%s:** `%s`\n\n", composeArgsLabel, strings.Join(def.ComposeArgs, " "))
		}
	case usercommands.CommandTypeScript:
		if def.Script != nil {
			shell := def.Script.Shell
			if shell == "" {
				shell = "sh"
			}
			fmt.Fprintf(&sb, "**%s:** `%s`\n\n", shellLabel, shell)
			if def.Script.Path != "" {
				fmt.Fprintf(&sb, "**%s:** `%s`\n\n", scriptLabel, def.Script.Path)
			}
			if def.Script.Run != "" {
				fmt.Fprintf(&sb, "## %s\n\n```sh\n%s\n```\n\n", scriptHeader, def.Script.Run)
			}
		}
		if def.Workdir != "" {
			fmt.Fprintf(&sb, "**%s:** `%s`\n\n", workdirLabel, def.Workdir)
		}
	case usercommands.CommandTypeBuiltin:
		if def.Cmd != "" {
			fmt.Fprintf(&sb, "**%s:** `%s`\n\n", builtinLabel, def.Cmd)
		}
		if len(def.With) > 0 {
			fmt.Fprintf(&sb, "## %s\n\n", withHeader)
			var keys []string
			for k := range def.With {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(&sb, "- `%s`: `%v`\n", k, def.With[k])
			}
			sb.WriteString("\n")
		}
	case usercommands.CommandTypeWorkflow:
		if len(def.Steps) > 0 {
			stepsHeader := store.T(locale, "docs.section.steps", "Steps")
			parallelLabel := store.T(locale, "docs.workflow.parallel", "parallel")
			subStepsLabel := store.T(locale, "docs.workflow.sub_steps", "sub-steps")
			sb.WriteString("## " + stepsHeader + "\n\n")
			for i, step := range def.Steps {
				switch {
				case step.Confirm != "":
					fmt.Fprintf(&sb, "%d. **confirm:** %s\n", i+1, step.Confirm)
				case step.Parallel != nil:
					p := step.Parallel
					var meta []string
					if p.MaxConcurrent > 0 {
						meta = append(meta, fmt.Sprintf("max_concurrent=%d", p.MaxConcurrent))
					}
					if p.FailFast != nil {
						meta = append(meta, fmt.Sprintf("fail_fast=%v", *p.FailFast))
					}
					line := fmt.Sprintf("%d. **%s:** %d %s", i+1, parallelLabel, len(p.Steps), subStepsLabel)
					if len(meta) > 0 {
						line += " (" + strings.Join(meta, ", ") + ")"
					}
					if step.When != "" {
						line += " (when: " + step.When + ")"
					}
					if step.ContinueOnError {
						line += " (continue_on_error)"
					}
					sb.WriteString(line + "\n")
					for _, sub := range p.Steps {
						subLine := "   - `" + sub.Command + "`"
						if desc := stepCommandDescription(reg, store, locale, sub.Command); desc != "" {
							subLine += " — " + desc
						}
						if len(sub.With) > 0 {
							var pairs []string
							for k, v := range sub.With {
								pairs = append(pairs, k+"="+v)
							}
							sort.Strings(pairs)
							subLine += " (with: " + strings.Join(pairs, ", ") + ")"
						}
						if sub.When != "" {
							subLine += " (when: " + sub.When + ")"
						}
						if sub.ContinueOnError {
							subLine += " (continue_on_error)"
						}
						sb.WriteString(subLine + "\n")
					}
				default:
					line := fmt.Sprintf("%d. `%s`", i+1, step.Command)
					if desc := stepCommandDescription(reg, store, locale, step.Command); desc != "" {
						line += " — " + desc
					}
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
		parametersHeader := store.T(locale, "docs.section.parameters", "Parameters")
		sb.WriteString("## " + parametersHeader + "\n\n")
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
			paramDesc := store.ParamDescription(locale, def.ID, name, p.Description)
			fmt.Fprintf(&sb, "| `%s` | `%s` | %s | %s | %s |\n",
				name, p.Type, required, defVal, paramDesc)
		}
		sb.WriteString("\n")
	}

	if len(def.Context) > 0 {
		contextHeader := store.T(locale, "docs.section.context", "Context")
		sb.WriteString("## " + contextHeader + "\n\n")
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
		filesHeader := store.T(locale, "docs.section.files", "Files")
		sb.WriteString("## " + filesHeader + "\n\n")
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
		envHeader := store.T(locale, "docs.section.environment", "Environment Variables")
		sb.WriteString("## " + envHeader + "\n\n")
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
func genCommandsIndex(reg *usercommands.Registry, dir string, includePrivate bool, store *i18n.Store, locale string) error {
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
			// Use group title from i18n if available
			if group != "" {
				groupLabel = store.GroupTitle(locale, group, group)
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
				// Use i18n lookup for description
				desc := store.CommandDescription(locale, def.ID, def.Description)
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
		// List language subdirectories under commands/
		commandsDir := filepath.Join(outDir, "commands")
		entries, err := os.ReadDir(commandsDir)
		wroteDir := false
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					lang := entry.Name()
					fmt.Fprintf(&sb, "- [Commands Reference (lang=%s)](commands/%s/index.md) — declarative command registry\n", lang, lang)
					wroteDir = true
				}
			}
		}
		// Fallback if commands dir doesn't exist yet (shouldn't happen in normal flow)
		if !wroteDir {
			sb.WriteString("- [Commands Reference](commands/index.md) — declarative command registry\n")
		}
	}
	sb.WriteString("\n")

	indexPath := filepath.Join(outDir, "index.md")
	return os.WriteFile(indexPath, []byte(sb.String()), 0o644)
}

// mmdcAvailable reports whether the configured mmdc binary resolves on $PATH
// (or as an absolute path). Used purely for diagnostic banners — the renderer
// itself handles the actual unavailable-fallback path.
func mmdcAvailable(bin string) bool {
	if bin == "" {
		return false
	}
	_, err := exec.LookPath(bin)
	return err == nil
}

func runDocsTUI(cmd *cobra.Command, flags *cmdctx.RootFlags, termWidth, termHeight int) error {
	// Load configuration for doc settings (mermaid config, etc.)
	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		// If config fails to load, use defaults (docs still work without full config)
		cfg = &config.DevboxConfig{}
	}

	// Get project root, user config language, and mermaid theme override.
	projectRoot := flags.ProjectRoot()
	var cfgLang string
	var mermaidTheme string
	if ucfg, err := userpkg.Load(projectRoot); err == nil && ucfg != nil {
		cfgLang = ucfg.Language
		mermaidTheme = ucfg.MermaidTheme
	}

	// Resolve locale directly from config and environment — do NOT use flags.Locale
	// (it is clamped to the YAML i18n store, a different namespace from markdown translations).
	locale := i18n.ResolveLocale("", cfgLang, os.Getenv("LANG"))

	// Build mermaid renderer chain based on config
	var renderer mermaid.Renderer
	cacheDir, err := mermaid.CacheDir()
	if err != nil {
		cacheDir = ""
	}

	cacheCapBytes := int64(config.MermaidCacheSizeMB(cfg) * 1024 * 1024)
	mermaidMode := config.MermaidMode(cfg)

	switch mermaidMode {
	case "off":
		renderer = mermaid.Disabled{}
	case "mmdc":
		// Strict mode: mmdc is required
		renderer = mermaid.New(config.MmdcBin(cfg), cacheDir, cacheCapBytes, true)
	default: // "auto"
		renderer = mermaid.New(config.MmdcBin(cfg), cacheDir, cacheCapBytes, false)
	}

	// Get sources (devbox + project docs)
	sources := docs.Sources(projectRoot)

	// Create translator for TUI strings
	translator := i18n.TranslatorOrNop(flags.I18n)

	// Create the model. Title shape matches cmdbrowser: "Devbox · <project> · Documentation".
	ctx := cmd.Context()
	projectName := ""
	if cfg != nil {
		projectName = cfg.Project.Name
	}
	title := selectorTitle(projectName, "Documentation")
	model, err := tui.NewModel(ctx, sources, locale, translator, renderer, termWidth, termHeight, projectRoot, title, mermaidTheme)
	if err != nil {
		return fmt.Errorf("failed to create TUI model: %w", err)
	}

	// Banner: warn once at startup when mmdc is missing on $PATH (and the user
	// hasn't explicitly disabled mermaid). Skipping the install entirely would
	// leave users guessing why diagrams never render — the banner points them
	// at the canonical install section in docs/reference/docs/index.md.
	if mermaidMode != "off" && !mmdcAvailable(config.MmdcBin(cfg)) {
		model.MmdcNotice = "> **⚠ `mmdc` not installed.** Mermaid diagrams cannot render. " +
			"Install with `npm i -g @mermaid-js/mermaid-cli` — see " +
			"`docs/reference/docs/index.md` § *Installing `mmdc`*.\n\n"
	}

	// Run via ui.RunWithPromptHooks for proper signal handling
	runErr := ui.RunWithPromptHooks(func() error {
		prog := tea.NewProgram(model, tea.WithContext(ctx))
		_, e := prog.Run()
		return e
	})

	if runErr != nil {
		if errors.Is(runErr, tea.ErrProgramPanic) {
			return runErr
		}
		if errors.Is(runErr, tea.ErrInterrupted) || errors.Is(runErr, tea.ErrProgramKilled) {
			return ui.ErrCancelled
		}
		return runErr
	}

	return nil
}
