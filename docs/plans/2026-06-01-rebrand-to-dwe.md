# Rebrand devbox → dwe (Dev Workspace Engine)

## Overview

Full rename of the product from `devbox` to `dwe` (decoded as **D**ev **W**orkspace **E**ngine) to avoid the naming collision with https://github.com/jetify-com/devbox (a popular Nix-based tool with the same binary name, ~10k stars). They dominate SEO and ship the same binary name. Our v0.1.0 release exists on a Homebrew tap but has no users yet — this is the narrow window for a clean rebrand without backwards-compatibility tail.

The name `dwe` has been vetted: Homebrew core is free, no competing CLIs, no significant Go repositories collide, the acronym landscape is niche.

### Design decisions (brand vs schema boundary)

A coherent split is enforced throughout the plan:

| Surface | Convention | Rationale |
|---|---|---|
| Binary, runtime dir, env vars, Go identifiers, YAML enums, Claude plugin name, Docker labels, snapshot tool-version field, **user-visible brand strings** (notifications, render headers, cobra root, llms.txt URI scheme, browser titles, docs root identifier) | **`dwe` / `Dwe` / `DWE_*` / `.dwe/` / `dwe.*` / `dwe-docs://`** | Tool brand. Renaming the tool requires renaming these by definition. |
| Project config file, project folder, snapshot's captured-config subdir, snapshot's captured-files manifest field | **`workspace.yml` / `workspace/` / `workspace_files`** | The user-facing schema describes "a workspace"; the tool happens to read/capture it. Decoupled so a future rebrand never breaks user files or snapshot archives. Parallel: `package.json` + `node_modules/` are independent of npm/yarn/pnpm. |

Concrete mappings:

- Binary: `cmd/devbox/` → `cmd/dwe/`, output `bin/dwe`.
- Go module: `github.com/semsemyonoff/devbox` → `github.com/semsemyonoff/dwe`.
- Runtime state dir (lock files, logs, per-project user config): `.devbox/` → `.dwe/`.
- Env-var contract exposed to user commands: `DEVBOX_*` → `DWE_*`.
- User-command type discriminator: `type: devbox` → `type: dwe`. Go runner type `DevboxRunner` → `DweRunner`. Go const `CommandTypeDevbox = "devbox"` → `CommandTypeDwe = "dwe"` (plus ~10 alias re-exports).
- Go identifiers: `DevboxConfig` → `DweConfig`, `DevboxBin` accessor → `DweBin`, user-config key `binary_devbox` → `binary_dwe`.
- Project config file: `devbox.yml` → `workspace.yml`.
- Project folder: `devbox/` → `workspace/` (also affects `filepath.Join(absRoot, "devbox", "templates", ...)` template-pack paths in `internal/core/execution/templates/{ide,ai,git,packroot}/`).
- Docker daemon labels: `devbox.project` → `dwe.project`, `devbox.daemon.id` → `dwe.daemon.id`, `devbox.daemon.params` → `dwe.daemon.params`.
- Snapshot archive schema: captured-config subdir `devbox/` → `workspace/`; manifest field `devbox_version` → `dwe_version` (tool brand); manifest field `devbox_files` → `workspace_files` (schema mirror); Go type `DevboxFiles` → `WorkspaceFiles`; constant `DevboxSubdir = "devbox"` → `WorkspaceSubdir = "workspace"`.
- **User-visible brand strings (Phase 3g):**
  - macOS notifications: `terminalNotifierGroup = "Devbox"` → `"DWE"`; temp file prefix `"devbox-notify-*.png"` → `"dwe-notify-*.png"`.
  - Linux/Other notifications: `beeep.AppName = "Devbox"` → `"DWE"`.
  - Notification title prefix: `"Devbox"` → `"DWE"`.
  - Render brand header (`ui/render/brand_header.go:32`): `"Devbox"` → `"DWE"` (prints at top of `dwe status`, `dwe info`, etc.).
  - Cobra root command `Use: "devbox"` (`internal/cli/root.go:124`) → `"dwe"` (controls `Usage: …` in `--help`).
  - Docs browser title (`internal/cli/docs/docs.go:38`): `"Devbox"` → `"DWE"`.
  - Command browser selector title (`internal/cli/command/list.go:232,234,237`): `"Devbox"` → `"DWE"`.
  - Generated CLI docs titles (`internal/cli/docs/generate.go:307,706,760-764`): `# devbox Reference Documentation` etc. → `# dwe …`.
  - llms.txt generator prose (`internal/core/docs/llmstxt/generator.go`): `# devbox` heading + descriptive prose → `# dwe`; **URI scheme `devbox-docs://` → `dwe-docs://`** (clean break — no cached llms.txt outputs exist).
  - Docs source/topic root identifier (`internal/core/docs/source.go:14,25`, `topic.go:14,23,231-235`): `Name: "devbox"` → `"dwe"`; tie-break ordering literal `"devbox"` → `"dwe"`.
- Tooling config: `.golangci.yml:51` `goimports.local-prefixes`, `.goreleaser.yaml:155` `release.github.name`, `Makefile:1,3` `BINARY_NAME` + `MODULE`.
- Drop `schema_version` entirely + gate symbols `internal/core/project/project.ValidateSchema`, `SchemaField`, `SupportedSchema` (YAGNI).

Rejected alternatives:
- `WORKSPACE_*` env vars — too generic.
- `devworkspace.yml` — collides with Eclipse Che DevWorkspace.
- Keeping `devbox-docs://` URI scheme for backward stability — no users have cached llms output, clean break preferred.
- Splitting Task 4 into 4a/4b/4c — chose dry-run/precheck gates instead (simpler, atomic phase preserved).

Re-release as v0.1.0 (same number, no users, new module path means no Go proxy cache concerns).

## Context (from discovery)

