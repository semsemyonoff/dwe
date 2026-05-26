# Info Auto-Blocks, Service Icon, and `info.paths`

## Overview

Replace hand-authored `URLs` and `Hosts` subgroups in `devbox/info.yml` with two declarative item types that build themselves from service data: `type: auto-urls` and `type: auto-hosts`. Give each service two new fields — a top-level `icon:` (visual hint, **used by info-dashboard rendering only in this plan**; status table and `internal/ui/cmdbrowser` adoption is a follow-up, see Post-Completion) and an `info:` block with `title:` override and an ordered `paths:` list (sub-URLs under the service's main URL). Define a built-in default `InfoConfig` so the auto-blocks render even with no `info.yml` on disk.

Net effect: services document their own dashboard contribution next to their other config, and adding a new service no longer requires editing `info.yml`. The legacy "Devbox / Project — <name>" subgroup (which is per-project YAML in user `info.yml` files, not code) becomes unnecessary because project identity already lives in the branded header.

**Out of scope**: updating any real project's `devbox/` files (service.yml or info.yml). Real-project migration happens separately, project-by-project, after this PR lands.

## Context (from discovery)

Affected packages and files:

- `internal/config/devbox.go` — `ServiceConfig` struct (~lines 648-669). Already has `Hosts map[string]string` and `Ports`. Add `Icon string` and `Info ServiceInfoBlock`. Loaders: `LoadServices(baseDir)` (~line 1279) and `LoadServiceFolder(baseDir, name)` (~line 1192).
- `internal/config/info.go` — `InfoConfig`, `InfoSection`, `InfoItem` (lines 10-127). **`InfoItem` already has `Icon string \`yaml:"icon"\``** at line 102 (used by the `definition` item type). The new `ServiceConfig.Icon` is a separate, non-conflicting field on a different type — but both exist; do not rename either. Currently valid `type:` values are `info`, `warning`, `definition`, `separator`, `subgroup` (lines 177-183). `LoadInfoConfig(path)` line 149; `ValidateInfoConfig(cfg)` line 164.
- `internal/ui/info.go` — `RenderInfo(cfg, infoCfg)` line 22, `renderBlock(...)` line 64, `renderInfoItem(...)` line 236. Single writer of dashboard output.
- `internal/ui/summary.go` — `RenderSummary(cfg, deploySummary)` line 15. **Has TWO callers**: (a) `internal/command/info.go:67` (missing-`info.yml` fallback — this one is being removed) and (b) `internal/command/root.go:300` (root command's project banner when invoked with no subcommand — this stays). `RenderSummary` itself MUST NOT be deleted; only the `info.go` caller goes away.
- `internal/validate/config/services_folder.go` — known-files allowlist (lines 13-17: `service.yml`, `deploy.yml`, `reset.yml`); no schema validation lives here. Per-service schema validation lives next to the config loader in `internal/validate/config/`.
- `internal/stack/topology.go` — `FetchComposeTopology(...)` line 25 and `ParseTopologyFromFiles(...)` line 46 both return `map[string][]string` (compose-svc → deps). **Not used by this plan**: their key space is compose service names (often equal to `container:`, e.g. folder `catalog` → compose name `app-catalog`), not folder keys. Auto-block ordering uses `config.TopoSortServices` at `internal/config/devbox.go:1975` instead, which sorts in folder-key space.
- Docs: `docs/reference/config/info.md` (item types reference), `docs/reference/config/services.md` (service schema).

Architectural decisions locked in:

- **InfoItem extension form**: follow the daemon-style sub-spec pattern (CLAUDE.md "Single-block → multi-command sugar"). New `SourceAutoURLsSpec *AutoURLsSpec` and `SourceAutoHostsSpec *AutoHostsSpec` fields on `InfoItem` (yaml:"-" in the field tag — yaml.v3 skips them entirely; the custom decoder dispatches on `type:` and populates them). Type-specific fields never bleed into the flat `InfoItem` surface.
- **Acronym casing** (Go naming convention): use `URL`/`URLs` not `Url`/`Urls` for all Go identifiers — `AutoURLsSpec`, `SourceAutoURLsSpec`, `renderAutoURLs`. YAML field values stay kebab-case (`auto-urls`, `auto-hosts`, `hide_paths`) — that is YAML convention, not Go.
- **When auto-blocks expand**: at *render* time inside `renderInfoItem`, not load time. Expansion needs `cfg.Services` + topology, which is the same constraint daemon-derived commands have (config-blind registry load; runtime resolution). `LoadInfoConfig` stays config-blind.
- **Fallback**: when `devbox/info.yml` is absent, `LoadInfoConfig` returns a built-in `InfoConfig` (urls + hosts auto-blocks). `RenderSummary` becomes dead code and is deleted; `internal/command/info.go:67` no longer special-cases missing-file.
- **Field/method coexistence** (`Icon` + `DisplayIcon()`): the exported `Icon string` field is required by yaml.v3 (decoder needs exported targets); the `DisplayIcon()` method returns the resolved value with type-default fallback. Both must coexist — this is NOT a getter collision to clean up; it is the only correct shape when the raw and resolved values both need to be reachable.
- **Map iteration determinism**: renderers MUST iterate services via the deploy-order helper (Task 5), never `range cfg.Services`. Go map iteration order is randomized, so any direct range would produce non-deterministic output and flaky golden tests.
- **Service-set source of truth and ordering**: services come from `cfg.Services`, NOT from a compose topology map. Many tool/app services have no `compose:` block. **Ordering uses `config.TopoSortServices(names, services)` at `internal/config/devbox.go:1975`** — it sorts by `DependsOn` in service-key (folder name) space, which is what we need. The compose topology map (`ParseTopologyFromFiles` at `internal/stack/topology.go:46`) is the WRONG key space — it returns compose service names which often differ from folder keys (e.g., folder `catalog` → compose name `app-catalog`). Do not pass `topology map[string][]string` through the auto-block renderers; the deploy-order helper instead takes `cfg.Services` + the types filter and dispatches to `TopoSortServices` internally.
- **`service.yml` is strict, not lenient** (corrects an earlier assumption): `LoadServiceFolder` at `internal/config/devbox.go:1199` runs a raw-map precheck against `allowedFieldsFor(type)` at `internal/config/devbox.go:607`, then strict-decodes with `yaml.Decoder.KnownFields(true)` at `internal/config/devbox.go:1265`. Adding `icon:` and `info:` requires updating BOTH allowlists in the same PR: (a) `allowedFieldsFor` in `internal/config/devbox.go` AND (b) `servicesAllowedFields` in `internal/validate/config/devbox.go:112`. Without those updates, any service.yml that uses `icon:` or `info:` fails loading with an opaque KnownFields error. This is enforced in Task 1 and Task 2 as explicit allowlist sub-bullets.
- **Validate-framework integration**: the existing info validator is `infoValidator` in `internal/validate/config/devbox.go:464` (NOT a separate `info.go` file). New auto-block diagnostic rules (the ones that need warning severity, like "unknown hide key" or unparseable IP) are added alongside the existing rules in `devbox.go`, registered through the same `infoValidator.Run`. `ValidateInfoConfig` (`internal/config/info.go:164`) keeps returning `error` — it stays the load-time hard-failure gate for malformed structure; diagnostics with finer severity gradation live in the validator.
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
- **Subtest discipline** (per `golang-testing` skill): every table-driven case has a `name` field; subtest names are lowercase descriptive phrases (`"main app with paths"`, not `"MainAppWithPaths"` or `"Main App With Paths"`).
- **Parallel by default**: every subtest calls `t.Parallel()` unless it mutates process-wide state. Use `t.TempDir()` for filesystem fixtures, `t.Setenv` for env-var manipulation (both are parallel-test-safe in Go 1.17+). Renderers and validators are pure functions — they should always be parallel.
- **Golden-output tests for renderers**: `renderAutoURLs` and `renderAutoHosts` are pure string returns (per CLAUDE.md "Section renderer signature contract") — easy to assert against expected output blocks.
- **Synthetic fixtures**: tests build `ServiceConfig` maps in memory or write minimal YAML to `t.TempDir()`. No dependency on any real project tree.
- **Compile-time interface checks**: place `var _ yaml.Unmarshaler = (*InfoItem)(nil)` next to the `UnmarshalYAML` method so the build fails if the method signature ever drifts off the gopkg.in/yaml.v3 interface.
- **No e2e**: dashboard is a CLI surface; render output is asserted directly. Manual smoke happens in Post-Completion.

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope
- Keep plan in sync with actual work done

## Target Rendered Output (illustrative — spec §2)

Hostnames below (`pilot.*`) are illustrative placeholders, not a reference to any real project. Real projects produce the same shape with their own hostnames.


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
  host_key: web                   # which hosts.<key> to surface; default "web"
  port_key: http                  # which ports.<key> to surface; default "http"
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

Example for a service with non-default host/port keys (e.g. an object-storage console with `hosts.console` and `ports.console`):

```yaml
type: infra
container: minio
icon: "⚙️"
hosts:
  s3: s3.local
  console: minio.local
ports:
  api: 9010
  console: 9011
info:
  title: "MinIO Console"
  host_key: console
  port_key: console
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

Auto-urls surfaces ONE host key + ONE port key per service. The keys are configurable per-service via `info.host_key:` (default `"web"`) and `info.port_key:` (default `"http"`). This is needed because some services use non-default key names — e.g. an S3-compatible console exposing both `hosts.s3` and `hosts.console`. For services that don't need URL surfacing, leave `info` empty — they are skipped.

| Service has | Main row output |
|---|---|
| `hosts[host_key]` AND `ports[port_key]` (direct binding, typically tool/infra) | `<proxied URL> \| <direct URL>` |
| only `hosts[host_key]` (app behind proxy, no direct port) | `<proxied URL>` |
| only `ports[port_key]` (no host) | `http://localhost:<port>` |
| neither | row silently omitted |

- `<proxied URL>` = `http(s)://<hosts[host_key]>[:<port>]` (port omitted if `:80`/`:443`).
- `<direct URL>` = `http(s)://localhost:<ports[port_key]>`.
- Scheme (`http`/`https`) sourced from existing `runtime.use_https` (same path used elsewhere). Applies to BOTH the proxied URL (via `port_via`) AND the direct URL — the selected `port_key` is `http` or `https` independent of the configured key name. **Note**: `port_key` is the literal map key into `service.ports` (so a value like `"console"` resolves to `ports.console`); it is not auto-translated to `http`/`https` based on scheme.
- **Port selection on `port_via`** (the proxy): when `runtime.use_https` is true, prefer `port_via.ports.https`; otherwise `port_via.ports.http`. If the selected port is empty, the proxied URL is host-only (no `:port` suffix) — do NOT fall through to the other key.
- **`port_via` auto-detect** (when `auto-urls.port_via:` is empty): pick the single enabled `type: infra` service whose `ports.http == 80` OR `ports.https == 443`. The port filter is what prevents false positives like opensearch (`type: infra`, `ports.http: 9200`) from being mistaken for a front proxy. If 0 or >1 candidates pass the filter, no `port_via` is detected — services with only `hosts[host_key]` render no proxied URL (fall back to direct URL or omit). Document this in `info.md`: users with non-standard proxy port should set `port_via:` explicitly in `info.yml`.
- Path rows chain to the proxied URL if present; otherwise to the direct URL.

## Status Validation Rules

`internal/validate/config/services_folder` (extended) and the new `info` auto-block validator emit:

| Field | Severity | Rule |
|---|---|---|
| `service.info.title` | error | contains control chars |
| `service.info.paths[].name` | error | empty; duplicate within service |
| `service.info.paths[].path` | error | empty |
| `service.info.paths[].path` | warning | does not start with `/` |
| `auto-urls.include` / `auto-hosts.include` | error | value not in `app\|tool\|infra` |
| `auto-urls.port_via` | error | explicitly named service does not exist |
| `auto-urls.hide` / `auto-hosts.hide` | warning | unknown service key |
| `auto-urls.hide_paths.<svc>` | warning | unknown service key or unknown path name |
| `auto-hosts.ip` | warning | not parseable as IPv4/IPv6 |

`service.icon` and `service.info.paths[].icon` are intentionally not validated for length — ZWJ-joined emoji break any rune-count threshold. Treat icons as opaque user content.

## Go Structures

```go
// internal/config/devbox.go
type ServiceConfig struct {
    // ...existing fields...
    Icon string            `yaml:"icon,omitempty"`
    Info ServiceInfoBlock  `yaml:"info,omitempty"`
}

type ServiceInfoBlock struct {
    Title   string            `yaml:"title,omitempty"`
    HostKey string            `yaml:"host_key,omitempty"`  // default "web"
    PortKey string            `yaml:"port_key,omitempty"`  // default "http"
    Paths   []ServiceInfoPath `yaml:"paths,omitempty"`
}

type ServiceInfoPath struct {
    Name string `yaml:"name"`
    Path string `yaml:"path"`
    Icon string `yaml:"icon,omitempty"`
}

// Display accessors — name signals "resolved with default", as opposed to the
// raw exported field (yaml.v3 needs the field exported to populate it).
// Receiver kind (value vs pointer) MUST match the existing receiver convention
// on ServiceConfig — see Task 1 for the verification step.
func (s ServiceConfig) DisplayIcon() string
func (s ServiceConfig) DisplayTitle(folderKey string) string
func (p ServiceInfoPath) DisplayIcon() string
```

```go
// internal/config/info.go
type AutoURLsSpec struct {
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
    SourceAutoURLsSpec  *AutoURLsSpec  `yaml:"-"`
    SourceAutoHostsSpec *AutoHostsSpec `yaml:"-"`
}

// Compile-time check — keeps the gopkg.in/yaml.v3 contract pinned.
var _ yaml.Unmarshaler = (*InfoItem)(nil)

// UnmarshalYAML decodes the flat fields via a type-alias shadow (to avoid
// infinite recursion into this same method), then dispatches on i.Type to
// decode the type-specific Source*Spec.
func (i *InfoItem) UnmarshalYAML(value *yaml.Node) error {
    type alias InfoItem
    var a alias
    if err := value.Decode(&a); err != nil {
        return err
    }
    *i = InfoItem(a)

    switch i.Type {
    case "auto-urls":
        var spec AutoURLsSpec
        if err := value.Decode(&spec); err != nil {
            return err
        }
        i.SourceAutoURLsSpec = &spec
    case "auto-hosts":
        var spec AutoHostsSpec
        if err := value.Decode(&spec); err != nil {
            return err
        }
        i.SourceAutoHostsSpec = &spec
    }
    return nil
}
```

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): all code, tests, fixture rewrite, docs — achievable in this repo
- **Post-Completion** (no checkboxes): real-project migration (updating `service.yml` and `info.yml` files in existing projects) and manual visual smoke against a real `devbox info`

## Implementation Steps

### Task 1: Add `service.icon` field with type-default accessor

**Files:**
- Modify: `internal/config/devbox.go`
- Modify: `internal/config/devbox_test.go` (or nearest existing test file for `ServiceConfig`)

- [ ] **first**: grep for existing methods on `ServiceConfig` and note their receiver kind (`func (s ServiceConfig)` vs `func (s *ServiceConfig)`). The new accessors MUST match — per `golang-structs-interfaces`, receiver type must be consistent across all methods of a type. If `ServiceConfig` has zero existing methods, prefer pointer receivers (the struct has maps and slices and is non-trivial in size).
- [ ] add `Icon string \`yaml:"icon,omitempty"\`` to `ServiceConfig`
- [ ] **register `icon` in BOTH strict-decode allowlists** (required — service.yml uses `yaml.Decoder.KnownFields(true)` at `internal/config/devbox.go:1265`; unlisted fields fail loading):
  - `allowedFieldsFor` at `internal/config/devbox.go:607` — add `"icon"` to the `common` slice (applies to all three service types)
  - `servicesAllowedFields` at `internal/validate/config/devbox.go:112` — mirror the addition
- [ ] add `DisplayIcon() string` method — returns `s.Icon` if non-empty; otherwise type default via a small `switch s.Type`: `app`→`📦`, `tool`→`⚙️`, `infra`→`🧱`, unknown→empty string. **No package-level icon map** — a switch inside the method is shorter, has no exported surface, and tests can read it through the method directly. Only extract to a `var` if a second consumer (status/cmdbrowser follow-up) actually appears.
- [ ] write table-driven tests (each subtest calls `t.Parallel()`; subtest names are lowercase descriptive phrases like `"app type default"`, `"explicit override"`, `"unknown type returns empty"`) for `DisplayIcon` covering all three type defaults, explicit override, unknown type, and zero-value ServiceConfig
- [ ] add a strict-decode test: a service.yml with `icon: "🔧"` loads cleanly via `LoadServiceFolder` (regression test against the allowlist update being forgotten)
- [ ] run `go test ./internal/config/... ./internal/validate/...` — must pass before Task 2

### Task 2: Add `service.info` block (title, paths) with accessors

**Files:**
- Modify: `internal/config/devbox.go`
- Modify: `internal/config/devbox_test.go`

- [ ] add `ServiceInfoBlock` (with `Title`, `HostKey`, `PortKey`, `Paths` fields), `ServiceInfoPath` structs as specified above
- [ ] add `Info ServiceInfoBlock \`yaml:"info,omitempty"\`` field to `ServiceConfig`
- [ ] **register `info` in BOTH strict-decode allowlists** (mirrors Task 1):
  - `allowedFieldsFor` at `internal/config/devbox.go:607` — add `"info"` to the `common` slice
  - `servicesAllowedFields` at `internal/validate/config/devbox.go:112` — mirror the addition
- [ ] implement `func (s ServiceConfig) DisplayTitle(folderKey string) string` — returns `s.Info.Title` if non-empty; otherwise title-case the folder key, replacing `_`/`-` with spaces (`redis_insight` → `Redis Insight`)
- [ ] implement `func (p ServiceInfoPath) DisplayIcon() string` — returns `p.Icon` if non-empty; otherwise `🔗`
- [ ] implement `func (s ServiceConfig) DisplayHostKey() string` (default `"web"`) and `func (s ServiceConfig) DisplayPortKey() string` (default `"http"`) — these resolve `s.Info.HostKey` and `s.Info.PortKey` for the renderer
- [ ] **the inner `ServiceInfoBlock` is decoded under strict-mode** by virtue of being part of `ServiceConfig`. Confirm during impl that yaml.v3's `KnownFields(true)` propagates into nested structs (it does, by default) — a typo like `info.tilte` will fail loading. This is the desired behaviour; document in `services.md` (Task 12).
- [ ] write tests for `DisplayTitle` (override / title-case / hyphen / underscore / preserve internal capitalisation), `DisplayIcon` (override / default), and `DisplayHostKey`/`DisplayPortKey` (override / default)
- [ ] write a loader round-trip test: write a service.yml with `icon:`, `info.title:`, `info.host_key:`, `info.port_key:`, `info.paths:` to `t.TempDir()`, load via `LoadServiceFolder`, assert fields parsed correctly and `paths` order preserved
- [ ] write a strict-decode failure test: a service.yml with `info.tilte:` (typo) fails loading with a clear KnownFields error
- [ ] run `go test ./internal/config/... ./internal/validate/...` — must pass before Task 3

### Task 3: Extend service-schema validation

**Files:**
- Modify: `internal/validate/config/devbox.go` (the existing per-service-schema validator — `services_folder.go` only checks file presence and is NOT the right home for these rules)
- Modify: the validator's test file

**Pre-decided**: rules go in `internal/validate/config/devbox.go` alongside other `ServiceConfig`-field rules. Do NOT create a new validator file unless `devbox.go` grows unwieldy.

- [ ] implement rule: `service.info.title` contains control chars (anything matching `unicode.IsControl`) → Error
- [ ] implement rule: `service.info.paths[].name` empty → Error
- [ ] implement rule: duplicate `name` within a single service's `paths:` → Error (hint listing the duplicate name)
- [ ] implement rule: `service.info.paths[].path` empty → Error
- [ ] implement rule: `service.info.paths[].path` does not start with `/` → Warning
- [ ] **drop the icon rune-length checks** for both `service.icon` and `service.info.paths[].icon`. ZWJ-joined emoji (skin-tone modifiers, family glyphs, profession emoji like `🧑‍💻`) span multiple Go runes but render as one glyph — any sensible threshold either lets legitimate icons trigger warnings or lets typos through. The signal/noise is too low to be worth implementing. Treat `icon` as opaque user content.
- [ ] write table-driven tests for each remaining rule (each subtest calls `t.Parallel()`; subtest names lowercase descriptive phrases). Assert Diagnostic.Severity, Diagnostic.Scope, and Diagnostic.Hint formatting per [[feedback_validate_diagnostic_hints]]
- [ ] run `go test ./internal/validate/...` — must pass before Task 4

### Task 4: Define `AutoURLsSpec` / `AutoHostsSpec` + custom InfoItem unmarshaller

**Files:**
- Modify: `internal/config/info.go`
- Modify: `internal/config/info_test.go`

- [ ] add `AutoURLsSpec` and `AutoHostsSpec` structs (fields per "Go Structures" section above). Per `golang-naming`, the acronym `URL` is all-caps in Go identifiers — `AutoURLsSpec`, not `AutoUrlsSpec`.
- [ ] add `SourceAutoURLsSpec *AutoURLsSpec` and `SourceAutoHostsSpec *AutoHostsSpec` fields to `InfoItem`. Use `yaml:"-"` so yaml.v3 ignores them in the default decode pass (the custom `UnmarshalYAML` below populates them by dispatching on `type:`).
- [ ] register `auto-urls` and `auto-hosts` as valid values in the type allowlist (lines ~177-183). YAML values stay kebab-case; only the Go identifiers use `URLs`.
- [ ] add the compile-time interface check next to the method: `var _ yaml.Unmarshaler = (*InfoItem)(nil)` — guarantees the build fails if the method signature ever drifts off `gopkg.in/yaml.v3`.
- [ ] implement `func (i *InfoItem) UnmarshalYAML(value *yaml.Node) error`. **Critical**: use the type-alias shadow trick to avoid infinite recursion (calling `value.Decode(i)` directly would re-enter this same `UnmarshalYAML` and overflow the stack). Pattern:
  ```go
  type alias InfoItem
  var a alias
  if err := value.Decode(&a); err != nil { return err }
  *i = InfoItem(a)
  ```
  Then dispatch on `i.Type`:
  - `auto-urls` → `var spec AutoURLsSpec; value.Decode(&spec); i.SourceAutoURLsSpec = &spec`
  - `auto-hosts` → same with `AutoHostsSpec`
  - other types → leave both Source* pointers nil
- [ ] preserve existing behaviour for all current type values (`info`, `warning`, `definition`, `separator`, `subgroup`) — round-trip them through the new unmarshaller unchanged
- [ ] extend `ValidateInfoConfig` at `internal/config/info.go:164` (load-time hard checks — returns `error`): require the appropriate `Source*Spec` pointer for the new types (defensive — should always be set by unmarshaller); reject `include[]` values not in {`app`,`tool`,`infra`}; validate `port_via` syntactically (non-empty service key when set — existence-check happens in the validator below since it needs `cfg.Services`)
- [ ] add the diagnostics-level rules to `infoValidator` at `internal/validate/config/devbox.go:464` (the existing info validator — NOT a new file):
  - `auto-hosts.ip` does not parse via `net.ParseIP` → Warning
  - `auto-urls.port_via` references an unknown service key → Error (needs `ctx.Cfg`)
  - `auto-urls.hide` / `auto-hosts.hide` references an unknown service key → Warning (needs `ctx.Cfg`)
  - `auto-urls.hide_paths.<svc>` references an unknown service key or unknown path name → Warning (needs `ctx.Cfg`)
- [ ] table-driven tests (every subtest calls `t.Parallel()`; subtest names lowercase descriptive phrases): unmarshal each existing item type unchanged including the pre-existing `InfoItem.Icon` field (e.g. a `definition` item with `icon: "🔧"` round-trips intact — this guards against the alias-trick decode dropping fields); unmarshal `auto-urls` populates `SourceAutoURLsSpec`; unmarshal `auto-hosts` populates `SourceAutoHostsSpec`; deeply nested `hide_paths` map decodes correctly; unknown type fails as before; validation rejects bad `include[]`, bad `ip`, malformed nested shape
- [ ] run `go test ./internal/config/...` — must pass before Task 5

### Task 5: Add deploy-order helper for info rendering

**Files:**
- Modify or create: `internal/stack/info_order.go` (helper lives in `internal/stack` for symmetry with other render-side stack helpers; the actual topological sort is delegated to `config.TopoSortServices`)
- Modify: corresponding test file

**Approach correction**: do NOT use `ParseTopologyFromFiles` / `FetchComposeTopology` — those return a graph keyed by *compose service names* which often differ from folder keys (e.g., folder `catalog` → compose name `app-catalog` via `container:` field; see `internal/stack/topology.go:183` which maps via `svc.Container`, not folder key). For auto-block rendering we need ordering in folder-key space. **Use `config.TopoSortServices(names, services) ([]string, error)` at `internal/config/devbox.go:1975` instead** — it sorts by `DependsOn` in service-key space, which is exactly the right unit.

- [ ] add `func DeployOrder(cfg *config.DevboxConfig, types []string) []string` in `internal/stack`. Signature does NOT take a topology map — internally calls `config.TopoSortServices` per type group.
- [ ] for each type in `types` (callers pass `["app","tool","infra"]`):
  1. collect enabled service folder keys with that type into a `[]string` (sorted alphabetically for stability before topo-sort)
  2. call `config.TopoSortServices(names, cfg.Services)` — returns dependency order
  3. on `TopoSortServices` error (cycle), fall back to the alphabetic list — log the error to stderr via the caller's writer (not from inside the helper; helper returns silently to honour the renderer-signature contract)
  4. append to the result
- [ ] disabled services (`enabled: false`) MUST be filtered out before sorting; users explicitly excluded via `hide:` are filtered by the renderer, not here
- [ ] **determinism contract**: structural guarantee — alphabetic pre-sort feeds `TopoSortServices` so the result is deterministic even when the dependency graph has multiple valid topological orderings. Document this in the helper's doc comment.
- [ ] no `topology` parameter anywhere downstream: `RenderInfo`, `renderAutoURLs`, `renderAutoHosts`, and `renderInfoItem` all stop receiving topology — they receive `cfg *config.DevboxConfig` and call `stack.DeployOrder(cfg, ...)` directly when they need ordering. This removes the topology-fetch step from `runInfo` entirely.
- [ ] write tests (each subtest calls `t.Parallel()`; subtest names lowercase descriptive phrases) covering: disabled-skipped, type-grouping in `types`-argument order, intra-group dependency order via `DependsOn`, services without `DependsOn` appear in alphabetic-of-input order, dependency cycle falls back to alphabetic without panicking, empty types argument returns empty slice, ServiceConfig map order does not affect result (insert in random order, assert sorted output)
- [ ] run `go test ./internal/stack/...` — must pass before Task 6

### Task 6: Implement `auto-urls` renderer

**Files:**
- Create: `internal/ui/info_auto_urls.go`
- Create: `internal/ui/info_auto_urls_test.go`

- [ ] implement `func renderAutoURLs(cfg *config.DevboxConfig, spec *config.AutoURLsSpec) string` — returns the rendered block as a single string per CLAUDE.md "Section renderer signature contract" (no `io.Writer` parameter, no topology parameter — service order comes from `stack.DeployOrder(cfg, spec.Include)` called inside the function). Per `golang-naming`, the acronym is `URLs`, not `Urls`. Note: this function is unreachable from `runInfo` until Task 8 wires the dispatch in `renderInfoItem`; tests in this task call `renderAutoURLs` directly.
- [ ] defaults: `spec.Include` empty → `[app, tool]`
- [ ] **service-level host/port key selection**: read `svc.Info.HostKey` (default `"web"`) and `svc.Info.PortKey` (default `"http"`) to determine which `hosts[<key>]` and `ports[<key>]` to surface. Services without an `info:` block use the defaults; services with non-default host/port keys (e.g. a console UI under `hosts.console` / `ports.console`) override via `info.host_key:` / `info.port_key:`.
- [ ] `port_via` resolution: if explicit, look up that service in `cfg.Services` and use its `ports.http`/`ports.https` (NOT the service's own port_key — the proxy is by convention HTTP). If empty, auto-detect with the narrowed filter: exactly one enabled `type: infra` service with `ports.http == 80` OR `ports.https == 443`. If 0 or >1 candidates → no proxied URL (fall back to direct).
- [ ] for each included service (deploy order from `stack.DeployOrder(cfg, spec.Include)`, filtered by `spec.Hide`):
  - resolve `host_key` and `port_key` for this service
  - skip the service silently if neither `hosts[host_key]` nor `ports[port_key]` is set AND `Info.Paths` is empty (nothing to render)
  - assemble main URL row per "URL Assembly Rules" table using the resolved keys
  - emit subgroup header = `service.DisplayTitle(folderKey)` on its own line
  - emit main row: `  <DisplayIcon> <DisplayTitle>  — <url>` (column-aligned within this service's subgroup)
  - for each `service.Info.Paths` entry NOT in `spec.HidePaths[folderKey]`: emit `     <pathIcon> <path.name>  — <base><path.path>` aligned to the same value column as the main row
  - if 0 paths, still emit the subgroup header + main row
  - if main row is omitted but paths exist with only direct URL available, chain paths to direct URL
- [ ] insert one blank line between subgroups within the same type; insert one blank line between type groups (consistent with spec §2)
- [ ] column alignment is *per-service-subgroup* (each service's own `—` column lines up), not global across the whole block — keep alignment math local
- [ ] honour `spec.HidePaths`: skip paths whose `name` matches; unknown service keys / unknown path names in `HidePaths` are not errors here (validator warns; render is silent)
- [ ] honour `runtime.use_https` for scheme; omit `:80` and `:443` from URLs
- [ ] **empty-output rule**: if the renderer produces zero subgroups (every included service got skipped), return `""` — the caller's `renderInfoItem` dispatch is responsible for not contributing this empty string to `contentCount` (see Task 8). Do NOT emit a "(no services)" placeholder.
- [ ] **service iteration**: always via `stack.DeployOrder(cfg, spec.Include)`. Do NOT `range cfg.Services` directly — Go map iteration is randomized and will produce flaky golden tests.
- [ ] write golden-output tests (each subtest calls `t.Parallel()`; subtest names lowercase descriptive phrases like `"app behind proxy with multiple paths"`) covering:
  - app behind proxy with multiple paths (matches Target Rendered Output "Main" subgroup)
  - app with no paths (just the main row + subgroup header)
  - tool with both hosts.web and ports.http (`proxied | direct` form, matches "Adminer")
  - tool with only ports.http (`http://localhost:PORT` only)
  - service with neither → silently omitted
  - `hide:` excludes a service
  - `hide_paths:` excludes individual paths
  - explicit `port_via:` resolves correctly
  - auto-detect `port_via:` picks the single infra with `ports.http == 80` (typical reverse-proxy case)
  - auto-detect `port_via:` declines when an extra infra with non-`:80` ports.http exists (e.g. an infra service binding `9200` — must NOT be selected; load-bearing regression case for the port-filter narrowing)
  - auto-detect `port_via:` declines when 0 candidates pass the port filter
  - service with `info.host_key: console` and `info.port_key: console` renders correctly — both `<proxied URL>` and `<direct URL>` use the `console` keys
  - service with neither standard nor configured host/port keys is silently omitted
  - disabled service is skipped (delegates to Task 5's helper)
- [ ] run `go test ./internal/ui/...` — must pass before Task 7

### Task 7: Implement `auto-hosts` renderer

**Files:**
- Create: `internal/ui/info_auto_hosts.go`
- Create: `internal/ui/info_auto_hosts_test.go`

- [ ] implement `func renderAutoHosts(cfg *config.DevboxConfig, spec *config.AutoHostsSpec) string` — no topology parameter (uses `stack.DeployOrder(cfg, spec.Include)`)
- [ ] defaults: `spec.Include` empty → `[app, tool, infra]`; `spec.IP` empty → `127.0.0.1`
- [ ] iterate services via `stack.DeployOrder(cfg, spec.Include)` (NEVER `range cfg.Services` directly — Go map iteration is randomized); for each, walk ALL `hosts.<key>` values regardless of which key — `hosts:` is the user's declared etc/hosts surface, every entry should be emitted. Iteration of the `Hosts` map per-service is also randomized, so collect entries into a slice first, then sort by host-key name within the service to keep output deterministic.
- [ ] filter: drop empty strings, drop literal `localhost`, drop duplicates (preserve first-seen order)
- [ ] honour `spec.Hide` — skip whole services
- [ ] emit `  <ip>\t<hostname>` per line (two-space indent, tab separator — matches existing spec text)
- [ ] sort: NOT alphabetical — preserve deploy order so URLs and Hosts blocks visually correspond (spec §4.2)
- [ ] golden-output tests (each subtest calls `t.Parallel()`; subtest names lowercase descriptive phrases) covering: dedup across services, `localhost` filtered, `hide:` works, deploy-order preserved, custom IP applied, empty result returns empty string
- [ ] run `go test ./internal/ui/...` — must pass before Task 8

### Task 8: Wire auto-blocks into `renderInfoItem` dispatch

**Files:**
- Modify: `internal/ui/info.go`
- Modify: `internal/ui/info_test.go`

- [ ] in `renderInfoItem`, add cases for `"auto-urls"` and `"auto-hosts"`. Each case:
  - check that the matching `Source*Spec` pointer is non-nil (defensive — should be set by unmarshaller; `DefaultInfoConfig()` also sets it explicitly)
  - call `renderAutoURLs(cfg, item.SourceAutoURLsSpec)` or `renderAutoHosts(cfg, item.SourceAutoHostsSpec)` and return the resulting string. The function signatures take `cfg` directly — no topology parameter; ordering is resolved inside the renderer via `stack.DeployOrder`.
- [ ] **`RenderInfo` signature does NOT change** — auto-blocks are wired through `renderInfoItem` which already has `cfg` in scope. No topology fetch is added to `runInfo`. This is simpler than the earlier topology-threading plan and avoids the compose-name-vs-folder-key confusion entirely.
- [ ] **`renderBlock` contentCount fix** at `internal/ui/info.go:123` — the existing flow appends `itemOut` to `survivors` and bumps `contentCount` for any non-decorative item, regardless of whether `itemOut == ""`. For auto-blocks that may legitimately render to `""` (no included services match, or all hidden), this would keep `hide_on_empty` sections visible with just the title/warning showing. **Fix**: in `renderBlock`'s non-subgroup branch (lines ~125-133), after calling `renderInfoItem`, check `if strings.TrimSpace(itemOut) == ""` — when empty, do NOT append to survivors and do NOT bump `contentCount`. This is a small, surgical change to the existing logic; it also benefits the existing item types whenever they happen to render empty (e.g., a `definition` item with empty `Value` and `Decorative: false`). Add a regression test asserting `hide_on_empty: true` sections vanish when their only content item is an auto-block that returned `""`.
- [ ] update `renderBlock` and `renderInfoItem` `when:` evaluation: auto-block `when:` clauses run against the same template context as existing items (no new variables needed)
- [ ] tests:
  - integration test through `RenderInfo` with a small synthetic `InfoConfig` containing one `auto-urls` and one `auto-hosts` item, asserting the output contains expected lines
  - `when:` on an auto-block hides it
  - **`hide_on_empty: true` section with an auto-block that returns `""` collapses (the title + warning items inside are dropped along with the empty auto-block)** — load-bearing regression test for the renderBlock fix
  - empty `cfg.Services` → renderers return `""` gracefully; no panic
- [ ] run `go test ./internal/ui/... ./internal/command/...` — must pass before Task 9

### Task 9: Add built-in default `InfoConfig` and drop the missing-`info.yml` fallback branch

**Files:**
- Modify: `internal/config/info.go` (add `DefaultInfoConfig()` helper)
- Modify: `internal/command/info.go` (delete the `missingInfo` branch at lines ~66-71)
- Modify: corresponding test files

**Scope clarification**: `internal/ui/summary.go` (`RenderSummary`) is NOT deleted — it has a second, unrelated caller at `internal/command/root.go:300` for the root-command project banner. Only the `info.go` caller goes away. The "Devbox / Project — <name>" subgroup that exists in some user `info.yml` files is YAML, not Go code — there is nothing to remove on the CLI side; users drop that subgroup from their own `info.yml` during migration (Post-Completion).

- [ ] add `func DefaultInfoConfig() *InfoConfig` returning an InfoConfig equivalent to spec §5:
  - section `urls` with title "URLs" containing one `auto-urls` `InfoItem` (`Type: "auto-urls"`, `SourceAutoURLsSpec: &AutoURLsSpec{}` — zero-value spec triggers defaults at render time)
  - section `hosts` with title "Hosts" containing one `warning` item ("Please, add these to your /etc/hosts:") followed by one `auto-hosts` `InfoItem` (`Type: "auto-hosts"`, `SourceAutoHostsSpec: &AutoHostsSpec{}`)
  - **CRITICAL**: `Source*Spec` pointers MUST be populated here because `UnmarshalYAML` does not run on Go-constructed configs. A nil `SourceAutoURLsSpec` would hit the renderer's defensive nil guard and produce empty output.
- [ ] in `LoadInfoConfig`, when the file is missing (`errors.Is(err, os.ErrNotExist)`) return `DefaultInfoConfig(), nil` instead of returning the missing-file error
- [ ] in `internal/command/info.go` delete the `if missingInfo { ... return nil }` block at lines ~66-71. `LoadInfoConfig` now always returns a config; the warning is no longer accurate (an absent `info.yml` is a supported state with a sensible default rendering)
- [ ] **update `infoValidator` at `internal/validate/config/devbox.go:464`**: the existing branch at line ~478 emits an `Info`-severity diagnostic `"no info.yml"` when `LoadInfoConfig` returns `errNotExist`. After this task, that branch becomes unreachable (LoadInfoConfig swallows ErrNotExist). Add an explicit `os.Stat(infoPath)` check BEFORE calling `LoadInfoConfig` — if the file does not exist, still emit the existing `"no info.yml"` Info diagnostic (preserves the current user-facing signal in `devbox validate config info` output), then proceed to validate the built-in default for completeness. This keeps the validator's behaviour observably identical for users running `devbox validate`.
- [ ] **do NOT delete** `internal/ui/summary.go` or `internal/ui/summary_test.go` — `RenderSummary` is still used by `internal/command/root.go:300`. Confirm with `grep -rn RenderSummary --include='*.go'` before touching the file (expect 2 production callers reduced to 1).
- [ ] do NOT add any `grep ... in internal/ui/` step for the "Devbox / Project — <name>" string — that text is fixture YAML, handled in Task 11.
- [ ] tests:
  - `LoadInfoConfig` on a directory without `info.yml` returns the built-in default (deep-equal check, including that both `Source*Spec` pointers are non-nil)
  - `RenderInfo` with the default + a minimal cfg renders a non-empty URLs block AND a non-empty Hosts block (asserts the `Source*Spec`-populated path actually renders, catching the nil-guard regression directly)
  - through `runInfo` (or its testable form): with no `info.yml` on disk in a `t.TempDir()` project, output contains the headers `── URLs ──` and `── Hosts ──`
- [ ] run `go test ./internal/...` — must pass before Task 10

### Task 10: Update reference docs

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
- [ ] run `make test` and `make lint` — final full pass before Task 11

### Task 11: Verify acceptance criteria

- [ ] verify all spec sections (§2–§9) are implemented (cross-reference each row of the validation table and the URL assembly table against the test files)
- [ ] verify edge cases via tests: missing `info.yml` (fallback), missing `port_via`, disabled service, malformed `paths[].path`, malformed `ip`, `NO_COLOR`-style or other rendering env vars (if applicable — most output is plain text)
- [ ] confirm no shell-completion functions need updating: `auto-urls`/`auto-hosts` are config item types in `info.yml`, not CLI flags; `service.icon`/`service.info` are not exposed via CLI surface. No `ValidArgsFunction` or `RegisterFlagCompletionFunc` calls require changes.
- [ ] confirm `internal/setup` (the wizard that generates new service.yml files) is not updated: `icon:` and `info:` are documented as hand-added; the wizard intentionally produces a minimal service.yml. (Verify by reading the wizard's service.yml emission once.)
- [ ] run `make test` — final full pass
- [ ] run `make lint` — all issues must be fixed (per CLAUDE.md: `errcheck`, `govet`, `staticcheck`, `revive`, `gocritic`, `modernize`)
- [ ] run `make build` — confirm the binary builds cleanly
- [ ] manual smoke against a real project is **out of scope** for this PR — see Post-Completion for the real-project migration step

### Task 12: Final documentation and plan move

**Files:**
- Modify: `AGENTS.md` (remember: `CLAUDE.md` is a symlink to `AGENTS.md`)
- Move: this plan file to `docs/plans/completed/`

- [ ] add a Key Patterns entry to `AGENTS.md`:
  > **`info.yml` auto-blocks**: `type: auto-urls` and `type: auto-hosts` items expand at *render* time inside `renderInfoItem` (`internal/ui/info.go`), not at load time. The `InfoItem` carries a `Source*Spec` pointer per the daemon-style sugar pattern (yaml:"-", populated by `InfoItem.UnmarshalYAML` dispatching on `type:` and using a `type alias InfoItem` shadow to avoid infinite recursion on the flat-field decode pass). `LoadInfoConfig` stays config-blind; `RenderInfo` does NOT take a topology parameter — auto-block renderers call `stack.DeployOrder(cfg, types)` directly, which delegates to `config.TopoSortServices` (folder-key space; `ParseTopologyFromFiles` is intentionally unused because it returns compose-name space). When `devbox/info.yml` is missing, `LoadInfoConfig` returns `DefaultInfoConfig()` — a synthesized config with a urls section + hosts section, both with `Source*Spec` pointers populated directly (UnmarshalYAML does not run on Go-constructed configs). Service `icon`, `info.title`, `info.host_key`, `info.port_key`, and `info.paths` are display-only fields read by these renderers (and by status/cmdbrowser for `icon` in a planned follow-up). **Service iteration in renderers MUST go through `stack.DeployOrder(...)` — never `range cfg.Services` directly**, because Go map iteration is randomized and would produce flaky golden tests. **`renderBlock` in `internal/ui/info.go` was tightened to drop empty `renderInfoItem` output from `contentCount`** so `hide_on_empty` sections collapse correctly when their auto-block renders to `""`. **Adding new top-level fields to `service.yml` requires updating BOTH `allowedFieldsFor` in `internal/config/devbox.go:607` AND `servicesAllowedFields` in `internal/validate/config/devbox.go:112`** — service.yml uses `KnownFields(true)` strict decode.
- [ ] `mkdir -p docs/plans/completed && git mv docs/plans/2026-05-26-info-auto-blocks.md docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention — no checkboxes, informational only*

**Real-project migration** (done separately per project, after this PR lands):

For each existing project that uses `devbox`:

- Update each `devbox/services/<name>/service.yml`:
  - add `icon:` (or rely on type default)
  - add `info:` block with `title:`, `host_key:`/`port_key:` (if non-default), and `paths:` (if any) for services that should surface in the URLs block
  - omit `info:` for services that should NOT surface in the dashboard (the renderer skips them silently)
- Rewrite `devbox/info.yml` to use `auto-urls` and `auto-hosts` in place of hand-authored URL/Host subgroups. Drop the legacy "Devbox / Project — <name>" subgroup if present (project identity already lives in the branded header). Set `include: [app, tool, infra]` on auto-urls if any infra services need to surface.
- Run `devbox info` and visually verify:
  - exact ordering: all apps in deploy order, blank line, all tools in deploy order, blank line, infra (if included)
  - per-service subgroup header on its own line
  - main row uses the service icon; path rows use the path icon (defaulting to `🔗`)
  - column alignment is consistent *within* each service subgroup
  - URLs without `:80`/`:443` suffix
  - "Devbox / Project — …" subgroup is gone (if it was previously present)
  - any hand-authored sections (e.g. `Credentials`) render unchanged
- Test the fallback: temporarily rename `devbox/info.yml` to `info.yml.bak` and run `devbox info` again — should render the built-in default (urls + hosts only, no hand-authored sections)
- Edge case to check: a project with no front-proxy (no `type: infra` with `ports.http: 80` or `ports.https: 443`) should render apps with their direct URLs or skip the main row gracefully without crashing

**External system updates**:

- None. Devbox is pre-release with no external consumers (per CLAUDE.md compatibility policy).

**Follow-up ideas (out of scope here)**:

- `info.paths[].when:` for conditional path visibility (currently all paths render unconditionally)
- Credentials as an auto-block sourced from service `credentials:` fields
- Light/dark theme variants for `icon` (most icons are emoji, so likely a no-op)
- Use `service.icon` in `internal/ui/cmdbrowser` and the status table (currently only consumed by info)
