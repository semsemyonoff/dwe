package commands

import (
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func TestHideDiagnostics_ValidExpr_NoDiagnostic(t *testing.T) {
	cmd := model.CommandDef{
		ID:   "svc.cmd",
		Hide: `{{ eq .Raw.foo "bar" }}`,
	}
	if got := hideDiagnostics(cmd, "commands/svc.yml"); len(got) != 0 {
		t.Errorf("valid template should not produce diagnostics; got %+v", got)
	}
}

func TestHideDiagnostics_EmptyHide_NoDiagnostic(t *testing.T) {
	cmd := model.CommandDef{ID: "svc.cmd", Hide: ""}
	if got := hideDiagnostics(cmd, "commands/svc.yml"); len(got) != 0 {
		t.Errorf("empty hide should not produce diagnostics; got %+v", got)
	}
}

func TestHideDiagnostics_BrokenTemplate_WarningDiagnostic(t *testing.T) {
	cmd := model.CommandDef{
		ID:   "svc.broken",
		Hide: `{{ if .x }`, // unclosed action
	}
	diags := hideDiagnostics(cmd, "commands/svc.yml")
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
	diags := groupHideDiagnostics("db", "{{ unbalanced", "commands/db.yml")
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	if !strings.Contains(diags[0].Message, "group \"db\"") {
		t.Errorf("expected group label in message; got %q", diags[0].Message)
	}
}

func TestGroupHideDiagnostics_Empty_NoDiagnostic(t *testing.T) {
	if got := groupHideDiagnostics("db", "", "commands/db.yml"); len(got) != 0 {
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
	diags := hideDiagnostics(cmd, "commands/svc.yml")
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic for snapshot var in hide:, got %d (%+v)", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "snapshot") {
		t.Errorf("expected snapshot-scope message; got %q", diags[0].Message)
	}
}
