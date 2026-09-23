# DWE snapshots, reset, and troubleshooting

Load when a service won't come up, a port conflicts, the journal looks stale, or the user wants to snapshot / reset. The triage trio is read-only; snapshots and reset are handoffs.

## Triage trio (read, lock-free)

```shell
dwe validate --output json                # config-level diagnostics
dwe status --output json                  # whole stack; `dwe status deploy <svc>` scopes to one
dwe logs <svc> --tail 0 --output json     # full log (default --tail 50); -f streams
```

Deeper, still read-only: `dwe compose argv up` (exact compose argv), `dwe compose files` (ordered overlay list; `--all`, also on `argv`/`raw`, adds disabled services' overlays for inspection), `dwe docker ps`, `dwe deploy state show` (the journal, YAML). `-v` / `--debug` echo to **stderr** only, so `dwe run --debug 2>debug.log` keeps stdout clean. Guide: `dwe docs show guides/troubleshooting --lang en`.

## Symptom → command

- **Port already in use** → `dwe validate env --output json`, then remap at the source: `service.yml` `ports:` or a `vars:` path (`render-and-vars.md`); hand off `dwe vars set` / `dwe deploy run`.
- **Stale journal after a branch switch** → `dwe deploy state show`; if out of sync hand off `dwe deploy state repair` (reconcile) or `dwe deploy state clear` (wipe). Never hand-edit `.dwe/deploy/state.yml`.
- **Container won't come up** → `dwe logs <svc> --tail 0`, then `dwe compose argv up` / `dwe compose files` to confirm the assembled overlays. Fixes land in `service.yml`, the service's `deploy.yml`, or an overlay (`pipelines-and-orchestration.md`).
- **Build fails fetching a base image** from a private/LAN registry while `docker pull` works → set `build.prepull_bases: true` in `workspace/docker.yml` (daemon-side pull of missing bases before compose builds; best-effort). `dwe docs show config/docker --lang en`.
- **`<encrypted>` / `secrets.unresolved`** → `dwe secrets status --output json`; `SKILL.md` § Anti-patterns.

## Snapshots — read first

```shell
dwe snapshot list --output json
dwe snapshot current --output json
dwe snapshot inspect <name|tar> --output json
dwe docs show config/snapshot --lang en
dwe docs show guides/write-snapshot-workflows --lang en
```

### Authoring `workspace/snapshot.yml`

Top-level: `dir`, `rollback_target` (what `dwe snapshot rollback` restores — create it once after a clean deploy), `require_matching_config`, `pack.exclude`; workflow blocks `create:` / `restore:` (+ optional `remove:`, run by `dwe snapshot remove`) of steps (`command:` + `with:`, `parallel:`, `when:`).

Load-bearing rules:

- `${snapshot.path}` (any `${snapshot.*}`) resolves **only** inside these blocks.
- Snapshots call the project's **own** dump/restore commands; they invent no backup logic.
- A `confirmation:` command cannot prompt under `parallel:` — without `--yes` the sub-step is rejected at preflight. Call `private:` wrapper commands with **no** `confirmation:` (e.g. `snapshot.db.restore`), so restore runs cleanly regardless of flags.
- Gate each dump/restore on `when: "file-exists ${snapshot.path}/…"` / `dir-exists` so partial snapshots restore cleanly; gate optional-service steps with `when: "${services.<name>.enabled}"`.

```yaml
create:
  steps:
    - command: snapshot.configs.dump
      with: { out_dir: "${snapshot.path}/configs" }
    - parallel:
        steps:
          - name: dump-main
            command: db.dump
            with: { database: "${vars.db.database}", out: "${snapshot.path}/db/main.sql.gz" }
restore:
  steps:
    - command: snapshot.db.restore            # private, no confirmation
      when: "file-exists ${snapshot.path}/db/main.sql.gz"
      with: { database: "${vars.db.database}", dump_file: "${snapshot.path}/db/main.sql.gz" }
```

### Snapshot handoff (all mutating)

```shell
dwe snapshot create <name> -d "WIP on …"
dwe snapshot restore <name>
dwe snapshot rollback
dwe snapshot remove <name>
dwe snapshot pack <name> --out <tar>
dwe snapshot unpack <tar> --as <name>
```

## Reset — destructive, always a handoff

Read `dwe docs show config/reset --lang en` and `dwe reset plan --output json` first. Always snapshot before resetting:

```shell
dwe snapshot create <name> -d "pre-reset"
dwe reset run
```

- **Volume cleanup is opt-in** — only if the reset pipeline includes `docker_remove_project_volumes`; confirm in `dwe reset plan`.
- **`--clear-generated`** also wipes `.dwe/generated.yml`; secrets re-mint on the next deploy only if the service's `deploy.yml` has the harvest step (`render-and-vars.md` § 2).
- `--service <name>` needs that service's own `deploy.yml` and never removes volumes.

True clean install: `dwe reset run && dwe deploy run` — never `deploy run --force`.
