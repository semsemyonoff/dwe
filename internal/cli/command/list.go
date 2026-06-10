package command

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/cmdbrowser"
	uirender "github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
	"github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
)

// commandsListJSON is the top-level DTO for `commands list --output json`.
type commandsListJSON struct {
	Commands []commandEntryJSON `json:"commands"`
}

// commandEntryJSON is a single entry in the flat JSON command list.
type commandEntryJSON struct {
	ID      string           `json:"id"`
	Group   string           `json:"group,omitempty"`
	Title   string           `json:"title"`
	Type    string           `json:"type"`
	Private bool             `json:"private,omitempty"`
	Params  []paramEntryJSON `json:"params,omitempty"`
}

// paramEntryJSON represents a single parameter definition in JSON output.
// Used by both the list and inspect JSON paths.
type paramEntryJSON struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required,omitempty"`
	Default     string `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

// buildCommandsListJSON builds a flat list of commands from the registry.
// When showAll is false, private commands are excluded. Hidden commands
// (resolved via reg.ApplyVisibility) are always excluded — they represent
// a runtime "command does not exist" state. --all does not surface them
// because --all is for developer-intent Private, not runtime hide.
func buildCommandsListJSON(reg *usercommands.Registry, groupFilter string, showAll bool, translator i18n.Translator, locale string) commandsListJSON {
	var defs []*usercommands.CommandDef
	if showAll {
		defs = reg.ListAll(groupFilter)
	} else {
		defs = reg.List(groupFilter)
	}
	entries := make([]commandEntryJSON, 0, len(defs))
	for _, def := range defs {
		if def.Hidden {
			continue
		}
		entries = append(entries, commandDefToEntryJSON(def, translator, locale))
	}
	return commandsListJSON{Commands: entries}
}

// buildParamEntriesJSON converts a CommandDef's params map to a sorted slice
// of paramEntryJSON. Returns nil when the params map is empty. Shared by both
// the list and inspect JSON serialization paths.
func buildParamEntriesJSON(def *usercommands.CommandDef, translator i18n.Translator, locale string) []paramEntryJSON {
	if len(def.Params) == 0 {
		return nil
	}
	names := make([]string, 0, len(def.Params))
	for name := range def.Params {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]paramEntryJSON, 0, len(names))
	for _, name := range names {
		p := def.Params[name]
		entries = append(entries, paramEntryJSON{
			Name:        name,
			Type:        string(p.Type),
			Required:    p.Required,
			Default:     p.Default,
			Description: translator.ParamDescription(locale, def.ID, name, p.Description),
		})
	}
	return entries
}

// commandDefToEntryJSON converts a single CommandDef to its JSON list entry.
func commandDefToEntryJSON(def *usercommands.CommandDef, translator i18n.Translator, locale string) commandEntryJSON {
	return commandEntryJSON{
		ID:      def.ID,
		Group:   def.Group,
		Title:   def.LocalName,
		Type:    string(def.Type),
		Private: def.Private,
		Params:  buildParamEntriesJSON(def, translator, locale),
	}
}

func newCommandListCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var showAll bool

	cmd := &cobra.Command{
		Use:   "list [group]",
		Short: "List available commands",
		Long: `List all available declarative commands from workspace/commands/.

