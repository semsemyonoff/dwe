# Unified Services Schema (app / tool / infra) with Multi-Port

## Overview

Collapse the current two-file model (`devbox/services.yml` + `devbox/tools.yml`) and the
flat `runtime.ports` / `runtime.hosts` namespaces into a single `devbox/services.yml` whose
entries are discriminated by `type:` (`app` | `tool` | `infra`). Each service owns its own
`ports: map[string]int` and `hosts: map[string]string` — always maps, no polymorphic
`int | map`. The `runtime.ports.*` and `runtime.hosts.*` template paths disappear; the
canonical dot-path becomes `services.<name>.ports.<port-name>` and
`services.<name>.hosts.<host-name>`.

**Why:**
- One canonical schema for "named container with options"; no parallel loaders, validators,
  overlay rules, or command surfaces per kind.
- Adding a new kind (`worker`, `scheduled_job`, …) is an enum value + per-type field
  allowlist, not a new YAML file.
- Multi-port falls out for free: `ports:` is always a map. No `Port int | map` gymnastics.
- Removes the awkward "role == service name" coupling forced by today's flat
  `runtime.ports`: each service now owns its own named-port namespace.

**Type semantics (locked):**
- `app` — has source under `dir:`, **deploy lifecycle** (only apps may have
  `devbox/deploy/<name>.yml`), IDE/AI/git render, `cli:`, `extends:`.
- `tool` — ephemeral utility container (adminer, mailhog). Cannot be a `depends_on` target
  of any service. No `dir`, `configs`, `extends`, `render`, `cli`. **Not deployable.**
- `infra` — backing service (db, cache, queue). **Can** be a `depends_on` target. Same
  field allowlist as `tool` otherwise. **Not deployable** (no `devbox/deploy/<name>.yml`
  permitted; deploy enumeration filters to `app` only).

