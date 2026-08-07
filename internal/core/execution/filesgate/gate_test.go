package filesgate

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStateString(t *testing.T) {
	tests := []struct {
		state    State
		expected string
	}{
		{StateReadable, "readable"},
		{StateMissing, "missing"},
	}

	for _, tt := range tests {
		if got := tt.state.String(); got != tt.expected {
			t.Errorf("State.String() = %q, want %q", got, tt.expected)
		}
	}
}

func TestStateUnmarshalYAML(t *testing.T) {
	tests := []struct {
		yaml     string
		expected State
		wantErr  bool
	}{
		{"readable", StateReadable, false},
		{"missing", StateMissing, false},
		{"invalid", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.yaml, func(t *testing.T) {
			var node yaml.Node
			err := yaml.Unmarshal([]byte(tt.yaml), &node)
			if err != nil {
				t.Fatalf("yaml.Unmarshal failed: %v", err)
			}

			var s State
			err = s.UnmarshalYAML(&node)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalYAML error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && s != tt.expected {
				t.Errorf("got %q, want %q", s, tt.expected)
			}
		})
	}
}

func TestUnmarshalRequireSpec(t *testing.T) {
	// Note: UnmarshalRequireSpec is primarily tested via FilesGate unmarshaling,
	// since it's extracted from a mapping node. Direct node tests are minimal.
	tests := []struct {
		name    string
		yamlDoc string // Full YAML doc with require in a mapping
		want    RequireSpec
		wantErr bool
	}{
		{
			name:    "required shorthand",
			yamlDoc: "{require: required}",
			want:    RequireRequired{},
		},
		{
			name:    "all shorthand",
			yamlDoc: "{require: all}",
			want:    RequireAll{},
		},
		{
			name:    "single file-id",
			yamlDoc: "{require: dump}",
			want:    RequireList{IDs: []string{"dump"}},
		},
		{
			name:    "list of file-ids",
			yamlDoc: "{require: [a, b]}",
			want:    RequireList{IDs: []string{"a", "b"}},
		},
		{
			name:    "empty list rejected",
			yamlDoc: "{require: []}",
			wantErr: true,
		},
		{
			name:    "null require (default)",
			yamlDoc: "{require: null}",
			want:    RequireRequired{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m map[string]yaml.Node
			err := yaml.Unmarshal([]byte(tt.yamlDoc), &m)
			if err != nil {
				t.Fatalf("yaml.Unmarshal failed: %v", err)
			}

			node := m["require"]
			got, err := UnmarshalRequireSpec(&node)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalRequireSpec error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !requireSpecEqual(got, tt.want) {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestFilesGateUnmarshalYAML(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    *FilesGate
		wantErr bool
	}{
		{
			name: "short form: readable",
			yaml: "readable",
			want: &FilesGate{
				State:   StateReadable,
				Require: RequireRequired{},
			},
		},
		{
			name: "short form: missing",
			yaml: "missing",
			want: &FilesGate{
				State:   StateMissing,
				Require: RequireRequired{},
			},
		},
		{
			name: "long form: state only",
			yaml: "{ state: readable }",
			want: &FilesGate{
				State:   StateReadable,
				Require: RequireRequired{},
			},
		},
		{
			name: "long form: state and require",
			yaml: "{ state: readable, require: all }",
			want: &FilesGate{
				State:   StateReadable,
				Require: RequireAll{},
			},
		},
		{
			name: "long form: state and require list",
			yaml: "{ state: readable, require: [dump, backup] }",
			want: &FilesGate{
				State:   StateReadable,
				Require: RequireList{IDs: []string{"dump", "backup"}},
			},
		},
		{
			name: "long form: all fields",
			yaml: "{ command: other-cmd, with: { param1: value1 }, state: missing, require: required }",
			want: &FilesGate{
				Command: "other-cmd",
				With:    map[string]any{"param1": "value1"},
				State:   StateMissing,
				Require: RequireRequired{},
			},
		},
		{
			name:    "unknown field",
			yaml:    "{ state: readable, unknown_field: foo }",
			wantErr: true,
		},
		{
			name:    "invalid state",
			yaml:    "{ state: invalid }",
			wantErr: true,
		},
		{
			name:    "short form: invalid state",
			yaml:    "bogus",
			wantErr: true,
		},
		{
			name:    "empty list in require",
			yaml:    "{ state: readable, require: [] }",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got FilesGate
			err := yaml.Unmarshal([]byte(tt.yaml), &got)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalYAML error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !filesGateEqual(&got, tt.want) {
				t.Errorf("got %#v, want %#v", &got, tt.want)
			}
		})
	}
}

func TestFilesGateRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"short form readable", "readable"},
		{"short form missing", "missing"},
		{"long form simple", "{ state: readable }"},
		{"long form with require", "{ state: readable, require: all }"},
		{"long form with list", "{ state: readable, require: [a, b] }"},
		{"long form all fields", "{ command: cmd, with: { p: v }, state: missing, require: required }"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fg1 FilesGate
			if err := yaml.Unmarshal([]byte(tt.yaml), &fg1); err != nil {
				t.Fatalf("UnmarshalYAML failed: %v", err)
			}

			// Re-marshal and ensure it parses the same way (not byte-identical, just semantically).
			data, err := yaml.Marshal(&fg1)
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}

			var fg2 FilesGate
			if err := yaml.Unmarshal(data, &fg2); err != nil {
				t.Fatalf("Second UnmarshalYAML failed: %v", err)
			}

			if !filesGateEqual(&fg1, &fg2) {
				t.Errorf("round trip failed: %#v != %#v", &fg1, &fg2)
			}
		})
	}
}

func requireSpecEqual(a, b RequireSpec) bool {
	switch av := a.(type) {
	case RequireRequired:
		_, ok := b.(RequireRequired)
		return ok
	case RequireAll:
		_, ok := b.(RequireAll)
		return ok
	case RequireList:
		bv, ok := b.(RequireList)
		if !ok {
			return false
		}
		if len(av.IDs) != len(bv.IDs) {
			return false
		}
		for i, id := range av.IDs {
			if bv.IDs[i] != id {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func filesGateEqual(a, b *FilesGate) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.Command != b.Command || a.State != b.State {
		return false
	}
	if !mapStringAnyEqual(a.With, b.With) {
		return false
	}
	return requireSpecEqual(a.Require, b.Require)
}

func mapStringAnyEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for key, aVal := range a {
		bVal, ok := b[key]
		if !ok {
			return false
		}
		// Simple equality for the test (assumes values are strings/numbers/etc).
		if aVal != bVal {
			return false
		}
	}
	return true
}
