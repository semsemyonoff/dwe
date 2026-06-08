# Service Config Rendering + Generated-Once Values

## Overview

Migrate service configuration materialization from the legacy **copy** mechanism
(`service_configs_copy` copying a fully-baked file from `configs/services/<svc>/`)
to the existing **render** subsystem (template packs + manifests + `.local`
overrides). Configs become pure render outputs derived from config + a durable
value store.

Alongside, add an opt-in **generated-once** mechanism for service-minted secrets
(Laravel `APP_KEY`, Magento `crypt.key`, …). The model is **harvest, not mint**:
the *service* generates the value (e.g. `php artisan key:generate`) writing it
into its own file; DWE **reads it back** ("harvest") into a durable per-service
store (`.dwe/generated.yml`) and **replays** it on every subsequent render via a
`${generated.<name>}` template/command namespace. A `generated-missing` predicate
gates the service's generation step so it runs only when the store is empty.

Benefits: configs are reproducible from source-of-truth, the bind-mount /
`mountpoint` / virtiofs nested-mount machinery is retired (render writes straight
into the already-mounted `src/` tree), and secrets survive `run`/redeploy while
staying out of git.

This is a **transitional** change: the copy mechanism keeps working but emits
deprecation warnings; its removal is a separate phase-2 (major) effort.

## Context (from discovery)

Files/components involved (anchors verified against code):
- **Render**: `internal/cli/render/` (env/ide/ai/git subcommands), `internal/core/execution/templates/` (`manifest/`, `packroot/`, `packcommon/` — `TemplateData` @ packcommon.go:96, raw `{{ }}` substrate — , `ide/`, `ai/`, `git/`), `internal/shared/tpl/` (engine; `${...}` `CompileVarSyntax` @ render_command.go:112-160; `RenderContext` @ render_command.go:20-40; `resolve` takes `.Raw` map @ :93/:158; `resolveMap .Params "key"` @ :263).
- **Copy (to deprecate)**: `internal/core/execution/builtin/services/configs_copy.go`, `configs_check.go`, `services/services.go` (`Builtins()` @ L8-11).
- **Config schema**: `internal/core/project/config/workspace.go` (`ServiceConfig`, `ServiceConfigEntry`; `allowedFieldsFor` signature @ L791, **app field list @ L804-808 with `render` already at L807**).
- **State pattern to mirror**: `internal/core/workflow/deploy/journal/state.go` (atomic Save; `DefaultRelPath = ".dwe/deploy/state.yml"` @ :13), `internal/shared/promptcache/` (leaf store IO).
- **Pipeline / conditions / builtins**: `internal/core/execution/pipeline/resolve.go` (`resolveStepWhen` @ :258); `internal/core/workflow/deploy/journal/decision.go` (**the `hasCheck → Run` skip-bypass lever @ :38-62, esp. `if hasCheck { return Run }` @ :56-58**); `internal/core/execution/pipeline/executor.go` (action-hash skip; post-step `check:` exec @ :861-892); `internal/core/execution/builtin/builtin.go` (`buildRegistry` @ :92, composes `services.Builtins()`); `builtin/spec/spec.go` (`Builtin` interface `Validate`/`Describe`/`Run` @ :42-86; `ExecContext.Output` is `*render.Writer` @ :30); `internal/core/execution/condition/condition.go` (**`EvalBuiltin` @ :72; `SplitN(predicate," ",2)` @ :74; `switch verb` @ :85-101** with `dir-empty`/`file-exists`/`file-missing`; single-path join @ :81-83).
- **Run/deploy/reset**: `internal/core/workflow/lifecycle/run.go` (`renderAndSourceDotEnv` call @ :134, def @ :345; deployment gate ~:265 — tracked services must be `StatusDeployed`), `internal/cli/lifecycle/run.go`, `internal/cli/deploy/deploy.go`, `internal/cli/lifecycle/reset.go` (`newResetRunCmd` @ :119, `resetRunCmd` @ :157, `resetServiceRunCmd` @ :260, `--yes` @ :151), reset builtins in `builtin/containers/` (`docker_remove_project_volumes`, `docker_stop_remove_container` @ containers.go:16-20). `render.Writer.Confirm` @ output.go:175, `Warning` @ output.go:63.
- **Validation**: `internal/core/validate/validate.go` (`Validator`, `All()`), `validate/diag/diag.go` (`Diagnostic`/`Severity`), `validate/config/workspace.go` (`servicesAllowedFields` @ L110, `render` at L117).
- **Docs**: `docs/reference/config/services/fields.md`, `docs/reference/config/deploy/builtins.md`, `docs/reference/config/conditions.md`, `docs/reference/render/`.

