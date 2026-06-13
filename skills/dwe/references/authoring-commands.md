# Authoring DWE commands and daemons

Load this file when the task is "add a command", "wrap a framework CLI" (`artisan` / `bin/magento` / `npm`), "run X in the container", or "background worker / daemon". You edit the yml; the **user** runs every mutating command.

Commands live in `workspace/commands/**.yml`. Inspecting them is a read (run freely); running one (`dwe cmd <id>`) is a mutation (handoff).

## 1. ID derivation + read/inspect

ID = folder path + filename + map-key, dot-joined: `workspace/commands/<path>/<file>.yml` key `k` → ID `<path>.<file>.k`.

- `commands/services/main/cache.yml` key `clear` → `services.main.cache.clear`
- `commands/app.yml` key `install` → `app.install`

Run (mutating — **hand to the user**):

```shell
dwe cmd <id> [--set key=value]      # alias of `dwe commands run`
```

Inspect / list (read — run freely):

```shell
dwe commands -i <id>                 # resolved shape: type, service, argv, params
dwe commands list --all --output json
```

## 2. Group header

Each file may carry a `group:` header + a `commands:` map:

```yaml
group:
  title: Main Artisan
  description: Everyday php artisan utilities for the main service
  bridge: { enabled: true, services: [main] }   # children inherit (see §7)
  # hide: '<go-template>'                        # conditional visibility

commands:
  # … per-command map (keys become the last ID segment)
```

Schema → `dwe docs show config/commands/index --lang en`.

## 3. Pick the TYPE (decision tree)

`type:` is one of `service_exec · service_run · shell · script · dwe · workflow · builtin · daemon`. One short example each below; full schema → `dwe docs show config/commands/types --lang en`.

**`service_exec`** — exec into a *running* container (the everyday `php artisan` / `bin/magento` / `mariadb` wrapper). Needs `service:` + `mode:` + `workdir_from:` + `argv:`/`cmd:`. Based on `laravel/.../artisan.yml`:

```yaml
db-seed:
  type: service_exec
  description: Run database seeders (php artisan db:seed [--class=<Seeder>])
  service: app-main
  mode: exec-or-run                              # exec if up, else throwaway run
  workdir_from: services.main.work_dir_internal
  params:
    class: { type: string, description: Seeder class, pattern: '^[A-Za-z][A-Za-z0-9_]*$' }
  cmd: "php artisan db:seed{{ with .Params.class }} --class={{ . }}{{ end }}"
```

**`service_run`** — throwaway container, runs even before the stack is up (pre-up `chown` as `user: root`, one-shot installs):

```yaml
chown-src:
  type: service_run
  private: true
  service: app-main
  user: root
  workdir_from: services.main.work_dir_internal
  argv: [sh, -c, "chown -R www-data:www-data ."]
```

**`shell`** — host `sh -c` glue (inherits `DWE_BIN`, `COMPOSE_PROJECT_NAME`, `COMPOSE_FILE`):

```yaml
auth-json:
  type: shell
  cmd: "printf '%s' \"$AUTH_JSON\" > ~/.composer/auth.json"
```

**`script`** — runs `workspace/scripts/**.sh` with declarative `files:` resolution + `env:` (nested maps reach the script as `DWE_CONTEXT_JSON`):

```yaml
dump-create:
  type: script
  env: { DB_USER: "${vars.db.user}", MYSQL_PWD: "${vars.db.password}" }
  files:
    dump: { access: write, path: '${param.dir}/db.sql.gz', mkdir: true, env: DUMP_FILE }
  script: { path: workspace/scripts/db/dump-create.sh, shell: bash }
```

**`dwe`** — wraps a top-level `dwe` subcommand string so pipelines/workflows can reuse it:

```yaml
up:
  type: dwe
  private: true
  cmd: "docker up db"
```

**`workflow`** — sequences other command IDs; supports `parallel:`, `when:`, `continue_on_error`, `always_show_output`:

```yaml
bootstrap:
  type: workflow
  steps:
    - command: db.start
    - command: services.main.composer-install
    - command: services.main.migrate.run
```

**`builtin`** — engine action, payload in `with:`:

```yaml
wait:
  type: builtin
  private: true
  cmd: docker_wait_healthy
  with: { services: [db], timeout: 120s, interval: 2s }
```

**`daemon`** — long-running worker; expands into FOUR virtual IDs `<group>.<key>.{start,logs,stop,restart}`, auto-reaped on `dwe stop`. Based on `laravel/.../services/main.yml`:

