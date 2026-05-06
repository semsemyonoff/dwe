# IDE rendering: explicit per-service policy with extends-aware collision resolution

## Overview

`devbox render ide` currently iterates *all* declared services in `cfg.Services` (regardless of `Enabled`), writes `<svc.Dir>/.devcontainer/devcontainer.json` per service, and relies on hardcoded `{{ if eq .Service "main" }}` switches inside templates to inject service-specific overlays. When two services share the same `Dir` (e.g. `main` and `main-debug` where the latter `extends: main`), this produces last-writer-wins output — and because the template's name-switch fires on the *parent* service while `.ServiceCfg.Container` still emits the child's container name, the resulting `devcontainer.json` is self-inconsistent and Zed reports `DevContainerParseFailed`.

This plan eliminates the implicit "all services participate" rule and the in-template name conditionals. It introduces:

1. **An explicit `ide:` block on `ServiceConfig`** — services opt in/out of IDE rendering with `ide.enabled`. Default is `true` only for `type: app`; everything else defaults to `false`. Selection also gates on the existing `svc.Enabled` (3-layer activation) — disabling either suppresses rendering.
2. **`extends` inheritance for the `ide:` block** — children inherit `ide.enabled` and `ide.template` from their parent unless they override, matching the field-by-field inheritance convention already in `LoadServicesConfig`.
3. **Per-service template subdirectories** with a 3-step resolution fallback (explicit override → by service name → global). Each variant ships its own clean template; no name-based switches.
4. **Extends-aware `Dir` collision resolution** — when several enabled services resolve to the same `Dir` (after `filepath.Clean` normalization), the most-derived service (deepest `extends` chain) wins. No new priority knob — `extends` already expresses "this is a more specific variant of that."

This aligns IDE rendering with the broader architectural goals: explicit policy over implicit iteration, no Make-as-DSL conditionals in templates.

## Context (from discovery)

- **Files involved (this repo only — `devbox-cli`)**:
  - `internal/config/devbox.go` — `ServiceConfig` (line 306), `Extends` field (line 316), `LoadServicesConfig` with field-by-field manual inheritance loop (lines 592–636), topo sort that already resolves multi-level `extends` chains (line 644+).
  - `internal/command/ide.go` — the IDE renderer (`renderIDEConfigs`, `loadIDETemplate`).
  - `internal/command/ide_test.go` — existing test scaffolding (`setupIDETemplates`, `makeIDECfg`).
  - `docs/reference/config/services.md` — services schema docs.
- **Patterns found**:
  - `LoadServicesConfig` does **manual field-by-field inheritance** — every new field that should be inherited must be added explicitly to the loop at line 592+. `IDE` will *not* inherit unless we add it. The `services.md` docs describe inheritance as the default behavior, so silently skipping `ide` would contradict documentation.
  - Tristate (default-by-type) requires presence detection. The cleanest path: `Enabled *bool` plus an accessor that resolves the default from `Type`.
  - `loadIDETemplate` already does `<root>/devbox/templates/ide/<name>.tpl` — the new resolver wraps this with two preceding lookup paths.
  - `Dir` is *not* cleaned by `LoadServicesConfig`; values like `./services/main` and `services/main` would pass through verbatim. Grouping must `filepath.Clean` before comparing.
  - `ServiceConfig.Enabled` is the computed `mandatory || services.<name>.enabled` flag from the 3-layer merge — this is independent of `IDE.Enabled` and represents whether the service runs at all.
- **Out of scope (separate `next-laravel` repo work)**: moving the laravel project's templates into `ide/main/`, adding `ide/main-debug/devcontainer.json.tpl`, re-rendering and verifying in Zed. This plan covers only the upstream `devbox-cli` changes.

## Development Approach

- **Testing approach**: Regular (code first, then tests).
- Complete each task fully before moving to the next.
- Make small, focused changes — one logical unit per task.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task.
- **CRITICAL: all tests must pass before starting next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation**.
- Run `go test ./internal/config/... ./internal/command/...` after each change.
- **Backward-compatibility surface** — call all of this out in docs:
  - **Enabled `type: app`** services with no `ide:` block continue to render (unchanged).
  - **Disabled `type: app`** services (declared in `services.yml` but `svc.Enabled == false` after the 3-layer merge) will *no longer* render — previously the iterator ignored `Enabled` and produced files anyway. This is intentional: writing IDE files for a service the user has turned off is misleading.
  - **Non-`app`** services (db/cache/queue/tool) with no `ide:` block will *no longer* render — previously they did. To restore, add `ide.enabled: true`.
  - **Sibling services sharing `dir`** now produce one file (the most-derived wins) instead of last-writer-wins. Behavior is now deterministic and a `lost-collision` warning explains what was skipped.

