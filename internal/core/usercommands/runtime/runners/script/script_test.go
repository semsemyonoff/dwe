package script

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// Local aliases keep the moved tests readable without rewriting every type
// qualifier. The tests live in the same package as Runner so the original
// `&Runner{}` form is renamed in-place to `&Runner{}`.
type (
	CommandDef    = model.CommandDef
	RunContext    = spec.RunContext
	ScriptDef     = model.ScriptDef
	FileSpec      = model.FileSpec
	FileCandidate = model.FileCandidate
)

const (
	CommandTypeScript = model.CommandTypeScript
	FileAccessRead    = model.FileAccessRead
	FileAccessWrite   = model.FileAccessWrite
	FileOnErrorRemove = model.FileOnErrorRemove
)

// writeScript writes content to a temporary file and returns its path.
func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatalf("writeScript: %v", err)
	}
	return p
}

// captureOutput runs the Runner and captures stdout/stderr.
func captureOutput(t *testing.T, ctx RunContext) (string, string, error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	ctx.Stdout = &outBuf
	ctx.Stderr = &errBuf
	r := &Runner{}
	err := r.Run(context.Background(), ctx)
	return outBuf.String(), errBuf.String(), err
}

// ---------------------------------------------------------------------------
// Contract env vars
// ---------------------------------------------------------------------------

func TestRunner_ContractEnvVars(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "check.sh", `
env | grep -E '^DWE_' | sort
`)

	cmd := &CommandDef{
		Type:   CommandTypeScript,
		ID:     "test.check",
		Script: &ScriptDef{Path: scriptPath},
	}
	ctx := RunContext{
		Cmd:         cmd,
		Params:      map[string]any{"key": "val"},
		Context:     map[string]any{"cfg": "cfgval"},
		Render:      &tpl.RenderContext{},
		ProjectRoot: dir,
	}

	out, _, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify all contract vars are present in the output.
	for _, want := range []string{
		"DWE_ROOT=",
		"DWE_COMMAND_ID=test.check",
		"DWE_TEMP_DIR=",
		"DWE_NONINTERACTIVE=",
		"DWE_PARAMS_JSON=",
		"DWE_CONTEXT_JSON=",
		"DWE_BIN=",
		"DWE_FILES_JSON=",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output; got:\n%s", want, out)
		}
	}
}

func TestRunner_ContractEnvVars_ParamsJSON(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "params.sh", `printf '%s' "$DWE_PARAMS_JSON"`)

	cmd := &CommandDef{
		Type:   CommandTypeScript,
		ID:     "test.params",
		Script: &ScriptDef{Path: scriptPath},
	}
	ctx := RunContext{
		Cmd:         cmd,
		Params:      map[string]any{"foo": "bar", "count": 3},
		Render:      &tpl.RenderContext{},
		ProjectRoot: dir,
	}

	out, _, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("DWE_PARAMS_JSON is not valid JSON: %v\ngot: %s", err, out)
	}
	if decoded["foo"] != "bar" {
		t.Errorf("expected foo=bar in params JSON; got %v", decoded)
	}
}

func TestRunner_ContractEnvVars_ContextJSON(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "ctx.sh", `printf '%s' "$DWE_CONTEXT_JSON"`)

	cmd := &CommandDef{
		Type:    CommandTypeScript,
		ID:      "test.ctx",
		Script:  &ScriptDef{Path: scriptPath},
		Context: map[string]model.ContextDef{},
	}
	ctx := RunContext{
		Cmd:         cmd,
		Context:     map[string]any{"db": "mydb"},
		Render:      &tpl.RenderContext{},
		ProjectRoot: dir,
	}

	out, _, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("DWE_CONTEXT_JSON is not valid JSON: %v\ngot: %s", err, out)
	}
	if decoded["db"] != "mydb" {
		t.Errorf("expected db=mydb in context JSON; got %v", decoded)
	}
}

