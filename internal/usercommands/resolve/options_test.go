package resolve

import (
	"testing"

	"devbox-cli/internal/usercommands/model"

	"github.com/stretchr/testify/require"
)

func TestResolveOptions_Static(t *testing.T) {
	// Test static list passthrough.
	opts := &model.ParamOptions{
		Static: []model.OptionItem{
			{Value: "a", Label: "Option A"},
			{Value: "b", Label: "Option B"},
		},
	}

	result, err := Options(opts, nil)
	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Equal(t, "a", result[0].Value)
	require.Equal(t, "Option A", result[0].Label)
	require.Equal(t, "b", result[1].Value)
	require.Equal(t, "Option B", result[1].Label)
}

func TestResolveOptions_EmptyStatic(t *testing.T) {
	opts := &model.ParamOptions{
		Static: []model.OptionItem{},
	}

	result, err := Options(opts, nil)
	require.NoError(t, err)
	require.Len(t, result, 0)
}

func TestResolveOptions_Nil(t *testing.T) {
	result, err := Options(nil, nil)
	require.NoError(t, err)
	require.Len(t, result, 0)
}

func TestResolveOptions_FromMissing(t *testing.T) {
	opts := &model.ParamOptions{
		From: "missing.key",
	}
	raw := map[string]any{"other": "value"}

	result, err := Options(opts, raw)
	require.NoError(t, err)
	require.Len(t, result, 0)
}

func TestResolveOptions_FromStringList(t *testing.T) {
	opts := &model.ParamOptions{
		From: "databases",
	}
	raw := map[string]any{
		"databases": []any{"users", "logs", "events"},
	}

	result, err := Options(opts, raw)
	require.NoError(t, err)
	require.Len(t, result, 3)
	require.Equal(t, "users", result[0].Value)
	require.Equal(t, "users", result[0].Label)
	require.Equal(t, "logs", result[1].Value)
	require.Equal(t, "events", result[2].Value)
}

func TestResolveOptions_FromMapList(t *testing.T) {
	opts := &model.ParamOptions{
		From: "drivers",
	}
	raw := map[string]any{
		"drivers": []any{
			map[string]any{"value": "pg", "label": "PostgreSQL 16"},
			map[string]any{"value": "mysql", "label": "MySQL 8"},
		},
	}

	result, err := Options(opts, raw)
	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Equal(t, "pg", result[0].Value)
	require.Equal(t, "PostgreSQL 16", result[0].Label)
	require.Equal(t, "mysql", result[1].Value)
	require.Equal(t, "MySQL 8", result[1].Label)
}

func TestResolveOptions_FromMapListWithUnknownKey(t *testing.T) {
	// Unknown keys in option maps (e.g. "description") are silently ignored.
	opts := &model.ParamOptions{
		From: "services",
	}
	raw := map[string]any{
		"services": []any{
			map[string]any{
				"value":       "main",
				"label":       "Main Service",
				"description": "The primary service",
			},
		},
	}

	result, err := Options(opts, raw)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "main", result[0].Value)
	require.Equal(t, "Main Service", result[0].Label)
}

func TestResolveOptions_FromMapListMissingLabel(t *testing.T) {
	// Label should default to value if missing.
	opts := &model.ParamOptions{
		From: "items",
	}
	raw := map[string]any{
		"items": []any{
			map[string]any{"value": "foo"},
		},
	}

	result, err := Options(opts, raw)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "foo", result[0].Value)
	require.Equal(t, "foo", result[0].Label)
}

