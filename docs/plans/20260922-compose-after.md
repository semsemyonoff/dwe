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
`compose:` — in one pass ordered by service name across all types, with the
author's list order preserved within a service.

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
byte-identical chain and byte-identical deployment hashes (Tasks 2 and 4 pin
both).

### Non-goals

Decided during design and review; do not re-litigate during implementation:

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
    `- file: .env` (`workspace.go:~767`).
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
  names sorted alphabetically, all types together (decided in review). The
  tool → infra → app grouping exists to let apps win over the tools layered
  under them; files in this tier are by definition patches over the whole
  stack, and two `compose_after` files patching the same key is an authoring
  conflict either way. `fields.md` states the tie-break (the later service name
  wins) and tells authors not to rely on it — patch disjoint keys.
- **No deduplication.** A path listed in both `compose:` and `compose_after:`, or
  by two services, appears twice — the same policy `workspace.md` documents for
  `compose.extra` ("Docker Compose tolerates duplicates"). This includes
  `extends:`: a parent and a child that are both enabled emit the inherited
  `compose_after` file twice, exactly as an inherited `compose:` list does today
  (pinned in Task 3, documented in `extends.md`).
- **No `Raw["services"]` mirror.** `injectServicesIntoRaw` mirrors `compose`, but
  nothing reads `services.<name>.compose_after`, and adding it would create a
  public dot-path surface (export rules, `docker.yml` templates, `default_from:`)
  that then has to be kept stable. Add it when a consumer exists.
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
  `dwe docker pull|build --all` via `internal/cli/docker/docker.go:263,300`,
  already covered by `docker_test.go:673-680`).
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
- `journal.serviceConfigToMap` — `internal/core/workflow/deploy/journal/hash.go:257`
  hashes `"compose": svc.Compose` into `ServiceConfigHash` / `ProjectConfigHash`.
- `injectServicesIntoRaw` — `workspace.go:2989-2995` mirrors `compose` into
  `Raw["services"][name]`. Deliberately NOT extended (see Non-goals).

**Allowlists** (the `AGENTS.md` "service.yml allowlists" trap)

- Loader: `allowedFieldsFor` — `workspace.go:1097`, `common` slice at `:1099-1103`;
  used by the raw first pass at `:2333`. Test: `TestAllowedFieldsFor`,
  `service_type_test.go:92` (per-type `mustHave`/`mustLack` table).
- Validator mirror: `servicesAllowedFields` —
  `internal/core/validate/config/workspace.go:149-170` (three per-type maps),
  consumed at `:284`. Precedent test for an all-types field:
  `TestServicesValidator_BridgeFieldAllowedAllTypes`
  (`internal/core/validate/config/workspace_test.go:560`).
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

**Existing order tests**

- `TestComposeFiles_grouped_tool_infra_app` — `type_gates_test.go:53` (single
  fixture, asserts `ComposeFiles()` and `ComposeFilesAll()`). Left as is.
- `TestComposeFiles_bridgeOverlayChainPosition` — `bridge_overlay_test.go:23`.
- `TestComposeFiles_LocalOverlays_GoldenFullPipeline` — `workspace_test.go:4367`.
- `internal/cli/compose/compose_test.go:37-151` — `TestBuildComposeFileList_*`.

**Docs that state the order or the chain's contents** (grep `tools → infra`):
`docs/reference/config/services/index.md:37`,
`docs/reference/config/workspace.md:452-458` (the "Final emission order" block,
which also omits the bridge overlay today),
`docs/reference/concepts/architecture.md:96`,
`docs/reference/concepts/docker.md:55-61` (§ Compose file list — a numbered
list that claims completeness but stops at the app group),
`docs/reference/config/tests.md:494` (which files leave a scenario's chain when
it disables a service), `docs/guides/observability-otel.md:123`,
`docs/internals/packages.md:82`, and their RU mirrors under `docs/i18n/ru/`.

