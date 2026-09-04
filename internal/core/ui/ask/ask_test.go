package ask

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
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
	_, binding, err := buildHuhField(f, false)
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

	huhField, binding, err := buildHuhField(f, false)
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

	huhField, binding, err := buildHuhField(f, false)
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

	huhField, binding, err := buildHuhField(f, false)
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

	huhField, binding, err := buildHuhField(f, false)
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

// TestBuildHuhFieldConfirmButtonLabels pins the optional button labels: set
// ones reach the rendered field, and empty ones leave huh's own Yes/No alone —
// which is what keeps every pre-existing FieldConfirm site byte-identical.
func TestBuildHuhFieldConfirmButtonLabels(t *testing.T) {
	custom, _, err := buildHuhField(Field{
		Key: "import", Kind: FieldConfirm, Title: "Enter the key?",
		Affirmative: "Enter key", Negative: "Abort",
	}, false)
	if err != nil {
		t.Fatalf("buildHuhField returned error: %v", err)
	}
	view := custom.View()
	if !strings.Contains(view, "Enter key") || !strings.Contains(view, "Abort") {
		t.Errorf("view = %q, want the custom button labels", view)
	}

	plain, _, err := buildHuhField(Field{Key: "agree", Kind: FieldConfirm, Title: "Agree?"}, false)
	if err != nil {
		t.Fatalf("buildHuhField returned error: %v", err)
	}
	view = plain.View()
	if !strings.Contains(view, "Yes") || !strings.Contains(view, "No") {
		t.Errorf("view = %q, want huh's default Yes/No buttons", view)
	}
}

// TestBuildHuhFieldInvalidKind tests error handling for unsupported kind.
func TestBuildHuhFieldInvalidKind(t *testing.T) {
	f := Field{
		Key:   "invalid",
		Kind:  FieldKind(999), // Invalid kind
		Title: "Test",
	}

	_, _, err := buildHuhField(f, false)
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

	huhField, _, err := buildHuhField(f, false)
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

	huhField, _, err := buildHuhField(f, false)
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

		huhField, _, err := buildHuhField(f, false)
		if err != nil {
			t.Fatalf("buildHuhField(%q) returned error: %v", tt.defaultStr, err)
		}
		if huhField == nil {
			t.Errorf("huhField for default %q should not be nil", tt.defaultStr)
		}
	}
}

// TestResultHasDistinguishesPresentFromAbsent verifies Result.Has
// distinguishes present from absent keys.
func TestResultHasDistinguishesPresentFromAbsent(t *testing.T) {
	r := NewResultForTest(map[string]any{"present": "value"})
	if !r.Has("present") {
		t.Error("Has(present) should return true")
	}
	if r.Has("absent") {
		t.Error("Has(absent) should return false")
	}
}

