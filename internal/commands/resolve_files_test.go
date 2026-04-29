package commands

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"devbox-cli/internal/tpl"
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

// Helper function to check if a string contains a substring.
func contains(s, substr string) bool {
	for i := range s {
		if i+len(substr) <= len(s) && s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
