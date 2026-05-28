# Internal Restructure: 3-Layer Architecture (cli/core/shared)

## Overview

The current `internal/` tree has 36+ sibling packages at one level. This makes navigation difficult — humans and AI cannot quickly answer "where do I look for X?" — and provides no enforcement against unwanted cross-layer dependencies (e.g., a domain package accidentally importing cobra).

This plan restructures `internal/` into a 3-layer + clustered layout enforceable via `depguard`:

- **`internal/cli/`** — cobra command tree, no domain logic
- **`internal/core/`** — domain logic, subclustered into `project/`, `execution/`, `workflow/`, plus standalone subtrees (`usercommands/`, `validate/`, `docs/`, `ui/`, `notify/`)
- **`internal/shared/`** — leaf infrastructure (docker, git, lock, render primitives, i18n, etc.)

Pre-release status (per `AGENTS.md` "Project Status & Compatibility Policy") means we restructure freely without back-compat shims. One big PR on the existing `refactoring` branch.

## Context (from discovery)

- **Project**: `devbox-cli`, Go CLI (cobra v2/fang), single binary, no released versions
- **Current pain**: `internal/` has 36+ sibling packages; CLAUDE.md mentally groups them (`docs/internals/packages.md`) but filesystem does not reflect groupings
- **Existing patterns to preserve**:
  - `internal/validate/` already a subtree (framework + 10 subdomains) → moves as-is to `core/validate/`
  - `internal/usercommands/` already a subtree (model/loader/registry/resolve/runtime) → moves as-is to `core/usercommands/`
  - `internal/command/` partially split (cmdctx, deploy, render, service, statusview, statustui subpkgs) → becomes `internal/cli/` (retaining existing subpackages `cmdctx/`, `deploy/`, `render/`, `service/`), with statusview/statustui relocated to `core/ui/`
- **Rename impact**:
  - ~30 files reference `userconfig.X` (PACKAGE RENAME: `userconfig` → `user`)
  - 6 files reference `localconfig.X` (PACKAGE RENAME: `localconfig` → `local`)
  - 104 files import `internal/usercommands` (illustrates mass-sed scope)
- **Confirmed variable-name collisions** (will break naive sed `package.` substitution):
  - `internal/command/service/service_toggle.go:312` and `:413` both contain `local, err := localconfig.LoadLocalYAML(...)` — naive sed produces `local, err := local.LoadLocalYAML(...)` which is a duplicate-name compile error
  - `internal/envfile/render.go`, `internal/tpl/render_command.go`, `internal/command/service_cli.go` all import `os/user` and call `user.Current()` — adjacent collision space for the `user` rename
  - Approach: import the renamed packages everywhere via stable aliases (`userpkg`, `localpkg`) during the rename step. Mechanical, no per-file judgment, no collisions. Drop-aliases-where-unambiguous is a follow-up.
- **Build scripts to update**: `scripts/sync-embedded-docs.sh`, `scripts/gen-docs-content-hashes.sh` (hardcoded `internal/docs/embedded/` and `internal/docs/content_hashes_gen.go` paths)
- **`Makefile` references that move**:
  - Lines 8–11: `LDFLAGS := -X devbox-cli/internal/version.Version=...` (four `-X` flags) — `internal/version` moves to `internal/shared/version`. The Go linker silently ignores `-X` against non-existent symbols, so an out-of-date LDFLAGS produces a built binary with `Version="dev"` rather than a build error.
  - Line 33: `test-race: go test -race ./internal/deploy/journal ./internal/lock ./internal/pipeline` — all three paths move (Task 2 lock, Task 4 pipeline, Task 5 deploy)
- **`.golangci.yml` exclusion rules with hardcoded paths** that stop matching after the moves:
  - Line 26: `path: internal/usercommands/usercommands\.go` → moves in Task 6
  - Line 32: `path: internal/docs/tui/` → moves in Task 6
  - Line 40: `path: internal/config/(info|ui)_test\.go` → moves in Task 3
- **`.golangci.yml`** is v2 schema; depguard rules go under `linters.settings.depguard.rules.*`
- **`internal/docs/embed.go:9`** contains `//go:generate ../../scripts/sync-embedded-docs.sh`. After move to `internal/core/docs/embed.go` the relative path is one segment deeper — must become `../../../scripts/sync-embedded-docs.sh`.

