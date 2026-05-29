package snapshot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"devbox-cli/internal/core/workflow/deploy/journal"
	"devbox-cli/internal/core/workflow/snapshot/meta"
)

// localYMLMaxBytes caps the size of any devbox/local.yml input the
// preserve_keys helpers will parse. The snapshot-embedded copy is untrusted
// input from an archive, so we reject pathological documents before handing
// them to yaml.v3. 1 MiB is orders of magnitude above any realistic local.yml.
const localYMLMaxBytes = 1 << 20

// captureDevboxFiles copies devbox/local.yml and the deploy state file from
// baseDir into <snapDir>/devbox/. Missing source files are skipped silently —
// neither file is mandatory at create time. The preserveKeys parameter lists
// dot-paths to strip from the captured local.yml so they can be re-spliced
// from the working copy on restore.
func captureDevboxFiles(baseDir, snapDir string, preserveKeys []string) (meta.DevboxFiles, error) {
	var df meta.DevboxFiles

	srcLocal := filepath.Join(baseDir, "devbox", "local.yml")
	dstLocalRel := filepath.Join(meta.DevboxSubdir, "local.yml")
	dstLocal := filepath.Join(snapDir, dstLocalRel)
	wrote, err := captureLocalYML(srcLocal, dstLocal, preserveKeys)
	if err != nil {
		return df, fmt.Errorf("snapshot: capture %s: %w", srcLocal, err)
	}
	if wrote {
		df.LocalYML = filepath.ToSlash(dstLocalRel)
	}

	srcState := filepath.Join(baseDir, journal.DefaultRelPath)
	dstStateRel := filepath.Join(meta.DevboxSubdir, "deploy-state.yml")
	dstState := filepath.Join(snapDir, dstStateRel)
	ok, err := copyFileIfExists(srcState, dstState)
	if err != nil {
		return df, fmt.Errorf("snapshot: capture %s: %w", srcState, err)
	}
	if ok {
		df.DeployState = filepath.ToSlash(dstStateRel)
	}
	return df, nil
}

