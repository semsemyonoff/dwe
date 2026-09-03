package local

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file implements the comment-preserving yaml.Node round-trip writer for
// dwe's YAML config layer files — the STRUCTURAL writer. Editing a loaded
// document node in place and re-encoding it keeps every comment, quoting style
// and key ordering that the developer wrote, which is what `vars set`, the
// `services` toggle and the setup wizard need: they add, reorder and reshape
// whole blocks.
//
// It does NOT preserve blank lines: yaml.v3 keeps them only inside comment
// blocks, and it re-indents the whole document to its own indent step. On a
// large hand-annotated layer file that turns a one-value edit into a whole-file
// diff, which is why the second writer in this package exists —
// local_splice.go's Splicer replaces only the bytes of the nodes it edits and is
// the writer for `dwe secrets set` / `init` / `rekey`. Pick the node writer for
// a structural edit, the Splicer for an edit whose diff must be reviewable.
//
// Flow: LoadYAMLNode(path, label) -> ApplyOverlay(doc, overlay, label) ->
// WriteYAMLNode(path, doc, label, policy). The overlay is a nested
// map[string]any (the same shape the map-based callers built) describing only
// the keys to set. EncodeYAMLNode produces exactly the bytes WriteYAMLNode
// would write, for callers that must validate a staged document before it is
// persisted.
//
// `label` names the file in errors that carry no path of their own ("local
// config", "workspace config"); read/parse errors name the path instead, which
// is strictly more specific.

// Labels for the three config layer files, used in node-writer error messages.
const (
	LabelLocal     = "local config"
	LabelWorkspace = "workspace config"
	LabelDefaults  = "workspace defaults config"
)

// LoadLocalYAMLNode reads workspace/local.yml into a document *yaml.Node.
// Thin wrapper over LoadYAMLNode with the local-config label.
func LoadLocalYAMLNode(localPath string) (*yaml.Node, error) {
	return LoadYAMLNode(localPath, LabelLocal)
}

// LoadYAMLNode reads a YAML config file into a document *yaml.Node, preserving
// comments and formatting. A missing, empty, or comment-only file yields a fresh
// empty mapping document (not an error), mirroring LoadLocalYAML. Multi-document
// YAML is rejected — a dwe config layer is always a single document.
func LoadYAMLNode(path, label string) (*yaml.Node, error) {
	localPath := path
	data, err := os.ReadFile(localPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyMappingDoc(), nil
		}
		return nil, fmt.Errorf("read %s: %w", localPath, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			// Empty or comment-only document. yaml.v3 hands us no node to anchor
			// comments to here (it returns io.EOF with an empty doc), so a
			// comment-only file would otherwise lose its whole comment block on
			// the first write. Carry the raw leading comments onto the synthetic
			// empty mapping so the round-trip preserves them.
			return commentOnlyDoc(data), nil
		}
		return nil, fmt.Errorf("parse %s: %w", localPath, err)
	}
	// Reject multi-document files: a second successful decode means there is a
	// `---`-separated document we would silently ignore on write.
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, fmt.Errorf("parse %s: multi-document YAML is not supported; %s must be a single document", localPath, label)
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse %s: %w", localPath, err)
	}

	return &doc, nil
}

// ApplyOverlayToNode patches a local.yml document node. Thin wrapper over
// ApplyOverlay with the local-config label.
func ApplyOverlayToNode(doc *yaml.Node, overlay map[string]any) error {
	return ApplyOverlay(doc, overlay, LabelLocal)
}

// ApplyOverlay patches the document node in place from a nested overlay map.
// Scalars replace the matched value node's content/tag/style (deriving them from
// the NEW value, preserving only the node's comments); nested maps recurse,
// creating mapping nodes when absent; absent keys are appended. Nothing is ever
// deleted. Descending a map overlay through an existing non-mapping node is
// rejected (guards against silently discarding developer data) — except the
// single legacy bare-int port-leaf upgrade (services.<svc>.ports.<port>), which
// is allowed to become rich-form {port: N}.
//
// label names the edited file in errors that carry no path.
func ApplyOverlay(doc *yaml.Node, overlay map[string]any, label string) error {
	root, err := documentRoot(doc, label)
	if err != nil {
		return err
	}
	return applyOverlayToMapping(root, overlay, nil, label)
}

