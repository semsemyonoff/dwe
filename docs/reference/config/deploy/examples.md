# Examples and patterns

Worked examples for orchestrator and per-service pipelines, the `after:` ordering field, parallel step groups, and per-sub-step overrides on workflow targets. Closes with common pitfalls.

## Contents

- [Example: orchestrator pipeline](#example-orchestrator-pipeline)
- [Example: per-service pipeline](#example-per-service-pipeline)
- [Example: infra service pipeline with `after:`](#example-infra-service-pipeline-with-after)
- [Parallel step groups](#parallel-step-groups)
- [Targeting workflow sub-steps with overrides](#targeting-workflow-sub-steps-with-overrides)
- [Common pitfalls](#common-pitfalls)

## Example: orchestrator pipeline

```yaml
# workspace/deploy.yml
phases:
  - name: services
    deploy_services: true
    description: Deploy all enabled services

  - name: start
    description: Start containers
    steps:
      - name: up
        type: dwe
        cmd: "docker up"
      - name: wait-healthy
        type: builtin
        cmd: docker_wait_healthy

  - name: post-deploy
    description: Post-deploy summary
    untracked: true
    steps:
      - name: info
        type: dwe
        cmd: "info"
      - name: success
        type: builtin
        cmd: message
        with:
          level: success
          text: Deploy completed successfully
```

## Example: per-service pipeline

```yaml
# workspace/services/main/deploy.yml
phases:
  - name: setup
    description: Create dirs and install
    when:
      type: builtin
      cmd: "dir-empty services/main/src"
    steps:
      - name: create-dirs
        type: builtin
        cmd: service_dirs_ensure
        with:
          service: main
      - name: install
        type: command
        cmd: app.install
      - name: copy-configs
        type: builtin
        cmd: service_configs_copy
        with:
          service: main
          mode: replace
        check:
          type: builtin
          cmd: service_configs_check
          with:
            service: main

  - name: init
    description: Initialize application
    steps:
      - name: db-create
        type: command
        cmd: services.main.db.create
      - name: composer-install
        type: command
        cmd: services.main.composer-install
      - name: migrate
        type: command
        cmd: services.main.migrate

  - name: finalize
    description: Generate IDE config
    steps:
      - name: render-ide
        type: dwe
        cmd: "render ide main"
```

## Example: infra service pipeline with `after:`

Any service type can have a deploy pipeline. An infra service like MinIO can declare initial bucket setup, and use `after:` to ensure it deploys after the app whose secrets it provisions:

```yaml
# workspace/services/minio/deploy.yml
after:
  - main  # deploy after the main app service

phases:
  - name: init
    description: Create MinIO buckets
    when:
      type: shell
      cmd: "mc alias ls local 2>/dev/null | grep -q local"
    steps:
      - name: create-bucket
        type: shell
        cmd: mc mb --ignore-existing local/uploads
```

## Parallel step groups

A step may declare a `parallel:` block instead of a leaf body (`type` + `cmd`). The orchestrator runs the inner sub-steps concurrently via `errgroup` + a semaphore, waits for all of them, and aggregates results. The same model applies to `lifecycle.yml` and `reset.yml`.

```yaml
phases:
  - name: init
    steps:
      - name: db-dumps
        when: ...                  # optional; evaluated once before the group
        skip_confirm: true         # optional; OR-merged into every sub-step
        parallel:
          max_concurrent: 4        # optional; default = min(NumCPU, len(steps))
          fail_fast: true          # optional; default true
          steps:                   # required, >= 2
            - name: download-main
              type: command
              cmd: services.main.db.dump-download
              files_gate: { ... }
            - name: download-stock
              type: command
              cmd: services.stock.db.dump-download
            - name: download-price
              type: command
              cmd: services.price.db.dump-download
```

### Schema

Group-level keys allowed on a step with `parallel:`:

| Field | Type | Notes |
|---|---|---|
| `name` | string | Required. Group name (used in plan output and reporter headers). |
| `description` | string | Optional. |
| `when` | condition | Evaluated **once** before launching sub-steps. On false the whole group is skipped and each sub-step is recorded as skipped in the journal. |
| `skip_confirm` | bool | OR-merged into every sub-step at resolve time. A sub-step's own `skip_confirm: false` cannot un-set the inherited true (monotonic). |
| `parallel.max_concurrent` | int | Default `min(runtime.NumCPU(), len(steps))`; capped to `len(steps)` when larger. |
| `parallel.fail_fast` | bool (tristate) | Default `true`. When true, the first sub-step failure cancels siblings via `context`. When false, all sub-steps run to completion and errors are joined. |
| `parallel.steps` | []DeployStep | Required, >= 2 entries. |

Leaf-only directives are **rejected** on a group step: `type`, `cmd`, `with`, `check`, `files_gate`, `continue_on_error`. The YAML loader returns a strict-decode error naming the offending field. Unknown fields under `parallel:` itself (e.g. typo `max_concurent`) are likewise rejected.

### Defaults and validation

| Rule | Diagnostic |
|---|---|
| Nested `parallel:` (sub-step has its own `parallel:`) | error — flat groups only in v1 |
| `parallel.steps` < 2 | error — use a leaf step if only one item |
| Sub-step missing `name` | error |
| Duplicate sub-step names within a phase (across groups + leaf steps) | error — the journal keys entries by `(phase, name)` |
| Interactive prompt in sub-step without `skip_confirm: true` (sub-step or inherited from group) | error — covers `confirmation: true` in the target command, `builtin: confirm`, and workflows recursively containing a confirm-step |
| `service_run` sub-step with TTY-allocating compose args | warning — TTY is never allocated in parallel mode |

Validation runs at `dwe validate` and at plan resolution; either path catches misconfigurations before execution.

### Execution semantics

- **Cancellation**: `errgroup.WithContext` carries the parent `context.Context` through every runner (`HostRunner`, `DWERunner`, `ServiceExecRunner`, `ServiceRunRunner`, `ScriptRunner`, `WorkflowRunner`) and every builtin. Shell-out children are spawned with `exec.CommandContext` and bound to `cmd.Cancel = SIGTERM` + `cmd.WaitDelay = 5s` — on cancel, the child receives SIGTERM first (graceful shutdown), then SIGKILL after the delay. The Go-side builtin `docker_wait_healthy` aborts its poll loop on `ctx.Done()` within one tick.
- **SIGINT**: `RunWithOptions` installs a `signal.NotifyContext(SIGINT, SIGTERM)` on the parent context once per pipeline run. A user Ctrl-C cancels the context, which propagates to every active sub-step's child process. No orphaned `docker compose` / `sleep` processes after a clean shutdown.
- **`fail_fast: true`**: first failing sub-step (not counting `continue_on_error: true` ones) cancels the group; remaining sub-steps observe `ctx.Done()` and their children are killed. The group's error is the first failure wrapped with its sub-step address.
- **`fail_fast: false`**: all sub-steps run to completion. Errors are wrapped per-sub-step (`parallel sub-step "phase/group/sub": <err>`) and combined via `errors.Join`.
- **Per-sub-step `when` / `files_gate` / journal-skip**: each sub-step still runs through the same `step-when → (files_gate ↔ journal-skip) → ExecAction → check` pipeline. The `files_gate ↔ journal-skip` interaction is asymmetric: `state: missing` bypasses journal-skip; `state: readable` and gateless steps consult journal-skip first (see [`files_gate:`](conditions.md#files_gate-pre-condition-for-files)). Group-`when` is evaluated **once**; per-sub-step `when` is **also** evaluated inside the goroutine.
- **Journal**: each sub-step is journaled independently under `(phase, sub-step.Name)`. The group itself is not journaled. `journal.StepHash(step)` is computed from the sub-step alone, so reordering or adding sub-steps does not invalidate siblings.

### Reporter and logging

- **Live view (TTY)**: the reporter drives a `LiveLine` sticky footer (`bubbles/v2/spinner` + pipeline `[<elapsed>]` stopwatch + `[N/M] <step>` text) for the whole pipeline, and switches to a `LiveBlock` while a parallel group is active. Each block row shows `<spinner-or-final-glyph> [<sub-elapsed>] [<pipelineIdx>/<pipelineTotal>] <sub-name>`, with the per-row spinner replaced by ✓/✗/◎ (in green/red/yellow) when the sub-step settles. The pipeline-wide `[N/M]` index in block rows lets parallel sub-steps blend into the surrounding step counter rather than restarting from `[1/3]`. The live view is rendered via the bubbles `Model.View()` API plus a private `time.Ticker` — `tea.NewProgram` is NOT used (so the terminal stays in cooked mode, Ctrl+C still raises SIGINT via VINTR, and no capability queries or kitty-keyboard sequences are emitted).
- **Sequential vs parallel routing**:
  - Sequential step bodies pause the LiveLine via `Reporter.SuspendForExec` and the child writes to the host terminal directly (with a PTY when stdout is a TTY). Colors, cursor positioning and interactive UX work as on a bare shell; a `frameLogWriter`-wrapped tee captures an ANSI-stripped, frame-collapsed copy to the on-disk log, flushed before the step's own finish line so both writers to that file stay in order. `ResumeAfterExec` repaints the footer after the child exits.
  - Parallel sub-steps do NOT allocate a PTY — granting a child a PTY while stdin is the empty reader makes `docker compose exec/run` fail with "cannot attach stdin to a TTY-enabled container". Sub-step output flows through `ansiOnlyStripper` → `lineTee` → `Reporter.StepOutput` so the block row shows the latest `\n`-frame; the host terminal is owned by the LiveBlock.
- **Frame-aware parser**: the `\r`-aware `lineTee` parses a child's stream into `(frame, final)` callbacks — directly for parallel sub-steps, through `frameLogWriter` for sequential step bodies. Only the latest frame is shown on the live row. In log files a non-final (`\r`-terminated) frame is *pending*: the next frame evicts it, so only the last frame of a redraw run is written and `50%\r100%\n` logs a single `100%` line rather than two. A run that ends on a bare `\r` is emitted by the writer's `Flush()` at step end. This is frame collapsing, not terminal emulation: `abc\rX\n` is logged as `X` while a terminal shows `Xbc`, and whole-frame redraws driven by cursor-up sequences (compose's `[+] up 2/3` block) are out of scope and still land once per redraw.
- **Buffer dump policy**: on sub-step finish, the buffered full output of the sub-step is replayed between `───── output ─────` separators when the sub-step failed, or when no log file is enabled. On TTY success with logging enabled, the dump is suppressed and a `Full log: <path>` line is emitted instead. Non-TTY mode always dumps (and dumps are clean lines thanks to the frame parser).
- **Per-sub-step log files**: `.dwe/logs/parallel/<pipeline>/<group>/<sub>.log` captures the sub-step's committed lines (ANSI stripped, non-final `\r` frames dropped). A `\r`-terminated tail is not written to this file — it still reaches the global pipeline log, because the same frame also goes to `Reporter.StepOutput`, which commits the trailing tail when the sub-step finishes. The global pipeline log (`.dwe/logs/<pipeline>.log`) receives every status line and every committed child line exactly once.
- **EndBlock semantics**: when a parallel group finishes, `LiveLine.EndBlock` ERASES the live footer that sat below the block (which would otherwise be frozen in scrollback showing a spinner mid-frame next to the last-started sub-step text) and paints a fresh single-line footer in its place. The finalised block rows (✓/✗/◎ + frozen elapsed) persist as scrollback.
- **Prompt handoff**: every `widgets.Run*` huh prompt fires package-level hooks registered by `NewPlainReporter` (`widgets.SetHuhHooks(live.Pause, live.Resume)`), so the footer is erased before the prompt renders and repainted after it returns. Sequential step bodies use the same `live.Pause`/`live.Resume` via `Reporter.SuspendForExec`/`ResumeAfterExec`.
- **Non-TTY parity**: when `term.IsTerminal(os.Stdout.Fd())` is false (CI, piped stdout) the live view is fully disabled (no ticker, no cursor sequences, no footer) but the frame-aware parser is still on, so CI dumps have no `\r`-spam.

### Plan output

`dwe deploy plan` renders parallel groups with a contiguous index range and indented sub-step lines:

```
[12-14/25] [parallel group: db-dumps (3 steps, max_concurrent=3, fail_fast=true)]
  [12/25]  download-main      command services.main.db.dump-download
                              [files_gate: readable (required)]
  [13/25]  download-stock     command services.stock.db.dump-download
  [14/25]  download-price     command services.price.db.dump-download
```

### Restrictions (v1)

- No nested `parallel:` inside `parallel:`.
- No interactive confirmation in sub-steps (`confirmation: true`, `builtin: confirm`, workflow with `WorkflowStep.Confirm`). Set `skip_confirm: true` or restructure.
- No DAG / `depends_on` between sub-steps. Flat groups only.
- No auto-parallelisation flag — explicit YAML opt-in only.
- No PTY in sub-steps. `service_run` with `-it`-style compose args will fail at the child with "cannot allocate tty" (surfaces as a normal sub-step failure subject to `continue_on_error` / `fail_fast`).

## Targeting workflow sub-steps with overrides

A pipeline step whose `type: command` targets a workflow can attach **per-sub-step orchestration directives** to that workflow without modifying the workflow itself. This keeps `WorkflowStep` minimal (only `command:` / `with:` / `confirm:` / `when:` / `continue_on_error:` / `parallel:`) and keeps gating decisions on the pipeline side, where they belong.

### Schema

```yaml
phases:
  - name: deploy-dumps
    steps:
      - name: db-dumps-deploy
        type: command
        cmd: services.main.db.dumps-deploy   # a workflow with a parallel block
        skip_confirm: true
        sub_step_overrides:
          deploy-main:
            files_gate:
              state: readable
              command: services.main.db.dump-deploy
          deploy-stock:
            files_gate:
              state: readable
              command: services.main.db.dump-deploy
              with: { database: "${vars.db.stock_database}" }
          deploy-price:
            files_gate:
              state: readable
              command: services.main.db.dump-deploy
              with: { database: "${vars.db.price_database}" }
```

The referenced workflow stays opaque and reusable:

```yaml
# workspace/commands/services/main/db.yml
commands:
  dumps-deploy:
    type: workflow
    description: Restore all dumps in parallel
    steps:
      - parallel:
          steps:
            - name: deploy-main
              command: services.main.db.dump-deploy
            - name: deploy-stock
              command: services.main.db.dump-deploy-stock
            - name: deploy-price
              command: services.main.db.dump-deploy-price
```

### Resolution

- Sub-step lookup uses `WorkflowStep.name` when set, otherwise the referenced `command`. Names must be unambiguous within the target workflow when an override key references them; collisions are rejected at plan time with `sub_step_overrides[<key>] is ambiguous`.
- Each override key must match a leaf sub-step (top-level Command step or a Command leaf inside the workflow's `parallel:` block). Sub-steps whose command is itself a workflow are not addressable in v1 — the override must reach a non-workflow sub-step.
- `files_gate:` inside an override is validated against the **sub-step's** target command using the same rules as a step-level `files_gate:` (state, require spec, with/default-from coverage of required params).
- Overrides only apply when the workflow is invoked via the originating pipeline step. The same workflow invoked ad-hoc (`dwe commands run …`) or as a sub-step of another workflow runs as-written. Overrides do NOT propagate through nested workflow invocations.

### Runtime semantics

When the workflow executes, every leaf sub-step is matched against `sub_step_overrides[<step-name>]`:

- **gate satisfied** → the sub-step runs normally.
- **gate not satisfied** → the sub-step is **skipped**, reported as `Skipped: <command> (files_gate: <state> [<offending-id>…])` on stderr and in the live block row. Skips do not fail the workflow.
- **gate evaluation error** (unknown command, missing files: block, bad require spec) → the sub-step **fails** with a wrapped error; standard `continue_on_error` / `fail_fast` apply.

The workflow's own `when:` on a sub-step is evaluated first; an override gate is only consulted when `when:` is true.

### When to use this versus a step-level `files_gate:`

| Situation | Use |
|---|---|
| Single non-workflow leaf step whose run depends on a file | step-level `files_gate:` |
| Workflow that orchestrates several similar sub-steps and you want gating per sub-step from the pipeline | `sub_step_overrides:` |
| You want the workflow to fail loudly when a required input is missing during ad-hoc invocation | leave overrides off — the underlying command's `files: required: true` enforces it |

### Restrictions (v1)

- Only `files_gate` is supported inside an override. Future versions may extend this to `when:` and `continue_on_error:` at the override level.
- Overrides cannot target a sub-step whose command is itself a workflow. Refactor the inner workflow to expose the leaf, or move the override one level deeper by re-declaring the pipeline-step against that inner workflow.
- Override keys must refer to sub-step names that exist in the immediate workflow. Validation runs at `dwe validate` and at plan resolution.

## Common pitfalls

- **Missing `with:` for builtin parameters** — builtins require `with:` for their parameters; passing them as top-level step fields does not work.
- **`deploy_services` in `reset.yml`** — rejected at load time. The reset pipeline does not iterate services; if you need per-service cleanup, declare it explicitly in the reset phases.
- **Forgetting `log: false` for noisy reset runs** — reset defaults to `log: false`, deploy defaults to `log: true`. Set the field explicitly when you want behaviour different from the default.
- **Using `continue_on_error` to mask real failures in core phases** — it is meant for hook phases (pre/post). A failed `docker up` should always abort.
- **Confusing `when:` and `check:`** — `when:` is evaluated before the step runs (pre-condition); `check:` is evaluated after success (post-action). `when:` uses the typed `type: builtin|shell|template` / `cmd:` shape; `check:` uses the typed `type: shell|dwe|command|builtin` shape.
- **Duplicating file-probe logic in `when:` instead of using `files_gate:`** — If a step should run conditionally based on whether a file exists, use `files_gate:` instead of hard-coding globs in a shell `when:` condition. That way, edits to the command's `files:` definition automatically apply to the step's probe logic — they stay in sync. The `files_gate:` references the command's canonical file spec.