**Generated, tracked file.** `internal/core/docs/content_hashes_gen.go` is
committed and regenerated only by `make build` (`make test` syncs the embedded
tree but does not regenerate it). Every task that edits docs ends with
`make build` and commits the regenerated file together with the docs.

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
5. **Journal hash.** `serviceConfigToMap` adds `"compose_after"` **only when
   non-empty** (the `configs`/`dirs` pattern at `hash.go:260-275`), never as an
   always-present key: an always-present `nil` entry would change the canonical
   bytes of every existing service and make every deployed project look
   config-changed after upgrade. Hashed at all because `compose` is — an edit to
   the patch list changes what the stack runs.
6. **`--all`.** `ComposeFilesAll()` includes every service's `compose_after`
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
is extended to name the new tier and point at both pinning tests:
`TestComposeFiles_grouped_tool_infra_app` (the groups, unchanged) and the new
`TestComposeFiles_composeAfterTier`.

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
- Modify: `internal/core/validate/config/workspace_test.go`

- [x] add `ComposeAfter []string \`yaml:"compose_after"\`` after `LocalComposeExtra`, with a doc comment: emitted by `composeFiles` after every service group and before the bridge overlay, same `all || svc.Enabled` gate as `Compose`
- [x] add `"compose_after"` to the `common` slice of `allowedFieldsFor`
- [x] add `"compose_after": true` to all three maps in `servicesAllowedFields` (app, infra, tool)
- [x] extend `TestAllowedFieldsFor`: add `compose_after` to `commonFields` so every type must have it and the unknown type must lack it
- [x] add `TestServicesValidator_ComposeAfterAllowedAllTypes` in `internal/core/validate/config/workspace_test.go`, mirroring `TestServicesValidator_BridgeFieldAllowedAllTypes` (:560): an app, an infra and a tool each declaring `compose_after:` produce no "field not allowed" diagnostic (the side the loader test cannot see)
- [x] write loader tests through `LoadConfig` on a temp project: `compose_after: [a.yml, b.yml]` decodes in list order for `app`, `infra` and `tool`; a scalar `compose_after: a.yml` is a load error (strict decode into `[]string`); the typo `compose_afer:` is rejected with `ErrServiceFieldNotAllowed`
- [x] run `go test ./internal/core/project/config/... ./internal/core/validate/config/...` - must pass before task 2

### Task 2: emission, `--all`, overlay-layer rejection

**Files:**
- Modify: `internal/core/project/config/workspace.go` (`composeFiles` :698-742 + doc comments :670-692)
- Modify: `internal/core/project/config/type_gates_test.go` (new sibling test next to `TestComposeFiles_grouped_tool_infra_app` :53)
- Modify: `internal/core/project/config/bridge_overlay_test.go` (`TestComposeFiles_bridgeOverlayChainPosition` :23)
- Modify: `internal/core/project/config/workspace_test.go` (`TestComposeFiles_LocalOverlays_GoldenFullPipeline` :4367, overlay-rejection tests)
- Modify: `internal/cli/compose/compose_test.go` (`dwe compose files`)

- [x] emit `svc.ComposeAfter` in `composeFiles` as in Technical Details: after the app group, before the bridge overlay, one sorted-by-name pass over all types, gate `all || svc.Enabled`
- [x] update the `ComposeFiles` / `ComposeFilesAll` / `composeFiles` doc comments to describe the new tier and its position, pointing at both `TestComposeFiles_grouped_tool_infra_app` and `TestComposeFiles_composeAfterTier`
- [x] leave `TestComposeFiles_grouped_tool_infra_app` untouched; add a sibling table-driven `TestComposeFiles_composeAfterTier` in `type_gates_test.go`, each row pinning the exact full slice for both `ComposeFiles()` and `ComposeFilesAll()`:
  - a tool (`otel`) with `compose: [tool-otel.yml]` and `compose_after: [after-otel-1.yml, after-otel-2.yml]`, plus an infra and an app each with one `compose_after` entry → groups as before, then the `compose_after` files ordered by service name across types, the tool's two in list order
  - owner disabled → its `compose_after` absent from `ComposeFiles()`, present in `ComposeFilesAll()` at the same position
  - required owner (`Required: true, Enabled: true`) → present
  - no `compose_after` anywhere → chain byte-identical to the `TestComposeFiles_grouped_tool_infra_app` expectation (the backward-compatibility pin)
