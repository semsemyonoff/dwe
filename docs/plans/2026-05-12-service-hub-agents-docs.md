# Service hub agentic docs (AGENTS.md / CLAUDE.md)

## Overview

Add generation of orienting agentic-docs (`AGENTS.md` + symlink `CLAUDE.md → AGENTS.md`) at the **service hub** level (`services/<name>/`), so an AI agent attached to that workspace (e.g. via devcontainer) understands it sits inside a devbox-managed hub — not the application repository — and that the source lives under `src/`.

**Problem**: When an agent works inside `services/<name>/`, the surrounding `configs/`, `home/`, `runtime/`, `logs/`, `.devcontainer/` artifacts look ambiguous. Without a hub-level hint the agent either ignores them or tries to "fix" them.

**Solution**: A new CLI subcommand `devbox render ai [service]` (alongside the existing `devbox render env` / `devbox render ide`) renders a template pack from `devbox/templates/agents/<template>/` (project-local; not bundled in the CLI) into the service hub directory. The pack produces an `AGENTS.md` plus a `CLAUDE.md → AGENTS.md` symlink. Resolves via the same explicit/implicit chain as IDE template packs (explicit `services.<name>.ai_docs.template` is strict; implicit chain: service-name → default). Re-runnable: a user can regenerate any time with `devbox render ai`.

**Integration**: New `ai_docs:` block on `ServiceConfig` (defaults to enabled), with inheritance via `extends` mirroring the existing `ide` block. New `devbox render ai` subcommand registered in `internal/command/env.go` next to `newRenderIDECmd`. Schema documented in `docs/reference/config/services.md`; Cobra-generated CLI reference lands at `docs/reference/cli/devbox_render_ai.md` (plus an updated `docs/reference/cli/devbox_render.md`) via `bin/devbox docs generate --scope cli`.

## Context (from discovery)

- **Closest analogue**: `internal/command/ide.go` — `newRenderIDECmd`, `resolveIDETemplatePack`, `walkIDEPack`, `renderIDETemplateFile`, `checkNoSymlinks`, `validateIDETemplateKey` / `validateServiceNameAsPackKey`. New work follows the same patterns and safety contract.
- **Render parent command**: `internal/command/env.go:12` (`newRenderCmd`) registers `env` and `ide` subcommands. Adding `ai` is a one-line `cmd.AddCommand(newRenderAICmd(flags))`.
- **Template engine**: Go `text/template` (`{{ .Service }}`, `{{ .Project.Name }}`) like the IDE renderer — NOT `${...}` pipeline syntax. Symmetric with IDE; no `service-self` alias needed in merged config.
- **Service config**: `internal/config/devbox.go:295-330` defines `ServiceConfig` and `ServiceIDEConfig` (with `Enabled *bool` tristate). New `ServiceAIDocsConfig` mirrors that shape.
- **Service inheritance**: `LoadServicesConfig` resolves `extends` chains and inherits unset fields from parents — see the IDE-block inheritance at `internal/config/devbox.go:645-652`. `ai_docs` must follow the same rule (child wins when set; parent fills the gap).
- **Template pack location**: Project-local `devbox/templates/agents/<template>/` — CLI does not bundle defaults; same as IDE packs.
- **Hub dir**: `filepath.Join(projectRoot, svc.Dir)` — `svc.Dir` is project-relative (e.g. `services/api`). Application code lives at `<hub>/src/`. `AGENTS.md` goes at the hub root.
- **Path-safety helpers to extract**: `checkNoSymlinks`, contained-rel checks, and the double-rooted `EvalSymlinks` check (`internal/command/ide.go:622` and `:625` — both project root **and** service dir are enforced as boundaries) will move to `internal/pathsafe` and be reused.

## Development Approach

- **Testing approach**: Regular (code first, then tests) — table-driven, fixtures in `testdata/`.
- Complete each task fully before moving to the next.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task.
- **CRITICAL: `make test` must pass before starting the next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run `make lint` after structural changes; fix all issues before next task.
- Maintain backward compatibility (no breaking changes to existing service YAML; new field is optional).

