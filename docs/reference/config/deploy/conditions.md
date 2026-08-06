# Conditions and checks

`when:`, `check:`, and `files_gate:` are the three directives that gate pipeline steps and phases. They use the typed condition/action shape — see [Conditions and Actions](../conditions.md) for the full predicate and action catalogue; this page covers the deploy-pipeline integration semantics.

## Contents

- [`when:` (pre-condition)](#when-pre-condition)
- [`check:` (post-condition)](#check-post-condition)
- [`check: auto` (the inverse of `when:`)](#check-auto-the-inverse-of-when)
- [`files_gate:` (pre-condition for files)](#files_gate-pre-condition-for-files)

## `when:` (pre-condition)

`when:` is a **pre-condition** evaluated before a phase or step runs. A falsy result skips the phase/step without executing it. It is a **typed condition** with three forms:

**Builtin predicates** — test filesystem state using the predicate registry:

```yaml
when:
  type: builtin
  cmd: "dir-empty services/main/src"
```

Available predicates: `dir-exists`, `dir-missing`, `dir-empty`, `dir-not-empty`, `file-exists`, `file-missing`, `generated-missing` (takes `<svc> <field>` not a path). These are distinct from the *engine builtins* (`service_configs_render`, etc.) used in step bodies and `check:` actions; see [conditions.md](../conditions.md) for the full distinction. The predicate registry uses hardcoded `sh -c` for POSIX portability regardless of the project's configured shell.

**Shell commands** — execute a shell command; exit 0 = true, non-zero = false:

```yaml
when:
  type: shell
  cmd: "test -f services/main/src/vendor/autoload.php"
```

Shell commands also use hardcoded `sh -c` (not `ShellBin`) for portability.

**Template expressions** — Go template syntax evaluated at plan time against the merged `DweConfig`:

```yaml
when:
  type: template
  expr: "{{ .Services.second.Enabled }}"
```

Template conditions do not support `check:` in the same step (no side effects at plan time). They are purely for idempotency checks like "skip this phase if the feature is not enabled" where the result is known before execution. See [Templates](../../templates.md) for the full template surface (helpers, sprout registries, `appURL`).

## `check:` (post-condition)

`check:` is a **post-action** evaluated after a step succeeds. It is a **typed action** — the same `type:` / `cmd:` / `with:` shape as step bodies, but its success/failure determines whether the step is reported as passed or failed.

Use `check:` to assert that a step had its intended effect — e.g. that a migration produced a certain file, that a service became reachable, or that configs were rendered successfully.

**Example: verify configs were deployed**

```yaml
- name: render-configs
  type: builtin
  cmd: service_configs_render
  with:
    service: main
  check:
    type: builtin
    cmd: service_configs_render_check
    with:
      service: main
```

**Example: verify a shell command produces expected output**

```yaml
- name: run-migration
  type: command
  cmd: services.main.migrate
  check:
    type: shell
    cmd: "test -f services/main/src/migrations/.done"
```

**Behavior of `continue_on_error` with `check:`:**

- When a step body fails and `continue_on_error: true` is set, `check:` is **not** evaluated. The step is reported as failed and the pipeline continues.
- When a step body succeeds but `check:` fails, the step is reported as failed. If `continue_on_error: true`, the pipeline continues; otherwise it aborts.

## `check: auto` (the inverse of `when:`)

`check: auto` is the one **scalar** form `check:` accepts. It resolves to the logical inverse of the step's own `when:` — "the step is done when the condition that asked for it no longer holds":

```yaml
- name: clone-source
  type: shell
  cmd: "git clone ${vars.source.repo} services/backend/src"
  when:
    type: shell
    cmd: "[ ! -e services/backend/src/.git ]"
  check: auto
```

which is exactly equivalent to writing the negation out by hand:

```yaml
  check:
    type: builtin
    cmd: shell
    with:
      cmd: "test -e services/backend/src/.git"
```

**Opt-in, never inferred.** `when:` ("should this run now") and `check:` ("is this already done") coincide often but are not the same question, so DWE never derives one from the other silently — a step with a `when:` and deliberately no `check:` keeps behaving exactly as before.

**Only `when: {type: shell}`.** The other two condition kinds are rejected at load time, each for its own reason:

| Form | Load-time result |
|---|---|
| `check: auto` with no `when:` | rejected — there is nothing to invert |
| `check: auto` with `when: {type: builtin}` | rejected — the predicate registry and the `check:` builtin registry are [disjoint](../conditions.md#two-type-builtin-registries); there is no action that expresses "NOT `dir-empty foo`" |
| `check: auto` with `when: {type: template}` | rejected — template conditions are evaluated at plan time and the step is dropped when false, so any step that reaches execution had `when == true` and its inverse would *always* fail |

Only the exact scalar `auto` is accepted; `Auto`, `AUTO` and `"auto "` keep the ordinary "action must be a mapping" error.

**How it resolves.** The inverse is built at plan-resolution time from the **rendered** `when:` command — the very string the runtime evaluation will see, so the pair can never disagree about what the command is — and it is wrapped across newlines (`! (\n<cmd>\n)`), not inline: an inline `! ( <cmd> )` turns a `when:` with a trailing `# comment` into a syntax error instead of an inversion.

The derived check is a `{type: builtin, cmd: shell}` action, which means it runs under hardcoded `sh -c` in the **project root** — the same shell and the same working directory `when:` uses — and it is **unbounded** (`timeout: "0"`), matching the `when:` it inverts rather than the `shell` builtin's 10s default.

**Plan output** reports what you wrote, not the machinery: `dwe deploy plan` prints `[check: auto (inverse of when)]`.

**Journal behaviour is identical to an explicit check.** The sentinel exists from load time onward, so `check: auto` forces the step to re-run on every deploy exactly as any other `check:` does. One migration note: the raw `check:` value participates in the project/service config hash, so switching a step from a hand-written inverse to `check: auto` shifts that hash and causes a **one-time re-run** of the service's steps.

## `files_gate:` (pre-condition for files)

`files_gate:` probes for the **existence or absence** of files before running a step. Unlike `when:` (which is a generic predicate) or `check:` (which validates after success), `files_gate:` decides whether to run based on **the same `files:` block declared in a command definition** — making the command's file spec the single source of truth.

**Use case:** skip a deployment step if the artifact already exists, or run it only when a pre-fetched cache is present. Example: "dump the database only if a dump file doesn't already exist" (producer step with `state: missing`), or "load the cache only if it was pre-fetched" (consumer step with `state: readable`).

**Short form:**

```yaml
- name: db-dump
  type: command
  cmd: services.main.db.dump
  files_gate: readable              # runs iff dump file exists
```

**Long form:**

```yaml
- name: db-load
  type: command
  cmd: services.main.db.load
  files_gate:
    state: missing                  # required: runs iff dump file does NOT exist
    command: services.main.db.dump  # optional: target command (default: step.cmd)
    require: all                    # optional: which files to probe (default: required)
    with:                           # optional: params for file resolution (default: step.with)
      database: test_db
```

**Field reference:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `state` | `readable` \| `missing` | (required) | `readable`: runs iff **all** selected files resolve (file exists). `missing`: runs iff **none** do. |
| `command` | string | `step.cmd` | Target command ID whose `files:` block is probed. When not specified, the step's own `cmd` is used (self-probe). |
| `require` | string \| list | `required` | Which files participate in the probe: `required` (files marked `required: true` or `read_write`), `all` (all readable files), or explicit list `[id1, id2]`. |
| `with` | mapping | `step.with` | Parameter overrides for file resolution templates. Merged with `step.with` for the targeted command. |

**Semantics:**

- **No file errors → skip**, not fail. If `state: readable` and no files match, the step is skipped (not failed). Configuration errors (bad template, bad glob, missing params) do produce an error and fail the step.
- **AND'ed with `when:`** — both must be satisfied for the step to run. If `when:` is false, the gate is never evaluated (short-circuits). If `when:` is true but the gate is unsatisfied, the step is skipped.
- **Journal-skip interaction (asymmetric by `state:`)** — the gate's interaction with the journal "already deployed" skip optimization depends on `state:`:
  - `state: missing` (producer pattern) **bypasses journal-skip**. The gate alone decides whether the step runs, every deploy. A producer step with `state: missing` re-runs after its artifact is deleted between deploys, because filesystem state — not the journal — is the source of truth.
  - `state: readable` (consumer pattern) **respects journal-skip**. The journal is consulted first; if it recorded a successful run, the step is skipped without probing the gate. The gate effectively fires only on the first run, after which the journal carries the load. This keeps destructive consumers (e.g. drop + restore) idempotent by default. To force re-evaluation on every run, add an explicit `check:` directive — the same lever used by any other step.
  - Gateless steps are journal-skipped as before.

  Adding or changing a `files_gate:` directive invalidates the recorded step hash, so the next run re-evaluates from scratch regardless of `state:`.
- **Probe scope** — only files with `access: read` or `access: read_write` participate. Files with `access: write` only are rejected at plan-time validation if listed in the gate's `require:` spec.

**Before and after example:**

*Without `files_gate:` — duplicated glob+regex logic:*

```yaml
# Deploy step: hard-coded shell condition duplicating the command's file logic
- name: dump-download
  type: command
  cmd: services.main.db.dump-download
  when:
    type: shell
    cmd: "test -f services/main/.backups/dump_*.sql.gz"  # duplicated glob logic
```

*With `files_gate:` — single source of truth:*

```yaml
# Deploy step: references the command's canonical file spec
- name: dump-download
  type: command
  cmd: services.main.db.dump-download
  files_gate: readable                # probes the dump_*.sql.gz from command definition
```

The command definition once:

```yaml
# workspace/commands/services/main/db.yml
commands:
  dump-download:
    type: shell
    files:
      dump:
        access: read
        candidates:
          - glob: "services/main/.backups/dump_*.sql.gz"
            sort: modtime_desc
        required: true
```
