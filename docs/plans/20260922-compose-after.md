# `service.yml` `compose_after:` — overlays emitted after every service group

## Overview

One branch (`feat/compose-after`, cut from `feat/otel-guide`), one PR.

**The problem.** `composeFiles` (`internal/core/project/config/workspace.go:698`)
emits the `-f` chain as `compose.base → tools → infra → apps` (alphabetical by
service name inside each group; each service's own `compose:` files followed by its
per-developer `services.<name>.compose.extra`), then the generated bridge overlay
(`.dwe/compose.bridge.yml`), then the project-wide `local.yml` `compose.extra`.
A `type: tool` service whose overlay patches an app service therefore loses every
whole-value merge — `command:`, `healthcheck:`, any `environment:` key both files
set — whenever that app is defined in its own `compose/<app>.yml` overlay, because
the app group comes later and wins. The OpenTelemetry guide
(`docs/guides/observability-otel.md:123`) documents this as a trap and works around
it with an "add-only" patch and a `register.mjs` that deletes `SENTRY_DSN` at
runtime (`:291`), because the overlay cannot override it.

**The change.** A new `service.yml` field:

```yaml
# workspace/services/otel/service.yml
type: tool
compose:
  - compose/otel.yml          # the backend — emitted in the tool group as before
compose_after:
  - compose/otel-apps.yml     # the patch — emitted after every app overlay
```

`compose_after:` files are emitted **after all three groups** (tool → infra →
app, per-service `local.yml` extras included) and **before** the generated bridge
overlay and the project-wide `local.yml` `compose.extra`. They are emitted only
while the owning service is enabled — the same `all || svc.Enabled` gate as
`compose:` — ordered by service name, with the author's list order preserved
within a service.

New chain:

```
compose.base
  → tools  (alpha) — each: svc.compose… + svc.local-extras…
  → infra  (alpha) — each: svc.compose… + svc.local-extras…
  → apps   (alpha) — each: svc.compose… + svc.local-extras…
  → compose_after  (alpha by service name, any type) — each: svc.compose_after…
  → .dwe/compose.bridge.yml   (when present)
  → compose.extra…            (project-wide local.yml, always last)
```

User-observable → `CHANGELOG.md` `## [Unreleased]` `### Added`. No existing
project changes behaviour: a project that does not declare the field produces a
byte-identical chain and byte-identical deployment hashes (see Task 3).

### Non-goals

Decided during design; do not re-litigate during implementation:

- **No per-entry gate on another service being enabled.** The disabled-app trap
  (a patch block with no `image:`/`build:` for an app that is currently disabled
  makes Compose reject the whole project) is NOT solved here. Reasons:
  - It changes the field's contract from "a function of the owning service's
    state" to "a function of two services' states". Every consumer that reasons
    about the chain from one service — `dwe services enable|disable` plans,
    `envtest.ScenarioView` toggles, the `tests` validator's per-scenario chain,
    `--all` — would have to learn the second dependency.
  - The real-world overlay patches *several* apps in one file (the otel guide
    patches every instrumented app), so a per-file gate on one service does not
    fit; the author would have to split the patch per app first — at which
    point a gate is a second feature with its own schema (`{file:, when_enabled:}`),
    not a flag on this one.
  - The shape is forward-compatible: `compose_after` is `[]string` now, and a
    future mapping form can be added additively via a string-or-struct
    `UnmarshalYAML`, exactly as `ServiceConfigEntry` accepts `- .env` and
    `- file: .env` (`workspace.go:~785`).
  - The guide keeps the caveat ("patch only services that exist whenever the tool
    is on"), reworded so it is clear that `compose_after` fixes the **order**, not
    the **presence**, of the patched service.
- **No `compose_after` in overlay layers.** `services.<name>.compose_after` in
  `defaults.yml` / `workspace.yml` / `local.yml` is rejected, exactly like a
  structural `compose:` list is today: `OverlayAllowedKeys`
  (`workspace.go:2022`) only admits `enabled`/`ports`/`hosts`, and the
  local-only `services.<name>.compose` escape hatch is `compose.extra`, not the
  service's own list. There is no per-developer `compose_after.extra`: a
  developer who needs the last word already has the project-wide
  `compose.extra`, which stays strictly last. No code change is needed for this —
  only a test pinning the rejection (Task 2).
- **No new file-existence / path-safety validator.** Verified: `compose:` entries
  in `service.yml` have **no** existence, containment or absolute-path check
  anywhere — neither in the loader nor in `internal/core/validate/` (the only
  `os.Stat` compose path checks are `validateLocalComposeExtraPaths`,
  `workspace.go:~2850`, for the per-developer `local.yml` overlays). A missing
  `-f` file is reported by Docker Compose itself on the first compose call.
  `compose_after:` is git-tracked service definition with the same trust as
  `compose:` and gets the same (absent) treatment. Adding an existence check for
  both lists is a separate, orthogonal change (see Post-Completion).
- **No type restriction.** Allowed for `app`, `infra` and `tool`. Emission is
  type-agnostic by construction; the use case is not tool-only (an `infra`
  reverse proxy or cache patching an app, an `app` admin SPA patching a sibling
  app that sorts after it); and `compose:` itself is allowed for every type.
  Nothing in the loader special-cases compose lists by type.
- **No type grouping inside the `compose_after` tier.** One pass over service
  names sorted alphabetically, all types together. The tool → infra → app
  grouping exists to let apps win over the tools layered under them; files in
  this tier are by definition patches over the whole stack, and two
  `compose_after` files patching the same key is an authoring conflict either
  way. (Open question recorded at the end in case review prefers grouping.)
- **No deduplication.** A path listed in both `compose:` and `compose_after:`, or
  by two services, appears twice — the same policy `workspace.md` documents for
  `compose.extra` ("Docker Compose tolerates duplicates").
- **No change to the bridge overlay generator** (`internal/core/bridge/composegen.go`
  reads no compose file list) **nor to the prompt hot path** (`shared/prompt`
  only resolves the compose project name, never the file list).
- **No cross-check test between `allowedFieldsFor` and `servicesAllowedFields`.**
  Both are unexported in different packages; a real drift test needs exporting
  one of them. Out of scope — this plan updates both tables and adds a test on
  each side, the same bookkeeping every previous field did.

## Context (from discovery)

Verified on `feat/compose-after` @ `879f058f`.

**Emission**

- `ServiceConfig.Compose []string \`yaml:"compose"\`` — `workspace.go:1165`.
  `LocalComposeExtra` (`yaml:"-"`, local.yml-injected) sits right after it.
- `composeFiles(all bool)` — `workspace.go:698-742`; `emitGroup` closure
  (`:708-724`) iterates `slices.Sorted(maps.Keys(c.Services))`, gate
  `all || svc.Enabled`, emits `svc.Compose...` then `svc.LocalComposeExtra...`;
  bridge overlay `:733-735` (`bridgeOverlayExists`, stat at call time); project
  `c.Compose.Extra` `:737-739`. Doc comments on `ComposeFiles` (`:670-682`) and
  `ComposeFilesAll` (`:684-692`) describe the order and must be updated.

**Every consumer of the chain** (all go through `ComposeFiles()` /
`ComposeFilesAll()`, so they pick the new files up with no change):

- `internal/shared/docker/compose.go:60,66` — `NewCompose` / `NewComposeAll`
  (every `dwe run`/`stop`/`docker …` invocation; `--all` for
  `dwe docker pull|build --all` via `internal/cli/docker/docker.go:263,300`).
- `internal/cli/compose/compose.go:70` (`dwe compose` passthrough) and `:154`
  (`dwe compose files`).
- `internal/core/usercommands/runtime/spec/runner.go:152` — `RunContext.Compose()`
  (service commands, daemons via `execution/builtin/containers`).
- `internal/core/project/stack/topology.go:262` — `ResolveTopology`.
- `internal/core/project/config/compose_scan.go:272` — `parseComposeFiles`,
  shared by `ScanComposeIsolation` and `ScanComposeCost`.
- `internal/core/validate/config/compose_name.go:58` — top-level `name:` check.
- `internal/core/validate/tests/tests.go:154` — per-scenario chain via
  `envtest.ScenarioView(cfg, scn.Env).ComposeFiles()`.

**Per-service readers of `svc.Compose`** (the ones that need an explicit sibling):

- `ResolveServiceExtends` — `workspace.go:2514-2519` clones `parent.Compose` /
  `parent.LocalComposeExtra` when the child has none.
- `injectServicesIntoRaw` — `workspace.go:2989-2995` mirrors `compose` into
  `Raw["services"][name]` (documented in `services/index.md` § Load behavior as
  "each resolved service is injected").
- `journal.serviceConfigToMap` — `internal/core/workflow/deploy/journal/hash.go:257`
  hashes `"compose": svc.Compose` into `ServiceConfigHash` / `ProjectConfigHash`.

**Allowlists** (the `AGENTS.md` "service.yml allowlists" trap)

- Loader: `allowedFieldsFor` — `workspace.go:1097`, `common` slice at `:1099-1103`;
  used by the raw first pass at `:2333`. Test: `TestAllowedFieldsFor`,
  `service_type_test.go:92` (per-type `mustHave`/`mustLack` table).
- Validator mirror: `servicesAllowedFields` —
  `internal/core/validate/config/workspace.go:149-170` (three per-type maps),
  consumed at `:284`.
- Overlay layers: `OverlayAllowedKeys` — `workspace.go:2022`; the
  `services.<name>.compose` special case at `:2061-2067`.

**Not affected (checked)**

- `services-folder` validator's `knownServiceFiles`
  (`validate/config/services_folder.go:13`) lists *file names* in a service
  folder; `compose_after` is a field in `service.yml`, not a file there.
- `envtest.CopyTree` (`envtest/copy.go:47`) copies the git-listed tree; it has no
  notion of compose lists, so `compose_after` files are copied like any tracked
  file.
- `envtest.ScenarioView` (`envtest/scenarioview.go`) strips only per-developer
  overlays (`Compose.Extra`, `LocalComposeExtra`). `compose_after` is
  git-tracked and must **survive** the view; the view's `maps.Clone` of
  `Services` is shallow, which is fine because nothing mutates the slice.
- No JSON schema, scaffold template or `llms-txt` section enumerates
  `service.yml` fields or compose lists.

**Existing order tests to extend**

- `TestComposeFiles_grouped_tool_infra_app` — `type_gates_test.go:53` (single
  fixture, asserts `ComposeFiles()` and `ComposeFilesAll()`).
- `TestComposeFiles_bridgeOverlayChainPosition` — `bridge_overlay_test.go:23`.
- `TestComposeFiles_LocalOverlays_GoldenFullPipeline` — `workspace_test.go:4367`.
- `TestComposeFilesAll_*` — `workspace_test.go:3736-4101`.
- `internal/cli/compose/compose_test.go:37-151` — `TestBuildComposeFileList_*`.

**Docs that state the order** (grep `tools → infra`):
`docs/reference/config/services/index.md:37`,
`docs/reference/config/workspace.md:452-458` (the "Final emission order" block,
which also omits the bridge overlay today),
`docs/reference/concepts/architecture.md:96`,
`docs/guides/observability-otel.md:123`, `docs/internals/packages.md:82`, and
their RU mirrors under `docs/i18n/ru/`.

## Development Approach

- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change (`make embedded-docs` once, then focused
  `go test ./internal/...`; `make test` before each commit)
- maintain backward compatibility: a project without `compose_after:` must get a
  byte-identical `-f` chain and unchanged journal hashes
- follow the `AGENTS.md` critical patterns — here chiefly "`info.yml` auto-blocks
  + `service.yml` allowlists" (both tables, nothing cross-checks them) and
  "Integration-test scenarios / `dwe test` isolation" (`ScenarioView` must not
  mutate its input)

## Testing Strategy

- **unit tests**: required for every task; the chain is a pure function of
  `DweConfig`, so order tests build `DweConfig` literals and compare whole
  slices (`slices.Equal`), never "contains"
- **loader tests**: through `LoadConfig` on a `t.TempDir()` project for the
  strict-decode, allowlist, overlay-rejection and extends paths
- **no e2e docker tests**; live verification on a real project is in
  Post-Completion

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

1. **Field.** `ComposeAfter []string \`yaml:"compose_after"\`` on `ServiceConfig`,
   directly after `LocalComposeExtra` (so the two compose lists and the
   local-only one read as one block), with a doc comment naming its chain
   position and gate.
2. **Allowlists.** `"compose_after"` joins the `common` slice of
   `allowedFieldsFor` and all three maps of `servicesAllowedFields`.
3. **Emission.** After the three `emitGroup` calls and before the bridge
   overlay, one loop over `slices.Sorted(maps.Keys(c.Services))` appends
   `svc.ComposeAfter...` under `all || svc.Enabled`. No type filter.
4. **Extends.** `ResolveServiceExtends` inherits `ComposeAfter` independently of
   `Compose`, with the same rule: cloned from the parent when the child declares
   none; the child's own list replaces, never merges. Reason: a child that
   inherits the parent's overlays but silently loses the parent's post-app patch
   would run a half-configured stack; field-by-field inheritance is how every
   other inherited field behaves.
5. **Raw mirror.** `injectServicesIntoRaw` writes `compose_after` next to
   `compose` when non-empty, so `services.<name>.compose_after` resolves in
   dot-paths exactly as `services.<name>.compose` does and the "each resolved
   service is injected" contract in the docs stays true.
6. **Journal hash.** `serviceConfigToMap` adds `"compose_after"` **only when
   non-empty** (the `configs`/`dirs` pattern at `hash.go:260-275`), never as an
   always-present key: an always-present `nil` entry would change the canonical
   bytes of every existing service and make every deployed project look
   config-changed after upgrade. Hashed at all because `compose` is — an edit to
   the patch list changes what the stack runs.
7. **`--all`.** `ComposeFilesAll()` includes every service's `compose_after`
   (the `all ||` half of the shared gate). Under `--all` every app overlay is in
   the chain too, so the disabled-app trap cannot arise there; this is the
   consistent reading of "all configured overlays" that `dwe docker pull|build
   --all` already relies on.

## Technical Details

### Emission (`workspace.go`, inside `composeFiles`)

```go
emitGroup(func(t ServiceType) bool { return t == ServiceTypeApp || t == "" })

// compose_after: patches that must win over every service group — a tool
// overlay adjusting an app defined in its own overlay. Same enabled gate as
// compose:, one pass by service name across all types, list order kept.
for _, name := range slices.Sorted(maps.Keys(c.Services)) {
	svc := c.Services[name]
	if (all || svc.Enabled) && len(svc.ComposeAfter) > 0 {
		files = append(files, svc.ComposeAfter...)
	}
}

// Generated host-bridge overlay: ...
```

The sentence in the existing comment "Order is part of the public surface —
overlay precedence depends on it (pinned by TestComposeFiles_grouped_tool_infra_app)"
is extended to name the new tier and its pinning test.

### Chain position relative to per-developer overlays

A service's per-developer `services.<name>.compose.extra` is emitted inside its
group, so it comes **before** every `compose_after` file: a tool's
`compose_after` patch can override an app's per-service local extra. That is the
intended precedence (the patch runs "over the stack"), and the developer's
last-word channel — project-wide `compose.extra` — still follows everything.
This is stated in `workspace.md` § Compose overlays and in `fields.md`, because a
developer patching an app locally and wondering why the otel patch wins needs to
know to use the project-wide layer.

### Overlay-layer rejection

No code: `validateServicesOverlay` already rejects every key outside
`OverlayAllowedKeys`, with the message "service definitions belong in
workspace/services/<name>/service.yml; overlays may only set …". Task 2 pins it
for `compose_after` in both a shared layer (`defaults.yml`) and `local.yml`, so a
future relaxation of the local-only `compose` special case cannot silently admit
it.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): tasks achievable within this
  codebase — code changes, tests, documentation updates
