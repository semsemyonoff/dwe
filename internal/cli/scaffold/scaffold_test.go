package scaffold

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
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

	t.Run("name flag with path separator is rejected", func(t *testing.T) {
		_, err := resolveName("foo/bar", nil)
		if err == nil {
			t.Fatal("expected error for name with slash, got nil")
		}
	})

	t.Run("positional arg / is rejected", func(t *testing.T) {
		_, err := resolveName("", []string{"/"})
		if err == nil {
			t.Fatal("expected error for positional arg /, got nil")
		}
	})

	t.Run("positional arg . is rejected", func(t *testing.T) {
		_, err := resolveName("", []string{"."})
		if err == nil {
			t.Fatal("expected error for positional arg ., got nil")
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

// TestSecondRunRefusesWithoutForce verifies that, once a project exists, a
// non-interactive re-run refuses with a scaffold_project_exists error rather
// than silently filling gaps.
func TestSecondRunRefusesWithoutForce(t *testing.T) {
	dir := t.TempDir()
	flags := &cmdctx.RootFlags{Output: "json"}

	if _, err := runCmd(t, flags, dir, "--name", "idem"); err != nil {
		t.Fatalf("first run: %v", err)
	}

	_, err := runCmd(t, flags, dir, "--name", "idem")
	if err == nil {
		t.Fatal("second non-interactive run should refuse without --force")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention an existing project, got: %v", err)
	}
}

// TestSecondRunForceRecreates verifies that --force recreates an existing
// project: every file is overwritten and reported as created (none skipped).
func TestSecondRunForceRecreates(t *testing.T) {
	dir := t.TempDir()
	flags := &cmdctx.RootFlags{Output: "json"}

	if _, err := runCmd(t, flags, dir, "--name", "idem"); err != nil {
		t.Fatalf("first run: %v", err)
	}

	out, err := runCmd(t, flags, dir, "--name", "idem", "--force")
	if err != nil {
		t.Fatalf("force run: %v", err)
	}
	var dto initJSON
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(dto.Created) == 0 {
		t.Error("force run should recreate (overwrite) files")
	}
	// .gitignore merge is idempotent (the block is already present), so it may
	// remain skipped; nothing else should be.
	for _, s := range dto.Skipped {
		if s != ".gitignore" {
			t.Errorf("force run should overwrite %q, but it was skipped", s)
		}
	}
	if !slices.Contains(dto.Created, "workspace.yml") {
		t.Errorf("force run should recreate workspace.yml, created = %v", dto.Created)
	}
}

// TestExistingProjectInteractiveConfirmRecreates verifies that, when a project
// already exists, an interactive run prompts for confirmation and — on yes —
// shows the form and recreates with force (all files overwritten).
func TestExistingProjectInteractiveConfirmRecreates(t *testing.T) {
	dir := t.TempDir()

	// Seed an existing project non-interactively.
	if _, err := runCmd(t, &cmdctx.RootFlags{Output: "json"}, dir, "--name", "seed"); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	origInteractive := widgets.IsInteractiveFn
	origForm := runFormFn
	origConfirm := confirmRecreateFn
	t.Cleanup(func() {
		widgets.IsInteractiveFn = origInteractive
		runFormFn = origForm
		confirmRecreateFn = origConfirm
	})
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }

	var confirmCalled bool
	confirmRecreateFn = func(string) (bool, error) {
		confirmCalled = true
		return true, nil
	}
	var formCalled bool
	runFormFn = func(_ context.Context, in formInput, _ io.Reader, _ io.Writer) (formInput, error) {
		formCalled = true
		return in, nil
	}

	// Corrupt workspace.yml so we can prove --force overwrote it.
	wsPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(wsPath, []byte("# clobbered\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Text output keeps the run interactive (JSON mode forces non-interactive).
	if _, err := runCmd(t, &cmdctx.RootFlags{Output: "text"}, dir, "--name", "seed"); err != nil {
		t.Fatalf("confirmed recreate: %v", err)
	}
	if !confirmCalled {
		t.Error("confirmation was not requested for an existing project")
	}
	if !formCalled {
		t.Error("form should be shown after confirming recreation")
	}
	ws, err := os.ReadFile(wsPath)
	if err != nil {
		t.Fatalf("read workspace.yml: %v", err)
	}
	if strings.Contains(string(ws), "clobbered") {
		t.Errorf("--force did not overwrite workspace.yml: %s", ws)
	}
	if !strings.Contains(string(ws), `name: "seed"`) {
		t.Errorf("recreated workspace.yml missing name: %s", ws)
	}
}

// TestExistingProjectInteractiveDeclineWritesNothing verifies that declining the
// recreate confirmation is a clean exit that touches nothing and never reaches
// the form.
func TestExistingProjectInteractiveDeclineWritesNothing(t *testing.T) {
	dir := t.TempDir()
	if _, err := runCmd(t, &cmdctx.RootFlags{Output: "json"}, dir, "--name", "seed"); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	origInteractive := widgets.IsInteractiveFn
	origForm := runFormFn
	origConfirm := confirmRecreateFn
	t.Cleanup(func() {
		widgets.IsInteractiveFn = origInteractive
		runFormFn = origForm
		confirmRecreateFn = origConfirm
	})
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	confirmRecreateFn = func(string) (bool, error) { return false, nil }
	runFormFn = func(context.Context, formInput, io.Reader, io.Writer) (formInput, error) {
		t.Error("form must not run after declining recreation")
		return formInput{}, nil
	}

	out, err := runCmd(t, &cmdctx.RootFlags{Output: "text"}, dir, "--name", "seed")
	if err != nil {
		t.Fatalf("decline should be a clean exit, got: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("declined recreate should write nothing, got: %q", out)
	}
}

// TestExistingProjectConfirmCancelledIsCleanExit verifies that an Esc/Ctrl-C at
// the confirmation prompt (widgets.ErrCancelled) exits cleanly.
func TestExistingProjectConfirmCancelledIsCleanExit(t *testing.T) {
	dir := t.TempDir()
	if _, err := runCmd(t, &cmdctx.RootFlags{Output: "json"}, dir, "--name", "seed"); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	origInteractive := widgets.IsInteractiveFn
	origConfirm := confirmRecreateFn
	t.Cleanup(func() {
		widgets.IsInteractiveFn = origInteractive
		confirmRecreateFn = origConfirm
	})
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	confirmRecreateFn = func(string) (bool, error) { return false, widgets.ErrCancelled }

	if _, err := runCmd(t, &cmdctx.RootFlags{Output: "text"}, dir, "--name", "seed"); err != nil {
		t.Fatalf("cancelled confirm should be a clean exit, got: %v", err)
	}
}

// TestInvalidFlagValuesRejected verifies that invalid non-interactive flag values
// (prefix, accent) are rejected before any disk write.
func TestInvalidFlagValuesRejected(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"bad prefix", []string{"--name", "x", "--prefix", "Bad Prefix"}},
		{"bad accent", []string{"--name", "x", "--accent", "red"}},
		{"short accent", []string{"--name", "x", "--accent", "#FFF"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			flags := &cmdctx.RootFlags{Output: "text"}
			if _, err := runCmd(t, flags, dir, tc.args...); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if _, err := os.Stat(filepath.Join(dir, "workspace.yml")); !os.IsNotExist(err) {
				t.Errorf("invalid input should not write workspace.yml: stat err = %v", err)
			}
		})
	}
}

// TestValidators exercises the field validators directly.
func TestValidators(t *testing.T) {
	t.Run("prefix", func(t *testing.T) {
		for _, ok := range []string{"", "dwe", "acme-2", "a_b"} {
			if err := validatePrefix(ok); err != nil {
				t.Errorf("validatePrefix(%q) = %v, want nil", ok, err)
			}
		}
		for _, bad := range []string{"-bad", "Bad", "a b", "a/b"} {
			if err := validatePrefix(bad); err == nil {
				t.Errorf("validatePrefix(%q) = nil, want error", bad)
			}
		}
	})
	t.Run("accent", func(t *testing.T) {
		for _, ok := range []string{"", "#2EC3EB", "#000000", "#ffffff"} {
			if err := validateAccent(ok); err != nil {
				t.Errorf("validateAccent(%q) = %v, want nil", ok, err)
			}
		}
		for _, bad := range []string{"red", "#FFF", "2EC3EB", "#12345G"} {
			if err := validateAccent(bad); err == nil {
				t.Errorf("validateAccent(%q) = nil, want error", bad)
			}
		}
	})
	t.Run("name", func(t *testing.T) {
		if err := validateName(""); err == nil {
			t.Error("empty name should be rejected")
		}
		if err := validateName("a/b"); err == nil {
			t.Error("name with separator should be rejected")
		}
		if err := validateName("ok-name"); err != nil {
			t.Errorf("validateName(ok-name) = %v, want nil", err)
		}
	})
	t.Run("brand text", func(t *testing.T) {
		v := brandTextValidator("tagline")
		if err := v(""); err != nil {
			t.Errorf("empty tagline should be allowed, got %v", err)
		}
		if err := v("ship it"); err != nil {
			t.Errorf("plain tagline should be allowed, got %v", err)
		}
		if err := v("line1\nline2"); err == nil {
			t.Error("tagline with newline should be rejected")
		}
	})
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

// TestFormErrorSurfaces verifies that a non-abort error returned by the form is
// propagated to the caller rather than swallowed.
func TestFormErrorSurfaces(t *testing.T) {
	dir := t.TempDir()

	origInteractive := widgets.IsInteractiveFn
	origForm := runFormFn
	t.Cleanup(func() {
		widgets.IsInteractiveFn = origInteractive
		runFormFn = origForm
	})
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	runFormFn = func(_ context.Context, _ formInput, _ io.Reader, _ io.Writer) (formInput, error) {
		return formInput{}, errors.New("form exploded")
	}

	flags := &cmdctx.RootFlags{Output: "text"}
	_, err := runCmd(t, flags, dir, "--name", "x")
	if err == nil {
		t.Fatal("expected non-nil error when form returns a non-abort error")
	}
}

// TestEmptyNameAfterFormErrors verifies that an empty name returned by the form
// produces a user-facing error rather than a silent zero-value scaffold.
func TestEmptyNameAfterFormErrors(t *testing.T) {
	dir := t.TempDir()

	origInteractive := widgets.IsInteractiveFn
	origForm := runFormFn
	t.Cleanup(func() {
		widgets.IsInteractiveFn = origInteractive
		runFormFn = origForm
	})
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }
	runFormFn = func(_ context.Context, _ formInput, _ io.Reader, _ io.Writer) (formInput, error) {
		return formInput{Name: "   ", Prefix: "x"}, nil
	}

	flags := &cmdctx.RootFlags{Output: "text"}
	_, err := runCmd(t, flags, dir)
	if err == nil {
		t.Fatal("expected error when form returns an empty name")
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
