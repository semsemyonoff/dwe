# IDE Template Pack Walker

## Overview

Replace the hardcoded IDE rendering logic in `internal/command/ide.go` with a generic walker that mirrors the directory structure of a *template pack* into the service directory. After this refactor `devbox render ide` no longer knows about VSCode, devcontainer, or JetBrains — it knows one rule:

> Every `*.tpl` file inside the chosen template pack is rendered into the service dir at the same relative path, minus the `.tpl` suffix.

Concretely:

```
devbox/templates/ide/default/.devcontainer/devcontainer.json.tpl
→ services/main/.devcontainer/devcontainer.json

devbox/templates/ide/default/.vscode/settings.json.tpl
→ services/main/.vscode/settings.json

devbox/templates/ide/main-debug/.vscode/launch.json.tpl
→ services/main/.vscode/launch.json
```

This lets users add support for any IDE/tool (`.idea/`, `.zed/`, `.cursor/`, `.envrc`, …) without touching Go code.

**No backward compatibility** is required: the legacy root-level `devcontainer.json.tpl` / `vscode_settings.json.tpl` / `vscode_launch.json.tpl` and the top-level `cfg.IDE.{Devcontainer,VSCode,JetBrains}.Enabled` flags are removed outright.

## Context (from discovery)

Files involved:
- `internal/command/ide.go:170` — `RunE` and orchestration
- `internal/command/ide.go:272` — `resolveIDETemplate` (file-level fallback, to be replaced with pack-level)
- `internal/command/ide.go:346` — `renderIDEConfigs` (3 hardcoded blocks for devcontainer / jetbrains-stub / vscode)
- `internal/command/ide.go:428` — `renderIDETemplate` (template execution + symlink guard, reusable)
- `internal/command/ide.go:314` — `checkNoSymlinks` (reusable)
- `internal/command/ide.go:248` — `validateIDETemplateKey` (reusable)
- `internal/command/ide.go:143` — `ideTemplateData` (drop the `IDE` field)
- `internal/config/devbox.go:99` — `IDEConfig`, `IDEEditorConfig` (delete)
- `internal/config/devbox.go:304` — `ServiceIDEConfig` (keep — `Enabled *bool` and `Template string`)
- `internal/config/devbox.go:659` — extends inheritance for `ide` block (keep)
- `internal/command/ide_test.go` — extensive existing tests; rewrite hardcoded fixtures, add walker coverage
- `docs/reference/config/services.md:167` — IDE block docs (rewrite)
- `docs/reference/cli/devbox_render_ide.md` — auto-generated, regenerate via `devbox docs generate`

What stays unchanged:
- Service selection policy: `selectIDEServices` (enabled gate, empty-dir drop, dir collision via extends depth)
- Per-service `ide.enabled` (tristate) and `ide.template` config
- Symlink defenses (`checkNoSymlinks`, post-mkdir `EvalSymlinks` re-check, refusal to overwrite symlinked files)
- Template key validation (no separators, no `..`, no leading dot, no absolute path)
- Go `text/template` engine and the `Project`/`Service`/`ServiceCfg`/`Runtime` data context

## Development Approach

- **Testing approach**: Regular (implementation first, tests in same task)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- No backward compatibility shims

## Testing Strategy

- Unit tests in `internal/command/ide_test.go` for: pack resolver, walker, end-to-end `renderIDEConfigs`, security rejections (symlinked source, escape via crafted relative path)
- Project has no UI/e2e suite — only Go unit tests apply
- Fixtures: replace `setupIDETemplates()` helper to write a directory-shaped pack instead of three flat files; add a second pack to exercise per-service override

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope

## What Goes Where

- **Implementation Steps**: code, tests, docs in this repo
- **Post-Completion**: anything requiring user-side migration (template repo restructure)

## Implementation Steps

**Task ordering note**: each task is a compile + `make test` checkpoint. The earlier draft put "delete `IDEConfig` types" as Task 1, but `internal/command/ide.go` references `config.IDEConfig` (in `ideTemplateData.IDE`) and `cfg.IDE.{Devcontainer,VSCode,JetBrains}.Enabled` (in three render branches), so deleting the types first would not compile. The order below first refactors `ide.go` and its tests end-to-end (eliminating every `cfg.IDE` reference), and only then removes the now-unused config types.

