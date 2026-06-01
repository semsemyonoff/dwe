package config_test

import (
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
