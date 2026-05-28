package command

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/ui"
	"devbox-cli/internal/core/ui/cmdbrowser"
	"devbox-cli/internal/core/usercommands"
	"devbox-cli/internal/shared/i18n"
	"devbox-cli/internal/shared/render"

	"github.com/spf13/cobra"
)

func newCommandListCmd(flags *cmdctx.RootFlags) *cobra.Command {
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
			reg, err := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
			if err != nil {
				return err
			}
			root := reg.Groups()
			nodes := buildTreeNodes(root, groupFilter, showAll, i18n.TranslatorOrNop(flags.I18n), flags.Locale)
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
func makeBrowserSelector(cfg *config.DevboxConfig, reg *usercommands.Registry, mode cmdbrowser.Mode, includePrivate bool, skipConfirmOut, forceFormOut *bool, translator i18n.Translator, locale string) selectCommandFn {
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
					printInspectAt(&buf, curDef, cfg, reg, width, translator, locale)
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
// devbox project is active.
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
			defs = reg.ListAll(arg)
		} else {
			defs = reg.List(arg)
		}
		if len(defs) == 0 {
			return "", fmt.Errorf("command %q not found", arg)
		}
		return selector(defs, selectorTitle(projectName, "Commands ("+arg+")"))
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
	return selector(defs, selectorTitle(projectName, "Commands"))
}

// selectorTitle composes the selector header from a fixed "Devbox" prefix,
// the project name (when set), and the base title, joined with middots. The
// "Devbox" prefix is always present so the TUI advertises which tool owns the
// window regardless of project context.
func selectorTitle(projectName, base string) string {
	parts := []string{"Devbox"}
	if projectName != "" {
		parts = append(parts, projectName)
	}
	parts = append(parts, base)
	return strings.Join(parts, " · ")
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
// are omitted when includePrivate is false.
func groupNodeToChildren(gn *usercommands.GroupNode, includePrivate bool, translator i18n.Translator, locale string) []*render.TreeNode {
	var nodes []*render.TreeNode
	for _, child := range gn.Children {
		childNode := groupNodeToSingleNode(child, includePrivate, translator, locale)
		if childNode != nil {
			nodes = append(nodes, childNode)
		}
	}
	for _, cmd := range gn.Commands {
		if !includePrivate && cmd.Private {
			continue
		}
		nodes = append(nodes, commandDefToTreeNode(cmd, translator, locale))
	}
	return nodes
}

// groupNodeToSingleNode converts a GroupNode into a single render.TreeNode.
// Returns nil when the group has no visible content (after private filtering).
func groupNodeToSingleNode(gn *usercommands.GroupNode, includePrivate bool, translator i18n.Translator, locale string) *render.TreeNode {
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
