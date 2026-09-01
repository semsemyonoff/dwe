# Command types

The command types define different execution contexts. Each type has its own dispatch payload and field allowlist.

## Contents

- [Overview](#overview)
- [Type: shell](#type-shell)
- [Type: dwe](#type-dwe)
- [Type: script](#type-script)
- [Type: service_exec](#type-service_exec)
- [Type: service_run](#type-service_run)
- [Type: workflow](#type-workflow)
- [Type: builtin](#type-builtin)
- [Type: daemon](#type-daemon)
- [Workdir resolution](#workdir-resolution)

## Overview

| Type | Executor | Payload | Use case |
|------|----------|---------|----------|
| `shell` | Host shell | `cmd` or `argv` | Host tasks (scripts, git, build) |
| `dwe` | DWE CLI | `cmd` | Subcommand invocation |
| `script` | Script runner | `script:` block | Structured multi-phase execution |
| `service_exec` | Docker Compose exec/run | `cmd` or `argv` | Container operations on existing/new containers |
| `service_run` | Docker Compose run | `cmd` or `argv` | Throwaway container execution |
| `workflow` | Command orchestrator | `steps[]` | Multi-command sequences (separate syntax, see below) |
| `builtin` | Engine-internal action | `cmd` (builtin name) + `with` | Invoke a shared engine builtin (e.g. wait-for-healthy) without a subprocess |
| `daemon` | Registry sugar | `daemon:` block + `service` + `argv` | Declare a long-running background container; expands to four virtual commands (`.start` / `.logs` / `.stop` / `.restart`) |

All types except `script` and `workflow` use the canonical `cmd:` field for their payload. `type: script` uses its own `script:` block with `run`, `plan`, `cleanup` phases. `type: workflow` uses its own `steps:` block with string-based `command:` / `confirm:` / `with:` / `when:` syntax — see [Type: workflow](#type-workflow) below. `type: builtin` puts the builtin name in `cmd:` and its parameters in `with:` — see [Type: builtin](#type-builtin) below.

## Type: shell

Runs a shell command on the **host** machine. Use this for tasks that don't need a container or the DWE binary.

| Field | Required | Description |
|-------|----------|-------------|
| `cmd` | one of cmd/argv | Shell command string passed to `sh -c` (full shell semantics) |
| `argv` | one of cmd/argv | Argument vector executed directly without a shell |
| `argv_append_from` | optional | Host shell expression whose stdout lines are appended to `argv` as individual elements — see [Computed arguments](directives.md#computed-arguments-argv_append_from). Requires `argv`; empty output skips the command |
| `workdir` | optional | Working directory; relative paths resolve against project root |

```yaml
chmod-scripts:
  type: shell
  description: Make all scripts executable
  cmd: chmod +x workspace/scripts/**/*.sh
```

```yaml
commit-config:
  type: shell
  description: Commit a generated config file
  argv:
    - git
    - commit
    - -m
    - "chore: regen config"
    - "${files.cfg.path}"
  files:
    cfg:
      access: read
      path: "config/generated.yml"
      required: true
```

`cmd` and `argv` are mutually exclusive.

### Shell env contract

`type: shell` subprocesses inherit the parent process environment plus the values from the command's `env:` block. On top of that, the runner exports a small contract so shell snippets can talk back to the host DWE CLI and the active compose project without rediscovery:

| Variable | Value |
|----------|-------|
| `DWE_BIN` | Absolute path to the running dwe binary — use it instead of hardcoding `./bin/dwe` |
| `COMPOSE_PROJECT_NAME` | Active compose project name (e.g. `dwe-laravel`) — `docker compose ...` picks this up without `-p` |
| `COMPOSE_FILE` | Colon-joined list of active overlay paths, made absolute against the project root — `docker compose ...` picks this up without any `-f` flags |

This is what lets a `type: shell` command reach docker compose with the same overlay set the rest of DWE uses:

```yaml
hub.chown-src-host:
  type: shell
  description: Chown the host-side mount via the running container
  cmd: |
    "$DWE_BIN" docker exec -u root app-main -- \
      chown -R www-data:www-data /workspace/src
```

`COMPOSE_FILE` is omitted when no overlay files are configured; `COMPOSE_PROJECT_NAME` is omitted when no project name is set. Entries already declared in the command's `env:` block are kept but the contract entry wins when keys collide — Go's `os/exec` uses the last entry for duplicate keys, and the contract is appended after `env:`.

## Type: dwe

Invokes another dwe subcommand using the currently running binary. This avoids hard-coding `./bin/dwe` in command definitions and makes invocations relocatable.

| Field | Required | Description |
|-------|----------|-------------|
| `cmd` | yes | Subcommand string (without binary path); passed through `sh -c` |

```yaml
db.up:
  type: dwe
  private: true
  description: Start the database container in the background
  cmd: "docker up db"

app.install:
  type: dwe
  description: Install the Laravel application via installer container
  cmd: "compose raw --bare -- --progress tty -f compose/installer.yml run --rm -u ${host.uid}:${host.gid} app-install"
```

`workdir` is not allowed for `type: dwe` (the subcommand inherits the project root).

## Type: script

Executes a shell script file with a strict environment contract injected by the runner. **Scripts are the structured exception in the command system** — the `type: script` command uses its own `script:` block with `run`, `plan`, `cleanup` fields rather than the canonical `cmd:` field. This is intentional: scripts are configurations of a run, not individual commands.

```yaml
db.dump-create:
  type: script
  description: Create a database dump file
  params:
    database: { default_from: vars.db.database, pattern: ^[a-zA-Z0-9_-]+$ }
    dump_dir: { default_from: vars.db.backup_dir, required: true }
  files:
    dump:
      access: write
      path: "${param.dump_dir}/${param.database}_{{ now | date \"2006-01-02\" }}.sql.gz"
      mkdir: true
      overwrite: true
      on_error: remove
      env: DUMP_FILE
  env:
    DB_NAME: "${param.database}"
    MYSQL_PWD: "${vars.db.password}"
  script:
    path: workspace/scripts/db/dump-create.sh
    shell: bash
```

### Script block

| Field | Description |
|-------|-------------|
| `script.shell` | Interpreter to invoke (default `sh`) |
| `script.path` | Single script (simple mode) |
| `script.run` | Main script (phased mode) |
| `script.plan` | Optional pre-script (phased mode) |
| `script.cleanup` | Optional always-runs-after script (phased mode) |

Modes are mutually exclusive: either `path` alone, or `run` (with optional `plan` / `cleanup`).

### Script paths

Script paths in `script.path`, `script.run`, etc. are resolved **against the project root** — never against `workdir`. This makes scripts safe to keep under `workspace/scripts/` regardless of where the command is run from.

### Script contract environment

The runner always injects the following env vars into the script process:

| Variable | Description |
|----------|-------------|
| `DWE_ROOT` | Absolute project root |
| `DWE_BIN` | Absolute path to the running dwe binary |
| `DWE_COMMAND_ID` | Full ID of this invocation |
| `DWE_TEMP_DIR` | Writable temp dir scoped to this invocation (auto-removed) |
| `DWE_NONINTERACTIVE` | `1` when the parent `RunContext` has `NonInteractive: true` (set by `commands --yes` / `-y`) **or** the runner inherits `DWE_NONINTERACTIVE=1` from its own environment (e.g. nested invocations). Otherwise `0`. TTY detection alone does not flip this — scripts that need to behave differently on a non-TTY should test their own stdin. |
| `DWE_PARAMS_JSON` | Resolved params as a JSON object |
| `DWE_CONTEXT_JSON` | Resolved context as a JSON object |
| `DWE_FILES_JSON` | JSON object mapping file IDs to `{path}` |

Use `DWE_BIN` instead of hard-coding `./bin/dwe`:

```bash
#!/bin/bash
set -euo pipefail

TMPFILE=$(mktemp "${DUMP_FILE}.XXXXXX")
trap 'rm -f "$TMPFILE"' EXIT

"$DWE_BIN" docker exec -T -e MYSQL_PWD db -- \
  mariadb-dump -u"$DB_USER" "$DB_NAME" | gzip > "$TMPFILE"
mv "$TMPFILE" "$DUMP_FILE"
```

### Linting scripts

DWE itself does not lint shell scripts. We **recommend** installing [ShellCheck](https://github.com/koalaman/shellcheck) and running it over `workspace/scripts/` as part of your local workflow or CI. It is an external tool, completely optional, but catches the classes of bugs that hurt most in this context: unquoted expansions, missing `set -euo pipefail`, broken `trap` handlers, subtle quoting issues around `$DUMP_FILE` / `$DWE_BIN`, and mismatched test syntax.

```bash
# one-off check
shellcheck workspace/scripts/db/dump-create.sh

# whole tree
shellcheck workspace/scripts/**/*.sh
```

If you adopt it, pin the dialect with a shebang or directive so ShellCheck picks the right rules (especially when `script.shell: bash` is set):

```bash
#!/bin/bash
# shellcheck shell=bash
set -euo pipefail
```

Suggested conventions for scripts under `workspace/scripts/` (independent of ShellCheck — these are good ideas regardless):

- Start with `set -euo pipefail` — fail fast, no silent unset-var bugs.
- Quote every expansion: `"$DUMP_FILE"`, `"$DWE_BIN"`, `"$DB_NAME"`.
- Use `trap 'rm -f "$TMPFILE"' EXIT` for ephemeral files; the runner cleans `$DWE_TEMP_DIR` for you, but per-step temps still need their own traps.
- Treat unset env vars as errors via `${VAR:?error message}` when a script must not run without them.

## Type: service_exec

Runs a command inside an existing container via `docker compose exec`. The `mode:` field controls what happens when the target container is not running — see [mode resolution](#mode-resolution) below.

| Field | Required | Description |
|-------|----------|-------------|
| `service` | yes | Compose service name |
| `cmd` / `argv` | one of | Shell command string OR raw argv |
| `argv_append_from` | optional | Shell expression whose stdout lines are appended to `argv`. Runs on the **host**, not in the container — see [Computed arguments](directives.md#computed-arguments-argv_append_from) |
| `mode` | optional | `exec-or-fail` (default), `exec`, `run`, or `exec-or-run` — see [mode resolution](#mode-resolution) |
| `user` | optional | Container user to run as. See [User resolution](#user-resolution) for the full list of accepted values and the fallback rules. |
| `workdir` | optional | Container workdir; rendered with templates. Omitted → falls back to the target service's own workdir; `internal` opts out entirely. See [Workdir resolution](#workdir-resolution) |
| `workdir_from` | optional | Dot-path into merged config resolving to the workdir string |
| `compose_args` | optional | Extra flags forwarded to `docker compose exec/run` (templated) |

### Mode resolution

| Mode | When container is running | When container is not running |
|------|---------------------------|-------------------------------|
| `exec-or-fail` (default) | runs via `docker compose exec` | refuses with a clear DWE error suggesting `dwe docker up <svc>` |
| `exec` | runs via `docker compose exec` | calls `compose exec` anyway; docker emits its own (cryptic) error |
| `run` | always runs a fresh ephemeral container via `docker compose run --rm` | same |
| `exec-or-run` | runs via `docker compose exec` | silently falls back to `docker compose run --rm`; emits a yellow warning so the ephemeral-container behaviour is visible |

Pick `exec-or-fail` (the default) for normal interactive tools that depend on persistent container state (databases, application servers, etc.) — a missing container should surface as an actionable error, not a side-effecting one-off run. Pick `exec-or-run` only for tools that legitimately work as ephemeral runs (mc, composer install on a fresh checkout, etc.) and where you understand that no state will persist between invocations. `runner.mode` follows the same enum and same precedence rules as `runner.user`.

### User resolution

The `user:` field on `service_exec` / `service_run` (and on the `runner:` override block) accepts the following values:

| Value | Effect |
|-------|--------|
| _(omitted / empty)_ | Falls back to `services.<svc>.cli.user` of the target service. If `cli.user` is also empty, no `--user` flag is passed and the container runs under the image's `USER` directive. This is the default for new commands — declare `cli.user` once on the service and every command targeting that service inherits it. |
| `current` | Passes `--user <HOST_UID>:<HOST_GID>` so the container process runs as the host user. Use this when the command writes files into bind-mounted directories and you need them owned by the host user. |
| `root` | Passes `--user root`. Use for one-off operations that need elevated privileges inside the container (package install, chown, etc.). |
| `internal` | Passes **no** `--user` flag and **skips** the `cli.user` fallback. The container runs under the image's built-in `USER` directive (or `root` if the image declares none). Use this to explicitly opt out of `cli.user` for a specific command (e.g. an entrypoint that must run as the image's default user). |
| any other string | Passed verbatim as `--user <value>`. Accepts the same forms `docker --user` accepts: a user name (`www-data`), a numeric UID (`1000`), or `UID:GID` (`1000:1000`). |

Precedence, top to bottom:

1. `runner.user` (if the `runner:` block sets it).
2. Top-level `user:` on the command.
3. `services.<svc>.cli.user` of the resolved target service (after `runner.service` redirect).
4. No `--user` flag (image's `USER`).

`runner.service` redirects the target before the `cli.user` lookup, so the fallback reads `cli.user` from the **redirected** service, not the original.

Setting `user: internal` short-circuits step 3 — the resolver treats `internal` as an explicit decision and never reads `cli.user`.

```yaml
composer-install:
  type: service_exec
  description: Install PHP dependencies via Composer
  service: app-main
  user: current
  workdir_from: services.main.work_dir_internal
  mode: exec-or-run
  argv:
    - composer
    - install
    - --prefer-dist
    - --no-interaction
```

```yaml
db.create:
  type: service_exec
  description: Create a database in the db container
  service: db
  mode: exec-or-run
  params:
    database: { required: true, pattern: ^[a-zA-Z0-9_-]+$ }
  env:
    MYSQL_PWD: "${vars.db.password}"
  cmd: "mariadb -u${vars.db.user} -e 'CREATE DATABASE IF NOT EXISTS `${param.database}`;'"
```

### compose_args

`compose_args` is a list of extra flags inserted **before** the runner-generated `--user` / `--workdir` / `-e` flags. Use it for `-T`, `-d`, `--name`, `--rm`, etc.

```yaml
compose_args:
  - "-T"                    # disable TTY (useful when piping)
  - "--name"
  - "${param.database}_loader"
```

### Env injection in service runners

For containers, env vars are injected via the docker process environment plus `-e KEY` flags (name only), so secret values never appear in `argv` (and therefore not in `ps` or `/proc/<pid>/cmdline`).

## Type: service_run

Same as `service_exec` but always uses `docker compose run --rm` to start a fresh, throwaway container. Use for one-off jobs that should not require an already-running container.

```yaml
artisan-tinker:
  type: service_run
  service: app-main
  user: current
  workdir_from: services.main.work_dir_internal
  argv: [php, artisan, tinker]
```

`mode` is not a valid field for `service_run` (it always uses `docker compose run --rm`) — writing `mode:` at all is rejected at load time; omit it.

`argv_append_from` is accepted here on the same terms as on `service_exec`: the expression runs on the host and its output lines are appended to `argv` — see [Computed arguments](directives.md#computed-arguments-argv_append_from).

### Runner override block

Both `service_exec` and `service_run` accept a `runner:` block to override `service` / `user` / `workdir` / `workdir_from` (and, for `service_exec` only, `mode`) without duplicating the rest of the definition. `runner.mode` is rejected for `service_run`, which always uses `docker compose run`. Non-zero fields in `runner:` win over the top-level fields.

```yaml
queue-worker:
  type: service_exec
  service: app-main
  argv: [php, artisan, queue:work]
  runner:
    user: root
    workdir: /workspace
    mode: run
```

## Type: workflow

A workflow runs an ordered sequence of other commands, with optional confirmations and conditional steps. Workflows are the only way to compose multiple commands behind a single ID.

**Note:** Workflow steps use a string-based `command:` / `confirm:` / `with:` / `when:` syntax. The `when:` conditions inside workflows are string mini-language expressions, distinct from the typed `when:` / `check:` used in pipeline steps (see [deploy](../deploy/index.md)).

```yaml
bootstrap:
  type: workflow
  description: Full bootstrap — start db, create database, install deps, migrate
  steps:
    - command: db.start
    - command: services.main.db.create
    - command: services.main.composer-install
    - command: services.main.key-generate
    - command: services.main.migrate
```

### Step shape

Each step is either a **command** step, a **confirm** step, or a **parallel** step (mutually exclusive).

| Field | Used in | Description |
|-------|---------|-------------|
| `command` | command step | Full ID of the command to invoke |
| `with` | command step | Map of param overrides (templated values) |
| `confirm` | confirm step | Prompt text shown before continuing |
| `parallel` | parallel step | Concurrent group of leaf command sub-steps (see [Parallel sub-steps](#parallel-sub-steps)) |
| `when` | command / parallel step | Condition; step (or whole group) is skipped when falsy |
| `continue_on_error` | command / parallel step | Failure logged as warning; workflow proceeds |

### with: param overrides

`with:` values are rendered against the parent workflow's render context, so they can pull from config, params, and host helpers:

```yaml
- command: db.create
  with:
    database: "${vars.db.database}"

- command: services.main.db.dump-deploy
  with:
    target_database: "${param.target_db}"
    dump_dir: "${param.backup_dir}"
```

### when: conditions

`when:` expressions are rendered first, then classified into one of three forms:

1. **Boolean literal** — `true`, `false`, `1`, `0`, empty string. After rendering, this is the fast-path.
2. **Builtin predicate** — filesystem checks against the project root.
3. **Shell command** — `cmd: <command>`; evaluated via `sh -c`; exit 0 = true.

```yaml
steps:
  - command: services.main.composer-install
    when: "file-missing services/main/src/vendor/autoload.php"

  - command: bootstrap-cache-warm
    when: "{{ if .Params.warm }}1{{ else }}0{{ end }}"

  - command: install-deps
    when: "cmd: test ! -d services/main/src/vendor"
```

Builtin predicates (path is project-root-relative):

| Predicate | True when |
|-----------|-----------|
| `dir-exists <path>` | path is an existing directory |
| `dir-missing <path>` | path is missing or not a directory |
| `dir-empty <path>` | path is missing or has no entries |
| `dir-not-empty <path>` | path is a directory with at least one entry |
| `file-exists <path>` | path is an existing regular file |
| `file-missing <path>` | path is missing or not a regular file |

### continue_on_error

Marks a step that may fail without aborting the workflow. The error is logged as a warning, then execution continues:

```yaml
steps:
  - command: optional-cache-warm
    continue_on_error: true
  - command: services.main.migrate
```

Not valid on `confirm` steps.

### confirm steps

```yaml
steps:
  - confirm: "This will drop the database. Continue?"
  - command: db.drop
    with:
      database: "${vars.db.database}"
```

Confirm steps are silently skipped under `--yes` or `DWE_NONINTERACTIVE=1`. Otherwise huh prompts on TTY, and a `[y/N]` stdin fallback handles piped inputs.

### Parallel sub-steps

A workflow step can declare a `parallel:` block that fans out a group of sub-steps concurrently. This mirrors the pipeline `parallel:` schema in [deploy → Parallel step groups](../deploy/examples.md#parallel-step-groups) — the same `max_concurrent` / `fail_fast` knobs and the same live-block UI — but lives inside a workflow so the group is reusable across pipelines and invocable ad-hoc via `dwe commands`.

```yaml
services.all.composer-install:
  type: workflow
  description: Run composer install across every app service in parallel
  steps:
    - parallel:
        max_concurrent: 4
        fail_fast: true
        steps:
          - command: services.main.composer-install
          - command: services.api.composer-install
          - command: services.worker.composer-install
          - command: services.admin.composer-install
```

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `max_concurrent` | optional | `min(NumCPU, len(steps))` | Upper bound on goroutines running at once |
| `fail_fast` | optional | `true` | When true, the first sub-step error cancels siblings via context; when false, all sub-steps run and errors aggregate via `errors.Join` |
| `always_show_output` | optional | `false` | When true, every sub-step's captured stdout/stderr is dumped between `───── output: <command> ─────` / `──────────────────` bars after the group finishes — including successful sub-steps. The default keeps the failure-only behaviour. Skipped and cancelled sub-steps never produce output and are unaffected. |
| `steps` | required | — | Sub-steps; each must be a leaf command step (no `confirm`, no nested `parallel`) |

Group-level `when:` and `continue_on_error:` are valid on the step that carries `parallel:` (they govern the whole group). Per-sub-step `when:` and `continue_on_error:` are also valid and behave the same as in a sequential workflow. Sub-step `when:` is evaluated once at preflight (before any goroutine launches) so predicates with side-effects do not double-execute.

#### Constraints

1. **At least two sub-steps** — a `parallel.steps:` list of length 0 or 1 is rejected at validate-time.
2. **No nested parallel** — a sub-step may not itself declare `parallel:`. Flatten the structure or split into separate workflow steps.
3. **No confirm in sub-steps** — `confirm:` steps interactively prompt; the parallel live-block UI owns the terminal and cannot host a prompt. A sub-step that references a command with `confirmation: true` requires `--yes` (or `DWE_NONINTERACTIVE=1`); preflight rejects the group otherwise, and a runtime guard catches transitive confirmation calls.
4. **No `with:` on the container** — the parallel container holds no params of its own; each sub-step carries its own `with:`.

#### Composition

- **Ad-hoc**: `dwe commands <workflow-id>` (alias `dwe cmd <workflow-id>`) runs the workflow's own live-block on the terminal. Ctrl-C propagates as SIGINT through `signal.NotifyContext`, which cancels the group and gives children up to 5 s to exit before SIGTERM is escalated.
- **Inside a sequential pipeline step**: when a pipeline's sequential `cmd:` resolves to a workflow with a `parallel:` block, the pipeline's footer is paused for the duration of the step body (existing `SuspendForExec` / `ResumeAfterExec` contract), and the workflow renders its own block rows in the gap. The pipeline-step counter advances by exactly one — sub-steps are NOT counted as pipeline steps.
- **Inside a parallel pipeline group OR another parallel workflow**: rejected at runtime. Only one live-block can own the terminal at a time. The error is the `ErrWorkflowNestedParallel` sentinel.

#### Live rendering and reporting

- **Block rows (TTY)**: each sub-step has a row showing `<spinner-or-glyph> [<elapsed>] [<i>/<N>] <command>[: <latest-line>]`. The latest line tracks both newline-terminated output AND carriage-return frames, so `curl` / `wget` / `docker pull` progress bars are visible in-place (the row updates every time the child writes a frame).
- **End-of-block summary (TTY)**: when the parallel group finishes, the rows freeze with their final glyph (✓/✗/◎) in scrollback and a single one-line summary footer is printed in their place: `✓ [<elapsed>] parallel: <workflow-id>` in green on success, `✗ ...` in red when any sub-step failed. The per-sub-step `✓ [i/N] Done: …` lines are NOT re-emitted on TTY because the same information is already in the frozen block rows above.
- **Non-TTY mode** (CI / piped stdout): no live block. Each sub-step prints its terminal-state line (`✓ [i/N] Done`, `◎ [i/N] Skipped`, `✗ [i/N] Failed`, `◎ [i/N] Cancelled`) followed by a plain-text summary footer (`✓ [<elapsed>] parallel: <workflow-id>`).
- **Failure dumps**: a failed sub-step's captured output is replayed between `───── output: <command> ─────` / `──────────────────` bars on stderr in BOTH TTY and non-TTY modes — the live row cannot show the full buffer. The top bar names the sub-step so multi-failure dumps stay attributable, and ANSI escape sequences in the child's output are forwarded verbatim so colours survive the round-trip.
- **Colour for parallel sub-steps**: each child is launched with a per-sub-step pseudo-terminal so tools that key colour output off `isatty(STDOUT)` — Pest, PHPUnit/Symfony Console, ripgrep, fzf, lipgloss-based CLIs, … — keep emitting ANSI codes even though the captured output is consumed by an in-process line tee rather than the user's terminal. The PTY's master side is read into the tee, so per-row progress (e.g. `docker pull` `\r`-frames) and the buffered dump both see the same byte stream. As a belt-and-braces fallback the children also inherit `CLICOLOR_FORCE=1`, `FORCE_COLOR=1`, and `COLORTERM=truecolor` (forwarded into the container via `-e` for `type: service_exec` / `service_run`) so env-aware tools also keep colour without depending on PTY detection.

#### Per-sub-step logs

Each sub-step's combined stdout/stderr is captured to `.dwe/logs/parallel/workflow/<workflow-id>/<sub-command>.log`. Only newline-terminated frames are written to the log file (carriage-return progress frames stay on the live row and are dropped from logs), so the file stays readable without `\r`-spam.

#### Sub-step naming and pipeline overrides

Each workflow sub-step may set an explicit `name:` (optional). When absent the effective name defaults to the referenced `command`. This name is the handle the pipeline uses for `sub_step_overrides:` — see [deploy → Targeting workflow sub-steps with overrides](../deploy/examples.md#targeting-workflow-sub-steps-with-overrides). When two sub-steps in the same workflow share the same effective name, a pipeline override targeting that name is rejected at plan time as ambiguous; set explicit `name:` on the sub-steps to disambiguate.

```yaml
commands:
  dumps-deploy:
    type: workflow
    steps:
      - parallel:
          steps:
            - name: deploy-main          # explicit name → pipeline can target it
              command: services.main.db.dump-deploy
            - name: deploy-stock
              command: services.main.db.dump-deploy-stock
```

Workflows never know whether overrides are attached to them — they are opaque to gating decisions. Invoking a workflow with overrides only happens through a pipeline step that declares `sub_step_overrides:`.

## Type: builtin

Invokes an engine-internal builtin action by name — the same registry pipelines use in `deploy.yml` / `reset.yml` / `lifecycle.yml`. No subprocess is spawned; the builtin runs in-process in Go.

Use `type: builtin` whenever a command would otherwise re-implement logic the engine already provides (waiting for healthy containers, removing project volumes, ensuring service directories, …). It is the right choice for any leaf step that needs structured, auditable execution rather than a shell pipeline — and it sidesteps the trap of embedding `{{...}}` from other tools (e.g. `docker inspect --format`) inside a `type: shell` `cmd:`, which would otherwise collide with command template rendering.

| Field | Required | Description |
|-------|----------|-------------|
| `cmd` | yes | Builtin name (e.g. `docker_wait_healthy`) |
| `with` | optional | Map of parameters passed to the builtin |

```yaml
db.wait:
  type: builtin
  private: true
  description: Wait for the db container to become healthy
  cmd: docker_wait_healthy
  with:
    services: [db]
    timeout: 120s
    interval: 2s
```

### Templating inside with

String values inside `with:` — including entries in nested lists and maps — are rendered against the command template space (`${...}`, `{{ ... }}`) before the builtin sees them. This lets you parameterise a builtin with config lookups, params, or context:

```yaml
db.wait-target:
  type: builtin
  cmd: docker_wait_healthy
  params:
    service: { required: true }
  with:
    services: ["${param.service}"]
    timeout: "${docker.wait_timeout}"
```

Non-string scalars (booleans, integers) pass through untouched.

### Builtin registry

The list of available builtins, their parameters, and their behaviour are documented once in [deploy → Available builtins](../deploy/builtins.md). The same builtins are usable from `type: builtin` commands — there is one shared registry.

The most useful builtins to expose as commands tend to be the long-running, idempotent ones — `docker_wait_healthy` in particular, which is meant to be called whenever the project needs to block until the stack (or a specific service) is healthy.

### Invalid fields

`type: builtin` is a leaf action — it rejects every type-specific field of the other types: `argv`, `script:`, `steps:`, `service`, `compose_args`, `workdir` / `workdir_from`, `user`, `mode`, and `runner:`. Use `params:` / `context:` / `env:` / `files:` / `messages:` as on any other type for inputs, env exposure, and styled output.

## Type: daemon

`type: daemon` is the declarative shape for long-running, parameterised background processes inside dwe services (canonical example: a Laravel queue worker). One YAML block expands at registry-load time into **four first-class virtual commands**:

| Virtual ID | Behaviour | Blocking |
|---|---|---|
| `<base>.start` | `docker compose run -d --name <full> ...` | no |
| `<base>.logs` | `docker logs -f --tail=100 <full>` | yes — Ctrl-C detaches (the container keeps running) |
| `<base>.stop` | `docker stop -t <timeout> <full>` | no |
| `<base>.restart` | `<base>.stop` followed by `<base>.start` | no |

Each virtual command appears in the registry, the `dwe cmd` browser, completion, `inspect`, and is referenceable from workflows. The source `<base>` command is **not** runnable on its own — only the four virtual commands are.

Container names are auto-prefixed with the resolved compose project name — `workspace/docker.yml`'s `project_name` if set, otherwise `ProjectConfig.FullName()` (`<prefix>-<name>`) — so daemons share the same project scope as compose-managed services (and the same project can run on multiple checkouts side by side). Every container carries standardised labels so `dwe status daemons`, completion, and `_auto_reap_daemons` can find them via `docker ps` — **no separate state file**.

### YAML form

```yaml
commands:
  queue:
    type: daemon
    description: "Laravel queue worker"
    service: app-main             # literal compose service name (no ${...})
    workdir_from: services.main.work_dir_internal
    user: www-data
    env:
      QUEUE_CONNECTION: redis
    params:
      name:
        default: default
        pattern: ^[a-zA-Z0-9_-]+$
    argv:
      - php
      - artisan
      - queue:listen
      - --timeout=0
      - --queue=${param.name}
    daemon:
      container_template: "php_queue_${param.name}"
      on_already_running: error   # error | noop
      auto_remove: true           # default true → adds --rm
      stop_timeout: 10s
```

`service`, `workdir`/`workdir_from`, `user`, `env`, `params`, `argv`, `compose_args` follow the same semantics as [`type: service_run`](#type-service_run). The daemon-specific configuration lives entirely under the `daemon:` block.

For `workdir` and `user` that is now literally true — the daemon runs the same resolvers as the service runners:

- **`workdir`** goes through the full [seven-rung chain](#the-service-runner-chain), including the `internal` sentinel and the `cli.workdir` → `work_dir_internal` → `dir_internal` service fallback. A daemon that declares no `workdir` therefore starts in the same directory `dwe shell <svc>` lands in, instead of the image's `WORKDIR`.
- **`user`** falls back to `services.<svc>.cli.user` of the target service when the command sets no `user:`, and `user: internal` opts out of that fallback — see [User resolution](#user-resolution).

Two daemon-specific behaviours changed with that alignment, and both are visible from the outside:

- **`workdir_from` now beats a literal `workdir`.** The daemon used to do the opposite (the literal won, and `workdir_from` was consulted only when `workdir` was empty), which contradicted the precedence documented for the service runners. A daemon that sets **both** fields now starts in the `workdir_from` directory.
- **A `workdir_from` dot-path that resolves to nothing no longer fails the daemon.** `.start` used to abort with `workdir_from "…": path not found in config`; it now falls through to the remaining rungs. That turns a hard error into a *success in a different directory* — if a daemon of yours relied on that error to catch a typo'd or renamed dot-path, nothing will report it any more. A non-string value at the dot-path is still a hard error.

### `daemon:` block fields

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `container_template` | yes | — | Container-name template; rendered against the command template space and prefixed with `<project.full>-`. Post-render must match `^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$`. |
| `on_already_running` | optional | `error` | `error` aborts `.start` if the container already exists; `noop` makes `.start` idempotent. |
| `auto_remove` | optional | `true` | When true, `.start` adds `--rm` so the container is removed when it stops. |
| `stop_timeout` | optional | `10s` | Duration string. Converted to integer seconds at `docker stop -t <secs>`; values below 1s round up to 1s (never `0`). |

### Container naming

```
<project.full>-<rendered container_template>
```

`project.full` is the resolved compose project name — `workspace/docker.yml`'s `project_name` if set, otherwise `ProjectConfig.FullName()` (`<prefix>-<name>` if `prefix:` is set, else `<name>`) — so daemons share the project scope of compose-managed services. The post-render regex `^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$` is the authoritative defense; invalid characters in rendered template values fail at runtime even if the param's `pattern:` happened to permit them.

### Standard labels

Every daemon container carries three labels so `docker ps` is the single source of truth:

- `dwe.project=<project.full>`
- `dwe.daemon.id=<base>` (e.g. `services.main.queue`)
- `dwe.daemon.params=<json>` (e.g. `{"name":"emails"}`) — produced via `encoding/json.Marshal` to round-trip safely through quotes, backslashes, and control characters

`dwe status daemons`, `--set` completion, and `_auto_reap_daemons` all filter on these labels.

### Virtual command behaviour

- **`.start`** — issues `docker compose run -d --no-deps --entrypoint "" [--rm] --name <full> [--user …] [--workdir …] -e K1 -e K2 --label dwe.project=… --label dwe.daemon.id=… --label dwe.daemon.params=… <service> <argv…>`. Environment **values** are passed via the child process environment (`cmd.Env`), never the host argv, so secrets do not appear in `ps` or `/proc/<pid>/cmdline`. `--no-deps` keeps the running stack untouched; `--entrypoint ""` ensures the user's `argv:` is what actually runs. On `on_already_running: error` plus a docker name-conflict error, the builtin surfaces `ErrDaemonAlreadyRunning`; on `noop`, the same error is swallowed and `.start` succeeds.
- **`.logs`** — runs `docker logs -f --tail=100 <full>` foreground. Ctrl-C sends `SIGINT` to the `docker logs` process only (graceful detach via `cmd.Cancel`); the container is never signalled. If the container is not running, `.logs` errors with a hint pointing at `.start`.
- **`.stop`** — runs `docker stop -t <stop_timeout-as-seconds> <full>`. Missing container is **not** an error (idempotent stop).
- **`.restart`** — a virtual `type: workflow` of `<base>.stop` followed by `<base>.start`. Workflow steps explicitly forward each declared `param.<name>` via `with:`, so `dwe cmd queue.restart --set name=emails` restarts the `emails` daemon (not the default).

### Validation

`dwe validate` and load-time `cmd.Validate()` enforce:

- `service:` is required and **must be literal** — no `${...}` or `{{...}}`. (Parameterised `service:` is intentionally out of scope for v1 to keep the `dwe.daemon.id` label stable.)
- `daemon.container_template` is required and non-empty.
- `daemon.on_already_running` is one of `error` / `noop` (empty = default `error`).
- `daemon.stop_timeout` parses via `time.ParseDuration` and is strictly positive.
- Every `${param.X}` referenced in `container_template` must be declared in `params:` AND carry a `pattern:` (advisory — the runtime regex on the rendered container name is the authoritative gate).
- Synthetic IDs (`<base>.start`, `.logs`, `.stop`, `.restart`) must not collide with any explicit command in the registry.

### Parallel and workflow restrictions

`.logs` is **interactive** — it tails container output foreground and is detached by Ctrl-C. Like `confirm`, it is rejected anywhere inside a `parallel:` step group at plan time (deploy / lifecycle pipelines and workflow parallel blocks), regardless of `--yes`. `.start`, `.stop`, and `.restart` may appear inside parallel groups.

### Lifecycle integration

Whenever `dwe stop` runs (whether `lifecycle.yml` exists or not), a synthetic `_auto_reap_daemons` phase is prepended to the stop pipeline. It enumerates every container labelled `dwe.project=<full>` with a non-empty `dwe.daemon.id` and stops them in parallel. There is no opt-out; the phase is visible in plan output. See [lifecycle.md](../lifecycle.md) for the stop pipeline shape.

If `lifecycle.yml` is absent, `dwe stop` still runs (with only the `_auto_reap_daemons` phase plus the default `Project is stopped. Have a nice day!` message).

### Security & privacy

- **Param values land in `dwe.daemon.params` as JSON labels**, which `docker inspect` exposes to anyone with docker socket access on the host. **Do not put secrets in `params:`.** Use `env:` instead — env values are passed through the container environment (`docker compose run -e KEY` with the value in `cmd.Env`), never through the host process argv, so they do not appear in `ps` or `/proc/<pid>/cmdline`.
- **The container-name regex is enforced after rendering** — invalid characters in rendered param values are a hard runtime error even if the YAML `pattern:` happened to allow them. The validator's param-pattern check is advisory; the rendered-name regex is the authoritative defense.
- **`service:` parameterisation is rejected** in v1 — the `dwe.daemon.id` label needs to be stable across restarts so completion, status, and reap can reliably correlate state across invocations.

### Invalid fields

The source daemon command rejects fields that conflict with its declarative shape: `script:`, `steps:`, `cmd:` (the action is implicit), `mode`. Use `params:` / `context:` / `env:` / `files:` / `messages:` / `argv` / `service` / `workdir` / `workdir_from` / `user` / `compose_args` / `runner` as on any service runner. All of these flow into the virtual `.start` invocation.

`argv_append_from` is rejected too, even though `daemon` builds an `argv`: the expansion packs that `argv` into the synthetic `.start` command, where the field's documented "empty output → skip" would read as silently failing to start the daemon. See [Computed arguments](directives.md#computed-arguments-argv_append_from).

### End-to-end flow

```bash
# Start a worker for the "emails" queue
dwe cmd queue.start --set name=emails

# Tail it (Ctrl-C detaches, container stays)
dwe cmd queue.logs --set name=emails

# Check what's running
dwe status daemons

# Restart it
dwe cmd queue.restart --set name=emails

# Stop one daemon
dwe cmd queue.stop --set name=emails

# Stop everything (reaps all daemons in this project automatically)
dwe stop
```

## Workdir resolution

`workdir` accepts a templated path. Relative paths resolve against the project root for host runners (`type: shell`, `type: script`) and against the container filesystem for service runners.

`workdir_from` is valid for `service_exec` / `service_run` / `daemon` and reads a string out of the merged config:

```yaml
workdir_from: services.main.work_dir_internal
```

When both `workdir` and `workdir_from` are set, `workdir_from` wins — the same "config wins, literal is the safety net" pattern as `params.*.default_from`. Inside a `runner:` block the same rule applies between `runner.workdir_from` and `runner.workdir`. The one exception is the `internal` sentinel, described below.

### The service-runner chain

`service_exec`, `service_run`, and `daemon` resolve the container workdir through a single seven-rung chain. The first rung that yields a non-empty value wins:

| # | Rung | Result |
|---|------|--------|
| 1 | `workdir: internal` (or `runner.workdir: internal`) | No `--workdir` flag, **and** the whole fallback is skipped. Stop. |
| 2 | `runner.workdir_from` → `workdir_from` | The string the dot-path resolves to |
| 3 | `runner.workdir` → `workdir` | The literal path |
| 4 | `services.<svc>.cli.workdir` | The service's declared CLI workdir |
| 5 | `services.<svc>.work_dir_internal` | The service's internal work directory |
| 6 | `services.<svc>.dir_internal` | The service's internal project directory |
| 7 | _(nothing matched)_ | No `--workdir` flag — the image's own `WORKDIR` applies |

Rungs 4–6 are exactly the chain `dwe shell` applies, so a shell session and a command targeting the same service finally land in the same directory. Before this, a command without an explicit `workdir` landed in the image's `WORKDIR` while `dwe shell` into the same service landed in the service's work directory.

The service for rungs 4–6 is looked up by its `container:` value, not by the `workspace/services/<name>/` folder key — `service: app-main` reads the fallback from the service folder whose `service.yml` declares `container: app-main`. The lookup happens **after** the `runner.service` redirect, exactly like the `cli.user` fallback.

`workdir_from` still hard-errors when the dot-path resolves to a non-string value (a configuration bug). A dot-path that is simply **absent** from the merged config is not an error: it yields nothing and resolution falls through to the next rung.

### `workdir: internal` outranks `workdir_from`

For this one value the published "`workdir_from` wins" rule **inverts**. `internal` is not a path, it is an explicit "give this command no `--workdir` and do not guess one either", and there is no other way to express that — so the resolver stops at rung 1 even when `workdir_from` is set and resolves to a perfectly good string. Every other literal `workdir` value keeps the normal precedence: `workdir_from` beats `workdir`.

This is the deliberate symmetry with [`user: internal`](#user-resolution), which likewise emits no `--user` flag and skips the `services.<svc>.cli.user` fallback. One sentinel word, one meaning, on both fields.

Resolution order:

```mermaid
flowchart TD
    S{workdir is internal} -- yes --> N[no --workdir flag]
    S -- no --> F[workdir_from]
    F -- non-string --> X[error]
    F -- string value --> U[use it]
    F -- empty/missing --> W[workdir]
    W -- non-empty --> U
    W -- empty --> A[cli.workdir]
    A -- non-empty --> U
    A -- empty --> B[work_dir_internal]
    B -- non-empty --> U
    B -- empty --> C[dir_internal]
    C -- non-empty --> U
    C -- empty --> N
```

Host runners (`type: shell`, `type: script`) have no target service to fall back to and reject `workdir_from` at load time: only the literal `workdir` applies, and an unset `workdir` means the project root.

### Templated service / workdir / workdir_from

`service`, `workdir`, and `workdir_from` are rendered through the command template space before resolution, the same as `argv`, `cmd`, and `compose_args`. This lets one definition target multiple services without duplication:

```yaml
hub.chown-src:
  type: service_run
  private: true
  params:
    service: { type: string, required: true, pattern: '^[a-z0-9_-]+$' }
  service: app-${param.service}
  workdir_from: services.${param.service}.work_dir_internal
  user: root
  argv: [sh, -c, "chown -R www-data:www-data /workspace/src"]
```

A pipeline (or another command) then invokes the same definition per service via `--set service=<name>`. The `runner:` block fields (`runner.service`, `runner.workdir`, `runner.workdir_from`) are rendered identically.
