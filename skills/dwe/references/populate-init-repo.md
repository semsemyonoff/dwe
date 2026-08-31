# Populate a DWE project from git repo URL(s)

Load this file when the user supplies one or more git repo URLs, asks to "set up / scaffold / populate" a DWE project, or you detect a freshly-`init`'d repo (only `workspace.yml` plus commented scaffold). This is the end-to-end flagship flow: empty repo → working stack that clones the user's source and brings it up. You **edit YAML; the user runs every mutating command** (`init`, `deploy run`, `vars set`, …).

Sibling references you will hand off to: `add-service-and-tools.md`, `authoring-commands.md`, `render-and-vars.md`, `pipelines-and-orchestration.md`.

## Step 0 — Orient and read the schema entry points

```shell
dwe docs llms-txt --lang en
dwe docs show guides/start-a-new-project --lang en
dwe docs show concepts/project-layout --lang en
```

If a root `AGENTS.md` already exists, read it — it is the project-specific layer (authoritative for project facts). `dwe docs show` and `dwe docs llms-txt` both always emit markdown and ignore `--output json` — never pass it to either (`llms-txt` writes to a file through its own `--out PATH`).

## Step 1 — Interview the user (ask, don't assume)

Ask all eight in **one batch**, then wait for answers before authoring anything:

1. **Stack / framework?** (Laravel/PHP, Magento, Node/Nuxt, Flask/Python, Vite, static, other) — picks the framework binary to wrap and the install method.
2. **Repo URL(s)?** One app, or **mono-repo vs multi-repo** — one URL → one service, several URLs → several services.
3. **Where does source live?** Default: cloned into the **gitignored** service hub `services/<name>/src` (bind-mounted), **not** committed under `workspace/`. Confirm branch/ref per repo.
4. **Which services beyond the app?** Database (which engine), cache (redis/valkey), search (opensearch/elastic), queue/broker, reverse proxy (nginx)?
5. **Tools?** dbgate / mailpit / redis-insight / elasticvue, etc.?
6. **Commands needed?** Which framework CLI verbs to expose (`artisan migrate`, `bin/magento setup:upgrade`), any background worker (queue daemon)?
7. **Secrets to persist?** App key / crypt key — anything the framework mints that must survive re-render.
8. **Hostnames / ports?** Preferred `app.localhost` host, any fixed ports.

## Step 2 — Scaffold if needed (handoff: `dwe init`)

If the repo has no `workspace.yml` yet, hand the user the init command. It is **mutating** — never run it yourself. Safe to re-run: gap-fills missing files; `--force` overwrites. For a multi-service project, init one starter service, then add the rest by hand (Step 3).

```shell
dwe init --name <project> --prefix <prefix> --service <first-service> --default
```

After init, `workspace/{deploy,lifecycle,docker,info}.yml` are **inert fully-commented mirrors** — built-in defaults stay active until you uncomment, and uncommenting **replaces the whole pipeline section** (copy every phase you still want). `workspace/local.yml` is **not** scaffolded (created lazily by `vars set` / `services` / setup wizard). The compose base (`compose.yaml`) is comment-only. `CLAUDE.md` is a symlink to `AGENTS.md`. Schema: `dwe docs show guides/start-a-new-project --lang en`.

## Step 3 — Per repo: author the service

For each source repo, create `workspace/services/<name>/service.yml`. The **folder name is the map key** — no `name:` field. Verify the schema before writing (use `--anchors` to scope):

```shell
dwe docs show config/services/fields --lang en
dwe docs show config/services/examples --lang en
```