// WriteLocalYAMLNode writes a local.yml document node. Thin wrapper over
// WriteYAMLNode with the local-config label and the 0o600 force policy that
// local.yml has always had.
func WriteLocalYAMLNode(localPath string, doc *yaml.Node) error {
	return WriteYAMLNode(localPath, doc, LabelLocal, ForceMode(0o600))
}

// EncodeYAMLNode marshals a document node into exactly the bytes WriteYAMLNode
// would persist. Callers that must validate a staged document before it reaches
// disk (`dwe secrets set`) encode, re-decode and validate, then write the same
// node.
func EncodeYAMLNode(doc *yaml.Node, label string) ([]byte, error) {
	clearMergeTags(doc)
	data, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal %s node: %w", label, err)
	}
	return data, nil
}

// clearMergeTags drops the resolved "!!merge" tag the parser attaches to every
// `<<` mapping key. The tag is redundant — yaml.v3 re-resolves `<<` on load —
// but the emitter prints a non-implicit tag verbatim, turning every merge key in
// the file into `!!merge <<: *anchor` on the first write. Clearing the tag is
// idempotent and leaves mappingHasMergeKey (which also matches on the key's
// value) working.
func clearMergeTags(node *yaml.Node) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			if key := node.Content[i]; key.Tag == "!!merge" {
				key.Tag = ""
			}
		}
	}
	for _, child := range node.Content {
		clearMergeTags(child)
	}
}

// WriteYAMLNode marshals the document node and writes it atomically using the
// shared write-temp + rename helper (0o755 parent dir, file mode per policy).
func WriteYAMLNode(path string, doc *yaml.Node, label string, policy WritePolicy) error {
	data, err := EncodeYAMLNode(doc, label)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, policy)
}

// emptyMappingDoc returns a fresh document node wrapping an empty mapping.
func emptyMappingDoc() *yaml.Node {
	return &yaml.Node{
		Kind:    yaml.DocumentNode,
		Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}},
	}
}

// commentOnlyDoc builds an empty mapping document that carries the raw leading
// comments of a comment-only local.yml as the root mapping's HeadComment, so the
// first write round-trips them. yaml.v3 attaches no comments when the document
// has no content node, so they are recovered verbatim from the file bytes. The
// trailing newline is trimmed (yaml re-emits one); a whitespace-only file yields
// a plain empty mapping (no comment to carry).
func commentOnlyDoc(raw []byte) *yaml.Node {
	doc := emptyMappingDoc()
	if comment := strings.TrimRight(string(raw), "\n"); strings.TrimSpace(comment) != "" {
		doc.Content[0].HeadComment = comment
	}
	return doc
}

// documentRoot returns the root mapping node of a document node, normalizing
// empty / null roots into an empty mapping. A non-mapping, non-null root is an
// error (we will not blow away a scalar or sequence document root).
func documentRoot(doc *yaml.Node, label string) (*yaml.Node, error) {
	if doc == nil {
		return nil, errors.New("nil document node")
	}
	node := doc
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			doc.Content = []*yaml.Node{root}
			return root, nil
		}
		node = doc.Content[0]
	}
	if node.Kind == yaml.ScalarNode && (node.Tag == "!!null" || node.Tag == "") {
		// Empty/null root — upgrade to an empty mapping so we have somewhere to write.
		*node = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		return node, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s root is not a mapping (got %s)", label, kindName(node.Kind))
	}
	return node, nil
}

