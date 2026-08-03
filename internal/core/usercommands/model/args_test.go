package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestArgsSpecResolve pins the two asymmetries that make the block useful:
// default applies only when the caller passed nothing, and prefix applies only
// when they passed something.
func TestArgsSpecResolve(t *testing.T) {
	cases := []struct {
		name string
		spec *ArgsSpec
		user []string
		want []string
	}{
		{"nil spec passes args through", nil, []string{"-v"}, []string{"-v"}},
		{"nil spec with no args yields nothing", nil, nil, nil},

		// `argv: [go, test, -race, "${args}"]` must fall back to ./... or it
		// would test the current directory instead of the module.
		{"default fills an empty call", &ArgsSpec{Default: []string{"./..."}}, nil, []string{"./..."}},
		{"default yields to real args", &ArgsSpec{Default: []string{"./..."}}, []string{"./internal"}, []string{"./internal"}},

		// `cmd: "npm test ${args}"` needs the -- that npm would otherwise eat.
		{"prefix precedes real args", &ArgsSpec{Prefix: []string{"--"}}, []string{"--run", "x"}, []string{"--", "--run", "x"}},

		// The prefix is a separator for caller arguments, not part of the
		// default: emitting `npm test --` for a bare call would be noise.
		{"prefix is not emitted for an empty call", &ArgsSpec{Prefix: []string{"--"}}, nil, nil},
		{"prefix is not applied to the default", &ArgsSpec{Prefix: []string{"--"}, Default: []string{"all"}}, nil, []string{"all"}},

		{"both, with args", &ArgsSpec{Prefix: []string{"--"}, Default: []string{"all"}}, []string{"x"}, []string{"--", "x"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.spec.Resolve(tc.user))
		})
	}
}

// TestReferencesArgs guards the opt-in gate. A command that does not name
// ${args} has no defined slot for them, and appending blindly would be a guess —
// so the reference is what grants pass-through, not the presence of an args:
// block.
func TestReferencesArgs(t *testing.T) {
	cases := []struct {
		name string
		def  *CommandDef
		want bool
	}{
		{"nil def", nil, false},
		{"plain cmd", &CommandDef{Cmd: "npm test"}, false},
		{"plain argv", &CommandDef{Argv: []string{"go", "test", "./..."}}, false},
		{"cmd references args", &CommandDef{Cmd: "npm test ${args}"}, true},
		{"argv element is exactly args", &CommandDef{Argv: []string{"go", "test", "${args}"}}, true},
		{"argv element contains args", &CommandDef{Argv: []string{"go", "test", "--filter=${args}"}}, true},

		// An args: block alone is inert — the runner has nowhere to substitute.
		// checkPassThroughArgs calls this out explicitly.
		{"args block without a reference", &CommandDef{Cmd: "npm test", Args: &ArgsSpec{Prefix: []string{"--"}}}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.def.ReferencesArgs())
		})
	}
}

// TestArgsFieldAllowed: `args` is only meaningful where cmd/argv exist. The
// strict decoder rejects unknown fields, so a type that accepts cmd/argv but not
// args would hard-fail an otherwise reasonable definition.
func TestArgsFieldAllowed(t *testing.T) {
	for _, tc := range []struct {
		typ  CommandType
		want bool
	}{
		{CommandTypeShell, true},
		{CommandTypeServiceExec, true},
		{CommandTypeServiceRun, true},
		{CommandTypeWorkflow, false},
		{CommandTypeScript, false},
		{CommandTypeBuiltin, false},
	} {
		t.Run(string(tc.typ), func(t *testing.T) {
			require.Equal(t, tc.want, allowedFieldsFor(tc.typ)["args"])
		})
	}
}
