---
name: dwe
description: Use when the current working directory is inside a DWE project — a Docker-based developer environment manager. Detect one by walking up from cwd to a directory containing `workspace.yml`; that directory is the project root. A populated project also has a `workspace/` subdirectory (services, pipelines), but its absence does not disqualify it (e.g. a freshly-initialized project). Applies anywhere beneath the root, including service folders like `workspace/services/<name>/` and their source subtrees. The skill is both a navigator — which `dwe` commands to use for inspection vs mutation, and how to look up everything else via the built-in `dwe docs` subsystem — and an authoring guide for extending a project (scaffolding from git repos, adding services and tools, authoring commands and daemons, wiring render packs and vars, customizing deploy/lifecycle pipelines, integration tests). Activates on the word "dwe", on editing any file under `workspace/`, or when working inside a service directory.
---

# DWE — Dev Workspace Engine

DWE is a CLI that orchestrates Docker-based local development environments on top of a project's compose file — configuration layering, lifecycle, validation, declarative commands. It does **not** replace compose; edit the compose file freely.

This skill is a **navigator with an authoring layer**, not a reference. It says **which** command to run, **which** file to edit and **in what order**. Every schema, field and deep behaviour lives in the built-in docs, versioned with the binary: `dwe docs show <topic> --lang en`. Look things up there instead of guessing.

## Detecting a DWE project

Walk up from cwd until a directory contains `workspace.yml` — that is the root. Every `dwe` command resolves the root itself; run it from the root or any descendant. A project with **only** `workspace.yml` plus commented scaffold files is a fresh `dwe init` — see `references/populate-init-repo.md`.

## First step: orient yourself

Once per session, inside the project:

```shell
dwe docs llms-txt --lang en
```

It emits a project-aware index (services, live command list, both builtin registries, template syntax by site, doc topics). Read it before anything else.

**If a root `AGENTS.md` exists, read it too.** It is the project-specific layer (real services, real command IDs, project rules); this skill is the generic layer. On a project fact the `AGENTS.md` wins; on a generic rule the skill wins.

Two different `AGENTS.md` files exist — do not confuse them:

| File | Origin | Edit? |
| --- | --- | --- |
| **root** `AGENTS.md` (next to `workspace.yml`) | written once by `dwe init`, then hand-maintained | **yes**, directly |
| **hub** `AGENTS.md` (inside a service hub, `services/<name>/`) | **generated** by the `ai` render pack (`dwe render ai`, or a `render ai` deploy step) | **no** — edit `workspace/templates/ai/<pack>/`, hand off `dwe render ai` |

Both have a `CLAUDE.md` symlink beside them; the generated one says so in its footer.

## Project anatomy

- **3-layer config**, later wins, maps deep-merge: `workspace.yml` (identity: `project`, `update`, `compose`, `secrets`) → `workspace/defaults.yml` (tracked: `services` toggles, `runtime`, `vars`, `exports`, `bridge`) → `workspace/local.yml` (gitignored per-dev overrides; tool-written). The root is **strict** — free-form values live **only** under `vars:`; a bare custom root key is a hard load error. A `vars.*` value may be an `ENC[age:…]` marker (committed secret, decrypted at load) → `config/secrets`.
- **`workspace/docker.yml`** — compose project name, shared volumes, compose base filename. Loaded **separately**, not in the 3-layer merge; per-key override, per-dev in `docker.local.yml` → `config/docker`.
- **Services = folders**: `workspace/services/<name>/service.yml`; the folder name **is** the key (no `name:` field). The container itself lives in the compose base or an overlay. Optional per-service `deploy.yml` / `reset.yml`.
- **User commands**: `workspace/commands/**.yml`; path + filename + key = dot-ID; run with `dwe cmd <id>`.
- **Render packs**: `workspace/templates/{config,ide,ai,git}/` — `config` writes runtime files into the service hub; `ide`/`ai`/`git` write hub dotfiles (devcontainer, hub `AGENTS.md`, git hooks).
- **Pipelines (optional, full-replacement)**: `workspace/{deploy,lifecycle,reset}.yml`. Absent = built-in default (reported `ⓘ`, not an error). Also `setup.yml` (first-deploy wizard), `validate.yml` (project checks), `snapshot.yml`, `info.yml` / `styles.yml` (dashboard, branding), `i18n/`.
- **Integration tests**: `workspace/tests/<scenario>.yml` (name = basename; deploy step schema).
- **Generated, never hand-edited**: `.env`, `.dwe/**` (deploy journal, `generated.yml`, logs, test runs), `workspace/local.yml`, rendered hub files.