func TestRunner_ContractEnvVars_DweBin(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "bin.sh", `printf '%s' "$DWE_BIN"`)

	cmd := &CommandDef{
		Type:   CommandTypeScript,
		ID:     "test.bin",
		Script: &ScriptDef{Path: scriptPath},
	}
	ctx := RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{},
		ProjectRoot: dir,
	}

	out, _, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out == "" {
		t.Error("DWE_BIN is empty")
	}
	if !filepath.IsAbs(out) {
		t.Errorf("DWE_BIN is not absolute; got %q", out)
	}
}

func TestRunner_ContractEnvVars_FilesJSON_Empty(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "files.sh", `printf '%s' "$DWE_FILES_JSON"`)

	cmd := &CommandDef{
		Type:   CommandTypeScript,
		ID:     "test.files-empty",
		Script: &ScriptDef{Path: scriptPath},
	}
	ctx := RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{},
		ProjectRoot: dir,
	}

	out, _, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("DWE_FILES_JSON is not valid JSON: %v\ngot: %s", err, out)
	}
	if len(decoded) != 0 {
		t.Errorf("expected empty files JSON; got %v", decoded)
	}
}

func TestRunner_ContractEnvVars_FilesJSON_WithFiles(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "files.sh", `printf '%s' "$DWE_FILES_JSON"`)

	cmd := &CommandDef{
		Type:   CommandTypeScript,
		ID:     "test.files-with",
		Script: &ScriptDef{Path: scriptPath},
	}
	ctx := RunContext{
		Cmd: cmd,
		Render: &tpl.RenderContext{
			Files: map[string]tpl.ResolvedFile{
				"dump": {Path: "/tmp/db_2026-04-29.sql.gz"},
				"log":  {Path: "/tmp/app.log"},
			},
		},
		ProjectRoot: dir,
	}

	out, _, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded map[string]map[string]string
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("DWE_FILES_JSON is not valid JSON: %v\ngot: %s", err, out)
	}

	if decoded["dump"]["path"] != "/tmp/db_2026-04-29.sql.gz" {
		t.Errorf("expected dump path in files JSON; got %v", decoded)
	}
	if decoded["log"]["path"] != "/tmp/app.log" {
		t.Errorf("expected log path in files JSON; got %v", decoded)
	}
}

// ---------------------------------------------------------------------------
// Simple mode
// ---------------------------------------------------------------------------

func TestRunner_SimpleMode(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "simple.sh", `printf 'hello from simple'`)

	cmd := &CommandDef{
		Type:   CommandTypeScript,
		ID:     "test.simple",
		Script: &ScriptDef{Path: scriptPath},
	}
	ctx := RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{},
		ProjectRoot: dir,
	}

	out, _, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "hello from simple") {
		t.Errorf("expected 'hello from simple'; got %q", out)
	}
}

func TestRunner_SimpleMode_ScriptFails(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "fail.sh", `exit 42`)

	cmd := &CommandDef{
		Type:   CommandTypeScript,
		ID:     "test.fail",
		Script: &ScriptDef{Path: scriptPath},
	}
	ctx := RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{},
		ProjectRoot: dir,
	}

	_, _, err := captureOutput(t, ctx)
	if err == nil {
		t.Fatal("expected error for failing script")
	}
}

// ---------------------------------------------------------------------------
// Phased mode
// ---------------------------------------------------------------------------

func TestRunner_PhasedMode_AllPhases(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "phases.log")

	plan := writeScript(t, dir, "plan.sh", `printf 'plan\n' >> `+logFile)
	run := writeScript(t, dir, "run.sh", `printf 'run\n' >> `+logFile)
	cleanup := writeScript(t, dir, "cleanup.sh", `printf 'cleanup\n' >> `+logFile)

	cmd := &CommandDef{
		Type: CommandTypeScript,
		ID:   "test.phased",
		Script: &ScriptDef{
			Plan:    plan,
			Run:     run,
			Cleanup: cleanup,
		},
	}
	ctx := RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{},
		ProjectRoot: dir,
	}

	_, _, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(logFile)
	got := string(data)
	for _, want := range []string{"plan", "run", "cleanup"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in log; got %q", want, got)
		}
	}
}

