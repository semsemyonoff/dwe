package command

import (
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// argsAtDash builds a cobra command whose ArgsLenAtDash reflects a real parse,
// so the split helpers are exercised against cobra's own behaviour rather than
// a hand-set field.
func argsAtDash(t *testing.T, argv []string) (*cobra.Command, []string) {
	t.Helper()
	var captured []string
	cmd := &cobra.Command{
		Use:  "commands",
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			captured = args
			return nil
		},
	}
	cmd.SetOut(&nopWriter{})
	cmd.SetErr(&nopWriter{})
	cmd.SetArgs(argv)
	require.NoError(t, cmd.Execute())
	return cmd, captured
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestArgSplitting pins the near/far split. Every id, group-filter and selector
// decision in RunE counts positional args, so a merged slice would make
// `dwe cmd site.test -- --run x` look like a three-argument invocation and fall
// through to the interactive selector instead of running the command.
func TestArgSplitting(t *testing.T) {
	cases := []struct {
		name     string
		argv     []string
		wantNear []string
		wantThru []string
	}{
		{"no dash, no args", nil, []string{}, nil},
		{"no dash, id only", []string{"site.test"}, []string{"site.test"}, nil},
		{"id plus passthrough", []string{"site.test", "--", "--run", "x.ts"}, []string{"site.test"}, []string{"--run", "x.ts"}},
		{"dash with nothing after it", []string{"site.test", "--"}, []string{"site.test"}, nil},
		{"passthrough only, no id", []string{"--", "-v"}, []string{}, []string{"-v"}},
		{"a second dash belongs to the callee", []string{"site.test", "--", "--", "-v"}, []string{"site.test"}, []string{"--", "-v"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, args := argsAtDash(t, tc.argv)
			require.Equal(t, tc.wantNear, nearArgs(cmd, args), "near args")
			require.Equal(t, tc.wantThru, passThroughArgs(cmd, args), "pass-through args")
		})
	}
}

// TestCommandIDArgs: only the near side is count-checked. cobra's stock
// MaximumNArgs(1) reported "Accepts at most 1 arg(s), received 3" for a
// perfectly reasonable `-- --run x` and left the caller nowhere to go.
func TestCommandIDArgs(t *testing.T) {
	t.Run("id plus passthrough is accepted", func(t *testing.T) {
		cmd, args := argsAtDash(t, []string{"site.test", "--", "--run", "x.ts"})
		require.NoError(t, commandIDArgs(cmd, args))
	})

	// The suggestion must carry BOTH the stray near-side words and anything the
	// caller already put after a real `--`. Building it from args[1:near] alone
	// silently dropped the latter, so a user who copied the corrected line lost
	// the very arguments that were already in the right place.
	t.Run("suggestion keeps arguments already past the dash", func(t *testing.T) {
		cmd, args := argsAtDash(t, []string{"site.test", "extra", "--", "--flag"})
		err := commandIDArgs(cmd, args)
		require.Error(t, err)
		require.Contains(t, err.Error(), "dwe cmd site.test -- extra --flag",
			"the corrected command must not drop the caller's existing pass-through args")
	})

	// Pass-through args with no id have nowhere to go: the run route would fall
	// through to the selector, and its non-interactive branch (CI pipe, or any
	// container — the bridge daemon force-sets DWE_NONINTERACTIVE=1) prints the
	// command list and exits 0, silently discarding them.
	t.Run("passthrough without an id is refused", func(t *testing.T) {
		cmd, args := argsAtDash(t, []string{"--", "--run", "x.ts"})
		err := commandIDArgs(cmd, args)
		require.Error(t, err)
		require.Contains(t, err.Error(), "need a command id")
		require.Contains(t, err.Error(), "dwe cmd <id> -- --run x.ts")
	})

	t.Run("a bare dash with nothing after it is still fine", func(t *testing.T) {
		cmd, args := argsAtDash(t, []string{"--"})
		require.NoError(t, commandIDArgs(cmd, args))
	})

	// A bare flag would be eaten by cobra's parser, so the realistic
	// two-positional case is an id plus a stray word (a filename, a package).
	t.Run("two ids without a dash still fail, but say how to pass args", func(t *testing.T) {
		cmd, args := argsAtDash(t, []string{"site.test", "src/foo.test.ts"})
		err := commandIDArgs(cmd, args)
		require.Error(t, err)
		require.Contains(t, err.Error(), "dwe cmd site.test -- src/foo.test.ts",
			"the error must show the corrected invocation")
		require.NotContains(t, err.Error(), "Accepts at most")
	})
}

// TestCheckPassThroughArgs: the rejection has to name the command, the reason
// and a way forward — that is the whole point of replacing the cobra text.
func TestCheckPassThroughArgs(t *testing.T) {
	t.Run("no args is always fine", func(t *testing.T) {
		def := &model.CommandDef{ID: "site.test", Cmd: "npm test"}
		require.NoError(t, checkPassThroughArgs(def, runOpts{}))
	})

	t.Run("args accepted when the command references them", func(t *testing.T) {
		def := &model.CommandDef{ID: "site.test", Cmd: "npm test ${args}"}
		require.NoError(t, checkPassThroughArgs(def, runOpts{PassThroughArgs: []string{"-v"}}))
	})

	t.Run("rejection names the command and the fix", func(t *testing.T) {
		def := &model.CommandDef{ID: "site.test", Cmd: "npm test", Service: "site"}
		err := checkPassThroughArgs(def, runOpts{PassThroughArgs: []string{"--run", "x"}})
		require.Error(t, err)
		msg := err.Error()
		require.Contains(t, msg, `"site.test"`)
		require.Contains(t, msg, "${args}", "must show the slot to declare")
		require.Contains(t, msg, "npm test ${args}", "the suggestion is built from the real cmd")
		require.Contains(t, msg, "dwe cmd -i site.test")
		require.Contains(t, msg, "dwe shell site -c", "the escape hatch is named when a service is known")
	})

	t.Run("argv commands get the argv-shaped suggestion", func(t *testing.T) {
		def := &model.CommandDef{ID: "backend.test", Argv: []string{"go", "test", "./..."}}
		err := checkPassThroughArgs(def, runOpts{PassThroughArgs: []string{"./internal"}})
		require.Error(t, err)
		require.Contains(t, err.Error(), `argv: [..., "${args}"]`)
	})

	t.Run("an inert args block is called out", func(t *testing.T) {
		def := &model.CommandDef{ID: "x.y", Cmd: "true", Args: &model.ArgsSpec{Prefix: []string{"--"}}}
		err := checkPassThroughArgs(def, runOpts{PassThroughArgs: []string{"z"}})
		require.Error(t, err)
		require.Contains(t, err.Error(), "declares an `args:` block")
	})

	t.Run("declared params are listed, deterministically", func(t *testing.T) {
		def := &model.CommandDef{
			ID:     "backend.migrate",
			Cmd:    "goose ${param.action}",
			Params: map[string]model.ParamDef{"action": {}, "zzz": {}, "aaa": {}},
		}
		err := checkPassThroughArgs(def, runOpts{PassThroughArgs: []string{"up"}})
		require.Error(t, err)
		require.Contains(t, err.Error(), "aaa, action, zzz",
			"param names come from a map; they must be sorted or the message flaps")
	})
}

// TestFirstWords keeps the cmd: suggestion to one readable line even when the
// command is a long shell pipeline.
func TestFirstWords(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "<command>"},
		{"npm test", "npm test"},
		{"npm test | tail -5", "npm test"},
		{"a; b", "a"},
		{"a && b", "a"},
		{"line one\nline two", "line one"},
	} {
		require.Equal(t, tc.want, firstWords(tc.in), "firstWords(%q)", tc.in)
	}
}
