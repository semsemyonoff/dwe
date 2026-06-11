# Config Formalization: Strict Root + `vars` Sandbox + Formalized `update:` Block

## Overview

Formalize the root of DWE's 3-layer project config (`workspace.yml` + `workspace/defaults.yml` + `workspace/local.yml`) and, as the motivating first consumer, lift the workspace self-update flag out of `lifecycle.yml` into a proper top-level `update:` block.

Two problems are solved:

1. **Lenient root mixes schema with free-form keys.** The merged root is decoded with plain `yaml.Unmarshal` (no `KnownFields`). Formalized typed blocks (`project`, `runtime`, `state`, `exports`, `compose`, `ui`, `docs`) share one flat namespace with arbitrary user "convention keys" (`db.*`, `app.*`, any `my_custom.*`) that land in `DweConfig.Raw`. Typos in formalized keys are silently swallowed; the schema cannot be tightened without touching user keys.

2. **Update flag is trapped in `lifecycle.yml` and partly dead.** `run.update.mode` lives in `lifecycle.yml`, which replaces lifecycle defaults *per section*. Writing `run: { update: { mode: on } }` blanks `run.phases`, losing the default `start` phase. Separately, the config surface was renamed to `on`/`off`, but the decision engine `git.Decide` still only knows `prompt`/`auto`/`check`/`off`, so raw `"on"` falls through to `ActionSkip` — **mode `on` is currently a silent no-op**.

**Solution:** introduce a single typed `vars:` block as the one legal home for arbitrary values (free-for-all *inside*, strict *at the root*); enforce a hard-error allowlist of permitted top-level keys; add a formalized top-level `update:` block that participates in the 3-layer merge; remove `run.update` from lifecycle; and collapse the git decision engine to `on`/`off`, fixing the dead `on`.

This is a **deliberate breaking change** (chosen over a transitional/deprecation path). No back-compat window, no auto-migration command — clear load-time errors guide migration.

## Context (from discovery)

Files/components involved:

- **Config core:** `internal/core/project/config/workspace.go` (DweConfig struct ~68-91; `LoadConfig` + 3-layer merge ~1267-1442; `deepMerge` ~3046-3064; `Raw` assignment ~1383; `__configPath` ~1385; `injectServicesIntoRaw` call ~1442; `LoadDweConfig` ~2956; `EffectiveMode`/`LifecycleUpdate` ~2509,2522-2537,2555; `ValidUpdateMode` ~2612).
- **UI/Docs blocks (precedent):** `internal/core/project/config/ui.go`, `docs.go` — small typed blocks with per-domain validators that *warn* on unknown keys *inside* the block. Our new check is one level up (which top-level keys exist) and is a *hard error*.
- **Update consumer:** `internal/core/workflow/lifecycle/run.go` (`resolveUpdateMode` ~79-90; mode validation ~149-150; `git.Decide` call ~208). Lifecycle defaults: `internal/core/workflow/lifecycle/defaults.go` (`DefaultRunConfig`, `EnsureRunConfig`).
- **Decision engine:** `internal/shared/git/policy.go` (`UpdateMode` consts ~10-14; `Decide` ~32-73), `internal/shared/git/policy_test.go`.
- **Raw consumers (dot-path, no code change — but our fixtures/docs/defaults must move to `vars.*`):** `internal/shared/envfile/render.go:54,65`; `internal/core/usercommands/resolve/resolve.go:32,88,144`; `internal/core/validate/commands/commands.go:459,470`; `internal/cli/command/runbyid.go:93`; `internal/core/execution/builtin/config_keys_present.go:58`; `internal/core/execution/filesgate/spec/spec.go:95`; `internal/core/project/config/docker.go:156,324`; `internal/core/project/stack/status.go:123,131`.
- **`${...}` resolver:** `internal/shared/tpl/render_command.go` (`CompileVarSyntax`) — default fall-through is `resolve .Raw "<dotpath>"`; **no change needed** (it already walks `Raw` by dot-path; `vars.*` resolves naturally).
- **Validate framework:** `internal/core/validate/config/all.go` (`All()`); `internal/core/validate/config/workspace.go:32-89` (`workspaceValidator`, e.g. `docs.mermaid` semantic check).
- **Docs:** `docs/reference/config/workspace.md` ("Project convention keys"), `setup.md`, `validate.md`, `snapshot.md`, `lifecycle.md` (`run.update`), `docs/reference/concepts/git.md`.

