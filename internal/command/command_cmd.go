package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"devbox-cli/internal/commands"
	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
	"devbox-cli/internal/tpl"

	"github.com/spf13/cobra"
)

func newCommandCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "command",
		Short:        "List, inspect, and run devbox commands",
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
		Args:  cobra.MaximumNArgs(1),
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
				render.Stdout().Info("No commands found.")
				return nil
			}
			render.Stdout().WriteTree(nodes)
			return nil
		},
		SilenceUsage: true,
	}
	cmd.Flags().BoolVar(&showAll, "all", false, "Include private commands")
	return cmd
}

func newCommandInspectCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <id>",
		Short: "Show full command definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			reg, err := loadCommandRegistry(flags.configPath)
			if err != nil {
				return err
			}
			def, err := reg.Get(id)
			if err != nil {
				return err
			}
			printCommandInspect(render.Stdout(), def)
			return nil
		},
		SilenceUsage: true,
	}
}

func newCommandRunCmd(flags *rootFlags) *cobra.Command {
	var setFlags []string

	cmd := &cobra.Command{
		Use:   "run <id>",
		Short: "Run a devbox command",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			cfg, err := config.LoadConfig(flags.configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			reg, err := loadCommandRegistry(flags.configPath)
			if err != nil {
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
			params, err := commands.ResolveParams(def.Params, provided, cfg)
			if err != nil {
				return fmt.Errorf("resolving params: %w", err)
			}
			ctx, err := commands.ResolveContext(def.Context, cfg)
			if err != nil {
				return fmt.Errorf("resolving context: %w", err)
			}
			rctx := &tpl.RenderContext{
				Raw:     cfg.Raw,
				Params:  params,
				Context: ctx,
				Host:    tpl.CurrentHostInfo(),
			}
			projectRoot := filepath.Dir(flags.configPath)
			dockerCfg, err := config.LoadDockerConfig(projectRoot, cfg)
			if err != nil {
				return fmt.Errorf("loading docker config: %w", err)
			}
			runner, err := commands.NewRunner(def)
			if err != nil {
				return fmt.Errorf("creating runner: %w", err)
			}
			return runner.Run(commands.RunContext{
				Cmd:          def,
				Params:       params,
				Context:      ctx,
				Render:       rctx,
				Config:       cfg,
				DockerConfig: dockerCfg,
				Registry:     reg,
				ProjectRoot:  projectRoot,
				Stdout:       os.Stdout,
				Stderr:       os.Stderr,
			})
		},
		SilenceUsage: true,
	}
	cmd.Flags().StringArrayVar(&setFlags, "set", nil, "Set a param value (key=value)")
	return cmd
}

// loadCommandRegistry loads the command registry from devbox/commands/ relative
// to the config file. Returns an empty registry when the directory does not exist.
func loadCommandRegistry(configPath string) (*commands.Registry, error) {
	commandsDir := filepath.Join(filepath.Dir(configPath), "devbox", "commands")
	if _, statErr := os.Stat(commandsDir); errors.Is(statErr, os.ErrNotExist) {
		return commands.NewEmptyRegistry(), nil
	}
	reg, err := commands.LoadRegistry(commandsDir)
	if err != nil {
		return nil, fmt.Errorf("loading command registry: %w", err)
	}
	if err := reg.Validate(); err != nil {
		return nil, fmt.Errorf("command registry validation: %w", err)
	}
	return reg, nil
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
func buildTreeNodes(root *commands.GroupNode, groupFilter string, includePrivate bool) []*render.TreeNode {
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
func findGroupNode(node *commands.GroupNode, id string) *commands.GroupNode {
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
func groupNodeToChildren(gn *commands.GroupNode, includePrivate bool) []*render.TreeNode {
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
func groupNodeToSingleNode(gn *commands.GroupNode, includePrivate bool) *render.TreeNode {
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
func commandDefToTreeNode(cmd *commands.CommandDef) *render.TreeNode {
	var tags []string
	if cmd.Private {
		tags = append(tags, "private")
	}
	tags = append(tags, string(cmd.Type))
	return &render.TreeNode{
		Label: cmd.LocalName,
		Tags:  tags,
		Desc:  cmd.Description,
	}
}

// printCommandInspect writes a detailed view of a command definition.
func printCommandInspect(w *render.Writer, def *commands.CommandDef) {
	w.TableHeader(def.ID)
	w.Definition("type", string(def.Type), 2, "")
	if def.Description != "" {
		w.Definition("description", def.Description, 2, "")
	}
	if def.Private {
		w.Definition("private", "true", 2, "")
	}

	switch def.Type {
	case commands.CommandTypeCommand:
		if def.Run != "" {
			w.Definition("run", def.Run, 2, "")
		}
		if len(def.Argv) > 0 {
			w.Definition("argv", strings.Join(def.Argv, " "), 2, "")
		}
		if def.Cwd != "" {
			w.Definition("cwd", def.Cwd, 2, "")
		}
	case commands.CommandTypeServiceExec, commands.CommandTypeServiceRun:
		if def.Service != "" {
			w.Definition("service", def.Service, 2, "")
		}
		if def.Runner != nil && def.Runner.Service != "" {
			w.Definition("service (runner)", def.Runner.Service, 2, "")
		}
		if def.User != "" {
			w.Definition("user", string(def.User), 2, "")
		}
		if def.Workdir != "" {
			w.Definition("workdir", def.Workdir, 2, "")
		}
		if def.WorkdirFrom != "" {
			w.Definition("workdir_from", def.WorkdirFrom, 2, "")
		}
		if def.Mode != "" {
			w.Definition("mode", string(def.Mode), 2, "")
		}
		if def.Run != "" {
			w.Definition("run", def.Run, 2, "")
		}
		if len(def.Argv) > 0 {
			w.Definition("argv", strings.Join(def.Argv, " "), 2, "")
		}
	case commands.CommandTypeScript:
		if def.Script != nil {
			shell := def.Script.Shell
			if shell == "" {
				shell = "sh"
			}
			w.Definition("script.shell", shell, 2, "")
			if def.Script.Path != "" {
				w.Definition("script.path", def.Script.Path, 2, "")
			}
			if def.Script.Plan != "" {
				w.Definition("script.plan", def.Script.Plan, 2, "")
			}
			if def.Script.Run != "" {
				w.Definition("script.run", def.Script.Run, 2, "")
			}
			if def.Script.Cleanup != "" {
				w.Definition("script.cleanup", def.Script.Cleanup, 2, "")
			}
		}
	case commands.CommandTypeWorkflow:
		w.TableSubheader("Steps")
		for i, step := range def.Steps {
			if step.Confirm != "" {
				w.Definition(fmt.Sprintf("[%d] confirm", i), step.Confirm, 2, "")
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
				w.Definition(label, desc, 2, "")
			}
		}
	}

	if len(def.Params) > 0 {
		w.TableSubheader("Params")
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
			w.Definition(name, desc, 4, "")
		}
	}

	if len(def.Context) > 0 {
		w.TableSubheader("Context")
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
			w.Definition(name, desc, 4, "")
		}
	}

	if len(def.Env) > 0 {
		w.TableSubheader("Env")
		var keys []string
		for k := range def.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			w.Definition(k, def.Env[k], 4, "")
		}
	}

	w.TableHeader("")
}
