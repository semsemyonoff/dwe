package setup

import (
	"errors"
	"os"
	"testing"

	"devbox-cli/internal/core/workflow/setup"
	"devbox-cli/internal/validate"
)

func TestParseValidator(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		path  string
		count int
	}{
		{
			name:  "no error",
			err:   nil,
			count: 0,
		},
		{
			name:  "file not found",
			err:   ErrNotFound,
			count: 0,
		},
		{
			name:  "parse error",
			err:   errTestParseError,
			path:  "/test/setup.yml",
			count: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &parseValidator{err: tt.err, path: tt.path}
			diags := v.Run(validate.Context{})
			if len(diags) != tt.count {
				t.Errorf("expected %d diags, got %d", tt.count, len(diags))
			}
		})
	}
}

func TestTypeKnownValidator(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *setup.Config
		count int
	}{
		{
			name:  "nil config",
			cfg:   nil,
			count: 0,
		},
		{
			name: "valid types",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeInput},
					{ID: "q2", Type: setup.TypeSelect},
					{ID: "q3", Type: setup.TypeMultiselect},
					{ID: "q4", Type: setup.TypeConfirm},
				},
			},
			count: 0,
		},
		{
			name: "invalid type",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: "invalid"},
				},
			},
			count: 1,
		},
		{
			name: "mixed valid and invalid",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeInput},
					{ID: "q2", Type: "bad"},
					{ID: "q3", Type: setup.TypeSelect},
					{ID: "q4", Type: "worse"},
				},
			},
			count: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &typeKnownValidator{cfg: tt.cfg}
			diags := v.Run(validate.Context{})
			if len(diags) != tt.count {
				t.Errorf("expected %d diags, got %d", tt.count, len(diags))
			}
		})
	}
}

func TestIdRequiredValidator(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *setup.Config
		count int
	}{
		{
			name:  "nil config",
			cfg:   nil,
			count: 0,
		},
		{
			name: "all questions have ids",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeInput},
					{ID: "q2", Type: setup.TypeSelect},
				},
			},
			count: 0,
		},
		{
			name: "empty id",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "", Type: setup.TypeInput},
				},
			},
			count: 1,
		},
		{
			name: "multiple missing ids",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeInput},
					{ID: "", Type: setup.TypeSelect},
					{ID: "", Type: setup.TypeConfirm},
				},
			},
			count: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &idRequiredValidator{cfg: tt.cfg}
			diags := v.Run(validate.Context{})
			if len(diags) != tt.count {
				t.Errorf("expected %d diags, got %d", tt.count, len(diags))
			}
		})
	}
}

func TestIdUniqueValidator(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *setup.Config
		count int
	}{
		{
			name:  "nil config",
			cfg:   nil,
			count: 0,
		},
		{
			name: "all unique ids",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeInput},
					{ID: "q2", Type: setup.TypeSelect},
				},
			},
			count: 0,
		},
		{
			name: "duplicate ids",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeInput},
					{ID: "q1", Type: setup.TypeSelect},
				},
			},
			count: 1,
		},
		{
			name: "multiple duplicates",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeInput},
					{ID: "q2", Type: setup.TypeSelect},
					{ID: "q1", Type: setup.TypeConfirm},
					{ID: "q2", Type: setup.TypeMultiselect},
				},
			},
			count: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &idUniqueValidator{cfg: tt.cfg}
			diags := v.Run(validate.Context{})
			if len(diags) != tt.count {
				t.Errorf("expected %d diags, got %d", tt.count, len(diags))
			}
		})
	}
}

func TestWritesRequiredValidator(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *setup.Config
		count int
	}{
		{
			name:  "nil config",
			cfg:   nil,
			count: 0,
		},
		{
			name: "all questions have writes",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Writes: "app.setting"},
					{ID: "q2", Writes: "db.config"},
				},
			},
			count: 0,
		},
		{
			name: "empty writes",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Writes: ""},
				},
			},
			count: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &writesRequiredValidator{cfg: tt.cfg}
			diags := v.Run(validate.Context{})
			if len(diags) != tt.count {
				t.Errorf("expected %d diags, got %d", tt.count, len(diags))
			}
		})
	}
}

