# DWE Reference Documentation

*Languages: **English** · [Русский](../i18n/ru/reference/index.md)*

User-facing reference for the DWE CLI: project configuration, render packs, the docs subsystem, and the template engine. Task-oriented recipes live under [`docs/guides/`](../guides/index.md).

## Sections

- [Concepts (`concepts/`)](concepts/index.md) — high-level orientation: getting started, architecture, project layout, container orchestration interaction, version control integration, pipelines, state and locks, and the [host bridge](concepts/bridge.md) (running `dwe` from inside dev containers)
- [Configuration (`config/`)](config/index.md) — project layout, services, commands, vars, encrypted secrets, deploy / reset / lifecycle pipelines, snapshot, info dashboard, validate, setup wizard, styles, UI, state, i18n, notifications, docker integration
- [Render packs (`render/`)](render/index.md) — `dwe render config`, `render env`, `render ide`, `render ai`, `render git`; pack manifest schema, collision policies, local overrides
- [Documentation subsystem (`docs/`)](docs/index.md) — the `dwe docs` TUI browser, non-interactive subcommands (`show`, `list`, `export`, `llms-txt`, `cache clear`), translations and the content-hash staleness check
- [Templates (`templates.md`)](templates.md) — the shared Go-template engine: `{{ ... }}` vs `${ ... }`, sprout registries, render context per site, command-scope resolvers

## See also

- [Guides (`../guides/`)](../guides/index.md) — recipes and integrations (Starship prompt, …)
- Run `dwe docs` for an interactive browser over the same content
- Run `dwe --help` (or any subcommand with `--help`) for the live CLI surface
- Run `dwe docs llms-txt` to produce a compact AI-agent index of the project
