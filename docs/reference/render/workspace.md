# dwe render workspace

Render workspace-owned template packs into the project root: root agent documents,
`.mcp.json`, tool configuration such as `.ralphex/`, or other shared files. Packs
live under `workspace/templates/workspace/<pack>/`; destinations are relative to
the directory containing the root `workspace.yml`.

Workspace packs use the [shared manifest schema](index.md#shared-manifest-schema)
and [local overrides](index.md#local-overrides). They have no service selection,
`extends` resolution, or implicit default pack.

## Contents

- [Activation and config layers](#activation-and-config-layers)
- [Pack layout and manifest](#pack-layout-and-manifest)
- [Templates, verbatim files and permissions](#templates-verbatim-files-and-permissions)
- [Template data](#template-data)
- [Local overrides](#local-overrides)
- [Protected paths and collisions](#protected-paths-and-collisions)
- [Planning and writing](#planning-and-writing)
- [CLI and validation](#cli-and-validation)
- [Starter packs](#starter-packs)
- [Limitations](#limitations)
- [Related references](#related-references)

## Activation and config layers

Declare the packs to render in the top-level `render.workspace` list:

```yaml
# workspace.yml
render:
  workspace: [ralphex, root-agents]
```

Without arguments, `dwe render workspace` renders that list in its declared order.
An absent or empty list prints `no workspace packs configured in render.workspace`
and exits successfully. Every selected pack must exist; a missing pack is an
error, with no fallback.

The list follows the ordinary [config layer order](../config/workspace.md): root
`workspace.yml` → `workspace/defaults.yml` → `workspace/local.yml`. Later lists
replace earlier ones entirely; they are not appended. To disable the configured
packs locally:

```yaml
# workspace/local.yml
render:
  workspace: []
```

Pack names must match `[A-Za-z0-9][A-Za-z0-9_-]*`; duplicate names are rejected.
These checks run during rendering and template validation rather than config
loading.

Rendering is explicit. `dwe run` and deploy do not automatically render workspace
packs. A project can opt in with a project-level pipeline step:

```yaml
- name: render-workspace
  type: dwe
  cmd: "render workspace"
```

## Pack layout and manifest

A minimal pack:

```text
workspace/templates/workspace/root-agents/
├── manifest.yml
├── AGENTS.md.tmpl
└── workspace-notes.txt
```

```yaml
# workspace/templates/workspace/root-agents/manifest.yml
render:
  - from: AGENTS.md.tmpl
    to: AGENTS.md
  - from: workspace-notes.txt
    to: workspace-notes.txt
```

The manifest uses strict YAML decoding: unknown fields, empty files, and empty
manifests are errors. Only declared sources are processed; other pack files are
ignored.

| Field | Meaning |
|-------|---------|
| `render[].from` | Required source path relative to the pack. Must resolve to a regular file within the pack or its local override. |
| `render[].to` | Required output path relative to the project root. Nested paths are allowed; absolute paths, the root itself, and escaping paths are rejected. |
| `symlinks[].link` | Required link path relative to the project root, subject to the same destination protections. |
| `symlinks[].to` | Required target relative to the project root. Must match a `render[].to` in the same manifest. |

For example, to expose `AGENTS.md` under a second name for another tool:

```yaml
symlinks:
  - link: GEMINI.md
    to: AGENTS.md
```

The pack directory, manifest, sources, and their parent paths must not be
symlinks. Generated symlinks use relative targets computed from the link's
location. An existing symlink is retained if correct or retargeted if necessary;
a regular file or directory at the link path is an error.

## Templates, verbatim files and permissions

The **source name** determines processing:

| `from` | Processing |
|--------|------------|
| Ends in `.tmpl` | Execute as Go `text/template`, with `missingkey=error`. |
| Any other name | Copy bytes exactly; no template parsing or variable substitution. |

For example, `config.tmpl` can use `{{ .Project.Name }}`, while a prompt source
`blocks/task.md` preserves `{{PLAN_FILE}}` for the tool that consumes it. The
output name is always the explicit `to`; no suffix is removed automatically.
`${...}` is not expanded by workspace packs.

Permissions follow the resolved source, including a local override:

- Any executable bit in the source produces mode `0755`.
- A source without executable bits produces mode `0644`.

DWE explicitly applies the mode on every render, including existing files. Adding
or removing the source's executable bit therefore changes the output's executable
bit on the next render. Other source permission distinctions are not preserved.

## Template data

`.tmpl` files receive the shared pack data with project-wide fields populated:

| Field or method | Value |
|-----------------|-------|
| `.Project` | Merged `project:` block. |
| `.Runtime` | Merged `runtime:` block. |
| `.Services` | All effective service configs, including disabled services. |
| `.AppServices`, `.ToolServices`, `.InfraServices` | Service maps filtered by type. |
| `.Cfg` | Sanitized merged config; `.Cfg.Raw` exposes the normalized merged map. |
| `.Commands` | Declared public command index, sorted by id. |
| `.CommandGroups` | Authored command groups with declared counts, sorted by id. |
| `.Service`, `.Resolved` | Empty strings: there is no current service. |
| `.ServiceCfg` | Zero-value service config. |
| `.ServiceCommands`, `.ServiceCommandGroups` | Empty: there is no service binding. |

For example, an `AGENTS.md.tmpl` can list services and commands:

```gotemplate
# {{ .Project.Name }}

## Services
{{ range $name, $svc := .Services }}- {{ $name }} ({{ $svc.Type }})
{{ end }}
## Declared commands
{{ range .Commands }}- {{ .ID }}: {{ .Summary }}
{{ end }}
```

The command index uses authored English strings and ignores `hide:` conditions;
tracked output therefore does not change with locale or live stack state. Private
commands are excluded. If command files fail to load, rendering emits a warning
and continues with an empty index; use `dwe validate commands` to diagnose them.
See [Declared command index](ai.md#declared-command-index) for the entry fields.

Only standard Go `text/template` functions such as `index`, `printf`, `and`, and
`or` are available; workspace packs do not register sprout or `appURL` helpers.
Missing map keys reached through dot notation are errors. Guard optional maps
with `with` and use `index` for keys that are not valid dot identifiers.

Config is loaded in sanitized mode: encrypted scalar secrets remain
`ENC[age:…]` markers even when the developer has a working decryption identity.
Sanitization does not remove ordinary plaintext values. Avoid developer-local or
sensitive `.Cfg.Raw` values in tracked outputs: local config still participates
in the merge.

## Local overrides

Place an alternative source at the same relative path in the sibling
`workspace/templates/workspace/<pack>.local/` directory:

```text
workspace/templates/workspace/root-agents/AGENTS.md.tmpl
workspace/templates/workspace/root-agents.local/AGENTS.md.tmpl
```

The override wins when present; missing override files fall back to the canonical
pack. A symlink, directory, or other invalid override is an error rather than a
fallback. The canonical pack remains required, and its `manifest.yml` is always
used; overrides substitute sources, not manifests or destinations.

Add a pattern such as `workspace/templates/*/*.local/` to the project's
`.gitignore` for private overrides. Rendering still writes the manifest's root
destinations, which may be tracked: an ignored input does not make its output
private. The CLI reports every local override it uses.

## Protected paths and collisions

A render destination or symlink location at or beneath any of these root paths
is rejected:

| Protected root path | Purpose |
|---------------------|---------|
| `workspace.yml` | Root project config. |
| `workspace/` | Tracked configuration, commands, scripts and template sources. |
| `services/` | Service hubs and repositories. |
| `.dwe/` | DWE runtime state. |
| `.git/` | Root Git metadata. |
| `.env` | Generated root environment file. |

Protection compares cleaned path components without regard to case:
`Workspace.yml` and `.GIT/hooks/pre-commit` are protected too. `.env.example` is
a separate path and is allowed.

Destinations across all selected packs must be distinct. Comparisons are
case-insensitive and include file-versus-directory prefixes: `.ralphex` collides
with `.ralphex/config`, and `AGENTS.md` collides with `agents.md`. These checks
apply to both rendered files and symlink locations, including entries within one
pack. There is no winning pack or overwrite precedence for collisions.

## Planning and writing

Rendering has two phases:

1. Plan all selected packs together: load and validate manifests, resolve sources
   and overrides, reject protected paths and collisions, prepare every output in
   memory, and check all destination paths against the current filesystem.
2. Write the prepared files and create or update symlinks, in pack and manifest
   order. Missing parent directories are created.

A planning error writes nothing, even when the error is in the last pack. Checks
reject escaping paths, symlinked parents, and rendered-file destinations that
are symlinks, directories, or other non-regular files. Existing regular output
files are overwritten without a confirmation prompt.

The write phase rechecks paths but is not a transaction. A later filesystem or
permission error does not roll back files already written. Avoid concurrent
changes to destination paths while rendering.

## CLI and validation

```bash
dwe render workspace                       # use render.workspace
dwe render workspace ralphex root-agents   # use only these packs
dwe validate templates workspace           # plan the configured list, without writes
```

Explicit names replace the configured list for that invocation. They may select
packs absent from `render.workspace`; validation still checks the configured
list, so add a pack there to include it in regular project validation.

Shell completion offers available canonical pack directories, excluding names
already supplied and `.local` override directories. Invalid project or config
paths yield no completions or diagnostics. Use `dwe render workspace --help` for
the current CLI surface.

Rendering uses human-readable output and has no JSON mode. It does not run
preflight or acquire project operation locks. The validator is read-only and uses
the same plan phase and sanitized template data, including cross-pack collision
checks. An empty list produces an info diagnostic. Workspace template diagnostics
do not join the lifecycle preflight gate.

## Starter packs

The repository includes tested, workspace-owned starters under
[`examples/workspace-packs/README.md`](../../../examples/workspace-packs/README.md):

- [ralphex](../../../examples/workspace-packs/ralphex/README.md) — multi-repository
  scope, tool config, scripts and prompt blocks. Follow
  [Run ralphex in a workspace](../../guides/run-ralphex-in-a-workspace.md).
- [root-agents](../../../examples/workspace-packs/root-agents/README.md) — root
  `AGENTS.md` from a template and a verbatim file. Move the existing root
  document into the template first: rendering overwrites it.

Take the examples from the release tag matching your installed `dwe --version`,
then maintain your own copies. They are not embedded in the binary or installed
by `dwe init`.

## Limitations

- Removing a manifest entry or disabling a pack leaves its old outputs on disk;
  DWE does not track ownership or prune them.
- A manifest renders files and creates symlinks; it does not execute hooks or
  commands. Run any tool-specific generation separately as a declared command.
- Workspace packs do not decrypt `.age` sources or use the service-config
  `${generated.*}` store. Non-`.tmpl` sources are copied verbatim.
- There is no automatic render during lifecycle commands and no write-phase
  rollback.

## Related references

- [Render index](index.md) — kinds, shared manifests and local overrides.
- [Workspace config](../config/workspace.md) — top-level `render` and merge layers.
- [Templates](../templates.md) — template substrates and context per site.
- [Validation](../config/validate.md) — domains and diagnostic output.