### Task 1: End-to-end refactor of internal/command/ide.go and its tests
*Compile + test checkpoint. After this task, `ide.go` and `ide_test.go` no longer reference `cfg.IDE` or `config.IDEConfig`. The `IDEConfig` types in `internal/config` still exist but are unreferenced — they are deleted in Task 2.*

**Resolver**:
- [x] add `resolveIDETemplatePack(svc config.ServiceConfig, projectRoot, serviceName string) (string, error)`
  - call `filepath.Abs(projectRoot)` once at entry; build candidate paths from the absolute root so the returned path is always absolute
  - validate `svc.IDE.Template` and `serviceName` via existing `validateIDETemplateKey`
  - **explicit is strict**: if `svc.IDE.Template` is set and `devbox/templates/ide/<svc.IDE.Template>/` does not exist, or exists but is not a regular directory, or is a symlink — return a clear error. **No fallback to service-name or default.** Rationale: a typo like `main-deubg` must not silently render the `default/` pack.
  - **implicit fallback chain** (only when `svc.IDE.Template` is empty). For each candidate `os.Lstat` the path; only `os.ErrNotExist` advances to the next candidate. Any other condition (regular file, symlink, foreign mode) is a hard error and does NOT fall through:
    1. `devbox/templates/ide/<service-name>/`
    2. `devbox/templates/ide/default/`
  - **pack root validation rule (applies to every candidate, explicit or implicit)**: `os.Lstat` the candidate path. If `Mode()&os.ModeSymlink != 0` → reject as "pack root is a symlink". If exists and not `IsDir()` → reject as "pack root is not a directory". Only `os.ErrNotExist` is treated as "candidate absent".
  - propagate non-`ErrNotExist` errors (validation, lstat failures, malformed candidate)
  - if the implicit chain finds nothing, return a wrapped `os.ErrNotExist` so callers can produce a clear user-facing message
- [x] write unit tests for `resolveIDETemplatePack`:
  - explicit-only, by-service-only, default-only, all-three-present priority, all-missing error
  - invalid template key, invalid service name
  - **explicit pack missing while default exists → error (not silent fallback)**
  - **explicit pack is a regular file → error**
  - **explicit pack is a symlink to a directory → error**
  - **service-name candidate is a regular file while `default/` exists → error (no fallback past a malformed candidate)**
  - **service-name candidate is a symlink to a directory while `default/` exists → error**
  - **`default/` itself is a regular file → error**
  - **`default/` itself is a symlink to a directory → error**
  - resolver called with a relative `projectRoot` still returns an absolute pack path

**Walker**:
- [x] add `walkIDEPack(packDir string) ([]packEntry, error)` where `packEntry` carries `RelPath` (without `.tpl`) and `SourcePath` (always absolute). Apply the checks below in this exact order — checking suffix before symlink-mode would let a symlinked non-`.tpl` file or a symlinked directory be silently skipped, defeating the security guarantee:
  1. **Normalize**: call `filepath.Abs(packDir)` once at entry; walk and build `SourcePath` from the absolute pack root so the contract holds even when callers pass a relative `packDir`.
  2. **Symlink rejection (must run BEFORE any suffix filter)**: for every directory entry encountered (file or directory, regardless of name), `Lstat` it. If `Mode()&os.ModeSymlink != 0`, return an error. Otherwise a file like `evil_link -> /etc/passwd` (no `.tpl` suffix) or a symlinked directory `outside -> /tmp` would be silently skipped. `filepath.WalkDir` does not follow symlinks but does report them as `DirEntry`, so explicit per-entry `Lstat` is required. Apply to both leaf files and every intermediate directory.
  3. **Suffix filter**: only after the symlink check passes, skip non-`.tpl` files silently.
  4. **Bare-`.tpl` rejection**: for surviving `.tpl` files, reject when the source filename is bare `.tpl`. The check operates on the basename **before** cleaning: `strings.TrimSuffix(filepath.Base(srcRelPath), ".tpl") == ""` → reject. This catches both a file literally named `.tpl` at pack root (post-trim path → `""`) and `dir/.tpl` (post-trim path → `dir/`, which after `Clean` becomes `dir` — a directory name, not a file destination — and would silently overwrite a directory or write into the parent). Cleaning first and inspecting `filepath.Base` does NOT catch the nested case, because `Base("dir")` is `"dir"`, which is non-empty.
  5. **Defensive empty-path guard**: also reject entries whose post-trim, post-clean relative path is empty or `"."`.
  6. **Path-shape**: reject entries whose cleaned relative path is absolute or escapes the pack root (`..` segments).
  7. **Sort**: sort surviving entries lexicographically by `RelPath` for deterministic output.
