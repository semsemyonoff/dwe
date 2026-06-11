# `dwe vars` — Command for Working with the `vars:` Sandbox

## Overview

The config-formalization work (`docs/plans/completed/20260610-config-formalization-vars.md`) made `vars:` the single legal home for all free-form project values: `Vars map[string]any` (yaml tag `vars`) on `DweConfig`, 3-layer merged (`workspace.yml` → `defaults.yml` → `local.yml`), resolved via `config.ResolvePath(cfg.Raw, "vars.x.y")` and referenced in templates as `${vars.x}` or structurally as `from: vars.x` / `default_from:` / `when:`.

Because every custom value now lives under one formalized namespace, we can build a first-class tool to **inspect, read, edit, and map** these values. This plan adds a new top-level command `dwe vars` with four subcommands (`get`, `set`, `inspect`, `list`) plus an interactive TUI browser (mirroring `dwe commands`), and — as an explicitly-chosen part of the same change — replaces the comment-destroying map-based `local.yml` writer with a comment-preserving `yaml.Node` round-trip writer shared by `vars set`, `services enable/disable`, and the setup wizard.

**Key benefits:**
- Discoverability: one place to enumerate, read and understand every project var.
- Safe editing: `set` writes `local.yml` overrides while preserving comments/formatting (and so do `services`/`setup` after this change).
- Traceability: `inspect` statically maps every place a var is used (`${vars.x}` in templates + structural `from:`/`default_from:`/`when:`).

## Context (from discovery)

Files/components involved:

- **Config core:** `internal/core/project/config/workspace.go` — `DweConfig.Vars` (`yaml:"vars"`), `DweConfig.Raw`, 3-layer merge in `LoadConfig` (~1422-1705), `deepMerge`, `ResolvePath` (~3213-3230). `vars.*` resolves naturally via `Raw` dot-paths.
- **Local writer (chokepoint):** `internal/core/project/local/local_yaml.go` — `LoadLocalYAML` (`yaml.Unmarshal` → `map[string]any`, drops comments) and `WriteLocalYAML` (`yaml.Marshal(map)`, atomic temp+rename, mode 0o600, drops comments). `SetLocalEntryEnabled`. `internal/core/project/local/services.go` — `ApplyServiceTogglesToYAML`, `DiffServiceSelection`, `ValidateServiceToggle`.
- **Writer callers (all funnel through `WriteLocalYAML`):** `internal/cli/service/service_toggle.go` (`mutateAndPlan` ~366-373, `mutateAndPlanBatch` ~474-481) and `internal/core/workflow/setup/wizard.go` (~127-157, builds 3 overlays via `BuildOverlay`/`BuildPortOverlay`/`BuildServiceTogglesOverlay`, merges via `MergeIntoLocal` in `merge.go`, then `WriteLocalYAML`).
- **TUI browser (already generic):** `internal/core/ui/cmdbrowser/` — `run.go` `Item` struct is **already decoupled** from `CommandDef` (`ID`, `Description`, `Type`, `Private`, `ParamCount`, `Inspect func(width int) string`); the namespace tree is built from `Item.ID` dot-paths (`tree.go`); `Action`/`Mode`/`Result` enums; fallback to `widgets.RunSelector` below 60×15; modal filter/inspect overlays. `Result.ForceParamForm` (key `e`) is the precedent for an "edit" intent.
- **Command tree precedent:** `internal/cli/command/` — `command.go` (`NewCmd`, TUI vs non-interactive dispatch ~147-162: `!widgets.IsInteractiveFn(...) || nonInteractiveEnv()` → falls back to `commands list`), `list.go` (`newCommandListCmd`, group filter, JSON via `commandsListJSON`, tree render), `inspect.go`, `runbyid.go` (`buildAskFields` → `ask.Run`), `bridgegate.go`.
- **huh forms:** `internal/core/ui/ask/` — `Run(ctx context.Context, title string, fields []Field, opts RunOptions)` (ask.go:124); `Field{Key,Title,Description,Kind,Required,Default,Validate}`; `FieldInput`; theme via `styles.Theme()` / `styles.HuhTheme`. Stubbed in tests via the package-var seam `runAsk = ask.Run` (precedent: `command.go:22`, stubbed in `runbyid_test.go:59`) — NOT by stubbing `ask.Run` directly.
- **Renderers / palette:** `internal/core/ui/render/` (return strings; only `cli/` writes stdout), `internal/core/ui/styles/` (7-token palette, `styles.Theme()`).
- **CLI cross-cutting:** `internal/cli/cmdctx/` — `WriteData[T]`, `WriteError`, `Err`/`ErrWrap`, `CompletionConfigPath`, `nonInteractiveEnv`. `internal/cli/root.go` (registration; `cmdService`/`cmdRender`/`cmdValidate`/`cmdScaffold` are `groupConfiguration`; `cmdCommand` is `groupAdvanced`).
- **Bridge:** `internal/cli/bridgepolicy.go` — `bridgeAllowedTopLevel` map (add `vars`; add a `render config` nested exception alongside `bridge status`), `bridgeCommandAllowed` (prefix-wide — see security note), `bridgeInvokedFromContainer()`. `internal/core/bridge/session.go` env policy (unchanged). New config: `bridge.vars_writable` top-level block (deny-by-default container-write allowlist) — must be added to `allowedRootKeys` per the AGENTS.md strict-root contract.
- **Render tree:** `internal/cli/render/` — `render.go` (`NewCmd`: env/ide/ai/git/config), `config.go` (`Use: "config [service]"`). The container exception targets `render config` only.
- **Template resolver (no change):** `internal/shared/tpl/render_command.go` — `CompileVarSyntax` special-cases `param`/`context`/`snapshot`/`generated`/`files`/`host`; everything else (incl. `vars.*`) falls through to `{{ resolve .Raw "<dotpath>" }}`. Useful as the source of truth for the `${vars.x}` syntax we scan for.
- **Docs:** `docs/reference/config/` + `docs/i18n/ru/reference/config/`; embedded sync via `make build` (`scripts/sync-embedded-docs.sh`, `content_hashes_gen.go`).

