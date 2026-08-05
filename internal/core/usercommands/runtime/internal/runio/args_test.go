package runio

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"

	"github.com/stretchr/testify/require"
)

// TestRenderShellCommand_NoInterpolation is the security test for the `cmd:`
// path. The property under test is structural, not cosmetic: the caller's bytes
// must not appear anywhere in the shell PROGRAM, only in its positional
// parameters. Quoting-and-pasting was the previous design and was exploitable —
// a command author writing `"${args}"` (the natural shell habit) turned the
// quoting inert and let `$(…)` execute.
func TestRenderShellCommand_NoInterpolation(t *testing.T) {
	// Distinctive multi-character payloads: a bare quote character would appear
	// in the script legitimately (`"$@"` contains one), so the "absent from the
	// program text" assertion needs values that cannot occur by accident. The
	// degenerate single-character and empty cases are covered by the positional
	// round-trip below.
	hostile := []string{"$(touch /tmp/dwe-pwned-marker)", "`id`", "a; rm -rf /", "x y", "'", `"`, ""}
	rc := &tpl.RenderContext{Args: hostile}

	for _, tmpl := range []string{
		`npm test ${args}`,           // unquoted slot — the documented form
		`printf "%s\n" "${args}"`,    // author quoted it — the exploit shape
		`echo '${args}'`,             // single-quoted
		`f() { :; }; f ${args} | wc`, // inside a larger script
	} {
		t.Run(tmpl, func(t *testing.T) {
			script, positional, err := RenderShellCommand(tmpl, rc)
			require.NoError(t, err)

			for _, h := range hostile {
				if len([]rune(h)) < 2 {
					continue
				}
				require.NotContains(t, script, h,
					"caller bytes must never reach the shell program text")
			}
			require.Equal(t, append([]string{shellArgv0}, hostile...), positional,
				"every argument travels as a positional parameter, $0 first")
			require.Contains(t, script, `"$@"`)
		})
	}
}

// TestRenderShellCommand_NoArgsSlot: a template without ${args} must produce a
// byte-identical script and no argv tail, so every existing command definition
// behaves exactly as before.
func TestRenderShellCommand_NoArgsSlot(t *testing.T) {
	script, positional, err := RenderShellCommand("npm test", &tpl.RenderContext{Args: []string{"ignored"}})
	require.NoError(t, err)
	require.Equal(t, "npm test", script)
	require.Nil(t, positional, "no slot means no argv tail")
}

// TestRenderShellCommand_RoundTripsThroughRealShell runs the rendered form to
// confirm the arguments arrive intact and nothing executes. Reasoning about
// quoting is exactly where the previous design went wrong, so this asserts
// against /bin/sh rather than against a model of it.
func TestRenderShellCommand_RoundTripsThroughRealShell(t *testing.T) {
	// Execution is proved by a side effect, not by scanning output: the literal
	// argument text legitimately contains whatever marker word it names, so only
	// a file the substitution would have created distinguishes "executed" from
	// "passed through as text".
	marker := filepath.Join(t.TempDir(), "executed")

	t.Run("unquoted slot keeps boundaries and executes nothing", func(t *testing.T) {
		args := []string{"a b", "c;d", "$(touch " + marker + ")", "`touch " + marker + "`", "'quoted'", ""}
		// Bare ${args} — the documented form. It becomes "$@", so the loop sees
		// one word per argument regardless of the bytes inside them.
		script, positional, err := RenderShellCommand(`for a in ${args}; do printf '[%s]' "$a"; done`, &tpl.RenderContext{Args: args})
		require.NoError(t, err)

		out, err := exec.Command("/bin/sh", append([]string{"-c", script}, positional...)...).CombinedOutput()
		require.NoError(t, err)

		require.NoFileExists(t, marker, "no substitution may execute")
		for _, a := range args {
			require.Contains(t, string(out), "["+a+"]",
				"argument must arrive as one parameter with its bytes intact")
		}
	})

	// The exploit shape: the author wrapped the slot in double quotes. Boundaries
	// degrade (that is the author's mis-quoting), but nothing may execute.
	t.Run("author-quoted slot still executes nothing", func(t *testing.T) {
		script, positional, err := RenderShellCommand(`printf "%s" "${args}"`,
			&tpl.RenderContext{Args: []string{"$(touch " + marker + ")"}})
		require.NoError(t, err)

		_, err = exec.Command("/bin/sh", append([]string{"-c", script}, positional...)...).CombinedOutput()
		require.NoError(t, err)
		require.NoFileExists(t, marker, "quoting the slot must not reopen command substitution")
	})
}