func TestRunner_PhasedMode_CleanupRunsOnFailure(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "phases.log")

	run := writeScript(t, dir, "run.sh", `printf 'run\n' >> `+logFile+`; exit 1`)
	cleanup := writeScript(t, dir, "cleanup.sh", `printf 'cleanup\n' >> `+logFile)

	cmd := &CommandDef{
		Type: CommandTypeScript,
		ID:   "test.cleanup-on-fail",
		Script: &ScriptDef{
			Run:     run,
			Cleanup: cleanup,
		},
	}
	ctx := RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{},
		ProjectRoot: dir,
	}

	_, _, err := captureOutput(t, ctx)
	if err == nil {
		t.Fatal("expected error from failing run script")
	}

	data, _ := os.ReadFile(logFile)
	got := string(data)
	if !strings.Contains(got, "cleanup") {
		t.Errorf("cleanup should have run even after failure; log: %q", got)
	}
}

func TestRunner_PhasedMode_PlanFails_RunNotExecuted(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "phases.log")

	plan := writeScript(t, dir, "plan.sh", `printf 'plan\n' >> `+logFile+`; exit 1`)
	run := writeScript(t, dir, "run.sh", `printf 'run\n' >> `+logFile)

	cmd := &CommandDef{
		Type: CommandTypeScript,
		ID:   "test.plan-fail",
		Script: &ScriptDef{
			Plan: plan,
			Run:  run,
		},
	}
	ctx := RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{},
		ProjectRoot: dir,
	}

	_, _, err := captureOutput(t, ctx)
	if err == nil {
		t.Fatal("expected error from failing plan script")
	}

	data, _ := os.ReadFile(logFile)
	got := string(data)
	if strings.Contains(got, "run") {
		t.Error("run should NOT have been executed after plan failure")
	}
}

// ---------------------------------------------------------------------------
// Shell override
// ---------------------------------------------------------------------------

func TestRunner_ShellOverride(t *testing.T) {
	// Use bash if available; otherwise skip.
	if _, err := os.Stat("/bin/bash"); os.IsNotExist(err) {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "shell.sh", `printf 'bash-ok'`)

	cmd := &CommandDef{
		Type:   CommandTypeScript,
		ID:     "test.shell",
		Script: &ScriptDef{Path: scriptPath, Shell: "bash"},
	}
	ctx := RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{},
		ProjectRoot: dir,
	}

	out, _, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "bash-ok") {
		t.Errorf("expected 'bash-ok'; got %q", out)
	}
}

// ---------------------------------------------------------------------------
// DWE_TEMP_DIR cleanup
// ---------------------------------------------------------------------------

func TestRunner_TempDirCreatedAndCleaned(t *testing.T) {
	dir := t.TempDir()
	tmpCapture := filepath.Join(dir, "tmpdir.txt")

	scriptPath := writeScript(t, dir, "tmpdir.sh", `printf '%s' "$DWE_TEMP_DIR" > `+tmpCapture)

	cmd := &CommandDef{
		Type:   CommandTypeScript,
		ID:     "test.tmpdir",
		Script: &ScriptDef{Path: scriptPath},
	}
	ctx := RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{},
		ProjectRoot: dir,
	}

	_, _, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	captured, _ := os.ReadFile(tmpCapture)
	tmpPath := strings.TrimSpace(string(captured))
	if tmpPath == "" {
		t.Fatal("DWE_TEMP_DIR was empty")
	}

	// After Run returns the temp dir should be cleaned up.
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("expected temp dir %q to be cleaned up after Run", tmpPath)
	}
}

// ---------------------------------------------------------------------------
// Files directive integration tests
// ---------------------------------------------------------------------------