## Testing Strategy

- **Unit tests**: required for every task. Cover happy path + error/edge cases.
- Use `testdata/` directories with fixture template packs and fixture YAML.
- For command tests, mirror `internal/command/ide_test.go` style (write fixture pack to temp dir, run renderer against temp project root, assert files + symlink + idempotency).
- No e2e tests in this repo (CLI only).

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update plan if implementation deviates from scope.

## What Goes Where

- **Implementation Steps** (`[ ]`): code, tests, doc updates in this repo.
- **Post-Completion**: anything requiring action outside the cli repo (creating actual template packs in user projects, running the new command in a real project).

## Decisions & Constraints (locked before implementation)

These resolve ambiguities flagged during planning. Tests and docs must match these rules.

1. **Manifest `to` / `link` paths** — **nested relative paths are allowed** (e.g. `.claude/CLAUDE.md`, `docs/agents/AGENTS.md`). Rejection rules:
   - must be a non-empty relative path (no absolute, no leading `/`)
   - cleaned form must not equal `.` or `..` and must not start with `../`
   - the resolved absolute path must be strictly inside the hub dir (containment check via `pathsafe.ContainedRel`)
   - per-segment leading-dot is allowed (so `.claude/...` is fine); the path itself just must not escape
2. **Symlink target validation** — **a symlink `to` must reference one of the manifest's `render to` outputs**. No "or exists on disk" allowance. Rationale: keeps the manifest self-describing and the resolver simple; pre-existing files are not a real use case (we own the hub). Validated at manifest-load time (no runtime check needed).
3. **Service `ai_docs` inheritance** — when a child service `extends` a parent, unset fields inherit from the parent (same rule as `ide`):
   - `Enabled` (nil → inherit parent's pointer value)
   - `Template` (empty → inherit parent's string)
   Inheritance happens in `LoadServicesConfig` in topological order (parents already resolved when child is processed). Must be unit-tested.
4. **`pathsafe.EnsureRealUnder`** — preserves both boundaries explicitly. Signature: `EnsureRealUnder(realDir string, roots ...string) error`. **All arguments must be already symlink-resolved** by the caller (via `filepath.EvalSymlinks`) — the helper is pure prefix math. Semantics: `realDir` must equal or be inside every root, i.e. `strings.HasPrefix(realDir+sep, root+sep)` must hold for each root (`HasPrefix` is true when `realDir == root`, which is intentional and required — a file rendered at the hub root produces `destDir == absHubDir`). Matches the existing IDE checks at `internal/command/ide.go:622` and `:625`, which use the same `HasPrefix` form and therefore also allow equality.

   **Where the helpers differ**: `pathsafe.ContainedRel` stays **strict** (returns an error when `rel == "."` or `rel == ".."`) because it is used for **file paths** — a file can never legitimately equal its containing directory. `pathsafe.EnsureRealUnder` is used for **directory boundaries** (parent of a destination file vs. hub/project root) and therefore allows equality.
5. **Existing non-symlink at a managed link path** — **fail with a clear error**; do not silently overwrite. Error message: `"refuse to overwrite non-symlink file at <path>; remove it or disable via ai_docs.enabled: false"`. Rationale: protects a user who hand-authored `CLAUDE.md` before adopting this feature; opt-out is one yaml line away. Idempotent symlink-replacement (replace symlink whose target differs) is still allowed.
6. **Default policy** — `ai_docs.enabled` defaults to `true` for any service type (in contrast to `ide` which defaults true only for `type: app`). Opt-out is explicit `enabled: false`.

## Implementation Steps

### Task 1: Extract path-safety helpers into `internal/pathsafe`

Move shared filesystem-guard helpers out of `internal/command/ide.go` to a small standalone package, so the new render command can reuse them without importing `internal/command`.

- [x] create `internal/pathsafe/pathsafe.go` (package `pathsafe`)
- [x] move `checkNoSymlinks(absRoot, absDir, label string) error` → `pathsafe.CheckNoSymlinks`
- [x] add `pathsafe.ContainedRel(absRoot, absChild string) (rel string, err error)` — returns cleaned relative path; rejects `.`, `..`, absolute, and `..\`-prefixed results (captures the IDE renderer's containment pattern)
- [x] add `pathsafe.EnsureRealUnder(realDir string, roots ...string) error` — pure prefix math, **no `EvalSymlinks` inside** (caller passes already-resolved paths for both `realDir` and each `root`). Passes when `strings.HasPrefix(realDir+sep, root+sep)` holds for **every** root (note: `HasPrefix` is `true` when `realDir == root` — equality is intentionally allowed; see locked decision §4). Mirrors the existing IDE checks at `internal/command/ide.go:622`/`:625`.
- [x] update `internal/command/ide.go` to delegate to `pathsafe` — replace local `checkNoSymlinks` body, and replace the two `strings.HasPrefix(realDir+sep, ...)` checks at lines 622/625 with a single call passing both roots
- [x] write `internal/pathsafe/pathsafe_test.go` — table-driven cases:
  - `CheckNoSymlinks`: symlink in middle of path, symlink at leaf, non-existent component (must pass), absolute input
  - `ContainedRel` (strict): escaping rel rejected, `rel == "."` rejected, `rel == ".."` rejected, normal nested rel accepted
  - `EnsureRealUnder`: single-root pass, dual-root pass, dual-root fail (under one but not the other), **`realDir == root` must PASS** (equality allowed; this is the AGENTS.md-at-hub-root case), `realDir` slightly outside (`/a/bX` vs root `/a/b`) must fail
  - all helpers tested with `EvalSymlinks`'d fixture paths so the macOS `/private/var` tempdir case is covered
- [x] re-run `internal/command/ide_test.go` — must pass without changes to behavior
- [x] run `make test && make lint` — must pass before next task

### Task 2: Add `ai_docs` field to `ServiceConfig` with inheritance

Add schema + accessor + extends-inheritance, mirroring the existing `ide` block exactly.

- [x] add `ServiceAIDocsConfig{ Enabled *bool, Template string }` in `internal/config/devbox.go` next to `ServiceIDEConfig`
- [x] add `AIDocs ServiceAIDocsConfig` field on `ServiceConfig` with `yaml:"ai_docs"`
- [x] add `(s ServiceConfig) AIDocsRenderEnabled() bool` — defaults to `true` for any service type when `Enabled` is nil
- [x] add `(s ServiceConfig) AIDocsRenderEnabledExplicit() (enabled, explicit bool)` mirroring `IDERenderEnabledExplicit`
- [x] extend the inheritance block in `LoadServicesConfig` (around `internal/config/devbox.go:645`):
  ```go
  if svc.AIDocs.Enabled == nil && parent.AIDocs.Enabled != nil {
      v := *parent.AIDocs.Enabled
      svc.AIDocs.Enabled = &v
  }
  if svc.AIDocs.Template == "" {
      svc.AIDocs.Template = parent.AIDocs.Template
  }
  ```
- [x] write tests in `internal/config/devbox_test.go`:
  - default: `Enabled` nil → `AIDocsRenderEnabled()` true
  - explicit `enabled: false` → false, explicit=true
  - explicit `enabled: true` → true, explicit=true
  - template round-trip
  - **inheritance**: child with empty `ai_docs` inherits parent's `Enabled` pointer + `Template` string
  - **inheritance override**: child with explicit `enabled: false` wins over parent's `enabled: true`
  - **inheritance via multi-hop**: grandchild → child → parent template propagation (topo-sort already gives parent-first order)
- [x] run `make test && make lint` — must pass before next task

### Task 3: Implement agents template-pack resolver

Mirror `resolveIDETemplatePack` for agents: explicit-strict + implicit chain (service-name → default), rooted at `devbox/templates/agents/`.

- [x] create `internal/command/render_ai.go` skeleton (package `command`)
- [x] add `resolveAgentsTemplatePack(svc config.ServiceConfig, projectRoot, serviceName string) (string, error)` — same shape as `resolveIDETemplatePack`, rooted at `devbox/templates/agents/`
- [x] reuse `validateIDETemplateKey` and `validateServiceNameAsPackKey` directly (they live in the same `command` package — no extraction needed)
- [x] write tests in `internal/command/render_ai_test.go`: explicit pack found, explicit missing (hard error, no fallback), implicit service-name found, implicit fallback to default, both candidates missing (error), symlinked pack rejected, non-dir pack rejected
- [x] run `make test && make lint` — must pass before next task

### Task 4: Define and parse `manifest.yml`

Manifest schema declares what to render and what to symlink. Strict YAML decode per repo convention for user-edited config.

- [x] in `internal/command/render_ai.go` define:
  - `agentsManifest{ Render []agentsRenderEntry, Symlinks []agentsSymlinkEntry }`
  - `agentsRenderEntry{ From, To string }` (yaml: `from`, `to`)
  - `agentsSymlinkEntry{ Link, To string }` (yaml: `link`, `to`)
- [x] add `loadAgentsManifest(packDir string) (*agentsManifest, error)` — open `<packDir>/manifest.yml`, decode with `yaml.Decoder.KnownFields(true)`. Manifest missing is a hard error.
- [x] add `validateAgentsManifest(m *agentsManifest, packDir string) error`:
  - reject **both** lists empty (no-op manifest is almost certainly a mistake)
  - for each `render`:
    - `from`: must be relative, end in `.tmpl`, must not escape `packDir`
    - resolve `absSource = filepath.Join(absPackDir, from)`; run `pathsafe.CheckNoSymlinks(absPackDir, filepath.Dir(absSource), "agents template pack")` — **rejects symlinked parent directories** inside the pack (e.g. `default/evil → /tmp/outside` with `from: evil/AGENTS.md.tmpl`; `os.Lstat(absSource)` alone follows the parent symlink and sees a regular file, so the parent-component walk is mandatory)
    - `os.Lstat(absSource)`: must exist as a regular file (reject directory, device file, etc.); must not be a symlink itself
    - `to`: must be a non-empty relative path; cleaned must not be `.`/`..` and must not start with `../`; nested paths are allowed
  - for each `symlink`:
    - `link`: same rules as render `to`
    - `to`: must match one of the manifest's `render to` paths exactly (cleaned-form comparison). **No "exists on disk" fallback** (per locked decision §2).
  - reject duplicate `render to` paths (two renders writing the same destination)
  - reject duplicate `symlink link` paths
- [x] write tests in `internal/command/render_ai_test.go` with fixture packs in `internal/command/testdata/agents/`:
  - valid manifest (render + symlinks)
  - unknown YAML field rejected
  - `from` escaping pack dir
  - `from` not ending in `.tmpl`
  - `from` is a symlink itself (rejected — leaf check)
  - **`from` has a symlinked parent directory** (e.g. `evil/AGENTS.md.tmpl` where `evil` is a symlink pointing outside the pack) — must be rejected by the `CheckNoSymlinks` walk
  - `from` resolves to a directory (rejected — must be regular file)
  - `to` escaping hub
  - symlink `to` not matching any render `to`
  - empty manifest rejected
  - duplicate render destinations rejected
  - duplicate symlink links rejected
- [x] run `make test && make lint` — must pass before next task

### Task 5: Implement render + symlink writers

Runtime path: render each `from` → `to`, then create relative symlinks. Render is idempotent (overwrite); symlinks are idempotent with the "no overwrite of regular file" guard (per locked decision §5).

- [x] add `agentsTemplateData{ Project config.ProjectConfig, Service string, ServiceCfg config.ServiceConfig, Runtime config.RuntimeConfig }`
- [x] add `renderAgentsTemplateFile(sourcePath string, data agentsTemplateData, dest, absHubDir, absRoot string) error`:
  - parse with `text/template`, `Option("missingkey=error")`
  - resolve `absDest`, run `pathsafe.ContainedRel(absHubDir, absDest)` — error if outside
  - run `pathsafe.CheckNoSymlinks(absRoot, filepath.Dir(absDest), "destination dir")` before `MkdirAll`
  - `MkdirAll` parent
  - `EvalSymlinks` parent dir, then `pathsafe.EnsureRealUnder(realDir, realRoot, realHub)` — both boundaries
  - refuse to write through a symlinked destination file (`os.Lstat` + `ModeSymlink` check)
  - `os.WriteFile` with `0o644`
- [x] add `ensureRelativeSymlink(linkPath, targetWithinHub, absHubDir, absRoot string) (changed bool, err error)`:
  - resolve `absLink`, run `pathsafe.ContainedRel(absHubDir, absLink)`
  - resolve `absTarget = filepath.Join(absHubDir, targetWithinHub)`, run `pathsafe.ContainedRel(absHubDir, absTarget)`
  - `MkdirAll` parent of `absLink` (with `CheckNoSymlinks` first)
  - compute `relTarget := filepath.Rel(filepath.Dir(absLink), absTarget)`
  - inspect `os.Lstat(absLink)`:
    - not exists → `os.Symlink(relTarget, absLink)` → return `(true, nil)`
    - is a symlink → if `os.Readlink` returns `relTarget` then return `(false, nil)`; else `os.Remove` + `os.Symlink` → return `(true, nil)`
    - is a regular file or dir → return `(false, error)` with the locked error message (§5)
  - the `changed` flag is consumed by `renderAgentsForService` (Task 6) to suppress the success line on no-op
- [x] add `renderAgentsForService(projectRoot, name string, svc config.ServiceConfig, cfg *config.DevboxConfig, w *render.Writer) error` — helper called from Task 6's cobra command
- [x] write tests in `internal/command/render_ai_test.go`:
  - fresh render writes files + symlink
  - re-run is idempotent (symlink still points to right target; no error)
  - symlink replaced when target changes between runs
  - **regular file at link path → error** (no overwrite)
  - render `to` escaping hub → error
  - symlink `link` escaping hub → error
  - symlink target outside hub → error (caught by render-output validation in Task 4, but defense-in-depth here too)
  - destination path with pre-existing symlinked component (e.g. `services/api/.claude` is a symlink) → error
- [x] run `make test && make lint` — passing tests; `renderAgentsForService` flagged as unused (expected, will be called from Task 6 command handler)

### Task 6: Wire `devbox render ai` cobra subcommand

Add the user-facing entry point under the existing `render` parent.

- [ ] add `newRenderAICmd(flags *rootFlags) *cobra.Command` in `internal/command/render_ai.go`:
  - `Use: "ai [service]"`, args: `cobra.MaximumNArgs(1)`
  - `ValidArgsFunction`: reuse `serviceNameCompletion(flags)`
  - `RunE`:
    1. `config.LoadConfig(flags.configPath)` (same as IDE)
    2. determine target services:
       - if explicit arg: validate the service exists, is enabled at project level, has non-empty `Dir`, and `AIDocsRenderEnabled()` — explicit-strict error messages parallel to `validateExplicitIDEArg`
       - if no arg: iterate `cfg.Services`, select services where `svc.Enabled` and `svc.AIDocsRenderEnabled()` and `Dir != ""`. Skip silently for default-disabled cases; emit `Warning` only for actionable skips (empty dir on an opted-in service). **Collision policy mirrors IDE rendering** (`internal/command/ide.go:54-140`): group selected services by `filepath.Clean(svc.Dir)`; for each group, the service with the **deepest `extends` chain wins** (tie-break lexicographic). This is required because `extends`-derived services legitimately share a `Dir` with their parent, and the derived child must win — picking the parent would render upstream docs over the specialized service. Extract the existing helpers (`selectIDEServices`, `extendsDepth`, `skippedService`) into something reusable, or, simpler, copy the same algorithm into a new `selectAgentsServices` function that calls the same `extendsDepth` (already package-local) and emits the same `skippedService` variants (rename to a shared name if duplication offends; otherwise keep symmetric).
       - if nothing selected → `Info("no services match the ai-docs rendering policy")`, return nil
    3. for each selected service: call `renderAgentsForService(projectRoot, name, svc, cfg, w)`
- [ ] add `renderAgentsForService(projectRoot, name string, svc config.ServiceConfig, cfg *config.DevboxConfig, w *render.Writer) error`:
  - validate `svc.Dir` non-empty
  - resolve `absRoot`, `absHubDir`; `pathsafe.ContainedRel(absRoot, absHubDir)` (hub strictly under root, not equal); `pathsafe.CheckNoSymlinks(absRoot, absHubDir, "service dir")`
  - `pack, err := resolveAgentsTemplatePack(svc, projectRoot, name)`
  - `manifest, err := loadAgentsManifest(pack)`; `validateAgentsManifest(manifest, pack)`
  - for each render entry: call `renderAgentsTemplateFile`; emit `Success(fmt.Sprintf("ai → %s", filepath.Join(svc.Dir, entry.To)))`
  - for each symlink entry: `changed, err := ensureRelativeSymlink(...)`; on error return; otherwise if `changed` emit `Success(fmt.Sprintf("ai → %s ⇒ %s", filepath.Join(svc.Dir, entry.Link), entry.To))`; if not changed, skip the success line
- [ ] register in `internal/command/env.go`: `cmd.AddCommand(newRenderAICmd(flags))` and update the `Long`/`Example` of `newRenderCmd` to list `ai`
- [ ] write integration-style tests in `internal/command/render_ai_test.go` exercising the full `RunE`:
  - happy path: pack + manifest in fixture project, single service, asserts files + symlink
  - explicit-arg variants: not-found, disabled at project, no Dir, `ai_docs.enabled: false`
  - no-arg auto-selection: enabled services rendered, disabled services skipped, empty-dir service warns
  - missing pack (both candidates) → error
  - existing user-authored `CLAUDE.md` (regular file) → error per locked §5
- [ ] run `make test && make lint` — must pass before next task

### Task 7: Documentation

Keep behavior and docs aligned (per `AGENTS.md`).

- [ ] update `docs/reference/config/services.md` — document the `ai_docs:` block: fields (`enabled`, `template`), defaults (enabled=true for all types), opt-out semantics, template pack resolution rules (explicit-strict, implicit chain), inheritance via `extends`. Short YAML example.
- [ ] in `docs/reference/config/services.md`, add a "Template pack layout" subsection describing `devbox/templates/agents/<name>/{AGENTS.md.tmpl,manifest.yml}` with a minimal example manifest and a short template (so projects have something to copy).
- [ ] update `docs/reference/config/index.md` — add `devbox render ai — generate hub-level AGENTS.md / CLAUDE.md` to the commands list (currently lines 85-86 list `render env` and `render ide`); otherwise the config-docs navigation goes stale.
- [ ] **rebuild and regenerate CLI docs**: `make build && bin/devbox docs generate --scope cli`. Without rebuilding first, the generator runs from a stale binary and `devbox_render_ai.md` won't appear. The command is project-independent per CLAUDE.md.
- [ ] verify `docs/reference/cli/devbox_render_ai.md` is created and `docs/reference/cli/devbox_render.md` is updated to list the new `ai` subcommand alongside `env` and `ide`
- [ ] check the docs diff — expected files: `docs/reference/config/services.md` (manually edited), `docs/reference/config/index.md` (manually edited), `docs/reference/cli/devbox_render.md` (auto-regenerated), `docs/reference/cli/devbox_render_ai.md` (auto-generated). No other files in `docs/reference/` should change.
- [ ] run `make test && make lint` — must pass before next task

### Task 8: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented: `devbox render ai` exists, schema field exists with inheritance, opt-out works, explicit/implicit pack resolution works, symlink created and idempotent, fail-safe on existing regular file
- [ ] verify all 6 locked decisions are reflected in code + tests + docs
- [ ] verify edge cases: missing pack, symlinks anywhere in pack or hub dir, escaping `to`/`from`, opt-out via `enabled: false`, nested `to` paths like `.claude/CLAUDE.md`
- [ ] run `make test` — full suite green
- [ ] run `make lint` — no issues
- [ ] manual smoke: build, run `bin/devbox render ai --help` and confirm the subcommand shows up alongside `env` and `ide`

### Task 9: [Final] Update `AGENTS.md` (project memory)

- [ ] update the `internal/command/` summary in `AGENTS.md` (root, canonical; `CLAUDE.md` is a symlink) to mention `devbox render ai`, `resolveAgentsTemplatePack`, `loadAgentsManifest`, `ensureRelativeSymlink`
- [ ] update the `internal/config/` summary in `AGENTS.md` to mention `AIDocs ServiceAIDocsConfig`, `AIDocsRenderEnabled` / `AIDocsRenderEnabledExplicit`, and that `ai_docs` is inherited via `extends`
- [ ] add `internal/pathsafe/` to the package-layout list in `AGENTS.md` with `CheckNoSymlinks`, `ContainedRel`, `EnsureRealUnder`
- [ ] note in the **YAML loader strictness** section that the agents manifest loader is another strict surface (user-edited)

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`*

## Technical Details

### Schema addition (`ServiceConfig`)

```go
// ServiceAIDocsConfig holds settings for hub-level agentic doc rendering.
type ServiceAIDocsConfig struct {
    Enabled  *bool  `yaml:"enabled"`
    Template string `yaml:"template"`
}

// On ServiceConfig:
AIDocs ServiceAIDocsConfig `yaml:"ai_docs"`
```

Default policy: `AIDocsRenderEnabled()` returns `true` when `Enabled` is nil — for any service type. Inherits from parent via `extends` (Task 2).

### Manifest schema

```yaml
render:
  - from: AGENTS.md.tmpl
    to:   AGENTS.md
  - from: .claude/CLAUDE.md.tmpl   # nested allowed
    to:   .claude/CLAUDE.md
symlinks:
  - link: CLAUDE.md
    to:   AGENTS.md                # MUST match a render `to`
```

- Strict YAML decode (unknown fields error).
- `from` paths: relative to pack dir, must end in `.tmpl`.
- `to` / `link` paths: relative to hub dir; nested allowed; must stay inside hub.
- Symlink `to` must reference a `render to` produced in the same manifest (no "exists on disk" allowance — locked §2).

### Template data context

```go
type agentsTemplateData struct {
    Project    config.ProjectConfig
    Service    string                // service name
    ServiceCfg config.ServiceConfig
    Runtime    config.RuntimeConfig
}
```

Templates use `{{ .Service }}`, `{{ .Project.Name }}`, `{{ .ServiceCfg.Container }}`, `{{ .ServiceCfg.WorkDirInternal }}`, `{{ .ServiceCfg.CLI.User }}`, etc. `text/template` with `Option("missingkey=error")` (same as IDE renderer).

### Symlink rule

Symlinks are always **relative** (e.g. `CLAUDE.md → AGENTS.md`, not absolute) and computed as `filepath.Rel(filepath.Dir(absLink), absTarget)`. Both endpoints validated to live inside `absHubDir` before any FS mutation. Pre-existing non-symlink at the link path → hard error (locked §5).

### Command invocation

```text
devbox render ai             # render for all enabled services
devbox render ai api         # render only service "api"
```

No pipeline-builtin form (architectural shift from the original spec) — generation is a user-facing render step and lives only as a CLI command. Project pipelines can still invoke `devbox render ai` via a `type: devbox` step in deploy.yml if integration into deploy is desired.

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Downstream / external** (out of scope for this repo):

- Create actual template packs in user devbox projects under `devbox/templates/agents/default/` (and per-service overrides as needed). The CLI does not bundle defaults.
- Optionally invoke `devbox render ai` from a `type: devbox` deploy step in the project's `deploy.yml` if the docs should regenerate on every deploy.
- Verify generated `AGENTS.md` and `CLAUDE.md` symlink land in service hubs after `devbox render ai` in a real project.
- Confirm AI tooling (Claude Code, Cursor, Codex, Aider) picks up `AGENTS.md` / `CLAUDE.md` at the hub level when attached to a devcontainer rooted there.

**Documentation outside the cli repo**:

- Update the devbox-config reference / examples repo with a sample `agents/default/` template pack.