Related patterns found:

- `deepMerge` is last-layer-wins for scalars, recursive for maps. Layer precedence: `workspace.yml` (base) → `defaults.yml` (overrides) → `local.yml` (overrides all).
- `injectServicesIntoRaw` and `__configPath` are injected into `Raw` *after* merge → the strict allowlist check must run on the merged **user** map within the window "after merge, before injections". **Critically, it must run AFTER the existing `binaries:`/`tools:` legacy-key rejections (workspace.go ~1358-1366)**, which emit their own dedicated migration messages — a generic allowlist placed before them would clobber those messages and break `workspace_test.go` (e.g. the `"tools: no longer supported"` assertion ~3186). So: after ~1366, before `__configPath` (~1385) and `injectServicesIntoRaw` (~1442). Alternatively, special-case `binaries`/`tools` inside the allowlist loop to preserve their messages.
- The `${...}` resolver special-cases `param`/`context`/`snapshot`/`generated`/`files`/`host`; everything else (including `project`/`services` and former custom keys) falls through to `.Raw`.

Dependencies identified: changes are confined to `internal/core/project/config`, `internal/core/workflow/lifecycle`, `internal/shared/git`, `internal/core/validate/config`, plus repo `testdata/` fixtures and `docs/`. No consuming-project code in this repo.

## Development Approach

- **Testing approach: Regular** (code first, then table-driven tests) per project convention.
- Complete each task fully before moving to the next; make small, focused changes.
- **Every task MUST include new/updated tests** (success + error/edge cases), listed as separate checklist items.
- **All tests must pass before starting the next task.** Use `make test` (NOT `go test ./...` — embedded docs are generated/gitignored). After any `docs/` edit run `make build` first (syncs embedded docs + regenerates `content_hashes_gen.go`).
- Honor CLAUDE.md contracts: config accessors (`GitBin`, etc.) not raw fields; diagnostics-as-data for validate; strict pipeline loaders tolerate `io.EOF`.
- Keep this plan in sync with actual work; update on scope changes.

## Testing Strategy

- **Unit tests** required every task (table-driven where natural — config load/merge, decision matrix, validators).
- **No UI e2e tests** in this project; golden/snapshot tests exist for docs and rendering — keep them green via `make build` before `make test`.
- After the final task, run `make test` and `make lint` to green.

## Progress Tracking

