# Info Auto-Blocks, Service Icon, and `info.paths`

## Overview

Replace hand-authored `URLs` and `Hosts` subgroups in `devbox/info.yml` with two declarative item types that build themselves from service data: `type: auto-urls` and `type: auto-hosts`. Give each service two new fields — a top-level `icon:` (visual hint reused by info, status, and the command browser) and an `info:` block with `title:` override and an ordered `paths:` list (sub-URLs under the service's main URL). Remove the legacy "Devbox / Project — <name>" section from the rendered dashboard (project name already lives in the branded header). Define a built-in fallback `info.yml` so the auto-blocks render even with no `info.yml` on disk.

Net effect: the tbm fixture's `info.yml` shrinks from ~120 lines to ~15 lines, services document their own dashboard contribution next to their other config, and adding a new service no longer requires editing `info.yml`.

## Context (from discovery)

Affected packages and files:

- `internal/config/devbox.go` — `ServiceConfig` struct (~lines 648-669). Already has `Hosts map[string]string` and `Ports`. Add `Icon string` and `Info ServiceInfoBlock`. Loaders: `LoadServices(baseDir)` (~line 1279) and `LoadServiceFolder(baseDir, name)` (~line 1192).
- `internal/config/info.go` — `InfoConfig`, `InfoSection`, `InfoItem` (lines 10-127). Currently valid `type:` values are `info`, `warning`, `definition`, `separator`, `subgroup` (lines 177-183). `LoadInfoConfig(path)` line 149; `ValidateInfoConfig(cfg)` line 164.
- `internal/ui/info.go` — `RenderInfo(cfg, infoCfg)` line 22, `renderBlock(...)` line 64, `renderInfoItem(...)` line 236. Single writer of dashboard output.
- `internal/ui/summary.go` — `RenderSummary(cfg, deploySummary)` line 15. Used as the fallback path when `info.yml` is missing (called from `internal/command/info.go:67`). Slated for replacement by the built-in fallback `InfoConfig`.
- `internal/validate/config/services_folder.go` — known-files allowlist (lines 13-17: `service.yml`, `deploy.yml`, `reset.yml`); no schema validation lives here. Per-service schema validation lives next to the config loader in `internal/validate/config/`.
- `internal/stack/topology.go` — `FetchComposeTopology(...)` line 25 and `ParseTopologyFromFiles(...)` line 46 both return `map[string][]string` (svc → deps). Deploy order is derived from this graph.
- Fixture project: `/Users/s/Projects/devbox/next/tbm/devbox/` — `info.yml` (legacy hand-authored URLs/Hosts subgroups), `services/<name>/service.yml` for each of `main`, `nginx`, `db`, `redis`, `opensearch`, `rabbitmq`, `catalog`, `sales`, `customer`, `minio`, `varnish`, `adminer`, `mailpit`, `elasticvue`, `redis_insight`, `opensearch_dashboards`. Must be rewritten alongside the CLI change (per CLAUDE.md "Live user projects ... are updated alongside CLI changes").
- Docs: `docs/reference/config/info.md` (item types reference), `docs/reference/config/services.md` (service schema).

Architectural decisions locked in:

- **InfoItem extension form**: follow the daemon-style sub-spec pattern (CLAUDE.md "Single-block → multi-command sugar"). New `SourceAutoUrlsSpec *AutoUrlsSpec` and `SourceAutoHostsSpec *AutoHostsSpec` fields on `InfoItem` (yaml:"-" in the field tag; populated by a per-item custom decoder that dispatches on `type:`). Type-specific fields never bleed into the flat `InfoItem` surface.
- **When auto-blocks expand**: at *render* time inside `renderInfoItem`, not load time. Expansion needs `cfg.Services` + topology, which is the same constraint daemon-derived commands have (config-blind registry load; runtime resolution). `LoadInfoConfig` stays config-blind.
- **Fallback**: when `devbox/info.yml` is absent, `LoadInfoConfig` returns a built-in `InfoConfig` (urls + hosts auto-blocks). `RenderSummary` becomes dead code and is deleted; `internal/command/info.go:67` no longer special-cases missing-file.
- **Pre-release policy** (CLAUDE.md): no `schema_version` bump, no dual-loader path, no aliasing the old `subgroup`-style URL sections. Rename freely; rewrite the fixture in the same PR.

## Development Approach

- **Testing approach**: Regular (code first, then tests in the same task)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional — they are a required part of the checklist
  - cover success and error scenarios
  - table-driven where applicable
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run `make test` after each task
- This CLI is pre-release: no backwards-compat constraints (see CLAUDE.md "Project Status & Compatibility Policy")

## Testing Strategy

- **Unit tests**: required for every task. Table-driven for config loading, validation, URL assembly, and rendering.
- **Golden-output tests for renderers**: `renderAutoUrls` and `renderAutoHosts` are pure string returns (per CLAUDE.md "Section renderer signature contract") — easy to assert against expected output blocks.
- **Fixture round-trip**: a test that loads the rewritten `tbm/devbox/info.yml` + all service.yml files through `LoadInfoConfig` / `LoadServices` and renders the dashboard, asserting headline lines match the spec's "Target rendered output" sample.
- **No e2e**: dashboard is a CLI surface; render output is asserted directly. Manual smoke happens in Post-Completion.

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope
- Keep plan in sync with actual work done

## Target Rendered Output (spec §2)

```
{▪} Devbox · devbox-pilot-next · 02f6f20-dirty

[ASCII banner]

── URLs ────────────────────────────────────────────────────────────────────────
Main
  📦 Main                — http://pilot.local
     📖 API specification — http://pilot.local/api/docs
     🔗 Clockwork         — http://pilot.local/__clockwork
     ⚡ SPX profiler      — http://pilot.local/?SPX_KEY=dev

Catalog
  📦 Catalog             — http://pilot.catalog.local
     📖 API specification — http://pilot.catalog.local/api/docs

Customer
  📦 Customer            — http://pilot.customer.local

Adminer
  ⚙️ Adminer             — http://pilot.db.local | http://localhost:8027

Mailpit
  ⚙️ Mailpit             — http://pilot.mail.local | http://localhost:8025

Elasticvue
  ⚙️ Elasticvue          — http://pilot.es.local | http://localhost:8026

Redis Insight
  ⚙️ Redis Insight       — http://pilot.redis.local | http://localhost:5540

MinIO Console
  ⚙️ MinIO Console       — http://pilot.minio.local | http://localhost:9011

── Hosts ───────────────────────────────────────────────────────────────────────
Please, add these to your /etc/hosts:
  127.0.0.1    pilot.local
  127.0.0.1    pilot.catalog.local
  ...
```

Service order: all `app` (deploy order) → blank line → all `tool` (deploy order) → blank line → any `infra` included. No "Apps"/"Tools" meta-headers; grouping is positional.

## Schemas

### `service.yml` (new fields)

```yaml
type: app
container: app-main
icon: "📦"                       # top-level. Default by type: app→📦, tool→⚙️, infra→🧱
hosts:
  web: pilot.local
info:                             # display-only block
  title: "Main"                   # override; default = title-case(folder-name)
  paths:                          # ordered list (preserves YAML order)
    - name: "API specification"
      path: /api/docs
      icon: "📖"                  # default "🔗" if omitted
    - name: Clockwork
      path: /__clockwork
    - name: SPX profiler
      path: /?SPX_KEY=dev
      icon: "⚡"
```

### `info.yml` — `auto-urls`

```yaml
- type: auto-urls
  include: [app, tool]            # default: [app, tool]; allowed: app|tool|infra
  hide: [varnish]                 # service folder keys to exclude entirely
  hide_paths:                     # exclude individual paths by name
    main: ["SPX profiler"]
  port_via: nginx                 # front proxy for apps; auto-detected if omitted
  when: '...'                     # standard InfoItem field — hide whole block
```

### `info.yml` — `auto-hosts`

```yaml
- type: auto-hosts
  include: [app, tool, infra]     # default: [app, tool, infra]
  ip: 127.0.0.1                   # default: 127.0.0.1
  hide: [varnish]
  when: '...'
```

## URL Assembly Rules (auto-urls)

| Service has | Main row output |
|---|---|
| `hosts.web` AND `ports.http` (direct binding, typically tool) | `<proxied URL> \| <direct URL>` |
| only `hosts.web` (app behind proxy) | `<proxied URL>` |
| only `ports.http` (no host) | `http://localhost:<port>` |
| neither | row silently omitted |

- `<proxied URL>` = `http(s)://<hosts.web>[:<port_via.ports.http>]` (port omitted if `:80`/`:443`).
- `<direct URL>` = `http(s)://localhost:<ports.http>`.
- Scheme (`http`/`https`) sourced from existing `runtime.use_https` (same path used elsewhere).
- `port_via` auto-detect: pick the single `type: infra` service with non-empty `ports.http`/`ports.https`. If 0 or >1 candidates, fall back to the service's own `ports.*`. If none, the main row is omitted but `paths:` may still render (chained to the direct URL when only direct is available).
- Path rows chain to the proxied URL if present; otherwise to the direct URL.

## Status Validation Rules

`internal/validate/config/services_folder` (extended) and the new `info` auto-block validator emit:

| Field | Severity | Rule |
|---|---|---|
| `service.icon` | warning | length > ~4 runes (likely typo) |
| `service.info.title` | error | contains control chars |
| `service.info.paths[].name` | error | empty; duplicate within service |
| `service.info.paths[].path` | error | empty |
| `service.info.paths[].path` | warning | does not start with `/` |
| `service.info.paths[].icon` | warning | length > ~4 runes |
| `auto-urls.include` / `auto-hosts.include` | error | value not in `app\|tool\|infra` |
| `auto-urls.port_via` | error | explicitly named service does not exist |
| `auto-urls.hide` / `auto-hosts.hide` | warning | unknown service key |
| `auto-urls.hide_paths.<svc>` | warning | unknown service key or unknown path name |
| `auto-hosts.ip` | warning | not parseable as IPv4/IPv6 |

## Go Structures

```go
// internal/config/devbox.go
type ServiceConfig struct {
    // ...existing fields...
    Icon string            `yaml:"icon,omitempty"`
    Info ServiceInfoBlock  `yaml:"info,omitempty"`
}

type ServiceInfoBlock struct {
    Title string            `yaml:"title,omitempty"`
    Paths []ServiceInfoPath `yaml:"paths,omitempty"`
}

type ServiceInfoPath struct {
    Name string `yaml:"name"`
    Path string `yaml:"path"`
    Icon string `yaml:"icon,omitempty"`
}

// Display accessors (nil-safe, applies defaults):
func (s ServiceConfig) DisplayIcon() string
func (s ServiceConfig) DisplayTitle(folderKey string) string
func (p ServiceInfoPath) DisplayIcon() string
```

```go
// internal/config/info.go
type AutoUrlsSpec struct {
    Include   []string            `yaml:"include,omitempty"`
    Hide      []string            `yaml:"hide,omitempty"`
    HidePaths map[string][]string `yaml:"hide_paths,omitempty"`
    PortVia   string              `yaml:"port_via,omitempty"`
}

type AutoHostsSpec struct {
    Include []string `yaml:"include,omitempty"`
    IP      string   `yaml:"ip,omitempty"`
    Hide    []string `yaml:"hide,omitempty"`
}

type InfoItem struct {
    Type string `yaml:"type"`
    // ...existing fields...
    SourceAutoUrlsSpec  *AutoUrlsSpec  `yaml:"-"`
    SourceAutoHostsSpec *AutoHostsSpec `yaml:"-"`
}

// Custom UnmarshalYAML on InfoItem dispatches on `type:` and populates
// the matching SourceXxxSpec pointer.
```

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): all code, tests, fixture rewrite, docs — achievable in this repo
- **Post-Completion** (no checkboxes): manual visual smoke (`devbox info` in the tbm fixture and any local user project)