func TestRunner_ExitErrorIncludesScriptPath(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "missing-command.sh", `definitely-not-a-dwe-test-command`)

	cmd := &CommandDef{
		Type:   CommandTypeScript,
		ID:     "test.missing-command",
		Script: &ScriptDef{Path: scriptPath, Shell: "sh"},
	}

	err := (&Runner{}).Run(context.Background(), RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{Raw: map[string]any{}, Params: map[string]any{}, Context: map[string]any{}},
		ProjectRoot: dir,
		Stderr:      &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected script error")
	}
	got := err.Error()
	if !strings.Contains(got, scriptPath) {
		t.Fatalf("error should include script path; got %q", got)
	}
	if !strings.Contains(got, "exit 127 usually means a command was not found") {
		t.Fatalf("error should explain exit 127; got %q", got)
	}
}

func TestRunner_ContractEnvVars_NonInteractiveContext(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "nonint.sh", `printf '%s' "$DWE_NONINTERACTIVE"`)

	cmd := &CommandDef{
		Type:   CommandTypeScript,
		ID:     "test.nonint-context",
		Script: &ScriptDef{Path: scriptPath},
	}
	ctx := RunContext{
		Cmd:            cmd,
		Render:         &tpl.RenderContext{},
		ProjectRoot:    dir,
		NonInteractive: true,
	}

	out, _, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out != "1" {
		t.Errorf("expected DWE_NONINTERACTIVE=1 when NonInteractive=true; got %q", out)
	}
}

// ---------------------------------------------------------------------------
// Workdir support
// ---------------------------------------------------------------------------

func TestRunner_Workdir_AbsolutePath(t *testing.T) {
	projectRoot := t.TempDir()
	workdirAbs := t.TempDir()

	// Resolve symlinks for consistent comparison (macOS)
	workdirAbs, _ = filepath.EvalSymlinks(workdirAbs)

	// Create a script in projectRoot that checks pwd
	scriptPath := writeScript(t, projectRoot, "check_pwd.sh", `pwd`)

	cmd := &CommandDef{
		Type:    CommandTypeScript,
		ID:      "test.workdir-abs",
		Workdir: workdirAbs,
		Script:  &ScriptDef{Path: scriptPath},
	}
	ctx := RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{Params: map[string]any{}, Context: map[string]any{}},
		ProjectRoot: projectRoot,
	}

	out, _, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out = strings.TrimSpace(out)
	expected, _ := filepath.EvalSymlinks(out)
	if expected != workdirAbs {
		t.Errorf("expected pwd=%q; got %q", workdirAbs, expected)
	}
}

func TestRunner_Workdir_RelativePath(t *testing.T) {
	projectRoot := t.TempDir()
	subdir := filepath.Join(projectRoot, "subdir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	scriptPath := writeScript(t, projectRoot, "check_pwd.sh", `pwd`)

	cmd := &CommandDef{
		Type:    CommandTypeScript,
		ID:      "test.workdir-rel",
		Workdir: "subdir",
		Script:  &ScriptDef{Path: scriptPath},
	}
	ctx := RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{Params: map[string]any{}, Context: map[string]any{}},
		ProjectRoot: projectRoot,
	}

	out, _, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out = strings.TrimSpace(out)
	expectedPwd, _ := filepath.EvalSymlinks(subdir)
	outResolved, _ := filepath.EvalSymlinks(out)
	if outResolved != expectedPwd {
		t.Errorf("expected pwd=%q; got %q", expectedPwd, outResolved)
	}
}