// TestFormRunCancelReturnsErrCancelled verifies that (*Form).Run — and by
// extension the top-level Run (Build+Form.Run) — returns widgets.ErrCancelled
// on user abort, not the raw huh.ErrUserAborted sentinel. Mirrors
// widgets.TestRunHuhForm_AbortTranslatesToErrCancelled: queue a ctrl+c
// keypress on the built huh.Form, then cancel the context so
// RunWithContext exits immediately with ErrUserAborted.
func TestFormRunCancelReturnsErrCancelled(t *testing.T) {
	fields := []Field{{Key: "test", Kind: FieldInput, Title: "Test"}}
	form, err := Build("Title", fields, RunOptions{Output: io.Discard})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	form.Huh().Update(tea.KeyPressMsg(tea.Key{Mod: tea.ModCtrl, Code: 'c'}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = form.Run(ctx)
	if !errors.Is(err, widgets.ErrCancelled) {
		t.Fatalf("Form.Run() error = %v, want widgets.ErrCancelled", err)
	}
	if errors.Is(err, huh.ErrUserAborted) {
		t.Error("Form.Run must not leak the raw huh.ErrUserAborted sentinel")
	}
}

// TestBuildDoesNotRun verifies Build constructs a runnable Form without
// blocking or executing it (no I/O is driven until Form.Run is called).
func TestBuildDoesNotRun(t *testing.T) {
	fields := []Field{{Key: "test", Kind: FieldInput, Title: "Test"}}
	form, err := Build("Title", fields, RunOptions{Output: io.Discard})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if form == nil {
		t.Fatal("Build returned nil Form")
	}
	if form.Huh() == nil {
		t.Error("Form.Huh() should expose the underlying huh.Form")
	}
	if len(form.bindings) != 1 {
		t.Errorf("Form should carry 1 binding, got %d", len(form.bindings))
	}
}

// TestBuildRejectsFieldUnknown verifies Build (not just the top-level Run)
// rejects a Field with the zero-value FieldUnknown kind.
func TestBuildRejectsFieldUnknown(t *testing.T) {
	fields := []Field{{Key: "test", Kind: FieldUnknown, Title: "Test"}}
	form, err := Build("Title", fields, RunOptions{})
	if err == nil {
		t.Fatal("Build with FieldUnknown should return error")
	}
	if form != nil {
		t.Error("Build should return a nil Form on error")
	}
	if !strings.Contains(err.Error(), "FieldUnknown") {
		t.Errorf("error message should mention FieldUnknown, got: %v", err)
	}
}

// TestBuildEmptyFieldsShortCircuit verifies the empty-fields short circuit
// survives the Build/Run split: Build succeeds, and Form.Run returns an empty
// Result immediately without touching the (nil) underlying huh.Form.
func TestBuildEmptyFieldsShortCircuit(t *testing.T) {
	form, err := Build("Title", []Field{}, RunOptions{})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if form.Huh() != nil {
		t.Error("Build with no fields should not construct an underlying huh.Form")
	}

	result, err := form.Run(context.Background())
	if err != nil {
		t.Fatalf("Form.Run returned error: %v", err)
	}
	if !result.IsEmpty() {
		t.Errorf("empty fields should produce empty result, got %v", result)
	}
}

// TestFormResultHarvestsBoundValues verifies (*Form).Result reads the current
// bound values off the form's fields, driving the bindings directly to
// simulate huh having updated them (rather than running a real form).
func TestFormResultHarvestsBoundValues(t *testing.T) {
	fields := []Field{
		{Key: "name", Kind: FieldInput},
		{Key: "tags", Kind: FieldMultiselect, Options: []Option{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}}},
		{Key: "agree", Kind: FieldConfirm},
	}
	form, err := Build("Title", fields, RunOptions{Output: io.Discard})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	for _, b := range form.bindings {
		switch v := b.ptr.(type) {
		case *string:
			*v = "alice"
		case *[]string:
			*v = []string{"a", "b"}
		case *bool:
			*v = true
		}
	}

	result := form.Result()
	if got := result.String("name"); got != "alice" {
		t.Errorf("Result.String(name) = %q, want %q", got, "alice")
	}
	if got := result.Strings("tags"); !slicesEqual(got, []string{"a", "b"}) {
		t.Errorf("Result.Strings(tags) = %v, want [a b]", got)
	}
	if got := result.Bool("agree"); !got {
		t.Error("Result.Bool(agree) = false, want true")
	}
}

// TestRunIsBuildPlusFormRun verifies the top-level Run is behaviourally
// equivalent to Build followed by (*Form).Run, on the same cases already
// covered by TestRunRejectsFieldUnknown / TestRunEmptyFields: a FieldUnknown
// field errors identically, and an empty field list short-circuits
// identically.
func TestRunIsBuildPlusFormRun(t *testing.T) {
	t.Run("FieldUnknown", func(t *testing.T) {
		fields := []Field{{Key: "test", Kind: FieldUnknown, Title: "Test"}}

		_, runErr := Run(context.Background(), "Title", fields, RunOptions{})

		form, buildErr := Build("Title", fields, RunOptions{})
		var formRunErr error
		if buildErr == nil {
			_, formRunErr = form.Run(context.Background())
		} else {
			formRunErr = buildErr
		}

		if runErr == nil || formRunErr == nil {
			t.Fatalf("expected both paths to error: Run=%v, Build+Form.Run=%v", runErr, formRunErr)
		}
		if runErr.Error() != formRunErr.Error() {
			t.Errorf("Run error = %q, Build+Form.Run error = %q", runErr.Error(), formRunErr.Error())
		}
	})

	t.Run("EmptyFields", func(t *testing.T) {
		runResult, runErr := Run(context.Background(), "Title", []Field{}, RunOptions{})
		if runErr != nil {
			t.Fatalf("Run returned error: %v", runErr)
		}

		form, buildErr := Build("Title", []Field{}, RunOptions{})
		if buildErr != nil {
			t.Fatalf("Build returned error: %v", buildErr)
		}
		formResult, formRunErr := form.Run(context.Background())
		if formRunErr != nil {
			t.Fatalf("Form.Run returned error: %v", formRunErr)
		}

		if !reflect.DeepEqual(runResult, formResult) {
			t.Errorf("Run result = %v, Build+Form.Run result = %v", runResult, formResult)
		}
	})
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

	huhField, _, err := buildHuhField(f, false)
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

	huhField, _, err := buildHuhField(f, false)
	if err != nil {
		t.Fatalf("buildHuhField returned error: %v", err)
	}
	if huhField == nil {
		t.Error("huhField should not be nil")
	}
}

// bindingKeysEqual compares a key.Binding's Keys() to want.
func bindingKeysEqual(b key.Binding, want []string) bool {
	return slicesEqual(b.Keys(), want)
}

// TestBuildKeyMapDefaults verifies that with no Quit/SubmitHelp override,
// buildKeyMap returns a keymap equivalent to huh's own default (no
// customization applied) for every slot the hijack touches.
func TestBuildKeyMapDefaults(t *testing.T) {
	fields := []Field{
		{Key: "s", Kind: FieldSelect},
		{Key: "m", Kind: FieldMultiselect},
		{Key: "i", Kind: FieldInput},
	}
	km := buildKeyMap(RunOptions{}, fields)
	def := huh.NewDefaultKeyMap()

	if !bindingKeysEqual(km.Quit, def.Quit.Keys()) {
		t.Errorf("Quit.Keys() = %v, want default %v", km.Quit.Keys(), def.Quit.Keys())
	}
	if !bindingKeysEqual(km.Select.Filter, def.Select.Filter.Keys()) {
		t.Errorf("Select.Filter changed without QuitSpec: %v", km.Select.Filter.Keys())
	}
	if !bindingKeysEqual(km.MultiSelect.Filter, def.MultiSelect.Filter.Keys()) {
		t.Errorf("MultiSelect.Filter changed without QuitSpec: %v", km.MultiSelect.Filter.Keys())
	}
	if !bindingKeysEqual(km.Input.AcceptSuggestion, def.Input.AcceptSuggestion.Keys()) {
		t.Errorf("Input.AcceptSuggestion changed without QuitSpec: %v", km.Input.AcceptSuggestion.Keys())
	}
	if km.Select.Submit.Help().Desc != def.Select.Submit.Help().Desc {
		t.Errorf("Select.Submit help changed without SubmitHelp: %q", km.Select.Submit.Help().Desc)
	}
}

// TestBuildKeyMapQuitSelectOnly verifies the Filter-slot hijack fires only
// for a select-only form, leaving multiselect/input slots untouched.
func TestBuildKeyMapQuitSelectOnly(t *testing.T) {
	fields := []Field{{Key: "s", Kind: FieldSelect}}
	quit := &QuitSpec{Keys: []string{"q", "esc"}, Help: "exit"}
	km := buildKeyMap(RunOptions{Quit: quit}, fields)
	def := huh.NewDefaultKeyMap()

	if !bindingKeysEqual(km.Quit, quit.Keys) {
		t.Errorf("Quit.Keys() = %v, want %v", km.Quit.Keys(), quit.Keys)
	}
	if got := km.Quit.Help().Desc; got != "exit" {
		t.Errorf("Quit.Help().Desc = %q, want %q", got, "exit")
	}
	if !bindingKeysEqual(km.Select.Filter, quit.Keys) {
		t.Errorf("Select.Filter.Keys() = %v, want %v", km.Select.Filter.Keys(), quit.Keys)
	}
	if got := km.Select.Filter.Help().Desc; got != "exit" {
		t.Errorf("Select.Filter.Help().Desc = %q, want %q", got, "exit")
	}
	if !bindingKeysEqual(km.MultiSelect.Filter, def.MultiSelect.Filter.Keys()) {
		t.Errorf("MultiSelect.Filter should stay default, got %v", km.MultiSelect.Filter.Keys())
	}
	if !bindingKeysEqual(km.Input.AcceptSuggestion, def.Input.AcceptSuggestion.Keys()) {
		t.Errorf("Input.AcceptSuggestion should stay default, got %v", km.Input.AcceptSuggestion.Keys())
	}
}

// TestBuildKeyMapQuitMultiselectOnly verifies the hijack targets
// MultiSelect.Filter for a multiselect-only form.
func TestBuildKeyMapQuitMultiselectOnly(t *testing.T) {
	fields := []Field{{Key: "m", Kind: FieldMultiselect}}
	quit := &QuitSpec{Keys: []string{"esc", "ctrl+c"}, Help: "cancel"}
	km := buildKeyMap(RunOptions{Quit: quit}, fields)
	def := huh.NewDefaultKeyMap()

	if !bindingKeysEqual(km.MultiSelect.Filter, quit.Keys) {
		t.Errorf("MultiSelect.Filter.Keys() = %v, want %v", km.MultiSelect.Filter.Keys(), quit.Keys)
	}
	if got := km.MultiSelect.Filter.Help().Desc; got != "cancel" {
		t.Errorf("MultiSelect.Filter.Help().Desc = %q, want %q", got, "cancel")
	}
	if !bindingKeysEqual(km.Select.Filter, def.Select.Filter.Keys()) {
		t.Errorf("Select.Filter should stay default, got %v", km.Select.Filter.Keys())
	}
}

// TestBuildKeyMapQuitInputOnly verifies the hijack targets
// Input.AcceptSuggestion for an input-only form.
func TestBuildKeyMapQuitInputOnly(t *testing.T) {
	fields := []Field{{Key: "i", Kind: FieldInput}}
	quit := &QuitSpec{Keys: []string{"esc", "ctrl+c"}, Help: "cancel"}
	km := buildKeyMap(RunOptions{Quit: quit}, fields)
	def := huh.NewDefaultKeyMap()

	if !bindingKeysEqual(km.Input.AcceptSuggestion, quit.Keys) {
		t.Errorf("Input.AcceptSuggestion.Keys() = %v, want %v", km.Input.AcceptSuggestion.Keys(), quit.Keys)
	}
	if got := km.Input.AcceptSuggestion.Help().Desc; got != "cancel" {
		t.Errorf("Input.AcceptSuggestion.Help().Desc = %q, want %q", got, "cancel")
	}
	if !bindingKeysEqual(km.Select.Filter, def.Select.Filter.Keys()) {
		t.Errorf("Select.Filter should stay default, got %v", km.Select.Filter.Keys())
	}
}

// TestBuildKeyMapQuitMixedFields verifies every present kind's slot is
// hijacked when a form mixes select, multiselect, and input fields.
func TestBuildKeyMapQuitMixedFields(t *testing.T) {
	fields := []Field{
		{Key: "s", Kind: FieldSelect},
		{Key: "m", Kind: FieldMultiselect},
		{Key: "i", Kind: FieldInput},
	}
	quit := &QuitSpec{Keys: []string{"q", "esc", "ctrl+c"}, Help: "exit"}
	km := buildKeyMap(RunOptions{Quit: quit}, fields)

	for name, b := range map[string]key.Binding{
		"Select.Filter":          km.Select.Filter,
		"MultiSelect.Filter":     km.MultiSelect.Filter,
		"Input.AcceptSuggestion": km.Input.AcceptSuggestion,
	} {
		if !bindingKeysEqual(b, quit.Keys) {
			t.Errorf("%s.Keys() = %v, want %v", name, b.Keys(), quit.Keys)
		}
		if got := b.Help().Desc; got != "exit" {
			t.Errorf("%s.Help().Desc = %q, want %q", name, got, "exit")
		}
	}
}

// TestBuildKeyMapEmptyQuitKeysTreatedAsNil pins the decided behaviour: a
// QuitSpec with an empty Keys slice is a no-op, identical to Quit == nil.
func TestBuildKeyMapEmptyQuitKeysTreatedAsNil(t *testing.T) {
	fields := []Field{{Key: "s", Kind: FieldSelect}}
	km := buildKeyMap(RunOptions{Quit: &QuitSpec{Help: "exit"}}, fields)
	def := huh.NewDefaultKeyMap()

	if !bindingKeysEqual(km.Quit, def.Quit.Keys()) {
		t.Errorf("Quit.Keys() = %v, want default %v (empty Keys should be a no-op)", km.Quit.Keys(), def.Quit.Keys())
	}
	if !bindingKeysEqual(km.Select.Filter, def.Select.Filter.Keys()) {
		t.Errorf("Select.Filter changed despite empty QuitSpec.Keys: %v", km.Select.Filter.Keys())
	}
}

// TestBuildKeyMapSubmitHelpRelabel verifies the cosmetic submit-help relabel
// applies to every present kind's Submit/Next slot.
func TestBuildKeyMapSubmitHelpRelabel(t *testing.T) {
	fields := []Field{
		{Key: "s", Kind: FieldSelect},
		{Key: "m", Kind: FieldMultiselect},
		{Key: "i", Kind: FieldInput},
	}
	km := buildKeyMap(RunOptions{SubmitHelp: "select"}, fields)

	if got := km.Select.Submit.Help().Desc; got != "select" {
		t.Errorf("Select.Submit.Help().Desc = %q, want %q", got, "select")
	}
	if got := km.MultiSelect.Submit.Help().Desc; got != "select" {
		t.Errorf("MultiSelect.Submit.Help().Desc = %q, want %q", got, "select")
	}
	if got := km.Input.Next.Help().Desc; got != "select" {
		t.Errorf("Input.Next.Help().Desc = %q, want %q", got, "select")
	}
}

// TestBuildHuhFieldFilterableRejectedForNonMultiselect verifies Filterable
// is rejected on every field kind except FieldMultiselect.
func TestBuildHuhFieldFilterableRejectedForNonMultiselect(t *testing.T) {
	yes := true
	kinds := []FieldKind{FieldInput, FieldSelect, FieldConfirm}
	for _, k := range kinds {
		f := Field{Key: "f", Kind: k, Filterable: &yes}
		_, _, err := buildHuhField(f, false)
		if err == nil {
			t.Errorf("kind %v: Filterable should be rejected, got no error", k)
		}
	}
}

// TestBuildHuhFieldMultiselectFilterableTogglesSlotVisibility pins the
// visibility caveat: a Filterable:false multiselect omits the
// Filter/SetFilter/ClearFilter bindings from KeyBinds() entirely (huh's own
// behaviour, not something ask implements), while Filterable:true (or nil
// default) keeps them.
func TestBuildHuhFieldMultiselectFilterableTogglesSlotVisibility(t *testing.T) {
	base := Field{
		Key:     "m",
		Kind:    FieldMultiselect,
		Options: []Option{{Value: "a", Label: "A"}},
	}

	no := false
	yes := true

	for _, tt := range []struct {
		name       string
		filterable *bool
	}{
		{"nil (default true)", nil},
		{"explicit true", &yes},
	} {
		f := base
		f.Filterable = tt.filterable
		huhField, _, err := buildHuhField(f, false)
		if err != nil {
			t.Fatalf("%s: buildHuhField returned error: %v", tt.name, err)
		}
		huhField = huhField.WithKeyMap(huh.NewDefaultKeyMap())
		if !containsFilterBinding(huhField.KeyBinds()) {
			t.Errorf("%s: KeyBinds() should include the Filter binding", tt.name)
		}
	}

	f := base
	f.Filterable = &no
	huhField, _, err := buildHuhField(f, false)
	if err != nil {
		t.Fatalf("filterable=false: buildHuhField returned error: %v", err)
	}
	huhField = huhField.WithKeyMap(huh.NewDefaultKeyMap())
	if containsFilterBinding(huhField.KeyBinds()) {
		t.Error("filterable=false: KeyBinds() should NOT include the Filter binding")
	}
}

// containsFilterBinding reports whether binds contains huh's default
// select/multiselect Filter binding ("/" key).
func containsFilterBinding(binds []key.Binding) bool {
	for _, b := range binds {
		if slicesEqual(b.Keys(), []string{"/"}) {
			return true
		}
	}
	return false
}

// TestBuildHuhFieldInputSuggestionsGatedByHasQuit verifies the fake
// SuggestionsFunc (and thus the AcceptSuggestion slot) is only installed
// when the form has a QuitSpec in effect.
func TestBuildHuhFieldInputSuggestionsGatedByHasQuit(t *testing.T) {
	f := Field{Key: "i", Kind: FieldInput}

	withoutQuit, _, err := buildHuhField(f, false)
	if err != nil {
		t.Fatalf("buildHuhField(hasQuit=false) returned error: %v", err)
	}
	withoutQuit = withoutQuit.WithKeyMap(huh.NewDefaultKeyMap())
	if got := len(withoutQuit.KeyBinds()); got != 3 {
		t.Errorf("hasQuit=false: KeyBinds() len = %d, want 3 (no AcceptSuggestion)", got)
	}

	withQuit, _, err := buildHuhField(f, true)
	if err != nil {
		t.Fatalf("buildHuhField(hasQuit=true) returned error: %v", err)
	}
	withQuit = withQuit.WithKeyMap(huh.NewDefaultKeyMap())
	if got := len(withQuit.KeyBinds()); got != 4 {
		t.Errorf("hasQuit=true: KeyBinds() len = %d, want 4 (AcceptSuggestion present)", got)
	}
}

// unexportedIntField reads an unexported int field via reflection. huh's
// Select/MultiSelect Height has no public getter, so this is the only way
// to verify the value was actually applied to the constructed field.
func unexportedIntField(t *testing.T, v any, field string) int {
	t.Helper()
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	fv := rv.FieldByName(field)
	if !fv.IsValid() {
		t.Fatalf("field %q not found on %T", field, v)
	}
	return int(fv.Int())
}

// TestBuildHuhFieldSelectHeightApplied verifies Field.Height reaches the
// underlying huh.Select field.
func TestBuildHuhFieldSelectHeightApplied(t *testing.T) {
	f := Field{Key: "s", Kind: FieldSelect, Height: 20, Options: []Option{{Value: "a", Label: "A"}}}
	huhField, _, err := buildHuhField(f, false)
	if err != nil {
		t.Fatalf("buildHuhField returned error: %v", err)
	}
	if got := unexportedIntField(t, huhField, "height"); got != 20 {
		t.Errorf("select height = %d, want 20", got)
	}
}

// TestBuildHuhFieldSelectHeightUnsetLeavesDefault verifies Height: 0 (unset)
// does not force a height onto the field.
func TestBuildHuhFieldSelectHeightUnsetLeavesDefault(t *testing.T) {
	f := Field{Key: "s", Kind: FieldSelect, Options: []Option{{Value: "a", Label: "A"}}}
	huhField, _, err := buildHuhField(f, false)
	if err != nil {
		t.Fatalf("buildHuhField returned error: %v", err)
	}
	if got := unexportedIntField(t, huhField, "height"); got != 0 {
		t.Errorf("select height = %d, want 0 (unset)", got)
	}
}

// TestBuildHuhFieldMultiselectHeightApplied verifies Field.Height reaches
// the underlying huh.MultiSelect field.
func TestBuildHuhFieldMultiselectHeightApplied(t *testing.T) {
	f := Field{Key: "m", Kind: FieldMultiselect, Height: 15, Options: []Option{{Value: "a", Label: "A"}}}
	huhField, _, err := buildHuhField(f, false)
	if err != nil {
		t.Fatalf("buildHuhField returned error: %v", err)
	}
	if got := unexportedIntField(t, huhField, "height"); got != 15 {
		t.Errorf("multiselect height = %d, want 15", got)
	}
}

// TestRunShowHelpTriState smoke-tests all three ShowHelp states through Run
// (nil/true/false) don't panic; internal huh group.showHelp is unexported so
// this only exercises the code path, mirroring TestRunOptionsDefaults.
func TestRunShowHelpTriState(t *testing.T) {
	trueVal := true
	falseVal := false

	for name, showHelp := range map[string]*bool{
		"nil":   nil,
		"true":  &trueVal,
		"false": &falseVal,
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
			defer cancel()

			fields := []Field{{Key: "test", Kind: FieldInput, Title: "Test"}}
			// Route stdio to buffers so the smoke test never touches the real
			// terminal (huh's group.showHelp is unexported, so the effect stays
			// unobservable — this only asserts no panic across the tri-state).
			_, _ = Run(ctx, "Title", fields, RunOptions{
				Input:    strings.NewReader(""),
				Output:   io.Discard,
				ShowHelp: showHelp,
			})
		})
	}
}