- **Post-Completion** (no checkboxes): items requiring external action — live
  verification on a real project, skill copy sync

## Implementation Steps

### Task 1: the field, both allowlists, strict-decode coverage

**Files:**
- Modify: `internal/core/project/config/workspace.go` (`ServiceConfig` :1165-1172, `allowedFieldsFor` :1099-1103)
- Modify: `internal/core/validate/config/workspace.go` (`servicesAllowedFields` :149-170)
- Modify: `internal/core/project/config/service_type_test.go` (`TestAllowedFieldsFor` :92)
- Modify: the `servicesValidator` tests in `internal/core/validate/config/` (the file that exercises `servicesAllowedFields`)

- [ ] add `ComposeAfter []string \`yaml:"compose_after"\`` after `LocalComposeExtra`, with a doc comment: emitted by `composeFiles` after every service group and before the bridge overlay, same `all || svc.Enabled` gate as `Compose`
- [ ] add `"compose_after"` to the `common` slice of `allowedFieldsFor`
- [ ] add `"compose_after": true` to all three maps in `servicesAllowedFields` (app, infra, tool)
- [ ] extend `TestAllowedFieldsFor`: add `compose_after` to `commonFields` so every type must have it and the unknown type must lack it
- [ ] write a validator-side test: a `service.yml` of each type declaring `compose_after:` produces no "field not allowed" diagnostic (this is the side the loader test cannot see)
- [ ] write loader tests through `LoadConfig` on a temp project: `compose_after: [a.yml, b.yml]` decodes in list order for `app`, `infra` and `tool`; a scalar `compose_after: a.yml` is a load error (strict decode into `[]string`); the typo `compose_afer:` is rejected with `ErrServiceFieldNotAllowed`
- [ ] run `go test ./internal/core/project/config/... ./internal/core/validate/config/...` - must pass before task 2

