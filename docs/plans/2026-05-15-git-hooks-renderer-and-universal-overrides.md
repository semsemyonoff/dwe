# Git Hooks Renderer and Universal Template Overrides

## Overview

Add a third template-pack renderer — `devbox render git` — alongside the existing `devbox render ide` and `devbox render ai`. While we are in the template subsystem, take three cross-cutting steps:

1. **New renderer (`git`)** — render shell git hooks from a manifest-driven pack into `<service.Dir>/src/.git/hooks/`. Skip services that don't have a `src/.git` directory (warn + continue), default-enabled for service `type: app` only (mirrors IDE).
2. **Universal per-file local override** — for any pack `<kind>/<name>`, a sibling shadow pack at `devbox/templates/<kind>/<name>.local/<rel>` overrides the in-repo `devbox/templates/<kind>/<name>/<rel>` file. Applies retroactively to IDE and AI. Mirrors the existing `devbox/local.yml` / `devbox/docker.local.yml` convention (sibling files with `.local` suffix inside the tracked `devbox/` dir, gitignored by pattern). `.devbox/` stays runtime-only.
3. **IDE manifest migration (hard cut)** — convert IDE from directory-walk to manifest-based, matching AI/git. Existing IDE packs without `manifest.yml` produce a hard error with a migration hint. All three renderers share one canonical schema.

Solves: the gap in the template subsystem (git hooks are checked-in scripts that need per-service templating); the inconsistency between IDE walk and AI manifest; and the lack of a local-override escape hatch for any committed template.

## Context (from discovery)

**Files/components involved:**
- `internal/templates/ai/ai.go` — reference implementation (manifest, ResolveTemplatePack, SelectServices, RenderTemplateFile)
- `internal/templates/ide/ide.go` — to be migrated to manifest format
- `internal/command/render_ai.go`, `internal/command/ide.go`, `internal/command/env.go` (parent `render` cmd)
- `internal/config/devbox.go` — `ServiceConfig`, `ServiceIDEConfig`, `ServiceAIConfig`, accessors
- `internal/validate/templates/` — per-kind validator wiring
- `internal/pathsafe/` — symlink/escape guards used by all renderers

**Key patterns found:**
- Manifest schema in AI: `render: [{from, to}]`, `symlinks: [{link, to}]`, strict-decode (`KnownFields(true)`).
- Pack resolution chain: explicit `<kind>.template` → `<service-name>` → `default`; `os.IsNotExist` falls through, anything else hard-errors.
- Collision policy: IDE = deepest extends wins (per-hub); AI = shallowest wins. Git follows IDE (deepest).
- Default-enabled accessor pattern: `<Kind>RenderEnabled() bool` and `<Kind>RenderEnabledExplicit() (bool, bool)` on `ServiceConfig`.
- `pathsafe.CheckNoSymlinks` + `pathsafe.ContainedRel` + `pathsafe.EnsureRealUnder` guard every write path.

**Dependencies identified:**
- `internal/command/root.go` — adds `render git` to the `render` group.
- `internal/validate/templates/` — registers a new git validator.
- Docs: new entry in `docs/reference/render/` and updates to `services.md`.

## Development Approach

- **Testing approach**: Regular (code first, then tests). Matches existing style (table-driven, `testdata/` fixtures).
- Complete each task fully before moving to the next.
- Make small, focused changes — each task is one logical unit.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task.
- **CRITICAL: all tests must pass before starting the next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run `make test` after each task (per-task gate, enforced by the explicit `make test` checkbox at the end of every task). `make lint` is run **once at final acceptance** (Task 15) rather than per-task to avoid stalling iteration on style nits in mid-implementation code; if a task introduces a clearly-suppressible lint regression, fix it in that task rather than carrying it to the end.
- Maintain backward compatibility for AI packs; IDE packs require a migration step (see Post-Completion).

## Testing Strategy

- **Unit tests**: required for every task. Manifest parsing (strict decode, error messages), pack resolution (override chain, fallback), renderer (template eval, missingkey=error, file mode 0755 for hooks), validators (diagnostic shape), and config accessors.
- **Fixture tests**: `testdata/` under each touched package — minimal pack fixtures with manifest, sample `.tmpl`, expected output.
- **E2E**: not applicable (CLI has no UI e2e suite). End-to-end coverage via `internal/command/*_test.go` table tests that exercise the cobra command with a fixture project.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.

## What Goes Where

- **Implementation Steps** (`[ ]`): in-tree code, tests, docs.
- **Post-Completion**: items requiring human verification or external migration (existing project IDE packs need to gain a `manifest.yml`).

## Implementation Steps

### Task 1: Create shared manifest types package

Extract the manifest schema into `internal/templates/manifest/` so AI, IDE, and git all reference one type. **Split validation into shape (pure) and source-existence (resolver-aware)** so callers can plug in override-aware lookup.

