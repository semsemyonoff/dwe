---
name: devbox
description: Use when the current working directory is inside a Devbox project — a Docker-based developer environment manager. Detect a Devbox project by walking up from cwd to find a directory that contains `devbox.yml`; that directory is the project root. A populated Devbox project additionally has a `devbox/` subdirectory holding services and pipelines, but its absence does not disqualify the project (e.g. a freshly-initialized one). The skill applies anywhere beneath the root, including service folders like `devbox/services/<name>/` and their source subtrees. The skill is a thin navigator — it teaches the agent which `devbox` commands to use for inspection vs mutation and how to look up everything else via the built-in `devbox docs` subsystem. Activates on the word "devbox", on editing any file under `devbox/`, or when working inside a service directory.
---

# Devbox

Devbox is a CLI that orchestrates Docker-based local development environments. It augments a project's `docker-compose.yml` with configuration layering, lifecycle management, validation, and tooling — it does **not** replace compose. Edit `docker-compose.yml` freely; Devbox runs on top of it.

This skill is a **navigator**, not a reference. All schema details, field meanings, and deep behavior live in the built-in `devbox docs` subsystem and are versioned with the binary. Use this skill to know **which** command to run and **when**; use `devbox docs` to learn **what** anything means.

## Detecting a Devbox project

Walk up from your current working directory until you find a directory that contains `devbox.yml`. That directory is the project root. All `devbox` commands resolve the root themselves — you can invoke them from the root or any descendant.

A populated project also has a `devbox/` subdirectory next to `devbox.yml` (it holds service definitions, pipelines, and i18n). Its presence is a strong signal, but a fresh project may have only `devbox.yml`.

Common cwd locations where this skill applies:

- `<root>/` — project root
- `<root>/devbox/` — Devbox configuration tree
- `<root>/devbox/services/<name>/` — a single service folder
- `<root>/devbox/services/<name>/src/` — service source code
- any other subdirectory of `<root>`

## First step: orient yourself

Run once per session inside the project:

```
devbox docs llms-txt --lang en
```

This emits a compact, project-aware index (services, commands, doc pointers) designed for AI agents. Read its output before doing anything else.

## Always English for docs

Devbox supports i18n. Pass `--lang en` to **every** `devbox docs ...` invocation. Translated docs may lag behind the English source, and reasoning is more reliable in English:

```
devbox docs llms-txt --lang en
devbox docs search <term> --lang en
devbox docs show <topic> --lang en
devbox docs list --lang en
```

## JSON for parsing, default for humans

When you need to parse output of a **data-emitting** command (e.g. `status`, `validate`, `docs list`, `docs search`, `services`), pass `--output json`. The default human-readable mode is for users, not agents.

**Exception — `devbox docs llms-txt`.** This command emits a markdown document, not structured data. Its `--output` flag is a **file path** (`--output PATH`), not a format selector. Passing `--output json` to it would write the markdown body to a file literally named `json` in the cwd. Just run `devbox docs llms-txt --lang en` and parse the markdown from stdout — never combine it with `--output json`.

**Exception — `devbox docs show`.** Emits a single topic as markdown (rendered for TTY, raw markdown with `--raw` or in a pipe). The global `--output json` flag is ignored — there is no JSON shape; the document IS the payload. Use `--anchors` or `--toc` to scope without reading the full body.

## When to use what

| Goal                          | Command                                                                  |
| ----------------------------- | ------------------------------------------------------------------------ |
| Inspect state                 | `devbox status --output json`                                            |
| Read logs                     | `devbox logs <service>`                                                  |
| Diagnose configuration        | `devbox validate --output json`                                          |
| Search docs for a term        | `devbox docs search <term> --lang en`                                    |
| Read one doc topic            | `devbox docs show <topic> --lang en`                                     |
| Project overview (start here) | `devbox docs llms-txt --lang en`                                         |
| Apply changes to a service (service.yml, configs, dirs, render, service deploy.yml) | edit yml, then ASK user to run `devbox deploy run` (see recipes for the `--service <name>` scoped variant — it has caveats) |
| Apply changes to the deploy orchestrator (`devbox/deploy.yml`) | ASK user to run `devbox deploy run`                                      |
| Apply changes to runtime lifecycle (`devbox/lifecycle.yml`) or container shape (`docker-compose.yml`) | ASK user to run `devbox run` (full runtime lifecycle)                    |
| Toggle a service              | ASK user to run `devbox services enable\|disable <name> --apply`         |
| Start fresh                   | ASK user to run `devbox reset run` (destructive)                         |

## Permission boundary — read freely, never mutate

You MAY run READ commands without asking:

- `status`, `logs`, `validate`
- `docs show`, `docs search`, `docs list`, `docs llms-txt` — read-only doc access.

The remaining `docs` subcommands are **mutating** and require user approval like any other mutation: `docs generate` (rewrites the docs tree), `docs export` (writes to a target directory), `docs cache clear` (deletes cached diagrams).

You MUST NOT invoke MUTATING commands yourself. Always prepare the change, then ask the user to run the exact command:

- `devbox deploy run` — runs the deploy pipeline (`devbox/deploy.yml`, including each service's `deploy.yml`): install / configure / migrate / render service setup, then `docker up --wait`. **This is the right command after editing a service's config or deploy steps, or after adding a new service.** A `--service <name>` scoped form exists but has caveats (requires service-level `deploy.yml`, skips final `docker up --wait`) — see the recipes file before recommending it.
- `devbox run` — runtime lifecycle only (git probe → docker compose up → wait → hooks). Brings the stack up without re-running service deploy setup. Use for changes to `lifecycle.yml` or `docker-compose.yml`.
- `devbox stop` / `devbox restart` — runtime siblings of `devbox run`.
- `devbox reset run` — destructive.
- `devbox services enable <name>` / `devbox services disable <name>` — toggle a service (use `--apply` to apply immediately, otherwise the change is pending — see the toggle recipe).

Pattern: edit yml files yourself → show the diff → tell the user the exact command → wait for them to run it. Do not invoke the mutation yourself, and do not work around the boundary by calling `docker` / `docker compose` / project scripts directly.

## Anti-patterns

- Do NOT bypass devbox lifecycle: use `devbox deploy run` / `devbox run` / `stop` / `restart` (whichever fits the change — see the table above). NEVER run `docker compose up/down` directly — Devbox tracks state and holds file locks; bypassing breaks both.
- Do NOT assume config shape. Before editing any yml file under `devbox/`, verify the schema with `devbox docs show config/<area> --lang en`.

## Common recipes

For task-to-command mappings (add a service, diagnose a failing service, share a snapshot, reset cleanly, find which config owns a setting), read `references/recipes.md` in this skill on demand.
