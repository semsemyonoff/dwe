package checks

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	"github.com/semsemyonoff/dwe/internal/core/validate/diag"
)

func newCfg() *config.DweConfig {
	return &config.DweConfig{Raw: map[string]any{}}
}

func runOne(t *testing.T, v validate.Validator) []validate.Diagnostic {
	t.Helper()
	return v.Run(validate.Context{Cfg: newCfg()})
}

// --- AllForStage ---

func TestAllForStage_NilCfg(t *testing.T) {
	if got := AllForStage(nil, nil, "", nil, ""); len(got) != 0 {
		t.Errorf("nil cfg with nil loadErr should yield empty slice, got %d", len(got))
	}
}

func TestAllForStage_NilCfgWithLoadErr(t *testing.T) {
	// A real parse error (not ErrNotExist) should produce a synthetic error
	// validator in the "checks" domain so scoped "checks" runs surface the
	// failure via the normal diagnostic table rather than silently returning zero.
	loadErr := errors.New("yaml: line 1: unknown field")
	vs := AllForStage(nil, loadErr, "", nil, "")
	if len(vs) != 1 {
		t.Fatalf("want 1 synthetic validator, got %d", len(vs))
	}
	v := vs[0]
	if v.Domain() != "checks" {
		t.Errorf("domain: want checks, got %s", v.Domain())
	}
	// Must implement GlobalValidator so "dwe validate env" surfaces the error
	// even though the env scope does not include the checks domain.
	gv, ok := v.(validate.GlobalValidator)
	if !ok {
		t.Error("validateYmlErrValidator must implement GlobalValidator")
	} else if !gv.IsGlobal() {
		t.Error("IsGlobal() must return true")
	}
	diags := v.Run(validate.Context{})
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(diags))
	}
	d := diags[0]
	if d.Severity != validate.SeverityError {
		t.Errorf("severity: want error, got %v", d.Severity)
	}
	if d.Message != loadErr.Error() {
		t.Errorf("message: want %q, got %q", loadErr.Error(), d.Message)
	}
	if d.File != diagFile {
		t.Errorf("file: want %q, got %q", diagFile, d.File)
	}
}

func TestAllForStage_NilCfgErrNotExist(t *testing.T) {
	// os.ErrNotExist means the file is absent (silently tolerated) — no validator.
	vs := AllForStage(nil, os.ErrNotExist, "", nil, "")
	if len(vs) != 0 {
		t.Errorf("ErrNotExist should yield empty slice, got %d", len(vs))
	}
}

func TestAllForStage_FiltersByStage(t *testing.T) {
	cfg := &config.ValidateConfig{Checks: []config.CheckEntry{
		{ID: "a", Type: "builtin", Cmd: "file_exists", With: map[string]any{"path": "AGENTS.md"},
			Stages: []string{"deploy"}, Severity: diag.SeverityError},
		{ID: "b", Type: "builtin", Cmd: "file_exists", With: map[string]any{"path": "AGENTS.md"},
			Stages: []string{"run"}, Severity: diag.SeverityError},
	}}
	vs := AllForStage(cfg, nil, "", nil, "deploy")
	if len(vs) != 1 || vs[0].ID() != "a" {
		t.Fatalf("expected [a], got %#v", ids(vs))
	}
}

func ids(vs []validate.Validator) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.ID()
	}
	return out
}

// --- Cached-failure paths ---

func TestBuildValidator_UnknownBuiltin(t *testing.T) {
	entry := config.CheckEntry{
		ID: "x", Type: "builtin", Cmd: "no_such_builtin",
		Severity: diag.SeverityError, Hint: "hint", SourceLine: 7,
	}
	v := buildValidator(entry, "", nil)
	diags := runOne(t, v)
	if len(diags) != 1 {
		t.Fatalf("want 1 diag, got %d", len(diags))
	}
	d := diags[0]
	if !strings.Contains(d.Message, "no_such_builtin") {
		t.Errorf("message should mention unknown name: %q", d.Message)
	}
	if d.Severity != diag.SeverityError || d.Domain != "checks" || d.Target != "x" {
		t.Errorf("wrong header: %+v", d)
	}
	if d.File != "workspace/validate.yml" || d.Line != 7 || d.Hint != "hint" {
		t.Errorf("wrong location/hint: %+v", d)
	}
}