## Development Approach

- **Testing approach**: **Regular (verification-driven)** — this is a mechanical refactor with zero behavior change. No new test code; existing tests are the gate. Every task ends with `go build ./... && make test` passing (and `make build` passing for tasks that touch embedded docs or build scripts).
- Complete each task fully before moving to the next.
- Make small, focused changes per task (one cluster of moves + import updates + co-located config/script updates per task).
- **CRITICAL: every task ends with build + tests passing.** Refactor tasks don't add new tests, but they must not regress the existing suite.
- **CRITICAL: do NOT silence depguard violations.** If task 10 surfaces a cross-layer import, that's a real bug in the dependency graph that must be fixed (move the importing code to the correct layer or invert the dependency). Do not add `nolint:depguard`.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Maintain backward compatibility is NOT a goal (pre-release).

## Testing Strategy

- **Unit tests**: existing test suite must pass unchanged after each task. No new test code added (refactor, not feature).
- **E2E tests**: project has no UI/Playwright e2e tests — golden-file tests inside packages serve as integration gate.
- **Architectural tests**: `depguard` rules added in task 10 enforce layer boundaries at lint time. `make lint` becomes part of the verification loop.
- **Per-task verification command**:
  ```bash
  go build ./... && make test
  ```
- Tasks that touch embedded docs / build scripts (tasks 6, 11) additionally must run:
  ```bash
  make build
  ```
- **Final verification command** (after task 10 onwards):
  ```bash
  make build && make test && make lint
  ```

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- If a task surfaces unexpected coupling (e.g., shared package imports core), document the cross-import here and decide: fix in place vs. defer to follow-up PR.
- Keep this plan in sync with actual work done.

## Solution Overview

**Three layers, enforced via depguard:**

```
internal/
  cli/                      (cobra tree, no domain logic)
  core/                     (domain logic, subclustered)
  shared/                   (leaf infrastructure)
```

**Dependency direction (DAG by construction):**

```
cli/        →  core/* + shared/*           (full access — composition root)
core/*      →  core/* + shared/*           (no cli imports — domain ignorant of cobra)
shared/*    →  shared/*                    (leaf — no domain imports)
core/ui/    →  imported only by cli/       (sink — no core package may import ui)
cli/<sub>/  →  no other cli/<sub>          (siblings communicate via cli/cmdctx/ + root.go)
```

**Cluster rationale inside `core/`:**

| Cluster | What lives there | Why grouped |
|---|---|---|
| `project/` | project root, config, user/local overlays, services, stack | All concern "what is a Devbox project" — tight mutual dependency |
| `execution/` | pipeline, condition, filesgate, builtin, templates, preflight | All collaborate via `pipeline.Context`; one engine |
| `workflow/` | deploy, lifecycle, reset, snapshot, setup | Each builds a pipeline atop `execution/` for a named user operation |
| `usercommands/` | model, loader, registry, resolve, runtime | Self-contained subtree, already grouped |
| `validate/` | framework + 10 subdomain validators | Self-contained subtree, already grouped |
| `docs/` | embedded, render, mermaid, tui, export | Self-contained doc subsystem |
| `ui/` | info, status, banners, statusview, statustui | Domain-aware renderers (sink — imported only by `cli/`) |
| `notify/` | notifications, hookpoints | Standalone, called from workflow + usercommands |

Max depth: 3 levels (`internal/core/workflow/snapshot/`). Max siblings per level: ≤7.

## Technical Details

### Full package mapping (current → target)

**CLI layer:**

| Current | Target |
|---|---|
| `internal/command/` | `internal/cli/` |
| `internal/command/statusview/` | `internal/core/ui/statusview/` *(moves out of cli)* |
| `internal/command/statustui/` | `internal/core/ui/statustui/` *(moves out of cli)* |

**Core/project cluster:**

