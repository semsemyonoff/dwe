package builtin

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/render"
)

// makeExecCtx returns an ExecContext with an in-memory output writer and the
// given project root directory.
func makeExecCtx(t *testing.T, projectRoot string) ExecContext {
	t.Helper()
	buf := &bytes.Buffer{}
	return ExecContext{
		Config:      &config.DevboxConfig{},
		ProjectRoot: projectRoot,
		Output:      render.NewWriter(buf),
	}
}

// makeCfgWithService builds a minimal DevboxConfig containing one service.
func makeCfgWithService(name, dir string, dirs []string) *config.DevboxConfig {
	return &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			name: {
				Dir:  dir,
				Dirs: dirs,
			},
		},
	}
}

// ---- Validate ------------------------------------------------------------

func TestServiceDirsEnsureValidate(t *testing.T) {
	b := serviceDirsEnsureBuiltin{}

	cases := []struct {
		name    string
		with    map[string]any
		wantErr bool
	}{
		{"missing service", map[string]any{}, true},
		{"valid skip", map[string]any{"service": "main"}, false},
		{"valid error mode", map[string]any{"service": "main", "mode": "error"}, false},
		{"valid recreate mode", map[string]any{"service": "main", "mode": "recreate"}, false},
		{"invalid mode", map[string]any{"service": "main", "mode": "bad"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := b.Validate(tc.with)
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// ---- Describe ------------------------------------------------------------

func TestServiceDirsEnsureDescribe(t *testing.T) {
	b := serviceDirsEnsureBuiltin{}
	got := b.Describe(map[string]any{"service": "main", "mode": "skip"})
	want := "builtin: service_dirs_ensure(service=main, mode=skip)"
	if got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}

	// Default mode when not specified.
	got = b.Describe(map[string]any{"service": "main"})
	want = "builtin: service_dirs_ensure(service=main, mode=skip)"
	if got != want {
		t.Errorf("Describe() default mode = %q, want %q", got, want)
	}
}

// ---- buildDirList --------------------------------------------------------

func TestBuildDirList(t *testing.T) {
	cases := []struct {
		name      string
		input     []string
		wantFirst []string // first N elements must equal these
		wantLen   int
	}{
		{
			name:      "no extras",
			input:     nil,
			wantFirst: []string{"src", "configs"},
			wantLen:   2,
		},
		{
			name:      "extras appended",
			input:     []string{"logs", "home"},
			wantFirst: []string{"src", "configs", "logs", "home"},
			wantLen:   4,
		},
		{
			name:      "deduplication: extra duplicates mandatory",
			input:     []string{"src", "logs"},
			wantFirst: []string{"src", "configs", "logs"},
			wantLen:   3,
		},
		{
			name:      "deduplication: duplicate extras",
			input:     []string{"logs", "logs", "home"},
			wantFirst: []string{"src", "configs", "logs", "home"},
			wantLen:   4,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildDirList(tc.input)
			if len(got) != tc.wantLen {
				t.Fatalf("buildDirList() len = %d, want %d; got %v", len(got), tc.wantLen, got)
			}
			for i, want := range tc.wantFirst {
				if got[i] != want {
					t.Errorf("buildDirList()[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

// ---- Security validation -------------------------------------------------

func TestValidateRelDir(t *testing.T) {
	cases := []struct {
		path    string
		wantErr bool
	}{
		{"logs", false},
		{"home/user", false},
		{"", true},
		{"/absolute", true},
		{"..", true},
		{"../escape", true},
		{"./logs", false}, // filepath.Clean resolves this to "logs"
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			err := validateRelDir(tc.path)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateRelDir(%q) err = %v, wantErr %v", tc.path, err, tc.wantErr)
			}
		})
	}
}

// ---- Run: skip mode (default) --------------------------------------------

func TestServiceDirsEnsureRun_SkipMode(t *testing.T) {
	b := serviceDirsEnsureBuiltin{}
	root := t.TempDir()
	svcDir := filepath.Join(root, "services/main")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := makeCfgWithService("main", "services/main", []string{"logs", "home", "runtime"})
	ctx := makeExecCtx(t, root)
	ctx.Config = cfg

	if err := b.Run(context.Background(), map[string]any{"service": "main"}, ctx); err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}

	// All dirs must now exist.
	for _, d := range []string{"src", "configs", "logs", "home", "runtime"} {
		p := filepath.Join(svcDir, d)
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("dir %q not created: %v", d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("path %q is not a directory", d)
		}
	}
}

func TestServiceDirsEnsureRun_SkipExisting(t *testing.T) {
	b := serviceDirsEnsureBuiltin{}
	root := t.TempDir()
	svcDir := filepath.Join(root, "services/main")
	// Pre-create all dirs.
	for _, d := range []string{"src", "configs", "logs"} {
		if err := os.MkdirAll(filepath.Join(svcDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := makeCfgWithService("main", "services/main", []string{"logs"})
	ctx := makeExecCtx(t, root)
	ctx.Config = cfg

	// Should succeed with no error (dirs exist, skip mode).
	if err := b.Run(context.Background(), map[string]any{"service": "main"}, ctx); err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
}

// ---- Run: error mode -----------------------------------------------------

func TestServiceDirsEnsureRun_ErrorMode_ExistingDir(t *testing.T) {
	b := serviceDirsEnsureBuiltin{}
	root := t.TempDir()
	svcDir := filepath.Join(root, "services/main")
	// Pre-create src to trigger error mode.
	if err := os.MkdirAll(filepath.Join(svcDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := makeCfgWithService("main", "services/main", nil)
	ctx := makeExecCtx(t, root)
	ctx.Config = cfg

	err := b.Run(context.Background(), map[string]any{"service": "main", "mode": "error"}, ctx)
	if err == nil {
		t.Fatal("Run() expected error for existing dir in error mode, got nil")
	}
}

func TestServiceDirsEnsureRun_ErrorMode_CreatesMissingDirs(t *testing.T) {
	b := serviceDirsEnsureBuiltin{}
	root := t.TempDir()
	svcDir := filepath.Join(root, "services/main")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := makeCfgWithService("main", "services/main", nil)
	ctx := makeExecCtx(t, root)
	ctx.Config = cfg

	// Error mode creates missing dirs without error.
	if err := b.Run(context.Background(), map[string]any{"service": "main", "mode": "error"}, ctx); err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	for _, d := range []string{"src", "configs"} {
		if _, err := os.Stat(filepath.Join(svcDir, d)); err != nil {
			t.Errorf("dir %q not created: %v", d, err)
		}
	}
}

// ---- Run: recreate mode --------------------------------------------------

func TestServiceDirsEnsureRun_RecreateMode(t *testing.T) {
	b := serviceDirsEnsureBuiltin{}
	root := t.TempDir()
	svcDir := filepath.Join(root, "services/main")

	// Pre-create runtime dir with a sentinel file.
	runtimeDir := filepath.Join(svcDir, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinelPath := filepath.Join(runtimeDir, "sentinel.txt")
	if err := os.WriteFile(sentinelPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := makeCfgWithService("main", "services/main", []string{"runtime"})
	ctx := makeExecCtx(t, root)
	ctx.Config = cfg

	if err := b.Run(context.Background(), map[string]any{"service": "main", "mode": "recreate"}, ctx); err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}

	// runtime dir must exist but sentinel file must be gone.
	if _, err := os.Stat(runtimeDir); err != nil {
		t.Errorf("runtime dir should exist after recreate: %v", err)
	}
	if _, err := os.Stat(sentinelPath); !os.IsNotExist(err) {
		t.Errorf("sentinel file should be gone after recreate; stat err = %v", err)
	}
}

func TestServiceDirsEnsureRun_RecreateMode_MandatoryDirsSafe(t *testing.T) {
	b := serviceDirsEnsureBuiltin{}
	root := t.TempDir()
	svcDir := filepath.Join(root, "services/main")

	// Pre-create src with a sentinel file (must survive recreate).
	srcDir := filepath.Join(svcDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinelPath := filepath.Join(srcDir, "code.php")
	if err := os.WriteFile(sentinelPath, []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := makeCfgWithService("main", "services/main", nil)
	ctx := makeExecCtx(t, root)
	ctx.Config = cfg

	if err := b.Run(context.Background(), map[string]any{"service": "main", "mode": "recreate"}, ctx); err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}

	// src sentinel file must still exist.
	if _, err := os.Stat(sentinelPath); err != nil {
		t.Errorf("sentinel in mandatory src dir must survive recreate: %v", err)
	}
}

// ---- Run: error cases ----------------------------------------------------

func TestServiceDirsEnsureRun_UnknownService(t *testing.T) {
	b := serviceDirsEnsureBuiltin{}
	root := t.TempDir()
	ctx := makeExecCtx(t, root)
	ctx.Config = makeCfgWithService("main", "services/main", nil)

	err := b.Run(context.Background(), map[string]any{"service": "other"}, ctx)
	if err == nil {
		t.Fatal("expected error for unknown service, got nil")
	}
}

func TestServiceDirsEnsureRun_NonDirConflict(t *testing.T) {
	b := serviceDirsEnsureBuiltin{}
	root := t.TempDir()
	svcDir := filepath.Join(root, "services/main")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Place a regular file where a directory is expected.
	if err := os.WriteFile(filepath.Join(svcDir, "src"), []byte("conflict"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := makeCfgWithService("main", "services/main", nil)
	ctx := makeExecCtx(t, root)
	ctx.Config = cfg

	err := b.Run(context.Background(), map[string]any{"service": "main"}, ctx)
	if err == nil {
		t.Fatal("expected error when path exists as file, got nil")
	}
}

func TestServiceDirsEnsureRun_AbsolutePathInDirs(t *testing.T) {
	b := serviceDirsEnsureBuiltin{}
	root := t.TempDir()

	cfg := makeCfgWithService("main", "services/main", []string{"/etc/passwd"})
	ctx := makeExecCtx(t, root)
	ctx.Config = cfg

	err := b.Run(context.Background(), map[string]any{"service": "main"}, ctx)
	if err == nil {
		t.Fatal("expected error for absolute path in dirs, got nil")
	}
}

func TestServiceDirsEnsureRun_PathTraversalInDirs(t *testing.T) {
	b := serviceDirsEnsureBuiltin{}
	root := t.TempDir()

	cfg := makeCfgWithService("main", "services/main", []string{"../../escape"})
	ctx := makeExecCtx(t, root)
	ctx.Config = cfg

	err := b.Run(context.Background(), map[string]any{"service": "main"}, ctx)
	if err == nil {
		t.Fatal("expected error for path traversal in dirs, got nil")
	}
}

// ---- Registry ------------------------------------------------------------

func TestServiceDirsEnsureRegistered(t *testing.T) {
	_, ok := Get("service_dirs_ensure", CtxUserYAML)
	if !ok {
		t.Error("service_dirs_ensure not found in builtin registry")
	}
}

// ---- ensureInsideBase -------------------------------------------------------

func TestEnsureInsideBase_Escaping(t *testing.T) {
	base := "/tmp/project"
	abs := "/tmp/other"
	if err := ensureInsideBase(base, abs); err == nil {
		t.Error("expected error for path outside base")
	}
}

func TestEnsureInsideBase_Valid(t *testing.T) {
	base := "/tmp/project"
	abs := "/tmp/project/services/main"
	if err := ensureInsideBase(base, abs); err != nil {
		t.Errorf("unexpected error for valid path: %v", err)
	}
}

// ---- ensureDir edge cases ---------------------------------------------------

func TestEnsureDir_ExistsNotDir(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "notadir.txt")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := ExecContext{Output: render.NewWriter(&bytes.Buffer{})}
	err := ensureDir(p, "notadir.txt", "skip", false, ctx)
	if err == nil {
		t.Fatal("expected error when path exists but is not a directory")
	}
}

func TestEnsureDir_ErrorMode_Exists(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "existing")
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := ExecContext{Output: render.NewWriter(&bytes.Buffer{})}
	err := ensureDir(p, "existing", "error", false, ctx)
	if err == nil {
		t.Fatal("expected error in error mode when dir exists")
	}
}

func TestEnsureDir_UnknownMode(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "newdir")
	ctx := ExecContext{Output: render.NewWriter(&bytes.Buffer{})}
	err := ensureDir(p, "newdir", "bogusmode", false, ctx)
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestServiceDirsEnsure_EmptyServiceDir(t *testing.T) {
	root := t.TempDir()
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"main": {Dir: ""},
		},
	}
	ctx := ExecContext{
		Config:      cfg,
		ProjectRoot: root,
		Output:      render.NewWriter(&bytes.Buffer{}),
	}
	b := serviceDirsEnsureBuiltin{}
	err := b.Run(context.Background(), map[string]any{"service": "main"}, ctx)
	if err == nil {
		t.Fatal("expected error when service dir is empty")
	}
}

func TestEnsureDir_RecreateMode_Mandatory_Exists(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "src")
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := ExecContext{Output: render.NewWriter(&bytes.Buffer{})}
	err := ensureDir(p, "src", "recreate", true, ctx)
	if err != nil {
		t.Fatalf("recreate on mandatory+existing dir should skip: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Error("mandatory dir should still exist after recreate skip")
	}
}
