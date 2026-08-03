package tpl

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRenderArgs covers the NON-execution ${args} references — a messages line,
// an env value, a workdir. Those land in a display string or a single exec
// argument with no shell to re-parse them, so a plain space-joined form is
// correct.
//
// This function deliberately does NOT quote. It used to, in order to be pasted
// into a `sh -c` program, and that was the wrong layer: quoting is safe only in
// an unquoted argument position, and nothing constrained where a command author
// wrote ${args}. `cmd: 'printf "%s\n" "${args}"'` with `$(id)` executed the
// substitution. Execution now passes the arguments as positional parameters
// instead (see runio.RenderShellCommand / RenderArgvWithArgs), which is why
// quoting here would be wrong rather than merely redundant.
func TestRenderArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"empty renders to nothing", nil, ""},
		{"plain args", []string{"a", "b"}, "a b"},
		{"metacharacters are not escaped — no shell will see this", []string{"x; y", "$(id)"}, "x; y $(id)"},
		{"single value passes through verbatim", []string{"src/a b.ts"}, "src/a b.ts"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, renderArgs(tc.args))
		})
	}
}

// TestRenderCommandArgs covers ${args} through the real compile+render path.
func TestRenderCommandArgs(t *testing.T) {
	t.Run("substitutes into a non-execution field", func(t *testing.T) {
		got, err := RenderCommand("ran with ${args}", &RenderContext{Args: []string{"--run", "x.test.ts"}})
		require.NoError(t, err)
		require.Equal(t, "ran with --run x.test.ts", got)
	})

	t.Run("empty args leave the text otherwise intact", func(t *testing.T) {
		got, err := RenderCommand("ran with ${args}", &RenderContext{})
		require.NoError(t, err)
		require.Equal(t, "ran with ", got)
	})

	// ${args} is a whole-namespace reference; a sub-key has nothing to index
	// into and must degrade like any other unknown ${...}, not error.
	t.Run("args with a tail falls through to the generic resolver", func(t *testing.T) {
		got, err := RenderCommand("x ${args.0}", &RenderContext{Args: []string{"a"}})
		require.NoError(t, err)
		require.Equal(t, "x ", got)
	})
}
