# Authoring project commands

Your project's README keeps accumulating copy-paste command snippets: "run this to seed the DB", "run this to rebuild the front-end", "run this when the queue is misbehaving". This guide walks through replacing those snippets with first-class `dwe <id>` commands the whole team can discover via `dwe commands`, run with `dwe cmd <id>`, and compose into larger workflows.

The full schema lives in [`../reference/config/commands/index.md`](../reference/config/commands/index.md); this page covers the three types you will reach for most often (`shell`, `service_exec`, `workflow`) and the directives that make them safe to ship.

## File layout and command IDs

Commands live under `workspace/commands/`. The directory path becomes the command ID, dot-separated. The file's basename is the leaf segment, and each key inside the file's `commands:` map adds another dot.

```
workspace/commands/
├── db.yml                       # group: db
└── db/
    └── seed.yml                 # group: db.seed
```

A command keyed `default` inside `workspace/commands/db/seed.yml` would be `dwe cmd db.seed.default`. More commonly, you put the core action under a descriptive key:

```yaml
# workspace/commands/db/seed.yml
group:
  title: Database seeding

commands:
  run:                            # full ID: db.seed.run
    type: shell
    description: Seed the database with development fixtures
    cmd: |
      "$DWE_BIN" shell app -c "php artisan db:seed --class=DevSeeder"
```

Run it with `dwe cmd db.seed.run`, see it under `dwe commands list`, or open the interactive browser with bare `dwe commands`.

Rule of thumb: put the core commands of a group in one file named after the group, split into a sub-directory only when there are enough commands to warrant logical sub-groups.

## `type: shell` — the simplest case

`type: shell` runs a command on the **host** through `sh -c`. Use it for git operations, host-side build steps, or any one-liner that does not need to be inside a container.

```yaml
commands:
  format:
    type: shell
    description: Run gofmt over the whole repo
    cmd: gofmt -w .
```

`cmd:` is a string passed to `sh -c`, so full shell semantics (pipes, redirects, env expansion) work. If you need to avoid the shell entirely, use `argv:` instead:

```yaml
commands:
  commit-config:
    type: shell
    description: Commit a generated config file
    argv:
      - git
      - commit
      - -m
      - "chore: regen config"
      - generated.yml
```

`cmd:` and `argv:` are mutually exclusive.

### The shell env contract

Every `type: shell` subprocess inherits three exported variables so it can reach the same compose project DWE is driving without rediscovery:

| Variable | Value |
|----------|-------|
| `DWE_BIN` | Absolute path to the running `dwe` binary |
| `COMPOSE_PROJECT_NAME` | Active compose project name |
| `COMPOSE_FILE` | Colon-joined list of active overlay paths (absolute) |

Use `"$DWE_BIN"` instead of hard-coding `./bin/dwe` — that keeps commands relocatable across machines:

```yaml
commands:
  warm-cache:
    type: shell
    cmd: |
      "$DWE_BIN" shell app -c "php artisan cache:warm"
```

`COMPOSE_PROJECT_NAME` and `COMPOSE_FILE` let `docker compose ...` invocations inside `cmd:` pick up DWE's overlay set without `-p` / `-f` flags.

## `type: service_exec` — run inside a container

`type: service_exec` runs a command inside an existing container via `docker compose exec`. Use this for application-level operations (artisan, manage.py, rails, mix, etc.) that should run against the live container.

```yaml
commands:
  db.create:
    type: service_exec
    description: Create a database in the db container
    service: db
    mode: exec-or-run
    params:
      database:
        type: string
        required: true
        pattern: ^[a-zA-Z0-9_-]+$
    env:
      MYSQL_PWD: "${vars.db.password}"
    cmd: "mariadb -u${vars.db.user} -e 'CREATE DATABASE IF NOT EXISTS `${param.database}`;'"
```

Key fields:

