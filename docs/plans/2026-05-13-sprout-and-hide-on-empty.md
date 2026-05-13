# Sprout template engine + `hide_on_empty` info sections

## Overview

Two related improvements to the templating/info subsystem:

1. **Migrate `internal/tpl/FuncMap` to `github.com/go-sprout/sprout` v1.0.3**, registering a hermetic subset of registries (no env / filesystem-read / network / crypto / random). **No backward compatibility kept**: the legacy zero-arg helpers `date`, `datetime`, `base`, `dir` and the `nowFn` test seam are removed entirely. YAML migrates to sprout idioms (`{{ now | date "2006-01-02" }}` etc.). Only `appURL` survives as a domain helper — sprout has no equivalent. Command-scope helpers `resolve`/`resolveMap`/`resolveFile` stay in `commandFuncMap`.
2. **Add `hide_on_empty: bool` on `InfoSection`** so a section with zero `when:`-surviving items is skipped entirely (no title, no frame). Footer rendering must also collapse if no section was rendered.

The two changes ship as **two separate commits** in the order above. The first is a behaviour-breaking rewrite of the template surface (with YAML/docs migration in the same commit); the second is an additive feature that uses the larger func vocabulary the first commit unlocks.

## Context (from discovery)

- Current template engine: `internal/tpl/funcs.go` exposes a tiny `FuncMap()` (`appURL`, `date`, `datetime`, `base`, `dir`). `text/template` is the only renderer. `engine.go` uses `template.New("").Funcs(FuncMap())`; `render_command.go` extends it with `resolve*` helpers.
- Info model: `internal/config/info.go` defines `InfoConfig{Settings, Sections, Footer}` and `InfoSection{ID, Title, Items}`. `LoadInfoConfig` uses **lenient** `yaml.Unmarshal` — unknown fields are silently ignored (per `CLAUDE.md`, this surface stays lenient).
- Info renderer: `internal/ui/info.go` `RenderInfo` iterates sections, evaluates `item.When` via `tpl.EvalCondition`, skips items where the condition is false, and unconditionally writes a section title if non-empty. Footer is rendered when `infoCfg.Footer == true`, independently of whether any section produced output.
- Sprout v1.0.3 API (confirmed on README/godoc): `sprout.New()` returns `*Handler`; registries added via `handler.AddRegistry(registry.NewRegistry())`; final map obtained via `handler.Build() template.FuncMap`. Registries live at `github.com/go-sprout/sprout/registry/<name>`. Official registry list includes (relevant to us): `std`, `strings`, `numeric`, `slices`, `maps`, `regexp`, `conversion`, `time`, `filesystem`, `semver`, `reflect`. Also exist but **deliberately excluded**: `env`, `network`, `crypto`, `random`, `encoding`, `checksum`, `uniqueid`, `backward`.
- Docs to update: `docs/reference/config/info.md` (template functions section + section field reference). `commands.md` already notes `${...}` templating; sweep to confirm FuncMap parity language.
- Project facts (verified locally): `go.mod` declares **Go 1.26** → `sync.OnceValue` (1.21+) is available for FuncMap caching. Tests use **standard library only** (no `testify`) — match existing style (`t.Fatal` / `t.Errorf` direct, no `assert.*`). Existing `engine_test.go` is per-function (not table-driven) — fine for trivial cases, but new sprout-coverage tests have enough cases to justify table-driven structure.
- Sprout `filesystem` registry naming (verified at docs.atom.codes/sprout/registries/filesystem): exports **`pathBase`/`pathDir`/`pathExt`/`pathClean`/`pathIsAbs`** (forward-slash semantics, platform-independent) and **`osBase`/`osDir`/`osExt`/`osClean`/`osIsAbs`** (OS-specific separator). There is **no generic `base`/`dir`**. Our current `base`/`dir` wrap `filepath.Base`/`Dir` (OS-aware), so the semantic equivalent is `osBase`/`osDir`. **Decision: migrate to `pathBase`/`pathDir`** — devbox templates predominantly describe container-internal paths (Linux, forward-slash), and `path*` is predictable across host OS (the macOS host vs Linux container split is otherwise a footgun with `os*`).

