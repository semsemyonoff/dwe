# Render packs, generated secrets, vars & `.env`

Load this file when a config file isn't getting generated, when an app secret (key/crypt key) must survive a re-render, when you need to know where free-form values go, or when something has to land in `.env`. This is the config-flow half of DWE: render packs → generated-secret harvest → the `vars` sandbox → `.env` exports.

You edit the source (template / var / export rule); the **user** runs every render/deploy. Never hand-edit a generated artifact (`.dwe/**`, `.env`, `local.yml`, rendered hub files incl. `AGENTS.md`).

## 1. Four pack kinds, two substrates

Render packs live under `workspace/templates/<kind>/<pack>/`. Two different substrates:

- **`config/<svc>/`** — writes **runtime files into the service hub** via the **`${...}` substrate**. `${...}` resolves the merged config: `${vars.x}`, `${generated.x}`, `${services.<svc>.hosts.web}`. This is the pack you wire when an app needs a rendered `.env` / `env.php` / `config.yaml`. Runs inside `dwe deploy run` (via the `service_configs_render` builtin) and on demand via `dwe render config`.
- **`ide/`, `ai/`, `git/`** — write **hub dotfiles** (devcontainer, the generated `AGENTS.md`, git hooks) via **raw Go-template** with capital-letter context (`.Project.Name`, `.Service`, `.ServiceCfg.Container` — NOT the `${...}` shorthand). These are inert config — they do not see vars through `${...}`.

```shell
dwe docs show render/index  --lang en   # overview of all packs
dwe docs show render/config --lang en   # the ${...} substrate
dwe docs show render/ide    --lang en
dwe docs show render/ai     --lang en
dwe docs show render/git    --lang en
dwe docs show templates     --lang en   # template-pack mechanics
```

## 2. `manifest.yml` shape

Each pack folder has a `manifest.yml` mapping template files to outputs. `to:` is **relative to the service hub dir**; `src/` is the dir-mounted app tree (e.g. `./services/main` → `/workspace`, so `src/.env` lands at `/workspace/src/.env`).

```yaml
# workspace/templates/config/<svc>/manifest.yml
render:
  - from: env.tmpl
    to: src/.env
```

`ai` packs also support symlinks (this is how `CLAUDE.md` mirrors `AGENTS.md`):

```yaml
# workspace/templates/ai/default/manifest.yml
render:
  - { from: AGENTS.md.tmpl, to: AGENTS.md }
symlinks:
  - { link: CLAUDE.md, to: AGENTS.md }
```

**Escape for app-owned `${...}` literals.** When a template line must emit a literal `${APP_NAME}` for the app itself to expand (not for DWE to resolve), write it double-brace-quote-dollar then `{NAME}` so DWE leaves it alone:

```text
MAIL_FROM_NAME="{{ "$" }}{APP_NAME}"   # renders to the literal ${APP_NAME}
```

Schema: `dwe docs show render/config --lang en`, `dwe docs show templates --lang en`.

## 3. Generated-secret lifecycle (harvest + replay)

The engine is hermetic — it never mints secrets. The **service** generates them; DWE harvests the value once into a durable store (`.dwe/generated.yml`, write-if-absent) and replays it on every later render. Pattern (a Laravel `APP_KEY` flow):

1. **Declare** in `service.yml`: a `generated:` block with `{file, pattern}` — `pattern` is a regex whose capture group 1 is the harvested value.
   ```yaml
   # workspace/services/<svc>/service.yml
   generated:
     app_key:
       file: src/.env
       pattern: "^APP_KEY=(.*)$"
   ```
2. **Reference** the stored value in the config template as `${generated.app_key}` (absent → `""`, like every `${...}` resolver).
3. **Gate + mint + harvest** in `deploy.yml`: gate the mint step on `when: generated-missing <svc> <field>`, run the service's own mint command (e.g. `php artisan key:generate`), then a `service_generated_harvest` builtin captures it write-if-absent into `.dwe/generated.yml`:
   ```yaml
   - name: key-generate
     type: command
     cmd: services.main.key-generate          # the service mints it
     when: { type: builtin, cmd: "generated-missing main app_key" }
   - name: harvest-app-key
     type: builtin
     cmd: service_generated_harvest
     with: { service: main }
   ```

