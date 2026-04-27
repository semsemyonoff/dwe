package builtin

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

// --- knownNames ---

func TestKnownNames_AllRegistered(t *testing.T) {
	names := knownNames()
	if len(names) == 0 {
		t.Fatal("expected at least one registered builtin")
	}
	// names must be sorted
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("knownNames not sorted: %v", names)
			break
		}
	}
	for _, expected := range []string{"confirm", "message", "service_dirs_ensure"} {
		if !slices.Contains(names, expected) {
			t.Errorf("expected %q in knownNames, got: %v", expected, names)
		}
	}
}

// --- Validate dispatcher ---

func TestValidate_UnknownBuiltin(t *testing.T) {
	err := Validate("nonexistent_builtin", nil)
	if err == nil {
		t.Fatal("expected error for unknown builtin")
	}
	if !strings.Contains(err.Error(), "nonexistent_builtin") {
		t.Errorf("error should mention the unknown name, got: %v", err)
	}
}

func TestValidate_KnownBuiltin_NoParams(t *testing.T) {
	// confirm builtin has no required params.
	err := Validate("confirm", nil)
	if err != nil {
		t.Errorf("unexpected error for confirm with nil params: %v", err)
	}
}

// --- Describe dispatcher ---

func TestDescribe_UnknownBuiltin(t *testing.T) {
	desc := Describe("nonexistent_builtin", nil)
	if !strings.Contains(desc, "nonexistent_builtin") {
		t.Errorf("Describe should mention unknown name, got: %q", desc)
	}
}

func TestDescribe_KnownBuiltin(t *testing.T) {
	desc := Describe("confirm", nil)
	if desc == "" {
		t.Error("expected non-empty describe for confirm builtin")
	}
}

// --- Run dispatcher ---

func TestRun_UnknownBuiltin(t *testing.T) {
	ctx := ExecContext{
		Config:      &config.DevboxConfig{},
		ProjectRoot: t.TempDir(),
		Output:      render.NewWriter(&bytes.Buffer{}),
	}
	err := Run("nonexistent_builtin", nil, ctx)
	if err == nil {
		t.Fatal("expected error for unknown builtin")
	}
	if !strings.Contains(err.Error(), "nonexistent_builtin") {
		t.Errorf("error should mention the unknown name, got: %v", err)
	}
}

// --- getStringSlice ---

func TestGetStringSlice_Nil(t *testing.T) {
	result, err := getStringSlice(nil, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for nil with, got %v", result)
	}
}

func TestGetStringSlice_MissingKey(t *testing.T) {
	result, err := getStringSlice(map[string]any{}, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for missing key, got %v", result)
	}
}

func TestGetStringSlice_NilValue(t *testing.T) {
	result, err := getStringSlice(map[string]any{"key": nil}, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for nil value, got %v", result)
	}
}

func TestGetStringSlice_StringSlice(t *testing.T) {
	result, err := getStringSlice(map[string]any{"key": []string{"a", "b"}}, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 || result[0] != "a" || result[1] != "b" {
		t.Errorf("unexpected result: %v", result)
	}
}

func TestGetStringSlice_AnySlice(t *testing.T) {
	result, err := getStringSlice(map[string]any{"key": []any{"x", "y", 42}}, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 || result[2] != "42" {
		t.Errorf("unexpected result: %v", result)
	}
}

func TestGetStringSlice_SingleString(t *testing.T) {
	result, err := getStringSlice(map[string]any{"key": "hello"}, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 || result[0] != "hello" {
		t.Errorf("unexpected result: %v", result)
	}
}

func TestGetStringSlice_EmptyString(t *testing.T) {
	result, err := getStringSlice(map[string]any{"key": ""}, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for empty string, got %v", result)
	}
}

func TestGetStringSlice_InvalidType(t *testing.T) {
	_, err := getStringSlice(map[string]any{"key": 123}, "key")
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
}