## Dependency Vetting — `github.com/go-sprout/sprout` v1.0.3

Recorded per `golang-dependency-management` skill checklist before adding the dependency:

- **Does the standard library cover this?** No. `text/template` provides ~5 builtin functions. We need ~20 additional helpers (`default`, `ternary`, `hasSuffix`, `regexMatch`, `now`, `add`, `max`, `list`, etc.) for ergonomic `info.yml` and command templates. Hand-rolling and maintaining these is more work than a vetted dep.
- **License:** verify at `go get` time that it is permissive (MIT / Apache-2.0 / BSD) — record the actual SPDX identifier in the commit message. Abort if copyleft.
- **Maintenance:** sprout is the actively-maintained successor to `Masterminds/sprig`. Tagged v1.0 → API stability commitment.
- **Why sprout and not sprig?** Sprout's registry model is the deciding factor: it lets us opt **out** of `env`, `crypto`, `random`, `network`, etc., enforcing hermetic templates by construction. Sprig bundles everything globally.
- **Attack surface:** zero registries with side effects are wired up (no env, no FS reads, no network, no crypto/random). A negative test (Task 2) pins this boundary against future drift.
- **Post-add verification:**
  - `go mod tidy` is implicit (CLAUDE.md: `make build` runs it).
  - `go mod verify` once after the first build — confirms `go.sum` checksums match the downloaded module.
  - `govulncheck ./...` added to the acceptance task — catches any known CVE in sprout's transitive tree at plan-close time.

## Development Approach

- **Testing approach**: Regular (code first, then tests) — both tasks are small and the failure modes are easier to reason about with the implementation in front of you.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task.
- **CRITICAL: all tests must pass before starting next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run `make test` after each task; run `make lint` at each commit boundary.
- **No backward compatibility** for legacy `date`/`datetime`/`base`/`dir` template helpers — Task 1 migrates every YAML and doc call site in the same commit; Task 2 deletes/rewrites the Go tests that asserted the legacy surface.

## Testing Strategy

- **Unit tests**: required for every task. Targets:
  - `internal/tpl/funcs_test.go` (and/or `engine_test.go`) — sprout function spot-checks + domain-override coverage.
  - `internal/tpl/render_command_test.go` — sanity that commandFuncMap still resolves sprout funcs (it inherits).
  - `internal/config/info_test.go` — `hide_on_empty` YAML decode round-trip.
  - `internal/ui/info_test.go` — section visibility / footer interaction matrix.
- **E2E tests**: none in this repo. Skip.
- **Lint**: `make lint` must pass; `golangci-lint` enforces `errcheck`, `govet`, `staticcheck`, `revive`, `gocritic`, `modernize`.

**Test conventions (from `golang-testing` skill, adapted to this repo):**

- **Standard library only** — no `testify`. Match `internal/tpl/engine_test.go` style: direct `t.Fatal` / `t.Errorf`.
- **Table-driven with named `name` field** for the sprout-coverage matrix (Task 2) and the renderer matrix (Task 6) — each row passed to `t.Run(tt.name, ...)`.
- **`t.Parallel()`** on every subtest in this plan — no package-level mutable state remains in `internal/tpl/` after the rewrite (the `nowFn` seam was deleted along with the legacy `date`/`datetime` helpers).
- **No implementation-detail coupling**: test the FuncMap's *observable surface* via `template.Parse(...).Execute(...)`, not by reaching into the returned `template.FuncMap` to call functions directly. This keeps tests valid through future internal restructuring.
- **Race detection in acceptance**: `go test -race ./...` is a required step in Task 8 — catches any accidental shared state introduced by the sprout caching change.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with `➕` prefix.
- Document issues/blockers with `⚠️` prefix.
- Update plan if implementation deviates from original scope.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code changes, tests, doc edits inside this repo.
- **Post-Completion** (no checkboxes): consumer-side YAML migrations, downstream-project notifications.