Related patterns found:
- `cmdbrowser.Item` already generic → generalization is small: add an **edit** action and map vars → Items; do NOT fork a second browser.
- Non-interactive dispatch pattern (`commands` → `commands list`) is copy-able verbatim for `vars` → `vars list`.
- Project already uses `yaml.Node` for reading (`validate/config/deprecations.go`, `composegen.go` for ordering) — node round-trip is familiar ground, but no existing round-trip *edit* helper.

Dependencies identified: new packages `internal/cli/vars/`, `internal/core/project/varsusage/`; modifications to `internal/core/project/config/workspace.go` (new `bridge:` block + shared layer helper), `internal/core/project/local/`, `internal/cli/service/`, `internal/core/workflow/setup/`, `internal/core/ui/cmdbrowser/`, `internal/core/ui/render/`, `internal/cli/root.go`, `internal/cli/bridgepolicy.go` (+ `render config` exception), plus docs.

## Development Approach

- **Testing approach: Regular** (code first, then table-driven tests) — per project convention (matches the completed formalization plan).
- Complete each task fully before moving to the next; small, focused changes.
- **Every task MUST include new/updated tests** (success + error/edge), listed as separate checklist items.
- **All tests must pass before starting the next task.** Use `make test` (NOT `go test ./...` — embedded docs are generated/gitignored). After any `docs/` edit run `make build` first (syncs embedded docs + regenerates `content_hashes_gen.go`). For focused loops: `make embedded-docs` once, then `go test ./internal/cli/vars/...` etc.
- Honor AGENTS.md contracts: config accessors (`GitBin`, …) not raw fields; section-renderer contract (`render/` returns strings, only `cli/` writes stdout); JSON output mode via `cmdctx.WriteData`/`cmdctx.Err`; display-string localization via `rflags.I18n` + `store.*`; completion path safety via `cmdctx.CompletionConfigPath`; bridge env/command policy.
- Keep this plan in sync with actual work; update on scope changes.

## Testing Strategy

- **Unit tests** required every task (table-driven where natural — node round-trip, layer resolution, usage scan, value coercion, CLI parsing, JSON shape).
- **No UI e2e tests** (project convention). Golden/snapshot tests exist for docs/rendering and the command browser — keep them green; the `cmdbrowser` generalization MUST NOT break the `dwe commands` browser goldens.
- After the final task, run `make test` (and `make test-race` if quick) and `make lint` to green.

## Progress Tracking