- [x] create `internal/templates/manifest/manifest.go` with `File`, `RenderEntry{From, To}`, `SymlinkEntry{Link, To}` (yaml tags identical to current AI types). The primary type is named `File` not `Manifest` to avoid `manifest.Manifest` stutter at call sites — callers write `m, err := manifest.Load(path)` returning `*manifest.File`.
- [x] add sentinel `ErrManifestMissing = errors.New("manifest file not found")` (lowercase, no punctuation, no package prefix in the bare sentinel since we wrap with package context at the call site)
- [x] add `Load(path string) (*File, error)` with strict-decode (`KnownFields(true)`). On missing file: `return nil, fmt.Errorf("loading %s: %w: %w", path, ErrManifestMissing, err)` (Go 1.20+ multi-`%w` so BOTH `errors.Is(err, manifest.ErrManifestMissing)` AND `errors.Is(err, os.ErrNotExist)` return true). On YAML errors: `fmt.Errorf("loading %s: %w", path, err)`. Add a unit test that asserts both `errors.Is` predicates on the missing-file path — protects against regression to single-`%w`.
- [x] add `ValidateShape(m *File, destRoot, label string) error` — pure schema validation, **no FS access**: at least one render or symlink entry exists; no duplicate `to`; every `to` and `link` is a contained relative path under `destRoot` (computed without reading disk); every symlink `to` references a declared render `to`; per-kind extras (e.g. git "no slashes in `to`") live in the kind's wrapper, not here. The param is named `destRoot` (not `hubDir`) so AI/IDE pass the hub dir and git passes the hooks dir using the same shared signature.
- [x] add `ValidateSources(m *File, resolve func(rel string) (path string, fromOverride bool, err error), label string) error` — for each `RenderEntry.From`, call `resolve(from)` and verify the returned path exists as a regular file (using `os.Lstat` so symlinked sources are rejected). Returns an error per missing/invalid source. **No path joining inside this function** — the resolver owns physical lookup.
- [x] write tests for `Load`: success, missing file (both `errors.Is` predicates true), unknown field error (message contains file path), malformed YAML
- [x] write tests for `ValidateShape`: empty manifest rejected, duplicate `to`, escaping `to`, dangling symlink target, escaping symlink — all checked WITHOUT requiring `from` files to exist on disk
- [x] write tests for `ValidateSources` with a stub resolver: `from` resolves to existing file → ok; resolver returns `os.ErrNotExist` → error; resolver returns symlink path → error
- [x] run `make test` — must pass before next task

### Task 2: Add packroot resolver for universal local override

Centralize the per-file shadow lookup so all three renderers behave identically. The shadow lives as a sibling `.local/` pack inside the same tracked `devbox/templates/<kind>/` directory — matching the existing `*.local.yml` sibling convention.

- [x] create `internal/templates/packroot/packroot.go` with `Resolve(projectRoot, kind, packName, rel string) (path string, fromOverride bool, err error)`
- [x] resolution order — both candidates apply the same regular-file discipline via `os.Lstat`:
  1. Override path `<projectRoot>/devbox/templates/<kind>/<packName>.local/<rel>`:
     - regular file → return with `fromOverride=true`
     - exists but NOT a regular file (directory, symlink, device, fifo) → **hard error**, do NOT silently fall back to canonical (otherwise a bad override masks itself)
     - does not exist → fall through to step 2
  2. Canonical path `<projectRoot>/devbox/templates/<kind>/<packName>/<rel>`:
     - regular file → return with `fromOverride=false`
     - exists but NOT a regular file → **hard error** with a clear message naming the offending path (do NOT let downstream `os.ReadFile` surface an unclear "is a directory" or "operation not supported" error)
     - does not exist → wrapped `os.ErrNotExist`
- [x] reuse `pathsafe.CheckNoSymlinks` + `pathsafe.ContainedRel` on both candidate paths — reject symlink components and escaping `rel`
- [x] write tests covering: shadow hit (regular file); fallback to in-repo pack when shadow absent; neither present; symlink in shadow path rejected; `rel` containing `..` rejected; **shadow path exists as directory → hard error (no silent fallback)**; **shadow path exists as a symlink → hard error**; **canonical path exists as directory → hard error with named path**; **canonical path exists as symlink/device → hard error** (parity with override discipline)
- [x] write tests for the `fromOverride` flag wiring (renderers will surface this as an info message)
- [x] run `make test` — must pass before next task

### Task 3: Migrate AI renderer to shared types and packroot resolver

Replace AI's private `Manifest`/`ValidateManifest` with the shared package, and route every `from:` read through `packroot.Resolve`.

- [x] in `internal/templates/ai/ai.go`, replace local `Manifest`/`RenderEntry`/`SymlinkEntry` with type aliases over `internal/templates/manifest` types: `type Manifest = manifest.File` (Go type alias preserves the existing `ai.Manifest` symbol used by external callers), plus aliases for `RenderEntry` and `SymlinkEntry`
- [x] **API change — `ai.ResolveTemplatePack`**: current signature returns only `packDir`, but the new validation flow needs `packName` too (to call `packroot.Resolve(projectRoot, "ai", packName, rel)`). Change `ai.ResolveTemplatePack(svc, projectRoot, serviceName)` to return `(packDir, packName string, err error)` — `packName` is the resolved chain choice (explicit `svc.AI.Template`, or `serviceName` when that pack exists, or `"default"`). Update ALL AI callers in this task: `internal/command/render_ai.go:46` AND `internal/validate/templates/ai.go:99`. **IDE has the same change, but it lives in Task 8 — do not touch IDE files here.** Git's `ResolveTemplatePack` is written with the dual-return signature from the start in Task 5.
- [x] **API change — `ai.ValidateManifest`**: the current `ai.ValidateManifest(m *Manifest, packDir string) error` cannot carry `projectRoot`/`packName` needed by `packroot.Resolve`. Introduce a new signature `ai.ValidateManifest(m *Manifest, projectRoot, packName, destRoot string) error`. The fourth param is named `destRoot` (not `hubDir`) so the same shape works for git in Task 5 where the destination is `hooksDir` rather than the service hub. For AI specifically, callers pass the hub dir. Update ALL AI callers in this task — `internal/command/render_ai.go:56` AND `internal/validate/templates/ai.go:126`. **IDE has the same change, but it lives in Task 8 — do not touch IDE files here.** Deprecate/remove the old signature in the same change — internal package, no external consumers, clean break. Repo must compile at the end of Task 3 (the mandatory `make test` gate would otherwise fail).
- [x] `ai.ValidateManifest` internally calls `manifest.ValidateShape(m, destRoot, "ai pack <packName>")` (pure, no FS) and then `manifest.ValidateSources(m, resolveFn, label)` where `resolveFn := func(rel string) (string, bool, error) { return packroot.Resolve(projectRoot, "ai", packName, rel) }`. For AI, `destRoot` is the service hub directory.
- [x] change `RenderTemplateFile` (and its callers) to take `(projectRoot, packName, rel)` and call `packroot.Resolve` instead of joining `packDir` + `rel` directly
- [x] when override is hit, emit one-line `render.Writer.Info` "using local override: devbox/templates/ai/<pack>.local/<rel>"
- [x] update existing AI tests to use the new signature; add a test where a shadow file at `devbox/templates/ai/default.local/foo.tmpl` overrides `devbox/templates/ai/default/foo.tmpl` and the resulting file has the override content
- [x] add a regression test: canonical pack has `manifest.yml` listing `foo.tmpl`, canonical `foo.tmpl` is **absent**, override `default.local/foo.tmpl` is present → `ai.ValidateManifest` passes (resolver hits the override) AND `render ai` succeeds. Without resolver-aware validation, this test fails.
- [x] run `make test` — must pass before next task

