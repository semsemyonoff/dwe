package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Action represents a typed action (used in step bodies and check: clauses).
// Exactly one of the four types must be set:
//   - shell:   direct shell command via os/exec
//   - dwe:     public dwe CLI subcommand
//   - command: dwe command ID reference (workflow context)
//   - builtin: engine-internal action
//
// Cmd is the payload for all four types (command string or builtin name).
// With holds parameters for command or builtin actions.
type Action struct {
	Type string         `yaml:"type"`
	Cmd  string         `yaml:"cmd"`
	With map[string]any `yaml:"with,omitempty"`
}

// AutoCheckType is the Action.Type carried by the `check: auto` sentinel — the
// one scalar form Action accepts. It is not an executable action: the pipeline
// resolver rewrites it into a real check that is the logical inverse of the
// step's `when:` (see pipeline.ResolveAutoCheck). The sentinel exists so
// step.Check != nil holds from load time onward, which is what keeps
// StepForcesRun and the journal's hasCheck → Run lever working unchanged.
const AutoCheckType = "auto"

// IsAutoCheck reports whether a is the `check: auto` sentinel. Consumers must
// ask through this instead of string-comparing Type, and the payload emptiness
// is part of the test: `{type: auto, cmd: ...}` is not a sentinel but an
// unknown action type, so a cmd written there is rejected rather than silently
// dropped by the rewrite.
func IsAutoCheck(a *Action) bool {
	return a != nil && a.Type == AutoCheckType && a.Cmd == "" && len(a.With) == 0
}

// UnmarshalYAML enforces that only the mapping form is accepted (defense-in-depth on top of
// strict file-level decode), with the single exception of the scalar `auto`
// sentinel. Rejects string shorthand and unknown keys with clear errors.
func (a *Action) UnmarshalYAML(node *yaml.Node) error {
	// Exactly "auto" — "Auto", "auto " and any other scalar keep the
	// pre-existing rejection below.
	if node.Kind == yaml.ScalarNode && node.Value == AutoCheckType {
		*a = Action{Type: AutoCheckType}
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("action must be a mapping (e.g., {type: shell, cmd: ...}), not a scalar string")
	}
	known := map[string]bool{"type": true, "cmd": true, "with": true}
	for i := 0; i < len(node.Content)-1; i += 2 {
		if key := node.Content[i].Value; !known[key] {
			return fmt.Errorf("action: unknown field %q", key)
		}
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
// Type must be one of {shell, dwe, command, builtin}.
// Cmd must be non-empty.
// shell and dwe types do not accept with.
// The `check: auto` sentinel is exempt: it carries no cmd by construction and
// its own shape rules (a when: exists, and it is type: shell) need the
// enclosing step, so they live in validateStepShape.
func (a *Action) Validate() error {
	if IsAutoCheck(a) {
		return nil
	}
	switch a.Type {
	case "shell", "dwe", "command", "builtin":
	default:
		return fmt.Errorf("unknown action type %q", a.Type)
	}
	if a.Cmd == "" {
		return fmt.Errorf("action type %q requires non-empty cmd", a.Type)
	}
	if (a.Type == "shell" || a.Type == "dwe") && len(a.With) > 0 {
		return fmt.Errorf("action type %q does not accept with", a.Type)
	}
	return nil
}