The gate closes once the store holds the key, so the secret is never rotated; `render-configs` (run after install with `check: service_configs_render_check`) replays `${generated.app_key}` every deploy.

**Safety — `reset --clear-generated` + `dwe run` must not blank secrets.** When a declared key is absent from the store, DWE **skips** that service's render rather than writing an empty value — so a cleared store followed by a plain run never erases the live secret (it is reminted on the next `dwe deploy run`).

```shell
dwe docs show config/services/fields#generated-block --lang en
dwe docs show config/deploy/conditions --lang en      # the generated-missing predicate
dwe docs show config/deploy/builtins   --lang en      # service_generated_harvest
dwe docs show render/config            --lang en
```

See `pipelines-and-orchestration.md` for the full deploy-step grammar.

## 4. The `vars` sandbox = the single free-form namespace

The merged config root is **strict** — a bare custom top-level key is a hard load error before any command runs. Every project-specific value nests under `vars:` in `workspace/defaults.yml` (a nestable, unvalidated leaf tree):

```yaml
# workspace/defaults.yml
vars:
  db:
    database: appdb
    user: root
    password: root
  source:
    repo: git@github.com:acme/app.git
    branch: main
```

**Three reference syntaxes, by field** — pick by where the value is consumed:

- `${vars.x}` — deploy `cmd:` strings, command `env:`, **config render templates**.
- `from: vars.x` — structural fields: `exports` rules, command `params` (`default_from:`).
- `{{ .Raw.vars.x }}` — Go-template fields: `info.yml`, typed `when.expr`.

Read/inspect (all safe, lock-free — run freely):

```shell
dwe vars list [namespace] --output json
dwe vars get <var>        --output json
dwe vars inspect <var>    --output json   # per-layer values + every static usage site
```

Schema: `dwe docs show config/vars --lang en`.

## 5. `vars set` is a handoff

`vars set` mutates `local.yml` — never run it. Edit the var in `defaults.yml` yourself when it's a project default; for a per-dev override, hand the user the command.

**The layer is a decision, not a formality: ask "is this value true for everyone who clones the repo?"** If not, it is a per-dev override and belongs in `local.yml` via the handed-off `vars set` — putting it in `defaults.yml` changes the tracked project default for the whole team. The failure is silent and `dwe validate` does not catch it: a `vars.source.<svc>.branch` pointed at a branch that exists only on this machine passed validation clean while every clean clone — a teammate, CI, and the project's own `dwe test run` — would fail at `source_clone`. Do not edit `defaults.yml` "for consistency" after changing something locally; a working checkout and the clone coordinates that provision it are deliberately independent (`source_clone` is idempotent and branch-blind — it never re-points an existing checkout).

**Coercion is load-bearing** (a var is always a single scalar leaf):

- bare `42` → int, `true` → bool, `1.5` → float
- quote to force a string: `'"42"'`
- maps / sequences are rejected (a var is a leaf)
- empty arg (`""`) → YAML null
- `yes` / `no` / `on` / `off` → string

```shell
# hand this to the user:
dwe vars set vars.db.user appuser
```

From inside a container, `set` is additionally gated by the top-level `bridge.vars_writable` allowlist (dot-boundary match, deny-by-default); on the host it is unrestricted. Schema: `dwe docs show config/vars --lang en`.

## 6. `.env` is generated — edit the export rule, not the file

The `.env` artifact is rendered from `exports.env` in `defaults.yml`. To surface a value in `.env`, **add/edit an export rule** — never touch `.env`. Each rule:

```yaml
# workspace/defaults.yml
exports:
  env:
    - { name: DB_DATABASE, from: vars.db.database }
    - { name: APP_PORT,    from: services.nginx.ports.http, format: int }
    - { name: USE_HTTPS,   from: runtime.use_https,          format: bool }
    - { name: DBGATE_PORT, from: services.dbgate.ports.http, format: int, when: services.dbgate.enabled }
```