// inputEchoMode reads the echo mode off the huh.Input's embedded bubbles
// textinput.Model. huh exposes EchoMode as a setter only, so reflection is
// the only way to verify the mask was actually applied (the field itself is
// an exported int-kind field on an unexported struct field, so no unsafe is
// needed to read it).
func inputEchoMode(t *testing.T, field huh.Field) int64 {
	t.Helper()
	rv := reflect.ValueOf(field)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	ti := rv.FieldByName("textinput")
	if !ti.IsValid() {
		t.Fatalf("textinput field not found on %T", field)
	}
	mode := ti.FieldByName("EchoMode")
	if !mode.IsValid() {
		t.Fatalf("EchoMode field not found on %T's textinput", field)
	}
	return mode.Int()
}

// TestBuildHuhFieldPassword verifies a password field builds, binds a string
// and masks its echo, while a plain input keeps the normal echo mode.
func TestBuildHuhFieldPassword(t *testing.T) {
	f := Field{
		Key:         "token",
		Kind:        FieldPassword,
		Title:       "Secret value",
		Description: "Input is hidden",
		Default:     "seed",
	}

	huhField, binding, err := buildHuhField(f, false)
	if err != nil {
		t.Fatalf("buildHuhField returned error: %v", err)
	}
	if huhField == nil {
		t.Fatal("huhField should not be nil")
	}
	if binding.key != "token" {
		t.Errorf("binding.key = %q, want token", binding.key)
	}
	if _, ok := binding.ptr.(*string); !ok {
		t.Errorf("binding.ptr should be *string for FieldPassword, got %T", binding.ptr)
	}
	if got := inputEchoMode(t, huhField); got != int64(huh.EchoModePassword) {
		t.Errorf("echo mode = %d, want EchoModePassword (%d)", got, huh.EchoModePassword)
	}

	plain, _, err := buildHuhField(Field{Key: "i", Kind: FieldInput}, false)
	if err != nil {
		t.Fatalf("buildHuhField(FieldInput) returned error: %v", err)
	}
	if got := inputEchoMode(t, plain); got != int64(huh.EchoModeNormal) {
		t.Errorf("FieldInput echo mode = %d, want EchoModeNormal (%d)", got, huh.EchoModeNormal)
	}
}

