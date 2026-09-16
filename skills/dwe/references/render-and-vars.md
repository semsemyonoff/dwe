# Render packs, generated secrets, vars & `.env`

Load when a config file is not being generated, an app secret must survive a re-render, you need to know where free-form values go, or something has to land in `.env`. You edit the source (template / var / export rule); the user runs every render/deploy. Never hand-edit a generated artifact.

## 1. Four pack kinds, two substrates

Packs live under `workspace/templates/<kind>/<pack>/`, each with a `manifest.yml` mapping template files to outputs (`to:` relative to the service hub; `src/` is the checkout).

- **`config/<svc>/`** — runtime files into the hub (`.env`, `env.php`, `config.yaml`) via the **`${...}` substrate**: `${vars.x}`, `${generated.x}`, `${services.<svc>.hosts.web}`; absent → `""`. Runs inside `dwe deploy run` (`service_configs_render`) and via `dwe render config`. Wired by `render.config.template: <pack>` in `service.yml`.
- **`ide/`, `ai/`, `git/`** — hub dotfiles (devcontainer, hub `AGENTS.md`, git hooks) via **Go templates** (`.Project.Name`, `.Service`, `.ServiceCfg.Container`, `.Commands`, `.ServiceCommandGroups`); `${...}` is **not** interpreted there. `ai` packs also declare `symlinks:` (how `CLAUDE.md` mirrors `AGENTS.md`). The shipped `default` ai pack renders a `Declared commands` block into each hub `AGENTS.md`; a project scaffolded before that block existed adopts it by pasting the snippet from `dwe docs show 'render/ai#shipped-declared-commands-block' --lang en` into its template.

Escape an app-owned `${...}` literal in a config template as `{{ "$" }}{APP_NAME}` so DWE leaves it alone.

```shell
dwe docs show render/index  --lang en    # packs overview, local overrides, collisions
dwe docs show render/config --lang en    # the ${...} substrate
dwe docs show render/ai --lang en        # also render/ide, render/git
dwe docs show templates --lang en        # template mechanics
```

## 2. Generated-secret lifecycle (harvest, never mint)

DWE never mints secrets. Either the **service** mints one and DWE harvests it once into `.dwe/generated.yml` (write-if-absent) and replays it on every render, or the developer sets one with `dwe vars set <path> --generate` (§ 4) and the template reads `${vars.*}`. The harvest flow (Laravel `APP_KEY`):

1. `service.yml`: `generated: { app_key: { file: src/.env, pattern: "^APP_KEY=(.*)$" } }` — capture group 1 is the value.
2. Template: `${generated.app_key}`.
3. `deploy.yml`: gate the mint on `when: {type: builtin, cmd: "generated-missing <svc> app_key"}` → run the service's own generator (`type: command`) → `type: builtin, cmd: service_generated_harvest, with: {service: <svc>}`.

The gate closes once the store holds the key, so it is never rotated; the render step (with `check: service_configs_render_check`) replays it every deploy. When a declared key is absent from the store, DWE **skips** that service's render rather than writing an empty value, so `reset --clear-generated` + `dwe run` never blanks a live secret. Docs: `config/services/fields#generated-block`, `config/deploy/builtins#service_generated_harvest`.

## 3. The `vars` sandbox

The config root is strict; every project-specific value nests under `vars:` in `workspace/defaults.yml` (nestable leaf tree). Three reference syntaxes, by field:

- `${vars.x}` — deploy `cmd:` / `with:` strings, command `env:` / `argv:`, config render templates.
- `from: vars.x` — structural fields: `exports.env` rules, command `params` (`default_from:`).
- `{{ .Raw.vars.x }}` — Go-template fields: `info.yml`, `when: {type: template}`.

```shell
dwe vars list [namespace] --output json
dwe vars get <var> --output json
dwe vars inspect <var> --output json     # per-layer values + every static usage site
```

