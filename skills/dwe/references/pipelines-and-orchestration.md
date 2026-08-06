# Authoring DWE pipelines & orchestration

Load this file when the task is "customize deploy/run/stop", "add a setup question", "add a preflight check", a post-up hook, or the info dashboard / branding. Covers the optional orchestration files: per-service & project `deploy.yml`, `lifecycle.yml`, `setup.yml`, `validate.yml`, `info.yml`/`styles.yml`, and `docker.yml`.

You edit the yml yourself, show the diff, tell the user the exact mutating command, then wait. Never run a mutating command. Read commands (`status`, `validate`, `*plan`, `info`) are lock-free — run them freely.

## 1. Full-replacement rule (load-bearing)

`workspace/deploy.yml`, `workspace/lifecycle.yml`, and `workspace/reset.yml` are **optional**. When absent, the built-in default pipeline runs (reported `ⓘ`, not an error). When present, an active override **replaces the entire pipeline section — it does NOT merge.** A half-edited override silently drops every phase you didn't copy.

That is why `dwe init` ships these as **inert, fully-commented mirrors**: the defaults stay active until you uncomment. To customize:

1. Uncomment the section.
2. Copy **every** phase you still want (not just the one you're changing).
3. Preview the resolved pipeline before handing off:

```shell
dwe deploy plan --output json
dwe reset plan --output json
```

Schema first:

```shell
dwe docs show concepts/pipelines --lang en
dwe docs show config/deploy/index --lang en
dwe docs show config/lifecycle --lang en
dwe docs show config/reset --lang en
```

## 2. Per-service `deploy.yml` — the workhorse

Prefer the **per-service** `workspace/services/<name>/deploy.yml` over a project-level `workspace/deploy.yml`. It declares how one service provisions and comes up. Shape: `phases: [{name, description, steps}]`.

A step is `type:` one of:

- `shell` — host `sh -c`, payload in `cmd:`.
- `dwe` — a dwe subcommand string in `cmd:` (e.g. `"docker up main --wait"`, `"render ide main"`).
- `command` — a declarative command ID in `cmd:` (e.g. `app.install`, `services.main.migrate.run`).
- `builtin` — an engine action in `cmd:`, payload in `with:` (e.g. `service_dirs_ensure`, `service_configs_render`).

Optional per-step keys: `name`, `description`, `when:`, `check:`, `continue_on_error: true`, `untracked: true`, `timeout:` (a general, opt-in duration string, e.g. `30s`/`2m`, bounding just the step body — absent/`0` = unbounded; only interrupts ctx-honoring bodies: `shell`/`dwe` subprocesses and ctx-aware builtins). `dwe docs show config/deploy/index#step-fields --lang en`.

**Gate the STEP, never the phase.** There is no phase-level `when:`. A coarse phase guard strands a partial install and forces a destructive `dwe reset run` to recover; per-step gates keep `dwe deploy run` re-runnable after a mid-pipeline failure (only unfinished steps execute).

`when:` and `check:` are themselves typed:

- `type: builtin` + `cmd:` a **condition predicate**: `dir-empty` · `dir-not-empty` · `dir-exists` · `dir-missing` · `file-exists` · `file-missing` · `generated-missing <svc> <field>`. **That list is the whole registry** — every verb takes arguments in the `cmd:` string (`"file-missing <path>"`), and anything else is `unknown builtin predicate` at eval time.
- `type: shell` + `cmd: "<sh test>"`.
- `type: template` + `expr: '{{ ne .Raw.vars.x "" }}'`.

**The two `type: builtin` registries are disjoint — this is the single most common authoring mistake.** The hyphenated verbs above belong to `when:` **only**; the underscored engine builtins below (`service_configs_render`, `containers_running`, `http_check`, …) belong to a step **body** and `check:` **only**. Neither side accepts the other's names: `check: {type: builtin, cmd: dir-not-empty}` and `when: {type: builtin, cmd: containers_running}` both fail. Need a `when:` the predicate registry doesn't cover? Write it as `type: shell`. Need the inverse of a shell `when:` as a `check:`? Write `check: auto`. Both registries are enumerated in full — names, kinds, one-line summaries — under **§ Builtins** of `dwe docs llms-txt --lang en`; read them there rather than guessing a verb.

**`check: auto`** — the scalar form of `check:`, resolving to the logical inverse of the step's own `when:`. Write it instead of spelling the negation out a second time (`when: "[ ! -e X ]"` + `check: "test -e X"` is one predicate written twice, and the two drift). It requires a `when: {type: shell}`; `auto` with no `when:`, with a `type: builtin` `when:` (the predicate and check-builtin registries are disjoint), or with a `type: template` `when:` (would always fail — a false template `when:` already removed the step) is a **load-time error**. Journal behaviour is identical to an explicit check (the step re-runs every deploy); migrating a step from a hand-written inverse to `check: auto` shifts the config hash and costs a one-time re-run. `dwe docs show config/deploy/conditions#check-auto-the-inverse-of-when --lang en`.

Engine **builtins** (as a step `cmd:`): `service_dirs_ensure` · `service_configs_render` · `service_configs_render_check` · `service_generated_harvest` · `source_clone` (clone a git repo into a project-relative `dir:`, **idempotent by construction** — needs no `when:`/`check:` pair; see the skeleton below) · `containers_running` · `docker_wait_healthy` · `docker_remove_project_volumes` · `docker_stop_remove_container` · `daemons_reap` · `message` · `http_check` (assert an HTTP endpoint returns an expected status / body substring, with retries — complements a `tcp_reachable` check for web stacks).

**Predicate builtins as step bodies = assertions.** A predicate builtin (`file_exists`, `executable_in_path`, `tcp_reachable`, `http_check`, `containers_running`, `env_keys_present`, `config_keys_present`, and the `shell` predicate) may be used directly as a step **body**, not only inside `check:`/`when:`. As a body it is an **assertion**: `false` fails the step with the predicate's own message, `true` succeeds — and such a step **always re-runs** (exempt from deploy's up-to-date/action-hash skip, same as a `check:` step). General across deploy/reset/lifecycle. `dwe docs show config/deploy/builtins#predicate-builtins-as-step-bodies-assertion-semantics --lang en`.

