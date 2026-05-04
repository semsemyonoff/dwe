package command

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/tpl"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands"

	"github.com/spf13/cobra"
)

func newCommandCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "commands",
		Short: "List, inspect, and run devbox commands",
		Long: `Manage declarative commands defined in devbox/commands/.

Commands are YAML-defined operations organized into groups (e.g. db, app, services.main).
They can be shell commands, scripts, service exec/run operations, or multi-step workflows.`,
		Example: `  devbox commands list
  devbox commands list db
  devbox commands inspect db.up
  devbox commands run db.up`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newCommandListCmd(flags))
	cmd.AddCommand(newCommandInspectCmd(flags))
	cmd.AddCommand(newCommandRunCmd(flags))
	return cmd
}

func newCommandListCmd(flags *rootFlags) *cobra.Command {
	var showAll bool

	cmd := &cobra.Command{
		Use:   "list [group]",
		Short: "List available commands",
		Long: `List all available declarative commands from devbox/commands/.

An optional group filter narrows the output to a specific command group (e.g. 'db', 'services.main').
Use --all to include private commands.`,
		Example: `  devbox commands list
  devbox commands list db
  devbox commands list --all`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			groupFilter := ""
			if len(args) > 0 {
				groupFilter = args[0]
			}
			reg, err := loadCommandRegistry(flags.configPath)
			if err != nil {
				return err
			}
			root := reg.Groups()
			nodes := buildTreeNodes(root, groupFilter, showAll)
			if len(nodes) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No commands found.")
				return nil
			}
			printTreeNodes(cmd.OutOrStdout(), nodes)
			return nil
		},
		SilenceUsage: true,
	}
	cmd.Flags().BoolVar(&showAll, "all", false, "Include private commands")
	return cmd
}

func newCommandInspectCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect [id|group]",
		Short: "Show full command definition",
		Long: `Show the full definition of a declarative command by its dot-separated ID.

Displays type, run/argv, params, context variables, env, and workflow steps.

When called without an argument, an interactive selector lists all commands.
When called with a group prefix (e.g. 'services.main'), the selector is
filtered to that group.  When called with a full command ID, it inspects
directly without showing a selector.`,
		Example: `  devbox commands inspect
  devbox commands inspect services.main
  devbox commands inspect db.up
  devbox commands inspect services.main.migrate`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := loadCommandRegistry(flags.configPath)
			if err != nil {
				return err
			}
			selector := selectCommandFn(defaultSelectCommand)
			if !ui.IsInteractiveFn(cmd.InOrStdin()) {
				selector = func(_ []*usercommands.CommandDef, _ string) (string, error) {
					return "", fmt.Errorf("no exact command ID given; pass a full command ID or run in an interactive terminal")
				}
			}
			id, err := resolveCommandID(reg, args, true, selector)
			if err != nil {
				if errors.Is(err, ui.ErrCancelled) {
					return nil
				}
				return err
			}
			def, err := reg.Get(id)
			if err != nil {
				return err
			}
			printCommandInspect(cmd.OutOrStdout(), def)
			return nil
		},
		SilenceUsage:      true,
		ValidArgsFunction: registryIDCompletion(flags, true),
	}
	return cmd
}

