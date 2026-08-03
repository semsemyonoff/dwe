package runio

import (
	"fmt"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// argsShellExpansion is what a ${args} slot becomes inside a `cmd:` string, and
// shellArgv0 is the $0 that precedes the real arguments in the `sh -c` argv.
//
// The caller's bytes reach the shell as POSITIONAL PARAMETERS and never appear
// in the program text. That is the whole security property. An earlier design
// shell-quoted the arguments and pasted them into the script, which is safe only
// in an unquoted argument position — and nothing constrains where a command
// author writes ${args}. The natural shell habit is to quote it, at which point
//
//	cmd: 'printf "%s\n" "${args}"'   +   dwe cmd x -- '$(touch /tmp/pwned)'
//
// renders as `printf "%s\n" "'$(touch /tmp/pwned)'"`: the surrounding double
// quotes make the single quotes literal and the command substitution executes.
// Verified exploitable before this change. With "$@" there is nothing to escape
// out of, in either placement.
const (
	argsShellExpansion = `"$@"`
	shellArgv0         = "dwe"
)

// RenderShellCommand renders a `cmd:` template into a `sh -c` script plus the
// positional parameters that back its "$@".
//
// The returned tail is nil when the template has no ${args} slot, so the argv of
// a command without pass-through stays byte-identical to before.
func RenderShellCommand(cmdTemplate string, rc *tpl.RenderContext) (script string, positional []string, err error) {
	hasArgs := strings.Contains(cmdTemplate, model.ArgsToken)

	src := cmdTemplate
	if hasArgs {
		src = strings.ReplaceAll(src, model.ArgsToken, argsShellExpansion)
	}

	script, err = tpl.RenderCommand(src, rc)
	if err != nil {
		return "", nil, fmt.Errorf("render cmd: %w", err)
	}
	if !hasArgs {
		return script, nil, nil
	}

	// $0 first: in `sh -c <script> <argv0> <args...>` the word after the script
	// becomes $0, so without it the first real argument would be swallowed and
	// never appear in "$@".
	return script, append([]string{shellArgv0}, renderArgsOf(rc)...), nil
}

// RenderArgvWithArgs renders an argv vector, splicing the ${args} element.
//
// The splice is restricted to an element that is exactly `${args}`: the
// arguments are already separate argv entries there and must land as N entries.
// An element that merely embeds the token (`--filter=${args}`) is rejected at
// load time by CommandDef.Validate — no useful rendering exists for it, since no
// shell re-splits an argv element, so any join would hand the child one mangled
// argument.
//
// An empty argument set splices to nothing, so the token vanishes rather than
// leaving an empty-string argument behind: `go test -race ""` would be a
// different (and failing) command from `go test -race`.
func RenderArgvWithArgs(argv []string, rc *tpl.RenderContext) ([]string, error) {
	args := renderArgsOf(rc)
	out := make([]string, 0, len(argv)+len(args))
	for i, arg := range argv {
		if arg == model.ArgsToken {
			out = append(out, args...)
			continue
		}
		rendered, err := tpl.RenderCommand(arg, rc)
		if err != nil {
			return nil, fmt.Errorf("render argv[%d]: %w", i, err)
		}
		out = append(out, rendered)
	}
	return out, nil
}

// renderArgsOf is the nil-safe accessor for the pass-through arguments.
func renderArgsOf(rc *tpl.RenderContext) []string {
	if rc == nil {
		return nil
	}
	return rc.Args
}