Layout reference: `dwe docs show concepts/project-layout --lang en`.

## Rules no validator will catch

`dwe validate` checks shape, not judgement. These pass validation either way and are what a fresh project gets wrong:

- **Two disjoint registries share the word `builtin`.** `when: {type: builtin}` takes the seven **condition predicates** (`dir-empty` · `dir-not-empty` · `dir-exists` · `dir-missing` · `file-exists` · `file-missing` · `generated-missing <svc> <field>`). A step body and `check:` take the **step builtins** (`source_clone`, `service_configs_render`, `containers_running`, `http_check`, …). Neither accepts the other's names. Need a `when:` the predicates lack → `type: shell`; need its inverse as a `check:` → `check: auto`. Both registries with kinds and summaries: `dwe docs llms-txt --lang en` § Builtins.
- **Mount the whole hub, not just `src/`.** `dir: ./services/<name>` + `dir_internal: /workspace` + `work_dir_internal: /workspace/src`, as a unit. Pointing `dir:` at the checkout leaves rendered configs, caches and tooling state outside the container.
- **One definition.** A deploy step that runs something the project already declares as a command uses `type: command` + `cmd: <id>`, never a pasted `type: shell` copy — the copies drift, and only the command carries service/workdir/user/env (a shell step runs on the **host**).
- **A `ports:` entry in `service.yml` is display-only** until an `exports.env` rule surfaces it (`{name: APP_PORT, from: services.<name>.ports.http}`) and compose interpolates that var. `PROJECT`, `UID`, `GID`, `COMPOSE_PROJECT_NAME` are injected into `.env` automatically and may not be redeclared; the compose project name is set through `docker.yml` `project_name`, always lowercased.

## Output conventions

- **Always `--lang en` for docs** (`llms-txt|search|show|list`); translations may lag.
- **`--output json` (`-o json`) for data you parse**: `status`, `validate`, `services list`, `vars get|list|inspect`, `snapshot list|inspect`, `info`, `logs`, `commands list`, `docs list|search`, `test list|run|clean`, `deploy plan`, `secrets status`.
- **Read the envelope keys before indexing** — a wrong key returns empty with exit 0. `status` is `{project, apps, tools, infra, daemons, deploy, topology, git}` (no top-level `services`); `info` is `{title, sections[]}`; `validate` is `{summary, diagnostics[]}`; `commands list` is `{commands[]}` with `id`, `type`, optional `group`, `description` (localized — match on `id`), `service`, `params`; `deploy plan` is `{phases[{name, steps[{name, type, cmd, unresolved[], when, check, …}]}]}`.
- **Exceptions**: `docs show` and `docs llms-txt` always emit markdown and ignore `-o json` (`docs show --raw` when piping; `llms-txt --out PATH` writes a file). `deploy state show` is always YAML. `render env` is always dotenv text.
- **Scope a doc** instead of reading it whole: `dwe docs show <topic>#<anchor>`, `--anchors`, `--toc`.
- **Bare `dwe commands` / `dwe docs` / `dwe status` / `dwe vars` open a TUI on a terminal** and fall back to plain output in a pipe. Always call the explicit read forms (`commands list`, `docs list|show|search`, `status --output json`, `vars list`).