**Locked decisions (from planning conversation):**
- `extends:` — only for `type: app`. Cross-type extends rejected.
- `compose:` — `[]string` for all types (tool's current `string` is widened).
- `container:` — inherited via extends chain (overridable). Unchanged.
- `hosts:` — always `map[string]string`, symmetric with `ports`.
- `mandatory:` — universal flag for any type.
- No `port:` / `host:` shorthand. Single port is `ports: { http: 80 }`.
- No backwards compatibility (pre-release policy in CLAUDE.md).
- Big-bang single PR. `next/tbm` fixture updated in same PR.

## Context (from discovery)

**Files/components involved:**
- `internal/config/devbox.go` — `ServiceConfig` (l.456), `ToolConfig` (l.602),
  `LoadConfig` (l.781), `LoadServicesConfig` (l.1042), `LoadToolsConfig` (l.955),
  `validateToolsOverlay` (l.979), `injectToolsIntoRaw` (l.1014),
  `injectServicesIntoRaw` (l.1189), runtime-ports/hosts resolution (l.881-882, l.712-745).
- `internal/tpl/render_command.go` — `CompileVarSyntax` (l.87), `resolveMapPath` (l.206).
- `internal/validate/config/devbox.go` — current services validator (l.89).
- `internal/command/service.go`, `services.go`, `tool.go`, `tools.go`, `status.go` —
  toggle, enable/disable, status subcommands, completion.
- `internal/stack/status.go` — `RenderServices` (l.42), `RenderTools` (l.70).
- `internal/stack/custom_columns.go` — `BuildCustomColumns` (l.25), `RenderCustomCells` (l.61).
- `internal/ui/` — `RenderServiceTable`, `RenderToolTable`.
- `internal/envfile/` — export rules referencing `runtime.ports.*`.
- `internal/templates/` — already generic across kinds (only `Render.IDE.Enabled` default
  differs for `type: "app"`).
- `next/tbm/devbox/` — in-repo fixture project. Updated alongside.
- Docs: `docs/reference/config/services.md`, `tools.md`, `docker.md`, anything referencing
  `runtime.ports` / `runtime.hosts`.
- `CLAUDE.md` (== `AGENTS.md`) — extensive description of current shape needs updating.

**Related patterns found:**
- Strict-decode YAML for user-edited pipeline/command/manifest files (matches new
  `services.yml` strictness expectation).
- 3-layer merge (devbox.yml → defaults.yml → local.yml) with overlay validation per-block.
  The new `validateServicesOverlay` mirrors `validateToolsOverlay` shape exactly.
- `injectServicesIntoRaw` already mirrors service fields; extend it to include the new
  `ports` / `hosts` nested maps so `${services.X.ports.Y}` resolves through `Raw`.

**Dependencies identified:**
- `tpl.CompileVarSyntax` does not need code changes — generic dot-path resolver already
  walks nested maps via `Raw`. Only the rewrite-namespace allowlist may need adjustment
  if `runtime.*` had special handling (verify in Task 2).
- Export rules in user fixtures referencing `runtime.ports.<name>` must be migrated to
  `services.<name>.ports.<port-name>`.

## Development Approach

- **Testing approach**: Regular (code first, then tests in same task).
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task.
- **CRITICAL: all tests must pass before starting next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run `make test` after each task.
- Big-bang policy: intermediate states may not compile cleanly between tasks (e.g., after
  removing `ToolConfig` but before updating its callers). Each task ends with a green
  build and green tests; partial-compile mid-task is fine.

## Testing Strategy

- **Unit tests**: required per task. Table-driven for parsers/validators (loader, overlay
  validation, type-discriminator field allowlist, extends resolution).
- **Fixtures**: package-local `testdata/` YAML fixtures per loader scenario (valid app /
  tool / infra; invalid: tool with `dir`, infra with `extends`, missing `type`, unknown
  type, `ports` not a map, single-port shorthand rejected).
- **Cross-package tests**: command-layer tests for the locked CLI shape (per Task 7):
  unified `devbox services` toggle (mixed-type fixture, mandatory short-circuit),
  `devbox services enable <name>` / `disable <name>` across types by name, completion
  helpers returning the union across all types, per-type `devbox status apps` /
  `tools` / `infra` subcommands, `--no-apps` / `--no-tools` / `--no-infra` flag
  suppression, plus a guard test that `devbox tools` is an unknown command.
- **End-to-end check**: `next/tbm` fixture must `make build && bin/devbox validate` cleanly
  at end of refactor.
- **No e2e UI suite** exists — manual TTY checks for toggle/cmdbrowser belong in
  Post-Completion.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, docs achievable in this repo.
- **Post-Completion** (no checkboxes): manual TTY verification, downstream project updates,
  perf/security review.

## Implementation Steps

### Task 1: Define unified `ServiceConfig` shape and type-discriminator helpers

- [x] in `internal/config/devbox.go`, extend `ServiceConfig` with `Ports map[string]int`
      and `Hosts map[string]string`; widen no fields yet (keep `Compose []string` as-is).
- [x] declare `type ServiceType string` (a **named string type** — not an alias, not an
      int-backed iota). YAML strict-decode handles named string types natively, so no
      `UnmarshalYAML` plumbing is required. Set the `ServiceConfig.Type` field to
      `Type ServiceType` (yaml tag `type`).
- [x] declare exported constants:
      ```
      const (
          ServiceTypeApp   ServiceType = "app"
          ServiceTypeTool  ServiceType = "tool"
          ServiceTypeInfra ServiceType = "infra"
      )
      ```
- [x] add unexported set `validServiceTypes` plus `(ServiceType).IsValid() bool` and
      `(ServiceType).Validate() error` (wraps sentinel `ErrServiceTypeUnknown` for
      values outside the set; wraps `ErrServiceTypeMissing` for the empty string).
      Error message lowercase, prefixed `"config: "`.
- [x] add predicate methods on `ServiceType`:
      `(t ServiceType).IsApp() / .IsTool() / .IsInfra()`. The `ServiceConfig` struct
      gets thin forwarders `(s ServiceConfig).IsApp() bool { return s.Type.IsApp() }`
      etc. so call sites that already hold a `ServiceConfig` value don't have to
      reach through `.Type`. Match value-vs-pointer receiver to the existing
      `ServiceConfig` methods.
- [x] add field-allowlist helper `allowedFieldsFor(t ServiceType) map[string]bool` —
      unexported, single source of truth for validators and loader strict-decode
      messages. Build and return a fresh map each call (small, defensive, no caller
      can corrupt shared state).
- [x] write tests for `(ServiceType).Validate` (valid cases + unknown matches
      `errors.Is(err, ErrServiceTypeUnknown)` + empty string matches
      `errors.Is(err, ErrServiceTypeMissing)`).
- [x] write tests for `allowedFieldsFor` covering app / tool / infra allowlists.
- [x] run `go test ./internal/config/...` — must pass before next task.

> **Design note (do not "fix" later):** `ServiceConfig` stays a single flat struct with
> field-allowlist enforcement rather than a sum type (`AppService` / `ToolService` /
> `InfraService` + custom `UnmarshalYAML`). The flat shape keeps YAML decoding
> ergonomic and the allowlist gives strict validation with clear per-field error
> messages — exactly what we want here.
>
> **Design note:** `ServiceType` is a **named string type** (`type ServiceType string`),
> not an int-backed iota. YAML decodes scalar strings into named string types natively
> — `type: app` → `ServiceType("app")` works without any `UnmarshalYAML` hook.
> Constants are typed (`ServiceTypeApp ServiceType = "app"`) so the compiler catches
> accidental untyped-string assignments. We get methods (`IsApp`, `Validate`) for free
> and avoid the int-enum YAML round-trip problem.
>
> **Deferred (not this PR):** `ServiceConfig` itself stutters at the call site
> (`config.ServiceConfig`). The Go-idiomatic name is `config.Service`. Rename is
> deliberately deferred — it would touch hundreds of call sites and is independent of
> the type-discriminator refactor. Note for a follow-up plan.

### Task 2: Verify template dot-path resolution and `runtime.*` namespace handling

- [x] read `internal/tpl/render_command.go` `CompileVarSyntax` (l.87) and confirm whether
      `runtime.*` had any special-case rewriting (the explore report says it routes
      through `.Raw` by default, but verify before deleting). Confirmed: only `files`,
      `host`, `param`, `context` namespaces are special-cased; `runtime.*` and
      `services.*` both route through the default `resolve .Raw` path.
- [x] confirm `resolveMapPath` (l.206) walks nested maps without needing patches for
      `services.X.ports.Y` (two-level nested map). Confirmed: recursive walk over
      `map[string]any` handles arbitrary depth.
- [x] write a tpl-level test fixture that resolves `${services.foo.ports.http}` against
      a synthetic `Raw` map shaped per the new model — verifies plumbing works before we
      change the producer.
- [x] document any required tpl changes (expected: none) inline in the test as a comment;
      open a `⚠️` blocker entry here if it turns out tpl needs modification. No tpl
      changes required.
- [x] run `go test ./internal/tpl/...` — must pass before next task.

### Task 3: Rewrite `LoadServicesConfig` to strict-decode with type discriminator

- [x] in `internal/config/devbox.go`, replace `LoadServicesConfig` body with a
      strict-decode (`KnownFields(true)`) parser that:
      - requires `type:` on every entry;
      - per-type rejects disallowed fields via `allowedFieldsFor`;
      - parses `ports: map[string]int` and `hosts: map[string]string` (reject `int`
        scalar / `string` scalar / missing-map shapes with a clear error);
      - validates port values `1..65535` (today's loader had no explicit bound check);
      - resolves `extends:` only for `type: app` (reject for tool/infra);
      - rejects `extends` cycles and unknown parents.
- [x] **Aggregate diagnostics with `errors.Join`**: when validating field-allowlist for a
      single file, collect every per-service violation and return them joined — do NOT
      bail on first. Callers see all violations in one parse pass. Wrap the final
      joined error with file path context using `fmt.Errorf("loading %s: %w", path, err)`.
- [x] add sentinel errors: `ErrServiceTypeMissing`, `ErrServiceTypeUnknown`,
      `ErrServiceFieldNotAllowed`, `ErrServiceExtendsCrossType`, `ErrServicePortsShape`,
      `ErrServiceHostsShape`, `ErrServicePortOutOfRange`. All error message strings
      lowercase, no trailing punctuation, prefixed with `"config: "`.
- [x] **Extends inheritance: defensive copy.** When the resolver copies parent
      `Configs []ServiceConfigEntry`, `Dirs []string`, `Compose []string`, `Ports`,
      `Hosts` into a child, use `slices.Clone` / `maps.Clone` to avoid the child
      sharing the parent's backing array (later mutation of child would silently
      corrupt the parent). Same applies in reverse for `injectServicesIntoRaw` output.
- [x] write table-driven tests with `testdata/services/` fixtures covering each
      sentinel-error path plus one happy-path file per type.
- [x] write tests for extends inheritance (container, dirs, configs, render — only app)
      and confirm tool/infra extends is rejected before any merge.
- [x] write a test that mutates a child's inherited slice/map and confirms the parent
      is unaffected (proves the defensive copy).
- [x] write a test confirming multi-error aggregation: a file with three different
      violations across two services produces an error where each of the three
      sentinels matches via `errors.Is(err, sentinel)`. (Joined errors expose
      `Unwrap() []error`, not a single-error chain — `errors.Is` walks the
      multi-unwrap automatically, but bare `errors.Unwrap()` returns nil. Don't
      assert against `errors.Unwrap`; assert against `errors.Is`.)
- [x] run `go test ./internal/config/...` — must pass before next task.

### Task 4: Remove `ToolConfig`, `LoadToolsConfig`, `tools.yml`, and `runtime.ports/hosts`

**`LoadConfig` sequencing (locked).** Today's `LoadConfig` (`internal/config/devbox.go:817`)
loads tools.yml first, then merges the 3-layer raw YAML, then validates the tools
overlay, then resolves services. After unification the order must be:

1. **Load `devbox/services.yml`** (canonical service declarations with `type:`,
   strict-decoded per Task 3) — this gives us the authoritative set of declared service
   names and types.
2. **Validate each overlay layer** (`defaults.yml`, `local.yml`) against that set:
   `validateServicesOverlay` enforces that any `services.<name>` block (a) names a
   declared service and (b) carries only `enabled:` — never `ports`, `hosts`, `dir`,
   `compose`, etc. Per-layer source attribution in errors.
3. **Merge raw YAML** layers (devbox.yml → defaults.yml → local.yml).
4. **Resolve `Enabled`** per service. `services.yml` does NOT carry an `enabled:` field
   — `ServiceConfig.Enabled` is `yaml:"-"`, computed only. Resolution:
   - if `mandatory: true` → `Enabled = true` regardless of overlay;
   - else → look up `services.<name>.enabled` in the merged overlay map. If present
     and truthy, `Enabled = true`; otherwise `Enabled = false`.
   - if no overlay layer ever set `services.<name>.enabled`, the service is disabled
     by default. (Matches today's tool semantics. The project's `defaults.yml` is
     where the "project default" lives — `services.yml` only declares shape, never
     enablement.)
5. **Inject services into `Raw`** via the new `injectServicesIntoRaw` (mirrors every
   resolved field including nested `ports` / `hosts`).

Tools are no longer a separate load step. Step 1 covers them.

- [x] delete the complete tools surface from `internal/config/devbox.go`.
- [x] delete `Runtime.Ports` / `Runtime.Hosts` fields and their resolution path in
      `LoadConfig`. `RuntimeConfig` now `{ UseHTTPS, SPX }`.
- [x] reorder `LoadConfig` per the sequencing above (services.yml first → overlay
      validation → merge → resolve Enabled → inject).
- [x] introduce `validateServicesOverlay` mirroring the strict shape.
- [x] write a sequencing test (TestLoadConfig_overlaySequencing_unknownServiceRejected).
- [x] rewrite `injectServicesIntoRaw` to mirror ports/hosts with lazy-init and
      absent-as-omitted semantics.
- [x] strip `Tools` field from `DevboxConfig`; call sites migrated to walk
      `cfg.Services` with `IsTool()`.
- [x] write tests for `validateServicesOverlay`.
- [x] write tests for `injectServicesIntoRaw` (ports/hosts round-trip + dot-path
      resolution).
- [x] run `go test ./internal/config/...` — passes.

### Task 5: Update `internal/validate/config/` for unified schema

- [ ] in `internal/validate/config/devbox.go`, replace the services validator with a
      type-aware version that emits per-field diagnostics:
      - missing/unknown `type:` → error;
      - tool/infra with app-only fields → error (point at file:line if available);
      - app missing `dir:` → warning (allowed but unusual);
      - `extends:` on non-app → error;
      - `depends_on:` referencing a `type: tool` → error (`infra` and `app` allowed);
      - `ports` / `hosts` shape mismatch → error.
- [ ] delete or merge the tools validator (was: tool-overlay strictness check) into the
      unified services validator; keep file as a thin re-export if other packages import
      by name.
- [ ] add a compile-time interface check next to the new validator:
      `var _ validate.Validator = (*servicesValidator)(nil)` so future signature drift
      fails the build immediately.
- [ ] write tests covering each new diagnostic.
- [ ] run `go test ./internal/validate/...` — must pass before next task.

### Task 6: Enforce type semantics at runtime (loader + deploy + compose)

Validator diagnostics in Task 5 only run under `devbox validate`. Normal `devbox deploy`,
`devbox up`, `devbox compose …` paths must also reject illegal type combinations — they
do not run the validator. Add hard errors in the load + plan paths:

- [ ] **Loader (`LoadConfig`)**: after services parse, walk every service's `DependsOn`
      and return an error if any target resolves to a `type: tool` service. Sentinel
      `ErrDependsOnTool`. This is the canonical gate — every caller goes through
      `LoadConfig`, so the rule cannot be bypassed.
- [ ] **Loader: add `ValidateServiceDeployFiles(baseDir string, allServices
      map[string]ServiceConfig) error`** in `internal/config/`. Walks
      `devbox/deploy/*.yml`, maps each filename stem to a declared service, and
      returns an error if any deploy file's owner is not `type: app`. Sentinel
      `ErrDeployFileForNonApp`. **Call from `LoadConfig` once, after services parse**,
      so the check is independent of whatever subset later callers pass to
      `LoadServiceDeployConfigs`. (`LoadServiceDeployConfigs` itself keeps its current
      signature and only-walks-given-names behavior — the new function is the
      authoritative gate; `LoadServiceDeployConfigs` stays a runtime lookup helper.)
      Also reject deploy files whose stem matches no declared service at all (today
      these are silently skipped — same class of "silently wrong" the pre-release
      policy forbids).
- [ ] **`ResolveServicesPlan`** (`internal/deploy/service_plan.go:51`): filter the
      `enabled` map to `IsApp()` only. Tool/infra services with `Enabled: true` are
      legal at the runtime layer (they show up in compose), but they must never
      appear in the deploy enumeration. Today the loop is type-blind.
- [ ] **`ResolveServicePlan`** (`internal/deploy/service_plan.go:15`, the `--service`
      single-target path): return a typed error if the named service is not `app`.
      Sentinel `ErrDeployTargetNotApp`.
- [ ] **Topology / `TrackedServices`**: audit `internal/deploy/journal/`'s tracked-set
      logic and `config.TopoSortServices`. Both must filter to `IsApp()` if they
      currently iterate all services. Note any other deploy-side service enumeration
      site found during the audit as a `➕` task.
- [ ] **`ComposeFiles()` / `ComposeFilesAll()`** (`internal/config/devbox.go:339`):
      currently emits `base → tools (sorted) → services (sorted)`. After unification
      pin the new order **explicitly**: `base → tools (sorted by name) →
      infra (sorted by name) → apps (sorted by name)`. Group by type, then sort by
      name within each group. Reordering would silently break overlay precedence in
      live `next/tbm` and any user fixture; the order is part of the public surface.
      Implement as: iterate services partitioned by `IsTool()` / `IsInfra()` /
      `IsApp()` in that order, sorting each partition by name.
- [ ] write tests for each new sentinel + each new filter (table-driven; one fixture
      per illegal case).
- [ ] write a `ComposeFiles` ordering test with a mixed app/tool/infra service set
      that asserts the exact emitted order (covers both `ComposeFiles` and
      `ComposeFilesAll`).
- [ ] run `go test ./internal/config/... ./internal/deploy/...` — must pass before
      next task.

### Task 7: Adapt status renderers and command-layer toggle/enable/disable

**User-facing CLI shape (locked — no decisions deferred):**

**Mutating top-level command (interactive toggle + scriptable enable/disable):**
- `devbox services` — single unified multi-select toggle. Shows every optional
  service regardless of type, sorted by name, with a `Type` column visible in the
  table. Mandatory items pre-checked + disabled (same as today). No `--type` filter.
- `devbox services enable <name>` / `devbox services disable <name>` — by name only;
  the type is looked up internally. No `--type` flag. Errors stay as today
  (mandatory + disable rejects; mandatory + enable warns no-op).
- **`devbox tools` is dropped.** No alias, no shim. Pre-release policy applies —
  rename freely. Users learn one verb (`services`) instead of two.
- Completion: `enable` completes disabled-optional across all types; `disable`
  completes enabled-optional across all types. No per-type completion variants.

**Read-only `devbox status` group:**
- `devbox status` (default) — renders sections in order: `health → apps → tools →
  infra → deploy → topology → git → daemons`. Apps/tools/infra are three distinct
  sections, each with its own header and table. (No combined "services" section.)
- Per-section subcommands: `devbox status apps` / `devbox status tools` /
  `devbox status infra` / `devbox status deploy [svc]` / `devbox status topology` /
  `devbox status git` / `devbox status daemons`.
- **`devbox status services` is dropped** — replaced by the three per-type
  subcommands. Section flags follow the same rename: `--no-apps`, `--no-tools`,
  `--no-infra` (plus the existing `--no-deploy`, `--no-topology`, `--no-git`,
  `--no-daemons`). The old `--no-services` flag is gone. The old `--no-tools` flag
  keeps its name but now refers specifically to the type-tool section.

**Implementation steps:**

- [ ] in `internal/stack/status.go`, replace `RenderServices` / `RenderTools` with
      **three** type-keyed section renderers: `RenderApps`, `RenderTools`,
      `RenderInfra`. Each returns `(string, []error)`. The command layer composes
      them in the order locked above.
- [ ] update `BuildCustomColumns` / `RenderCustomCells` to accept `ServiceType`
      instead of the old `Kind` enum; only one underlying data shape now.
- [ ] in `internal/command/`:
      - merge `service.go` + `tool.go` toggle commands into a single
        `newServicesCmd` with a unified `runServicesToggle` that walks every
        optional service of any type.
      - **delete** `newToolCmd` / `runToolsToggle` and any `devbox tools` wiring at
        root. No alias.
      - merge `services.go` + `tools.go` row builders into one `buildServiceRows`
        (no type filter — returns all, the renderer slices by type).
      - completion helpers: collapse `completeDisabledOptional` /
        `completeEnabledOptional` / `completeToolDisabled` / `completeToolEnabled`
        into the first two. Both now span all service types.
      - `status` group: define the `Section` enum with `Apps`, `Tools`, `Infra`,
        `Deploy`, `Topology`, `Git`, `Daemons` (matches the locked order). Wire
        `--no-<section>` flags from the enum. Drop `--no-services`. The `Section`
        enum is the single source of truth for both the orchestrator order and
        the flag set (per the existing pattern in CLAUDE.md).
      - `status` subcommands: add `status apps` / `status tools` / `status infra`
        (each calls `loadStatusContext(flags)` then the corresponding renderer).
        Delete `status services`.
- [ ] in `internal/ui/`, collapse `RenderServiceTable` + `RenderToolTable` into one
      `RenderServicesTable(rows, extraCols, withDirCol bool)`. Apps pass
      `withDirCol=true`; tool/infra pass `false`.
- [ ] write tests for: unified toggle (mandatory short-circuit still applies across
      types; mixed app+tool+infra fixture); `enable`/`disable` across types by name;
      completion returns the union across types filtered by enabled-state;
      `RenderApps` / `RenderTools` / `RenderInfra` each produce the expected table;
      `--no-apps` / `--no-tools` / `--no-infra` suppress their sections in default
      `status` output; `devbox tools` is unknown (verify cobra emits the standard
      "unknown command" error to guard against accidental re-introduction).
- [ ] run `go test ./internal/command/... ./internal/stack/... ./internal/ui/...` —
      must pass before next task.

### Task 8: Migrate envfile, docker.yml, and Go-template references

This task covers **two distinct migration surfaces**:

1. **Dot-path templates** (the `${...}` style used in `info.yml` exports, `docker.yml`
   templates, `commands.yml` `default_from:`, etc.) — resolved by `tpl.Render` against
   `.Raw`.
2. **Go-template field access** (the `{{ .Foo.Bar }}` style used inside template-pack
   files under `internal/templates/`) — resolved against `TemplateData` structs.

Both must be migrated. The previous explore only enumerated dot-path hits; Go-template
hits are a distinct grep.

- [ ] grep the entire repo (including `next/tbm/`, `docs/`, every `*.tmpl`, every
      `*.yml`) for the dot-path forms `runtime.ports`, `runtime.hosts`. List all hits
      in this task's notes. Migrate each to `services.<name>.ports.<port-name>` /
      `services.<name>.hosts.<host-name>`.
- [ ] grep the entire repo for the **Go-template namespace forms**:
      `.Tools.`, `.Tools }}`, `.Tools[`, `.Runtime.Ports`, `.Runtime.Hosts`,
      `range .Tools`, `with .Tools`. These accessed the deleted `cfg.Tools` /
      `cfg.Runtime.Ports` / `cfg.Runtime.Hosts` fields in `TemplateData` and will fail
      template execution after Task 4 removes them.
- [ ] update `internal/templates/` `TemplateData` to expose the new shape
      (locked — no alternatives):
      - field `.Services` — `map[string]ServiceConfig` of every service (all types).
      - zero-arg methods `(d TemplateData).AppServices() / .ToolServices() /
        .InfraServices()` — each returns `map[string]ServiceConfig` filtered by type.
        Names deliberately do NOT collide with `.Tools` so the grep guard works.
        (Map iteration order in `{{ range }}` is documented as sorted-by-key by
        `text/template`, so callers get stable output without extra plumbing.)
      - helper methods on `ServiceConfig`: `Port(name string) int` and
        `Host(name string) string`. Both return the zero value if `name` is absent
        (matches map-index semantics). These exist so templates write
        `{{ (index .Services "X").Port "http" }}` instead of
        `{{ index (index .Services "X").Ports "http" }}`.
- [ ] migrate every Go-template file under `internal/templates/.../templates/` and
      every `next/tbm/.../*.tmpl`:
      - `{{ .Tools }}` / `{{ range .Tools }}` → `{{ .ToolServices }}` /
        `{{ range .ToolServices }}`.
      - `{{ .Runtime.Ports.X }}` → `{{ (index .Services "X").Port "<port-name>" }}`.
      - `{{ .Runtime.Hosts.X }}` → `{{ (index .Services "X").Host "<host-name>" }}`.
- [ ] update `internal/envfile/` export-rule resolution if it had `runtime.ports.*`
      special-cased; should route through generic `.Raw` resolution since
      `injectServicesIntoRaw` mirrors ports/hosts.
- [ ] migrate any `docker.yml` templates / `info.yml` exports / `commands.yml`
      `default_from:` references — for single-port services this requires picking
      a port name during fixture migration (Task 10).
- [ ] write tests for envfile generation against a fixture covering both single-port
      and multi-port services.
- [ ] write a template-rendering test that exercises the locked accessors
      `.AppServices` / `.ToolServices` / `.InfraServices` plus
      `(index .Services "X").Port "<name>"` / `.Host "<name>"` against a synthetic
      config — proves the new `TemplateData` surface works end-to-end.
- [ ] **Source-level regression check** (separate from the rendering test, runs in
      the same task): a Go test that walks the source-template tree
      (`internal/templates/.../templates/**`, `next/tbm/.../*.tmpl`,
      `docs/reference/render/**` examples) and asserts zero hits for `.Tools`,
      `.Runtime.Ports`, `.Runtime.Hosts` as substrings. This catches stale source
      that happens not to be exercised by any rendering fixture. Mirrors the global
      Task 13 acceptance grep, scoped to template-source paths. Apply the same path
      exclusions as Task 13: this plan file and everything under
      `docs/plans/completed/**` (recursive — the bare directory pathspec does not
      cover its contents).
- [ ] run `go test ./internal/envfile/... ./internal/templates/...` plus any
      docker/template tests — must pass before next task.

### Task 9: Update template packs (IDE / AI / git) for new service shape

- [ ] in `internal/templates/`, audit `SelectServices` and `TemplateData` to confirm
      they consume only the unified `ServiceConfig` (the explore report says they're
      already generic; verify against the changes in Task 8).
- [ ] update default-IDE-render-enabled-for-app logic to use the new
      `(ServiceConfig).IsApp()` predicate.
- [ ] verify pack manifest validators still apply uniformly (no type-specific gates).
- [ ] write tests for `SelectServices` with each type (apps render IDE by default;
      tool/infra do not unless explicit `render.<kind>.enabled: true`).
- [ ] run `go test ./internal/templates/...` — must pass before next task.

### Task 10: Migrate `next/tbm` fixture to new schema

- [ ] convert `next/tbm/devbox/tools.yml` entries into `services.yml` with
      `type: tool`. **Note**: today's tools use `compose: <single-file.yml>` (scalar
      string). New schema is `compose: [<file.yml>]` (list). Strict YAML decode will
      reject the scalar form — every migrated entry must use list syntax even for one
      file.
