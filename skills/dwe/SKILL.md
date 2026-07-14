---
name: dwe
description: Use when the current working directory is inside a DWE project — a Docker-based developer environment manager. Detect one by walking up from cwd to a directory containing `workspace.yml`; that directory is the project root. A populated project also has a `workspace/` subdirectory (services, pipelines), but its absence does not disqualify it (e.g. a freshly-initialized project). Applies anywhere beneath the root, including service folders like `workspace/services/<name>/` and their source subtrees. The skill is both a navigator — which `dwe` commands to use for inspection vs mutation, and how to look up everything else via the built-in `dwe docs` subsystem — and an authoring guide for extending a project (scaffolding from git repos, adding services and tools, authoring commands and daemons, wiring render packs and vars, customizing deploy/lifecycle pipelines). Activates on the word "dwe", on editing any file under `workspace/`, or when working inside a service directory.
---

# DWE — Dev Workspace Engine

DWE is a CLI that orchestrates Docker-based local development environments. It augments a project's compose file with configuration layering, lifecycle management, validation, and declarative tooling — it does **not** replace compose. Edit the compose file freely; DWE runs on top of it.

This skill is a **navigator with an authoring layer**, not a reference. It teaches **which** command to run, **which** file to edit, and **in what order** — all schema details, field meanings, and deep behavior live in the built-in `dwe docs` subsystem and are versioned with the binary. Every authoring step ends with a `dwe docs show <topic>` pointer; use it to learn **what** anything means.

## Detecting a DWE project

Walk up from your current working directory until you find a directory that contains `workspace.yml`. That directory is the project root. All `dwe` commands resolve the root themselves — invoke them from the root or any descendant.

A populated project also has a `workspace/` subdirectory next to `workspace.yml` (service definitions, pipelines, commands, templates, i18n). Its presence is a strong signal. A project with **only** `workspace.yml` plus a set of commented scaffold files is a freshly-`dwe init`'d project — see `references/populate-init-repo.md` to fill it in.

## First step: orient yourself

Run once per session inside the project:

```shell
dwe docs llms-txt --lang en
```

This emits a compact, project-aware index (services, commands, doc pointers) designed for AI agents. Read its output before doing anything else.

**If a root `AGENTS.md` exists, read it too.** A populated project renders its own `AGENTS.md` (the `ai` render pack) — that is the **project-specific** layer: the real service list, the real command IDs, project-local rules. This skill is the **generic** layer: universal DWE mechanics and the read/mutate discipline. They are designed to agree. On a *project fact* the `AGENTS.md` wins; on a *generic rule* the skill wins. Never edit the generated `AGENTS.md` to change behavior — edit its template (`workspace/templates/ai/<pack>/`) and hand off `dwe render ai`.

## Project anatomy (the map)

What lives where (paths, not schemas — look up any schema with the slug noted):

- **3-layer config**, later wins, maps deep-merge: `workspace.yml` (identity only — `project`, `update`, `compose`) → `workspace/defaults.yml` (git-tracked: `services` toggles, `runtime`, the `vars` sandbox, `exports`, `bridge`) → `workspace/local.yml` (gitignored per-dev overrides; tool-written). The merged root is **strict** — free-form values live **only** under `vars:`; a bare custom root key is a hard load error.
- **Services = folders**: `workspace/services/<name>/service.yml`; the folder name **is** the map key (no `name:` field). The real container lives in the compose base or an overlay.
- **User commands**: `workspace/commands/**.yml`; path + filename + key = a dot-ID; run with `dwe cmd <id>`.
- **Render packs**: `workspace/templates/{config,ide,ai,git}/`; `config` writes runtime files into the service hub, `ide`/`ai`/`git` write hub dotfiles (devcontainer, the generated `AGENTS.md`, git hooks).
- **Pipelines (optional, full-replacement)**: per-service `deploy.yml` and project `workspace/{deploy,lifecycle,reset,snapshot,setup,validate,info,styles}.yml`. Absence = built-in default (reported `ⓘ`, not an error).
- **Integration-test scenarios**: `workspace/tests/<scenario>.yml` (one file = one scenario; the name is the file basename; reuses the deploy step schema) → `dwe docs show config/tests --lang en`. See `references/integration-tests.md`.

Schema for any of these → `dwe docs show concepts/project-layout --lang en` and the per-area slugs in the recipes below.

## Output conventions