## Implementation Steps

### Task 1: Add `service.icon` field with type-default accessor

**Files:**
- Modify: `internal/config/devbox.go`
- Modify: `internal/config/devbox_test.go` (or nearest existing test file for `ServiceConfig`)

- [ ] add `Icon string \`yaml:"icon,omitempty"\`` to `ServiceConfig`
- [ ] add `func (s ServiceConfig) DisplayIcon() string` — returns `s.Icon` if non-empty; otherwise type default: `app`→`📦`, `tool`→`⚙️`, `infra`→`🧱`, unknown→empty string
- [ ] define the type-default map as a package-level `var` so tests can read it
- [ ] write table-driven tests for `DisplayIcon` covering all three type defaults, explicit override, unknown type, and zero-value ServiceConfig
- [ ] run `go test ./internal/config/...` — must pass before Task 2

### Task 2: Add `service.info` block (title, paths) with accessors

**Files:**
- Modify: `internal/config/devbox.go`
- Modify: `internal/config/devbox_test.go`

- [ ] add `ServiceInfoBlock`, `ServiceInfoPath` structs as specified above
- [ ] add `Info ServiceInfoBlock \`yaml:"info,omitempty"\`` field to `ServiceConfig`
- [ ] implement `func (s ServiceConfig) DisplayTitle(folderKey string) string` — returns `s.Info.Title` if non-empty; otherwise title-case the folder key, replacing `_`/`-` with spaces (`redis_insight` → `Redis Insight`)
- [ ] implement `func (p ServiceInfoPath) DisplayIcon() string` — returns `p.Icon` if non-empty; otherwise `🔗`
- [ ] confirm `LoadServiceFolder` / `LoadServices` round-trip the new fields without changes (lenient yaml.Unmarshal already permits unknown→present, but per CLAUDE.md `LoadServices` is lenient — verify and document)
- [ ] write tests for `DisplayTitle` (override / title-case / hyphen / underscore / preserve internal capitalisation) and `DisplayIcon` (override / default)
- [ ] write a loader round-trip test: write a service.yml with `icon:`, `info.title:`, `info.paths:` to `t.TempDir()`, load via `LoadServiceFolder`, assert fields parsed correctly and `paths` order preserved
- [ ] run `go test ./internal/config/...` — must pass before Task 3