- **Repo:** `github.com/semsemyonoff/devbox`, Go 1.26, main branch.
- **Quantitative footprint (2026-06-01 audit):**
  - **570 Go files** match `devbox` (4281 lines).
  - **53 RU markdown files** under `docs/i18n/ru/`.
  - **6 YAML files**.
  - **3 root `.md` files**.
  - **3 files in Makefile/scripts/**.
- **Release infrastructure:** `.goreleaser.yaml` (23+ `devbox` occurrences including `release.github.name: devbox` at line ~155), `.github/workflows/release.yml` (uses secret `HOMEBREW_TAP_GITHUB_TOKEN`).
- **Tooling config:** `.golangci.yml:51 goimports.local-prefixes: github.com/semsemyonoff/devbox`. `Makefile:1 BINARY_NAME := devbox`, `Makefile:3 MODULE := github.com/semsemyonoff/devbox`.
- **AGENTS.md is canonical**, CLAUDE.md is a symlink.
- **Embedded docs subsystem:** `internal/core/docs/embedded/` (gitignored) + `internal/core/docs/content_hashes_gen.go`. Regenerated by `make build` / `make test`.
- **Prompt hot path:** `cmd/devbox/main.go` short-circuits before cobra for `devbox prompt`.
- **Auto-generated CLI reference:** `docs/reference/cli/devbox_*.md` (~104 files).
- **Shell completions:** `completions/devbox.{bash,zsh,fish}` (generated, shipped via `.goreleaser.yaml`).
- **Runtime state dir:** `.devbox/deploy/deploy.lock`, `.devbox/snapshots/snapshot.lock`, `.devbox/logs/parallel/workflow/`, `.devbox/config`.
- **Docker daemon labels:** `internal/shared/daemon/daemon.go:23-25` (`LabelProject`, `LabelDaemonID`, `LabelDaemonParams`).
- **Snapshot archive schema:** `internal/core/workflow/snapshot/meta/paths.go:14 stateSubdir = ".devbox/snapshots"` (runtime dir, Phase 3b owns), `paths.go:28 DevboxSubdir = "devbox"` (archive subdir, Phase 3f owns), `meta/manifest.go:107-115 DevboxVersion`/`DevboxFiles`, `internal/core/workflow/snapshot/devbox_files.go` (entire file rename, Phase 3f owns).
- **Env-var contract:** `DEVBOX_BIN`, `DEVBOX_ROOT`, `DEVBOX_COMMAND_ID`, `DEVBOX_TEMP_DIR`, `DEVBOX_NONINTERACTIVE`, `DEVBOX_PARAMS_JSON`, `DEVBOX_CONTEXT_JSON`, `DEVBOX_FILES_JSON`.
- **YAML enum** `type: devbox`. Go const `CommandTypeDevbox = "devbox"` at `internal/core/usercommands/model/types.go:36`, plus ~10 alias re-exports (`usercommands/usercommands.go:79`, four `testaliases_test.go` files, `runner.go:113`, `types.go:78,738,810,834`, `cli/docs/generate.go:452`, `cli/command/inspect.go:100,274`).
- **Go identifiers:** `DevboxConfig`, `DevboxBin` (AGENTS.md critical pattern), `BinaryOverride("devbox")`, `binary_devbox`.
- **User-visible brand-string cluster** (Phase 3g): `internal/core/notify/{native,native_darwin,native_other}.go`, `internal/core/ui/render/brand_header.go:32`, `internal/cli/docs/{docs,generate}.go`, `internal/cli/command/list.go`, `internal/core/docs/llmstxt/generator.go` (prose + `devbox-docs://` URI scheme), `internal/core/docs/{source,topic}.go`, `internal/cli/root.go:124 Use: "devbox"`.
- **Template-pack paths** use `filepath.Join(absRoot, "devbox", "templates", ...)` — multi-arg join, won't be caught by `rg '"devbox/"'`. Files: `internal/core/execution/templates/{ide,ai,git,packroot}/*.go` + their `_test.go`.
- **`internal/shared/i18n/translations/en.yml`** is the baked-in EN translations (via `//go:embed`). Spot-check: empty for `devbox|Devbox`. Confirmed clean but should be audited explicitly.
- **`LICENSE`** has no `devbox` reference (copyright only). Confirmed clean.
- **schema_version:** root config + gate symbols at `internal/core/project/project/project.go:14-23` + test fixtures `internal/core/validate/config/testdata/devbox-{legacy,missing}-schema/`. All deleted, not renamed.
- **Test-name dirs:** `internal/core/validate/config/testdata/devbox-v2-{good,bad-keys}/` are test IDs, NOT product references.
- **Claude plugin:** `.claude-plugin/{plugin,marketplace}.json`, `skills/devbox/SKILL.md`, `skills/devbox/references/recipes.md`.
- **Project.Prefix:** audit during Task 4.
- **i18n namespaces:** project-local YAML at `devbox/i18n/<lang>.yml` (path rename to `workspace/i18n/<lang>.yml`); long-form markdown at `docs/i18n/<lang>/reference/` and `docs/i18n/<lang>/internals/` (layout unchanged).
- **Memory:** `~/.claude/projects/.../memory/project_rename_devbox_to_dwe.md`.

## Development Approach

- **Testing approach:** Regular (mechanical rename + golden fixture updates).
- **Atomic phases:** every Phase ends green on `make test`. Fixtures/golden files for a surface are renamed in the SAME task as the code that consumes them.
- **Dry-run gates before mass rename:** Task 4 (heaviest fixture migration) prints the file lists from `find` BEFORE running `git mv`, to catch any test-ID dir leaks.
- **File ownership in cross-phase areas:** when a single file is touched by multiple phases (e.g. `internal/core/workflow/snapshot/meta/paths.go`), each phase owns specific lines. Documented per-task.
- Use `git mv` for directory renames (preserves history). `git mv` of a file with content edits in the same phase = single commit, rename detection survives.
- Use `gofmt`/`goimports` after bulk sed replacements.
- Backwards compatibility is NOT required.

## Testing Strategy

- **Unit tests:** must pass after every phase. Each phase folds in its own fixture/golden updates.
- **Integration:** `make test-race` after Phase 7.
- **Build:** `make build` after every phase that touches code.
- **Lint:** `make lint` finally — `golangci-lint`.
- **Manual smoke test** (Task 16) exercises Phase 3c/3d/3f surfaces explicitly (daemon labels, env vars, snapshot schema).

## Progress Tracking

- Mark completed items with `[x]` immediately.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update the plan if scope changes during execution.

## Solution Overview

Phased migration ordered by increasing visibility:

1. **Phase 0** — audit.
2. **Phase 1** — Go module + cmd folder + Makefile BINARY_NAME/MODULE + cobra root `Use:` + .golangci.yml goimports prefix + main.go prompt hot path.
3. **Phase 2** — shell completions + install code.
4. **Phase 3** — atomic project-schema and runtime rename, split by surface:
   - **3a** workspace.yml/workspace/ + Go identifiers + drop schema_version + fixtures.
   - **3b** `.devbox/` runtime dir + tests (owns `meta/paths.go:14` only).
   - **3c** Docker daemon labels + daemon tests + status golden files.
   - **3d** `DEVBOX_*` env vars + tests.
   - **3e** YAML enum `type: devbox` + `CommandTypeDevbox` const + aliases + fixtures.
   - **3f** Snapshot archive schema (owns `meta/paths.go:28` + `devbox_files.go` rename + content + manifest fields).
   - **3g** User-visible brand strings (notify, render header, browser titles, llms.txt prose + URI scheme, docs root identifier).
5. **Phase 4** — docs EN + RU + auto-generated CLI ref regeneration.
6. **Phase 5** — narrative (AGENTS.md, README.md, packages.md) + Claude plugin + skills/.
7. **Phase 6** — release config (.goreleaser.yaml + GitHub workflows).
8. **Phase 7** — final validation + concrete smoke test.
9. **Post-Completion** — release operations.

## Technical Details

### Go module rename mechanics

```
git mv cmd/devbox cmd/dwe      # FIRST, before content sed
go mod edit -module github.com/semsemyonoff/dwe
find . -type f -name '*.go' \
  -not -path './internal/core/docs/embedded/*' \
  -print0 | xargs -0 sed -i '' 's|github.com/semsemyonoff/devbox|github.com/semsemyonoff/dwe|g'
go mod tidy
goimports -w .
```

### Phase 3b vs 3f file ownership (cross-phase conflict resolution)

`internal/core/workflow/snapshot/meta/paths.go`:
- **3b owns line 14:** `stateSubdir = ".devbox/snapshots"` → `".dwe/snapshots"` (runtime dir).
- **3f owns line 28:** `DevboxSubdir = "devbox"` → `WorkspaceSubdir = "workspace"` (archive subdir).
- Lines are independent; each phase edits its own line, leaves the other untouched.

`internal/core/workflow/snapshot/devbox_files.go`:
- **3f owns entirely** — `git mv devbox_files.go workspace_files.go` + all content edits (path joins, internal func names, godoc).
- **3b does NOT touch this file.**

`internal/core/workflow/snapshot/create.go`, `create_test.go`:
- **3b** touches references to `.devbox/deploy/state.yml` (runtime path).
- **3f** touches references to `<snap>/devbox/...` (archive subdir).
- Edits are line-disjoint; both phases can touch but only their own lines.

### Drop schema_version field — full enumeration

- Remove `SchemaVersion` struct field + yaml tag from root config (`internal/core/project/config/workspace.go`).
- Delete validator function in `internal/core/validate/config/` (including the Hint `"Ensure devbox.yml has schema_version: \"2\""`).
- Delete gate symbols at `internal/core/project/project/project.go:14-23`: `ValidateSchema`, `SchemaField`, `SupportedSchema`.
- Delete test fixtures `internal/core/validate/config/testdata/devbox-legacy-schema/`, `devbox-missing-schema/`.
- Delete corresponding subtests (`internal/core/validate/config/{devbox_test,all_test}.go`, `internal/core/project/project_test.go`).
- Remove `schema_version` from `docs/reference/config/workspace.md` (renamed) + RU equivalent + every code-block example.
- Strip `schema_version` lines from every fixture `workspace.yml`.

### Docker daemon labels — what changes

`internal/shared/daemon/daemon.go:23-25`:

```go
LabelProject      = "dwe.project"
LabelDaemonID     = "dwe.daemon.id"
LabelDaemonParams = "dwe.daemon.params"
```

Plus godoc at `daemon.go:77,151`. Status/inspect golden files. Auto-reap label filters.

### Snapshot archive schema — what changes (Phase 3f)

`meta/paths.go:28`: `const WorkspaceSubdir = "workspace"` (was `DevboxSubdir = "devbox"`).

`meta/manifest.go:107-115`:
```go
DweVersion     string         `yaml:"dwe_version,omitempty" json:"dwe_version,omitempty"`
WorkspaceFiles WorkspaceFiles `yaml:"workspace_files,omitempty" json:"workspace_files,omitzero"`
```

`git mv internal/core/workflow/snapshot/devbox_files.go internal/core/workflow/snapshot/workspace_files.go` + internal func renames `restoreDevboxFiles` → `restoreWorkspaceFiles` + path joins use `meta.WorkspaceSubdir`.

Brand/schema gradient: captured-config subdir + captured-files field = schema mirror; tool-version field = brand.

### Runtime dir rename `.devbox/` → `.dwe/`

Code sites: `internal/shared/lock/project.go`, `internal/core/validate/env/{env_test,project_perms}.go`, `internal/core/usercommands/runtime/runners/workflow/log.go`, `internal/core/execution/pipeline/logging{,_test}.go`, `internal/core/project/user/{load,load_test}.go`, `internal/core/project/config/workspace.go` (path godoc), `internal/core/workflow/snapshot/meta/paths.go:14` (stateSubdir), `internal/core/workflow/snapshot/create.go` + `create_test.go` (`.devbox/deploy/state.yml` paths). Plus `.gitignore`, `~/.config/devbox/` (user-level config).

### Env-var contract `DEVBOX_*` → `DWE_*`

Set: `internal/core/usercommands/runtime/runners/host/host.go:114`, `runtime/runners/script/script.go:30+`. Consumers: `internal/core/notify/factory.go:30`, `runtime/internal/runio/runio.go:73`. Docs: `docs/reference/config/commands/{types,directives,validation,index}.md`.

### YAML enum + Go const (Phase 3e)

Decode at `internal/core/project/config/workspace.go:2052` (`case "shell", "devbox":` → `"dwe"`). Runner type `DevboxRunner` → `DweRunner`. UI style key in `internal/core/ui/cmdbrowser/styles.go:23`.

Go const **`CommandTypeDevbox`** in `internal/core/usercommands/model/types.go:36` → **`CommandTypeDwe = "dwe"`**. Alias re-exports (~10): `usercommands/usercommands.go:79`, four `testaliases_test.go`, `runner.go:113`, `types.go:78,738,810,834`, `cli/docs/generate.go:452`, `cli/command/inspect.go:100,274`.

### User-visible brand strings (Phase 3g) — full enumeration

These ship in the binary and print to users. The first review missed this entire surface.

**Notify package:**
- `internal/core/notify/native_darwin.go:28` `const terminalNotifierGroup = "Devbox"` → `"DWE"`.
- `internal/core/notify/native_darwin.go:76` `os.CreateTemp("", "devbox-notify-*.png")` → `"dwe-notify-*.png"`.
- `internal/core/notify/native_other.go:8` `beeep.AppName = "Devbox"` → `"DWE"`.
- `internal/core/notify/native.go:100` notification title prefix `"Devbox"` → `"DWE"`.

**Render & UI chrome:**
- `internal/core/ui/render/brand_header.go:32` brand header `"Devbox"` → `"DWE"` (prints at top of `dwe status`/`dwe info`).
- `internal/cli/docs/docs.go:38` `parts := []string{"Devbox"}` → `"DWE"` (docs browser title).
- `internal/cli/command/list.go:232,234,237` `"Devbox"` selector title prefix → `"DWE"`.

**Generated docs prose:**
- `internal/cli/docs/generate.go:307,706,760-764` generated CLI reference titles `# devbox …` → `# dwe …`. Re-emitted by `bin/dwe docs generate` so they bleed into `docs/reference/cli/dwe_*.md`.

**llms.txt generator:**
- `internal/core/docs/llmstxt/generator.go:56-73,189,200` `# devbox` heading + prose + URI scheme `devbox-docs://` → `dwe-docs://` (clean break — confirmed by user, no cached llms.txt exists).
- Update all corresponding asserts in `generator_test.go`.

**Docs source root:**
- `internal/core/docs/source.go:14,25` `Name: "devbox"` → `"dwe"`.
- `internal/core/docs/topic.go:14,23,231-235` tie-break ordering literal `"devbox"` → `"dwe"`.

### Cobra root command Use:

`internal/cli/root.go:124` `Use: "devbox"` → `"dwe"` (controls `Usage: …` in `--help`). Owned by Phase 1 (Task 2) since it's tightly coupled to the binary rename.

### Makefile BINARY_NAME and MODULE

`Makefile:1` `BINARY_NAME := devbox` → `dwe`. `Makefile:3` `MODULE := github.com/semsemyonoff/devbox` → `github.com/semsemyonoff/dwe`. Owned by Phase 1.

### Template-pack paths (multi-arg filepath.Join)

`internal/core/execution/templates/{ide,ai,git,packroot}/*.go` + `_test.go` use `filepath.Join(absRoot, "devbox", "templates", ...)` (8 files). Won't be caught by `rg '"devbox/"'` — need `rg '"devbox"' internal/core/execution/templates/`. Owned by Phase 3a (Task 4) — folder schema.

### Prompt hot path

`cmd/dwe/main.go` after rename: help-text `devbox prompt --check` → `dwe prompt --check`; `main_test.go` argv[0] `"devbox"` → `"dwe"`.

### Shell completions

Regenerate from `bin/dwe completion <shell>`, delete `completions/devbox.*`, update `scripts/gen-completions.sh`, `internal/cli/completion/{install,uninstall}.go` (dest names + temp prefixes), update completion tests.

### Claude plugin metadata

`.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json`, `git mv skills/devbox skills/dwe`, update `skills/dwe/SKILL.md` (34 mentions) + `references/recipes.md`.

### Embedded docs regeneration

```
make build         # invokes sync-embedded-docs.sh + gen-docs-content-hashes.sh
```

### CLI reference regeneration

```
rm docs/reference/cli/devbox.md docs/reference/cli/devbox_*.md
bin/dwe docs generate --scope cli
```

`devbox.md` (root command page) is a separate file from the `devbox_*.md` glob and must be removed explicitly. `--scope cli` is required because the default `--scope all` requires a project root (commands scope check at `internal/cli/docs/generate.go:59`).

### Tooling config

`.golangci.yml:51` `goimports.local-prefixes: github.com/semsemyonoff/devbox` → `dwe`.

### Release config (.goreleaser.yaml)

23+ occurrences enumerated in Task 15. Includes `release.github.name: devbox` at line ~155.

### Actual workflow secret name

`HOMEBREW_TAP_GITHUB_TOKEN`. PAT re-issue policy: **always re-issue after `gh repo rename`** (cheap insurance, simpler than guessing PAT scope).

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): file changes inside this repository.
- **Post-Completion** (no checkboxes): GitHub repo rename, tag/release deletion, homebrew-tap update, push of the new tag.

## Implementation Steps

### Task 1: Phase 0 — Baseline audit and clean working tree

**Files:**
- Modify: `docs/plans/2026-06-01-rebrand-to-dwe.md` (this file)

- [x] confirm `git status` clean; commit/stash pre-existing edits
- [x] run `make test` on baseline — record green/red (one pre-existing Docker-dependent failure: TestDeployRunCmd_LockHeldBlocksDeploy requires Docker daemon; all other tests pass)
- [x] capture audit counts:
  - [x] `rg -l --hidden devbox` (total) — 848 files
  - [x] `rg -l --hidden Devbox` (PascalCase) — 351 files
  - [x] `rg -l --hidden DEVBOX` (UPPER) — 45 files
  - [x] `rg -l --hidden 'github.com/semsemyonoff/devbox'` — 499 files
- [x] grep schema/runtime surfaces:
  - [x] `rg -l --hidden 'schema_version'` — 69 files
  - [x] `rg -l --hidden '\.devbox/'` — 100 files
  - [x] `rg -l --hidden 'devbox\.project|devbox\.daemon'` (Docker labels) — 16 files enumerated
  - [x] `rg -l --hidden 'DevboxSubdir|DevboxFiles|DevboxVersion|devbox_version|devbox_files'` (snapshot schema) — 23 files enumerated
- [x] grep user-visible brand-string sites (Phase 3g):
  - [x] `rg -n '"Devbox"' --type go` — notify, brand_header, docs browser, command list, tests
  - [x] `rg -n 'devbox-docs://' --type go` — generator.go:189 + generator_test.go:115
  - [x] `rg -n 'CommandTypeDevbox' --type go` — ~15 sites enumerated
  - [x] `rg -n 'Use:\s*"devbox"' --type go` — root.go:124 + 13 test files
  - [x] `rg -n 'Name:\s*"devbox"' --type go` — source.go:25 + many test files
  - [x] `rg -n 'beeep.AppName|terminalNotifierGroup' --type go` — native_darwin.go:28, native_other.go:8
- [x] grep tooling/release:
  - [x] `rg -n 'devbox' .golangci.yml .goreleaser.yaml .github/` — goreleaser: 23+ occurrences; github: 1 occurrence
  - [x] `rg -n 'BINARY_NAME|MODULE' Makefile` — line 1 BINARY_NAME, line 3 MODULE
- [x] grep user-config + binary lookup:
  - [x] `rg -n 'binary_devbox|BinaryOverride\("devbox"\)'` — config/devbox.go:28
- [x] grep template-pack paths (multi-arg join):
  - [x] `rg -n '"devbox"' internal/core/execution/templates/` — 8 source files + test files enumerated
- [x] grep `cmd/devbox` and `bin/devbox` in non-Go (Makefile, scripts, workflows) — Makefile, scripts/gen-completions.sh, README.md
- [x] grep `Project.Prefix` default — `"devbox"` NOT present; Prefix has no hardcoded default (empty string)
- [x] confirm `internal/shared/i18n/translations/en.yml` has no `devbox|Devbox` — confirmed clean
- [x] confirm `LICENSE` has no `devbox` references (currently confirmed clean)
- [x] add ➕ task for any unexpected area — no unexpected areas found; all surfaces already covered by existing tasks

### Task 2: Phase 1 — Rename Go module + cmd folder + binary + tooling config + cobra root + command-path gates

**Files:**
- Modify: `go.mod`
- Rename: `cmd/devbox/` → `cmd/dwe/`
- Modify: every `*.go` with `github.com/semsemyonoff/devbox/...` imports
- Modify: `Makefile` (BINARY_NAME, MODULE, cmd path, bin path), `scripts/sync-embedded-docs.sh`, `scripts/gen-docs-content-hashes.sh`, `.gitignore`
- Modify: `.golangci.yml` (goimports `local-prefixes`)
- Modify: `internal/cli/root.go` lines 124 (`Use:`), 264-275 (`allowedWithoutProject` — hardcoded `devbox ...` command-path prefixes), 277-283 (`isValidateCommand` — same), root `Short`/`Long`
- Modify: any test constructing `cobra.Command{Use: "devbox"}` (grep first)

**CRITICAL:** `allowedWithoutProject` (root.go:268-275) and `isValidateCommand` (root.go:280-283) compare `cmd.CommandPath()` against literal `"devbox"`, `"devbox version"`, `"devbox prompt"`, `"devbox completion"`, `"devbox docs"`, `"devbox validate"`. After `Use:` rename, `dwe` command paths won't match — basic dispatch breaks. These must be updated in the same task.

- [x] `git mv cmd/devbox cmd/dwe` **first** (preserve rename detection)
- [x] `go mod edit -module github.com/semsemyonoff/dwe`
- [x] `find . -type f -name '*.go' -not -path './internal/core/docs/embedded/*' -print0 | xargs -0 sed -i '' 's|github.com/semsemyonoff/devbox|github.com/semsemyonoff/dwe|g'`
- [x] `go mod tidy && goimports -w .`
- [x] update `.golangci.yml:51` `goimports.local-prefixes`
- [x] update `Makefile:1` `BINARY_NAME := devbox` → `dwe`
- [x] update `Makefile:3` `MODULE := github.com/semsemyonoff/devbox` → `github.com/semsemyonoff/dwe`
- [x] update Makefile path references `cmd/devbox` → `cmd/dwe`, `bin/devbox` → `bin/dwe`
- [x] update `scripts/sync-embedded-docs.sh`, `scripts/gen-docs-content-hashes.sh`, `.gitignore`
- [x] update `internal/cli/root.go:124` `Use: "devbox"` → `Use: "dwe"`
- [x] update `internal/cli/root.go:268-275` `allowedWithoutProject` — replace every `"devbox"` / `"devbox version"` / `"devbox prompt"` / `"devbox completion"` / `"devbox docs"` literal with `"dwe"` equivalents
- [x] update `internal/cli/root.go:280-283` `isValidateCommand` — replace `"devbox validate"` / `"devbox validate "` with `"dwe validate"` / `"dwe validate "`
- [x] update `root.go` godoc references (line 265 `devbox.yml` mention, line 285 `applyStyles` godoc)
- [x] update root command `Short`/`Long` text in `root.go` (search for `"devbox"` strings in `cobra.Command{...}` literals)
- [x] **grep tests:** `rg -n 'Use:\s*"devbox"' --type go` → update each (typically `cobra.Command{Use: "devbox"}` test fixtures)
- [x] update `cmd/dwe/main.go`: prompt hot-path text hints, godoc
- [x] update `cmd/dwe/main_test.go`: argv[0] `"devbox"` → `"dwe"`
- [x] `make build && make test && make lint` — all green (lint verifies goimports re-grouping)

### Task 3: Phase 2 — Shell completions

**Files:**
- Rename+regenerate: `completions/devbox.{bash,zsh,fish}` → `completions/dwe.{bash,zsh,fish}`
- Modify: `scripts/gen-completions.sh`, `internal/cli/completion/{install,uninstall,install_test,uninstall_test}.go`

- [x] delete `completions/devbox.{bash,zsh,fish}`
- [x] regenerate: `bin/dwe completion bash > completions/dwe.bash` (zsh, fish)
- [x] update `scripts/gen-completions.sh`
- [x] update `install.go`: `_devbox` → `_dwe`, `devbox.fish` → `dwe.fish`, `devbox-completion.ps1` → `dwe-completion.ps1`, temp prefix `.devbox-completion-*` → `.dwe-completion-*`
- [x] update `uninstall.go` mirrors
- [x] update completion tests
- [x] `make build && make test`

### Task 4: Phase 3a — Project schema + Go identifiers + drop schema_version + atomic fixture migration

**Heaviest task. Dry-run gates BEFORE mass mv.**

**Files:**
- Rename: `internal/core/project/config/devbox.go` → `workspace.go`
- Modify: `internal/core/project/project/project.go:14-23` (delete gate symbols)
- Modify: `internal/core/validate/config/` (drop schema_version validator)
- Delete: `internal/core/validate/config/testdata/devbox-{legacy,missing}-schema/`
- Modify: `internal/shared/i18n/` (path)
- Modify: every code site with `"devbox.yml"`, `"devbox/"`, `DevboxConfig`, `DevboxBin`, `BinaryOverride("devbox")`, `binary_devbox`
- Modify: `internal/core/execution/templates/{ide,ai,git,packroot}/*.go` (multi-arg `filepath.Join(…, "devbox", "templates", …)`)
- Rename: every `testdata/*/devbox.yml` → `workspace.yml` (EXACT name)
- Rename: every `testdata/*/devbox/` folder → `workspace/` (EXACT name)
- Modify: every fixture YAML — strip `schema_version` lines
- Modify: tests hardcoding `"devbox.yml"`/`"devbox/"`

**Step-by-step with dry-run gates:**

- [x] `git mv internal/core/project/config/devbox.go internal/core/project/config/workspace.go`
- [x] rename Go struct `DevboxConfig` → `DweConfig`, accessor `DevboxBin` → `DweBin` (gopls or sed)
- [x] update default fallback inside `DweBin` (`"devbox"` → `"dwe"`)
- [x] update `BinaryOverride("devbox")` → `BinaryOverride("dwe")` callers
- [x] update user-config key `binary_devbox` → `binary_dwe`
- [x] replace literal strings: `"devbox.yml"` → `"workspace.yml"`, `"devbox/"` (folder constants) → `"workspace/"`
- [x] update template-pack multi-arg joins: `rg -n '"devbox"' internal/core/execution/templates/` then replace `filepath.Join(…, "devbox", "templates", …)` → `filepath.Join(…, "workspace", "templates", …)` in each of the 8 files
- [x] update `internal/cli/root.go:297` `filepath.Join(projectRoot, "devbox", "styles.yml")` → `filepath.Join(projectRoot, "workspace", "styles.yml")` (multi-arg join, not caught by literal `"devbox/"` grep)
- [x] update `internal/shared/prompt/prompt.go:18` const `configFilename = "devbox.yml"` → `"workspace.yml"` and `:20` const `stylesRelPath = "devbox/styles.yml"` → `"workspace/styles.yml"` (prompt hot path, AGENTS.md critical pattern — note line :19 `stateRelPath` belongs to Task 5)
- [x] update prompt tests `internal/shared/prompt/*_test.go` matching the consts above
- [x] update i18n loader path: `devbox/i18n/<lang>.yml` → `workspace/i18n/<lang>.yml`
- [x] **drop schema_version completely:** remove `SchemaVersion` struct field + yaml tag; delete validator + Hint; delete gate symbols at `internal/core/project/project/project.go:14-23` (`ValidateSchema`, `SchemaField`, `SupportedSchema`); remove godoc; delete legacy fixture dirs (`git rm -r internal/core/validate/config/testdata/devbox-legacy-schema/ devbox-missing-schema/`); delete subtests in `internal/core/validate/config/{devbox_test,all_test}.go` and `internal/core/project/project/project_test.go`
- [x] **rename root config validator** (`devboxValidator` → `workspaceValidator`):
  - [x] `internal/core/validate/config/devbox.go:33` `type devboxValidator struct{}` → `workspaceValidator`; `ID() string` returns `"workspace"` (was `"devbox"`); `Domain()` stays `"config"`
  - [x] consider `git mv internal/core/validate/config/devbox.go internal/core/validate/config/workspace.go` (filename alignment)
  - [x] update diagnostic targets `config.devbox*` → `config.workspace*` throughout this file
  - [x] `internal/core/validate/config/all.go:10` registration `&devboxValidator{}` → `&workspaceValidator{}`
  - [x] `internal/cli/validate/validate.go:142-144` Cobra help text: `devbox validate config <devbox|services|...>` → `dwe validate config <workspace|services|...>` (the `devbox` validator ID is now `workspace`)
  - [x] update `internal/core/validate/config/devbox_test.go` (or renamed test file) — every fixture/golden expecting `Target: "config.devbox"` etc.
- [x] audit `Project.Prefix` default; replace `"devbox"` → `"dwe"` if found
- [x] **DRY-RUN GATE — fixture file list:** `find . -path '*/testdata/*' -type f -name 'devbox.yml' -print` — visually confirm output (review every path before mv)
- [x] **DRY-RUN GATE — fixture dir list:** `find . -path '*/testdata/*' -type d -name 'devbox' -print` — visually confirm NO `devbox-v2-*`/`devbox-legacy-*`/`devbox-missing-*` appears (those are test IDs or deleted)
- [x] execute mass `git mv`: for each path in the two find outputs, `git mv <path> <path with workspace>`
- [x] strip schema_version: `rg -l 'schema_version' --type yaml` → delete those lines
- [x] update test literals: `rg -l '"devbox\.yml"' -t go` → replace with `"workspace.yml"`
- [x] **PRECHECK before make test:** `rg -n '"devbox\.yml"' --type go` returns zero
- [x] `make build && make test` — must end **green**

### Task 5: Phase 3b — Runtime dir `.devbox/` → `.dwe/`

**File ownership note:** Task 5 owns only line 14 (`stateSubdir`) of `internal/core/workflow/snapshot/meta/paths.go`. Line 28 (`DevboxSubdir`) is owned by Task 9. Task 5 does NOT touch `internal/core/workflow/snapshot/devbox_files.go` — fully owned by Task 9.

**Files:**
- Modify: `internal/shared/lock/project.go`
- Modify: `internal/core/validate/env/{env_test,project_perms}.go`
- Modify: `internal/core/usercommands/runtime/runners/workflow/log.go`
- Modify: `internal/core/execution/pipeline/logging.go`, `logging_test.go`
- Modify: `internal/core/project/user/load.go`, `load_test.go`
- Modify: `internal/core/project/config/workspace.go` (path godoc only)
- Modify: `internal/core/workflow/snapshot/meta/paths.go` (line 14 only)
- Modify: `internal/core/workflow/snapshot/create.go`, `create_test.go` (`.devbox/deploy/state.yml` runtime refs only)
- Modify: `internal/core/usercommands/runtime/runners/workflow/parallel_test.go`
- Modify: `internal/shared/prompt/prompt.go:19` const `stateRelPath = ".devbox/deploy/state.yml"` → `.dwe/deploy/state.yml"` (other prompt.go consts at :18 and :20 belong to Task 4)
- Modify: `internal/core/docs/mermaid/cache.go:202,206,209` — `CacheDir()` multi-arg joins (`filepath.Join(xdg, "devbox", "mermaid")`, `filepath.Join(userCache, "devbox", "mermaid")`, `filepath.Join(os.TempDir(), "devbox-mermaid")`) → `dwe` equivalents. Also update godoc lines 196-198 mentioning `devbox/mermaid` and `devbox-mermaid` paths. User-visible cache dir.
- Modify: `internal/core/docs/tui/diagram_inline.go:48,171` — same cache namespace
- Modify: `.gitignore` (`.devbox/` → `.dwe/`)
- Modify: any `~/.config/devbox/` → `~/.config/dwe/` references

- [x] `rg -n '\.devbox' --type go` — enumerate; replace `.devbox/` → `.dwe/`, `.devbox` (literal) → `.dwe`
- [x] specifically verify `paths.go` edit ONLY changes line 14; line 28 stays unchanged
- [x] confirm `devbox_files.go` is NOT modified in this task
- [x] `rg -n 'config/devbox' --type go` for XDG path; replace with `config/dwe`
- [x] update mermaid `CacheDir()`: 3 `filepath.Join` literals + godoc; `rg -n 'devbox' internal/core/docs/mermaid/`
- [x] update `internal/core/docs/tui/diagram_inline.go:48,171` cache references
- [x] update `internal/shared/prompt/prompt.go:19` stateRelPath
- [x] update `.gitignore`
- [x] verify no testdata fixtures hardcode `.devbox/` as input data
- [x] `make build && make test` — must end **green**

### Task 6: Phase 3c — Docker daemon labels

**Note on golden file overlap with Task 8 (Phase 3e):** if a status golden file shows both daemon labels AND `type: devbox` commands, Task 6 updates the label portion; Task 8 will re-touch the same file for the type discriminator. After each task, run `go test` for the affected package; if golden mismatch persists due to the other phase's pending work, use `go test -update` (if supported) or hand-edit only the label-related lines in Task 6.

**Files:**
- Modify: `internal/shared/daemon/daemon.go:23-25` (label constants), `:77,151` (godoc)
- Modify: daemon tests, status golden files mentioning `devbox.project`/`devbox.daemon.id`/`devbox.daemon.params`
- Modify: auto-reap logic if it filters by label string (`internal/core/workflow/deploy/`)
- Modify: docs `docs/reference/config/commands/types.md`, `directives.md` daemon sections; same in RU

- [x] rename label constants: `LabelProject = "dwe.project"`, etc.
- [x] update godoc refs in `daemon.go`
- [x] `rg -n 'devbox\.project|devbox\.daemon\.id|devbox\.daemon\.params'` — find every consumer (tests, golden files, completion suggestions, docs)
- [x] update docs that show `docker inspect` output with `devbox.*` labels
- [x] `make build && make test` — must end **green**

### Task 7: Phase 3d — Env-var contract `DEVBOX_*` → `DWE_*`

**Files:**
- Modify: `internal/core/usercommands/runtime/runners/host/host.go:114`, `runtime/runners/script/script.go:30+`
- Modify: `internal/core/notify/factory.go:30`, `runtime/internal/runio/runio.go:73`
- Modify: tests asserting env-var names
- Modify: any fixture YAML with `${DEVBOX_*}` references

- [x] `rg -n 'DEVBOX_' --type go` — full enumeration
- [x] bulk replace `DEVBOX_BIN`/`ROOT`/`COMMAND_ID`/`TEMP_DIR`/`NONINTERACTIVE`/`PARAMS_JSON`/`CONTEXT_JSON`/`FILES_JSON` → `DWE_*`
- [x] update tests pinning env-var names
- [x] `rg -n 'DEVBOX_' --type yaml` — update fixture references
- [x] `make build && make test` — must end **green**

### Task 8: Phase 3e — YAML enum `type: devbox` + `CommandTypeDevbox` const + alias re-exports

**Files:**
- Modify: `internal/core/project/config/workspace.go` (decoder case)
- Rename: Go const `CommandTypeDevbox` → `CommandTypeDwe = "dwe"` in `internal/core/usercommands/model/types.go:36`
- Modify: ~10 alias re-export sites — `internal/core/usercommands/usercommands.go:79`, four `testaliases_test.go` files, `internal/core/usercommands/runtime/runner.go:113`, `usercommands/model/types.go:78,738,810,834`, `internal/cli/docs/generate.go:452`, `internal/cli/command/inspect.go:100,274`
- Rename: Go type `DevboxRunner` → `DweRunner` in `internal/core/usercommands/runtime/runners/host/`
- Modify: `internal/core/ui/cmdbrowser/styles.go:23` (`case "devbox":`)
- Modify: `internal/core/project/config/devbox_test.go` (~20 hits; rename file → `workspace_test.go`)
- Modify: `internal/core/usercommands/model/types_test.go`, `action_test.go`
- Modify: all fixture YAML with `type: devbox`

- [x] `rg -n 'case "shell", "devbox"' --type go` → replace with `"dwe"`
- [x] rename `CommandTypeDevbox` → `CommandTypeDwe` (gopls rename for cross-package safety)
- [x] update all ~10 alias re-export sites
- [x] rename `DevboxRunner` → `DweRunner`
- [x] update UI style discriminator key
- [x] `rg -l 'type: devbox' --type yaml` → bulk replace with `type: dwe`
- [x] update tests asserting on the discriminator
- [x] `make build && make test` — must end **green**

### Task 9: Phase 3f — Snapshot archive schema (owns `meta/paths.go:28` + `devbox_files.go`)

**Files:**
- Modify: `internal/core/workflow/snapshot/meta/paths.go` (line 28 only — `DevboxSubdir` constant)
- Modify: `internal/core/workflow/snapshot/meta/manifest.go` (`DevboxVersion` → `DweVersion`/`yaml:"dwe_version"`; `DevboxFiles` type + field → `WorkspaceFiles`/`yaml:"workspace_files"`)
- Rename: `internal/core/workflow/snapshot/devbox_files.go` → `workspace_files.go` (full content edit + internal func renames)
- Modify: `internal/core/workflow/snapshot/create.go`, `create_test.go` (archive subdir refs only — runtime refs were done in Task 5)
- Modify: `internal/cli/snapshot/` inspect output golden files
- Modify: docs `docs/reference/snapshot.md` and RU equivalent (archive layout)

- [x] rename constant: `DevboxSubdir` → `WorkspaceSubdir`, value `"devbox"` → `"workspace"`
- [x] rename type + fields: `DevboxFiles` struct → `WorkspaceFiles`; manifest field + tag `devbox_files` → `workspace_files`; `DevboxVersion` field + tag `devbox_version` → `dwe_version`
- [x] `git mv internal/core/workflow/snapshot/devbox_files.go internal/core/workflow/snapshot/workspace_files.go`
- [x] rename internal funcs (`restoreDevboxFiles` → `restoreWorkspaceFiles`)
- [x] update path joins (`meta.WorkspaceSubdir`) in capture + restore code
- [x] regenerate snapshot golden files OR hand-update fixture archives' `manifest.yml` entries
- [x] update godoc throughout `internal/core/workflow/snapshot/`
- [x] `make build && make test` — must end **green**

### Task 10: Phase 3g — User-visible brand strings + docs source discriminators + Cobra help text

**Why this exists:** the brand strings in this task ship in the compiled binary and are printed to users. Without this task, `bin/dwe status` would print `Devbox` in its header, macOS notifications would arrive from `Devbox`, `dwe docs llms-txt` would emit `# devbox` + `devbox-docs://` URI scheme, and `dwe docs export` would silently return nothing (because callers branch on `r.Name == "devbox"`).

**Files:**
- Modify: `internal/core/notify/native.go:100`, `native_darwin.go:28,76`, `native_other.go:8`
- Modify: `internal/core/ui/render/brand_header.go:32`
- Modify: `internal/cli/docs/docs.go:38`, `internal/cli/command/list.go:232,234,237`
- Modify: `internal/cli/docs/generate.go:307,706,760-764` (generated docs titles)
- Modify: `internal/core/docs/llmstxt/generator.go:56-73,189,200` + `generator_test.go`
- Modify: `internal/core/docs/source.go:14,25` (root identifier)
- Modify: `internal/core/docs/topic.go:14,23,231-235` (tie-break literal)
- **Modify (docs source discriminator branches that compare against `"devbox"`):** `internal/cli/docs/export.go:74`, `internal/core/docs/export/export.go:28,95`, `internal/cli/docs/show.go:280`, `internal/core/docs/tui/tree_widget.go:161`. After source.go renames `"devbox"` → `"dwe"`, these callers must update in lockstep or silently break.
- **Modify (Cobra Short/Long help text):** `internal/cli/command/command.go:53` (`Run, inspect, and list devbox commands`), `internal/cli/command/list.go:98`, `internal/cli/docs/show.go:44`, `internal/cli/docs/generate.go:27`, `internal/cli/docs/llmstxt.go:35`, `internal/cli/validate/validate.go:142` (the entire `Scope targets:` help block with `devbox validate …` examples). These feed `--help` output and regenerated CLI docs (Task 13 re-emits them).
- Modify: any test asserts hardcoding `"Devbox"`/`devbox-docs://`/`Name: "devbox"`

**Precheck before this task:**
```
rg -n 'devbox|Devbox|DEVBOX' internal/cli internal/core/docs internal/core/ui internal/shared/prompt -g '*.go' --no-heading
```
Review the full list. Allow only intentional test-data identifiers (e.g. `devbox-v2-good` test IDs); everything else gets rebranded.

- [x] notify package: `"Devbox"` → `"DWE"` in `native.go:100`, `native_darwin.go:28` (`terminalNotifierGroup`), `native_other.go:8` (`beeep.AppName`); `"devbox-notify-*.png"` → `"dwe-notify-*.png"` in `native_darwin.go:76`
- [x] `internal/core/ui/render/brand_header.go:32` `"Devbox"` → `"DWE"`
- [x] `internal/cli/docs/docs.go:38` `parts := []string{"Devbox"}` → `"DWE"`
- [x] `internal/cli/command/list.go:232,234,237` selector title `"Devbox"` → `"DWE"`
- [x] `internal/cli/docs/generate.go`: prose `# devbox Reference Documentation` and similar headings → `# dwe …`
- [x] **Cobra Short/Long updates** in `command/command.go:53`, `command/list.go:98`, `docs/show.go:44`, `docs/generate.go:27`, `docs/llmstxt.go:35` — replace product-name prose `devbox` → `dwe`
- [x] llmstxt generator:
  - [x] `internal/core/docs/llmstxt/generator.go:56-73` `# devbox` heading + prose → `# dwe`
  - [x] `internal/core/docs/llmstxt/generator.go:189,200` URI scheme `devbox-docs://` → `dwe-docs://`
  - [x] update all asserts in `generator_test.go`
- [x] docs source root + downstream branches:
  - [x] `internal/core/docs/source.go:14,25` `Name: "devbox"` → `"dwe"`
  - [x] `internal/core/docs/topic.go:14,23,231-235` `"devbox"` tie-break literal → `"dwe"`
  - [x] **`internal/cli/docs/export.go:74`** `if r.Name == "devbox"` → `"dwe"` (filter for built-in docs root)
  - [x] **`internal/core/docs/export/export.go:28,95`** `root.Name == "devbox"` branches → `"dwe"`
  - [x] **`internal/cli/docs/show.go:280`** any `"devbox"` discriminator → `"dwe"`
  - [x] **`internal/core/docs/tui/tree_widget.go:161`** TUI root identifier → `"dwe"`
  - [x] update `--source` help text and export destination assertions in `internal/cli/docs/` tests
- [x] `rg -n '"Devbox"|devbox-docs://|Name:\s*"devbox"' --type go` returns zero (precheck)
- [x] `make build && make test` — must end **green**

### Task 11: Phase 4a — English docs

**Files:**
- Modify: every `docs/reference/**/*.md` mentioning `devbox`
- Rename: `docs/reference/config/devbox.md` → `workspace.md` (if present)
- Modify: `docs/internals/packages.md`

- [x] `rg -l 'devbox' docs/reference/ docs/internals/` (exclude `cli/devbox_*.md`)
- [x] replace: command `devbox` → `dwe`, file `devbox.yml` → `workspace.yml`, folder `devbox/` → `workspace/`, runtime `.devbox/` → `.dwe/`, env `DEVBOX_*` → `DWE_*`, YAML enum, Docker labels `devbox.*` → `dwe.*`, snapshot schema, product "Devbox" → "DWE" / "Dev Workspace Engine" (first mention per file), package godoc lines (`// Package … devbox …`) → `dwe`, llms URI scheme `devbox-docs://` → `dwe-docs://`
- [x] **manually** review — leave jetify devbox comparative refs intact
- [x] delete schema_version section from `docs/reference/config/workspace.md`
- [x] update `docs/internals/packages.md` — Binary accessors pattern `DevboxBin` → `DweBin`
- [x] update env-var contract sections
- [x] update daemon docs (`types.md:~626`) to show `dwe.*` labels
- [x] update snapshot doc (`docs/reference/snapshot.md`) for new archive layout
- [x] update render template-pack docs (`docs/reference/render/{ide,ai,git,index}.md`) — ~20 occurrences of `devbox/templates/...` paths
- [x] `make build && make test`