- **Always `--lang en` for docs.** Translated docs may lag the English source, and reasoning is more reliable in English: `dwe docs llms-txt|search|show|list … --lang en`.
- **`--output json` for data you parse.** The default human mode is for users, not agents. Add `--pretty` if you like. Applies to `status`, `validate` (incl. `validate tests`), `services`, `vars get/list/inspect`, `deploy state show`, `snapshot list/inspect`, `info`, `logs`, `commands list`, `docs list/search`, `test list/run/clean`.
- **Exception — `dwe docs llms-txt`.** Its `--output` is a **file path**, not a format selector. `--output json` would write the markdown body to a file literally named `json`. Just run it and parse the markdown from stdout.
- **Exception — `dwe docs show`.** Emits markdown (rendered for TTY; raw with `--raw` or in a pipe). The global `--output json` is **ignored** — the document IS the payload. Use `#anchor`, `--anchors`, or `--toc` to scope without reading the full body.
- **Bare `dwe commands` / `dwe docs` / `dwe status` open a full-screen TUI on an interactive terminal**, but auto-fall back to plain output when not attached to one — bare `dwe commands`→`commands list`, `dwe docs`→`docs list`, `dwe status`→plain text — so they never hang a piped agent (a pipe is non-interactive). `commands`/`docs` additionally honor `DWE_NONINTERACTIVE=1` (the bridge sets it in containers); `status` does **not** — it drops the TUI only on a non-TTY stdout, `--no-tui`, `TERM=dumb`, or `--output json`. Always call the explicit read subcommands (`commands list`, `docs list|show|search`, `status --output json`) rather than the bare TUI form.

## When to use what

| Goal | Command / reference |
| --- | --- |
| Project overview (start here) | `dwe docs llms-txt --lang en` |
| Inspect state | `dwe status --output json` |
| Read logs | `dwe logs <service> --output json` |
| Diagnose configuration | `dwe validate --output json` |
| Search docs / read one topic | `dwe docs search <term> --lang en` · `dwe docs show <topic> --lang en` |
| Inspect vars (read) / set a var (handoff) | `dwe vars get\|list\|inspect <var> --output json` · ASK user → `dwe vars set <path> <value>` |
| **Populate a fresh repo from git URL(s)** | `references/populate-init-repo.md` (ends in user-run `dwe deploy run`) |
| **Add a service / tool / infra** | `references/add-service-and-tools.md` |
| **Author a command or background daemon** | `references/authoring-commands.md` |
| **Wire render packs / vars / `.env` / generated secrets** | `references/render-and-vars.md` |
| **Author a pipeline** (deploy / lifecycle / reset / setup / validate / info / styles) | `references/pipelines-and-orchestration.md` |
| **Verify a clean deploy in isolation / author an integration test** | `references/integration-tests.md` |
| **Snapshot / reset / troubleshoot** | `references/snapshots-reset-troubleshoot.md` |
| Apply a change | see **Picking the apply command** below — edit yml, then ASK the user to run it |

## Picking the apply command

After editing yml, the apply command depends on **what** changed (never run it yourself — hand it to the user):

- `service.yml` / a service's `deploy.yml` / `configs` / `dirs` / `render` / **added a service** → `dwe deploy run`
- `workspace/deploy.yml` → `dwe deploy run`
- `workspace/lifecycle.yml` or the compose base/overlays → `dwe run`
- toggled a service → `dwe services enable|disable <name> --apply`
- only icon / host / display strings → `dwe validate` (then `run`/`deploy run` if it affects runtime)
- mixed / unsure → `dwe deploy run` (ends in `docker up --wait`, so it covers a restart)
- authored/edited a `workspace/tests/<scenario>.yml` → verify read-only with `dwe validate tests`, then hand off `dwe test run <scenario>` (a slow clean deploy in a throwaway copy — does not touch the live stack). Propose this only for **substantial** changes (new service, reworked deploy pipeline), and let the user run it — never on your own initiative. See `references/integration-tests.md`.

Never recommend `dwe deploy run --force` as a clean install — `--force` only ignores prior state (`when:` still applies). A true clean install is `dwe reset run && dwe deploy run`.

## Permission boundary — read freely, never mutate

