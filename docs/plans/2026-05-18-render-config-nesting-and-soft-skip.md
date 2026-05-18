# Render Config Nesting and Soft-Skip on Missing Implicit Pack

## Overview

Two related changes to the services-render surface:

1. **Nest render toggles under `render:`** — in `services.yml`, move the three per-service render blocks (`ide`, `ai`, `git`) under a single nested `render:` field on each service. Hard replace, no backwards compatibility, no migration messages. The change is a clean rename in struct shape; accessor methods (`IDERenderEnabled`, `AIRenderEnabled`, `GitRenderEnabled`, and `*Explicit` variants) keep their public signatures, only their internal field paths change.

2. **Soft-skip on implicit missing template pack** — when a service has rendering enabled for a kind (ide/ai/git) and no `template:` is set explicitly, and the implicit fallback chain (`<service-name>` then `default`) exhausts without finding a pack, surface a warning and skip that service for that kind instead of returning an error. **Explicit** `template: foo` pointing at a nonexistent pack continues to be a hard error (catches typos). The warning surfaces in both `devbox render {ide,ai,git}` runtime output and in `devbox validate templates` diagnostics (severity warning).

Why: today, adding a new service in a project that doesn't yet have IDE/AI/git template packs in `devbox/templates/<kind>/` immediately breaks `devbox render`. The default-on render policy is correct for the common case (every app service gets editor and git hooks rendered), but the failure mode is too aggressive — a missing pack on a single service should not abort the run.

## Context (from discovery)

**Struct shape** (`internal/config/devbox.go:272-365`):
- `ServiceIDEConfig{Enabled *bool, Template string}` (yaml: `ide`)
- `ServiceAIConfig{Enabled *bool, Template string}` (yaml: `ai`)
- `ServiceGitHooksConfig{Enabled *bool, Template string}` (yaml: `git`)
- All three live directly on `ServiceConfig`; accessors at `ServiceConfig.IDERenderEnabled()`, etc.

**Extends inheritance** (`internal/config/devbox.go:800-823`): six explicit field-by-field merges for ide/ai/git Enabled+Template inside `LoadServicesConfig`.

**Raw injection** (`internal/config/devbox.go:888-921`): `injectServicesIntoRaw` already excludes ide/ai/git — no change needed there.

**Template-pack resolution** (one per kind):
- `internal/templates/ide/ide.go:172-230` — `ResolveTemplatePack` returns `(packDir, packName, error)`
- `internal/templates/ai/ai.go:96-156` — same shape
- `internal/templates/git/git.go:67-125` — same shape
- All three: explicit `template: foo` not found → error; implicit chain exhausted → error wrapping `os.ErrNotExist`.

**Render command call sites** (each currently surfaces resolver error as a hard failure):
- `internal/command/ide.go:209-211`
- `internal/command/render_ai.go:46-48`
- `internal/command/render_git.go:44-46`

**Validators** (all three currently emit `SeverityError` on resolver failure):
- `internal/validate/templates/ide.go:109-117`
- `internal/validate/templates/ai.go:106-114`
- `internal/validate/templates/git.go:114-122`

**Services loader strictness** (`internal/config/devbox.go:747`): `yaml.Unmarshal` — lenient. Old `ide:`/`ai:`/`git:` keys at service level will be silently dropped after the rename. Acceptable per user direction ("no migration messages"). Document the rename in `docs/reference/config/services.md` so users find it.

**Tests covering accessors**: `internal/config/devbox_test.go` lines 2993, 3045, 3178, 3230, 3377 (plus the missing-but-implied Git tests).

**Docs**: `docs/reference/config/services.md` (lines 69-77, 177-296) shows the current flat format and needs the wholesale `render:` rewrite.

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- Run `make test` after each change
- This is a hard rename; expect compile errors during Task 1 until call sites are migrated

## Testing Strategy