### Task 12: Phase 4b — Russian docs

**Files:**
- Modify: every file under `docs/i18n/ru/reference/**/*.md` and `docs/i18n/ru/internals/**/*.md`
- Rename: `docs/i18n/ru/reference/config/devbox.md` → `workspace.md`

- [x] `rg -l 'devbox' docs/i18n/ru/`
- [x] apply Task 11 replacements for RU narrative; "Devbox" → "DWE" / "Dev Workspace Engine" / "движок dev-окружения"
- [x] **manually** adapt RU inflections (`devbox'а`, `devbox-проект`, `в devbox`)
- [x] delete RU schema_version sections
- [x] `make build && make test`

### Task 13: Phase 4c — Regenerate auto-generated CLI reference

**Files:**
- Delete: `docs/reference/cli/devbox.md`, `docs/reference/cli/devbox_*.md` (NOT `index.md`, NOT `dwe.md` after regen)
- Create: `docs/reference/cli/dwe.md`, `docs/reference/cli/dwe_*.md`

**Note:** `bin/dwe docs generate` defaults to `--scope all` which requires a project (commands scope check at `generate.go:59`). Must use `--scope cli` to regenerate outside a project. The glob `devbox_*.md` misses `devbox.md` (root command page) — must remove it explicitly.

- [ ] `rm docs/reference/cli/devbox.md docs/reference/cli/devbox_*.md`
- [ ] `bin/dwe docs generate --scope cli`
- [ ] inspect diff — verify `docs/reference/cli/dwe.md` produced; no stale `devbox*.md` remains; `index.md` regenerated with `dwe` references
- [ ] `make build && make test`

