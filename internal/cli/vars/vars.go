// Package vars implements the `dwe vars` command tree: inspect, read, edit, and
// map the values in a project's vars: sandbox (the single formalized home for
// free-form config values). Read subcommands (get / list / inspect) resolve
// against the merged 3-layer config; set writes comment-preserving overrides to
// workspace/local.yml.
package vars

import (
	"fmt"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/varsusage"

	"github.com/spf13/cobra"
)

// NewCmd builds the `dwe vars` command tree. Registered under
// groupConfiguration in cli/root.go, alongside the other config-mutation
// commands (service / render / validate / scaffold).
//
// With no positional arg the bare command opens the interactive TUI browser
// on a real terminal; in non-interactive / container / JSON mode (or when a
// namespace arg narrows the output) it falls back to `vars list`. The
// get / list / inspect subcommands are read-only; set writes local.yml
// overrides.
func NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "vars [namespace]",
		Aliases: []string{"var"},
		GroupID: groupID,
		Short:   "Inspect, read, and edit project vars",
		Long: `Work with the vars: sandbox — the single formalized home for free-form
project values.

Values live under vars: in workspace.yml / workspace/defaults.yml (author
defaults) and workspace/local.yml (developer overrides). They are referenced in
templates as ${vars.x} and structurally via from: / default_from: / when:.

Subcommands:
  list     enumerate vars.* leaves with their effective values
  get      print a single var's effective value (scalar or subtree)
  inspect  show per-layer values, origin file, and every static usage

Without a subcommand the leaves are listed (an optional namespace narrows the
output).`,
		Example: `  dwe vars
  dwe vars list
  dwe vars list vars.db
  dwe vars get vars.db.host`,
		Args:              cobra.MaximumNArgs(1),
		SilenceUsage:      true,
		ValidArgsFunction: namespaceCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			namespace := ""
			if len(args) > 0 {
				namespace = args[0]
			}
			// A namespace filter, JSON output, or any non-interactive context
			// (CI pipe, DWE_NONINTERACTIVE, the bridge daemon's container forks)
			// lists leaves. A bare invocation on a real terminal opens the TUI
			// browser. Mirrors `dwe commands`' interactive/non-interactive split.
			if namespace != "" || flags.Output == "json" ||
				!isInteractive(cmd.InOrStdin()) || cmdctx.NonInteractiveEnv() {
				return runVarsList(cmd, flags, namespace)
			}
			return runVarsBrowser(cmd, flags)
		},
	}

	cmd.AddCommand(newVarsGetCmd(flags))
	cmd.AddCommand(newVarsListCmd(flags))
	cmd.AddCommand(newVarsInspectCmd(flags))
	cmd.AddCommand(newVarsSetCmd(flags))
	return cmd
}

// loadConfigForVars loads the merged project config, wrapping load failures in
// the typed project_invalid_config envelope (matching the other config
// commands).
func loadConfigForVars(flags *cmdctx.RootFlags) (*config.DweConfig, error) {
	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return nil, cmdctx.ErrWrap("project_invalid_config", err)
	}
	return cfg, nil
}

// leafCompletion returns a ValidArgsFunction that completes vars.* leaf paths.
// It is the shared completion for the <var> arg of get / inspect / set. Errors
// yield empty completions silently (completion never surfaces errors).
func leafCompletion(flags *cmdctx.RootFlags) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		configPath, _, err := cmdctx.CompletionConfigPath(flags, cmd)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return varsusage.EnumerateVars(cfg), cobra.ShellCompDirectiveNoFileComp
	}
}

// namespaceCompletion returns a ValidArgsFunction completing both leaf paths
// and their interior namespaces (so `dwe vars list vars.db<TAB>` works). Used
// for the bare `vars` / `vars list` namespace arg.
func namespaceCompletion(flags *cmdctx.RootFlags) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		configPath, _, err := cmdctx.CompletionConfigPath(flags, cmd)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return namespaceCandidates(varsusage.EnumerateVars(cfg)), cobra.ShellCompDirectiveNoFileComp
	}
}

// namespaceCandidates expands a sorted leaf list into leaves plus every
// interior namespace prefix (deduplicated, order-preserving) so completion
// offers both `vars.db` and `vars.db.host`.
func namespaceCandidates(leaves []string) []string {
	seen := make(map[string]struct{}, len(leaves)*2)
	var out []string
	add := func(s string) {
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, leaf := range leaves {
		parts := strings.Split(leaf, ".")
		// Skip the bare "vars" head; offer every interior namespace and the leaf.
		for i := 2; i < len(parts); i++ {
			add(strings.Join(parts[:i], "."))
		}
		add(leaf)
	}
	return out
}

// notFoundError builds the typed not-found error for a missing var path.
func notFoundError(path string) error {
	return cmdctx.Err("vars_not_found", fmt.Sprintf("var %q not found", path)).WithDetail("var", path)
}

// isVarsPath reports whether a dot-path lives in the vars.* sandbox (head
// segment == "vars"). The read subcommands (get / inspect) confine to it so a
// non-vars path (e.g. project.name, __configPath, services.<x>) cannot be read
// through the vars surface — which matters because `vars` is reachable from a
// container (bridgeAllowedTopLevel) and would otherwise leak arbitrary host
// project config across the bridge trust boundary. `set` has its own stricter
// confinement (validateVarsSetPath). The bare `vars` namespace is allowed so
// `get vars` can print the whole subtree.
func isVarsPath(path string) bool {
	head, _, _ := strings.Cut(path, ".")
	return head == varsusage.VarsPrefix
}