func TestRenderArgvWithArgs(t *testing.T) {
	rc := &tpl.RenderContext{Args: []string{"./a", "./b"}}

	t.Run("exact element splices element-wise", func(t *testing.T) {
		got, err := RenderArgvWithArgs([]string{"go", "test", "${args}"}, rc)
		require.NoError(t, err)
		require.Equal(t, []string{"go", "test", "./a", "./b"}, got)
	})

	// An empty set must remove the element, not leave "" behind:
	// `go test -race ""` is a different, failing command.
	t.Run("empty args remove the element", func(t *testing.T) {
		got, err := RenderArgvWithArgs([]string{"go", "test", "${args}"}, &tpl.RenderContext{})
		require.NoError(t, err)
		require.Equal(t, []string{"go", "test"}, got)
	})

	t.Run("nil context does not panic", func(t *testing.T) {
		got, err := RenderArgvWithArgs([]string{"go", "test"}, nil)
		require.NoError(t, err)
		require.Equal(t, []string{"go", "test"}, got)
	})

	t.Run("other elements still render templates", func(t *testing.T) {
		got, err := RenderArgvWithArgs([]string{"echo", "${args}"},
			&tpl.RenderContext{Args: []string{strings.TrimSpace(" x ")}})
		require.NoError(t, err)
		require.Equal(t, []string{"echo", "x"}, got)
	})
}

// TestRenderShellCommand_ArgsHiddenFromTemplates closes the second-round review
// finding: rewriting the ${args} token is not enough if the render context still
// exposes the arguments to the raw Go-template form. `{{ index .Args 0 }}` would
// interpolate a caller-controlled string straight into the shell program, which
// is precisely what the "$@" transport exists to prevent.
func TestRenderShellCommand_ArgsHiddenFromTemplates(t *testing.T) {
	payload := "$(touch /tmp/dwe-should-not-exist)"
	rc := &tpl.RenderContext{Args: []string{payload}}

	// Hiding the field makes the reference fail loudly rather than silently
	// render empty — a definition that tried to smuggle arguments in this way
	// breaks at render time instead of shipping a subtly different command.
	t.Run("cmd template cannot reach .Args", func(t *testing.T) {
		script, _, err := RenderShellCommand(`: ${args}; echo {{ index .Args 0 }}`, rc)
		require.Error(t, err, "a .Args reference must not silently succeed")
		require.NotContains(t, script, payload)
	})

	t.Run("argv element cannot reach .Args either", func(t *testing.T) {
		_, err := RenderArgvWithArgs([]string{"echo", `{{ index .Args 0 }}`, "${args}"}, rc)
		require.Error(t, err)
	})

	// A bare `{{ .Args }}` has no index to run out of, so it renders — but of an
	// empty slice, which is the point: no caller bytes reach the program text.
	t.Run("bare .Args renders empty, not the payload", func(t *testing.T) {
		script, positional, err := RenderShellCommand(`echo ${args} {{ .Args }}`, rc)
		require.NoError(t, err)
		require.NotContains(t, script, payload,
			"the raw template form must not interpolate caller arguments")
		require.Equal(t, []string{shellArgv0, payload}, positional,
			"the arguments still travel as positional parameters")
	})

	// The sanitized copy must not disturb anything else the template reads.
	t.Run("other context fields still render", func(t *testing.T) {
		script, _, err := RenderShellCommand(`echo ${args} ${param.who}`,
			&tpl.RenderContext{Args: []string{"x"}, Params: map[string]any{"who": "world"}})
		require.NoError(t, err)
		require.Contains(t, script, "world")
		require.Contains(t, script, `"$@"`)
	})
}

// --- argv_append_from ------------------------------------------------------

// appendRC is the minimal RunContext AppendArgvFrom needs: a command carrying
// the expression, a project root to run it in, and a stderr sink.
func appendRC(t *testing.T, expr string, args []string, stderr io.Writer) spec.RunContext {
	t.Helper()
	return spec.RunContext{
		Cmd:         &model.CommandDef{Type: model.CommandTypeShell, ID: "q.staged", ArgvAppendFrom: expr},
		Render:      &tpl.RenderContext{Args: args},
		ProjectRoot: t.TempDir(),
		Stderr:      stderr,
	}
}

// TestAppendArgvFrom_OneElementPerLine is the core contract: stdout is DATA.
// Every line becomes exactly one argv element, whatever bytes it contains —
// spaces, quotes and `$(…)` included. Anything that re-parsed the output as
// shell would split "a b.py" into two arguments and run the substitution.
func TestAppendArgvFrom_OneElementPerLine(t *testing.T) {
	expr := `printf '%s\n' 'a.py' 'src/a b.py' "it's.py" '$(touch /tmp/dwe-append-pwned)'`
	got, err := AppendArgvFrom(context.Background(), appendRC(t, expr, nil, io.Discard), []string{"ruff", "check"})
	require.NoError(t, err)
	require.Equal(t, []string{"ruff", "check", "a.py", "src/a b.py", "it's.py", "$(touch /tmp/dwe-append-pwned)"}, got)
	require.NoFileExists(t, "/tmp/dwe-append-pwned", "output must never be re-parsed as shell")
}