### Task 14: Phase 5 — Narrative docs + Claude plugin + skills

**Files:**
- Modify: `AGENTS.md`, `README.md`, `cmd/dwe/main.go` godoc, root `.md` if present
- Modify: `.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json`
- Rename: `skills/devbox/` → `skills/dwe/`
- Modify: `skills/dwe/SKILL.md`, `skills/dwe/references/recipes.md`

- [ ] update AGENTS.md fully: opening sentences, product mentions, file refs, command examples, Critical Patterns section (`cmd/devbox/main.go` → `cmd/dwe/main.go`, `DevboxBin` → `DweBin`, `.devbox/` → `.dwe/`, `devbox.yml` → `workspace.yml`, `devbox/` → `workspace/`, daemon labels, snapshot schema, llms URI scheme, brand strings)
- [ ] verify `ls -la CLAUDE.md` symlink intact
- [ ] update README.md: title, badges, install (`brew install dwe`), examples
- [ ] update package godoc lines `// Package … devbox …` across `internal/cli/*` (bulk-replaceable)
- [ ] `git mv skills/devbox skills/dwe`
- [ ] update `skills/dwe/SKILL.md` (34 mentions), detection prose
- [ ] update `skills/dwe/references/recipes.md`
- [ ] update `.claude-plugin/plugin.json`: name, description, activation hint, skill path
- [ ] update `.claude-plugin/marketplace.json`: name, description, command examples
- [ ] `make build && make test`