## Testing Strategy

- **Unit tests**: required for every task. Existing pattern: table-driven tests in `internal/command/ide_test.go` and `internal/config/devbox_test.go`.
- **No e2e tests** for the CLI in this repo — the laravel project is the manual e2e sandbox (and is out of scope here).
- Coverage targets: every new public function/method gets success + error tests; every new branch in modified functions gets a case.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, docs in `devbox-cli`.
- **Post-Completion** (no checkboxes): the laravel-side migration and Zed verification.

## Implementation Steps

### Task 1: Add `ServiceIDEConfig` type, `IDE` field, and `extends` inheritance
- [x] in `internal/config/devbox.go`, define `ServiceIDEConfig { Enabled *bool; Template string }` (yaml tags: `enabled`, `template`)
- [x] add `IDE ServiceIDEConfig \`yaml:"ide"\`` field to `ServiceConfig`
- [x] add accessor `(ServiceConfig).IDERenderEnabledExplicit() (enabled bool, explicit bool)` — returns `(*Enabled, true)` if pointer non-nil, else `(Type == "app", false)`. Keep a thin `IDERenderEnabled() bool` wrapper that drops the second return value for callers that don't need to distinguish
- [x] in `LoadServicesConfig`, extend the inheritance loop at line 592+: `if svc.IDE.Enabled == nil { svc.IDE.Enabled = parent.IDE.Enabled }` and `if svc.IDE.Template == "" { svc.IDE.Template = parent.IDE.Template }` — matches the field-by-field convention already in place
- [x] write tests in `internal/config/devbox_test.go` for `IDERenderEnabledExplicit` covering: explicit `true` (returns `true, true`), explicit `false` (`false, true`), omitted on `type: app` (`true, false`), omitted on `type: db` (`false, false`), omitted on empty type (`false, false`)
- [x] write inheritance tests via `LoadServicesConfig` on a temp `services.yml`: parent with `ide.enabled: false` + child without ide block → child resolves to disabled; parent with `ide.template: foo` + child without override → child sees `foo`; child with `ide.enabled: true` overrides parent's `false`; child with `ide.template: bar` overrides parent's `foo`
- [x] write a YAML round-trip test that decodes a `services.yml` snippet with `ide.enabled` and `ide.template` and asserts the values
- [x] run `go test ./internal/config/...` — must pass before Task 2