- [x] write unit tests for `walkIDEPack`: empty pack (no `.tpl` → no entries, no error), nested dirs, mixed `.tpl` and non-tpl files, **symlinked non-`.tpl` file rejected (proves symlink-check runs before suffix-filter)**, symlinked `.tpl` file rejected, symlinked source dir rejected, deterministic ordering, **bare `.tpl` at root rejected**, **nested `dir/.tpl` rejected**, **walker called with relative `packDir` returns absolute `SourcePath`s**

**Render function**:
- [x] **replace** `renderIDETemplate(tplStr, name string, data, dest, absRoot string) error` with `renderIDETemplateFile(sourcePath string, data ideTemplateData, dest, absDir, absRoot string) error`. The new function reads the file, parses the template (using `filepath.Base(sourcePath)` as the template name for error messages), and reuses the existing destDir symlink check, `MkdirAll`, post-`EvalSymlinks` containment check, and refusal to overwrite symlinked destinations. **It also enforces that the resolved `dest` is contained within `absDir` (the resolved service directory)**, not just within `absRoot`.
  - **Concrete containment check**: do NOT use `strings.HasPrefix` for the service-dir boundary — `/services/main2` has prefix `/services/main`, so prefix-matching admits sibling-service escapes. Compute `absDest, _ := filepath.Abs(dest)` and `rel, err := filepath.Rel(absDir, absDest)`; reject when `err != nil`, when `rel == ".."`, or when `strings.HasPrefix(rel, ".."+string(filepath.Separator))`. Reject equally when `rel` is absolute (defensive — `Rel` may return absolute on cross-volume Windows). The existing `absRoot` prefix check (in `renderIDETemplate` today) is kept as an additional outer guard, but the inner service-dir guard is the strict one.
  - Rationale: locks the service-dir containment invariant at the render boundary, independent of walker correctness, and avoids the well-known prefix-vs-`Rel` bug class.
- [x] write unit tests for `renderIDETemplateFile` service-dir containment:
  - manually crafted `dest` outside `absDir` but inside `absRoot` (e.g. via `RelPath = "../sibling/file"`) — must reject
  - **sibling-prefix attack**: `absDir = /tmp/.../services/main`, `dest = /tmp/.../services/main2/leak` — must reject (would slip through a naive `HasPrefix(absDir)` check)

**Drop ideTemplateData.IDE and rewrite renderIDEConfigs**:
- [x] remove the `IDE config.IDEConfig` field from `ideTemplateData` in `internal/command/ide.go:143`
- [x] rewrite `renderIDEConfigs` to: resolve pack → walk → for each entry compute `destPath := filepath.Join(absDir, entry.RelPath)` → call `renderIDETemplateFile(entry.SourcePath, data, destPath, absDir, absRoot)`. The render function enforces `serviceDir` containment internally.
- [x] delete the three hardcoded blocks (`cfg.IDE.Devcontainer.Enabled`, `cfg.IDE.JetBrains.Enabled` stub, `cfg.IDE.VSCode.Enabled`)
- [x] delete the now-unused `resolveIDETemplate` and old `renderIDETemplate` (if no other caller remains — verify with grep before deletion)
- [x] keep `checkNoSymlinks`, the post-mkdir `EvalSymlinks` re-check, and the refusal to overwrite symlinked files unchanged inside `renderIDETemplateFile`

**Rewrite ide_test.go**:
- [x] replace `setupIDETemplates()` (`internal/command/ide_test.go:21`) with a helper that writes a directory-shaped pack (e.g. `default/.devcontainer/devcontainer.json.tpl`, `default/.vscode/settings.json.tpl`)
- [x] update `makeIDECfg()` (`ide_test.go:40`) to drop `cfg.IDE.*.Enabled` setup — selection is now pack-driven
- [x] rewrite or replace the integration tests in `ide_test.go:228`–`391` (devcontainerOnly, vscodeOnly, bothEnabled, neitherEnabled, devcontainerSubstitutesValues, missingTemplates) — drive coverage from pack contents, not `cfg.IDE.*.Enabled`
- [x] add a test for per-service pack override (`svc.IDE.Template = "main-debug"` with a second pack on disk)
- [x] add a test for service-name fallback (no explicit `template`, pack named after the service exists, default also exists → service-named pack wins)
- [x] add a test for default-only fallback (only `default/` exists → used)
- [x] add a test that empty pack dir produces no files and no error
- [x] add a test that pack-not-found is reported as a clear user-facing error
- [x] **add a test for explicit-strict semantics**: `svc.IDE.Template = "main-deubg"` (typo), `default/` exists on disk, `main-deubg/` does not → render must return an error mentioning the missing explicit pack, must NOT render `default/`
- [x] add tests that an explicit pack which is a regular file or a symlink to a real directory is rejected with a clear error
- [x] update or replace `TestResolveIDETemplate` with `TestResolveIDETemplatePack` per the resolver test list above
- [x] keep symlink-security tests (`TestRenderIDETemplate_symlinkDir`, `TestRenderIDETemplate_symlinkFile`) but rename / reshape them to match `renderIDETemplateFile`