This same step schema powers isolated integration-test scenarios (`workspace/tests/*.yml`) — see `integration-tests.md` and `dwe docs show config/tests --lang en`.

### Canonical skeleton

Each step is independently idempotent and gated by filesystem truth:

```yaml
phases:
  - name: setup
    description: Prepare directories and install the application
    steps:
      - name: create-dirs
        type: builtin
        cmd: service_dirs_ensure
        with: { service: <name> }
      - name: clone               # builtin gate: never clobbers an existing checkout
        type: builtin
        cmd: source_clone
        with:
          repo: "${vars.source.repo}"
          dir: services/<name>/src
          branch: "${vars.source.branch}"
      - name: render-configs
        type: builtin
        cmd: service_configs_render
        with: { service: <name> }
        # pair the render-check so template / vars edits re-render EVERY deploy
        check:
          type: builtin
          cmd: service_configs_render_check
          with: { service: <name> }
      - name: chown-src           # service_run container as root, before stack is up
        type: command
        cmd: services.<name>.chown-src
        when:
          type: builtin
          cmd: "dir-not-empty services/<name>/src"
  - name: init
    steps:
      - name: install
        type: command
        cmd: <name>.install
        when:
          type: builtin
          cmd: "dir-empty services/<name>/src/vendor"
  - name: finalize
    steps:
      - name: up
        type: dwe
        cmd: "docker up <name> --wait"
        check:                    # self-heal: re-up if not already running
          type: builtin
          cmd: containers_running
          with: { services: [<name>] }
      - name: render-ide
        type: dwe
        cmd: "render ide <name>"
```

**One definition — reuse the command, don't retype it as a shell step.** When a phase needs
something the project already declares in `workspace/commands/**`, dispatch it with
`type: command` + `cmd: <id>` (as `install` does above). Pasting the equivalent `php artisan
…` / `npm ci` line into `deploy.yml` as `type: shell` compiles fine and passes validation,
but the two copies drift, and only the command carries the service, workdir, user, env and
compose flags — the shell step always runs on the **host**, while a service-scoped command
runs in its container. If a step is worth having in the deploy, it is worth being a command
the developer can also run by hand. (It also shows up: every step that executes on the host —
a `type: shell` step, and a `type: command` whose command is not service-scoped — is counted
in the `host_steps` field that gates unattended `dwe test run` — see `integration-tests.md`
§ 1.)