## Implementation Steps

### Task 1: Add sprout dependency and cached Handler wiring
- [x] run `go get github.com/go-sprout/sprout@v1.0.3` to pin the version in `go.mod`/`go.sum`
- [x] capture the module's license → record SPDX identifier in commit message; abort if not MIT/Apache-2.0/BSD. Sources, in order of preference: `cat $(go env GOMODCACHE)/github.com/go-sprout/sprout@v1.0.3/LICENSE`, the upstream tag on GitHub, or the License field on `pkg.go.dev/github.com/go-sprout/sprout@v1.0.3`. (Note: `go.sum` only stores cryptographic checksums, no license metadata.) **SPDX: MIT**
- [x] verify the actual registry import paths once locally (`go doc github.com/go-sprout/sprout/registry/std` etc.) — record any deviation from assumed names in a `⚠️` note here before continuing
- [x] **rewrite `internal/tpl/funcs.go` to the new minimal shape:**
  - private `buildFuncMap()` constructs `*sprout.Handler` via `sprout.New()`, calls `AddRegistries(...)` for `std`, `strings`, `numeric`, `slices`, `maps`, `regexp`, `conversion`, `time`, `filesystem`, `semver`, then `handler.Build()` and overlays exactly **one** domain entry: `appURL`.
  - **No date/datetime/base/dir overlay.** All four legacy helpers are deleted with no replacement under those names. YAML migrates to sprout idioms: `{{ date }}` → `{{ now | date "2006-01-02" }}`, `{{ base ... }}` → `{{ pathBase ... }}`, `{{ dir ... }}` → `{{ pathDir ... }}` (sprout `filesystem` exports `pathBase`/`pathDir` and `osBase`/`osDir`, **not** generic `base`/`dir`). The Task 1 commit migrates every YAML/doc usage in the same commit (see migration sub-task below).
  - **Delete `nowFn`.** It existed only to inject time in tests of the now-removed `dateFunc`. Sprout's `now` reads the real clock; tests that care about exact values use `testing/synctest` (Go 1.24+, available in 1.26) or assert against the format pattern via regexp.
  - **Cache** via `var funcMapOnce = sync.OnceValue(buildFuncMap)` at package level. Building Handler + 10 registries on every `Render` is unnecessary work; once at first call is enough.
  - `FuncMap()` returns `maps.Clone(funcMapOnce())` — a shallow copy on every call. **Mandatory**: `internal/tpl/render_command.go:154` (`commandFuncMap()`) mutates the returned map by injecting `resolve` / `resolveMap` / `resolveFile`. Without the clone, first command render permanently leaks `resolve*` into base info templates **and** races under `-race`. A shallow clone of a ~200-entry `map[string]any` is one small alloc — negligible vs template parse/execute.
  - Doc comment on `FuncMap()`: "returns a per-call shallow copy; safe to extend (e.g. `commandFuncMap`) without affecting other callers. Registers sprout `std`, `strings`, `numeric`, `slices`, `maps`, `regexp`, `conversion`, `time`, `filesystem`, `semver`. Hermetic: no env, no FS reads, no network, no crypto/random."