### Task 15: Phase 6 — Release config

**Files:**
- Modify: `.goreleaser.yaml` (23+ occurrences)
- Modify: `.github/workflows/release.yml`

`.goreleaser.yaml` enumeration:
- [ ] `project_name`
- [ ] build `id`/`main`/`binary`
- [ ] three `-X github.com/semsemyonoff/devbox/internal/shared/version.*` ldflags
- [ ] archive `name_template`, `files`
- [ ] nfpm `bindir`/`package_name`/`homepage`
- [ ] **nfpm description (line 72)** — prose `"Devbox orchestrates Docker services, deploy pipelines, and project commands…"` → rewrite for DWE; ships in `.deb`/`.rpm` package metadata visible to users
- [ ] three `completions/devbox.{bash,zsh,fish}` sources
- [ ] three completion install destinations (`/usr/share/.../devbox`, `_devbox`, `devbox.fish`)
- [ ] LICENSE dst `/usr/share/doc/devbox/LICENSE` → `/usr/share/doc/dwe/LICENSE`
- [ ] cask `name`, `homepage`
- [ ] cask post-install xattr hook `#{staged_path}/devbox` → `#{staged_path}/dwe`
- [ ] three Homebrew completion sources
- [ ] `release.github.name: devbox` (line ~155) → `dwe`
- [ ] `.github/workflows/release.yml`: workflow name, hardcoded `devbox` strings, artifact upload paths, comment text `Formula/devbox.rb`
- [ ] verify secret name in workflow: `HOMEBREW_TAP_GITHUB_TOKEN` (correct)
- [ ] **dry-run:** `goreleaser release --snapshot --clean --skip=publish` — verify artifact names
- [ ] commit; `actionlint` if installed