- [x] extend `TestComposeFiles_bridgeOverlayChainPosition` with a `compose_after` entry: it lands after the app's `compose.extra` and before `BridgeOverlayRelPath`, which stays before the project-wide `compose.local.yml`
- [x] extend `TestComposeFiles_LocalOverlays_GoldenFullPipeline` with a `compose_after` on the disabled `redis` and on an enabled tool: the active chain has only the tool's, after `compose/apps/web.yml` and before the project-wide extras; the all-chain has both
- [x] write overlay-rejection tests via `LoadConfig`: `services.<name>.compose_after` in `workspace/defaults.yml` and in `workspace/local.yml` are load errors naming the layer file and `services.<name>.compose_after`
- [x] add a `dwe compose files` test in `internal/cli/compose/compose_test.go` on a temp project with a tool `compose_after`: the printed lines equal `cfg.ComposeFiles()` with the `compose_after` file after the app overlay
- [x] run `go test ./internal/core/project/config/... ./internal/cli/compose/...` - must pass before task 3

### Task 3: `extends:` inheritance

**Files:**
- Modify: `internal/core/project/config/workspace.go` (`ResolveServiceExtends` :2514-2519)
- Modify: the `ResolveServiceExtends` tests in `internal/core/project/config/`

- [x] inherit `ComposeAfter` in `ResolveServiceExtends` next to `Compose` (`slices.Clone` from the parent when the child's list is empty; the child's own list replaces)
- [x] write extends tests via `LoadConfig`: child without `compose_after` inherits the parent's, and mutating the child's slice does not affect the parent's; child with its own list keeps only its own; child declaring `compose:` but not `compose_after:` still inherits the parent's `compose_after` (independent fields)
- [x] add the no-dedup row: parent and child both enabled, child inherits → the inherited file appears **twice** in `ComposeFiles()` (once per service, in service-name order); a comment on the assertion names it as the decided no-dedup policy, matching an inherited `compose:` list
- [x] run `go test ./internal/core/project/config/...` - must pass before task 4

### Task 4: journal hash

**Files:**
- Modify: `internal/core/workflow/deploy/journal/hash.go` (`serviceConfigToMap` :247-275)
- Modify: `internal/core/workflow/deploy/journal/hash_test.go`

- [x] **before** changing `hash.go`, capture `ServiceConfigHash` of a fixed fixture service (no `compose_after`, a representative set of fields) and pin it as a literal in a new test — this is the "no redeploy on upgrade" guarantee, checked against the pre-change value rather than against itself. Capturing it at this point is equivalent to capturing it at the branch base: Tasks 1–3 add a struct field and touch `composeFiles` / `ResolveServiceExtends` only, and `serviceConfigToMap` builds its map from named fields, so a new `ServiceConfig` field cannot reach the hash until this task adds it (if in doubt, compute the literal on `879f058f` with the same fixture and compare)
- [x] `serviceConfigToMap`: add `m["compose_after"] = svc.ComposeAfter` only when `len > 0`
- [x] test: `serviceConfigToMap` of a service without `compose_after` has no `compose_after` key
- [x] test: adding a `compose_after` entry changes the hash, and reordering two entries changes it again
- [x] run `go test ./internal/core/workflow/deploy/journal/...` (the literal pin must still pass after the change) - must pass before task 5

### Task 5: the scanners and `dwe test` inherit the tier

