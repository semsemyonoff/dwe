# Guides

Task-oriented recipes and integrations for DWE. Each page solves a concrete user-facing problem rather than describing a single command or config field.

Guides sit alongside the [reference](../reference/index.md) (schemas, command surface, template engine) — the reference tells you *what* exists, guides tell you *how to use it together*.

## Pages

### Starting and joining a project

For developers bootstrapping a new project or getting productive in an existing one.

- [Starting a new project with `dwe init`](start-a-new-project.md) — no `workspace.yml` yet; scaffold a minimal-but-complete project that validates clean on the first run.
- [Joining a DWE project](joining-a-project.md) — you cloned a repo with a `workspace.yml`; what to run first and what ends up on disk.
- [Daily workflow](daily-workflow.md) — the small handful of commands you reach for every day: status, services, shell, commands, logs, stop/restart.
- [Troubleshooting](troubleshooting.md) — your stack misbehaves; a triage map from `dwe validate` and `dwe logs` down to `dwe compose raw`.
- [Switching tasks with snapshots](switching-tasks-with-snapshots.md) — save your current environment, switch to other work, and restore it later; the snapshot create/restore cookbook.

### Authoring and maintaining a project

For developers who write DWE config — author services, commands, daemons, snapshot workflows, theming, localization, IDE template packs, preflight checks.

- [Adding a service](add-a-service.md) — wire a new `worker` (or app, or infra) service into an existing project.
- [Authoring project commands](author-project-commands.md) — turn README incantations into first-class `dwe <id>` commands.
- [Background daemons](background-daemons.md) — declare a long-running queue worker, watcher, or scheduler with `type: daemon`.
- [Preflight checks](preflight-checks.md) — fail fast before a deploy touches the machine, with a useful diagnostic.
- [Shared IDE and agent config](shared-ide-and-agent-config.md) — same VS Code, `AGENTS.md`, and git hooks for everyone via template packs.
- [Localize for your team](localize-for-your-team.md) — translate user commands and command-browser strings into RU / DE / FR / …
- [Brand your project](brand-your-project.md) — customize the ASCII header, palette, and `dwe info` dashboard.
- [Write snapshot workflows](write-snapshot-workflows.md) — author `workspace/snapshot.yml`: decide what gets captured, restored, and cleaned up.

### Integrations

- [Starship prompt integration](starship.md) — render a compact, project-aware DWE segment inside your [Starship](https://starship.rs/) shell prompt.

## See also

- [Reference (`reference/`)](../reference/index.md) — schemas, config fields, render packs, docs subsystem
- Run `dwe --help` (or any subcommand with `--help`) for the live CLI surface
- Run `dwe docs` for an interactive browser over the whole documentation tree