### Task 2: emission, `--all`, extends, overlay-layer rejection

**Files:**
- Modify: `internal/core/project/config/workspace.go` (`composeFiles` :698-742 + doc comments :670-692, `ResolveServiceExtends` :2514-2519)
- Modify: `internal/core/project/config/type_gates_test.go` (`TestComposeFiles_grouped_tool_infra_app` :53)
- Modify: `internal/core/project/config/bridge_overlay_test.go` (`TestComposeFiles_bridgeOverlayChainPosition` :23)
- Modify: `internal/core/project/config/workspace_test.go` (`TestComposeFiles_LocalOverlays_GoldenFullPipeline` :4367, `TestComposeFilesAll_*`, extends tests)
- Modify: `internal/cli/compose/compose_test.go` (`dwe compose files`)

- [ ] emit `svc.ComposeAfter` in `composeFiles` as in Technical Details: after the app group, before the bridge overlay, one sorted-by-name pass over all types, gate `all || svc.Enabled`
- [ ] update the `ComposeFiles` / `ComposeFilesAll` / `composeFiles` doc comments to describe the new tier and its position
- [ ] inherit `ComposeAfter` in `ResolveServiceExtends` next to `Compose` (clone from parent when the child's list is empty; child's own list replaces)
- [ ] convert `TestComposeFiles_grouped_tool_infra_app` into a table (existing fixture as the first row, expectations unchanged) and add a `compose_after` row: a tool (`otel`) with `compose: [tool-otel.yml]` and `compose_after: [after-otel-1.yml, after-otel-2.yml]`, an infra and an app each with a `compose_after` entry — pin the exact full slice: groups as before, then the `compose_after` files ordered by service name across types with the tool's two files in list order, for both `ComposeFiles()` and `ComposeFilesAll()`
- [ ] add rows: owner disabled → its `compose_after` absent from `ComposeFiles()` but present in `ComposeFilesAll()` at the same position; required owner (`Required: true, Enabled: true`) → present; a project with no `compose_after` anywhere → chain byte-identical to the pre-change expectation (reuse the first row)
- [ ] extend `TestComposeFiles_bridgeOverlayChainPosition` with a `compose_after` entry: it lands after the app's `compose.extra` and before `BridgeOverlayRelPath`, which stays before the project-wide `compose.local.yml`
- [ ] extend `TestComposeFiles_LocalOverlays_GoldenFullPipeline` with a `compose_after` on the disabled `redis` and on an enabled tool: active chain has only the tool's, after `compose/apps/web.yml` and before the project-wide extras; the all-chain has both
- [ ] write extends tests via `LoadConfig`: child without `compose_after` inherits the parent's (and mutating the child's slice does not affect the parent's); child with its own list keeps only its own; child declaring `compose:` but not `compose_after:` still inherits the parent's `compose_after` (independent fields)
- [ ] write overlay-rejection tests via `LoadConfig`: `services.<name>.compose_after` in `workspace/defaults.yml` and in `workspace/local.yml` are load errors naming the layer file and `services.<name>.compose_after`
- [ ] add a `dwe compose files` test in `internal/cli/compose/compose_test.go` on a temp project with a tool `compose_after`: the printed lines equal `cfg.ComposeFiles()` with the `compose_after` file after the app overlay
- [ ] run `go test ./internal/core/project/config/... ./internal/cli/compose/...` - must pass before task 3

