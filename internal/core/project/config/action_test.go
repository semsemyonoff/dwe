package config_test

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// --- Decode ---

func TestAction_DecodeShell(t *testing.T) {
	data := `type: shell
cmd: echo hello`
	var a config.Action
	if err := yaml.Unmarshal([]byte(data), &a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Type != "shell" {
		t.Errorf("Type = %q, want %q", a.Type, "shell")
	}
	if a.Cmd != "echo hello" {
		t.Errorf("Cmd = %q, want %q", a.Cmd, "echo hello")
	}
}

func TestAction_DecodeDwe(t *testing.T) {
	data := `type: dwe
cmd: docker down`
	var a config.Action
	if err := yaml.Unmarshal([]byte(data), &a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Type != "dwe" {
		t.Errorf("Type = %q, want %q", a.Type, "dwe")
	}
	if a.Cmd != "docker down" {
		t.Errorf("Cmd = %q, want %q", a.Cmd, "docker down")
	}
}

func TestAction_DecodeCommand(t *testing.T) {
	data := `type: command
cmd: services.main.migrate
with:
  timeout: "30s"`
	var a config.Action
	if err := yaml.Unmarshal([]byte(data), &a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Type != "command" {
		t.Errorf("Type = %q, want %q", a.Type, "command")
	}
	if a.Cmd != "services.main.migrate" {
		t.Errorf("Cmd = %q, want %q", a.Cmd, "services.main.migrate")
	}
}

func TestAction_DecodeBuiltin(t *testing.T) {
	data := `type: builtin
cmd: service_configs_copy
with:
  service: main
  mode: skip`
	var a config.Action
	if err := yaml.Unmarshal([]byte(data), &a); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Type != "builtin" {
		t.Errorf("Type = %q, want %q", a.Type, "builtin")
	}
	if a.Cmd != "service_configs_copy" {
		t.Errorf("Cmd = %q, want %q", a.Cmd, "service_configs_copy")
	}
	if a.With["service"] != "main" {
		t.Errorf("With[service] = %q, want %q", a.With["service"], "main")
	}
}

// --- Reject String Shorthand ---

func TestAction_RejectStringShorthand(t *testing.T) {
	data := `"echo hello"`
	var a config.Action
	err := yaml.Unmarshal([]byte(data), &a)
	if err == nil {
		t.Error("expected error when decoding string shorthand")
	}
}

func TestAction_RejectBareString(t *testing.T) {
	data := `echo hello`
	var a config.Action
	err := yaml.Unmarshal([]byte(data), &a)
	if err == nil {
		t.Error("expected error when decoding bare string")
	}
}

func TestAction_RejectUnknownField(t *testing.T) {
	data := `type: shell
cmd: echo hello
typo: oops`
	var a config.Action
	err := yaml.Unmarshal([]byte(data), &a)
	if err == nil {
		t.Error("expected error for unknown field, got nil")
	}
}

// --- check: auto sentinel ---

func TestAction_DecodeAutoScalar(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"bare", `auto`},
		{"double quoted", `"auto"`},
		{"single quoted", `'auto'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var a config.Action
			if err := yaml.Unmarshal([]byte(tc.data), &a); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !config.IsAutoCheck(&a) {
				t.Fatalf("IsAutoCheck = false, want true (got %#v)", a)
			}
			if a.Cmd != "" || len(a.With) != 0 {
				t.Errorf("sentinel carries payload: %#v", a)
			}
		})
	}
}

func TestAction_RejectNearMissAuto(t *testing.T) {
	// Only the exact spelling "auto" is the sentinel; everything else keeps
	// the pre-existing scalar rejection message.
	for _, data := range []string{`Auto`, `AUTO`, `"auto "`, `" auto"`, `autos`, `auto check`} {
		var a config.Action
		err := yaml.Unmarshal([]byte(data), &a)
		if err == nil {
			t.Errorf("%q: expected error, got none (%#v)", data, a)
			continue
		}
		if !strings.Contains(err.Error(), "not a scalar string") {
			t.Errorf("%q: error = %v, want the scalar-rejection message", data, err)
		}
	}
}

func TestAction_NullCheckStaysNil(t *testing.T) {
	// A null `check:` never reaches UnmarshalYAML — yaml.v3 resolves the null
	// tag before calling the Unmarshaler, so the pointer is simply left nil.
	// Pinned because check: auto handling must not change it.
	var s struct {
		Check *config.Action `yaml:"check"`
	}
	if err := yaml.Unmarshal([]byte("check:\n"), &s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Check != nil {
		t.Errorf("Check = %#v, want nil", s.Check)
	}
}

func TestAction_AutoSentinelExemptFromValidate(t *testing.T) {
	a := config.Action{Type: "auto"}
	if err := a.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAction_AutoTypeWithPayloadIsNotSentinel(t *testing.T) {
	// `{type: auto, cmd: ...}` is not the sentinel: the sentinel carries no
	// payload, so a cmd here would be silently ignored by the rewrite.
	a := config.Action{Type: "auto", Cmd: "test -e x"}
	if config.IsAutoCheck(&a) {
		t.Fatal("IsAutoCheck = true for an action carrying a cmd")
	}
	if err := a.Validate(); err == nil {
		t.Error("expected error for type auto with cmd")
	}
}

func TestAction_IsAutoCheckNil(t *testing.T) {
	if config.IsAutoCheck(nil) {
		t.Error("IsAutoCheck(nil) = true, want false")
	}
}

// --- Validation ---

func TestAction_ValidateShell_OK(t *testing.T) {
	a := config.Action{Type: "shell", Cmd: "echo hello"}
	if err := a.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAction_ValidateDwe_OK(t *testing.T) {
	a := config.Action{Type: "dwe", Cmd: "docker down"}
	if err := a.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAction_ValidateCommand_OK(t *testing.T) {
	a := config.Action{Type: "command", Cmd: "services.main.migrate"}
	if err := a.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAction_ValidateBuiltin_OK(t *testing.T) {
	a := config.Action{Type: "builtin", Cmd: "service_configs_copy", With: map[string]any{"service": "main"}}
	if err := a.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAction_ValidateUnknownType(t *testing.T) {
	a := config.Action{Type: "unknown", Cmd: "foo"}
	err := a.Validate()
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestAction_ValidateEmptyCmd(t *testing.T) {
	a := config.Action{Type: "shell", Cmd: ""}
	err := a.Validate()
	if err == nil {
		t.Error("expected error for empty cmd")
	}
}

func TestAction_ValidateAllTypes_Empty(t *testing.T) {
	types := []string{"shell", "dwe", "command", "builtin"}
	for _, typ := range types {
		a := config.Action{Type: typ, Cmd: ""}
		if err := a.Validate(); err == nil {
			t.Errorf("expected error for %s with empty cmd", typ)
		}
	}
}