## When to use what

| Goal | Command / reference |
| --- | --- |
| Project overview (start here) | `dwe docs llms-txt --lang en` |
| **Run a project task** (tests, lint, migrate, …) | `dwe commands list --output json` → `dwe cmd <id>`; `dwe cmd -i <id> --output json` first if unfamiliar — see **Running things** |
| **One-off command in a service container** | `dwe shell <service> -c '<cmd>'` |
| Inspect state / logs / config | `dwe status --output json` · `dwe logs <service> --output json` · `dwe validate --output json` |
| Search docs / read a topic | `dwe docs search <term> --lang en` · `dwe docs show <topic> --lang en` |
| Read vars / set a var | `dwe vars get\|list\|inspect <var> --output json` · handoff `dwe vars set <path> <value>` (writes `local.yml`); secret-like → `--generate hex[:N]\|base64url[:N]\|uuid` |
| Encrypted-secret inventory | `dwe secrets status --output json` (read-only, exit 0 even when nothing decrypts; `identity.reason` / `identity.hint` name the fix) |
| **Populate a fresh repo from git URL(s)** | `references/populate-init-repo.md` |
| **Add a service / tool / infra** | `references/add-service-and-tools.md` |
| **Author a command or daemon** | `references/authoring-commands.md` |
| **Render packs / vars / `.env` / generated secrets** | `references/render-and-vars.md` |
| **Author a pipeline** (deploy / lifecycle / setup / validate / info / styles / docker.yml) | `references/pipelines-and-orchestration.md` |
| **Integration test / verify a clean deploy** | `references/integration-tests.md` |
| **Snapshot / reset / troubleshoot** | `references/snapshots-reset-troubleshoot.md` |
| Apply a change | **Picking the apply command** below — edit yml, then ASK the user to run it |

## Running things

Most work in an existing project is running its own tasks. Two commands, not interchangeable:

**`dwe cmd <id>` — a task the project declares.** Prefer it: it carries the right service, workdir, user, env and compose flags. Find IDs with `dwe commands list --output json` (entries carry `id`, `description`, `service`; match on `id`, the description follows the project locale). `dwe cmd -i <id> --output json` shows what it runs, whether it takes `--set key=value` params or `${args}` pass-through after `--` (`dwe cmd site.test -- --run x.test.ts`), and its `confirmation:` flag. A command that rejects extra arguments has no `${args}` slot — the error names the one-line fix; adding it beats working around it.

**`dwe shell <service> -c '<cmd>'` — anything not declared.** Legitimate escape hatch; repetition is the signal it wants to be a declared command — say so rather than running it a third time.

- **Check the registry before any task a declared command may cover** — tests, linters, formatters, builds, codegen, migrations, seeds, cache/token management, package-manager scripts. Once per session, again after `workspace/commands/` changes. Do not run the underlying npm/composer/make/docker command until that check is made.
- **Scoped first, full before giving up.** A hub `AGENTS.md` may carry a `Declared commands` block naming the service's groups (`dwe commands list <group> --output json`). A scoped listing that finds nothing is not proof of absence: project-wide and workflow commands declare no `service:` — run one full listing before falling back to `dwe shell`. Block counts are *declared*; a `hide:` can shorten the live listing.
- **Long-running command → `dwe shell … --tty`** (`dwe cmd` has no such flag). Without it the child's stdout is a pipe, block-buffers, and looks hung. Leave it off when parsing output (a PTY turns `\n` into `\r\n`).
- **Never `docker exec` / `docker compose exec` instead.** `dwe shell` resolves the container from the service name and applies its `cli:` block; `docker ps | grep` is the tell you wanted `dwe status` or `dwe shell`.

## Picking the apply command

After editing yml, hand the user the command that matches **what** changed (never run it yourself):

