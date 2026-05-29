package spec

import "testing"

func TestGetStringSlice_Nil(t *testing.T) {
	result, err := GetStringSlice(nil, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for nil with, got %v", result)
	}
}

func TestGetStringSlice_MissingKey(t *testing.T) {
	result, err := GetStringSlice(map[string]any{}, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for missing key, got %v", result)
	}
}

func TestGetStringSlice_NilValue(t *testing.T) {
	result, err := GetStringSlice(map[string]any{"key": nil}, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for nil value, got %v", result)
	}
}

func TestGetStringSlice_StringSlice(t *testing.T) {
	result, err := GetStringSlice(map[string]any{"key": []string{"a", "b"}}, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 || result[0] != "a" || result[1] != "b" {
		t.Errorf("unexpected result: %v", result)
	}
}

func TestGetStringSlice_AnySlice(t *testing.T) {
	result, err := GetStringSlice(map[string]any{"key": []any{"x", "y", 42}}, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 || result[2] != "42" {
		t.Errorf("unexpected result: %v", result)
	}
}

func TestGetStringSlice_SingleString(t *testing.T) {
	result, err := GetStringSlice(map[string]any{"key": "hello"}, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 || result[0] != "hello" {
		t.Errorf("unexpected result: %v", result)
	}
}

func TestGetStringSlice_EmptyString(t *testing.T) {
	result, err := GetStringSlice(map[string]any{"key": ""}, "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for empty string, got %v", result)
	}
}

func TestGetStringSlice_InvalidType(t *testing.T) {
	_, err := GetStringSlice(map[string]any{"key": 123}, "key")
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
}
