# Concepts

High-level prose that frames what DWE is and how its pieces fit together. Read these before diving into the field-level [config reference](../config/index.md).

The pages are ordered for first-time reading: start at "Getting started", then walk the architecture and project layout, then the interaction layers (Docker, Git, pipelines), and finish with state and locks.

## Pages

- [Getting started](getting-started.md) — install the binary, enter a project, run your first `dwe deploy` and `dwe run`, see the info dashboard, and find the next thing to read.
- [Architecture](architecture.md) — the boundary view: what DWE owns vs what Docker owns, how a `dwe` command turns into a `docker compose` invocation, and what state lives on disk vs in the Docker engine.
- [Project layout](project-layout.md) — what a real DWE project looks like on disk: `workspace.yml` at the root, the `workspace/` config tree with `services/`, `commands/`, `templates/`, `i18n/`, and `scripts/`; the parallel `compose/` overlays; and the runtime-managed `.dwe/` artifacts.
- [Docker integration](docker.md) — how DWE drives Docker Compose: project-name derivation, the compose file list assembled from base + service overlays + tools + local, environment propagation, volume conventions, and why some lifecycle commands bypass compose and call `docker stop` / `docker rm` directly.
- [Git integration](git.md) — what DWE renders into Git: shell hook templates copied into `<svc.Dir>/src/.git/hooks/`, hook inheritance through the pack root, the `dwe status git` view, and the `.gitignore` conventions that keep `.dwe/` out of version control.
- [Pipelines](pipelines.md) — the phase → step → condition execution model shared by deploy, reset, and lifecycle: how parallel groups work, how sub-step overrides flow, the available step types, and the three condition kinds (`when:`, `check:`, `files_gate:`).
- [State and locks](state-and-locks.md) — how `.dwe/deploy/state.yml` records hashes and decides what to skip; how `deploy.lock` and `snapshot.lock` are acquired alphabetically and released in reverse; how DWE recovers from a crash mid-pipeline; and how pending state defers work between `services enable` and the next `deploy run`.

## Related

- [Configuration reference](../config/index.md) — field-level reference for every config file.
- [Render packs](../render/index.md) — `dwe render env`, `render ide`, `render ai`, `render git`.
- [CLI reference](../cli/index.md) — auto-generated command tree.
