package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		"DEVBOX_BIN=",
		"DEVBOX_FILES_JSON=",
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

func TestScriptRunner_ContractEnvVars_DevboxBin(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "bin.sh", `printf '%s' "$DEVBOX_BIN"`)

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
		t.Error("DEVBOX_BIN is empty")
	}
	if !filepath.IsAbs(out) {
		t.Errorf("DEVBOX_BIN is not absolute; got %q", out)
	}
}

func TestScriptRunner_ContractEnvVars_FilesJSON_Empty(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "files.sh", `printf '%s' "$DEVBOX_FILES_JSON"`)

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
		t.Fatalf("DEVBOX_FILES_JSON is not valid JSON: %v\ngot: %s", err, out)
	}
	if len(decoded) != 0 {
		t.Errorf("expected empty files JSON; got %v", decoded)
	}
}

func TestScriptRunner_ContractEnvVars_FilesJSON_WithFiles(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "files.sh", `printf '%s' "$DEVBOX_FILES_JSON"`)

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
		t.Fatalf("DEVBOX_FILES_JSON is not valid JSON: %v\ngot: %s", err, out)
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

// ---------------------------------------------------------------------------
// Files directive integration tests
// ---------------------------------------------------------------------------

func TestScriptRunner_FilesWrite_EnvInjection(t *testing.T) {
	dir := t.TempDir()
	// Script writes hello to the env var pointing to a file
	scriptPath := writeScript(t, dir, "write.sh", `printf 'hello' > "$DUMP_FILE"`)

	dumpDir := filepath.Join(dir, "dumps")
	dumpFile := filepath.Join(dumpDir, "db.sql.gz")

	cmd := &CommandDef{
		Type:   CommandTypeScript,
		ID:     "test.dump-write",
		Script: &ScriptDef{Path: scriptPath},
		Files: map[string]FileSpec{
			"dump": {
				Access:    FileAccessWrite,
				Path:      filepath.Join(dumpDir, "db.sql.gz"),
				Mkdir:     true,
				Overwrite: true,
				OnError:   FileOnErrorRemove,
				Env:       "DUMP_FILE",
			},
		},
	}

	var outBuf, errBuf bytes.Buffer
	ctx := RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{Raw: map[string]any{}, Params: map[string]any{}, Context: map[string]any{}},
		ProjectRoot: dir,
		Stdout:      &outBuf,
		Stderr:      &errBuf,
	}

	err := RunCommand(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// File should exist
	data, err := os.ReadFile(dumpFile)
	if err != nil {
		t.Fatalf("dump file not created: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("expected 'hello', got %q", string(data))
	}
}

func TestScriptRunner_FilesRead_GlobMatchSort(t *testing.T) {
	dir := t.TempDir()
	dumpsDir := filepath.Join(dir, "dumps")
	if err := os.MkdirAll(dumpsDir, 0o755); err != nil {
		t.Fatalf("mkdir dumps: %v", err)
	}

	// Create multiple dated dump files with different modification times
	files := []struct {
		name string
		time time.Time
	}{
		{"db_2026-04-27.sql.gz", time.Date(2026, 4, 27, 10, 0, 0, 0, time.Local)},
		{"db_2026-04-28.sql.gz", time.Date(2026, 4, 28, 10, 0, 0, 0, time.Local)},
		{"db_2026-04-29.sql.gz", time.Date(2026, 4, 29, 10, 0, 0, 0, time.Local)},
	}
	for _, f := range files {
		path := filepath.Join(dumpsDir, f.name)
		if err := os.WriteFile(path, []byte("dump data"), 0o644); err != nil {
			t.Fatalf("write file %s: %v", f.name, err)
		}
		// Set modification time to match the date in the filename
		if err := os.Chtimes(path, f.time, f.time); err != nil {
			t.Fatalf("chtimes for %s: %v", f.name, err)
		}
	}

	// Script reads from DUMP_FILE env var
	scriptPath := writeScript(t, dir, "read.sh", `
if [ -f "$DUMP_FILE" ]; then
  basename "$DUMP_FILE"
else
  exit 1
fi
`)

	cmd := &CommandDef{
		Type:   CommandTypeScript,
		ID:     "test.dump-read",
		Script: &ScriptDef{Path: scriptPath},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				Candidates: []FileCandidate{
					{
						Glob:  "dumps/db_*.sql.gz",
						Match: `db_\d{4}-\d{2}-\d{2}`,
						Sort:  FileSortModtimeDesc,
					},
				},
				Required: true,
				Env:      "DUMP_FILE",
			},
		},
	}

	var outBuf, errBuf bytes.Buffer
	ctx := RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{Raw: map[string]any{}, Params: map[string]any{}, Context: map[string]any{}},
		ProjectRoot: dir,
		Stdout:      &outBuf,
		Stderr:      &errBuf,
	}

	err := RunCommand(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := outBuf.String()
	// The newest file (by modtime) should be selected; this is db_2026-04-29.sql.gz
	if !strings.Contains(out, "db_2026-04-29.sql.gz") {
		t.Errorf("expected newest file to be selected; got output: %q", out)
	}
}