- [x] **YAML/doc migration** (part of this commit — breaking change). All four legacy helpers (`date`, `datetime`, `base`, `dir`) replaced with sprout idioms:
  - `internal/usercommands/testdata/files_dump_create.yml:24` → `{{ date }}` → `{{ now | date "2006-01-02" }}`
  - `docs/reference/config/commands.md`:
    - lines 255, 276, 456, 469, 580, 984 → `{{ date }}` / `{{ datetime }}` → `{{ now | date "2006-01-02" }}` / `{{ now | date "2006-01-02_15-04-05" }}`
    - line 461 → `{{ .Params.script_path | base }}` → `{{ .Params.script_path | pathBase }}`
    - line 931 (helper table) → rewrite the row to list `{{ now | date "..." }}`, `{{ pathBase }}` / `{{ pathDir }}`, `{{ appURL ... }}`
  - `docs/reference/config/info.md:194` → rewrite the date/datetime sentence to point at sprout's `now | date` idiom; mention `pathBase`/`pathDir` if relevant
  - **Legacy-only grep recipe** (matches legacy shapes only; new sprout idioms `{{ now | date ... }}` / `{{ pathBase ... }}` are intentionally allowed):
    ```
    rg '\{\{\s*(date|datetime)\s*\}\}|\{\{\s*(base|dir)\b|\|\s*(base|dir)\s*\}\}' --glob '*.yml' --glob '*.yaml' --glob '*.md'
    ```
    Catches: zero-arg `{{ date }}`/`{{ datetime }}`; first-token `{{ base ... }}`/`{{ dir ... }}` (word-boundary excludes `pathBase`/`basement`/etc.); piped `{{ ... | base }}`/`{{ ... | dir }}`. Must return zero hits outside `docs/plans/` before the commit. Note: the test files `internal/tpl/engine_test.go` and `internal/tpl/render_command_test.go` contain literal template strings that match this regex — those are handled in Task 2, not here.
- [x] `make build` — must succeed (also runs `go mod tidy` per Makefile)
- [x] `go mod verify` — confirms `go.sum` checksums match the downloaded module
- [x] tests in next task

### Task 2: Rewrite tpl tests for the new surface
**Context:** existing tests in `internal/tpl/` reference deleted symbols (`dateFunc`, `datetimeFunc`) and legacy template invocations (`{{ date }}`, `{{ datetime }}`, `{{ base "..." }}`, `{{ dir "..." }}`). After Task 1 the package won't even compile its own tests. Do the surgery in lock-step.

- [x] **Delete from `internal/tpl/engine_test.go`** (all stale — replaced by new tests below):
  - `TestDateFunc` / `TestDatetimeFunc` (lines ≈158–181) — referenced removed `dateFunc`/`datetimeFunc`
  - `TestRender_dateFunc` / `TestRender_datetimeFunc` (≈183–217) — tested removed `{{ date }}` / `{{ datetime }}`
  - `TestBase*` / `TestRender_baseFunc` (≈220–250) — tested removed local `base` helper
  - `TestDir*` / `TestRender_dirFunc` (≈253–280) — tested removed local `dir` helper
- [x] **Delete from `internal/tpl/render_command_test.go`**:
  - `TestRenderCommand_dateFunc` (≈line 292), `TestRenderCommand_datetimeFunc` (≈311), `TestRenderCommand_baseFunc` (≈330), `TestRenderCommand_dirFunc` (≈353), `TestRenderCommand_baseDirChained` (≈395)
  - the `backup_{{ date }}.sql` assertion at ≈line 385 — rewrite to `backup_{{ now | date "2006-01-02" }}.sql`
  - **Keep** the `dir-exists` / `dir-missing` / `dir-empty` / `file-exists` tests starting ≈line 606 — those are `EvalCommandCondition` builtin predicates, a different system from template helpers.
- [x] **Add new `internal/tpl/funcs_test.go`** with the sprout-coverage matrix. Table-driven, `{name, template, want}`, executed via `tpl.Render(template, nil)` (observable surface, not direct map lookup). All subtests `t.Parallel()`:
  - `hasSuffix` from `strings`
  - `default` from `std`
  - `ternary` from `std`
  - `regexMatch` from `regexp`
  - `add` / `max` from `numeric`
  - `list` + `first` from `slices`
  - `pathBase "/a/b/c.txt"` → `"c.txt"` (replacement for the deleted `base` test)
  - `pathDir "/a/b/c.txt"` → `"/a/b"` (replacement for the deleted `dir` test)
  - chained: `{{ pathDir (pathBase "/a/b/c.txt") }}` → `"."` (replacement for `TestRenderCommand_baseDirChained`)
