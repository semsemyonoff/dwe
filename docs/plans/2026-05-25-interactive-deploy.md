# Interactive `devbox deploy`

## Overview

Turn `devbox deploy` into the canonical entry point for project deploys:

- **No-subcommand invocation** opens an interactive TUI menu (huh `Select`) with Run / Run service / Plan / Plan service / Wizard / Exit.
- **Wizard** is shown only on a fresh project (`devbox/local.yml` missing or empty). It probes for port conflicts via `env.CollectPortConflicts` directly (bypassing `preflight.Run`, which would abort), then prompts for: setup questions declared in `devbox/setup.yml` (when present), plus port override values (when conflicts exist). Either source can be empty — the wizard runs as long as at least one has work to do. It atomically writes the merged answer overlay into `local.yml`, **reloads config**, then runs the normal preflight → `deploy run` path in-process.
- **CI / scripted flow** is unchanged: `devbox deploy run`, `devbox deploy plan`, `devbox deploy state` keep their current non-interactive behavior.
- `devbox deploy step` is **removed** — covered by user commands + state.
- Non-TTY `devbox deploy` (no subcommand) prints help and exits 2.

Pending-deploy hint from `journal.PendingApply` is surfaced at the top of the menu.

## Context (from discovery)

- **Deploy group:** `internal/command/deploy.go:33-51` (`newDeployCmd`). Subcommands `plan`, `run`, `step`, `state` wired at lines 46–49. Group is a pure container — no `RunE`, no `PersistentPreRunE`.
- **`deploy step` to delete:** `internal/command/deploy.go:793-919` (`newDeployStepCmd`) + registration on line 48.
- **Deploy subcommands actually wired:** `plan`, `run`, `step`, `state` (`internal/command/deploy.go:46-49`). There is no `deploy status` — the project-wide status view lives under `devbox status deploy`. Wizard/menu must not reference a nonexistent subcommand.
- **`huh` is already a dependency:** `charm.land/huh/v2 v2.0.3` in `go.mod:9`. Existing UI code in `internal/ui/` imports `huh "charm.land/huh/v2"` (see `internal/ui/huh.go`, `selector.go`, `multiselect.go`, `paramform.go`). The repo also exposes `ui.Theme()`, `ui.RunWithPromptHooks(...)`, `ui.RunConfirm`, `ui.RunSelector`, `ui.RunMultiSelect`, `ui.RunParamForm` — the wizard must reuse these (themed glyphs, palette, LiveLine hook integration), not build a fresh `huh.Form` from scratch.
- **Local config writer:** `internal/localconfig/local_yaml.go:32-44` — currently `os.WriteFile`, not atomic. Needs replacement with temp-file + rename helper.
- **Service overlay allowlist:** `internal/config/devbox.go:1091-1095` (`overlayAllowedKeys`) + enforcement at `validateServicesOverlay` lines 1102–1131. Keys: `enabled`, `ports`, `hosts`.
- **`cfg.Raw`:** `internal/config/devbox.go:108` — `map[string]any`, populated at line 1017 from deep-merge of devbox.yml + defaults.yml + local.yml.
- **Preflight:** `preflight.Run(ctx, cfg, cmdRegistry, baseDir, stage, skip, errOut)` — `internal/preflight/preflight.go:81`. Call from deploy run at `internal/command/deploy.go:260`.
- **`env.ports_free`:** `internal/validate/env/ports.go:74-87` — returns `[]validate.Diagnostic` with `port`, `service`, `port_name`, container owner in message.
- **Validate domain registration template:** `internal/validate/env/all.go:11-21`. New domain registers via `All()` returning slice of `validate.Validator`.
- **Journal pending:** `internal/deploy/journal/pending.go:37` defines `PendingApply`. Read-side: `ui.RenderPendingBanner(*journal.PendingApply)` in `internal/ui/pending.go`. State load via `journal.Load(path)`.
- **TTY detection:** existing helper at `internal/ui/interactive.go:14-23` (`IsInteractiveFn`) — uses `github.com/charmbracelet/x/term`.
- **Atomic write idiom:** `internal/deploy/journal/state.go:114-163` (`Save`) — `os.CreateTemp` → write → `os.Chmod` → `os.Rename`. Same pattern in snapshot/manifest, completion install, service toggle.
- **`internal/validate/setup/` + `devbox/setup.yml`:** do not exist yet.

## Development Approach

- **Testing approach: Regular (code first, tests after).**
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **Every task includes new/updated tests** — unit tests on loader + executor + validators. The huh-driven menu/wizard glue is exercised via the executor's pure data layer (we feed it pre-collected answers in tests, not via huh forms).
- All tests must pass before starting the next task.
- Update this plan file when scope changes during implementation.
- No back-compat shims: `deploy step` goes away in one PR; no alias.

### Go conventions (apply throughout)

- **`RunE`, never `Run`** — every new Cobra hook must return an error so `main` controls exit codes.
- **`cmd.OutOrStdout()` / `cmd.ErrOrStderr()`** — never write to `os.Stdout` / `os.Stderr` from command handlers, including the menu. For the huh form output, pass `cmd.OutOrStdout()` into `huh.Form.WithOutput(...)` so test harnesses can redirect.
- **Diagnostic / log output → stderr; the menu prompts → stdout** (stdout stays clean of decoration; users can still pipe `devbox deploy run`).
- **`SilenceUsage: true`** is already on root and inherited; rely on that — don't reset it on the new menu RunE.
- **Sentinel error for cancel:** declare `var ErrWizardCanceled = errors.New("wizard canceled")` in `internal/setup/`. The command layer maps `errors.Is(err, setup.ErrWizardCanceled)` → exit 130; everything else flows through the existing error→exit mapper.
- **Error wrapping uses `%w`** at every layer crossing (`fmt.Errorf("load setup.yml: %w", err)`). Error strings are lowercase, no trailing punctuation.
- **Single handling rule:** an error is either logged or returned, never both. The wizard executor returns; the command layer at the top decides whether to print.
- **Compile-time interface checks** on every new validator: `var _ validate.Validator = (*setupParseValidator)(nil)`.
- **Enums for question types:** keep `Question.Type` as `string` (YAML-friendly). The loader (`LoadSetupYAML`, Task 2) is intentionally syntax-strict + semantics-permissive — it does NOT reject unknown `Type` values; that's the job of the `setup.type_known` validator in Task 5. Don't co-mingle: putting semantic checks in the loader makes the validate-domain dead code and breaks the established repo split.
- **No `init()`** in new packages. Wire from constructors / `All()` functions, matching the existing validate-domain pattern.
- **Nil-safety:** treat `cfg.Raw` as possibly nil/empty in the loader-default resolver — return "" cleanly, do not panic on missing keys.

## Testing Strategy