| Current | Target |
|---|---|
| `internal/project/` | `internal/core/project/project/` |
| `internal/config/` | `internal/core/project/config/` |
| `internal/userconfig/` | `internal/core/project/user/` *(PACKAGE RENAME)* |
| `internal/localconfig/` | `internal/core/project/local/` *(PACKAGE RENAME)* |
| `internal/services/` | `internal/core/project/services/` |
| `internal/stack/` | `internal/core/project/stack/` |

**Core/execution cluster:**

| Current | Target |
|---|---|
| `internal/pipeline/` | `internal/core/execution/pipeline/` |
| `internal/condition/` | `internal/core/execution/condition/` |
| `internal/filesgate/` | `internal/core/execution/filesgate/` |
| `internal/builtin/` | `internal/core/execution/builtin/` |
| `internal/templates/` | `internal/core/execution/templates/` |
| `internal/preflight/` | `internal/core/execution/preflight/` |

**Core/workflow cluster:**

| Current | Target |
|---|---|
| `internal/deploy/` | `internal/core/workflow/deploy/` |
| `internal/lifecycle/` | `internal/core/workflow/lifecycle/` |
| `internal/reset/` | `internal/core/workflow/reset/` |
| `internal/snapshot/` | `internal/core/workflow/snapshot/` |
| `internal/setup/` | `internal/core/workflow/setup/` |

**Core (top-level subtrees):**

| Current | Target |
|---|---|
| `internal/usercommands/` | `internal/core/usercommands/` |
| `internal/validate/` | `internal/core/validate/` |
| `internal/docs/` | `internal/core/docs/` |
| `internal/ui/` | `internal/core/ui/` |
| `internal/notify/` | `internal/core/notify/` |

**Shared (leaf infra):**

| Current | Target |
|---|---|
| `internal/docker/` | `internal/shared/docker/` |
| `internal/git/` | `internal/shared/git/` |
| `internal/daemon/` | `internal/shared/daemon/` |
| `internal/lock/` | `internal/shared/lock/` |
| `internal/pathsafe/` | `internal/shared/pathsafe/` |
| `internal/envfile/` | `internal/shared/envfile/` |
| `internal/render/` | `internal/shared/render/` |
| `internal/liveui/` | `internal/shared/liveui/` |
| `internal/tpl/` | `internal/shared/tpl/` |
| `internal/i18n/` | `internal/shared/i18n/` |
| `internal/version/` | `internal/shared/version/` |
| `internal/prompt/` | `internal/shared/prompt/` |

### Depguard config (golangci-lint v2 schema)

To add to `.golangci.yml`:

```yaml
linters:
  enable:
    - depguard
    # ... existing linters

  settings:
    depguard:
      rules:
        shared-no-domain:
          files: ["**/internal/shared/**"]
          deny:
            - pkg: devbox-cli/internal/core
              desc: "shared/ — leaf infra, must not import core"
            - pkg: devbox-cli/internal/cli
              desc: "shared/ must not import cli"
        core-no-cli:
          files: ["**/internal/core/**"]
          deny:
            - pkg: devbox-cli/internal/cli
              desc: "core/ — domain logic, must not import cli (cobra)"
        ui-is-sink:
          files:
            - "**/internal/core/**"
            - "!**/internal/core/ui/**"
          deny:
            - pkg: devbox-cli/internal/core/ui
              desc: "core/ui/ — sink, imported only by cli/"
```

### Already-verified risk points

- `//go:embed` directives in `internal/`: exactly three sites, all use relative paths that travel with the parent on `git mv`:
  - `internal/docs/embed.go:10` (`//go:embed embedded`)
  - `internal/notify/native.go:12` (`//go:embed assets/icon.png`)
  - `internal/i18n/loader.go:16` (`//go:embed translations/*.yml`)