// TestFormResultPasswordIsString verifies a password field harvests as a
// plain string through Result, like any other input.
func TestFormResultPasswordIsString(t *testing.T) {
	form, err := Build("Title", []Field{{Key: "token", Kind: FieldPassword}}, RunOptions{Output: io.Discard})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	for _, b := range form.bindings {
		if v, ok := b.ptr.(*string); ok {
			*v = "s3cret"
		}
	}
	result := form.Result()
	if got := result.String("token"); got != "s3cret" {
		t.Errorf("Result.String(token) = %q, want s3cret", got)
	}
}

// TestBuildHuhFieldPasswordValidation verifies Required and a custom Validate
// reach the built field: Blur runs the validator and stores the error.
func TestBuildHuhFieldPasswordValidation(t *testing.T) {
	tests := []struct {
		name    string
		field   Field
		wantErr string
	}{
		{
			name:    "required empty",
			field:   Field{Key: "token", Kind: FieldPassword, Required: true},
			wantErr: "required",
		},
		{
			name:  "required with value",
			field: Field{Key: "token", Kind: FieldPassword, Required: true, Default: "v"},
		},
		{
			name: "custom validate rejects",
			field: Field{Key: "token", Kind: FieldPassword, Default: "short", Validate: func(s string) error {
				if len(s) < 8 {
					return errors.New("too short")
				}
				return nil
			}},
			wantErr: "too short",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			huhField, _, err := buildHuhField(tt.field, false)
			if err != nil {
				t.Fatalf("buildHuhField returned error: %v", err)
			}
			huhField.Blur()
			got := huhField.Error()
			switch {
			case tt.wantErr == "" && got != nil:
				t.Errorf("Error() = %v, want nil", got)
			case tt.wantErr != "" && got == nil:
				t.Errorf("Error() = nil, want %q", tt.wantErr)
			case tt.wantErr != "" && got != nil && !strings.Contains(got.Error(), tt.wantErr):
				t.Errorf("Error() = %v, want it to contain %q", got, tt.wantErr)
			}
		})
	}
}

