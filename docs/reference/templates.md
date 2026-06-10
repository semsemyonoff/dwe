# Templates

Go templates (with the [go-sprout](https://docs.atom.codes/sprout/) function library) are evaluated across multiple DWE surfaces: info dashboard items, declarative commands, pipeline `when:` conditions, the `message` builtin, and the IDE / AI / git / config render packs. This page is the single reference for the template engine, the available helpers, and the conventions shared by every site. Note that the **config** render pack diverges from the other render kinds: it uses the `${...}` shorthand substrate (lenient — absent → `""`), not the strict `{{ ... }}` syntax of the ide/ai/git packs.

## Contents

- [Where templates are evaluated](#where-templates-are-evaluated)
- [Two syntaxes: shorthand and full templates](#two-syntaxes-shorthand-and-full-templates)
  - [Quoting templates inside YAML](#quoting-templates-inside-yaml)
- [Render context per site](#render-context-per-site)
- [Built-in `text/template` functions](#built-in-texttemplate-functions)
- [Domain helper: appURL](#domain-helper-appurl)
- [Sprout registries](#sprout-registries)
- [Command-scope resolvers](#command-scope-resolvers)
- [Strict rendering (render packs)](#strict-rendering-render-packs)
- [Common patterns](#common-patterns)
- [Conventions and gotchas](#conventions-and-gotchas)
- [Further reading](#further-reading)

## Where templates are evaluated

| Site | Syntax | Context | Notes |
|------|--------|---------|-------|
| `info.yml` — `text`, `value`, `when` | `{{ ... }}` | Resolved project config | See [info.md](config/info.md) |
| `workspace/commands/` — `cmd`, `argv`, `workdir`, `compose_args`, `env`, `messages.*`, `confirmation_text`, `files.*.path`/`candidates`, workflow `steps[].with[<key>]` / `steps[].when` | `${...}` and `{{ ... }}` | Command context (`.Raw` + `.Params` + `.Context` + `.Files` + `.Host`) | See [commands/](config/commands/index.md) |
| `deploy.yml` / `lifecycle.yml` / `reset.yml` — `when: type: template, expr:` | `{{ ... }}` | Resolved project config | Evaluated at plan time. See [deploy](config/deploy/index.md) |
| `message` builtin — `text:` | `{{ ... }}` | Resolved project config | See [message builtin](config/deploy/builtins.md#message) |
| `docker.yml` — `project_name` | `${...}` only | Resolved project config (`.Raw` lookups) | Dot-path lookups (no `{{ }}` logic). See [docker.md](config/docker.md) |
| `workspace/templates/git/<pack>/**/*.tmpl` | `{{ ... }}` | Render-pack context (`.Project`, `.Service`, `.ServiceCfg`, `.Runtime`, `.Services`, `.Cfg`) | Strict mode. See [render/git.md](render/git.md) |
| `workspace/templates/ide/<pack>/**/*.tmpl` | `{{ ... }}` | Render-pack context (`.Project`, `.Service`, `.ServiceCfg`, `.Runtime`, `.Services`, `.Cfg`) | Strict mode. See [render/ide.md](render/ide.md) |
| `workspace/templates/ai/<pack>/**/*.tmpl` | `{{ ... }}` | Render-pack context (`.Project`, `.Service`, `.ServiceCfg`, `.Runtime`, `.Services`, `.Cfg`) | Strict mode. See [render/ai.md](render/ai.md) |
| `workspace/templates/config/<pack>/**` | `${...}` | Resolved project config (`.Raw`) + curated `${services.<name>...}` subset + `${generated.<name>}` | Lenient (absent → `""`). See [render/config.md](render/config.md) |
| `params.*.default_from`, `context.*.from` | — | — | Plain dot-paths only (no template expressions). |

## Two syntaxes: shorthand and full templates

Two interpolation layers exist; both are evaluated by the same engine.

**`${...}` — shorthand lookups.** Compact, no logic. Used in command definitions and `docker.yml`'s `project_name`. The compiler rewrites each `${...}` into an equivalent `{{ ... }}` expression at parse time.

**`{{ ... }}` — full Go `text/template`.** Conditionals, loops, pipelines, helper functions. Available everywhere templates are evaluated.

```yaml
# Mixed in a single string (command site)
path: "${param.dump_dir}/${param.database}{{ if .Params.dump_date }}_{{ now | date \"2006-01-02\" }}{{ end }}.sql.gz"
```

Rule of thumb: use `${...}` for plain lookups; reach for `{{ ... }}` whenever you need a condition, a comparison, a default, a string transform, or a pipeline.

### `${...}` namespaces

`${...}` resolves through namespaces; the first segment routes to a specific data source:

| Expression | Resolved as |
|------------|-------------|
| `${vars.db.user}` | Dot-path into the merged DWE config (`Raw`) |
| `${param.<name>}` | Resolved param value |
| `${context.<name>}` | Resolved context value |
| `${files.<id>.path}` | Absolute path of a resolved file artefact |
| `${host.uid}` / `${host.gid}` | Effective UID/GID (1000:1000 on macOS, real values on Linux) |
| `${generated.<name>}` | Per-service value harvested into `.dwe/generated.yml` (config render packs only; absent → `""`). See [render/config.md](render/config.md) |

Anything that doesn't match a known namespace (`${foo}`, `${a.b.c}`) is treated as a dot-path lookup against `Raw`. A literal `$$` passes through unchanged.

### Quoting templates inside YAML

The string between `{{ }}` is the same in every YAML form — only the wrapping changes:

```yaml
# double-quoted scalar: inner " must be escaped as \"
path: ".dwe/logs/{{ now | date \"2006-01-02\" }}.log"

# single-quoted scalar: no escaping needed (recommended for templates)
path: '.dwe/logs/{{ now | date "2006-01-02" }}.log'

# literal block scalar: no escaping needed
cmd: |
  echo "{{ now | date "2006-01-02" }}"
```

Prefer single-quoted (`'...'`) scalars for one-line templates whose body contains `"`. Reserve double-quoted (`"..."`) for strings that need YAML's `\n`/`\t` escape sequences. The `\|` you may see inside this page's tables is markdown-cell escaping for the rendered docs — your YAML always uses a plain `|` inside `{{ }}`.

## Render context per site

The data exposed to a template depends on the site. Field access uses dot syntax (`.Project.Name`).

**Commands:**

| Path | Contents |
|------|----------|
| `.Raw` | Merged `workspace.yml` + `defaults.yml` + `local.yml` as a nested map |
| `.Params` | Resolved param values (map keyed by param name) |
| `.Context` | Resolved context values (map keyed by context name) |
| `.Files` | Resolved file artefacts (map keyed by file id; each has a `.Path` field) |
| `.Host.UID` / `.Host.GID` | Host UID/GID strings |

**Info, pipelines, `message` builtin:** the resolved project config — addressed via the same dot syntax as the render-pack `.Cfg` below (e.g. `.Project.Name`, `((index .Services "main").Port "http")`, `(index .Services "catalog").Enabled`).

**Render packs (git / ide / ai, strict):**

| Variable | Source |
|----------|--------|
| `.Project` | `project:` block from `workspace.yml` |
| `.Service` | service name (the map key in `services:`) |
| `.ServiceCfg` | effective service config after `extends` resolution |
| `.Runtime` | merged `runtime` block (`.Runtime.UseHTTPS`, `.Runtime.SPX.Path`). Per-service ports / hosts live on each service entry (see `.Services` below). |
| `.Services` | services keyed by name. Use `(index .Services "<name>")` to fetch; per-entry helpers `.Port "<port-name>"` / `.Host "<host-name>"` / `.PortScheme "<port-name>"` (returns `""` if no override) / `.EffectiveScheme "<port-name>" .Runtime.UseHTTPS` (returns `"http"` / `"https"` resolved through the per-port → service → runtime precedence chain). Type-filtered subsets via `.AppServices` / `.ToolServices` / `.InfraServices`. |
| `.Cfg` | the merged project config (advanced). `.Cfg.Raw` is the post-merge config tree (`services.*` is injected from per-service `service.yml` files). Dot syntax (`.Cfg.Raw.git.project_prefix`) works only for identifier-safe keys; use `{{ index .Cfg.Raw "my-key" }}` for keys with hyphens, dots, leading digits, etc. Prefer the dedicated fields above for common cases. |

IDE and AI packs render into tracked project files. Avoid consuming developer-local or secret keys via `.Cfg.Raw` in those templates — values from `local.yml` will produce per-developer diffs. Git hooks render under `.git/hooks/` (gitignored) and are not subject to this constraint.

## Built-in `text/template` functions

The standard library exposes these out of the box. Full reference: [pkg.go.dev/text/template#hdr-Functions](https://pkg.go.dev/text/template#hdr-Functions).

| Function | Use |
|----------|-----|
| `eq`, `ne`, `lt`, `le`, `gt`, `ge` | Comparison |
| `and`, `or`, `not` | Boolean logic |
| `len` | Length of string / slice / map |
| `index` | Map / slice indexing |
| `printf` | Formatted strings (Go format verbs) |
| `print`, `println` | Concatenation |
| `html`, `js`, `urlquery` | Escaping |

Control structures: `{{ if }}`, `{{ range }}`, `{{ with }}`, `{{ define }}` / `{{ template }}`.

```yaml
# emit a flag only when a bool param is true
argv:
  - "{{ if .Params.fresh }}--fresh{{ end }}"

# nested if / else if / else
env:
  LOG_LEVEL: |-
    {{ if eq .Params.profile "prod" }}error
    {{ else if eq .Params.profile "stage" }}warn
    {{ else }}debug{{ end }}

# range with index
env:
  TAGS: "{{ range $i, $t := .Params.tags }}{{ if $i }},{{ end }}{{ $t }}{{ end }}"

# with / default
cmd: "mariadb -u${vars.db.user}{{ with .Params.database }} -D{{ . }}{{ end }}"
env:
  REGION: '{{ or .Params.region "us-east-1" }}'
```

### Trimming whitespace

`{{- ... -}}` strips surrounding whitespace. Useful when a multi-line `{{ if }}` block is rendered into a single shell argument:

```yaml
cmd: |-
  echo "{{- if .Params.verbose -}}verbose{{- else -}}quiet{{- end -}}"
```

## Domain helper: appURL

The only project-specific helper. Builds a URL from host, port, HTTPS flag, and optional path. The port is omitted when it matches the scheme default (80 for http, 443 for https).

Signature: `appURL host port useHTTPS [path]`

```yaml
# App hostname + app port (proxied via the main service)
value: '{{ appURL ((index .Services "main").Host "web") ((index .Services "main").Port "http") .Runtime.UseHTTPS }}'
# → "http://laravel.localhost" or "https://laravel.localhost"

# Tool hostname + main reverse-proxy port (tool routed via the main app, not the tool's direct port)
value: '{{ appURL ((index .Services "adminer").Host "web") ((index .Services "main").Port "http") .Runtime.UseHTTPS "/login" }}'
# → "http://adminer.localhost/login"
```

## Sprout registries

The following registries from [go-sprout](https://docs.atom.codes/sprout/registries/) are available everywhere templates are evaluated.

| Registry | Examples | Description |
|----------|----------|-------------|
| `std` | `default`, `ternary`, `empty`, `coalesce` | Defaults, conditionals, emptiness checks |
| `strings` | `hasSuffix`, `hasPrefix`, `lower`, `upper`, `trim`, `replace`, `split` | String manipulation |
| `numeric` | `add`, `sub`, `mul`, `div`, `max`, `min` | Numeric operations |
| `slices` | `first`, `last`, `slice`, `join`, `reverse`, `uniq` | List/array operations |
| `maps` | `keys`, `values`, `has`, `pick`, `omit` | Map/object operations |
| `regexp` | `regexMatch`, `regexReplace`, `regexSplit` | Regular expression matching |
| `conversion` | `toInt`, `toFloat`, `toString`, `toBool` | Type conversion |
| `time` | `now`, `date`, `dateFormat`, `duration` | Date/time operations |
| `filesystem` | `pathBase`, `pathDir`, `pathExt`, `pathClean`, `osBase`, `osDir` | Path manipulation |
| `semver` | `semverCompare`, `semverSort` | Semantic version operations |

**Hermetic by construction.** The helper set is built without any function that touches the environment, filesystem, network, or random/crypto sources. Sprout's `shuffle` (math/rand seeded from crypto) and `hello` (debug stub) are deliberately removed.

For full per-function documentation see the [sprout registries reference](https://docs.atom.codes/sprout/registries/).

## Command-scope resolvers

Three additional helpers are available **only** inside `workspace/commands/` templates. They accept raw maps and walk dot-paths, returning `""` for any missing key (no template error).

| Helper | Signature | Use |
|--------|-----------|-----|
| `resolve` | `resolve .Raw "vars.db.host"` | Dot-path lookup in merged config. Equivalent to `${vars.db.host}`. |
| `resolveMap` | `resolveMap .Params "name"` | Key lookup in a flat `map[string]any`. Equivalent to `${param.name}` / `${context.name}`. |
| `resolveFile` | `resolveFile .Files "id" "path"` | Subkey lookup in a resolved file artefact. Equivalent to `${files.id.path}`. |

These exist so the `${...}` shorthand can be expanded to portable Go-template form, and so authors can reach raw config when the dotted `.Raw.<x>.<y>` style is awkward (keys with dots, numeric keys, etc.).

## Strict rendering (render packs)

`render ide`, `render ai`, and `render git` parse templates with `{{.Option "missingkey=error"}}` semantics: a typo like `{{.Servic.Name}}` aborts the entire pack render rather than writing `<no value>` to disk. Guard genuinely optional fields with `{{if ...}}`:

```gotemplate
{{if .ServiceCfg.CLI.Workdir}}WORKDIR={{.ServiceCfg.CLI.Workdir}}{{end}}
```

Other sites (info, commands, pipeline conditions, `message`) use lenient rendering — a missing key resolves to `<no value>` or empty string, never an error.

## Common patterns

| Task | Snippet |
|------|---------|
| Current date | `{{ now \| date "2006-01-02" }}` |
| Current datetime | `{{ now \| date "2006-01-02_15-04-05" }}` |
| Path basename | `{{ .Params.script_path \| pathBase }}` |
| Path directory | `{{ .Params.script_path \| pathDir }}` |
| Default / fallback | `{{ .Value \| default "N/A" }}` or `{{ or .Params.region "us-east-1" }}` |
| Conditional value | `{{ if eq .State "ready" }}Ready{{ else }}Not ready{{ end }}` |
| Empty-guarded block | `{{ with .Params.database }} -D{{ . }}{{ end }}` |
| Join list | `{{ join "," .Params.tags }}` |
| Raw config lookup | `{{ resolve .Raw "vars.db.host" }}` (commands only) |
| Build URL | `{{ appURL ((index .Services "main").Host "web") ((index .Services "main").Port "http") .Runtime.UseHTTPS }}` |

## Conventions and gotchas

- **Prefer `path*` over `os*` for container paths.** `pathBase` / `pathDir` use forward-slash semantics; `osBase` / `osDir` follow the host OS separator. Container paths should be predictable when rendered on macOS hosts — stick with the `path*` variants unless you genuinely need OS-specific behaviour.

- **`date` is a filter, not a constructor.** It takes a format string and a `time.Time`, not the other way around:
  - `{{ now | date "2006-01-02" }}` ✓
  - `{{ date "2006-01-02" }}` ✗ (no time value)

  The format string uses Go's reference time `Mon Jan 2 15:04:05 MST 2006` — see [Go date/time formatting cheat sheet](https://yourbasic.org/golang/format-parse-string-time-date-example/).

- **`when:` truthiness.** A rendered `when:` value is truthy unless it equals `""`, `"false"`, or `"0"` (after trimming). Comparisons that return a Go `bool` render as `"true"`/`"false"`; comparisons that return an integer-like value (e.g. lengths) render as decimal strings.

- **No env, FS, network, or randomness.** Templates are evaluated in a hermetic FuncMap by design. If a template needs project state, surface it through the resolved project config (info / pipelines) or through a `context.<name>: from: <dot.path>` declaration (commands).

- **Mixing `${...}` and `{{ ... }}` is fine.** They share the same context and render in one pass — `${...}` is rewritten to template calls before parsing.

## Further reading

- [`text/template` package docs](https://pkg.go.dev/text/template) — full Go template language reference
- [Action syntax](https://pkg.go.dev/text/template#hdr-Actions) — `{{ if }}`, `{{ range }}`, `{{ with }}`
- [Built-in functions](https://pkg.go.dev/text/template#hdr-Functions) — `eq`, `printf`, `index`, etc.
- [Pipelines and the `.` cursor](https://pkg.go.dev/text/template#hdr-Pipelines)
- [Sprout registries](https://docs.atom.codes/sprout/registries/) — per-function documentation
- [Go date/time formatting cheat sheet](https://yourbasic.org/golang/format-parse-string-time-date-example/) — reference layout `2006-01-02 15:04:05` and common format strings