func TestResolveOptions_FromMapListMissingValue(t *testing.T) {
	opts := &model.ParamOptions{
		From: "items",
	}
	raw := map[string]any{
		"items": []any{
			map[string]any{"label": "Missing Value"},
		},
	}

	_, err := Options(opts, raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing required field 'value'")
}

func TestResolveOptions_FromMap(t *testing.T) {
	// Map should produce sorted-key list.
	opts := &model.ParamOptions{
		From: "config.envs",
	}
	raw := map[string]any{
		"config": map[string]any{
			"envs": map[string]any{
				"prod": "production",
				"dev":  "development",
				"qa":   "quality assurance",
			},
		},
	}

	result, err := Options(opts, raw)
	require.NoError(t, err)
	require.Len(t, result, 3)
	// Should be sorted by key.
	require.Equal(t, "dev", result[0].Value)
	require.Equal(t, "development", result[0].Label) // map value used as label
	require.Equal(t, "prod", result[1].Value)
	require.Equal(t, "production", result[1].Label)
	require.Equal(t, "qa", result[2].Value)
	require.Equal(t, "quality assurance", result[2].Label)
}

func TestResolveOptions_FromNestedPath(t *testing.T) {
	opts := &model.ParamOptions{
		From: "services.main.options",
	}
	raw := map[string]any{
		"services": map[string]any{
			"main": map[string]any{
				"options": []any{"opt1", "opt2", "opt3"},
			},
		},
	}

	result, err := Options(opts, raw)
	require.NoError(t, err)
	require.Len(t, result, 3)
	require.Equal(t, "opt1", result[0].Value)
	require.Equal(t, "opt2", result[1].Value)
	require.Equal(t, "opt3", result[2].Value)
}

func TestResolveOptions_FromIntList(t *testing.T) {
	opts := &model.ParamOptions{
		From: "ports",
	}
	raw := map[string]any{
		"ports": []any{8000, 8001, 8002},
	}

	result, err := Options(opts, raw)
	require.NoError(t, err)
	require.Len(t, result, 3)
	require.Equal(t, "8000", result[0].Value)
	require.Equal(t, "8001", result[1].Value)
	require.Equal(t, "8002", result[2].Value)
}

func TestResolveOptions_FromFloatList(t *testing.T) {
	opts := &model.ParamOptions{
		From: "versions",
	}
	raw := map[string]any{
		"versions": []any{1.0, 1.5, 2.0},
	}

	result, err := Options(opts, raw)
	require.NoError(t, err)
	require.Len(t, result, 3)
	require.Equal(t, "1", result[0].Value)
}

func TestResolveOptions_FromBoolList(t *testing.T) {
	opts := &model.ParamOptions{
		From: "flags",
	}
	raw := map[string]any{
		"flags": []any{true, false},
	}

	result, err := Options(opts, raw)
	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Equal(t, "true", result[0].Value)
	require.Equal(t, "false", result[1].Value)
}

func TestResolveOptions_FromSingleString(t *testing.T) {
	opts := &model.ParamOptions{
		From: "single",
	}
	raw := map[string]any{
		"single": "value",
	}

	result, err := Options(opts, raw)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "value", result[0].Value)
	require.Equal(t, "value", result[0].Label)
}

func TestResolveOptions_FromEmptyList(t *testing.T) {
	opts := &model.ParamOptions{
		From: "empty",
	}
	raw := map[string]any{
		"empty": []any{},
	}

	result, err := Options(opts, raw)
	require.NoError(t, err)
	require.Len(t, result, 0)
}

func TestResolveOptions_MixedListError(t *testing.T) {
	opts := &model.ParamOptions{
		From: "mixed",
	}
	raw := map[string]any{
		"mixed": []any{
			"string",
			map[string]any{"value": "map"},
		},
	}

	_, err := Options(opts, raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "mixed scalar and non-scalar")
}

func TestResolveOptions_SingleInt(t *testing.T) {
	// Single integer is treated as a one-element list.
	opts := &model.ParamOptions{
		From: "port",
	}
	raw := map[string]any{
		"port": 8080,
	}

	result, err := Options(opts, raw)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "8080", result[0].Value)
}

func TestResolveOptions_InvalidTypeError(t *testing.T) {
	// Use a truly unsupported type (e.g., nested structure that's not a map[string]any).
	opts := &model.ParamOptions{
		From: "invalid",
	}

	type CustomType struct {
		Value string
	}

	raw := map[string]any{
		"invalid": &CustomType{Value: "test"}, // Pointer to custom struct
	}

	_, err := Options(opts, raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected list or map")
}

func TestResolveOptions_ListWithComplexMapError(t *testing.T) {
	opts := &model.ParamOptions{
		From: "items",
	}
	// Mix strings with map inside — should catch at second element.
	raw := map[string]any{
		"items": []any{
			"first",
			map[string]any{"value": "second"},
		},
	}

	_, err := Options(opts, raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "mixed scalar and non-scalar")
}
