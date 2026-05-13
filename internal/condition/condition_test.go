package condition_test

import (
	"os"
	"path/filepath"
	"testing"

	"devbox-cli/internal/condition"
)

// --- Classify ---

func TestClassify_emptyIsTemplate(t *testing.T) {
	kind, payload := condition.Classify("")
	if kind != condition.KindTemplate {
		t.Errorf("Classify(\"\") kind = %v, want KindTemplate", kind)
	}
	if payload != "" {
		t.Errorf("Classify(\"\") payload = %q, want \"\"", payload)
	}
}

func TestClassify_goTemplate(t *testing.T) {
	cases := []string{
		"{{.Runtime.UseHTTPS}}",
		"{{ .Tools.adminer.Enabled }}",
		"prefix {{.Foo}} suffix",
	}
	for _, expr := range cases {
		kind, payload := condition.Classify(expr)
		if kind != condition.KindTemplate {
			t.Errorf("Classify(%q) kind = %v, want KindTemplate", expr, kind)
		}
		if payload != expr {
			t.Errorf("Classify(%q) payload = %q, want %q", expr, payload, expr)
		}
	}
}

func TestClassify_cmdPrefix(t *testing.T) {
	kind, payload := condition.Classify("cmd: test -f /tmp/foo")
	if kind != condition.KindCmd {
		t.Errorf("kind = %v, want KindCmd", kind)
	}
	if payload != "test -f /tmp/foo" {
		t.Errorf("payload = %q, want %q", payload, "test -f /tmp/foo")
	}
}

func TestClassify_cmdPrefixNoSpace(t *testing.T) {
	kind, payload := condition.Classify("cmd:echo hi")
	if kind != condition.KindCmd {
		t.Errorf("kind = %v, want KindCmd", kind)
	}
	if payload != "echo hi" {
		t.Errorf("payload = %q, want %q", payload, "echo hi")
	}
}

func TestClassify_builtin(t *testing.T) {
	builtins := []string{
		"dir-exists services/main/src",
		"dir-missing services/main/src",
		"dir-empty services/main/src",
		"dir-not-empty services/main/src",
		"file-exists .env",
		"file-missing .env",
	}
	for _, expr := range builtins {
		kind, payload := condition.Classify(expr)
		if kind != condition.KindBuiltin {
			t.Errorf("Classify(%q) kind = %v, want KindBuiltin", expr, kind)
		}
		if payload != expr {
			t.Errorf("Classify(%q) payload = %q, want %q", expr, payload, expr)
		}
	}
}

func TestClassify_leadingSpaceTrimmed(t *testing.T) {
	kind, _ := condition.Classify("  dir-exists foo  ")
	if kind != condition.KindBuiltin {
		t.Errorf("expected KindBuiltin after trimming, got %v", kind)
	}
}

// --- IsRuntime ---