func TestCachedValidator_AlwaysEmitsError(t *testing.T) {
	// A load-time failure must be SeverityError regardless of the entry's declared severity.
	for _, sev := range []diag.Severity{diag.SeverityWarning, diag.SeverityInfo, diag.SeverityError} {
		entry := config.CheckEntry{
			ID: "x", Type: "builtin", Cmd: "no_such_builtin", Severity: sev,
		}
		v := buildValidator(entry, "", nil)
		diags := runOne(t, v)
		if len(diags) != 1 {
			t.Fatalf("want 1 diag, got %d", len(diags))
		}
		if diags[0].Severity != diag.SeverityError {
			t.Errorf("load-time failure with declared severity %d should always emit SeverityError, got %d", sev, diags[0].Severity)
		}
	}
}

func TestBuildValidator_InvalidWithRejected(t *testing.T) {
	// file_exists requires "path".
	entry := config.CheckEntry{
		ID: "x", Type: "builtin", Cmd: "file_exists",
		With: nil, Severity: diag.SeverityError,
	}
	v := buildValidator(entry, "", nil)
	diags := runOne(t, v)
	if len(diags) != 1 {
		t.Fatalf("want 1 diag, got %d", len(diags))
	}
	if diags[0].Message == "" {
		t.Error("expected message from builtin.Validate")
	}
}

func TestBuildValidator_DisallowedBuiltin(t *testing.T) {
	// Internal builtins are rejected via the kind system (engine-internal message).
	// Action builtins are rejected by the explicit allowlist (may only use builtins: message)
	// since actions are allowed in CtxPredicate but restricted further by the allowlist in validate.yml.
	tests := []struct {
		cmd         string
		wantContain string
	}{
		{"daemons_reap", "engine-internal"},
		{"docker_daemon_start", "engine-internal"},
		{"docker_daemon_stop", "engine-internal"},
		{"docker_daemon_logs", "engine-internal"},
		{"docker_remove_project_volumes", "may only use builtins:"},
		{"confirm", "may only use builtins:"},
	}
	for _, tc := range tests {
		t.Run(tc.cmd, func(t *testing.T) {
			entry := config.CheckEntry{
				ID: "x", Type: "builtin", Cmd: tc.cmd, Severity: diag.SeverityError,
			}
			v := buildValidator(entry, "", nil)
			diags := runOne(t, v)
			if len(diags) != 1 || !strings.Contains(diags[0].Message, tc.wantContain) {
				t.Fatalf("expected rejection for %q (want %q in message), got %+v", tc.cmd, tc.wantContain, diags)
			}
		})
	}
}

func TestBuildValidator_UnknownCommand(t *testing.T) {
	entry := config.CheckEntry{
		ID: "x", Type: "command", Cmd: "nope.missing",
		Severity: diag.SeverityError,
	}
	reg := registry.NewEmptyRegistry()
	v := buildValidator(entry, "", reg)
	diags := runOne(t, v)
	if len(diags) != 1 || !strings.Contains(diags[0].Message, "unknown command: nope.missing") {
		t.Fatalf("expected unknown-command diag, got %+v", diags)
	}
}

func TestBuildValidator_CommandNilRegistry(t *testing.T) {
	entry := config.CheckEntry{
		ID: "x", Type: "command", Cmd: "anything", Severity: diag.SeverityError,
	}
	v := buildValidator(entry, "", nil)
	diags := runOne(t, v)
	if len(diags) != 1 || !strings.Contains(diags[0].Message, "unknown command:") {
		t.Fatalf("expected unknown-command diag, got %+v", diags)
	}
}

func TestBuildValidator_CommandTypeWhitelist(t *testing.T) {
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(&model.CommandDef{
		ID: "wf", Type: model.CommandTypeWorkflow,
	})
	entry := config.CheckEntry{
		ID: "x", Type: "command", Cmd: "wf", Severity: diag.SeverityError,
	}
	v := buildValidator(entry, "", reg)
	diags := runOne(t, v)
	if len(diags) != 1 ||
		!strings.Contains(diags[0].Message, "may only invoke user commands of type shell or script") {
		t.Fatalf("expected whitelist rejection, got %+v", diags)
	}
}

