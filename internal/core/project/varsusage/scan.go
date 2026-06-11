package varsusage

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/shared/tpl"

	"gopkg.in/yaml.v3"
)

// Usage is a single static reference to a vars.* path found in a project file.
type Usage struct {
	// File is the path relative to the project root (forward slashes).
	File string
	// Line is the 1-based line number of the reference.
	Line int
	// Kind identifies the reference syntax/field: "template" (${vars.x} in a
	// rendered field or a render template), "from"/"default_from" (structural
	// dot-path), or "when" (a vars.x token inside a typed when.expr Go template).
	Kind string
	// Text is the trimmed source line containing the reference.
	Text string
}

// ScanResult is the ordered set of usages for a queried var. It is sorted by
// file then line then kind, and de-duplicated on (File, Line, Kind, Text).
type ScanResult struct {
	Usages []Usage
}

// Field→engine map (which fields the runtime actually renders, by syntax).
//
// The scanner is deliberately NOT a flat ${vars.x} grep over whole files — that
// yields false positives (comments, quoted literals, non-rendered fields) and
// false negatives (structural dot-paths, Go-template resolves). Instead it walks
// each YAML file's node tree and inspects ONLY the value scalars of the fields
// listed below, keyed by the field's render engine:
//
//   - templatedKeys  — scalar values rendered through tpl.CompileVarSyntax /
//     RenderCommand (the ${...} shorthand). Matched with the renderer's OWN
//     pattern (tpl.VarPattern), filtered to head segment == "vars".
//     cmd / text / value / project_name / confirm: direct scalar fields.
//     env / with: mappings whose *values* are templated.
//     when (scalar form only): command/workflow when supports ${...}.
//   - structuralKeys — from / default_from: the value IS a config dot-path; a
//     "vars." prefix is a reference (no ${...} wrapper).
//   - typed when (mapping with expr): expr is a Go template; a bare vars.x token
//     inside it (e.g. {{ resolve .Raw "vars.x" }}) is matched structurally.
//
// Render templates under workspace/services/*/render/** are arbitrary text (not
// YAML) and are scanned with a whole-file ${vars.x} regex pass.
//
// The top-level vars: sandbox is EXCLUDED from the YAML walk: its values are
// config data resolved by dot-path (ResolvePath), never re-rendered through the
// template engine, so a ${vars.x} / from: appearing inside vars: is not a
// runtime usage (see scanYAMLFile).
//
// CAVEAT (surfaced to the user): Go-template FIELD access of the form .Vars.x /
// .Raw.vars.x inside info-item text or condition exprs is NOT tracked, and
// dynamically-built dot-paths cannot be tracked statically.
var (
	templatedKeys = map[string]bool{
		"cmd":          true,
		"text":         true,
		"value":        true,
		"project_name": true,
		"confirm":      true,
		"when":         true, // scalar form only; mapping form handled separately
	}
	// templatedMapKeys are mappings whose every value scalar is templated.
	templatedMapKeys = map[string]bool{
		"env":  true,
		"with": true,
	}
	structuralKeys = map[string]bool{
		"from":         true,
		"default_from": true,
	}
)

// varDotPath matches a bare vars.<dot.path> token (used inside typed when.expr
// Go templates, e.g. {{ resolve .Raw "vars.db.host" }}). The leading word
// boundary keeps it from matching the Go-template field form .Vars.db (capital
// V), which is documented as not tracked.
var varDotPath = regexp.MustCompile(`\bvars(?:\.[A-Za-z_][A-Za-z0-9_]*)+`)

// ScanUsages walks a project's workspace tree and returns every static usage of
// the queried var path (e.g. "vars.db.host"). Matching is exact OR at a dot
// boundary in either direction: a query of "vars.db" matches a usage of
// "vars.db.host" (usage under the queried namespace), and a query of
// "vars.db.host" matches a usage of "vars.db" (a whole-namespace reference).
//
// queryPath must be a vars.* dot-path; an empty/invalid query yields no usages.
// Missing files/dirs are skipped silently (a project need not have every file).
func ScanUsages(projectRoot, queryPath string) (ScanResult, error) {
	var res ScanResult
	if queryPath == "" || !strings.HasPrefix(queryPath, VarsPrefix) {
		return res, nil
	}

	workspace := filepath.Join(projectRoot, "workspace")

	// 1. YAML files under workspace/ — node-walk the templated/structural fields.
	yamlFiles, err := collectYAMLFiles(workspace)
	if err != nil {
		return res, err
	}
	for _, f := range yamlFiles {
		hits, err := scanYAMLFile(projectRoot, f, queryPath)
		if err != nil {
			// A malformed YAML file is not fatal to the whole scan — skip it.
			continue
		}
		res.Usages = append(res.Usages, hits...)
	}

	// 2. Render templates under workspace/services/*/render/** — raw text regex.
	renderHits, err := scanRenderTemplates(projectRoot, workspace, queryPath)
	if err != nil {
		return res, err
	}
	res.Usages = append(res.Usages, renderHits...)

	res.Usages = dedupeAndSort(res.Usages)
	return res, nil
}

// collectYAMLFiles returns every *.yml/*.yaml file under the workspace tree,
// EXCLUDING the per-service render/ subtrees (those are scanned as raw text).
func collectYAMLFiles(workspace string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(workspace, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if d.Name() == "render" {
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".yml", ".yaml":
			out = append(out, path)
		}
		return nil
	})
	if err != nil && os.IsNotExist(err) {
		return nil, nil
	}
	return out, err
}

