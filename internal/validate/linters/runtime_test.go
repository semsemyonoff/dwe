package linters

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/validate"
	"devbox-cli/internal/validate/diag"
)

// fakeLinterBin is the absolute path to the compiled cmd/fake-linter binary,
// shared across all runtime tests in this package.
var fakeLinterBin string

func TestMain(m *testing.M) {
	code := buildAndRun(m)
	os.Exit(code)
}

func buildAndRun(m *testing.M) int {
	tmp, err := os.MkdirTemp("", "fake-linter-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "TestMain mkdir:", err)
		return 2
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	binName := "fake-linter"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	fakeLinterBin = filepath.Join(tmp, binName)

	// Locate the cmd/fake-linter package relative to this test file.
	_, thisFile, _, _ := runtime.Caller(0)
	pkgDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "cmd", "fake-linter")

	build := exec.Command("go", "build", "-o", fakeLinterBin, ".")
	build.Dir = pkgDir
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "TestMain go build fake-linter:", err)
		return 2
	}
	return m.Run()
}

// withFakePath puts the directory containing the fake-linter binary onto PATH
// for the duration of the test, then renames the binary in place to the name
// the test expects (so exec.LookPath finds e.g. "shellcheck" instead of
// "fake-linter"). Cleanup restores both PATH and the binary name via t.Cleanup.
func withFakePath(t *testing.T, asName string) string {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, asName)
	if runtime.GOOS == "windows" {
		target += ".exe"
	}
	src, err := os.ReadFile(fakeLinterBin)
	if err != nil {
		t.Fatalf("read fake-linter: %v", err)
	}
	if err := os.WriteFile(target, src, 0o755); err != nil {
		t.Fatalf("write %s: %v", target, err)
	}
	t.Setenv("PATH", dir)
	return target
}

// withTestTimeout swaps DefaultLinterTimeout for the duration of the test
// and restores it on cleanup. Used by the timeout-fires test.
func withTestTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	prev := DefaultLinterTimeout
	DefaultLinterTimeout = d
	t.Cleanup(func() { DefaultLinterTimeout = prev })
}

// withTestOutputCap swaps MaxLinterOutputBytes for the duration of the test.
func withTestOutputCap(t *testing.T, n int64) {
	t.Helper()
	prev := MaxLinterOutputBytes
	MaxLinterOutputBytes = n
	t.Cleanup(func() { MaxLinterOutputBytes = prev })
}

// fakeAdapter is an in-test Adapter that wraps the fake-linter binary. The
// mode field is read from FAKE_LINTER_MODE in the subprocess — set via
// t.Setenv before each Run.
type fakeAdapter struct {
	id           string
	bin          string
	defaultPaths []string
	reserved     []string

	// parseFn lets each test choose how the captured stdout/stderr is mapped
	// to diagnostics — keeps the runtime test focused on Run() behavior, not
	// adapter parsing.
	parseFn func(stdout, stderr []byte, exitCode int) ([]validate.Diagnostic, error)
}

func (f *fakeAdapter) ID() string                  { return f.id }
func (f *fakeAdapter) DefaultBin() string          { return f.bin }
func (f *fakeAdapter) DefaultPaths() []string      { return f.defaultPaths }
func (f *fakeAdapter) DefaultExtensions() []string { return []string{".sh"} }
func (f *fakeAdapter) DefaultFilenames() []string  { return nil }
func (f *fakeAdapter) ReservedFlags() []string     { return f.reserved }
func (f *fakeAdapter) BuildArgs(files, userFlags []string) []string {
	args := append([]string(nil), userFlags...)
	if len(files) > 0 {
		args = append(args, "--")
		args = append(args, files...)
	}
	return args
}
func (f *fakeAdapter) ParseOutput(stdout, stderr []byte, exitCode int) ([]validate.Diagnostic, error) {
	if f.parseFn != nil {
		return f.parseFn(stdout, stderr, exitCode)
	}
	if exitCode == 0 {
		return nil, nil
	}
	return []validate.Diagnostic{
		finding(f.id, validate.SeverityError, "", 0, "non-zero exit", ""),
	}, nil
}