func newCommandRunCmd(flags *rootFlags) *cobra.Command {
	var setFlags []string
	var skipConfirm bool

	cmd := &cobra.Command{
		Use:   "run [id|group]",
		Short: "Run a devbox command",
		Long: `Execute a declarative command by its dot-separated ID.

Use --set key=value to override declared params at runtime.
Use --yes to skip confirmation prompts (intended for non-interactive use).
Private commands cannot be run directly.

When called without an argument, an interactive selector lists all public
commands.  When called with a group prefix (e.g. 'services.main'), the
selector is filtered to that group.  When called with a full command ID,
it runs directly without showing a selector.`,
		Example: `  devbox commands run
  devbox commands run services.main
  devbox commands run db.up
  devbox commands run services.main.migrate --set db=mydb
  devbox commands run db.drop --set database=mydb --yes`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: registryIDCompletion(flags, false),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			reg, err := loadCommandRegistry(flags.configPath)
			if err != nil {
				return err
			}
			selector := selectCommandFn(defaultSelectCommand)
			if !ui.IsInteractiveFn(cmd.InOrStdin()) {
				selector = func(_ []*usercommands.CommandDef, _ string) (string, error) {
					return "", fmt.Errorf("no exact command ID given; pass a full command ID or run in an interactive terminal")
				}
			}
			id, err := resolveCommandID(reg, args, false, selector)
			if err != nil {
				if errors.Is(err, ui.ErrCancelled) {
					return nil
				}
				return err
			}
			def, err := reg.Get(id)
			if err != nil {
				return err
			}
			if def.Private {
				return fmt.Errorf("command %q is private and cannot be run directly", id)
			}
			provided, err := parseSetFlags(setFlags)
			if err != nil {
				return err
			}
			params, err := usercommands.ResolveParams(def.Params, provided, cfg)
			if err != nil {
				return fmt.Errorf("resolving params: %w", err)
			}
			ctx, err := usercommands.ResolveContext(def.Context, cfg)
			if err != nil {
				return fmt.Errorf("resolving context: %w", err)
			}
			rctx := &tpl.RenderContext{
				Raw:     cfg.Raw,
				Params:  params,
				Context: ctx,
				Host:    tpl.CurrentHostInfo(),
			}
			projectRoot := flags.ProjectRoot()
			dockerCfg, err := config.LoadDockerConfig(projectRoot, cfg)
			if err != nil {
				return fmt.Errorf("loading docker config: %w", err)
			}

			// Determine if we should skip confirmation:
			// 1. --yes flag takes precedence
			// 2. inherited DEVBOX_NONINTERACTIVE env var
			shouldSkip := skipConfirm
			if !shouldSkip && (os.Getenv("DEVBOX_NONINTERACTIVE") == "1" || os.Getenv("DEVBOX_NONINTERACTIVE") == "true") {
				shouldSkip = true
			}

			if err := usercommands.RunCommand(usercommands.RunContext{
				Cmd:            def,
				Params:         params,
				Context:        ctx,
				Render:         rctx,
				Config:         cfg,
				DockerConfig:   dockerCfg,
				Registry:       reg,
				ProjectRoot:    projectRoot,
				Stdout:         os.Stdout,
				Stderr:         os.Stderr,
				Stdin:          os.Stdin,
				SkipConfirm:    shouldSkip,
				NonInteractive: shouldSkip,
			}); err != nil {
				return fmt.Errorf("running command %q: %w", id, err)
			}
			return nil
		},
		SilenceUsage: true,
	}
	cmd.Flags().StringArrayVar(&setFlags, "set", nil, "Set a param value (key=value)")
	cmd.Flags().BoolVarP(&skipConfirm, "yes", "y", false, "Skip confirmation prompts; intended for non-interactive use such as scripts and nested command runs")
	return cmd
}

// loadCommandRegistry loads the command registry from devbox/commands/ relative
// to the config file. Returns an empty registry when the directory does not exist.
func loadCommandRegistry(configPath string) (*usercommands.Registry, error) {
	return usercommands.LoadRegistryFromConfigPath(configPath)
}

// selectCommandFn is the function signature for interactive command selection.
// It receives a slice of CommandDefs and a display title, and returns the chosen ID.
type selectCommandFn func(defs []*usercommands.CommandDef, title string) (string, error)

// defaultSelectCommand shows an interactive selector via ui.RunSelector.
func defaultSelectCommand(defs []*usercommands.CommandDef, title string) (string, error) {
	items := make([]ui.SelectorItem, len(defs))
	for i, d := range defs {
		items[i] = ui.SelectorItem{
			Label:       d.ID,
			Description: d.Description,
		}
	}
	idx, err := ui.RunSelector(title, items)
	if err != nil {
		return "", err
	}
	return defs[idx].ID, nil
}

