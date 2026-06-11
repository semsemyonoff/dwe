package vars

import (
	"path/filepath"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/varsusage"
	uirender "github.com/semsemyonoff/dwe/internal/core/ui/render"

	"github.com/spf13/cobra"
)

// varInspectJSON is the JSON shape for `dwe vars inspect --output json`.
type varInspectJSON struct {
	Var    string            `json:"var"`
	Layers varInspectLayers  `json:"layers"`
	Origin string            `json:"origin"`
	Usages []varInspectUsage `json:"usages"`
}

// varInspectLayers carries the per-layer resolved values. Each *Set flag
// reports presence so a JSON consumer can distinguish an explicit null from an
// absent layer (a bare null value alone is ambiguous).
type varInspectLayers struct {
	Author       any  `json:"author"`
	AuthorSet    bool `json:"author_set"`
	Local        any  `json:"local"`
	LocalSet     bool `json:"local_set"`
	Effective    any  `json:"effective"`
	EffectiveSet bool `json:"effective_set"`
}

// varInspectUsage is one static reference: its file (relative to the project
// root), line, reference kind, and the source line text.
type varInspectUsage struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

func newVarsInspectCmd(flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect <var>",
		Short: "Show per-layer values, origin file, and every static usage",
		Long: `Inspect a single var across all three config layers (author default,
local override, effective) and statically map every place it is used —
${vars.x} in rendered fields and render templates, plus structural
from: / default_from: / when: references.

Dynamically-built var paths and Go-template field access (.Vars.x) cannot be
tracked statically and are not reported.`,
		Example: `  dwe vars inspect vars.db.host
  dwe vars inspect vars.db.host --output json`,
		Args:              cobra.ExactArgs(1),
		SilenceUsage:      true,
		ValidArgsFunction: leafCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVarsInspect(cmd, flags, args[0])
		},
	}
	return cmd
}

// runVarsInspect resolves a var across the three layers, scans the project for
// static usages, and writes the JSON envelope or the styled inspect block. The
// path must resolve at some layer (or have a usage) — an entirely unknown path
// is a typed not-found error, mirroring `get`.
func runVarsInspect(cmd *cobra.Command, flags *cmdctx.RootFlags, path string) error {
	// Confine inspection to the vars.* sandbox — mirrors `get`. Without this a
	// container could resolve arbitrary project config (per-layer values, origin
	// file) through the bridge-reachable `vars` surface.
	if !isVarsPath(path) {
		return notFoundError(path)
	}

	layered, err := config.ResolveLayeredPath(flags.ConfigPath, path)
	if err != nil {
		return cmdctx.ErrWrap("project_invalid_config", err)
	}

	scan, err := varsusage.ScanUsages(flags.ProjectRoot(), path)
	if err != nil {
		return cmdctx.ErrWrap("internal_error", err)
	}

	// A path unresolved at every layer AND referenced nowhere does not exist.
	if !layered.AuthorOK && !layered.LocalOK && !layered.EffectiveOK && len(scan.Usages) == 0 {
		return notFoundError(path)
	}

	origin := originDisplay(flags, layered.Origin)
	inspect := uirender.VarInspect{
		Path:        path,
		Author:      layered.Author,
		AuthorOK:    layered.AuthorOK,
		Local:       layered.Local,
		LocalOK:     layered.LocalOK,
		Effective:   layered.Effective,
		EffectiveOK: layered.EffectiveOK,
		Origin:      origin,
		Usages:      scan.Usages,
	}

	data := buildInspectJSON(path, layered, origin, scan.Usages)
	return cmdctx.WriteData(flags, cmd, data, func(varInspectJSON) string {
		return uirender.VarInspectView(inspect, 0)
	})
}

// buildInspectJSON maps the resolved layers and usages to the JSON envelope.
func buildInspectJSON(path string, layered config.LayeredValue, origin string, usages []varsusage.Usage) varInspectJSON {
	entries := make([]varInspectUsage, 0, len(usages))
	for _, u := range usages {
		entries = append(entries, varInspectUsage{
			File: u.File,
			Line: u.Line,
			Kind: u.Kind,
			Text: u.Text,
		})
	}
	return varInspectJSON{
		Var: path,
		Layers: varInspectLayers{
			Author:       layered.Author,
			AuthorSet:    layered.AuthorOK,
			Local:        layered.Local,
			LocalSet:     layered.LocalOK,
			Effective:    layered.Effective,
			EffectiveSet: layered.EffectiveOK,
		},
		Origin: origin,
		Usages: entries,
	}
}

// originDisplay turns the absolute origin layer path into a project-relative
// display path (forward slashes); an empty origin stays empty. If the path
// cannot be made relative it is returned as-is.
func originDisplay(flags *cmdctx.RootFlags, origin string) string {
	if origin == "" {
		return ""
	}
	root := flags.ProjectRoot()
	if root == "" {
		return filepath.ToSlash(origin)
	}
	rel, err := filepath.Rel(root, origin)
	if err != nil {
		return filepath.ToSlash(origin)
	}
	return filepath.ToSlash(rel)
}