- [ ] add explicit port names to every entry (e.g., `ports: { web: 8080 }`); pick
      consistent names (`web` / `http` / `mysql` per common convention).
- [ ] delete `next/tbm/devbox/tools.yml`.
- [ ] update `next/tbm/devbox/defaults.yml` and `local.yml` overlays — only `enabled:`
      remains under `services.<name>` (no port/host overrides; if any existed, document
      and migrate to top-level overrides via a different mechanism — flag as `⚠️` if
      blocked).
- [ ] update any `next/tbm/devbox/docker.yml` / `info.yml` / `commands.yml` references
      from `runtime.ports.X` → `services.X.ports.<name>`.
- [ ] run `bin/devbox -c next/tbm validate` (after `make build`); must report zero
      errors. Capture output and include in PR description.
- [ ] run full `make test` — must pass before next task.

### Task 11: Update docs to match new schema

- [ ] rewrite `docs/reference/config/services.md` to describe the unified schema with a
      `type:` table, per-type field allowlist, and migration notes (since pre-release,
      "migration notes" = "this is the shape, period").
- [ ] **delete** `docs/reference/config/tools.md` outright. No redirect stub. Then
      `git grep -nF 'reference/config/tools.md' -- ':!docs/plans/2026-05-22-unified-services-schema.md' ':(exclude)docs/plans/completed/**'`
      must return zero hits; rewrite every surviving link to point at
      `docs/reference/config/services.md` (anchor the link at the specific
      `type:` subsection where appropriate — e.g.
      `services.md#service-types` or whatever heading slug the rewritten page
      ends up using). The same grep is added to the Task 13 acceptance check for
      regression coverage.