**Gate**:
- [x] `grep -rn 'cfg\.IDE\.\|config\.IDEConfig\|config\.IDEEditorConfig' internal/command/` returns nothing
- [x] run `make test` — must pass before next task

### Task 2: Delete IDEConfig from config schema
*All references in `internal/command/` were eliminated in Task 1, so this task is a pure schema deletion + test cleanup. After this task `IDEConfig` does not exist anywhere in the codebase.*

- [x] delete `IDEConfig` and `IDEEditorConfig` types in `internal/config/devbox.go:99`
- [x] delete the `IDE IDEConfig \`yaml:"ide"\`` field from `DevboxConfig`
- [x] sweep the rest of the codebase for any remaining references with `grep -rn 'IDEConfig\|IDEEditorConfig\|cfg\.IDE\b\|\.IDE\.Devcontainer\|\.IDE\.VSCode\|\.IDE\.JetBrains' internal/ cmd/` — must be empty
- [x] update `internal/config` tests that reference `IDEConfig`/`IDE.{Devcontainer,VSCode,JetBrains}.Enabled` to drop those assertions
- [x] write/update test asserting that the typed config no longer carries any IDE state — i.e. the resulting `*DevboxConfig` has no `IDE` field, and no code path reads top-level IDE behavior. Note: `cfg.Raw` will still retain a top-level `ide:` key if a user leaves one in `devbox.yml` / `devbox/defaults.yml` (lenient layered loader behavior is intentional and preserved); we do not special-case-delete it. The test should assert "not mapped into typed config / no `cfg.IDE` behavior remains", not "key disappears from `cfg.Raw`".
- [x] run `make test` — must pass before next task

### Task 3: Update CLI help text and documentation
- [ ] rewrite the Cobra `Long`/`Short` text in `newRenderIDECmd` (`internal/command/ide.go:155`):
  - drop the hardcoded list (`devcontainer: .devcontainer/devcontainer.json`, `vscode: .vscode/launch.json` …)
  - drop the line about `ide:` in `devbox/defaults.yml` (the top-level block no longer exists)
  - explain the new model: walks the chosen template pack under `devbox/templates/ide/<pack>/` and renders each `*.tpl` to the matching relative path inside the service dir
  - state the explicit-strict / implicit-fallback resolution rules
- [ ] rewrite `docs/reference/config/services.md:167`–`223` IDE section: remove mentions of devcontainer/vscode/jetbrains-specific behavior; describe the pack convention with mirrored relative paths; document explicit-is-strict vs implicit-fallback resolution
- [ ] add a worked example showing `devbox/templates/ide/default/...` and `devbox/templates/ide/main-debug/...` directory layouts and the resulting service-dir output (note: service definitions live in `devbox/services.yml`, not `devbox.yml`)
- [ ] remove any reference to top-level `ide:` block from any docs (search `docs/` for `ide:` at top-level / `devbox/defaults.yml`)
- [ ] regenerate `docs/reference/cli/devbox_render_ide.md` via `make build && bin/devbox docs generate --scope cli` so the CLI help reflects the new Long/Short text
- [ ] run `make test` — must pass before final verification

### Task 4: Verify acceptance criteria
- [ ] verify Overview goal achieved: no editor names appear in `internal/command/ide.go`
- [ ] verify `cfg.IDE` no longer exists anywhere in the codebase (`grep -rn 'cfg\.IDE\.' internal/ cmd/`)
- [ ] verify `selectIDEServices` policy is unchanged (existing tests still pass)
- [ ] verify symlink and path-escape security tests still pass
- [ ] run full `make test` and `make lint`
- [ ] verify no orphan symbols remain (delete `resolveIDETemplate` if fully replaced; remove `ideTemplateData.IDE`)
- [ ] verify `internal/command/ide_test.go` no longer references removed types