// captureLocalYML reads the working-copy local.yml at src, strips any
// preserveKeys from it, and writes the result to dst. Returns (false, nil) if
// src is missing. When src exists, captureLocalYML always writes dst — even
// when the strip leaves the document structurally empty — so restore can rely
// on the snapshot's local.yml presence reflecting capture-time presence.
func captureLocalYML(src, dst string, preserveKeys []string) (bool, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	stripped, err := stripPreservedKeys(data, preserveKeys)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	if err := meta.WriteFileAtomic(dst, stripped, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// copyFileIfExists copies src to dst atomically when src exists; returns
// (false, nil) when src is missing.
func copyFileIfExists(src, dst string) (bool, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	if err := meta.WriteFileAtomic(dst, data, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// restoreDevboxFiles writes the snapshot's local.yml and deploy-state.yml over
// the working-copy paths in baseDir. Writes are atomic.
//
// local.yml restore follows a four-row edge case table driven by
// preserveKeys (see docs/reference/config/snapshot.md):
//
//	snapshot/current
//	yes/yes  → merge: snapshot overlay + preserved keys spliced from current
//	yes/no   → write snapshot's local.yml as-is (preserve_keys no-op)
//	no/yes   → write a minimal local.yml containing only preserved keys
//	           extracted from current (when preserveKeys is empty the working
//	           copy is removed instead)
//	no/no    → no-op
//
// deploy-state.yml is a plain overwrite: when the snapshot has no copy the
// working-copy file is removed so restored state matches captured state.
func restoreDevboxFiles(snapDir, baseDir string, preserveKeys []string) error {
	if err := restoreLocalYML(
		filepath.Join(snapDir, meta.DevboxSubdir, "local.yml"),
		filepath.Join(baseDir, "devbox", "local.yml"),
		preserveKeys,
	); err != nil {
		return err
	}

	stateSrc := filepath.Join(snapDir, meta.DevboxSubdir, "deploy-state.yml")
	stateDst := filepath.Join(baseDir, journal.DefaultRelPath)
	data, err := os.ReadFile(stateSrc)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if rErr := os.Remove(stateDst); rErr != nil && !errors.Is(rErr, os.ErrNotExist) {
				return fmt.Errorf("remove stale %s: %w", stateDst, rErr)
			}
			return nil
		}
		return fmt.Errorf("read %s: %w", stateSrc, err)
	}
	if err := os.MkdirAll(filepath.Dir(stateDst), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(stateDst), err)
	}
	if err := meta.WriteFileAtomic(stateDst, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", stateDst, err)
	}
	return nil
}

// restoreLocalYML implements the four-row edge case table documented on
// restoreDevboxFiles.
func restoreLocalYML(src, dst string, preserveKeys []string) error {
	snapData, snapErr := os.ReadFile(src)
	snapMissing := errors.Is(snapErr, os.ErrNotExist)
	if snapErr != nil && !snapMissing {
		return fmt.Errorf("read %s: %w", src, snapErr)
	}

	curData, curErr := os.ReadFile(dst)
	curMissing := errors.Is(curErr, os.ErrNotExist)
	if curErr != nil && !curMissing {
		return fmt.Errorf("read %s: %w", dst, curErr)
	}

	switch {
	case snapMissing && curMissing:
		// no/no — nothing to do
		return nil
	case snapMissing && !curMissing:
		// no/yes — write minimal local.yml containing only preserved keys.
		minimal, err := extractPreservedKeys(curData, preserveKeys)
		if err != nil {
			return fmt.Errorf("extract preserved keys from %s: %w", dst, err)
		}
		if len(minimal) == 0 {
			if rErr := os.Remove(dst); rErr != nil && !errors.Is(rErr, os.ErrNotExist) {
				return fmt.Errorf("remove stale %s: %w", dst, rErr)
			}
			return nil
		}
		return writeLocalYML(dst, minimal)
	case !snapMissing && curMissing:
		// yes/no — write snapshot as-is.
		return writeLocalYML(dst, snapData)
	default:
		// yes/yes — merge preserved keys from current into snapshot.
		merged, err := mergePreservedKeys(snapData, curData, preserveKeys)
		if err != nil {
			return fmt.Errorf("merge preserved keys into %s: %w", dst, err)
		}
		return writeLocalYML(dst, merged)
	}
}

func writeLocalYML(dst string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
	}
	if err := meta.WriteFileAtomic(dst, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

// extractPreservedKeys returns a minimal YAML document containing only the
// given dot-paths from src. Paths missing in src are silently dropped; the
// returned bytes are empty when no paths resolve.
func extractPreservedKeys(src []byte, dotPaths []string) ([]byte, error) {
	if len(src) > localYMLMaxBytes {
		return nil, fmt.Errorf("local.yml exceeds %d bytes (got %d)", localYMLMaxBytes, len(src))
	}
	if len(dotPaths) == 0 {
		return nil, nil
	}
	curRoot, err := parseYAMLDoc(src)
	if err != nil {
		return nil, err
	}
	if curRoot == nil {
		return nil, nil
	}
	out := newMappingNode()
	hits := 0
	for _, p := range dotPaths {
		segs := splitDotPath(p)
		if len(segs) == 0 {
			continue
		}
		keyNode, valNode, found, err := lookupPath(curRoot, segs, p)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		if err := setPath(out, segs, valNode, keyNode, p); err != nil {
			return nil, err
		}
		hits++
	}
	if hits == 0 {
		return nil, nil
	}
	return marshalYAMLDoc(out)
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
		keyNode, valNode, found, err := lookupPath(curRoot, segs, p)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		if err := setPath(snapRoot, segs, valNode, keyNode, p); err != nil {
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

// lookupPath resolves segs in mapping and returns (keyNode, valNode, true, nil)
// when the leaf is present. A missing key returns (nil, nil, false, nil).
// Structural errors at intermediate segments are returned wrapped. Returning
// the key node alongside the value node lets callers carry comments attached
// to the key into the merged document.
func lookupPath(mapping *yaml.Node, segs []string, fullPath string) (*yaml.Node, *yaml.Node, bool, error) {
	for i, seg := range segs {
		if mapping.Kind != yaml.MappingNode {
			return nil, nil, false, fmt.Errorf("preserve_keys %q: expected mapping at %q, got %s", fullPath, seg, yamlKindName(mapping.Kind))
		}
		idx := findKey(mapping, seg)
		if idx < 0 {
			return nil, nil, false, nil
		}
		key := mapping.Content[idx]
		val := mapping.Content[idx+1]
		if i == len(segs)-1 {
			return key, val, true, nil
		}
		mapping = val
	}
	return nil, nil, false, nil
}

// setPath splices value into mapping at segs, creating intermediate mappings
// where they do not exist. When an intermediate segment exists but is not a
// mapping, setPath returns a type-conflict error. When the leaf exists and
// disagrees with value in kind, setPath also returns a type-conflict error
// so we never silently overwrite differently-shaped state.
//
// leafKey, when non-nil, is used as the key node at the final segment. This
// carries head/line comments from the source document into the target so
// comments on preserved keys survive merge and extract operations.
func setPath(mapping *yaml.Node, segs []string, value *yaml.Node, leafKey *yaml.Node, fullPath string) error {
	if mapping.Kind != yaml.MappingNode {
		return fmt.Errorf("preserve_keys %q: expected mapping at root, got %s", fullPath, yamlKindName(mapping.Kind))
	}
	for i, seg := range segs {
		idx := findKey(mapping, seg)
		if i == len(segs)-1 {
			if idx < 0 {
				kn := leafKey
				if kn == nil {
					kn = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: seg}
				}
				mapping.Content = append(mapping.Content, kn, value)
				return nil
			}
			existing := mapping.Content[idx+1]
			if existing.Kind != value.Kind {
				return fmt.Errorf("preserve_keys %q: type conflict (snapshot=%s, current=%s)", fullPath, yamlKindName(existing.Kind), yamlKindName(value.Kind))
			}
			if leafKey != nil {
				mapping.Content[idx] = leafKey
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
