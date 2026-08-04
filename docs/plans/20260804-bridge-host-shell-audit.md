# Audit — host shell surfaces reachable from a container via the bridge

## Overview

While reviewing Plan B (`20260804-pipeline-primitives.md`) an adversarial review found that
the security rule that plan adds — "a command may not declare both `bridge.enabled: true`
and `argv_append_from`" — does **not** close the class it appears to close. Equivalent
host-shell paths are already reachable from a container on `main`, independently of any of
the three plans.

**This is a pre-existing property of the product, not a regression introduced by that
work.** It is filed separately so the narrow rule in Plan B is not mistaken for a fix.

The concrete mechanism (verified in code):

- `tpl.EvalCommandCondition` (`internal/shared/tpl/render_command.go:402-427`) first renders
  the expression through `RenderCommand` — substituting `${vars.*}`, `${param.*}`,
  `${context.*}` — and then classifies the result. For `condition.KindCmd` it calls
  `condition.EvalCmd(payload, projectRoot)`, which executes `sh -c` **on the host**.
- That evaluator backs at least two author-facing surfaces: `hide:` on a command, and
  `when:` on a workflow step.
- `commands` is in `bridgeAllowedTopLevel` (`internal/cli/bridgepolicy.go:30`), and a
  command becomes container-reachable via `bridge: {enabled: true}` (directly, via the
  file's `group:` header, or through a service-level `extends:` chain).
- Container-influenced inputs exist: `${param.*}` comes from the caller and
  `ParamDef.Pattern` is optional (`usercommands/model/types.go:579-582`), while
  `bridge.vars_writable` deliberately lets a container write selected `${vars.*}` through
  `dwe vars set`.

Composing those: a bridged command whose `hide:` is `"cmd: …${vars.x}…"`, or a bridged
workflow step whose `when:` is `"cmd: …${param.x}…"`, routes container-influenced bytes into
the *text* of a host shell program. The daemon's environment hardening (`LD_*`, `PATH`,
`HOME`, the host-identity set) does not help, because the injection is in program text, not
in the environment.

## Why this is not obviously a vulnerability

Stated plainly, because it changes how urgent this is:

- The command author writes the `hide:` / `when:` expression. A workspace that never
  references `${param.*}` or a bridge-writable var in a `cmd:` condition is unaffected.
- `bridge.enabled` is opt-in per command and defaults to off for every service type.
- `bridge.vars_writable` is deny-by-default; without it, container-written vars do not
  exist at all.
- Checked across the five surveyed workspaces (podlapka, AlbFetcharr, beetDeck, alto,
  cueBreaker): **zero** commands use a `cmd:` condition in `hide:` or in a workflow `when:`,
  and **zero** services or commands declare a `bridge:` block at all. The whole surface is
  therefore dormant in every real project today — which is exactly why this is worth
  settling deliberately now, before the first workspace opts in and the answer becomes a
  compatibility question.

So this is a latent sharp edge in the authoring surface rather than an exploitable default.
The decision to make is whether it should be blocked, narrowed, or documented.

## Questions this task must answer

1. **What is the intended trust boundary?** Either (a) a bridged command is trusted to run
   host shell — in which case Plan B's rule is a consistency wart and the docs must say so
   plainly; or (b) container-controlled bytes must never become host shell program text — in
   which case several surfaces need validation, not just `argv_append_from`.
2. **Full inventory of surfaces** that reach `sh -c` on the host from a container-invocable
   command: `hide:` conditions, workflow step `when:`, `type: shell` command bodies,
   `type: dwe` bodies, `files:` path expressions, and anything else `EvalCommandCondition`
   or `RenderCommand` feeds into `condition.EvalCmd`.
3. **Which inputs are genuinely container-controlled** per surface: `${param.*}` (always),
   `${vars.*}` (only paths matched by `bridge.vars_writable`), `${context.*}`,
   `${args}` (already neutralized — rewritten to `"$@"`, see commits `2a1d6b73`, `0429a6e9`).
4. **What enforcement is proportionate**: a load-time rejection like Plan B's, a runtime
   check only when `bridgeclient.InContainer()`, a validator warning, or documentation only.
   Note the precedent already in the code — `vars set` enforces `bridge.vars_writable` at
   runtime rather than in the allowlist, precisely because the prefix-wide command allowlist
   cannot distinguish read from write.

## Scope

- **In scope**: the audit, the decision, and whatever enforcement follows from it.
- **Out of scope**: Plan B's `argv_append_from` rule, which stands on its own as
  "do not widen the surface" and is already written.

## Notes

- Do not start by adding checks. Start by answering question 1 — the enforcement shape
  follows from it, and guessing produces either security theatre or a rule that blocks
  legitimate authoring.
- `AGENTS.md` § "Host bridge env contract & container command policy" is the authoritative
  description of the existing boundary and should be updated with whatever this concludes.
