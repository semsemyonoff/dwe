package runtime

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// TestComputeFilePaths_SinglePathHit tests basic path resolution in read mode.
func TestComputeFilePaths_SinglePathHit(t *testing.T) {
	tmpdir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(tmpdir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	cmd := &CommandDef{
		ID:   "test.read",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"input": {
				Access:   FileAccessRead,
				Path:     "test.txt",
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

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}

	if resolved, ok := paths["input"]; !ok {
		t.Fatalf("expected path with id 'input', not found")
	} else if resolved.Path != testFile {
		t.Fatalf("expected path %s, got %s", testFile, resolved.Path)
	}
}

// TestComputeFilePaths_SinglePathMissRequired tests required path miss error.
func TestComputeFilePaths_SinglePathMissRequired(t *testing.T) {
	tmpdir := t.TempDir()

	cmd := &CommandDef{
		ID:   "test.read",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"input": {
				Access:   FileAccessRead,
				Path:     "missing.txt",
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

	_, err := ComputeFilePaths(ctx)
	if err == nil {
		t.Fatalf("expected error for missing required file")
	}
	if !contains(err.Error(), "required file not found") {
		t.Fatalf("expected 'required file not found' error, got: %v", err)
	}
}

// TestComputeFilePaths_SinglePathMissOptional tests optional path miss (omit from result).
func TestComputeFilePaths_SinglePathMissOptional(t *testing.T) {
	tmpdir := t.TempDir()

	cmd := &CommandDef{
		ID:   "test.read",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"input": {
				Access:   FileAccessRead,
				Path:     "missing.txt",
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

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	if len(paths) != 0 {
		t.Fatalf("expected 0 paths (optional miss), got %d", len(paths))
	}
}

// TestComputeFilePaths_CandidatesGlobMatch tests glob candidate with match filtering and sort.
func TestComputeFilePaths_CandidatesGlobMatch(t *testing.T) {
	tmpdir := t.TempDir()

	// Create test files
	files := []string{"db_2026-04-28.sql.gz", "db_2026-04-29.sql.gz", "db_2026-04-30.sql.gz"}
	for _, f := range files {
		path := filepath.Join(tmpdir, f)
		if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
			t.Fatalf("create test file: %v", err)
		}
	}

	cmd := &CommandDef{
		ID:   "test.glob",
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

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}

	// Should select the last file (desc sort by name)
	expected := filepath.Join(tmpdir, "db_2026-04-30.sql.gz")
	if resolved, ok := paths["dump"]; !ok {
		t.Fatalf("expected path with id 'dump', not found")
	} else if resolved.Path != expected {
		t.Fatalf("expected path %s, got %s", expected, resolved.Path)
	}
}

// TestComputeFilePaths_CandidatesModtimeDesc tests modtime descending (newest first).
func TestComputeFilePaths_CandidatesModtimeDesc(t *testing.T) {
	tmpdir := t.TempDir()

	// Create files with different modification times (spaced apart)
	files := []struct {
		name string
		path string
	}{
		{"old.txt", filepath.Join(tmpdir, "old.txt")},
		{"mid.txt", filepath.Join(tmpdir, "mid.txt")},
		{"new.txt", filepath.Join(tmpdir, "new.txt")},
	}

	for i, f := range files {
		if err := os.WriteFile(f.path, []byte{}, 0o644); err != nil {
			t.Fatalf("create test file: %v", err)
		}
		// Set explicit modification times
		newTime := time.Now().Add(time.Duration(-1000+i*100) * time.Second)
		if err := os.Chtimes(f.path, newTime, newTime); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	cmd := &CommandDef{
		ID:   "test.modtime",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"latest": {
				Access: FileAccessRead,
				Candidates: []FileCandidate{
					{
						Glob: "*.txt",
						Sort: FileSortModtimeDesc,
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

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}

	// Should select the newest file by modtime (new.txt)
	expected := filepath.Join(tmpdir, "new.txt")
	if resolved, ok := paths["latest"]; !ok {
		t.Fatalf("expected path with id 'latest', not found")
	} else if resolved.Path != expected {
		t.Fatalf("expected path %s, got %s", expected, resolved.Path)
	}
}

// TestComputeFilePaths_CandidatesFallthrough tests fallthrough to next candidate.
func TestComputeFilePaths_CandidatesFallthrough(t *testing.T) {
	tmpdir := t.TempDir()

	// Create only the second candidate file
	secondPath := filepath.Join(tmpdir, "second.txt")
	if err := os.WriteFile(secondPath, []byte{}, 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	cmd := &CommandDef{
		ID:   "test.fallthrough",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"fallback": {
				Access: FileAccessRead,
				Candidates: []FileCandidate{
					{Path: "missing.txt"}, // First candidate misses
					{Path: "second.txt"},  // Second candidate hits
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

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}

	if resolved, ok := paths["fallback"]; !ok {
		t.Fatalf("expected path with id 'fallback', not found")
	} else if resolved.Path != secondPath {
		t.Fatalf("expected path %s, got %s", secondPath, resolved.Path)
	}
}

// TestComputeFilePaths_AllCandidatesMiss tests all candidates missing with required.
func TestComputeFilePaths_AllCandidatesMiss(t *testing.T) {
	tmpdir := t.TempDir()

	cmd := &CommandDef{
		ID:   "test.allmiss",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"missing": {
				Access: FileAccessRead,
				Candidates: []FileCandidate{
					{Path: "missing1.txt"},
					{Path: "missing2.txt"},
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

	_, err := ComputeFilePaths(ctx)
	if err == nil {
		t.Fatalf("expected error for all candidates missing")
	}
	if !contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got: %v", err)
	}
}

// TestComputeFilePaths_WriteMode tests write mode (no filesystem checks).
func TestComputeFilePaths_WriteMode(t *testing.T) {
	tmpdir := t.TempDir()

	cmd := &CommandDef{
		ID:   "test.write",
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

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}

	expected := filepath.Join(tmpdir, "output.txt")
	if resolved, ok := paths["output"]; !ok {
		t.Fatalf("expected path with id 'output', not found")
	} else if resolved.Path != expected {
		t.Fatalf("expected path %s, got %s", expected, resolved.Path)
	}
}

// TestComputeFilePaths_ReadWriteModeRequired tests read_write mode always requires presence.
func TestComputeFilePaths_ReadWriteModeRequired(t *testing.T) {
	tmpdir := t.TempDir()

	// Do NOT create the file — read_write should fail
	cmd := &CommandDef{
		ID:   "test.readwrite",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"inout": {
				Access:   FileAccessReadWrite,
				Path:     "missing.txt",
				Required: false, // Even with false, read_write requires presence
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

	_, err := ComputeFilePaths(ctx)
	if err == nil {
		t.Fatalf("expected error for missing read_write file")
	}
	if !contains(err.Error(), "read_write") || !contains(err.Error(), "exist") {
		t.Fatalf("expected read_write presence error, got: %v", err)
	}
}

// TestComputeFilePaths_RelativePath tests relative path resolution against ProjectRoot.
func TestComputeFilePaths_RelativePath(t *testing.T) {
	tmpdir := t.TempDir()
	subdir := filepath.Join(tmpdir, "subdir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("create subdir: %v", err)
	}

	testFile := filepath.Join(subdir, "test.txt")
	if err := os.WriteFile(testFile, []byte{}, 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	cmd := &CommandDef{
		ID:   "test.relative",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"nested": {
				Access:   FileAccessRead,
				Path:     "subdir/test.txt",
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

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}

	if resolved, ok := paths["nested"]; !ok {
		t.Fatalf("expected path with id 'nested', not found")
	} else if resolved.Path != testFile {
		t.Fatalf("expected path %s, got %s", testFile, resolved.Path)
	}
}

// TestComputeFilePaths_AbsolutePath tests absolute path passthrough.
func TestComputeFilePaths_AbsolutePath(t *testing.T) {
	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.txt")
	if err := os.WriteFile(testFile, []byte{}, 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	cmd := &CommandDef{
		ID:   "test.absolute",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"abs": {
				Access:   FileAccessRead,
				Path:     testFile, // Absolute path
				Required: true,
			},
		},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: t.TempDir(), // Different ProjectRoot, should not matter
		Render: &tpl.RenderContext{
			Raw:     map[string]any{},
			Params:  map[string]any{},
			Context: map[string]any{},
		},
	}

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}

	if resolved, ok := paths["abs"]; !ok {
		t.Fatalf("expected path with id 'abs', not found")
	} else if resolved.Path != testFile {
		t.Fatalf("expected path %s, got %s", testFile, resolved.Path)
	}
}

// TestComputeFilePaths_TemplateRendering tests template rendering in path via ${...} syntax.
func TestComputeFilePaths_TemplateRendering(t *testing.T) {
	tmpdir := t.TempDir()

	// Create a file with a param-based name
	testFile := filepath.Join(tmpdir, "output_test_value.txt")
	if err := os.WriteFile(testFile, []byte{}, 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	cmd := &CommandDef{
		ID:   "test.template",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"output": {
				Access:   FileAccessRead,
				Path:     "output_${param.name}.txt",
				Required: true,
			},
		},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Params:      map[string]any{"name": "test_value"},
		Render: &tpl.RenderContext{
			Raw:     map[string]any{},
			Params:  map[string]any{"name": "test_value"},
			Context: map[string]any{},
		},
	}

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}

	if resolved, ok := paths["output"]; !ok {
		t.Fatalf("expected path with id 'output', not found")
	} else if resolved.Path != testFile {
		t.Fatalf("expected path %s, got %s", testFile, resolved.Path)
	}
}

// TestResolveRelative_EmptyProjectRoot tests fallback to os.Getwd().
func TestResolveRelative_EmptyProjectRoot(t *testing.T) {
	tmpdir := t.TempDir()

	// Save current wd and change to tempdir
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(oldwd)
	}()

	if err := os.Chdir(tmpdir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	// Resolve relative path with empty ProjectRoot
	resolved, err := resolveRelative("", "subdir/file.txt")
	if err != nil {
		t.Fatalf("resolveRelative: %v", err)
	}

	// Should resolve against os.Getwd()
	// Note: on macOS, tmpdir may be symlinked (/var -> /private/var), so we compare the resolved path
	cwd, _ := os.Getwd()
	expected := filepath.Join(cwd, "subdir", "file.txt")
	// Normalize both paths to handle symlinks
	resolvedAbs, _ := filepath.Abs(resolved)
	expectedAbs, _ := filepath.Abs(expected)

	if resolvedAbs != expectedAbs {
		t.Fatalf("expected %s, got %s", expectedAbs, resolvedAbs)
	}
}

// TestPrepareFileEffects_MkdirCreatesParent tests mkdir creates parent directories.
func TestPrepareFileEffects_MkdirCreatesParent(t *testing.T) {
	tmpdir := t.TempDir()

	cmd := &CommandDef{
		ID:   "test.mkdir",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"output": {
				Access:    FileAccessWrite,
				Path:      "subdir/nested/output.txt",
				Mkdir:     true,
				Overwrite: false,
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

	// Compute paths first
	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	// Prepare effects (should create directories)
	_, err = PrepareFileEffects(ctx, paths)
	if err != nil {
		t.Fatalf("PrepareFileEffects: %v", err)
	}

	// Verify parent directories were created
	parentDir := filepath.Join(tmpdir, "subdir", "nested")
	if _, err := os.Stat(parentDir); err != nil {
		t.Fatalf("expected parent directory to exist: %v", err)
	}
}

// TestPrepareFileEffects_OverwriteFalseExisting tests overwrite=false with existing file returns error.
func TestPrepareFileEffects_OverwriteFalseExisting(t *testing.T) {
	tmpdir := t.TempDir()

	// Create an existing file
	testFile := filepath.Join(tmpdir, "output.txt")
	if err := os.WriteFile(testFile, []byte("existing"), 0o644); err != nil {
		t.Fatalf("create existing file: %v", err)
	}

	cmd := &CommandDef{
		ID:   "test.overwrite",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"output": {
				Access:    FileAccessWrite,
				Path:      "output.txt",
				Overwrite: false,
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

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	// Prepare should fail due to overwrite constraint
	_, err = PrepareFileEffects(ctx, paths)
	if err == nil {
		t.Fatalf("expected error for overwrite=false with existing file")
	}
	if !contains(err.Error(), "already exists") || !contains(err.Error(), "overwrite") {
		t.Fatalf("expected overwrite error, got: %v", err)
	}
}

// TestPrepareFileEffects_CleanupOnlyNewFiles tests on_error=remove only cleans newly-created files.
func TestPrepareFileEffects_CleanupOnlyNewFiles(t *testing.T) {
	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "output.txt")

	// Test 1: existing file should NOT be cleaned up
	if err := os.WriteFile(testFile, []byte("existing"), 0o644); err != nil {
		t.Fatalf("create existing file: %v", err)
	}

	cmd := &CommandDef{
		ID:   "test.cleanup",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"output": {
				Access:    FileAccessWrite,
				Path:      "output.txt",
				Overwrite: true,
				OnError:   FileOnErrorRemove,
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

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	cleanups, err := PrepareFileEffects(ctx, paths)
	if err != nil {
		t.Fatalf("PrepareFileEffects: %v", err)
	}

	// Invoke cleanups (simulating a failed run)
	for _, cleanup := range slices.Backward(cleanups) {
		cleanup()
	}

	// Existing file should still exist (cleanup should not remove it)
	if _, err := os.Stat(testFile); err != nil {
		t.Fatalf("existing file should not be removed: %v", err)
	}

	// Test 2: new file SHOULD be cleaned up
	testFile2 := filepath.Join(tmpdir, "newfile.txt")
	cmd.Files = map[string]FileSpec{
		"output": {
			Access:  FileAccessWrite,
			Path:    "newfile.txt",
			OnError: FileOnErrorRemove,
		},
	}

	ctx.Cmd = cmd
	paths, err = ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	cleanups, err = PrepareFileEffects(ctx, paths)
	if err != nil {
		t.Fatalf("PrepareFileEffects: %v", err)
	}

	// Create the file (simulating a successful write by runner)
	if err := os.WriteFile(testFile2, []byte("new"), 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	// Invoke cleanups
	for _, cleanup := range slices.Backward(cleanups) {
		cleanup()
	}

	// New file should be removed
	if _, err := os.Stat(testFile2); err == nil {
		t.Fatalf("new file should be removed by cleanup")
	}
}

func TestPrepareFileEffects_CleanupIgnoresMissingFile(t *testing.T) {
	tmpdir := t.TempDir()
	var errBuf bytes.Buffer

	cmd := &CommandDef{
		ID:   "test.cleanup-missing",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"output": {
				Access:  FileAccessWrite,
				Path:    "missing-output.txt",
				OnError: FileOnErrorRemove,
			},
		},
	}
	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Stderr:      &errBuf,
		Render: &tpl.RenderContext{
			Raw:     map[string]any{},
			Params:  map[string]any{},
			Context: map[string]any{},
		},
	}

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}
	cleanups, err := PrepareFileEffects(ctx, paths)
	if err != nil {
		t.Fatalf("PrepareFileEffects: %v", err)
	}
	for _, cleanup := range slices.Backward(cleanups) {
		cleanup()
	}
	if got := errBuf.String(); got != "" {
		t.Fatalf("missing file cleanup should be silent, got %q", got)
	}
}

// TestPrepareFileEffects_OnErrorKeep tests on_error=keep never removes files.
func TestPrepareFileEffects_OnErrorKeep(t *testing.T) {
	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "output.txt")

	cmd := &CommandDef{
		ID:   "test.keep",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"output": {
				Access:  FileAccessWrite,
				Path:    "output.txt",
				OnError: FileOnErrorKeep,
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

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	cleanups, err := PrepareFileEffects(ctx, paths)
	if err != nil {
		t.Fatalf("PrepareFileEffects: %v", err)
	}

	// Create the file (simulating a successful write by runner)
	if err := os.WriteFile(testFile, []byte("content"), 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	// Invoke cleanups (should do nothing)
	for _, cleanup := range slices.Backward(cleanups) {
		cleanup()
	}

	// File should still exist
	if _, err := os.Stat(testFile); err != nil {
		t.Fatalf("file should not be removed when on_error=keep: %v", err)
	}
}

// TestPrepareFileEffects_ReadWriteNoCleanup tests read_write mode never registers cleanup.
func TestPrepareFileEffects_ReadWriteNoCleanup(t *testing.T) {
	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "inout.txt")

	// Create the file (read_write requires it to exist)
	if err := os.WriteFile(testFile, []byte("existing"), 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	cmd := &CommandDef{
		ID:   "test.readwrite",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"inout": {
				Access:  FileAccessReadWrite,
				Path:    "inout.txt",
				OnError: FileOnErrorRemove, // Should be ignored for read_write
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

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	cleanups, err := PrepareFileEffects(ctx, paths)
	if err != nil {
		t.Fatalf("PrepareFileEffects: %v", err)
	}

	// No cleanups should be registered for read_write
	if len(cleanups) != 0 {
		t.Fatalf("expected no cleanups for read_write, got %d", len(cleanups))
	}

	// Invoke cleanups (none to invoke)
	for _, cleanup := range slices.Backward(cleanups) {
		cleanup()
	}

	// File should still exist (no cleanup happened)
	if _, err := os.Stat(testFile); err != nil {
		t.Fatalf("file should not be removed: %v", err)
	}
}

// TestComputeFilePaths_DumpDeployIntegration tests the full dump-deploy workflow:
// glob+match+sort to find the most recent dated dump, fallback to legacy path,
// and env injection with context-resolved variables.
func TestComputeFilePaths_DumpDeployIntegration(t *testing.T) {
	tmpdir := t.TempDir()
	dumpsDir := filepath.Join(tmpdir, "dumps")
	if err := os.MkdirAll(dumpsDir, 0o755); err != nil {
		t.Fatalf("create dumps dir: %v", err)
	}

	// Create dated dump files
	files := []string{
		"mydb_2026-04-28.sql.gz",
		"mydb_2026-04-29.sql.gz",
	}
	for _, f := range files {
		path := filepath.Join(dumpsDir, f)
		if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
			t.Fatalf("create dump file: %v", err)
		}
	}

	cmd := &CommandDef{
		ID:   "db.dump-deploy",
		Type: CommandTypeScript,
		Params: map[string]ParamDef{
			"database": {
				Type:    ParamTypeString,
				Default: "mydb",
			},
			"target_database": {
				Type:     ParamTypeString,
				Required: true,
			},
		},
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				Candidates: []FileCandidate{
					{
						Glob:  "dumps/mydb_*.sql.gz",
						Match: `\d{4}-\d{2}-\d{2}`,
						Sort:  FileSortNameDesc,
					},
					{
						Path: "dumps/mydb.sql.gz",
					},
				},
				Required: true,
				Env:      "DUMP_FILE",
			},
		},
		Env: map[string]string{
			"DB_NAME":        "${param.database}",
			"TARGET_DB_NAME": "${param.target_database}",
			"DB_USER":        "${vars.db.user}",
			"DUMP_LOCATION":  "${files.dump.path}",
		},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Render: &tpl.RenderContext{
			Raw: map[string]any{
				"vars": map[string]any{
					"db": map[string]any{
						"user":     "root",
						"password": "secret",
					},
				},
			},
			Params: map[string]any{
				"database":        "mydb",
				"target_database": "mydb_restored",
			},
			Context: map[string]any{},
		},
	}

	// Compute file paths
	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	// Assign paths to render context for template resolution
	ctx.Render.Files = paths

	// Should resolve to the most recent dated dump (name_desc sort)
	expectedPath := filepath.Join(dumpsDir, "mydb_2026-04-29.sql.gz")
	if resolved, ok := paths["dump"]; !ok {
		t.Fatalf("expected path with id 'dump', not found")
	} else if resolved.Path != expectedPath {
		t.Fatalf("expected path %s, got %s", expectedPath, resolved.Path)
	}

	// Verify env injection includes file env vars
	env, err := BuildEnv(cmd, ctx.Render.Params, ctx.Render.Context, paths)
	if err != nil {
		t.Fatalf("BuildEnv: %v", err)
	}

	// Render template variables in env values
	renderedEnv := make(map[string]string, len(env))
	for k, v := range env {
		rendered, err := tpl.RenderCommand(v, ctx.Render)
		if err != nil {
			t.Fatalf("render env %q: %v", k, err)
		}
		renderedEnv[k] = rendered
	}

	// Check that file env var is injected with absolute path
	if dump, ok := renderedEnv["DUMP_FILE"]; !ok {
		t.Fatalf("expected DUMP_FILE in env, not found")
	} else if dump != expectedPath {
		t.Fatalf("expected DUMP_FILE=%s, got %s", expectedPath, dump)
	}

	// Check that other env vars are populated
	if db, ok := renderedEnv["DB_NAME"]; !ok {
		t.Fatalf("expected DB_NAME in env")
	} else if db != "mydb" {
		t.Fatalf("expected DB_NAME=mydb, got %s", db)
	}

	if target, ok := renderedEnv["TARGET_DB_NAME"]; !ok {
		t.Fatalf("expected TARGET_DB_NAME in env")
	} else if target != "mydb_restored" {
		t.Fatalf("expected TARGET_DB_NAME=mydb_restored, got %s", target)
	}

	// Check context-resolved env var
	if user, ok := renderedEnv["DB_USER"]; !ok {
		t.Fatalf("expected DB_USER in env")
	} else if user != "root" {
		t.Fatalf("expected DB_USER=root, got %s", user)
	}

	// Check file path exposed in template context
	if loc, ok := renderedEnv["DUMP_LOCATION"]; !ok {
		t.Fatalf("expected DUMP_LOCATION in env")
	} else if loc != expectedPath {
		t.Fatalf("expected DUMP_LOCATION=%s, got %s", expectedPath, loc)
	}
}

// TestComputeFilePaths_DumpDeployFallback tests fallback from glob to legacy path.
func TestComputeFilePaths_DumpDeployFallback(t *testing.T) {
	tmpdir := t.TempDir()
	dumpsDir := filepath.Join(tmpdir, "dumps")
	if err := os.MkdirAll(dumpsDir, 0o755); err != nil {
		t.Fatalf("create dumps dir: %v", err)
	}

	// Create only the legacy (undated) dump file
	legacyPath := filepath.Join(dumpsDir, "mydb.sql.gz")
	if err := os.WriteFile(legacyPath, []byte{}, 0o644); err != nil {
		t.Fatalf("create legacy dump file: %v", err)
	}

	cmd := &CommandDef{
		ID:   "db.dump-deploy",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"dump": {
				Access: FileAccessRead,
				Candidates: []FileCandidate{
					{
						Glob:  "dumps/mydb_*.sql.gz",
						Match: `\d{4}-\d{2}-\d{2}`,
						Sort:  FileSortNameDesc,
					},
					{
						Path: "dumps/mydb.sql.gz",
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

	// Compute file paths
	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	// Should fall back to legacy path when glob finds no matches
	if resolved, ok := paths["dump"]; !ok {
		t.Fatalf("expected path with id 'dump', not found")
	} else if resolved.Path != legacyPath {
		t.Fatalf("expected fallback path %s, got %s", legacyPath, resolved.Path)
	}
}

// TestComputeFilePaths_CandidatesNameAsc tests name ascending sort (A-Z picks first alphabetically).
func TestComputeFilePaths_CandidatesNameAsc(t *testing.T) {
	tmpdir := t.TempDir()

	for _, name := range []string{"c.sql.gz", "a.sql.gz", "b.sql.gz"} {
		if err := os.WriteFile(filepath.Join(tmpdir, name), []byte{}, 0o644); err != nil {
			t.Fatalf("create test file: %v", err)
		}
	}

	cmd := &CommandDef{
		ID:   "test.name_asc",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"first": {
				Access: FileAccessRead,
				Candidates: []FileCandidate{
					{Glob: "*.sql.gz", Sort: FileSortNameAsc},
				},
				Required: true,
			},
		},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Render:      &tpl.RenderContext{Raw: map[string]any{}, Params: map[string]any{}, Context: map[string]any{}},
	}

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	expected := filepath.Join(tmpdir, "a.sql.gz")
	if resolved, ok := paths["first"]; !ok {
		t.Fatalf("expected path with id 'first', not found")
	} else if resolved.Path != expected {
		t.Fatalf("expected %s (first alphabetically), got %s", expected, resolved.Path)
	}
}

// TestComputeFilePaths_CandidatesModtimeAsc tests modtime ascending sort (oldest first).
func TestComputeFilePaths_CandidatesModtimeAsc(t *testing.T) {
	tmpdir := t.TempDir()

	// old.txt gets the earliest modtime, new.txt gets the latest — same layout as modtime_desc test.
	files := []struct {
		name string
		path string
	}{
		{"old.txt", filepath.Join(tmpdir, "old.txt")},
		{"mid.txt", filepath.Join(tmpdir, "mid.txt")},
		{"new.txt", filepath.Join(tmpdir, "new.txt")},
	}

	for i, f := range files {
		if err := os.WriteFile(f.path, []byte{}, 0o644); err != nil {
			t.Fatalf("create test file: %v", err)
		}
		mt := time.Now().Add(time.Duration(-1000+i*100) * time.Second)
		if err := os.Chtimes(f.path, mt, mt); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	cmd := &CommandDef{
		ID:   "test.modtime_asc",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"oldest": {
				Access: FileAccessRead,
				Candidates: []FileCandidate{
					{Glob: "*.txt", Sort: FileSortModtimeAsc},
				},
				Required: true,
			},
		},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Render:      &tpl.RenderContext{Raw: map[string]any{}, Params: map[string]any{}, Context: map[string]any{}},
	}

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	// old.txt has the oldest modtime (i=0, offset=-1000s)
	expected := filepath.Join(tmpdir, "old.txt")
	if resolved, ok := paths["oldest"]; !ok {
		t.Fatalf("expected path with id 'oldest', not found")
	} else if resolved.Path != expected {
		t.Fatalf("expected %s (oldest by modtime), got %s", expected, resolved.Path)
	}
}

// TestComputeFilePaths_GoTemplateSyntaxWithoutDollar tests that pure {{ }} template syntax
// (without any ${...} expressions) is rendered correctly by renderPath.
// Regression: renderPath previously skipped rendering when the path contained no "${",
// which meant paths like `{{ date }}.sql.gz` or `output_{{ resolveMap .Params "x" }}.txt`
// were returned as literal strings instead of being evaluated.
func TestComputeFilePaths_GoTemplateSyntaxWithoutDollar(t *testing.T) {
	tmpdir := t.TempDir()

	testFile := filepath.Join(tmpdir, "output_testval.txt")
	if err := os.WriteFile(testFile, []byte{}, 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	cmd := &CommandDef{
		ID:   "test.gotemplate",
		Type: CommandTypeScript,
		Files: map[string]FileSpec{
			"output": {
				Access:   FileAccessRead,
				Path:     `output_{{ resolveMap .Params "name" }}.txt`,
				Required: true,
			},
		},
	}

	ctx := RunContext{
		Cmd:         cmd,
		ProjectRoot: tmpdir,
		Render: &tpl.RenderContext{
			Raw:     map[string]any{},
			Params:  map[string]any{"name": "testval"},
			Context: map[string]any{},
		},
	}

	paths, err := ComputeFilePaths(ctx)
	if err != nil {
		t.Fatalf("ComputeFilePaths: %v", err)
	}

	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}

	if resolved, ok := paths["output"]; !ok {
		t.Fatalf("expected path with id 'output', not found")
	} else if resolved.Path != testFile {
		t.Fatalf("expected path %s, got %s", testFile, resolved.Path)
	}
}

// TestComputeFilePaths_CandidatesInvalidRegex tests that a malformed match pattern aborts immediately.
func TestComputeFilePaths_CandidatesInvalidRegex(t *testing.T) {
	tmpdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpdir, "db.sql.gz"), []byte{}, 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	cmd := &CommandDef{
		ID:   "test.invalid_regex",
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
		Render:      &tpl.RenderContext{Raw: map[string]any{}, Params: map[string]any{}, Context: map[string]any{}},
	}

	_, err := ComputeFilePaths(ctx)
	if err == nil {
		t.Fatalf("expected error for invalid regex match pattern, got nil")
	}
}

func contains(s, substr string) bool {
	for i := range s {
		if i+len(substr) <= len(s) && s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
