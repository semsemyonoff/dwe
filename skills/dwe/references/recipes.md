# DWE recipes for agents

Task-to-command mappings for common DWE workflows. Load this file when the user's task matches one of the recipes below.

Rules that apply to every recipe:

- Read commands — run freely without asking: `status`, `logs`, `validate`, and `docs show` / `docs search` / `docs list` / `docs llms-txt`.
- Mutating commands — **never run yourself**; prepare the change, then show the user the exact command and wait for them to run it: `deploy run`, `run`, `stop`, `restart`, `reset run`, `services enable/disable`, AND the writing `docs` subcommands: `docs generate` (rewrites `docs/reference/`), `docs export` (writes to a target dir), `docs cache clear` (deletes cached diagrams).
- Pick the apply command by what changed:
  - **Service config** (`workspace/services/<name>/service.yml`, that service's `deploy.yml`, `configs`, `dirs`, `render` blocks) → `dwe deploy run`. The deploy pipeline installs / configures / migrates the service and ends with `docker up --wait`. The scoped form `dwe deploy run --service <name>` exists, but it only works if the service has its own `deploy.yml` and it skips the final `docker up --wait` — recommend it only when the user explicitly wants to re-run a single service's provisioning steps.
  - **Deploy orchestrator** (`workspace/deploy.yml`) → `dwe deploy run`.
  - **Runtime lifecycle** (`workspace/lifecycle.yml`) or **container shape** (the compose base — `compose.yaml` / `docker-compose.yml`, name set by `compose.base` — or an overlay) → `dwe run` (runtime lifecycle: git probe → docker up → wait → hooks, no deploy steps).
  - **Mixed changes** → run `dwe deploy run` (it ends with `docker up --wait`, so it covers a restart too).
- Always pass `--lang en` to `dwe docs ...`.
- Use `--output json` for **data-emitting** commands you need to parse (e.g. `status`, `validate`, `docs list`, `docs search`, `services`). **Do NOT** combine it with `dwe docs llms-txt` — that command's `--output` is a file path, so `--output json` would write the markdown body to a file literally named `json`. For `dwe docs show`, the global `--output json` flag is silently ignored — the command always emits markdown (use `--raw` for unrendered plain markdown when piping).

## Reference-file index

This file holds the quick daily/inspection recipes. For an authoring task, jump straight to the matching reference file:

- **Populate a fresh / `init`'d repo from git URL(s)** → `populate-init-repo.md`
- **Add an app / tool / infra service** → `add-service-and-tools.md`
- **Author a user command or background daemon** → `authoring-commands.md`
- **Render packs, the `vars` sandbox, generated secrets, `.env` exports** → `render-and-vars.md`
- **Author a pipeline** (deploy / lifecycle / reset / setup / validate / info / styles) → `pipelines-and-orchestration.md`
- **Snapshots, reset, and the read-only triage trio** → `snapshots-reset-troubleshoot.md`

## Add a new service

Quick form (full version with app/tool/infra templates, compose overlays, and `extends` → `add-service-and-tools.md`):

1. Look up the service schema before editing anything (the `services` topic is split into sub-pages):
   ```
   dwe docs show config/services/fields   --lang en   # field-by-field reference
   dwe docs show config/services/index    --lang en   # overview
   dwe docs show config/services/examples --lang en   # worked examples
   ```
2. Create `workspace/services/<name>/service.yml` with the required fields. The folder name **is** the service key — there is no separate `name:` field.
3. If the service runs a container, add it to the compose base (`compose.yaml` / `docker-compose.yml`, whichever `compose.base` names) or a per-service overlay — see `add-service-and-tools.md`.
4. Validate the config (safe — read-only):
   ```
   dwe validate --output json
   ```
5. Show the user the deploy command and ask them to run it. For adding a new service use the **full project deploy** — it inlines every enabled service's own `deploy.yml` and ends with `docker up --wait`, bringing the new container up:
   ```
   dwe deploy run
   ```
   Do NOT use `dwe deploy run --service <name>` for a brand-new service: that scoped form requires the service to already have its own `deploy.yml` (it errors with `ErrServiceNoDeployFile` otherwise) and does not run the project-orchestrator's final `docker up --wait`. Use `--service` only later, when the service has a `deploy.yml` and the user explicitly wants to re-run just that service's provisioning steps.

## Service fails to start

For the full read-only triage trio (`validate` → `status` → `logs`), deeper diagnostics (`compose argv`, `docker ps`, `deploy state show`), and the symptom→command map, see **`snapshots-reset-troubleshoot.md`**. Quick path:

1. Check current state:
   ```
   dwe status --output json
   ```
2. Read recent logs:
   ```
   dwe logs <service>
   ```
3. Look for config-level diagnostics:
   ```
   dwe validate --output json
   ```
4. If validation reports an issue, search the docs for the error keyword or field name:
   ```
   dwe docs search <keyword> --lang en
   ```
5. Propose a fix, edit the relevant yml files, then ask the user to run the apply command that matches the change:
   - Edits to a service's own files (`service.yml`, that service's `deploy.yml`, `configs`, `dirs`, `render`) or to `workspace/deploy.yml` → `dwe deploy run`. (`--service <name>` exists for re-running a single service's provisioning, but it requires that service to have a `deploy.yml` and does not run the project's final `docker up --wait` — not what you want here.)
   - Edits to `workspace/lifecycle.yml` or the compose base/overlays only → `dwe run`.
   - Not sure / mixed → `dwe deploy run` (it ends with `docker up --wait`, so it covers a restart too).

