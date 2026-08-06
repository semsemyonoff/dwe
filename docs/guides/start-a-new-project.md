# Starting a new project with `dwe init`

You want to put a brand-new project under DWE. There is no `workspace.yml` yet, and hand-assembling the config file by file is tedious and easy to get subtly wrong. `dwe init` does the bootstrap for you: one command writes a minimal-but-complete project that loads and `dwe validate`s clean on the first run.

This is the mirror image of [Joining a DWE project](joining-a-project.md): that guide is for a repo that already ships DWE config; this one creates that config from nothing.

## What `dwe init` is for

`dwe init` is the only DWE command that runs **outside** a project — it creates one rather than acting inside one. It is:

- **A skeleton to configure, not a running stack.** It writes a minimal but valid project structure — `workspace.yml`, `workspace/defaults.yml`, one enabled `app` service, a base `compose.yaml`, an AI template pack, a starter test scenario, and a set of commented override files. You fill in the real service config — image or build, ports, hosts — from there. The override files ship fully commented, so the built-in deploy/lifecycle defaults stay in effect until you deliberately take one over.
- **Safe to re-run.** On a directory with no `workspace.yml` it fills gaps and never overwrites an existing file unless you pass `--force`. If a project already exists there, it refuses to clobber it silently: interactively it asks you to confirm a recreate, and non-interactively it stops unless `--force` is passed.

## Interactive run

In an empty directory, just run:

```shell
dwe init
```

A short form asks for:

- **Project name** (required) — written to `workspace.yml` as `project.name`.
- **Compose prefix** (default `dwe`) — the `project.prefix` that namespaces your Docker resources.
- **Branding** (optional) — a title, a tagline (the short subtitle under the header), and an accent color (a 6-digit hex code such as `#2EC3EB`) rendered into `workspace/styles.yml`. Each field is validated as you type. Leave these blank for a generic header; you can always [brand it later](brand-your-project.md).

The whole form is collected before anything is written to disk, so a mid-form `Ctrl-C` leaves the directory untouched.

## Non-interactive run

`dwe init` switches to flag-driven mode automatically when stdin/stdout is not a TTY (CI, scripts, pipes), or when you pass `--default` or `--output json`. Drive everything from flags:

```shell
dwe init --name my-project --prefix acme --service api
```

Or take all defaults without the form:

```shell
dwe init my-project --default
```

A positional `[name]` creates the project in `./<name>/` instead of the current directory. Name resolution precedence is `--name` → positional `[name]` → the current directory's basename.

### Flags

| Flag | Default | Effect |
| --- | --- | --- |
| `--name` | `[name]` arg, else cwd basename | project name in `workspace.yml` |
| `--prefix` | `dwe` | compose/project prefix |
| `--brand-title` / `--tagline` / `--accent` | empty | `workspace/styles.yml` branding (accent is a 6-digit hex like `#2EC3EB`) |
| `--service` | `app` | starter service folder name; `""` creates none |
| `-f, --force` | off | recreate an existing project / overwrite existing files |
| `-d, --default` | off | skip the form, take all defaults |
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
│  └─ config            gitignored, all-commented per-developer user-config template
└─ workspace/
   ├─ defaults.yml      starter service toggle + commented runtime/exports examples
   ├─ styles.yml        branding from the form + commented rest
   ├─ deploy.yml        inert mirror of the built-in deploy pipeline
   ├─ lifecycle.yml     inert mirror of the built-in lifecycle pipeline
   ├─ info.yml          inert mirror of the dashboard config
   ├─ docker.yml        inert mirror of the compose policy
   ├─ services/app/
   │  ├─ service.yml    type, container, source hub, icon and info.title active
   │  └─ deploy.yml     inert per-service pipeline skeleton (source → image → bootstrap → render)
   ├─ templates/ai/default/
   │  ├─ manifest.yml   ACTIVE AI template pack
   │  └─ AGENTS.md.tmpl rendered into the service hub by `dwe render ai`
   └─ tests/
      └─ smoke.yml      ACTIVE starter scenario (a description, no assertions yet)