### Task 2: Add `selectIDEServices` helper — filter + dir-normalization + extends-aware collision resolution
- [x] in `internal/command/ide.go`, add unexported `selectIDEServices(services map[string]config.ServiceConfig) (selected []string, skipped []skippedService)` where `skippedService` carries `Name`, `Reason` (`disabled-by-policy` | `empty-dir` | `lost-collision`), and reason-specific context fields `Dir` (set for `lost-collision`) and `Winner` (set for `lost-collision`). Empty for other reasons. Caller renders a single deterministic warning per skipped entry using these fields
- [x] step A — **gate on both flags**: drop services where `svc.Enabled == false` (3-layer activation; mandatory services are always `Enabled == true` per loader contract) **or** `svc.IDERenderEnabled() == false` (IDE policy). Both checks are required: a `type: app` service that's been disabled at the project level should not render IDE files, and a non-`app` service that's enabled at the project level must still opt in via `ide.enabled: true`. Record drops with reason `disabled-by-policy`
- [x] step B — **normalize `Dir`**: drop services where `strings.TrimSpace(svc.Dir) == ""` with reason `empty-dir` (IDE rendering at the project root is almost certainly a misconfiguration; better to surface than silently write — `filepath.Clean("")` returns `"."`, so we cannot rely on the post-clean value being empty). For survivors, group by `filepath.Clean(svc.Dir)` so that `./services/main` and `services/main` collapse into a single group
- [x] step C — **extends depth**: for each `Dir` group with >1 member, compute extends depth by walking `svc.Extends` through the services map; cap at depth 32 (loader already rejects cycles, but defense-in-depth) and keep the deepest; tie-break by lexicographic name for determinism. Losers go into `skipped` with reason `lost-collision`
- [x] return `selected` sorted lexicographically; `skipped` sorted by name
- [x] write table-driven tests in `internal/command/ide_test.go` covering:
  - all enabled distinct dirs → all kept
  - `IDERenderEnabled() == false` → dropped (reason `disabled-by-policy`)
  - `svc.Enabled == false` but `IDERenderEnabled() == true` → dropped (reason `disabled-by-policy`) — this is the case the original bug analysis missed
  - mandatory app service constructed with `Enabled: true` (the selector receives already-resolved `ServiceConfig` values from `LoadConfig`, which normalizes `mandatory: true` to `Enabled: true`; the selector itself does *not* re-derive from `Mandatory`) → kept. Test either drives through `LoadConfig` on a fixture `services.yml`, or constructs the map inline with `Enabled: true` set explicitly
  - two services share dir; one `extends` the other; both `Enabled` → child wins, parent `skipped` with reason `lost-collision`
  - same as above but with `./services/main` vs `services/main` (verifies `filepath.Clean` normalization)
  - three-level chain `c extends b extends a` all sharing dir → c wins
  - tie on equal depth (two unrelated services share dir, neither extends the other) → lexicographic winner; loser `skipped`
  - empty `Dir` → dropped (reason `empty-dir`)
- [x] run `go test ./internal/command/...` — must pass before Task 3

