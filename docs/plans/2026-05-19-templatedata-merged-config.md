# TemplateData: expose merged DevboxConfig to render templates

## Overview

Render templates (git hooks, IDE configs, AI agent docs) today receive a fixed 4-field `TemplateData` (`Project`, `Service`, `ServiceCfg`, `Runtime`). Anything outside that shape — including arbitrary project-level conventions like a JIRA prefix or per-service hook commands — must be smuggled in via `cli.env` or hardcoded.

This plan adds one field, `Cfg *config.DevboxConfig`, to all three `TemplateData` structs and plumbs it through the render entry points. With `Cfg` in scope, templates can reach:

- typed fields directly (`{{ .Cfg.Project.Name }}`, `{{ .Cfg.Tools.adminer.Host }}`, etc.); and
- the post-merge config map via `{{ .Cfg.Raw.<key>... }}` (dot syntax) or `{{ index .Cfg.Raw "<key>" }}` (index syntax) for user-defined top-level blocks (e.g. a `git:` block in `defaults.yml` carrying conventions specific to a project).

### Dot syntax has identifier-only segments

Go `text/template` resolves `.Map.key` as a method/field/key lookup only when `key` matches `[A-Za-z_][A-Za-z0-9_]*` (the template parser's identifier rule). Keys with **hyphens, dots, spaces, leading digits, or any non-identifier character** must be reached via `index`:

```gotemplate
{{ .Cfg.Raw.git.project_prefix }}                  {{- /* OK — identifier-safe */ -}}
{{ index .Cfg.Raw "my-tool" "api-key" }}           {{- /* required when keys are not identifiers */ -}}
{{ $tool := index .Cfg.Raw "my-tool" }}{{ index $tool "api-key" }}
```

This is a Go template engine constraint, not a devbox one — the same rule applies to `RenderContext.Raw` in command sites today. The plan exposes the same access pattern; per-renderer docs (Task 6) must show both forms.

Scope explicitly excluded (per planning conversation):

- No typed sub-config additions (no `GitConfig`, no schema additions). `defaults.yml` is intentionally fully customisable; user-defined blocks reach templates through `.Cfg.Raw` (with the access-syntax caveat below).
- No changes to `LoadConfig`, no new identifier-safe key validation, no schema bump. Authors who use non-identifier keys (hyphens, dots, leading digits, etc.) must reach them via `index`, as detailed below.

### Alignment with existing template sites

Most other template sites already see merged config: info/pipelines/`message` builtin get typed `DevboxConfig` (`docs/reference/templates.md:94`), command sites get `RenderContext.Raw` (`docs/reference/templates.md:88`), `docker.yml` resolves `${...}` against `Raw` (`docs/reference/templates.md:28`). Render packs (git, ide, ai) are the outlier — this plan closes the gap.

### Risk: IDE/AI outputs live in the project tree

This is the central trade-off, called out explicitly because the three renderers do **not** target equivalent output locations:

- **git hooks** write to `<svc.Dir>/src/.git/hooks/` (gitignored). Per-developer variation is harmless.
- **ide and ai packs** write into `<projectRoot>/<svc.Dir>/<entry.To>` — typically tracked files (e.g. `.vscode/settings.json`, `AGENTS.md`). Any value reached from `.Cfg.Raw` that originates in `devbox/local.yml` will, after `deepMerge`, surface in the rendered output and create developer-specific diffs.

Existing surface already exposes this property in a narrow way: `.Runtime.Ports.app`, `.ServiceCfg.*`, and other typed fields already flow through `local.yml` `deepMerge` today, so a developer who overrides a port in `local.yml` already gets a different rendered IDE settings file. `.Cfg.Raw` widens that surface to arbitrary top-level blocks.

**Decision** (per planning conversation): expose `.Cfg` in all three renderers without restriction. Template authors are responsible for not consuming developer-local-only or secret keys in tracked IDE/AI outputs. This is documented prominently in the render docs (Task 5) and asserted by a backward-compat test in each renderer (Tasks 1–3) that proves the existing 4-field outputs remain byte-identical when templates do not reference `.Cfg`.

### `.Raw` contract — be precise

`DevboxConfig.Raw` is **not** "the 3-layer merged YAML". It is the merged map after devbox's normalization and injection steps, performed in `internal/config/devbox.go:LoadConfig`:

1. `deepMerge(devbox.yml, defaults.yml, local.yml)` into a single `map[string]any` (`devbox.go:632-655`).
2. Inject `__configPath` (`devbox.go:688`).
3. **Normalize `binaries`** — overwrite the merged `binaries:` block with values from top-level `devbox.yml` only, applying defaults (`devbox.go:693-697`). Any `binaries:` block in `defaults.yml` or `local.yml` is silently discarded. This is also documented at `AGENTS.md:13` ("binaries... read from top-level `devbox.yml` only — not layered").
4. **Inject `services.*`** — `devbox/services.yml` is loaded separately (not 3-layer merged), then `injectServicesIntoRaw` writes service definitions into `Raw["services"]` so dot-paths like `services.main.container` resolve (`devbox.go:699-720`; cross-referenced in `docs/reference/config/devbox.md:74-91`).
5. `render.<kind>.*` fields on services are **not** injected into `Raw["services"]` (`AGENTS.md` notes this in the service render config section).

Documentation for the new `.Cfg.Raw` access (Task 5) must teach this mental model, not "literal 3-layer YAML".

## Context (from discovery)

Files involved (paths relative to `cli/`):

- `internal/templates/git/git.go:302-308` — `TemplateData` struct. Populated at `git.go:364-369` inside `RenderHooks(ctx Context)`. `ctx.Cfg *config.DevboxConfig` is already in scope.
- `internal/templates/ide/ide.go:259-265` — `TemplateData` struct (identical shape). Caller constructs it.
- `internal/templates/ai/ai.go:189-195` — `TemplateData` struct (identical shape). Caller constructs it.
- `internal/command/render_git.go:79` — calls `RenderHooks(gitpkg.Context{Cfg: cfg, ...})`. Already passes cfg.
- `internal/command/ide.go` — caller constructs `ide.TemplateData{Project: cfg.Project, Service: name, ServiceCfg: svc, Runtime: cfg.Runtime}` and passes it to `ide.RenderTemplateFile`. Output destination: `<projectRoot>/<svc.Dir>/<entry.To>` (line 190; tracked path).
- `internal/command/render_ai.go:70-75` — caller constructs `aipkg.TemplateData{...}` symmetrically. Output destination: `<projectRoot>/<svc.Dir>/<entry.To>` (line 33,86; tracked path).
- `internal/config/devbox.go:77-96` — `DevboxConfig` definition; `Raw map[string]any` populated and post-processed by `LoadConfig` (see "`.Raw` contract" above).

Existing tests to extend:

- `internal/templates/git/git_test.go` — has `TestRenderHooks` with `mkPack()` helper; table-driven subtests; dynamic file creation (no `testdata/`).
- `internal/command/render_git_test.go`, `internal/command/render_ai_test.go`, `internal/command/ide_test.go` — command-level integration tests already exist.
- No direct unit tests in `internal/templates/ide` or `internal/templates/ai` — coverage there is via the command-level tests above.

Docs to update (full list — corrected from the previous draft):

- `docs/reference/templates.md`:
  - "Where templates are evaluated" table (lines 22–31): current rows omit git render packs entirely and label the shared section as "IDE / AI" — must add a git row and broaden the description.
  - "Render context per site" → "IDE / AI render packs (strict)" heading (line 96): rename to cover git too; extend the variable table (lines 98–103) with `.Cfg`.
- `docs/reference/render/git.md`, `docs/reference/render/ide.md`, `docs/reference/render/ai.md` — per-renderer variable reference + `.Cfg.Raw` example + risk advisory for ide/ai.
- `docs/reference/render/index.md` — if it lists template context fields.

Related patterns:

- Template authors access `.Cfg.Raw.<key>...` via Go's `text/template` map-dot syntax. Identifier-safe key validation is enforced today only for `Tools`, `Runtime.Ports`, `Runtime.Hosts` (`internal/config/devbox.go:540-554`); `.Cfg.Raw` reaches into arbitrary subtrees with no such enforcement. `missingkey=error` (the existing strict-render mode) will surface typos at render time.

## Development Approach

- **Testing approach**: Regular (code first, then tests per task).
- Complete each task fully before moving to the next.
- Make small, focused changes — one renderer at a time, git first (has the strongest existing test coverage to anchor the change).
- **CRITICAL: every task MUST include new/updated tests for code changes in that task.**
- **CRITICAL: all tests must pass before starting the next task** (`go test ./...`).
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run `make lint` after the code tasks, before the docs task.
- Maintain backward compatibility — adding a struct field is additive; existing templates that do not reference `.Cfg` must produce byte-identical output. Tasks 1–3 each include an explicit backward-compat assertion to guarantee this.

## Testing Strategy

- **Unit tests** required per task:
  - **Positive `.Cfg.Raw.*` access**: a template referencing `{{ .Cfg.Raw.<custom>.<field> }}` renders the expected value.
  - **`missingkey=error` for typos**: a template referencing an absent `.Cfg.Raw.<key>` fails with a clear error.
  - **Backward-compat byte-identical output**: render the pack with a template that does **not** reference `.Cfg`, assert output matches the pre-change golden bytes. This guards the IDE/AI tracked-artifact risk: as long as templates do not reference `.Cfg`, rendered output is unchanged.
- **E2E tests**: N/A — this is a CLI tool with no browser UI.
- **Manual verification** (Post-Completion): run all three `devbox render *` against the pilot project after it adopts a `git:` block in `defaults.yml`.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.

## What Goes Where

- **Implementation Steps** (`[ ]`): everything achievable inside `cli/` — code, tests, docs.
- **Post-Completion** (no checkboxes): pilot project's `defaults.yml`/hook templates and any consuming projects that want to adopt `.Cfg`.

## Implementation Steps

### Task 1: Extend git renderer TemplateData with Cfg field

- [x] add `Cfg *config.DevboxConfig` to `TemplateData` struct in `internal/templates/git/git.go:302-308`
- [x] populate `Cfg: ctx.Cfg` in the `data := TemplateData{...}` literal at `internal/templates/git/git.go:364-369`
- [x] add a positive subtest to `TestRenderHooks` in `internal/templates/git/git_test.go` that:
  - writes a hook template containing `{{ .Cfg.Raw.git.project_prefix }}` (dot — identifier-safe key) and `{{ index (index .Cfg.Raw.git.hooks .Service) "pre_commit" }}` (index — `pre_commit` is identifier-safe but `.Service` is dynamic so `index` is needed for the outer lookup)
  - constructs a `DevboxConfig` with `Raw: map[string]any{"git": map[string]any{"project_prefix": "PRJ", "hooks": map[string]any{"main": map[string]any{"pre_commit": "echo hi"}}}}`
  - asserts the rendered output contains `PRJ` and `echo hi`
- [x] add a non-identifier-key subtest: template uses `{{ index .Cfg.Raw "my-tool" "api-key" }}` against `Raw: map[string]any{"my-tool": map[string]any{"api-key": "VALUE"}}`; assert rendered output contains `VALUE` — proves the `index` escape hatch works and locks the documented contract
- [x] add a negative subtest: same template, but `Raw` missing the `git` key → expect `missingkey=error` failure mentioning `git`
- [x] add a backward-compat subtest: render a template that does **not** reference `.Cfg`, verify byte-identical output vs the pre-change expectation (reuse an existing golden assertion from `TestRenderHooks` if one exists; otherwise add a small golden)
- [x] run `go test ./internal/templates/git/...` — must pass before next task

### Task 2: Extend IDE renderer TemplateData with Cfg field

- [x] add `Cfg *config.DevboxConfig` to `TemplateData` struct in `internal/templates/ide/ide.go:259-265`
- [x] populate `Cfg: cfg` in the caller at `internal/command/ide.go` where `ide.TemplateData{...}` is constructed (locate by searching for `ide.TemplateData{`)
- [x] extend `internal/command/ide_test.go` with four subtests mirroring Task 1's shape (positive `.Cfg.Raw` dot access, non-identifier `index`-syntax access, missingkey error, backward-compat byte-identical output); use the existing fixture-builder helpers in that file
- [x] run `go test ./internal/templates/ide/... ./internal/command/...` — must pass before next task

### Task 3: Extend AI renderer TemplateData with Cfg field

- [x] add `Cfg *config.DevboxConfig` to `TemplateData` struct in `internal/templates/ai/ai.go:189-195`
- [x] populate `Cfg: cfg` in the caller at `internal/command/render_ai.go:70-75`
- [x] extend `internal/command/render_ai_test.go` with four subtests mirroring Task 1's shape (positive dot, non-identifier `index`, missingkey, backward-compat)
- [x] run `go test ./internal/templates/ai/... ./internal/command/...` — must pass before next task

### Task 4: Cross-renderer consistency and lint

- [ ] grep the three renderer packages to confirm `TemplateData` shapes stay symmetric (`Project`, `Service`, `ServiceCfg`, `Runtime`, `Cfg`) — adjust if any drift
- [ ] for ide and ai callers, ensure `Cfg: cfg` is set on every code path that constructs `TemplateData` (`grep -rn "ide.TemplateData{\|aipkg.TemplateData{\|ai.TemplateData{" internal/`)
- [ ] decide nil-handling for ide/ai: either add a `if cfg == nil` guard at the caller boundary (mirroring `git.go:330-332`) or document at the type that `Cfg` is non-nil when callers come from `internal/command`. Implement the chosen option; do not leave silent nil dereferences possible
- [ ] run `go test ./...` — full suite must pass
- [ ] run `make lint` — no new issues

### Task 5: Update central template variables documentation

This task addresses the central `docs/reference/templates.md` page in full. Per-renderer docs are Task 6.

- [ ] in the "Where templates are evaluated" table (`docs/reference/templates.md:22-31`):
  - **add a new row** for `devbox/templates/git/<pack>/**/*.tmpl` with the same context shape and a link to `render/git.md`
  - **rewrite** the existing ide/ai rows so each lists its full context shape explicitly (do not collapse `ai` into "same shape as IDE" — the page must list each site canonically). New shape for all three rows: `{.Project, .Service, .ServiceCfg, .Runtime, .Cfg}`
- [ ] in the "Render context per site" section (`docs/reference/templates.md:80+`):
  - **rename** the "IDE / AI render packs (strict)" heading at line 96 to "Render packs (git / ide / ai, strict)"
  - extend the variable table (lines 98-103) with a `.Cfg` row, described as: `merged DevboxConfig (advanced). .Cfg.Raw is the post-merge config map after devbox normalization (binaries normalized; services.* injected from services.yml). Dot syntax (.Cfg.Raw.git.project_prefix) works only for identifier-safe keys; use {{ index .Cfg.Raw "my-key" }} for keys with hyphens, dots, leading digits, etc. Prefer the dedicated fields above for common cases.`
  - immediately after the table, add a short advisory paragraph: "IDE and AI packs render into tracked project files. Avoid consuming developer-local or secret keys via `.Cfg.Raw` in those templates — values from `local.yml` will produce per-developer diffs. Git hooks render under `.git/hooks/` (gitignored) and are not subject to this constraint."
- [ ] verify the page renders cleanly (no broken anchors / link checker if one runs in CI)

### Task 6: Update per-renderer documentation

- [ ] `docs/reference/render/git.md`: add `.Cfg` to the variable list; add a worked example showing **both** access forms — `{{ .Cfg.Raw.git.project_prefix }}` for identifier-safe keys and `{{ index .Cfg.Raw "my-tool" "api-key" }}` for non-identifier keys; explain the rule briefly (Go template dot-segments must match `[A-Za-z_][A-Za-z0-9_]*`); note that hooks live under `.git/hooks/` so `local.yml`-derived variation is harmless
- [ ] `docs/reference/render/ide.md`: add `.Cfg` to the variable list with the same dot-vs-`index` explanation and worked example; **prepend** the advisory copy from Task 5: outputs are tracked, do not consume developer-local keys
- [ ] `docs/reference/render/ai.md`: same as ide.md
- [ ] `docs/reference/render/index.md`: if it lists template context fields, sync with the new shape (including the dot-vs-`index` access rule)
- [ ] sanity-check that the `.Raw` description across all four files matches the contract spelled out in Overview ("merged config map after devbox normalization — binaries normalized, services.* injected") and never claims "the literal 3-layer YAML"; also confirm every `.Cfg.Raw` example using a non-identifier key uses `index`, never dot

### Task 7: Update AGENTS.md project structure notes

- [ ] in the `internal/templates/git/`, `ide/`, `ai/` bullets in `AGENTS.md`, add one terse line to each noting that `TemplateData` now carries `Cfg *config.DevboxConfig`
- [ ] do **not** edit `CLAUDE.md` directly — it is a symlink to `AGENTS.md`

### Task 8: Verify acceptance criteria

- [ ] verify all three `TemplateData` structs carry `Cfg *config.DevboxConfig`
- [ ] verify all three callers populate `Cfg: cfg`
- [ ] verify the three positive `.Cfg.Raw.*` (dot) subtests added in Tasks 1–3 pass
- [ ] verify the three non-identifier-key (`index`) subtests added in Tasks 1–3 pass — these lock the documented access-syntax contract
- [ ] verify the three `missingkey=error` subtests added in Tasks 1–3 pass
- [ ] verify the three backward-compat byte-identical subtests added in Tasks 1–3 pass — this is the canonical guard against the IDE/AI tracked-artifact risk
- [ ] verify the central `docs/reference/templates.md` lists git, ide, and ai as separate rows with the new context shape and the renamed "Render packs (git / ide / ai, strict)" section
- [ ] run full test suite: `make test`
- [ ] run linter: `make lint`

## Technical Details

### Struct change (identical in all three packages)

```go
type TemplateData struct {
    Project    config.ProjectConfig
    Service    string
    ServiceCfg config.ServiceConfig
    Runtime    config.RuntimeConfig
    Cfg        *config.DevboxConfig // NEW — merged config; .Cfg.Raw exposes the post-merge config map (see docs/reference/templates.md)
}
```

### Caller change (git already passes ctx.Cfg; ide/ai callers add one line)

```go
data := <pkg>.TemplateData{
    Project:    cfg.Project,
    Service:    name,
    ServiceCfg: svc,
    Runtime:    cfg.Runtime,
    Cfg:        cfg, // NEW
}
```

### Template author experience

Top-level arbitrary block in `devbox/defaults.yml`:

```yaml
git:
  project_prefix: PRJ
  hooks:
    main:
      pre_commit: "devbox commands run services.main.cs-check"
      pre_push: ""
```

In a git hook template (strict mode — `missingkey=error`):

```gotemplate
JIRA_PREFIX="{{ .Cfg.Raw.git.project_prefix }}"        {{- /* dot — `git` and `project_prefix` are identifier-safe */ -}}
{{- $hooks := index .Cfg.Raw.git.hooks .Service }}     {{- /* index — outer key `.Service` is dynamic */ -}}
{{- $cmd := index $hooks "pre_commit" }}
{{ if $cmd }}{{ $cmd }}{{ else }}exit 0{{ end }}
```

If a top-level block uses non-identifier keys (e.g. `my-tool` with a hyphen), the entire access chain must go through `index`:

```yaml
# defaults.yml
my-tool:
  api-key: "abc123"
```

```gotemplate
TOKEN="{{ index .Cfg.Raw "my-tool" "api-key" }}"
```

Dot syntax (`.Cfg.Raw.my-tool.api-key`) is a **parse error** in Go templates — the engine treats `-` as the subtract operator. Documentation in Tasks 5 and 6 spells this out with the same example.

`devbox/local.yml` per-developer override flows through the existing `deepMerge` in `LoadConfig`. For git hooks this is the intended use case. For ide/ai packs, template authors should treat `.Cfg.Raw` as "values that may vary per developer" and not consume keys that come exclusively from `local.yml` if they want repo-deterministic output.

### `.Raw` contract — what it actually is

After `LoadConfig`, `cfg.Raw` is:

1. `deepMerge(devbox.yml, defaults.yml, local.yml)` as `map[string]any` —
2. with `binaries:` re-overwritten from top-level `devbox.yml` only (defaults/local discarded) —
3. with `__configPath` injected —
4. with `services.*` injected from the separately loaded `devbox/services.yml`, minus `render.<kind>.*` fields.

Source: `internal/config/devbox.go:622-738`. Doc cross-refs: `docs/reference/config/devbox.md:74-91`, `AGENTS.md:13`.

### Why no typed sub-config

Per planning conversation: `defaults.yml` is intentionally fully customisable with user-defined blocks. Forcing every consumer convention through a typed Go struct would defeat that. Template authors get untyped `.Cfg.Raw` access (dot syntax for identifier-safe keys, `index` for the rest); `missingkey=error` plus the backward-compat test in each renderer is the guard rail.

## Post-Completion

*Items requiring manual intervention or external systems — informational only.*

**Pilot project updates (not in this repo)**:

- Move `GIT_PROJECT_PREFIX` and hook-command env entries out of `services.main.cli.env`.
- Add a `git:` block to `devbox/defaults.yml` with `project_prefix` and `hooks.<svc>` entries.
- Update the pilot's hook templates to reference `{{ .Cfg.Raw.git.project_prefix }}` and `{{ index (index .Cfg.Raw.git.hooks .Service) "pre_commit" }}`.
- Optionally, add a `devbox/local.yml` snippet to the pilot project's README showing per-developer hook overrides.
- Audit any existing IDE/AI templates in the pilot for accidental `.Cfg.Raw` consumption of `local.yml`-sourced keys (none today since `.Cfg` is new — but worth a sweep once adopted).

**Manual verification**:

- Run `devbox render git`, `devbox render ide`, `devbox render ai` against the pilot and confirm rendered files match expectations.
- With a non-empty `local.yml`, render ide/ai and confirm the diff against a clean `local.yml` is **empty** unless the pilot's templates intentionally consume the overridden key (validates that the documentation advisory is respected in practice).
- Verify `devbox validate` still passes against the pilot.

**Follow-up (optional, not in this plan)**:

- If a particular `defaults.yml` block (e.g. `git:`) stabilises across many projects, consider promoting it to a typed sub-config with `validIdentifierKey` enforcement on map keys (`internal/config/devbox.go:540-554`). Both typed and `.Raw` access paths would remain available.