The `vars.` prefix is optional on every `dwe vars` path. Schema: `dwe docs show config/vars --lang en`.

## 4. `vars set` is a handoff — and the layer is a decision

`dwe vars set <path> <value>` writes `workspace/local.yml` (this developer only). Ask: **is this value true for everyone who clones the repo?** Yes → edit `defaults.yml` yourself. No → hand off `vars set`. A machine-local value in `defaults.yml` passes validation and breaks every clean clone (teammates, CI, `dwe test run`) — e.g. a `vars.source.<svc>.branch` that exists only locally; `source_clone` is branch-blind and never re-points an existing checkout, so the local checkout and the clone coordinates are deliberately independent.

Coercion: bare `42` → int, `true` → bool, `1.5` → float; quote to force a string (`'"42"'`); `yes` / `no` / `on` / `off` stay strings; `""` → null; maps and sequences are rejected (a var is a leaf).

Secret-like var (app key, session secret, Fernet key): hand off `dwe vars set vars.app.secret_key --generate hex` (`hex[:N]` / `base64url[:N]` / `uuid`, `N` bytes, default 32; `base64url:32` is a valid Fernet key; an existing value is refused without `--force`). Never invent the value or hand over a `python -c` one-liner. A secret the whole team shares is `dwe secrets set <vars.path>` instead (`dwe docs show config/secrets --lang en`). From inside a container `set` is additionally gated by `bridge.vars_writable`.

## 5. `.env` is generated — edit the export rule

`.env` is rendered from `exports.env` in `defaults.yml`:

```yaml
exports:
  env:
    - { name: DB_DATABASE, from: vars.db.database }
    - { name: APP_PORT,    from: services.nginx.ports.http, format: int }
    - { name: DBGATE_PORT, from: services.dbgate.ports.http, format: int, when: services.dbgate.enabled }
```

Fields: `name`, `from` (dot-path into the merged config), `format` (`bool|int|string`), `when` (dot-path; falsy skips the rule entirely), `default`, `required`, `comment`. `dwe validate` warns on an unresolved `from:` / `when:` (an unresolved `from:` writes `NAME=`). `PROJECT`, `UID`, `GID`, `COMPOSE_PROJECT_NAME` are injected and may not be redeclared. Multi-line values are refused — those belong in a `render config` pack.

A host port exported `from: services.<name>.ports.<x>` or `from: vars.<path>` is what `dwe test` remaps per scenario copy — route every compose host port through one of the two (`integration-tests.md` § 5).

**Read vs write.** Bare `dwe render env` prints to stdout and touches nothing — always scoped (`| grep -E '^<NAME>='`), since the full body is the exported secret set; host-only; ignores `-o json`. `--out <path>` is the write: it resolves against the **caller's cwd**, so always hand it over with the project-root path. A rewritten `.env` reaches containers only on the next up/recreate; verify against the file, not `printenv` in a running container. Schema: `dwe docs show render/env --lang en`.

## 6. Render handoff

Renders normally run inside `dwe deploy run`. To iterate on one pack, hand over the scoped render (all mutating): `dwe render config [<svc>]`, `dwe render ide|ai|git [<svc>]`, `dwe render env --out <project-root>/.env`. `dwe render config --harvest` does not render — it stores declared `generated:` values write-if-absent (host-only).

`.env` re-renders for free inside `dwe deploy run` (implicit first step), `dwe run` / `restart`, `dwe services enable|disable`, and `dwe docker up|run|exec|restart|build` — so a **`vars`** edit followed by any of those needs no separate render. An **`exports.env`-only** edit is the exception: that block is in no config hash, so `dwe deploy run` journal-skips the render step (the built-in pipeline's always-run `up` then re-ups against the stale file). Apply it with `dwe run` or `dwe deploy run --force`.

Apply by source: `config` template / `generated:` / `service.yml` → `dwe deploy run`; `ide` / `ai` / `git` template → `dwe render <kind>` (or the next deploy if the pipeline has a render step).