func TestWritesUniqueValidator(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *setup.Config
		count int
	}{
		{
			name:  "nil config",
			cfg:   nil,
			count: 0,
		},
		{
			name: "all unique writes",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Writes: "app.setting"},
					{ID: "q2", Writes: "db.config"},
				},
			},
			count: 0,
		},
		{
			name: "duplicate writes",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Writes: "app.setting"},
					{ID: "q2", Writes: "app.setting"},
				},
			},
			count: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &writesUniqueValidator{cfg: tt.cfg}
			diags := v.Run(validate.Context{})
			if len(diags) != tt.count {
				t.Errorf("expected %d diags, got %d", tt.count, len(diags))
			}
		})
	}
}

func TestWritesSyntaxValidator(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *setup.Config
		count int
	}{
		{
			name:  "nil config",
			cfg:   nil,
			count: 0,
		},
		{
			name: "valid paths",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Writes: "app"},
					{ID: "q2", Writes: "app.setting"},
					{ID: "q3", Writes: "deep.nested.path.here"},
				},
			},
			count: 0,
		},
		{
			name: "leading dot",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Writes: ".app"},
				},
			},
			count: 1,
		},
		{
			name: "trailing dot",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Writes: "app."},
				},
			},
			count: 1,
		},
		{
			name: "invalid segment",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Writes: "app.123invalid"},
				},
			},
			count: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &writesSyntaxValidator{cfg: tt.cfg}
			diags := v.Run(validate.Context{})
			if len(diags) != tt.count {
				t.Errorf("expected %d diags, got %d", tt.count, len(diags))
			}
		})
	}
}

func TestWritesScopeValidator(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *setup.Config
		count int
	}{
		{
			name:  "nil config",
			cfg:   nil,
			count: 0,
		},
		{
			name: "valid custom paths",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Writes: "custom"},
					{ID: "q2", Writes: "app.setting"},
				},
			},
			count: 0,
		},
		{
			name: "forbidden namespace",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Writes: "info.something"},
				},
			},
			count: 1,
		},
		{
			name: "exact services root forbidden",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Writes: "services"},
				},
			},
			count: 1,
		},
		{
			name: "invalid services path",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Writes: "services.web"},
				},
			},
			count: 1,
		},
		{
			name: "valid services.enabled",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Writes: "services.web.enabled"},
				},
			},
			count: 0,
		},
		{
			name: "valid services.ports.name",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Writes: "services.web.ports.http"},
				},
			},
			count: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &writesScopeValidator{cfg: tt.cfg}
			diags := v.Run(validate.Context{})
			if len(diags) != tt.count {
				t.Errorf("expected %d diags, got %d", tt.count, len(diags))
			}
		})
	}
}

func TestOptionsValidValidator(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *setup.Config
		count int
	}{
		{
			name:  "nil config",
			cfg:   nil,
			count: 0,
		},
		{
			name: "select with valid options",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeSelect, Options: []setup.Option{
						{Value: "a", Label: "A"},
						{Value: "b", Label: "B"},
					}},
				},
			},
			count: 0,
		},
		{
			name: "select without options",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeSelect, Options: []setup.Option{}},
				},
			},
			count: 1,
		},
		{
			name: "select with empty option value",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeSelect, Options: []setup.Option{
						{Value: "", Label: "None"},
					}},
				},
			},
			count: 1,
		},
		{
			name: "select with duplicate option",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeSelect, Options: []setup.Option{
						{Value: "a", Label: "A"},
						{Value: "a", Label: "A2"},
					}},
				},
			},
			count: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &optionsValidValidator{cfg: tt.cfg}
			diags := v.Run(validate.Context{})
			if len(diags) != tt.count {
				t.Errorf("expected %d diags, got %d", tt.count, len(diags))
			}
		})
	}
}

func TestValidateExclusiveValidator(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *setup.Config
		count int
	}{
		{
			name:  "nil config",
			cfg:   nil,
			count: 0,
		},
		{
			name: "no validation",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Validate: nil},
				},
			},
			count: 0,
		},
		{
			name: "preset only",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Validate: &setup.ValidateSpec{Preset: setup.PresetPort}},
				},
			},
			count: 0,
		},
		{
			name: "both preset and regex",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Validate: &setup.ValidateSpec{Preset: setup.PresetPort, Regex: ".*"}},
				},
			},
			count: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &validateExclusiveValidator{cfg: tt.cfg}
			diags := v.Run(validate.Context{})
			if len(diags) != tt.count {
				t.Errorf("expected %d diags, got %d", tt.count, len(diags))
			}
		})
	}
}