- [x] **time-rendering smoke test** in `funcs_test.go`: `tpl.Render("{{ now | date \"2006-01-02\" }}", nil)` returns a string matching `^\d{4}-\d{2}-\d{2}$`. We don't pin exact values — sprout's `now`/`date` are sprout's responsibility. `t.Parallel()` allowed (no shared state).
- [x] **`appURL` regression** in `funcs_test.go`, table-driven (host/port/scheme/path rows, default-port elision per `internal/tpl/funcs.go:appURL`), all subtests `t.Parallel()`.
- [x] **Hermetic boundary** negative tests — each must return a non-nil error from `tpl.Render`:
  - `{{ env "PATH" }}` — `env` registry not registered
  - `{{ getHostByName "x" }}` — `network` registry not registered
  - `{{ randAlpha 8 }}` — `random` registry not registered
- [x] **Legacy-removal negative tests** (pin the breaking change so future "convenience re-adds" fail loudly):
  - `tpl.Render("{{ date }}", nil)` must error — sprout's `date` is variadic, errors on zero-arg invocation
  - `tpl.Render("{{ datetime }}", nil)` must error — helper deleted entirely; no sprout equivalent under that name
  - `tpl.Render(\`{{ base "/x" }}\`, nil)` must error — no generic `base`; must use `pathBase`/`osBase`
  - `tpl.Render(\`{{ dir "/x" }}\`, nil)` must error — same as above
- [x] **`commandFuncMap` inheritance**: in `render_command_test.go` add one new case using a sprout func (`default`) in a command template — proves the command surface inherits sprout via the cloned base map.
- [x] **OnceValue caching test**: call `FuncMap()` twice, assert function-value equality (via `reflect.ValueOf(...).Pointer()`) on the `appURL` entry — confirms the cache hits, not silently rebuilt. Map identity will differ (per-call shallow clone), function values come from the cached source.
- [x] **Isolation test** (regression guard for the cache-leak bug): call `commandFuncMap()` (which adds `resolve` entries), then call `FuncMap()`, assert `_, ok := fm["resolve"]; !ok`. Proves the shallow-clone defence holds.
- [x] run `go test ./internal/tpl/...` — must pass before next task

### Task 3: Full-suite migration sanity
- [x] run `go test ./...` — full suite green; in particular `internal/usercommands/...` tests that load `testdata/files_dump_create.yml` must pass with the new `{{ now | date "..." }}` template
- [x] re-run the broad grep from Task 1 — must return zero hits outside `docs/plans/`:
  ```
  rg '\{\{\s*(date|datetime)\s*\}\}|\{\{\s*(base|dir)\b|\|\s*(base|dir)\s*\}\}' --glob '*.yml' --glob '*.yaml' --glob '*.md'
  ```
  This catches **only legacy shapes**, by design:
  - `{{ date }}` / `{{ datetime }}` — zero-arg legacy
  - `{{ base ... }}` / `{{ dir ... }}` — first-token legacy (with or without args; word-boundary `\b` keeps `pathBase` / `pathDir` / `basement` etc. from matching)
  - `{{ ... | base }}` / `{{ ... | dir }}` — piped legacy
  - The new sprout idiom `{{ now | date "..." }}` is **allowed** (no zero-arg form, `date` is not first token); `{{ pathBase ... }}` and `{{ x | pathBase }}` are allowed (word-boundary).
  **Doc convention to avoid self-matches:** the migration tables in Task 4 reference legacy helpers as backtick-wrapped identifiers (e.g. `` `base` ``, `` `date` ``) — never as full template literals `{{ base }}`. Combined with the narrowed regex above, this keeps the migration documentation out of the grep's match set without needing an exclude rule.