// applyOverlayToMapping recursively applies overlay onto a mapping node. path is
// the dotted location of the mapping (empty at root); it drives the legacy
// port-leaf exception. New keys are inserted in sorted order for determinism.
func applyOverlayToMapping(mapping *yaml.Node, overlay map[string]any, path []string, label string) error {
	keys := make([]string, 0, len(overlay))
	for k := range overlay {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		ov := overlay[key]
		childPath := append(append([]string{}, path...), key)
		_, valNode := findMappingPair(mapping, key)

		// A key absent from the explicit pairs may still be supplied through a
		// `<<: *anchor` merge key. findMappingPair only sees explicit pairs, so
		// appending a new explicit key here would silently shadow the
		// merge-inherited value (YAML explicit keys override merged ones) —
		// e.g. `vars set vars.db.port` hiding an inherited vars.db.host subtree.
		// Reject; the dev can materialize the merged value explicitly first. The
		// merge itself is never rewritten or dropped: a mapping that carries
		// `<<` but already holds the target key explicitly is edited in place
		// like any other, and so is every mapping elsewhere in the document.
		if valNode == nil && mappingHasMergeKey(mapping) {
			return fmt.Errorf("cannot set %q: parent mapping uses a YAML merge key (<<) and the key may be merge-inherited; materialize it explicitly in %s first", strings.Join(childPath, "."), label)
		}

		if sub, isMap := ov.(map[string]any); isMap {
			switch {
			case valNode == nil:
				newVal := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				mapping.Content = append(mapping.Content, scalarKeyNode(key), newVal)
				valNode = newVal
			case valNode.Kind != yaml.MappingNode:
				// Descending a map overlay through a scalar/sequence/alias/null
				// node would discard the developer's data. Permit only the
				// legacy bare-int port-leaf upgrade to rich-form {port: N}.
				if !isLegacyPortLeaf(childPath) {
					return fmt.Errorf("cannot merge map into %s value at %q", kindName(valNode.Kind), strings.Join(childPath, "."))
				}
				hc, lc, fc := valNode.HeadComment, valNode.LineComment, valNode.FootComment
				*valNode = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", HeadComment: hc, LineComment: lc, FootComment: fc}
			}
			if err := applyOverlayToMapping(valNode, sub, childPath, label); err != nil {
				return err
			}
			continue
		}

		// Scalar (or null) overlay value.
		newNode, err := encodeValueNode(ov)
		if err != nil {
			return fmt.Errorf("encode value at %q: %w", strings.Join(childPath, "."), err)
		}
		if valNode == nil {
			mapping.Content = append(mapping.Content, scalarKeyNode(key), newNode)
			continue
		}
		if valNode.Kind == yaml.MappingNode || valNode.Kind == yaml.SequenceNode {
			return fmt.Errorf("cannot replace %s value at %q with a scalar", kindName(valNode.Kind), strings.Join(childPath, "."))
		}
		// Preserve only the comments; tag/style/value derive from the new value
		// so a coerced bool/int does not inherit a previous quoted style (which
		// would reload as a string and break the set-coercion contract).
		newNode.HeadComment = valNode.HeadComment
		newNode.LineComment = valNode.LineComment
		newNode.FootComment = valNode.FootComment
		*valNode = *newNode
	}
	return nil
}

// findMappingPair returns the key and value nodes for key within a mapping node,
// or (nil, nil) when absent. A mapping node's Content is [k0, v0, k1, v1, ...].
func findMappingPair(mapping *yaml.Node, key string) (keyNode, valNode *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i], mapping.Content[i+1]
		}
	}
	return nil, nil
}

// mappingHasMergeKey reports whether a mapping node carries a YAML merge key
// (`<<: *anchor`). yaml.v3 represents it as a key scalar with value "<<" and
// tag "!!merge"; either signal is sufficient.
func mappingHasMergeKey(mapping *yaml.Node) bool {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		k := mapping.Content[i]
		if k.Tag == "!!merge" || k.Value == "<<" {
			return true
		}
	}
	return false
}

// scalarKeyNode builds a plain string key node for insertion.
func scalarKeyNode(key string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
}

// encodeValueNode encodes a coerced Go value (bool/int/float/string/nil) into a
// scalar yaml.Node with the correct tag/style for that value. Encoding a string
// whose plain form is ambiguous (e.g. "true", "42") yields a quoted scalar on
// marshal, so it reloads as a string.
func encodeValueNode(v any) (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, err
	}
	return &n, nil
}

// isLegacyPortLeaf reports whether path matches `services.<svc>.ports.<port>` —
// the only place a scalar->map overlay upgrade is permitted (legacy bare-int
// ports in local.yml being rewritten to rich form {port: N}). Mirrors the
// identically-named guard in internal/core/workflow/setup/merge.go.
func isLegacyPortLeaf(path []string) bool {
	return len(path) == 4 && path[0] == "services" && path[2] == "ports" && path[1] != "" && path[3] != ""
}

// kindName renders a yaml.Kind for error messages.
func kindName(k yaml.Kind) string {
	switch k {
	case yaml.DocumentNode:
		return "document"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.MappingNode:
		return "mapping"
	case yaml.ScalarNode:
		return "scalar"
	case yaml.AliasNode:
		return "alias"
	default:
		return "unknown"
	}
}