No production code is expected here — every consumer goes through
`ComposeFiles()`. These tests pin that the scanners and the scenario view
inherit the new tier, so a later change to either cannot silently drop it.

**Files:**
- Modify: `internal/core/workflow/envtest/scenarioview_test.go`
- Modify: `internal/core/project/config/compose_scan_test.go`
- Modify: `internal/cli/test/profile_test.go`

- [x] `ScenarioView`: `compose_after` survives the view (not stripped like `LocalComposeExtra`)
- [x] `ScenarioView`: a scenario `env.services.disable` of the owner drops its `compose_after` from `view.ComposeFiles()`, `enable` of a disabled owner adds it
- [x] extend `TestScenarioView_DoesNotMutateInput` with a service carrying a `compose_after` slice
- [x] `ScanComposeIsolation`: a `compose_after` file resetting a `container_name:` set by the app's own overlay clears the finding — last-wins over the app overlay, because the tier follows the app group (mirror `TestScanComposeIsolation_ContainerNameReset`)
- [x] `ScanComposeCost`: a `compose_after` file that resets the app overlay's `build:` (`!reset`) and sets `image:` plus a `healthcheck.start_period` leaves `BuildServices` empty and `ExternalImages == []string{"<image ref>"}`, and `MaxStartPeriod` reports the `compose_after` value (give the app overlay a different or no baseline `start_period`) — the tier wins the cost merge too (mirror `TestScanComposeCost_OverlayResetClearsBuild`)
- [x] `dwe test list` cost profile (`internal/cli/test/profile_test.go`, next to `TestCostProfile_IgnoresLocalComposeExtra`): one scenario enabling and one disabling the owner of a `compose_after` file that adds a `build:` service — the profile's build facts differ between them (`profile.go:188` feeds `ScenarioView` into both scanners)
- [x] run `go test ./internal/core/workflow/envtest/... ./internal/core/project/config/... ./internal/cli/test/...` - must pass before task 6

### Task 6: reference docs, RU mirrors, CHANGELOG

**Files:**
- Modify: `docs/reference/config/services/fields.md` (+ `docs/i18n/ru/reference/config/services/fields.md`)
- Modify: `docs/reference/config/services/index.md` (+ RU)
- Modify: `docs/reference/config/services/extends.md` (+ RU)
- Modify: `docs/reference/config/workspace.md` (+ RU)
- Modify: `docs/reference/concepts/architecture.md` (+ RU)
- Modify: `docs/reference/concepts/docker.md` (+ RU)
- Modify: `docs/reference/config/tests.md` (+ RU)
- Modify: `docs/reference/concepts/project-layout.md` (+ RU)
- Modify: `docs/reference/concepts/index.md` (+ RU)
- Modify: `internal/core/docs/content_hashes_gen.go` (regenerated by `make build`, committed)
- Modify: `CHANGELOG.md`

