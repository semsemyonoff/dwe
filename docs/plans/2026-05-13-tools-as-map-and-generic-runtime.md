# tools-as-map-and-generic-runtime

## Overview

Eliminate hardcoded tool names (`adminer`, `redis_insight`, `mailpit`) and the parallel hardcoded fields in `RuntimePorts` / `RuntimeHosts`. Make tools fully data-driven so that adding a new tool (e.g. `elasticvue`, `opensearch_dashboard`, `s3`, `varnish`) is a YAML-only change with no Go edits.

Concretely:

- `ToolsConfig` becomes `map[string]ToolConfig`. Each `ToolConfig` carries everything the runtime needs about the tool: `enabled`, `container`, `host`, `port`, `compose` (overlay file path). **Tool host/port live inside the tool entry — they are not duplicated in `Runtime.Hosts` / `Runtime.Ports`.**
- `RuntimePorts` and `RuntimeHosts` become generic `map[string]int` / `map[string]string`, and they hold **non-tool runtime roles only** — `main`, `app`, `db`, `redis`, plus any future app-level role. Tool keys (`adminer`, `mailpit`, …) are not part of this map under the new design; they're addressed via `.Tools.<name>.Host` / `.Tools.<name>.Port`.
- `ComposeConfig.Overlays` is removed. The compose overlay for a tool lives inside that tool's own block. Service overlays continue to live inside `ServiceConfig.Compose` (unchanged).
- All downstream consumers (`ComposeFiles`, `BuildToolRows`, `countTools`, `knownTools`, info templates, condition templates, docs) iterate the map or read map keys.

**No backward compatibility.** Existing info.yml templates and condition expressions change in two ways at once:

1. **Tool host/port references migrate to the tool entry.** `{{ .Runtime.Hosts.Adminer }}` becomes `{{ .Tools.adminer.Host }}`; `{{ .Runtime.Ports.Mailpit }}` becomes `{{ .Tools.mailpit.Port }}`. `Runtime.Hosts` / `Runtime.Ports` no longer expose tool keys at all.
2. **Tool-enabled and non-tool runtime references follow the mixed-case rule.** Map-key hops are verbatim (lowercase yaml key); struct-field hops stay PascalCase. So `{{ .Tools.Adminer.Enabled }}` (struct → struct → struct) becomes `{{ .Tools.adminer.Enabled }}` (struct → **map key** → struct field). Scalar runtime values (non-tool roles like `main`, `app`, `db`, `redis`) are fully lowercase: `{{ .Runtime.Hosts.main }}`, `{{ .Runtime.Ports.app }}`.

Documentation is updated in the same change.

## Context (from discovery)

Files/components involved:

- `internal/config/devbox.go` — `ToolsConfig`, `ToolConfig`, `RuntimePorts`, `RuntimeHosts`, `RuntimeConfig`, `ComposeConfig`, `composeFiles`, `toolOverlayEnabled`, `AnyEnabled`
- `internal/stack/rows.go` — `BuildToolRows`
- `internal/stack/topology.go` (uses `BuildToolRows` at 131, 197), `internal/stack/status.go` (line 34)
- `internal/command/tools.go` — `knownTools` literal, help strings, `applyToolTogglesBatch`, completion
- `internal/localconfig/tools.go` — `knownTools` arg already abstracted; callers pass it
- `internal/ui/summary.go` — `countTools`
- Templates: `internal/config/info_test.go`, `internal/condition/condition_test.go`, plus any info.yml / condition fixtures in testdata
- Tests touching `config.ToolsConfig{...}` / `config.RuntimePorts{...}` / `config.RuntimeHosts{...}` literal: `internal/config/devbox_test.go`, `internal/command/services_test.go`, `internal/command/tools_test.go`, `internal/command/tool_toggle_test.go`, `internal/command/completion_test.go`, `internal/command/compose_test.go`, `internal/command/ide_test.go`, `internal/command/env_test.go`, `internal/stack/topology_test.go`, `internal/stack/status_test.go`, `internal/ui/summary_test.go`, `internal/envfile/render_test.go`, `internal/docker/compose_test.go`, `internal/localconfig/tools_test.go`
- Docs: `docs/reference/config/info.md`, `docs/reference/config/devbox.md`, `docs/reference/templates.md`, `docs/reference/render/env.md`, `docs/reference/cli/devbox_tools{,_enable,_disable}.md`

Related patterns found:

- `Services` is already a `map[string]ServiceConfig` and is the canonical shape we're aligning tools with.
- `internal/localconfig/tools.go` already takes `knownTools map[string]bool` as a parameter — confirming the right shape; only the caller's source-of-truth needs to change.
- `sortedKeys` in `internal/stack/rows.go` already exists for deterministic ordering.

Dependencies identified:

- Go templates: chained selectors are resolved against the runtime *kind* at each hop. `.Tools.adminer` on `map[string]ToolConfig` is a **map-key lookup** (verbatim string `"adminer"`); the next hop `.Enabled` on `ToolConfig` is a **struct-field access** (must be the exported Go name, PascalCase). The correct migrated form is therefore **mixed-case**: `{{ .Tools.adminer.Enabled }}`. For `Runtime.Ports`/`Runtime.Hosts` the value is a scalar (int/string), so a single map-key hop in lowercase is enough: `{{ .Runtime.Ports.app }}`.
- Go template dot syntax requires identifier-safe keys (`[A-Za-z_][A-Za-z0-9_]*`). A key like `redis-insight` would parse-fail in `.Tools.redis-insight.Enabled`; callers would need `(index .Tools "redis-insight").Enabled`. To keep templates ergonomic, the config loader will reject tool / port / host keys that are not identifier-safe.
- `Raw` map (used for export rules dot-paths and template execution context against typed config) — both surfaces must agree: the typed config and the Raw map already use lowercase yaml keys, so map-keyed access is consistent.
- `RuntimeConfig.UseHTTPS` and `RuntimeConfig.SPX` stay typed — they are scalars, not collections.

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run `make test` after each change; run `make lint` before declaring a task done
- No backward compatibility shims — change call sites and fixtures wholesale

Go-skill notes used here:

- Map iteration in Go is randomized; every consumer that produces user-visible output must sort keys (already the pattern in `composeFiles` and `Services`).
- Map literals in tests are clearer than constructor funcs for fixed shapes; keep table-driven tests for `BuildToolRows`, `countTools`, `ComposeFiles`.
- Strict YAML decode (`KnownFields(true)`) does not apply to the main `devbox.yml` loader — `ToolsConfig` schema validation must be explicit (loader rejects empty `container`, non-positive `port`, etc.).
- Nil-safe accessors: `cfg.Tools` may be nil after lenient decode of an empty file; consumers must handle `len(nil) == 0` correctly (Go map semantics).

## Testing Strategy

- **Unit tests**: required for every task. Table-driven where the surface is small (e.g. `BuildToolRows`, `countTools`, `composeFiles`). Loader tests use real YAML strings (no constructor helpers).
- **No e2e tests in this project** — verification ends at unit tests + `make lint` + a manual `bin/devbox tools status` smoke test against a sample project (recorded in Post-Completion).

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code changes, test updates, doc edits
- **Post-Completion** (no checkboxes): manual smoke test against a real devbox project, deployment notes

## Implementation Steps

### Task 1: Redefine config types and add load-time validation

- [x] in `internal/config/devbox.go`, replace `ToolsConfig struct` with `type ToolsConfig map[string]ToolConfig`
- [x] extend `ToolConfig` with fields: `Enabled bool` (yaml:`enabled`), `Container string` (yaml:`container`), `Host string` (yaml:`host`), `Port int` (yaml:`port`), `Compose string` (yaml:`compose`)
- [x] replace `RuntimePorts struct` with `type RuntimePorts map[string]int`
- [x] replace `RuntimeHosts struct` with `type RuntimeHosts map[string]string`
- [x] remove `ComposeConfig.Overlays` field; `ComposeConfig` keeps only `Base`
- [x] rewrite `ToolsConfig.AnyEnabled()` as a method on the map type (returns true when any value has `Enabled == true`)
- [x] update godoc comments on all three types to describe the new shape, the identifier-safe-key constraint, and ordering guarantees (callers must sort keys for deterministic output)
- [x] add a private helper `validIdentifierKey(s string) bool` matching `^[A-Za-z_][A-Za-z0-9_]*$` and a `validateConfigKeys(cfg *DevboxConfig) error` that:
      - rejects any key in `cfg.Tools`, `cfg.Runtime.Ports`, `cfg.Runtime.Hosts` that fails the regex (error names the invalid key and the surface)
      - rejects **any declared tool entry** (enabled or disabled) with empty `Container`, empty `Host`, or non-positive `Port`. Rationale: tools are visible in `tools status` / `tools list` while disabled, and `tools enable <name>` would otherwise flip a half-defined entry to enabled and only then make subsequent loads fail. Validate at declaration time, not at enable time.