func TestBuildValidator_UnknownEntryType(t *testing.T) {
	entry := config.CheckEntry{
		ID: "x", Type: "bogus", Cmd: "anything", Severity: diag.SeverityError,
	}
	v := buildValidator(entry, "", nil)
	diags := runOne(t, v)
	if len(diags) != 1 || !strings.Contains(diags[0].Message, "unknown check type: bogus") {
		t.Fatalf("expected unknown-type diag, got %+v", diags)
	}
}

// --- Runtime dispatch (builtin) ---

func TestBuiltinRunner_Success(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "present.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := config.CheckEntry{
		ID: "exists", Type: "builtin", Cmd: "file_exists",
		With:     map[string]any{"path": "present.txt"},
		Severity: diag.SeverityError,
	}
	v := buildValidator(entry, dir, nil)
	diags := v.Run(validate.Context{Cfg: newCfg(), ProjectRoot: dir})
	if len(diags) != 1 || diags[0].Severity != validate.SeverityOK {
		t.Fatalf("expected one SeverityOK diagnostic on pass, got %+v", diags)
	}
}

func TestTargetWithStages(t *testing.T) {
	tests := []struct {
		name   string
		entry  config.CheckEntry
		expect string
	}{
		{"no stages", config.CheckEntry{ID: "foo"}, "foo"},
		{"one stage", config.CheckEntry{ID: "foo", Stages: []string{"deploy"}}, "foo\n(deploy)"},
		{"multi stage", config.CheckEntry{ID: "foo", Stages: []string{"deploy", "run"}}, "foo\n(deploy, run)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := targetWithStages(tt.entry); got != tt.expect {
				t.Errorf("targetWithStages: want %q, got %q", tt.expect, got)
			}
		})
	}
}

func TestBuiltinRunner_FailurePropagatesEntryMeta(t *testing.T) {
	dir := t.TempDir()
	entry := config.CheckEntry{
		ID: "exists", Type: "builtin", Cmd: "file_exists",
		With:       map[string]any{"path": "missing.txt"},
		Severity:   diag.SeverityWarning,
		Hint:       "create the file first",
		SourceLine: 42,
	}
	v := buildValidator(entry, dir, nil)
	diags := v.Run(validate.Context{Cfg: newCfg(), ProjectRoot: dir})
	if len(diags) != 1 {
		t.Fatalf("want 1 diag, got %d", len(diags))
	}
	d := diags[0]
	if d.Severity != diag.SeverityWarning {
		t.Errorf("severity not propagated: %v", d.Severity)
	}
	if d.Hint != "create the file first" {
		t.Errorf("hint not propagated: %q", d.Hint)
	}
	if d.Line != 42 || d.File != "workspace/validate.yml" || d.Target != "exists" {
		t.Errorf("wrong location: %+v", d)
	}
	if !strings.Contains(d.Message, "missing.txt") {
		t.Errorf("message should mention missing path: %q", d.Message)
	}
}

func TestCommandRunner_NilCfgReturnsDiagnostic(t *testing.T) {
	def := &model.CommandDef{
		Type: model.CommandTypeShell,
		Cmd:  "true",
		ID:   "niltest",
	}
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(def)

	entry := config.CheckEntry{
		ID: "niltest", Type: "command", Cmd: "niltest",
		Severity: diag.SeverityError,
	}
	v := buildValidator(entry, t.TempDir(), reg)
	// Must not panic; must return a diagnostic instead.
	diags := v.Run(validate.Context{Cfg: nil})
	if len(diags) != 1 {
		t.Fatalf("want 1 diag on nil cfg, got %d", len(diags))
	}
	if diags[0].Severity != diag.SeverityError {
		t.Errorf("expected error severity, got %d", diags[0].Severity)
	}
}

