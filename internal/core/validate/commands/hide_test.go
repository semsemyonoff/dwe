package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func TestHideDiagnostics_ValidExpr_NoDiagnostic(t *testing.T) {
	cmd := model.CommandDef{
		ID:   "svc.cmd",
		Hide: `{{ eq .Raw.foo "bar" }}`,
	}
	if got := hideDiagnostics(cmd, "commands/svc.yml", nil); len(got) != 0 {
		t.Errorf("valid template should not produce diagnostics; got %+v", got)
	}
}

func TestHideDiagnostics_EmptyHide_NoDiagnostic(t *testing.T) {
	cmd := model.CommandDef{ID: "svc.cmd", Hide: ""}
	if got := hideDiagnostics(cmd, "commands/svc.yml", nil); len(got) != 0 {
		t.Errorf("empty hide should not produce diagnostics; got %+v", got)
	}
}

func TestHideDiagnostics_BrokenTemplate_WarningDiagnostic(t *testing.T) {
	cmd := model.CommandDef{
		ID:   "svc.broken",
		Hide: `{{ if .x }`, // unclosed action
	}
	diags := hideDiagnostics(cmd, "commands/svc.yml", nil)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	if diags[0].Severity != validate.SeverityWarning {
		t.Errorf("severity = %v, want warning", diags[0].Severity)
	}
	if !strings.Contains(diags[0].Message, "svc.broken") {
		t.Errorf("message should reference command ID; got %q", diags[0].Message)
	}
}

func TestGroupHideDiagnostics_BrokenTemplate(t *testing.T) {
	diags := groupHideDiagnostics("db", "{{ unbalanced", "commands/db.yml", nil)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	if !strings.Contains(diags[0].Message, "group \"db\"") {
		t.Errorf("expected group label in message; got %q", diags[0].Message)
	}
}

func TestGroupHideDiagnostics_Empty_NoDiagnostic(t *testing.T) {
	if got := groupHideDiagnostics("db", "", "commands/db.yml", nil); len(got) != 0 {
		t.Errorf("empty group hide should not produce diagnostics; got %+v", got)
	}
}

func TestHideDiagnostics_SnapshotVar_Rejected(t *testing.T) {
	// `hide:` runs at SnapshotScopeNone — any ${snapshot.*} reference is a
	// scope error caught at validate time (mirrors RenderCommand's runtime
	// check, so validate output matches runtime behaviour).
	cmd := model.CommandDef{
		ID:   "svc.bad",
		Hide: `${snapshot.created_at}`,
	}
	diags := hideDiagnostics(cmd, "commands/svc.yml", nil)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic for snapshot var in hide:, got %d (%+v)", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "snapshot") {
		t.Errorf("expected snapshot-scope message; got %q", diags[0].Message)
	}
}

func TestHideDiagnostics_RenderCheck(t *testing.T) {
	cfg := &config.DweConfig{Raw: map[string]any{
		"services": map[string]any{"db": map[string]any{"enabled": true}},
		"vars":     map[string]any{"db_engine": "sqlite"},
	}}
	sentinel := filepath.Join(t.TempDir(), "executed")

	tests := []struct {
		name      string
		hide      string
		cfg       *config.DweConfig
		wantDiags int
	}{
		{"raw index evaluates", `{{ not (index .Raw "services" "db" "enabled") }}`, cfg, 0},
		{"vars index evaluates", `{{ eq (index .Raw "vars" "db_engine") "sqlite" }}`, cfg, 0},
		{"missing field on context", `{{ not (index .services "db" "enabled") }}`, cfg, 1},
		{"index into missing service", `{{ not (index .Raw "services" "cache" "enabled") }}`, cfg, 1},
		{"config failed to load skips render", `{{ not (index .services "db" "enabled") }}`, nil, 0},
		{"cmd predicate is not executed", "cmd: touch " + sentinel, cfg, 0},
		{"builtin predicate is not rendered", `file-exists {{ .services }}`, cfg, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := model.CommandDef{ID: "db.reset", Hide: tc.hide}
			diags := hideDiagnostics(cmd, "workspace/services/db/commands.yml", tc.cfg)
			if len(diags) != tc.wantDiags {
				t.Fatalf("got %d diagnostics, want %d: %+v", len(diags), tc.wantDiags, diags)
			}
			for _, d := range diags {
				if d.Severity != validate.SeverityWarning {
					t.Errorf("severity = %v, want warning", d.Severity)
				}
				if !strings.Contains(d.Message, "does not evaluate") {
					t.Errorf("message should say the expression does not evaluate; got %q", d.Message)
				}
				if d.File != "workspace/services/db/commands.yml" {
					t.Errorf("file = %q, want the command file", d.File)
				}
			}
		})
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Error("cmd: predicate was executed by the validator")
	}
}

func TestGroupHideDiagnostics_RenderCheck(t *testing.T) {
	cfg := &config.DweConfig{Raw: map[string]any{}}
	diags := groupHideDiagnostics("db", `{{ not (index .services "db" "enabled") }}`, "commands/db.yml", cfg)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d (%+v)", len(diags), diags)
	}
	if diags[0].Target != "group:db" || !strings.Contains(diags[0].Message, `group "db": hide: expression does not evaluate`) {
		t.Errorf("unexpected diagnostic: %+v", diags[0])
	}
}
