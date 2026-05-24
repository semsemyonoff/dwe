package snapshot

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"devbox-cli/internal/deploy/journal"
)

// localYMLMaxBytes caps the size of any devbox/local.yml input the
// preserve_keys helpers will parse. The snapshot-embedded copy is untrusted
// input from an archive, so we reject pathological documents before handing
// them to yaml.v3. 1 MiB is orders of magnitude above any realistic local.yml.
const localYMLMaxBytes = 1 << 20

// captureDevboxFiles copies devbox/local.yml and the deploy state file from
// baseDir into <snapDir>/devbox/. Missing source files are skipped silently —
// neither file is mandatory at create time. The preserveKeys parameter is the
// list of dot-paths the caller wants stripped from the captured local.yml so
// they can be re-spliced from the working copy on restore; Task 7 wires the
// strip step into the local.yml copy path.
func captureDevboxFiles(baseDir, snapDir string, preserveKeys []string) (DevboxFiles, error) {
	_ = preserveKeys // wired by Task 7
	var df DevboxFiles
	targets := []struct {
		src    string
		dstRel string
		field  *string
	}{
		{filepath.Join(baseDir, "devbox", "local.yml"), filepath.Join(DevboxSubdir, "local.yml"), &df.LocalYML},
		{filepath.Join(baseDir, journal.DefaultRelPath), filepath.Join(DevboxSubdir, "deploy-state.yml"), &df.DeployState},
	}
	for _, t := range targets {
		ok, err := copyFileIfExists(t.src, filepath.Join(snapDir, t.dstRel))
		if err != nil {
			return df, fmt.Errorf("snapshot: capture %s: %w", t.src, err)
		}
		if ok {
			*t.field = filepath.ToSlash(t.dstRel)
		}
	}
	return df, nil
}

// copyFileIfExists copies src to dst when src exists; returns (false, nil)
// when src is missing.
func copyFileIfExists(src, dst string) (bool, error) {
	in, err := os.Open(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return false, err
	}
	if err := out.Close(); err != nil {
		return false, err
	}
	return true, nil
}

// restoreDevboxFiles copies <snapDir>/devbox/local.yml and
// <snapDir>/devbox/deploy-state.yml over the working-copy paths in baseDir.
// Each destination write is atomic. When a source file is absent from the
// snapshot (it did not exist at capture time), the corresponding working-copy
// file is removed so that the restored state exactly matches the captured
// state — leaving a stale file would diverge from what was snapshotted. The
// preserveKeys parameter is the list of dot-paths the caller wants spliced
// from the current working-copy local.yml back into the restored local.yml;
// Task 7 wires the merge step into the local.yml copy path.
func restoreDevboxFiles(snapDir, baseDir string, preserveKeys []string) error {
	_ = preserveKeys // wired by Task 7
	type pair struct{ src, dst string }
	pairs := []pair{
		{filepath.Join(snapDir, DevboxSubdir, "local.yml"), filepath.Join(baseDir, "devbox", "local.yml")},
		{filepath.Join(snapDir, DevboxSubdir, "deploy-state.yml"), filepath.Join(baseDir, journal.DefaultRelPath)},
	}
	for _, p := range pairs {
		data, err := os.ReadFile(p.src)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if rErr := os.Remove(p.dst); rErr != nil && !errors.Is(rErr, os.ErrNotExist) {
					return fmt.Errorf("remove stale %s: %w", p.dst, rErr)
				}
				continue
			}
			return fmt.Errorf("read %s: %w", p.src, err)
		}
		if err := os.MkdirAll(filepath.Dir(p.dst), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(p.dst), err)
		}
		if err := writeFileAtomic(p.dst, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", p.dst, err)
		}
	}
	return nil
}

