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
		{"builtin predicate template is rendered", `file-exists {{ index .services "db" "dir" }}/x`, cfg, 1},
		{"builtin predicate is not evaluated", `file-exists {{ index .Raw "services" "db" "dir" }}/x`, cfg, 0},
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

// TestHideDiagnostics_RenderedResult covers what runtime does after rendering:
// a result that is neither a literal boolean, a `cmd:` nor a well-formed
// predicate is silently fail-open at runtime, so the validator must flag it —
// without running the command or probing the filesystem.
func TestHideDiagnostics_RenderedResult(t *testing.T) {
	cfg := &config.DweConfig{Raw: map[string]any{
		"services": map[string]any{"db": map[string]any{"enabled": true}},
	}}
	sentinel := filepath.Join(t.TempDir(), "executed")

	tests := []struct {
		name string
		hide string
		want string // message substring; "" means no diagnostic
	}{
		{"true", "true", ""},
		{"false", "false", ""},
		{"one", "1", ""},
		{"zero", "0", ""},
		{"renders empty", `{{ if false }}x{{ end }}`, ""},
		{"template renders true", `{{ index .Raw "services" "db" "enabled" }}`, ""},
		{"padded boolean", "  true  ", ""},
		{"yes", "yes", `renders to "yes", which is neither a boolean nor a known predicate`},
		{"known predicate", "dir-exists workspace", ""},
		{"generated-missing", "generated-missing db password", ""},
		{"unknown verb", "dir-exist foo", `unknown builtin predicate "dir-exist"`},
		{"unknown verb from a template branch", `{{ if index .Raw "services" "db" "enabled" }}dir-exist foo{{ end }}`, `renders to "dir-exist foo"`},
		{"predicate without argument", "file-exists", `renders to "file-exists"`},
		{"generated-missing with one arg", "generated-missing db", `expected "<svc> <field>"`},
		{"cmd is not executed", "cmd: touch " + sentinel, ""},
		{"cmd test", "cmd: test -f x", ""},
		{"empty cmd", "cmd:   ", "db.reset: hide: expression renders to an empty `cmd:` command"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := model.CommandDef{ID: "db.reset", Hide: tc.hide}
			diags := hideDiagnostics(cmd, "workspace/commands/db.yml", cfg)
			if tc.want == "" {
				if len(diags) != 0 {
					t.Fatalf("want no diagnostics, got %+v", diags)
				}
				return
			}
			if len(diags) != 1 {
				t.Fatalf("want 1 diagnostic, got %+v", diags)
			}
			d := diags[0]
			if d.Severity != validate.SeverityWarning || d.Domain != "commands" || d.Target != "commands:db.reset" || d.File != "workspace/commands/db.yml" {
				t.Errorf("unexpected diagnostic shape: %+v", d)
			}
			if !strings.Contains(d.Message, tc.want) {
				t.Errorf("message %q does not contain %q", d.Message, tc.want)
			}
			if tc.name == "empty cmd" {
				// Its own message: "cmd:" is not "neither a boolean nor a
				// known predicate", and runtime fails on it rather than hiding.
				if d.Message != tc.want {
					t.Errorf("message = %q, want %q", d.Message, tc.want)
				}
				if !strings.Contains(d.Hint, "evaluation error at runtime") {
					t.Errorf("hint should say an empty cmd: fails at runtime; got %q", d.Hint)
				}
				return
			}
			if !strings.Contains(d.Hint, "true/false/1/0") || !strings.Contains(d.Hint, "cmd:") {
				t.Errorf("hint should list the valid results; got %q", d.Hint)
			}
		})
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Error("cmd: hide was executed by the validator")
	}
}

func TestGroupHideDiagnostics_RenderedResult(t *testing.T) {
	cfg := &config.DweConfig{Raw: map[string]any{}}
	if diags := groupHideDiagnostics("db", "dir-missing workspace/services/db", "commands/db.yml", cfg); len(diags) != 0 {
		t.Fatalf("valid predicate: want no diagnostics, got %+v", diags)
	}
	diags := groupHideDiagnostics("db", "yes", "commands/db.yml", cfg)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d (%+v)", len(diags), diags)
	}
	if diags[0].Target != "group:db" || !strings.Contains(diags[0].Message, `group "db": hide: expression renders to "yes"`) {
		t.Errorf("unexpected diagnostic: %+v", diags[0])
	}
}