- [ ] grep `docs/` for `runtime.ports` and `runtime.hosts`; replace every reference with
      the new dot-path.
- [ ] update `docs/reference/config/conditions.md` if it had examples touching
      `runtime.ports`.
- [ ] update `docs/reference/config/commands.md` `default_from:` examples.
- [ ] regenerate CLI reference: `bin/devbox docs generate` (per CLAUDE.md).
- [ ] tests: docs aren't code, but verify any docs-snippet tests still pass.
- [ ] run `make test` — must pass before next task.

### Task 12: Update `AGENTS.md` (== `CLAUDE.md`) to reflect new model

- [ ] update the long `internal/config/` paragraph in `AGENTS.md` to describe:
      unified `ServiceConfig`, type discriminator, removal of `ToolConfig` /
      `LoadToolsConfig` / `validateToolsOverlay` / `runtime.ports` / `runtime.hosts`.
- [ ] update the binary-accessor pattern note (no change to accessors themselves).
- [ ] update `internal/stack/` paragraph to mention type-keyed status sections.
- [ ] update `internal/command/` paragraph for unified toggle / completion / status
      subcommands.
- [ ] update `Key Patterns` section if any pattern referenced tools-vs-services duality.
- [ ] **do not** edit `CLAUDE.md` directly — it is a symlink to `AGENTS.md`.

