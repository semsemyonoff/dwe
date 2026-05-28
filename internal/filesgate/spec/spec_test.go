package spec

import (
	"testing"

	"github.com/stretchr/testify/require"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/filesgate"
	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/usercommands/registry"
)

func TestValidate_CommandRequired(t *testing.T) {
	reg := registry.NewEmptyRegistry()

	reg.AddCommandForTest(&model.CommandDef{
		ID:   "cmd_no_files",
		Type: "shell",
		Cmd:  "echo",
	})

	tests := []struct {
		name      string
		fg        *filesgate.FilesGate
		ref       filesgate.StepRef
		wantIssue bool
		wantMsg   string
	}{
		{
			name: "non-command step without explicit gate command",
			fg: &filesgate.FilesGate{
				State: "readable",
			},
			ref:       filesgate.StepRef{Type: "shell", Cmd: "echo hello"},
			wantIssue: true,
			wantMsg:   "files_gate.command is required",
		},
		{
			name: "command has no files block",
			fg: &filesgate.FilesGate{
				State:   "readable",
				Command: "cmd_no_files",
			},
			ref:       filesgate.StepRef{Type: "shell", Cmd: "echo"},
			wantIssue: true,
			wantMsg:   "has no files: block",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := Validate(nil, reg, tt.ref, tt.fg)
			if tt.wantIssue {
				require.NotEmpty(t, issues, "expected at least one issue")
				require.Contains(t, issues[0].Message, tt.wantMsg)
			} else {
				require.Empty(t, issues, "expected no issues, got: %+v", issues)
			}
		})
	}
}

func TestValidate_WithInheritedFromRef(t *testing.T) {
	reg := registry.NewEmptyRegistry()

	reg.AddCommandForTest(&model.CommandDef{
		ID:   "cmd_req_param",
		Type: "shell",
		Cmd:  "echo $DB",
		Params: map[string]model.ParamDef{
			"db": {Type: model.ParamTypeString, Required: true},
		},
		Files: map[string]model.FileSpec{
			"dump": {Access: model.FileAccessRead, Required: true},
		},
	})

	tests := []struct {
		name      string
		fg        *filesgate.FilesGate
		ref       filesgate.StepRef
		wantIssue bool
	}{
		{
			name: "gate.with nil, param satisfied via ref.with",
			fg: &filesgate.FilesGate{
				State:   "readable",
				Command: "cmd_req_param",
				With:    nil,
			},
			ref:       filesgate.StepRef{Type: "command", Cmd: "cmd_req_param", With: map[string]any{"db": "mydb"}},
			wantIssue: false,
		},
		{
			name: "gate.with nil, ref.with also missing param",
			fg: &filesgate.FilesGate{
				State:   "readable",
				Command: "cmd_req_param",
				With:    nil,
			},
			ref:       filesgate.StepRef{Type: "command", Cmd: "cmd_req_param"},
			wantIssue: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := Validate(nil, reg, tt.ref, tt.fg)
			if tt.wantIssue {
				require.NotEmpty(t, issues)
			} else {
				require.Empty(t, issues, "expected no issues, got: %+v", issues)
			}
		})
	}
}

