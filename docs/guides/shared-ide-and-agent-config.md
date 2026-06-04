# Shared IDE and Agent Config

Make sure every developer on the team gets the same VS Code settings, the same `AGENTS.md` / `CLAUDE.md`, and the same git hooks — without anyone hand-editing those files. DWE's three rendering subcommands (`dwe render ide`, `dwe render ai`, `dwe render git`) drive all of this from template packs checked into the repo.

This guide gets you from zero to a working shared config with room for per-developer tweaks. For the full schema and edge cases, see the [render reference](../reference/render/index.md).

## Sections

- [Template pack layout](#template-pack-layout)
- [The `manifest.yml` file](#the-manifestyml-file)
- [Pack resolution: which pack does a service use?](#pack-resolution-which-pack-does-a-service-use)
- [Collision policies (deepest vs shallowest)](#collision-policies-deepest-vs-shallowest)
- [Dry run — render everything](#dry-run--render-everything)
- [Personal overrides with `<pack>.local/`](#personal-overrides-with-packlocal)
- [What gets tracked vs gitignored](#what-gets-tracked-vs-gitignored)

## Template pack layout

All three renderers read packs from `workspace/templates/<kind>/<pack>/`, where `<kind>` is one of `ide`, `ai`, `git`:

```
workspace/templates/
  ide/
    default/                # implicit fallback pack
      manifest.yml
      .vscode/settings.json.tmpl
      .devcontainer/devcontainer.json.tmpl
    main-debug/             # pack named after a service (or pinned via render.ide.template)
      manifest.yml
      .vscode/launch.json.tmpl
      .vscode/settings.json.tmpl
  ai/
    default/
      manifest.yml
      AGENTS.md.tmpl
  git/
    default/
      manifest.yml
      pre-commit.tmpl
```

Each pack is a directory; the renderer never follows symlinked packs. Template files end in `.tmpl` and use [Go text/template syntax](../reference/templates.md).

Outputs land in each enabled service's hub directory:

| Kind | Output destination |
|------|--------------------|
| `ide` | `<svc.Dir>/<rel>` — e.g. `services/main/.vscode/settings.json` |
| `ai` | `<svc.Dir>/<rel>` — e.g. `services/main/AGENTS.md` |
| `git` | `<svc.Dir>/src/.git/hooks/<basename>` — chmod `0755` on every run |

By default, only `type: app` services render (for all three kinds — except `ai`, which still defaults `true` only for apps). Other service types opt in with `services.<name>.render.<kind>.enabled: true`.

## The `manifest.yml` file

Every pack must contain a `manifest.yml` at its root. It is the single source of truth for what gets rendered — the renderer never walks the pack on its own.

```yaml
render:
  - from: .vscode/settings.json.tmpl
    to:   .vscode/settings.json
  - from: .devcontainer/devcontainer.json.tmpl
    to:   .devcontainer/devcontainer.json

symlinks:                              # ide / ai only — git rejects symlinks
  - link: CLAUDE.md
    to:   AGENTS.md
```

Per-kind constraints:

| Kind | `to` shape | `symlinks` block |
|------|------------|------------------|
| `ide` | any path inside the hub | allowed |
| `ai`  | any path inside the hub | allowed; each `to` must reference a `render` entry |
| `git` | **basename only** (no slashes) | rejected |

Unknown keys in `manifest.yml` are a hard error (strict YAML decode). A file inside the pack that is not listed in `render:` is silently ignored — add it to the manifest to include it.

## Pack resolution: which pack does a service use?

A service can pin a pack explicitly:

```yaml
# workspace/services/api/service.yml
render:
  ide:
    template: corporate-vscode    # use workspace/templates/ide/corporate-vscode/
```

If `render.<kind>.template` is set, that pack must exist — typos are a hard error, never a silent fallback to `default/`. That protects you from `templete: corporete-vscode` accidentally rendering whatever `default/` happens to contain.

When `render.<kind>.template` is unset, the resolver walks an implicit chain — first hit wins:

1. `workspace/templates/<kind>/<service-name>/`
2. each ancestor in the service's `extends:` chain, ancestor-by-ancestor
3. `workspace/templates/<kind>/default/`

This matches the way `extends:` works in services: a child like `main-debug extends: main` typically inherits the parent's IDE pack rather than dropping straight to `default/`.

## Collision policies (deepest vs shallowest)

When two services share the same `dir:` (the classic case is a child `extends:` parent layout where both point at `services/main`), only one of them renders for any given kind — but which one depends on the kind:

| Kind | Collision policy | Why |
|------|------------------|-----|
| `ide` | **deepest wins** | IDE configs are about per-variant behavior (different debugger, different launch profile). The most specialized variant owns the rendered files. |
| `git` | **deepest wins** | Same rationale — hooks tend to vary per variant. |
| `ai`  | **shallowest wins** | `AGENTS.md` describes the hub's canonical identity. Variants share that identity; the parent owns the description. |

Ties at the same depth break lexicographically by service name. Losing services emit a warning naming the winner and the contested directory — that warning is your hint that two services are accidentally colliding.

`dwe render ide main` (with an explicit argument) still respects the collision policy: if `main-debug` is the deepest variant sharing `services/main`, it renders instead, and an info line announces the substitution.

## Dry run — render everything

After editing `manifest.yml` or any `.tmpl`, run the renderers manually to see the output:

```sh
dwe render ide       # render IDE files for every eligible service
dwe render ai        # render AGENTS.md (and symlinks)
dwe render git       # write executable git hooks (mode 0755)
```

Each subcommand accepts an optional `[service]` argument to scope down:

```sh
dwe render ide api
dwe render ai api
dwe render git api
```

If you want to see what would happen without writing, point the service argument at one service first and inspect the result, then commit. There is no separate `--dry-run` flag — the commands overwrite existing regular files at the destinations without prompting, but they refuse to overwrite a symlink (you'll get a clear error and the offending path).

In normal day-to-day work, deploy pipelines run these automatically — you shouldn't usually need to call them by hand unless you're authoring or debugging a pack.

## Personal overrides with `<pack>.local/`

Want your personal editor preferences without committing them to the team's tracked pack? Drop a sibling shadow pack:

```
workspace/templates/ide/
  default/                       # tracked, team-wide
    manifest.yml
    .vscode/settings.json.tmpl
  default.local/                 # gitignored, personal
    .vscode/settings.json.tmpl   # substitutes the one above
```

When the renderer reads `default/.vscode/settings.json.tmpl`, it first checks `default.local/.vscode/settings.json.tmpl`. If your local file exists, that one is used as the source instead, and the renderer prints:

```
using local override: workspace/templates/ide/default.local/.vscode/settings.json.tmpl
```

Key rules:

- The shadow pack is **input substitution**, not output redirection. The rendered file still lands at the same destination (e.g. `services/main/.vscode/settings.json`).
- The shadow pack needs to contain **only the files you're overriding**. It does not need its own `manifest.yml` — the canonical pack's manifest still drives what gets rendered.
- Add `workspace/templates/*/*.local/` (or a broader `*.local/` rule) to `.gitignore`.

This matches DWE's broader local-override convention:

| Canonical (tracked) | Local sibling (gitignored) |
|---------------------|----------------------------|
| `workspace/workspace.yml` | `workspace/local.yml` |
| `workspace/docker.yml` | `workspace/docker.local.yml` |
| `workspace/templates/<kind>/<pack>/` | `workspace/templates/<kind>/<pack>.local/` |

### Caveat for IDE/AI overrides

For `git`, the rendered output goes into `.git/hooks/` which is never tracked — overrides are fully private with no friction.

For `ide` and `ai`, the rendered output is typically a tracked file (`.vscode/settings.json`, `AGENTS.md`). A personal override that produces a different file means re-running `dwe render ide` will create a tracked-file diff for you. Don't commit those diffs — `git stash`, `git checkout -- <path>`, or a personal pre-commit guard are all fine ways to keep them local.

## What gets tracked vs gitignored

| Path | Tracked? | Note |
|------|----------|------|
| `workspace/templates/<kind>/<pack>/` | yes | The team-wide pack — the source of truth. |
| `workspace/templates/<kind>/<pack>.local/` | **no** | Personal overrides. Gitignore the `.local/` pattern. |
| `services/<name>/.vscode/settings.json` (and similar IDE outputs) | usually yes | Rendered output; commit so teammates see the same editor config without running `dwe render ide`. |
| `services/<name>/AGENTS.md`, `services/<name>/CLAUDE.md` | usually yes | Rendered output; same reasoning. |
| `services/<name>/src/.git/hooks/<name>` | **never** | Lives inside `.git/`, which git itself ignores. |

A typical project commits the rendered IDE and AI outputs so a fresh clone has working configs immediately, then re-runs `dwe render ide` / `dwe render ai` whenever the pack or `service.yml` changes. Git hooks are the exception — they live inside `.git/` and must be re-rendered after every clone.

## See also

- [render reference index](../reference/render/index.md) — full schema, path-safety guards, edge cases
- [`render ide`](../reference/render/ide.md) — IDE-specific details and template variables
- [`render ai`](../reference/render/ai.md) — `AGENTS.md` rendering and symlink semantics
- [`render git`](../reference/render/git.md) — git-hook rendering and worktree caveats
- [add-a-service](add-a-service.md) — adding a service that participates in IDE/AI/git rendering