- **`service:`** — compose service name to target.
- **`mode:`** — what to do if the container is not running:
  - `exec-or-run` (default) — fall back to a fresh `docker compose run --rm` container and warn about the ephemeral run.
  - `exec-or-fail` — refuse with an actionable error suggesting `dwe docker up <svc>`.
  - `exec` — bare `docker compose exec`; docker emits its own error if the container is down.
  - `run` — always start a fresh container.

  Omit `mode:` for anything that works as a one-off too (composer install on a fresh checkout, etc.). Declare `mode: exec-or-fail` for tools that depend on persistent container state (databases, app servers) and must never create a container behind your back.
- **`user:`** — `current` runs as the host UID:GID (use for commands that write into bind mounts), `root` for privileged container ops, or any literal `name` / `1000` / `1000:1000`. Omit to inherit the service's `cli.user`.
- **`workdir_from:`** — dot-path into the merged config (e.g. `services.main.work_dir_internal`). Preferred over hard-coding `workdir:` because it lets `local.yml` overrides reach commands.

For one-off tools that should always start fresh (artisan tinker, php -a, irb), use `type: service_run` — same fields, but always `docker compose run --rm`.

Full reference: [`../reference/config/commands/types.md#type-service_exec`](../reference/config/commands/types.md#type-service_exec).

## `type: workflow` — compose multiple commands

A workflow stitches existing commands into one named sequence. Use it whenever you find yourself running three or four commands in the same order, or whenever a single user action ("bootstrap", "reset-and-reseed") naturally spans multiple steps.

```yaml
commands:
  bootstrap:
    type: workflow
    description: Full bootstrap — db, deps, migrate, seed
    steps:
      - command: db.start
      - command: db.create
        with:
          database: "${vars.db.database}"
      - command: composer-install
      - command: migrate
      - command: db.seed.run
        when: "{{ if .Params.seed }}1{{ else }}0{{ end }}"
        continue_on_error: true
    params:
      seed:
        type: bool
        default: true
```

Each step is one of three kinds (mutually exclusive):

- **`command: <id>`** — invoke another command. `with:` overrides its params; `when:` skips the step when the expression is falsy; `continue_on_error: true` turns a failure into a warning instead of aborting.
- **`confirm: <text>`** — prompt the user before continuing. Bypassed under `--yes` and `DWE_NONINTERACTIVE=1`.
- **`parallel:`** — fan a group of leaf command steps out concurrently. Useful for "run composer install in every app service" patterns.

### `when:` expressions

A workflow step's `when:` is rendered first, then classified:

| Form | Example | Notes |
|------|---------|-------|
| Boolean literal | `"true"`, `"1"`, `""` | Fast path after rendering |
| Builtin predicate | `file-missing services/main/vendor/autoload.php` | `dir-exists`, `dir-missing`, `dir-empty`, `dir-not-empty`, `file-exists`, `file-missing`; paths are project-root-relative |
| Shell command | `cmd: test ! -d services/main/vendor` | Evaluated via `sh -c`; exit 0 = true |

Prefer the builtin predicates when they fit — they are cheaper and don't spawn a shell.

Full reference: [`../reference/config/commands/types.md#type-workflow`](../reference/config/commands/types.md#type-workflow).

## Params: typed inputs

Every command type can declare `params:` — typed inputs the user supplies via `--set key=value` on the CLI, or that a workflow / pipeline step passes via `with:`.

```yaml
commands:
  db.create:
    type: service_exec
    service: db
    params:
      database:
        type: string                # string (default) | bool | int | path
        description: Database name to create
        required: true
        default: "app"              # literal fallback
        default_from: vars.db.database   # dot-path into merged config (preferred)
        env: DB_NAME                # exposes the resolved value as $DB_NAME
        pattern: ^[a-zA-Z0-9_-]+$   # anchored regex (string/path only)
    env:
      MYSQL_PWD: "${vars.db.password}"
    cmd: "mariadb -u${vars.db.user} -e 'CREATE DATABASE `${param.database}`;'"
```

Resolution order, top to bottom:

1. Caller-supplied value (`--set database=foo` or `with: { database: foo }`).
2. `default_from` — dot-path into the merged DWE config. Empty result is treated as missing.
3. Literal `default:`.
4. If still empty and `required: true`, error.

The `default_from` rule lets `local.yml` overrides reach commands without each developer rewriting the literal default. This is the same "config wins, code provides safety net" pattern used elsewhere in DWE.

Use the resolved value with `${param.<name>}` in `cmd:`, `argv:`, `env:`, `workdir:`, `confirmation_text:`, and file paths.

To present params as a friendly form (dropdowns, multi-select, confirm widgets) in the interactive command browser, declare `widget:` and `options:` — see [param widgets](../reference/config/commands/directives.md#param-widgets).

## Confirmation and `--yes`

Any command can require a confirmation before running:

```yaml
commands:
  db.drop:
    type: service_exec
    description: Drop a database (irreversible!)
    confirmation: true
    confirmation_text: "Drop database `${param.database}`?"
    service: db
    params:
      database:
        required: true
        default_from: vars.db.database
    env:
      MYSQL_PWD: "${vars.db.password}"
    cmd: "mariadb -u${vars.db.user} -e 'DROP DATABASE IF EXISTS `${param.database}`;'"
```

`confirmation_text:` supports `${...}` templating so you can echo back the values the user is about to act on. The prompt is bypassed in three cases:

- The user passed `--yes` / `-y` on the CLI.
- The command runs as a workflow sub-step under a parent that itself was started with `--yes`.
- A non-TTY stdin under `CI=1` (auto-confirms via the plain Y/n fallback).

For scripted use, the canonical idiom is `dwe cmd db.drop --set database=app --yes`.

## Notifications

`notify: true` opts the command into a desktop notification when it finishes (success or failure). The notification only fires when the command is the **top-level** invocation — workflows and pipelines never bubble inner `notify:` flags up to the user.

```yaml
commands:
  db.import:
    type: shell
    notify: true
    cmd: |
      "$DWE_BIN" shell app -c "php artisan db:import ${param.dump}"
```

Use this for long-running, "kick it off and switch to another window" commands (large imports, full bootstraps, snapshot pack/unpack). For sub-second commands, skip it — desktop popups for instant operations are noise.

`notify: true` on `type: daemon` is a validator error (daemons have no completion event), and on a `parallel:` sub-step it emits an info diagnostic (the runtime suppresses it anyway).

Full reference: [`../reference/config/notifications.md`](../reference/config/notifications.md).

## Visibility: `private:` and `hide:`

By default every command shows up in `dwe commands list`, the interactive browser, and tab completion. Two directives hide them:

- **`private: true`** — static developer intent. The command is removed from `commands list`, the browser, and direct invocation via `dwe cmd <id>`, but is still callable from workflows and pipelines. Use it for "step" commands that should never be run directly.

  ```yaml
  commands:
    db.up:
      type: dwe
      private: true                  # used only inside db.start workflow
      cmd: "docker up db"
  ```

- **`hide:`** — runtime condition. Same expression syntax as workflow step `when:`. The command appears when the expression is falsy and disappears when truthy. Typical use: tie commands to enabled services.

  ```yaml
  commands:
    db.engine.reset:
      type: shell
      hide: '{{ eq (index .services "db" "engine") "sqlite" }}'
      cmd: db reset --engine
  ```

  A `hide:` on the `group:` block hides the whole group and every descendant.

`private:` is appropriate for "internal plumbing" — a workflow's atomic steps that should not be exposed. `hide:` is appropriate for "only relevant in some configurations" — a command that only makes sense for one of several service engines, or only when an optional service is enabled.

## Cross-links

- [`../reference/config/commands/index.md`](../reference/config/commands/index.md) — full file structure and execution lifecycle.
- [`../reference/config/commands/types.md`](../reference/config/commands/types.md) — every command type (`shell`, `dwe`, `script`, `service_exec`, `service_run`, `workflow`, `builtin`, `daemon`) with all type-specific fields.
- [`../reference/config/commands/directives.md`](../reference/config/commands/directives.md) — every common directive in one page.
- [`../reference/config/commands/templating.md`](../reference/config/commands/templating.md) — the full `${...}` and `{{ ... }}` resolver table.
- [`background-daemons.md`](background-daemons.md) — the `type: daemon` shape for long-running background processes.
