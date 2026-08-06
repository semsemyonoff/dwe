package runio

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
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

	// Render with Args hidden. The ${args} slot was already rewritten above and
	// needs nothing from the context; leaving the field visible would let the
	// raw Go-template form reach it — `{{ index .Args 0 }}` — and interpolate a
	// caller-controlled string straight into the program text, which is the
	// exact hole the "$@" transport exists to close.
	script, err = tpl.RenderCommand(src, withoutArgs(rc))
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
	// Same reasoning as RenderShellCommand: the splice below is the only
	// sanctioned way arguments enter an argv, so the per-element render must not
	// be able to reach them through `{{ .Args }}`. No shell re-parses an argv
	// entry, so this is an argument-boundary concern rather than an injection
	// one, but the single sanctioned path is worth keeping single.
	sanitized := withoutArgs(rc)

	out := make([]string, 0, len(argv)+len(args))
	for i, arg := range argv {
		if arg == model.ArgsToken {
			out = append(out, args...)
			continue
		}
		rendered, err := tpl.RenderCommand(arg, sanitized)
		if err != nil {
			return nil, fmt.Errorf("render argv[%d]: %w", i, err)
		}
		out = append(out, rendered)
	}
	return out, nil
}

// RenderArgvAppendFrom renders an argv_append_from expression into the host
// shell program text it will be executed as.
//
// It exists so both runners reach the same entry point: the expression is
// rendered with the caller's arguments hidden, exactly like `cmd:` and every
// argv element. ${args} is rewritten to "$@" and passed as positional
// parameters everywhere else, so leaving Args visible here would make this one
// field interpolate caller bytes into program text while no other field does.
// A literal ${args} in the expression is rejected at load time
// (CommandDef.Validate); this is the belt to that braces, and covers the raw
// Go-template form `{{ index .Args 0 }}` which the load-time check cannot see.
//
// withoutArgs stays unexported deliberately: the point is one safe entry
// point, not a second primitive each runner can wire up its own way.
func RenderArgvAppendFrom(expr string, rc *tpl.RenderContext) (string, error) {
	script, err := tpl.RenderCommand(expr, withoutArgs(rc))
	if err != nil {
		return "", fmt.Errorf("render argv_append_from: %w", err)
	}
	return script, nil
}

// AppendArgvFrom executes the command's argv_append_from expression on the host
// and returns argv with one appended element per output line.
//
// Execution details, all deliberate:
//   - the expression runs on the HOST via config.ShellBin even for a
//     service_exec command — it computes the argument list, it is not part of
//     the work done in the container;
//   - cwd is the project root (not cmd.Workdir, which for a service command
//     names a path inside the container), matching condition.EvalCmd;
//   - stdout is captured as DATA — split on newlines, never re-parsed as shell,
//     so a filename containing spaces, quotes or `$(…)` stays one argv element;
//   - stderr streams to the user so a failing expression explains itself;
//   - stdin is left unwired: the expression must not consume the user's input.
//
// An expression producing no items returns spec.ErrArgvAppendEmpty — see that
// sentinel for the full skip contract.
func AppendArgvFrom(ctx context.Context, rc spec.RunContext, argv []string) ([]string, error) {
	if rc.Cmd == nil || rc.Cmd.ArgvAppendFrom == "" {
		return argv, nil
	}

	script, err := RenderArgvAppendFrom(rc.Cmd.ArgvAppendFrom, rc.Render)
	if err != nil {
		return nil, err
	}

	c := exec.CommandContext(ctx, config.ShellBin(rc.Config), "-c", script) //nolint:gosec
	BindCancel(c)
	if rc.ProjectRoot != "" {
		c.Dir = rc.ProjectRoot
	}
	var stdout bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = StderrOf(rc)

	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("argv_append_from %q: %w", rc.Cmd.ArgvAppendFrom, err)
	}

	items := splitArgvAppendLines(stdout.String())
	if len(items) == 0 {
		return nil, spec.ErrArgvAppendEmpty
	}

	// Appended items come last, after the declared argv with ${args} already
	// spliced in place. Pinned by test: `argv: [ruff, check, ${args}]` +
	// `-- --fix` + two changed files runs `ruff check --fix a.py b.py`.
	out := make([]string, 0, len(argv)+len(items))
	out = append(out, argv...)
	return append(out, items...), nil
}

// splitArgvAppendLines turns captured stdout into argv elements, one per line.
//
// A trailing newline is ignored rather than producing a final empty element,
// and empty lines are dropped: no argument the field is meant to carry is the
// empty string, while a stray `""` in an argv can silently change what a tool
// does. Lines are otherwise taken byte-for-byte — no trimming — because
// leading/trailing spaces are legal in a path.
func splitArgvAppendLines(out string) []string {
	var items []string
	for line := range strings.SplitSeq(out, "\n") {
		if line == "" {
			continue
		}
		items = append(items, line)
	}
	return items
}

// renderArgsOf is the nil-safe accessor for the pass-through arguments.
func renderArgsOf(rc *tpl.RenderContext) []string {
	if rc == nil {
		return nil
	}
	return rc.Args
}

// withoutArgs returns a shallow copy of rc with Args cleared, so a template
// rendered with it cannot interpolate caller-supplied arguments into the result.
// The copy is shallow on purpose: every other field is read-only during render.
func withoutArgs(rc *tpl.RenderContext) *tpl.RenderContext {
	if rc == nil {
		return nil
	}
	sanitized := *rc
	sanitized.Args = nil
	return &sanitized
}