### Task 3: Add `resolveIDETemplate` helper for 3-step template fallback
- [x] in `internal/command/ide.go`, add unexported `resolveIDETemplate(projectRoot string, svc config.ServiceConfig, serviceName, fileBase string) (path string, contents []byte, err error)`
- [x] **`ide.template` is a single directory key, not a path** — validate with an unexported `validateIDETemplateKey(s string) error`: reject if it contains a path separator (`/` or `\`), is `..` or contains `..` segments, is absolute (`filepath.IsAbs`), or starts with `.`. Same constraint applies to `serviceName` (already alphanumeric in practice from existing `LoadServicesConfig` validation, but defense-in-depth: re-validate at the resolver). Invalid `ide.template` → return a wrapped error from `resolveIDETemplate` that *is not* `os.ErrNotExist`, so callers surface it instead of silently skipping
- [x] try `<root>/devbox/templates/ide/<svc.IDE.Template>/<fileBase>.tpl` if `svc.IDE.Template != ""` (after key validation)
- [x] then `<root>/devbox/templates/ide/<serviceName>/<fileBase>.tpl`
- [x] then `<root>/devbox/templates/ide/<fileBase>.tpl` (current global behavior)
- [x] return `os.ErrNotExist` (wrapped) only when *all three* are missing, so existing `errors.Is(err, os.ErrNotExist)` skip-with-warning logic in callers keeps working
- [x] keep `loadIDETemplate` for back-compat callers if any; the new helper is what `renderIDEConfigs` uses
- [x] write tests covering: only global exists (used), only by-name exists (used), only explicit-template exists (used), explicit beats by-name beats global precedence, none exist (returns wrapped `os.ErrNotExist`), empty `svc.IDE.Template` skips step 1, **invalid `ide.template`** values rejected with non-`ErrNotExist` error: `"foo/bar"`, `"../escape"`, `"./foo"`, `"/abs/path"`, `".."`, `".hidden"`, `"foo/../bar"`, **invalid `serviceName`** for the by-name fallback: at least one case where the service name itself fails `validateIDETemplateKey` (e.g. simulated name `"../oops"` or `"a/b"`) — assert the resolver returns a non-`ErrNotExist` error rather than silently falling through to the global template (defense-in-depth: even though `LoadServicesConfig` already restricts service names, the resolver must not assume that)
- [x] run `go test ./internal/command/...` — must pass before Task 4

### Task 4: Wire selector and resolver into `render ide`
- [ ] in `newRenderIDECmd.RunE`, when no service argument is given, call `selected, skipped := selectIDEServices(cfg.Services)` and use `selected` as the iteration list. For each `skipped` entry, emit a `w.Warning` with a reason-specific message: `disabled-by-policy` → `"ide [<name>] — skipped (ide.enabled is false or service is disabled)"`; `empty-dir` → `"ide [<name>] — skipped (service has no dir)"`; `lost-collision` → `"ide [<name>] — skipped (dir <dir> rendered by <winner>)"`
- [ ] when an explicit service argument is given:
  - validate it exists in `cfg.Services` (existing behavior)
  - if `strings.TrimSpace(svc.Dir) == ""`, return error `service %q has no dir; cannot render IDE files`
  - if `svc.Enabled == false`, return error `service %q is disabled at the project level`
  - if `IDERenderEnabled()` is false, distinguish via `IDERenderEnabledExplicit`: explicit-false → `service %q has ide.enabled: false`; default-by-type-false → `service %q (type: %s) does not participate in IDE rendering by default; set ide.enabled: true to opt in`
  - do not silently rewrite to its parent — explicit naming is explicit
- [ ] in `renderIDEConfigs`, replace each `loadIDETemplate(projectRoot, "<base>")` call with `resolveIDETemplate(projectRoot, svc, name, "<base>")`
- [ ] keep existing `errors.Is(err, os.ErrNotExist)` warning paths for missing templates
- [ ] update existing tests in `internal/command/ide_test.go` that constructed services without setting `Type` — set `Type: "app"` (or explicit `IDE.Enabled: ptr(true)`) so they continue to render under the new default
- [ ] add an integration test: cfg with `main` (`type: app`, `dir: services/main`, `Enabled: true`) and `main-debug` (`type: app`, `extends: main`, `dir: services/main`, `Enabled: true`) — render writes the `main-debug` variant to `services/main/.devcontainer/devcontainer.json` and emits a `lost-collision` warning for `main`; flip `main-debug.IDE.Enabled` to `&falseVal` and re-render, expect `main` variant and a `disabled-by-policy` warning for `main-debug`
- [ ] add integration tests for explicit-arg error paths: disabled-by-`Enabled`, disabled-by-`ide.enabled: false` (explicit), disabled-by-default-for-non-app (omitted on `type: db`), empty `Dir`. Assert the error messages distinguish the four cases
- [ ] run `go test ./internal/command/...` — must pass before Task 5

### Task 5: Update services config docs
- [ ] in `docs/reference/config/services.md`, add an `ide:` subsection documenting the schema (`enabled`, `template`), the default-by-type policy, the inheritance rule (children inherit `ide.enabled` and `ide.template` from `extends` parent unless overridden — matches existing field-by-field inheritance docs), the extends-aware collision rule, and the `ide.template` constraints (single directory key — no separators, no `..`, no absolute paths, no leading `.`)
- [ ] add a short example showing two services sharing a `dir` and which renders
- [ ] document that selection requires both `enabled: true` (project activation, computed from the 3-layer merge) **and** `ide.enabled: true` (IDE policy) — disabling either suppresses rendering
- [ ] document the explicit-arg error matrix (project-disabled vs ide-disabled-explicit vs ide-disabled-default vs empty-dir)
- [ ] note the **breaking** behavior change: services without an explicit `ide.enabled` whose `type` is not `app` will no longer get IDE files written; users who want them must set `ide.enabled: true`
- [ ] no test required for this task (docs only)

### Task 6: Verify acceptance criteria
- [ ] verify all four decisions from Overview are implemented (ide block, template fallback, extends-aware collision, default-by-type)
- [ ] verify breaking-change behavior is documented and tested
- [ ] run `make test` — full suite must pass
- [ ] run `make lint` — all issues must be fixed
- [ ] run `make build` — binary builds cleanly
- [ ] manually inspect `selectIDEServices` and `resolveIDETemplate` for unused branches or dead code (the simplify pattern)

## Technical Details

**Schema additions (services.yml)**:
```yaml
services:
  main:
    type: app
    dir: ./services/main
    # ide block omitted → enabled (type: app default)

  main-debug:
    type: app
    extends: main
    dir: ./services/main
    ide:
      template: main-debug   # optional explicit override
```

**Resolution semantics** (in order):

1. *Filter*: keep services where **both** `svc.Enabled == true` (3-layer activation; mandatory services are always true) **and** `IDERenderEnabled() == true` (`*svc.IDE.Enabled` if set, else `svc.Type == "app"`).
2. *Group*: by `filepath.Clean(svc.Dir)` — `LoadServicesConfig` does not clean `Dir`, so `./services/main` and `services/main` would otherwise hash to different groups while writing to the same destination. Empty `Dir` (clean → `.`) is dropped with a warning, not silently skipped.
3. *Pick*: deepest `extends` chain; tie-break by name. Losers are reported as `lost-collision` warnings naming the winner.
4. *Render*: for each surviving service and each enabled IDE family, look up template via 3-step fallback.

**Inheritance** (handled inside `LoadServicesConfig`'s manual inheritance loop, not the renderer):
- `IDE.Enabled` (pointer): inherited from parent if child's pointer is nil; child's explicit `false` or `true` overrides.
- `IDE.Template`: inherited from parent if child's value is empty; non-empty child overrides.

**`ide.template` constraints**: a single directory key under `devbox/templates/ide/`, not a subpath. Path separators (`/`, `\`), `..` segments, absolute paths, and leading `.` are rejected by `validateIDETemplateKey`. Subpaths are *not* allowed — if users want deeper nesting, they should restructure their template directory. Rationale: keeps the resolver predictable (one filesystem stat per step), prevents traversal out of the project, and discourages template trees that smuggle service-specific logic into directory layouts.

**Explicit-arg error taxonomy** (when user runs `devbox render ide <name>`):
- service unknown → existing `service %q not found in config`
- `strings.TrimSpace(svc.Dir) == ""` (checked *before* `filepath.Clean`, since `filepath.Clean("")` returns `"."` not `""`) → `service %q has no dir; cannot render IDE files`
- `svc.Enabled == false` → `service %q is disabled at the project level`
- `IDERenderEnabledExplicit` returns `(false, true)` → `service %q has ide.enabled: false`
- `IDERenderEnabledExplicit` returns `(false, false)` → `service %q (type: %s) does not participate in IDE rendering by default; set ide.enabled: true to opt in`

**`extends` depth computation**: a plain helper `extendsDepth(services map[string]config.ServiceConfig, name string) (depth int, capped bool)` walks the parent pointer through `services[svc.Extends]`, counting hops. Cap at 32 hops as a paranoid guard (loader already rejects cycles, but defense-in-depth). On hitting the cap, returns `(32, true)`; otherwise `(depth, false)`. Selector treats a `capped` result the same as the maximum depth for tie-break purposes — the cycle would have been rejected upstream, so this is effectively unreachable in production. Tests can assert `capped == false` for normal chains and (optionally) construct a synthetic cyclic map directly to verify the cap holds.

**Why pointer for `Enabled`**: a bare `bool` defaults to `false` on YAML decode, indistinguishable from "user wrote `enabled: false`" vs "user omitted the field." The pointer makes the tristate explicit and `IDERenderEnabled()` is the only place the default-by-type rule lives.

**Error vs warn for missing template**: stays warn (current behavior). If *all three* lookup paths miss, we keep the existing skip-with-warning. This is a deliberate choice — IDE rendering is best-effort and shouldn't fail the command.

## Post-Completion

*Items requiring manual work outside this repo — informational only, no checkboxes.*

**Downstream `next-laravel` migration** (separate PR in that repo):
- Add `ide:` blocks to `services.yml` where defaults need overriding (likely just `main-debug` if it should opt in/out).
- Move current `devbox/templates/ide/devcontainer.json.tpl` into `devbox/templates/ide/main/devcontainer.json.tpl`, stripping the `{{ if eq .Service "main" }}` switch.
- Add `devbox/templates/ide/main-debug/devcontainer.json.tpl` for the debug variant — Zed-specific settings, pre-installed Node.js refs, future Claude-Code-in-container wiring; reference `compose/services/main/debug.yml` via `.ServiceCfg.Compose` (no name conditional).
- Run `devbox render ide` and confirm `services/main/.devcontainer/devcontainer.json` is consistent in both states (`main-debug.enabled: true` → debug variant; `: false` → plain `main` variant).
- Open Zed; verify it parses the dev container without `DevContainerParseFailed`.

**Manual verification (this repo)**:
- After installing the new binary in a project that previously relied on the old "render everything" behavior, confirm services now opt out are intentionally absent.