- **Unit tests:** loader for `setup.yml` (strict decode, error cases); validate-presets (`port`, `hostname`, `path`, `non-empty`); merge logic that turns answer-map into nested `local.yml` overlay; atomic write helper (write-temp-then-rename; partial write must not corrupt existing file).
- **No huh-driven e2e:** huh forms are not asserted end-to-end. The wizard executor is split into "collect answers" (huh-bound) and "apply answers" (pure) — tests target the pure half.
- **Cobra command tests:** `devbox deploy` with non-TTY → exits 2 with help. `devbox deploy run` / `plan` / `state` unchanged.
- **`deploy step` deletion:** confirm removal at the test layer (any existing test for `step` deleted, not skipped).

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.

## What Goes Where

- **Implementation Steps** (`[ ]`): code changes, tests, docs in this repo.
- **Post-Completion** (no checkboxes): manual verification of the TUI in a real terminal, since huh forms can't be reliably e2e-tested in CI.

## Implementation Steps

### Task 1: Make `WriteLocalYAML` atomic

- [x] rewrite `WriteLocalYAML` in `internal/localconfig/local_yaml.go` to use the journal `Save` pattern: `os.MkdirAll(dir, 0o755)` → `os.CreateTemp(dir, ".local-*.yml")` → write → `f.Sync()` → `f.Close()` → `os.Chmod(tmp, 0o600)` → `os.Rename(tmp, path)`
- [x] on any error path before rename, `os.Remove(tmpPath)` so no `.local-*.yml` stragglers stay in `devbox/` (defer with a guarded flag)
- [x] keep the function signature unchanged — atomic-ness is internal, all existing callers benefit automatically
- [x] write tests in `internal/localconfig/local_yaml_test.go`: happy path; missing parent dir auto-created; simulated marshal failure leaves the existing file untouched; concurrent reader during write sees either old content or new content, never a half-written file (validated by checking no `.local-*.yml` artifact remains on success)
- [x] note: a generic `internal/atomicfs.WriteFile` helper could later unify the four call sites that re-implement this (journal, snapshot manifest, completion install, service toggle) — out of scope for this PR
- [x] run `go test ./internal/localconfig/...` — must pass before next task

### Task 2: `internal/setup/` package — model + loader

- [x] create `internal/setup/model.go` with `SetupConfig{Questions []Question}`, `Question{ID, Type, Title, Description string; Required bool; Writes string; Options []Option; Validate *ValidateSpec}`, `Option{Value, Label string}`, `ValidateSpec{Preset, Regex string}`. Question-type values are package constants: `TypeInput = "input"`, `TypeSelect = "select"`, `TypeMultiselect = "multiselect"`, `TypeConfirm = "confirm"`.
- [x] create `internal/setup/loader.go` with `LoadSetupYAML(path string) (*SetupConfig, error)` using `yaml.NewDecoder` + `dec.KnownFields(true)`; missing file returns `(nil, nil)` (not an error); other read/parse errors are wrapped `fmt.Errorf("load %s: %w", path, err)` — match the existing `LoadLocalYAML` style.
- [x] the loader is intentionally **syntax-strict but semantics-permissive**: unknown YAML fields fail here; unknown `Type` values / duplicate IDs / invalid `writes:` are caught by the `setup.*` validator domain (Task 5), not by the loader. This matches the repo's "strict for user-edited pipeline / command / manifest YAML" pattern.
- [x] write table-driven loader tests in `internal/setup/loader_test.go`: each question type parses; unknown top-level field → error; unknown field inside a question → error; empty file → empty `SetupConfig`; missing file → `(nil, nil)`.
- [x] run `go test ./internal/setup/...` — must pass before next task

### Task 3: `internal/setup/` — answer-to-overlay merge

- [x] create `internal/setup/merge.go` with `BuildOverlay(questions []Question, answers map[string]any) (map[string]any, error)` that walks each `Writes` dot-path and inserts the answer into a fresh nested `map[string]any`. Skips questions whose `id` is missing from `answers` (allows partial submission, though `setup.required` validators will normally prevent that upstream).
- [x] add `BuildPortOverlay(overrides map[PortKey]int) (map[string]any, error)` that emits `services.<service>.ports.<port_name>` entries, where `PortKey{Service, PortName string}`. Port overrides are **not** setup questions — they're a separate concern with a separate overlay builder. The caller merges both overlays before writing.
  - **Range-validate every override before emitting:** reject anything outside `1..65535` with a wrapped error citing the offending `PortKey` and value. The huh asker's coercion pass (`coercePortOverrides`) already enforces this for the production path, but a programmatic / test caller can hand `BuildPortOverlay` arbitrary ints — the executor must not write `services.web.ports.http: 99999` to `local.yml`. Defensive guard at the boundary, single source of truth.
- [x] add `MergeIntoLocal(existing map[string]any, overlay map[string]any) (map[string]any, error)` performing deep-merge (overlay wins on conflict at leaf, recursive on maps); reuse existing deep-merge from `internal/config/devbox.go` if one is exported, otherwise define here and note it as a candidate for extraction.
- [x] reject collisions where a non-map value is asked to become a map (return error so wizard surfaces it before atomic write)
- [x] write tests: single-level dot-path; nested 3-deep; merge with pre-existing local.yml content; conflict error; multiple questions writing under same parent; `BuildPortOverlay` happy path + empty input → empty map; **`BuildPortOverlay` out-of-range int (`99999`, `0`, `-1`) → wrapped error, no map returned** (asserts the defensive range check at the executor boundary); merging question overlay + port overlay produces both branches under `services.<name>`.
- [x] run `go test ./internal/setup/...` — must pass before next task

### Task 4: Validate + coerce answers (`port`, `hostname`, `path`, `non-empty`)

- [x] add `internal/setup/validate.go` exporting `ValidateAndCoerce(q Question, raw string) (any, error)`. The combined entry point is deliberate: every preset validates a raw string AND defines the typed value that lands in `local.yml`. Splitting them would invite drift between "what's allowed" and "what gets written".
- [x] presets and their typed return values:
  - `port` → `int` (parse `1..65535`; reject anything else)
  - `hostname` → `string` (RFC 1123 short-name regex)
  - `path` → `string` (non-empty, no NUL byte)
  - `non-empty` → `string` (`strings.TrimSpace != ""`)
  - no preset, no regex → `string` (raw value unchanged; trim left to caller)
  - regex (when set) → `string` (matched value); use `regexp.Compile` and return a wrapped error on failure. Even though `setup.validate_regex_compiles` (Task 5) and the pre-wizard validator run (Task 9 dispatch sequence) catch bad patterns first, the runtime path stays defensive — `MustCompile` here would panic the whole CLI if any of the upstream gates ever regresses or is bypassed.
