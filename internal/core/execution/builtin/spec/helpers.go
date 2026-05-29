package spec

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// GetStringParam returns the string value of key from with, or defaultVal if absent/nil.
func GetStringParam(with map[string]any, key, defaultVal string) string {
	if with == nil {
		return defaultVal
	}
	v, ok := with[key]
	if !ok || v == nil {
		return defaultVal
	}
	return fmt.Sprintf("%v", v)
}

// GetStringSlice returns a string slice from with[key].
// Accepts []any, []string, or a single string value.
func GetStringSlice(with map[string]any, key string) ([]string, error) {
	if with == nil {
		return nil, nil
	}
	v, ok := with[key]
	if !ok || v == nil {
		return nil, nil
	}
	switch val := v.(type) {
	case []any:
		result := make([]string, 0, len(val))
		for _, item := range val {
			result = append(result, fmt.Sprintf("%v", item))
		}
		return result, nil
	case []string:
		return val, nil
	case string:
		if val == "" {
			return nil, nil
		}
		return []string{val}, nil
	default:
		return nil, fmt.Errorf("param %q: expected string or list, got %T", key, v)
	}
}

// GetBoolParam returns the boolean value of key from with, or defaultVal if
// absent/nil. Accepts bool or string ("true"/"false") values.
func GetBoolParam(with map[string]any, key string, defaultVal bool) bool {
	if with == nil {
		return defaultVal
	}
	v, ok := with[key]
	if !ok || v == nil {
		return defaultVal
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		switch strings.ToLower(strings.TrimSpace(val)) {
		case "true", "yes", "1":
			return true
		case "false", "no", "0", "":
			return false
		}
	}
	return defaultVal
}

// GetStringMap returns a string map from with[key], rendering values via
// fmt.Sprint when they are not already strings. Returns nil when key absent.
func GetStringMap(with map[string]any, key string) (map[string]string, error) {
	if with == nil {
		return nil, nil
	}
	v, ok := with[key]
	if !ok || v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		if ms, ok := v.(map[string]string); ok {
			return ms, nil
		}
		return nil, fmt.Errorf("param %q: expected map, got %T", key, v)
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		out[k] = fmt.Sprint(val)
	}
	return out, nil
}

// GetMapAny returns a map[string]any from with[key]. Returns nil when absent.
func GetMapAny(with map[string]any, key string) (map[string]any, error) {
	if with == nil {
		return nil, nil
	}
	v, ok := with[key]
	if !ok || v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("param %q: expected map, got %T", key, v)
	}
	return m, nil
}

// SortedKeys returns the sorted keys of a string map for deterministic iteration.
func SortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// GetDurationParam returns the time.Duration value of key from with, or defaultVal if absent/nil.
// Accepts string values parseable by time.ParseDuration.
func GetDurationParam(with map[string]any, key string, defaultVal time.Duration) (time.Duration, error) {
	s := GetStringParam(with, key, "")
	if s == "" {
		return defaultVal, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("param %q: invalid duration %q: %w", key, s, err)
	}
	return d, nil
}