- [x] `fields.md`: a `compose_after` row after `compose` (list, no, all): emitted after every service group and before the bridge overlay and project-wide `compose.extra`; same enabled gate; one pass ordered by service name across types, list order kept; between two `compose_after` files touching the same key the later service name wins — do not rely on it, patch disjoint keys; the use case (patch an app defined in its own overlay; whole-value keys now win); the caveat that it fixes order, not presence (a patch block for a disabled app still breaks the project); not settable from overlay layers; paths resolve like `compose:` (against the first `-f` file's directory); unlike `compose`, NOT exposed through merged-config dot paths — `${services.<name>.compose_after}`, an `exports.env` `from:` and a command `default_from:` do not see it
- [x] `services/index.md`: allowlist table row (✓ ✓ ✓); rewrite the ordering bullet at :37 to `tool → infra → app → compose_after`; add `compose_after` to the structural-fields example list in § Load behavior (overlays may not set it)
- [x] `extends.md`: `compose_after` in the inherited-fields list (:32) with the same wording as `compose`, noting that the two lists inherit independently and that a parent and child both enabled emit an inherited file twice (no dedup)
- [x] `workspace.md` § "Where service fields come from" (:96): that paragraph lists `compose` among the resolved fields available under `services.<name>`; add that `compose_after` is deliberately not exposed there (no `${services.<name>.compose_after}`, `from:` or `default_from:` path)
- [x] `workspace.md` service-overlay description (:403, "Service-specific overlays live under `services.<name>.compose` …"): mention `compose_after` as the second per-service list and its tier
- [x] `workspace.md` § Compose overlays: update the "Final emission order" block (:452-458) to include the `compose_after` tier **and** the bridge overlay line it currently omits (the block claims to be the final order); add one sentence that a per-service `compose.extra` precedes every `compose_after` file, so the project-wide layer is the one with the last word
- [x] `concepts/architecture.md:96`: "tools → infra → apps → `compose_after` patches"
- [x] `concepts/docker.md` § Compose file list (:55-61): add item 5 "enabled services' `compose_after` files, by service name", then complete the tail the list claims to cover — item 6 the generated bridge overlay (when present), item 7 the project-wide `local.yml` `compose.extra`; adjust the following "Service type order matters" paragraph and note that `--all` includes disabled services' `compose_after` files too
- [x] `config/tests.md:494`: the sentence listing a service's own compose files that leave the chain when a scenario disables it names `compose_after:` alongside `compose:` and the `local.yml` overlays
- [x] `concepts/project-layout.md:168`: fix the existing error — it says the compose file list "picks up `workspace/docker.local.yml` last", but `docker.local.yml` is compose **policy** and never enters the `-f` list (`composeFiles` does not read it); the last layer is the project-wide `workspace/local.yml` `compose.extra`. Rewrite the sentence accordingly and mention `compose_after` as the tier for tracked patches that must follow the app overlays
- [x] `concepts/index.md:12`: the Docker-integration summary ("base + service overlays + tools + local") gets the real order — base, tool/infra/app overlays, `compose_after` patches, local
- [x] RU mirrors of all nine pages: translate the same changes
- [x] `CHANGELOG.md` `## [Unreleased]` `### Added`: the `compose_after:` field — one entry naming the chain position, the enabled gate, and linking `docs/reference/config/services/fields.md`
- [x] run `make build` (regenerates `internal/core/docs/content_hashes_gen.go`), set each RU `> Translated from: … @ <hash>` header to the new 12-char hash from that file, then `make test` (`TestRussianTranslationsAreFresh` fails on a stale header) - must pass before task 7
- [x] commit the docs together with the regenerated `internal/core/docs/content_hashes_gen.go`

### Task 7: the OpenTelemetry guide and the agent skill

**Files:**
- Modify: `docs/guides/observability-otel.md` (+ `docs/i18n/ru/guides/observability-otel.md`)
- Modify: `skills/dwe/references/add-service-and-tools.md`
- Modify: `internal/core/docs/content_hashes_gen.go` (regenerated by `make build`, committed)
- Modify: `CHANGELOG.md`

- [x] guide § 1: the `service.yml` example gains `compose_after: [compose/otel-apps.yml]`; its header comment says the backend is in `compose:` and the app patch in `compose_after:`
- [x] guide § 2: split the overlay — `compose/otel.yml` keeps the `otel` service and the `otel_data` volume; the `# --- patch of the base app service ---` block moves to a second listing, `compose/otel-apps.yml`, whose comment says it is emitted after every app overlay so `command:` / `healthcheck:` / `environment:` replacements win; every later reference to "`compose/otel.yml`, in the patched app service" (e.g. :302) points at the new file
- [x] guide traps list: replace the "A tool overlay cannot override what an app's own overlay sets … design the patch to add only" bullet (:123) with the `compose_after` explanation (chain order, `dwe compose files` to see it); keep the "Patch only services that exist whenever the tool is on" bullet (:122), reworded so it says `compose_after` fixes the order but not the presence of the patched service
- [x] guide Node/Sentry paragraph (:291): drop the "since the overlay cannot override the app's `SENTRY_DSN`" rationale; the patch now sets `SENTRY_DSN: ""` in `compose/otel-apps.yml`; keep one sentence that an app which falls back on an empty DSN (`?? default`) still needs the `register.mjs` delete
- [x] re-read the Node recipe ("zero diff via `NODE_OPTIONS`") for any other sentence justified only by the ordering limit, and adjust it; the recipe itself stays add-only by choice, not by necessity
- [x] RU guide: same edits
- [x] `skills/dwe/references/add-service-and-tools.md`: add `compose_after:` to the "Common:" field list (:21), and one sentence in the neighbour-patching paragraph (:64) — an overlay that patches app services goes in `compose_after:`, not `compose:`, so it lands after the apps' own overlays
- [x] `CHANGELOG.md`: amend the wording of the existing unreleased otel-guide entry (the service's overlay is now split into a `compose:` backend and a `compose_after:` app patch) rather than adding a separate line about the guide
- [x] run `make build`, refresh the RU guide's translation hash from the regenerated `content_hashes_gen.go`, then `make test` - must pass before task 8
- [x] commit the guide, skill and CHANGELOG together with the regenerated `internal/core/docs/content_hashes_gen.go`

### Task 8: verify acceptance criteria

Every command below runs the binary freshly built from this branch —
`"$repo/bin/dwe"` after `make build` — never a `dwe` from `PATH`. `dwe compose`
has only the `files`, `raw` and `argv` subcommands
(`internal/cli/compose/compose.go:18-25`); the merged config is read through
`raw`.

- [x] on a temp project with an app defined in `compose/app.yml` (with `command:` and an `environment:` key) and an enabled tool whose `compose_after` file overrides both: `"$repo/bin/dwe" compose files` lists the tool's file after `compose/app.yml`, and `"$repo/bin/dwe" compose raw -- config` shows the tool's `command:` and env value
- [x] `"$repo/bin/dwe" services disable <tool>` on that project: `"$repo/bin/dwe" compose files` no longer lists its `compose_after` file
- [x] `services.<name>.compose_after` added to that project's `workspace/local.yml`: `"$repo/bin/dwe" validate` reports a load failure whose message names `workspace/local.yml` and `services.<name>.compose_after` (the diagnostic's `File` field is `workspace.yml`, set by the workspace validator)
- [x] backward compatibility is covered by Task 2's byte-identical row and Task 4's literal hash pin — no separate binary comparison
- [x] `make build`, `make lint`, `make test` clean

### Task 9: [Final] Update documentation

- [ ] `docs/internals/packages.md` § Core — Foundation (`project/config/`, :82): extend the "order is part of the public surface, locked" sentence with the `compose_after` tier (position, gate, one sorted pass across types, before the bridge overlay); add `ComposeAfter` to the `ResolveServiceExtends` inherited-field note (independent of `Compose`, no dedup); add `compose_after` to the list of fields that required BOTH allowlist entries; state the hash rule (key present only when non-empty, so existing deployment hashes are unchanged) and that the field is deliberately not mirrored into `Raw["services"]`
- [ ] `AGENTS.md`: no change expected — the existing "`info.yml` auto-blocks + `service.yml` allowlists" bullet already carries the only trap this field touches; add at most a one-line pointer only if implementation uncovers a new one (`TestAgentsMdBudget` pins the file size)
- [ ] move this plan to `docs/plans/completed/`
- [ ] run `make build && make test` (this task edits `packages.md` after the last `make build`) and commit the regenerated `internal/core/docs/content_hashes_gen.go` with the `packages.md` change

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
- A `dwe validate` warning for overlapping keys across `compose_after` files of
  different services (the case where the service-name tie-break silently
  decides), on the same parser.
- An existence check for `service.yml` `compose:` / `compose_after:` paths, which
  today only Docker Compose reports.