func TestScriptRunner_FilesOnError_RemovesFailed(t *testing.T) {
	dir := t.TempDir()
	dumpDir := filepath.Join(dir, "dumps")
	dumpFile := filepath.Join(dumpDir, "db.sql.gz")

	// Script fails after creating the file
	scriptPath := writeScript(t, dir, "fail.sh", `
touch "$DUMP_FILE"
exit 1
`)

	cmd := &CommandDef{
		Type:   CommandTypeScript,
		ID:     "test.dump-fail",
		Script: &ScriptDef{Path: scriptPath},
		Files: map[string]FileSpec{
			"dump": {
				Access:    FileAccessWrite,
				Path:      dumpFile,
				Mkdir:     true,
				Overwrite: true,
				OnError:   FileOnErrorRemove,
				Env:       "DUMP_FILE",
			},
		},
	}

	var outBuf, errBuf bytes.Buffer
	ctx := RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{Raw: map[string]any{}, Params: map[string]any{}, Context: map[string]any{}},
		ProjectRoot: dir,
		Stdout:      &outBuf,
		Stderr:      &errBuf,
	}

	err := RunCommand(ctx)
	if err == nil {
		t.Fatal("expected error from failing script")
	}

	// File should have been removed by on_error cleanup
	if _, err := os.Stat(dumpFile); !os.IsNotExist(err) {
		t.Errorf("expected dump file to be removed on error; still exists")
	}
}

func TestScriptRunner_ExitErrorIncludesScriptPath(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "missing-command.sh", `definitely-not-a-devbox-test-command`)

	cmd := &CommandDef{
		Type:   CommandTypeScript,
		ID:     "test.missing-command",
		Script: &ScriptDef{Path: scriptPath, Shell: "sh"},
	}

	err := (&ScriptRunner{}).Run(RunContext{
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

func TestScriptRunner_FilesOnError_PreservesExisting(t *testing.T) {
	dir := t.TempDir()
	dumpDir := filepath.Join(dir, "dumps")
	dumpFile := filepath.Join(dumpDir, "db.sql.gz")

	// Pre-create the file
	if err := os.MkdirAll(dumpDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dumpFile, []byte("existing data"), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	// Script fails
	scriptPath := writeScript(t, dir, "fail.sh", `exit 1`)

	cmd := &CommandDef{
		Type:   CommandTypeScript,
		ID:     "test.dump-fail-existing",
		Script: &ScriptDef{Path: scriptPath},
		Files: map[string]FileSpec{
			"dump": {
				Access:    FileAccessWrite,
				Path:      dumpFile,
				Overwrite: true,
				OnError:   FileOnErrorRemove,
				Env:       "DUMP_FILE",
			},
		},
	}

	var outBuf, errBuf bytes.Buffer
	ctx := RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{Raw: map[string]any{}, Params: map[string]any{}, Context: map[string]any{}},
		ProjectRoot: dir,
		Stdout:      &outBuf,
		Stderr:      &errBuf,
	}

	err := RunCommand(ctx)
	if err == nil {
		t.Fatal("expected error from failing script")
	}

	// File should still exist (pre-existing, not removed)
	data, err := os.ReadFile(dumpFile)
	if err != nil {
		t.Fatalf("expected file to be preserved; got error: %v", err)
	}
	if string(data) != "existing data" {
		t.Errorf("expected 'existing data'; got %q", string(data))
	}
}

func TestScriptRunner_ContractEnvVars_NonInteractiveContext(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "nonint.sh", `printf '%s' "$DEVBOX_NONINTERACTIVE"`)

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
		t.Errorf("expected DEVBOX_NONINTERACTIVE=1 when NonInteractive=true; got %q", out)
	}
}