- [x] confirm no Go code references the removed helpers: `grep -rn 'FuncMap()\[\("date"\|"datetime"\|"base"\|"dir"\)\]' internal/` returns zero hits

### Task 4: Update template-function documentation
- [x] `docs/reference/config/info.md`: rewrite the "Template functions" section
  - list **info-scope** domain funcs: `appURL` only — this is the single non-sprout entry in the base `FuncMap()` evaluated by `tpl.Render` against `*config.DevboxConfig`
  - list sprout registries enabled with a one-line description per family and a link to upstream sprout docs; mention common idioms like `{{ now | date "2006-01-02" }}` so users know how to do what the old `{{ date }}` did
  - explicitly: **no `env`, no filesystem reads, no network, no crypto/random** — templates are hermetic by design
  - **Do NOT list `resolve`, `resolveMap`, `resolveFile` here.** They are command-scope only (injected by `commandFuncMap()` in `internal/tpl/render_command.go`), accept command data (raw maps), and would error against info's `*config.DevboxConfig` argument. Documenting them in info.md would mislead users into writing `info.yml` entries that fail at runtime.
- [x] `docs/reference/config/commands.md`: cover the **command-scope superset**
  - cross-link to info.md for the shared base (`appURL` + sprout registries)
  - **here** document `resolve`, `resolveMap`, `resolveFile` with short examples showing the raw-map dot-path semantics they assume
  - migration note (one paragraph): legacy template helpers removed in this release. Document the mapping **as a table without inline `{{ ... }}` literals**, so the grep guard in Task 3 does not flag the docs themselves. Suggested wording:
    > | Legacy helper (removed) | Replacement |
    > |---|---|
    > | `date` | `now \| date "2006-01-02"` |
    > | `datetime` | `now \| date "2006-01-02_15-04-05"` |
    > | `base` | `pathBase` (sprout `filesystem` registry — forward-slash semantics) |
    > | `dir` | `pathDir` (sprout `filesystem` registry — forward-slash semantics) |
    >
    > `osBase`/`osDir` are also available if you need OS-specific separator semantics; we recommend the `path*` variants for cross-platform predictability.
    Pure backtick-wrapped identifiers (no `{{` / `}}`) keep the doc out of the grep's match set.
- [x] no test changes; doc-only task — but verify links resolve relative to repo root
- [x] **Commit boundary** — stage Tasks 1–4 as commit `feat(tpl)!: migrate template FuncMap to go-sprout` (note the `!` — breaking change); ensure `make test` and `make lint` pass

### Task 5: Add `HideOnEmpty` to the info section model
- [x] in `internal/config/info.go`, add field on `InfoSection`:
  ```go
  HideOnEmpty bool `yaml:"hide_on_empty"`
  ```
  Comment: "When true, the section (including its title) is omitted if no item survives `when:` filtering. Default false preserves legacy rendering."
- [x] no loader changes needed — `LoadInfoConfig` is lenient `yaml.Unmarshal`; unknown-but-now-known field decodes automatically
- [x] add round-trip decode test in `internal/config/info_test.go`: YAML with `hide_on_empty: true` → struct field is `true`; absence defaults to `false`
- [x] run `go test ./internal/config/...` — must pass before next task

### Task 6: Apply `HideOnEmpty` in the renderer
- [x] refactor `RenderInfo` in `internal/ui/info.go`:
  - for each section, **first** evaluate every item's `when:` and collect those that survive into a `[]config.InfoItem` slice
  - if `len(surviving) == 0 && section.HideOnEmpty` → skip the section entirely (no title, no items)
  - otherwise render title (if non-empty) then iterate the surviving slice for output (do not re-evaluate `when:`)
  - track `renderedAnySection bool`
- [x] adjust footer: render `infoCfg.Footer` only when `renderedAnySection == true`. This avoids a lone footer line when every section was hidden.
- [x] semantics decisions (record in code comment near the new logic):
  - decorative items (`warning`, `info`, `subheader`) without `when:` always survive → they count as content → such a section is never "empty"
  - errors from `when:` evaluation propagate as before (no change to error behaviour)
