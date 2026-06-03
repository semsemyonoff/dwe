# Per-Developer Compose Overlays in `workspace/local.yml`

## Overview

Add per-developer Docker Compose overlay support without modifying any git-tracked project files. Today, DWE assembles `docker compose -f ...` from an explicit list (base + per-service `compose:` fields from `service.yml`), so the standard `docker-compose.override.yml` discovery does not apply. There is no way for a developer to inject container-level overrides (env vars, volumes, ports) locally.

**Motivating use case:** a developer wants to pass `GIT_CONFIG_COUNT` / `GIT_CONFIG_KEY_N` / `GIT_CONFIG_VALUE_N` into the `dev` container so commits made inside the container use the project-specific git identity, independent of the host `~/.gitconfig`. This must be possible per-developer without touching `workspace/services/dev/*.yml`.

**Solution:** extend the already per-developer `workspace/local.yml` (currently used for `enabled` / `ports` / `hosts` overrides) with two new optional sections:

- **Project-wide:** `compose.extra: [<path>, …]` — paths appended last to the `-f` chain, always.
- **Per-service:** `services.<name>.compose.extra: [<path>, …]` — paths appended right after the service's own compose files, only when the service is enabled (inherits the existing `if all || svc.Enabled` gate in `composeFiles`).

Fully additive, backward-compatible, no breaking changes.

## Context (from discovery)

- **Files involved:**
  - `internal/core/project/local/local_yaml.go` — local.yml loader (`LoadLocalYAML` returns `map[string]any`, lenient YAML) and atomic writer.
  - `internal/core/project/local/services.go` — toggle-write logic for `dwe services enable/disable`. NOT the config-load validation path.
  - `internal/core/project/config/workspace.go` — `ComposeConfig` (line 376), `ServiceConfig.Compose` (line 832), `composeFiles(all bool)` (lines 438–465), `OverlayAllowedKeys` (line 1385), `validateServicesOverlay` (line 1396), `validateOverlayPorts` (line 1445), `LoadConfig` layer-3 path (lines 1238–1310).
  - `internal/core/project/config/services_overlay_test.go` — existing tests for the overlay validator; new shape tests for `compose.extra` go here.
  - `internal/core/validate/config/workspace.go` — `workspaceValidator` surfaces `LoadConfig` errors as `dwe validate config workspace` diagnostics. **No separate `local.yml` validator domain exists or is needed** — load errors flow through this validator.
  - `internal/shared/pathsafe/` — exports `pathsafe.ContainedRel(baseDir, candidate)`. **`pathsafe.Resolve` does NOT exist.**
  - `internal/shared/docker/compose.go:132` — sets `cmd.Dir = c.BaseDir`; relative compose paths resolve via cwd. Overlay paths must follow the same relative-storage convention.
- **Related patterns:**
  - **Pre-merge overlay validation** is performed by `validateServicesOverlay` BEFORE merging into the typed config. `OverlayAllowedKeys` is a closed whitelist `{enabled, ports, hosts}`; anything else (including `compose`) is rejected. This blocks our new fields unless we extend the validator.
  - **Services are NOT auto-merged from local.yml** by a generic deep merge — there is explicit manual injection code that walks `local["services"][name]` and writes `enabled`/`ports`/`hosts` onto the already-decoded typed `ServiceConfig`. The new injection for `LocalComposeExtra` lives next to that code.
  - **No top-level overlay validator** exists for local.yml — anything outside `services.*` is silently ignored today. Project-wide `compose.extra` needs a new `validateLocalCompose` (shape-only, does not whitelist other top-level keys since `state:` / `runtime:` etc. are legitimate). Symmetrically, non-local layers need a `compose.extra` rejection guard (see Solution Overview).
  - `composeFiles` already groups services tools→infra→apps with alpha-sort within group via `slices.Sorted(maps.Keys(c.Services))` inside `emitGroup`; per-service local overlays must reuse this exact path so test output stays deterministic.
  - `ComposeFilesAll()` (`dwe docker --all`) symmetrically includes everything — local overlays of disabled services must also be included in `--all` mode.
