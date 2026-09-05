package config

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
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
						File:     "workspace/validate.yml",
						Line:     7,
						Message:  `check "my-check": stage "deplooy" is not a known preflight stage`,
					},
				},
			},
			wantLen:  1,
			wantSev:  validate.SeverityWarning,
			wantMsg:  `check "my-check": stage "deplooy" is not a known preflight stage`,
			wantFile: "workspace/validate.yml",
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
				ValidateCfgLoadErr: errors.New("workspace/validate.yml:3: unknown field \"foo\" — allowed here: cmd, description, hint, id, services, severity, stages, type, with\n(a field you did not invent may come from a newer dwe version — check `dwe version`)"),
			},
			wantLen:  1,
			wantSev:  validate.SeverityError,
			wantMsg:  "workspace/validate.yml:3: unknown field \"foo\" — allowed here: cmd, description, hint, id, services, severity, stages, type, with\n(a field you did not invent may come from a newer dwe version — check `dwe version`)",
			wantFile: "workspace/validate.yml",
		},
		{
			name: "unknown severity load error surfaces as error diagnostic",
			ctx: validate.Context{
				ValidateCfgLoadErr: errors.New(`parse workspace/validate.yml: check "foo": unknown severity "fatal" (allowed: error, warning, info)`),
			},
			wantLen:  1,
			wantSev:  validate.SeverityError,
			wantMsg:  `parse workspace/validate.yml: check "foo": unknown severity "fatal" (allowed: error, warning, info)`,
			wantFile: "workspace/validate.yml",
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

func TestValidateYmlValidator_UnknownServiceRefs(t *testing.T) {
	cfg := &config.DweConfig{Services: map[string]config.ServiceConfig{
		"api":    {Enabled: true},
		"worker": {Enabled: false},
	}}
	vcfg := &config.ValidateConfig{Checks: []config.CheckEntry{
		{ID: "ok", Services: []string{"api", "worker"}, SourceLine: 5},
		{ID: "typo", Services: []string{"api", "wokrer"}, SourceLine: 12},
		{ID: "no-services", SourceLine: 20},
	}}

	diags := (&validateYmlValidator{}).Run(validate.Context{Cfg: cfg, ValidateCfg: vcfg})
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic for the typo, got %d: %+v", len(diags), diags)
	}
	d := diags[0]
	if d.Severity != validate.SeverityError {
		t.Errorf("severity: want error, got %v", d.Severity)
	}
	if !strings.Contains(d.Message, `"wokrer"`) {
		t.Errorf("message should name the unknown service: %q", d.Message)
	}
	if d.Line != 12 {
		t.Errorf("line: want 12 (from CheckEntry.SourceLine), got %d", d.Line)
	}
}

func TestValidateYmlValidator_SkipsWhenCfgMissing(t *testing.T) {
	// Defensive: nil Cfg or nil ValidateCfg must not panic and must return nothing.
	v := &validateYmlValidator{}
	if got := v.Run(validate.Context{Cfg: nil, ValidateCfg: &config.ValidateConfig{Checks: []config.CheckEntry{{ID: "x", Services: []string{"api"}}}}}); len(got) != 0 {
		t.Errorf("nil Cfg should produce no diagnostics, got %d", len(got))
	}
	if got := v.Run(validate.Context{Cfg: &config.DweConfig{}, ValidateCfg: nil}); len(got) != 0 {
		t.Errorf("nil ValidateCfg should produce no diagnostics, got %d", len(got))
	}
}