App service shape (edit, don't run):

```yaml
type: app
container: <name>
icon: "🧩"
required: true                  # always-on; OR omit + toggle in defaults.yml
dir: ./services/<name>
dir_internal: /workspace
work_dir_internal: /workspace/src
hosts:
  web: <name>.localhost
render:
  config:
    template: <name>            # config pack — Step 6
bridge:
  enabled: true                 # only if commands must run in-container
# generated:  declared in Step 6 if a minted secret must persist
```

**Mount the whole hub, not just the checkout.** `dir: ./services/<name>` is the **hub**: the checkout (`src/`) plus everything that lives next to it — rendered runtime configs, caches, tooling state. It mounts at `dir_internal: /workspace`, and `work_dir_internal: /workspace/src` is what puts commands inside the checkout. Pointing `dir:` straight at `src/` validates fine and then leaves the render packs writing outside the container. The triplet is identical in every service that uses it — copy it as a unit.

**A `ports:` entry is display-only on its own.** It feeds `dwe info` and the test-runner's port isolation, but nothing binds until an `exports.env` rule surfaces it (`{name: APP_PORT, from: services.<name>.ports.http}`) and the compose file interpolates `${APP_PORT}`. `PROJECT`, `UID` and `GID` are injected into `.env` automatically — do not declare export rules for them (it is a hard error).

Add the real container to a compose overlay (`compose/services/<name>.yml`) or the compose base; overlays consume generated `.env` vars and patch the proxy for the vhost. For an **optional** service add `services.<name>.enabled: true` to `workspace/defaults.yml`; for **always-on** set `required: true`. Add db/cache/search/proxy as `type: infra` / `type: tool` services the same way — full detail in `add-service-and-tools.md`. Compose assembly: `dwe docs show config/docker --lang en`, `dwe docs show concepts/docker --lang en`.

## Step 4 — Author the per-service `deploy.yml` (clone source + provision)

Source lives in the **gitignored** `services/<name>/src` — clone it via a deploy step gated so re-deploys never clobber. **Gate the step, never the phase** so `dwe deploy run` is re-runnable after a mid-pipeline failure. `when:` / `check:` blocks are themselves typed (need a `type:`). Verify:

```shell
dwe docs show config/deploy/index --lang en
dwe docs show config/deploy/steps --lang en
dwe docs show config/deploy/conditions --lang en
dwe docs show config/deploy/builtins --lang en
```

Skeleton `workspace/services/<name>/deploy.yml`:

```yaml
phases:
  - name: setup
    steps:
      - type: builtin
        cmd: service_dirs_ensure
        with: { service: <name> }
      - type: builtin
        description: clone source (idempotent — skips an existing checkout)
        cmd: source_clone
        with:
          repo: "${vars.source.repo}"
          dir: services/<name>/src
          branch: "${vars.source.branch}"
      - type: builtin
        cmd: service_configs_render
        with: { service: <name> }
        check:                                  # force re-render every deploy
          type: builtin
          cmd: service_configs_render_check
          with: { service: <name> }
  - name: init
    steps:
      - type: command
        cmd: <name>.install
        when:
          type: builtin
          cmd: "file-missing services/<name>/src/vendor/autoload.php"
  - name: finalize
    steps:
      - type: dwe
        cmd: "docker up <name> --wait"
        check:                                  # self-heal if the container died
          type: builtin
          cmd: containers_running
          with: { services: [<name>] }
      - type: dwe
        cmd: "render ide <name>"
```

Put the clone coordinates under `vars:` in `defaults.yml` (`vars.source.repo`, `vars.source.branch`) — never inline secrets/URLs as bare root keys (strict root). See `render-and-vars.md`. Preview (read): `dwe deploy plan --service <name> --output json`. Step semantics and the full builtin/predicate lists live in `pipelines-and-orchestration.md`.

## Step 5 — Author commands (framework CLI + any daemon)

One file per framework namespace; each command is `service_exec` wrapping the binary (`php artisan <verb>` / `bin/magento <verb>`). The clone/install step in Step 4 (`<name>.install`) is itself a command you author here. Verify:

```shell
dwe docs show config/commands/types --lang en
dwe docs show guides/author-project-commands --lang en
dwe docs show guides/background-daemons --lang en    # only if a worker is needed
```

Example file `workspace/commands/services/<name>/artisan.yml` — see `authoring-commands.md` for the full type zoo, params, templating, and bridge opt-in. A long-running worker uses `type: daemon` (expands to `.start` / `.logs` / `.stop` / `.restart`, auto-reaped on `dwe stop`). Inspect any command's resolved shape (read): `dwe commands -i <name>.install`.

## Step 6 — Render packs, generated secrets, vars + exports

If the app needs a rendered runtime config (`.env`, `env.php`, `config.yaml`) or a minted secret that must survive re-render, wire a `config` pack and the harvest lifecycle. Verify:

```shell
dwe docs show render/config --lang en
dwe docs show render/env --lang en
dwe docs show config/vars --lang en
```

- `workspace/templates/config/<name>/manifest.yml` → `render: [{from: env.tmpl, to: src/.env}]`; reference values as `${vars.x}` / `${generated.x}` / `${services.<svc>.hosts.web}`; escape app-owned literals as `{{ "$" }}{APP_NAME}` so DWE leaves them for the app.
- Persist a secret: declare `generated: { app_key: {file: src/.env, pattern: '^APP_KEY=(.*)$'} }` in `service.yml` (capture group 1 = harvested value); reference `${generated.app_key}` in the template; in `deploy.yml` gate the mint step on `when: { type: builtin, cmd: "generated-missing <name> app_key" }`, then run a `service_generated_harvest` builtin to write-if-absent into `.dwe/generated.yml`.
- Put **every** free-form value under `vars:` in `defaults.yml`. To surface a value in `.env`, add an `exports.env` rule (`{name, from, format?, when?}`) — never hand-edit `.env`. Inspect resolved env (read): `dwe render env` (no `--out` prints to stdout).

Full detail: `render-and-vars.md`.

## Step 7 — Validate (read, safe even on errors)

```shell
dwe validate --output json
dwe validate config services --output json
```

Fix any reported issue (edit YAML), re-validate. `validate` never executes anything.

## Step 8 — Hand off the deploy

Tell the user the exact command and what it does (clone source → render configs → install → bring the stack up):

```shell
dwe deploy run
```

`dwe deploy run` inlines every enabled per-service `deploy.yml` in dependency order and ends with `docker up --wait`. **Caveats:** the scoped `dwe deploy run --service <name>` form requires that service's own `deploy.yml` and **skips** the final stack `docker up --wait` — use it only later to re-run one provisioned service, never for a brand-new one. Never use `dwe deploy run --force` as a clean install (`--force` only ignores prior state; `when:` predicates still apply) — a true clean install is `dwe reset run && dwe deploy run`.

**The two `AGENTS.md` files after this flow.** `dwe init` writes the **root** `AGENTS.md` once — that is the project-specific layer, hand-maintained: edit it directly as the project grows (real service list, real command IDs, project-local rules). The `ai` render pack writes a **different** file into each service **hub** (`services/<name>/AGENTS.md`, alongside a `CLAUDE.md` symlink), regenerated by every `dwe render ai` / `dwe deploy run` — never edit that one in place; edit `workspace/templates/ai/<pack>/` and hand off `dwe render ai`. The hub file does not exist until the first clone creates the hub directory, which is expected, not an error.
