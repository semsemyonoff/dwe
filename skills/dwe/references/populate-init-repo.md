# Populate a DWE project from git repo URL(s)

Load when the user supplies git repo URL(s), asks to "set up / scaffold / populate" a DWE project, or you detect a freshly-`init`'d repo. End-to-end: empty repo → working stack that clones the user's source and brings it up. You edit YAML; the user runs every mutating command.

## Step 0 — Orient

```shell
dwe docs llms-txt --lang en
dwe docs show guides/start-a-new-project --lang en
dwe docs show concepts/project-layout --lang en
```

Read a root `AGENTS.md` if present.

## Step 1 — Interview (one batch, then wait)

1. **Stack / framework?** (Laravel, Magento, Node/Nuxt, Flask, Vite, static, …) — picks the CLI to wrap and the install method.
2. **Repo URL(s)?** One URL → one service; several → several services. Branch/ref per repo.
3. **Source location?** Default: cloned into the gitignored hub `services/<name>/src`, not committed under `workspace/`.
4. **Backing services?** Database (engine), cache, search, queue, reverse proxy.
5. **Tools?** dbgate / mailpit / redis-insight / …
6. **Commands?** Framework verbs to expose, any background worker.
7. **Secrets to persist?** Anything the framework mints that must survive a re-render.
8. **Hostnames / ports?** Preferred `*.localhost` host, fixed ports.

## Step 2 — Scaffold (handoff: `dwe init`)

If no `workspace.yml` yet, hand over (safe to re-run: gap-fills; `--force` overwrites):

```shell
dwe init --name <project> --prefix <prefix> --service <first-service> --default
```

Result: `workspace.yml`, `workspace/{defaults,styles,docker}.yml`, one starter `services/<name>/{service.yml,deploy.yml}`, a root `AGENTS.md` (+ `CLAUDE.md` symlink), the `ai` render pack, and `workspace/tests/smoke.yml`. `workspace/{deploy,lifecycle,info}.yml` and the service `deploy.yml` are **inert commented mirrors** (uncommenting replaces the whole section); `smoke.yml` and the ai pack ship **active**; `compose.yaml` is comment-only; `local.yml` is created lazily. For several services, init one and add the rest by hand.

## Step 3 — Per repo: the service

Create `workspace/services/<name>/service.yml` (folder name = key). Verify fields first: `dwe docs show config/services/fields --lang en` (`--anchors` to scope), `config/services/examples`.

```yaml
type: app
container: <name>
icon: "🧩"
required: true                  # or omit + `services.<name>.enabled` toggle in defaults.yml
dir: ./services/<name>          # the hub — mount it whole (SKILL.md § Rules)
dir_internal: /workspace
work_dir_internal: /workspace/src
hosts:
  web: <name>.localhost
ports:
  http: 8080                    # display + test isolation; an exports.env rule binds it (render-and-vars.md § 5)
# render: { config: { template: <name> } }   # Step 6, only if runtime files are rendered
# generated: …                                # Step 6, only if a minted secret must persist
```

Add the container to the compose base or an overlay (`compose/services/<name>.yml`); overlays consume `.env` vars and patch the proxy vhost. Add db/cache/search/proxy as `type: infra` / `tool` the same way — `add-service-and-tools.md`.

## Step 4 — Per-service `deploy.yml`

Use the canonical skeleton in `pipelines-and-orchestration.md` § 2: `service_dirs_ensure` → `source_clone` (self-gating — no `when:` / `check:`) → optional `service_configs_render` + check → `docker build` → install via a **declared command** gated by `file-missing` → `docker up <name> --wait` with `check: containers_running`. Clone coordinates go under `vars.source.<name>.{repo,branch}` in `defaults.yml`. Preview: `dwe deploy plan --service <name> --output json`.

## Step 5 — Commands

One file per framework namespace, each entry a `service_exec` wrapping the binary; the install step's command (`services.<name>.install`) is authored here. A worker is `type: daemon`. See `authoring-commands.md`; verify with `dwe cmd -i <id> --output json`.

## Step 6 — Render packs, generated secrets, vars, exports

Only if the app needs a rendered runtime config or a minted secret that must survive re-render: wire a `config` pack (`workspace/templates/config/<name>/manifest.yml` → `{from: env.tmpl, to: src/.env}`), reference `${vars.x}` / `${generated.x}`, declare `generated:` + the harvest steps. A secret the app does not mint is a `dwe vars set <path> --generate hex` handoff. Every free-form value goes under `vars:`; surface values in `.env` with `exports.env` rules. Full detail: `render-and-vars.md`.

## Step 7 — Validate (read)

```shell
dwe validate --output json
dwe validate tests --output json     # smoke.yml is active; add an http_check once the app answers
```

Fix, re-validate. `validate` executes nothing.

## Step 8 — Hand off the deploy

```shell
dwe deploy run
```

It inlines every enabled service's `deploy.yml` in dependency order and ends with `docker up --wait`. Not `--service <name>` for a new service, not `--force` as a clean install (`SKILL.md` § Picking the apply command). The hub `AGENTS.md` appears only after the first clone creates the hub and a `render ai` step runs — expected, not an error.