// writeScript creates a .sh file under baseDir so collectFiles has something
// to feed to the fake linter. Content does not matter — the fake-linter
// ignores argv.
func writeScript(t *testing.T, baseDir, name string) {
	t.Helper()
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", baseDir, err)
	}
	p := filepath.Join(baseDir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func TestLinterValidator_SuccessPath(t *testing.T) {
	withFakePath(t, "myfakelinter")
	t.Setenv("FAKE_LINTER_MODE", "clean")

	base := t.TempDir()
	writeScript(t, base, "a.sh")

	a := &fakeAdapter{id: "fake", bin: "myfakelinter"}
	v := newLinterValidator(config.LinterEntry{ID: "fake", Paths: []string{"."}}, a, base)

	diags := v.Run(validate.Context{Ctx: context.Background()})
	for _, d := range diags {
		if d.Severity == validate.SeverityError || d.Severity == validate.SeverityWarning {
			t.Errorf("unexpected operational diag: %#v", d)
		}
	}
}

func TestLinterValidator_NonZeroProducesFindings(t *testing.T) {
	withFakePath(t, "myfakelinter")
	t.Setenv("FAKE_LINTER_MODE", "findings")

	base := t.TempDir()
	writeScript(t, base, "a.sh")

	a := &fakeAdapter{id: "fake", bin: "myfakelinter"}
	v := newLinterValidator(config.LinterEntry{ID: "fake", Paths: []string{"."}}, a, base)

	diags := v.Run(validate.Context{})
	if len(diags) == 0 {
		t.Fatal("want at least one diagnostic")
	}
	found := false
	for _, d := range diags {
		if d.Severity == validate.SeverityError && d.Domain == Domain && d.Target == "fake" {
			found = true
		}
	}
	if !found {
		t.Errorf("want one Error finding stamped Domain=linters Target=fake, got %#v", diags)
	}
}

func TestLinterValidator_MissingDefaultBinSilentSkip(t *testing.T) {
	dir := t.TempDir() // empty — fake binary NOT present
	t.Setenv("PATH", dir)

	a := &fakeAdapter{id: "ghost", bin: "ghost-linter"}
	v := newLinterValidator(config.LinterEntry{ID: "ghost"}, a, t.TempDir())

	diags := v.Run(validate.Context{})
	if len(diags) != 0 {
		t.Fatalf("autodetect should silently skip when bin missing; got %#v", diags)
	}
}

func TestLinterValidator_MissingExplicitBinWarning(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	a := &fakeAdapter{id: "ghost", bin: "ghost-default"}
	v := newLinterValidator(
		config.LinterEntry{ID: "ghost", Bin: "ghost-explicit"},
		a, t.TempDir(),
	)

	diags := v.Run(validate.Context{})
	if len(diags) != 1 || diags[0].Severity != validate.SeverityWarning {
		t.Fatalf("want one Warning for explicit-bin-missing; got %#v", diags)
	}
}

func TestLinterValidator_TimeoutFiresOperationalError(t *testing.T) {
	withFakePath(t, "myfakelinter")
	t.Setenv("FAKE_LINTER_MODE", "hang")
	withTestTimeout(t, 100*time.Millisecond)

	base := t.TempDir()
	writeScript(t, base, "a.sh")

	a := &fakeAdapter{id: "fake", bin: "myfakelinter"}
	v := newLinterValidator(
		config.LinterEntry{ID: "fake", Paths: []string{"."}},
		a, base,
	)

	start := time.Now()
	diags := v.Run(validate.Context{})
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Fatalf("timeout did not fire in time, elapsed=%s", elapsed)
	}
	if len(diags) != 1 || diags[0].Severity != validate.SeverityError {
		t.Fatalf("want one Error operational diag for timeout; got %#v", diags)
	}
}

func TestLinterValidator_TimeoutNotSilencedByClamp(t *testing.T) {
	// Severity clamp set to Info; timeout (operational) must still be Error.
	withFakePath(t, "myfakelinter")
	t.Setenv("FAKE_LINTER_MODE", "hang")
	withTestTimeout(t, 80*time.Millisecond)

	base := t.TempDir()
	writeScript(t, base, "a.sh")

	clamp := diag.SeverityInfo
	a := &fakeAdapter{id: "fake", bin: "myfakelinter"}
	v := newLinterValidator(
		config.LinterEntry{ID: "fake", Paths: []string{"."}, Severity: &clamp},
		a, base,
	)
	diags := v.Run(validate.Context{})
	if len(diags) != 1 || diags[0].Severity != validate.SeverityError {
		t.Fatalf("severity clamp must not mute operational timeout; got %#v", diags)
	}
}

func TestLinterValidator_OutputTruncationWarning(t *testing.T) {
	withFakePath(t, "myfakelinter")
	t.Setenv("FAKE_LINTER_MODE", "huge-output")
	withTestOutputCap(t, 2048) // 1 KB per stream — fake emits 64 KB.

	base := t.TempDir()
	writeScript(t, base, "a.sh")

	a := &fakeAdapter{
		id: "fake", bin: "myfakelinter",
		parseFn: func(_, _ []byte, _ int) ([]validate.Diagnostic, error) { return nil, nil },
	}
	v := newLinterValidator(
		config.LinterEntry{ID: "fake", Paths: []string{"."}},
		a, base,
	)
	diags := v.Run(validate.Context{})
	gotWarning := false
	for _, d := range diags {
		if d.Severity == validate.SeverityWarning {
			gotWarning = true
		}
	}
	if !gotWarning {
		t.Fatalf("want truncation Warning; got %#v", diags)
	}
}