### Task 3: Extend services-folder + service-schema validation

**Files:**
- Modify: `internal/validate/config/services_folder.go` (only if it inspects file contents — confirm during impl; if it only checks file presence, schema rules go in `internal/validate/config/devbox.go` or a new sibling file)
- Modify or create: `internal/validate/config/services_schema.go` (or extend the existing per-service-schema validator if one already exists — verify during implementation)
- Modify: nearest existing `*_test.go` for the validator

- [ ] confirm where per-service-schema validation lives today (services_folder.go vs a separate devbox/services validator). Add new rules in whichever file already validates ServiceConfig fields. If no such file exists, create `services_schema.go` and register it via the package's `All()` function (no `init()` side effects per CLAUDE.md).
- [ ] implement rule: `service.icon` rune-length > 4 → Warning, hint "icon should be 1–4 runes (emoji or short symbol)"
- [ ] implement rule: `service.info.title` contains control chars → Error
- [ ] implement rule: `service.info.paths[].name` empty → Error
- [ ] implement rule: duplicate `name` within a single service's `paths:` → Error (hint listing the duplicate)
- [ ] implement rule: `service.info.paths[].path` empty → Error
- [ ] implement rule: `service.info.paths[].path` does not start with `/` → Warning
- [ ] implement rule: `service.info.paths[].icon` rune-length > 4 → Warning
- [ ] write table-driven tests for each rule (Diagnostic.Severity, Diagnostic.Scope, Diagnostic.Hint formatting per [[feedback_validate_diagnostic_hints]])
- [ ] run `go test ./internal/validate/...` — must pass before Task 4