- **Dependencies:**
  - `pathsafe.ContainedRel(baseDir, abs)` for path safety (returns containment-validated relative path or error). Pattern reference: `internal/core/validate/linters/walk.go:65`.
  - `make build` regenerates embedded docs via `scripts/sync-embedded-docs.sh` + `scripts/gen-docs-content-hashes.sh` (required after any change under `docs/`).
- **Russian translations:** `docs/i18n/ru/reference/config/workspace.md` and `docs/i18n/ru/reference/config/docker.md` exist and must be updated in lockstep. `docs/internals/packages.md` has no Russian translation — English only.

## Development Approach

- **Testing approach:** Regular (code first, then tests within the same task). Tests are required per task before moving to the next; both success and error paths covered.
- Complete each task fully before moving to the next; small focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task.
- **CRITICAL: all tests must pass before starting next task.**
- Run `make test` (or focused `go test ./internal/core/project/...`) after each change.
- Maintain backward compatibility — no existing `local.yml` should break.

## Testing Strategy

- **Unit tests:** required for every task touching code. Use table-driven tests; place fixtures in `testdata/` per package convention.
- **No e2e suite in this project** for the CLI surface affected — verification is by exercising `dwe deploy` / `dwe docker config` in a manual smoke step (Post-Completion).
- Tests cover:
  - Project-wide local overlays appear last in `-f`.
  - Per-service overlays appear right after the service's own compose files.
  - Per-service overlays are excluded when service is disabled (`ComposeFiles()`).
  - Per-service overlays are included regardless of disabled state in `ComposeFilesAll()`.
  - Unknown service name in `services.<name>.compose.extra` → hard error.
  - Path escaping project root (`../`) → hard error.
  - Non-existent file path → hard error.
  - Missing local.yml or local.yml without `compose:` section → no-op, no error.
  - Round-trip read → write (e.g. via `dwe services enable foo`) preserves existing `compose.extra` entries.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.

## Solution Overview

Four load-bearing decisions (revised after Codex review of real codebase):

1. **Schema accessible only from `local.yml`** — both new fields (`ComposeConfig.Extra`, `ServiceConfig.LocalComposeExtra`) carry `yaml:"-"` so neither workspace.yml/defaults.yml nor service.yml can populate them. Population is explicit post-decode injection from the local.yml raw map only.
2. **Pre-merge overlay validation must layer-gate `compose.extra` to local.yml only** — `validateServicesOverlay` (workspace.go:1396) runs BEFORE merge against ALL layers (defaults.yml, workspace.yml, local.yml). Adding `"compose"` to the global `OverlayAllowedKeys` would silently permit `services.<name>.compose.extra` in defaults.yml/workspace.yml where it would pass validation but never be injected — confusing. Instead, layer-gate the `compose` key inline in `validateServicesOverlay`'s key loop (accept only when `layerPath == localPath`). Plus add `validateLocalCompose` that validates the SHAPE of project-wide `compose.extra` without whitelisting top-level keys (local.yml legitimately carries `state:`, `runtime:`, etc.).
3. **Reuse existing enabled-gate** — per-service overlays sit inside the same `if all || svc.Enabled` branch that already gates per-service compose files in `composeFiles`. No new filtering logic.
4. **Project-wide overlay appended last** — matches docker compose merge semantics (last `-f` wins), giving devs a clean "patch everything" surface. **Documented consequence**: project-wide overlays CAN override per-service overlays. Per-service-last (the other defensible order) was rejected because it makes the project-wide layer redundant in the common case.

Data flow:

```
local.yml (map[string]any, lenient)
  │
  ├──► validateLocalCompose(layerPath, raw)        ◄── NEW: only validates shape of
  │       raw["compose"] when present; does NOT reject other top-level keys
  │       (state:, runtime:, etc. remain valid)
  │       Runs only for local.yml layer.
  │
  ├──► validateServicesOverlay(layerPath, raw, …)  ◄── EXTENDED:
  │       per-key loop layer-gates "compose" — accept only when
  │       layerPath == localPath; delegate to validateOverlayCompose for shape.
  │       OverlayAllowedKeys map itself is unchanged.
  │
  ├──► (existing) typed DweConfig decode + service overlay merge for
  │       enabled/ports/hosts (this is manual, not generic deep merge)
  │
  ├──► post-decode injection (NEW, in LoadConfig, source-gated to local layer):
  │       local["compose"]["extra"]                   → cfg.Compose.Extra (yaml:"-")
  │       local["services"][name]["compose"]["extra"] → cfg.Services[name].LocalComposeExtra (yaml:"-")
  │
  ├──► ResolveServiceExtends (EXTENDED): copy LocalComposeExtra parent→child
  │       when child has none (deep copy, no merge)
  │
  └──► path validation (NEW): for each entry in both layers:
        1. reject filepath.IsAbs(p)
        2. pathsafe.ContainedRel(baseDir, filepath.Join(baseDir, p))
        3. os.Stat(abs) — must exist

composeFiles(all):
  ├─ Compose.Base
  ├─ for svc in tools→infra→apps (alpha-sorted, existing emitGroup):
  │     if all || svc.Enabled:
  │         ├─ svc.Compose...
  │         └─ svc.LocalComposeExtra...            ◄── NEW (same gate)
  └─ Compose.Extra...                              ◄── NEW (project-wide, always last)
```

