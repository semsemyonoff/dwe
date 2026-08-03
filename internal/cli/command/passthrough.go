package command

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
)

// commandIDArgs validates the positional arguments of `dwe commands`.
//
// Only the arguments before `--` are the command's own — at most the id. cobra
// records the dash position in ArgsLenAtDash(); everything at or after it is
// the caller's pass-through payload and is checked per-command later, once the
// definition is known. ArgsLenAtDash() returns -1 when no `--` was written, in
// which case every argument is a near-side one and the old at-most-one rule
// applies unchanged.
func commandIDArgs(cmd *cobra.Command, args []string) error {
	near := len(args)
	if d := cmd.ArgsLenAtDash(); d >= 0 {
		near = d
	}
	if near > 1 {
		// The suggestion keeps everything after the id, including anything the
		// caller already put past a real `--`: args[1:] is args[1:near] followed
		// by args[near:], so copying the suggested line never silently drops the
		// arguments that were already in the right place. With no dash present
		// near == len(args) and this is exactly args[1:near].
		return cmdctx.Err("usage_error", fmt.Sprintf(
			"expected one command id, got %d arguments (%s)\n\n"+
				"To pass arguments through to the command, put them after `--`:\n"+
				"  dwe cmd %s -- %s",
			near, strings.Join(args[:near], " "), args[0], strings.Join(args[1:], " "),
		))
	}
	return nil
}

// passThroughArgs returns the arguments the caller wrote after `--`.
func passThroughArgs(cmd *cobra.Command, args []string) []string {
	d := cmd.ArgsLenAtDash()
	if d < 0 || d >= len(args) {
		return nil
	}
	return args[d:]
}

// nearArgs returns the arguments before `--` — the ones `dwe commands` itself
// interprets (the id or group filter).
func nearArgs(cmd *cobra.Command, args []string) []string {
	d := cmd.ArgsLenAtDash()
	if d < 0 || d > len(args) {
		return args
	}
	return args[:d]
}

// checkPassThroughArgs rejects `dwe cmd <id> -- <args>` for a command that has
// nowhere to put them, replacing cobra's stock "Accepts at most 1 arg(s),
// received 3" — a message in the framework's terms that names neither the
// command, the reason, nor a way forward.
//
// Extra arguments are opt-in per command precisely because there is no safe
// default placement (see model.CommandDef.ReferencesArgs), so the error's job is
// to point at the one-line config change that grants them, and at `dwe shell`
// for the case where editing the command is not what the caller wanted.
func checkPassThroughArgs(def *model.CommandDef, opts runOpts) error {
	if len(opts.PassThroughArgs) == 0 {
		return nil
	}
	if def.ReferencesArgs() {
		return nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "command %q does not accept extra arguments", def.ID)
	if def.Args != nil {
		// An args: block with no ${args} reference is inert — say so plainly,
		// since the author clearly intended the command to take arguments.
		b.WriteString("\n\nIt declares an `args:` block, but neither `cmd:` nor `argv:` " +
			"references ${args}, so there is nowhere to substitute them.")
	}
	// Only suggest a slot the command's type actually accepts — allowedFieldsFor
	// gates cmd/argv per type, and telling the author of a `script` or
	// `workflow` command to add `cmd:` would hand them a definition the strict
	// decoder rejects outright.
	switch {
	case len(def.Argv) > 0:
		b.WriteString("\n\nTo let it take arguments, reference ${args} in its definition:")
		b.WriteString("\n  argv: [..., \"${args}\"]      # spliced as separate arguments")
	case def.Cmd != "":
		b.WriteString("\n\nTo let it take arguments, reference ${args} in its definition:")
		b.WriteString("\n  cmd: \"" + firstWords(def.Cmd) + " ${args}\"")
	default:
		// No cmd/argv to substitute into — a script, workflow or daemon command.
		// Pass-through has nowhere to land regardless of what the author writes.
		fmt.Fprintf(&b, "\n\nA %s command has no `cmd:`/`argv:` to substitute ${args} into, "+
			"so it cannot take pass-through arguments. Use its params instead, "+
			"or call the underlying command directly.", def.Type)
	}
	fmt.Fprintf(&b, "\n\nSee its current definition with:  dwe cmd -i %s", def.ID)
	if svc := def.EffectiveService(); svc != "" {
		fmt.Fprintf(&b, "\nOr run a one-off command instead:  dwe shell %s -c '<command>'", svc)
	}
	if len(def.Params) > 0 {
		fmt.Fprintf(&b, "\n\nIt does take these params (pass with --set key=value): %s",
			strings.Join(paramNames(def), ", "))
	}
	return cmdctx.Err("command_args_unsupported", b.String()).WithDetail("id", def.ID)
}

// firstWords trims a cmd string to its opening words so the suggestion stays a
// single readable line even when the command is a long shell pipeline.
func firstWords(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "<command>"
	}
	if i := strings.IndexAny(cmd, "\n|;&"); i >= 0 {
		cmd = strings.TrimSpace(cmd[:i])
	}
	// Truncate by runes, not bytes — a byte slice through a multi-byte
	// character would emit a replacement char into the suggestion.
	const maxRunes = 40
	if r := []rune(cmd); len(r) > maxRunes {
		return string(r[:maxRunes]) + "…"
	}
	return cmd
}

// paramNames lists a command's declared param names in definition order.
func paramNames(def *model.CommandDef) []string {
	names := make([]string, 0, len(def.Params))
	for name := range def.Params {
		names = append(names, name)
	}
	slices.Sort(names) // map iteration is randomized; keep the message stable
	return names
}