## Technical Details

**New resolver signature**:

```go
func resolveIDETemplatePack(svc config.ServiceConfig, projectRoot, serviceName string) (string, error)
```

Always returns an **absolute** path to a directory under `devbox/templates/ide/`. The resolver calls `filepath.Abs(projectRoot)` once at entry and builds candidate paths from the absolute root, so callers may pass either a relative or absolute `projectRoot` and get a stable absolute pack path back. (Today `flags.ProjectRoot()` is already absolute, but tests sometimes use relative roots — this contract removes that footgun.)

**Walker entry**:

```go
type packEntry struct {
    SourcePath string // absolute path to the .tpl file (walker normalizes packDir via filepath.Abs at entry)
    RelPath    string // path inside the pack with .tpl stripped, used to mirror into service dir
}
```

The walker contract guarantees `SourcePath` is absolute regardless of how `packDir` was passed in. This frees callers (and tests) from having to pre-normalize.

**Walker rejection rules** (an entry is rejected if any of these fail; rejection is a hard error, not a silent skip):

1. The source file is a symlink (`os.Lstat` → `Mode()&os.ModeSymlink != 0`).
2. Any directory traversed to reach the file is a symlink (per-entry `Lstat` during walk).
3. The relative path from `packDir` to the source file, after stripping the `.tpl` suffix and `filepath.Clean`, is absolute or contains a `..` segment.
4. **The source filename is bare `.tpl`**. Concretely: `strings.TrimSuffix(filepath.Base(srcRelPath), ".tpl") == ""` → reject. The check operates on the **original basename**, before any `filepath.Clean`. Examples that must fail: `.tpl` at the pack root (post-trim → `""`); `dir/.tpl` (post-trim → `dir/`, after `Clean` → `dir` — a directory name, would overwrite a directory or write into the parent). A post-clean `Base` check is insufficient: `Base("dir")` is `"dir"`, which is non-empty, so cleaning-first hides the bug. As an additional belt-and-suspenders guard, also reject when the post-trim, post-clean path is `""` or `"."`.
5. Two entries that, after trim+clean, produce identical `RelPath` (defensive — should not happen for well-formed pack contents on a case-sensitive filesystem; protects only against walker bugs that emit duplicate paths). **This is a raw-string equality check, not a case-folded one** — it does NOT catch `Foo.tpl` vs `foo.tpl` collisions on macOS/Windows. Case-fold collision detection is out of scope for this refactor; revisit if it becomes a real issue.

**Render function signature change**:

The current `renderIDETemplate(tplStr, name string, data ideTemplateData, dest, absRoot string) error` (`internal/command/ide.go:428`) is replaced with:

```go
func renderIDETemplateFile(sourcePath string, data ideTemplateData, dest, absDir, absRoot string) error
```

The new function does its own `os.ReadFile`, uses `filepath.Base(sourcePath)` as the template name for error messages, and reuses the existing destDir symlink check, `MkdirAll`, post-`EvalSymlinks` containment check, and symlinked-destination refusal — these blocks move into the new function unchanged. It additionally enforces that the resolved `dest` is contained within `absDir` (the resolved service directory). `absDir` must itself be contained within `absRoot`, but that is a loop precondition.

**Service-dir containment uses `filepath.Rel`, not prefix matching**:

```go
absDest, err := filepath.Abs(dest)
if err != nil { return err }
rel, err := filepath.Rel(absDir, absDest)
if err != nil { return fmt.Errorf("dest %q outside service dir: %w", dest, err) }
if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
    return fmt.Errorf("dest %q escapes service dir %q", dest, absDir)
}
```

The naive `strings.HasPrefix(absDest, absDir)` check is wrong because `/services/main2` has prefix `/services/main` — a sibling service would slip through. The existing prefix check against `absRoot` is kept as an outer guard but the inner service-dir boundary is the strict one.

**Render loop sketch** (replaces the three hardcoded blocks):

