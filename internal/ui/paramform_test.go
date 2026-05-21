package ui

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	huh "charm.land/huh/v2"
)

func withStubRunForm(t *testing.T, stub func(*huh.Form) error) {
	t.Helper()
	orig := runFormFn
	t.Cleanup(func() { runFormFn = orig })
	runFormFn = stub
}

// TestBuildParamForm_InvalidPattern verifies bad regex surfaces as an error
// (never panics) so a malformed user-authored pattern can't crash the CLI.
func TestBuildParamForm_InvalidPattern(t *testing.T) {
	_, _, err := BuildParamForm("title", []ParamField{
		{Name: "x", Type: FieldTypeString, Pattern: "[invalid"},
	})
	if err == nil {
		t.Fatal("expected error for invalid pattern")
	}
	if !strings.Contains(err.Error(), "invalid pattern") {
		t.Errorf("expected wrap with 'invalid pattern', got %v", err)
	}
	if !strings.Contains(err.Error(), `"x"`) {
		t.Errorf("expected param name in error, got %v", err)
	}
}

// TestBuildParamForm_BoolDefaultsToFalse verifies bool prefill is normalized
// — a missing/non-bool default becomes "false" so the safe option wins.
func TestBuildParamForm_BoolDefaultsToFalse(t *testing.T) {
	_, bindings, err := BuildParamForm("t", []ParamField{
		{Name: "yolo", Type: FieldTypeBool},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if *bindings[0].ptr != "false" {
		t.Errorf("expected bool default 'false', got %q", *bindings[0].ptr)
	}
}

// TestBuildParamForm_BoolNormalizesAlternateForms verifies that non-canonical
// ParseBool-accepted values are normalized to "true"/"false" so the select
// highlights the correct option and the form result matches runtime coercion.
func TestBuildParamForm_BoolNormalizesAlternateForms(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "1 becomes true", input: "1", want: "true"},
		{name: "0 becomes false", input: "0", want: "false"},
		{name: "T becomes true", input: "T", want: "true"},
		{name: "F becomes false", input: "F", want: "false"},
		{name: "empty becomes false", input: "", want: "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, bindings, err := BuildParamForm("t", []ParamField{
				{Name: "flag", Type: FieldTypeBool, Default: tt.input},
			})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if *bindings[0].ptr != tt.want {
				t.Errorf("input %q: got %q want %q", tt.input, *bindings[0].ptr, tt.want)
			}
		})
	}
}

// TestBuildParamForm_BoolErrorsOnInvalidPrefill verifies that a non-empty
// prefill that cannot be parsed as bool returns an error instead of silently
// coercing to "false". This keeps interactive and non-interactive paths
// consistent: resolve.coerceParam also returns an error for unparseable bools.
func TestBuildParamForm_BoolErrorsOnInvalidPrefill(t *testing.T) {
	_, _, err := BuildParamForm("t", []ParamField{
		{Name: "flag", Type: FieldTypeBool, Default: "garbage"},
	})
	if err == nil {
		t.Fatal("expected error for invalid bool prefill, got nil")
	}
	if !strings.Contains(err.Error(), `"garbage"`) {
		t.Errorf("expected offending value in error, got %v", err)
	}
	if !strings.Contains(err.Error(), `"flag"`) {
		t.Errorf("expected param name in error, got %v", err)
	}
}