- **`//go:generate` directive that DOES break on move**: `internal/docs/embed.go:9` (`//go:generate ../../scripts/sync-embedded-docs.sh`). After move to `internal/core/docs/embed.go`, must be updated to `../../../scripts/sync-embedded-docs.sh` (extra path segment).
- `testdata/` directories travel with parent automatically.
- `cmd/devbox/main.go` imports `internal/prompt`, `internal/ui`, `internal/version`, `internal/command`, `internal/config`, `internal/pipeline`, `internal/project` — all 7 updated by mass sed.
- `Makefile` LDFLAGS (lines 8–11) and `test-race` target (line 33) — updated inline with the relevant move tasks (see Task 2, 4, 5).
- `.golangci.yml` exclusion `path:` patterns (lines 26, 32, 40) — updated inline with the relevant move tasks (see Task 3, 6).
- Symlink `CLAUDE.md → AGENTS.md` not affected by package moves (points at the file, not paths inside).
- `devbox/validate.yml` references linters by binary name (`shellcheck`, `hadolint`) — no Go import paths.
- `internal/docs/content_hashes_gen.go` has `package docs` declaration; the gen script writes `package docs` (constant) — works after move to `internal/core/docs/` provided Task 6 also updates the gen script's output path.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): all mechanical moves, sed updates, depguard config, docs rewrites — fully achievable in this repo.
- **Post-Completion** (no checkboxes): nothing external. Self-contained refactor.

## Implementation Steps

### Task 1: Baseline & sed-script preparation

**Files:**
- Create: `scripts/restructure-imports.sh` *(throwaway helper, deleted in Task 10)*

- [x] verify clean baseline: `git status` shows only `refactoring` branch state; no uncommitted changes (user already confirmed revert)
- [x] run `make test` and record passing baseline — capture failing tests if any (must be empty)
- [x] run `make lint` and record passing baseline
- [x] write `scripts/restructure-imports.sh`. **Substitution ordering rule**: for any pair of paths where one is a prefix of the other (e.g., `internal/command/statusview` and `internal/command`), put the longer/more-specific substitution FIRST in the script so sed doesn't pre-rewrite the prefix. Apply the same rule for `internal/docs` vs `internal/docs/tui` and any similar pairs.
- [x] script also runs `goimports -w .` at end. Note: running `goimports` on a partially-renamed tree is safe — it operates on file syntax, not package resolution.
- [x] commit baseline state: `chore: prep for internal/ restructure (baseline)`

### Task 2: Move shared/ packages (incl. Makefile LDFLAGS update)

**Files:**
- Modify: `internal/{docker,git,daemon,lock,pathsafe,envfile,render,liveui,tpl,i18n,version,prompt}/*` → `internal/shared/...`
- Modify: all `.go` files importing these (mass sed)
- Modify: `Makefile` (LDFLAGS lines 8–11 — `internal/version` → `internal/shared/version`; test-race `./internal/lock` → `./internal/shared/lock`)

- [ ] `git mv internal/docker internal/shared/docker` (repeat for all 12 shared packages)
- [ ] update `Makefile` LDFLAGS: four `-X devbox-cli/internal/version.<Field>=...` lines become `-X devbox-cli/internal/shared/version.<Field>=...`
- [ ] update `Makefile` `test-race` target: replace `./internal/lock` with `./internal/shared/lock` (other paths in that line still old; final paths updated in Tasks 4, 5)
- [ ] run `scripts/restructure-imports.sh` (full pass — harmless to run substitutions for not-yet-moved pkgs)
- [ ] `go build ./...` — must pass
- [ ] `make build` — must pass and binary should still embed correct version (verify `bin/devbox version` shows real commit hash, not "dev")
- [ ] `make test` — must pass
- [ ] commit: `refactor(internal): move leaf infra to shared/`

### Task 3: Move core/project cluster (incl. .golangci.yml exclusion update)

**Files:**
- Modify: `internal/{project,config,services,stack}/*` → `internal/core/project/{project,config,services,stack}/`
- Modify: all `.go` files importing these (mass sed)
- Modify: `.golangci.yml` line 40 — `path: internal/config/(info|ui)_test\.go` → `path: internal/core/project/config/(info|ui)_test\.go`

- [ ] `git mv internal/project internal/core/project/project`
- [ ] `git mv internal/config internal/core/project/config`
- [ ] `git mv internal/services internal/core/project/services`
- [ ] `git mv internal/stack internal/core/project/stack`
- [ ] *(userconfig + localconfig deferred to Tasks 8–9 for rename)*
- [ ] update `.golangci.yml` exclusion path for `internal/config/(info|ui)_test\.go` to new location
- [ ] run sed for these 4 packages (or full sed pass)
- [ ] `goimports -w .`
- [ ] `go build ./...` — must pass
- [ ] `make test` — must pass
- [ ] `make lint` — must pass (exclusion still matches; no new revive/modernize warnings)
- [ ] commit: `refactor(internal): cluster project model under core/project/`

