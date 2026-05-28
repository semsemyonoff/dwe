package resolve

import (
	"fmt"
	"sort"
	"strconv"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/usercommands/model"
)

// Options resolves a ParamOptions to a list of OptionItem.
//
// When Options.Static is set, it returns a copy of the list.
// When Options.From is set (a dot-path), it resolves the path against the
// raw config map and normalizes the result:
//   - List of scalars → each becomes {Value: string(v), Label: string(v)}
//   - List of maps with "value" key → each becomes an OptionItem
//   - Map → sorted-key list with {Value: key, Label: key}
//   - Other types → error
//
// If the path is missing or resolves to nil, an empty slice is returned
// without error (the caller decides whether to enforce non-empty).
func Options(opts *model.ParamOptions, raw map[string]any) ([]model.OptionItem, error) {
	if opts == nil {
		return []model.OptionItem{}, nil
	}

	// Static case: return a copy.
	if opts.Static != nil {
		result := make([]model.OptionItem, len(opts.Static))
		copy(result, opts.Static)
		return result, nil
	}

	// Dynamic case: resolve From dot-path.
	if opts.From != "" {
		resolved, found := config.ResolvePath(raw, opts.From)
		if !found || resolved == nil {
			return []model.OptionItem{}, nil
		}

		return normalizeOptions(opts.From, resolved)
	}

	return []model.OptionItem{}, nil
}

// normalizeOptions converts a resolved value (from config) to []OptionItem.
func normalizeOptions(path string, resolved any) ([]model.OptionItem, error) {
	switch v := resolved.(type) {
	case []any:
		// List of scalars or maps.
		if len(v) == 0 {
			return []model.OptionItem{}, nil
		}

		// Check the first element to determine the type.
		switch v[0].(type) {
		case string, int, float64, bool:
			// List of scalars. Validate all elements are scalar.
			result := make([]model.OptionItem, len(v))
			for i, elem := range v {
				switch elem.(type) {
				case string, int, float64, bool:
					// Valid scalar.
					str := fmt.Sprint(elem)
					result[i] = model.OptionItem{
						Value: str,
						Label: str,
					}
				default:
					return nil, fmt.Errorf("options %s[%d]: mixed scalar and non-scalar sequence not allowed", path, i)
				}
			}
			return result, nil

		case map[string]any:
			// List of maps. Try to decode each as OptionItem.
			result := make([]model.OptionItem, len(v))
			for i, elem := range v {
				m, ok := elem.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("options %s[%d]: mixed scalar and non-scalar sequence not allowed", path, i)
				}

				// Extract value, label, description from the map.
				value, ok := m["value"]
				if !ok {
					return nil, fmt.Errorf("options %s[%d]: missing required field 'value'", path, i)
				}

				item := model.OptionItem{
					Value: fmt.Sprint(value),
				}

				// Label is optional; if missing, use value.
				if label, ok := m["label"]; ok {
					item.Label = fmt.Sprint(label)
				} else {
					item.Label = item.Value
				}

				result[i] = item
			}
			return result, nil

		default:
			return nil, fmt.Errorf("options %s: list elements must be scalar or map, got %T", path, v[0])
		}

	case map[string]any:
		// Map: sorted-key list with Value=key, Label=map-value (falls back to key).
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		result := make([]model.OptionItem, len(keys))
		for i, k := range keys {
			label := k
			if s, ok := v[k].(string); ok && s != "" {
				label = s
			}
			result[i] = model.OptionItem{
				Value: k,
				Label: label,
			}
		}
		return result, nil

	case string:
		// Single string: treat as one-element list.
		return []model.OptionItem{{Value: v, Label: v}}, nil

	case int:
		str := strconv.Itoa(v)
		return []model.OptionItem{{Value: str, Label: str}}, nil

	case float64:
		str := fmt.Sprint(v)
		return []model.OptionItem{{Value: str, Label: str}}, nil

	case bool:
		str := fmt.Sprint(v)
		return []model.OptionItem{{Value: str, Label: str}}, nil

	default:
		return nil, fmt.Errorf("options %s: expected list or map, got %T", path, resolved)
	}
}