func TestLinterValidator_AdapterParseErrorBecomesError(t *testing.T) {
	withFakePath(t, "myfakelinter")
	t.Setenv("FAKE_LINTER_MODE", "clean")

	base := t.TempDir()
	writeScript(t, base, "a.sh")

	a := &fakeAdapter{
		id: "fake", bin: "myfakelinter",
		parseFn: func(_, _ []byte, _ int) ([]validate.Diagnostic, error) {
			return nil, errors.New("synthetic parse boom")
		},
	}
	v := newLinterValidator(
		config.LinterEntry{ID: "fake", Paths: []string{"."}},
		a, base,
	)
	diags := v.Run(validate.Context{})
	if len(diags) != 1 || diags[0].Severity != validate.SeverityError {
		t.Fatalf("want one Error for parse error; got %#v", diags)
	}
}

func TestLinterValidator_CrashNotSilencedByClamp(t *testing.T) {
	// A parse error (operational diag) must not be downgraded by severity: info clamp.
	withFakePath(t, "myfakelinter")
	t.Setenv("FAKE_LINTER_MODE", "clean")

	base := t.TempDir()
	writeScript(t, base, "a.sh")

	clamp := diag.SeverityInfo
	a := &fakeAdapter{
		id: "fake", bin: "myfakelinter",
		parseFn: func(_, _ []byte, _ int) ([]validate.Diagnostic, error) {
			return nil, errors.New("crash: non-zero exit with no parsable output")
		},
	}
	v := newLinterValidator(
		config.LinterEntry{ID: "fake", Paths: []string{"."}, Severity: &clamp},
		a, base,
	)
	diags := v.Run(validate.Context{})
	if len(diags) != 1 || diags[0].Severity != validate.SeverityError {
		t.Fatalf("severity clamp must not mute parse error; got %#v", diags)
	}
}

func TestLinterValidator_SeverityClampDowngradesFindings(t *testing.T) {
	withFakePath(t, "myfakelinter")
	t.Setenv("FAKE_LINTER_MODE", "findings")

	base := t.TempDir()
	writeScript(t, base, "a.sh")

	clamp := diag.SeverityWarning
	a := &fakeAdapter{
		id: "fake", bin: "myfakelinter",
		parseFn: func(_, _ []byte, _ int) ([]validate.Diagnostic, error) {
			return []validate.Diagnostic{
				finding("fake", validate.SeverityError, "a.sh", 1, "boom", ""),
			}, nil
		},
	}
	v := newLinterValidator(
		config.LinterEntry{ID: "fake", Paths: []string{"."}, Severity: &clamp},
		a, base,
	)
	diags := v.Run(validate.Context{})
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %d (%#v)", len(diags), diags)
	}
	if diags[0].Severity != validate.SeverityWarning {
		t.Errorf("severity clamp failed: got %v want Warning", diags[0].Severity)
	}
}

func TestLinterValidator_UserMissingPathWarning(t *testing.T) {
	withFakePath(t, "myfakelinter")
	t.Setenv("FAKE_LINTER_MODE", "clean")

	base := t.TempDir()
	// no scripts written — directory exists but the user-configured path
	// is a nonexistent subdir.
	a := &fakeAdapter{id: "fake", bin: "myfakelinter"}
	v := newLinterValidator(
		config.LinterEntry{ID: "fake", Paths: []string{"nope"}},
		a, base,
	)
	diags := v.Run(validate.Context{})
	gotWarning := false
	for _, d := range diags {
		if d.Severity == validate.SeverityWarning {
			gotWarning = true
		}
	}
	if !gotWarning {
		t.Fatalf("user-configured missing path must surface Warning; got %#v", diags)
	}
}

func TestLinterValidator_DisabledIsSilentSkip(t *testing.T) {
	withFakePath(t, "myfakelinter")
	t.Setenv("FAKE_LINTER_MODE", "findings")
	base := t.TempDir()
	writeScript(t, base, "a.sh")

	off := false
	a := &fakeAdapter{id: "fake", bin: "myfakelinter"}
	v := newLinterValidator(
		config.LinterEntry{ID: "fake", Paths: []string{"."}, Enabled: &off},
		a, base,
	)
	if diags := v.Run(validate.Context{}); len(diags) != 0 {
		t.Fatalf("enabled:false must be silent skip; got %#v", diags)
	}
}

func TestBoundedWriter(t *testing.T) {
	t.Parallel()
	w := newBoundedWriter(8)
	n, err := w.Write([]byte("abcdef"))
	if err != nil || n != 6 {
		t.Fatalf("first write: n=%d err=%v", n, err)
	}
	if w.Truncated() {
		t.Errorf("must not be truncated yet")
	}
	n, _ = w.Write([]byte("ghijkl"))
	if n != 6 {
		t.Errorf("must always return len(p): got %d", n)
	}
	if !w.Truncated() {
		t.Errorf("truncation flag should be set after over-cap write")
	}
	if got := string(w.Bytes()); got != "abcdefgh" {
		t.Errorf("captured bytes: want %q got %q", "abcdefgh", got)
	}
}