// TestBuildHuhFieldPasswordFilterableRejected pins that Filterable stays
// multiselect-only for the new kind too.
func TestBuildHuhFieldPasswordFilterableRejected(t *testing.T) {
	yes := true
	if _, _, err := buildHuhField(Field{Key: "token", Kind: FieldPassword, Filterable: &yes}, false); err == nil {
		t.Error("Filterable should be rejected for FieldPassword")
	}
}

// TestDetectKindsPasswordCountsAsInput verifies a password-only form drives
// the same keymap slots as an input-only form: the SubmitHelp relabel lands
// on Input.Next and the quit hint hijacks Input.AcceptSuggestion.
func TestDetectKindsPasswordCountsAsInput(t *testing.T) {
	fields := []Field{{Key: "token", Kind: FieldPassword}}

	if kinds := detectKinds(fields); !kinds.input {
		t.Error("detectKinds should classify FieldPassword as an input kind")
	}

	km := buildKeyMap(RunOptions{SubmitHelp: "save"}, fields)
	if got := km.Input.Next.Help().Desc; got != "save" {
		t.Errorf("Input.Next.Help().Desc = %q, want %q", got, "save")
	}

	quit := &QuitSpec{Keys: []string{"esc", "ctrl+c"}, Help: "cancel"}
	km = buildKeyMap(RunOptions{Quit: quit}, fields)
	if !bindingKeysEqual(km.Input.AcceptSuggestion, quit.Keys) {
		t.Errorf("Input.AcceptSuggestion.Keys() = %v, want %v", km.Input.AcceptSuggestion.Keys(), quit.Keys)
	}
	if got := km.Input.AcceptSuggestion.Help().Desc; got != "cancel" {
		t.Errorf("Input.AcceptSuggestion.Help().Desc = %q, want %q", got, "cancel")
	}
}

// TestBuildHuhFieldPasswordSuggestionsGatedByHasQuit verifies the password
// field shares FieldInput's quit-hint suggestion trick.
func TestBuildHuhFieldPasswordSuggestionsGatedByHasQuit(t *testing.T) {
	f := Field{Key: "token", Kind: FieldPassword}

	withoutQuit, _, err := buildHuhField(f, false)
	if err != nil {
		t.Fatalf("buildHuhField(hasQuit=false) returned error: %v", err)
	}
	withoutQuit = withoutQuit.WithKeyMap(huh.NewDefaultKeyMap())
	if got := len(withoutQuit.KeyBinds()); got != 3 {
		t.Errorf("hasQuit=false: KeyBinds() len = %d, want 3 (no AcceptSuggestion)", got)
	}

	withQuit, _, err := buildHuhField(f, true)
	if err != nil {
		t.Fatalf("buildHuhField(hasQuit=true) returned error: %v", err)
	}
	withQuit = withQuit.WithKeyMap(huh.NewDefaultKeyMap())
	if got := len(withQuit.KeyBinds()); got != 4 {
		t.Errorf("hasQuit=true: KeyBinds() len = %d, want 4 (AcceptSuggestion present)", got)
	}
}