// resolveCommandID determines the target command ID from optional positional args.
//
//   - No args: calls selector with all public (or all when includePrivate is true) usercommands.
//   - One arg that is a full command ID (registry.Get succeeds): returns it directly.
//   - One arg that is a group prefix (registry.List returns results): calls selector
//     filtered to that group.
//   - One arg that is neither: returns an error.
func resolveCommandID(reg *usercommands.Registry, args []string, includePrivate bool, selector selectCommandFn) (string, error) {
	if len(args) == 1 {
		arg := args[0]
		// Exact command ID — use directly without selector.
		if _, err := reg.Get(arg); err == nil {
			return arg, nil
		}
		// Try as a group prefix.
		var defs []*usercommands.CommandDef
		if includePrivate {
			defs = reg.ListAll(arg)
		} else {
			defs = reg.List(arg)
		}
		if len(defs) == 0 {
			return "", fmt.Errorf("command %q not found", arg)
		}
		return selector(defs, "Select command ("+arg+")")
	}
	// No arg — show full list.
	var defs []*usercommands.CommandDef
	if includePrivate {
		defs = reg.ListAll("")
	} else {
		defs = reg.List("")
	}
	if len(defs) == 0 {
		return "", fmt.Errorf("no commands available")
	}
	return selector(defs, "Select command")
}

// parseSetFlags parses --set key=value flags into a map.
func parseSetFlags(flags []string) (map[string]string, error) {
	result := make(map[string]string, len(flags))
	for _, f := range flags {
		k, v, found := strings.Cut(f, "=")
		if !found {
			return nil, fmt.Errorf("--set %q: expected key=value format", f)
		}
		if k == "" {
			return nil, fmt.Errorf("--set %q: key must not be empty", f)
		}
		result[k] = v
	}
	return result, nil
}

// buildTreeNodes converts a GroupNode tree to render.TreeNode slices.
// When groupFilter is non-empty, only the matching sub-tree is rendered.
// Private commands are excluded when includePrivate is false.
func buildTreeNodes(root *usercommands.GroupNode, groupFilter string, includePrivate bool) []*render.TreeNode {
	if groupFilter != "" {
		target := findGroupNode(root, groupFilter)
		if target == nil {
			return nil
		}
		return groupNodeToChildren(target, includePrivate)
	}
	return groupNodeToChildren(root, includePrivate)
}

// findGroupNode searches the tree for a node with the given dot-separated ID.
func findGroupNode(node *usercommands.GroupNode, id string) *usercommands.GroupNode {
	if node.ID == id {
		return node
	}
	for _, child := range node.Children {
		if found := findGroupNode(child, id); found != nil {
			return found
		}
	}
	return nil
}

// groupNodeToChildren converts a GroupNode's contents into render.TreeNode slices,
// adding sub-groups and commands as children. Sub-groups without visible content
// are omitted when includePrivate is false.
func groupNodeToChildren(gn *usercommands.GroupNode, includePrivate bool) []*render.TreeNode {
	var nodes []*render.TreeNode
	for _, child := range gn.Children {
		childNode := groupNodeToSingleNode(child, includePrivate)
		if childNode != nil {
			nodes = append(nodes, childNode)
		}
	}
	for _, cmd := range gn.Commands {
		if !includePrivate && cmd.Private {
			continue
		}
		nodes = append(nodes, commandDefToTreeNode(cmd))
	}
	return nodes
}

// groupNodeToSingleNode converts a GroupNode into a single render.TreeNode.
// Returns nil when the group has no visible content (after private filtering).
func groupNodeToSingleNode(gn *usercommands.GroupNode, includePrivate bool) *render.TreeNode {
	children := groupNodeToChildren(gn, includePrivate)
	if !includePrivate && len(children) == 0 {
		return nil
	}
	node := &render.TreeNode{
		Label:    gn.Name,
		Desc:     gn.Meta.Description,
		Children: children,
	}
	return node
}

// commandDefToTreeNode converts a CommandDef into a leaf render.TreeNode.
func commandDefToTreeNode(cmd *usercommands.CommandDef) *render.TreeNode {
	var tags []string
	if cmd.Private {
		tags = append(tags, "private")
	}
	tags = append(tags, string(cmd.Type))
	return &render.TreeNode{
		Label: cmd.ID,
		Tags:  tags,
		Desc:  cmd.Description,
	}
}