func TestRunner_Workdir_ScriptPathIsProjectRootRelative(t *testing.T) {
	projectRoot := t.TempDir()
	workdirAbs := t.TempDir()

	// Create script in projectRoot/scripts/
	scriptsDir := filepath.Join(projectRoot, "scripts")
	if err := os.Mkdir(scriptsDir, 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}

	// Script that checks both its location (via $0) and current working directory
	_ = writeScript(t, scriptsDir, "check_paths.sh", `echo "$0"; pwd`)

	cmd := &CommandDef{
		Type:    CommandTypeScript,
		ID:      "test.script-path-abs",
		Workdir: workdirAbs,
		Script:  &ScriptDef{Path: "scripts/check_paths.sh"},
	}
	ctx := RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{Params: map[string]any{}, Context: map[string]any{}},
		ProjectRoot: projectRoot,
	}

	out, _, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected 2+ lines of output; got %q", out)
	}

	scriptPathOutput := lines[0]
	pwdOutput := strings.Join(lines[1:], "\n")

	// Script should still be referenced from project root perspective
	if !strings.Contains(scriptPathOutput, "scripts/check_paths.sh") && !strings.Contains(scriptPathOutput, "check_paths.sh") {
		t.Errorf("expected script path to include 'check_paths.sh'; got %q", scriptPathOutput)
	}

	// But pwd should reflect workdir (resolved for symlinks on macOS)
	pwdResolved, _ := filepath.EvalSymlinks(pwdOutput)
	workdirResolved, _ := filepath.EvalSymlinks(workdirAbs)
	if pwdResolved != workdirResolved {
		t.Errorf("expected pwd=%q; got %q", workdirResolved, pwdResolved)
	}
}

func TestRunner_Workdir_TemplateRendering(t *testing.T) {
	projectRoot := t.TempDir()
	subdir := filepath.Join(projectRoot, "mydir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir mydir: %v", err)
	}

	scriptPath := writeScript(t, projectRoot, "check_pwd.sh", `pwd`)

	cmd := &CommandDef{
		Type:    CommandTypeScript,
		ID:      "test.workdir-template",
		Workdir: "${param.dir}",
		Script:  &ScriptDef{Path: scriptPath},
	}
	ctx := RunContext{
		Cmd: cmd,
		Render: &tpl.RenderContext{
			Params:  map[string]any{"dir": "mydir"},
			Context: map[string]any{},
		},
		ProjectRoot: projectRoot,
	}

	out, _, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out = strings.TrimSpace(out)
	expected, _ := filepath.EvalSymlinks(subdir)
	outResolved, _ := filepath.EvalSymlinks(out)
	if outResolved != expected {
		t.Errorf("expected pwd=%q (from template); got %q", expected, outResolved)
	}
}

func TestRunner_Workdir_RenderError(t *testing.T) {
	projectRoot := t.TempDir()
	scriptPath := writeScript(t, projectRoot, "check_pwd.sh", `pwd`)

	// Use invalid Go template syntax to trigger a parse error
	cmd := &CommandDef{
		Type:    CommandTypeScript,
		ID:      "test.workdir-render-err",
		Workdir: "{{ .Invalid.Unclosed",
		Script:  &ScriptDef{Path: scriptPath},
	}
	ctx := RunContext{
		Cmd: cmd,
		Render: &tpl.RenderContext{
			Raw:     map[string]any{},
			Params:  map[string]any{},
			Context: map[string]any{},
		},
		ProjectRoot: projectRoot,
	}

	_, _, err := captureOutput(t, ctx)
	if err == nil {
		t.Fatal("expected error when workdir template fails to render")
	}
	if !strings.Contains(err.Error(), "render workdir") {
		t.Errorf("expected 'render workdir' in error; got %q", err)
	}
}

func TestRunner_Workdir_Empty_FallsBackToProjectRoot(t *testing.T) {
	projectRoot := t.TempDir()
	scriptPath := writeScript(t, projectRoot, "check_pwd.sh", `pwd`)

	cmd := &CommandDef{
		Type:    CommandTypeScript,
		ID:      "test.workdir-empty",
		Workdir: "",
		Script:  &ScriptDef{Path: scriptPath},
	}
	ctx := RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{Params: map[string]any{}, Context: map[string]any{}},
		ProjectRoot: projectRoot,
	}

	out, _, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out = strings.TrimSpace(out)
	expected, _ := filepath.EvalSymlinks(projectRoot)
	outResolved, _ := filepath.EvalSymlinks(out)
	if outResolved != expected {
		t.Errorf("expected pwd=%q (project root); got %q", expected, outResolved)
	}
}