Rule fields: `name`, `from` (dot-path into the **merged** config), optional `format` (`bool`|`int`|`string`), `when` (dot-path — skip if falsy), `default`, `required`, `comment`. A host port sourced `from: services.<name>.ports.<x>` is also what `dwe test` auto-remaps to a free port for every **enabled** service in the test copy, so a scenario runs alongside the live env — model host ports under `services.<name>.ports` and integration tests isolate them for free (`integration-tests.md`).

**`--out` is what makes it a write; bare `dwe render env` is a read.** Without it the resolved env goes to stdout and **`.env` is not touched** — the flag is easy to drop, and the command then looks like it worked while the stack keeps running on the old file. The bare form has no `-o` short form (`-o` is the global output-format flag) and ignores `--output json` — it always prints dotenv text. It resolves every rule against the merged config, so its unfiltered output is the project's whole exported secret set — **always scope it** (`dwe render env | grep -E '^<NAME>='`, or `grep -q` for a presence check), never print the full body into a transcript or log. It is **host-only** — the container allowlist admits `render config` and nothing else from the `render` family.

Two traps in the write form:

- **`--out` resolves against the caller's cwd**, not the project root — unlike every other dwe path. Run from `workspace/services/<name>/` it writes a stray `.env` there, exits 0, and leaves the real one stale. Always hand it over with an explicit project-root path.
- **A rewritten `.env` does not reach running containers.** Compose reads it at up/recreate, so the new value lands only on the next `dwe run` — a plain `dwe deploy run` after a manual render can answer `already up-to-date` and return before `docker up` ever runs (§ 7). Verify against the file (`grep <NAME> <project-root>/.env`); `dwe shell <svc> -c 'printenv <NAME>'` reports the container's creation-time env and stays stale until it is recreated. Never reach for `docker compose exec`: it drops dwe's `-p`/`-f` argv and resolves a different compose project.

```shell
dwe render env                            # read: prints to stdout, writes nothing
dwe render env --out <project-root>/.env  # write: regenerates the file (mutating — hand it to the user)
```

Schema: `dwe docs show render/env --lang en`.

## 7. Render handoff

Renders normally run inside `dwe deploy run`. To iterate on one pack, hand the **user** the scoped render (all mutating):

```shell
dwe render env --out <project-root>/.env  # .env only — bare `render env` writes nothing (§6)
dwe render config [<svc>]                 # the ${...} runtime files
dwe render ide|ai|git [<svc>]             # hub dotfiles
```

`.env` re-renders for free inside `dwe deploy run` (implicit first step), `dwe run` / `dwe restart`, `dwe services enable|disable` (written immediately, with or without `--apply`), and `dwe docker up|run|exec|restart|build` — **not** `docker down|stop|pull`. So a **`vars`** edit followed by any of those needs no separate render.

An **`exports.env`-only** edit is the exception, and it fails on two layers: that block is in neither the project nor the service config hash (`vars` is in both), so `dwe deploy run` answers `already up-to-date` and returns — and on a run where some other always-run step defeats that early return, the implicit `render-env` step carries no `check:`, so the journal skips it on the still-matching hash anyway. Apply that one with `dwe run` (renders unconditionally, ahead of everything else) or `dwe deploy run --force`. The scoped `render env --out` is a last resort — it rewrites the file but leaves the running stack untouched (§ 6).

`dwe render config --harvest` does NOT render — it write-if-absent stores declared `generated:` values into `.dwe/generated.yml`. It is a **host-only** mutation; never suggest it from inside a container.

Pick the apply command for the change: a `config` template / `generated:` / `service.yml` edit applies on `dwe deploy run`; an `ide`/`ai`/`git` template edit applies on `dwe render <kind>` (or the next deploy). Cross-links: `authoring-commands.md` (command `env:` / params consume `${vars.x}` / `from: vars.x`), `pipelines-and-orchestration.md` (the deploy steps that drive `service_configs_render` + `service_generated_harvest`).
