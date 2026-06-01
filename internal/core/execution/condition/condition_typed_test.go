package condition_test

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
)

// --- Typed Condition Decode ---

func TestTypedCondition_DecodeBuiltin(t *testing.T) {
	data := `type: builtin
cmd: dir-empty foo`
	var c condition.Condition
	if err := yaml.Unmarshal([]byte(data), &c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Type != condition.TypeBuiltin {
		t.Errorf("Type = %q, want %q", c.Type, condition.TypeBuiltin)
	}
	if c.Cmd != "dir-empty foo" {
		t.Errorf("Cmd = %q, want %q", c.Cmd, "dir-empty foo")
	}
}

func TestTypedCondition_DecodeShell(t *testing.T) {
	data := `type: shell
cmd: test -f /tmp/foo`
	var c condition.Condition
	if err := yaml.Unmarshal([]byte(data), &c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Type != condition.TypeShell {
		t.Errorf("Type = %q, want %q", c.Type, condition.TypeShell)
	}
	if c.Cmd != "test -f /tmp/foo" {
		t.Errorf("Cmd = %q, want %q", c.Cmd, "test -f /tmp/foo")
	}
}

func TestTypedCondition_DecodeTemplate(t *testing.T) {
	data := `type: template
expr: "{{ .Services.main.Enabled }}"`
	var c condition.Condition
	if err := yaml.Unmarshal([]byte(data), &c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Type != condition.TypeTemplate {
		t.Errorf("Type = %q, want %q", c.Type, condition.TypeTemplate)
	}
	if c.Expr != "{{ .Services.main.Enabled }}" {
		t.Errorf("Expr = %q, want %q", c.Expr, "{{ .Services.main.Enabled }}")
	}
}

// --- Reject String Shorthand ---

func TestTypedCondition_RejectStringShorthand(t *testing.T) {
	data := `"dir-empty foo"`
	var c condition.Condition
	err := yaml.Unmarshal([]byte(data), &c)
	if err == nil {
		t.Error("expected error when decoding string shorthand")
	}
}

func TestTypedCondition_RejectBareString(t *testing.T) {
	data := `dir-empty foo`
	var c condition.Condition
	err := yaml.Unmarshal([]byte(data), &c)
	if err == nil {
		t.Error("expected error when decoding bare string")
	}
}

// --- Validation ---

func TestTypedCondition_ValidateBuiltin_Empty(t *testing.T) {
	c := condition.Condition{Type: condition.TypeBuiltin, Cmd: ""}
	err := c.Validate()
	if err == nil {
		t.Error("expected error for builtin with empty cmd")
	}
}

func TestTypedCondition_ValidateShell_Empty(t *testing.T) {
	c := condition.Condition{Type: condition.TypeShell, Cmd: ""}
	err := c.Validate()
	if err == nil {
		t.Error("expected error for shell with empty cmd")
	}
}

func TestTypedCondition_ValidateTemplate_Empty(t *testing.T) {
	c := condition.Condition{Type: condition.TypeTemplate, Expr: ""}
	err := c.Validate()
	if err == nil {
		t.Error("expected error for template with empty expr")
	}
}

func TestTypedCondition_ValidateUnknownType(t *testing.T) {
	c := condition.Condition{Type: "unknown", Cmd: "foo"}
	err := c.Validate()
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestTypedCondition_ValidateBuiltin_OK(t *testing.T) {
	c := condition.Condition{Type: condition.TypeBuiltin, Cmd: "dir-empty foo"}
	if err := c.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTypedCondition_ValidateShell_OK(t *testing.T) {
	c := condition.Condition{Type: condition.TypeShell, Cmd: "test -f foo"}
	if err := c.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTypedCondition_ValidateTemplate_OK(t *testing.T) {
	c := condition.Condition{Type: condition.TypeTemplate, Expr: "{{ .Foo }}"}
	if err := c.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- IsRuntime ---

func TestTypedCondition_IsRuntime(t *testing.T) {
	cases := []struct {
		cond     condition.Condition
		wantTrue bool
	}{
		{condition.Condition{Type: condition.TypeBuiltin, Cmd: "dir-empty foo"}, true},
		{condition.Condition{Type: condition.TypeShell, Cmd: "test -f foo"}, true},
		{condition.Condition{Type: condition.TypeTemplate, Expr: "{{ .Foo }}"}, false},
	}
	for _, tc := range cases {
		got := tc.cond.IsRuntime()
		if got != tc.wantTrue {
			t.Errorf("IsRuntime(%q) = %v, want %v", tc.cond.Type, got, tc.wantTrue)
		}
	}
}

// --- EvalRuntimeTyped ---

func TestTypedCondition_EvalRuntimeTyped_Nil(t *testing.T) {
	ok, err := condition.EvalRuntimeTyped(nil, t.TempDir())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("nil condition should evaluate to true")
	}
}

func TestTypedCondition_EvalRuntimeTyped_Builtin_DirEmpty(t *testing.T) {
	root := t.TempDir()
	c := &condition.Condition{Type: condition.TypeBuiltin, Cmd: "dir-empty foo"}
	ok, err := condition.EvalRuntimeTyped(c, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("dir-empty should be true for non-existing directory")
	}
}

func TestTypedCondition_EvalRuntimeTyped_Shell_Success(t *testing.T) {
	root := t.TempDir()
	c := &condition.Condition{Type: condition.TypeShell, Cmd: "exit 0"}
	ok, err := condition.EvalRuntimeTyped(c, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("exit 0 should evaluate to true")
	}
}

func TestTypedCondition_EvalRuntimeTyped_Shell_Failure(t *testing.T) {
	root := t.TempDir()
	c := &condition.Condition{Type: condition.TypeShell, Cmd: "exit 1"}
	ok, err := condition.EvalRuntimeTyped(c, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("exit 1 should evaluate to false")
	}
}

func TestTypedCondition_EvalRuntimeTyped_TemplateReturnsError(t *testing.T) {
	c := &condition.Condition{Type: condition.TypeTemplate, Expr: "{{ .Foo }}"}
	_, err := condition.EvalRuntimeTyped(c, t.TempDir())
	if err == nil {
		t.Error("template condition should return error in EvalRuntimeTyped")
	}
}

func TestTypedCondition_EvalRuntimeTyped_UnknownTypeReturnsError(t *testing.T) {
	c := &condition.Condition{Type: "unknown", Cmd: "foo"}
	_, err := condition.EvalRuntimeTyped(c, t.TempDir())
	if err == nil {
		t.Error("unknown condition type should return error")
	}
}

func TestTypedCondition_EvalRuntimeTyped_FileTest(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "test.txt")
	if err := os.WriteFile(f, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &condition.Condition{Type: condition.TypeShell, Cmd: "test -f test.txt"}
	ok, err := condition.EvalRuntimeTyped(c, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("test -f should be true for existing file")
	}
}
