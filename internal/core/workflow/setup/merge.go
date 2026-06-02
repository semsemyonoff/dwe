package setup

import (
	"fmt"
	"strings"
)

// PortKey identifies a port in a service.
type PortKey struct {
	Service  string
	PortName string
}

// BuildOverlay creates a nested map from questions and their answers.
// It walks each question's Writes dot-path and inserts the answer value.
// Questions missing from answers are skipped.
//
// Answers written to a `services.<n>.ports.<key>` path are auto-wrapped in
// the rich-form mapping `{port: <answer>}` (when the answer is an integer or
// stringy-int) so that downstream deep-merge against an existing rich-form
// local.yml entry preserves any per-port scheme override the developer had
// set. Non-int answers are written as-is so the path can still accept the
// full `{port, scheme}` mapping form (used by ports-overlay-on-rich-form
// questions, if any).
func BuildOverlay(questions []Question, answers map[string]any) (map[string]any, error) {
	overlay := make(map[string]any)
	for _, q := range questions {
		answer, exists := answers[q.ID]
		if !exists {
			continue
		}
		value := answer
		if isPortLeafPath(q.Writes) {
			if wrapped, ok := wrapPortLeaf(answer); ok {
				value = wrapped
			}
		}
		if err := setAtPath(overlay, q.Writes, value); err != nil {
			return nil, fmt.Errorf("question %q: %w", q.ID, err)
		}
	}
	return overlay, nil
}

// isPortLeafPath reports whether path matches `services.<n>.ports.<key>`
// (exactly four dot-segments, "services" at [0], "ports" at [2]). The wizard
// uses this shape to wrap bare-int answers into rich-form `{port: <n>}` maps.
func isPortLeafPath(path string) bool {
	parts := strings.Split(path, ".")
	return len(parts) == 4 && parts[0] == "services" && parts[2] == "ports" && parts[1] != "" && parts[3] != ""
}

// wrapPortLeaf wraps an int (or already-rich mapping) answer for a
// services.<n>.ports.<key> question into the rich-form mapping. Returns
// (wrapped, true) when the wrap was applied; (answer, false) when the answer
// shape is not recognised (caller writes the raw answer).
func wrapPortLeaf(answer any) (any, bool) {
	switch v := answer.(type) {
	case int:
		return map[string]any{"port": v}, true
	case map[string]any:
		// Already rich form (or future expansion) — pass through.
		return v, true
	}
	return answer, false
}

// BuildPortOverlay creates a services overlay from port overrides.
// Each override is validated to be in range 1..65535.
//
// Each entry is written as a rich-form mapping `{port: <n>}` (not a bare int)
// so that deepMerge against an existing local.yml entry of either shape
// behaves correctly: a prior bare-int entry is replaced with the new rich
// form; a prior rich-form entry `{port: old, scheme: https}` merges to
// `{port: new, scheme: https}` — the developer's per-port scheme override
// is preserved across setup runs.
func BuildPortOverlay(overrides map[PortKey]int) (map[string]any, error) {
	overlay := make(map[string]any)
	for key, port := range overrides {
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("port override %s.%s: %d out of range (1..65535)", key.Service, key.PortName, port)
		}
		path := fmt.Sprintf("services.%s.ports.%s", key.Service, key.PortName)
		if err := setAtPath(overlay, path, map[string]any{"port": port}); err != nil {
			return nil, fmt.Errorf("port override %s.%s: %w", key.Service, key.PortName, err)
		}
	}
	return overlay, nil
}

// MergeIntoLocal performs a deep merge of overlay into existing.
// The overlay wins on conflicts at the leaf level.
// Rejects cases where a non-map value is asked to become a map.
func MergeIntoLocal(existing map[string]any, overlay map[string]any) (map[string]any, error) {
	if err := validateMergeable(existing, overlay, nil); err != nil {
		return nil, err
	}
	result := deepCopyMapInternal(existing)
	deepMerge(result, overlay)
	return result, nil
}