## Technical Details

### Schema (lenient YAML, no `KnownFields`)

```yaml
# workspace/local.yml — per-developer, gitignored by convention

compose:
  extra:
    - compose.local.yml
    - path/relative/to/project/root.yml

services:
  <name>:
    enabled: true|false
    ports:  { ... }
    hosts:  { ... }
    compose:
      extra:
        - compose/<name>.local.yml
```

### Struct extensions

Both new fields use `yaml:"-"` and are populated **only** by explicit post-decode injection from the local.yml raw map. They are unreachable from any other YAML.

- `ComposeConfig` (workspace.go:376) gains:
  ```go
  type ComposeConfig struct {
      Base  string   `yaml:"base"`
      Extra []string `yaml:"-"` // populated only from workspace/local.yml compose.extra
  }
  ```
- `ServiceConfig` (workspace.go:832) gains:
  ```go
  LocalComposeExtra []string `yaml:"-"` // populated only from workspace/local.yml services.<name>.compose.extra
  ```
- Do NOT add `LocalComposeExtra` to `allowedFieldsFor()` (workspace.go:776) or to `servicesAllowedFields` in `internal/core/validate/config/workspace.go` — overlay-extra MUST never appear in a git-tracked service.yml.

### Overlay validators (pre-merge)

Local.yml validation happens BEFORE merge — see `LoadConfig` flow around workspace.go:1238–1260. Two validators must be extended/added:

1. **Extend `validateServicesOverlay`** (workspace.go:1396) WITHOUT modifying `OverlayAllowedKeys`. Inline a layer-gated branch in the per-key loop: when `key == "compose"` AND `layerPath == localPath`, accept and delegate to `validateOverlayCompose(layerPath, svcName, raw)`; otherwise fall through to the existing rejection. `validateOverlayCompose` (sibling of `validateOverlayPorts` / `validateOverlayHosts`) under `services.<name>.compose` accepts only `extra: [<string>, …]` and rejects anything else with a clear error. `OverlayAllowedKeys` is left untouched so any external tooling that introspects it keeps the same answer.
2. **New `validateLocalCompose(layerPath, raw)`**: today nothing validates the top level of local.yml, and local.yml legitimately carries other top-level convention keys (e.g. `state:`, `runtime:`) so we must NOT whitelist top-level keys. Instead, when `local["compose"]` is present, validate ONLY its shape: must be a mapping containing exactly `extra: [<string>, …]`; reject unknown subkeys and non-string entries with field-path-bearing errors. Wire it into `LoadConfig` next to the `validateServicesOverlay` call (workspace.go:1259), gated to run only for the local.yml layer (`layer.path == localPath`).

   **Symmetric non-local guard**: for layers OTHER than local.yml, explicitly reject `compose.extra` at the top level — otherwise `workspace.yml` / `defaults.yml` could carry `compose.extra` that strict YAML decode silently ignores (since `ComposeConfig.Extra` is `yaml:"-"`), confusing users. Add a sibling check `validateNonLocalCompose(layerPath, raw)` that runs when `layer.path != localPath`: when `raw["compose"]` is present and contains `extra`, return a hard error explaining `compose.extra` is per-developer and belongs in `workspace/local.yml`. `compose.base` (the existing field) remains allowed everywhere.