### Task 4: Define `AutoUrlsSpec` / `AutoHostsSpec` + custom InfoItem unmarshaller

**Files:**
- Modify: `internal/config/info.go`
- Modify: `internal/config/info_test.go`

- [ ] add `AutoUrlsSpec` and `AutoHostsSpec` structs (fields per "Go Structures" section above)
- [ ] add `SourceAutoUrlsSpec *AutoUrlsSpec` and `SourceAutoHostsSpec *AutoHostsSpec` fields to `InfoItem` (yaml:"-" so they do not double-decode)
- [ ] register `auto-urls` and `auto-hosts` as valid values in the type allowlist (lines ~177-183)
- [ ] implement `func (i *InfoItem) UnmarshalYAML(value *yaml.Node) error` that first decodes into the existing flat fields, then dispatches on `i.Type`:
  - `auto-urls` → decode the same node into a temporary `AutoUrlsSpec` and store as `i.SourceAutoUrlsSpec`
  - `auto-hosts` → same with `AutoHostsSpec`
  - other types → leave Source* pointers nil
- [ ] preserve existing behaviour for all current type values (`info`, `warning`, `definition`, `separator`, `subgroup`) — round-trip them through the new unmarshaller unchanged
- [ ] extend `ValidateInfoConfig` to require the appropriate `Source*Spec` pointer for the new types (defensive — should always be set by unmarshaller); reject `include[]` values not in {`app`,`tool`,`infra`}; reject `auto-hosts.ip` that does not parse via `net.ParseIP` (Warning per spec); validate `port_via` syntactically (non-empty service key — existence-check happens at render time when service list is available)
- [ ] table-driven tests: unmarshal each existing item type unchanged; unmarshal `auto-urls` populates `SourceAutoUrlsSpec`; unmarshal `auto-hosts` populates `SourceAutoHostsSpec`; unknown type fails as before; validation rejects bad `include[]`, bad `ip`, malformed nested shape
- [ ] run `go test ./internal/config/...` — must pass before Task 5

