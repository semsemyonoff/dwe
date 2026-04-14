package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/tpl"
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

// captureOutput runs the ScriptRunner and captures stdout/stderr.
func captureOutput(t *testing.T, ctx RunContext) (string, string, error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	ctx.Stdout = &outBuf
	ctx.Stderr = &errBuf
	r := &ScriptRunner{}
	err := r.Run(ctx)
	return outBuf.String(), errBuf.String(), err
}

// ---------------------------------------------------------------------------
// Contract env vars
// ---------------------------------------------------------------------------

func TestScriptRunner_ContractEnvVars(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "check.sh", `
env | grep -E '^DEVBOX_' | sort
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
		"DEVBOX_ROOT=",
		"DEVBOX_COMMAND_ID=test.check",
		"DEVBOX_TEMP_DIR=",
		"DEVBOX_NONINTERACTIVE=",
		"DEVBOX_PARAMS_JSON=",
		"DEVBOX_CONTEXT_JSON=",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output; got:\n%s", want, out)
		}
	}
}

func TestScriptRunner_ContractEnvVars_ParamsJSON(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "params.sh", `printf '%s' "$DEVBOX_PARAMS_JSON"`)

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
		t.Fatalf("DEVBOX_PARAMS_JSON is not valid JSON: %v\ngot: %s", err, out)
	}
	if decoded["foo"] != "bar" {
		t.Errorf("expected foo=bar in params JSON; got %v", decoded)
	}
}

func TestScriptRunner_ContractEnvVars_ContextJSON(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "ctx.sh", `printf '%s' "$DEVBOX_CONTEXT_JSON"`)

	cmd := &CommandDef{
		Type:    CommandTypeScript,
		ID:      "test.ctx",
		Script:  &ScriptDef{Path: scriptPath},
		Context: map[string]ContextDef{},
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
		t.Fatalf("DEVBOX_CONTEXT_JSON is not valid JSON: %v\ngot: %s", err, out)
	}
	if decoded["db"] != "mydb" {
		t.Errorf("expected db=mydb in context JSON; got %v", decoded)
	}
}

// ---------------------------------------------------------------------------
// Simple mode
// ---------------------------------------------------------------------------

func TestScriptRunner_SimpleMode(t *testing.T) {
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

func TestScriptRunner_SimpleMode_ScriptFails(t *testing.T) {
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

func TestScriptRunner_PhasedMode_AllPhases(t *testing.T) {
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

func TestScriptRunner_PhasedMode_CleanupRunsOnFailure(t *testing.T) {
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

func TestScriptRunner_PhasedMode_PlanFails_RunNotExecuted(t *testing.T) {
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

func TestScriptRunner_ShellOverride(t *testing.T) {
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
// DEVBOX_TEMP_DIR cleanup
// ---------------------------------------------------------------------------

func TestScriptRunner_TempDirCreatedAndCleaned(t *testing.T) {
	dir := t.TempDir()
	tmpCapture := filepath.Join(dir, "tmpdir.txt")

	scriptPath := writeScript(t, dir, "tmpdir.sh", `printf '%s' "$DEVBOX_TEMP_DIR" > `+tmpCapture)

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
		t.Fatal("DEVBOX_TEMP_DIR was empty")
	}

	// After Run returns the temp dir should be cleaned up.
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("expected temp dir %q to be cleaned up after Run", tmpPath)
	}
}

// ---------------------------------------------------------------------------
// NewRunner dispatching
// ---------------------------------------------------------------------------

func TestNewRunner_Returns_ScriptRunner(t *testing.T) {
	cmd := &CommandDef{Type: CommandTypeScript}
	runner, err := NewRunner(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := runner.(*ScriptRunner); !ok {
		t.Errorf("expected *ScriptRunner, got %T", runner)
	}
}