3. **Layer-gate `compose` under `services.<name>` to local.yml only**: adding `"compose"` to the global `OverlayAllowedKeys` map would also permit `services.<name>.compose: {extra: […]}` in `defaults.yml` and `workspace.yml` — where it would pass validation but never trigger injection (since injection is source-gated to local.yml). That silent ignore is confusing. Instead, do NOT add `"compose"` to the global map. Inline the per-key check in `validateServicesOverlay`: when iterating keys under a service, accept `compose` only when `layerPath == localPath`, otherwise reject with the existing "service definitions belong in workspace/services/<name>/service.yml" message. Keep `OverlayAllowedKeys` semantics intact for tools that introspect it.

### Post-decode injection

After validation passes and the typed `DweConfig` is built, inject from the raw local.yml map:

- `local["compose"]["extra"]` → `cfg.Compose.Extra` (list of strings).
- `local["services"][name]["compose"]["extra"]` → `cfg.Services[name].LocalComposeExtra` for each declared service. Mirror the existing manual injection used for `enabled`/`ports`/`hosts` (locate the function that performs that injection during Task 1; it lives near the layer-3 loader at workspace.go:1238–1310).
- **Inheritance behavior**: `ResolveServiceExtends` currently copies `Compose`, `Ports`, `Hosts`, etc. for `extends:`-children. `LocalComposeExtra` MUST be explicitly added to that copy logic: if a child has no `LocalComposeExtra` of its own, inherit the parent's; if it has its own, the child's wins (no merge — keeps semantics simple). Add a test exercising both branches. Inject BEFORE `ResolveServiceExtends` so the resolver sees populated parent values.

### Path resolution