- Mark completed items `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix; blockers with ⚠️ prefix.
- Update plan if implementation deviates from scope.

## Solution Overview

**Strict root via explicit allowlist.** A single source-of-truth set of permitted top-level keys — `{project, runtime, state, exports, compose, ui, docs, services, vars, update}` — checked against the merged user map after `deepMerge`. Any other key → hard error with a `vars:` hint. Chosen over `KnownFields(true)` for human-readable errors, a single extensible list, and clean interaction with the map-merge-then-inject flow.

**`vars:` sandbox.** New typed field `Vars map[string]any` (`yaml:"vars"`). Strict at the root (it's an allowed key); free-for-all inside (no validation, any nesting). References become `${vars.db.password}` / `from: vars.db.user`. Resolver unchanged.

**Formalized `update:` block.** New `Update *UpdateConfig{ Mode string }` top-level field, 3-layer merged (scalar last-layer-wins): project author sets policy in `workspace.yml`, dev overrides in `local.yml`. `nil` → `"off"`; present-but-empty → `"on"`. Decouples enabling update from lifecycle phases.

**Engine fix.** Collapse `git.Decide` to `on`/`off`. `on` = current `prompt` behavior: interactive TTY → ask then `pull --ff-only`; non-TTY/CI → warn "behind, skipping". All guard branches (dirty / no-upstream / fetch-failed / diverged → warn; up-to-date → skip) retained. `auto`/`check`/`prompt` removed (YAGNI).

**File-layout convention (docs-only, no code).** Recommend `vars:`+`exports:` in `defaults.yml` (bulkiest); compact formalized blocks in `workspace.yml`; personal overrides in `local.yml`. Guideline only — violating it is not an error.

## Technical Details

- `UpdateConfig.EffectiveMode()` replaces `LifecycleRunConfig.EffectiveMode()` (same nil→"off", empty→"on" semantics).
- Allowlist check returns an error like: `workspace.yml: unknown top-level key "db" — move custom values under "vars:" (e.g. vars.db.*)`. The reported filename should reflect the layer where the offending key appears if cheaply available; otherwise reference the merged config generically.
- `run.go:208` must pass the resolved `on`/`off` mode (typed as `git.UpdateMode`) into `git.Decide`; the switch in `Decide` now matches `ModeOn`.
- Validate: `workspaceValidator` gains (1) unknown-top-level-key diagnostic (error) and (2) `update.mode ∉ {on,off}` diagnostic (error), mirroring load-time errors as diagnostics-as-data.

## What Goes Where

- **Implementation Steps** (`[ ]`): all code, tests, fixtures, and docs in this repo.
- **Post-Completion** (no checkboxes): downstream DWE projects in the wild must move root custom keys under `vars:` and prefix references — out of scope for this repo.

## Implementation Steps

### Task 1: Add `vars:` field + strict-root allowlist enforcement

**Files:**
- Modify: `internal/core/project/config/workspace.go`
- Modify/Create: `internal/core/project/config/workspace_test.go` (or a focused `*_test.go` near the loader)

- [x] Add `Vars map[string]any \`yaml:"vars"\`` to `DweConfig` (top-level, alongside `project`/`runtime`/…).
- [x] Define a single source-of-truth allowlist of permitted top-level keys `{project, runtime, state, exports, compose, ui, docs, services, vars, update}` (a package-level set/slice). NOTE: also includes `schema_version` (reserved forward-compat metadata, used pervasively in fixtures) — without it ~36 existing fixtures would be rejected.
- [x] In `LoadConfig`, place the allowlist check AFTER the existing `binaries:`/`tools:` legacy rejections (workspace.go ~1366) and BEFORE `__configPath`/`injectServicesIntoRaw`; iterate the merged map's top-level keys and hard-error on any key not in the allowlist, with the `vars:` hint message. Preserve the dedicated `binaries`/`tools` messages (special-cased to skip in the loop; rejections run earlier). Iterates per layer so the error names the source file.
- [x] Ensure `vars` survives into `Raw` (it already will — it's a normal merged key) so `${vars.*}` and `from: vars.*` resolve unchanged.
- [x] Write tests: known keys load OK; unknown root key → error with hint; `vars:` with arbitrary nested free-form structure loads and is reachable via `ResolvePath(cfg.Raw, "vars.x.y")`; 3-layer merge keeps `vars` from `defaults.yml`.
- [x] Write error-case tests: unknown key in each layer (`workspace.yml`, `defaults.yml`, `local.yml`) is rejected. Also migrated the one breaking LoadConfig-path inline test (`config_keys_present_test.go`) early so the suite stays green (Task 2 covers the rest).
- [x] `make test` — config + builtin packages pass. (Pre-existing unrelated failure: `TestRussianTranslationsAreFresh` for `validate.md`, present on branch HEAD before my changes; covered by Task 7.)

### Task 2: Migrate repo `testdata/` fixtures + our own docs examples to `vars.*`

**Files:**
- Modify: every `testdata/**/{workspace,defaults,local}.yml` fixture using root custom keys
- Modify: docs examples touched in later doc tasks are handled in Task 7; here only fixtures + any inline test YAML

- [x] **Scope note:** only YAML that flows through `LoadConfig` is affected by strict-root. Tests that build `DweConfig.Raw` directly via map literals (e.g. `usercommands/resolve/resolve_test.go:81-82`, `shared/envfile/render_test.go:157-159`) BYPASS LoadConfig and stay valid — do NOT migrate them. `prompt_test.go:261`'s `unknown_top_level` is in `.dwe/deploy/state.yml` (different schema) — also unaffected. Verified: these were left untouched.
- [x] Confirmed LoadConfig-path case to migrate: `internal/core/execution/builtin/config_keys_present_test.go:124` → already migrated to `vars.*` during Task 1 (it was the one breaking LoadConfig-path inline test; wrapped under `vars:` with `vars.db.*`/`vars.app.*` paths).
- [x] Grep all `testdata/**/{workspace,defaults,local}.yml` fixtures + inline LoadConfig-path YAML literals for root custom keys (`db:`, `app:`, `user:`, `my_custom:`, etc.). Exhaustive column-0 scan found NO offending keys: the 2 static `workspace.yml` fixtures are clean, and the only inline custom-root-key literals are the Task 1 strict-root error-case tests (which intentionally test rejection and must stay). No `from:`/`${...}` dot-paths needed repointing.
- [x] `make test` — strict-root rejects no fixtures: full suite green except the pre-existing unrelated `TestRussianTranslationsAreFresh` (`validate.md`, branch-HEAD failure covered by Task 7). All config/builtin/loader/LoadConfig-path tests pass.

### Task 3: Add formalized top-level `update:` block + 3-layer merge wiring

**Files:**
- Modify: `internal/core/project/config/workspace.go`
- Modify: `internal/core/project/config/workspace_test.go`

- [x] Add `type UpdateConfig struct { Mode string \`yaml:"mode"\` }` and `Update *UpdateConfig \`yaml:"update"\`` to `DweConfig`.
- [x] Add `func (c *UpdateConfig) EffectiveMode() string` (nil → "off"; empty mode → "on"; else the mode), porting semantics from the old `LifecycleRunConfig.EffectiveMode`.
- [x] Confirm `update` is in the Task 1 allowlist; confirm scalar `mode` merges last-layer-wins through `deepMerge`. (Verified: `update` already in `allowedRootKeys`; 3-layer merge test confirms last-layer-wins.)
- [x] **Load-time value validation (parity with the old lifecycle loader at workspace.go:2590):** when the `update:` block is present, hard-error in `LoadConfig` on `mode ∉ {on,off}` (reuse `ValidUpdateMode`). The allowlist only checks key *names*; without this, a bad value (`update: {mode: yes}`) would pass load and silently `ActionSkip` at run-time. (Task 6 still adds the equivalent `dwe validate` diagnostic.)
- [x] Write tests: `EffectiveMode` (nil/empty/on/off); 3-layer merge override (defaults `on` → workspace `off` → local `on` yields `on`); absent block → "off". Also added present-empty-mode → "on" and invalid-mode-rejected cases.
- [x] `make test` — must pass before Task 4. (Config package green; only pre-existing unrelated `TestRussianTranslationsAreFresh` for `validate.md` fails — branch-HEAD, covered by Task 7.)

### Task 4: Remove `run.update` from lifecycle; repoint the run consumer

**Files:**
- Modify: `internal/core/project/config/workspace.go` (remove `LifecycleUpdate`, `LifecycleRunConfig.Update`, its `EffectiveMode`, and lifecycle-loader normalization/validation of `update`)
- Modify: `internal/core/workflow/lifecycle/run.go`
- Modify: `internal/core/workflow/lifecycle/defaults.go` (**required** — `DefaultRunConfig` sets `Update: &config.LifecycleUpdate{Mode: "off"}` at line 20; removing `LifecycleUpdate` in this task won't compile until this line is dropped)
- Modify: `internal/cli/lifecycle/run.go` (flag help at ~67-68 — `--no-update`/`--update` help says "regardless of lifecycle.yml config"; update to reference the `update:` block)
- Modify: lifecycle + workspace `*_test.go` as needed

- [x] Delete `run.update` struct field, loader normalization, and the lifecycle-side mode validation; keep the rest of `LifecycleRunConfig` (phases, show_info, final_message, log) intact. (Also removed the now-orphaned `LifecycleUpdate` type and `LifecycleRunConfig.EffectiveMode`.)
- [x] Repoint `resolveUpdateMode` to read `cfg.Update.EffectiveMode()` (main config) instead of `runCfg.Update`; preserve CLI precedence `--no-update` > `--update` > config and the `ValidUpdateMode` check at `run.go:149-150` (signature now takes `*config.UpdateConfig`; nil-safe via `EffectiveMode`).
- [x] Drop `Update: …` from `DefaultRunConfig` (defaults.go:20) and fix the `--no-update`/`--update` flag help in `internal/cli/lifecycle/run.go:67-68`.
- [x] Verify `EnsureRunConfig`/`DefaultRunConfig` and the default `start` phase no longer reference update — enabling update must not touch phases (the core fix). New test `TestRunRun_UpdateEnabledViaTopLevelConfig_ProbesAndKeepsDefaultPhases` proves it.
- [x] Write/adjust tests: enabling update via main config leaves default `run` phases intact (new test); removed obsolete `run.update` tests (`TestEffectiveMode`, `TestLoadLifecycleConfig_defaultMode`/`_invalidUpdateMode`/`_RejectsOldMode*`/`_RejectsEnabledField`), added `TestLoadLifecycleConfig_RejectsUpdateBlock` (KnownFields now rejects lingering `run.update`); migrated `resolveUpdateMode` tests to `*config.UpdateConfig`; repointed `restart_test.go` to enable update via top-level workspace.yml.
- [x] `make test` — passes (only the pre-existing branch-HEAD `TestRussianTranslationsAreFresh` for `validate.md` fails — unrelated, covered by Task 7). Lint clean on affected packages.

### Task 5: Collapse `git.Decide` to on/off and fix the dead `on`

**Files:**
- Modify: `internal/shared/git/policy.go`
- Modify: `internal/shared/git/policy_test.go`
- Modify: `internal/core/workflow/lifecycle/run.go` (line ~208 mode mapping)

- [x] Replace `ModePrompt`/`ModeAuto`/`ModeCheck` with `ModeOn` (keep `ModeOff`); map `on` to the former `prompt` behavior in `Decide` (TTY → `ActionPullPrompt`; non-TTY → `ActionWarn` "behind … skipping in non-interactive").
- [x] Retain all guard branches: not-repo/off → skip; dirty/no-upstream/fetch-failed/diverged → warn; up-to-date → skip.
- [x] Ensure `run.go` passes the resolved mode as `git.UpdateMode("on"|"off")` matching the new constants. (Already passed `git.UpdateMode(effectiveMode)`; effectiveMode is now constrained to `on`/`off` upstream.)
- [x] `Decide` no longer returns `ActionPullAuto` (no `auto` mode) → removed both the unreachable `case git.ActionPullAuto:` in run.go AND the now-dead `ActionPullAuto` constant in policy.go (nothing else referenced it).
- [x] Rewrite `policy_test.go`: remove prompt/auto/check cases; add `on` (behind+TTY → prompt; behind+CI → warn; up-to-date → skip; dirty/diverged/no-upstream/fetch-failed → warn) and `off` (always skip).
- [x] `make test` — passes (only the pre-existing branch-HEAD `TestRussianTranslationsAreFresh` for `validate.md` fails — unrelated, covered by Task 7). Lint clean on `git` + `lifecycle` packages.

### Task 6: Add validate diagnostic for `update.mode`

**Files:**
- Modify: `internal/core/validate/config/workspace.go`
- Modify: `internal/core/validate/config/workspace_test.go` (or nearest validator test)

- [x] **No separate unknown-top-level-key check.** `workspaceValidator.Run` already calls `config.LoadConfig` (validate/config/workspace.go:50) and converts a load error into a `SeverityError` diagnostic (51-58). Once strict-root hard-fails on an unknown key (Task 1) AND on bad `update.mode` (Task 3 load-time validation), those are *already* surfaced as diagnostics-as-data via that path — a check added in the `else` branch (59-86) would be unreachable dead code. Skip it. (Confirmed: Task 1 + Task 3 load-time checks are in place at workspace.go:1453 and :1462; no validator production change needed.)
- [x] (Optional, only if Task 3 did NOT add load-time `update.mode` validation) emit an **error** diagnostic for `update.mode ∉ {on, off}` in the reachable `else` branch. If Task 3 added load-time validation, this whole task may reduce to tests only. (Task 3 DID add load-time validation → this task reduced to tests only; no else-branch check added.)
- [x] Write tests: unknown root key → error diagnostic (via the LoadConfig-error path); bad `update.mode` → error diagnostic; clean config → no diagnostics. Added `TestWorkspaceValidator_UnknownRootKey`, `TestWorkspaceValidator_BadUpdateMode`, `TestWorkspaceValidator_GoodUpdateMode`.
- [x] `make test` — passes (only the pre-existing branch-HEAD `TestRussianTranslationsAreFresh` for `validate.md` fails — unrelated, covered by Task 7). validate/config package + lint clean.

### Task 7: Docs rewrite + file-layout convention + embedded sync

**Files:**
- Modify: `docs/reference/config/workspace.md` (rewrite "Project convention keys" → `vars:`; add strict-root semantics + file-layout convention)
- Modify: `docs/reference/config/lifecycle.md` (remove `run.update`)
- Create or Modify: update docs for the `update:` block — add a section to `workspace.md` (or `docs/reference/config/update.md`) covering `mode: on|off`, 3-layer merge, on=prompt/CI-warn behavior, `--update`/`--no-update` precedence
- Modify: `docs/reference/config/setup.md`, `validate.md`, `snapshot.md` (root custom-key examples → `vars.*`)
- Modify: `docs/reference/concepts/git.md` (where update mode lives)
- Modify (i18n — hand-maintained Russian translations, no validator catches staleness): `docs/i18n/ru/reference/config/lifecycle.md` (documents `update:` under `run:` at ~78-80, 125, 211, 254-256) and `docs/i18n/ru/reference/concepts/git.md` (~86-91). Keep parity with the English changes, or add a tracked note explicitly deferring the translation.

- [x] Rewrite `workspace.md` convention section to document `vars:` (strict root + sandbox) and the recommended file-layout convention (defaults.yml: vars+exports; workspace.yml: compact blocks; local.yml: overrides). Added new `## Strict root + the vars: sandbox` section, `### The update: block` (replacing the old open-namespace "Project convention keys" section), and `## Recommended file-layout convention` table. Updated the layer table + dot-path examples to `vars.*`.
- [x] Document the formalized `update:` block in the chosen location; remove `run.update` from `lifecycle.md`. Chose `workspace.md` (`### The update: block`) as the home; rewrote `lifecycle.md`'s `## run.update` → `## Self-update probe` pointer, removed the `update.mode` rows/examples/validation bullets, and updated `concepts/git.md` + `concepts/pipelines.md` cross-links to the top-level block.
- [x] Migrate every `db.*`/`app.*`/`user.*` root example across `setup.md`/`validate.md`/`snapshot.md`/`git.md` to `vars.*`. Migrated all `writes:` targets + prose in `setup.md` (incl. `secrets.*`/`workspace.root`/`cache.*`/`debug.*` → `vars.*`) and `config_keys_present` examples in `validate.md`. `snapshot.md`/`git.md` had no root custom-key config examples (their `db.*` are user-command names / a service named `db`) — left unchanged.
- [x] Update the Russian i18n markdown to match. Translated content parity for ru `lifecycle.md`, `concepts/git.md`, `concepts/pipelines.md`, plus the mechanical `vars.*` migration in ru `setup.md`/`validate.md` and the new strict-root/`vars`/`update`/file-layout sections in ru `workspace.md`; fixed ru cross-link anchors to translated slugs; refreshed all 6 `> Translated from: … @ <hash>` headers to the regenerated English content hashes (incl. the pre-existing `validate.md` staleness).
- [x] If a per-package internals note is warranted, add it to `docs/internals/packages.md` — deferred to Task 9, which explicitly covers the strict-root allowlist / `vars` sandbox / update-block-location contracts in `packages.md` / AGENTS.md.
- [x] Run `make build` (syncs `internal/core/docs/embedded/` + regenerates `content_hashes_gen.go`).
- [x] Write/adjust any doc-presence tests if the docs subsystem asserts specific pages; ensure golden/hash tests pass. No new tests needed; the existing docs-subsystem golden/hash tests (incl. `TestRussianTranslationsAreFresh`) pass.
- [x] `make test` — docs-subsystem tests pass; full `make test` green (0 failures), `make lint` clean.

### Task 8: Verify acceptance criteria

- [x] Verify: enabling update is a one-liner in `workspace.yml`/`local.yml` and does NOT blank lifecycle `run` phases. (Test-verified: `TestRunRun_UpdateEnabledViaTopLevelConfig_ProbesAndKeepsDefaultPhases` in `lifecycle/run_test.go` — top-level `update:` triggers the git probe while default `run` phases stay intact.)
- [x] Verify: `update: { mode: on }` actually prompts+pulls on a behind+clean TTY repo and warns in CI (the dead-`on` regression is gone). (Test-verified: `git/policy_test.go` `on/behind-tty` → `ActionPullPrompt`, `on/behind-ci` → `ActionWarn`; `ModeOn` is now wired through `Decide`.)
- [x] Verify: unknown root key produces the helpful hard error; `vars:` accepts arbitrary nested content; `${vars.*}` and `from: vars.*` resolve. (Test-verified: `workspace_test.go` unknown-top-level-key cases assert the `"unknown top-level key"` hint; `ResolvePath(cfg.Raw, "vars.db.password")` / `vars.my_custom.timeout` resolve arbitrary nested content.)
- [x] Verify 3-layer override precedence for `update.mode` and that `defaults.yml`/`local.yml` share the same strict key set. (Test-verified: `workspace_test.go` defaults-`on` → workspace-`off` → local-`on` yields `on` (last-layer-wins); unknown-key rejection runs per layer across all three files.)
- [x] Run full suite: `make test` (and `make test-race` if quick) and `make lint` — all green. (`make test`: 0 failures incl. the formerly-failing `TestRussianTranslationsAreFresh`; `make test-race`: 0 failures/races; `make lint`: 0 issues.)

### Task 9: Finalize

- [x] Update `docs/internals/packages.md` / AGENTS.md if new load-bearing contracts emerged (strict-root allowlist; `vars` sandbox; update block location). Added strict-root + `vars:` sandbox + top-level `update:` contract to the `project/config/` § in `packages.md`, rewrote the `internal/shared/git/` § to the binary `ModeOn`/`ModeOff` matrix, and added a new "Strict root + `vars:` sandbox + top-level `update:`" Critical Patterns bullet to `AGENTS.md` (CLAUDE.md symlink follows). `make build` re-synced embedded docs; `make test`/`make lint` green.
- [x] Move this plan to `docs/plans/completed/20260610-config-formalization-vars.md`.

## Post-Completion

*Informational only — external action required, no checkboxes.*

**External system updates:**
- Downstream/real DWE projects with root custom keys (`db.*`, etc.) must wrap them under `vars:` and prefix all references (`${vars.db.*}`, `from: vars.db.*`) — this is the intended breaking migration. A short migration note in release notes is advisable.
- Projects that enabled update via `lifecycle.yml`'s `run.update` must move it to the top-level `update:` block.

**Manual verification:**
- Smoke-test `dwe run` against a real project with a behind upstream (TTY prompt path) and in a CI-like non-interactive shell (warn path).
- Confirm `dwe validate` reports unknown root keys and bad `update.mode` as errors with correct exit code.