You MAY run READ commands without asking (all read-only — they don't mutate or tear down project state; safe even when they report errors):

- `status`, `logs`, `validate` (+ `validate config|checks|env|tests`, `--stage`), `info`
- `deploy plan`, `reset plan`, `deploy state show`
- `snapshot list|current|inspect`
- `compose argv|files`, `docker ps|logs|project-name`, `bridge status|logs`
- `vars get|list|inspect`, `commands list` / `commands -i <id>`
- `docs show|search|list|llms-txt` — read-only doc access.
- `test list` — list integration-test scenarios (lock-free, no Docker). `test clean --dry-run` is also safe to run without asking (it previews a sweep and destroys nothing), but is NOT strictly lock-free/Docker-free: it does a read-only `docker ps` orphan probe and briefly acquires-then-releases each scenario's flock. (`validate tests` sits in the `validate` family above; the destructive `test run` / `test clean` forms are below.)

You MUST NOT invoke MUTATING commands yourself. Prepare the change, then ask the user to run the exact command:

- `dwe init` — scaffold a project (safe to re-run: gap-fills; `--force` overwrites).
- `dwe deploy run` — run the deploy pipeline (the right command after editing a service's config/deploy steps or adding a service; ends with `docker up --wait`). The `--service <name>` form requires that service's own `deploy.yml` and **skips** the final stack up — see the recipes before recommending it.
- `dwe run` / `stop` / `restart` — runtime lifecycle (no deploy steps); `dwe reset run` — destructive.
- `dwe services enable|disable <name> --apply` — toggle a service.
- `dwe vars set`, `dwe render env|config|ide|ai|git`, `dwe cmd <id>`, `dwe snapshot create|restore|rollback|remove|pack|unpack`, `dwe bridge start|stop`, `dwe docs generate|export|cache clear`.
- `dwe test run [scenario...]` / `dwe test clean` (without `--dry-run`) — integration-test runner and sweeper. Isolated/disposable (they operate on throwaway project copies under `.dwe/tests/`), so they never touch the live stack — but a run is a **full, slow Docker deploy**. Only *propose* it for substantial changes, and run it strictly on the user's explicit request. See `references/integration-tests.md`.

Pattern: **edit yml files yourself → show the diff → tell the user the exact command → wait for them to run it.** Do not invoke the mutation, and do not work around the boundary by calling `docker` / `docker compose` / project scripts directly.

## Anti-patterns

- Do NOT bypass the dwe lifecycle: use `dwe deploy run` / `dwe run` / `stop` / `restart` (whichever fits — see the table). NEVER run `docker compose up/down`, `dwe docker up`, or `dwe compose` write ops directly — DWE tracks state and holds file locks; bypassing breaks both.
- Do NOT hand-edit generated artifacts: `.dwe/**`, `.env`, `workspace/local.yml`, or rendered hub files (incl. `AGENTS.md`). Edit the **source** (export rule / var / template / ai pack) and hand off the render/deploy.
- Do NOT put a free-form key at the config root — the strict root hard-fails the load. It goes under `vars:`.
- Do NOT assume config shape. Before editing any yml under `workspace/`, verify the schema with `dwe docs show config/<area> --lang en`.
- Do NOT enumerate pending-op consumers from memory after `dwe services …` without `--apply` — run `dwe status` and follow its banner; that banner is authoritative.

## Recipes

Load a reference file on demand when the task matches:

- `references/recipes.md` — daily/inspection recipes + the index: add-a-service (quick), service-fails-to-start, toggle a service, find which config owns a setting, look up a field.
- `references/populate-init-repo.md` — user gives git repo URL(s) / "set up this project" / fresh `init`'d repo: interview → init → services → deploy clone steps → commands → render/vars → validate → deploy.
- `references/add-service-and-tools.md` — add an app/tool/infra service, optional toggles, compose overlays, `extends`.
- `references/authoring-commands.md` — author user commands & background daemons (the type zoo, params, templating, bridge opt-in).
- `references/render-and-vars.md` — render packs (config vs ide/ai/git), generated-secret harvest, the `vars` sandbox, `exports.env`.
- `references/pipelines-and-orchestration.md` — per-service & project `deploy.yml`, `lifecycle`, `setup` wizard, `validate` checks, `info`/`styles`.
- `references/integration-tests.md` — author `workspace/tests/<scenario>.yml` and verify an isolated clean deploy with `dwe test run` (you write the scenarios; the user runs them). Also the general engine additions: `http_check` predicate, predicate-as-assertion, per-step `timeout:`.
- `references/snapshots-reset-troubleshoot.md` — snapshot workflows, reset, and the read-only triage trio.