// stripPreservedKeys parses yamlBytes as a YAML mapping document and removes
// each dot-path in dotPaths from it, returning the re-marshaled document.
//
// Missing paths are silent no-ops. A structural error — an intermediate
// segment whose value is not a mapping — is returned as a wrapped error so
// the caller can surface a meaningful diagnostic. yaml.v3 normalizes
// indentation and flow/block style on marshal; key order at untouched levels
// is preserved.
//
// Inputs larger than localYMLMaxBytes are rejected before parsing.
func stripPreservedKeys(yamlBytes []byte, dotPaths []string) ([]byte, error) {
	if len(yamlBytes) > localYMLMaxBytes {
		return nil, fmt.Errorf("local.yml exceeds %d bytes (got %d)", localYMLMaxBytes, len(yamlBytes))
	}
	if len(dotPaths) == 0 || len(yamlBytes) == 0 {
		return yamlBytes, nil
	}

	root, err := parseYAMLDoc(yamlBytes)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return yamlBytes, nil
	}

	for _, p := range dotPaths {
		segs := splitDotPath(p)
		if len(segs) == 0 {
			continue
		}
		if err := removePath(root, segs, p); err != nil {
			return nil, err
		}
	}

	return marshalYAMLDoc(root)
}

// mergePreservedKeys parses snapshotBytes and currentBytes as YAML mapping
// documents and, for each dot-path in dotPaths, splices the current document's
// value (with its attached comments where yaml.v3 retains them) into the
// snapshot document at the same path, creating intermediate mappings on the
// snapshot side if missing. Returns the re-marshaled snapshot document.
//
// Paths that do not resolve in current are silent no-ops. A type conflict
// between the two trees at a resolved path returns a wrapped error rather than
// silently overwriting the snapshot's structure.
//
// Both inputs are capped at localYMLMaxBytes.
func mergePreservedKeys(snapshotBytes, currentBytes []byte, dotPaths []string) ([]byte, error) {
	if len(snapshotBytes) > localYMLMaxBytes {
		return nil, fmt.Errorf("snapshot local.yml exceeds %d bytes (got %d)", localYMLMaxBytes, len(snapshotBytes))
	}
	if len(currentBytes) > localYMLMaxBytes {
		return nil, fmt.Errorf("current local.yml exceeds %d bytes (got %d)", localYMLMaxBytes, len(currentBytes))
	}
	if len(dotPaths) == 0 {
		return snapshotBytes, nil
	}

	snapRoot, err := parseYAMLDoc(snapshotBytes)
	if err != nil {
		return nil, fmt.Errorf("parse snapshot local.yml: %w", err)
	}
	curRoot, err := parseYAMLDoc(currentBytes)
	if err != nil {
		return nil, fmt.Errorf("parse current local.yml: %w", err)
	}
	if curRoot == nil {
		// nothing to merge in
		return marshalYAMLDoc(snapRoot)
	}
	if snapRoot == nil {
		snapRoot = newMappingNode()
	}

	for _, p := range dotPaths {
		segs := splitDotPath(p)
		if len(segs) == 0 {
			continue
		}
		valNode, found, err := lookupPath(curRoot, segs, p)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		if err := setPath(snapRoot, segs, valNode, p); err != nil {
			return nil, err
		}
	}

	return marshalYAMLDoc(snapRoot)
}

// parseYAMLDoc decodes yamlBytes into a single mapping node, or returns nil
// when the document is structurally empty. Non-mapping root documents (e.g.
// a top-level sequence or scalar) are rejected.
func parseYAMLDoc(b []byte) (*yaml.Node, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("local.yml: root must be a mapping, got %s", yamlKindName(root.Kind))
	}
	return root, nil
}

// marshalYAMLDoc serializes a mapping root back into a byte slice.
func marshalYAMLDoc(root *yaml.Node) ([]byte, error) {
	if root == nil {
		return nil, nil
	}
	return yaml.Marshal(root)
}

func newMappingNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