- [x] for non-input types: `select` → `string` (the chosen value), `multiselect` → `[]string`, `confirm` → `bool`. These bypass `ValidateAndCoerce` because huh already produces the typed value — the wizard wires them straight through.
- [x] **The wizard pipeline now is: huh-raw → `ValidateAndCoerce` → typed answer → `BuildOverlay`.** `BuildOverlay` writes the typed value verbatim into the overlay map; YAML marshaling preserves the Go type, so `services.web.ports.http: 8080` round-trips as an int (matching `config/devbox.go:1157-1160`) instead of `"8080"`.
- [x] write table-driven tests covering each preset's accept + reject cases, regex path (precompiled), and the typed return value for each (e.g. `port` returns `int(8080)`, not `"8080"`). One test asserts `local.yml` written by an end-to-end stub roundtrips through `yaml.Unmarshal` with the expected Go types per field.
- [x] run `go test ./internal/setup/...` — must pass before next task

### Task 5: `internal/validate/setup/` — new validate domain

- [x] **Prerequisite — export the config identifier helper.** The current `validIdentifierKey` at `internal/config/devbox.go:858` is unexported. Add `func ValidIdentifierKey(key string) bool` (thin exported wrapper, or rename the existing function) so both this validator and `setup.writes_syntax` can use it without duplicating the regex. Update the original call sites in `internal/config/` to call the exported name. No behavioral change.
- [x] **No new fields on `validate.Context`.** Adding `*setup.SetupConfig` to the root `internal/validate` package would create an import cycle: `internal/setup` imports `internal/validate/env` (for `env.PortConflict`, Task 7), `env` imports `internal/validate`, so `internal/validate` cannot in turn import `internal/setup`. Instead, mirror the snapshot-domain assembly: `valsetup.All(setupCfg *setup.SetupConfig, setupErr error, setupPath string) []validate.Validator` takes the loaded values as function arguments. `internal/validate/setup` is allowed to import `internal/setup` (leaf-side), and the caller — `runValidate` for `devbox validate`, the menu RunE for `devbox deploy` — loads `setup.yml` once and passes the result in. No shared mutable context, no cycle.
- [x] **Assembly contract** (mirroring snapshot/checks): the SOLE emitter of `setup.parse` load-error diagnostics is the `setup.parse` validator. Each caller loads `setup.yml` once and constructs the validator set via `valsetup.All(setupCfg, setupErr, setupPath)`. The `setup.parse` validator turns a non-nil `setupErr` into one Error diagnostic. `os.ErrNotExist` is silent (absent file is not an error — the wizard handles "no questions" gracefully).
- [x] create `internal/validate/setup/all.go` with `func All(setupCfg *setup.SetupConfig, setupErr error, setupPath string) []validate.Validator` — function-arg form (template: `internal/validate/env/all.go`, plus the load-error threading pattern from `internal/validate/checks/`). Both `runValidate` (the `devbox validate` command in `internal/command/`) and the menu RunE (Task 9) call this exact signature. `runValidate` is updated to load `setup.yml` once via `setup.LoadSetupYAML(setupPath)` and pass the result into its registry-assembly function (`buildRegistry` or equivalent); it does NOT use a global / context field.
- [x] `setup.parse` — loader-level parse errors (strict-decode failures) become a single diagnostic
- [x] `setup.type_known` — `Question.Type` is one of `input`/`select`/`multiselect`/`confirm`; unknown values are errors
- [x] `setup.id_required` — every question MUST have a non-empty `id` (used as the key into the answers map; `BuildOverlay` cannot map an answer without it)
- [x] `setup.writes_required` — every question MUST have a non-empty `writes:` path. Display-only questions are not supported — confirms / selects still need to record their result so the wizard is deterministic across runs. (Decision: explicitly rejected display-only mode; if needed later, add a `display_only: true` flag and re-open this gate.)
- [x] `setup.id_unique` — duplicate `id` is an error with both offending positions
- [x] `setup.writes_unique` — duplicate `writes` paths across questions
- [x] `setup.writes_syntax` — dot-path lexically valid (no empty segment, no leading/trailing dot). Segment regex matches **the same identifier rule the config loader uses** for port/host keys (`^[A-Za-z_][A-Za-z0-9_]*$`, no hyphens) — reuse the config-side helper rather than defining a parallel regex here, so `services.web.ports.http-api` is rejected at `devbox validate` time instead of slipping through to `config.LoadConfig` after the wizard has written. Top-level segments (`db`, `app`, etc.) use the same rule for consistency.
- [x] `setup.writes_scope` — two enforcement layers, not just prefix matching:
  - **Forbidden top-level namespaces:** `info.*`, `styles.*`, `docker.*`, `binaries.*` → error.
  - **Service-overlay shape gate** for any path starting with `services.<name>.`: only three exact shapes are valid leaves — `services.<name>.enabled` (scalar bool), `services.<name>.ports.<port_name>` (scalar int), `services.<name>.hosts.<host_name>` (scalar string). Targeting `services.<name>.ports` or `services.<name>.hosts` directly (without a leaf segment) is rejected, because the wizard answer would be a scalar where the overlay merge expects a map — producing an invalid `local.yml` that `config.validateServicesOverlay` would reject after the wizard already wrote. Targeting `services.<name>.<any-other-key>` is rejected for the same reason today's `overlayAllowedKeys` rejects it at config load.
  - Reuse `overlayAllowedKeys` from `internal/config/devbox.go` (export it or wrap with `config.IsServiceOverlayKey`); the shape gate is local to this validator.
