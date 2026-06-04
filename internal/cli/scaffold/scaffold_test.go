package scaffold

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"

	huh "charm.land/huh/v2"
)

// runCmd executes the init command with the given args, capturing stdout.
// It runs with cwd set to dir so the (default) cwd target lands in the temp dir.
func runCmd(t *testing.T, flags *cmdctx.RootFlags, dir string, args ...string) (string, error) {
	t.Helper()

	cmd := NewCmd("configuration", flags)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(bytes.NewReader(nil)) // non-TTY stdin
	cmd.SetArgs(args)
	cmd.SetContext(context.Background())

	// Execute inside the target dir so resolveName / cwd target work.
	var err error
	withWD(t, dir, func() {
		err = cmd.Execute()
	})
	return out.String(), err
}

// withWD runs fn with the working directory temporarily set to dir.
func withWD(t *testing.T, dir string, fn func()) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	fn()
}

func TestResolveName(t *testing.T) {
	t.Run("name flag wins", func(t *testing.T) {
		got, err := resolveName("explicit", []string{"arg"})
		if err != nil {
			t.Fatalf("resolveName: %v", err)
		}
		if got != "explicit" {
			t.Errorf("got %q, want explicit", got)
		}
	})

	t.Run("positional arg used when no flag", func(t *testing.T) {
		got, err := resolveName("", []string{"from-arg"})
		if err != nil {
			t.Fatalf("resolveName: %v", err)
		}
		if got != "from-arg" {
			t.Errorf("got %q, want from-arg", got)
		}
	})

	t.Run("falls back to cwd basename", func(t *testing.T) {
		dir := t.TempDir()
		sub := filepath.Join(dir, "my-proj")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		var got string
		var err error
		withWD(t, sub, func() {
			got, err = resolveName("", nil)
		})
		if err != nil {
			t.Fatalf("resolveName: %v", err)
		}
		if got != "my-proj" {
			t.Errorf("got %q, want my-proj", got)
		}
	})
}

// TestNonInteractiveScaffold verifies a flag-driven run maps flags to the right
// Options: name/prefix land in workspace.yml, service folder is created, and
// branding lands in styles.yml.
func TestNonInteractiveScaffold(t *testing.T) {
	dir := t.TempDir()
	flags := &cmdctx.RootFlags{Output: "text"}

	_, err := runCmd(t, flags, dir,
		"--name", "acme-app",
		"--prefix", "acme",
		"--service", "api",
		"--brand-title", "Acme",
		"--tagline", "ship it",
		"--accent", "#FF0000",
	)
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	wsBytes, err := os.ReadFile(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("read workspace.yml: %v", err)
	}
	ws := string(wsBytes)
	if !strings.Contains(ws, `name: "acme-app"`) {
		t.Errorf("workspace.yml missing name: %s", ws)
	}
	if !strings.Contains(ws, `prefix: "acme"`) {
		t.Errorf("workspace.yml missing prefix: %s", ws)
	}

	if _, err := os.Stat(filepath.Join(dir, "workspace", "services", "api", "service.yml")); err != nil {
		t.Errorf("service folder not created: %v", err)
	}

	stylesBytes, err := os.ReadFile(filepath.Join(dir, "workspace", "styles.yml"))
	if err != nil {
		t.Fatalf("read styles.yml: %v", err)
	}
	styles := string(stylesBytes)
	for _, want := range []string{"Acme", "ship it", "#FF0000"} {
		if !strings.Contains(styles, want) {
			t.Errorf("styles.yml missing %q: %s", want, styles)
		}
	}
}

// TestServiceNoneOmitsFolder verifies --service "" scaffolds no service folder.
func TestServiceNoneOmitsFolder(t *testing.T) {
	dir := t.TempDir()
	flags := &cmdctx.RootFlags{Output: "text"}

	if _, err := runCmd(t, flags, dir, "--name", "x", "--service", ""); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "workspace", "services")); !os.IsNotExist(err) {
		t.Errorf("expected no services dir, stat err = %v", err)
	}
}

// TestJSONOutputShape verifies --output json emits the documented envelope.
func TestJSONOutputShape(t *testing.T) {
	dir := t.TempDir()
	flags := &cmdctx.RootFlags{Output: "json"}

	out, err := runCmd(t, flags, dir, "--name", "jsonproj")
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	var dto initJSON
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if dto.Target == "" {
		t.Error("target empty")
	}
	if len(dto.Created) == 0 {
		t.Error("created should be non-empty on first run")
	}
	// created/skipped must serialize as arrays, never null.
	if !strings.Contains(out, `"created"`) || strings.Contains(out, `"created":null`) {
		t.Errorf("created should be a JSON array: %s", out)
	}
}

// TestIdempotentSecondRunSkips verifies a second run reports everything skipped.
func TestIdempotentSecondRunSkips(t *testing.T) {
	dir := t.TempDir()
	flags := &cmdctx.RootFlags{Output: "json"}

	if _, err := runCmd(t, flags, dir, "--name", "idem"); err != nil {
		t.Fatalf("first run: %v", err)
	}

	out, err := runCmd(t, flags, dir, "--name", "idem")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	var dto initJSON
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(dto.Created) != 0 {
		t.Errorf("second run created %v, want none", dto.Created)
	}
	if len(dto.Skipped) == 0 {
		t.Error("second run should skip existing files")
	}
}

// TestFormAbortWritesNothing verifies that a mid-form Ctrl-C
// (huh.ErrUserAborted) returned by the form leaves the disk untouched and exits
// cleanly.
func TestFormAbortWritesNothing(t *testing.T) {
	dir := t.TempDir()

	// Force the interactive branch and make the form abort.
	origInteractive := widgets.IsInteractiveFn
	origForm := runFormFn
	t.Cleanup(func() {
		widgets.IsInteractiveFn = origInteractive
		runFormFn = origForm
	})
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	runFormFn = func(context.Context, formInput, io.Reader, io.Writer) (formInput, error) {
		return formInput{}, huh.ErrUserAborted
	}

	flags := &cmdctx.RootFlags{Output: "text"}
	out, err := runCmd(t, flags, dir, "--name", "aborted")
	if err != nil {
		t.Fatalf("abort should be a clean exit, got: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("aborted run should write nothing, got: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "workspace.yml")); !os.IsNotExist(err) {
		t.Errorf("aborted run wrote workspace.yml: stat err = %v", err)
	}
}

// TestFormValuesUsed verifies that values returned by the form (not the flag
// defaults) drive the scaffold Options.
func TestFormValuesUsed(t *testing.T) {
	dir := t.TempDir()

	origInteractive := widgets.IsInteractiveFn
	origForm := runFormFn
	t.Cleanup(func() {
		widgets.IsInteractiveFn = origInteractive
		runFormFn = origForm
	})
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	runFormFn = func(_ context.Context, in formInput, _ io.Reader, _ io.Writer) (formInput, error) {
		// Ignore the prefill; return form-collected values.
		return formInput{Name: "from-form", Prefix: "ff"}, nil
	}

	flags := &cmdctx.RootFlags{Output: "text"}
	if _, err := runCmd(t, flags, dir, "--name", "from-flag"); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	ws, err := os.ReadFile(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("read workspace.yml: %v", err)
	}
	if !strings.Contains(string(ws), `name: "from-form"`) {
		t.Errorf("form name not used: %s", ws)
	}
	if !strings.Contains(string(ws), `prefix: "ff"`) {
		t.Errorf("form prefix not used: %s", ws)
	}
}