### Task 13: Verify acceptance criteria

- [ ] grep production code, fixtures, and docs for residual symbols and dot-paths —
      must be zero hits. **Exclude this plan file itself**
      (`docs/plans/2026-05-22-unified-services-schema.md`) and everything under
      `docs/plans/completed/` from the grep; both intentionally enumerate the old
      names. Concrete command (note the `/**` on the directory exclusion — a bare
      `:!docs/plans/completed/` pathspec matches only the directory entry, not its
      contents, so files inside would still be scanned):
      `git grep -nF -- '<term>' -- ':!docs/plans/2026-05-22-unified-services-schema.md' ':(exclude)docs/plans/completed/**'`.
      Terms:
      - Go symbols: `ToolConfig`, `ToolsConfig`, `LoadToolsConfig`,
        `validateToolsOverlay`, `injectToolsIntoRaw`, `cfg.Tools`, `c.Tools`,
        `.AnyEnabled`, `Runtime.Ports`, `Runtime.Hosts`. (Note `ToolConfig` is not
        a substring of `ToolsConfig` under `git grep -F`, so both terms are needed
        explicitly. `c.Tools` catches the `composeFiles` receiver pattern in
        addition to `cfg.Tools`.)
      - Dot-path templates: `runtime.ports`, `runtime.hosts`, `tools.<` (any tools.
        prefix in templates is suspicious now).
      - Go-template field access: `.Tools`, `.Runtime.Ports`, `.Runtime.Hosts`.
      - Docs link paths: `reference/config/tools.md` (the deleted page from Task 11).