```

Four things are worth understanding:

- **The override files ship commented out on purpose.** DWE pipeline composition is *full-replacement* — an active `workspace/deploy.yml` replaces the entire built-in deploy pipeline, so a half-edited file silently drops phases. `deploy.yml`, `lifecycle.yml`, and `info.yml` therefore arrive fully commented, as does the per-service `services/app/deploy.yml`; the built-in default stays active until you deliberately uncomment and own the whole pipeline. `docker.yml` is commented for the same reason but overrides **per key**, not whole-file: uncommenting one key leaves the built-in defaults for the rest. Each file's header points at the authoritative default it mirrors.
- **Two files ship active because they cannot ship commented.** A template-pack `manifest.yml` is strict-decoded and must declare at least one entry; the test-scenario loader rejects an empty document outright. `workspace/tests/smoke.yml` is therefore a real scenario with a `description:` and `steps: []` — deliberately assertion-free, because on a fresh scaffold `compose.yaml` declares no services, so the deploy the scenario wraps fails at `start/up` with "empty compose file" and `dwe test run smoke` reports a failure. The scenario becomes meaningful once your service exists in compose; add steps then.
- **There are two `AGENTS.md` files, and only one is yours to edit.** The root `AGENTS.md` is scaffolded once and meant to be edited by hand. The one the AI pack renders lands in the *service hub* (`services/app/`, gitignored and absent until the first clone) — edit `workspace/templates/ai/default/AGENTS.md.tmpl` instead, and re-render with `dwe render ai`.
- **`.gitignore` is merged, not overwritten.** If the directory already has a `.gitignore`, `dwe init` appends only the DWE runtime lines it is missing, under a `# dwe` marker. Re-running is a no-op. The entire `.dwe/` directory is ignored — it holds CLI-managed runtime data and the per-developer `.dwe/config`, none of which belongs in version control.

## After init

```shell
dwe validate     # confirm the fresh project is internally consistent
```

A fresh `dwe init` is designed to validate clean immediately. The scaffolded `app` service carries its identity and source hub but nothing to run yet, so configure it — image or build, ports, hosts — in `workspace/services/app/service.yml`, and add the matching service to `compose.yaml`, before bringing the stack up:

```shell
dwe deploy run   # bring the configured stack up
```

Two pairings the scaffold documents in comments but no validator can check for you:

- **A port is display-only until it is exported.** `ports:` in `service.yml` feeds `dwe status` and info blocks; nothing binds it. Uncomment it together with the matching `exports.env` rule in `workspace/defaults.yml`, which is what turns it into an environment variable `compose.yaml` can reference. `PROJECT`, `UID` and `GID` are injected automatically and must not be redeclared there.
- **Mount the whole hub, not just the sources.** `dir` is the host directory holding the checkout *and* everything next to it (build artefacts, caches, tooling state); `dir_internal` is where that whole directory lands in the container, and `work_dir_internal` is where commands run inside it.

From there the usual authoring path applies: [add a service](add-a-service.md), [author project commands](author-project-commands.md), [brand the dashboard](brand-your-project.md), and uncomment an override file when you genuinely need to reshape a pipeline. For an orientation pass in a fresh project — services, commands, pipeline builtins, diagnostic flags — run `dwe docs llms-txt --lang en`.

## Edge cases

- **A project already exists.** If the target directory already has a `workspace.yml`, `dwe init` will not silently overwrite it. Interactively it asks you to confirm recreating it (and recreates everything with `--force` on yes); non-interactively it stops with an error telling you to pass `--force`.
- **Existing `.gitignore` / `.editorconfig`.** `.gitignore` is append-merged; `.editorconfig` is written only when absent. Neither is clobbered.
- **Nested projects.** If an ancestor directory already has a `workspace.yml`, `dwe init` warns (`nested_warning` in JSON) but does not block — sometimes a nested project is what you want.
- **No starter service.** `--service ""` scaffolds a valid, service-less project. Everything that references the starter service is dropped with it — its folder, the AI template pack and the starter scenario — so nothing is left dangling. Add services later as folders under `workspace/services/`.
- **Windows symlinks.** Where `CLAUDE.md` cannot be symlinked to `AGENTS.md`, it is written as a verbatim copy and the run notes the fallback.

## See also

- [Joining a DWE project](joining-a-project.md) — the other side: a repo that already ships `workspace.yml`
- [workspace.yml reference](../reference/config/workspace.md) — the structural identity file and the three-layer merge model
- [Adding a service](add-a-service.md) — grow the starter into a real stack
- [Brand your project](brand-your-project.md) — flesh out the `styles.yml` the form seeded