- `service.yml` / a service's `deploy.yml` / `configs` / `dirs` / `render` / **added a service** / `workspace/deploy.yml` → `dwe deploy run`
- `workspace/lifecycle.yml` or the compose base/overlays → `dwe run`
- toggled a service → `dwe services enable|disable <name> --apply`
- `exports.env` **only** → `dwe run` (or `dwe deploy run --force`) — that block is in no config hash, so a plain `dwe deploy run` journal-skips the `.env` render (`references/render-and-vars.md` § 6)
- only icon / host / display strings → `dwe validate` (then `run` / `deploy run` if runtime is affected)
- authored a `workspace/tests/<scenario>.yml` → `dwe validate tests`, then `dwe test run <scenario>` (gated — see below)
- mixed / unsure → `dwe deploy run` (ends in `docker up --wait`, so it covers a restart)

`dwe deploy run --service <name>` requires that service's own `deploy.yml` and **skips** the final stack up — only for re-running one provisioned service, never for a new one. Never recommend `--force` as a clean install: it only ignores prior state (`when:` still applies). A true clean install is `dwe reset run && dwe deploy run`.

## Permission boundary — read freely, never mutate

**READ — run without asking** (no state change, safe even when they report errors):

- `status`, `logs`, `validate` (+ `validate config|checks|env|tests|…`, `--stage`), `info`
- `deploy plan`, `reset plan`, `deploy state show`, `deploy eject` / `reset eject` **without `--out`**
- `snapshot list|current|inspect`
- `compose argv <subcommand>` (bare, it errors), `compose files`, `docker ps|logs|project-name`, `bridge status|logs`
- `vars get|list|inspect`, `commands list [group]` / `cmd -i <id>`, `services list`
- `secrets status`, `secrets key list` — no plaintext, no key material. `secrets get` and `secrets key export` DO print secret material — handoff.
- `render env` **bare only** (no `--out`): prints the resolved `.env`, writes nothing. Its unfiltered body is the whole exported secret set — **always scope it**: `dwe render env | grep -E '^<NAME>='`. Host-only.
- `docs show|search|list|llms-txt`
- `test list` (lock-free, no Docker; `-o json` carries each scenario's `cost_profile`), `test clean --dry-run`

### Running project tasks — judge the task, not the verb

`dwe cmd <id>` and `dwe shell <service> -c '…'` are **transports**: their risk is whatever they carry.

- **Run without asking** when the task only reads or verifies in place: linters, type-checks, formatters in check mode, in-container inspection, a test suite that talks to nothing stateful.
- **Ask first** when it changes project or data state: migrations, seeds, resets, dependency installs, anything writing outside a build cache — and anything you are unsure about.
- **A test suite that reaches the project's database or another stateful service is in the second group.** Integration suites routinely truncate and re-seed the schema they run against; pointed at the live stack they destroy the developer's working data (observed: one `npx nx test` via `dwe shell` silently replaced every row of a dev database). Check what the suite connects to; when it needs the stack, the isolated way is a `workspace/tests/` scenario via `dwe test run`, gated below.
- **`dwe cmd -i <id>` shows `confirmation:`** — a declared one means ask. Being asked to "run the tests" is permission to run the tests, not to run them against the live stack when they write to it.

### The `dwe test run` gate — cheap AND isolated, or ask

`dwe test run` is a full clean Docker deploy of a throwaway copy — mutating and slow, but the only way to prove a deploy pipeline still works. Read `dwe test list --output json` first; each scenario's `cost_profile` decides:

- **Hard stops — hand it over, no judgement call**: `isolation_findings` non-empty after dropping entries with `"shared": true` (the fix for `interpolated_host_port` is routing the port through `services.<name>.ports` or an `exports.env` rule `from: vars.<path>`); `shared_volumes` > 0; `host_steps` > 0 (project-authored code on the host — `type: shell`, the `shell` builtin, host commands, shell `when:`/`check:`; dwe's own subcommands don't count, so the built-in pipeline reports 0).
- **Cost — judge it**: `build_services`, `external_images`, `max_start_period_seconds`. A build is not an automatic stop — read the Dockerfile; the profile reports whether there *is* a build, never what it costs (layer-cache warmth is not modelled).

Run unattended only when all hard stops are clear **and** you can account for the cost; otherwise — and when unsure — hand over the exact command. Always `dwe validate tests` (free) first, and propose a run only for substantial changes (new service, reworked pipeline, provisioning/secrets), never after a display-only edit. Details: `references/integration-tests.md`.

**MUTATE — never invoke yourself.** Prepare the change, then give the user the exact command:

- `dwe init` (safe to re-run: gap-fills; `--force` overwrites)
- `dwe deploy run [--service|--force|--resume]`, `dwe run` / `stop` / `restart`, `dwe reset run` (destructive)
- `dwe services enable|disable <name> --apply`
- `dwe vars set`, `dwe render config|ide|ai|git`, `dwe render env --out <path>`, `dwe render config --harvest`
- `dwe deploy eject --out` / `dwe reset eject --out`, `dwe deploy state clear|repair`
- `dwe snapshot create|restore|rollback|remove|pack|unpack`, `dwe bridge start|stop`, `dwe docs generate|export|cache clear`
- `dwe secrets init|set|get|encrypt|decrypt|rekey|key import|export|remove` — each writes a layer / keyfile or prints secret material. `key import` is a **human handoff**: it opens a hidden prompt; never ask the user for the identity text so you can paste it (it would land in a transcript).
- `dwe cmd <id>` / `dwe shell … -c` **when the task they carry mutates**
- `dwe test clean` (without `--dry-run`); `dwe test run` when the gate above does not clear it

Pattern: **edit yml → show the diff → give the exact command → wait.** Never work around the boundary through `docker` / `docker compose` / project scripts — including through `dwe shell`.

## Anti-patterns

- Do NOT bypass the lifecycle: never `docker compose up/down`, `dwe docker up`, or `dwe compose raw` writes — DWE tracks state and holds locks.
- Do NOT hand-edit generated artifacts (`.dwe/**`, `.env`, `workspace/local.yml`, rendered hub files incl. the hub `AGENTS.md`). Edit the source (export rule / var / template / pack) and hand off the render/deploy.
- Do NOT put a free-form key at the config root — it goes under `vars:`.
- Do NOT "fix" an `<encrypted>` value or a `secrets.unresolved` block by editing yml. The marker means this machine lacks the project's age identity; `dwe secrets status --output json` names the cause and the fix (`dwe secrets key import`, run by the user, or `DWE_AGE_KEY`). At a terminal `dwe run`/`restart`/`deploy` offer the import themselves. Rewriting the marker as plaintext commits the credential; deleting it breaks everyone else.
- Do NOT assume config shape — verify with `dwe docs show config/<area> --lang en` before editing anything under `workspace/`.
- Do NOT enumerate pending-op consumers from memory after `dwe services …` without `--apply` — run `dwe status`; its banner is authoritative.

## Reference files

Load on demand when the task matches:

- `references/populate-init-repo.md` — git URL(s) / "set up this project" / fresh `init`'d repo: interview → init → services → deploy → commands → render/vars → validate → deploy handoff.
- `references/add-service-and-tools.md` — app/tool/infra service, toggles, compose overlays, `extends`.
- `references/authoring-commands.md` — user commands and daemons: type zoo, params, templating, bridge opt-in.
- `references/render-and-vars.md` — render packs, generated-secret harvest, the `vars` sandbox, `exports.env`.
- `references/pipelines-and-orchestration.md` — per-service & project `deploy.yml`, `lifecycle`, `setup`, `validate`, `info`/`styles`, `docker.yml`.
- `references/integration-tests.md` — `workspace/tests/<scenario>.yml`, port isolation, the debugging loop.
- `references/snapshots-reset-troubleshoot.md` — triage trio, snapshot workflows, reset.
