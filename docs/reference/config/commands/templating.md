# Templating in command files

Devbox uses two layers of interpolation in command definitions: the lightweight `${...}` syntax and full Go `text/template` blocks. Both are evaluated by the same engine and may be mixed freely.

The full template engine — `${...}` namespaces, the render context, Go template control flow, built-in functions, sprout registries, and conventions — is documented in [Templates](../../templates.md). This page only covers the parts specific to command files; everything else is cross-cutting.

## Contents

- [Command render context](#command-render-context)
- [Command-scope resolvers](#command-scope-resolvers)
- [Examples specific to commands](#examples-specific-to-commands)
- [Where templates are evaluated](#where-templates-are-evaluated)
- [Command-template space (the full reference)](#command-template-space-the-full-reference)

## Command render context

Templates inside `devbox/commands/` render against `RenderContext`:

| Path | Contents |
|------|----------|
| `.Raw` | Merged `devbox.yml` + `defaults.yml` + `local.yml` as a nested map |
| `.Params` | Resolved param values (map keyed by param name) |
| `.Context` | Resolved context values (map keyed by context name) |
| `.Files` | Resolved file artefacts (map keyed by file id; each has a `.Path` field) |
| `.Host.UID` / `.Host.GID` | Host UID/GID strings |

The `${...}` namespaces (`${db.x}`, `${param.x}`, `${context.x}`, `${files.id.path}`, `${host.uid}`) route into these same fields. See [Templates](../../templates.md) for the full namespace table.

## Command-scope resolvers

Three template helpers are available **only** in command files; they are not registered in info or render-pack templates:

| Helper | Use |
|--------|-----|
| `resolve .Raw <dot.path>` | Dot-path lookup in merged config (same as `${dot.path}`) |
| `resolveMap .Params <key>` | Key lookup in a flat map (same as `${param.key}` / `${context.key}`) |
| `resolveFile .Files <id> <subkey>` | Subkey lookup in a resolved file artefact |

They walk maps and return `""` on miss — useful when the key has a dot or a numeric segment that breaks the direct `.Raw.x.y` form.

## Examples specific to commands

```yaml
# helpers chained via pipe
files:
  log:
    access: write
    path: ".devbox/logs/{{ .Params.task }}_{{ now | date \"2006-01-02_15-04-05\" }}.log"
    mkdir: true

# pipeline form: pass a value through a function
env:
  SCRIPT_NAME: '{{ .Params.script_path | pathBase }}'

# mixing the two syntaxes
path: "${param.dump_dir}/${param.database}{{ if .Params.dump_date }}_{{ now | date \"2006-01-02\" }}{{ end }}.sql.gz"
```

## Where templates are evaluated

| Location | Templated |
|----------|-----------|
| `messages.success`, `messages.error` | yes |
| `confirmation_text` | yes |
| `cmd`, `argv`, `workdir`, `compose_args` | yes |
| `env:` map values | yes |
| `files.*.path`, `files.*.candidates[].path/glob/match` | yes |
| `params.*.default_from`, `context.*.from` | no — plain dot-paths only |
| Workflow `steps[].with[<key>]`, `steps[].when` | yes |
| `description`, `group.title`, `group.description` | no — printed verbatim by `commands list` / `commands inspect` / completion |

## Command-template space (the full reference)

When the docs say *"command template space"* this is the set of expressions available inside any templated field of a command:

| Expression | Meaning |
|------------|---------|
| `${<dot.path>}` | Lookup in merged `DevboxConfig.Raw` |
| `${param.<name>}` | Resolved param |
| `${context.<name>}` | Resolved context value |
| `${files.<id>.path}` | Absolute path of a file artefact |
| `${host.uid}` / `${host.gid}` | Effective UID/GID for container `--user` |
| `{{ .Raw.x.y }}` | Direct dot access on the merged config |
| `{{ .Params.<name> }}` | Direct dot access on params |
| `{{ .Context.<name> }}` | Direct dot access on context |
| `{{ .Host.UID }}` | Host info (Go template form) |
| `{{ now \| date "..." }}` / `{{ pathBase }}` / `{{ pathDir }}` / `{{ appURL ... }}` | Helper functions (sprout + domain) |

Use the simpler `${...}` form for one-off lookups; reach for `{{ ... }}` when you need conditionals, comparisons, or pipelines.