### Task 3: raw mirror and journal hash

**Files:**
- Modify: `internal/core/project/config/workspace.go` (`injectServicesIntoRaw` :2989-2995)
- Modify: `internal/core/workflow/deploy/journal/hash.go` (`serviceConfigToMap` :247-275)
- Modify: `internal/core/workflow/deploy/journal/hash_test.go`
- Modify: the `injectServicesIntoRaw` tests in `internal/core/project/config/`

- [ ] `injectServicesIntoRaw`: write `entry["compose_after"]` as `[]any` when non-empty, omitted when empty (same shape and omit rule as `compose`)
- [ ] `serviceConfigToMap`: add `m["compose_after"] = svc.ComposeAfter` only when `len > 0`
- [ ] write a hash test: `ServiceConfigHash` of a service without `compose_after` equals the hash computed from the map **without** a `compose_after` key (assert `serviceConfigToMap` has no such key — this is the "no redeploy on upgrade" guarantee), and adding or reordering a `compose_after` entry changes the hash
- [ ] write a raw-injection test: `services.<name>.compose_after` resolves to the list after `LoadConfig`; a service without the field has no key (update any test pinning the injected key set)
- [ ] run `go test ./internal/core/workflow/deploy/journal/... ./internal/core/project/config/...` - must pass before task 4

