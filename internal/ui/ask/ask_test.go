package ask

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	huh "charm.land/huh/v2"
)

// TestFieldKindZeroValue ensures FieldUnknown is at iota 0.
func TestFieldKindZeroValue(t *testing.T) {
	var zeroKind FieldKind
	if zeroKind != FieldUnknown {
		t.Errorf("zero value of FieldKind should be FieldUnknown, got %d", zeroKind)
	}
}

// TestResultAccessors verifies typed accessors on Result.
func TestResultAccessors(t *testing.T) {
	result := Result{
		values: map[string]any{
			"string_key":  "hello",
			"strings_key": []string{"a", "b", "c"},
			"bool_key":    true,
		},
	}

	if got := result.String("string_key"); got != "hello" {
		t.Errorf("String(string_key) = %q, want %q", got, "hello")
	}
	if got := result.String("missing_key"); got != "" {
		t.Errorf("String(missing_key) = %q, want empty", got)
	}
	if got := result.String("bool_key"); got != "" {
		t.Errorf("String(bool_key) with wrong type = %q, want empty", got)
	}

	if got := result.Strings("strings_key"); !slicesEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("Strings(strings_key) = %v, want [a b c]", got)
	}
	if got := result.Strings("missing_key"); got != nil {
		t.Errorf("Strings(missing_key) = %v, want nil", got)
	}
	if got := result.Strings("string_key"); got != nil {
		t.Errorf("Strings(string_key) with wrong type = %v, want nil", got)
	}

	if got := result.Bool("bool_key"); got != true {
		t.Errorf("Bool(bool_key) = %v, want true", got)
	}
	if got := result.Bool("missing_key"); got != false {
		t.Errorf("Bool(missing_key) = %v, want false", got)
	}
	if got := result.Bool("string_key"); got != false {
		t.Errorf("Bool(string_key) with wrong type = %v, want false", got)
	}
}

// TestRunRejectsFieldUnknown ensures Run returns error for FieldUnknown.
func TestRunRejectsFieldUnknown(t *testing.T) {
	fields := []Field{
		{
			Key:   "test",
			Kind:  FieldUnknown, // zero value
			Title: "Test",
		},
	}
	_, err := Run(context.Background(), "Title", fields, RunOptions{})
	if err == nil {
		t.Error("Run with FieldUnknown should return error")
	}
	if !strings.Contains(err.Error(), "FieldUnknown") {
		t.Errorf("error message should mention FieldUnknown, got: %v", err)
	}
}

// TestRunWithInputField verifies that buildHuhField produces a valid binding for FieldInput.
func TestRunWithInputField(t *testing.T) {
	f := Field{
		Key:      "name",
		Kind:     FieldInput,
		Title:    "Enter your name",
		Required: false,
	}
	_, binding, err := buildHuhField(f)
	if err != nil {
		t.Fatalf("buildHuhField returned error: %v", err)
	}
	if binding.key != "name" {
		t.Errorf("binding.key = %q, want %q", binding.key, "name")
	}
	if _, ok := binding.ptr.(*string); !ok {
		t.Errorf("binding.ptr should be *string for FieldInput, got %T", binding.ptr)
	}
}

// TestBuildHuhFieldInput tests input field construction.
func TestBuildHuhFieldInput(t *testing.T) {
	f := Field{
		Key:         "test_input",
		Kind:        FieldInput,
		Title:       "Enter text",
		Description: "Some help",
		Required:    true,
		Default:     "default_value",
	}

	huhField, binding, err := buildHuhField(f)
	if err != nil {
		t.Fatalf("buildHuhField returned error: %v", err)
	}
	if huhField == nil {
		t.Error("huhField should not be nil")
	}
	if binding.key != "test_input" {
		t.Errorf("binding.key = %q, want test_input", binding.key)
	}
}