```yaml
queue:                                            # → services.main.queue.{start,logs,stop,restart}
  type: daemon
  service: app-main
  user: www-data
  workdir_from: services.main.work_dir_internal
  params:
    name: { type: string, default: "default", pattern: '^[a-zA-Z0-9_-]+$' }
  argv: [php, artisan, "queue:listen", --timeout=0, "--queue=${param.name}"]
  daemon:
    container_template: "queue_${param.name}"
    on_already_running: error
    auto_remove: true
    stop_timeout: 60s
```

Multi-instance: `dwe cmd services.main.queue.start --set name=emails`. Guide → `dwe docs show guides/background-daemons --lang en`.

## 4. Params

`params:` is a map; each entry is `{type: string|int|bool|path, description, required, default, default_from: vars.x, pattern, widget: select, options: [...]}`.

Resolution order at run time: caller **`--set k=v`** → **`default_from`** (a `vars.*` dot-path) → **`default`** → error if `required:`.

Secrets do **not** go in params (params land in a docker label) — use `env:` (§6). Schema → `dwe docs show config/commands/validation --lang en`.

## 5. Three templating substrates

Pick by who resolves the value (full rules → `dwe docs show config/commands/templating --lang en`):

| Syntax | Resolved by | Use in |
| --- | --- | --- |
| `${vars.x}` / `${param.x}` | DWE, **before** exec | `argv:` items, `cmd:`, `env:`, `messages:` |
| `{{ .Params.x }}` / `{{ with .Params.x }}…{{ end }}` | Go-template, before exec | inside a `cmd:` string |
| `$VAR` (no braces) | left **for the shell** | inside a `shell` `cmd:` |

**Prefer `argv:` over `cmd:`** for anything with SQL or quoting — `argv` is a list, so no shell re-parsing. Reserve `cmd:` for simple strings or Go-template conditionals.

## 6. Directives

- `private: true` — pipeline/workflow-only; not runnable via `dwe cmd`.
- `confirmation: true` + `confirmation_text: "…"` — interactive prompt before running.
- `env:` — environment for the process; **secrets go ONLY here** (e.g. `MYSQL_PWD: "${vars.db.password}"`), never in params.
- `messages: { success, error }` — user-facing result lines.
- `files:` — declarative file resolution (read/write candidates, globs, `env:` binding) for `script` commands.
- `notify:` — desktop notification on completion.

Schema → `dwe docs show config/commands/directives --lang en`.

## 7. Bridge opt-in (optional)

Container-reachability is **opt-in, default-deny**. To let a command run from inside a service container, add a `bridge:` block on the command or the group header (children inherit; per-field override wins):

```yaml
bridge: { enabled: true, services: [main] }       # services = workspace service KEYS
```

Keep interactive (`tinker`), secret-minting, and daemon commands **host-only** (`bridge: { enabled: false }`). The service must also carry `bridge.enabled: true` in its `service.yml`. Concept → `dwe docs show concepts/bridge --lang en`.

## 8. Port a framework CLI (the namespace pattern)

One file per framework namespace; every command is a `service_exec` wrapping the binary verb.

- `commands/services/main/migrate.yml` → `services.main.migrate.{run,status,rollback,…}` each `argv: [php, artisan, "migrate:…"]`
- `commands/services/magento/indexer.yml` → `services.magento.indexer.{reindex,…}` each `argv: [bin/magento, "indexer:…"]`

Set the namespace's `bridge:` + `service:` once on the group header; the per-command entries inherit. Pattern guide → `dwe docs show guides/author-project-commands --lang en`.

## 9. Verify (read) + handoff

Verify the resolved shape before handing off:

```shell
dwe commands -i <id> --output json
dwe validate commands --output json
```

Then apply by **role**:

- **Pipeline-referenced** commands (used in a `deploy.yml`/`lifecycle.yml` step as `type: command`) apply when the **user** runs the matching pipeline: `dwe deploy run` (deploy steps) or `dwe run` (lifecycle hooks).
- **Standalone** commands are run by the **user** via `dwe cmd <id> [--set k=v]` — a mutation; never run it yourself. Edit the yml → show the diff → tell the user the exact command → wait.

Cross-links: pipeline wiring (`type: command` steps, `deploy_services`) → `pipelines-and-orchestration.md`. Vars sandbox, `default_from: vars.x`, and `${generated.x}` in command env → `render-and-vars.md`.