```go
pack, err := resolveIDETemplatePack(svc, projectRoot, serviceName)
if err != nil { /* surface as hard error */ }

entries, err := walkIDEPack(pack)
if err != nil { /* surface */ }

for _, e := range entries {
    dest := filepath.Join(serviceDir, e.RelPath)
    // renderIDETemplateFile enforces: dest is inside absDir, absDir is inside absRoot,
    // no symlinks in destDir, no symlinked destination file.
    if err := renderIDETemplateFile(e.SourcePath, data, dest, absDir, absRoot); err != nil { /* surface */ }
}
```

**Data context** stays the same minus `IDE`:

```go
type ideTemplateData struct {
    Project    config.ProjectConfig
    Service    string
    ServiceCfg config.ServiceConfig
    Runtime    config.RuntimeConfig
}
```

**Example user-facing configuration** (service definitions live in `devbox/services.yml`; `devbox.yml` participates only in the layered enablement/toggle side):

```yaml
# devbox/services.yml
services:
  main:
    type: app
    dir: services/main
    container: php
    ide:
      enabled: true   # default for type: app, shown for clarity

  main-debug:
    extends: main
    container: php-debug
    compose:
      - compose/services/main/debug.yml
    ide:
      template: main-debug
```

**Example project layout**:

```
devbox/templates/ide/
  default/
    .devcontainer/devcontainer.json.tpl
    .vscode/settings.json.tpl
  main-debug/
    .devcontainer/devcontainer.json.tpl
    .vscode/settings.json.tpl
    .vscode/launch.json.tpl
```

**Example template** (`devbox/templates/ide/default/.devcontainer/devcontainer.json.tpl`):

```json
{
  "name": "{{ .Project.Prefix }}_{{ .Project.Name }}",
  "dockerComposeFile": [
    "../../../compose.yaml"{{ range .ServiceCfg.Compose }},
    "../../../{{ . }}"{{ end }}
  ],
  "service": "{{ .ServiceCfg.Container }}",
  "runServices": ["{{ .ServiceCfg.Container }}"],
  "workspaceFolder": "{{ .ServiceCfg.DirInternal }}"
}
```

**Security invariants preserved / added**:
1. Template pack key cannot contain `/`, `\`, `..`, leading `.`, or be absolute (existing `validateIDETemplateKey`).
2. **Pack root validation** (new): `os.Lstat` on every candidate pack directory (explicit and each implicit step) — must not be a symlink, must be a regular directory. Only `os.ErrNotExist` advances the implicit chain; any other condition (regular file, symlink, foreign mode) is a hard error and does NOT fall through to the next implicit candidate.
3. Walker rejects any symlink encountered inside the pack tree (file or directory entries).
4. Walker rejects entries whose cleaned relative path is absolute or escapes the pack root.
5. **Render-boundary containment** (new): `renderIDETemplateFile` independently enforces that `dest` is inside `absDir` (the resolved service directory). This invariant lives at the render call, not only inside `walkIDEPack`, so a future walker bug or crafted `RelPath` cannot silently escape into a sibling service.
6. The resolved service dir must be inside `projectRoot`.
7. `EvalSymlinks` re-check after `MkdirAll` catches symlink-based escapes during destination creation.
8. Pre-existing symlinked files at the destination are refused.
9. **Explicit-strict resolution** (new behavioral guarantee): a non-empty `svc.IDE.Template` that does not resolve to an existing valid pack directory is a hard error — never silently falls back. Protects against typo-driven silent rendering of the wrong pack.

## Post-Completion

*Items requiring manual intervention or external systems — informational only.*

**User-side migration** (any project that previously used flat templates):
- Move `devbox/templates/ide/devcontainer.json.tpl` → `devbox/templates/ide/default/.devcontainer/devcontainer.json.tpl`
- Move `devbox/templates/ide/vscode_settings.json.tpl` → `devbox/templates/ide/default/.vscode/settings.json.tpl`
- Move `devbox/templates/ide/vscode_launch.json.tpl` → `devbox/templates/ide/default/.vscode/launch.json.tpl` (or into a per-service pack)
- Remove the top-level `ide:` block from `devbox/defaults.yml` (or any layered config file) if present — it is no longer parsed
- Per-service `ide.enabled` and `ide.template` in `devbox/services.yml` continue to work unchanged
- Note that `ide.template` is now strict: a typo will fail loudly instead of silently rendering the `default/` pack

**Manual verification** (recommended):
- Run `devbox render ide` on a real project and inspect the generated tree
- Confirm a brand-new tool config (e.g. `.zed/settings.json.tpl`) appears at the right destination without code changes