## Toggle a service on/off temporarily

1. Inspect current state: `dwe status --output json`
2. Tell the user the one-shot command (writes `workspace/local.yml` AND applies the toggle, including `on_enable`/`on_disable` hooks):
   ```
   dwe services disable <name> --apply     # or `enable`
   ```
   Without `--apply`, the change is written to `workspace/local.yml` and a pending op is recorded in `.dwe/deploy/state.yml`. Several mutating commands consume or clear pending ops (`dwe restart`, `dwe stop`, `dwe deploy run`, etc. — the exact set evolves with the CLI), and `dwe run` is **not** one of them. **Do not enumerate consumers from memory** — always run `dwe status` and read the follow-up banner it prints; that banner is the authoritative instruction. Do not run any mutating command yourself.

## Share or restore an environment snapshot

Snapshots use a dedicated scope and templating rules — the workflow is non-obvious. Read the inspection-first flow, the authoring schema, and the mutating handoffs in **`snapshots-reset-troubleshoot.md`**. In short: read first (`dwe docs show config/snapshot --lang en`, `dwe snapshot list|current|inspect --output json`), then hand the user the `dwe snapshot ...` command they need.

## Reset everything cleanly

`dwe reset run` is destructive — **do NOT run it yourself.** The full flow (read `config/reset` + `dwe reset plan` first, always `dwe snapshot create` before resetting, volume cleanup is opt-in, `--clear-generated` wipes `.dwe/generated.yml`) lives in **`snapshots-reset-troubleshoot.md`**. Hand the user `dwe reset run` (then `dwe deploy run` for a true clean install — never `deploy run --force`).

## Find which config file owns a setting

Use docs search before guessing:

```
dwe docs search <term> --lang en
dwe docs list --lang en
```

Common owners:

- Per-service: `workspace/services/<name>/service.yml`
- Project-wide deploy pipeline: `workspace/deploy.yml`
- Lifecycle hooks (stop / restart / reap): `workspace/lifecycle.yml`
- Reset behavior: `workspace/reset.yml`
- User-level overrides (enabled flags, ports, hosts, local prefs): `workspace/local.yml`
- Deferred pending operations / status follow-up banner: `.dwe/deploy/state.yml` (the deploy state journal). Inspect with `dwe deploy state show`. Do NOT edit by hand and do NOT guess the consumer — clear it only by running the command that `dwe status`'s follow-up banner names (the set evolves; trust the banner, not a memorized list).
- Display / styling / info blocks: `workspace/styles.yml`, `workspace/info.yml`
- Rendered runtime config (e.g. a service's `.env`, `env.php`): `workspace/templates/config/<svc>/` — see `render-and-vars.md`. The output file itself (`services/<svc>/src/.env`) is generated; never edit it.
- IDE / AI-agent / git-hook dotfiles (devcontainer, the generated `AGENTS.md`, hooks): `workspace/templates/{ide,ai,git}/`.
- User commands: `workspace/commands/**.yml` (path + filename + key = the dot-ID) — see `authoring-commands.md`.
- Setup-wizard prompts: `workspace/setup.yml`; project preflight checks: `workspace/validate.yml` — see `pipelines-and-orchestration.md`.
- Compose project name + shared volumes (loaded **separately**, not in the 3-layer merge): `workspace/docker.yml` (override locally in `workspace/docker.local.yml`).
- All free-form / custom values (db creds, idekeys, clone coords): the `vars:` block in `workspace/defaults.yml` — inspect with `dwe vars list|get|inspect`; see `render-and-vars.md`.
- Container shape (image, ports, env, volumes): the compose base/overlays — edit directly.

## Look up a specific config field

`dwe docs show` accepts an anchor for direct section jumps:

```
dwe docs show config/workspace#binary-overrides --lang en
```

Use `--anchors` to list every section slug in a topic before requesting a specific one:

```
dwe docs show config/services/fields --anchors --lang en
```

Use `--toc` for a level/slug/text TSV — handy for picking the right section without loading the full body.