- [x] `setup.options_valid` — `select`/`multiselect` must have non-empty `options` with unique `value`s, and **each `value` must be non-empty**. Empty `value: ""` is rejected because it collides with the runtime "no answer" zero-value for `select` (a `Required` question with an empty-value option would be impossible to satisfy cleanly).
- [x] `setup.validate_exclusive` — `validate.preset` and `validate.regex` cannot both be set
- [x] `setup.validate_only_on_input` — `validate.preset` and `validate.regex` are meaningful only for `type: input`. Setting either on `select`, `multiselect`, or `confirm` is an error — runtime would silently ignore them, which is exactly the kind of "looks-OK config does nothing" trap the validate domain exists to prevent.
- [x] `setup.validate_preset_known` — `validate.preset` (when set) must be one of `port`, `hostname`, `path`, `non-empty`; unknown values are errors with a hint listing the supported presets
- [x] `setup.validate_regex_compiles` — `validate.regex` (when set) must compile via `regexp.Compile`; compile errors are surfaced as diagnostics so `devbox validate` catches bad patterns instead of the wizard panicking at runtime
- [x] `setup.type_writes_consistent` — enforce that the question type produces a value compatible with what `local.yml` accepts at the writes target. The three known service-overlay leaves have hardcoded rules; all other paths fall back to "any type is fine; the wizard writes the typed answer value verbatim (scalar for `input`/`select`/`confirm`, `[]string` for `multiselect`) and trusts the consumer template to handle it":
  - `services.<name>.enabled` → MUST be `type: confirm` (writes a bool; `input`/`select`/`multiselect` would produce strings or slices that `validateServicesOverlay` rejects)
  - `services.<name>.ports.<port_name>` → MUST be `type: input` with `validate.preset: port` (writes an `int` via Task 4's typed coercion)
  - `services.<name>.hosts.<host_name>` → MUST be `type: input` (writes a string; `validate.preset: hostname` is recommended but not required, since `confirm`/`multiselect` would still produce wrong-shaped values)
  - Each rule is a separate diagnostic so the user sees exactly which constraint failed.
- [x] `setup.required_consistent` — `confirm` ignores `required` (warning)
- [x] add compile-time interface checks at the top of each validator file: `var _ validate.Validator = (*setupParseValidator)(nil)` etc.
- [x] register the domain in the top-level validate registry assembly: in `runValidate` (or wherever the registry is built for `devbox validate`), load `setup.yml` once via `setup.LoadSetupYAML(setupPath)` and pass `setupCfg, setupErr, setupPath` into `valsetup.All(...)` alongside the other `All(...)` calls. Mirror the existing checks/env loading pattern — single load, single emitter for load-error diagnostics.
- [x] **Add a `devbox validate setup` subcommand** to match the existing per-domain layout at `internal/command/validate.go:66` (one subcommand per major domain). The subcommand reuses the same registry-build helper but scoped to `domain == "setup"` via `MatchScope`. Help text: `Validate devbox/setup.yml schema and writes: paths`. The full `devbox validate` command continues to include the setup domain as one of many; the subcommand is for targeted runs (matching the existing pattern users will expect).
- [x] write per-validator tests using fixtures in `internal/validate/setup/testdata/`
- [x] run `go test ./internal/validate/setup/...` — must pass before next task

### Task 6: Export a typed port-conflict probe from `internal/validate/env/`

- [x] the current `internal/validate/env/ports.go` keeps the validator (`portsFreeValidator`) and its useful typed innards (`declaredPort`, `collectDeclaredPorts`, `classifyPort`) unexported. The wizard needs a structured port-conflict list, not parsed `Diagnostic.Message` strings.
- [x] add an exported function `func CollectPortConflicts(ctx context.Context, cfg *config.DevboxConfig, baseDir string) ([]PortConflict, error)` plus an exported `type PortConflict struct { Service, PortName string; RequestedPort int; OccupiedBy string }`. Both wizard and validator must consume this — no duplication of port enumeration logic.
- [x] **Document the Docker-missing / `docker ps` failure semantics on the exported function**, preserving the current validator behavior verbatim:
  - Docker binary missing or unresolvable → return `nil, nil` (no conflicts detected, no error). The wizard sees an empty list and skips its port-fix step; preflight will surface the missing-Docker problem separately when the user proceeds to `deploy run`.
  - `docker ps` invocation fails → fall back to listen-probe results for the declared ports (matching today's `ports_free` behavior). Conflicts produced from the fallback set `OccupiedBy` to a sentinel like `"unknown (docker ps failed)"` so the wizard's huh prompt can render a meaningful label.
  - Add a godoc comment on `CollectPortConflicts` stating these two contracts explicitly — they are load-bearing for the wizard's UX and for any future caller.
- [x] refactor `portsFreeValidator.Run` (the actual method name at `internal/validate/env/ports.go:49`, not `Validate`) to call `CollectPortConflicts` and convert the result to `[]validate.Diagnostic`. The validator's diagnostic shape is unchanged from the outside; this is a pure extraction.
- [x] export the relevant supporting types (`PortClass` or whatever `classifyPort` returns) only if `CollectPortConflicts` actually exposes them in `PortConflict` — keep the surface minimal.
- [x] update / extend existing `ports_test.go` to cover the new exported function directly with table-driven cases: declared port free, conflict with a foreign container, conflict with one of our compose containers (which is NOT a conflict), no declared ports.
- [x] add a compile-time assertion in `portsFreeValidator` file that the validator still implements `validate.Validator` (in case the refactor moves things).
- [x] run `go test ./internal/validate/env/...` — must pass before next task

### Task 7: Wizard executor (pure half — answers → write)

- [x] declare `var ErrWizardCanceled = errors.New("wizard canceled")` in `internal/setup/errors.go` — used by the huh half and by tests to assert the no-write cancel path.
- [x] create `internal/setup/wizard.go` with `Run(ctx context.Context, deps WizardDeps) error` where `WizardDeps` is a plain struct (not functional options — these are required collaborators, not optional config):
  - `BaseDir string`, `LocalPath string`
  - `Questions []Question` (already loaded by caller; loader concern stays out of executor)
  - `PortConflicts []env.PortConflict` (gathered by caller via the exported `env.CollectPortConflicts` probe from Task 6, NOT via `preflight.Run` and NOT by parsing diagnostic messages)
  - `AskQuestions func(ctx, []Question) (answers map[string]any, err error)` — stubbed in tests, huh-driven in production
  - `AskPortOverrides func(ctx, []env.PortConflict) (overrides map[PortKey]int, err error)` — same shape
- [x] flow inside `Run`:
  - [x] if `len(PortConflicts) > 0`: call `AskPortOverrides`; on `errors.Is(err, ErrWizardCanceled)` return immediately without writing
  - [x] if `len(Questions) > 0`: call `AskQuestions`; same cancel behavior
  - [x] **Runtime answer normalization** (before any overlay is built): for every question, assert both the Go type AND the semantic constraint. `AskQuestions` returns `map[string]any`, so a buggy or stubbed asker can return `"true"` for a `confirm`, an unknown enum for a `select`, a `string` where `[]string` is expected for `multiselect`, OR — and this is the load-bearing case the type check alone misses — `int(99999)` for a `port`-preset input, `"bad host!"` for a `hostname` preset, or a string that fails a custom regex. The wizard runtime is the last line of defense before write — this is non-negotiable for safety.
    - [x] **Type assertions:**
      - [x] `confirm` → require `bool` (not `"true"` / `"false"` strings)
      - [x] `input` with `validate.preset: port` → require `int` (Task 4's `ValidateAndCoerce` returns `int` for this preset; the asker is expected to thread that typed value through)
      - [x] `input` with any other preset, with a regex, or with no preset/regex → require `string`
      - [x] `select` → require `string` AND the value must appear in `Question.Options[].Value` (membership check)
      - [x] `multiselect` → require `[]string` AND every element must appear in `Question.Options[].Value`
    - [x] **Semantic re-validation for `input` types** (after the type assertion passes):
      - [x] `port` preset → check range `1..65535` on the int value (catches `99999`, `0`, `-1`)
      - [x] `hostname` / `path` / `non-empty` presets → re-run `ValidateAndCoerce(q, value.(string))` and discard the typed result, propagating any error. (All identifiers in `internal/setup/` are referenced without the `setup.` prefix from inside the package — this and other helper names are unqualified throughout the implementation; tests and external callers use `setup.ValidateAndCoerce`.) This re-runs the same preset logic that `coerceInputAnswers` ran in the huh path; for non-huh askers (tests, future programmatic callers) it's the only enforcement.
      - [x] `validate.regex` set → compile (already validated at load time, but defensive: use `regexp.Compile`, not `MustCompile`) and `MatchString` against the value; non-match → wrapped error.
    - [x] Any mismatch returns a wrapped error citing the question `id` and the specific constraint that failed; no overlay is built and no file is written.
  - [x] **Runtime required-answer enforcement**: for every question with `Required: true` whose answer is missing from the asker's returned map, or whose answer is the zero value for its (now-asserted) type, return a wrapped error and write nothing. Zero values per type:
    - [x] `confirm` → `false` is a valid answer, **not** treated as "empty"; `Required` on a `confirm` is a no-op (the static validator `setup.required_consistent` already warns on this)
    - [x] `input` with `port` preset → `0` (out of valid port range, so this is unreachable via `ValidateAndCoerce` — defensive guard only)
    - [x] `input` (string) / `select` → `""`
    - [x] `multiselect` → `len == 0`
    The static validator can only check the YAML shape; this is the runtime guard that prevents a buggy asker from producing a half-filled `local.yml`.
  - [x] `qOverlay, err := BuildOverlay(Questions, answers)`
  - [x] `pOverlay, err := BuildPortOverlay(overrides)`
  - [x] `existing, err := LoadLocalYAML(LocalPath)` — **DO NOT discard this error.** Any read/parse failure must be returned wrapped (`fmt.Errorf("read existing local.yml: %w", err)`) so the wizard refuses to write rather than silently overwriting a malformed-but-present file. A missing file is already a non-error path inside `LoadLocalYAML` itself (returns empty map, nil).
  - [x] sequential merge (cannot nest, both return `(map, error)`):
    - [x] `merged, err := MergeIntoLocal(existing, qOverlay)` — return wrapped on error
    - [x] `merged, err = MergeIntoLocal(merged, pOverlay)` — return wrapped on error
  - [x] `WriteLocalYAML(LocalPath, merged)` (Task 1 made this atomic)
  - [x] return; caller is responsible for config reload + preflight + deploy
- [x] **Preflight is intentionally NOT run from `Run`** — `preflight.Run` aborts on any `env.ports_free` error diagnostic (`internal/preflight/preflight.go:123,147`), which would defeat the whole point of the wizard's port-fix step. Instead, the menu RunE (Task 9) probes port conflicts directly via `env.CollectPortConflicts` (Task 6), passes them into the wizard, and only runs full `preflight.Run` **after** local.yml has been written and config reloaded.
- [x] write tests with stub callbacks: full happy path; port-conflict path with port overlay merged into `services.<name>.ports.<port_name>`; cancel from `AskQuestions` returns `ErrWizardCanceled` and leaves `local.yml` untouched; write failure (simulated via read-only dir) returns a wrapped error; deep-merge preserves pre-existing local.yml keys not touched by questions; question overlay + port overlay both present produces a single nested map; **malformed existing `local.yml` returns wrapped error and leaves the file untouched** (asserts the no-silent-overwrite rule); **required-answer enforcement: asker omits a `Required: true` question → error before any write, asker returns `""` for a `Required` input → error**; **type-mismatch enforcement (per-type, file untouched on each): `confirm` answered with `"true"` string → error, `select` answered with a value not in `Options` → error, `multiselect` answered with a `string` instead of `[]string` → error, `multiselect` answered with `[]string{"unknown"}` not in `Options` → error**; **semantic-mismatch enforcement (file untouched on each): `port`-preset input answered with `int(99999)` → error, `hostname`-preset input answered with `"bad host!"` → error, regex-validated input answered with a non-matching string → error**; **`AskPortOverrides` returns `int(99999)` → wrapped error from `BuildPortOverlay`, no write**.
- [x] run `go test ./internal/setup/...` — must pass before next task

### Task 8: Wizard executor (huh half — actual TUI bindings)

- [x] `huh` is already a dependency (`charm.land/huh/v2 v2.0.3`). Do NOT add `github.com/charmbracelet/huh`. Import as `huh "charm.land/huh/v2"` to match `internal/ui/huh.go` and the rest of the repo.
- [x] create `internal/setup/huh.go` exposing `NewHuhAsker(out io.Writer) (askQuestions, askPortOverrides)`. The returned callbacks build `huh.NewForm(...)` configured with **both** `.WithTheme(ui.Theme())` (project palette + glyphs, `internal/ui/huh.go:84-89`, `Theme()` line 119) **and** `.WithOutput(out)`. The menu RunE (Task 9) threads `cmd.OutOrStdout()` in so tests / piped invocations can redirect.
- [x] also expose an input-side hook via the form's `.WithInput(in io.Reader)` if huh's API supports it for the version in use; otherwise document why stdin can't be redirected and skip. (Not load-bearing for the menu — non-TTY bail in Task 9 prevents the wizard from ever opening without a real terminal.)
- [x] wrap each form `.Run(...)` call in `ui.RunWithPromptHooks(func() error { return form.Run() })` so any active LiveLine pauses cleanly (consistent with `RunSelector` / `RunMultiSelect` / `RunConfirm` patterns at `internal/ui/{selector,multiselect,confirm}.go`).
- [x] map question types to huh widgets: `input` → `huh.NewInput()`, `select` → `huh.NewSelect()`, `multiselect` → `huh.NewMultiSelect()`, `confirm` → `huh.NewConfirm()`. Preset / regex validators plug into `huh.NewInput().Validate(func(s string) error { _, err := ValidateAndCoerce(q, s); return err })` (unqualified — this file lives in package `setup`). The `Validate` callback is reject-only; it does NOT mutate the bound string into a typed value.
- [x] **Post-`form.Run` coercion pass for `askQuestions`**: huh's `Input.Value(&raw)` binds to a `*string`, so after the form returns, walk the input questions and coerce their raw strings to typed Go values. Factor this into a small **pure helper** `coerceInputAnswers(inputQuestions []Question, raws map[string]string) (map[string]any, error)` living in `internal/setup/huh.go` (or `internal/setup/coerce.go` if cleaner). The helper is scoped to input questions only — `raws` contains only `input`-type entries (the caller filters before calling), so the helper does not iterate non-input types and the signature reflects that. For each question:
  - Call `ValidateAndCoerce(q, raws[q.ID])` (unqualified — same package) to get the typed result (`int` for `port` preset, `string` otherwise) and store it under `q.ID` in the returned map.
  - Returns a wrapped error on the first coercion failure (defensive — huh's `Validate` callback should already have rejected; this is the belt-and-suspenders layer).
  - The caller (`askQuestions`) merges this helper's output with the already-typed values from huh's `select` / `multiselect` / `confirm` bindings into the final `map[string]any` returned to the wizard executor.
- [x] **Unit tests on `coerceInputAnswers`** (this is pure, not huh internals): table-driven cases for `port` input string `"8080"` → `int(8080)`; `port` input `"99999"` → wrapped error; `port` input `"abc"` → wrapped error; non-port input `"hello"` → `string("hello")` unchanged; regex-matched input → string preserved; non-input question types pass through untouched. Test the helper directly without constructing a huh form.
- [x] for `askPortOverrides`, build one `huh.NewInput()` per `PortConflict` with title `"Port for <service>.<port_name> (currently used by <occupiedBy>)"`. Wire the `port` preset into its `Validate` callback (same `ValidateAndCoerce` pattern). Group into one form for atomic submit/cancel.
- [x] **Post-`form.Run` coercion pass for `askPortOverrides`**: same pattern — factor into `coercePortOverrides(conflicts []env.PortConflict, raws map[PortKey]string) (map[PortKey]int, error)` as a pure helper (in package `setup`, so `PortKey` is unqualified). Each raw value goes through `strconv.Atoi` + range check `1..65535`. Failure returns a wrapped error (defensive — the in-form `port` preset validator already rejected non-int input).
- [x] **Unit tests on `coercePortOverrides`** (pure, not huh internals): happy path `{web/http: "8080"}` → `{web/http: 8080}`; out-of-range `"99999"` → wrapped error; non-numeric `"abc"` → wrapped error; empty raws → empty result map, no error.
- [x] huh returns `huh.ErrUserAborted` on Ctrl-C / Esc — translate to `ErrWizardCanceled` via an `errors.Is` mapping at the boundary, not a string compare. Any other huh error is wrapped `fmt.Errorf("wizard: %w", err)`.
- [x] consider whether the existing `ui.RunParamForm` (a generic huh-backed `map[string]string` form, `internal/ui/paramform.go:202`) can subsume the `input`-type questions; reuse if shapes match, otherwise keep a thin wizard-local form to support `select`/`multiselect`/`confirm` in the same flow.
- [x] no unit tests on **huh form rendering itself** — add a `// Form rendering tested manually; see plan Post-Completion.` comment at the top of the file. The pure coercion helpers (`coerceInputAnswers`, `coercePortOverrides`) ARE unit-tested per the bullets above; "no huh tests" means we don't drive `form.Run()` from `go test`, not that everything in this file is unverifiable.
- [x] run `make build` and confirm the executable compiles

### Task 9: Deploy command — interactive menu

- [x] update `newDeployCmd` in `internal/command/deploy.go` to set `RunE` on the group itself (Cobra runs the parent's `RunE` only when no subcommand matched — that's exactly the bare-`devbox deploy` case). Set `Args: cobra.NoArgs` on the group so `devbox deploy garbage` still falls through to Cobra's "unknown command" path rather than silently dropping into the menu.
- [x] do NOT add `PersistentPreRunE` on the deploy group — that would silently replace the root's chain (per CLAUDE.md's grouped-commands rule). The root's `PersistentPreRunE` already resolves the project for us before the menu RunE fires.
- [x] add `runDeployMenu(cmd *cobra.Command, flags *rootFlags) error` in a new file `internal/command/deploy_menu.go`. The function reads cfg / journal / setup.yml / port-conflicts, then opens the menu.
- [x] **Test seams in `deploy_menu.go`** (package-level `var`s, test-only override via `_test.go` helpers): so command-level tests can drive the menu paths without invoking huh OR re-executing the real deploy/plan helpers.
  - [x] `var collectPortConflictsFn = env.CollectPortConflicts` — stub returns a fixed `[]env.PortConflict` in tests
  - [x] `var loadSetupYAMLFn = setup.LoadSetupYAML` — stub returns synthetic `(SetupConfig, error)` pairs to exercise the load-error / absent-file / valid branches
  - [x] `var newHuhAskerFn = setup.NewHuhAsker` — stub returns deterministic answer maps (`AskQuestions`) and override maps (`AskPortOverrides`); production wiring is unchanged
  - [x] `var runWizardFn = setup.Run` — stub records that it was called with which deps, returns either nil / `ErrWizardCanceled` / a synthetic error
  - [x] `var selectMenuItemFn func(...) (menuChoice, error)` — stub returns the desired choice (`run` / `plan` / `wizard` / `exit`) without opening huh
  - [x] `var runDeployRunFn = runDeployRun` — stub records that it was called with the expected `cmd`/`flags`/`opts`, returns nil; without this seam the test would actually execute `deploy run` against real Docker/state
  - [x] `var runDeployPlanFn = runDeployPlan` — same pattern for the plan dispatch branch
  - [x] Production code uses these vars directly; tests assign stubs in `t.Cleanup`-restored closures. No interfaces, no DI container — same package-level seam pattern used elsewhere in `internal/command/`.
- [x] **Extract `runDeployRun`, `runDeployPlan` package-level helpers** from the existing `deploy run` / `deploy plan` RunE callbacks. Signature: `runDeployRun(ctx context.Context, cmd *cobra.Command, flags *rootFlags, opts deployRunOpts) error` (similar for plan). The helpers take `cmd` so all output goes through `cmd.OutOrStdout()` / `cmd.ErrOrStderr()` and stdin via `cmd.InOrStdin()`. Each helper internally calls `config.LoadConfig(flags.configPath)` so it always works against the latest on-disk state — this is the seam that makes "reload after wizard" automatic. Both the subcommands and the menu dispatch to these helpers; the menu passes its own `cmd` through (which is the deploy group cmd, sharing the root's I/O wiring). No re-entry through Cobra.
- [x] **Replace the `render.Stdout()` call in `deploy plan` table output** at `internal/command/deploy.go:97`: rewrite the writer to come from `cmd.OutOrStdout()`. The current direct-stdout path bypasses Cobra's I/O wiring and can't be captured in tests or redirected by the menu. This is a small, mechanical fix that must land with the extraction — leaving it as `render.Stdout()` would mean the I/O rule (Go conventions § above) silently doesn't apply to plan output.
- [x] non-interactive bail: use the existing `ui.IsInteractiveFn` (checks both stdin and stdout). When false, call `cmd.Help()` and return a sentinel that maps to exit 2 in the central error→exit mapper (check what other "usage" returns from the codebase look like — match that idiom). Do not call `os.Exit` directly from `RunE`.
- [x] all menu output flows through `cmd.OutOrStdout()` / `cmd.ErrOrStderr()`; huh prompts use `ui.Theme()` and route through `ui.RunWithPromptHooks` (Task 8).
- [x] load journal `PendingApply`; if non-nil, render the existing `ui.RenderPendingBanner` above the menu's `huh.NewSelect`.
- [x] menu items: `Run (all)`, `Run service…` (sub-select of enabled services), `Plan (all)`, `Plan service…`, `Wizard`, `Exit`.
- [x] **Wizard visibility rule (single source of truth):**
  - [x] If `devbox/local.yml` exists but **fails to parse**, the menu RunE returns a wrapped error and refuses to open at all (`fmt.Errorf("read existing local.yml: %w", err)`). This is the same no-silent-overwrite rule the wizard executor applies — the menu cannot make a sensible "fresh vs. configured" decision on a malformed file, and silently treating it as either would risk overwriting (if read as empty) or hiding a real problem (if read as non-empty). Mapped to exit 1.
  - [x] Otherwise, the Wizard item is shown **iff** `local.yml` is missing OR parses to an empty map.
  - [x] Once `local.yml` has any content, the Wizard is permanently hidden — port conflicts on an already-configured project are a preflight concern surfaced by `devbox deploy run` with an actionable error, not a reason to re-run the wizard.
  - [x] On a fresh project, the Wizard is additionally hidden when both `devbox/setup.yml` is absent AND the port probe finds zero conflicts (nothing to ask).
  - [x] This rule appears in exactly one place; the acceptance criteria (Task 12) reference it verbatim.
- [x] **Wizard dispatch sequence** (the load-bearing fix for the preflight/port-conflict contradiction). All external calls go through the test seams (the `*Fn` package-level vars defined above) so the dispatch wiring is testable without invoking huh or executing real deploys.
  1. [x] probe port conflicts via `collectPortConflictsFn(ctx, cfg, baseDir)` from Task 6 (NOT `preflight.Run`, NOT by parsing `Diagnostic.Message`)
  2. [x] **load `setup.yml` once, capture both result and error:** `setupCfg, setupErr := loadSetupYAMLFn(setupPath)`. **DO NOT** short-circuit on `setupErr` here — strict-decode failures (unknown YAML fields) must be surfaced by `setup.parse`, not by a bare error return that bypasses the diagnostic table. Absent file is `(nil, nil)` per Task 2's loader contract; that's a valid state, not a load error.
  3. [x] **run the `setup.*` validator domain.** Construct the validator set via `valsetup.All(setupCfg, setupErr, setupPath)` (the function-arg seam from Task 5 — no `validate.Context` extension, no import cycle). Build a one-shot `validate.Registry` from that set and run it: `diags := registry.Run(ctx, scope...)`. **Render diagnostics via the existing two-step helper pipeline** at `internal/command/validate.go:303-308`: `rows := ui.FormatDiagnostics(diags, quiet)` then `ui.RenderDiagnosticsTable(rows)`. `RenderDiagnosticsTable` takes formatted rows, not raw `[]validate.Diagnostic` — calling it directly with diagnostics would not compile. Write the rendered table to `cmd.OutOrStdout()` to match the `runValidate` convention; the summary line is optional here (the wizard cares about block/proceed, not summary stats). If any diagnostic has severity ≥ Error → return exit 1; do NOT open the wizard. Warnings render but do not block. This makes the wizard runtime path use the same diagnostic surface as `devbox validate`.
  4. [x] **distinguish "load error" from "absent file":**
     - [x] if `setupErr != nil` → already handled in step 3 (validator surfaced it and returned exit 1); not reached here
     - [x] if `setupCfg == nil && setupErr == nil` → file is absent. Proceed to the wizard with `Questions: nil`. This is the port-only path: fresh project + no `setup.yml` + port conflicts → wizard opens with just the port-fix step.
     - [x] if `setupCfg != nil` → proceed with `Questions: setupCfg.Questions`
  5. [x] construct the asker via `newHuhAskerFn(cmd.OutOrStdout())` and assemble `setup.WizardDeps` (with `Questions` resolved per step 4)
  6. [x] call `runWizardFn(ctx, deps)`; on success, `local.yml` is on disk
  7. [x] on `errors.Is(err, setup.ErrWizardCanceled)` return the sentinel for exit 130
  8. [x] on success, dispatch via `runDeployRunFn(ctx, cmd, flags, opts)` — which itself reloads cfg via `config.LoadConfig` and runs full `preflight.Run` (this catches any remaining non-port issues + verifies that the chosen port overrides actually resolved the conflicts). The `plan` menu choice uses `runDeployPlanFn` instead.
- [x] completion sanity-check: verify `devbox deploy <TAB>` still completes subcommand names. Cobra's `__complete` path doesn't invoke `RunE`, so adding a `RunE` on the group should not affect completion — include this in the manual verification.
- [x] write Cobra-level tests:
  - [x] non-TTY → help printed + correct exit-code-sentinel returned
  - [x] **malformed `local.yml` → menu refuses to open, returns a wrapped error, exits 1, and the file on disk is byte-identical to the malformed input** (asserts the no-silent-overwrite extension of the visibility rule)
  - [x] **invalid `setup.yml` (strict-decode failure, unknown question type, regex-compile failure, or any other `setup.*` error diagnostic) → diagnostics rendered to stderr via the validate-domain table, exit 1, wizard does not open, `local.yml` byte-identical on disk** (asserts that the Task 9 step-3 validator run actually fires and blocks the wizard — this path is load-bearing because the wizard runtime relies on those validators having run)
  - [x] **absent `setup.yml` + port conflicts present → `runWizardFn` is invoked with `Questions: nil` and non-empty `PortConflicts`** (uses the `loadSetupYAMLFn` + `collectPortConflictsFn` + `runWizardFn` seams to assert dispatch wiring without actually opening huh; asserts step 4's distinguish-load-error-from-absent-file branch is reachable)
  - [x] **menu select → run dispatch:** stub `selectMenuItemFn` to return `run` and stub `runDeployRunFn` to record calls; assert `runDeployRunFn` is invoked once with the expected `cmd`/`flags`/`opts`. Stub `selectMenuItemFn` to return `exit`; assert neither `runDeployRunFn` nor `runDeployPlanFn` is invoked and the command returns nil. Parallel case for `plan` → `runDeployPlanFn`.
  - [x] **Override `ui.IsInteractiveFn` in every test that expects the menu to open.** The production guard short-circuits to help/exit-2 in non-TTY environments — including `go test`, which is exactly the case here. Assign a stub returning `true` and restore via `t.Cleanup`, matching how other command tests in `internal/command/` handle this gate. The non-TTY test case is the exception: it relies on the production behavior, so it does NOT override.
  - [x] Use a fresh root command per test (Cobra accumulates flag state across `Execute()` calls). Menu rendering itself is not asserted (huh is exercised manually); these tests target the dispatch wiring above huh, via the package-level seams.
- [x] run `go test ./internal/command/...` — must pass before next task

### Task 10: Remove `devbox deploy step`

- [x] delete `newDeployStepCmd` and its helpers in `internal/command/deploy.go` (lines 793–919 plus any private helpers used only by it)
- [x] remove its registration in `newDeployCmd`
- [x] remove or update any existing tests referencing `deploy step`
- [x] regenerate `docs/reference/cli/` (`devbox docs generate`); commit regenerated files
- [x] run `make build && go test ./...` — must pass before next task

### Task 11: Documentation

- [ ] create `docs/reference/config/setup.md` — schema for `setup.yml`, all question types, validate presets, write-scope rules (with the forbidden-prefix list verbatim)
- [ ] update `docs/reference/config/devbox.md` — note that the wizard may write into `local.yml` for keys merged into `cfg.Raw`
- [ ] update `docs/internals/packages.md` — add entries for `internal/setup/` and `internal/validate/setup/`, plus a one-line note on the deploy menu under "User commands / UI"
- [ ] sanity-grep that no doc still references `devbox deploy step`

### Task 12: Verify acceptance criteria

- [ ] `devbox deploy` in a TTY shows menu; non-TTY prints help + exits 2
- [ ] Wizard item visibility matches the rule in Task 9: shown iff `local.yml` is missing or parses to an empty map, AND (`setup.yml` exists OR port probe finds at least one conflict). Once `local.yml` has any content, Wizard is permanently hidden. A malformed `local.yml` makes the menu refuse to open (exit 1) rather than treating the file as empty or non-empty.
- [ ] Cancel during wizard leaves `local.yml` untouched on disk
- [ ] `devbox deploy run` / `plan` / `state` behavior unchanged
- [ ] `devbox deploy step` is gone from `--help` and from generated CLI reference
- [ ] `devbox validate` surfaces `setup.*` diagnostics for malformed `setup.yml`
- [ ] full `make test`
- [ ] `make lint`

## Technical Details

- **Allowed `writes:` paths (canonical, see Task 5 `setup.writes_scope`):**
  - Forbidden top-level namespaces: `info.*`, `styles.*`, `docker.*`, `binaries.*`.
  - Under `services.<name>.`, exactly three leaf shapes are allowed:
    - `services.<name>.enabled` (scalar bool, requires `type: confirm`)
    - `services.<name>.ports.<port_name>` (scalar int, requires `type: input` + `validate.preset: port`)
    - `services.<name>.hosts.<host_name>` (scalar string, requires `type: input`)
  - Anywhere else (e.g. `db.*`, `app.*`, custom namespaces) — any path is fine; the wizard writes the typed answer value verbatim (scalar for `input` / `select` / `confirm`, `[]string` for `multiselect`) and trusts the consumer template to handle the type.
  - Reuse `overlayAllowedKeys` from `internal/config/devbox.go` (wrap as `config.IsServiceOverlayKey` if still unexported) — don't duplicate the list. Reuse `config.ValidIdentifierKey` (exported in Task 5 prerequisite) for every dot-path segment so identifier rules can't drift between wizard and config loader.
- **Preflight vs. port conflicts:** `preflight.Run` returns `*preflight.Error` on any error diagnostic, including `env.ports_free` (`internal/preflight/preflight.go:123,147`). Running it before the wizard would abort before the user can fix conflicts. The chosen design avoids both the contradiction and a new "partial preflight" mode: probe `env.CollectPortConflicts` standalone for the wizard's port step (Task 6 introduces this exported typed probe — the wizard does NOT parse `validate.Diagnostic.Message` to recover service/port metadata), then run full `preflight.Run` after `local.yml` is written and config reloaded. The post-write preflight is the source of truth — if the user picked an override that's still occupied, preflight will catch it on the retry.
- **Config reload after the wizard:** `config.LoadConfig` reads `devbox/local.yml` at load time (`internal/config/devbox.go:960-985`) and resolves the service port overlay (`:1031-1045`). The menu does NOT reuse the pre-wizard `*config.DevboxConfig`; it dispatches to `runDeployRun`, which calls `config.LoadConfig(flags.configPath)` itself. That keeps the seam in one place and makes both subcommand and menu paths identical.
- **Answer-merge semantics:** deep-merge over `LoadLocalYAML(localPath)` (which may be empty). That preserves anything the user might have set before opening the menu. Question overlay and port overlay are merged separately into `existing` so a buggy question writing to `services.<name>.ports.*` (which the `setup.writes_scope` validator should already reject) can't override a port the user explicitly fixed.
- **Port-conflict data shape:** `env.PortConflict{Service, PortName string, RequestedPort int, OccupiedBy string}` — owned by `internal/validate/env/` (Task 6), consumed unchanged by the wizard. Result written under `services.<service>.ports.<port_name>`. The validator (`portsFreeValidator`) and the wizard share the same probe; there is no second port enumeration path.
- **huh import:** `huh "charm.land/huh/v2"` — already in go.mod and used by `internal/ui/`. The wizard uses `ui.Theme()` for palette/glyphs and `ui.RunWithPromptHooks` for LiveLine integration; it does not import `github.com/charmbracelet/huh`.
- **Deploy subcommand naming:** the deploy group has `plan`, `run`, `step`, `state`. There is no `deploy status`. The project-wide status view is `devbox status deploy` (different command tree). Plan/test text must not reference a nonexistent `deploy status`.
- **Cancel vs usage error:** `ErrWizardCanceled` (sentinel in `internal/setup/`) → exit 130. Non-TTY bail (no subcommand, no terminal) → exit 2. All other wizard errors → exit 1. Map these in whatever central error→exit table already exists in `cmd/devbox/main.go` (verify before Task 9).
- **Menu → run/plan dispatch:** the extracted helpers (`runDeployRun`, `runDeployPlan`) are the canonical reload point — each loads cfg from disk on entry. The menu calls them after the wizard succeeds; the existing subcommands call them with no wizard step. One code path, two entry points.
- **Test isolation reminder:** every Cobra test in Task 9 builds a fresh root command (e.g. via the existing `newRootCmd(...)` factory). Cobra accumulates flag state across `Execute()` calls on the same instance — sharing the root across tests will produce flaky results.

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only.*

**Manual verification:**
- Run `devbox deploy` in an interactive terminal on a real project to walk the menu visually.
- Run the wizard happy path against a fresh project with a `setup.yml` containing one of each question type; confirm `local.yml` ends up with the expected nested shape.
- Test Ctrl-C at each wizard step; confirm `local.yml` is not created/modified.
- Provoke a port conflict (run a host service on a declared port) and confirm the port-override step appears and writes under `services.<name>.ports`.
- Confirm `devbox deploy` in a non-TTY context (e.g. piped to a file) prints help and exits 2.

**External system updates:**
- None expected. No consuming projects depend on `devbox deploy step`.