func TestValidateOnlyOnInputValidator(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *setup.Config
		count int
	}{
		{
			name:  "nil config",
			cfg:   nil,
			count: 0,
		},
		{
			name: "input with preset",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeInput, Validate: &setup.ValidateSpec{Preset: setup.PresetPort}},
				},
			},
			count: 0,
		},
		{
			name: "select with preset",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeSelect, Validate: &setup.ValidateSpec{Preset: setup.PresetPort}},
				},
			},
			count: 1,
		},
		{
			name: "confirm with regex",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeConfirm, Validate: &setup.ValidateSpec{Regex: ".*"}},
				},
			},
			count: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &validateOnlyOnInputValidator{cfg: tt.cfg}
			diags := v.Run(validate.Context{})
			if len(diags) != tt.count {
				t.Errorf("expected %d diags, got %d", tt.count, len(diags))
			}
		})
	}
}

func TestValidatePresetKnownValidator(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *setup.Config
		count int
	}{
		{
			name:  "nil config",
			cfg:   nil,
			count: 0,
		},
		{
			name: "known preset",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Validate: &setup.ValidateSpec{Preset: setup.PresetPort}},
				},
			},
			count: 0,
		},
		{
			name: "unknown preset",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Validate: &setup.ValidateSpec{Preset: "unknown"}},
				},
			},
			count: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &validatePresetKnownValidator{cfg: tt.cfg}
			diags := v.Run(validate.Context{})
			if len(diags) != tt.count {
				t.Errorf("expected %d diags, got %d", tt.count, len(diags))
			}
		})
	}
}

func TestValidateRegexCompilesValidator(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *setup.Config
		count int
	}{
		{
			name:  "nil config",
			cfg:   nil,
			count: 0,
		},
		{
			name: "valid regex",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Validate: &setup.ValidateSpec{Regex: "^[a-z]+$"}},
				},
			},
			count: 0,
		},
		{
			name: "invalid regex",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Validate: &setup.ValidateSpec{Regex: "[invalid("}},
				},
			},
			count: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &validateRegexCompilesValidator{cfg: tt.cfg}
			diags := v.Run(validate.Context{})
			if len(diags) != tt.count {
				t.Errorf("expected %d diags, got %d", tt.count, len(diags))
			}
		})
	}
}

func TestTypeWritesConsistentValidator(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *setup.Config
		count int
	}{
		{
			name:  "nil config",
			cfg:   nil,
			count: 0,
		},
		{
			name: "services.enabled with confirm",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeConfirm, Writes: "services.web.enabled"},
				},
			},
			count: 0,
		},
		{
			name: "services.enabled with input",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeInput, Writes: "services.web.enabled"},
				},
			},
			count: 1,
		},
		{
			name: "services.ports with input+port preset",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeInput, Validate: &setup.ValidateSpec{Preset: setup.PresetPort}, Writes: "services.web.ports.http"},
				},
			},
			count: 0,
		},
		{
			name: "services.ports with select",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeSelect, Writes: "services.web.ports.http"},
				},
			},
			count: 1,
		},
		{
			name: "services.hosts with input",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeInput, Writes: "services.web.hosts.local"},
				},
			},
			count: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &typeWritesConsistentValidator{cfg: tt.cfg}
			diags := v.Run(validate.Context{})
			if len(diags) != tt.count {
				t.Errorf("expected %d diags, got %d", tt.count, len(diags))
			}
		})
	}
}

func TestRequiredConsistentValidator(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *setup.Config
		count int
	}{
		{
			name:  "nil config",
			cfg:   nil,
			count: 0,
		},
		{
			name: "input with required",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeInput, Required: true},
				},
			},
			count: 0,
		},
		{
			name: "confirm with required",
			cfg: &setup.Config{
				Questions: []setup.Question{
					{ID: "q1", Type: setup.TypeConfirm, Required: true},
				},
			},
			count: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &requiredConsistentValidator{cfg: tt.cfg}
			diags := v.Run(validate.Context{})
			if len(diags) != tt.count {
				t.Errorf("expected %d diags, got %d", tt.count, len(diags))
			}
		})
	}
}

// Test error constants used in tests.
var (
	ErrNotFound       = os.ErrNotExist
	errTestParseError = errors.New("test parse error")
)
