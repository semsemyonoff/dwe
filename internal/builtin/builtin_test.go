package builtin

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

// --- IsInteractive ---

func TestIsInteractive(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"confirm", true},
		{"docker_daemon_logs", true},
		{"message", false},
		{"docker_daemon_start", false},
		{"docker_daemon_stop", false},
		{"service_dirs_ensure", false},
		{"unknown_builtin_xyz", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsInteractive(tc.name); got != tc.want {
				t.Errorf("IsInteractive(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

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
	err := Validate("nonexistent_builtin", nil, CtxUserYAML)
	if err == nil {
		t.Fatal("expected error for unknown builtin")
	}
	if !strings.Contains(err.Error(), "nonexistent_builtin") {
		t.Errorf("error should mention the unknown name, got: %v", err)
	}
}

func TestValidate_KnownBuiltin_NoParams(t *testing.T) {
	// confirm builtin has no required params.
	err := Validate("confirm", nil, CtxUserYAML)
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
	err := Run(context.Background(), "nonexistent_builtin", nil, ctx, CtxUserYAML)
	if err == nil {
		t.Fatal("expected error for unknown builtin")
	}
	if !strings.Contains(err.Error(), "nonexistent_builtin") {
		t.Errorf("error should mention the unknown name, got: %v", err)
	}
}

// --- Kind categorization and CallerContext gating ---

// TestKindCategorization verifies that every registered builtin
// has the expected kind, and that kind/context gating works as intended.
// This is the single source of truth for the 18-entry registry categorization.
func TestKindCategorization(t *testing.T) {
	type kindCase struct {
		name        string
		kind        Kind
		userYAMLOK  bool // CtxUserYAML allowed?
		predicateOK bool // CtxPredicate allowed?
		internalOK  bool // CtxInternal allowed?
	}
	cases := []kindCase{
		// KindAction: allowed in body (CtxUserYAML) and check: (CtxPredicate)
		{"confirm", KindAction, true, true, false},
		{"message", KindAction, true, true, false},
		{"service_configs_copy", KindAction, true, true, false},
		{"service_configs_check", KindAction, true, true, false},
		{"service_dirs_ensure", KindAction, true, true, false},
		{"docker_remove_project_volumes", KindAction, true, true, false},
		{"docker_wait_healthy", KindAction, true, true, false},
		{"remove_paths", KindAction, true, true, false},
		// KindPredicate: only in check: (CtxPredicate) — NEVER in step body
		{"containers_running", KindPredicate, false, true, false},
		{"shell", KindPredicate, false, true, false},
		{"file_exists", KindPredicate, false, true, false},
		{"executable_in_path", KindPredicate, false, true, false},
		{"env_keys_present", KindPredicate, false, true, false},
		{"tcp_reachable", KindPredicate, false, true, false},
		// KindInternal: only engine-synthetic contexts (CtxInternal)
		{"docker_daemon_start", KindInternal, false, false, true},
		{"docker_daemon_logs", KindInternal, false, false, true},
		{"docker_daemon_stop", KindInternal, false, false, true},
		{"daemons_reap", KindInternal, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry, ok := registry[tc.name]
			if !ok {
				t.Fatalf("builtin %q not in registry", tc.name)
			}
			if entry.kind != tc.kind {
				t.Errorf("kind = %v, want %v", entry.kind, tc.kind)
			}
			// Verify each context combination
			_, gotUserYAML := Get(tc.name, CtxUserYAML)
			if gotUserYAML != tc.userYAMLOK {
				t.Errorf("Get(%q, CtxUserYAML) = %v, want %v", tc.name, gotUserYAML, tc.userYAMLOK)
			}
			_, gotPredicate := Get(tc.name, CtxPredicate)
			if gotPredicate != tc.predicateOK {
				t.Errorf("Get(%q, CtxPredicate) = %v, want %v", tc.name, gotPredicate, tc.predicateOK)
			}
			_, gotInternal := Get(tc.name, CtxInternal)
			if gotInternal != tc.internalOK {
				t.Errorf("Get(%q, CtxInternal) = %v, want %v", tc.name, gotInternal, tc.internalOK)
			}
		})
	}
}

// TestGetKindMismatch verifies that kind-incompatible Get calls return (nil, false)
// and that Validate returns descriptive errors for context mismatches.
func TestGetKindMismatch(t *testing.T) {
	t.Run("internal builtin rejected from user YAML", func(t *testing.T) {
		b, ok := Get("docker_daemon_start", CtxUserYAML)
		if ok || b != nil {
			t.Error("docker_daemon_start must not be callable from CtxUserYAML")
		}
		err := Validate("docker_daemon_start", nil, CtxUserYAML)
		if err == nil {
			t.Fatal("expected error for internal builtin in user YAML context")
		}
		if !strings.Contains(err.Error(), "engine-internal") {
			t.Errorf("error should mention engine-internal, got: %v", err)
		}
	})

	t.Run("predicate builtin rejected from step body", func(t *testing.T) {
		b, ok := Get("containers_running", CtxUserYAML)
		if ok || b != nil {
			t.Error("containers_running must not be callable from CtxUserYAML (body position)")
		}
		err := Validate("containers_running", nil, CtxUserYAML)
		if err == nil {
			t.Fatal("expected error for predicate builtin in body context")
		}
		if !strings.Contains(err.Error(), "predicate") {
			t.Errorf("error should mention predicate, got: %v", err)
		}
	})

	t.Run("predicate builtin allowed in check position", func(t *testing.T) {
		b, ok := Get("containers_running", CtxPredicate)
		if !ok || b == nil {
			t.Error("containers_running must be callable from CtxPredicate (check: position)")
		}
	})

	t.Run("internal builtin rejected from check position", func(t *testing.T) {
		b, ok := Get("daemons_reap", CtxPredicate)
		if ok || b != nil {
			t.Error("daemons_reap must not be callable from CtxPredicate")
		}
	})

	t.Run("internal builtin allowed in internal context", func(t *testing.T) {
		b, ok := Get("daemons_reap", CtxInternal)
		if !ok || b == nil {
			t.Error("daemons_reap must be callable from CtxInternal")
		}
	})
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