- [ ] verify `bin/devbox validate` on `next/tbm` reports zero diagnostics.
- [ ] run full `make test` and `make lint`; all must be green.
- [ ] manual TTY check (per the locked CLI shape in Task 7): `devbox services`
      unified toggle, `devbox status` default (renders Apps / Tools / Infra
      sections), `devbox status apps`, `devbox status tools`, `devbox status infra`
      — confirm renders correctly. Also verify `devbox tools` errors as
      "unknown command" (the rename guard). This is the only checkbox that requires
      interactive verification; if running headless, mark as
      `[ ] (manual, deferred to Post-Completion)` and move the check to that section.
- [ ] confirm test coverage for new code in `internal/config/` ≥ 80% (per project standard).

### Task 14: [Final] Update plan and supporting docs

- [ ] mark all checkboxes `[x]` in this plan.
- [ ] confirm no `⚠️` entries are unresolved.
- [ ] note any newly-discovered `➕` tasks added during implementation and confirm they're
      checked off too.

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`.*

## Technical Details

**New `ServiceConfig` shape:**

```go
type ServiceType string // named string; YAML decodes scalars natively

const (
    ServiceTypeApp   ServiceType = "app"
    ServiceTypeTool  ServiceType = "tool"
    ServiceTypeInfra ServiceType = "infra"
)

