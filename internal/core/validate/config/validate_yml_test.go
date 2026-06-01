package config

import (
	"errors"
	"os"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func TestValidateYmlValidator(t *testing.T) {
	v := &validateYmlValidator{}

	if got := v.ID(); got != "validate" {
		t.Errorf("ID() = %q, want %q", got, "validate")
	}
	if got := v.Domain(); got != "config" {
		t.Errorf("Domain() = %q, want %q", got, "config")
	}

	tests := []struct {
		name     string
		ctx      validate.Context
		wantLen  int
		wantSev  validate.Severity
		wantMsg  string
		wantFile string
	}{
		{
			name:    "nil err and no warnings yields nothing",
			ctx:     validate.Context{},
			wantLen: 0,
		},
		{
			name: "nil err with warnings passes them through",
			ctx: validate.Context{
				ValidateCfgWarnings: []validate.Diagnostic{
					{
						Severity: validate.SeverityWarning,
						Domain:   "config",
						Target:   "validate",
						File:     "devbox/validate.yml",
						Line:     7,
						Message:  `check "my-check": stage "deplooy" is not a known preflight stage`,
					},
				},
			},
			wantLen:  1,
			wantSev:  validate.SeverityWarning,
			wantMsg:  `check "my-check": stage "deplooy" is not a known preflight stage`,
			wantFile: "devbox/validate.yml",
		},
		{
			name: "ErrNotExist is silently tolerated",
			ctx: validate.Context{
				ValidateCfgLoadErr: os.ErrNotExist,
			},
			wantLen: 0,
		},
		{
			name: "strict decode error surfaces as single error diagnostic",
			ctx: validate.Context{
				ValidateCfgLoadErr: errors.New("parse devbox/validate.yml: yaml: unmarshal errors:\n  line 3: field foo not found in type config.rawCheckEntry"),
			},
			wantLen:  1,
			wantSev:  validate.SeverityError,
			wantMsg:  "parse devbox/validate.yml: yaml: unmarshal errors:\n  line 3: field foo not found in type config.rawCheckEntry",
			wantFile: "devbox/validate.yml",
		},
		{
			name: "unknown severity load error surfaces as error diagnostic",
			ctx: validate.Context{
				ValidateCfgLoadErr: errors.New(`parse devbox/validate.yml: check "foo": unknown severity "fatal" (allowed: error, warning, info)`),
			},
			wantLen:  1,
			wantSev:  validate.SeverityError,
			wantMsg:  `parse devbox/validate.yml: check "foo": unknown severity "fatal" (allowed: error, warning, info)`,
			wantFile: "devbox/validate.yml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := v.Run(tt.ctx)
			if len(diags) != tt.wantLen {
				t.Fatalf("Run() returned %d diagnostics, want %d: %+v", len(diags), tt.wantLen, diags)
			}
			if tt.wantLen == 0 {
				return
			}
			d := diags[0]
			if d.Severity != tt.wantSev {
				t.Errorf("Severity = %v, want %v", d.Severity, tt.wantSev)
			}
			if d.Message != tt.wantMsg {
				t.Errorf("Message = %q, want %q", d.Message, tt.wantMsg)
			}
			if d.File != tt.wantFile {
				t.Errorf("File = %q, want %q", d.File, tt.wantFile)
			}
			if d.Domain != "config" {
				t.Errorf("Domain = %q, want %q", d.Domain, "config")
			}
			if d.Target != "validate" {
				t.Errorf("Target = %q, want %q", d.Target, "validate")
			}
		})
	}
}