### Task 4: Move core/execution cluster (incl. test-race update)

**Files:**
- Modify: `internal/{pipeline,condition,filesgate,builtin,templates,preflight}/*` → `internal/core/execution/...`
- Modify: all `.go` files importing these (mass sed)
- Modify: `Makefile` line 33 — replace `./internal/pipeline` with `./internal/core/execution/pipeline`

- [ ] `git mv internal/pipeline internal/core/execution/pipeline`
- [ ] `git mv internal/condition internal/core/execution/condition`
- [ ] `git mv internal/filesgate internal/core/execution/filesgate`
- [ ] `git mv internal/builtin internal/core/execution/builtin`
- [ ] `git mv internal/templates internal/core/execution/templates`
- [ ] `git mv internal/preflight internal/core/execution/preflight`
- [ ] update `Makefile` `test-race` — `./internal/pipeline` → `./internal/core/execution/pipeline`
- [ ] sed import paths
- [ ] `goimports -w .`
- [ ] `go build ./...` — must pass
- [ ] `make test` — must pass
- [ ] `make test-race` — must pass (target now points at moved paths for lock+pipeline; deploy/journal still old until Task 5)
- [ ] commit: `refactor(internal): cluster pipeline engine under core/execution/`

### Task 5: Move core/workflow cluster (incl. final test-race update)

**Files:**
- Modify: `internal/{deploy,lifecycle,reset,snapshot,setup}/*` → `internal/core/workflow/...`
- Modify: all `.go` files importing these (mass sed)
- Modify: `Makefile` line 33 — replace `./internal/deploy/journal` with `./internal/core/workflow/deploy/journal`

- [ ] `git mv internal/deploy internal/core/workflow/deploy` (preserves `deploy/journal/` subpkg)
- [ ] `git mv internal/lifecycle internal/core/workflow/lifecycle`
- [ ] `git mv internal/reset internal/core/workflow/reset`
- [ ] `git mv internal/snapshot internal/core/workflow/snapshot`
- [ ] `git mv internal/setup internal/core/workflow/setup`
- [ ] update `Makefile` `test-race` — `./internal/deploy/journal` → `./internal/core/workflow/deploy/journal`
- [ ] sed import paths
- [ ] `goimports -w .`
- [ ] `go build ./...` — must pass
- [ ] `make test` — must pass
- [ ] `make test-race` — must pass (all three paths now point at moved locations)
- [ ] commit: `refactor(internal): cluster named workflows under core/workflow/`

### Task 6: Move remaining core/ subtrees + update embedded-docs scripts + go:generate path

**Files:**
- Modify: `internal/{usercommands,validate,docs,ui,notify}/*` → `internal/core/...`
- Modify: all `.go` files importing these (mass sed)
- Modify: `scripts/sync-embedded-docs.sh` (EMBEDDED_DIR target)
- Modify: `scripts/gen-docs-content-hashes.sh` (default OUTPUT_FILE path)
- Modify: `internal/core/docs/embed.go:9` (`//go:generate` relative path: `../../scripts/` → `../../../scripts/`)
- Modify: `.golangci.yml` lines 26, 32 — `internal/usercommands/usercommands\.go` and `internal/docs/tui/` exclusion paths

> **Rationale for merging script updates into this task**: `make build` invokes `sync-embedded-docs` and `gen-docs-content-hashes` before compilation. If `internal/docs/` moves but the scripts are not updated in the same commit, the next `make build` writes generated files into the old `internal/docs/...` path (which no longer holds Go source), corrupting the tree. Co-locate the script updates with the move.