- **Unit tests**: required for every task
- No e2e suite in this repo — Go test suite only
- Focused commands: `go test ./internal/config/...`, `go test ./internal/templates/...`, `go test ./internal/validate/...`, `go test ./internal/command/...`
- Full suite + linter before merge: `make test && make lint`

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope

## What Goes Where

- **Implementation Steps** (`[ ]`): code, tests, docs that live in this repo
- **Post-Completion** (no checkboxes): downstream consumer projects, anything external

## Implementation Steps

### Task 1: Introduce `ServiceRenderConfig` wrapper and update struct shape

- [x] add `ServiceRenderConfig{IDE ServiceIDEConfig, AI ServiceAIConfig, Git ServiceGitHooksConfig}` struct in `internal/config/devbox.go` with yaml tags `ide`, `ai`, `git`
- [x] in `ServiceConfig`, replace the three top-level fields `IDE`, `AI`, `Git` with a single `Render ServiceRenderConfig` field (yaml: `render`)
- [x] update accessor methods `IDERenderEnabled`, `IDERenderEnabledExplicit`, `AIRenderEnabled`, `AIRenderEnabledExplicit`, `GitRenderEnabled`, `GitRenderEnabledExplicit` to read from `svc.Render.*` instead of `svc.IDE`/`svc.AI`/`svc.Git` — public signatures unchanged
- [x] update extends-inheritance loop in `LoadServicesConfig` (around line 800-823) to operate on `parent.Render.*` / `svc.Render.*`

**Migration sweep — non-test reads and literals**:
- [x] `internal/templates/ide/ide.go` — `ResolveTemplatePack` reads `svc.IDE.Template` (lines 178-199); rewrite to `svc.Render.IDE.Template` and update the doc comment at line 170
- [x] `internal/templates/ai/ai.go` — `ResolveTemplatePack` reads `svc.AI.Template` (lines 103-124); rewrite to `svc.Render.AI.Template` and update the doc comments at lines 92-94
- [x] `internal/templates/git/git.go` — `ResolveTemplatePack` reads `svc.Git.Template` (lines 73-94); rewrite to `svc.Render.Git.Template` and update doc comment at line 65
- [x] grep `internal/{templates,command,validate}` for remaining `.IDE\.\|.AI\.\|.Git\.` field reads and `ServiceConfig{…IDE:/AI:/Git:…}` literals; rewrite each to the `Render.*` path / `Render: ServiceRenderConfig{…}` literal (completed: updated hash.go, all template resolver tests, command tests, and validate tests)

**Migration sweep — test fixtures and literals**:
- [x] `internal/config/devbox_test.go` — broad sweep, not just the accessor tests. Run `rg '\.IDE\.|\.AI\.|\.Git\.|^\s+(ide|ai|git):\s*$' internal/config` first to enumerate every hit (~83 hits today, spanning lines ~3091-3589). Categories to migrate: (a) `ServiceConfig{IDE: …}` / `{AI: …}` / `{Git: …}` struct literals in the accessor test tables; (b) field reads in extends-inheritance tests, e.g. `parent.Git.Template`, `child.IDE.Enabled` around lines 3500-3580; (c) assertions like `cfg.Services["main"].Git.Template` (e.g. line 3588) — rewrite to `cfg.Services["main"].Render.Git.Template`; (d) inline YAML fixtures with `ide:`/`ai:`/`git:` at service indent — nest each under `render:`. Do not skip a hit just because it's outside the accessor-table range.
- [x] `internal/templates/git/git_test.go` — `ServiceConfig{Git: config.ServiceGitHooksConfig{Template: …}}` at lines 39, 53, 99 and any other call sites; rewrite to `Render: config.ServiceRenderConfig{Git: …}`
- [x] (no dedicated `ide_test.go`/`ai_test.go` in `internal/templates/`; resolver coverage lives in `internal/command/ide_test.go` and `internal/command/render_ai_test.go` — captured below)
- [x] `internal/command/ide_test.go` — `IDE: config.ServiceIDEConfig{Template: …}` at lines 300, 375, 404, 422 and elsewhere; rewrite to nested form
- [x] `internal/command/render_ai_test.go` — same sweep (e.g., line 66) (completed with regex and targeted fixes; also updated YAML fixtures)
- [x] `internal/command/render_git_test.go` — same sweep (no struct literal errors found)
- [x] `internal/validate/templates/templates_test.go` — same sweep (1 instance fixed)
- [x] any YAML fixtures under `testdata/` that use top-level `ide:`/`ai:`/`git:` on service entries (grep `testdata/` for those keys); migrate to nested `render:` form (no instances found)