- Mark completed items `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix; blockers with ⚠️ prefix.
- Update plan if implementation deviates from scope.

## Solution Overview

**Command surface** (`internal/cli/vars/`, `NewCmd(groupID, flags)`, registered in `cli/root.go` under `groupConfiguration` — alongside `service`/`render`/`validate`/`scaffold`, the config-mutation group):

- `dwe vars get <var>` — print value. Leaf → scalar; namespace (`vars.db`) → subtree as YAML. Default = effective value.
- `dwe vars set <var> [value]` — write to `local.yml`. No `value` → huh form (inspect-style info + input field). Value parsed as a YAML scalar (`true`/`42`/`1.5` typed; quoted → string). **Path-confined to `vars.*`** (else error — also the container trust boundary). JSON mode with no value → `vars_value_required` error (no form).
- `dwe vars inspect <var>` — per-layer values (author default, local override, effective) + originating file + every static usage.
- `dwe vars list [namespace]` — flat list of `vars.*` leaves, optional namespace filter (mirrors `commands list`).
- `dwe vars` (no args) → TUI browser; non-interactive/container (`!IsInteractive || nonInteractiveEnv()`) → `vars list`.

**Data model.** A "var" = a leaf dot-path under `vars:` (e.g. `vars.db.password`); nested maps are namespaces (tree nodes). Enumeration = walk leaves of merged `cfg.Vars`. Layers: *author default* (`workspace.yml`/`defaults.yml`), *local override* (`local.yml`), *effective* (post-merge — what `${vars.x}` resolves to). Origin = last non-empty layer; to attribute it we load each layer raw and resolve `vars.*` in each.

**Comment-preserving writer (shared).** New `yaml.Node` round-trip in `internal/core/project/local/`: `LoadLocalYAMLNode` → `ApplyOverlayToNode(doc, overlay)` (recursive patch: scalar → replace existing value node's `Value` keeping style/comments; map → recurse into nested mapping, create if absent; missing key → append pair; no deletion) → `WriteLocalYAMLNode` (same atomic temp+rename, mode 0o600). **All three writers route through it** (`vars set`, `services` toggle, setup wizard) so comments survive everywhere.

**Usage scan (FIELD-AWARE, not a flat file grep).** Pure function in `internal/core/project/varsusage/` → structured result (no stdout). The renderer differs per field, so a blanket `${vars.x}` grep over whole files yields both false positives (comments, quoted literals, non-`${...}`-rendered fields) and false negatives (Go-template `.Vars`/`.Raw` references). Therefore: walk each YAML file's `yaml.Node` and scan ONLY the fields the runtime actually renders, keyed by file type — confirming the per-field render path against the code during implementation (Task 4). Two syntaxes: (1) `${vars.x}` (for fields rendered via `tpl.CompileVarSyntax`/`RenderCommand`) matched with the renderer's OWN pattern — `tpl.varPattern = \$\{([a-zA-Z_][a-zA-Z0-9_.]*)\}` (`internal/shared/tpl/render_command.go:92`); reuse it (export+import or replicate with a parity test); (2) `vars.x` as the value of structural keys (`from:`/`default_from:`, and typed `when:` where the reference lives in `when.expr`, NOT a bare `when: vars.x` scalar). Fields rendered via Go templates (e.g. info items through `tpl.Render`/`EvalCondition`, referencing `.Vars`/`.Raw`) are EITHER scanned for that syntax too OR explicitly documented as unsupported — decide in Task 4. Render templates (`services/*/render/**`) are arbitrary text (not YAML) → regex pass only, no node walk. Match by exact path OR namespace prefix.

**TUI generalization.** `cmdbrowser.Item` is already generic; add an **edit** action (`ActionEdit` + keymap, generalizing the `ForceParamForm`/`e` precedent), map vars → Items (effective value in `Description`, layer-source as the `Type` badge, `Inspect` closure rendering the inspect view). No second browser; `dwe commands` behavior unchanged.

## Technical Details

- **YAML scalar coercion (`set`):** parse the raw arg via a `yaml.Node` and REQUIRE a `ScalarNode` (reject maps/sequences); derive the typed value (bool/int/float/string/null) from the node so the writer emits the correct bare-vs-quoted scalar. Explicitly-quoted input stays a string. Pin the ambiguous cases (timestamp/octal/leading-zero/`null`/`~`/empty/`yes|no|on|off`/`1.2.3`) per Task 3. Build the overlay as nested `map[string]any` from the `vars.<...>` dot-path.
- **Path confinement:** `set` rejects any `<var>` whose first segment ≠ `vars` (typed error). This is the same gate that makes container `set` safe.
- **`set` flow (mirrors `service_toggle.mutateAndPlan`):** acquire project locks via `cmdctx.AcquireProjectLocksOrReport(baseDir, w)` → capture pre-state bytes of `local.yml` for rollback → `LoadLocalYAMLNode` → `ApplyOverlayToNode` → `WriteLocalYAMLNode` → reload config (so subsequent output reflects the write); on any post-write error, restore the captured bytes. **No preflight** (not a lifecycle/stack mutation), **but DO acquire project locks** — `service_toggle.mutateAndPlan`/`mutateAndPlanBatch` (service_toggle.go:338,452) call `lock.AcquireProjectLocks`, and since Task 1/2 make `vars set` and the `services` toggle share the same `local.yml` node writer, a lock-free `set` would race a lock-holding `services enable` on the same file (TOCTOU). Symmetry via the shared lock is the fix.
- **Effective vs origin:** reuse `config.ResolvePath`. For origin, load each layer's raw map (the loader already reads them during merge — expose a small helper or re-read the three files) and resolve `vars.<path>` per layer; report the highest layer that yields a value.
- **Usage scan sources (verify the render path per field during Task 4):** render templates (`workspace/services/*/render/**`, arbitrary text → regex only), `info.yml` (`text`/`value`/`when` — confirm whether these render via `${...}` or Go templates `.Vars`/`.Raw`), command files (`cmd`/`when`/`env`/`with`), `deploy.yml`/`reset.yml`/`lifecycle.yml` (note: shell-step `cmd` may execute as raw shell, not via `tpl.RenderCommand` — confirm; typed `when:` references live in `when.expr`), `docker.yml` (`project_name` via `resolveVarTemplate`), exports (`when`); structural `from:`/`default_from:` in context defs, param defaults, exports, files-gate. Honest caveat in output: dynamically-built paths are not tracked.
- **JSON shapes:** `get` → `{var, value}`; `list` → `{vars:[{path, value, layer}]}`; `inspect` → `{var, layers:{author,local,effective}, origin, usages:[{file, line, kind, text}]}`. All via `cmdctx.WriteData[T]`; errors via `cmdctx.Err`/`ErrWrap`.
- **Bridge:** add `"vars": true` to `bridgeAllowedTopLevel` (makes `get`/`list`/`inspect` reachable; `set` reachable but RUNTIME-gated). `bridgeCommandAllowed` is prefix-wide, so the allowlist alone cannot distinguish read vs write — and `vars.*` path confinement does NOT bound behavioral impact (vars feed templates, commands, deploy/reset/lifecycle, docker project-name). Therefore container `set` is **deny-by-default** and opt-in via a new config allowlist `bridge.vars_writable` (a list of `vars.*` path patterns, exact or `vars.x.*` prefix). `vars set` enforces it ONLY when `bridgeInvokedFromContainer()` is true: target var must match a pattern, else typed error `vars_not_container_writable`; from the host, `set` is unrestricted. Empty/absent list → no container writes (the safe default). The user's intent (edit a config value from a devcontainer without hopping to the host) is served by listing the editable vars. Separately, allow **`render config`** from the container (the nested-exception precedent `bridge status`) so a container can regenerate config after a `set` — `render`'s other subcommands (env/ide/ai/git) stay host-only. CAVEAT: `render config` has a `--harvest` flag that MUTATES host state (`.dwe/generated.yml` via `HarvestGenerated`, acquiring locks) — a separate container-write path that bypasses `vars_writable`. So `render config` must reject `--harvest` when `bridgeInvokedFromContainer()` (typed error); only the read-only render is container-reachable. TUI auto-disabled in container via the existing non-interactive dispatch → `vars list`.

## What Goes Where

- **Implementation Steps** (`[ ]`): all code, tests, and docs in this repo.
- **Post-Completion** (no checkboxes): manual smoke tests (TTY form, container `set` via bridge), and downstream projects choosing to adopt `dwe vars` in their workflows.

## Implementation Steps

### Task 1: Comment-preserving `yaml.Node` round-trip writer in `local/`

**Files:**
- Create: `internal/core/project/local/local_node.go`
- Create: `internal/core/project/local/local_node_test.go`
- Modify: `internal/core/project/local/local_yaml.go` (keep `WriteLocalYAML` as a thin wrapper over the node writer, or mark it for removal once callers migrate in Task 2)

- [x] Implement `LoadLocalYAMLNode(localPath string) (*yaml.Node, error)` — read file into a document `*yaml.Node`; missing file → a fresh empty mapping document (not an error), mirroring `LoadLocalYAML`.
- [x] Implement `ApplyOverlayToNode(doc *yaml.Node, overlay map[string]any) error` — recursive patch: scalar → replace the matched value node's `Value`, and set `Tag`/`Style` **from the coerced new value, NOT from the old node** (preserve only `HeadComment`/`LineComment`/`FootComment`); nested map → recurse into the child mapping node, creating it if absent; absent key → append a new key+value pair; never delete. **Style/Tag must derive from the new value** — blindly preserving a previous `DoubleQuoted` style would serialize a coerced `true`/`42` as `"true"`/`"42"`, which reloads as a string and breaks the `set` coercion contract.
- [x] Define and implement a node-aware `validateMergeable` guard before applying an overlay branch: descending a map overlay through an existing **scalar / sequence / alias / explicit null / merge-key (`<<`) -derived** node must be an explicit, tested decision (reject by default to avoid silently discarding the dev's `local.yml` data), with the documented exception ported from `setup/merge.go` (legacy bare-int port leaf → `{port: N}` map upgrade). Handle anchors/aliases, multi-document files (operate on the first document / error on multi-doc), flow-style mappings, and comment-only/empty documents explicitly.
- [x] Implement `WriteLocalYAMLNode(localPath string, doc *yaml.Node) error` — marshal the node and write via the existing atomic temp+rename + `MkdirAll(0o755)` + `Chmod(0o600)` logic (extract/share the atomic-write helper with `WriteLocalYAML`).
- [x] Decide and document the fate of map-based `WriteLocalYAML`: keep as a thin compatibility wrapper (`map → marshal-to-node → ApplyOverlayToNode onto empty doc`) OR plan its removal in Task 2; note the decision in the file comment.
- [x] CHARACTERIZATION FIRST (before Task 2 swaps callers): capture golden output of the current map-based `WriteLocalYAML` for representative inputs (empty file, existing tree, nested service toggle) and assert the node writer reproduces semantically-equivalent YAML. Explicitly decide whether key-ordering changes are acceptable — note that map-based output is already nondeterministically ordered, so the node round-trip is an improvement, not a regression.
- [x] Write tests: comment/blank-line/formatting preserved across a scalar edit; overwriting a quoted string with a coerced bool/int emits a BARE scalar (reloads typed); overwriting a bare bool/int with an explicitly-quoted string stays a string; new nested key inserted into existing tree; brand-new file created; deep nesting (`vars.a.b.c`).
- [x] Write edge/error-case tests: malformed YAML on load; non-mapping document root; overlay descending through scalar / sequence / alias / explicit null / merge-key (rejected per the guard); legacy bare-int port → map upgrade still allowed; flow-style mapping; comment-only / empty document.
- [x] `make embedded-docs` once, then `go test ./internal/core/project/local/...` (focused) — must pass before Task 2.

### Task 2: Migrate `services` toggle + setup wizard to the node writer

**Files:**
- Modify: `internal/cli/service/service_toggle.go` (`mutateAndPlan`, `mutateAndPlanBatch`)
- Modify: `internal/core/project/local/services.go` (`ApplyServiceTogglesToYAML` → produce an overlay or patch a node)
- Modify: `internal/core/workflow/setup/wizard.go` (write path ~127-157) and `internal/core/workflow/setup/merge.go` if `MergeIntoLocal` needs a node-aware variant
- Modify: corresponding `*_test.go`

- [x] Re-express service toggles as an overlay (`{services:{<name>:{enabled:bool}}}`) applied via `ApplyOverlayToNode` + `WriteLocalYAMLNode`, replacing the load-map → mutate-map → `WriteLocalYAML` path. Preserve validation (`ValidateServiceToggle`) and rollback semantics. (New `local.ServiceTogglesOverlay`; `service_toggle.go` `mutateAndPlan`/`mutateAndPlanBatch` now node-write.)
- [x] Re-express the setup wizard write: build the same 3 overlays, apply them onto the loaded node (sequentially, last wins) instead of `MergeIntoLocal` + `WriteLocalYAML`. **Preserve `MergeIntoLocal`'s guarantees** — `validateMergeable` (`setup/merge.go:91-100`) rejects map-over-scalar collisions EXCEPT the legacy bare-int port leaf → `{port:N}` upgrade. The node-aware guard from Task 1 must reproduce both the rejection and that single exception, or setup can silently discard developer `local.yml` data / break the port upgrade. Preserve overlay precedence and existing behavior. (`wizard.go` applies q/p/s overlays via `ApplyOverlayToNode`; the Task 1 node guard reproduces both behaviors.)
- [x] Remove the now-dead map-based write path (or confirm the thin wrapper from Task 1 is the only remaining `map` entry point) — no behavior change intended. (`WriteLocalYAML` is now a thin wrapper over the node writer; `ApplyServiceTogglesToYAML`, `MergeIntoLocal`, `deepMerge`, `validateMergeable`, `deepCopyMapInternal` removed.)
- [x] Update/extend tests: existing service-toggle and setup tests still pass; ADD a round-trip test proving comments in `local.yml` survive a `services enable` and a wizard run; ADD a test that all three writers (vars/services/setup) produce byte-identical output for an equivalent overlay onto the same base file; ADD a test that the legacy bare-int port-leaf upgrade still works through the node path and that a map-over-scalar collision is still rejected. (`TestRoundTrip_ServiceTogglePreservesComposeExtra`, `TestWizardRunPreservesComments`, `TestNodeWriter_ThreeWriterParity`, `TestWizardRunLegacyPortLeafUpgrade`, `TestWizardRunMapOverScalarRejected`.)
- [x] Write error/edge tests: write failure triggers rollback to captured bytes; empty/absent `local.yml` handled. (Existing `TestSingleToggle_Rollback*` exercise the migrated node path; `TestWriteLocalYAMLNode_AtomicNoTempLeftover` + node-writer empty/absent-file tests from Task 1.)
- [x] `make test` (config/local/service/setup packages + dependents) — must pass before Task 3.

### Task 3: Vars leaf enumeration + per-layer resolution helpers

**Files:**
- Create: `internal/core/project/varsusage/resolve.go` (or a `vars` helper file under `config/` if it fits better — keep stdout-free)
- Create: `internal/core/project/varsusage/resolve_test.go`

- [x] Implement leaf enumeration over merged `cfg.Vars` → sorted `[]string` of dot-paths (`vars.a.b`), treating nested maps as namespaces. Deterministic ordering (sort) to avoid flaky tests.
- [x] Implement `ResolveVar(cfg, path) (value any, found bool)` (effective) via `config.ResolvePath(cfg.Raw, path)`; subtree return for namespace paths.
- [x] Implement per-layer resolution by FACTORING layer loading/validation/merge into `config` (the three layers are local variables inside `LoadConfig` — only merged `cfg.Raw` survives, so a `varsusage` re-read would duplicate path selection, optional-layer handling, strict-root/legacy validation, and `deepMerge`'s nil-skip and lose source-attributed errors). Expose a shared helper (e.g. `config.LoadConfigLayers` / `config.ResolveLayeredPath`) used by BOTH `LoadConfig` and vars inspection so the two cannot drift. Return `{author, local, effective}` + originating layer for a path.
- [x] Tests for the layer helper: absent optional `defaults.yml`/`local.yml`; a `local.yml` explicit-null that does NOT override (per `deepMerge` nil-skip); scalar-over-map and map-over-scalar precedence; strict-root/legacy-key error attribution names the right file; origin matches effective.
- [x] Implement YAML-scalar coercion `CoerceScalar(string)` with a PINNED grammar: parse via `yaml.Node`, REQUIRE a `ScalarNode` (reject `{...}` maps and `[...]` sequences — they violate the var=leaf model), and explicitly decide+document behavior for the ambiguous cases below. Surprising yaml.v3 defaults to pin: bare timestamps → `time.Time` (treat as string), legacy octal `0755`/leading-zero `01` (decide: keep as string to avoid lossy reinterpretation), `~`/`null`/empty arg → null (decide whether `set x ""` writes empty-string vs null — recommend explicit `""` → empty string, bare `null`/`~` → YAML null), `yes`/`no`/`on`/`off` → strings under interface{} decode (document; not YAML 1.1 bools), `1.2.3` → string.
- [x] Write tests: enumeration over nested vars; effective resolution (leaf + subtree); 3-layer override picks the right origin; missing path → not found. Coercion table covering EACH pinned case: `true`/`false`, `42`, `1.5`, `"quoted"` → string, plain string, `yes`/`no`/`on`/`off`, `0755`, `01`, `1.2.3`, empty `""`, `null`, `~`, `2024-01-02` (timestamp), and `{a: b}`/`[a]` → rejected (not a scalar).
- [x] `make embedded-docs` once, then `go test ./internal/core/project/varsusage/...` (focused) — must pass before Task 4.

### Task 4: Static usage scanner

**Files:**
- Create: `internal/core/project/varsusage/scan.go`
- Create: `internal/core/project/varsusage/scan_test.go`
- Create: `internal/core/project/varsusage/testdata/` (fixture project tree covering all sources + both syntaxes + prefix matches)

- [x] Define the result type: `Usage{File string; Line int; Kind string; Text string}` and `ScanResult` keyed/sortable by file then line. (`scan.go`; sorted file→line→kind→text, de-duped on the full tuple.)
- [x] FIRST: confirm, per file type, WHICH fields the runtime renders and with WHICH engine (`${...}` via `tpl.CompileVarSyntax`/`RenderCommand` vs Go templates `.Vars`/`.Raw` via `tpl.Render`/`EvalCondition` vs raw shell vs `resolveVarTemplate`). Record the field→engine map in the package doc; the scanner keys off it. This prevents both false positives (scanning a non-rendered field/comment/literal) and false negatives. (Field→engine map documented in `scan.go`: `templatedKeys` cmd/text/value/project_name/confirm/when-scalar + `templatedMapKeys` env/with render `${...}`; `structuralKeys` from/default_from carry a dot-path; typed `when.expr` Go-template resolves matched structurally.)
- [x] Implement `${vars.x}` scanning using the renderer's pattern semantics — `tpl.varPattern = \$\{([a-zA-Z_][a-zA-Z0-9_.]*)\}` (`internal/shared/tpl/render_command.go:92`). Export and import it (preferred) or replicate verbatim with a parity test; keep only matches whose head segment is `vars`. Apply ONLY to fields confirmed to render via `${...}`, plus render templates (arbitrary text, regex over the whole file). Capture exact line + line text. Must NOT permit `${ vars.x }` (internal whitespace) nor a leading digit. (Exported `tpl.VarPattern` and imported it; head-segment filtered to `vars`; render templates under `services/*/render/**` get a raw-text pass.)
- [x] Implement structural scanning via `yaml.Node` walk over YAML files: values of `from:`/`default_from:` with a `vars.` prefix, and typed `when.expr` (NOT a bare `when:` scalar); line from `node.Line`. Walk only the relevant fields — do not match arbitrary scalars. (`walkYAML` + `hitsForField`; `varDotPath` matches bare `vars.x` tokens inside `when.expr`.)
- [x] Decide and document Go-template `.Vars`/`.Raw` references (info items etc.): either scan for them as a third kind OR list them as an explicit "not tracked" limitation in the output caveat. Do not silently miss them without saying so. (DECIDED: Go-template field access `.Vars.x`/`.Raw.vars.x` is NOT tracked — documented as a caveat in the package doc; the `resolve .Raw "vars.x"` form inside `when.expr` IS tracked structurally via `varDotPath`.)
- [x] Match a queried var by exact path OR namespace prefix (`${vars.db.host}` counts toward `vars.db`); de-duplicate identical (file,line,kind) hits; sort deterministically. (`refMatches` dot-boundary in both directions; `dedupeAndSort`.)
- [x] Write golden/table tests over the fixture: every source hit found, both syntaxes, prefix-vs-exact; FALSE-POSITIVE cases NOT matched — `${vars.x}` inside a YAML comment, inside a non-rendered/quoted literal field, `${ vars.x }` (whitespace), `${1vars}` (leading digit), a bare `when: vars.x` scalar that is not a real reference; FALSE-NEGATIVE cases matched — a real `${vars.x}` in a deploy `cmd`, a `from: vars.x`, a typed `when.expr` referencing the var, and (if supported) a Go-template `.Vars` reference; relative paths from project root. (`scan_test.go` + `testdata/proj/` fixture tree; `TestScanUsages`, `TestScanUsages_FalsePositives`, `TestScanUsages_TextCaptured`, `TestRefMatches`.)
- [x] `make embedded-docs` once, then `go test ./internal/core/project/varsusage/...` (focused) — must pass before Task 5.

### Task 5: Renderers for get / list / inspect

**Files:**
- Create: `internal/core/ui/render/vars.go`
- Create: `internal/core/ui/render/vars_test.go`

- [x] `RenderVarValue(...)` — scalar or YAML subtree string for `get`.
- [x] `RenderVarsList(...)` — flat list of leaves (path + effective value + layer badge), namespace-filterable; styled via `styles` palette; returns string.
- [x] `RenderVarInspect(...)` — the per-layer block (`author`/`local`/`effective`), origin file, and grouped usages (relative path, `file:line`, line text with the matched fragment accented), plus the "dynamic paths not tracked" caveat. Width-aware wrapping.
- [x] Ensure all functions RETURN strings (no `io.Writer`); per section-renderer contract only `cli/` writes stdout.
- [x] Write tests: golden output for list (filtered/unfiltered), inspect (with/without local override, with/without usages), value (scalar/subtree). Use deterministic ordering.
- [x] `make embedded-docs` once, then `go test ./internal/core/ui/render/...` (focused) — must pass before Task 6.

### Task 6: `dwe vars` command skeleton + `get` + `list` + registration

**Files:**
- Create: `internal/cli/vars/vars.go` (`NewCmd`, top-level dispatch)
- Create: `internal/cli/vars/get.go`
- Create: `internal/cli/vars/list.go`
- Create: `internal/cli/vars/vars_test.go`
- Modify: `internal/cli/root.go` (register `cmdVars.NewCmd(groupConfiguration, flags)`)

- [ ] `NewCmd(groupID, flags)` building the `vars` cobra tree (aliases optional), with `get`/`list` subcommands and a `RunE` that dispatches the no-arg case (TUI in Task 9; for now wire to `list` so the command is usable).
- [ ] Implement `get <var>`: resolve effective value, render via `RenderVarValue`; JSON via `cmdctx.WriteData` (`{var,value}`); not-found → typed error.
- [ ] Implement `list [namespace]`: enumerate leaves, optional namespace filter (mirror `commands list`), render via `RenderVarsList`; JSON `{vars:[...]}`.
- [ ] Add `ValidArgsFunction` for `<var>` completing `vars.*` leaves via `cmdctx.CompletionConfigPath` (silent empty + `NoFileComp` on error).
- [ ] Localize display strings via `rflags.I18n`/`store.*` (no direct `Description` reads); storage/scan stay English.
- [ ] Register in `root.go`; confirm `dwe vars --help` and `dwe vars list` work.
- [ ] Write tests: `get`/`list` text + JSON, namespace filter, not-found error + exit code, JSON-mode stdout cleanliness, completion returns leaves.
- [ ] `make embedded-docs` once, then `go test ./internal/cli/vars/... ./internal/cli/...` (focused) — must pass before Task 7.

### Task 7: `dwe vars inspect`

**Files:**
- Create: `internal/cli/vars/inspect.go`
- Modify: `internal/cli/vars/vars_test.go` (or add `inspect_test.go`)

- [ ] Wire `inspect <var>`: per-layer resolution (Task 3) + usage scan (Task 4) → `RenderVarInspect` (Task 5).
- [ ] JSON mode: `{var, layers:{author,local,effective}, origin, usages:[...]}` via `cmdctx.WriteData`.
- [ ] Reuse the same `ValidArgsFunction` completion as `get`.
- [ ] Write tests: inspect text + JSON for a var with author-only, with local override, with/without usages; not-found error.
- [ ] `make embedded-docs` once, then `go test ./internal/cli/vars/...` (focused) — must pass before Task 8.

### Task 8: `dwe vars set` (value arg + huh form) + container-write config allowlist

**Files:**
- Create: `internal/cli/vars/set.go`
- Modify: `internal/core/project/config/workspace.go` (new `BridgeConfig{VarsWritable []string}` + `Bridge *BridgeConfig` on `DweConfig`; add `bridge` to `allowedRootKeys`; 3-layer merge; nil-safe accessor)
- Modify: `internal/core/validate/config/workspace.go` (optional: validate `bridge.vars_writable` entries are `vars.*` patterns)
- Modify: `internal/cli/vars/vars_test.go` (or add `set_test.go`); config `workspace_test.go`

- [ ] Add the `bridge:` top-level config block: `BridgeConfig{VarsWritable []string}`, `Bridge *BridgeConfig` on `DweConfig`, add `"bridge"` to `allowedRootKeys` (AGENTS.md strict-root contract — otherwise every fixture using it load-fails), 3-layer merged, nil-safe accessor (`nil`/empty → no container writes). NOTE the distinct scope from per-service `services.<name>.bridge:` in `service.yml` (enablement) — this top-level block is project-wide container-write POLICY; document the distinction.
- [ ] Implement a PINNED `vars_writable` matcher with dot-boundary semantics (do NOT use naive `strings.HasPrefix`): an exact pattern matches only the identical path; a `vars.x.*` wildcard matches only when `target == base` is FALSE and `target` begins with `base + "."` (real dot boundary). So `vars.db.*` allows `vars.db.host` but DENIES `vars.db`, `vars.dbx.host`, and `vars.database.host`. Validate/fail-closed on malformed patterns.
- [ ] Implement `set <var> <value>`: enforce `vars.*` path confinement (typed error otherwise); if `bridgeInvokedFromContainer()`, additionally require the target var to match a `cfg.Bridge.VarsWritable` pattern else typed error `vars_not_container_writable` (host = unrestricted); coerce value (`CoerceScalar`, Task 3 grammar); acquire project locks via `cmdctx.AcquireProjectLocksOrReport(baseDir, w)` (symmetry with the `services` toggle, which shares the writer/file); build overlay; apply via the Task 1 node writer; capture pre-state bytes and rollback on post-write failure; reload config. No preflight (not a lifecycle mutation).
- [ ] Implement `set <var>` (no value, interactive): open the huh form via the `runAsk = ask.Run` seam with one `FieldInput`, `Title`/`Description` carrying inspect-style info (current value per layer). Submit → same write path.
- [ ] JSON mode / non-interactive with no value → `cmdctx.Err("vars_value_required")` (do NOT open a form); with value → confirmation payload via `cmdctx.WriteData`.
- [ ] Add `ValidArgsFunction` completion for the `<var>` arg.
- [ ] Write tests (stub the `runAsk` seam): set scalar with coercion writes correct typed YAML; comment preservation on edit; `vars.*` path-confinement rejection; rollback on simulated write error; JSON value-required error; reload reflects new value. Config tests: `bridge.vars_writable` 3-layer merge; nil/empty accessor.
- [ ] Write container-policy tests (set `DWE_INVOKED_FROM=container`): a var NOT in `vars_writable` → `vars_not_container_writable`; a matching exact path and a `vars.x.*` prefix match → allowed; host context (env unset) → unrestricted. DOT-BOUNDARY denial tests: `vars.db.*` allows `vars.db.host` but denies `vars.db`, `vars.dbx.host`, `vars.database.host`; malformed pattern fails closed.
- [ ] `make embedded-docs` once, then `go test ./internal/cli/vars/... ./internal/core/project/config/...` (focused) — must pass before Task 9.

### Task 9: Generalize `cmdbrowser` + `dwe vars` TUI dispatch

**Files:**
- Modify: `internal/core/ui/cmdbrowser/run.go` (add `ActionEdit`), `keymap.go`, model/update files as needed for the edit intent
- Create: `internal/cli/vars/browser.go` (build `Item`s from vars, run browser, handle inspect/edit results)
- Modify: `internal/cli/vars/vars.go` (no-arg dispatch → browser; non-interactive/container → `list`)
- Modify: `internal/core/ui/cmdbrowser/*_test.go` and add browser-mapping tests in `vars`

- [ ] CORRECTION to the prior review: do NOT gate the EXISTING `e`/`ForceParamForm` binding. `dwe commands` ModeRun already handles `e` as `ForceParamForm` and shows `e edit params` in the footer (`model.go:296-308`); gating that shared binding behind `AllowEdit=false` would REMOVE existing command-browser behavior and break its goldens — the opposite of intended. Keep the ModeRun `e`/`ForceParamForm` path fully intact and independent.
- [ ] Add the vars edit intent ADDITIVELY: a new `ActionEdit` (and, if a distinct key/footer entry is needed, a `Mode`-scoped or `Options`-scoped binding) that does NOT alter the command-mode `e edit params` binding or footer text. The vars browser opts into edit; the command browser is byte-identical to today. Reference the additive-extension contract in the `Result` struct comment (run.go:84-97).
- [ ] Map vars → `cmdbrowser.Item`: `ID = vars.<path>` (drives the namespace tree), `Description = effective value`, `Type = layer-source badge` (`local`/`default`), `Inspect = func(width) string` rendering the inspect view. No second browser.
- [ ] No-arg `dwe vars` dispatch: interactive → run browser; on `ActionInspect` show overlay (already in browser); on `ActionEdit`/Enter → open the `set` huh form, write, reload, refresh items.
- [ ] Non-interactive/container guard: `!widgets.IsInteractiveFn(...) || cmdctx.NonInteractiveEnv()` → call `vars list` (mirror of `command.go`; call the EXPORTED `cmdctx.NonInteractiveEnv()` directly rather than copying `command.go`'s private wrapper).
- [ ] Write tests: vars→Item mapping (tree namespaces, badges, value-in-description); non-interactive dispatch falls back to `list`.
- [ ] HARD REGRESSION GATE: assert `dwe commands` browser output is byte-identical to today (run `cmdbrowser` golden tests + `dwe commands` browser tests). Specifically assert the command-mode footer STILL shows `e edit params` and the existing `ForceParamForm` tests still pass — the vars `ActionEdit` addition must not change anything in command mode.
- [ ] `make embedded-docs` once, then `go test ./internal/core/ui/cmdbrowser/... ./internal/cli/vars/... ./internal/cli/command/...` (focused) — must pass before Task 10.

### Task 10: Bridge allowlist (`vars` + `render config` exception)

**Files:**
- Modify: `internal/cli/bridgepolicy.go` (`bridgeAllowedTopLevel`, `bridgeCommandAllowed`)
- Modify: `internal/cli/render/config.go` (reject `--harvest` when invoked from a container)
- Modify: `internal/cli/bridgepolicy_test.go` (or nearest), `internal/cli/render/config_test.go`

- [ ] Add `"vars": true` to `bridgeAllowedTopLevel` with a comment: read subcommands always reachable; `set` is reachable but deny-by-default gated at runtime by `bridge.vars_writable` (enforced in Task 8's `set`, since `bridgeCommandAllowed` is prefix-wide and cannot see the var arg). TUI auto-disabled in container via non-interactive dispatch.
- [ ] Add a `render config` nested exception in `bridgeCommandAllowed` (mirroring the existing `bridge status` exception): `render config` reachable from container, all other `render` subcommands (env/ide/ai/git) stay host-only.
- [ ] Guard `render config --harvest` in `config.go`: when `bridgeInvokedFromContainer()` is true, reject `--harvest` with a typed error — it mutates host `.dwe/generated.yml` and would be a write path that bypasses `bridge.vars_writable`. Only read-only render is container-reachable.
- [ ] Write tests: `bridgeCommandAllowed("dwe vars get/list/inspect")` → true; `dwe vars set ...` path is reachable (true) BUT a container `set` of a non-`vars_writable` var is denied by Task 8's runtime gate (cross-checked here or in Task 8); `dwe render config` → true while `dwe render env/ide/ai/git` → false; container `render config --harvest` → rejected; a non-allowlisted sibling top-level command stays blocked (regression guard).
- [ ] `make embedded-docs` once, then `go test ./internal/cli/...` (focused) — must pass before Task 11.

### Task 11: Documentation + embedded sync

**Files:**
- Create: `docs/reference/config/vars.md` (DECIDED: a dedicated page — cleaner for a top-level command and matches per-page doc density; cross-link from `workspace.md`'s `vars:` section. Document subcommands, layer model, comment-preserving writes, usage scan + its caveat, container behavior)
- Create/Modify: `docs/i18n/ru/reference/config/vars.md` (Russian parity + updated `> Translated from: … @ <hash>` header)
- Modify: `docs/reference/config/workspace.md` cross-links if a new page is added
- Optional: llms-txt index mention

- [ ] Write the reference page covering `get`/`set`/`inspect`/`list` + no-arg TUI, the author/local/effective model, comment-preserving `local.yml` writes (now also for services/setup), the static usage scan and its "dynamic paths not tracked" caveat, JSON output, and container behavior.
- [ ] Document the new `bridge.vars_writable` config block (purpose, `vars.*` pattern syntax, deny-by-default, host-vs-container semantics) and that `dwe render config` is reachable from a container — in `vars.md` and/or the appropriate config reference; cross-link from `workspace.md`'s root-keys/`update:`-style section. Note the scope distinction from per-service `services.<name>.bridge:`.
- [ ] Add the Russian translation with parity; refresh the content-hash header.
- [ ] Run `make build` (syncs `internal/core/docs/embedded/` + regenerates `content_hashes_gen.go`).
- [ ] Verify docs-subsystem golden/hash tests (incl. `TestRussianTranslationsAreFresh`) pass.
- [ ] `make test` — docs-subsystem green.

### Task 12: Verify acceptance criteria

- [ ] `dwe vars get/list/inspect/set` work end-to-end against a fixture project (text + `--output json`).
- [ ] `set` preserves comments/formatting in `local.yml`; so do `services enable/disable` and the setup wizard (round-trip parity test green).
- [ ] `inspect` maps `${vars.x}` and structural `from:`/`default_from:`/`when:` usages, with exact + namespace-prefix matching, file:line + line text, and the dynamic-paths caveat.
- [ ] No-arg `dwe vars` opens the TUI; non-interactive/container falls back to `list`; `set` is reachable (and path-confined) from the container; `dwe commands` browser unaffected.
- [ ] JSON mode keeps stdout clean (typed errors on stderr); completion returns `vars.*` leaves.
- [ ] Run full suite: `make test` (+ `make test-race` if quick) and `make lint` — all green.

### Task 13: Finalize

- [ ] Update `docs/internals/packages.md` + `AGENTS.md` (CLAUDE.md is the symlink) with the new load-bearing contracts: comment-preserving node writer (with node-aware `validateMergeable` + legacy port exception) is the single `local.yml` write path (vars/services/setup); shared `config.LoadConfigLayers`/`ResolveLayeredPath` layer helper; `vars set` scalar-coercion grammar; field-aware `varsusage` scan + its caveat; `cmdbrowser` additive `ActionEdit` (existing `e`/ForceParamForm untouched); top-level `bridge.vars_writable` deny-by-default container-write allowlist (dot-boundary matcher) + `vars`/`render config` bridge reachability + the prefix-wide-allowlist runtime-gating note + the `render config --harvest` container block. Re-run `make build`.
- [ ] Move this plan to `docs/plans/completed/20260611-dwe-vars-command.md`.

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only.*

**Manual verification:**
- Smoke-test the `set` huh form on a real TTY (value omitted) and confirm reload reflects the change.
- Smoke-test container `set` through the bridge: from inside a devcontainer, a var listed in `bridge.vars_writable` is editable via `dwe vars set` (mutates host `local.yml`), a var NOT listed is rejected with `vars_not_container_writable`, and `dwe render config` regenerates config in-container while `dwe render env` stays host-only.
- Confirm `dwe vars` TUI rendering (tree, inspect overlay, edit form) against a wide and a narrow terminal (and the <60×15 fallback).

**External system updates:**
- Downstream DWE projects may adopt `dwe vars` in their own docs/workflows; no migration is required (the command is purely additive over the existing `vars:` data).
