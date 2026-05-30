package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/shared/tpl"
)

// TestComputeFilePathsProbe_GlobMatchPresent tests probe with a matching glob candidate.
func TestComputeFilePathsProbe_GlobMatchPresent(t *testing.T) {
	tmpdir := t.TempDir()

	// Create matching dump files
	files := []string{"db_2026-04-28.sql.gz", "db_2026-04-29.sql.gz"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(tmpdir, f), []byte{}, 0o644); err != nil {
			t.Fatalf("create test file: %v", err)
		}
	}

	cmd := &CommandDef{
		ID:   "test.probe",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				Candidates: []FileCandidate{
					{
						Glob:  "db_*.sql.gz",
						Match: "^db_[0-9]{4}-[0-9]{2}-[0-9]{2}\\.sql\\.gz$",
						Sort:  FileSortNameDesc,
					},
				},
				Required: true,
			},
		},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Render: &tpl.RenderContext{
			Raw:     map[string]any{},
			Params:  map[string]any{},
			Context: map[string]any{},
		},
	}

	results, err := ComputeFilePathsProbe(ctx, []string{"dump"})
	if err != nil {
		t.Fatalf("ComputeFilePathsProbe: %v", err)
	}

	res := results["dump"]
	if !res.Resolved {
		t.Fatalf("expected Resolved=true for existing glob match")
	}
	if res.Err != nil {
		t.Fatalf("expected Err=nil, got %v", res.Err)
	}
	if res.Path == "" {
		t.Fatalf("expected non-empty Path for resolved file")
	}
}

// TestComputeFilePathsProbe_GlobNoMatchFallbackMissing tests probe with no glob match and missing fallback.
func TestComputeFilePathsProbe_GlobNoMatchFallbackMissing(t *testing.T) {
	tmpdir := t.TempDir()

	cmd := &CommandDef{
		ID:   "test.probe_nomatch",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				Candidates: []FileCandidate{
					{Glob: "db_*.sql.gz"},   // No matches
					{Path: "backup.sql.gz"}, // Missing fallback
				},
				Required: true,
			},
		},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Render: &tpl.RenderContext{
			Raw:     map[string]any{},
			Params:  map[string]any{},
			Context: map[string]any{},
		},
	}

	results, err := ComputeFilePathsProbe(ctx, []string{"dump"})
	if err != nil {
		t.Fatalf("ComputeFilePathsProbe: %v", err)
	}

	res := results["dump"]
	if res.Resolved {
		t.Fatalf("expected Resolved=false for missing file")
	}
	if res.Err != nil {
		t.Fatalf("expected Err=nil for soft missing, got %v", res.Err)
	}
	if res.Path != "" {
		t.Fatalf("expected empty Path for unresolved file, got %q", res.Path)
	}
}

// TestComputeFilePathsProbe_BadRegexError tests that bad regex in match produces a configuration error.
func TestComputeFilePathsProbe_BadRegexError(t *testing.T) {
	tmpdir := t.TempDir()

	if err := os.WriteFile(filepath.Join(tmpdir, "db.sql.gz"), []byte{}, 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	cmd := &CommandDef{
		ID:   "test.probe_bad_regex",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				Candidates: []FileCandidate{
					{Glob: "*.sql.gz", Match: "[invalid"},
				},
				Required: true,
			},
		},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Render: &tpl.RenderContext{
			Raw:     map[string]any{},
			Params:  map[string]any{},
			Context: map[string]any{},
		},
	}

	_, err := ComputeFilePathsProbe(ctx, []string{"dump"})
	if err == nil {
		t.Fatalf("expected configuration error for bad regex")
	}
	if !contains(err.Error(), "regex") {
		t.Fatalf("expected regex error, got: %v", err)
	}
}