// splitDotPath splits "a.b.c" into ["a","b","c"]; empty segments are
// discarded so " .a." returns ["a"].
func splitDotPath(p string) []string {
	parts := strings.Split(p, ".")
	out := parts[:0]
	for _, s := range parts {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// removePath walks segs from mapping and removes the leaf key. Intermediate
// segments must resolve to mapping nodes; otherwise removePath returns a
// structural error. A missing key at any segment is a silent no-op.
func removePath(mapping *yaml.Node, segs []string, fullPath string) error {
	if mapping.Kind != yaml.MappingNode {
		return fmt.Errorf("preserve_keys %q: expected mapping at root, got %s", fullPath, yamlKindName(mapping.Kind))
	}
	for i, seg := range segs {
		idx := findKey(mapping, seg)
		if idx < 0 {
			return nil
		}
		valNode := mapping.Content[idx+1]
		if i == len(segs)-1 {
			mapping.Content = append(mapping.Content[:idx], mapping.Content[idx+2:]...)
			return nil
		}
		if valNode.Kind != yaml.MappingNode {
			return fmt.Errorf("preserve_keys %q: expected mapping at %q, got %s", fullPath, seg, yamlKindName(valNode.Kind))
		}
		mapping = valNode
	}
	return nil
}

// lookupPath resolves segs in mapping and returns (node, true, nil) when the
// leaf is present. A missing key returns (nil, false, nil). Structural errors
// at intermediate segments are returned wrapped.
func lookupPath(mapping *yaml.Node, segs []string, fullPath string) (*yaml.Node, bool, error) {
	for i, seg := range segs {
		if mapping.Kind != yaml.MappingNode {
			return nil, false, fmt.Errorf("preserve_keys %q: expected mapping at %q, got %s", fullPath, seg, yamlKindName(mapping.Kind))
		}
		idx := findKey(mapping, seg)
		if idx < 0 {
			return nil, false, nil
		}
		val := mapping.Content[idx+1]
		if i == len(segs)-1 {
			return val, true, nil
		}
		mapping = val
	}
	return nil, false, nil
}

// setPath splices value into mapping at segs, creating intermediate mappings
// where they do not exist. When an intermediate segment exists but is not a
// mapping, setPath returns a type-conflict error. When the leaf exists and
// disagrees with value in kind, setPath also returns a type-conflict error
// so we never silently overwrite differently-shaped state.
func setPath(mapping *yaml.Node, segs []string, value *yaml.Node, fullPath string) error {
	if mapping.Kind != yaml.MappingNode {
		return fmt.Errorf("preserve_keys %q: expected mapping at root, got %s", fullPath, yamlKindName(mapping.Kind))
	}
	for i, seg := range segs {
		idx := findKey(mapping, seg)
		if i == len(segs)-1 {
			if idx < 0 {
				keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: seg}
				mapping.Content = append(mapping.Content, keyNode, value)
				return nil
			}
			existing := mapping.Content[idx+1]
			if existing.Kind != value.Kind {
				return fmt.Errorf("preserve_keys %q: type conflict (snapshot=%s, current=%s)", fullPath, yamlKindName(existing.Kind), yamlKindName(value.Kind))
			}
			mapping.Content[idx+1] = value
			return nil
		}
		if idx < 0 {
			keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: seg}
			child := newMappingNode()
			mapping.Content = append(mapping.Content, keyNode, child)
			mapping = child
			continue
		}
		val := mapping.Content[idx+1]
		if val.Kind != yaml.MappingNode {
			return fmt.Errorf("preserve_keys %q: type conflict at %q (snapshot=%s, current=mapping)", fullPath, seg, yamlKindName(val.Kind))
		}
		mapping = val
	}
	return nil
}

// findKey returns the index of the key entry in a mapping's Content slice, or
// -1 when absent. Mapping Content alternates [key, value, key, value, ...] so
// the value lives at idx+1.
func findKey(mapping *yaml.Node, name string) int {
	for i := 0; i < len(mapping.Content); i += 2 {
		k := mapping.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == name {
			return i
		}
	}
	return -1
}

func yamlKindName(k yaml.Kind) string {
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
