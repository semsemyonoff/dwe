# Authoring DWE commands and daemons

Load when the task is "add a command", "wrap a framework CLI" (`artisan` / `bin/magento` / `npm`), "run X in the container", or "background worker / daemon". You edit the yml; running a command that mutates is a handoff (`SKILL.md` § Permission boundary).

Commands live in `workspace/commands/**.yml`. Schema: `dwe docs show config/commands/index --lang en` (sub-pages `types`, `directives`, `templating`, `validation`); pattern guide: `dwe docs show guides/author-project-commands --lang en`.

## 1. IDs and inspection

ID = folder path + filename + map key, dot-joined: `commands/services/main/cache.yml` key `clear` → `services.main.cache.clear`; `commands/app.yml` key `install` → `app.install`.

```shell
dwe commands list [group] --output json   # id, type, description, service, params (--all adds private)
dwe cmd -i <id> --output json             # resolved shape: type, service, argv, params, confirmation
dwe cmd <id> [--set key=value] [-- args]  # run (`dwe cmd` is an alias of `dwe commands`)
```

## 2. File shape

```yaml
group:
  title: Main Artisan
  description: Everyday php artisan utilities for the main service
  bridge: { enabled: true, services: [main] }      # optional; children inherit (§ 6)
  # hide: '{{ not .Raw.services.main.enabled }}'   # conditional visibility

commands:
  # per-command map; keys become the last ID segment
```

The first non-empty line of a command's or group's `description:` is its summary — the only text one-line surfaces show (run banner, listing, completion, llms-txt), so write that line first and put usage notes after it in a `|` block (`>` folds everything into one line); JSON output carries both `description` and `summary`.

One file per framework namespace (`commands/services/main/migrate.yml` → `services.main.migrate.{run,status,rollback}`), each entry a `service_exec` wrapping the binary verb; set `service:` / `bridge:` once on the group header.

## 3. Pick the type

`type:` is one of `service_exec · service_run · shell · script · dwe · workflow · builtin · daemon`. Full schema: `dwe docs show config/commands/types --lang en`.

**`service_exec`** — exec into a *running* container; the everyday wrapper. Needs `service:` + `argv:`/`cmd:`. Defaults: `mode: exec-or-run`; `workdir` and `user` fall back to the service's own `cli:` block (declare either only to override). A container TTY is granted only to a user-launched terminal invocation — pipeline steps, `check:` probes and piped output run with `-T`, so keep the output parseable.

```yaml
db-seed:
  type: service_exec
  description: Run database seeders
  service: app-main
  params:
    class: { type: string, description: Seeder class, pattern: '^[A-Za-z][A-Za-z0-9_]*$' }
  cmd: "php artisan db:seed{{ with .Params.class }} --class={{ . }}{{ end }}"
```

**`service_run`** — throwaway container; works before the stack is up (pre-up `chown` as `user: root`, one-shot installs):

```yaml
chown-src:
  type: service_run
  private: true
  service: app-main
  user: root
  argv: [sh, -c, "chown -R www-data:www-data ."]
```

**`shell`** — host `sh -c` glue; inherits `DWE_BIN`, `COMPOSE_PROJECT_NAME`, `COMPOSE_FILE`. Counts as a host step for the `dwe test run` gate.

**`script`** — runs `workspace/scripts/**.sh` with declarative `files:` resolution and `env:` (nested maps reach the script as `DWE_CONTEXT_JSON`):

```yaml
dump-create:
  type: script
  env: { DB_USER: "${vars.db.user}", MYSQL_PWD: "${vars.db.password}" }
  files:
    dump: { access: write, path: '${param.dir}/db.sql.gz', mkdir: true, env: DUMP_FILE }
  script: { path: workspace/scripts/db/dump-create.sh, shell: bash }
```

**`dwe`** — wraps a `dwe` subcommand string (`cmd: "docker up db"`) so pipelines and workflows can reuse it.