- [ ] `git mv internal/usercommands internal/core/usercommands`
- [ ] `git mv internal/validate internal/core/validate`
- [ ] `git mv internal/docs internal/core/docs`
- [ ] `git mv internal/ui internal/core/ui`
- [ ] `git mv internal/notify internal/core/notify`
- [ ] update `scripts/sync-embedded-docs.sh`: change `EMBEDDED_DIR="$REPO_ROOT/internal/docs/embedded"` → `EMBEDDED_DIR="$REPO_ROOT/internal/core/docs/embedded"`
- [ ] update `scripts/gen-docs-content-hashes.sh`: change default `OUTPUT_FILE="${1:-internal/docs/content_hashes_gen.go}"` → `OUTPUT_FILE="${1:-internal/core/docs/content_hashes_gen.go}"`
- [ ] update `internal/core/docs/embed.go:9` go:generate directive: `../../scripts/sync-embedded-docs.sh` → `../../../scripts/sync-embedded-docs.sh`
- [ ] update `.golangci.yml`: `path: internal/usercommands/usercommands\.go` → `path: internal/core/usercommands/usercommands\.go`
- [ ] update `.golangci.yml`: `path: internal/docs/tui/` → `path: internal/core/docs/tui/`
- [ ] sed import paths
- [ ] `goimports -w .`
- [ ] `go build ./...` — must pass
- [ ] `make build` — must pass (regenerates embedded docs into new location; confirms scripts hit new paths)
- [ ] verify no stale files at old paths: `[ ! -d internal/docs ]` and `[ ! -f internal/docs/content_hashes_gen.go ]`
- [ ] `make test` — must pass
- [ ] `make lint` — must pass
- [ ] commit: `refactor(internal): move usercommands/validate/docs/ui/notify to core/ + update embed scripts`

### Task 7: Rename internal/command/ → internal/cli/ and relocate statusview/statustui

**Files:**
- Modify: `internal/command/*` → `internal/cli/*`
- Modify: `internal/cli/statusview/*` → `internal/core/ui/statusview/*`
- Modify: `internal/cli/statustui/*` → `internal/core/ui/statustui/*`
- Modify: all `.go` files importing these (including `cmd/devbox/main.go`)

- [ ] `git mv internal/command internal/cli`
- [ ] `git mv internal/cli/statusview internal/core/ui/statusview`
- [ ] `git mv internal/cli/statustui internal/core/ui/statustui`
- [ ] **Sed order** (longest path FIRST to avoid prefix-rewrite of subpackage references):
  - [ ] sed `devbox-cli/internal/command/statusview` → `devbox-cli/internal/core/ui/statusview`
  - [ ] sed `devbox-cli/internal/command/statustui` → `devbox-cli/internal/core/ui/statustui`
  - [ ] then sed `devbox-cli/internal/command` → `devbox-cli/internal/cli` (catches cmdctx/deploy/render/service subpkgs naturally because they were already inside command/)
- [ ] `goimports -w .`
- [ ] `go build ./...` — must pass (verify `cmd/devbox/main.go` builds with new `internal/cli` import)
- [ ] `make test` — must pass
- [ ] commit: `refactor(internal): rename command/ to cli/, move statusview+statustui to core/ui/`

### Task 8: Rename userconfig → user (package-alias approach)

**Files:**
- Modify: `internal/userconfig/*.go` (5 files: config.go, load.go, load_test.go, parser.go, parser_test.go) → `internal/core/project/user/*.go`
- Modify: ~30 files referencing `userconfig.X`

> **Why the alias approach**: bare `user.X` collides with the `os/user` package imported in `internal/shared/envfile/render.go`, `internal/shared/tpl/render_command.go`, `internal/cli/service_cli.go`. Importing as `userpkg "devbox-cli/internal/core/project/user"` everywhere is mechanical, has zero collision surface, and is uniform. Dropping the alias where unambiguous is a follow-up.

- [ ] `git mv internal/userconfig internal/core/project/user`
- [ ] sed in `internal/core/project/user/*.go` only: replace `^package userconfig$` with `package user`
- [ ] sed across ALL `.go` files: import string `"devbox-cli/internal/userconfig"` → `userpkg "devbox-cli/internal/core/project/user"`
- [ ] sed across ALL `.go` files: identifier `userconfig.` → `userpkg.`
- [ ] `goimports -w .`
- [ ] `go build ./...` — must pass (no `user`/`os/user` ambiguity because callers use `userpkg`)
- [ ] `make test` — must pass
- [ ] commit: `refactor(config): rename userconfig package to user (with userpkg alias)`

