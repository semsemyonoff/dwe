package config

import (
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"

	"gopkg.in/yaml.v3"
)

// formalBlockStructs maps each formalized top-level config block to its backing
// Go struct. The recognized nested-key set is DERIVED from the struct's yaml
// tags (see buildFormalBlockFields), NOT hand-listed — so it cannot drift when a
// struct gains a field, which would otherwise make a newly-valid field warn as
// "unknown" (a false positive that fails open).
//
// `ui` is intentionally absent: the dedicated uiValidator already warns on
// unknown keys directly under `ui:` AND descends into `ui.commands`, so listing
// it here would only double-report the shallow `ui:` level. `vars` (free-form)
// and `services` (per-service) are likewise out of scope by design.
var formalBlockStructs = map[string]reflect.Type{
	"project": reflect.TypeFor[config.ProjectConfig](),
	"runtime": reflect.TypeFor[config.RuntimeConfig](),
	"exports": reflect.TypeFor[config.ExportsConfig](),
	"compose": reflect.TypeFor[config.ComposeConfig](),
	"docs":    reflect.TypeFor[config.DocsConfig](),
	"update":  reflect.TypeFor[config.UpdateConfig](),
	"bridge":  reflect.TypeFor[config.BridgeConfig](),
	"stop":    reflect.TypeFor[config.StopConfig](),
}

// formalBlockFields is the block → known-nested-keys index, derived once from
// formalBlockStructs' yaml tags.
var formalBlockFields = buildFormalBlockFields()

// buildFormalBlockFields reflects each formal block's struct yaml tags into a
// set of recognized nested keys. yaml:"-" and untagged fields are skipped, with
// one explicit exception: compose.extra is yaml:"-" (post-decode injected from
// local.yml, not struct-decoded) yet a legal key, so it is re-added so it is not
// flagged — LoadConfig enforces its local.yml-only placement separately.
func buildFormalBlockFields() map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(formalBlockStructs))
	for block, typ := range formalBlockStructs {
		fields := map[string]bool{}
		for f := range typ.Fields() {
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if name == "" || name == "-" {
				continue
			}
			fields[name] = true
		}
		out[block] = fields
	}
	out["compose"]["extra"] = true
	return out
}

// formalBlocksValidator warns on unknown nested keys under formalized top-level
// config blocks, scanning all three layer files (workspace.yml /
// workspace/defaults.yml / workspace/local.yml). Every finding is a
// SeverityWarning (the merged root is decoded leniently, so a typo'd nested key
// is dropped or — for a few legacy keys — hard-errors elsewhere) carrying a
// File:Line anchor and the known-field list.
type formalBlocksValidator struct{}

func (v *formalBlocksValidator) ID() string     { return "formal_block_fields" }
func (v *formalBlocksValidator) Domain() string { return "config" }

func (v *formalBlocksValidator) Run(ctx validate.Context) []validate.Diagnostic {
	layerFiles := []string{
		filepath.Join(ctx.ProjectRoot, "workspace.yml"),
		filepath.Join(ctx.ProjectRoot, "workspace", "defaults.yml"),
		filepath.Join(ctx.ProjectRoot, "workspace", "local.yml"),
	}
	var diags []validate.Diagnostic
	for _, path := range layerFiles {
		diags = append(diags, scanFormalBlocks(ctx.ProjectRoot, path)...)
	}
	return diags
}

// scanFormalBlocks inspects the top-level mappings of one layer file. Missing or
// unparseable files are silent (dedicated loaders surface real load errors).
func scanFormalBlocks(projectRoot, path string) []validate.Diagnostic {
	doc, ok := parseYAMLDoc(path)
	if !ok {
		return nil
	}
	root := rootMappingNode(doc)
	if root == nil {
		return nil
	}
	file := relPath(projectRoot, path)

	var diags []validate.Diagnostic
	for i := 0; i+1 < len(root.Content); i += 2 {
		blockKey := root.Content[i]
		allowed, isFormal := formalBlockFields[blockKey.Value]
		if !isFormal {
			continue
		}
		// Resolve a block value that is itself a YAML alias (`stop: *anchor`).
		block := resolveMapping(root.Content[i+1])
		if block == nil {
			continue
		}
		// effectiveChildKeys expands `<<` merges so a typo hidden inside a merged
		// mapping (`runtime: { <<: { use_htps: true } }`) is still inspected.
		for _, keyNode := range effectiveChildKeys(block) {
			name := keyNode.Value
			if name == "" || name == "<<" || allowed[name] {
				continue
			}
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityWarning,
				Domain:   "config",
				Target:   "config.unknown_field:" + blockKey.Value,
				File:     file,
				Line:     keyNode.Line,
				Message:  fmt.Sprintf("unknown field %q under %q", name, blockKey.Value),
				Hint:     fmt.Sprintf("known fields: %s — check for a typo (unknown keys here are not applied)", strings.Join(slices.Sorted(maps.Keys(allowed)), ", ")),
			})
		}
	}
	return diags
}

// rootMappingNode returns the top-level mapping node of a parsed YAML document,
// or nil when the document root is not a mapping.
func rootMappingNode(doc *yaml.Node) *yaml.Node {
	n := doc
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	return resolveMapping(n)
}

// resolveMapping follows alias node(s) to their anchor and returns the mapping
// node, or nil when n does not resolve to a mapping. The bounded loop guards
// against a pathological alias chain (yaml.v3 does not emit cycles).
func resolveMapping(n *yaml.Node) *yaml.Node {
	for range 100 {
		if n == nil || n.Kind != yaml.AliasNode {
			break
		}
		n = n.Alias
	}
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// effectiveChildKeys returns the key nodes of a block mapping, expanding YAML
// merge keys (`<<: *anchor`, inline, or a sequence of either) so typos inside a
// merged mapping are inspected. The `<<` key node itself is never returned.
func effectiveChildKeys(m *yaml.Node) []*yaml.Node {
	var keys []*yaml.Node
	for j := 0; j+1 < len(m.Content); j += 2 {
		k, v := m.Content[j], m.Content[j+1]
		if k.Value == "<<" {
			keys = append(keys, mergedKeys(v)...)
			continue
		}
		keys = append(keys, k)
	}
	return keys
}

// mergedKeys collects the key nodes contributed by a `<<` merge value, which may
// be an inline mapping, an alias to a mapping, or a sequence of either.
func mergedKeys(v *yaml.Node) []*yaml.Node {
	if m := resolveMapping(v); m != nil {
		var keys []*yaml.Node
		for j := 0; j+1 < len(m.Content); j += 2 {
			keys = append(keys, m.Content[j])
		}
		return keys
	}
	if v != nil && v.Kind == yaml.SequenceNode {
		var keys []*yaml.Node
		for _, item := range v.Content {
			keys = append(keys, mergedKeys(item)...)
		}
		return keys
	}
	return nil
}