### Task 4: Add `ServiceGitHooksConfig` type, accessors, and `extends` inheritance

Wire the new per-service block into config. Inheritance through `extends` is REQUIRED — without it, child services sharing a hub will behave inconsistently with their parent (mismatching IDE/AI which already inherit `enabled`/`template`).

- [x] in `internal/config/devbox.go`, add `ServiceGitHooksConfig{Enabled *bool, Template string}` with yaml tags `enabled`, `template`
- [x] add `Git ServiceGitHooksConfig \`yaml:"git"\`` to `ServiceConfig`
- [x] add accessors `GitRenderEnabledExplicit() (bool, bool)` (mirrors IDE — `(true, false)` for `type: app` when nil, `(false, false)` otherwise; respects explicit value) and `GitRenderEnabled() bool`
- [x] **extend `resolveExtendsInheritance`** (the function at ~config/devbox.go:715 that handles `IDE`/`AI` inheritance at lines 776–790): add a parallel block for `Git` — `if svc.Git.Enabled == nil && parent.Git.Enabled != nil { v := *parent.Git.Enabled; svc.Git.Enabled = &v }` and `if svc.Git.Template == "" { svc.Git.Template = parent.Git.Template }`. Same shape as the existing IDE/AI inheritance, applied in the same topological pass.
- [x] ensure the `git.*` block is NOT injected into `raw["services"]` (same rule as `ide.*` and `ai.*`); verify by reading the relevant `LoadConfig` injection logic and confirming no change needed
- [x] write tests for the accessors: explicit true, explicit false, nil + app, nil + non-app
- [x] **write tests for `extends` inheritance**: single-hop (child inherits `enabled=true` from parent), multi-hop C→B→A (grandchild inherits from grandparent), child explicit overrides parent, child explicit `enabled: false` wins over parent `enabled: true`, child template overrides parent template
- [x] write a config-load test confirming `git.*` does not appear in `Raw["services"][svc]` (parity with ide/ai)
- [x] run `make test` — must pass before next task

### Task 5: Add git pack types, resolver, and renderer

The bulk of the new feature.

- [x] create `internal/templates/git/git.go` with: `SkippedService` struct (`Name`, `Reason`), `ResolveTemplatePack(svc, projectRoot, serviceName) (packDir, packName string, err error)` — same dual-return signature that Task 3 introduces for AI and Task 8 introduces for IDE; same chain (explicit → service-name → default). `SelectServices(cfg) (selected []string, skipped []SkippedService)` (filter on `Enabled && GitRenderEnabled()`, drop empty `Dir`, **deepest-extends-wins** on `Dir` collisions for parity with IDE)
- [x] **pre-flight service-dir containment** (match IDE/AI discipline before any `MkdirAll`): caller must validate `svc.Dir` via `pathsafe.ContainedRel(absRoot, filepath.Join(absRoot, svc.Dir))` (rejects `dir: ../outside` and `dir: /etc/...`) AND `pathsafe.CheckNoSymlinks(absRoot, filepath.Join(absRoot, svc.Dir), "service "+name)` (rejects a real symlinked `services/<name>` that points elsewhere) BEFORE calling `ResolveGitHooksDir`. The downstream `EnsureRealUnder` (after MkdirAll) catches TOCTOU but cannot prevent creating `../outside/src/.git/hooks` on disk if the hub itself escapes. Implement as a small helper `git.PrepareHub(absRoot, svcName, svc) (absHub string, err error)` called once per service before `ResolveGitHooksDir`. Same helper pattern is fine to extract for AI/IDE later, but not in scope here.
- [x] add `ResolveGitHooksDir(absHub string) (absHooks string, status DirStatus, err error)` where `DirStatus` is one of `DirOK | DirMissing | DirWorktree`. Compute `<absHub>/src/.git`. If it's a regular file (worktree or submodule), return `(_, DirWorktree, nil)` — caller surfaces a `Warning` and skips. Initial scope does **not** follow `gitdir:` pointers; worktree support is deferred (see Post-Completion). If `src/.git` is missing entirely, return `DirMissing`. If it's a directory, return `(<absHub>/src/.git/hooks, DirOK, nil)`. Reject any symlink in `<absHub>/src/.git/{hooks}` path components via `pathsafe.CheckNoSymlinks`.
- [x] add `LoadManifest(packDir string) (*manifest.File, error)` — thin wrapper around `manifest.Load(filepath.Join(packDir, "manifest.yml"))` (strict YAML, returns `manifest.File` with no validation beyond parsing). Kept signature-minimal so callers that only need parsing (e.g. unit tests) don't fabricate paths.
- [x] add `ValidateManifest(m *manifest.File, projectRoot, packName, destRoot string) error` — matches the AI/IDE signature shape introduced in Tasks 3 and 8. The fourth param is `destRoot`, NOT `hubDir`: for git, callers pass `absHooks` (the actual write destination — `<absHub>/src/.git/hooks/`), not `absHub`. This is semantically accurate (`to` paths are computed relative to where files actually land) and works for the shared validator since git's basename-only rule trivially satisfies "contained under `destRoot`". Order:
  1. `manifest.ValidateShape(m, destRoot, "git pack "+packName)` — catches empty manifest, duplicate `to`, escaping paths (shared with AI/IDE)
  2. git-specific extras: every `to` is a basename (no path separators, no `..`); the `symlinks` block is empty (git pack does not support symlinks)
  3. `manifest.ValidateSources(m, resolveFn, "git pack "+packName)` where `resolveFn := func(rel string) (string, bool, error) { return packroot.Resolve(projectRoot, "git", packName, rel) }` — verifies each `from` exists either in `<pack>.local/` or canonical pack
  Callers (command Task 6 + validator Task 7) invoke `LoadManifest` then `ValidateManifest`. Renderer never reaches source files unless `ValidateManifest` passes.
