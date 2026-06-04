# Starting a new project with `dwe init`

You want to put a brand-new project under DWE. There is no `workspace.yml` yet, and hand-assembling the config file by file is tedious and easy to get subtly wrong. `dwe init` does the bootstrap for you: one command writes a minimal-but-complete project that loads and `dwe validate`s clean on the first run.

This is the mirror image of [Joining a DWE project](joining-a-project.md): that guide is for a repo that already ships DWE config; this one creates that config from nothing.

## What `dwe init` is for

`dwe init` is the only DWE command that runs **outside** a project — it creates one rather than acting inside one. It is:

- **Opinionated but inert.** It writes a real, working starter (one active `app` service, the built-in deploy/lifecycle pipelines) plus a set of commented override files you can grow into. Nothing it ships is half-configured.
- **Idempotent.** It fills gaps and never overwrites an existing file unless you pass `--force`, so it is safe to re-run on a partially set-up directory.
- **Quiet about machine state.** There is no project yet, so it runs no preflight and takes no locks. It only touches the filesystem.

## Interactive run

In an empty directory, just run:

```shell
dwe init
```

A short form asks for:

- **Project name** (required) — written to `workspace.yml` as `project.name`.
- **Compose prefix** (default `dwe`) — the `project.prefix` that namespaces your Docker resources.
- **Branding** (optional) — a title, tagline, and accent color rendered into `workspace/styles.yml`. Leave these blank for a generic header; you can always [brand it later](brand-your-project.md).

The whole form is collected before anything is written to disk, so a mid-form `Ctrl-C` leaves the directory untouched.

## Non-interactive run

`dwe init` switches to flag-driven mode automatically when stdin/stdout is not a TTY (CI, scripts, pipes), or when you pass `--yes` or `--output json`. Drive everything from flags:

```shell
dwe init --name my-project --prefix acme --service api
```

Or take all defaults without the form:

```shell
dwe init my-project --yes
```

A positional `[name]` creates the project in `./<name>/` instead of the current directory. Name resolution precedence is `--name` → positional `[name]` → the current directory's basename.

### Flags

| Flag | Default | Effect |
| --- | --- | --- |
| `--name` | `[name]` arg, else cwd basename | project name in `workspace.yml` |
| `--prefix` | `dwe` | compose/project prefix |
| `--brand-title` / `--tagline` / `--accent` | empty | `workspace/styles.yml` branding |
| `--service` | `app` | starter service folder name; `""` creates none |
| `--force` | off | overwrite existing files instead of skipping |
| `-y, --yes` | off | skip the form, take all defaults |
| `--output json` | text | machine-readable report (implies non-interactive) |

With `--output json` you get a structured report instead of prose:

```json
{
  "target": "/abs/path/to/project",
  "created": ["workspace.yml", "workspace/defaults.yml", "..."],
  "skipped": [],
  "symlink_fallback": false,
  "nested_warning": false
}
```

`skipped` lists files that already existed (and were left alone), `symlink_fallback` is true when `CLAUDE.md` had to be written as a copy instead of a symlink, and `nested_warning` is true when an ancestor `workspace.yml` was found.

## What ends up on disk

```
<target>/
├─ workspace.yml        project.name + project.prefix (+ commented optional fields)
├─ compose.yaml         base compose file, referenced by defaults.yml
├─ .gitignore           DWE runtime entries (append-merged if the file already exists)
├─ .editorconfig        repo conventions (only written if absent)
├─ AGENTS.md            brief project prompt for AI agents
├─ CLAUDE.md          → symlink to AGENTS.md (a copy where symlinks are unavailable)
├─ .dwe/
│  └─ config            committed, all-commented user-config template
└─ workspace/
   ├─ defaults.yml      starter service toggle + commented runtime/exports examples
   ├─ styles.yml        branding from the form + commented rest
   ├─ deploy.yml        inert mirror of the built-in deploy pipeline
   ├─ lifecycle.yml     inert mirror of the built-in lifecycle pipeline
   ├─ info.yml          inert mirror of the dashboard config
   ├─ docker.yml        inert mirror of the compose policy
   └─ services/app/
      └─ service.yml    type + container active, optional fields commented
```

Two things are worth understanding:

- **The override files ship commented out on purpose.** DWE pipeline composition is *full-replacement* — an active `workspace/deploy.yml` replaces the entire built-in deploy pipeline, so a half-edited file silently drops phases. `deploy.yml`, `lifecycle.yml`, `info.yml`, and `docker.yml` therefore arrive fully commented; the built-in default stays active until you deliberately uncomment and own the whole pipeline. Each file's header points at the authoritative default it mirrors.
- **`.gitignore` is merged, not overwritten.** If the directory already has a `.gitignore`, `dwe init` appends only the DWE runtime lines it is missing, under a `# dwe` marker. Re-running is a no-op. The runtime subdirs (`.dwe/deploy/`, `.dwe/snapshots/`, `.dwe/logs/`, `.dwe/prompt-cache.yml`) are ignored individually so the committed `.dwe/config` template stays tracked.

## After init

```shell
dwe validate     # confirm the fresh project is internally consistent
dwe deploy run   # bring the starter stack up
```

A fresh `dwe init` is designed to validate clean immediately. From here, the usual authoring path applies: [add a service](add-a-service.md), [author project commands](author-project-commands.md), [brand the dashboard](brand-your-project.md), and uncomment an override file when you genuinely need to reshape a pipeline.

## Edge cases

- **Existing `.gitignore` / `.editorconfig`.** `.gitignore` is append-merged; `.editorconfig` is written only when absent. Neither is clobbered.
- **Nested projects.** If an ancestor directory already has a `workspace.yml`, `dwe init` warns (`nested_warning` in JSON) but does not block — sometimes a nested project is what you want.
- **No starter service.** `--service ""` scaffolds a valid, service-less project; add services later with `dwe init`-style folders under `workspace/services/`.
- **Windows symlinks.** Where `CLAUDE.md` cannot be symlinked to `AGENTS.md`, it is written as a verbatim copy and the run notes the fallback.

## See also

- [Joining a DWE project](joining-a-project.md) — the other side: a repo that already ships `workspace.yml`
- [workspace.yml reference](../reference/config/workspace.md) — the structural identity file and the three-layer merge model
- [Adding a service](add-a-service.md) — grow the starter into a real stack
- [Brand your project](brand-your-project.md) — flesh out the `styles.yml` the form seeded