Related patterns found:
- Render packs resolve by convention (service-name → `extends` chain → `default`) plus an optional `render.<kind>.template` pin and a gitignored `<pack>.local/` file-substitution sibling.
- `${...}` shorthand is rewritten to `{{ resolve … }}` by `tpl.CompileVarSyntax`; engine is hermetic (no crypto/random) — so generation cannot live in templates (validates harvest-not-mint).
- Builtins register per-domain via `Builtins()` maps; predicates live in a **separate** registry (`condition.go`) and are runtime-evaluated through `sh -c`.
- `service.yml` uses strict `KnownFields(true)`; new fields require BOTH `allowedFieldsFor` (config) and `servicesAllowedFields` (validator).
- `service_configs_copy` is paired with `service_configs_check` as a `check:` so it re-runs every deploy (action-hash skip is bypassed when `hasCheck`).

Dependencies identified:
- `.dwe/` is fully gitignored (store is safe from commits). `/services/` is gitignored. Snapshot restore overwrites `.dwe/deploy/state.yml`; the new store is a separate file, intentionally untouched by snapshots.

## Development Approach
- **testing approach**: **Regular** (implement first, then table-driven tests alongside the code — DWE style: `*_test.go` next to code).
- complete each task fully before moving to the next; small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** (success + error scenarios); list tests as separate checklist items.
- **CRITICAL: all tests must pass before starting next task** — run `make test` (NOT `go test ./...`; the embedded-docs tree is generated).
- **CRITICAL: update this plan file when scope changes during implementation.**
- maintain backward compatibility — the copy mechanism must keep working (warnings only) until phase 2.

## Testing Strategy
- **unit tests**: required for every task (table-driven — store IO, regex harvest, namespace resolution, pack resolution, validators, predicate arg-splitting).
- **golden/fixture tests**: render output is path-sensitive; iterate services via `config.DeployOrder(...)` (never `range cfg.Services`) for deterministic golden output. Fixtures in package-local `testdata/`.
- **partial-implementation exception**: Task 7's "render re-runs on unchanged redeploy" test only passes once the paired `service_configs_render_check` exists (same task) — it is the test that motivates the check; keep it in Task 7.
- **no UI e2e**: CLI project; end-to-end Laravel/Magento validation is manual (Post-Completion).
- after docs edits, `make build` re-embeds docs; docs-subsystem tests run under `make test`.

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix; blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Solution Overview