func TestValidate_Parameters(t *testing.T) {
	// Build a test registry with commands that have various param configurations.
	reg := registry.NewEmptyRegistry()

	reg.AddCommandForTest(&model.CommandDef{
		ID:   "cmd_no_params",
		Type: "shell",
		Cmd:  "echo 'hello'",
		Files: map[string]model.FileSpec{
			"dump": {Access: model.FileAccessRead, Required: true},
		},
	})

	reg.AddCommandForTest(&model.CommandDef{
		ID:   "cmd_req_param",
		Type: "shell",
		Cmd:  "echo $DB",
		Params: map[string]model.ParamDef{
			"db": {
				Type:     model.ParamTypeString,
				Required: true,
			},
		},
		Files: map[string]model.FileSpec{
			"dump": {Access: model.FileAccessRead, Required: true},
		},
	})

	reg.AddCommandForTest(&model.CommandDef{
		ID:   "cmd_req_with_default",
		Type: "shell",
		Cmd:  "echo $DB",
		Params: map[string]model.ParamDef{
			"db": {
				Type:     model.ParamTypeString,
				Required: true,
				Default:  "mydb",
			},
		},
		Files: map[string]model.FileSpec{
			"dump": {Access: model.FileAccessRead, Required: true},
		},
	})

	reg.AddCommandForTest(&model.CommandDef{
		ID:   "cmd_req_with_default_from",
		Type: "shell",
		Cmd:  "echo $DB",
		Params: map[string]model.ParamDef{
			"db": {
				Type:        model.ParamTypeString,
				Required:    true,
				DefaultFrom: "databases.main.name",
			},
		},
		Files: map[string]model.FileSpec{
			"dump": {Access: model.FileAccessRead, Required: true},
		},
	})

	reg.AddCommandForTest(&model.CommandDef{
		ID:   "cmd_multi_params",
		Type: "shell",
		Cmd:  "echo",
		Params: map[string]model.ParamDef{
			"required_param": {
				Type:     model.ParamTypeString,
				Required: true,
			},
			"optional_param": {
				Type:     model.ParamTypeString,
				Required: false,
			},
			"param_with_default": {
				Type:     model.ParamTypeString,
				Required: true,
				Default:  "default_value",
			},
		},
		Files: map[string]model.FileSpec{
			"dump": {Access: model.FileAccessRead, Required: true},
		},
	})

	tests := []struct {
		name      string
		fg        *filesgate.FilesGate
		ref       filesgate.StepRef
		cfg       *config.DevboxConfig
		wantIssue bool
		wantMsg   string
	}{
		{
			name: "no required params",
			fg: &filesgate.FilesGate{
				State:   "readable",
				Command: "cmd_no_params",
			},
			ref:       filesgate.StepRef{Type: "command", Cmd: "cmd_no_params"},
			wantIssue: false,
		},
		{
			name: "required param provided in with",
			fg: &filesgate.FilesGate{
				State:   "readable",
				Command: "cmd_req_param",
				With: map[string]any{
					"db": "mydb",
				},
			},
			ref:       filesgate.StepRef{Type: "command", Cmd: "cmd_req_param"},
			wantIssue: false,
		},
		{
			name: "required param empty in with",
			fg: &filesgate.FilesGate{
				State:   "readable",
				Command: "cmd_req_param",
				With: map[string]any{
					"db": "",
				},
			},
			ref:       filesgate.StepRef{Type: "command", Cmd: "cmd_req_param"},
			wantIssue: true,
			wantMsg:   "required parameter \"db\" must be provided",
		},
		{
			name: "required param not provided but has default",
			fg: &filesgate.FilesGate{
				State:   "readable",
				Command: "cmd_req_with_default",
			},
			ref:       filesgate.StepRef{Type: "command", Cmd: "cmd_req_with_default"},
			wantIssue: false,
		},
		{
			// default_from with no cfg: cannot resolve the path, no literal default → issue.
			name: "required param with default_from only, cfg nil",
			fg: &filesgate.FilesGate{
				State:   "readable",
				Command: "cmd_req_with_default_from",
			},
			ref:       filesgate.StepRef{Type: "command", Cmd: "cmd_req_with_default_from"},
			cfg:       nil,
			wantIssue: true,
			wantMsg:   "required parameter \"db\" must be provided",
		},
		{
			// default_from with cfg that has the path → resolves → no issue.
			name: "required param with default_from, cfg resolves path",
			fg: &filesgate.FilesGate{
				State:   "readable",
				Command: "cmd_req_with_default_from",
			},
			ref: filesgate.StepRef{Type: "command", Cmd: "cmd_req_with_default_from"},
			cfg: &config.DevboxConfig{
				Raw: map[string]any{
					"databases": map[string]any{
						"main": map[string]any{
							"name": "mydb",
						},
					},
				},
			},
			wantIssue: false,
		},
		{
			// default_from with cfg that does NOT have the path → cannot resolve → issue.
			name: "required param with default_from, cfg missing path",
			fg: &filesgate.FilesGate{
				State:   "readable",
				Command: "cmd_req_with_default_from",
			},
			ref: filesgate.StepRef{Type: "command", Cmd: "cmd_req_with_default_from"},
			cfg: &config.DevboxConfig{
				Raw: map[string]any{
					"databases": map[string]any{},
				},
			},
			wantIssue: true,
			wantMsg:   "required parameter \"db\" must be provided",
		},
		{
			// default_from with cfg that resolves to empty string → falls through → issue.
			name: "required param with default_from, cfg resolves to empty string",
			fg: &filesgate.FilesGate{
				State:   "readable",
				Command: "cmd_req_with_default_from",
			},
			ref: filesgate.StepRef{Type: "command", Cmd: "cmd_req_with_default_from"},
			cfg: &config.DevboxConfig{
				Raw: map[string]any{
					"databases": map[string]any{
						"main": map[string]any{
							"name": "",
						},
					},
				},
			},
			wantIssue: true,
			wantMsg:   "required parameter \"db\" must be provided",
		},
		{
			name: "multiple params - some required missing",
			fg: &filesgate.FilesGate{
				State:   "readable",
				Command: "cmd_multi_params",
				With: map[string]any{
					"optional_param": "value",
				},
			},
			ref:       filesgate.StepRef{Type: "command", Cmd: "cmd_multi_params"},
			wantIssue: true,
			wantMsg:   "required parameter \"required_param\" must be provided",
		},
		{
			name: "multiple params - all required provided",
			fg: &filesgate.FilesGate{
				State:   "readable",
				Command: "cmd_multi_params",
				With: map[string]any{
					"required_param": "value",
				},
			},
			ref:       filesgate.StepRef{Type: "command", Cmd: "cmd_multi_params"},
			wantIssue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := Validate(tt.cfg, reg, tt.ref, tt.fg)
			if tt.wantIssue {
				require.Len(t, issues, 1, "expected one issue")
				require.Contains(t, issues[0].Message, tt.wantMsg)
			} else {
				require.Len(t, issues, 0, "expected no issues, got: %+v", issues)
			}
		})
	}
}