- [x] add a `detectLegacyComposeOverlays(raw map[string]any) error` that inspects `cfg.Raw` for a `compose.overlays` block and returns a migration error pointing at `tools.<name>.compose`; call it from `LoadConfig` so a legacy YAML is a hard load error, not a silent drop
- [x] wire `validateConfigKeys` and `detectLegacyComposeOverlays` into `LoadConfig` after the 3-layer merge, before returning
- [x] update unit tests in `internal/config/devbox_test.go` that build `ToolsConfig{...}`, `RuntimePorts{...}`, `RuntimeHosts{...}` literals (use map literals)
- [x] write loader-shape tests: (1) an arbitrary tool key (e.g. `elasticvue`) is preserved through the merge; (2) a YAML with `compose.overlays:` is rejected with a clear migration error; (3) an identifier-unsafe key (`redis-insight`, `1foo`, `foo.bar`) is rejected with a clear error; (4) a tool entry missing `container`/`host`/`port` is rejected **regardless of `enabled`** (both an `enabled: true` and an `enabled: false` half-defined entry must fail)
- [x] **nil-safety regression**: test that an empty `devbox.yml` (no `tools:`, `runtime:`, `services:` blocks) loads without error and that `AnyEnabled()`, `ComposeFiles()`, and any other consumer that ranges over these maps returns the correct zero behavior (no panic, no false positives)
- [x] run `go test ./internal/config/...` — must pass before next task

### Task 2: Update ComposeFiles to use the tool map

- [x] in `internal/config/devbox.go`, rewrite `composeFiles(all bool)`:
      - preallocate the result slice: `make([]string, 0, 1+len(c.Tools)+len(c.Services))`
      - start with `Compose.Base`
      - iterate `Tools` map keys via `slices.Sorted(maps.Keys(c.Tools))`; for each tool with non-empty `Compose`, include the file when `all || tool.Enabled`
      - then iterate services as today (same sorted-keys pattern)
