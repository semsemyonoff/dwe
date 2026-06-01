package runtime

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// writeScriptFile is the integration-test sibling of the subpkg's writeScript
// helper. Kept here so root tests stay self-contained and don't import the
// runners/script subpkg's _test files.
func writeScriptFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatalf("writeScriptFile: %v", err)
	}
	return p
}

func TestScriptRunner_FilesWrite_EnvInjection(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScriptFile(t, dir, "write.sh", `printf 'hello' > "$DUMP_FILE"`)

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

	if err := RunCommand(context.Background(), ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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
		if err := os.Chtimes(path, f.time, f.time); err != nil {
			t.Fatalf("chtimes for %s: %v", f.name, err)
		}
	}

	scriptPath := writeScriptFile(t, dir, "read.sh", `
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

	if err := RunCommand(context.Background(), ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := outBuf.String()
	if !strings.Contains(out, "db_2026-04-29.sql.gz") {
		t.Errorf("expected newest file to be selected; got output: %q", out)
	}
}

func TestScriptRunner_FilesOnError_RemovesFailed(t *testing.T) {
	dir := t.TempDir()
	dumpDir := filepath.Join(dir, "dumps")
	dumpFile := filepath.Join(dumpDir, "db.sql.gz")

	scriptPath := writeScriptFile(t, dir, "fail.sh", `
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

	if err := RunCommand(context.Background(), ctx); err == nil {
		t.Fatal("expected error from failing script")
	}

	if _, err := os.Stat(dumpFile); !os.IsNotExist(err) {
		t.Errorf("expected dump file to be removed on error; still exists")
	}
}

func TestScriptRunner_FilesOnError_PreservesExisting(t *testing.T) {
	dir := t.TempDir()
	dumpDir := filepath.Join(dir, "dumps")
	dumpFile := filepath.Join(dumpDir, "db.sql.gz")

	if err := os.MkdirAll(dumpDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dumpFile, []byte("existing data"), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	scriptPath := writeScriptFile(t, dir, "fail.sh", `exit 1`)

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

	if err := RunCommand(context.Background(), ctx); err == nil {
		t.Fatal("expected error from failing script")
	}

	data, err := os.ReadFile(dumpFile)
	if err != nil {
		t.Fatalf("expected file to be preserved; got error: %v", err)
	}
	if string(data) != "existing data" {
		t.Errorf("expected 'existing data'; got %q", string(data))
	}
}
