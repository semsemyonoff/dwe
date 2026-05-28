// Package filesgate implements the files_gate step directive for deploy/lifecycle/reset pipelines.
package filesgate

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// State represents the expected state of files for a gate to pass.
type State string

const (
	// StateReadable requires all selected files to exist/resolve.
	StateReadable State = "readable"
	// StateMissing requires all selected files to not exist.
	StateMissing State = "missing"
)

// String returns the string representation of the state.
func (s State) String() string {
	return string(s)
}

// UnmarshalYAML validates state is one of the allowed values.
func (s *State) UnmarshalYAML(node *yaml.Node) error {
	var val string
	if err := node.Decode(&val); err != nil {
		return err
	}
	switch val {
	case "readable", "missing":
		*s = State(val)
		return nil
	default:
		return fmt.Errorf("invalid state %q, must be readable or missing", val)
	}
}

// RequireSpec specifies which files participate in the probe.
type RequireSpec interface {
	isRequireSpec()
}

// RequireRequired selects files where (access == read && required) || access == read_write.
type RequireRequired struct{}

func (RequireRequired) isRequireSpec() {}

// RequireAll selects files where access is read or read_write.
type RequireAll struct{}

func (RequireAll) isRequireSpec() {}

// RequireList selects the explicit list of file IDs.
type RequireList struct {
	IDs []string
}

func (RequireList) isRequireSpec() {}

// UnmarshalRequireSpec parses a require spec from a YAML value.
func UnmarshalRequireSpec(node *yaml.Node) (RequireSpec, error) {
	if node == nil {
		return RequireRequired{}, nil
	}

	// Handle null case (node.Tag == "!!null" or node.Value == "null").
	if node.Tag == "!!null" || (node.Kind == yaml.ScalarNode && node.Value == "null") {
		return RequireRequired{}, nil
	}

	// Try to parse as a string (shorthand).
	if node.Kind == yaml.ScalarNode {
		var str string
		if err := node.Decode(&str); err != nil {
			return nil, err
		}
		switch str {
		case "required":
			return RequireRequired{}, nil
		case "all":
			return RequireAll{}, nil
		default:
			// Single file ID.
			return RequireList{IDs: []string{str}}, nil
		}
	}

	// Try to parse as a list.
	if node.Kind == yaml.SequenceNode {
		var list []string
		if err := node.Decode(&list); err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, fmt.Errorf("require: [] is not allowed (empty list has no semantics)")
		}
		return RequireList{IDs: list}, nil
	}

	return nil, fmt.Errorf("require must be a string (required, all, or file-id) or a list of file-ids")
}

// FilesGate declares the files_gate directive for a step.
type FilesGate struct {
	// Command is the target command whose files: block is probed.
	// Default: the step's own cmd.
	Command string `yaml:"command,omitempty"`
	// With holds parameter overrides for files: template resolution.
	// Default: the step's own with.
	With map[string]any `yaml:"with,omitempty"`
	// Require specifies which files participate in the probe.
	// Default: required.
	Require RequireSpec `yaml:"-"` // Manually handled in UnmarshalYAML/MarshalYAML
	// requireRaw holds the raw require value for marshaling round-trip fidelity.
	requireRaw any
	// State is the expected state (readable or missing).
	State State `yaml:"state"`
}

// UnmarshalYAML implements the yaml.Unmarshaler interface.
// Accepts both short form (string scalar -> {State: <state>})
// and long form (mapping with all fields).
func (fg *FilesGate) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}

	// Try to parse as a scalar (short form).
	var scalarVal string
	if node.Kind == yaml.ScalarNode {
		if err := node.Decode(&scalarVal); err == nil {
			// Short form: just the state value — validate it.
			switch scalarVal {
			case "readable", "missing":
				fg.State = State(scalarVal)
				fg.Require = RequireRequired{}
				return nil
			default:
				return fmt.Errorf("invalid files_gate state %q, must be readable or missing", scalarVal)
			}
		}
	}

	// Parse as a mapping (long form).
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("files_gate must be a string (short form) or a mapping (long form)")
	}

	// Manually decode mapping fields for strict validation.
	type rawFilesGate struct {
		Command string         `yaml:"command,omitempty"`
		With    map[string]any `yaml:"with,omitempty"`
		State   State          `yaml:"state"`
	}

	// Check for unknown fields.
	allowedKeys := map[string]bool{
		"command": true,
		"with":    true,
		"require": true,
		"state":   true,
	}

	var requireNode *yaml.Node
	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]
		key := keyNode.Value

		if !allowedKeys[key] {
			return fmt.Errorf("unknown field %q in files_gate", key)
		}

		if key == "require" {
			requireNode = valNode
		}
	}

	// Decode the allowed fields.
	var raw rawFilesGate
	if err := node.Decode(&raw); err != nil {
		return err
	}

	fg.Command = raw.Command
	fg.With = raw.With
	fg.State = raw.State

	// Parse require from the raw node.
	if requireNode != nil {
		spec, err := UnmarshalRequireSpec(requireNode)
		if err != nil {
			return err
		}
		fg.Require = spec
		// Store raw value for marshaling.
		if err := requireNode.Decode(&fg.requireRaw); err == nil && fg.requireRaw != nil {
			// Successfully stored.
		} else {
			fg.requireRaw = nil
		}
	} else {
		fg.Require = RequireRequired{}
		fg.requireRaw = nil
	}

	return nil
}

// MarshalYAML implements the yaml.Marshaler interface.
func (fg *FilesGate) MarshalYAML() (any, error) {
	type raw struct {
		Command string         `yaml:"command,omitempty"`
		With    map[string]any `yaml:"with,omitempty"`
		Require any            `yaml:"require,omitempty"`
		State   State          `yaml:"state"`
	}

	// Convert Require back to a marshalable form.
	var requireVal any
	switch spec := fg.Require.(type) {
	case RequireRequired:
		requireVal = "required"
	case RequireAll:
		requireVal = "all"
	case RequireList:
		if len(spec.IDs) == 1 {
			requireVal = spec.IDs[0]
		} else {
			requireVal = spec.IDs
		}
	case nil:
		requireVal = nil
	}

	// If requireRaw is set, prefer it (for round-trip fidelity).
	if fg.requireRaw != nil {
		requireVal = fg.requireRaw
	}

	return &raw{
		Command: fg.Command,
		With:    fg.With,
		Require: requireVal,
		State:   fg.State,
	}, nil
}

// StepRef is a minimal step-shaped value used by validators.
type StepRef struct {
	Type string         `yaml:"type"`
	Cmd  string         `yaml:"cmd"`
	With map[string]any `yaml:"with,omitempty"`
}
