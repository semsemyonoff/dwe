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
func BuildOverlay(questions []Question, answers map[string]any) (map[string]any, error) {
	overlay := make(map[string]any)
	for _, q := range questions {
		answer, exists := answers[q.ID]
		if !exists {
			continue
		}
		if err := setAtPath(overlay, q.Writes, answer); err != nil {
			return nil, fmt.Errorf("question %q: %w", q.ID, err)
		}
	}
	return overlay, nil
}

// BuildPortOverlay creates a services overlay from port overrides.
// Each override is validated to be in range 1..65535.
func BuildPortOverlay(overrides map[PortKey]int) (map[string]any, error) {
	overlay := make(map[string]any)
	for key, port := range overrides {
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("port override %s.%s: %d out of range (1..65535)", key.Service, key.PortName, port)
		}
		path := fmt.Sprintf("services.%s.ports.%s", key.Service, key.PortName)
		if err := setAtPath(overlay, path, port); err != nil {
			return nil, fmt.Errorf("port override %s.%s: %w", key.Service, key.PortName, err)
		}
	}
	return overlay, nil
}

// MergeIntoLocal performs a deep merge of overlay into existing.
// The overlay wins on conflicts at the leaf level.
// Rejects cases where a non-map value is asked to become a map.
func MergeIntoLocal(existing map[string]any, overlay map[string]any) (map[string]any, error) {
	if err := validateMergeable(existing, overlay); err != nil {
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
// without overwriting non-map values with maps.
func validateMergeable(existing map[string]any, overlay map[string]any) error {
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
			return fmt.Errorf("cannot merge map into non-map value at key %q", key)
		}
		if err := validateMergeable(ev_map, ov_map); err != nil {
			return err
		}
	}
	return nil
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