// registryIDCompletion returns a ValidArgsFunction that completes command IDs
// from the registry. When includePrivate is true, private command IDs are also
// returned (useful for `inspect`). When false, only public IDs are returned
// (useful for `run`).
func registryIDCompletion(flags *rootFlags, includePrivate bool) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// Only complete the first positional argument.
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		reg, err := loadCommandRegistry(flags.configPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var defs []*usercommands.CommandDef
		if includePrivate {
			defs = reg.ListAll("")
		} else {
			defs = reg.List("")
		}
		completions := make([]string, 0, len(defs)+1)
		if !includePrivate && len(defs) > 0 {
			// Active Help: hint for run subcommand.
			completions = cobra.AppendActiveHelp(completions, "Use 'devbox commands inspect <id>' to see command details")
		}
		for _, d := range defs {
			entry := d.ID
			if d.Description != "" {
				entry = cobra.CompletionWithDesc(d.ID, d.Description)
			}
			completions = append(completions, entry)
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
}

// printCommandInspect writes a detailed view of a command definition using Lipgloss styles.
func printCommandInspect(w io.Writer, def *usercommands.CommandDef) {
	def2 := func(name, value string, indent int) {
		_, _ = fmt.Fprintln(w, ui.RenderDefinition(name, value, indent, ""))
	}
	sub := func(title string) {
		_, _ = fmt.Fprintln(w, ui.RenderSubheader("  "+title))
	}

	_, _ = fmt.Fprintln(w, ui.RenderSectionTitle(def.ID))
	def2("type", string(def.Type), 2)
	if def.Description != "" {
		def2("description", def.Description, 2)
	}
	if def.Private {
		def2("private", "true", 2)
	}
	if def.Confirmation {
		def2("confirmation", "true", 2)
		def2("confirmation_text", def.EffectiveConfirmationText(), 2)
	}
	if def.Messages.Success != "" || def.Messages.Error != "" {
		sub("Messages")
		if def.Messages.Success != "" {
			def2("success", def.Messages.Success, 4)
		}
		if def.Messages.Error != "" {
			def2("error", def.Messages.Error, 4)
		}
	}

	switch def.Type {
	case usercommands.CommandTypeCommand:
		if def.Run != "" {
			def2("run", def.Run, 2)
		}
		if len(def.Argv) > 0 {
			def2("argv", strings.Join(def.Argv, " "), 2)
		}
		if def.Workdir != "" {
			def2("workdir", def.Workdir, 2)
		}
	case usercommands.CommandTypeServiceExec, usercommands.CommandTypeServiceRun:
		if def.Service != "" {
			def2("service", def.Service, 2)
		}
		if def.Runner != nil && def.Runner.Service != "" {
			def2("service (runner)", def.Runner.Service, 2)
		}
		if def.User != "" {
			def2("user", string(def.User), 2)
		}
		if def.Workdir != "" {
			def2("workdir", def.Workdir, 2)
		}
		if def.WorkdirFrom != "" {
			def2("workdir_from", def.WorkdirFrom, 2)
		}
		if def.Mode != "" {
			def2("mode", string(def.Mode), 2)
		}
		if len(def.ComposeArgs) > 0 {
			def2("compose_args", strings.Join(def.ComposeArgs, " "), 2)
		}
		if def.Run != "" {
			def2("run", def.Run, 2)
		}
		if len(def.Argv) > 0 {
			def2("argv", strings.Join(def.Argv, " "), 2)
		}
	case usercommands.CommandTypeScript:
		if def.Script != nil {
			shell := def.Script.Shell
			if shell == "" {
				shell = "sh"
			}
			def2("script.shell", shell, 2)
			if def.Script.Path != "" {
				def2("script.path", def.Script.Path, 2)
			}
			if def.Script.Plan != "" {
				def2("script.plan", def.Script.Plan, 2)
			}
			if def.Script.Run != "" {
				def2("script.run", def.Script.Run, 2)
			}
			if def.Script.Cleanup != "" {
				def2("script.cleanup", def.Script.Cleanup, 2)
			}
		}
		if def.Workdir != "" {
			def2("workdir", def.Workdir, 2)
		}
	case usercommands.CommandTypeWorkflow:
		sub("Steps")
		for i, step := range def.Steps {
			if step.Confirm != "" {
				def2(fmt.Sprintf("[%d] confirm", i), step.Confirm, 4)
			} else {
				label := fmt.Sprintf("[%d]", i)
				desc := step.Command
				if len(step.With) > 0 {
					var pairs []string
					for k, v := range step.With {
						pairs = append(pairs, k+"="+v)
					}
					sort.Strings(pairs)
					desc += "  with: " + strings.Join(pairs, ", ")
				}
				if step.When != "" {
					desc += "  when: " + step.When
				}
				if step.ContinueOnError {
					desc += "  (continue_on_error)"
				}
				def2(label, desc, 4)
			}
		}
	}

	if len(def.Params) > 0 {
		sub("Params")
		var names []string
		for name := range def.Params {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			p := def.Params[name]
			desc := string(p.Type)
			if p.Description != "" {
				desc = p.Description + " (" + string(p.Type) + ")"
			}
			if p.Required {
				desc += " [required]"
			}
			if p.Default != "" {
				desc += " [default: " + p.Default + "]"
			}
			if p.DefaultFrom != "" {
				desc += " [default_from: " + p.DefaultFrom + "]"
			}
			def2(name, desc, 4)
		}
	}

	if len(def.Context) > 0 {
		sub("Context")
		var names []string
		for name := range def.Context {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			c := def.Context[name]
			desc := "from: " + c.From
			if c.Required {
				desc += " [required]"
			}
			if c.Env != "" {
				desc += " [env: " + c.Env + "]"
			}
			def2(name, desc, 4)
		}
	}

	if len(def.Env) > 0 {
		sub("Env")
		var keys []string
		for k := range def.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			def2(k, def.Env[k], 4)
		}
	}

	if len(def.Files) > 0 {
		sub("Files")
		var fids []string
		for fid := range def.Files {
			fids = append(fids, fid)
		}
		sort.Strings(fids)
		for _, fid := range fids {
			f := def.Files[fid]
			desc := string(f.Access)
			if f.Path != "" {
				desc += "  path: " + f.Path
			} else if len(f.Candidates) > 0 {
				desc += fmt.Sprintf("  candidates: %d", len(f.Candidates))
			}
			if f.Env != "" {
				desc += "  env: " + f.Env
			}
			var flags []string
			if f.Required {
				flags = append(flags, "required")
			}
			if f.Mkdir {
				flags = append(flags, "mkdir")
			}
			if f.Overwrite {
				flags = append(flags, "overwrite")
			}
			if f.OnError != "" {
				flags = append(flags, "on_error: "+string(f.OnError))
			}
			if len(flags) > 0 {
				desc += "  [" + strings.Join(flags, ", ") + "]"
			}
			def2(fid, desc, 4)
		}
	}

	_, _ = fmt.Fprintln(w, ui.RenderSectionTitle(""))
}

// printTreeNodes renders a flat list of tree nodes to w using Lipgloss styles.
func printTreeNodes(w io.Writer, nodes []*render.TreeNode) {
	for _, node := range nodes {
		printTreeNode(w, node, 0)
	}
}

// printTreeNode renders a single tree node and its children recursively.
// Group nodes (those with children) use the group/section style; leaf nodes use the key style.
func printTreeNode(w io.Writer, node *render.TreeNode, depth int) {
	indent := strings.Repeat("  ", depth)
	var sb strings.Builder
	sb.WriteString(indent)

	if len(node.Children) > 0 {
		sb.WriteString(ui.StyleGroup(node.Label))
	} else {
		sb.WriteString(ui.StyleKey(node.Label))
		if len(node.Tags) > 0 {
			sb.WriteString("  ")
			sb.WriteString(ui.StyleMuted("[" + strings.Join(node.Tags, ", ") + "]"))
		}
	}

	if node.Desc != "" {
		sb.WriteString("  ")
		sb.WriteString(ui.StyleMuted("—"))
		sb.WriteString(" ")
		sb.WriteString(node.Desc)
	}

	_, _ = fmt.Fprintln(w, sb.String())

	for _, child := range node.Children {
		printTreeNode(w, child, depth+1)
	}
}