Two render-gate idioms:

- **Re-render every deploy** → pair `service_configs_render` with `check: service_configs_render_check` (template / vars edits, a cleared store, always apply).
- **Render once** → drop the check and gate the render step with `when: { type: builtin, cmd: "file-missing <path>" }`.

For a step that genuinely is "run it until the thing exists", write the predicate once as `when:` and add `check: auto` rather than repeating its negation — e.g. `when: { type: shell, cmd: "[ ! -e services/<name>/src/vendor/autoload.php ]" }` + `check: auto`.

`workspace/deploy.yml` phase shortcut: a phase carrying `deploy_services: true` inlines every enabled per-service `deploy.yml` in dependency order. Use it only when you genuinely need a project-level orchestrator.

Pointers:

```shell
dwe docs show config/deploy/steps --lang en
dwe docs show config/deploy/builtins --lang en
dwe docs show config/deploy/conditions --lang en
dwe docs show config/deploy/examples --lang en
```

See `populate-init-repo.md` for the clone-from-git flow and `render-and-vars.md` for the `${generated.*}` harvest gate (`when: generated-missing <svc> <field>` → mint → `service_generated_harvest`).

## 3. `lifecycle.yml` — pre/post hooks around run/stop/restart

`run:` and `stop:` blocks wrap the runtime lifecycle (no deploy steps). `run:` supports `show_info: true`, `final_message:`, and `phases:` (e.g. a `start` phase = `docker up --wait`, plus post-up `tools-init` phases). Best-effort steps set `continue_on_error: true`; optional-service steps gate with `when:`.

Minimal shape:

```yaml
run:
  show_info: true
  final_message: "Project is ready for work!"
  phases:
    - name: start
      steps:
        - { name: up, type: dwe, cmd: "docker up --wait" }
    - name: tools-init
      steps:
        - name: redis-insight
          when:
            type: template
            expr: '{{ (index .Services "redis-insight").Enabled }}'
          type: command
          cmd: services.redis-insight.init
          continue_on_error: true
```

**Self-update policy is NOT here.** It lives in the formalized top-level `update: { mode: on|off }` block (in `workspace.yml` / `defaults.yml`) — the former `run.update` was removed. Schema:

```shell
dwe docs show config/lifecycle --lang en
```

## 4. `setup.yml` — the first-deploy wizard

`questions: [{id, type: input|select, title, description, required, writes: vars.x, validate: {preset|regex}, options:[...]}]`. The wizard runs on the first `dwe deploy` when `workspace/local.yml` is **absent/empty**; answers merge into `local.yml` (gitignored), then deploy proceeds.

Most answers write `vars.*`; a service leaf may instead write `enabled`/`ports`/`hosts`. Example shape:

```yaml
questions:
  - id: locale-code
    type: input
    title: Store locale
    description: Locale code (xx_XX) — e.g. en_US.
    required: true
    writes: vars.app.locale.code
    validate:
      regex: "^[a-z]{2}_[A-Z]{2}$"
  - id: ide-urn
    type: select
    title: IDE URN catalog
    required: true
    writes: vars.app.ide.urn
    options:
      - { value: vscode, label: "VS Code (default)" }   # first option = default
      - { value: phpstorm, label: PhpStorm }
```

Wizard-written values that a check depends on need `stages: [post-setup]` (next section). Schema:

```shell
dwe docs show config/setup --lang en
```

## 5. `validate.yml` — project readiness checks

Host probes (docker/git/shell/ports) are hardcoded; this file adds project-specific checks. `checks: [{id, description, stages:[...], severity, services:[...], hint, type: builtin|command, cmd, with}]`.

`stages:` values: `deploy` · `run` · `stop` · `command` · `post-setup`. A value written by the setup wizard into `local.yml` must use `stages: [post-setup]` (NOT `[deploy]`) so it runs at the final preflight, after the wizard — never at the pre-wizard gate.

Builtin probes (set on `type: builtin`, `cmd:`): `tcp_reachable` · `executable_in_path` · `config_keys_present` · `shell`.

Example shape:

```yaml
checks:
  - id: marketplace-credentials-set
    description: Marketplace credentials must be set
    stages: [post-setup]
    severity: error
    hint: |
      Set vars.app.marketplace.* in workspace/local.yml,
      or run `dwe deploy` and complete the setup wizard.
    type: builtin
    cmd: config_keys_present
    with:
      keys:
        - vars.app.marketplace.username
        - vars.app.marketplace.password
```

Run the checks (read, safe even on errors):

```shell
dwe validate --output json
dwe validate checks --output json
dwe validate --stage post-setup --output json
```

Schema + guide:

```shell
dwe docs show config/validate --lang en
dwe docs show guides/preflight-checks --lang en
```

## 6. `info.yml` dashboard + `styles.yml` branding

`info.yml` — `sections:` with item types. `type: auto-urls` / `auto-hosts` expand at render time (use `port_via:`, `include`/`hide` to scope and pin scheme); `type: subgroup` groups items and can gate with `when:`; `type: definition` is templated via `.Raw.vars.x`. Example shape:

```yaml
sections:
  - id: urls
    title: URLs
    items:
      - { type: auto-urls, include: [app, tool, infra], port_via: nginx, hide: [db, redis] }
  - id: credentials
    title: Credentials
    items:
      - type: subgroup
        title: MinIO
        when: '{{ (index .Services "minio").Enabled }}'
        items:
          - { type: definition, name: User, value: "{{ .Raw.vars.minio.user }}" }
```

`styles.yml` — `header: { lines: [...], font: ogre }`, a `colors:` palette (semantic tokens: `accent`, `success`, `warning`, `danger`, `muted`, `border`, `text` — empty falls back to the built-in default), and a `separator:`. Example shape:

```yaml
header:
  lines: ["MyApp"]
  font: ogre
colors:
  accent: "#3B82F6"
  text: ""           # empty — let the terminal pick the foreground
separator: "—"
```

Preview the rendered dashboard (read):

```shell
dwe info --output json
```

Schema + guide:

```shell
dwe docs show config/info --lang en
dwe docs show config/styles --lang en
dwe docs show guides/brand-your-project --lang en
```

## 7. `docker.yml` — loaded separately (NOT in the 3-layer merge)

Pins `project_name` (**must be lowercase** — mixed-case `container_name` breaks `dwe logs` / stack resolution), declares shared external cache volumes (`resources.volumes.<name>: { name, shared: true, ensure_before: [up, deploy] }`), and can set the compose base filename (`compose.base`). Overridable per-dev via gitignored `workspace/docker.local.yml`. Example shape:

```yaml
project_name: "${project.prefix}_${project.name}"   # keep lowercase
resources:
  volumes:
    composer_cache:
      name: dwe_composer_cache
      shared: true
      ensure_before: [up, deploy]
```

Schema:

```shell
dwe docs show config/docker --lang en
```

## 8. Handoff table

After editing, hand the user the exact apply command and wait:

| Edited | Apply command (user runs) |
| --- | --- |
| per-service `deploy.yml` or `workspace/deploy.yml` | `dwe deploy run` |
| `workspace/lifecycle.yml` | `dwe run` |
| `workspace/docker.yml` (volumes / base) or a compose overlay | `dwe run` (or `dwe deploy run` if mixed) |
| `setup.yml` | read at first deploy → `dwe deploy run` (only when `local.yml` is absent) |
| `validate.yml` | verify with `dwe validate --output json`, then the normal apply command for whatever it gates |
| `info.yml` / `styles.yml` | display-only → `dwe validate`, preview with `dwe info` |
| `reset.yml` | destructive → see `snapshots-reset-troubleshoot.md` (always `dwe snapshot create` first, then `dwe reset run`) |

Mixed / unsure → `dwe deploy run` (ends in `docker up --wait`, so it covers a restart). Never recommend `dwe deploy run --force` as a clean install — `--force` only ignores prior state (`when:` still applies); a true clean install is `dwe reset run && dwe deploy run`.

Cross-links: `populate-init-repo.md` (full setup-from-git flow), `render-and-vars.md` (config render packs, generated secrets, the `vars` sandbox), `snapshots-reset-troubleshoot.md` (reset pipeline, snapshots, triage), `integration-tests.md` (the same step schema, `http_check`, and predicate-as-assertion, applied in isolated `workspace/tests/*.yml` scenarios).
