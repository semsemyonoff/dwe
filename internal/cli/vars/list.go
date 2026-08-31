package vars

import (
	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/varsusage"
	uirender "github.com/semsemyonoff/dwe/internal/core/ui/render"

	"github.com/spf13/cobra"
)

// varsListJSON is the JSON shape for `dwe vars list --output json`.
type varsListJSON struct {
	Vars []varListEntryJSON `json:"vars"`
}

// varListEntryJSON is one leaf row: its dot-path, effective value, and the
// layer badge naming where the value originates ("local" / "default" / "").
type varListEntryJSON struct {
	Path  string `json:"path"`
	Value any    `json:"value"`
	Layer string `json:"layer"`
}

func newVarsListCmd(flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [namespace]",
		Short: "List vars.* leaves and their effective values",
		Long: `Enumerate every leaf under vars: with its effective value and the layer
that supplies it (local override vs author default).

An optional namespace narrows the output to a sub-tree (e.g. db); the vars.
prefix is optional ("db" and "vars.db" are equivalent).

Values are printed verbatim and are never masked: this dumps every var, so
check what it contains before pasting it anywhere.`,
		Example: `  dwe vars list
  dwe vars list db
  dwe vars list --output json`,
		Args:              cobra.MaximumNArgs(1),
		SilenceUsage:      true,
		ValidArgsFunction: namespaceCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			namespace := ""
			if len(args) > 0 {
				namespace = args[0]
			}
			return runVarsList(cmd, flags, namespace)
		},
	}
	return cmd
}

// runVarsList enumerates the vars leaves (optionally filtered to a namespace),
// resolves each effective value and layer badge, and writes the JSON list or
// the styled text table. Shared by `vars list` and the bare `vars` fallback.
func runVarsList(cmd *cobra.Command, flags *cmdctx.RootFlags, namespace string) error {
	// The vars. prefix is optional on the namespace filter too: `vars list db`
	// narrows to vars.db. Empty (no filter) passes through unchanged.
	namespace = normalizeVarPath(namespace)

	cfg, err := loadConfigForVars(flags)
	if err != nil {
		return err
	}

	// Layers are read once; the per-leaf origin scan reuses them rather than
	// re-reading the three files for every path.
	layers, err := config.LoadLayers(flags.ConfigPath)
	if err != nil {
		return cmdctx.ErrWrap("project_invalid_config", err)
	}
	localPath := config.LocalLayerPath(flags.ConfigPath)

	leaves := varsusage.EnumerateVars(cfg)
	items := make([]uirender.VarListItem, 0, len(leaves))
	for _, path := range leaves {
		value, _ := varsusage.ResolveVar(cfg, path)
		items = append(items, uirender.VarListItem{
			Path:  path,
			Value: value,
			Layer: layerBadge(layers, localPath, path),
		})
	}

	data := buildVarsListJSON(items, namespace)
	return cmdctx.WriteData(flags, cmd, data, func(varsListJSON) string {
		return uirender.VarsList(items, namespace)
	})
}

// buildVarsListJSON filters items to the namespace and maps them to JSON
// entries (mirroring the text filter so both modes agree on membership).
func buildVarsListJSON(items []uirender.VarListItem, namespace string) varsListJSON {
	entries := make([]varListEntryJSON, 0, len(items))
	for _, it := range items {
		if !namespaceContains(it.Path, namespace) {
			continue
		}
		entries = append(entries, varListEntryJSON{
			Path:  it.Path,
			Value: it.Value,
			Layer: it.Layer,
		})
	}
	return varsListJSON{Vars: entries}
}

// namespaceContains mirrors render.namespaceMatches: an empty namespace matches
// everything; otherwise the match is exact or at a real dot boundary so
// "vars.db" matches "vars.db" and "vars.db.host" but not "vars.dbx".
func namespaceContains(path, namespace string) bool {
	if namespace == "" {
		return true
	}
	return path == namespace || len(path) > len(namespace) &&
		path[:len(namespace)] == namespace && path[len(namespace)] == '.'
}

// layerBadge returns the origin layer badge for a leaf: "local" when the
// local.yml layer supplies the effective value, "default" when an author layer
// does, and "" when the path is unresolved everywhere. The highest-precedence
// non-nil layer wins (mirroring deepMerge's nil-skip via ResolvePath).
func layerBadge(layers []config.Layer, localPath, path string) string {
	origin := ""
	for _, l := range layers {
		if v, ok := config.ResolvePath(l.Data, path); ok && v != nil {
			origin = l.Path
		}
	}
	switch origin {
	case "":
		return ""
	case localPath:
		return "local"
	default:
		return "default"
	}
}