**`workflow`** — sequences other command IDs (`steps: [{command: db.start}, …]`); supports `parallel:`, `when:`, `continue_on_error`, `always_show_output`.

**`builtin`** — engine action or predicate, payload in `with:` (`cmd: docker_wait_healthy`, `with: {services: [db], timeout: 120s}`).

**`daemon`** — long-running worker; expands into `<id>.{start,logs,stop,restart}`, auto-reaped on `dwe stop`:

```yaml
queue:
  type: daemon
  service: app-main
  params:
    name: { type: string, default: "default", pattern: '^[a-zA-Z0-9_-]+$' }
  argv: [php, artisan, "queue:listen", --timeout=0, "--queue=${param.name}"]
  daemon:
    container_template: "queue_${param.name}"
    on_already_running: error
    auto_remove: true
    stop_timeout: 60s
```

Multi-instance: `dwe cmd services.main.queue.start --set name=emails`. Guide: `dwe docs show guides/background-daemons --lang en`.

## 4. Params

`params:` map; each entry `{type: string|int|bool|path, description, required, default, default_from: vars.x, pattern, widget: select, options: [...]}`. Resolution: `--set k=v` → `default_from` → `default` → error if `required`. `dwe validate` warns (domain `commands`) on a `default_from` / `options.from` / `context.<name>.from` dot-path that does not resolve.

Secrets do **not** go in params (they land in a docker label) — use `env:`. Schema: `dwe docs show config/commands/validation --lang en`.

## 5. Templating and directives

| Syntax | Resolved by | Use in |
| --- | --- | --- |
| `${vars.x}` / `${param.x}` / `${args}` | DWE, before exec | `argv:` items, `cmd:`, `env:`, `messages:` |
| `{{ .Params.x }}` / `{{ with .Params.x }}…{{ end }}` | Go template, before exec | inside a `cmd:` string |
| `$VAR` | left for the shell | a `shell` `cmd:` |

Prefer `argv:` over `cmd:` for anything with quoting or SQL — a list is not re-parsed by a shell. Full rules: `dwe docs show config/commands/templating --lang en`.

Directives (`dwe docs show config/commands/directives --lang en`):

- `private: true` — pipeline/workflow-only, not runnable via `dwe cmd`, listed only with `--all`. This is how a test-only helper (seed fixtures, dump to a fixed filename) stays off the everyday listing — use `private`, not `hide` (pipelines skip `hide` commands).
- `confirmation: true` + `confirmation_text:` — prompt before running; `-y` skips it.
- `env:` — process environment; **secrets go only here** (`MYSQL_PWD: "${vars.db.password}"`).
- `${args}` in `argv:`/`cmd:` — pass-through arguments after `--`.
- `argv_append_from: "<host shell expr>"` — appends the expression's stdout lines to `argv:` as elements; the idiom for a staged-files linter (`argv: [ruff, check]` + `argv_append_from: "git -C services/app/src diff --cached --name-only …"`). Requires `argv:`; runs on the host even for a container command; empty output skips the command with exit 0.
- `messages: {success, error}`, `notify:`, `files:` (declarative file resolution for `script` commands).

## 6. Bridge opt-in (optional)

Running a command from **inside** a service container is opt-in, default-deny: add `bridge: { enabled: true, services: [<service keys>] }` on the command or group header, and set `bridge.enabled: true` in the service's `service.yml`. Keep interactive, secret-minting and daemon commands host-only. Concept: `dwe docs show concepts/bridge --lang en`.

## 7. Verify and hand off

```shell
dwe cmd -i <id> --output json
dwe validate commands --output json
```

A command referenced from a pipeline step (`type: command`) applies when the user runs that pipeline (`dwe deploy run` / `dwe run`); a standalone command is run by the user via `dwe cmd <id>` — or by you when the task it carries only reads or verifies (`SKILL.md` § Running project tasks).

Cross-links: pipeline wiring → `pipelines-and-orchestration.md`; `${vars.*}` / `default_from` → `render-and-vars.md`; calling a `private` command from a test scenario → `integration-tests.md`.
