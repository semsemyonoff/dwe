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
		if port < minPort || port > maxPort {
			return nil, fmt.Errorf("port override %s.%s: %d out of range (1..65535)", key.Service, key.PortName, port)
		}
		path := fmt.Sprintf("services.%s.ports.%s", key.Service, key.PortName)
		if err := setAtPath(overlay, path, map[string]any{"port": port}); err != nil {
			return nil, fmt.Errorf("port override %s.%s: %w", key.Service, key.PortName, err)
		}
	}
	return overlay, nil
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
