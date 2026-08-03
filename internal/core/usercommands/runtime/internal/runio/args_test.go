package runio

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
