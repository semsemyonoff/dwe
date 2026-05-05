package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Action represents a typed action (used in step bodies and check: clauses).
// Exactly one of the four types must be set:
//   - shell:   direct shell command via os/exec
//   - devbox:  public devbox CLI subcommand
//   - command: devbox command ID reference (workflow context)
//   - builtin: engine-internal action
//
// Cmd is the payload for all four types (command string or builtin name).
// With holds parameters for command or builtin actions.
type Action struct {
	Type string         `yaml:"type"`
	Cmd  string         `yaml:"cmd"`
	With map[string]any `yaml:"with,omitempty"`
}

// UnmarshalYAML enforces that only the mapping form is accepted (defense-in-depth on top of
// strict file-level decode). Rejects string shorthand with a clear error.
func (a *Action) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("action must be a mapping (e.g., {type: shell, cmd: ...}), not a scalar string")
	}
	type actionAlias Action
	var aa actionAlias
	if err := node.Decode(&aa); err != nil {
		return err
	}
	*a = Action(aa)
	return nil
}

// Validate checks that the Action is well-formed.
// Type must be one of {shell, devbox, command, builtin}.
// Cmd must be non-empty.
// shell and devbox types do not accept with.
func (a *Action) Validate() error {
	switch a.Type {
	case "shell", "devbox", "command", "builtin":
	default:
		return fmt.Errorf("unknown action type %q", a.Type)
	}
	if a.Cmd == "" {
		return fmt.Errorf("action type %q requires non-empty cmd", a.Type)
	}
	if (a.Type == "shell" || a.Type == "devbox") && len(a.With) > 0 {
		return fmt.Errorf("action type %q does not accept with", a.Type)
	}
	return nil
}