- [x] write **table-driven** tests in `internal/ui/info_test.go` (rows: `{name, infoCfg, wantContains, wantNotContains}`), each subtest `t.Parallel()`:
  - section with all items filtered out + `hide_on_empty: true` → not in output (no title)
  - same setup + `hide_on_empty: false` (or omitted) → title still rendered (legacy behaviour preserved)
  - mixed: one hidden, one visible → output contains only the visible section's title/items
  - all sections hidden + `footer: true` → no footer line in output
  - at least one section rendered + `footer: true` → footer rendered (regression guard)
  - decorative warning without `when:` keeps the section visible even if everything else is filtered (asserts the "warning counts as content" rule)
  - section with items whose `when:` errors → error propagates (regression guard for existing behaviour)
- [x] run `go test ./internal/ui/...` — must pass before next task

### Task 7: Document `hide_on_empty`
- [ ] `docs/reference/config/info.md`:
  - in the "Section fields" table, add row: `hide_on_empty | bool | false | Skip the section entirely (no title, no frame) when no item survives when-filtering.`
  - add a short "Common pitfalls" note: items without `when:` always count as content; if you want a section to truly disappear when its data is empty, every item must carry a `when:` predicate.
  - add a note on footer interaction: footer is suppressed when all sections are hidden.
- [ ] no test changes
- [ ] **Commit boundary** — stage Tasks 5–7 as commit `feat(info): hide_on_empty for sections`; ensure `make test` and `make lint` pass

### Task 8: Verify acceptance criteria
- [ ] verify Task 1 outcome: `go doc devbox-cli/internal/tpl` shows the new FuncMap doc, sprout is in `go.mod`
- [ ] verify Task 5–6 outcome: a synthetic `info.yml` fixture with `hide_on_empty: true` and all items gated on a false `when:` produces empty output when rendered
- [ ] run full `make test` — green
- [ ] run `go test -race ./...` — green (catches accidental shared state from the OnceValue caching)
- [ ] run `make lint` — zero issues
- [ ] run `govulncheck ./...` — no advisories on sprout's tree (install via `go install golang.org/x/vuln/cmd/govulncheck@latest` if not present)
- [ ] run `go mod verify` — `go.sum` checksums match
- [ ] confirm test coverage on new code paths (sprout layering, OnceValue cache, hide_on_empty branch) at the package's existing standard

## Technical Details

**Sprout `FuncMap()` shape (Task 1, after rewrite):**

```go
// funcMapOnce caches the base sprout-built FuncMap. Building 10 sprout
// registries on every template render is wasteful; once at first call is
// enough. No mutable state — the cached value is read-only once frozen.
var funcMapOnce = sync.OnceValue(buildFuncMap)

// FuncMap returns a per-call shallow copy of the cached base map.
// The clone is mandatory: commandFuncMap (render_command.go) extends the
// returned map with resolve/resolveMap/resolveFile. Without the clone, the
// first command render would permanently leak those entries into base info
// templates and race under -race. A shallow clone of a ~200-entry map is one
// small alloc — negligible vs template parse/execute.
//
// Hermetic surface: only sprout's std, strings, numeric, slices, maps,
// regexp, conversion, time, filesystem, semver registries plus one domain
// helper (appURL). No env, no FS reads, no network, no crypto/random.
func FuncMap() template.FuncMap { return maps.Clone(funcMapOnce()) }

func buildFuncMap() template.FuncMap {
    h := sprout.New()
    if err := h.AddRegistries(
        std.NewRegistry(),
        strings_.NewRegistry(),
        numeric.NewRegistry(),
        slices_.NewRegistry(),
        maps_.NewRegistry(),
        regexp_.NewRegistry(),
        conversion.NewRegistry(),
        time_.NewRegistry(),
        filesystem.NewRegistry(),
        semver.NewRegistry(),
    ); err != nil {
        // Registry registration failing at startup is a programmer/dep-version
        // bug, not a runtime condition. Panic per the "panic for bugs" rule.
        panic(fmt.Errorf("tpl: sprout registry registration: %w", err))
    }
    fm := h.Build()
    fm["appURL"] = appURL
    return fm
}
```

