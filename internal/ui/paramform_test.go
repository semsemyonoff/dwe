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