- All paths in `compose.extra` (both layers) MUST be relative to the project root (directory containing `workspace.yml`). They are stored **relative** on the typed config — matching `ServiceConfig.Compose` semantics, which `docker.Compose` resolves by setting `cmd.Dir = c.BaseDir` (see `internal/shared/docker/compose.go:132`). Storing absolute paths would diverge from this pattern.
- **Absolute paths are rejected**: if `filepath.IsAbs(p)` is true, hard error with field path. Rationale: `filepath.Join(baseDir, "/x.yml")` returns `/x.yml` (not `baseDir/x.yml`), so containment validation would pass through and docker compose would receive an absolute path the user didn't intend. Fail fast.
- Path safety via `pathsafe.ContainedRel(baseDir, abs)` after `abs := filepath.Join(baseDir, p)` — `pathsafe.ContainedRel` is the helper that exists in this codebase (NOT `pathsafe.Resolve`, which doesn't exist). Reject `../`-escapes with a clear error message that includes the field path.
- File existence checked at load time via `os.Stat(abs)`; missing file → hard error containing the as-written path AND the resolved absolute path (computed for the diagnostic only).

### Validation

- **Unknown service name** in `services.<name>.compose.extra` → hard error.
  - Mirror the existing service-name validation used by `services.<name>.enabled` etc. in `internal/core/project/local/services.go`.
- **Non-existent path** → hard error during config load (fail fast).
- **Path escapes project root** → hard error from `pathsafe.ContainedRel(baseDir, abs)`.
- **Absolute path** → hard error from `filepath.IsAbs(p)` check (runs BEFORE the join, since `filepath.Join(baseDir, "/x")` returns `/x` and bypasses containment).
- **Do NOT** validate the content of overlay YAML — docker compose is the authority.

### Edge cases

- Empty/missing local.yml: no-op. (`LoadLocalYAML` already returns empty map for missing file.)
- local.yml present but no `compose:` section anywhere: no-op.
- Duplicate paths between layers (e.g. same file in service-extra and project-extra): not deduped — docker compose tolerates duplicates; let it surface any issue. Document this behavior.
- `docker.local.yml` (compose **policy**) coexists unchanged — different file, different purpose. Cross-reference in docs to prevent confusion.

## What Goes Where

- **Implementation Steps** (checkboxes): schema, merge plumbing, validation, tests, docs (EN + RU).
- **Post-Completion** (no checkboxes): manual smoke test by editing a real `local.yml` in a sample project and running `dwe docker config` to confirm overlay ordering; verify `dwe services disable <svc>` removes the per-service overlay from compose args.

## Implementation Steps

### Task 1: Schema + overlay validators + post-decode injection

**Files:**
- Modify: `internal/core/project/config/workspace.go` (lines around 376, 832, 1238–1310, 1385–1431)
- Modify: `internal/core/project/config/services_overlay_test.go` (extend with new positive/negative cases)
- Modify: `internal/core/project/config/workspace_test.go` (load-end-to-end tests)
- Possibly Create: `internal/core/project/config/testdata/local-overlays/...` fixture project (workspace.yml + minimal services + local.yml + dummy overlay files)

**Schema:**
- [x] Add `Extra []string `yaml:"-"`` to `ComposeConfig` (workspace.go:376). `yaml:"-"` is deliberate — never decodable from any file.
- [x] Add `LocalComposeExtra []string `yaml:"-"`` to `ServiceConfig` (workspace.go:832).
- [x] Confirm `LocalComposeExtra` is NOT added to `allowedFieldsFor()` (workspace.go:776) and `servicesAllowedFields` in `internal/core/validate/config/workspace.go` is NOT extended.

**Overlay validators (pre-merge):**
- [x] Do NOT add `"compose"` to `OverlayAllowedKeys`. Instead, inline a layer-gated branch in `validateServicesOverlay`'s key loop (workspace.go:1417–1422): when `key == "compose"` AND `layerPath == localPath`, accept and delegate to `validateOverlayCompose`. Otherwise fall through to the existing rejection. Pass `localPath` (or a `isLocalLayer bool`) into `validateServicesOverlay` — locate the call at workspace.go:1259 and update the signature accordingly.
- [x] Add `validateOverlayCompose(layerPath, svcName string, raw any) error` next to `validateOverlayPorts` (workspace.go:1445). Accept `nil`; otherwise require `raw` to be `map[string]any` with exactly one key `extra` whose value is `[]any` of strings. Reject empty/non-string entries with field-path-bearing errors.
- [x] Add new function `validateLocalCompose(layerPath string, raw map[string]any) error`: validate ONLY the shape of `raw["compose"]` when present. Do NOT whitelist other top-level keys — local.yml legitimately carries other convention keys (e.g. `state:`, `runtime:`) and rejecting them would break existing files. Under `compose` accept only `extra: [<string>, …]` (reuse the same shape check as `validateOverlayCompose`). Wire it into `LoadConfig` right next to the `validateServicesOverlay` call (workspace.go:1259), guarded to run ONLY when `layer.path == localPath`.
- [x] Add sibling function `validateNonLocalCompose(layerPath string, raw map[string]any) error`: when `layer.path != localPath` AND `raw["compose"]["extra"]` is present, return a hard error: `<layerPath>: compose.extra: per-developer overlays belong in workspace/local.yml, not in this file`. Wire it into the same per-layer loop as `validateLocalCompose` (mutually exclusive guard). `compose.base` is left untouched.

**Post-decode injection:**
- [x] Locate the existing function that injects `enabled`/`ports`/`hosts` from the local.yml raw map onto typed services (search around workspace.go:1238–1310 for the loop that walks `raw["services"]` after decode). Mirror its structure for compose.extra: walk `local["services"][name]["compose"]["extra"]`, write the resulting `[]string` into `cfg.Services[name].LocalComposeExtra`. Source the data ONLY from the local.yml layer (defense in depth — should be unreachable since pre-merge validators reject `services.<name>.compose` outside local.yml, but explicit source-gating prevents future regressions).
- [x] Order this injection BEFORE `ResolveServiceExtends` so the resolver sees populated parent values.
- [x] **Update `ResolveServiceExtends`** (find via grep) to include `LocalComposeExtra` in its inheritance copy logic: if a child has zero-length `LocalComposeExtra`, copy the parent's slice (deep copy to avoid aliasing); if the child has its own non-empty slice, it wins (no merge, no append — keeps semantics simple). This matches how `Compose`/`Ports`/`Hosts` are handled.
- [x] Add sibling injection for project-wide: walk `local["compose"]["extra"]`, write into `cfg.Compose.Extra` (only from local.yml layer).

**Tests:**
- [x] Extend `services_overlay_test.go`:
  - positive: `services.<name>.compose.extra: [a.yml, b.yml]` in local.yml accepted
  - negative: `services.<name>.compose: {extra: [...]}` in `defaults.yml` or `workspace.yml` rejected with the existing "service definitions belong in workspace/services/<name>/service.yml" message (layer-gate check)
  - negative: `services.<name>.compose: {foo: bar}` in local.yml rejected (unknown subkey under compose)
  - negative: `services.<name>.compose.extra: "string-not-list"` rejected
  - negative: `services.<name>.compose.extra: [123]` rejected (non-string entry)
- [x] Add tests for `validateLocalCompose`:
  - positive: `compose.extra: [x.yml]` accepted
  - negative: `compose: {foo: bar}` rejected
  - negative: `compose.extra` non-list rejected
  - positive: local.yml WITHOUT `compose:` section but WITH unknown top-level keys (e.g. `state:`, `runtime:`) is accepted — no whitelist
- [x] Add tests for `validateNonLocalCompose`:
  - negative: `compose.extra: [x]` in `workspace.yml` or `defaults.yml` → rejected with clear message pointing to `workspace/local.yml`
  - positive: `compose.base: compose.yml` in `workspace.yml` is unaffected
- [x] Add load-end-to-end tests in `workspace_test.go`: load a fixture project with local.yml containing both project-wide and per-service overlays; assert `cfg.Compose.Extra` and `cfg.Services[name].LocalComposeExtra` are populated as expected. (Implemented in `services_overlay_test.go` next to the existing overlay e2e cases — same package, same conventions.)
- [x] Inheritance test: a child service with `extends:` parent inherits parent's `LocalComposeExtra` when child has none; child's wins when both present.
- [x] Backward-compat test: load a fixture with a local.yml that contains ONLY `services.<name>.enabled` — assert zero-length `Extra` slices and no error.
- [x] Source-gating test: hand-construct a layer set where a non-local layer somehow carries `services.<name>.compose.extra` (bypass validator in test), confirm injection does NOT write to `LocalComposeExtra` for that layer. Defense-in-depth assertion.
- [x] Run tests — must pass before Task 2.

### Task 2: Extend `composeFiles` to emit local overlays in order

**Files:**
- Modify: `internal/core/project/config/workspace.go` (lines 438–465)
- Modify: `internal/core/project/config/workspace_test.go`

- [x] In `composeFiles(all bool)`, inside the existing `emitGroup` closure, after `files = append(files, svc.Compose...)` add a sibling `files = append(files, svc.LocalComposeExtra...)`. Both appends MUST live inside the same `if (all || svc.Enabled)` block — local overlays reuse the existing enabled-gate, not a separate one.
- [x] After the three `emitGroup` calls, append `c.Compose.Extra...` (project-wide, unconditional).
- [x] Verify the per-service overlay iteration uses the existing `emitGroup` / alpha-sort path (`slices.Sorted(maps.Keys(c.Services))`) — do NOT add a fresh `range cfg.Services` anywhere. Map iteration without sort would make golden tests flaky (per CLAUDE.md § `info.yml` auto-blocks, this rule applies project-wide to any service iteration in rendered output).
- [x] Update godoc on `ComposeFiles` / `ComposeFilesAll` / `composeFiles` to mention local overlays and their ordering.
- [x] Write table-driven test `TestComposeFiles_LocalOverlays`:
  - per-service overlay present when service enabled
  - per-service overlay absent when service disabled (under `ComposeFiles`)
  - per-service overlay present when service disabled (under `ComposeFilesAll`)
  - project-wide overlay always last
  - deterministic order across tools→infra→apps groups (alpha-sorted within group)
  - overlay-only (no `svc.Compose`) service still emits overlays
- [x] Write a golden-style assertion: a single test case fixes the FULL expected `-f` list (base + tool overlays + tool local + infra overlays + infra local + app overlays + app local + project-wide local) and compares slice equality. This catches any future regression in iteration order.
- [x] Run tests — must pass before Task 3.

### Task 3: `dwe validate` surface + round-trip preservation

Note: unknown-service-name rejection for `services.<name>.compose.extra` is already handled by the existing `validateServicesOverlay` (workspace.go:1405–1408 — rejects unknown service names at the service-key level, BEFORE the per-key allowlist check). So no additional name validation is needed — Task 1's `validateOverlayCompose` only needs to validate the shape of the `compose` block; the service name itself is already covered.

This task focuses on (a) making sure `dwe validate` surfaces these diagnostics nicely, and (b) preserving `compose.extra` entries through `dwe services enable/disable` round-trips.

**Files:**
- Read & possibly Modify: `internal/core/validate/config/workspace.go` (the existing `workspaceValidator` — already surfaces `LoadConfig` failures as diagnostics)
- Modify: `internal/core/project/local/local_yaml_test.go` (round-trip test)
- Possibly Modify: `internal/core/validate/config/` tests for the workspace validator

- [x] **`dwe validate` integration check**: the existing `workspaceValidator` surfaces `LoadConfig` errors as diagnostics, but per Codex round-2 review its `Diagnostic.File` field attribution currently points to `workspace.yml` even for errors originating in `local.yml` — the specific path appears only in the error message. Add an integration test exercising the new validators (`validateOverlayCompose`, `validateLocalCompose`) via `dwe validate config workspace`: fixture project with a malformed `local.yml`, run validate, assert the error message text contains `workspace/local.yml` (NOT assert on `Diagnostic.File`, which will say `workspace.yml`). Document this attribution quirk in the test's comment so a future cleanup PR knows it can tighten the assertion if/when `workspaceValidator` is improved.
- [x] If improving `Diagnostic.File` attribution proves trivial during this work (e.g. a one-line annotation when the underlying error is a `*LayerError` or similar), do it; otherwise file as Post-Completion follow-up. (Deferred — non-trivial, would require introducing a typed `*LayerError` and threading it through every loader error site. Filed as Post-Completion follow-up.)
- [x] **Round-trip preservation test**: write a test in `local_yaml_test.go` that:
  1. Writes a `local.yml` containing both `compose.extra` and `services.<name>.compose.extra` (plus `enabled`/`ports`/`hosts` entries).
  2. Loads via `LoadLocalYAML`, applies `ApplyServiceTogglesToYAML` (the `dwe services enable/disable` write path) to flip a service's enabled bit.
  3. Writes back via `WriteLocalYAML`, reloads, and asserts ALL `compose.extra` entries (both layers) survive untouched.
  4. Since the loader uses `map[string]any`, this should be automatic — the test pins the behavior so a future refactor to typed structs doesn't silently drop unknown keys.
- [x] Run tests — must pass before Task 4.

### Task 4: Path safety + existence validation

**Files:**
- Modify: `internal/core/project/config/workspace.go` (right after post-decode injection from Task 1)
- Modify: `internal/core/project/config/workspace_test.go`

- [x] After post-decode injection populates `Compose.Extra` and per-service `LocalComposeExtra`, walk each path and validate IN THIS ORDER:
  1. **Absolute rejection**: `if filepath.IsAbs(p) → hard error`. Must come BEFORE the join, since `filepath.Join(baseDir, "/x.yml")` returns `/x.yml` and bypasses containment.
  2. **Containment**: compute `abs := filepath.Join(baseDir, p)` then `pathsafe.ContainedRel(baseDir, abs)` — on error wrap with the local.yml field path that produced the bad value. (Pattern mirrors `internal/core/validate/linters/walk.go:65`.)
  3. **Existence**: `os.Stat(abs)`; missing file → hard error including the as-written path AND the absolute path for easy debugging.
- [x] Store paths **as written** (relative form) on the typed config — do NOT replace with absolute paths. Downstream `composeFiles` returns these relative paths; `docker.Compose` resolves them via `cmd.Dir = c.BaseDir` (compose.go:132), matching how `ServiceConfig.Compose` paths already behave.
- [x] Tests:
  - relative path inside project root passes validation
  - absolute path (e.g. `/etc/passwd`) → error referencing `filepath.IsAbs` rejection
  - `../` escape → error referencing `pathsafe.ContainedRel` diagnostic + field path
  - missing file → error showing both as-written and absolute path
  - duplicate path between project-wide and per-service is allowed (no dedup)
  - paths-as-written are preserved through `cfg.Compose.Extra` / `cfg.Services[name].LocalComposeExtra` (not absolutized)
- [x] Run tests — must pass before Task 5.

### Task 5: English documentation updates

**Files:**
- Modify: `docs/reference/config/workspace.md` (extend the `local.yml` section with a "Compose overlays" subsection)
- Modify: `docs/reference/config/docker.md` (add a short cross-reference distinguishing `docker.local.yml` from `local.yml → compose.extra`)
- Modify: `docs/internals/packages.md` (add invariant under the `project/config/workspace.go` / `composeFiles` description)

- [x] In `workspace.md` under the `local.yml` section, document:
  - schema for `compose.extra` (project-wide) and `services.<name>.compose.extra` (per-service)
  - ordering rules (per-service after service.compose, project-wide always last)
  - per-service overlays inherit enabled-gate
  - paths relative to project root, must exist, no `..` escapes
  - full GIT_CONFIG_* example for the motivating use case
  - guidance: add `local.yml` and any referenced overlay files to `.gitignore`
- [x] In `docker.md`, add a short note: "for compose **policy** (project name, args, env) → `docker.local.yml`; for compose **service overlays** → `local.yml → compose.extra`. They are independent surfaces."
- [x] In `packages.md`, under `project/config/workspace.go`, add an invariant bullet: "per-service `compose.extra` overlays from local.yml are emitted inside the same `all || svc.Enabled` gate as `svc.Compose`; project-wide `compose.extra` is appended strictly last to `composeFiles()` output."
- [x] Run `make build` to regenerate embedded docs and content hashes.
- [x] Run `make test` — embedded-docs-dependent tests must pass.

### Task 6: Russian documentation updates

**Files:**
- Modify: `docs/i18n/ru/reference/config/workspace.md`
- Modify: `docs/i18n/ru/reference/config/docker.md`

- [x] Translate the new `workspace.md` "Compose overlays" subsection into Russian, preserving the YAML examples verbatim (only prose translated). Keep terminology consistent with the rest of the Russian docs (e.g. use of "сервис", "оверлей", "локальный").
- [x] Translate the new `docker.md` cross-reference paragraph.
- [x] Run `make build` again to refresh embedded docs.
- [x] Run `make test`.
- [x] Note: `docs/internals/packages.md` has no Russian translation in the repo — no RU internals update needed. If `docs/i18n/ru/internals/` is later added, this plan needs revisiting.

### Task 7: Verify acceptance criteria

- [ ] Verify schema accepts both layers (project-wide + per-service) with the GIT_CONFIG_* example fixture.
- [ ] Verify `ComposeFiles()` ordering matches the documented spec.
- [ ] Verify `ComposeFilesAll()` includes overlays of disabled services.
- [ ] Verify all four hard-error cases (unknown service, missing file, escape, `services.<name>.compose.extra` with unknown name) produce clear messages.
- [ ] Run full test suite: `make test`.
- [ ] Run `make lint` — no new violations.
- [ ] Run `make build` — embedded docs in sync.
- [ ] Manual smoke (Post-Completion): in a sample project, drop a `compose.local.yml` with the GIT_CONFIG_* env vars, reference it from local.yml, run `dwe docker config`, confirm env vars appear on the target container; then `dwe services disable <other-svc>` and confirm only that service's overlay disappears from the `-f` list (use `dwe docker --dry-run` or equivalent if available).

### Task 8: Move plan to completed

- [ ] Verify all checkboxes above are marked `[x]`.
- [ ] `mkdir -p docs/plans/completed`.
- [ ] `git mv docs/plans/20260603-local-compose-overlays.md docs/plans/completed/`.

## Post-Completion

*Items requiring manual intervention — no checkboxes, informational only.*

**Manual verification:**
- In a real project with `dev` (tool) and `postgres` (infra) services, create `workspace/local.yml` with the GIT_CONFIG_* example and run:
  - `dwe docker config` — verify env vars are merged onto the `dev` container.
  - `dwe services disable postgres` — verify postgres's local overlay (if any) drops out of subsequent `-f` invocations.
  - `dwe deploy run` — verify the full flow still succeeds with overlays in place.
- Confirm with a teammate that nothing in their (overlay-less) `local.yml` breaks after pulling these changes.

**External system updates:**
- None. Self-contained CLI change. No consuming projects, no deployment configs, no third-party integrations.

**Documentation surface to watch in follow-up reviews:**
- If `dwe init` ever scaffolds a `local.yml`, consider adding a commented-out `compose.extra` example. Out of scope for this plan.
- If `docs/i18n/ru/internals/` is created later, port the `packages.md` invariant entry then.