### Task 16: Phase 7 — Final validation and concrete smoke test

**Smoke test is authoritative — schema-validated against real types** (per codex 5th-pass review; previous "starting point" disclaimer was a cop-out). All YAML below conforms to `internal/core/usercommands/model/types.go` and `internal/cli/snapshot/create.go` requirements.

**Project structure:**

```
smoke-test/
├── workspace.yml                              # root project marker (no schema_version)
├── docker-compose.yml                         # compose stack
└── workspace/
    ├── snapshot.yml                           # required for `dwe snapshot create`
    ├── scripts/
    │   └── print-env.sh                       # actual script file for type: script
    ├── services/
    │   └── hello/
    │       └── service.yml                    # type: tool, container: hello-svc
    └── commands/
        └── commands.yml                       # top-level `commands:` map (one file or many)
```

`workspace.yml`:
```yaml
# Minimal root — extend if validator demands more
project:
  name: dwe-smoke
  prefix: dwe-smoke
```

`docker-compose.yml`:
```yaml
services:
  hello-svc:
    image: alpine:3
    command: ["sh", "-c", "sleep 3600"]
```

`workspace/services/hello/service.yml`:
```yaml
type: tool
container: hello-svc
```

`workspace/snapshot.yml`:
```yaml
create:
  steps: []
```

