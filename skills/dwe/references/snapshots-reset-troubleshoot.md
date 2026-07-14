# DWE snapshots, reset, and troubleshooting

Load this file when a service won't come up, a port conflicts, the journal looks stale, or the user wants to snapshot / reset the project. The triage trio is all-read and lock-free; snapshots and reset are mutating — edit yml yourself, hand the user the exact command, wait.

These rules from `recipes.md` apply here too: read commands run freely; mutating commands are a handoff; `--lang en` on every `dwe docs …`; `--output json` only on data commands (and never on `docs show` / `docs llms-txt`).

## Triage trio (all read, lock-free)

Run these in order — each is safe even when it reports errors. Start broad, then narrow:

```shell
dwe validate --output json                       # config-level diagnostics
dwe status --output json                          # whole-stack state
dwe status deploy <svc> --output json             # scope to one service to cut noise
dwe logs <svc> --tail 0 --output json             # all logs for one service (default tail is only 50)
```

`dwe logs <svc>` defaults to `--tail 50`; use `--tail 0` for the full log or `-f`/`--follow` to stream. Then go deeper (still all read):

```shell
dwe compose argv up        # exact docker-compose argv DWE would run for `up`
dwe compose files          # the ordered overlay list assembled for this stack
dwe docker ps              # live container state
dwe deploy state show --output json   # the deploy journal (.dwe/deploy/state.yml)
```

`-v` / `--debug` echoes go to **stderr only**, so you can capture them without corrupting stdout JSON:

```shell
dwe run --debug 2>debug.log    # echoes/decisions land in debug.log; stdout stays clean
```

Guide: `dwe docs show guides/troubleshooting --lang en`.

## Symptom → command map

- **Port already in use** → `dwe validate env --output json` to confirm the clash, then remap the port. Ports live in `service.yml` (`ports:`/`hosts:`) or as vars in `defaults.yml`; edit the source, then hand off `dwe vars set <path> <value>` (a single port) or `dwe services enable|disable <name> --apply` (if a toggle is involved). See `render-and-vars.md` for the vars side.
- **Stale journal after a branch switch** → `dwe deploy state show --output json` to inspect what the journal thinks is deployed. If it's out of sync, hand the user `dwe deploy state repair` (reconcile) or `dwe deploy state clear` (wipe the journal) — both mutating. Never hand-edit `.dwe/deploy/state.yml`.
- **Container won't come up** → `dwe logs <svc> --tail 0 --output json` first, then `dwe compose argv up` and `dwe compose files` to confirm the assembled overlays look right. Most fixes land in `service.yml`, the service's `deploy.yml`, or a compose overlay — see `pipelines-and-orchestration.md` for the deploy/lifecycle side.
- **Build fails fetching a base image** from a LAN / private registry (`failed to fetch oauth token … no route to host`) while a plain `docker pull` of that image works → the buildkit builder can't reach the registry that the daemon can. Set `docker.build.prepull_bases: true` in `workspace/docker.yml` (default off): `dwe docker build` / `dwe docker up` (hence the default deploy's `docker up --wait`) then daemon-side `docker pull` any **missing** base images before compose builds. Best-effort/advisory — it never makes a build worse. Edit the yml, hand off `dwe deploy run`. `dwe docs show config/docker --lang en`.

## Snapshots — read first

Snapshots use a dedicated scope and templating rules; read before authoring or running:

```shell
dwe snapshot list --output json
dwe snapshot current --output json
dwe snapshot inspect <name|tar> --output json
dwe docs show config/snapshot --lang en
dwe docs show guides/write-snapshot-workflows --lang en
dwe docs show guides/switching-tasks-with-snapshots --lang en
```

### Authoring `workspace/snapshot.yml`

Top-level keys: `dir` (where snapshots live), `rollback_target` (the snapshot `dwe snapshot rollback` restores — create it once after a clean deploy), `require_matching_config` (block restore if the captured config hash diverged), and `pack.exclude` (globs dropped from the shared tarball). Two pipelines, `create:` and `restore:`, each a list of steps.

Load-bearing rules when writing these pipelines:

- `${snapshot.path}` (and any `${snapshot.*}`) resolves **only** inside snapshot workflow blocks — never elsewhere.
- Snapshots call the project's **own** `db.dump` / `db.restore` commands (and any search/index dump). They do not invent backup logic.
- A `confirmation:` command **can't be prompted interactively under `parallel:`** — so the outcome forks on `--yes`: **without** it (and without a non-interactive stdin / `DWE_NONINTERACTIVE=1`) the sub-step is hard-rejected at the parallel preflight (*"rerun with --yes or set DWE_NONINTERACTIVE=1"*); **with** it, `SkipConfirm` propagates into each sub-step and the prompt is skipped, so it runs. Either way, don't make `restore:` hinge on the caller passing `--yes` — call `private:` wrapper commands that carry **no** `confirmation:` block (e.g. a `snapshot.db.restore` wrapper, not the interactive `db.restore`), so restore runs cleanly regardless.
- Gate each dump/restore on `file-exists` / `dir-exists` so partial snapshots (created before an optional service existed) restore cleanly. Gate optional-service steps with `${services.<name>.enabled}`.

Skeleton (multi-DB project):

```yaml
dir: snapshots
require_matching_config: false
rollback_target: baseline
pack:
  exclude:
    - "**/.DS_Store"

create:
  description: Capture .env files and all DB dumps.
  steps:
    - command: snapshot.configs.dump
      with: { out_dir: "${snapshot.path}/configs" }
    - parallel:
        steps:
          - name: dump-main
            command: db.dump
            with:
              database: "${vars.db.database}"
              out: "${snapshot.path}/db/${vars.db.database}.sql.gz"
          - name: dump-sales
            command: db.dump
            when: "${services.sales.enabled}"   # optional service
            with:
              database: "${vars.db.sales_database}"
              out: "${snapshot.path}/db/${vars.db.sales_database}.sql.gz"

restore:
  description: Restore .env files and all DB dumps.
  steps:
    - command: snapshot.configs.restore
      when: "dir-exists ${snapshot.path}/configs"
      with: { in_dir: "${snapshot.path}/configs" }
    - parallel:
        steps:
          - name: restore-main
            command: snapshot.db.restore          # private no-prompt wrapper
            when: "file-exists ${snapshot.path}/db/${vars.db.database}.sql.gz"
            with:
              database: "${vars.db.database}"
              dump_file: "${snapshot.path}/db/${vars.db.database}.sql.gz"
```

Schema for every field: `dwe docs show config/snapshot --lang en`. The `private:` wrappers it calls are authored like any command — see `authoring-commands.md`.

### Snapshot handoff (all mutating)

Edit `snapshot.yml`, then hand the user whichever applies:

```shell
dwe snapshot create <name> -d "WIP on …"   # capture current state
dwe snapshot restore <name>                # restore a named snapshot
dwe snapshot rollback                      # restore rollback_target
dwe snapshot remove <name>                 # delete a snapshot
dwe snapshot pack <name> --out <tar>       # share a snapshot as a tarball
dwe snapshot unpack <tar> --as <name>      # import a shared tarball
```

## Reset — destructive, always a handoff

`dwe reset run` is destructive — never run it yourself. The reset pipeline is project-defined, so read it first, then preview:

```shell
dwe docs show config/reset --lang en
dwe reset plan --output json
```

Before any reset, **always create a snapshot first** so the state is recoverable:

```shell
dwe snapshot create <name> -d "pre-reset"   # user runs this BEFORE reset run
dwe reset run                                # then this
```

Two things that are easy to get wrong:

- **Volume cleanup is opt-in.** Volumes are wiped only if the project's reset pipeline includes the `docker_remove_project_volumes` builtin. Do not assume volumes are gone — confirm against `dwe reset plan` / `config/reset`.
- **`--clear-generated` also wipes `.dwe/generated.yml`.** Run `dwe reset run --clear-generated` only when secrets should be re-minted next deploy; they regenerate on the following `dwe deploy run` **only if the service's `deploy.yml` has a harvest step** (a `pattern:` regex that recaptures the value a generate `command:` mints) — a plain deploy with no harvest step won't re-mint. See `render-and-vars.md` for the generated-secret lifecycle.

For a true clean install, hand the user the pair — never `deploy run --force` (that only ignores prior state; `when:` still applies):

```shell
dwe reset run && dwe deploy run
```

## Related references

- `recipes.md` — the quick "service fails to start" / "share or restore a snapshot" / "reset everything" entries point here for the full version.
- `pipelines-and-orchestration.md` — authoring the `deploy` / `lifecycle` / `reset` pipelines and their step types, builtins, and `when:` predicates.
- `render-and-vars.md` — vars + ports for the port-conflict fix, and the generated-secret lifecycle behind `--clear-generated`.