### Task 5: Add deploy-order helper for info rendering

**Files:**
- Modify or create: `internal/stack/info_order.go` (or extend an existing topology helper if one already returns `[]string` deploy order)
- Modify: corresponding test file

- [ ] verify whether a `func DeployOrder(...) []string` helper already exists in `internal/stack`. If yes, reuse it. If no, add a new helper that takes the topology map (already produced by `FetchComposeTopology` or `ParseTopologyFromFiles`) and a set of allowed types, and returns service folder keys in topological order filtered by type
- [ ] the helper signature should be `func DeployOrderForTypes(cfg *config.DevboxConfig, topology map[string][]string, allowedTypes []string) []string` (or equivalent — match style of existing stack helpers)
- [ ] honour the spec ordering rule: within the result, group by type in the order `app`, `tool`, `infra`, with each group internally in topological order. Caller of `auto-urls` / `auto-hosts` will insert blank-line separators between type groups.
- [ ] disabled services (`enabled: false`) MUST be filtered out; users explicitly excluded via `hide:` are filtered by the renderer, not here
- [ ] write tests with a synthetic topology + ServiceConfig map covering: disabled-skipped, type-grouping, intra-group topological order, missing-from-topology services appended at end (stable alphabetic) so a service without compose deps still renders
- [ ] run `go test ./internal/stack/...` — must pass before Task 6

### Task 6: Implement `auto-urls` renderer

**Files:**
- Create: `internal/ui/info_auto_urls.go`
- Create: `internal/ui/info_auto_urls_test.go`