`workspace/scripts/print-env.sh`:
```sh
#!/usr/bin/env sh
echo "BIN=$DWE_BIN ROOT=$DWE_ROOT COMMAND_ID=$DWE_COMMAND_ID"
```

`workspace/commands/commands.yml` (top-level `commands:` map is required):
```yaml
commands:
  print-env:
    type: script         # type: script reads scripts/<name>.sh; type: dwe only accepts `cmd:` (a dwe subcommand), not inline shell
    script:
      path: print-env.sh
  self-version:
    type: dwe            # exercises Phase 3e enum + Phase 3d env vars (DWE_BIN populated by host runner)
    cmd: version
  ping-daemon:
    type: daemon         # exercises Phase 3c daemon labels (dwe.project / dwe.daemon.id / dwe.daemon.params)
    service: hello
    argv: ["sh", "-c", "while true; do echo ping; sleep 5; done"]
    daemon:
      container_template: dwe-smoke-ping
```

(If the actual schema rejects any of the above, fix here in the plan first; don't paper over with `validate --fix`. The exact field requirements are in `internal/core/usercommands/model/types.go:55,78,468` and `internal/core/project/config/devbox.go` service schema.)

**Smoke test sequence:**

- [ ] `make clean && make build` — `bin/dwe` produced
- [ ] `make test`, `make test-race`, `make lint` — green (no Docker required)
- [ ] `./bin/dwe --help` — `Usage: dwe …` (not `devbox`); brand header `DWE`; root Short/Long updated
- [ ] `./bin/dwe docs` — docs browser title `DWE`, root identifier `dwe`
- [ ] **(Docker-dependent below — skip + mark manual if no Docker)**
- [ ] in smoke-test dir: `./bin/dwe validate` — no schema_version errors; project detector finds `workspace.yml`
- [ ] `./bin/dwe deploy run` — `.dwe/deploy/deploy.lock` created (not `.devbox/`); `ping-daemon` container starts; `docker inspect <container>` shows `dwe.project=dwe-smoke`, `dwe.daemon.id=ping-daemon`, `dwe.daemon.params=…`
- [ ] `./bin/dwe commands print-env` (alias `dwe cmd print-env`) — output contains `BIN=/path/to/bin/dwe ROOT=<smoke-dir> COMMAND_ID=print-env`
- [ ] `./bin/dwe commands self-version` — runs `dwe version` via the type: dwe runner; exercises Phase 3e + Phase 3d together
- [ ] `./bin/dwe snapshot create smoke -y` — snapshot manifest contains `dwe_version:`, `workspace_files:`; archive layout `<snap>/workspace/local.yml` (not `<snap>/devbox/`)
- [ ] `./bin/dwe docs llms-txt` — output starts with `# dwe`, URIs are `dwe-docs://`
- [ ] `./bin/dwe docs export --include-internals /tmp/dwe-docs-export && ls /tmp/dwe-docs-export` — export root is `dwe` (was `devbox`)
- [ ] **wide grep verification** (using `rg --hidden`):
  - [ ] `rg --hidden -l devbox -g '!.git' -g '!internal/core/docs/embedded' -g '!dist' -g '!docs/plans/completed' -g '!docs/plans'` — only meaningful mentions (CHANGELOG, jetify comparisons, this plan)
  - [ ] `rg --hidden schema_version -g '!.git' -g '!internal/core/docs/embedded'` — empty
  - [ ] `rg --hidden DEVBOX_ -g '!.git' -g '!internal/core/docs/embedded'` — empty
  - [ ] `rg --hidden '\.devbox' -g '!.git' -g '!internal/core/docs/embedded'` — empty
  - [ ] `rg --hidden 'devbox\.project|devbox\.daemon' -g '!.git' -g '!internal/core/docs/embedded'` — empty
  - [ ] `rg --hidden 'devbox_version|devbox_files|DevboxSubdir|DevboxFiles' -g '!.git' -g '!internal/core/docs/embedded'` — empty
  - [ ] `rg --hidden '"Devbox"|devbox-docs://|CommandTypeDevbox' -g '!.git' -g '!internal/core/docs/embedded'` — empty
- [ ] `git status` — all changes ready

### Task 17: Update memory and move plan

- [ ] update memory `~/.claude/projects/.../memory/project_rename_devbox_to_dwe.md` — code phase done
- [ ] mark every task in this plan `[x]`
- [ ] `mkdir -p docs/plans/completed && git mv docs/plans/2026-06-01-rebrand-to-dwe.md docs/plans/completed/`

## Post-Completion

*External actions on GitHub and the Homebrew tap.*

**Release operations (in order):**

1. **Merge all code changes** into `main`.
2. **Delete old release and tag:**
   - `gh release delete v0.1.0 --yes --cleanup-tag`.
   - `git tag -d v0.1.0`.
3. **Rename GitHub repository:**
   - `gh repo rename dwe`.
   - `git remote set-url origin git@github.com:semsemyonoff/dwe.git`.
4. **Re-issue Homebrew tap PAT** (cheap insurance over guessing PAT scope):
   - Open https://github.com/settings/tokens (or fine-grained equivalent).
   - Revoke the old `HOMEBREW_TAP_GITHUB_TOKEN`.
   - Create a new token with `repo` scope for `semsemyonoff/homebrew-tap` (or whatever the tap is named).
   - Update the secret in `https://github.com/semsemyonoff/dwe/settings/secrets/actions` → `HOMEBREW_TAP_GITHUB_TOKEN`.
5. **Update Homebrew tap repo:**
   - Delete `Casks/devbox.rb` (or `Formula/devbox.rb`) so users with old install don't hit checksum mismatch on `brew upgrade`.
   - GoReleaser will auto-commit `dwe.rb` in the next release.
6. **Push new tag:**
   - `git tag -a v0.1.0 -m "v0.1.0 — rebranded from devbox to dwe"`.
   - `git push origin v0.1.0`.
7. **Release workflow** runs automatically; GoReleaser publishes `dwe-darwin-arm64.tar.gz`, etc., commits `dwe.rb` to tap.
8. **User-perspective smoke test:**
   - `brew untap semsemyonoff/tap && brew tap semsemyonoff/tap`.
   - `brew install dwe`.
   - `dwe --version` shows v0.1.0.

**Safe to ignore:**

- Go proxy cache for `github.com/semsemyonoff/devbox@v0.1.0` — new module path has no cache.
- GitHub HTTP redirect on old URL — works "forever" for clone/fetch.

**Manual smoke test after release:**

- On a clean machine: `brew install semsemyonoff/tap/dwe`, verify binary works.
- Fresh folder: hand-craft `workspace.yml`, run lifecycle, verify `.dwe/` state dir, `DWE_*` env vars, `dwe.*` Docker labels.

**External system updates:**

- Update external artifacts (docs sites, articles, third-party READMEs) linking to `devbox`.
- If announcing publicly — prepare a post explaining motivation.