### Task 4: the scanners and `dwe test` see the new files

No production code is expected here — every consumer goes through
`ComposeFiles()`. This task pins it, so a future consumer that walks
`svc.Compose` directly is caught.

**Files:**
- Modify: `internal/core/project/config/compose_scan_test.go`
- Modify: `internal/core/workflow/envtest/scenarioview_test.go`
- Modify: `internal/core/validate/tests/` tests (per-scenario chain)

- [ ] `ScanComposeIsolation`: a `container_name:` declared only in an enabled service's `compose_after` file is reported with that file as `File`; a `compose_after` file resetting a base `container_name` (the existing `!reset` / last-wins fixtures) clears the finding — the tier is last in the chain, so it wins the collapse
- [ ] `ScanComposeCost`: an `image:` override in a `compose_after` file wins over the app overlay's `image:` (mirror `TestScanComposeCost_OverlayWins`)
- [ ] `ScenarioView`: `compose_after` survives the view (not stripped like `LocalComposeExtra`); a scenario `env.services.disable` of the owner drops its `compose_after` from `view.ComposeFiles()`, `enable` adds it; extend `TestScenarioView_DoesNotMutateInput` with a `compose_after` slice
- [ ] `validate/tests`: an interpolated-port finding in a `compose_after` file of a service the scenario disables is not attributed to that scenario's chain (mirror the existing disabled-service `compose:` case)
- [ ] run `go test ./internal/core/project/config/... ./internal/core/workflow/envtest/... ./internal/core/validate/tests/...` - must pass before task 5

