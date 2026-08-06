package docs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
	"github.com/semsemyonoff/dwe/internal/shared/pathsafe"

	"github.com/spf13/cobra"
)

func newDocsGenerateCmd(flags *cmdctx.RootFlags) *cobra.Command {
	df := &docsFlags{}

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate reference documentation",
		Long: `Generate reference documentation for the declarative command registry
(workspace/commands/) as markdown.`,
		Example: `  dwe docs generate
  dwe docs generate --lang ru --include-private`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDocsGenerate(cmd, flags, df)
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&df.output, "out", "docs/reference", "Output directory for generated docs")
	cmd.Flags().StringVar(&df.lang, "lang", "", "Language code (default: from userconfig / $LANG)")
	cmd.Flags().BoolVar(&df.includePrivate, "include-private", false, "Include private registry commands")

	return cmd
}

func runDocsGenerate(cmd *cobra.Command, rflags *cmdctx.RootFlags, df *docsFlags) error {
	if err := validateDocsFlags(df); err != nil {
		return err
	}

	projectRoot := rflags.ProjectRoot()
	if projectRoot == "" {
		return fmt.Errorf("dwe docs generate requires a dwe project (workspace.yml not found)")
	}
	outDir := df.output
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(projectRoot, outDir)
	}

	reg, err := usercommands.LoadRegistryFromConfigPath(rflags.ConfigPath)
	if err != nil {
		return err
	}
	// Apply hide: visibility so generated docs match the runtime CLI
	// surface — parity with `dwe docs llms-txt`. Best-effort: cfg load
	// errors are tolerated; ApplyVisibility is fail-open on per-expression
	// failures.
	cfg, _ := config.LoadConfig(rflags.ConfigPath)
	_ = reg.ApplyVisibility(cfg, rflags.ProjectRoot())

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
	if err := genRegistryMarkdown(reg, commandsDir, df.includePrivate, rflags.I18n, resolvedLocale); err != nil {
		return fmt.Errorf("generating commands docs: %w", err)
	}
	if err := genCommandsIndex(reg, commandsDir, df.includePrivate, rflags.I18n, resolvedLocale); err != nil {
		return fmt.Errorf("generating commands index: %w", err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s Command docs written to %s\n", render.LogoMark(), commandsDir)

	if err := genTopLevelIndex(outDir); err != nil {
		return fmt.Errorf("generating top-level index: %w", err)
	}

	return nil
}

// listCommandsByGroup groups defs by their Group field, returning a sorted
// slice of group IDs and a map of group → commands. The groups slice is sorted
// ascending so index pages and directory trees are stable across runs.
func listCommandsByGroup(defs []*usercommands.CommandDef) (groups []string, byGroup map[string][]*usercommands.CommandDef) {
	byGroup = make(map[string][]*usercommands.CommandDef)
	for _, def := range defs {
		g := def.Group
		if _, seen := byGroup[g]; !seen {
			groups = append(groups, g)
		}
		byGroup[g] = append(byGroup[g], def)
	}
	sort.Strings(groups)
	return groups, byGroup
}

func validateDocsFlags(df *docsFlags) error {
	// --lang all is reserved for future multi-locale generation; reject it now.
	if df.lang == "all" {
		return fmt.Errorf("--lang all is not supported; specify a single locale (e.g. --lang en)")
	}
	return nil
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
	groups, byGroup := listCommandsByGroup(all)

	for _, group := range groups {
		defs := byGroup[group]
		groupDir := dir
		if group != "" {
			var err error
			groupDir, err = containedJoin(dir, filepath.FromSlash(strings.ReplaceAll(group, ".", "/")))
			if err != nil {
				return fmt.Errorf("group %q: %w", group, err)
			}
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

	writeCommandTypeDetails(&sb, def, reg, store, locale)
	writeCommandParams(&sb, def, store, locale)
	writeCommandContext(&sb, def, store, locale)
	writeCommandFiles(&sb, def, store, locale)
	writeCommandEnv(&sb, def, store, locale)

	path, err := containedJoin(dir, def.LocalName+".md")
	if err != nil {
		return fmt.Errorf("command %q: %w", def.ID, err)
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

// containedJoin joins a caller-supplied relative element onto dir and rejects
// the result if it escapes dir.
//
// Both elements passed here are raw user text: a command's LocalName is the
// verbatim YAML map key from workspace/commands/*.yml and a group id is built
// from the same source, and neither is validated beyond non-emptiness. A plain
// filepath.Join would silently RESOLVE a "../.." in either one rather than
// reject it, turning `dwe docs generate` on an untrusted project into an
// arbitrary file write outside both --out and the project root.
func containedJoin(dir, elem string) (string, error) {
	joined := filepath.Join(dir, elem)
	if _, err := pathsafe.ContainedRel(dir, joined); err != nil {
		return "", fmt.Errorf("refusing to write outside the output directory: %w", err)
	}
	return joined, nil
}

// writeCommandTypeDetails writes the type-specific detail section (command/argv/
// script/with/steps) for a command's markdown.
func writeCommandTypeDetails(sb *strings.Builder, def *usercommands.CommandDef, reg *usercommands.Registry, store *i18n.Store, locale string) {
	commandHeader := store.T(locale, "docs.section.command", "Command")
	argvHeader := store.T(locale, "docs.section.argv", "Argv")
	scriptHeader := store.T(locale, "docs.section.script", "Script")
	withHeader := store.T(locale, "docs.section.with", "With")
	workdirLabel := store.T(locale, "docs.property.workdir", "Working directory")
	serviceLabel := store.T(locale, "docs.property.service", "Service")
	shellLabel := store.T(locale, "docs.property.shell", "Shell")
	composeArgsLabel := store.T(locale, "docs.property.compose_args", "Compose args")
	argvAppendFromLabel := store.T(locale, "docs.property.argv_append_from", "Argv append from")
	scriptLabel := store.T(locale, "docs.property.script", "Script")
	builtinLabel := store.T(locale, "docs.property.builtin", "Builtin")

	switch def.Type {
	case usercommands.CommandTypeShell, usercommands.CommandTypeDwe:
		if def.Cmd != "" {
			fmt.Fprintf(sb, "## %s\n\n```sh\n%s\n```\n\n", commandHeader, def.Cmd)
		}
		if len(def.Argv) > 0 {
			fmt.Fprintf(sb, "## %s\n\n```\n%s\n```\n\n", argvHeader, strings.Join(def.Argv, " "))
		}
		if def.ArgvAppendFrom != "" {
			fmt.Fprintf(sb, "**%s:** `%s`\n\n", argvAppendFromLabel, def.ArgvAppendFrom)
		}
		if def.Workdir != "" {
			fmt.Fprintf(sb, "**%s:** `%s`\n\n", workdirLabel, def.Workdir)
		}
	case usercommands.CommandTypeServiceExec, usercommands.CommandTypeServiceRun:
		if def.Service != "" {
			fmt.Fprintf(sb, "**%s:** `%s`\n\n", serviceLabel, def.Service)
		}
		if def.Cmd != "" {
			fmt.Fprintf(sb, "## %s\n\n```sh\n%s\n```\n\n", commandHeader, def.Cmd)
		}
		if len(def.Argv) > 0 {
			fmt.Fprintf(sb, "## %s\n\n```\n%s\n```\n\n", argvHeader, strings.Join(def.Argv, " "))
		}
		if def.ArgvAppendFrom != "" {
			fmt.Fprintf(sb, "**%s:** `%s`\n\n", argvAppendFromLabel, def.ArgvAppendFrom)
		}
		if len(def.ComposeArgs) > 0 {
			fmt.Fprintf(sb, "**%s:** `%s`\n\n", composeArgsLabel, strings.Join(def.ComposeArgs, " "))
		}
	case usercommands.CommandTypeScript:
		if def.Script != nil {
			shell := def.Script.Shell
			if shell == "" {
				shell = "sh"
			}
			fmt.Fprintf(sb, "**%s:** `%s`\n\n", shellLabel, shell)
			if def.Script.Path != "" {
				fmt.Fprintf(sb, "**%s:** `%s`\n\n", scriptLabel, def.Script.Path)
			}
			if def.Script.Run != "" {
				fmt.Fprintf(sb, "## %s\n\n```sh\n%s\n```\n\n", scriptHeader, def.Script.Run)
			}
		}
		if def.Workdir != "" {
			fmt.Fprintf(sb, "**%s:** `%s`\n\n", workdirLabel, def.Workdir)
		}
	case usercommands.CommandTypeBuiltin:
		if def.Cmd != "" {
			fmt.Fprintf(sb, "**%s:** `%s`\n\n", builtinLabel, def.Cmd)
		}
		if len(def.With) > 0 {
			fmt.Fprintf(sb, "## %s\n\n", withHeader)
			var keys []string
			for k := range def.With {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(sb, "- `%s`: `%v`\n", k, def.With[k])
			}
			sb.WriteString("\n")
		}
	case usercommands.CommandTypeWorkflow:
		writeCommandWorkflowSteps(sb, def, reg, store, locale)
	}
}

// writeCommandWorkflowSteps writes the Steps section for a workflow command.
func writeCommandWorkflowSteps(sb *strings.Builder, def *usercommands.CommandDef, reg *usercommands.Registry, store *i18n.Store, locale string) {
	if len(def.Steps) == 0 {
		return
	}
	stepsHeader := store.T(locale, "docs.section.steps", "Steps")
	parallelLabel := store.T(locale, "docs.workflow.parallel", "parallel")
	subStepsLabel := store.T(locale, "docs.workflow.sub_steps", "sub-steps")
	sb.WriteString("## " + stepsHeader + "\n\n")
	for i, step := range def.Steps {
		switch {
		case step.Confirm != "":
			fmt.Fprintf(sb, "%d. **confirm:** %s\n", i+1, step.Confirm)
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

// writeCommandParams writes the Parameters table.
func writeCommandParams(sb *strings.Builder, def *usercommands.CommandDef, store *i18n.Store, locale string) {
	if len(def.Params) == 0 {
		return
	}
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
		fmt.Fprintf(sb, "| `%s` | `%s` | %s | %s | %s |\n",
			name, p.Type, required, defVal, paramDesc)
	}
	sb.WriteString("\n")
}

// writeCommandContext writes the Context table.
func writeCommandContext(sb *strings.Builder, def *usercommands.CommandDef, store *i18n.Store, locale string) {
	if len(def.Context) == 0 {
		return
	}
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
		fmt.Fprintf(sb, "| `%s` | `%s` | %s | %s |\n", name, c.From, required, c.Env)
	}
	sb.WriteString("\n")
}

// writeCommandFiles writes the Files section.
func writeCommandFiles(sb *strings.Builder, def *usercommands.CommandDef, store *i18n.Store, locale string) {
	if len(def.Files) == 0 {
		return
	}
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
		fmt.Fprintf(sb, "### `%s` (%s)\n\n", id, attrs)
		if f.Env != "" {
			fmt.Fprintf(sb, "**Env:** `%s`\n\n", f.Env)
		}
		if f.Path != "" {
			fmt.Fprintf(sb, "**Path:** `%s`\n\n", f.Path)
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
					fmt.Fprintf(sb, "%d. path: `%s`\n", i+1, c.Path)
				}
			}
			sb.WriteString("\n")
		}
	}
}

// writeCommandEnv writes the Environment Variables table.
func writeCommandEnv(sb *strings.Builder, def *usercommands.CommandDef, store *i18n.Store, locale string) {
	if len(def.Env) == 0 {
		return
	}
	envHeader := store.T(locale, "docs.section.environment", "Environment Variables")
	sb.WriteString("## " + envHeader + "\n\n")
	sb.WriteString("| Name | Value |\n|---|---|\n")
	var keys []string
	for k := range def.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(sb, "| `%s` | `%s` |\n", k, def.Env[k])
	}
	sb.WriteString("\n")
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
	sb.WriteString("Reference for declarative commands defined in `workspace/commands/`.\n\n")

	if len(defs) == 0 {
		sb.WriteString("No commands defined.\n")
	} else {
		// Group by group.
		groups, byGroup := listCommandsByGroup(defs)

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
func genTopLevelIndex(outDir string) error {
	var sb strings.Builder
	sb.WriteString("# dwe Reference Documentation\n\n")
	sb.WriteString("Generated reference documentation for dwe.\n\n")
	sb.WriteString("## Sections\n\n")

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