type ServiceConfig struct {
    Type      ServiceType            `yaml:"type"` // validated at load
    Container string                 `yaml:"container,omitempty"`
    Mandatory bool                   `yaml:"mandatory,omitempty"`
    Enabled   bool                   `yaml:"-"` // resolved from overlay
    Compose   []string               `yaml:"compose,omitempty"`
    Ports     map[string]int         `yaml:"ports,omitempty"`
    Hosts     map[string]string      `yaml:"hosts,omitempty"`
    DependsOn []string               `yaml:"depends_on,omitempty"`
    Status    []StatusColumn         `yaml:"status,omitempty"`

    // app-only (validator rejects these for tool/infra)
    Dir             string                 `yaml:"dir,omitempty"`
    DirInternal     string                 `yaml:"dir_internal,omitempty"`
    WorkDirInternal string                 `yaml:"work_dir_internal,omitempty"`
    Configs         []ServiceConfigEntry   `yaml:"configs,omitempty"`
    Dirs            []string               `yaml:"dirs,omitempty"`
    Extends         string                 `yaml:"extends,omitempty"`
    CLI             ServiceCLIConfig       `yaml:"cli,omitempty"`
    Render          ServiceRenderConfig    `yaml:"render,omitempty"`
}
```

**Type-discriminator field allowlist (final):**

| field          | app | tool | infra |
|----------------|:---:|:----:|:-----:|
| type           |  ✓  |  ✓   |   ✓   |
| container      |  ✓  |  ✓   |   ✓   |
| mandatory      |  ✓  |  ✓   |   ✓   |
| compose        |  ✓  |  ✓   |   ✓   |
| ports          |  ✓  |  ✓   |   ✓   |
| hosts          |  ✓  |  ✓   |   ✓   |
| depends_on     |  ✓  |  —   |   ✓   |
| status         |  ✓  |  ✓   |   ✓   |
| dir            |  ✓  |  —   |   —   |
| dir_internal   |  ✓  |  —   |   —   |
| work_dir_internal | ✓ |  —   |   —   |
| configs        |  ✓  |  —   |   —   |
| dirs           |  ✓  |  —   |   —   |
| extends        |  ✓  |  —   |   —   |
| cli            |  ✓  |  —   |   —   |
| render         |  ✓  |  —   |   —   |

Note: `depends_on:` targets pointing at a `type: tool` service are **rejected at load
time** (Task 6 loader gate — `ErrDependsOnTool`), not only at validate time.
`depends_on: [<app>]` and `depends_on: [<infra>]` are both allowed for any service type
that has a `depends_on` field at all (i.e. `app` and `infra`).

**Template dot-path migration (`${...}` syntax, resolved against `Raw`):**

| old                          | new                                  |
|------------------------------|--------------------------------------|
| `${runtime.ports.app}`       | `${services.app.ports.<port-name>}`  |
| `${runtime.ports.adminer}`   | `${services.adminer.ports.<port-name>}` |
| `${runtime.hosts.main}`      | `${services.main.hosts.<host-name>}` |
| `${tools.<n>.enabled}`       | `${services.<n>.enabled}` (still works; `tools.*` namespace gone) |

**Go-template field migration (`{{ ... }}` syntax, resolved against `TemplateData`):**

| old                       | new (locked)                                                    |
|---------------------------|-----------------------------------------------------------------|
| `{{ .Tools }}`            | `{{ .ToolServices }}` (zero-arg method on `TemplateData`)       |
| `{{ range .Tools }}`      | `{{ range .ToolServices }}`                                     |
| `{{ .Runtime.Ports.X }}`  | `{{ (index .Services "X").Port "<port-name>" }}` (helper method on `ServiceConfig`) |
| `{{ .Runtime.Hosts.X }}`  | `{{ (index .Services "X").Host "<host-name>" }}`                |

Accessor methods on `TemplateData`: `AppServices()`, `ToolServices()`, `InfraServices()`
— all return `map[string]ServiceConfig` filtered by type. Names deliberately do NOT
shadow `.Tools` so the acceptance grep can require zero residual `.Tools` hits.
Helper methods on `ServiceConfig`: `Port(name string) int`, `Host(name string) string`
— zero-value if name is absent (matches map-index semantics).

The exact port/host names are user-chosen per service during Task 10 fixture migration.
Single-port services need a chosen name (recommendation: `http` for web, `tcp` for raw
TCP, role-specific like `mysql` / `postgres` / `amqp` when obvious).

**Go conventions applied (audit checkpoints during review):**

- **Naming**: type discriminator is `type ServiceType string` (named string type, not
  int-backed enum, not an alias). Typed constants: `ServiceTypeApp ServiceType = "app"`,
  `ServiceTypeTool`, `ServiceTypeInfra`. Boolean predicates use `Is` prefix on both
  `ServiceType` (`(t ServiceType).IsApp()`) and `ServiceConfig` (`(s ServiceConfig).IsApp()`
  forwarders). Sentinel errors use `Err` prefix (`ErrServiceTypeMissing`,
  `ErrServiceTypeUnknown`, `ErrDependsOnTool`, `ErrDeployFileForNonApp`,
  `ErrDeployTargetNotApp`, etc.). Helpers unexported (`allowedFieldsFor`,
  `servicesValidator`, `validServiceTypes`).
- **Error strings**: lowercase, no trailing punctuation, prefixed with `"config: "`
  (e.g., `"config: unknown service type %q"`, `"config: field %q not allowed for type
  %s"`). Sentinel `errors.New` strings also include the prefix.
