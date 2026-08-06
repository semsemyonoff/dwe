package pipeline

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// autoCheckTimeout is the timeout param of a derived check. "0" is unbounded
// (see builtin.Shell.Run): the `when:` a derived check inverts has no timeout
// at all, so capping the inverse at the shell builtin's 10s default would
// silently bound something that was unbounded.
const autoCheckTimeout = "0"

// ResolveAutoCheck builds the real check action for a step whose `check:` is
// the config.AutoCheckType sentinel. when must be the step's *rendered* runtime
// condition — the same value the executor will evaluate — so the check and the
// condition it inverts can never disagree about what the command is.
//
// The derived form is deliberately `{type: builtin, cmd: shell}` rather than
// `{type: shell}`: the builtin gets a hard `sh -c`, matching condition.EvalCmd,
// while a type: shell action would run under the project's overridable
// ShellBin (a project on fish would break). The builtin's cwd is the project
// root, which condition.EvalCmd also uses.
//
// Both error paths are unreachable through the loader — validateAutoCheck
// rejects an auto check without a when:, or with a non-shell one — but callers
// outside it (dwe reset step) route through here too, so they are real returns
// rather than panics.
func ResolveAutoCheck(when *condition.Condition) (*config.Action, error) {
	if when == nil {
		return nil, fmt.Errorf("check: auto has no when: to invert")
	}
	if when.Type != condition.TypeShell {
		return nil, fmt.Errorf("check: auto requires when: {type: shell}, got %q", when.Type)
	}
	return &config.Action{
		Type: "builtin",
		Cmd:  "shell",
		With: map[string]any{
			"cmd":     InvertShellCommand(when.Cmd),
			"timeout": autoCheckTimeout,
		},
	}, nil
}

// InvertShellCommand wraps cmd in a POSIX logical negation.
//
// The wrapping spans newlines on purpose. An inline `! ( cmd )` breaks on any
// command whose last line is not self-terminating — a trailing `# comment`
// swallows the closing paren and turns the inversion into a syntax error
// (which the check reports as failure, not as the inverse), and the same goes
// for a trailing `&` or an unterminated heredoc.
func InvertShellCommand(cmd string) string {
	return "! (\n" + cmd + "\n)"
}