func TestIsRuntime(t *testing.T) {
	cases := []struct {
		expr string
		want bool
	}{
		{"", false},
		{"{{.Foo}}", false},
		{"dir-exists foo", true},
		{"dir-missing foo", true},
		{"dir-empty foo", true},
		{"dir-not-empty foo", true},
		{"file-exists foo", true},
		{"file-missing foo", true},
		{"cmd: echo hi", true},
	}
	for _, tc := range cases {
		got := condition.IsRuntime(tc.expr)
		if got != tc.want {
			t.Errorf("IsRuntime(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

// --- EvalBuiltin ---

func TestEvalBuiltin_dirExists(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	ok, err := condition.EvalBuiltin("dir-exists sub", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("dir-exists: expected true for existing dir")
	}

	ok, err = condition.EvalBuiltin("dir-exists nosuchdir", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("dir-exists: expected false for non-existing dir")
	}
}

func TestEvalBuiltin_dirMissing(t *testing.T) {
	root := t.TempDir()

	ok, err := condition.EvalBuiltin("dir-missing nosuchdir", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("dir-missing: expected true for non-existing dir")
	}

	// create it, now should be false
	if err := os.MkdirAll(filepath.Join(root, "existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	ok, err = condition.EvalBuiltin("dir-missing existing", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("dir-missing: expected false for existing dir")
	}
}

func TestEvalBuiltin_dirEmpty(t *testing.T) {
	root := t.TempDir()

	// non-existing dir → empty (true)
	ok, err := condition.EvalBuiltin("dir-empty nosuchdir", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("dir-empty: expected true for non-existing dir")
	}

	// empty dir → true
	emptyDir := filepath.Join(root, "emptydir")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ok, err = condition.EvalBuiltin("dir-empty emptydir", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("dir-empty: expected true for empty dir")
	}

	// non-empty dir → false
	if err := os.WriteFile(filepath.Join(emptyDir, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err = condition.EvalBuiltin("dir-empty emptydir", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("dir-empty: expected false for non-empty dir")
	}
}

func TestEvalBuiltin_dirNotEmpty(t *testing.T) {
	root := t.TempDir()

	// non-existing → empty → false
	ok, err := condition.EvalBuiltin("dir-not-empty nosuchdir", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("dir-not-empty: expected false for non-existing dir")
	}

	// create with a file → true
	sub := filepath.Join(root, "populated")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err = condition.EvalBuiltin("dir-not-empty populated", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("dir-not-empty: expected true for non-empty dir")
	}
}

func TestEvalBuiltin_fileExists(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "test.txt")
	if err := os.WriteFile(f, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	ok, err := condition.EvalBuiltin("file-exists test.txt", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("file-exists: expected true for existing file")
	}

	ok, err = condition.EvalBuiltin("file-exists nosuchfile.txt", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("file-exists: expected false for missing file")
	}
}

func TestEvalBuiltin_fileMissing(t *testing.T) {
	root := t.TempDir()

	ok, err := condition.EvalBuiltin("file-missing nosuchfile.txt", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("file-missing: expected true for missing file")
	}

	f := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err = condition.EvalBuiltin("file-missing existing.txt", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("file-missing: expected false for existing file")
	}
}

func TestEvalBuiltin_unknownVerb(t *testing.T) {
	_, err := condition.EvalBuiltin("nonexistent-verb path", t.TempDir())
	if err == nil {
		t.Error("expected error for unknown verb")
	}
}

func TestEvalBuiltin_missingPath(t *testing.T) {
	_, err := condition.EvalBuiltin("dir-exists", t.TempDir())
	if err == nil {
		t.Error("expected error when path is missing")
	}
}

func TestEvalBuiltin_absolutePath(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "abs")
	if err := os.MkdirAll(abs, 0o755); err != nil {
		t.Fatal(err)
	}
	// absolute path should bypass projectRoot join
	ok, err := condition.EvalBuiltin("dir-exists "+abs, "/some/other/root")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("dir-exists with absolute path: expected true")
	}
}

// --- EvalCmd ---

func TestEvalCmd_successExitZero(t *testing.T) {
	root := t.TempDir()
	ok, err := condition.EvalCmd("exit 0", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("EvalCmd: expected true for exit 0")
	}
}

func TestEvalCmd_failureNonZeroExit(t *testing.T) {
	root := t.TempDir()
	ok, err := condition.EvalCmd("exit 1", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("EvalCmd: expected false for exit 1")
	}
}

func TestEvalCmd_emptyCommand(t *testing.T) {
	_, err := condition.EvalCmd("", t.TempDir())
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestEvalCmd_fileTestTrue(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "marker")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err := condition.EvalCmd("test -f marker", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("EvalCmd: expected true for test -f existing file")
	}
}

func TestEvalCmd_fileTestFalse(t *testing.T) {
	root := t.TempDir()
	ok, err := condition.EvalCmd("test -f nosuchfile", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("EvalCmd: expected false for test -f missing file")
	}
}

// --- EvalRuntime ---

func TestEvalRuntime_builtin(t *testing.T) {
	root := t.TempDir()
	ok, err := condition.EvalRuntime("dir-missing nosuchdir", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("EvalRuntime builtin: expected true")
	}
}

func TestEvalRuntime_cmd(t *testing.T) {
	root := t.TempDir()
	ok, err := condition.EvalRuntime("cmd: exit 0", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("EvalRuntime cmd: expected true")
	}
}

func TestEvalRuntime_templateReturnsError(t *testing.T) {
	_, err := condition.EvalRuntime("{{.Foo}}", t.TempDir())
	if err == nil {
		t.Error("EvalRuntime with template expr should return error")
	}
}

func TestEvalRuntime_emptyReturnsError(t *testing.T) {
	_, err := condition.EvalRuntime("", t.TempDir())
	if err == nil {
		t.Error("EvalRuntime with empty expr should return error")
	}
}
