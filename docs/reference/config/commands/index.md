# commands/

Declarative command definitions for the DWE project.

## Contents

- [Purpose](#purpose)
- [File layout and command IDs](#file-layout-and-command-ids)
- [File structure](#file-structure)
- [Execution lifecycle](#execution-lifecycle)
- [Visibility, registration, and discovery](#visibility-registration-and-discovery)
- [End-to-end examples](#end-to-end-examples)
- [Related commands](#related-commands)
- [Further reading](#further-reading)

## Purpose

`workspace/commands/` is the home of every reusable, scriptable action the project exposes through the CLI: container shells, build steps, database operations, multi-step workflows, deploy hooks, custom scripts, etc.

Each YAML file declares one or more named commands. Commands are discovered automatically by walking the directory tree, are addressable by a dot-separated ID, and can be executed directly (`dwe commands <id>`) or referenced from workflows and pipelines (`deploy.yml`, `lifecycle.yml`).

This page is the entry point to the configuration reference. Directives, types, templating, and validation each have their own sibling page — see [Further reading](#further-reading) at the bottom.

## File layout and command IDs

The directory layout determines the **group** prefix and the file's basename determines the leaf segment. Files and subdirectories are entirely up to the project — names below are illustrative, and any number of files at any depth may exist.

```
workspace/commands/
├── <top-group>.yml              → group: <top-group>
├── …                            → any number of top-level groups
└── <parent-group>/              → optional subdirectory expands a group
    ├── <child>.yml              → group: <parent-group>.<child>
    ├── <child>/                 → optional deeper subdirectory
    │   └── <leaf>.yml           → group: <parent-group>.<child>.<leaf>
    └── …                        → any number of children, any depth
```

Concretely, a project might lay things out like this — every name here is the project's choice, not a convention enforced by the CLI:

```
workspace/commands/
├── db.yml                       → group: db
├── app.yml                      → group: app
└── services/
    ├── <service-a>.yml          → group: services.<service-a>
    ├── <service-a>/
    │   └── db.yml               → group: services.<service-a>.db
    └── <service-b>.yml          → group: services.<service-b>
```

Each command's full ID is `<group>.<name>` where `<name>` is its key inside the file's `commands:` map.

Pattern: put the **core** commands of a group in a single file named after the group (`services/<service>.yml`), and split larger groups into a sibling subdirectory only when there are enough commands to warrant logical sub-groups (`services/<service>/db.yml`, `services/<service>/cache.yml`). The subdirectory is optional — small groups stay in one file. There are no required files: a project may have zero, one, or dozens of groups at any depth.

```yaml
# workspace/commands/db.yml  →  group "db"
commands:
  cli:                          # full ID: db.cli
    type: service_exec
    ...
  dump-create:                  # full ID: db.dump-create
    type: script
    ...
```

There are no reserved file names — every `*.yml` file contributes a segment derived from its path.

## File structure

Each file has two top-level keys: an optional `group:` block of metadata, and a required `commands:` map.

```yaml
group:
  title: Database
  description: Database container management commands

commands:
  <local-name>:
    type: <type>
    description: <text>
    # ... directives below ...
```

| Field | Type | Description |
|-------|------|-------------|
| `group.title` | string | Display title shown by `dwe commands list` |
| `group.description` | string | Short description shown next to the group |
| `group.hide` | string | Optional condition expression; when truthy, hides the group and cascades to every descendant (commands and sub-groups). See [Hide condition](directives.md#hide-condition). |
| `commands` | map | Named command definitions (key = local name) |

## Execution lifecycle

Every command, regardless of type, flows through the same execution pipeline:

```mermaid
flowchart TD
    A[Resolve params] --> B[Resolve context]
    B --> C[Compute file paths]
    C --> D{Confirmation?}
    D -- yes --> E[Prompt user]
    D -- no  --> F[Prepare file effects]
    E --> F
    F --> G[Dispatch runner]
    G --> H{Success?}
    H -- yes --> I[Emit success message]
    H -- no  --> J[Run cleanups in LIFO order]
    J --> K[Emit error message]
```

Phases:

1. **Resolve params** — for each declared parameter try, in order: caller-supplied value → `default_from` (dot-path into the merged config; empty result is treated as missing) → literal `default` → required-error. Then coerce to the declared type and validate `pattern`.
2. **Resolve context** — read each `context.<key>.from` dot-path out of the merged config.
3. **Compute file paths** — render `path` / `candidates` templates, normalise to absolute, discover files. Non-mutating.
4. **Confirmation** — when `confirmation: true`, prompt the user; the prompt is bypassed only by `SkipConfirm` (set by `--yes` / `-y` and inherited by workflow children). Otherwise dispatch is by stdin: TTY → `huh.Confirm`, non-TTY → plain Y/n fallback that auto-answers "yes" when the `CI` environment variable is set (to any non-empty value). Refusal aborts the command. See [Confirmation flow](directives.md#confirmation-flow) for the full decision tree.
5. **Prepare file effects** — `mkdir`, `overwrite` checks, register cleanup callbacks.
6. **Run** — dispatch to the type-specific runner (host shell, DWE CLI, container exec/run, script, or workflow).
7. **Success / error** — emit `messages.success` or `messages.error`. On error, registered cleanups fire in LIFO order before the error message.

## Visibility, registration, and discovery

- Files under `workspace/commands/` are discovered recursively at startup.
- Each file is parsed and validated; a load failure halts startup with a structured error pointing to the file and field.
- `private: true` hides commands from `dwe commands list` and rejects direct invocation via `dwe commands`. Private commands can still be referenced from workflows and pipelines — useful for steps that should only run as part of a larger sequence.

```yaml
db.up:
  type: dwe
  private: true              # used only inside db.start workflow
  cmd: "docker up db"
```

## End-to-end examples

### A self-contained service_exec command

```yaml
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
  messages:
    success: "Database `${param.database}` is ready."
    error: "Failed to create database `${param.database}`."
  cmd: "mariadb -u${vars.db.user} -e 'CREATE DATABASE IF NOT EXISTS `${param.database}` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;'"
```

### A script command with file artefacts

```yaml
db.dump-create:
  type: script
  description: Create a database dump file
  params:
    database:
      type: string
      default_from: vars.db.database
      pattern: ^[a-zA-Z0-9_-]+$
    dump_dir:
      type: string
      default_from: vars.db.backup_dir
      required: true
      pattern: ^[^*?\[\]]+$
    dump_date:
      type: bool
      default: true
  env:
    DB_NAME: "${param.database}"
    DB_USER: "${vars.db.user}"
    MYSQL_PWD: "${vars.db.password}"
  files:
    dump:
      access: write
      path: "${param.dump_dir}/${param.database}{{ if .Params.dump_date }}_{{ now | date \"2006-01-02\" }}{{ end }}.sql.gz"
      mkdir: true
      overwrite: true
      on_error: remove
      env: DUMP_FILE
  script:
    path: workspace/scripts/db/dump-create.sh
    shell: bash
  messages:
    success: "Database dump created at ${files.dump.path}"
    error: "Failed to create database dump"
```

### A read-with-fallback file spec

```yaml
db.dump-deploy:
  type: script
  confirmation: true
  confirmation_text: "This will DROP and recreate `${param.target_database}`. Continue?"
  params:
    target_database:
      default_from: vars.db.database
      required: true
    dump_dir:
      default_from: vars.db.backup_dir
      required: true
  files:
    dump:
      access: read
      candidates:
        - glob: "${param.dump_dir}/${param.target_database}_*.sql.gz"
          match: '\d{4}-\d{2}-\d{2}'
          sort: name_desc
        - path: "${param.dump_dir}/${param.target_database}.sql.gz"
      required: true
      env: DUMP_FILE
  script:
    path: workspace/scripts/db/dump-deploy.sh
    shell: bash
```

### A workflow with conditional and confirm steps

```yaml
reset-and-bootstrap:
  type: workflow
  description: Drop, recreate, and bootstrap the main service
  steps:
    - confirm: "Drop and re-bootstrap `${vars.db.database}`?"

    - command: db.drop
      with:
        database: "${vars.db.database}"

    - command: services.main.db.create

    - command: services.main.composer-install
      when: "file-missing services/main/src/vendor/autoload.php"

    - command: services.main.migrate

    - command: optional-cache-warm
      continue_on_error: true
```

### A private composition

```yaml
# workspace/commands/db.yml
db.up:
  type: dwe
  private: true
  description: Start the database container in the background
  cmd: "docker up db"

db.wait:
  type: builtin
  private: true
  description: Wait for the db container to become healthy
  cmd: docker_wait_healthy
  with:
    services: [db]
    timeout: 120s
    interval: 2s

db.start:
  type: workflow
  private: true
  description: Start the database container and wait until healthy
  steps:
    - command: db.up
    - command: db.wait
```

`db.start` cannot be invoked directly via `dwe commands db.start`, but `bootstrap` can reference it from its `steps:`. The composition above is the canonical pattern: a thin `type: dwe` for the start, a `type: builtin` for the wait, and a `type: workflow` that strings them together.

## Related commands

- `dwe commands list` — list all public commands grouped by file
- `dwe commands <id> [--set k=v] [--yes]` — execute a command (alias: `dwe cmd <id>`)
- `dwe commands --inspect <id>` (or `-i`) — show the resolved definition (params, context, env, runner)
- `dwe docs generate` — regenerate the per-command reference under `docs/reference/commands/`

When `dwe commands` is invoked without an exact command ID on an interactive terminal, an interactive two-panel command browser opens. Its behaviour (default expansion depth, auto-collapse during fuzzy filter, type badges) is configured via the [`ui:` block in `workspace.yml`](../ui.md).

`--inspect` / `-i` is mutually exclusive with `--set` and `--yes`; it requires an exact command id and prints the definition without running it.

## Further reading

- [directives.md](directives.md) — common fields: identity, confirmation, messages, notifications, params, context, env, files
- [types.md](types.md) — the eight command types and their type-specific fields, plus workdir resolution
- [templating.md](templating.md) — render context, command-scope resolvers, template-space reference
- [validation.md](validation.md) — validation rules cheat sheet and common pitfalls