**Shared core, four entry points.** Two core functions — `RenderConfigs(cfg, service, store)` (resolve pack → render templates → write files under `src/`, mode `replace`) and `HarvestGenerated(cfg, service, store)` (iterate the service's `generated:` fields, read each output file, extract via regex, write-if-absent into the store) — are called by: (1) pipeline builtins `service_configs_render` / `service_generated_harvest`; (2) CLI `dwe render config [service]` (+ `--harvest`); (3) the `dwe run` preamble (auto re-render). The store (`.dwe/generated.yml`) is leaf infra mirroring `promptcache`.

**Deploy flow:** `service_configs_render` (carrying a `check:` → `service_configs_render_check`, so it always re-runs and template edits / store clears apply) → service generate command gated by `when: { type: builtin, cmd: "generated-missing <svc> <field>" }` → `service_generated_harvest`. Invariant: *store empty for a key ⟺ value re-minted*. Render is `replace`.

**Lifecycle:** value survives `run`/redeploy; `reset` preserves the store by default, with opt-in clearing via `--clear-generated` (+ interactive prompt). Rotation = clear + redeploy.

Key design decisions and rationale:
- **Harvest not mint** — the engine is hermetic; reusing the service's own generator is format-agnostic from DWE's side (DWE only reads a string).
- **`pattern` (regex, capture group 1)** instead of a format `type` enum — we never enumerate config formats and never *write* foreign formats, only read one string.
- **Declaration in `service.yml`, not the manifest** — generated values are per-service lifecycle state, orthogonal to whether DWE renders the file (replay can target a command arg, not just a template).
- **`${...}` substrate for config templates** — config templates use the `${...}` shorthand (user/devbox expectation) via `tpl.CompileVarSyntax`+`RenderContext`, a deliberate divergence from the raw-`{{ }}`/`packcommon` substrate used by ide/ai/git, justified by config-file ergonomics and `${APP_*}`/`${DB_*}` parity.
- **Render into `src/` directly** — `src/` is already dir-mounted; no per-file bind mount / `mountpoint`.

## Technical Details

**Store schema** (`.dwe/generated.yml`, no `schema_version`):
```yaml
services:
  main:
    app_key: "base64:Xa3…=="
  magento:
    crypt_key: "241f4fa60be8f69638343cacc5a1a192"
```
String values (block scalars for multi-line). Atomic write (temp + rename). Missing file → empty store; corrupt file → surfaced error (NOT swallowed).

**`service.yml` additions** (both kept parseable alongside deprecated `configs:`):
```yaml
render:
  config:
    template: laravel          # optional pin; else convention (name → extends → default) + .local
generated:
  app_key:
    file: configs/.env         # output file, path relative to service hub (svc.Dir)
    pattern: '^APP_KEY=(.*)$'   # regex; capture group 1 = value
```

**Template namespace (corrected to match the real `tpl` API):** add a `case "generated":` in `CompileVarSyntax` routing to a NEW lenient resolver (mirroring `resolveMap .Params "key"` @ render_command.go:263), e.g. `${generated.app_key}` → `{{ resolveGenerated .Generated "app_key" }}`; add a `Generated map[string]string` field to `tpl.RenderContext`. **Absent key → `""`** — consistent with the existing `${...}` resolvers, which are ALL lenient (`resolve`/`resolveMap` return `""` for missing paths, render_command.go:248-270); there is no strict-vs-lenient distinction. (The earlier `{{ resolve "generated" "X" }}` shape was wrong: `resolve` takes the `.Raw` map, not a namespace literal.)

**Config render substrate (DESIGN POINT — RESOLVED):** config templates are rendered via `tpl.CompileVarSyntax` over a per-service `tpl.RenderContext`, NOT via `packcommon.TemplateData`/raw-`{{ }}`. Config values are referenced with **bare `${<dotpath>}`** — `CompileVarSyntax` compiles `${X}` to `{{ resolve .Raw "X" }}`, so there is **NO `raw.` prefix** (codex: `${raw.services...}` would wrongly look up `Raw["raw"]`). Per-service fields use `${services.<name>...}` (e.g. `${services.main.ports.http}` — the confirmed working form), top-level config uses `${databases.magento}` etc. ⚠️ `${services.<name>...}` exposes only the **CURATED subset** injected by `injectServicesIntoRaw` (call @ workspace.go:1420; type/container/dirs/configs/ports/hosts/…), NOT `render`/`generated`/arbitrary merged fields; an omitted field renders `""` (lenient). So config templates stick to the injected subset or `injectServicesIntoRaw` is extended (decided in Task 3). A singular current-service `${service....}` binding does NOT exist and would need a new `case "service":`. `${generated.<name>}` supplies harvested values. Settled before Task 3 so Task 3 threads `Generated` into the right struct.

**Predicate:** `generated-missing <svc> <field>` — runtime, reads `.dwe/generated.yml`, true when the field is absent (gates the generate step). NOTE the two-arg parsing caveat (Task 8).

**Processing flow (deploy, first time):** render writes `APP_KEY=` (store empty) → gate open → service mints `APP_KEY=base64:…` → harvest captures. **Subsequent:** render replays stored value → gate closed → generate skipped → harvest no-op (write-if-absent). Render re-runs every deploy via its `check:`.

## What Goes Where
- **Implementation Steps** (`[ ]`): all code, tests, and in-repo docs.
- **Post-Completion** (no checkboxes): manual end-to-end validation against a real project (e.g. `vto`), migrating real projects' configs to packs, and the phase-2 removal of deprecated copy code.

## Implementation Steps

### Task 1: Generated-value store (leaf IO package)

**Files:**
- Create: `internal/shared/generatedstore/store.go`
- Create: `internal/shared/generatedstore/store_test.go`

- [x] define `Store` (`map[string]map[string]string`, service → field → value) and `DefaultRelPath = ".dwe/generated.yml"`
- [x] implement `Load(path)` (missing → empty store; corrupt → error, NOT swallowed) and `Save(path)` (atomic temp + rename, mirror `journal.Save`)
- [x] implement accessors: `Has(svc, field)`, `Get(svc, field)`, `SetIfAbsent(svc, field, val) bool`, `ClearService(svc)`, `ClearAll()`, `IsEmpty()`
- [x] write tests for Load/Save round-trip, missing-file, corrupt-file error, atomicity, multi-line block scalar
- [x] write tests for SetIfAbsent (no-overwrite), Has/Get, ClearService vs ClearAll scoping
- [x] run `make test` — must pass before next task

### Task 2: `service.yml` schema — `render.config` + `generated:` (+ allowlists)

**Files:**
- Modify: `internal/core/project/config/workspace.go`
- Modify: `internal/core/validate/config/workspace.go`
- Modify/Create: `internal/core/project/config/*_test.go`

- [x] add a `Config *RenderConfigSection` (`template string`) field to the typed `ServiceRenderConfig` struct (workspace.go:548-552) — render sub-fields are struct-enforced under top-level `KnownFields(true)`, NOT a nested allowlist map, so `render` itself needs no allowlist change
- [x] add `Generated map[string]GeneratedField` (`File`, `Pattern`) to `ServiceConfig`; since `generated` is a NEW top-level service field, add it to the app field allowlist in `allowedFieldsFor` (**@ L804-808; `render` already at L807**) AND `servicesAllowedFields` (**@ L110; `render` at L117**)
- [x] keep deprecated `configs:` in both allowlists so strict decode doesn't hard-error
- [x] write tests: `service.yml` with `render.config` + `generated:` decodes; deprecated `configs:` still decodes; unknown sibling still hard-errors
- [x] run `make test` — must pass before next task

### Task 3: Config render context + `generated` namespace

**Files:**
- Modify: `internal/shared/tpl/render_command.go` (`CompileVarSyntax`, `RenderContext`, resolvers)
- Modify (only if config needs more service fields): `internal/core/project/config/workspace.go` (`injectServicesIntoRaw`)
- Modify/Create: `internal/shared/tpl/*_test.go`

- [x] add a `Generated map[string]string` field to `tpl.RenderContext` (per-service generated values for the render pass)
- [x] add `case "generated":` to `CompileVarSyntax` routing to a resolver (mirror `resolveMap` @ :263) returning the value or `""` if absent. ⚠️ **(codex) all `${...}` resolvers are ALREADY lenient** — `resolve`/`resolveMap` return `""` for missing paths (render_command.go:248-270), so `generated` just follows the same convention; there is NO strict-vs-lenient distinction to enforce
- [x] ⚠️ **(codex) use `${services.<name>...}`, NOT `${raw.services...}`** — there is no `raw` namespace; `${X}` → `{{ resolve .Raw "X" }}` and services live at `Raw["services"]` (confirmed form `${services.app.ports.http}`). This exposes only the **CURATED subset** from `injectServicesIntoRaw` (type/container/dirs/configs/ports/hosts/…), NOT `render`/`generated`/arbitrary fields; an omitted field renders `""` (lenient). Decision: (a) restrict to the injected subset and document it — no `injectServicesIntoRaw` extension needed for Task 3 (`${generated.*}` covers the per-service generated values; config templates use the injected subset for service fields). Top-level config uses bare `${databases.x}` etc.; a singular `${service....}` would need a new `case "service":`
- [x] write tests: `${generated.x}` resolves from `RenderContext.Generated`; absent → `""` (consistent with existing `.Raw` leniency); `${services.<name>.<injected-field>}` resolves; an uninjected field renders `""` (documents the limitation); namespaces coexist
- [x] run `make test` — must pass before next task

### Task 4: Config render kind — pack resolution + `RenderConfigs` core

**Files:**
- Create: `internal/core/execution/templates/config/config.go`
- Create: `internal/core/execution/templates/config/config_test.go`
- Create: `internal/core/execution/templates/config/testdata/...`

- [ ] implement `config` pack resolution (convention service→`extends`→`default` + `.local` via `packroot.Resolve`) and manifest load/validate (`manifest.File`, `render: from→to`)
- [ ] implement `RenderConfigs(cfg, service, store)`: build the per-service `tpl.RenderContext` (merged config + `Generated` = store[service], per Task 3); render each manifest entry via `CompileVarSyntax`+engine into `<svc.Dir>/<to>` where `to` is the manifest entry (authors target the app tree by writing `to: src/...` — `src/` is a usage convention, NOT a hardcoded join), mode **replace**; pathsafe destination guards
- [ ] iterate services via `config.DeployOrder(...)` where multiple are processed (deterministic golden output)
- [ ] write tests: pack resolution + `.local` override; render writes under `src/`; `${generated.x}` replay from store; replace overwrites; path-escape rejected
- [ ] run `make test` — must pass before next task

### Task 5: `HarvestGenerated` core + regex extraction

**Files:**
- Create: `internal/core/execution/templates/config/harvest.go`
- Create: `internal/core/execution/templates/config/harvest_test.go`

- [ ] implement `HarvestGenerated(cfg, service, store)`: iterate the service's `generated:` fields, read `<svc.Dir>/<file>` (pathsafe), apply `pattern` (regex, capture group 1), `SetIfAbsent` into the store, then `Save`
- [ ] handle errors precisely: missing file, no regex match, no capture group — surface, don't silently skip
- [ ] write tests: extract from a dotenv file and a php-array file; write-if-absent (no overwrite); missing file / no-match / no-capture errors
- [ ] write tests: multi-field service harvests all declared fields
- [ ] run `make test` — must pass before next task

### Task 6: CLI `dwe render config [service]` (+ `--harvest`)

**Files:**
- Create: `internal/cli/render/config.go`
- Modify: `internal/cli/render/render.go` (command tree)
- Create: `internal/cli/render/config_test.go`

- [ ] add `config` subcommand under `dwe render` (optional `[service]` → one; none → all eligible; same pack resolution as ide/ai/git; read-only wrt locks — no preflight/locks)
- [ ] default action renders via `RenderConfigs`; `--harvest` flag switches to a **harvest-only** pass (`HarvestGenerated`, NO render) — for bootstrapping an existing project's already-committed values
- [ ] resolve store path from baseDir; route output through the standard writer (cli is the single stdout writer); note this builtin/command is opt-in (no `dwe init` scaffold wiring, matching today's user-authored copy)
- [ ] write tests: render path renders expected files; `--harvest` populates store from on-disk values without rendering; service selection (one vs all)
- [ ] run `make test` — must pass before next task

### Task 7: Pipeline builtins — `service_configs_render` (+ check) + `service_generated_harvest`

**Files:**
- Create: `internal/core/execution/builtin/services/configs_render.go`
- Create: `internal/core/execution/builtin/services/generated_harvest.go`
- Modify: `internal/core/execution/builtin/services/services.go` (`Builtins()` @ L8-11)
- Create/Modify: `internal/core/execution/builtin/services/*_test.go`

- [ ] implement `service_configs_render` (KindAction; `with: {service, mode?}`, default `replace`) → `RenderConfigs`; `Validate`/`Describe`/`Run`
- [ ] implement `service_configs_render_check` (KindAction, used as a `check:`) verifying rendered outputs exist/are current — its PRESENCE forces the render step to re-run every deploy via the `hasCheck → Run` lever in `journal/decision.go:38-62` (`if hasCheck { return Run }`); mirrors `service_configs_copy`+`service_configs_check`. The user pairs it on the render step via `check:`.
- [ ] implement `service_generated_harvest` (KindAction; `with: {service}`) → `HarvestGenerated`
- [ ] register all three in `services.Builtins()`
- [ ] write tests: `Validate` rejects bad params; `Run` renders/harvests against a temp project; **render re-runs on an unchanged redeploy when paired with the check** (this test fails until the check builtin exists — it motivates it); registry has no dup panic
- [ ] run `make test` — must pass before next task

### Task 8: `generated-missing` predicate

**Files:**
- Modify: `internal/core/execution/condition/condition.go` (`EvalBuiltin` @ :72; add `case "generated-missing":` in `switch verb` @ :85-101)
- Modify/Create: `internal/core/execution/condition/condition_test.go`

- [ ] add `case "generated-missing":` — ⚠️ `EvalBuiltin` does `SplitN(predicate," ",2)` (@:74), so `parts[1]` is the **whole** remaining string and L81-83 would join it with `projectRoot` as a path. The case MUST **re-split `parts[1]` on whitespace** into `<svc>`/`<field>` itself — do NOT reuse the single-path `path`/`rel` variable
- [ ] validate exactly two sub-args (own error message); resolve store via `baseDir`(=projectRoot) + `generatedstore.DefaultRelPath`; true when field absent or store missing
- [ ] write tests: present → false; absent → true; missing store → true; wrong sub-arg count → error; correct 2-arg whitespace split
- [ ] update `docs/reference/config/conditions.md` predicate table (build deferred to Task 13)
- [ ] run `make test` — must pass before next task

### Task 9: `dwe run` preamble auto-render

**Files:**
- Modify: `internal/core/workflow/lifecycle/run.go`
- Modify/Create: `internal/core/workflow/lifecycle/run_test.go`

- [ ] auto-render service configs via `RenderConfigs` (replay from store) in a helper, called **AFTER the deployment gate (~:265, tracked services must be `StatusDeployed`) AND after the post-pull cfg reload (~:235-246), but BEFORE lifecycle phases (~:288)** — NOT at the early `.env` render (:134). do NOT run generate/harvest on `run`
- [ ] ⚠️ **(codex) ordering IS the safety argument**: render must run only after the gate passes. At :134 it would run *before* the gate at :265, so a `reset --clear-generated` (which un-deploys: full reset removes state @ reset.go:241-246, per-service marks pending-deploy @ :455-461) followed by `dwe run` would blank secrets at :134 before the gate rejects the run. Post-gate placement is also post-reload, so a `git pull` that changed templates is not rendered stale
- [ ] ⚠️ **(codex) run-render must be non-destructive when replay data is absent**: the gate only proves `StatusDeployed` in the journal, NOT that the store holds the keys (a deleted/upgraded `.dwe/generated.yml` with a deployed journal is possible). So in the run-preamble, for each service declaring `generated:` keys, if ANY declared key is MISSING from the store, **skip that service's render** (emit a hint to run `dwe deploy`/harvest) instead of rendering blank. Deploy render stays lenient (first deploy mints); this guard is run-only
- [ ] write tests: `run` renders configs after the gate and replays stored values; **reset-cleared store → `dwe run` fails the gate WITHOUT rewriting/blanking any config file**; **deployed service + missing store key → run SKIPS that service's render (no blanking) with a hint**; **post-pull template change → configs re-rendered fresh before phases**; absent pack → skip (no error)
- [ ] run `make test` — must pass before next task

### Task 10: Reset `--clear-generated` + interactive prompt

**Files:**
- Modify: `internal/cli/lifecycle/reset.go` (`resetRunCmd` @ :157 / pipeline via `RunWithOptions` :217-231; `resetServiceRunCmd` @ :260; `resetConfirmFn` seam :321-327)
- Modify/Create: `internal/cli/lifecycle/reset_test.go`

- [ ] add `--clear-generated` flag; clear the store **only after the FULL reset succeeds — including the post-pipeline journal cleanup** (codex): `journal.Remove` for full reset (@ reset.go:241-246) / `journal.ReplaceServiceWithPending` for per-service (@ :455-461), NOT merely after `RunWithOptions` returns. Scoped by `--service`/all via `generatedstore.ClearService`/`ClearAll` + `Save`. Never clear if the pipeline OR the journal mutation failed — else a deployed-journal + empty-store mismatch makes the run gate trust a service with no secrets
- [ ] ⚠️ **(codex) full reset has NO command-level confirm to hook after**: confirmation is a PIPELINE step (`reset/defaults.go:13-22`), not a `resetRunCmd` prompt. So for interactive (TTY + store non-empty), decide the prompt at the **command level up front** — "also clear N generated values? (forces regeneration on next deploy) [y/N]", default No, remember the decision, then clear post-success. Per-service path reuses the existing `resetConfirmFn` seam (:321-327). Non-interactive: flag only, no prompt
- [ ] default behavior unchanged: store preserved
- [ ] write tests: flag clears scoped store after full success; **pipeline failure → store NOT cleared**; **journal-cleanup failure (force `journal.Remove`/`ReplaceServiceWithPending` to fail) → store NOT cleared**; non-interactive without flag preserves; interactive prompt decision honored (mock confirm via the real seam); empty store → no prompt
- [ ] run `make test` — must pass before next task

### Task 11: New validation for `generated:` / `render.config`

**Files:**
- Modify/Create: `internal/core/validate/config/` (validator for the new fields)
- Create: corresponding `*_test.go`

- [ ] validate `generated.<field>`: `pattern` required and compiles as a regex with **≥1 capture group** (else `SeverityError`); `file` is a pathsafe contained-relative path (no `../`); field name is a valid `${generated.<name>}` identifier
- [ ] validate `render.config.template` like ide/ai/git (warn if a pinned pack doesn't resolve)
- [ ] optional cross-check: `generated-missing <svc> <field>` in pipelines references a declared `generated:` field — reuse the same two-arg parser finalized in Task 8 (don't re-implement)
- [ ] write tests: invalid regex / missing capture group → error; path escape → error; bad field name → error; valid declaration → clean
- [ ] run `make test` — must pass before next task

### Task 12: Deprecation warnings for the copy mechanism

**Files:**
- Modify/Create: `internal/core/validate/config/` (deprecation validator)
- Modify: `internal/core/execution/builtin/services/configs_copy.go` (single-site runtime warning)
- Create/Modify: corresponding `*_test.go`

- [ ] `dwe validate` config-domain: emit `SeverityWarning` (with `File:Line` + migration hint) for `configs:`/`mountpoint` in `service.yml` and for `service_configs_copy`/`service_configs_check` in `deploy.yml`/`reset.yml`
- [ ] runtime: emit a SINGLE deprecation notice via `ectx.Output.Warning` (`*render.Writer.Warning` @ output.go:63) from **`ConfigsCopy.Run` only** — do NOT also warn from `ConfigsCheck.Run` (it runs as the copy step's check → would double-warn per step)
- [ ] confirm `SeverityWarning` does not change exit code (deploy/validate still succeed)
- [ ] write tests: validator emits warning at the right location; severity is Warning (non-fatal); exactly one runtime notice per copy step
- [ ] run `make test` — must pass before next task

### Task 13: Documentation + re-embed

**Files:**
- Create: `docs/reference/render/config.md`
- Modify: `docs/reference/config/services/fields.md` (add `render.config`, `generated:`; mark `configs:`/`mountpoint` deprecated)
- Modify: `docs/reference/config/deploy/builtins.md` (add `service_configs_render`/`service_configs_render_check`/`service_generated_harvest`; mark copy builtins deprecated)
- Modify: `docs/reference/config/conditions.md` (add `generated-missing` predicate)

- [ ] write the new reference pages and deprecation notices; cross-link `generated:` ↔ `render config` ↔ predicate; document the `${...}` config-template substrate
- [ ] run `make build` to sync + re-embed docs (`internal/core/docs/embedded/`) and regenerate content hashes
- [ ] adjust docs-subsystem golden references if any change
- [ ] run `make test` — must pass before next task

### Task 14: Verify acceptance criteria
- [ ] verify all Overview requirements: render replaces copy; `generated:` harvest+replay; render check-pairing re-runs; gate predicate; run auto-render; reset opt-in clear; deprecation warnings
- [ ] verify edge cases: empty-store bootstrap, store-clear → regeneration, render re-run on template edit, multi-field service, path-escape rejection, non-interactive reset, **run-render-after-gate (reset-cleared store does NOT blank on `dwe run`)**, **post-pull config re-render**, reset-failure does-not-clear-store
- [ ] run full suite: `make test` and `make lint`
- [ ] verify deterministic golden output (service iteration via `DeployOrder`)
- [ ] verify coverage on new packages meets project norm

### Task 15: [Final] Update project docs and close out
- [ ] update `AGENTS.md` "Critical Patterns" with the generated-store / config-render contract (render check-pairing; `${...}` substrate; store leaf placement) if a load-bearing invariant emerged (edit `AGENTS.md`, NOT the `CLAUDE.md` symlink)
- [ ] update `docs/internals/packages.md` for new/changed package responsibilities
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion
*Items requiring manual intervention or external systems — informational only*

**Manual verification:**
- End-to-end Laravel flow: fresh deploy mints `APP_KEY` via `php artisan key:generate`, harvest stores it, redeploy/run replays it unchanged; `--clear-generated` + redeploy rotates it; editing the `.env` template + redeploy re-renders (proves the render check-pairing).
- End-to-end Magento flow: migrate `env.php` to a render template (db/redis/etc. as `${...}`); bootstrap the existing committed `crypt.key` into the store via `dwe render config magento --harvest`, then stop committing it.
- Confirm render-into-`src/` works without the retired bind-mount/`mountpoint` on Docker Desktop (virtiofs).
- Confirm snapshot create/restore leaves `.dwe/generated.yml` untouched.

**External / future work:**
- Migrate real projects (e.g. `vto`) from `configs:`-copy to config template packs.
- **Phase 2 (major)**: remove `service_configs_copy`/`service_configs_check`, the `configs:`/`mountpoint` fields (drop from both allowlists → unknown-field hard error), and the copy `update` mode.
- Deferred (YAGNI now): `cmd:` extractor for container-only values; surgical per-field rotate command; snapshot capture of the store for cross-key-rotation consistency.