### Task 9: Rename localconfig → local (package-alias approach)

**Files:**
- Modify: `internal/localconfig/*.go` (4 files: local_yaml.go, local_yaml_test.go, services.go, services_test.go) → `internal/core/project/local/*.go`
- Modify: 6 files referencing `localconfig.X`

> **Confirmed collision**: `internal/cli/service/service_toggle.go:312` and `:413` contain `local, err := localconfig.LoadLocalYAML(...)`. The alias approach (`localpkg`) avoids any per-file variable rename.

- [ ] `git mv internal/localconfig internal/core/project/local`
- [ ] sed in `internal/core/project/local/*.go` only: replace `^package localconfig$` with `package local`
- [ ] sed across ALL `.go` files: import string `"devbox-cli/internal/localconfig"` → `localpkg "devbox-cli/internal/core/project/local"`
- [ ] sed across ALL `.go` files: identifier `localconfig.` → `localpkg.`
- [ ] `goimports -w .`
- [ ] `go build ./...` — must pass (no collision with `local` local variables because callers use `localpkg`)
- [ ] `make test` — must pass
- [ ] commit: `refactor(config): rename localconfig package to local (with localpkg alias)`

### Task 10: Add depguard rules + smoke-test enforcement

**Files:**
- Modify: `.golangci.yml`
- Delete: `scripts/restructure-imports.sh` *(throwaway, no longer needed)*

- [ ] add `depguard` to enabled linters in `.golangci.yml`
- [ ] add the depguard `rules` block under `linters.settings.depguard.rules` (3 rules: `shared-no-domain`, `core-no-cli`, `ui-is-sink`) — see "Depguard config" section above
- [ ] **smoke-test that rules actually engage** (catches silent glob misconfigurations):
  - [ ] add a TEMP intentional violation, e.g. import `devbox-cli/internal/cli/cmdctx` from `internal/shared/lock/lock.go`
  - [ ] run `make lint` — must error with the rule's `desc` string
  - [ ] revert the temp import
  - [ ] repeat for the other two rules: temp-import `internal/cli` from any `internal/core/` file (expects `core-no-cli` to trigger); temp-import `internal/core/ui` from another `internal/core/<sub>` file (expects `ui-is-sink` to trigger). Revert after each.
- [ ] run `make lint` on clean tree — must pass green
- [ ] if lint surfaces real cross-layer imports on the clean tree: fix in-place by moving the importing code to the correct layer or inverting the dependency. Do NOT add `nolint:depguard`. If a fix is genuinely complex, document the violation with ⚠️ in this plan and decide: defer to follow-up or block until fixed.
- [ ] delete `scripts/restructure-imports.sh`
- [ ] commit: `chore(lint): enforce cli→core→shared layering via depguard`

### Task 11: Rewrite docs/internals/packages.md

**Files:**
- Modify: `docs/internals/packages.md`

- [ ] rewrite top-level grouping section: replace alphabetical/flat layout description with 3-layer structure (`cli/`, `core/{project,execution,workflow,...}`, `shared/`)
- [ ] under each layer section, list packages with their new paths
- [ ] add new "Dependency Rules" section documenting the depguard contract (3 rules + soft intra-core ordering)
- [ ] `make build` — must succeed (regenerates `internal/core/docs/embedded/internals/packages.md`; content hash also updates)
- [ ] verify the regenerated embedded copy is up-to-date: `git diff --exit-code internal/core/docs/embedded/ internal/core/docs/content_hashes_gen.go` (should show no uncommitted changes)
- [ ] `make test` — must pass
- [ ] commit: `docs(internals): rewrite packages.md under new layered structure`

### Task 12: Update AGENTS.md + cleanup stale path comments

**Files:**
- Modify: `AGENTS.md` (which `CLAUDE.md` symlinks to)
- Modify: various `.go` files containing comment references to old `internal/<oldpath>` (no `devbox-cli/` prefix)