// TestAppendArgvFrom_TrailingNewlineAndBlankLines: the trailing newline every
// well-behaved tool emits must not become an empty argv element, and neither
// must a blank line in the middle — `ruff check ""` is a different command.
func TestAppendArgvFrom_TrailingNewlineAndBlankLines(t *testing.T) {
	got, err := AppendArgvFrom(context.Background(),
		appendRC(t, `printf 'a.py\n\nb.py\n'`, nil, io.Discard), []string{"ruff"})
	require.NoError(t, err)
	require.Equal(t, []string{"ruff", "a.py", "b.py"}, got)
}

// TestAppendArgvFrom_EmptyOutputSkips pins the empty-list decision: an
// expression that succeeds with no output is the "nothing to process" signal,
// never "run the declared argv anyway" (`ruff check` with no files lints the
// whole tree — the opposite of the intent).
func TestAppendArgvFrom_EmptyOutputSkips(t *testing.T) {
	for _, expr := range []string{"true", "printf ''", `printf '\n\n'`} {
		t.Run(expr, func(t *testing.T) {
			_, err := AppendArgvFrom(context.Background(),
				appendRC(t, expr, nil, io.Discard), []string{"ruff", "check"})
			require.ErrorIs(t, err, spec.ErrArgvAppendEmpty)
		})
	}
}

// TestAppendArgvFrom_ExpressionFailureSurfaces: a failing expression fails the
// command (it is not an empty list), and its stderr reaches the user rather
// than being swallowed with the captured stdout.
func TestAppendArgvFrom_ExpressionFailureSurfaces(t *testing.T) {
	var stderr bytes.Buffer
	_, err := AppendArgvFrom(context.Background(),
		appendRC(t, `echo "not a git repo" >&2; exit 3`, nil, &stderr), []string{"ruff"})
	require.Error(t, err)
	require.NotErrorIs(t, err, spec.ErrArgvAppendEmpty, "a failure is not an empty list")
	require.Contains(t, err.Error(), "argv_append_from")
	require.Contains(t, stderr.String(), "not a git repo")
}

// TestAppendArgvFrom_ArgsHidden is the consistency point of the ${args} slot:
// the caller's bytes travel as positional parameters everywhere else, so they
// must not be reachable from this expression's program text either. The literal
// ${args} token is rejected at load time; the raw Go-template form is only
// stoppable here.
func TestAppendArgvFrom_ArgsHidden(t *testing.T) {
	payload := "$(touch /tmp/dwe-append-args-pwned)"

	t.Run("template form cannot reach .Args", func(t *testing.T) {
		_, err := AppendArgvFrom(context.Background(),
			appendRC(t, `echo {{ index .Args 0 }}`, []string{payload}, io.Discard), nil)
		require.Error(t, err, "a .Args reference must not silently succeed")
	})

	t.Run("bare .Args renders empty, not the payload", func(t *testing.T) {
		script, err := RenderArgvAppendFrom(`echo {{ .Args }}`, &tpl.RenderContext{Args: []string{payload}})
		require.NoError(t, err)
		require.NotContains(t, script, payload)
	})

	t.Run("other context fields still render", func(t *testing.T) {
		got, err := AppendArgvFrom(context.Background(),
			spec.RunContext{
				Cmd:    &model.CommandDef{ArgvAppendFrom: "echo ${param.who}"},
				Render: &tpl.RenderContext{Params: map[string]any{"who": "world"}},
				Stderr: io.Discard,
			}, []string{"echo"})
		require.NoError(t, err)
		require.Equal(t, []string{"echo", "world"}, got)
	})
}

// TestAppendArgvFrom_RunsInProjectRoot: the expression is host-side and its
// relative paths must mean the same thing regardless of where dwe was invoked
// from — the same rule condition.EvalCmd applies.
func TestAppendArgvFrom_RunsInProjectRoot(t *testing.T) {
	root := t.TempDir()
	rc := spec.RunContext{
		Cmd:         &model.CommandDef{ArgvAppendFrom: "pwd"},
		Render:      &tpl.RenderContext{},
		ProjectRoot: root,
		Stderr:      io.Discard,
	}
	got, err := AppendArgvFrom(context.Background(), rc, nil)
	require.NoError(t, err)
	require.Len(t, got, 1)
	// macOS resolves /var → /private/var, so compare resolved paths.
	wantRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	gotRoot, err := filepath.EvalSymlinks(got[0])
	require.NoError(t, err)
	require.Equal(t, wantRoot, gotRoot)
}

// TestAppendArgvFrom_NoExpressionIsIdentity: a command without the field must
// keep its argv byte-identical and spawn nothing.
func TestAppendArgvFrom_NoExpressionIsIdentity(t *testing.T) {
	got, err := AppendArgvFrom(context.Background(),
		spec.RunContext{Cmd: &model.CommandDef{Type: model.CommandTypeShell}}, []string{"git", "status"})
	require.NoError(t, err)
	require.Equal(t, []string{"git", "status"}, got)
}

// TestAppendArgvFrom_ContextCancellation: the expression is a child process and
// must die with the invocation.
func TestAppendArgvFrom_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := AppendArgvFrom(ctx, appendRC(t, "sleep 30", nil, io.Discard), []string{"ruff"})
	require.Error(t, err)
}