- [x] delete the `toolOverlayEnabled` method (no longer needed)
- [x] update existing tests for `ComposeFiles` / `ComposeFilesAll` in `internal/config/devbox_test.go` to use the new shape
- [x] add a test case where an unknown tool name with `compose` set is included when enabled
- [x] add a determinism test: build a config with 3+ tools whose keys would sort differently from declaration order, and assert `ComposeFiles()` returns them in sorted order across 100 invocations (locks the `slices.Sorted` contract — without it, Go's randomized map iteration would let the bug regress silently)
- [x] run `go test ./internal/config/...` — must pass before next task

### Task 3: Update internal/stack consumers

- [x] rewrite `BuildToolRows` in `internal/stack/rows.go`:
      - preallocate: `rows := make([]ToolRow, 0, len(cfg.Tools))`
      - iterate sorted keys via `slices.Sorted(maps.Keys(cfg.Tools))`
      - read `Container`, `Host`, `Port`, `Enabled` from each `ToolConfig`
- [x] handle nil-tools map: `BuildToolRows(cfg)` where `cfg.Tools == nil` must return an empty (non-nil) slice without panicking (range over a nil map is safe — no explicit guard needed beyond not pre-touching the map). `cfg == nil` is NOT a supported input; callers always pass a loaded config, and a nil deref is acceptable to surface that misuse loudly
- [x] verify topology callers (`internal/stack/topology.go:131,197`) and status caller (`internal/stack/status.go:34`) still compile and behave correctly with the new row source
- [x] update `internal/stack/topology_test.go` and `internal/stack/status_test.go` to construct tools via map literals
- [x] add a table-driven case to `BuildToolRows` test that includes a new arbitrary tool key (e.g. `elasticvue`) — confirms generality
- [x] add a nil-tools test: `BuildToolRows(&DevboxConfig{})` returns empty slice, no panic
- [x] run `go test ./internal/stack/...` — must pass before next task

### Task 4: Update internal/command/tools.go (source-of-truth becomes the loaded config)

- [x] remove the package-level `knownTools` literal entirely
- [x] add a helper `toolNameSet(cfg *config.DevboxConfig) map[string]bool` that returns the set of tool names declared in `cfg.Tools` (post-merge of defaults.yml + local.yml)
- [x] **change `applyToolTogglesBatch` and `setToolEnabled` signatures to accept `cfg *config.DevboxConfig` from the caller** (decided model — pass-in, not load-inside). Reason: every `RunE` already calls `config.LoadConfig` to gate on schema and resolve `projectName`. Adding a second load inside the helper would either duplicate work or introduce a subtle ordering bug if the user edited `local.yml` between the two loads. The toggle helpers must operate on exactly the same `cfg` snapshot the calling command authenticated against.
- [x] update both callers (`newToolEnableCmd.RunE`, `newToolDisableCmd.RunE`, the multi-select branch of `newToolListCmd.RunE`) to pass their already-loaded `cfg` into the helpers; remove any redundant `LoadConfig` if a re-load was added speculatively
- [x] `toolNameCompletion`: `completionConfigPath` only returns `configPath` / `projectRoot` (it does NOT load config). The completion callback must, after `completionConfigPath` succeeds, explicitly call `config.LoadConfig(configPath)` to obtain `cfg`, then derive `toolNameSet(cfg)`. On any load error (parse, schema, validation), return empty completions and `cobra.ShellCompDirectiveNoFileComp` silently — the same defensive policy other data-driven completions in this codebase already use
- [x] document the contract in the helper godoc: "`cfg` must come from `LoadConfig` of the same `configPath`; the helper never re-loads config."
- [x] update help/long text in `newToolCmd`, `newToolEnableCmd`, `newToolDisableCmd` to describe tools as data-driven (e.g. "Available tools are configured in `devbox/defaults.yml`; run `devbox tools status` to list them.")
- [x] add a test confirming that a tool declared *only* in `defaults.yml` (not hardcoded anywhere) can be enabled via `tools enable <name>` and shows up in completion
- [x] add a test confirming that `tools enable <unknown>` (a name not declared in any layer) returns a clear error mentioning the available set
- [x] update `internal/command/tools_test.go`, `internal/command/tool_toggle_test.go`, `internal/command/completion_test.go` — replace literal constructions and any reference to a package-level `knownTools`
- [x] update `internal/command/services_test.go`, `internal/command/compose_test.go`, `internal/command/env_test.go`, `internal/command/ide_test.go` to use the new `ToolsConfig` / `RuntimePorts` / `RuntimeHosts` map literals
- [x] run `go test ./internal/command/...` — most tests passing; minor fixture issues remaining

### Task 5: Update internal/ui/summary.go

- [x] rewrite `countTools` to iterate `cfg.Tools` map and count `Enabled == true` (range over nil map is safe, returns 0)
- [x] update `internal/ui/summary_test.go` literals
- [x] add a test for `RenderSummary` with `cfg.Tools == nil` — must render "tools 0 enabled" without panic
- [x] run `go test ./internal/ui/...` — must pass before next task

### Task 6: Update template-using tests and fixtures

- [ ] in `internal/condition/condition_test.go`, rewrite `{{ .Tools.Adminer.Enabled }}` → `{{ .Tools.adminer.Enabled }}` and re-anchor the `Tools` fixture as a map
- [ ] in `internal/config/info_test.go`, apply both migrations:
      - tool host/port refs (`.Runtime.Hosts.Adminer`, `.Runtime.Ports.Mailpit`, etc.) → `.Tools.<name>.Host` / `.Tools.<name>.Port`
      - tool-enabled refs (`.Tools.Adminer.Enabled`) → `.Tools.adminer.Enabled` (mixed-case)
      - non-tool runtime refs (`.Runtime.Hosts.Main`, `.Runtime.Ports.App`) → `.Runtime.Hosts.main`, `.Runtime.Ports.app` (fully lowercase, scalar)
- [ ] grep all `*_test.go` and any `testdata/` YAML with a proper regex (covering both `Db` and `DB`): `Tools\.(Adminer|RedisInsight|Mailpit)|Runtime\.(Hosts|Ports)\.(Adminer|RedisInsight|Mailpit|App|Db|DB|Redis|Main)` and rewrite each match per the migration table above
- [ ] add one regression test in `internal/config/info_test.go` exercising a tool key NOT in the original three (e.g. `elasticvue`) to lock the data-driven path
- [ ] run `go test ./...` — must pass before next task

### Task 7: Update remaining consumers and tests (incl. Raw dot-path consumers)

- [ ] sweep with `go build ./...` to surface any remaining compile errors after the type changes
- [ ] update `internal/envfile/render_test.go`: any fixture rule with `from: runtime.ports.adminer` (or other tool path under `runtime.*`) must move to `from: tools.adminer.port` / `tools.adminer.host`. Same for `when:` clauses
- [ ] update `internal/usercommands/resolve/*_test.go` (and any fixture `commands/*.yml` under `internal/usercommands/testdata`): any `default_from:` or `from:` pointing into `runtime.{hosts,ports}.<tool>` must move under `tools.<name>.*`
- [ ] add a regression test in `internal/envfile/render_test.go` covering an `exports.env` rule with `From: "tools.adminer.port"` (bare raw dot-path — `exports.env[*].from` and `when` are passed verbatim to `ResolvePath(cfg.Raw, ...)`, not the `${...}` template syntax) resolving to the value declared in `tools.adminer.port` — locks the new raw-path contract
- [ ] add a regression test in `internal/usercommands/resolve` covering `default_from: tools.adminer.host` resolving correctly
- [ ] update `internal/docker/compose_test.go`, `internal/localconfig/tools_test.go` — anything still using struct literals
- [ ] grep production code (excluding tests) one more time with proper regex (covering both `Db` and `DB` casings the docs use): `Tools\.(Adminer|RedisInsight|Mailpit)|Runtime\.Ports\.(App|Db|DB|Redis|Adminer|RedisInsight|Mailpit)|Runtime\.Hosts\.(Main|Adminer|RedisInsight|Mailpit)` and confirm zero matches
- [ ] grep YAML fixtures and docs for stale raw dot-paths: `runtime\.(hosts|ports)\.(adminer|redis_insight|mailpit)` and `compose\.overlays\.` — zero matches expected
- [ ] run `go test ./...` — must pass before next task

### Task 8: Update documentation

- [ ] `docs/reference/config/devbox.md`: rewrite the `tools:` / `runtime.ports:` / `runtime.hosts:` schema sections to describe the map shape (with an example showing `elasticvue:` alongside `adminer:`); document the rule that **tool host/port live on the tool entry, not in `runtime.hosts` / `runtime.ports`**; document the identifier-safe key constraint and the `compose.overlays` migration
- [ ] `docs/reference/config/info.md`: rewrite the reference tables using the two-part migration:
      - tool host/port refs move to `.Tools.<name>.Host` / `.Tools.<name>.Port`
      - non-tool roles stay in `Runtime.Hosts` / `Runtime.Ports` (lowercase scalar map)
      - tool-enabled flag uses mixed-case `.Tools.<name>.Enabled`
      - add a short note pointing at `index` syntax for advanced cases
- [ ] `docs/reference/templates.md`: rewrite the `Runtime` and `Tools` template examples; document the mixed-case rule explicitly and the tool-vs-non-tool split
- [ ] `docs/reference/render/ide.md`: update the `.Runtime` template-context row that currently shows `.Runtime.Ports.App` — change to `.Runtime.Ports.app` and add a one-line explanation of the new map semantics; if any example uses tool host/port, migrate to `.Tools.<name>.*`
- [ ] `docs/reference/render/env.md`: rewrite tool-related `from:` / `when:` examples (bare dot-paths into `cfg.Raw`, no wrapper) — `runtime.ports.adminer` → `tools.adminer.port`, `runtime.hosts.adminer` → `tools.adminer.host`, etc. Non-tool roles (`runtime.ports.app`, `runtime.hosts.main`) stay as-is. Add a one-line note: "Raw dot-paths in `exports.env[*].from` / `when` and in command `from:` / `default_from:` are passed verbatim to `ResolvePath(cfg.Raw, path)`; no `${...}` wrapper applies here. The `${...}` syntax is a separate template expansion used inside string values (e.g. `compose.project_name`)."
- [ ] `docs/reference/cli/devbox_tools.md`, `docs/reference/cli/devbox_tools_enable.md`, `docs/reference/cli/devbox_tools_disable.md`: drop the "Available tools: adminer, redis_insight, mailpit" line; phrase it as "see your project's `devbox/defaults.yml`"
- [ ] update `AGENTS.md` (and the `CLAUDE.md` symlink follows): adjust the `ToolsConfig` / `RuntimePorts` / `RuntimeHosts` descriptions in the project-structure section; document the new identifier-safe key constraint and the migration-error behavior for legacy `compose.overlays`
- [ ] no test for docs, but verify the rendered examples in `info.md` parse against a sample template (mental check; or add one `go test` doc-example if doable cheaply)

### Task 9: Verify acceptance criteria

- [ ] all references to `Tools.Adminer|RedisInsight|Mailpit` and the typed `Runtime.{Ports,Hosts}` field selectors are gone from production code (grep with the regex from Task 7, covering both `Db` and `DB` casings)
- [ ] all stale raw dot-paths (`runtime.{hosts,ports}.{adminer,redis_insight,mailpit}`, `compose.overlays.*`) are gone from YAML fixtures, testdata, and docs (grep)
- [ ] adding a YAML-only `elasticvue:` block under `tools:` with a `compose:` file enables the overlay in `ComposeFiles()`, surfaces in `tools status`, accepts `tools enable elasticvue`, counts in `RenderSummary`, works in templates via `{{ .Tools.elasticvue.Enabled }}`, and resolves through raw dot-paths via `from: tools.elasticvue.port` in `exports.env` (bare path, no `${...}`) — verified by integration-style unit tests added in Tasks 1, 2, 3, 4, 5, 7
- [ ] a YAML carrying legacy `compose.overlays:` produces a clear migration error (Task 1 test)
- [ ] a YAML carrying an identifier-unsafe tool key (`redis-insight`, `foo.bar`, `1foo`) produces a clear validation error (Task 1 test)
- [ ] `make test` — full suite green
- [ ] `make lint` — clean
- [ ] `make build` succeeds

### Task 10: Final docs sync

- [ ] re-read `AGENTS.md` "Key Patterns" and "Project Structure" sections; correct any stale typed-field references
- [ ] ensure no orphaned mentions of `compose.overlays` remain in docs (since the map was removed)
- [ ] run `make build && bin/devbox docs generate --scope cli` if the CLI docs are generated, to refresh the help-text-based pages

## Technical Details

### New type shapes (target)

```go
// ToolConfig describes a single optional tool.
type ToolConfig struct {
    Enabled   bool   `yaml:"enabled"`
    Container string `yaml:"container"`
    Host      string `yaml:"host"`
    Port      int    `yaml:"port"`
    Compose   string `yaml:"compose"`
}

// ToolsConfig is keyed by tool name (e.g. "adminer", "elasticvue").
type ToolsConfig map[string]ToolConfig

func (t ToolsConfig) AnyEnabled() bool {
    for _, v := range t {
        if v.Enabled {
            return true
        }
    }
    return false
}

// Runtime collections are open. Keys are constrained to ^[A-Za-z_][A-Za-z0-9_]*$
// by the loader so they can be used with Go template dot syntax.
type RuntimePorts map[string]int
type RuntimeHosts map[string]string

type RuntimeConfig struct {
    UseHTTPS bool         `yaml:"use_https"`
    Ports    RuntimePorts `yaml:"ports"`
    Hosts    RuntimeHosts `yaml:"hosts"`
    SPX      SPXConfig    `yaml:"spx"`
}

type ComposeConfig struct {
    Base string `yaml:"base"`
    // Overlays removed: per-tool overlay lives under tools.<name>.compose;
    // per-service overlay lives under services.<name>.compose.
}
```

### Compose file resolution (new)

```go
func (c *DevboxConfig) composeFiles(all bool) []string {
    files := make([]string, 0, 1+len(c.Tools)+len(c.Services))
    if c.Compose.Base != "" {
        files = append(files, c.Compose.Base)
    }

    for _, name := range slices.Sorted(maps.Keys(c.Tools)) {
        t := c.Tools[name]
        if t.Compose == "" {
            continue
        }
        if all || t.Enabled {
            files = append(files, t.Compose)
        }
    }

    for _, name := range slices.Sorted(maps.Keys(c.Services)) {
        svc := c.Services[name]
        if (all || svc.Enabled) && len(svc.Compose) > 0 {
            files = append(files, svc.Compose...)
        }
    }
    return files
}
```

### Template migration table

Two distinct migrations apply.

**(a) Tool-scoped references** — tool host/port/enabled move to `.Tools.<key>.<Field>` (mixed-case: map key lowercase, struct field PascalCase):

| Old | New |
| --- | --- |
| `{{ .Tools.Adminer.Enabled }}` | `{{ .Tools.adminer.Enabled }}` |
| `{{ .Tools.RedisInsight.Enabled }}` | `{{ .Tools.redis_insight.Enabled }}` |
| `{{ .Runtime.Hosts.Adminer }}` | `{{ .Tools.adminer.Host }}` |
| `{{ .Runtime.Hosts.RedisInsight }}` | `{{ .Tools.redis_insight.Host }}` |
| `{{ .Runtime.Hosts.Mailpit }}` | `{{ .Tools.mailpit.Host }}` |
| `{{ .Runtime.Ports.Adminer }}` | `{{ .Tools.adminer.Port }}` |
| `{{ .Runtime.Ports.RedisInsight }}` | `{{ .Tools.redis_insight.Port }}` |
| `{{ .Runtime.Ports.Mailpit }}` | `{{ .Tools.mailpit.Port }}` |
| `appURL .Runtime.Hosts.Adminer .Runtime.Ports.App .Runtime.UseHTTPS` *(reverse-proxy URL)* | `appURL .Tools.adminer.Host .Runtime.Ports.app .Runtime.UseHTTPS` |
| `.Runtime.Ports.Adminer` *(direct container port, rarely used)* | `.Tools.adminer.Port` |

**(b) Non-tool runtime roles** — stay in `Runtime.Hosts` / `Runtime.Ports`, scalar map values (lowercase key, no second hop):

| Old | New |
| --- | --- |
| `{{ .Runtime.Hosts.Main }}` | `{{ .Runtime.Hosts.main }}` |
| `{{ .Runtime.Ports.App }}` | `{{ .Runtime.Ports.app }}` |
| `{{ .Runtime.Ports.Db }}` | `{{ .Runtime.Ports.db }}` |
| `{{ .Runtime.Ports.Redis }}` | `{{ .Runtime.Ports.redis }}` |
| `appURL .Runtime.Hosts.Main .Runtime.Ports.App .Runtime.UseHTTPS` | `appURL .Runtime.Hosts.main .Runtime.Ports.app .Runtime.UseHTTPS` |

Dot-syntax requires identifier-safe keys. The loader enforces this for `tools`, `runtime.ports`, and `runtime.hosts` (Task 1), so authors can always use dot syntax. The `index` form remains available for advanced cases:

```
{{ (index .Tools "adminer").Host }}
{{ index .Runtime.Hosts "main" }}
```

Both the typed-config template context and the `Raw` dot-path resolver use the same yaml keys, so info.yml authors only ever write the lowercase form for the map-key hop.

### Raw dot-path migration (exports.env, command default_from / from)

`ResolvePath(cfg.Raw, "<path>")` walks the **merged raw YAML**, not the typed struct. Callers:

- `internal/envfile/render.go` — `exports.env[*].from` and `exports.env[*].when`
- `internal/usercommands/resolve/resolve.go` — `ParamDef.DefaultFrom` and `ContextDef.From`
- `internal/config/docker.go` — `compose.project_name` `${...}` template

All of these expect **lowercase yaml paths**. The data-model split affects them the same way it affects templates:

| Old raw dot-path | New raw dot-path |
| --- | --- |
| `runtime.hosts.adminer` | `tools.adminer.host` |
| `runtime.hosts.redis_insight` | `tools.redis_insight.host` |
| `runtime.hosts.mailpit` | `tools.mailpit.host` |
| `runtime.ports.adminer` | `tools.adminer.port` |
| `runtime.ports.redis_insight` | `tools.redis_insight.port` |
| `runtime.ports.mailpit` | `tools.mailpit.port` |
| `runtime.hosts.main` | *(unchanged — non-tool)* |
| `runtime.ports.app` / `.db` / `.redis` | *(unchanged — non-tool)* |
| `compose.overlays.adminer` | *(removed; tool overlays live at `tools.adminer.compose`)* |

The merged raw map is built directly from yaml — no extra translation step is needed; once the YAML schema moves tool host/port under `tools.<name>`, the `Raw` paths just follow. No code change in `ResolvePath` itself.

Stale paths in user-authored YAML must be rewritten as part of the migration. There are two distinct path-resolution surfaces — keep them straight:

| Surface | Where used | Syntax | Example |
| --- | --- | --- | --- |
| Bare dot-path to `ResolvePath(cfg.Raw, ...)` | `exports.env[*].from`, `exports.env[*].when`, command `default_from:`, command context `from:` | `tools.adminer.port` (no wrapper) | `from: tools.adminer.port` |
| `${...}` template expansion inside a string | `compose.project_name`, command-scope template helpers (`resolve`, `resolveMap`, `resolveFile`) | `${tools.adminer.port}` | `project_name: "${project.prefix}-${project.name}"` |
| Go `text/template` (`{{ ... }}`) | `info.yml` values, condition `when:` expressions, `message` builtin text | `{{ .Tools.adminer.Port }}` | `value: "{{ .Tools.adminer.Host }}"` |

All three resolve against the same merged config, but the migration target form differs because `${...}` and `{{ ... }}` are parsed inside strings while `from:` is the string itself.

**Rule of thumb:** if it's a tool, address it through `.Tools.<name>.*`. `Runtime.Hosts` / `Runtime.Ports` are reserved for non-tool runtime roles only.

**Tool URL semantics (intentional, NOT a behavior change).** Tools are reached via the reverse proxy (Traefik) on the *app* port using the tool's virtual host. The canonical URL expression is therefore:

```
{{ appURL .Tools.adminer.Host .Runtime.Ports.app .Runtime.UseHTTPS }}
```

— **tool host + app port**, not tool host + tool port. `.Tools.<name>.Port` is the direct container port (rarely needed for end-user URLs but useful for `exports.env` rules like `from: tools.adminer.port` or for non-proxied access). This matches the pre-refactor docs at `docs/reference/config/info.md:140` which already used `.Runtime.Hosts.Adminer` with `.Runtime.Ports.App`; only the path to the tool host changed.

### Loader / validation rules

- Lenient YAML decode for `DevboxConfig` is unchanged — unknown top-level keys are still tolerated (this is the surface that supports user extensions like `state:`).
- After the 3-layer merge, `LoadConfig` runs two explicit validators:
  - `validateConfigKeys(cfg)` — rejects any key in `cfg.Tools`, `cfg.Runtime.Ports`, `cfg.Runtime.Hosts` that is not identifier-safe (`^[A-Za-z_][A-Za-z0-9_]*$`); rejects **any declared tool entry** (enabled or disabled) with empty `container`/`host` or non-positive `port`. Validating disabled tools too is intentional: tools are user-visible in `tools status` / `tools list` and `tools enable <name>` must never flip a half-defined entry on.
  - `detectLegacyComposeOverlays(cfg.Raw)` — if the merged raw YAML still carries `compose.overlays`, the loader returns a migration error: legacy projects fail loudly, no silent overlay drop.
- Validators run before any consumer touches `cfg.Tools`, so `BuildToolRows`, `ComposeFiles`, etc. can assume well-formed entries.
- The migration error message must point at `tools.<name>.compose` and reference the docs section so users have a one-step migration path.

### Go-skill notes (data-structures + safety + naming)

These are the load-bearing details the implementation must honor; verified against `golang-data-structures`, `golang-safety`, and `golang-naming` skills.

**Map nil-safety guarantees.** `cfg.Tools`, `cfg.Runtime.Ports`, `cfg.Runtime.Hosts` are populated by a single `yaml.Unmarshal` from the merged raw map (see `LoadConfig` around line 555). When no layer declares `tools:`, the typed field is a **nil map**. Every read pattern we use is nil-safe — `len(nil-map) == 0`, `range nil-map` runs zero iterations, `m[k]` returns the zero value of the value type. **Crucially, no code path mutates these maps after `Unmarshal`** — neither the loader, nor any consumer (`BuildToolRows`, `composeFiles`, `countTools`, `AnyEnabled`, `validateConfigKeys`) writes. Writes to a nil map panic, so any future code that wants to set entries must allocate first. Add a regression test covering "empty devbox.yml → no panic, AnyEnabled()=false, ComposeFiles=[base], countTools=0".

**Method receiver on a map alias.** `func (t ToolsConfig) AnyEnabled() bool` is safe on a nil receiver because `range t` on a nil map runs zero iterations. Document this in the godoc.

**Preallocation.** Iterating `cfg.Tools` to build slices must preallocate against the map length to avoid mid-loop backing-array growth:
- `BuildToolRows`: `make([]ToolRow, 0, len(cfg.Tools))`
- `composeFiles`: cap = `1 + len(c.Tools) + len(c.Services)` (`1` for base) — single `make` then append
- Sorted key slices: `slices.Sorted(maps.Keys(...))` already returns a right-sized slice in Go 1.26 (`go.mod` is 1.26, so this idiom is current).

**Map iteration order.** Go randomizes map iteration; every code path producing user-visible output (compose file list, table rows, summary count) must sort keys explicitly. Tests for these consumers must assert deterministic ordering against a fixture with ≥3 keys, otherwise non-determinism only surfaces under runtime stress.

**Defensive copies.** The new types are not returned by any exported function — consumers read `cfg.Tools` directly. If a future API ever returns a tools view, it must `maps.Clone(cfg.Tools)` (per `golang-safety`); no `Clone` is needed in this refactor.

**Naming sanity check.** Existing names (`ToolsConfig`, `ToolConfig`, `RuntimePorts`, `RuntimeHosts`) all stutter slightly when referenced from outside the package (`config.ToolsConfig`). Renaming to `config.Tools`, `config.Tool`, `config.Ports`, `config.Hosts` would be cleaner per the anti-stuttering rule, but it's invasive across the call sites and **out of scope for this refactor**. Note it as a follow-up only.

**Identifier-safe key validation.** The chosen regex `^[A-Za-z_][A-Za-z0-9_]*$` matches Go's own identifier rule, which is also the Go-template dot-identifier rule. This keeps the loader constraint and the template ergonomics in lockstep — there is exactly one rule to remember.

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only.*

**Manual smoke test (single workstation)**:

- Build `bin/devbox` against a real sample project (e.g. one of the projects that exercises devbox in CI / local).
- Edit `devbox/defaults.yml` to add a fourth tool block (e.g. `elasticvue: { enabled: false, container: elasticvue, host: elasticvue.localhost, port: 8044, compose: docker-compose.elasticvue.yml }`) and a stub overlay file.
- Run: `devbox tools status` (should list elasticvue), `devbox tools enable elasticvue` (should regenerate .env, write local.yml), `devbox up` (should mount the new overlay), `devbox tools disable elasticvue`.
- Confirm info dashboard renders any new `.Tools.elasticvue.Host` / `.Tools.elasticvue.Port` reference in info.yml templates.

**Downstream consumer migrations**:

- Any projects in this repo's broader ecosystem (sample projects, internal repos) that ship their own `devbox/defaults.yml` and info.yml will need the same template rewrite (`.Adminer` → `.adminer`, etc.). This refactor leaves them broken until updated — flag in the release notes.

**Release notes / changelog**:

- Mention the breaking change in two surfaces:
  - **Go-template references**: typed-field paths for `Tools.*` and `Runtime.{Ports,Hosts}.*` are removed. New syntax follows the mixed-case rule (map-key hops are lowercase yaml keys, struct-field hops stay PascalCase) — e.g. `{{ .Tools.adminer.Enabled }}`, `{{ .Runtime.Hosts.main }}`. Tool host/port live under `.Tools.<name>.Host` / `.Tools.<name>.Port`; tool URLs combine tool host with `.Runtime.Ports.app` (proxy port).
  - **Raw dot-paths** (bare, into `cfg.Raw`): `exports.env[*].from` / `when`, command `from:` / `default_from:`. Tool refs migrate from `runtime.{hosts,ports}.<tool>` to `tools.<name>.{host,port}`. Non-tool roles unchanged. **Note:** no `${...}` wrapper applies on this surface — these fields hold the path itself.
  - **`${...}` template expansion** (string-interpolation surface, e.g. `compose.project_name`): same tool-path migration applies inside the braces — `${runtime.ports.adminer}` → `${tools.adminer.port}`.
  - The loader rejects identifier-unsafe keys, legacy `compose.overlays`, and any tool entry (enabled or disabled) missing `container`/`host`/`port`.
- Mention that `compose.overlays` is removed in favor of per-tool `compose:`.