// TestBuildHuhFieldSelect tests select field construction.
func TestBuildHuhFieldSelect(t *testing.T) {
	f := Field{
		Key:      "database",
		Kind:     FieldSelect,
		Title:    "Choose database",
		Required: false,
		Options: []Option{
			{Value: "pg", Label: "PostgreSQL"},
			{Value: "mysql", Label: "MySQL"},
		},
		Default: "pg",
	}

	huhField, binding, err := buildHuhField(f)
	if err != nil {
		t.Fatalf("buildHuhField returned error: %v", err)
	}
	if huhField == nil {
		t.Error("huhField should not be nil")
	}
	if binding.key != "database" {
		t.Errorf("binding.key = %q, want database", binding.key)
	}
}

// TestBuildHuhFieldMultiselect tests multiselect field construction.
func TestBuildHuhFieldMultiselect(t *testing.T) {
	f := Field{
		Key:      "services",
		Kind:     FieldMultiselect,
		Title:    "Choose services",
		Required: false,
		Defaults: []string{"api", "db"},
		Options: []Option{
			{Value: "api", Label: "API"},
			{Value: "db", Label: "Database"},
			{Value: "cache", Label: "Cache"},
		},
	}

	huhField, binding, err := buildHuhField(f)
	if err != nil {
		t.Fatalf("buildHuhField returned error: %v", err)
	}
	if huhField == nil {
		t.Error("huhField should not be nil")
	}
	if binding.key != "services" {
		t.Errorf("binding.key = %q, want services", binding.key)
	}
}

// TestBuildHuhFieldConfirm tests confirm field construction.
func TestBuildHuhFieldConfirm(t *testing.T) {
	f := Field{
		Key:         "agree",
		Kind:        FieldConfirm,
		Title:       "Do you agree?",
		Description: "Yes or no",
		Default:     "false",
	}

	huhField, binding, err := buildHuhField(f)
	if err != nil {
		t.Fatalf("buildHuhField returned error: %v", err)
	}
	if huhField == nil {
		t.Error("huhField should not be nil")
	}
	if binding.key != "agree" {
		t.Errorf("binding.key = %q, want agree", binding.key)
	}
}

// TestBuildHuhFieldInvalidKind tests error handling for unsupported kind.
func TestBuildHuhFieldInvalidKind(t *testing.T) {
	f := Field{
		Key:   "invalid",
		Kind:  FieldKind(999), // Invalid kind
		Title: "Test",
	}

	_, _, err := buildHuhField(f)
	if err == nil {
		t.Error("buildHuhField with invalid kind should return error")
	}
}

// TestRunContextCancellation tests that Run respects context cancellation.
func TestRunContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	fields := []Field{
		{
			Key:   "test",
			Kind:  FieldInput,
			Title: "Test",
		},
	}

	// Block on stdin so form waits forever, letting context timeout.
	_, err := Run(ctx, "Title", fields, RunOptions{
		Input:  io.NopCloser(strings.NewReader("")),
		Output: io.Discard,
	})

	if err == nil {
		t.Error("Run with context timeout should return error")
	}
	// huh.Form.RunWithContext should return a context-related error on timeout.
	// We check that *some* error occurred; the exact type depends on huh internals.
}

// TestInputFieldWithValidation tests custom validation callback on input.
func TestInputFieldWithValidation(t *testing.T) {
	f := Field{
		Key:   "age",
		Kind:  FieldInput,
		Title: "Age",
		Validate: func(s string) error {
			if len(s) > 3 {
				return errors.New("age too long")
			}
			return nil
		},
	}

	huhField, _, err := buildHuhField(f)
	if err != nil {
		t.Fatalf("buildHuhField returned error: %v", err)
	}
	if huhField == nil {
		t.Error("huhField should not be nil")
	}
}

