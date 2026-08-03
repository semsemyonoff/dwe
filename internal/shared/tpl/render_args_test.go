package tpl

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRenderArgsQuoting is a security test, not a formatting one. ${args} in a
// `cmd:` string is interpolated into text handed to `sh -c`, and its content
// comes from whoever typed the command line. Anything short of full quoting
// lets an argument change the command's structure.
func TestRenderArgsQuoting(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"empty renders to nothing", nil, ""},
		{"plain args", []string{"a", "b"}, `'a' 'b'`},
		{"space stays one argument", []string{"src/a b.ts"}, `'src/a b.ts'`},
		{"semicolon cannot chain a command", []string{"x; rm -rf /"}, `'x; rm -rf /'`},
		{"command substitution stays literal", []string{"$(whoami)"}, `'$(whoami)'`},
		{"backtick substitution stays literal", []string{"`id`"}, "'`id`'"},
		{"variable stays literal", []string{"$HOME"}, `'$HOME'`},
		{"redirect cannot escape", []string{"> /etc/passwd"}, `'> /etc/passwd'`},
		{"embedded single quote is escaped", []string{"it's"}, `'it'\''s'`},
		{"quote-escape cannot break out", []string{`'; rm -rf /; '`}, `''\''; rm -rf /; '\'''`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, renderArgs(tc.args))
		})
	}
}

// TestRenderCommandArgs covers ${args} through the real compile+render path.
func TestRenderCommandArgs(t *testing.T) {
	t.Run("substitutes into a cmd string", func(t *testing.T) {
		got, err := RenderCommand("npm test ${args}", &RenderContext{Args: []string{"--run", "x.test.ts"}})
		require.NoError(t, err)
		require.Equal(t, `npm test '--run' 'x.test.ts'`, got)
	})

	t.Run("empty args leave the command otherwise intact", func(t *testing.T) {
		got, err := RenderCommand("npm test ${args}", &RenderContext{})
		require.NoError(t, err)
		require.Equal(t, "npm test ", got)
	})

	// ${args} is a whole-namespace reference; a sub-key has nothing to index
	// into and must degrade like any other unknown ${...}, not error.
	t.Run("args with a tail falls through to the generic resolver", func(t *testing.T) {
		got, err := RenderCommand("x ${args.0}", &RenderContext{Args: []string{"a"}})
		require.NoError(t, err)
		require.Equal(t, "x ", got)
	})
}