No local `dateFunc` / `datetimeFunc` / `base` / `dir` **implementation** remains in `internal/tpl/funcs.go`, and `nowFn` is deleted. The package's test files still contain template-literal *strings* like `{{ date }}` and `{{ now | date "2006-01-02" }}` — those are the legacy-removal negative tests and the new sprout smoke tests respectively, both intentional. Exact registry import aliases will be set during Task 1 once the upstream package names are confirmed. `Build()` returns a fresh map per call — what we want; the `maps.Clone` at `FuncMap()` is independent and protects the cached map from caller mutation.

**`InfoSection` schema (Task 5):**

```yaml
sections:
  - id: tools
    title: Tools
    hide_on_empty: true     # NEW — optional, default false
    items:
      - type: definition
        when: '{{ .Tools.docker.Enabled }}'
        name: docker
        value: '{{ ... }}'
```

**Renderer flow (Task 6):**

```
for each section:
    survivors := []InfoItem{}
    for each item:
        ok, err := tpl.EvalCondition(item.When, cfg)
        if err: return err
        if ok: survivors = append(survivors, item)
    if len(survivors) == 0 && section.HideOnEmpty: continue
    if section.Title != "": writeTitle()
    for each item in survivors: writeRendered()
    renderedAnySection = true

if infoCfg.Footer && renderedAnySection: writeFooter()
```

## Post-Completion

*Items requiring manual or downstream action — no checkboxes.*

**Consumer YAML migration (BREAKING — release notes must call this out):**

- `{{ date }}` → `{{ now | date "2006-01-02" }}`
- `{{ datetime }}` → `{{ now | date "2006-01-02_15-04-05" }}`
- `{{ base ... }}` → `{{ pathBase ... }}` (sprout `filesystem` does not export a generic `base`; it has `pathBase` for forward-slash semantics and `osBase` for OS separator. We chose `pathBase` for cross-platform predictability of container paths.)
- `{{ dir ... }}` → `{{ pathDir ... }}`
- Downstream devbox projects must update their `info.yml` and `commands/*.yml` files. Provide a multi-substitution `sed` recipe in release notes (run from project root):
  ```
  sed -i \
    -e 's/{{ date }}/{{ now | date "2006-01-02" }}/g' \
    -e 's/{{ datetime }}/{{ now | date "2006-01-02_15-04-05" }}/g' \
    -e 's/| *base }}/| pathBase }}/g' \
    -e 's/| *dir }}/| pathDir }}/g' \
    -e 's/{{ *base /{{ pathBase /g' \
    -e 's/{{ *dir /{{ pathDir /g' \
    devbox/info.yml devbox/commands/*.yml
  ```

**Downstream:**

- Once the sprout migration ships, the broader sprout function set becomes available across all devbox projects. Announce in release notes the new vocabulary: `default`, `ternary`, `hasSuffix`, `regexMatch`, `now | date`, `add`/`max`, `list`/`first`, etc.
- The `hide_on_empty` change is additive; document it in release notes alongside an example.

**Future scope (explicitly out of this plan):**

- Per-subgroup empty-suppression (e.g. hide a `subheader` whose following items all got filtered) — different mechanism, not covered here.
- A template lint pass / `devbox doctor` check that flags legacy `{{ date }}` / `{{ datetime }}` / `{{ base }}` / `{{ dir }}` call sites and suggests the sprout idiom (`{{ now | date }}` / `{{ pathBase }}` / `{{ pathDir }}`) — would smooth downstream migration. Out of scope for these two commits but a natural follow-up.