### Task 5: reference docs, RU mirrors, CHANGELOG

**Files:**
- Modify: `docs/reference/config/services/fields.md` (+ `docs/i18n/ru/reference/config/services/fields.md`)
- Modify: `docs/reference/config/services/index.md` (+ RU)
- Modify: `docs/reference/config/services/extends.md` (+ RU)
- Modify: `docs/reference/config/workspace.md` (+ RU)
- Modify: `docs/reference/concepts/architecture.md` (+ RU)
- Modify: `CHANGELOG.md`

- [ ] `fields.md`: a `compose_after` row after `compose` (list, no, all): emitted after every service group and before the bridge overlay and project-wide `compose.extra`; same enabled gate; ordered by service name, list order kept; the use case (patch an app defined in its own overlay; whole-value keys now win); the caveat that it fixes order, not presence (a patch block for a disabled app still breaks the project); not settable from overlay layers; paths resolve like `compose:` (against the first `-f` file's directory)
- [ ] `services/index.md`: allowlist table row (✓ ✓ ✓); rewrite the ordering bullet at :37 to `tool → infra → app → compose_after`; add `compose_after` to the structural-fields example list in § Load behavior (overlays may not set it)
- [ ] `extends.md`: `compose_after` in the inherited-fields list (:32) with the same wording as `compose`, noting the two lists inherit independently
- [ ] `workspace.md` § Compose overlays: update the "Final emission order" block (:452-458) to include the `compose_after` tier **and** the bridge overlay line it currently omits (the block claims to be the final order); add one sentence that a per-service `compose.extra` precedes every `compose_after` file, so the project-wide layer is the one with the last word
- [ ] `concepts/architecture.md:96`: "tools → infra → apps → `compose_after` patches"
- [ ] RU mirrors of all five pages: translate the same changes, then set each `> Translated from: … @ <hash>` header to the new 12-char hash (run `make build` first so `internal/core/docs/content_hashes_gen.go` holds the new English hashes; `TestRussianTranslationsAreFresh` fails on a stale header)
- [ ] `CHANGELOG.md` `## [Unreleased]` `### Added`: the `compose_after:` field — one entry naming the chain position, the enabled gate, and linking `docs/reference/config/services/fields.md`
- [ ] run `make build` then `make test` - must pass before task 6

### Task 6: the OpenTelemetry guide and the agent skill

**Files:**
- Modify: `docs/guides/observability-otel.md` (+ `docs/i18n/ru/guides/observability-otel.md`)
- Modify: `skills/dwe/references/add-service-and-tools.md`

- [ ] guide § 1: the `service.yml` example gains `compose_after: [compose/otel-apps.yml]`; its header comment says the backend is in `compose:` and the app patch in `compose_after:`
- [ ] guide § 2: split the overlay — `compose/otel.yml` keeps the `otel` service and the `otel_data` volume; the `# --- patch of the base app service ---` block moves to a second listing, `compose/otel-apps.yml`, whose comment says it is emitted after every app overlay so `command:` / `healthcheck:` / `environment:` replacements win; every later reference to "`compose/otel.yml`, in the patched app service" (e.g. :302) points at the new file
- [ ] guide traps list: replace the "A tool overlay cannot override what an app's own overlay sets … design the patch to add only" bullet (:123) with the `compose_after` explanation (chain order, `dwe compose files` to see it); keep the "Patch only services that exist whenever the tool is on" bullet (:122), reworded so it says `compose_after` fixes the order but not the presence of the patched service
- [ ] guide Node/Sentry paragraph (:291): drop the "since the overlay cannot override the app's `SENTRY_DSN`" rationale; the patch now sets `SENTRY_DSN: ""` in `compose/otel-apps.yml`; keep one sentence that an app which falls back on an empty DSN (`?? default`) still needs the `register.mjs` delete
- [ ] re-read the Node recipe ("zero diff via `NODE_OPTIONS`") for any other sentence justified only by the ordering limit, and adjust it; the recipe itself stays add-only by choice, not by necessity
- [ ] RU guide: same edits; refresh its translation hash after `make build`
- [ ] `skills/dwe/references/add-service-and-tools.md`: add `compose_after:` to the "Common:" field list (:21) and one sentence in the compose paragraph (:62) — a tool/infra overlay that patches app services goes in `compose_after:`, not `compose:`
- [ ] `CHANGELOG.md`: extend the existing otel-guide entry, or add a line, saying the guide now uses `compose_after:`
- [ ] run `make build` then `make test` - must pass before task 7

### Task 7: verify acceptance criteria

- [ ] on a temp project with an app defined in `compose/app.yml` (with `command:` and an `environment:` key) and an enabled tool whose `compose_after` file overrides both: `dwe compose files` lists the tool's file after `compose/app.yml`, and `dwe compose config` shows the tool's `command:` and env value
- [ ] disabling the tool removes its `compose_after` file from `dwe compose files`; `dwe docker pull|build --all` still includes it — `dwe docker` has no dry-run, so assert it through `resolvePullInvocation` / `resolveBuildInvocation` (`internal/cli/docker/docker.go:263,300`, `all=true`) in `internal/cli/docker` tests: the returned `Compose.Files` contain the disabled owner's `compose_after` file
- [ ] a project without `compose_after` prints the same `dwe compose files` as on `feat/otel-guide`
- [ ] `services.<name>.compose_after` in `workspace/local.yml` fails `dwe validate` / load with the overlay error
- [ ] `make build`, `make lint`, `make test` clean

### Task 8: [Final] Update documentation

- [ ] `docs/internals/packages.md` § Core — Foundation (`project/config/`, :82): extend the "order is part of the public surface, locked" sentence with the `compose_after` tier (position, gate, one sorted pass across types, before the bridge overlay); add `ComposeAfter` to the `ResolveServiceExtends` inherited-field note; add `compose_after` to the list of fields that required BOTH allowlist entries; state the hash rule (key present only when non-empty, so existing deployment hashes are unchanged)
- [ ] `AGENTS.md`: no change expected — the existing "`info.yml` auto-blocks + `service.yml` allowlists" bullet already carries the only trap this field touches; add at most a one-line pointer only if implementation uncovers a new one (`TestAgentsMdBudget` pins the file size)
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Live verification** (binary from the branch, a real project with an optional
tool service patching an app that is defined in its own overlay):

- Move the app patch into a `compose_after` file, enable the tool,
  `dwe run`: the replaced `command:` / `healthcheck:` and the overridden env key
  are in effect in the running container (`docker inspect`).
- Disable the patched app while the tool stays enabled: Compose rejects the
  project, confirming the documented caveat reads correctly in the guide.
- `dwe deploy run` right after upgrading the binary on a project that does not
  use the field: every step reports `already up-to-date` (hash unchanged).

**External updates**

- The installed `skills/dwe` copy updates from `main` after merge; test the
  branch edit locally before then if needed.

**Follow-ups, deliberately not in this plan**

- An opt-in gate on another service being enabled
  (`compose_after: [{file: …, when_enabled: <svc>}]`), additive over the
  `[]string` form via a string-or-struct `UnmarshalYAML`. Justify with a second
  real use case first.
- A `dwe validate` warning for a service block in a `compose_after` (or tool
  `compose:`) file that has no `image:`/`build:` anywhere earlier in the active
  chain — the disabled-app trap caught statically. Could reuse
  `parseComposeFiles`; must not become a third compose parser.
- An existence check for `service.yml` `compose:` / `compose_after:` paths, which
  today only Docker Compose reports.

## Open questions

- **Type grouping inside the tier.** This plan emits `compose_after` in one
  alphabetical pass across types. The alternative is a second tool → infra → app
  pass so an app's `compose_after` outranks a tool's. No known use case needs
  it; switching later is a precedence change (user-observable), so decide in
  review rather than after release.