- [x] add `RenderHooks(ctx Context) error` where `Context` carries `ProjectRoot`, `Cfg`, `Service`, `PackName`, `Manifest *manifest.File`, `HooksDir`, `Writer`. For each `RenderEntry`: (1) resolve `from` via `packroot.Resolve(projectRoot, "git", packName, from)`; (2) render with Go template + `TemplateData{Project, Service, ServiceCfg, Runtime}` (mirror AI's data shape) + `missingkey=error`; (3) **destination guard**: `os.Lstat(<hooksDir>/<to>)` — if the destination exists and is a symlink (mode `ModeSymlink`), refuse with a clear error (`hook destination is a symlink: <path>`) — matches IDE/AI source-symlink rejection but applied to the *write* path; (4) write rendered bytes via `os.WriteFile` to `<hooksDir>/<to>`; (5) **explicit `os.Chmod(<hooksDir>/<to>, 0o755)` AFTER write** — `os.WriteFile` only sets mode on file *create*, leaving an existing `0644` hook at `0644`. Apply `Chmod` unconditionally so re-renders normalize permissions.
- [x] guard `<hooksDir>` containment via the same pattern IDE/AI use (`internal/templates/ai/ai.go:418-430`, `internal/templates/ide/ide.go:419-431`): after `MkdirAll(hooksDir, 0o755)`, resolve all three via `filepath.EvalSymlinks` — `realRoot` (project root), `realGitDir` (the resolved `<svc.Dir>/src/.git` directory from `ResolveGitHooksDir`), `realHooksDir` (`<resolved-.git>/hooks`) — then call `pathsafe.EnsureRealUnder(realHooksDir, realRoot, realGitDir)`. Catches the case where `MkdirAll` followed a pre-existing `hooks -> /tmp/...` symlink in `.git/` and the actual directory escapes both the project root and the real `.git` dir. The previous one-arg `EnsureRealUnder(hooksDir, hooksDir)` was self-referential and a no-op.
- [x] **defer-in-loop safety**: per-entry render+write MUST be implemented as a separate helper function (e.g. `renderOneHook(ctx, entry)`) so any `defer f.Close()` runs at iteration boundary, not at `RenderHooks` exit. Do NOT use `defer` inside the per-entry `for` loop in `RenderHooks` itself.
- [x] write tests for `ResolveTemplatePack`: explicit pack hit (returns `packName == svc.Git.Template`); explicit pack missing (hard error); implicit service-name fallback (returns `packName == serviceName`); implicit default fallback (returns `packName == "default"`); none found (clear error). Each test asserts BOTH `packDir` and `packName` returns.
- [x] write tests for `SelectServices`: disabled service dropped, `GitRenderEnabled()=false` dropped, empty `Dir` dropped, deepest-extends wins collision, lex tiebreak
- [x] write tests for `PrepareHub`: `dir: ../outside` rejected with `ContainedRel` error; `dir: /etc/...` rejected; `services/main` as a symlink to outside-of-root rejected; valid `services/main` directory accepted
- [x] write tests for `ResolveGitHooksDir`: directory `.git` → `DirOK`, file `.git` → `DirWorktree`, missing `.git` → `DirMissing`, symlink rejection
- [x] write tests for `LoadManifest` (parse-only): valid YAML returns `*manifest.File` with entries populated; missing `manifest.yml` returns chain containing both `manifest.ErrManifestMissing` and `os.ErrNotExist`; malformed YAML returns wrapped error with file path; unknown field rejected by strict decode
- [x] write tests for `ValidateManifest` (full validation): empty manifest (no render entries) rejected by `ValidateShape`; duplicate `to` rejected by `ValidateShape`; `to` with slash rejected by git-specific check; `to` with `..` rejected by git-specific check; non-empty `symlinks` rejected by git-specific check; missing `from` file rejected by `ValidateSources`; `from` satisfied only by `<pack>.local/` override passes (resolver-aware)
- [x] write tests for `RenderHooks`: (a) golden file via fixture pack; (b) override file via `<pack>.local` shadow; (c) missingkey error path; (d) **destination symlink rejected** — pre-create `<hooksDir>/pre-commit` as a symlink to `/tmp/whatever` and assert `RenderHooks` errors with a symlink-rejection message and does NOT follow/overwrite the link; (e) **chmod-on-rewrite** — pre-create `<hooksDir>/pre-commit` as a regular file with mode `0644`, run `RenderHooks`, assert mode is now `0755` via `os.Stat`; (f) file-handle accounting test (open multiple entries, ensure all closed before function returns — protects against the defer-in-loop regression)
- [x] run `make test` — must pass before next task

### Task 6: Add `devbox render git` command

Cobra subcommand wiring.

- [x] create `internal/command/render_git.go` with `newRenderGitCmd(flags *rootFlags) *cobra.Command` modeled on `render_ai.go`: `Args: cobra.MaximumNArgs(1)`, optional service-name positional, no extra flags
- [x] flow: `config.LoadConfig` → if positional present, validate via `validateExplicitGitArg` (mirrors AI) and resolve hub-anchor with deepest-wins; else `git.SelectServices(cfg)` → for each selected service:
  1. `absHub, err := git.PrepareHub(absRoot, name, svc)` — preflight `svc.Dir` containment + symlink rejection (defined in Task 5). On error, fail fast for this service.
  2. `git.ResolveTemplatePack(svc, projectRoot, name)` — resolve pack name + abs pack dir
  3. `absHooks, status, err := git.ResolveGitHooksDir(absHub)` — branch on `DirStatus`:
     - `DirOK` → proceed to step 4
     - `DirMissing` → `Writer.Warning("service <name>: no src/.git, skipping")`, continue
     - `DirWorktree` → `Writer.Warning("service <name>: src/.git is a worktree pointer (not yet supported), skipping")`, continue
  4. `m, err := git.LoadManifest(packDir)`
  5. `err := git.ValidateManifest(m, projectRoot, packName, absHooks)` — pass `absHooks` (the actual write destination from step 3) as `destRoot`, not `absHub`. Runs shape + git-specific + source-existence checks (per Task 5). RenderHooks is NEVER called when validation fails.
  6. `git.RenderHooks(...)` with the validated manifest
- [x] register in `internal/command/env.go` (parent `render` cmd) alongside `ide` and `ai`
- [x] update `render` help text to list `git` as a subcommand
- [x] **completion**: use the existing `serviceNameCompletion(flags)` (defined in `internal/command/shell.go:184`) — same as `render_ai.go:181` and `ide.go:49`. This returns ALL service names, not filtered by `Enabled` / `GitRenderEnabled`. Consistent with AI/IDE precedent; filtered completion would be a separate cross-renderer improvement.
- [x] write tests for the cobra command: positional success; positional unknown service error; positional service with no `src/.git` warning + non-error exit; positional service with `dir: ../outside` → `PrepareHub` rejects, command errors before any `MkdirAll`; positional service with manifest missing a `from` file → `ValidateManifest` fails, `RenderHooks` is NOT entered (assert nothing was written under `src/.git/hooks/`); no positional iterates only enabled+app services; completion callback returns all service names (matches `serviceNameCompletion` behavior — do NOT assert filtering by `Enabled`/`GitRenderEnabled`)
- [x] run `make test` — must pass before next task

### Task 7: Add validator for git packs

Static validation surfaces broken packs without running `render git`.

- [ ] create `internal/validate/templates/git.go` mirroring the updated `internal/validate/templates/ai.go` (post-Task-3). The validator's contract is "surface broken packs without running `render git`" — so it MUST validate the pack regardless of whether `src/.git` exists. The flow:
  - `PrepareHub` (containment check on `svc.Dir` — diagnostic if escaping)
  - `ResolveTemplatePack` → packDir, packName
  - **DO NOT call `ResolveGitHooksDir` as a gate.** Validators run on CI / fresh fixture projects that have no nested `src/.git` yet. Skipping validation when `.git` is missing would let broken packs slip through.
  - Synthesize `destRoot := filepath.Join(absHub, "src", ".git", "hooks")` purely for `ValidateManifest`'s containment math — git's basename-only `to` rule means the directory doesn't need to exist for shape validation to be meaningful.
  - `git.LoadManifest(packDir)` → `git.ValidateManifest(m, projectRoot, packName, destRoot)`. Validator does NOT duplicate basename/symlink checks — those live in `git.ValidateManifest`.
  - **Optional info diagnostic**: call `git.ResolveGitHooksDir(absHub)` AFTER validation, purely as an info-severity probe. `DirMissing` → info "no src/.git, render will be skipped"; `DirWorktree` → info "worktree pointer, render not yet supported". These are advisory — they do NOT gate validation.
  - Diagnostics report manifest-load errors and validate-errors as separate rows.
- [ ] write tests: pack with invalid `to: foo/bar` → validator emits error diagnostic EVEN when `src/.git` is missing (regression for the skip-on-missing-git bug); pack with valid manifest + missing `src/.git` → no error, optional info diagnostic for the missing dir
- [ ] register the validator in the templates `All()` set
- [ ] write tests for diagnostics: missing pack, missing manifest, invalid `to`, missing `from` file, shadow-override resolves the missing-file diagnostic
- [ ] run `make test` — must pass before next task

### Task 8: Convert IDE pack to manifest format (hard cut)

This is the breaking change. Sequence carefully.

- [ ] update `internal/templates/ide/ide.go`: drop `WalkPack`/`PackEntry` (or keep types but remove the walker); add `LoadManifest(packDir)` using the shared schema; require `manifest.yml` to exist — missing manifest returns a wrapped error `ErrManifestMissing` with hint: "IDE packs now require a manifest.yml; see docs/reference/render/ide.md for the migration"
- [ ] **API change — `ide.ResolveTemplatePack`**: change signature to return `(packDir, packName string, err error)` — same shape as the AI change in Task 3. Update ALL in-tree callers in this same task: `internal/command/ide.go:207` AND `internal/validate/templates/ide.go:103`.
- [ ] add `ide.ValidateManifest(m *Manifest, projectRoot, packName, destRoot string) error` (same signature shape as the new `ai.ValidateManifest` from Task 3 — `destRoot` is the hub directory for IDE, matching AI's usage; the param is named generically so the same shape works for git). Wrapper calls `manifest.ValidateShape` then `manifest.ValidateSources` with a packroot-backed resolver (`packroot.Resolve(projectRoot, "ide", packName, rel)`). No symlink restriction — IDE may want symlinks, align with AI. Resolver-aware existence ensures `<pack>.local` overrides satisfy `from` checks identically to AI. Update ALL IDE callers in this task — at minimum `internal/command/ide.go` AND `internal/validate/templates/ide.go`. Repo must compile at the end of Task 8.
- [ ] update IDE renderer to read every `from` via `packroot.Resolve(projectRoot, "ide", packName, from)`; emit one-line info on override hit (same as AI)
- [ ] keep deepest-extends-wins collision policy unchanged
- [ ] update `internal/command/ide.go` to use the manifest flow; ensure error message on missing manifest is friendly
- [ ] update `internal/validate/templates/ide.go` to require manifest and validate via shared rules
- [ ] update IDE renderer tests: convert existing walk-based fixtures by adding `manifest.yml` to each `testdata/` pack
- [ ] write a new test asserting "missing manifest" produces `ErrManifestMissing` with the migration hint
- [ ] run `make test` — must pass before next task

### Task 9: Surface override-source in validators

Validators should know if a `from` is being satisfied by the shadow tree, but must not be noisy. **Validators MUST call the kind-level `ValidateManifest` (not `manifest.ValidateShape`/`ValidateSources` directly)** — otherwise git-specific basename/no-symlinks checks (and any future per-kind rules) would be bypassed. This Task only adds an aggregation channel for `fromOverride` hits without changing the validation pipeline.

- [ ] each kind's validator (`internal/validate/templates/{ai,ide,git}.go`) continues to call `<kind>.ValidateManifest(m, projectRoot, packName, destRoot)` (already wired in Tasks 3, 5, 7, 8 — AI/IDE pass hub dir as `destRoot`, git passes hooks dir). Do NOT bypass it.
- [ ] **resolver wrapper for aggregation**: `manifest.ValidateSources` already takes a resolver `func(rel) (path string, fromOverride bool, err error)`. Add an optional aggregation hook so callers can collect `fromOverride=true` events. Concretely: add `manifest.ValidateSourcesWith(m, resolve, sink func(rel string, fromOverride bool), label)` — the existing `ValidateSources` becomes a thin wrapper that passes a `nil` sink. Kind-level `ValidateManifest` accepts an optional sink param (variadic or via context) so validators can install one.
- [ ] each validator installs a sink that appends overridden `rel` paths to a per-pack list. After validation finishes, if the list is non-empty, emit **one** info-severity diagnostic per kind+pack: `target=<pack-name>`, message `"using N local overrides: <basename1>, <basename2>, ..."` (cap the listed basenames at 5 with `...`-truncation to keep `RenderDiagnosticsTable` cell width predictable).
- [ ] write validator tests: zero overrides → no info diagnostic; one override → one info diagnostic with that basename; >5 overrides → truncated listing; **git basename check still runs**: invalid `to: foo/bar` in a manifest validated by the git validator still emits an error diagnostic (proves we did NOT bypass kind-level `ValidateManifest`)
- [ ] run `make test` — must pass before next task

### Task 10: Reject identifier-unsafe template pack names

Belt-and-braces: pack names go on disk and into error messages. Applies to BOTH explicit `*.template` values AND implicit service-name candidates — service names are validated elsewhere by a permissive rule that accepts `"."` and leading-dot names, neither of which is safe as a filesystem path component.

- [ ] in `manifest` package (or a new tiny `internal/templates/packname` helper), add `ValidatePackName(name string) error` enforcing `^[A-Za-z0-9][A-Za-z0-9_-]*$` (no leading dot or hyphen, no traversal, must match what `ResolveTemplatePack` writes to disk)
- [ ] **explicit `*.template`**: each `ResolveTemplatePack` (AI, IDE, git) calls `ValidatePackName` on `svc.{IDE,AI,Git}.Template` when non-empty. Failure → hard error.
- [ ] **implicit service-name candidate**: each `ResolveTemplatePack` calls `ValidatePackName(serviceName)` before trying `<templates-root>/<kind>/<serviceName>/` as the second candidate in the chain. If the service name is not a valid pack name, **silently skip** the service-name candidate (do NOT error) and fall through to `<templates-root>/<kind>/default/`. Rationale: a service named `.api` should not crash `devbox render git`; it just can't have an implicit pack of its own name.
- [ ] write tests: `default`, `my-pack`, `pack_2` accepted; `../etc`, `pack/sub`, empty string, leading dot, leading hyphen rejected
- [ ] write tests for implicit-candidate skip: service named `.api` with no explicit template and no `<templates-root>/<kind>/default/` errors with "no pack found" (NOT a pack-name validation error); service named `.api` with `<templates-root>/<kind>/default/` present renders against `default`
- [ ] write tests for explicit-rejection: `svc.AI.Template = ".."` → hard error before any FS lookup
- [ ] run `make test` — must pass before next task

### Task 11: Verify completion callbacks survive broken packs

Cobra completions must not error or panic. Note: `serviceNameCompletion` (used by all three render subcommands) only loads `config.LoadConfig` — it does NOT load manifests or resolve packs. A malformed manifest therefore does NOT affect completion output; it only affects the actual render command. So the relevant test surface is config-level malformation, not manifest-level.

- [ ] for `devbox render git`, follow the existing pattern (`completionConfigPath` → empty completions on `ErrNotFound`, schema errors, bad `-c` path)
- [ ] ensure the IDE completion path still works after the manifest cut — the test must NOT rely on manifest state, only on config loading
- [ ] add a test where `__complete` is invoked on a project with a **broken `devbox.yml`** (e.g. bad schema_version) — `serviceNameCompletion` returns empty, no panic, no error
- [ ] add a test where `__complete` is invoked on a project with a **malformed `manifest.yml`** but a healthy config — `serviceNameCompletion` returns all service names normally (this documents the boundary: completion is config-scoped, not manifest-scoped)
- [ ] run `make test` — must pass before next task

### Task 12: Update render reference docs

`docs/reference/render/` already exists (`index.md`, `ide.md`, `ai.md`, `env.md`). Extend it, do NOT create a parallel page under `docs/reference/config/`.

- [ ] create `docs/reference/render/git.md` modeled on the existing `ide.md` / `ai.md` shape: overview, manifest schema (link to shared section in `index.md`), per-service config (`git.enabled`, `git.template`, default app-type policy, `extends` inheritance), output location (`<svc.Dir>/src/.git/hooks/`), worktree (`.git` as file) currently unsupported with warning, example pack and manifest distilled from the legacy hooks
- [ ] include an example git pack manifest plus example `.tmpl` snippets distilled from the legacy hooks (PROJECT_PREFIX moved to a per-service config field referenced via `{{ .ServiceCfg.X }}`, or kept as `.env.git` reading inside the hook — document the trade-off without mandating one)
- [ ] update `docs/reference/render/index.md`:
  - add `devbox render git` to the Subcommands table (Output: `<svc.Dir>/src/.git/hooks/<basename>`; Source: `devbox/templates/git/<pack>/` driven by `manifest.yml`)
  - add `git` to the mermaid flow diagram
  - add a new section **"Local overrides"** documenting the sibling `<pack>.local/` mechanism: how `packroot.Resolve` falls back from override to canonical pack, that the override pack only needs to contain the files being overridden (not the full pack), that `manifest.yml` is read only from the canonical pack (override cannot change the manifest), and a `.gitignore` recommendation (`devbox/templates/*/*.local/` or broader `*.local/`)
  - add a new subsection **"Input vs output"** documenting: the override **input** at `devbox/templates/<kind>/<pack>.local/<rel>` is gitignored by the `.local/` pattern and never committed; the *rendered output* still lands at the manifest-declared `to`. For `git`, output is inside `.git/hooks/` and is never tracked. For `ide`/`ai`, the output path is typically tracked — re-rendering from a local override modifies the tracked artifact, and the developer is responsible for not committing those changes (`git stash`, `git checkout -- <path>`, or a personal pre-commit guard).
  - link to the existing `devbox/local.yml` / `devbox/docker.local.yml` precedent in `services.md` so readers see the consistent pattern
- [ ] update `docs/reference/render/ide.md`: note the now-mandatory `manifest.yml`, link to the shared schema description in `index.md`, mention `<pack>.local/` override
- [ ] update `docs/reference/render/ai.md`: mention `<pack>.local/` override (manifest already required, no schema change)
- [ ] regenerate CLI docs (`devbox docs generate --scope cli` or whatever Makefile target wires this) so the auto-generated `devbox render git` page reflects the new command — verify the regenerated output lands in the expected location and is included in the diff
- [ ] no test step (docs-only task), but verify markdown renders by grep'ing for broken relative links and previewing the mermaid flow
- [ ] run `make test` — must pass (no Go changes but ensures we didn't regress)

### Task 13: Update services.md and devbox.md docs

Point users to the new `git.*` block.

- [ ] in `docs/reference/config/services.md`, add the `git: { enabled, template }` block to the service schema reference (alongside `ide:` and `ai:`); cross-link to `docs/reference/render/git.md`; note `extends` inheritance behavior matches `ide`/`ai`
- [ ] in `docs/reference/config/devbox.md`, mention `git` in the list of render targets if such a list exists; otherwise add a one-line note pointing to `docs/reference/render/git.md`
- [ ] verify any other doc that lists render kinds (search for "render ide" / "render ai" in docs/) is updated
- [ ] no test step
- [ ] run `make test` — must pass

### Task 14: Update CLAUDE.md / AGENTS.md

Keep the agent context current.

- [ ] in `AGENTS.md` (which `CLAUDE.md` symlinks to), update the `internal/templates/` bullet to list `git/` package and the shared `manifest/` and `packroot/` packages; note IDE has migrated to manifest; document the sibling `devbox/templates/<kind>/<pack>.local/` override convention (parallel to the existing `devbox/local.yml` / `devbox/docker.local.yml` pattern)
- [ ] add `internal/templates/git/` and the new validator to the relevant entries
- [ ] update the `internal/config/` bullet to document `ServiceGitHooksConfig` and the `GitRenderEnabled*` accessors
- [ ] no test step
- [ ] run `make test` — must pass

### Task 15: Final acceptance verification

- [ ] verify all requirements from Overview are implemented: `devbox render git` runs, override works for all three kinds, IDE migrated to manifest with hard-cut error
- [ ] verify edge cases: missing `src/.git`, worktree `.git` file, malformed manifest, missing pack, override file with override metadata
- [ ] run full test suite (`make test`)
- [ ] run linter (`make lint`) — all issues fixed
- [ ] verify code coverage of new packages with `go test ./internal/templates/... -cover` — aim ≥ 80% (matches surrounding code)
- [ ] verify `devbox validate templates` runs against a fixture project and reports clean

## Technical Details

**Shared manifest schema** (`internal/templates/manifest`):
```go
type File struct {
    Render   []RenderEntry  `yaml:"render"`
    Symlinks []SymlinkEntry `yaml:"symlinks"`
}
type RenderEntry struct {
    From string `yaml:"from"`
    To   string `yaml:"to"`
}
type SymlinkEntry struct {
    Link string `yaml:"link"`
    To   string `yaml:"to"`
}

var ErrManifestMissing = errors.New("manifest file not found")

func Load(path string) (*File, error) {
    f, err := os.Open(path)
    if err != nil {
        if errors.Is(err, os.ErrNotExist) {
            // Multi-%w preserves BOTH sentinels in the chain (Go 1.20+).
            return nil, fmt.Errorf("loading %s: %w: %w", path, ErrManifestMissing, err)
        }
        return nil, fmt.Errorf("loading %s: %w", path, err)
    }
    // ... strict YAML decode (KnownFields(true)) ...
}

// Pure schema validation. No FS access. Same for all kinds.
// destRoot is the directory that `to` paths must be contained under:
// hub dir for AI/IDE, hooks dir for git. Named generically so all three kinds
// share the same signature.
func ValidateShape(m *File, destRoot, label string) error { /* ... */ }

// Resolver-aware source existence. The caller supplies the resolver so
// `<pack>.local/<rel>` overrides satisfy `from` existence checks identically
// to how the renderer reads them. No path joining inside.
func ValidateSources(m *File, resolve func(rel string) (path string, fromOverride bool, err error), label string) error { /* ... */ }
```
The primary type is `manifest.File` (not `manifest.Manifest`) — avoids stutter at call sites. Strict decode via `yaml.Decoder.KnownFields(true)`. Per-kind constraints (git: no symlinks; git: `to` is basename-only) live in each kind's `LoadManifest`/`Validate` wrapper, not in the shared schema.

The validation split is load-bearing: shape and source-existence are decoupled so the renderer's override behavior and the validator's existence check stay in sync. Without it, a manifest where `from` lives only in `<pack>.local/` would render successfully but fail validation — or vice versa — because shape-validation would touch disk under the canonical pack root.

**Universal override** (`internal/templates/packroot`):
```go
func Resolve(projectRoot, kind, packName, rel string) (path string, fromOverride bool, err error)
```
Checks `<projectRoot>/devbox/templates/<kind>/<packName>.local/<rel>` first (sibling shadow pack, gitignored by `*.local/` pattern), then `<projectRoot>/devbox/templates/<kind>/<packName>/<rel>` (tracked canonical pack). Uses `pathsafe` on both candidates. Returns wrapped `os.ErrNotExist` when neither hit.

This mirrors the existing user-local override convention in the project:
- `devbox/local.yml` shadows `devbox/devbox.yml` defaults
- `devbox/docker.local.yml` shadows `devbox/docker.yml`
- `devbox/templates/<kind>/<pack>.local/` shadows `devbox/templates/<kind>/<pack>/` (new)

`.devbox/` remains strictly for devbox-managed runtime artifacts (`state.yml`, `deploy.lock`, `logs/`) and is never used for user-authored overrides.

**Git pack on-disk layout** (project example):
```
devbox/templates/git/default/
├── manifest.yml
├── pre-commit.tmpl
├── prepare-commit-msg.tmpl
├── commit-msg.tmpl
└── pre-push.tmpl
```
```yaml
# manifest.yml
render:
  - { from: pre-commit.tmpl,         to: pre-commit }
  - { from: prepare-commit-msg.tmpl, to: prepare-commit-msg }
  - { from: commit-msg.tmpl,         to: commit-msg }
  - { from: pre-push.tmpl,           to: pre-push }
```

**Per-service config**:
```yaml
services:
  main:
    type: app
    dir: services/main
    git:
      enabled: true        # nil + type:app → enabled; nil + other type → disabled
      template: default    # optional; defaults via service-name → default chain
```

**Render destination**: `<absRoot>/<svc.Dir>/src/.git/hooks/<basename>`, mode `0755`. If `<svc.Dir>/src/.git` is a file (worktree/submodule pointer), the service is skipped with a `Warning` in this iteration — full worktree support is deferred (Post-Completion).

**Template data** (parity with AI — see `internal/templates/ai/ai.go:358`):
```go
type TemplateData struct {
    Project    config.ProjectConfig   // matches existing renderers
    Service    string                 // service name
    ServiceCfg config.ServiceConfig
    Runtime    config.RuntimeConfig
}
```

**Processing flow** (per service, `render git`):
1. `SelectServices(cfg)` (or validate explicit positional via `validateExplicitGitArg` + deepest-wins hub anchor)
2. `git.PrepareHub(absRoot, name, svc)` — preflight `svc.Dir` containment (`pathsafe.ContainedRel`) + no-symlinks (`pathsafe.CheckNoSymlinks`); rejects `dir: ../outside` BEFORE any `MkdirAll`
3. `git.ResolveTemplatePack(svc, projectRoot, name)` — chain: explicit `git.template` → `<serviceName>` (silent-skip if not a valid pack name) → `default`; returns `packName` + absolute pack dir (used for `manifest.yml` read only — per-file reads go through `packroot.Resolve`)
4. `git.ResolveGitHooksDir(absHub)` → `DirStatus`: `DirOK` proceed; `DirMissing`/`DirWorktree` → `Writer.Warning` and skip service
5. `git.LoadManifest(packDir)` — parse-only, returns `*manifest.File`
6. `git.ValidateManifest(m, projectRoot, packName, absHooks)` — `absHooks` is passed as `destRoot` (the actual write destination, e.g. `<absHub>/src/.git/hooks/`); shape (shared) + git-specific (basename `to`, empty `symlinks`) + sources via packroot resolver
7. `git.RenderHooks(...)` — for each `RenderEntry`: `packroot.Resolve(projectRoot, "git", packName, from)` → render → `Lstat`-reject existing symlink at destination → `os.WriteFile` → explicit `os.Chmod(0o755)`; `pathsafe.EnsureRealUnder(realHooksDir, realRoot, realGitDir)` containment guard after `MkdirAll`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only.*

**Required project-side migration after upgrade**:
- Every existing IDE pack under `devbox/templates/ide/*/` must gain a `manifest.yml` listing each `.tmpl` to render. `devbox validate templates ide` will report which packs are missing the file. We can ship a one-shot helper later (`devbox render ide --migrate-manifest`) but that is out of scope here.

**Deferred features (follow-up plans)**:
- **Git worktree / submodule support**: this plan skips services where `src/.git` is a file (not a directory). A follow-up should resolve `gitdir: <path>` pointers (with size cap on read, relative-path resolution against the `.git` file's directory, symlink check on the resolved target, and reasonable-root containment).

**Manual verification** (recommended on consuming projects):
- Run `devbox render ide` and `devbox render ai` against a real project — confirm output is byte-identical to pre-change baseline (with a manifest added for IDE).
- Run `devbox render git` against a service with a real git checkout — confirm hooks land in `src/.git/hooks/`, are executable, and fire on commit.
- Drop a file at `devbox/templates/ai/default.local/AGENTS.md.tmpl` in a project (ensure `*.local/` is gitignored), then re-run `devbox render ai` — confirm the override file is used and an info-line is printed. Verify `git status` shows the override pack as untracked-and-ignored, not staged.

**External docs**:
- If there is a top-level Devbox docs site (outside this repo), add a release-notes entry for the IDE manifest migration.
