package runtime

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// The skip contract for argv_append_from lives at the RunCommand level: the
// runners return spec.ErrArgvAppendEmpty, and this is the single place that
// decides what "skipped" means for the exit code, the success message and the
// notification. These tests pin that translation.

func argvAppendCmd(id, expr string, argv []string) *CommandDef {
	return &CommandDef{
		ID:             id,
		Type:           CommandTypeShell,
		Argv:           argv,
		ArgvAppendFrom: expr,
	}
}

// TestRunCommand_ArgvAppendFrom_EmptyIsSkipNotFailure: an expression that
// succeeds with no output must exit 0 — a pipeline `type: command` step wrapping
// this command Finishes rather than failing the deploy.
func TestRunCommand_ArgvAppendFrom_EmptyIsSkipNotFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	err := RunCommand(context.Background(), RunContext{
		Cmd:         argvAppendCmd("quality.staged", "true", []string{"false"}),
		Render:      &tpl.RenderContext{},
		ProjectRoot: t.TempDir(),
		Stdout:      &out,
		Stderr:      &errOut,
	})
	if err != nil {
		t.Fatalf("empty append must be a skip, got error: %v", err)
	}
	// `false` as the declared argv is the load-bearing part: had the runner run
	// the command anyway, this would have failed.
	if !strings.Contains(errOut.String(), "skipped (nothing to process)") {
		t.Errorf("expected a skip note on stderr, got %q", errOut.String())
	}
	if out.String() != "" {
		t.Errorf("expected no stdout, got %q", out.String())
	}
}

// TestRunCommand_ArgvAppendFrom_EmptySuppressesSuccessMessage: messages.success
// claims the work was done. Nothing ran, so it must not be printed.
func TestRunCommand_ArgvAppendFrom_EmptySuppressesSuccessMessage(t *testing.T) {
	cmd := argvAppendCmd("quality.staged", "true", []string{"true"})
	cmd.Messages = CommandMessages{Success: "All staged files linted."}

	var out, errOut bytes.Buffer
	err := RunCommand(context.Background(), RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{},
		ProjectRoot: t.TempDir(),
		Stdout:      &out,
		Stderr:      &errOut,
	})
	if err != nil {
		t.Fatalf("expected skip, got error: %v", err)
	}
	if strings.Contains(out.String(), "All staged files linted.") {
		t.Errorf("success message must not be emitted for a skipped command, got %q", out.String())
	}
}

// TestRunCommand_ArgvAppendFrom_NonEmptyRuns is the positive half: the computed
// items reach the child process as separate arguments, after the declared argv.
func TestRunCommand_ArgvAppendFrom_NonEmptyRuns(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "seen")

	var out, errOut bytes.Buffer
	err := RunCommand(context.Background(), RunContext{
		Cmd: argvAppendCmd("quality.staged",
			`printf '%s\n' 'a b.py' 'c.py'`,
			[]string{"sh", "-c", `printf '[%s]' "$@" > ` + marker, "sh"}),
		Render:      &tpl.RenderContext{},
		ProjectRoot: root,
		Stdout:      &out,
		Stderr:      &errOut,
	})
	if err != nil {
		t.Fatalf("expected command to run: %v", err)
	}
	got, readErr := os.ReadFile(marker)
	if readErr != nil {
		t.Fatalf("command did not run: %v", readErr)
	}
	if string(got) != "[a b.py][c.py]" {
		t.Errorf("child argv = %q, want each line as one argument", string(got))
	}
	if strings.Contains(errOut.String(), "skipped") {
		t.Errorf("a non-empty result must not skip, got %q", errOut.String())
	}
}

// TestRunCommand_ArgvAppendFrom_ExpressionFailureFails: a broken expression is
// not "nothing to process" — the command must fail and say so.
func TestRunCommand_ArgvAppendFrom_ExpressionFailureFails(t *testing.T) {
	cmd := argvAppendCmd("quality.staged", `echo boom >&2; exit 2`, []string{"true"})
	cmd.Messages = CommandMessages{Error: "Linting failed."}

	var out, errOut bytes.Buffer
	err := RunCommand(context.Background(), RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{},
		ProjectRoot: t.TempDir(),
		Stdout:      &out,
		Stderr:      &errOut,
	})
	if err == nil {
		t.Fatal("expected the command to fail when the expression fails")
	}
	if !strings.Contains(errOut.String(), "boom") {
		t.Errorf("expression stderr must reach the user, got %q", errOut.String())
	}
	if !strings.Contains(errOut.String(), "Linting failed.") {
		t.Errorf("expected the error message to be emitted, got %q", errOut.String())
	}
}

// TestRunCommand_ArgvAppendFrom_ArgsOrdering pins the documented order end to
// end: `${args}` expands in place inside the declared argv, and the computed
// items follow it. This is the consistency point of the ${args} transport —
// caller arguments arrive as argv elements here exactly as they do without the
// field, and never as program text for the expression.
func TestRunCommand_ArgvAppendFrom_ArgsOrdering(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "seen")

	var out, errOut bytes.Buffer
	err := RunCommand(context.Background(), RunContext{
		Cmd: argvAppendCmd("quality.staged",
			`printf '%s\n' 'a.py'`,
			[]string{"sh", "-c", `printf '[%s]' "$@" > ` + marker, "sh", "--fix", "${args}"}),
		Render:      &tpl.RenderContext{Args: []string{"$(touch " + filepath.Join(root, "pwned") + ")", "x y"}},
		ProjectRoot: root,
		Stdout:      &out,
		Stderr:      &errOut,
	})
	if err != nil {
		t.Fatalf("expected command to run: %v", err)
	}
	got, readErr := os.ReadFile(marker)
	if readErr != nil {
		t.Fatalf("command did not run: %v", readErr)
	}
	want := "[--fix][$(touch " + filepath.Join(root, "pwned") + ")][x y][a.py]"
	if string(got) != want {
		t.Errorf("child argv = %q, want %q (declared argv with ${args} in place, then appended items)", string(got), want)
	}
	if _, statErr := os.Stat(filepath.Join(root, "pwned")); statErr == nil {
		t.Error("caller arguments must never be evaluated as shell")
	}
}

// TestRunCommand_ArgvAppendFrom_EmptySuppressesNotification: the desktop
// notification reports the outcome of work that happened. A skipped command did
// none, so it is suppressed — the same rule a declined confirmation follows.
func TestRunCommand_ArgvAppendFrom_EmptySuppressesNotification(t *testing.T) {
	rec := installRecordingNotifier(t)

	cmd := argvAppendCmd("quality.staged", "true", []string{"true"})
	cmd.Notify = true

	var out, errOut bytes.Buffer
	err := RunCommand(context.Background(), RunContext{
		Cmd:         cmd,
		Render:      &tpl.RenderContext{},
		ProjectRoot: t.TempDir(),
		Stdout:      &out,
		Stderr:      &errOut,
	})
	if err != nil {
		t.Fatalf("expected skip, got error: %v", err)
	}
	if got := rec.snapshot(); len(got) != 0 {
		t.Errorf("expected no notification for a skipped command, got %+v", got)
	}
}