**New tests for the new shape**:
- [x] add one extends-inheritance test case covering the nested render block (parent has `render.ide.enabled = true`, child unset → child inherits) — already covered by existing TestLoadServicesConfig_IDEEnabled/AIEnabled/GitEnabled with updated YAML fixtures
- [x] add one `LoadServicesConfig` YAML parsing test for the new `render:` block (inline fixture, assert accessors return expected values) — updated existing tests to use nested format
- [x] run `go test ./...` (full repo) — must pass before next task; compile errors in any package mean the migration sweep missed something (all tests passing)

### Task 2: Migrate template-pack helpers to extra-bool return — single landing with all callers

This task is structured as one atomic landing because changing `ResolveTemplatePack`'s signature breaks every caller. Splitting it leaves the repo uncompilable across tasks, so we update helpers **and** all callers before running the test suite.

- [x] change `internal/templates/ide/ide.go` `ResolveTemplatePack` signature to `(packDir, packName string, found bool, err error)`; when implicit fallback chain exhausts without finding any pack and `explicit == ""`, return `("", "", false, nil)` instead of an error; explicit-not-found path still returns `err != nil`
- [x] mirror the change in `internal/templates/ai/ai.go` `ResolveTemplatePack`
- [x] mirror the change in `internal/templates/git/git.go` `ResolveTemplatePack`
- [x] update **callers' signatures only** (not yet the warn+skip behavior — that lands in Tasks 3/4): `internal/command/ide.go:209`, `internal/command/render_ai.go:46`, `internal/command/render_git.go:44` — change call sites to receive the extra bool; for now, treat `!found` as if it were the prior error (return a synthesized error preserving today's behavior) so existing tests still pass. Tasks 3/4 will replace that synthesized-error branch with warn+skip / SeverityWarning.
- [x] same shim in validators: `internal/validate/templates/{ide,ai,git}.go` — receive the bool, treat `!found` as `SeverityError` for now; Task 4 will downgrade to `SeverityWarning`.
- [x] update resolver-coverage tests (which live in `internal/command/`, not the resolver packages — `internal/command/ide_test.go` lines 275-450ish, plus `internal/command/render_ai_test.go` and `internal/command/render_git_test.go`): mechanical signature update; add a new case per kind for "implicit-missing returns found=false, err=nil" against the resolver directly. Keep the explicit-missing-returns-error cases.
- [x] verify no other **production** callers of `ResolveTemplatePack` exist beyond the three render commands and three validators (`grep -rn ResolveTemplatePack internal/` — test files in `internal/command/` will also match and need the signature update, that's expected)
- [x] run `go test ./...` (full repo) — must pass before next task; behavior unchanged from pre-Task-2, only the internal signature has shifted

### Task 3: Render commands warn-and-skip on implicit missing pack

- [x] update `internal/command/ide.go` (around line 209) — inside `renderIDEConfigs` (the per-service helper at line 177, called from the outer loop): when `ResolveTemplatePack` returns `found == false && err == nil`, emit a warning via `render.Writer.Warning` (one line per service: e.g., `"ide [<name>] — skipped (no template pack found; tried <name>, default)"`) and `return nil`. The outer service loop continues on its own. Keep the `err != nil` branch as a hard return.
- [x] same change in `internal/command/render_ai.go` `renderAgentsForService` (around line 46) — warn and `return nil`
- [x] update `internal/command/render_git.go` `renderGitHooksForService` (around lines 23-44): **reorder so explicit-pack typos still surface as errors**. Today the flow is: `PrepareHub` → `ResolveGitHooksDir` (early-return on `DirMissing`/`DirWorktree`) → `ResolveTemplatePack`. With the new signature this lets an explicit `render.git.template: typo` get silently skipped when `src/.git` is missing. Fix: if `svc.Render.Git.Template != ""`, call `ResolveTemplatePack` **before** the `ResolveGitHooksDir` switch, so the explicit-template error is reported regardless of `.git` state. For implicit (no explicit template), keep the existing order — `.git` absence wins over the implicit-pack warning (one signal per service is enough). Then in the post-`.git`-check branch, apply the same `found == false && err == nil` warning + `return nil` as IDE/AI.
- [x] add tests for each render command exercising: (a) implicit missing → warning + service skipped + other services continue, (b) explicit missing → hard error (regression)
- [x] add a focused git test: service with `render.git.template: typo` AND no `src/.git` → explicit-template error wins (not the `.git`-missing warning)
- [x] note on output channel: `render.Writer.Warning` writes yellow text to the writer the command was wired with (commands use `render.Stdout()` — see `internal/command/{ide,render_ai,render_git}.go`); we don't promise stderr. Warning goes to the same stream as other render warnings.
- [x] run `go test ./internal/command/...` — must pass before next task

### Task 4: Validators emit warning instead of error on implicit missing pack

- [ ] update `internal/validate/templates/ide.go` (around line 109-117): when resolver returns `found == false && err == nil`, emit `SeverityWarning` diagnostic with message describing the missing implicit pack and a hint pointing at `devbox/templates/ide/<service>` and `devbox/templates/ide/default`; keep `SeverityError` path for `err != nil`
- [ ] same change in `internal/validate/templates/ai.go` (around 106-114)
- [ ] same change in `internal/validate/templates/git.go` (around 114-122)
- [ ] update `internal/validate/templates/templates_test.go` — add cases for implicit-missing → warning, keep explicit-missing → error cases
- [ ] verify exit-code behavior with `--strict`: warnings still trip exit code 1 under `--strict`, otherwise non-fatal
- [ ] run `go test ./internal/validate/...` — must pass before next task

### Task 5: Update docs to reflect both changes

- [ ] rewrite `docs/reference/config/services.md` examples (lines 69-77 and 177-296) to use nested `render: { ide:, ai:, git: }` form throughout
- [ ] add a subsection under "Template pack resolution" stating: implicit fallback exhaustion → warning + skip; explicit `template:` typo → hard error. Mention both `devbox render` runtime and `devbox validate templates`.
- [ ] rewrite `docs/reference/render/ide.md` — examples and prose currently use flat-key paths and the old hard-error semantics (e.g., lines 53 and 116); update to nested `render.ide.*` paths and document the new implicit-missing-warn behavior
- [ ] same for `docs/reference/render/ai.md`
- [ ] same for `docs/reference/render/git.md`
- [ ] update command help long descriptions in `internal/command/{ide,render_ai,render_git}.go` (cobra `Long` strings) — they encode flat paths and old hard-error semantics that propagate into generated CLI docs
- [ ] regenerate generated CLI docs: `bin/devbox docs generate --scope cli` (the `commands` scope is for the declarative command registry and needs a project) — verify `docs/reference/cli/devbox_render_ide.md`, `devbox_render_ai.md`, `devbox_render_git.md` reflect the new help text
- [ ] grep `docs/` for other mentions of `ide:`/`ai:`/`git:` at service level and update each (likely under deploy.md, agents docs, info.md examples — verify and migrate)
- [ ] edit `AGENTS.md` (canonical; `CLAUDE.md` is a symlink to it — repo guidelines say edit `AGENTS.md` only): update the long descriptive paragraph about `ServiceConfig` to reference `Render.IDE/AI/Git` and the new soft-skip semantics; update the `internal/templates/{ide,ai,git}` summary lines noting the new `(packDir, packName, found bool, err error)` signature; note the git "resolve explicit pack before .git check" ordering quirk
- [ ] verify `CLAUDE.md` is still a symlink to `AGENTS.md` after the edit (`ls -l CLAUDE.md`) — do not overwrite it
- [ ] no test step (docs only) — proceed to next task

### Task 6: Verify acceptance criteria and run full test suite

- [ ] all `ide:`/`ai:`/`git:` references at service level in YAML examples and fixtures migrated to `render:` form (grep `testdata/` and `internal/`)
- [ ] no public API surface broken — accessor methods on `ServiceConfig` keep their names and return types
- [ ] missing implicit pack: render commands warn and continue; validator emits warning severity
- [ ] missing explicit pack: render commands error; validator emits error severity (incl. the git ordering case: explicit-typo + missing `.git` → explicit-typo error wins)
- [ ] generated CLI docs in `docs/reference/cli/devbox_render_*.md` reflect the new help text (no stale flat paths)
- [ ] `make test` passes
- [ ] `make lint` passes
- [ ] `bin/devbox validate templates` against a project with one service missing implicit pack reports the warning (smoke-check)

## Technical Details

**New struct shape**:
```go
type ServiceRenderConfig struct {
    IDE ServiceIDEConfig       `yaml:"ide"`
    AI  ServiceAIConfig        `yaml:"ai"`
    Git ServiceGitHooksConfig  `yaml:"git"`
}

type ServiceConfig struct {
    // ... existing fields ...
    Render ServiceRenderConfig `yaml:"render"`
    // IDE, AI, Git fields removed
}
```

**New resolver signature** (all three kinds):
```go
func ResolveTemplatePack(svc config.ServiceConfig, projectRoot, svcName string) (packDir, packName string, found bool, err error)
```

Semantics:
- `err != nil` — resolver could not proceed (filesystem error, explicit pack missing, identifier-unsafe name, etc.) → caller treats as hard failure
- `err == nil && !found` — implicit chain exhausted cleanly → caller emits warning + skips
- `err == nil && found` — pack resolved → caller proceeds with `packDir`/`packName`

**Strictness note**: `LoadServicesConfig` stays lenient (`yaml.Unmarshal`), matching the current pattern. Old top-level `ide:`/`ai:`/`git:` keys at service level will be silently dropped after the rename. Per user direction we do not add a migration error; the rename is documented in `services.md`.

**Warning channel**: render commands route warnings through `render.Writer.Warning` (yellow output via the configured writer). Today commands are wired with `render.Stdout()` — we do not promise stderr routing in this plan. Validator diagnostics use `validate.SeverityWarning` so they sort and render with the existing diagnostics table. Both surfaces give the user the same actionable text.

**Git command ordering quirk**: `renderGitHooksForService` currently checks for `src/.git` before resolving the template pack, so a service missing `.git` short-circuits before any pack-resolution diagnostic fires. With the new soft-skip semantics this would hide explicit-typo errors (`render.git.template: typo` + missing `.git` → silent skip). The plan resolves this by reordering: when an explicit `Render.Git.Template` is set, call `ResolveTemplatePack` before the `.git` switch so the explicit-typo error still surfaces; when the template is implicit, keep the existing `.git`-first ordering so a service that legitimately has no `.git` produces a single skip warning rather than two.

## Post-Completion

*Items requiring manual or downstream action — no checkboxes.*

**Downstream consumers of devbox**:
- Any real Devbox projects in the org with services using `ide:`/`ai:`/`git:` at service level need their `services.yml` updated to nest under `render:`. There is no migration tooling — coordinate the rename with project owners after merge.

**Smoke validation against a real project**:
- Run `bin/devbox validate templates` on at least one project that previously errored due to a missing implicit pack; confirm warning + non-zero-only-under-strict behavior.
- Run `bin/devbox render ide` (and ai, git) on a project where one service has no pack — confirm the warning appears and other services render successfully.