- [ ] implement `func renderAutoUrls(cfg *config.DevboxConfig, spec *config.AutoUrlsSpec, topology map[string][]string) string` — returns the rendered block as a single string per CLAUDE.md "Section renderer signature contract" (no `io.Writer` parameter)
- [ ] defaults: `spec.Include` empty → `[app, tool]`
- [ ] `port_via` resolution: if explicit, look up that service in `cfg.Services` and use its `ports.http`/`ports.https`; if empty, auto-detect — exactly one `type: infra` service with non-empty `ports.http` OR `ports.https` is the front proxy; 0 or >1 candidates → no proxied URL (fall back to direct)
- [ ] for each included service (deploy order from Task 5 helper, filtered by `spec.Hide`):
  - assemble main URL row per "URL Assembly Rules" table
  - emit subgroup header = `service.DisplayTitle(folderKey)` on its own line
  - emit main row: `  <DisplayIcon> <DisplayTitle>  — <url>` (column-aligned within this service's subgroup)
  - for each `service.Info.Paths` entry NOT in `spec.HidePaths[folderKey]`: emit `     <pathIcon> <path.name>  — <base><path.path>` aligned to the same value column as the main row
  - if 0 paths, still emit the subgroup header + main row
  - if main row is omitted but paths exist with only direct URL available, chain paths to direct URL
- [ ] insert one blank line between subgroups within the same type; insert one blank line between type groups (consistent with spec §2)
- [ ] column alignment is *per-service-subgroup* (each service's own `—` column lines up), not global across the whole block — keep alignment math local
- [ ] honour `spec.HidePaths`: skip paths whose `name` matches; unknown service keys / unknown path names in `HidePaths` are not errors here (validator warns; render is silent)
- [ ] honour `runtime.use_https` for scheme; omit `:80` and `:443` from URLs
- [ ] write golden-output tests covering:
  - app behind proxy with multiple paths (matches Target Rendered Output "Main" subgroup)
  - app with no paths (just the main row + subgroup header)
  - tool with both hosts.web and ports.http (`proxied | direct` form, matches "Adminer")
  - tool with only ports.http (`http://localhost:PORT` only)
  - service with neither → silently omitted
  - `hide:` excludes a service
  - `hide_paths:` excludes individual paths
  - explicit `port_via:` resolves correctly
  - auto-detect `port_via:` resolves when exactly one infra-with-ports exists
  - auto-detect `port_via:` declines when 0 or >1 candidates (falls back to direct or omits main row)
  - disabled service is skipped (delegates to Task 5's helper)
- [ ] run `go test ./internal/ui/...` — must pass before Task 7

### Task 7: Implement `auto-hosts` renderer

**Files:**
- Create: `internal/ui/info_auto_hosts.go`
- Create: `internal/ui/info_auto_hosts_test.go`

- [ ] implement `func renderAutoHosts(cfg *config.DevboxConfig, spec *config.AutoHostsSpec, topology map[string][]string) string`
- [ ] defaults: `spec.Include` empty → `[app, tool, infra]`; `spec.IP` empty → `127.0.0.1`
- [ ] iterate services in deploy order (Task 5 helper); for each, walk all `hosts.<key>` values
- [ ] filter: drop empty strings, drop literal `localhost`, drop duplicates (preserve first-seen order)
- [ ] honour `spec.Hide` — skip whole services
- [ ] emit `  <ip>\t<hostname>` per line (two-space indent, tab separator — matches existing spec text)
- [ ] sort: NOT alphabetical — preserve deploy order so URLs and Hosts blocks visually correspond (spec §4.2)
- [ ] golden-output tests covering: dedup across services, `localhost` filtered, `hide:` works, deploy-order preserved, custom IP applied, empty result returns empty string
- [ ] run `go test ./internal/ui/...` — must pass before Task 8

### Task 8: Wire auto-blocks into `renderInfoItem` dispatch

**Files:**
- Modify: `internal/ui/info.go`
- Modify: `internal/ui/info_test.go`

- [ ] in `renderInfoItem`, add cases for `"auto-urls"` and `"auto-hosts"`. Each case:
  - check that the matching `Source*Spec` pointer is non-nil (defensive — should be set by unmarshaller)
  - thread the topology through. Source of topology: `RenderInfo` should accept a `map[string][]string` (call site in `internal/command/info.go` fetches it once via existing `stack` helpers and passes it down; do NOT call `FetchComposeTopology` from `renderInfoItem`)
  - call `renderAutoUrls` / `renderAutoHosts` and append the returned string
- [ ] update `RenderInfo` signature to accept topology. Update the single call site in `internal/command/info.go` (line ~67 region) to pre-fetch topology before calling RenderInfo. Topology fetch failure is non-fatal: pass an empty map and let renderers fall back to alphabetical service order (still better than nothing) — emit a warning to stderr via the existing `render.Writer`
- [ ] update `renderBlock` and `renderInfoItem` `when:` evaluation: auto-block `when:` clauses run against the same template context as existing items (no new variables needed)
- [ ] tests:
  - integration test through `RenderInfo` with a small synthetic `InfoConfig` containing one `auto-urls` and one `auto-hosts` item, asserting the output contains expected lines
  - `when:` on an auto-block hides it
  - topology-fetch-failure path renders gracefully (empty topology → services in `cfg.Services` map order, possibly alphabetic — assert no panic + output contains expected hostnames)
- [ ] run `go test ./internal/ui/... ./internal/command/...` — must pass before Task 9

### Task 9: Replace legacy `Devbox/Project` section and `RenderSummary` fallback with built-in default `InfoConfig`

**Files:**
- Modify: `internal/config/info.go` (add `BuiltinDefaultInfoConfig()` helper)
- Modify: `internal/command/info.go` (call site at ~line 67)
- Delete or shrink: `internal/ui/summary.go` (if it has callers other than the missing-info.yml fallback, keep only those; otherwise delete entirely)
- Modify: corresponding test files

- [ ] add `func BuiltinDefaultInfoConfig() *InfoConfig` returning an InfoConfig equivalent to spec §5:
  - section `urls` with title "URLs" containing one `auto-urls` item (defaults)
  - section `hosts` with title "Hosts" containing one `warning` item ("Please, add these to your /etc/hosts:") followed by one `auto-hosts` item (defaults)
- [ ] in `LoadInfoConfig`, when the file is missing (`errors.Is(err, os.ErrNotExist)`) return `BuiltinDefaultInfoConfig(), nil` instead of bubbling the error
- [ ] in `internal/command/info.go` remove the `RenderSummary` fallback branch — `LoadInfoConfig` now always returns a config
- [ ] delete `internal/ui/summary.go` and its tests IF no other caller exists. If `RenderSummary` is referenced elsewhere (deploy summary, status, etc. — verify with `grep -r RenderSummary`), keep only those reachable functions and remove the missing-info.yml branch
- [ ] verify that the "Devbox / Project — <name>" subgroup is no longer rendered: search for any hardcoded "Project — " or "Devbox" subgroup emission in `internal/ui/` and remove. (The fixture's own `info.yml` may still declare it; that gets fixed in Task 11.)
- [ ] tests:
  - `LoadInfoConfig` on a directory without `info.yml` returns the built-in default (deep-equal check)
  - `RenderInfo` with the built-in default + a minimal cfg renders the URLs and Hosts blocks
  - confirm no "Project — " line appears in any rendered output for the synthetic cfg
- [ ] run `go test ./internal/...` — must pass before Task 10

### Task 10: Update `tbm` fixture services with `icon` and `info` blocks

**Files:**
- Modify: `/Users/s/Projects/devbox/next/tbm/devbox/services/*/service.yml` (every service folder)

- [ ] for each service folder under `/Users/s/Projects/devbox/next/tbm/devbox/services/`, add an appropriate `icon:` (or omit and rely on type default) and `info:` block where the spec calls for a non-default `title:` or non-empty `paths:`. Specifically:
  - `main` (app): `icon: "📦"` (or omit — default suffices), `info.title: "Main"`, `info.paths: [API specification (📖), Clockwork, SPX profiler (⚡)]`
  - `catalog` (app): `info.title: "Catalog"`, `info.paths: [API specification (📖)]`
  - `customer` (app): `info.title: "Customer"`
  - `sales` (app): same pattern as catalog/customer per existing usage — verify which paths exist
  - `nginx`, `varnish` (infra): no `info` block needed (they are the proxies, not surfaced)
  - `db`, `redis`, `opensearch`, `rabbitmq`, `minio`, `mailpit` (infra or tool depending on current type): no `info` block unless they appear in spec §2 output
  - `adminer`, `mailpit`, `elasticvue`, `redis_insight`, `minio` (tools): `info.title` matching spec output ("Adminer", "Mailpit", "Elasticvue", "Redis Insight", "MinIO Console"), no paths
  - `opensearch_dashboards` (tool, if applicable): `info.title: "OpenSearch Dashboards"` if surfaced
- [ ] verify the resulting set of services renders to exactly the spec §2 "Target Rendered Output" (run `devbox info` from inside the fixture after building the CLI)
- [ ] do NOT add `info:` blocks to services that should not appear in the dashboard — let `hide:` in `info.yml` handle exceptions
- [ ] no new tests in this task — the round-trip test from Task 11 covers fixture validity
- [ ] manually inspect each modified file once before moving on (small diff; no automation needed)

### Task 11: Rewrite `tbm/devbox/info.yml` and add fixture round-trip test

**Files:**
- Modify: `/Users/s/Projects/devbox/next/tbm/devbox/info.yml` (shrink from ~120 lines to ~15)
- Create: `internal/ui/info_fixture_test.go` (or extend existing `info_test.go`)

- [ ] rewrite `tbm/devbox/info.yml` to the auto-blocks form. Approximate target (adjust to match actual sections/credentials block already present):
  ```yaml
  sections:
    - id: urls
      title: URLs
      items:
        - type: auto-urls
          hide: [varnish]   # only if varnish is currently surfaced and should be hidden

    - id: hosts
      title: Hosts
      items:
        - type: warning
          text: "Please, add these to your /etc/hosts:"
        - type: auto-hosts

    - id: credentials
      title: Credentials
      items:
        # existing manual credentials items preserved verbatim
  ```
- [ ] remove the legacy `id: devbox_info` subgroup that emits "Project — <name>" (handled in fixture; the renderer code path was already removed in Task 9)
- [ ] add `internal/ui/info_fixture_test.go` (or extend an existing test) that:
  - loads `/Users/s/Projects/devbox/next/tbm/devbox/info.yml` + `tbm/devbox/services/*/service.yml` via the real loaders
  - asserts loaded `InfoConfig` validates cleanly (no error diagnostics)
  - calls `RenderInfo` with a synthetic topology covering the fixture's services
  - asserts headline lines from spec §2 appear in the output (e.g. `📦 Main                — http://`, `Please, add these to your /etc/hosts:`)
- [ ] test uses fixture path under `../../../tbm/devbox/` relative to the test file — confirm the path is reachable from `go test`; if the test would break by adding a sibling-monorepo dependency, isolate the fixture inside `internal/ui/testdata/info_fixture/` as a smaller copy
- [ ] run `go test ./internal/ui/...` — must pass before Task 12

### Task 12: Update reference docs

**Files:**
- Modify: `docs/reference/config/info.md`
- Modify: `docs/reference/config/services.md`
- Modify (if it exists): `docs/reference/config/index.md`

- [ ] in `info.md`:
  - new section documenting `type: auto-urls` (fields: `include`, `hide`, `hide_paths`, `port_via`, `when`; defaults; URL assembly table; example)
  - new section documenting `type: auto-hosts` (fields: `include`, `ip`, `hide`, `when`; defaults; example)
  - new section "Fallback when info.yml is absent" describing the built-in default
  - remove/replace any prose describing the now-deleted "Devbox / Project — <name>" section
- [ ] in `services.md`:
  - new field doc for top-level `icon:` (default-by-type table)
  - new field doc for `info:` block (`title`, `paths` — including the path `name`/`path`/`icon` fields)
  - example with both fields populated, matching the schema sample in this plan
- [ ] auto-generated CLI reference (`docs/reference/cli/`) does not need manual editing — `devbox docs generate` covers it (per CLAUDE.md). No need to run it as part of this task; release process handles regeneration.
- [ ] no code tests for docs; run `make lint` to catch any doc-adjacent issues
- [ ] run `make test` and `make lint` — final full pass before Task 13

### Task 13: Verify acceptance criteria

- [ ] verify all spec sections (§2–§9) are implemented (cross-reference each row of the validation table and the URL assembly table against the test files)
- [ ] verify edge cases via tests: missing `info.yml` (fallback), missing `port_via`, disabled service, malformed `paths[].path`, malformed `ip`, `NO_COLOR`-style or other rendering env vars (if applicable — most output is plain text)
- [ ] run `make test` — final full pass
- [ ] run `make lint` — all issues must be fixed (per CLAUDE.md: `errcheck`, `govet`, `staticcheck`, `revive`, `gocritic`, `modernize`)
- [ ] run `make build` — confirm the binary builds cleanly
- [ ] run `./bin/devbox info` from inside `/Users/s/Projects/devbox/next/tbm/` and visually diff the output against spec §2 "Target Rendered Output" (allowing for hostname differences — the spec uses `pilot.*` placeholders, the fixture uses `tbm.*`)

### Task 14: Final documentation and plan move

**Files:**
- Modify: `AGENTS.md` (remember: `CLAUDE.md` is a symlink to `AGENTS.md`)
- Move: this plan file to `docs/plans/completed/`

- [ ] add a Key Patterns entry to `AGENTS.md`:
  > **`info.yml` auto-blocks**: `type: auto-urls` and `type: auto-hosts` items expand at *render* time inside `renderInfoItem` (`internal/ui/info.go`), not at load time. The `InfoItem` carries a `Source*Spec` pointer per the daemon-style sugar pattern (yaml:"-", populated by `InfoItem.UnmarshalYAML` dispatch on `type:`). `LoadInfoConfig` stays config-blind; `RenderInfo` takes topology as a parameter (fetched once by the caller). When `devbox/info.yml` is missing, `LoadInfoConfig` returns `BuiltinDefaultInfoConfig()` — a synthesized config with a urls section + hosts section. Service `icon` and `info.title` / `info.paths` are display-only fields read only by these renderers (and by status/cmdbrowser for `icon`).
- [ ] `mkdir -p docs/plans/completed && git mv docs/plans/2026-05-26-info-auto-blocks.md docs/plans/completed/` (ralphex does this automatically on completion — manual fallback if needed)

## Post-Completion

*Items requiring manual intervention — no checkboxes, informational only*

**Manual visual verification** (separate from automated golden tests):

- From `/Users/s/Projects/devbox/next/tbm/`, run `./../cli/bin/devbox info` and visually compare against the spec §2 layout. Look specifically for:
  - exact ordering: all apps in deploy order, blank line, all tools in deploy order
  - per-service subgroup header on its own line
  - main row uses the service icon; path rows use the path icon (defaulting to `🔗`)
  - column alignment is consistent *within* each service subgroup
  - URLs without `:80`/`:443` suffix
  - "Devbox / Project — …" section is GONE
  - `Credentials` section (still hand-authored in `info.yml`) renders unchanged
- Test the fallback: temporarily rename `tbm/devbox/info.yml` to `info.yml.bak` and run `devbox info` again — should render the built-in default (urls + hosts only, no credentials)
- Test with a synthetic project that has no front-proxy (no `type: infra` with ports): main URL rows should gracefully omit or fall back to direct URLs without crashing

**External system updates**:

- None. Devbox is pre-release with no external consumers (per CLAUDE.md compatibility policy).

**Follow-up ideas (out of scope here)**:

- `info.paths[].when:` for conditional path visibility (currently all paths render unconditionally)
- Credentials as an auto-block sourced from service `credentials:` fields
- Light/dark theme variants for `icon` (most icons are emoji, so likely a no-op)
- Use `service.icon` in `internal/ui/cmdbrowser` and the status table (currently only consumed by info)