func TestCommandRunner_NilCfgAlwaysEmitsError(t *testing.T) {
	// A config-load failure is an infrastructure problem, not a check result.
	// The diagnostic must be SeverityError regardless of the entry's declared severity.
	for _, sev := range []diag.Severity{diag.SeverityWarning, diag.SeverityInfo, diag.SeverityError} {
		def := &model.CommandDef{
			Type: model.CommandTypeShell,
			Cmd:  "true",
			ID:   "sevtest",
		}
		reg := registry.NewEmptyRegistry()
		reg.AddCommandForTest(def)

		entry := config.CheckEntry{
			ID: "sevtest", Type: "command", Cmd: "sevtest",
			Severity: sev,
		}
		v := buildValidator(entry, t.TempDir(), reg)
		diags := v.Run(validate.Context{Cfg: nil})
		if len(diags) != 1 {
			t.Fatalf("severity %d: want 1 diag on nil cfg, got %d", sev, len(diags))
		}
		if diags[0].Severity != diag.SeverityError {
			t.Errorf("severity %d: nil-cfg failure must always be SeverityError, got %d", sev, diags[0].Severity)
		}
	}
}

// --- Runtime dispatch (command) ---

func TestCommandRunner_PassesWithToParams(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "out.txt")

	// type: shell user command — writes ${param.foo} to a sentinel file.
	def := &model.CommandDef{
		Type: model.CommandTypeShell,
		Params: map[string]model.ParamDef{
			"foo": {Type: "string", Required: true},
		},
		Cmd: "printf %s \"$FOO\" > " + sentinel,
		Env: map[string]string{"FOO": "${param.foo}"},
		ID:  "writefoo",
	}
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(def)

	entry := config.CheckEntry{
		ID: "writefoo", Type: "command", Cmd: "writefoo",
		With:     map[string]any{"foo": "bar"},
		Severity: diag.SeverityError,
	}
	v := buildValidator(entry, dir, reg)
	diags := v.Run(validate.Context{Cfg: newCfg(), ProjectRoot: dir})
	if len(diags) != 1 || diags[0].Severity != validate.SeverityOK {
		t.Fatalf("expected one SeverityOK diagnostic on pass, got %+v", diags)
	}
	got, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("sentinel not written: %v", err)
	}
	if string(got) != "bar" {
		t.Errorf("with.foo not threaded into params: got %q want %q", got, "bar")
	}
}

func TestCommandRunner_NonInteractiveSkipsConfirm(t *testing.T) {
	// A user command with Confirmation=true would block on stdin without
	// NonInteractive=true. The check runner overrides both, so the command
	// must complete without hanging.
	def := &model.CommandDef{
		Type:             model.CommandTypeShell,
		Cmd:              "true",
		Confirmation:     true,
		ConfirmationText: "Proceed?",
		ID:               "confirmcmd",
	}
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(def)

	entry := config.CheckEntry{
		ID: "confirmcmd", Type: "command", Cmd: "confirmcmd",
		Severity: diag.SeverityError,
	}
	v := buildValidator(entry, t.TempDir(), reg)
	// If NonInteractive/SkipConfirm weren't applied, this would hang on stdin
	// and the test would time out rather than fail. A clean pass proves the
	// override path works.
	diags := v.Run(validate.Context{Cfg: newCfg()})
	if len(diags) != 1 || diags[0].Severity != validate.SeverityOK {
		t.Fatalf("expected one SeverityOK diagnostic on pass, got %+v", diags)
	}
}

func TestCommandRunner_FailureAttachesStderrTail(t *testing.T) {
	def := &model.CommandDef{
		Type: model.CommandTypeShell,
		Cmd:  "echo first-line >&2; echo last-line >&2; exit 1",
		ID:   "failer",
	}
	reg := registry.NewEmptyRegistry()
	reg.AddCommandForTest(def)

	entry := config.CheckEntry{
		ID: "failer", Type: "command", Cmd: "failer",
		Severity: diag.SeverityError,
	}
	v := buildValidator(entry, t.TempDir(), reg)
	diags := v.Run(validate.Context{Cfg: newCfg()})
	if len(diags) != 1 {
		t.Fatalf("want 1 diag, got %d", len(diags))
	}
	if !strings.Contains(diags[0].Message, "last-line") {
		t.Errorf("stderr tail not attached: %q", diags[0].Message)
	}
}

// --- lastLine helper ---

func TestLastLine(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"only":         "only",
		"a\nb\nc":      "c",
		"a\nb\nc\n":    "c",
		"a\nb\n  \n":   "",
		"line\n  end ": "end",
	}
	for in, want := range cases {
		if got := lastLine(in); got != want {
			t.Errorf("lastLine(%q) = %q, want %q", in, got, want)
		}
	}
}