An optional group filter narrows the output to a specific command group (e.g. 'db', 'services.main').
Use --all to include private commands.`,
		Example: `  dwe commands list
  dwe commands list db
  dwe commands list --all`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			groupFilter := ""
			if len(args) > 0 {
				groupFilter = args[0]
			}
			reg, err := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
			if err != nil {
				return cmdctx.ErrWrap("command_registry_invalid", err)
			}
			// Visibility is best-effort here: cfg load errors are tolerated so
			// a broken project can still list its commands. ApplyVisibility
			// is fail-open — per-expression failures log + treat as visible.
			cfg, _ := config.LoadConfig(flags.ConfigPath)
			_ = reg.ApplyVisibility(cfg, flags.ProjectRoot())
			return writeCommandsList(cmd, flags, reg, groupFilter, showAll)
		},
		SilenceUsage: true,
	}
	cmd.Flags().BoolVar(&showAll, "all", false, "Include private commands")
	return cmd
}

// writeCommandsList renders the command list for reg — the JSON DTO in JSON
// mode, the styled tree otherwise. Shared by `commands list` and the
// non-interactive fallback of the bare `dwe commands` browser. Callers are
// responsible for reg.ApplyVisibility.
func writeCommandsList(cmd *cobra.Command, flags *cmdctx.RootFlags, reg *usercommands.Registry, groupFilter string, showAll bool) error {
	translator := i18n.TranslatorOrNop(flags.I18n)
	if flags.Output == "json" {
		data := buildCommandsListJSON(reg, groupFilter, showAll, translator, flags.Locale)
		return cmdctx.WriteJSON(flags, cmd, data)
	}
	nodes := buildTreeNodes(reg.Groups(), groupFilter, showAll, translator, flags.Locale)
	if len(nodes) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No commands found.")
		return nil
	}
	printTreeNodes(cmd.OutOrStdout(), nodes)
	return nil
}

// selectCommandFn is the function signature for interactive command selection.
// It receives a slice of CommandDefs and a display title, and returns the chosen ID.
type selectCommandFn func(defs []*usercommands.CommandDef, title string) (string, error)

// makeBrowserSelector returns a selectCommandFn that drives the cmdbrowser
// TUI. The returned closure captures cfg (for resolving ui.commands.*
// defaults via the nil-safe accessors), mode, the includePrivate flag, and
// (run-site only) pointers to bools that receive Result.SkipConfirm and
// Result.ForceParamForm.
//
// For ModeInspect the skipConfirmOut / forceFormOut pointers are unused
// (their key bindings are disabled in inspect mode); pass nil.
func makeBrowserSelector(cfg *config.DweConfig, reg *usercommands.Registry, mode cmdbrowser.Mode, includePrivate bool, skipConfirmOut, forceFormOut *bool, translator i18n.Translator, locale, baseDir string) selectCommandFn {
	return func(defs []*usercommands.CommandDef, title string) (string, error) {
		items := make([]cmdbrowser.Item, len(defs))
		for i, d := range defs {
			curDef := d
			items[i] = cmdbrowser.Item{
				ID:          d.ID,
				Description: translator.CommandDescription(locale, d.ID, d.Description),
				Type:        string(d.Type),
				Private:     d.Private,
				ParamCount:  len(d.Params),
				Inspect: func(width int) string {
					var buf bytes.Buffer
					printInspectAt(&buf, curDef, cfg, reg, width, translator, locale, baseDir)
					return buf.String()
				},
			}
		}
		opts := cmdbrowser.Options{
			DefaultExpandedDepth: config.UICommandsDefaultDepth(cfg),
			AutoCollapseEmpty:    config.UICommandsAutoCollapseEmpty(cfg),
			ShowTypeBadges:       config.UICommandsShowTypeBadges(cfg),
			IncludePrivate:       includePrivate,
			Mode:                 mode,
		}
		res, err := cmdbrowser.Run(title, items, opts)
		if err != nil {
			return "", err
		}
		if skipConfirmOut != nil && res.SkipConfirm {
			*skipConfirmOut = true
		}
		if forceFormOut != nil && res.ForceParamForm {
			*forceFormOut = true
		}
		if res.Idx < 0 || res.Idx >= len(defs) {
			return "", fmt.Errorf("cmdbrowser: result index %d out of range [0, %d)", res.Idx, len(defs))
		}
		return defs[res.Idx].ID, nil
	}
}

// resolveCommandID determines the target command ID from optional positional args.
//
//   - No args: calls selector with all public (or all when includePrivate is true) usercommands.
//   - One arg that is a full command ID (registry.Get succeeds): returns it directly.
//   - One arg that is a group prefix (registry.List returns results): calls selector
//     filtered to that group.
//   - One arg that is neither: returns an error.
//
// projectName, when non-empty, is prepended to the selector title as
// "<project> · Select command [...]" so the TUI header makes clear which
// dwe project is active.
func resolveCommandID(reg *usercommands.Registry, args []string, includePrivate bool, projectName string, selector selectCommandFn) (string, error) {
	if len(args) == 1 {
		arg := args[0]
		// Exact command ID — use directly without selector.
		if _, err := reg.Get(arg); err == nil {
			return arg, nil
		}
		// Try as a group prefix.
		var defs []*usercommands.CommandDef
		if includePrivate {
			defs = reg.ListAllIncludingHidden(arg)
		} else {
			defs = reg.List(arg)
		}
		if len(defs) == 0 {
			return "", fmt.Errorf("command %q not found", arg)
		}
		return selector(defs, uirender.BrandedSelectorTitle(projectName, "Commands ("+arg+")"))
	}
	// No arg — show full list.
	var defs []*usercommands.CommandDef
	if includePrivate {
		defs = reg.ListAllIncludingHidden("")
	} else {
		defs = reg.List("")
	}
	if len(defs) == 0 {
		return "", fmt.Errorf("no commands available")
	}
	return selector(defs, uirender.BrandedSelectorTitle(projectName, "Commands"))
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
func buildTreeNodes(root *usercommands.GroupNode, groupFilter string, includePrivate bool, translator i18n.Translator, locale string) []*render.TreeNode {
	if groupFilter != "" {
		target := findGroupNode(root, groupFilter)
		if target == nil {
			return nil
		}
		return groupNodeToChildren(target, includePrivate, translator, locale)
	}
	return groupNodeToChildren(root, includePrivate, translator, locale)
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
// are omitted when includePrivate is false. Hidden groups and hidden commands
// are always omitted (hide is a runtime condition, --all does not bypass it).
func groupNodeToChildren(gn *usercommands.GroupNode, includePrivate bool, translator i18n.Translator, locale string) []*render.TreeNode {
	var nodes []*render.TreeNode
	for _, child := range gn.Children {
		if child.Hidden {
			continue
		}
		childNode := groupNodeToSingleNode(child, includePrivate, translator, locale)
		if childNode != nil {
			nodes = append(nodes, childNode)
		}
	}
	for _, cmd := range gn.Commands {
		if cmd.Hidden {
			continue
		}
		if !includePrivate && cmd.Private {
			continue
		}
		nodes = append(nodes, commandDefToTreeNode(cmd, translator, locale))
	}
	return nodes
}

// groupNodeToSingleNode converts a GroupNode into a single render.TreeNode.
// Returns nil when the group is hidden or has no visible content.
func groupNodeToSingleNode(gn *usercommands.GroupNode, includePrivate bool, translator i18n.Translator, locale string) *render.TreeNode {
	if gn.Hidden {
		return nil
	}
	children := groupNodeToChildren(gn, includePrivate, translator, locale)
	if !includePrivate && len(children) == 0 {
		return nil
	}
	desc := translator.GroupDescription(locale, gn.ID, gn.Meta.Description)
	node := &render.TreeNode{
		Label:    translator.GroupTitle(locale, gn.ID, gn.Name),
		Desc:     desc,
		Children: children,
	}
	return node
}

// commandDefToTreeNode converts a CommandDef into a leaf render.TreeNode.
func commandDefToTreeNode(cmd *usercommands.CommandDef, translator i18n.Translator, locale string) *render.TreeNode {
	var tags []string
	if cmd.Private {
		tags = append(tags, "private")
	}
	tags = append(tags, string(cmd.Type))
	desc := translator.CommandDescription(locale, cmd.ID, cmd.Description)
	return &render.TreeNode{
		Label: cmd.ID,
		Tags:  tags,
		Desc:  desc,
	}
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
		sb.WriteString(styles.StyleGroup(node.Label))
	} else {
		sb.WriteString(styles.StyleKey(node.Label))
		if len(node.Tags) > 0 {
			sb.WriteString("  ")
			sb.WriteString(styles.StyleMuted("[" + strings.Join(node.Tags, ", ") + "]"))
		}
	}

	if node.Desc != "" {
		sb.WriteString("  ")
		sb.WriteString(styles.StyleMuted("—"))
		sb.WriteString(" ")
		sb.WriteString(node.Desc)
	}

	_, _ = fmt.Fprintln(w, sb.String())

	for _, child := range node.Children {
		printTreeNode(w, child, depth+1)
	}
}