// TestComputeFilePathsProbe_UnknownFileIDError tests that unknown file IDs are rejected.
func TestComputeFilePathsProbe_UnknownFileIDError(t *testing.T) {
	tmpdir := t.TempDir()

	cmd := &CommandDef{
		ID:   "test.probe_unknown_id",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				Path:   "test.txt",
			},
		},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Render: &tpl.RenderContext{
			Raw:     map[string]any{},
			Params:  map[string]any{},
			Context: map[string]any{},
		},
	}

	_, err := ComputeFilePathsProbe(ctx, []string{"unknown"})
	if err == nil {
		t.Fatalf("expected error for unknown file ID")
	}
	if !contains(err.Error(), "unknown") {
		t.Fatalf("expected unknown file error, got: %v", err)
	}
}

// TestComputeFilePathsProbe_EmptyOnlyError tests that empty only slice is rejected.
func TestComputeFilePathsProbe_EmptyOnlyError(t *testing.T) {
	tmpdir := t.TempDir()

	cmd := &CommandDef{
		ID:   "test.probe_empty",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				Path:   "test.txt",
			},
		},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Render: &tpl.RenderContext{
			Raw:     map[string]any{},
			Params:  map[string]any{},
			Context: map[string]any{},
		},
	}

	_, err := ComputeFilePathsProbe(ctx, []string{})
	if err == nil {
		t.Fatalf("expected error for empty only")
	}
	if !contains(err.Error(), "non-empty") {
		t.Fatalf("expected non-empty error, got: %v", err)
	}
}

// TestComputeFilePathsProbe_NilOnlyError tests that nil only slice is rejected.
func TestComputeFilePathsProbe_NilOnlyError(t *testing.T) {
	tmpdir := t.TempDir()

	cmd := &CommandDef{
		ID:   "test.probe_nil",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				Path:   "test.txt",
			},
		},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Render: &tpl.RenderContext{
			Raw:     map[string]any{},
			Params:  map[string]any{},
			Context: map[string]any{},
		},
	}

	_, err := ComputeFilePathsProbe(ctx, nil)
	if err == nil {
		t.Fatalf("expected error for nil only")
	}
}

// TestComputeFilePathsProbe_WriteOnlyError tests that write-only files cannot be probed.
func TestComputeFilePathsProbe_WriteOnlyError(t *testing.T) {
	tmpdir := t.TempDir()

	cmd := &CommandDef{
		ID:   "test.probe_writeonly",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"output": {
				Access: FileAccessWrite,
				Path:   "output.txt",
			},
		},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Render: &tpl.RenderContext{
			Raw:     map[string]any{},
			Params:  map[string]any{},
			Context: map[string]any{},
		},
	}

	_, err := ComputeFilePathsProbe(ctx, []string{"output"})
	if err == nil {
		t.Fatalf("expected error for write-only file in probe")
	}
	if !contains(err.Error(), "write") {
		t.Fatalf("expected write-only error, got: %v", err)
	}
}

// TestComputeFilePathsProbe_ReadWritePresent tests read_write file present.
func TestComputeFilePathsProbe_ReadWritePresent(t *testing.T) {
	tmpdir := t.TempDir()

	testFile := filepath.Join(tmpdir, "inout.txt")
	if err := os.WriteFile(testFile, []byte{}, 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	cmd := &CommandDef{
		ID:   "test.probe_readwrite",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"inout": {
				Access:   FileAccessReadWrite,
				Path:     "inout.txt",
				Required: true,
			},
		},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Render: &tpl.RenderContext{
			Raw:     map[string]any{},
			Params:  map[string]any{},
			Context: map[string]any{},
		},
	}

	results, err := ComputeFilePathsProbe(ctx, []string{"inout"})
	if err != nil {
		t.Fatalf("ComputeFilePathsProbe: %v", err)
	}

	res := results["inout"]
	if !res.Resolved {
		t.Fatalf("expected Resolved=true for existing read_write file")
	}
	if res.Err != nil {
		t.Fatalf("expected Err=nil, got %v", res.Err)
	}
	if res.Path != testFile {
		t.Fatalf("expected Path=%s, got %s", testFile, res.Path)
	}
}

