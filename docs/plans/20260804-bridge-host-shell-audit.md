# Audit — host shell surfaces reachable from a container via the bridge

## Overview

`tpl.EvalCommandCondition` renders an expression and then, for a `cmd:` condition, executes
`sh -c` **on the host**. That evaluator backs `hide:` on commands and `when:` on workflow
steps. Because `commands` is bridge-allowlisted and a command can opt into container
reachability via `bridge: {enabled: true}`, a bridged command can route
container-influenced values — `${param.*}` from the caller, or `${vars.*}` paths made
writable by `bridge.vars_writable` — into the **text** of a host shell program. The daemon's
environment hardening does not help, because the injection is in program text rather than in
the environment.

This was found while reviewing `20260804-pipeline-primitives.md` (Plan B). That plan adds a
rule forbidding `argv_append_from` together with `bridge.enabled: true`; the rule is kept
because it prevents *widening* the surface, but it does not close the class, and it must not
be mistaken for a fix. **This is a pre-existing property of `main`, not a regression
introduced by any of the three agent-ergonomics plans.**

The deliverable is a decision plus whatever enforcement follows from it — not a predetermined
set of checks. Guessing the enforcement shape before settling the trust boundary produces
either security theatre or a rule that blocks legitimate authoring.

## Context (from discovery)

- `internal/shared/tpl/render_command.go:402-427` — `EvalCommandCondition` calls
  `RenderCommand` (substituting `${vars.*}`, `${param.*}`, `${context.*}`), then
  `condition.Classify`; for `condition.KindCmd` it calls `condition.EvalCmd(payload,
  projectRoot)`, which runs `sh -c` on the host.
- Consumers of that evaluator: `hide:` on a command definition, and `when:` on a workflow
  step.
- `internal/cli/bridgepolicy.go:30` — `commands` is in `bridgeAllowedTopLevel`. Per-command
  opt-in is `bridge: {enabled: true}`, resolvable directly, from the command file's `group:`
  header, or through a service-level `extends:` chain (`registry.ApplyVisibility`,
  `applyBridgeVisibility`).
- `internal/core/usercommands/model/types.go:579-582` — `ParamDef.Pattern` is optional; an
  unconstrained param is a free-form caller string.
- `bridge.vars_writable` (`config.BridgeVarsWritable`, `config.VarsWritableAllows`) is
  deny-by-default but deliberately lets a container write selected `${vars.*}` via
  `dwe vars set`.
- Already neutralized, and the precedent for what "closed" looks like: `${args}` is rewritten
  to `"$@"` before rendering so caller bytes never enter program text (commits `2a1d6b73`,
  `0429a6e9`).
- Measured across the five surveyed workspaces (podlapka, AlbFetcharr, beetDeck, alto,
  cueBreaker): **zero** commands use a `cmd:` condition in `hide:` or workflow `when:`, and
  **zero** services or commands declare a `bridge:` block at all.

## Critical constraints for the executor (traps — read before every task)

1. **Answer the trust-boundary question before writing any check.** The enforcement shape
   follows from it; the reverse order produces rules nobody can justify later.
2. **The surface is dormant, not broken.** Nothing in any workspace exercises it today, so
   there is no incident to race. Prefer the answer that is right long-term over the one that
   ships fastest.
3. **Do not re-open Plan B's rule.** `argv_append_from` + `bridge.enabled` stays rejected
   regardless of what this task concludes; it is "do not widen", not "this is safe now".
4. **Runtime-gating has precedent**: `vars set` enforces `bridge.vars_writable` at the call
   site via `bridgeclient.InContainer()`, precisely because the prefix-wide command
   allowlist cannot distinguish read from write. A runtime check is a legitimate answer here
   too, not a lesser one.
5. **`make build` / `make test`, never bare `go test ./...`** (generated embedded docs).
6. **`AGENTS.md` § "Host bridge env contract & container command policy"** is the
   authoritative description of the existing boundary and must end up consistent with
   whatever is decided.

## Development Approach

- **testing approach**: **TDD** for any enforcement that lands — this is diagnostic/rejection
  behaviour, and the fixture-first order is what proves the check fires on the bad shape.
  Task 1 is an inventory and carries no tests by nature.
- complete each task fully before moving to the next
- **CRITICAL: update this plan file when the decision in Task 2 changes the scope** — Tasks 3+
  are written conditionally on purpose and must be rewritten once the boundary is settled
- run tests after each change

## Testing Strategy

- **unit tests**: required for every task that changes code
- **e2e tests**: none in this project; the bridge has its own test seams
  (`ensureDaemonFn`/`stopDaemonFn`/`probeDaemonFn`) that must be stubbed — production spawns
  via `os.Executable()`, which would re-exec the test binary
- table-driven tests for the rejection matrix (surface × input origin)

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- keep this plan in sync with the decision recorded in Task 2

## Solution Overview

Three stages. **Inventory** every path by which a container-invocable command can reach a
host shell, and which of its inputs are genuinely container-controlled. **Decide** the trust
boundary between two coherent positions:

- **(A) A bridged command is trusted to run host shell.** Then the surfaces are working as
  intended, the documentation must say so without euphemism, and the enforcement work is
  limited to making that explicit at authoring time.
- **(B) Container-controlled bytes must never become host shell program text.** Then several
  surfaces need enforcement, and the `${args}` → `"$@"` treatment is the template for what
  "closed" means.

**Enforce** according to the decision, then align `AGENTS.md` and the reference docs.

## Technical Details

**Inventory axes** (Task 1). For each surface: does it reach `condition.EvalCmd` or another
host `sh -c`; is it reachable when the command is bridged; which template namespaces can
appear in it; and which of those are container-controlled — `${param.*}` always,
`${vars.*}` only for paths matched by `bridge.vars_writable`, `${context.*}` derived from
config, `${args}` already neutralized.

**Candidate surfaces to confirm or dismiss**: `hide:` conditions; workflow step `when:`;
`type: shell` command bodies; `type: dwe` bodies; `files:` path expressions; anything else
routing `RenderCommand` output into `condition.EvalCmd`.

**Enforcement options** (Task 3, conditional on Task 2): load-time rejection in a
registry/config-aware pass, as Plan B does; a runtime gate active only under
`bridgeclient.InContainer()`, mirroring `vars set`; a validator diagnostic; or documentation
only. The options are not exclusive — a runtime gate plus a validator warning is a plausible
pairing.

## What Goes Where

- **Implementation Steps** (`[ ]`): the audit, the decision record, any enforcement, tests
  and documentation in this repository.
- **Post-Completion** (no checkboxes): nothing to verify in external systems — no workspace
  currently uses the surface.

## Implementation Steps

### Task 1: Inventory the host-shell surfaces and their container-controlled inputs

**Files:**
- Create: a findings section appended to this plan (no code changes)

- [ ] enumerate every path from a container-invocable command to a host `sh -c`, starting
      from `condition.EvalCmd` callers and `RenderCommand` consumers
- [ ] for each, record whether it is reachable while `bridge.enabled: true` and which
      template namespaces it admits
- [ ] classify each namespace by who controls it (caller/container, config author, engine)
- [ ] record which surfaces are already neutralized and how (the `${args}` → `"$@"` case is
      the reference)
- [ ] write the findings into this plan file before proceeding — Task 2 is a decision about
      this table, and an unwritten table cannot be reviewed
- [ ] no tests (inventory task)

### Task 2: Decide and record the trust boundary

**Files:**
- Modify: this plan (decision record)
- Modify: `AGENTS.md` (§ Host bridge env contract & container command policy)

- [ ] choose between position (A) and position (B) from the Solution Overview, in writing,
      with the reasoning
- [ ] state explicitly what a workspace author is allowed to assume — this is the sentence
      future work will be measured against
- [ ] update the `AGENTS.md` section so the documented boundary matches the decision
- [ ] rewrite Tasks 3–4 below to match; they are placeholders until this lands
- [ ] no tests (decision task)

### Task 3: Implement the enforcement the decision calls for

**Files:**
- *(determined by Task 2 — likely `internal/core/usercommands/registry/`,
  `internal/core/validate/commands/`, or the `bridgeclient.InContainer()` call sites)*

- [ ] (TDD) write the fixture and test for each shape that must now be rejected or gated,
      before the implementation
- [ ] implement the check at the layer the decision implies, reusing the existing precedent
      rather than inventing a parallel mechanism
- [ ] ensure the message names the trust boundary and the specific input, so an author can
      act on it
- [ ] write tests for the shapes that must stay **allowed** — an over-broad rule here blocks
      legitimate authoring and is the main risk of this task
- [ ] run tests — must pass before task 4

### Task 4: Verify acceptance criteria

- [ ] verify the inventory from Task 1 is fully addressed by the decision and enforcement
- [ ] confirm the five surveyed workspaces still load and validate unchanged (they use none
      of these constructs, so any diagnostic firing there is a false positive)
- [ ] confirm Plan B's `argv_append_from` rule and this work do not contradict each other
- [ ] run full test suite: `make test`
- [ ] run `make lint`

### Task 5: [Final] Update documentation

- [ ] document the boundary in the bridge reference page and in the commands reference
      (`hide:`, workflow `when:`), stating plainly what a bridged command may do on the host
- [ ] update the Russian mirrors under `docs/i18n/ru/`
- [ ] run `make build` to resync embedded docs and content hashes
- [ ] update `skills/dwe/` if the conclusion changes what an agent may author
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Nothing to migrate.** No workspace declares a `bridge:` block or uses a `cmd:` condition
today, so no existing project changes behaviour regardless of the outcome. That is precisely
the argument for settling this now rather than after the first opt-in, when the same decision
would become a compatibility question.

**Related work**: `docs/plans/20260804-pipeline-primitives.md` (Plan B) — its
`argv_append_from` × `bridge.enabled` rejection stays in force independently of this outcome.