- [ ] update `AGENTS.md` "Project Structure & Module Organization" section: replace "High-level grouping" list with new paths
- [ ] update `AGENTS.md` "Key Patterns" section: every path like `internal/command/...`, `internal/validate/...`, `internal/stack/...` etc. — update to new locations
- [ ] verify `CLAUDE.md` symlink still resolves to `AGENTS.md` (`readlink CLAUDE.md` should show `AGENTS.md`)
- [ ] grep for stale path comments in code (lines that mention `internal/<old>` without the `devbox-cli/` prefix — these are doc comments, not import strings, so mass-sed missed them):
  ```bash
  grep -rn 'internal/[a-z_]\+' --include='*.go' | grep -v '"devbox-cli/internal/' | grep -E '//|/\*'
  ```
  Update each match to the new path. Examples flagged at plan-review time: `internal/daemon/daemon.go`, `internal/ui/info.go`, `internal/ui/huh.go`, `internal/validate/validate.go`, `internal/usercommands/model/types.go`, `internal/command/statustui/load.go`. (Note: after Tasks 2–7 these files are at new locations — re-grep on the moved tree.)
- [ ] `make build` — must succeed (embedded AGENTS.md and packages.md regenerated)
- [ ] `make test` — must pass
- [ ] commit: `docs(agents): update path references for layered internal/ structure`

### Task 13: Verify acceptance criteria

- [ ] verify directory layout matches plan: `find internal -maxdepth 3 -type d | sort` shows exactly `cli/`, `core/`, `shared/` and their expected children
- [ ] verify no leftover `internal/<flat-package>/` siblings: `ls internal/` should show ONLY `cli`, `core`, `shared`
- [ ] verify all 36 packages relocated correctly per mapping table (spot-check a few: `internal/core/workflow/snapshot/`, `internal/shared/lock/`, `internal/core/project/user/`)
- [ ] verify `userconfig`/`localconfig` package names no longer exist: `grep -rn '^package userconfig\|^package localconfig' --include='*.go'` returns empty
- [ ] verify `cmd/devbox/main.go` imports updated: `grep -c 'devbox-cli/internal/' cmd/devbox/main.go` returns 7; `grep 'devbox-cli/internal/command\|devbox-cli/internal/config\|devbox-cli/internal/pipeline' cmd/devbox/main.go` returns empty (all paths now layered)
- [ ] verify `Makefile` LDFLAGS point at new version package: `grep 'devbox-cli/internal/shared/version' Makefile` returns 4 matches
- [ ] verify `Makefile` test-race target points at moved paths: `grep -E 'test-race.*internal/(shared|core)' Makefile` returns the updated line
- [ ] verify no `nolint:depguard` was added: `grep -rn 'nolint:depguard' --include='*.go'` returns empty
- [ ] verify no stale files: `[ ! -d internal/docs/embedded ] && [ ! -f internal/docs/content_hashes_gen.go ]`
- [ ] run final gate: `make build && make test && make lint`
- [ ] verify `git log --oneline -20` shows clean refactor commits (no merge mess)
- [ ] verify built binary version: `bin/devbox version` shows real commit hash (not "dev" / "unknown")

### Task 14: Final cleanup

- [ ] verify `docs/plans/completed/` exists (it does — `ls docs/plans/completed/ | head -3`)
- [ ] `mv docs/plans/2026-05-28-internal-restructure-layered.md docs/plans/completed/`
- [ ] commit: `move completed plan: 2026-05-28-internal-restructure-layered.md`

## Post-Completion

*This refactor is self-contained — no external systems, no manual verification needed beyond `make build && make test && make lint` passing.*

**Follow-up PRs (explicitly out of scope here):**
- Drop `userpkg`/`localpkg` aliases where unambiguous (cosmetic; mechanical follow-up after the rename has stabilized)
- Group `cli/` singletons by domain (e.g., `cli/lifecycle/` for run+stop+restart+reset which share preflight + lock + journal patterns)
- API refactorings within packages (e.g., simplifying public surfaces now that boundaries are explicit)
- Renaming any packages beyond `userconfig`/`localconfig`
- Sub-layering inside `core/` (currently soft DAG project ← execution ← workflow; could be enforced if needed)
- Optional: `internal/cli/<sub>/` cross-import prevention via depguard (currently soft guideline)