// TestComputeFilePathsProbe_ReadWriteMissing tests read_write file missing (soft missing).
func TestComputeFilePathsProbe_ReadWriteMissing(t *testing.T) {
	tmpdir := t.TempDir()

	cmd := &CommandDef{
		ID:   "test.probe_readwrite_missing",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"inout": {
				Access:   FileAccessReadWrite,
				Path:     "inout.txt",
				Required: false,
			},
		},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Render: &tpl.RenderContext{
			Raw:     map[string]any{},
			Params:  map[string]any{},
			Context: map[string]any{},
		},
	}

	results, err := ComputeFilePathsProbe(ctx, []string{"inout"})
	if err != nil {
		t.Fatalf("ComputeFilePathsProbe: %v", err)
	}

	res := results["inout"]
	if res.Resolved {
		t.Fatalf("expected Resolved=false for missing read_write file")
	}
	if res.Err != nil {
		t.Fatalf("expected Err=nil (soft missing), got %v", res.Err)
	}
	if res.Path != "" {
		t.Fatalf("expected empty Path for unresolved file, got %q", res.Path)
	}
}

// TestComputeFilePathsProbe_MultipleFiles tests probing multiple files at once.
func TestComputeFilePathsProbe_MultipleFiles(t *testing.T) {
	tmpdir := t.TempDir()

	// Create one file, leave another missing
	file1 := filepath.Join(tmpdir, "file1.txt")
	if err := os.WriteFile(file1, []byte{}, 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	cmd := &CommandDef{
		ID:   "test.probe_multi",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"f1": {
				Access:   FileAccessRead,
				Path:     "file1.txt",
				Required: true,
			},
			"f2": {
				Access:   FileAccessRead,
				Path:     "file2.txt",
				Required: false,
			},
		},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Render: &tpl.RenderContext{
			Raw:     map[string]any{},
			Params:  map[string]any{},
			Context: map[string]any{},
		},
	}

	results, err := ComputeFilePathsProbe(ctx, []string{"f1", "f2"})
	if err != nil {
		t.Fatalf("ComputeFilePathsProbe: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// f1 should be resolved
	if !results["f1"].Resolved {
		t.Fatalf("expected f1 to be Resolved=true")
	}
	if results["f1"].Err != nil {
		t.Fatalf("expected f1 Err=nil, got %v", results["f1"].Err)
	}

	// f2 should be unresolved but without error
	if results["f2"].Resolved {
		t.Fatalf("expected f2 to be Resolved=false")
	}
	if results["f2"].Err != nil {
		t.Fatalf("expected f2 Err=nil, got %v", results["f2"].Err)
	}
}

// TestComputeFilePathsProbe_PathCandidate tests probe with a direct path candidate.
func TestComputeFilePathsProbe_PathCandidate(t *testing.T) {
	tmpdir := t.TempDir()

	testFile := filepath.Join(tmpdir, "input.txt")
	if err := os.WriteFile(testFile, []byte{}, 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	cmd := &CommandDef{
		ID:   "test.probe_path",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"input": {
				Access:   FileAccessRead,
				Path:     "input.txt",
				Required: true,
			},
		},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Render: &tpl.RenderContext{
			Raw:     map[string]any{},
			Params:  map[string]any{},
			Context: map[string]any{},
		},
	}

	results, err := ComputeFilePathsProbe(ctx, []string{"input"})
	if err != nil {
		t.Fatalf("ComputeFilePathsProbe: %v", err)
	}

	res := results["input"]
	if !res.Resolved {
		t.Fatalf("expected Resolved=true for existing path")
	}
	if res.Err != nil {
		t.Fatalf("expected Err=nil, got %v", res.Err)
	}
	if res.Path != testFile {
		t.Fatalf("expected Path=%s, got %s", testFile, res.Path)
	}
}

// TestComputeFilePathsProbe_NoFiles tests command with no files block.
func TestComputeFilePathsProbe_NoFiles(t *testing.T) {
	tmpdir := t.TempDir()

	cmd := &CommandDef{
		ID:    "test.probe_nofiles",
		Type:  CommandTypeScript,
		Files: map[string]FileSpec{},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Render: &tpl.RenderContext{
			Raw:     map[string]any{},
			Params:  map[string]any{},
			Context: map[string]any{},
		},
	}

	_, err := ComputeFilePathsProbe(ctx, []string{"dump"})
	if err == nil {
		t.Fatalf("expected error for command with no files")
	}
}