// setAtPath inserts value at the given dot-path in m, creating intermediate maps as needed.
// Returns an error if a non-map value would need to become a map.
func setAtPath(m map[string]any, path string, value any) error {
	if path == "" {
		return fmt.Errorf("empty path")
	}
	parts := strings.Split(path, ".")
	for i, part := range parts {
		if part == "" {
			return fmt.Errorf("empty path segment")
		}
		if i == len(parts)-1 {
			// Refuse to replace an existing map node with a scalar — this would silently
			// discard all values written under that prefix by earlier questions.
			if ev, exists := m[part]; exists {
				if _, isMap := ev.(map[string]any); isMap {
					return fmt.Errorf("path segment %q already holds a map; cannot replace with a scalar value", part)
				}
			}
			m[part] = value
			return nil
		}
		existing, exists := m[part]
		if !exists {
			next := make(map[string]any)
			m[part] = next
			m = next
			continue
		}
		next, isMap := existing.(map[string]any)
		if !isMap {
			return fmt.Errorf("path segment %q is not a map; cannot descend", part)
		}
		m = next
	}
	return nil
}

// validateMergeable recursively checks that overlay can be merged into existing
// without ambiguous shape mismatches. The default rule is "cannot merge a map
// over an existing non-map scalar" — that would silently overwrite a leaf
// value the developer set earlier with a nested structure, which is almost
// always a wiring mistake on the wizard side.
//
// The single permitted exception is the legacy bare-int port leaf:
// `services.<svc>.ports.<port>` may hold an int from a previous run, and a
// new rich-form overlay `{port: N}` (auto-wrapped by BuildOverlay /
// BuildPortOverlay) is allowed to upgrade it. Any other scalar→map collision
// remains an error so a `writes: app.name` answer cannot wipe an unrelated
// scalar.
//
// path is the dotted location at the current recursion level (empty at the
// root). The legacy-port exception is keyed on `services.<svc>.ports.<port>`
// shape — exactly four segments with `services` at [0] and `ports` at [2].
func validateMergeable(existing map[string]any, overlay map[string]any, path []string) error {
	for key, ov := range overlay {
		if ov == nil {
			continue
		}
		ov_map, ov_isMap := ov.(map[string]any)
		if !ov_isMap {
			continue
		}
		ev, exists := existing[key]
		if !exists {
			continue
		}
		if ev == nil {
			continue
		}
		ev_map, ev_isMap := ev.(map[string]any)
		if !ev_isMap {
			// Scalar → map upgrade is permitted only for the legacy bare-int
			// port leaf shape. Everywhere else this is a wiring mistake we
			// want to surface as an error rather than silently lose the
			// existing scalar.
			childPath := make([]string, 0, len(path)+1)
			childPath = append(childPath, path...)
			childPath = append(childPath, key)
			if !isLegacyPortLeaf(childPath) {
				return fmt.Errorf("cannot merge map into non-map value at key %q", key)
			}
			continue
		}
		if err := validateMergeable(ev_map, ov_map, append(path, key)); err != nil {
			return err
		}
	}
	return nil
}

// isLegacyPortLeaf reports whether path matches `services.<svc>.ports.<port>`
// — the only place where a scalar→map overlay upgrade is permitted (legacy
// bare-int ports in local.yml being rewritten to rich form `{port: N}` by
// BuildPortOverlay / BuildOverlay).
func isLegacyPortLeaf(path []string) bool {
	return len(path) == 4 && path[0] == "services" && path[2] == "ports" && path[1] != "" && path[3] != ""
}

// deepMerge recursively merges src into dst, with src winning on conflicts at leaf level.
func deepMerge(dst, src map[string]any) {
	for k, sv := range src {
		if sv == nil {
			continue
		}
		dv, exists := dst[k]
		if !exists {
			dst[k] = sv
			continue
		}
		dsm, dIsMap := dv.(map[string]any)
		ssm, sIsMap := sv.(map[string]any)
		if dIsMap && sIsMap {
			deepMerge(dsm, ssm)
		} else {
			dst[k] = sv
		}
	}
}

// deepCopyMapInternal recursively copies a map[string]any.
func deepCopyMapInternal(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	result := make(map[string]any)
	for k, v := range m {
		if vm, isMap := v.(map[string]any); isMap {
			result[k] = deepCopyMapInternal(vm)
		} else {
			result[k] = v
		}
	}
	return result
}