- **Error aggregation**: per-file validation uses `errors.Join` to surface every
  violation in one pass; per-call-site wrapping uses `fmt.Errorf("%s: %w", ctx, err)`.
- **Single handling rule**: validators emit `Diagnostic`s; loaders return errors.
  Neither logs-and-returns.
- **Defensive copies**: `extends` inheritance and any other parent→child field copy
  uses `slices.Clone` / `maps.Clone` — never shares backing storage. Loader does NOT
  return its internal `cfg.Services` map directly to mutating callers; if a caller
  needs to mutate, it copies first.
- **Nil safety**: `injectServicesIntoRaw` lazy-inits every intermediate map level
  before writing. `Ports`/`Hosts` on `ServiceConfig` are accessed via direct map
  indexing (zero value for missing key is fine) — no nil-deref site introduced.
- **Compile-time checks**: `var _ validate.Validator = (*servicesValidator)(nil)`
  next to the new validator.
- **Receiver consistency**: pick value-vs-pointer once for `ServiceConfig` methods
  (`IsApp` etc.) and apply uniformly. Existing `ServiceConfig` methods determine the
  choice — match them.
- **Bounds checks**: port values validated as `1..65535` at load time; the field
  stays `int` since 65535 fits comfortably.

**Overlay shape (defaults.yml / local.yml):**

```yaml
services:
  adminer:
    enabled: true   # ONLY field allowed under services.<name> in overlay
```

Any other key under `services.<name>` in an overlay is a strict-mode error with per-layer
source attribution (matches today's `validateToolsOverlay` behavior, generalized).

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only.*

**Manual verification:**
- TTY: run `bin/devbox -c next/tbm services` (multi-select toggle); confirm mandatory
  apps appear pre-checked + disabled.
- TTY: run `bin/devbox -c next/tbm status` and confirm Apps/Tools/Infra sections render
  with correct columns and custom status cells.
- Run `bin/devbox -c next/tbm commands` and confirm cmdbrowser still works (no service
  identity coupling beyond what changed).
- Exercise a deploy step that references a port (e.g., a `commands.yml` with a
  `default_from: services.X.ports.Y`) and confirm rendering succeeds.

**External system updates** (if applicable):
- None — devbox is pre-release with no external consumers (per CLAUDE.md project status).

**Performance / regressions to watch:**
- `LoadConfig` adds one strict-decode pass per service file; expect no measurable change
  on projects with O(10) services.
- `injectServicesIntoRaw` writes more keys (ports/hosts nested maps). Negligible — same
  asymptotic shape as today's tools/services injection combined.