// TestBuildParamForm_BoolHonorsPrefill verifies an explicit "true" default
// flows into the bound pointer so huh selects the matching option.
func TestBuildParamForm_BoolHonorsPrefill(t *testing.T) {
	_, bindings, err := BuildParamForm("t", []ParamField{
		{Name: "yolo", Type: FieldTypeBool, Default: "true"},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if *bindings[0].ptr != "true" {
		t.Errorf("expected bool default 'true', got %q", *bindings[0].ptr)
	}
}

// TestRunParamForm_ReturnsBoundValues verifies bindings carry through into
// the returned map.
func TestRunParamForm_ReturnsBoundValues(t *testing.T) {
	withStubRunForm(t, func(f *huh.Form) error { return nil })

	values, err := RunParamForm("title", []ParamField{
		{Name: "task", Type: FieldTypeString, Default: "deploy"},
		{Name: "count", Type: FieldTypeInt, Default: "3"},
		{Name: "wet", Type: FieldTypeBool, Default: "true"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if values["task"] != "deploy" {
		t.Errorf("task: got %q", values["task"])
	}
	if values["count"] != "3" {
		t.Errorf("count: got %q", values["count"])
	}
	if values["wet"] != "true" {
		t.Errorf("wet: got %q", values["wet"])
	}
}

// TestRunParamForm_OptionalBoolNoDefault verifies the safe-default rule: an
// optional bool with no Default/--set yields "false" in the result map.
func TestRunParamForm_OptionalBoolNoDefault(t *testing.T) {
	withStubRunForm(t, func(f *huh.Form) error { return nil })

	values, err := RunParamForm("title", []ParamField{
		{Name: "dry", Type: FieldTypeBool},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if values["dry"] != "false" {
		t.Errorf("expected dry=false, got %q", values["dry"])
	}
}

// TestRunParamForm_Cancel verifies huh.ErrUserAborted maps to ErrCancelled.
func TestRunParamForm_Cancel(t *testing.T) {
	withStubRunForm(t, func(f *huh.Form) error { return huh.ErrUserAborted })

	_, err := RunParamForm("title", []ParamField{
		{Name: "x", Type: FieldTypeString},
	})
	if !errors.Is(err, ErrCancelled) {
		t.Errorf("expected ErrCancelled, got %v", err)
	}
}

// TestRunParamForm_GenericError verifies non-abort errors propagate.
func TestRunParamForm_GenericError(t *testing.T) {
	sentinel := errors.New("boom")
	withStubRunForm(t, func(f *huh.Form) error { return sentinel })

	_, err := RunParamForm("title", []ParamField{
		{Name: "x", Type: FieldTypeString},
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel, got %v", err)
	}
}

// TestValidators verifies the per-field validators produced by combineValidators.
func TestValidators(t *testing.T) {
	tests := []struct {
		name    string
		field   ParamField
		input   string
		wantErr bool
		extra   func(string) error
	}{
		{name: "required empty errors", field: ParamField{Name: "x", Required: true}, input: "", wantErr: true},
		{name: "required filled ok", field: ParamField{Name: "x", Required: true}, input: "ok"},
		{name: "optional empty ok", field: ParamField{Name: "x"}, input: ""},
		{name: "pattern mismatch errors", field: ParamField{Name: "x", Pattern: `^\d+$`}, input: "abc", wantErr: true},
		{name: "pattern match ok", field: ParamField{Name: "x", Pattern: `^\d+$`}, input: "42"},
		{name: "int extra rejects non-digit", field: ParamField{Name: "x"}, input: "abc", wantErr: true, extra: validateInt},
		{name: "int extra accepts digits", field: ParamField{Name: "x"}, input: "42", extra: validateInt},
		{name: "optional empty skips pattern", field: ParamField{Name: "x", Pattern: `^\d+$`}, input: ""},
		{name: "partial match rejected by full-string check", field: ParamField{Name: "x", Pattern: `\d+`}, input: "abc123", wantErr: true},
		{name: "full string match accepted", field: ParamField{Name: "x", Pattern: `\d+`}, input: "123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var re *regexp.Regexp
			if tt.field.Pattern != "" {
				re = regexp.MustCompile(tt.field.Pattern)
			}
			v := combineValidators(tt.field, re, tt.extra)
			err := v(tt.input)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestBuildParamForm_InvalidPatternIgnoredForNonStringTypes verifies that an
// invalid regex on an int or bool field does not cause BuildParamForm to error.
// resolve.go:54 skips pattern validation for non-string types, so the form must
// match: compiling a pattern that would never be used is an unintended failure.
func TestBuildParamForm_InvalidPatternIgnoredForNonStringTypes(t *testing.T) {
	tests := []struct {
		name      string
		fieldType ParamFieldType
	}{
		{name: "int", fieldType: FieldTypeInt},
		{name: "bool", fieldType: FieldTypeBool},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := BuildParamForm("title", []ParamField{
				{Name: "x", Type: tt.fieldType, Pattern: "[invalid"},
			})
			if err != nil {
				t.Errorf("expected no error for invalid pattern on %s field, got %v", tt.name, err)
			}
		})
	}
}

// TestBuildParamForm_IntIgnoresPattern verifies that a pattern on an int field
// does not produce a validator that rejects otherwise-valid integers. Runtime
// (resolve.go:54) skips pattern validation for int; the form must match.
func TestBuildParamForm_IntIgnoresPattern(t *testing.T) {
	withStubRunForm(t, func(f *huh.Form) error { return nil })

	// Pattern ^[1-9]\d*$ would reject "0" and negative ints. The form should
	// accept "0" regardless because int pattern checks are not enforced.
	values, err := RunParamForm("title", []ParamField{
		{Name: "port", Type: FieldTypeInt, Pattern: `^[1-9]\d*$`, Default: "0"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if values["port"] != "0" {
		t.Errorf("expected port=0 to pass through, got %q", values["port"])
	}
}

// TestDisplayTitle verifies the * marker is added only for required fields.
func TestDisplayTitle(t *testing.T) {
	tests := []struct {
		name string
		in   ParamField
		want string
	}{
		{name: "optional", in: ParamField{Name: "x"}, want: "x"},
		{name: "required", in: ParamField{Name: "x", Required: true}, want: "x *"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := displayTitle(tt.in); got != tt.want {
				t.Errorf("got %q want %q", got, tt.want)
			}
		})
	}
}