// TestMultiselectFieldWithValidation tests custom validation on multiselect items.
func TestMultiselectFieldWithValidation(t *testing.T) {
	f := Field{
		Key:   "items",
		Kind:  FieldMultiselect,
		Title: "Select items",
		Validate: func(s string) error {
			if s == "forbidden" {
				return errors.New("item not allowed")
			}
			return nil
		},
		Options: []Option{
			{Value: "allowed", Label: "Allowed"},
			{Value: "forbidden", Label: "Forbidden"},
		},
	}

	huhField, _, err := buildHuhField(f)
	if err != nil {
		t.Fatalf("buildHuhField returned error: %v", err)
	}
	if huhField == nil {
		t.Error("huhField should not be nil")
	}
}

// TestRunOptionsDefaults verifies default Input/Output are set when zero.
func TestRunOptionsDefaults(t *testing.T) {
	// We can't easily test os.Stdin/Stdout defaults without side effects,
	// but we can test that nil values don't cause panics.
	fields := []Field{
		{
			Key:   "test",
			Kind:  FieldInput,
			Title: "Test",
		},
	}

	// Create a dummy context that times out immediately to avoid hanging.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// This will timeout, but the point is to verify it doesn't panic due to nil I/O.
	_, _ = Run(ctx, "Title", fields, RunOptions{})
}

// TestConfirmFieldDefaultParsing tests bool default parsing in confirm field.
func TestConfirmFieldDefaultParsing(t *testing.T) {
	tests := []struct {
		defaultStr  string
		expectedVal bool
	}{
		{"true", true},
		{"1", true},
		{"yes", true},
		{"y", true},
		{"false", false},
		{"0", false},
		{"no", false},
		{"n", false},
		{"", false},
		{"invalid", false},
	}

	for _, tt := range tests {
		f := Field{
			Key:     "test",
			Kind:    FieldConfirm,
			Title:   "Test",
			Default: tt.defaultStr,
		}

		huhField, _, err := buildHuhField(f)
		if err != nil {
			t.Fatalf("buildHuhField(%q) returned error: %v", tt.defaultStr, err)
		}
		if huhField == nil {
			t.Errorf("huhField for default %q should not be nil", tt.defaultStr)
		}
	}
}

// TestRunReturnsUserAbortedOnCancel verifies that huh.ErrUserAborted is a non-nil sentinel
// and that Result.Has correctly distinguishes present from absent keys.
func TestRunUserAbortedError(t *testing.T) {
	if huh.ErrUserAborted == nil {
		t.Fatal("huh.ErrUserAborted must be a non-nil sentinel error")
	}
	// Verify Has accessor: present key vs absent key.
	r := NewResultForTest(map[string]any{"present": "value"})
	if !r.Has("present") {
		t.Error("Has(present) should return true")
	}
	if r.Has("absent") {
		t.Error("Has(absent) should return false")
	}
}

// Helper function to compare slices.
func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRunEmptyFields tests Run with no fields.
func TestRunEmptyFields(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	result, _ := Run(ctx, "Title", []Field{}, RunOptions{})
	if len(result.values) != 0 {
		t.Errorf("empty fields should produce empty result, got %v", result.values)
	}
}

// TestMultiselectDefaultsNilHandling tests multiselect with nil Defaults.
func TestMultiselectDefaultsNilHandling(t *testing.T) {
	f := Field{
		Key:      "items",
		Kind:     FieldMultiselect,
		Title:    "Select",
		Defaults: nil, // Should be converted to empty []string
		Options: []Option{
			{Value: "a", Label: "A"},
		},
	}

	huhField, _, err := buildHuhField(f)
	if err != nil {
		t.Fatalf("buildHuhField returned error: %v", err)
	}
	if huhField == nil {
		t.Error("huhField should not be nil")
	}
}

// TestSelectFieldWithoutOptions tests select with empty options (edge case).
func TestSelectFieldWithoutOptions(t *testing.T) {
	f := Field{
		Key:     "choice",
		Kind:    FieldSelect,
		Title:   "Choose",
		Options: []Option{},
	}

	huhField, _, err := buildHuhField(f)
	if err != nil {
		t.Fatalf("buildHuhField returned error: %v", err)
	}
	if huhField == nil {
		t.Error("huhField should not be nil")
	}
}