func scanYAMLFile(projectRoot, absPath, queryPath string) ([]Usage, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	rel := relPath(projectRoot, absPath)
	lines := strings.Split(string(data), "\n")

	var hits []Usage
	visit := func(keyName string, valNode *yaml.Node) {
		hits = append(hits, hitsForField(keyName, valNode, queryPath, rel, lines)...)
	}
	root := doc.Content[0]
	// Skip the top-level vars: sandbox subtree. Its values are config DATA,
	// resolved by dot-path (ResolvePath) and never re-rendered through the
	// template engine — so a key named value/cmd/from/when *inside* vars: is not
	// a runtime usage. Scanning it would mis-report e.g. `vars.x.value:
	// "${vars.y}"` as a usage of vars.y. vars: is only meaningful at the file
	// root (workspace.yml / defaults.yml / local.yml), so the skip is scoped
	// there; a key literally named "vars" nested elsewhere is still scanned.
	if root.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(root.Content); i += 2 {
			key := root.Content[i]
			val := root.Content[i+1]
			if key.Value == VarsPrefix {
				continue
			}
			visit(key.Value, val)
			walkYAML(val, visit)
		}
	} else {
		walkYAML(root, visit)
	}
	return hits, nil
}

// walkYAML descends a YAML node tree, invoking fn for every (key, value) pair of
// every mapping node. keyName is the mapping key; valNode is its value node.
func walkYAML(n *yaml.Node, fn func(keyName string, valNode *yaml.Node)) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i]
			val := n.Content[i+1]
			fn(key.Value, val)
			walkYAML(val, fn)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			walkYAML(c, fn)
		}
	}
}

func hitsForField(keyName string, val *yaml.Node, queryPath, rel string, lines []string) []Usage {
	var hits []Usage

	switch {
	case structuralKeys[keyName] && val.Kind == yaml.ScalarNode:
		// from:/default_from: value IS a dot-path; a vars.* prefix is a reference.
		if refMatches(val.Value, queryPath) {
			hits = append(hits, Usage{File: rel, Line: val.Line, Kind: keyName, Text: lineText(lines, val.Line)})
		}

	case keyName == "when" && val.Kind == yaml.MappingNode:
		// Typed condition: scan expr (Go template) for bare vars.x tokens.
		if expr := mappingScalar(val, "expr"); expr != nil {
			for _, ref := range varDotPath.FindAllString(expr.Value, -1) {
				if refMatches(ref, queryPath) {
					hits = append(hits, Usage{File: rel, Line: expr.Line, Kind: "when", Text: lineText(lines, expr.Line)})
				}
			}
		}

	case templatedKeys[keyName] && val.Kind == yaml.ScalarNode:
		hits = append(hits, templateHits(val, queryPath, rel, lines)...)

	case templatedMapKeys[keyName] && val.Kind == yaml.MappingNode:
		for i := 0; i+1 < len(val.Content); i += 2 {
			v := val.Content[i+1]
			if v.Kind == yaml.ScalarNode {
				hits = append(hits, templateHits(v, queryPath, rel, lines)...)
			}
		}
	}
	return hits
}

// templateHits matches ${vars.x} references inside a scalar value.
func templateHits(val *yaml.Node, queryPath, rel string, lines []string) []Usage {
	var hits []Usage
	for _, m := range tpl.VarPattern.FindAllStringSubmatch(val.Value, -1) {
		inner := m[1]
		if head, _, _ := strings.Cut(inner, "."); head != VarsPrefix {
			continue
		}
		if refMatches(inner, queryPath) {
			hits = append(hits, Usage{File: rel, Line: val.Line, Kind: "template", Text: lineText(lines, val.Line)})
		}
	}
	return hits
}

func scanRenderTemplates(projectRoot, workspace, queryPath string) ([]Usage, error) {
	servicesDir := filepath.Join(workspace, "services")
	entries, err := os.ReadDir(servicesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var hits []Usage
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		renderDir := filepath.Join(servicesDir, e.Name(), "render")
		walkErr := filepath.WalkDir(renderDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			fileHits, err := scanRawTextFile(projectRoot, path, queryPath)
			if err != nil {
				return nil
			}
			hits = append(hits, fileHits...)
			return nil
		})
		if walkErr != nil && !os.IsNotExist(walkErr) {
			return nil, walkErr
		}
	}
	return hits, nil
}

func scanRawTextFile(projectRoot, absPath, queryPath string) ([]Usage, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	rel := relPath(projectRoot, absPath)
	var hits []Usage
	for i, line := range strings.Split(string(data), "\n") {
		for _, m := range tpl.VarPattern.FindAllStringSubmatch(line, -1) {
			inner := m[1]
			if head, _, _ := strings.Cut(inner, "."); head != VarsPrefix {
				continue
			}
			if refMatches(inner, queryPath) {
				hits = append(hits, Usage{File: rel, Line: i + 1, Kind: "template", Text: strings.TrimSpace(line)})
			}
		}
	}
	return hits, nil
}

// refMatches reports whether a found reference path matches the queried path,
// exactly or at a dot boundary in either direction.
func refMatches(ref, query string) bool {
	if ref == query {
		return true
	}
	return strings.HasPrefix(ref, query+".") || strings.HasPrefix(query, ref+".")
}

func mappingScalar(m *yaml.Node, key string) *yaml.Node {
	if m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key && m.Content[i+1].Kind == yaml.ScalarNode {
			return m.Content[i+1]
		}
	}
	return nil
}

func lineText(lines []string, line int) string {
	if line >= 1 && line <= len(lines) {
		return strings.TrimSpace(lines[line-1])
	}
	return ""
}

func relPath(projectRoot, absPath string) string {
	rel, err := filepath.Rel(projectRoot, absPath)
	if err != nil {
		rel = absPath
	}
	return filepath.ToSlash(rel)
}

func dedupeAndSort(in []Usage) []Usage {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[Usage]bool, len(in))
	out := in[:0:0]
	for _, u := range in {
		if seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Text < b.Text
	})
	return out
}
