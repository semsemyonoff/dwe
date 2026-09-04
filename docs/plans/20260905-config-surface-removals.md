# Config surface removals + `COMPOSE_PROJECT_NAME` export

Four independent changes to the project-config surface, grouped because they
edit the same reference pages (`workspace.md`, `docker.md`, `render/env.md`)
and each needs a paragraph on the upgrade page that is written later.

## Overview

1. **Drop the `state:` root key.** A free-form string with one consumer — a
   line in the bare-`dwe` summary — and zero adoption across the twelve real
   workspaces. The docs claim it is "exported as `STATE` in `.env`"; it never
   was. Removal makes the key a hard load error through the strict-root
   allowlist that already exists.
2. **Drop the `ui:` block.** Three command-browser knobs (`default_expanded_depth`,
   `auto_collapse_empty`, `show_type_badges`) with a dedicated validator and
   two consumers. The three projects with the most commands never set it. The
   browser stays; it runs on `cmdbrowser.DefaultOptions()`, which is exactly
   what an absent block resolved to. The `ui.md` page is the only place the
   browser's hotkeys, fallback ladder, parameter-form overlay and mouse
   behaviour are documented, so that content moves to `commands/index.md`
   instead of disappearing.
3. **Export `COMPOSE_PROJECT_NAME` as the fourth reserved `.env` variable.**
   Eleven of twelve workspaces hand-rebuild the compose project name from
   `PROJECT` in Makefiles and scripts. The value comes from
   `config.ResolveComposeProjectName` — the single resolver, lowercased,
   honouring `docker.yml` / `docker.local.yml` `project_name` — so a manual
   `docker compose` from the project root scopes to the same project as
   `dwe`. Reserving the name is a hard load failure for any project that
   already declares an `exports.env` rule with it; that goes on the upgrade page.
4. **Drop the Windows build stubs.** Nothing ships or checks a Windows build
   (`.goreleaser.yaml` is linux + darwin, no `GOOS=windows` anywhere in CI,
   Makefile or lint). The stubs exist only to keep `GOOS=windows go build`
   type-checking, and `lock_other.go` does it by returning "file locking is
   not supported on Windows" from `Acquire` — a hypothetical Windows binary
   would run the whole lifecycle without deploy or snapshot locks. Removing
   the stubs makes the platform statement true at compile time: macOS and
   Linux; Windows through WSL2.

Not in scope: `docker.local.yml` (alive — written by
`internal/core/workflow/envtest/dockeridentity.go`, swept by `envtest/clean.go`),
`mermaid/term.go`'s `case "windows"`, `notify/native_other.go` (`!darwin`),
`bridgeproto/peercred_other.go`, the i18n store's `ui:` root namespace
(`internal/shared/i18n/translations/en.yml`, `validate/i18n/unknown_ui_key.go`),
`docs/reference/config/state/` (the deploy journal, unrelated to the config key).

## Context (from discovery)

Line numbers are against `release/0.6.0` at `183f62d4`.

**`state:`**
- `internal/core/project/config/workspace.go:44` (`allowedRootKeys`), `:120`
  (`State string`), `:2110` (godoc on `validateLocalCompose` naming it).
- `internal/shared/tpl/render_command.go:138` (`KnownVarHeads`, mirrors the
  allowlist; `TestAllowedRootKeysSubsetOfKnownVarHeads` at
  `workspace_test.go:5578-5595` is a subset check).
- `internal/core/ui/render/summary.go:19-22` — the only consumer.
- No validator, no i18n key, no scaffold value (only the commented
  `# state: ""` at `scaffold/templates/workspace/defaults.yml.tmpl:28`).

**`ui:`**
- `internal/core/project/config/ui.go` (whole file: `UIConfig`,
  `UICommandsConfig`, three nil-safe accessors) + `ui_test.go`.
- `workspace.go:47` (`allowedRootKeys`), `:124` (`UI UIConfig`);
  `render_command.go:141`.
- `internal/core/validate/config/ui.go` (whole file, `uiValidator`; also owns
  `findMappingChild` / `rawChild` — no other callers) + `ui_test.go`;
  registered at `validate/config/all.go:12`; `formal_blocks.go:23-26` explains
  why `ui` is deliberately absent from `formalBlockStructs`;
  `formal_blocks_test.go:156,168-171` asserts `hasUI == false`.
- Consumers: `internal/cli/command/list.go:205-208` and
  `internal/cli/vars/browser.go:54-58` build `cmdbrowser.Options` from the
  accessors. `cmdbrowser.DefaultOptions()` (`cmdbrowser/run.go:227-236`) is
  depth 1 / collapse on / badges on — identical to the absent-block defaults.
- Comments naming the config: `cmdbrowser/run.go:52,151,153`,
  `internal/core/ui/tui/help.go:24-27`.
- `.golangci.yml:35-40` excludes a rule for `config/(info|ui)_test.go`.
- Docs: `docs/reference/config/ui.md` (schema `:9-52`; Hotkeys `:54-77`;
  Parameter form `:78-87`; Fallback ladder `:88-100`; Mouse `:101-109`),
  `docs/reference/config/index.md:121` (TOC — also drives the web sidebar via
  `web/scripts/sync-docs.mjs`), `workspace.md:104,276,506-508`,
  `commands/index.md:288` (links `../ui.md`), `docs/internals/tui-keymap.md:287`,
  `AGENTS.md:23`. RU mirrors: `docs/i18n/ru/reference/config/{ui.md,
  index.md:123, workspace.md:105,277,507-509, commands/index.md}`.

**`COMPOSE_PROJECT_NAME`**
- `config.ReservedExportNames` at `workspace.go:1540` (`PROJECT, UID, GID`);
  loader rejection at `workspace.go:1895-1900` interpolates the slice into the
  error text and is a hard failure for every command.
- `internal/shared/envfile/render.go:32-50` — `BuildContent(cfg)` builds a
  local `systemValues` map and iterates the slice (slice order = line order);
  `write.go:15` `Write(cfg, outputPath)`, `write.go:39` `Regenerate(configPath)`
  already computes `baseDir`.
- Callers: `internal/cli/render/env.go:44,51` (`flags.ProjectRoot()` in scope),
  `internal/core/workflow/lifecycle/run.go:464` (`workDir`),
  `internal/cli/service/service_toggle.go:392,509` (`configPath` in scope),
  `internal/cli/docker/docker.go:65` (via `Regenerate`).
- `config.ResolveComposeProjectName(baseDir, cfg)` at
  `internal/core/project/config/docker.go:261-276`: reads `workspace/docker.yml`
  merged with `docker.local.yml` (`docker.go:358-362`), template-resolves
  `project_name`, falls back to `project.FullName()`, always lowercases.
  Resolution errors propagate; an empty result means `dwe` omits `-p`
  (`internal/shared/docker/compose.go:118`).
- The `type: shell` host contract already exports the same variable from the
  same resolver (`usercommands/runtime/runners/host/host.go:98-125`).
- `deploy.SourceDotEnv` has two production callers: `lifecycle/run.go:467`
  (after `envfile.Write` in `renderAndSourceDotEnv`) and
  `internal/cli/deploy/deploy.go:658-661` (the `postStepHooks` entry keyed on
  `deploy.ImplicitEnvStep.Name`). Both load every `.env` key into the `dwe`
  process env for the rest of the pipeline. `envtest.ScrubComposeEnv`
  unsets ambient `COMPOSE_*` in the `dwe test` process before any subprocess;
  `envtest/copy.go:21` excludes `.env` from the disposable copy, which then
  regenerates its own from its stamped `docker.local.yml`.
- `bridgeclient.StripEnv` needs no change: the daemon already replaces
  `COMPOSE_*` through `hostIdentityEnvPrefixes`.
- Tests that pin the current three names: `envfile/render_test.go:33-59`
  (`strings.Count(out, name+"=")` — `PROJECT=` substring-matches
  `COMPOSE_PROJECT_NAME=`), `scaffold/templates_content_test.go:345-352`
  (every reserved name must appear in `defaults.yml.tmpl`; comment at `:36`),
  `core/docs/llmstxt/generator_test.go:155` + golden
  `llmstxt/testdata/llms_txt_briefing.golden:55-57` (the only golden with the
  reserved-names section). `config/workspace_test.go:2365-2400` iterates
  `ReservedExportNames` in both tests and needs no edit.
- `internal/cli/docs/llmstxt_test.go:160,173` — `TestDocsLlmsTxtCommand_SizeBudget`
  caps `docs llms-txt --no-project` at 12 KB; +22 bytes for the new name is
  offset by the removed `reference/config/ui` topic.

**Windows**
- Stubs: `internal/shared/lock/lock_other.go` (tag `windows` despite the
  name), `internal/core/bridge/exec_windows.go`, `bridge/spawn_windows.go`,
  `internal/core/docs/mermaid/mmdc_windows.go`.
- `!windows` tags: `lock/lock.go:1`, `lock/lock_test.go:1`,
  `lock/project_test.go:1`, `usercommands/runtime/internal/runio/runio_unix_test.go:1`
  (uses `syscall.Pipe`, semantically `unix`).
- `exec_unix.go`, `spawn_unix.go`, `mmdc_unix.go` carry `//go:build unix` and
  stay.
- Docs naming the split: `docs/internals/packages.md:168,177,273,280,281,294,296`.
  Platform claims to align with `docs/reference/concepts/bridge.md:272`
  ("Windows … out of scope; WSL2 works as the Linux case"):
  `docs/reference/config/notifications.md:50,131,197`,
  `userconfig.md:24`, RU `notifications.md:52,133,199`, `userconfig.md:26`.
  `README.md:30` says macOS + Linux for Homebrew only; no platform statement.

**Cross-cutting**
- Docs live in `docs/` (source) and are copied into
  `internal/core/docs/embedded/` by `make embedded-docs`;
  `make gen-docs-manifest` regenerates `internal/core/docs/content_hashes_gen.go`.
  `make build` and `make test` run the sync. Never bare `go test ./...`.
- **RU provenance.** Every RU mirror starts with
  `> Translated from: <relpath> @ <12-hex>`; `internal/core/docs/lang.go:64`
  compares that hash with `ContentHashFor(relPath)` from the generated
  manifest and flags the page as stale when they differ. The manifest hashes
  only `docs/reference`, `docs/guides`, `docs/internals` — not `docs/i18n` —
  and no test checks freshness repo-wide, so a forgotten header ships
  silently. The docs-edit sequence is therefore fixed: edit EN → edit RU →
  `make gen-docs-manifest` → copy each changed hash from
  `content_hashes_gen.go` into the matching RU header → `make embedded-docs`.
- `web/package.json` `npm test` runs the transform against synthetic strings
  only; `npm run sync` is what walks `docs/` and prints the dangling-link
  warning (`web/scripts/sync-docs.mjs:481-483`, a warning, never a failure).
- `CHANGELOG.md` `## [Unreleased]` currently has `### Added` and `### Changed`;
  this plan adds `### Removed`.
- `TestAgentsMdBudget` pins `AGENTS.md` size; the only edit here removes text.

## Development Approach

- **testing approach**: Regular (code first, then tests in the same task).
- One commit per item, in the order below. Commits 1 and 2 touch adjacent
  lines in `workspace.go`, `render_command.go` and the same line of
  `workspace.md:104`, and all four append to `CHANGELOG.md`, so the
  revertability property is **reverse order**: reverting 4, then 3, then 2,
  then 1 applies cleanly; reverting one from the middle needs a manual merge.
  New `envfile` tests go in a **new** file, not appended to `render_test.go`,
  to keep that merge small.
- Tasks 5 and 6 are one atomic unit: Task 5 cannot end green on its own
  (appending to `ReservedExportNames` breaks two existing tests and the
  signature change breaks every caller in `_test.go`), so the "all tests pass"
  gate applies at the end of Task 6.
- Complete each task fully before moving to the next; small, focused changes.
- **CRITICAL: every task includes new/updated tests** for the code it changes —
  both success and error paths; tests are separate checklist items.
- **CRITICAL: all tests pass before the next task** (`make test`, plus the
  focused `go test ./internal/...` runs named in each task).
- **CRITICAL: update this plan when scope changes** (`➕` new tasks, `⚠️`
  blockers).
- Regenerate embedded docs after every docs edit; never edit
  `internal/core/docs/embedded/` or `web/src/content/docs/` by hand.

## Testing Strategy

- **unit tests**: required per task. Removal tasks delete the tests of the
  removed surface and update every fixture that mentions it; the addition
  task gets a fresh table-driven file.
- **goldens**: `scaffold/testdata/golden_default.txt` (item 1),
  `llmstxt/testdata/llms_txt_briefing.golden` (item 3). Regenerate through the
  package's own update mechanism, then eyeball the diff — only the expected
  lines may change.
- **negative control** (item 4): `GOOS=windows go build ./...` must fail
  after the stubs are gone. A passing cross-build means a stub survived.
- **web**: `cd web && npm run sync` walks the real `docs/` tree and must
  print no dangling-link warning naming `ui.md`; `npm test` only covers the
  transform on synthetic input and is run for completeness.
- **e2e**: none in-repo; the live workspace checks are in Post-Completion and
  Task 10.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- keep this file in sync with the actual work

## Solution Overview

**Removals go through the existing strict-root gate.** Dropping `state` and
`ui` from `allowedRootKeys` makes `validateLayerRoots` (`layers.go:369-388`)
reject them per layer with the file name and the allowed list. No deprecation
branch, no warning tier: adoption is zero, and the strict root is the policy
the loader already enforces for every other unknown key. `dwe validate`
proceeds past a failed config load, so the diagnostic stays visible there too.

**The browser keeps its options, loses its config.** `cmdbrowser.Options`
stays as is (about a hundred browser tests build it); the two CLI consumers
start from `cmdbrowser.DefaultOptions()` and set only their own fields.
Runtime behaviour is byte-identical for every existing project.

**`COMPOSE_PROJECT_NAME` is resolved inside `envfile`, not by the callers.**
`BuildContent` and `Write` gain a `baseDir` parameter and call
`config.ResolveComposeProjectName` themselves, so the "one resolver" invariant
lives in one place instead of four call sites. Rejected: reading
`cfg.Raw["__configPath"]` (absent on literal-built configs; hidden contract).

Three rules for the value:
- resolver error → wrapped and returned. `dwe render env` and the implicit
  deploy step fail. A silently wrong name in `.env` is precisely the
  split-brain the export exists to remove, and a broken `project_name`
  template fails the compose step a moment later anyway.
- resolved empty → the line is **omitted**, mirroring the compose wrapper
  omitting `-p`. An empty `COMPOSE_PROJECT_NAME=` would override compose's
  own default with nothing.
- resolved non-empty → the same `checkValue` guards as `PROJECT` (undecrypted
  marker, single line), with its own `systemSources` label.

`PROJECT` keeps its raw `project.name` semantics; `COMPOSE_PROJECT_NAME` is
the lowercased compose name. The docs say so explicitly, and the
`type: shell` env contract — which already exports the same name from the same
resolver — gets a cross-reference so the two sources read as one value.

**Windows: delete the stubs, keep the honest tags.** The four `*_windows.go`
files and `lock_other.go` go; `!windows` on `lock.go` and its tests goes;
`runio_unix_test.go` gets `unix` because that is what it needs. The
`*_unix.go` files and their `unix` tags are untouched — the code is unix-only,
and merging them into the main files is a refactor this plan does not need.

## Technical Details

### `envfile` after the change

```go
// BuildContent renders the .env content from the export spec.
//
// System variables (always emitted, not part of the export spec):
//   - PROJECT              — project.name, verbatim
//   - UID / GID            — host UID/GID for container builds
//   - COMPOSE_PROJECT_NAME — the compose project name dwe passes as -p:
//     docker.yml/docker.local.yml project_name, else <prefix>-<name>,
//     always lowercased. Omitted when the name resolves empty (dwe then
//     omits -p as well). Resolution errors fail the render.
func BuildContent(cfg *config.DweConfig, baseDir string) (string, error)
func Write(cfg *config.DweConfig, baseDir, outputPath string) error
```

Inside `BuildContent`:

```go
composeName, err := config.ResolveComposeProjectName(baseDir, cfg)
if err != nil {
    return "", fmt.Errorf("compose project name: %w", err)
}
systemValues := map[string]string{
    "PROJECT": cfg.Project.Name, "UID": HostUID(), "GID": HostGID(),
    "COMPOSE_PROJECT_NAME": composeName,
}
systemSources := map[string]string{
    "PROJECT":              "project.name",
    "COMPOSE_PROJECT_NAME": "the compose project name (docker.yml project_name or project.prefix/name)",
}
for _, name := range config.ReservedExportNames {
    value := systemValues[name]
    if name == "COMPOSE_PROJECT_NAME" && value == "" {
        continue // mirrors the compose wrapper omitting -p
    }
    if err := checkValue(name, systemSources[name], value); err != nil { … }
    fmt.Fprintf(&b, "%s=%s\n", name, value)
}
```

Output order: `PROJECT`, `UID`, `GID`, `COMPOSE_PROJECT_NAME`, then user rules.

### `ReservedExportNames`

```go
var ReservedExportNames = []string{"PROJECT", "UID", "GID", "COMPOSE_PROJECT_NAME"}
```

The loader error becomes
`exports.env[N]: "COMPOSE_PROJECT_NAME" is a reserved system variable and cannot be redeclared as an export rule (reserved: PROJECT, UID, GID, COMPOSE_PROJECT_NAME)`
with no code change at the site.

### Browser consumers

```go
opts := cmdbrowser.DefaultOptions()
opts.IncludePrivate = includePrivate
opts.Mode = mode
opts.RunForm = …
opts.Translator, opts.Locale = translator, locale
```

### `.env` sourced into the `dwe` process

Both `SourceDotEnv` sites — `renderAndSourceDotEnv` in `dwe run` and the
implicit-step post hook in `dwe deploy run` — now also set
`COMPOSE_PROJECT_NAME` in the `dwe` process environment for the rest of the
pipeline. For dwe's own compose calls nothing changes: every one goes
through `docker.Compose`, which always passes `-p` when the name is non-empty
(`compose.go:117-120`); `type: dwe` steps re-exec with `os.Environ()`
(`pipeline/executor.go:176`) and the nested dwe resolves and passes `-p`
again; the `type: shell` user-command contract appends its own copy after
`os.Environ()` so it wins on Go's last-key dedupe (`host.go:110-123`).
Record this in `packages.md` beside the `SourceDotEnv` description so nobody
later reads the ambient variable as a bug.

### Raw `docker compose` now follows dwe's project name

This is the user-visible part and it cuts both ways. `.env` lives at the
project root, which is also compose's project directory (`compose.go:151`
sets `cmd.Dir = c.BaseDir`). Docker Compose reads `.env` there and honours
`COMPOSE_PROJECT_NAME` from it **above** a compose file's top-level `name:`.
Two paths that never passed `-p` therefore change scope:

- a raw `docker compose …` from the project root (a Makefile target, a
  developer's shell) — previously scoped by the compose file's `name:` or the
  directory name, now by dwe's resolved name;
- a pipeline `type: shell` step that calls `docker compose` itself — it
  inherits the sourced variable (`bridgeclient/env.go:50-51`: pipeline shell
  steps set no `cmd.Env`).

For the eleven workspaces that rebuild the name by hand this is the fix they
wanted. For a project whose compose chain declares a `name:` that diverges
from dwe's resolver — the population `config.compose_project_name` already
warns about — raw compose now agrees with `dwe` and stops seeing the
containers, networks and volumes it created under the old name. That goes on
the upgrade page, in `docker.md`, and in the `validate.md:74` paragraph,
which gains a third remedy: `name: ${COMPOSE_PROJECT_NAME}` in the compose
file now resolves from `.env` (the validators at `compose_name.go:70,73` and
`container_name.go:57-58` already skip that interpolated form as unprovable).

### "Always emitted" becomes "emitted when resolvable"

The `ReservedExportNames` doc comment (`workspace.go:1535-1539`) and
`render/env.md:41` both say the system variables are *always* emitted. With
the omit-when-empty rule that is true for three of the four; both sentences
get the qualification.

## Implementation Steps

### Task 1: Remove the `state:` root key from the loader, templates and summary

**Files:**
- Modify: `internal/core/project/config/workspace.go`
- Modify: `internal/core/project/config/workspace_test.go`
- Modify: `internal/core/project/config/services_overlay_test.go`
- Modify: `internal/shared/tpl/render_command.go`
- Modify: `internal/shared/tpl/render_command_test.go`
- Modify: `internal/core/ui/render/summary.go`
- Modify: `internal/core/ui/render/summary_test.go`
- Modify: `internal/core/validate/config/template_refs.go`
- Modify: `internal/core/validate/config/template_refs_test.go`

- [ ] remove `"state"` from `allowedRootKeys` (`workspace.go:44`), the
      `State string` field (`:120`) and the mention in the `validateLocalCompose`
      godoc (`:2110`)
- [ ] `template_refs.go:25,55`: the validator's doc comment uses `${state}`
      as its head-only example — switch the example to `${update}` / `${host}`
      so the comment does not name a removed key
- [ ] remove `"state"` from `tpl.KnownVarHeads` (`render_command.go:138`);
      keep `TestAllowedRootKeysSubsetOfKnownVarHeads` green
- [ ] drop the `state` line from `render.Summary` (`summary.go:19-22`) and
      its godoc; the summary now starts at the counts line
- [ ] update fixtures: `workspace_test.go:104,121,281,362,3774` (`state: ""`
      lines), `:321,346-348` and `:721,727-729` (layer-precedence cases —
      switch to another scalar root key such as `runtime.*` or drop the case
      if it only proved `state`), `:812-813` (`ResolvePath` — use a different
      key), `:2337-2352` (`from: state` export rule — use `from: project.name`
      or a `vars.*` path); `services_overlay_test.go:614,628-637` (keep the
      "unknown top-level keys are accepted" subtest, rename the key in both
      the comment and the fixture); `render_command_test.go:38` (`${state}`
      head-only literal case — replace with another head, e.g. `${host}`);
      `template_refs_test.go:93,107` (rename the shell loop variable
      `state` → `item`; `${item}` is then filtered by `IsAllowedRootKey`, not
      the head-only rule, so keep `${vars}` and `${stop}` on the same line —
      they are what still exercise the head-only assertion)
- [ ] add a test asserting that `state: x` in any layer fails `LoadConfig`
      with the strict-root error naming the file (table over
      `workspace.yml` / `defaults.yml` / `local.yml`)
- [ ] update `summary_test.go` (`:30,34,43-45,147,152`) — remove the
      `State: "running"` fixtures and the "label absent when empty" case
- [ ] run `go test ./internal/core/project/config/... ./internal/shared/tpl/... ./internal/core/ui/render/... ./internal/core/validate/...` — must pass before Task 2

### Task 2: `state:` — scaffold, docs, changelog; commit 1

**Files:**
- Modify: `internal/core/workflow/scaffold/templates/workspace/defaults.yml.tmpl`
- Modify: `internal/core/workflow/scaffold/testdata/golden_default.txt`
- Modify: `docs/reference/config/workspace.md`
- Modify: `docs/reference/config/index.md`
- Modify: `docs/reference/templates.md`
- Modify: `docs/reference/concepts/project-layout.md`
- Modify: `docs/i18n/ru/reference/config/workspace.md`
- Modify: `docs/i18n/ru/reference/config/index.md`
- Modify: `docs/i18n/ru/reference/templates.md`
- Modify: `docs/i18n/ru/reference/concepts/project-layout.md`
- Modify: `CHANGELOG.md`
- Modify: `internal/core/docs/content_hashes_gen.go` (generated, git-tracked)

- [ ] delete the `# state: ""` line from `defaults.yml.tmpl:28`; regenerate
      `golden_default.txt` and confirm the diff is that single line
- [ ] `workspace.md`: drop the TOC entry (`:21`), the layer-placement row
      (`:63`), `state` from the strict-root list (`:104`) and the layer table
      (`:278`), the whole `### state` section (`:326-332`), the
      `state: staging` line in the `local.yml` example (`:408`), and the two
      "Common mistakes" bullets (`:502-503`); same edits in the RU mirror
      (`:23,105,279,327-333,409,503-504`)
- [ ] `config/index.md:21`, `templates.md:65`, `concepts/project-layout.md:103`
      + RU mirrors: remove `state` from the root-key lists
- [ ] `CHANGELOG.md`: add `### Removed` under `[Unreleased]` with the
      `state:` entry (hard load error, no replacement; free-form values belong
      in `vars:`)
- [ ] `make gen-docs-manifest`, then copy the new hashes for
      `reference/config/workspace.md`, `reference/config/index.md`,
      `reference/templates.md`, `reference/concepts/project-layout.md` into
      the `> Translated from:` header of each RU mirror; `make embedded-docs`
- [ ] `make test` for `./internal/core/docs/...` and
      `./internal/core/workflow/scaffold/...`
- [ ] commit: `refactor(config)!: drop the state: root key`

### Task 3: Remove the `ui:` block from the loader, validator and browser consumers

**Files:**
- Delete: `internal/core/project/config/ui.go`
- Delete: `internal/core/project/config/ui_test.go`
- Delete: `internal/core/validate/config/ui.go`
- Delete: `internal/core/validate/config/ui_test.go`
- Modify: `internal/core/project/config/workspace.go`
- Modify: `internal/core/project/config/workspace_test.go`
- Modify: `internal/shared/tpl/render_command.go`
- Modify: `internal/core/validate/config/all.go`
- Modify: `internal/core/validate/config/formal_blocks.go`
- Modify: `internal/core/validate/config/formal_blocks_test.go`
- Modify: `internal/cli/command/list.go`
- Modify: `internal/cli/vars/browser.go`
- Modify: `internal/core/ui/cmdbrowser/run.go`
- Modify: `internal/core/ui/tui/help.go`
- Modify: `.golangci.yml`

- [ ] delete the four `ui.go` / `ui_test.go` files; remove `"ui"` from
      `allowedRootKeys` (`workspace.go:47`), the `UI UIConfig` field (`:124`)
      and `KnownVarHeads` (`render_command.go:141`)
- [ ] remove `&uiValidator{}` from `validate/config/all.go:12`; confirm
      `findMappingChild` / `rawChild` had no other callers (grep) and are gone
      with the file
- [ ] `formal_blocks.go:22-26`: delete only the sentences about `ui` and keep
      the "`vars` (free-form) and `services` (per-service) are likewise out of
      scope by design" sentence at `:25-26` (line 25 mixes the tail of the
      `ui` sentence with the start of the kept one — reflow);
      `formal_blocks_test.go:156-157` (the `ui` comment — line 158 is a live
      `port_release_timeout` assertion, keep it; the sentence opener on `:155`
      needs the same reflow) and `:168-171` (`hasUI` comment + assertion):
      delete both
- [ ] `cli/command/list.go:205-208` and `cli/vars/browser.go:54-58`: build
      from `cmdbrowser.DefaultOptions()` and set only the consumer's own fields
- [ ] rewrite the comments at `cmdbrowser/run.go:52,151,153,227` (no
      `config.UICommands*` reference — `:227` is the `DefaultOptions` godoc;
      the `Options` doc describes the fields on their own) and
      `tui/help.go:24-27` (drop the sentence about the workspace.yml validator)
- [ ] `.golangci.yml:34-41`: narrow the exclusion path to
      `config/info_test\.go` and delete the stale comment line `:35` about
      `ui_test.go`'s `intPtr`; that helper lived only in the deleted
      `config/ui_test.go` (`info_test.go` uses its own `ptrBool`) — confirm
      with a grep that nothing else in the package references `intPtr`
- [ ] add a test asserting `ui: {commands: {}}` in any layer fails
      `LoadConfig` with the strict-root error (extend the table from Task 1)
- [ ] run `make lint` and `go test ./internal/core/project/config/... ./internal/core/validate/... ./internal/cli/command/... ./internal/cli/vars/... ./internal/core/ui/...` — must pass before Task 4

### Task 4: `ui:` — relocate browser docs, fix cross-references; commit 2

**Files:**
- Delete: `docs/reference/config/ui.md`
- Delete: `docs/i18n/ru/reference/config/ui.md`
- Modify: `docs/reference/config/commands/index.md`
- Modify: `docs/i18n/ru/reference/config/commands/index.md`
- Modify: `docs/reference/config/index.md`
- Modify: `docs/i18n/ru/reference/config/index.md`
- Modify: `docs/reference/config/workspace.md`
- Modify: `docs/i18n/ru/reference/config/workspace.md`
- Modify: `docs/reference/config/validate.md`
- Modify: `docs/i18n/ru/reference/config/validate.md`
- Modify: `docs/internals/tui-keymap.md`
- Modify: `AGENTS.md`
- Modify: `CHANGELOG.md`
- Modify: `internal/core/docs/content_hashes_gen.go` (generated, git-tracked)

- [ ] `commands/index.md`: replace the paragraph at `:288` (links `../ui.md`)
      with a new `## Interactive browser` section carrying the Hotkeys table,
      Parameter form overlay, Fallback ladder and Mouse content from
      `ui.md:54-109` verbatim; drop the schema, pointer-semantics, example and
      `## Related` (`:110`) sections — fold any still-valid `Related` link into
      the page's existing `## Further reading`; add the new section to the
      page's `## Contents`; RU mirror gets the RU `ui.md` counterparts
- [ ] delete `ui.md` EN + RU
- [ ] `validate.md:76` + RU `:78`: the `config.formal_block_fields`
      paragraph ends with "The `ui:` block is covered by the dedicated
      `config.ui` validator instead …" — rewrite the sentence so it only names
      the intentionally excluded free-form and per-service blocks
- [ ] `config/index.md:121` + RU `:123`: drop the TOC entry (this also removes
      the web sidebar item); `workspace.md:104` (root list), `:276`
      ("Compact formalized blocks" row), `:506-508` (the `## Optional ui: block`
      section) + RU `:105,277,507-509`
- [ ] `docs/internals/tui-keymap.md:287`: rewrite the sentence that cites the
      `ui:` validator; `AGENTS.md:23`: drop "and command browser settings
      (`ui.md`)"
- [ ] `CHANGELOG.md` `### Removed`: `ui:` block entry (hard load error, browser
      behaviour unchanged, hotkey docs now under `commands/index.md`)
- [ ] `make gen-docs-manifest`, then copy the new hashes for
      `reference/config/commands/index.md`, `reference/config/index.md`,
      `reference/config/workspace.md`, `reference/config/validate.md` into the
      RU headers; `make embedded-docs`; confirm `content_hashes_gen.go` no
      longer lists `reference/config/ui.md`
- [ ] `make test`; `cd web && npm run sync` must print no dangling-link
      warning naming `ui.md` (sidebar derives from the config index TOC);
      `npm test`
- [ ] commit: `refactor(config)!: drop the ui: block`

### Task 5: Reserve `COMPOSE_PROJECT_NAME` and emit it from `envfile`

**Files:**
- Modify: `internal/core/project/config/workspace.go`
- Modify: `internal/shared/envfile/render.go`
- Modify: `internal/shared/envfile/write.go`
- Modify: `internal/cli/render/env.go`
- Modify: `internal/core/workflow/lifecycle/run.go`
- Modify: `internal/cli/service/service_toggle.go`

- [ ] `workspace.go:1540`: append `"COMPOSE_PROJECT_NAME"` to
      `ReservedExportNames`; rewrite the doc comment (`:1535-1539`): the slice
      order is the `.env` emission order, and "always emits" becomes "emits
      whenever the value resolves" (the compose name is omitted when empty)
- [ ] `render.go`: `BuildContent(cfg, baseDir)`; resolve via
      `config.ResolveComposeProjectName(baseDir, cfg)`, wrap errors as
      `compose project name: %w`, skip the line when empty, guard non-empty
      values with `checkValue` and a `systemSources` label; rewrite the godoc
      per Technical Details
- [ ] `write.go`: `Write(cfg, baseDir, outputPath)`; `Regenerate` passes the
      `baseDir` it already computes
- [ ] update callers: `cli/render/env.go:44,51` (`flags.ProjectRoot()`),
      `lifecycle/run.go:464` (`workDir` — also fix the stale "workspace/.env"
      wording in the godoc at `:458`), `service_toggle.go:392,509` (both
      `mutateAndPlan` at `:327` and `mutateAndPlanBatch` at `:451` already take
      `baseDir` as a parameter — pass it, do not re-derive from `configPath`);
      `cli/docker/docker.go:65` is unchanged
- [ ] `go build ./...` clean; `go vet` type-checks `_test.go` too, so it and
      the two known test breaks are expected to fail here and are fixed in
      Task 6 — both the vet-clean and tests-green gates are at the end of
      Task 6

### Task 6: `COMPOSE_PROJECT_NAME` — tests, goldens, scaffold comment

**Files:**
- Create: `internal/shared/envfile/compose_name_test.go`
- Modify: `internal/shared/envfile/render_test.go`
- Modify: `internal/shared/envfile/write_test.go`
- Modify: `internal/shared/envfile/secrets_test.go`
- Modify: `internal/cli/render/env_test.go`
- Modify: `internal/core/workflow/scaffold/templates/workspace/defaults.yml.tmpl`
- Modify: `internal/core/workflow/scaffold/testdata/golden_default.txt`
- Modify: `internal/core/docs/llmstxt/generator_test.go`
- Modify: `internal/core/docs/llmstxt/testdata/llms_txt_briefing.golden`

- [ ] `compose_name_test.go` (table-driven, temp dirs): (a) no `docker.yml` →
      `COMPOSE_PROJECT_NAME=<prefix>-<name>` lowercased, last in the system
      block; (b) uppercase `project.name` → lowercase value while `PROJECT=`
      keeps the original case; (c) `workspace/docker.yml` `project_name` wins
      over `<prefix>-<name>`; (d) `workspace/docker.local.yml` `project_name`
      wins over `docker.yml` (this is the `dwe test` copy mechanism); (e) empty
      `project.name` **and** empty `project.prefix`, no `docker.yml` → no
      `COMPOSE_PROJECT_NAME` line at all (`FullName()` returns `prefix-` for a
      prefix-only project, which is non-empty and is emitted);
      (f) a `project_name` with an unresolvable `${…}` template → `BuildContent`
      returns an error mentioning `compose project name`; (g) `Regenerate` on a
      temp project writes the line
- [ ] `render_test.go:33-59` (`silentlyDropsReservedRules`): switch from
      `strings.Count(out, name+"=")` to counting lines with
      `strings.HasPrefix(line, name+"=")`; fix every other `BuildContent`/`Write`
      call for the new signature (`render_test.go`, `write_test.go:25,71`,
      `secrets_test.go`, `cli/render/env_test.go` ×10 + `:260`) — the marker
      and multi-line guards in `secrets_test.go` must still exercise
      `PROJECT`
- [ ] `workspace_test.go:2365-2400`: no edit — both
      `TestLoadConfig_reservedExportNameRejected` and `TestIsReservedExportName`
      range over `ReservedExportNames`, so the fourth name is covered
      automatically; just confirm they pass and that `"PROJECT_NAME"` stays in
      the negative list
- [ ] `defaults.yml.tmpl:36`: extend the comment to
      `# PROJECT, UID, GID and COMPOSE_PROJECT_NAME are injected automatically and must not be redeclared.`
      (pinned by `templates_content_test.go:345-352`); regenerate
      `golden_default.txt`
- [ ] `llmstxt/generator_test.go:155`: add the fourth name; regenerate
      `llms_txt_briefing.golden` and confirm
      `TestDocsLlmsTxtCommand_SizeBudget` (`internal/cli/docs/llmstxt_test.go:173`,
      12 KB cap) still passes
- [ ] run `make test` for `./internal/shared/envfile/... ./internal/cli/render/... ./internal/cli/service/... ./internal/core/project/config/... ./internal/core/workflow/... ./internal/core/docs/...` — must pass before Task 7

### Task 7: `COMPOSE_PROJECT_NAME` — docs, skills, internals; commit 3

**Files:**
- Modify: `docs/reference/render/env.md`
- Modify: `docs/reference/config/workspace.md`
- Modify: `docs/reference/config/docker.md`
- Modify: `docs/reference/config/validate.md`
- Modify: `docs/reference/config/secrets.md`
- Modify: `docs/reference/docs/commands.md`
- Modify: `docs/reference/config/commands/types.md`
- Modify: `docs/guides/start-a-new-project.md`
- Modify: `docs/guides/author-project-commands.md`
- Modify: RU mirrors of all of the above under `docs/i18n/ru/`
- Modify: `docs/internals/packages.md`
- Modify: `skills/dwe/SKILL.md`
- Modify: `skills/dwe/references/populate-init-repo.md`
- Modify: `skills/dwe/references/authoring-commands.md`
- Modify: `CHANGELOG.md`
- Modify: `internal/core/docs/content_hashes_gen.go` (generated, git-tracked)

- [ ] `render/env.md`: mermaid node (`:26`), "Three variables are always
      emitted" → four, emitted whenever they resolve, with a new table row
      (`:41-49`), reword the `PROJECT` row so it no longer claims
      to be the compose project name (`:45`), the reserved-names sentence
      (`:49`), the guard paragraph (`:170-171`), the output-format block
      (`:188-196`), the worked example (`:265-272`), the redeclare pitfall
      (`:290`); add a short "differs from `PROJECT`" note (lowercased,
      `docker.yml` precedence, omitted when empty, same value as the
      `type: shell` contract); RU mirror (`:28,43-51,173,190-194,267-271,292`)
- [ ] `config/workspace.md:366-376` ("Implicit system variables": four rows) +
      RU `:373-375`; `config/docker.md:80-95` + RU `:82-95`: state that
      `project_name` is also written to `.env` as `COMPOSE_PROJECT_NAME`, and
      that a raw `docker compose` from the project root (and a pipeline
      `type: shell` step) therefore scopes to the same project as `dwe`, above
      any `name:` in the compose file; `config/validate.md:74` + RU: the
      `config.compose_project_name` paragraph gains the third remedy
      `name: ${COMPOSE_PROJECT_NAME}`, now resolvable from `.env`;
      `config/secrets.md:733` + RU `:745` ("three system variables" → four);
      `docs/commands.md:182` + RU `:184` (llms-txt section list);
      `guides/start-a-new-project.md:122` + RU `:124`
- [ ] `commands/types.md:77,91` and `guides/author-project-commands.md:73,86`
      (+ RU): cross-reference — the shell contract's `COMPOSE_PROJECT_NAME` and
      the `.env` line are the same value from the same resolver
- [ ] `docs/internals/packages.md`: update the `envfile` / reserved-exports
      description and add the `SourceDotEnv` note (the variable now lands in
      the `dwe` process env at both call sites — `dwe run`'s
      `renderAndSourceDotEnv` and `dwe deploy run`'s implicit-step post hook;
      harmless because `-p` is always passed and the shell contract's copy
      wins on dedupe)
- [ ] `skills/dwe/SKILL.md:75-77`, `references/populate-init-repo.md:71`,
      `references/authoring-commands.md:74`: four reserved names; note the
      `.env` line beside the shell-contract mention
- [ ] `CHANGELOG.md` `### Added`: `COMPOSE_PROJECT_NAME` entry including the
      collision consequence (a project with its own export rule of that name
      fails to load; delete the rule) and the raw-compose scope change (a
      compose file with a divergent top-level `name:` is now overridden from
      `.env`)
- [ ] `make gen-docs-manifest`, then copy the new hashes into the RU headers
      of every page edited in this task (`reference/render/env.md`,
      `reference/config/{workspace,docker,validate,secrets}.md`,
      `reference/docs/commands.md`, `reference/config/commands/types.md`,
      `guides/{start-a-new-project,author-project-commands}.md`);
      `make embedded-docs`
- [ ] `make test` for `./internal/core/docs/...` and `./internal/cli/docs/...`
- [ ] commit: `feat(env)!: export COMPOSE_PROJECT_NAME as a reserved .env variable`

### Task 8: Drop the Windows build stubs

**Files:**
- Delete: `internal/shared/lock/lock_other.go`
- Delete: `internal/core/bridge/exec_windows.go`
- Delete: `internal/core/bridge/spawn_windows.go`
- Delete: `internal/core/docs/mermaid/mmdc_windows.go`
- Modify: `internal/shared/lock/lock.go`
- Modify: `internal/shared/lock/lock_test.go`
- Modify: `internal/shared/lock/project_test.go`
- Modify: `internal/core/usercommands/runtime/internal/runio/runio_unix_test.go`
- Modify: `internal/core/docs/mermaid/mmdc.go`

- [ ] delete the four stub files
- [ ] remove the `//go:build !windows` line (and the blank line after it) from
      `lock.go`, `lock_test.go`, `project_test.go`
- [ ] `runio_unix_test.go:1`: `//go:build unix`
- [ ] `mermaid/mmdc.go:82`: fix the comment that points at `mmdc_windows.go`
- [ ] negative control: `GOOS=windows go build ./... 2>&1 | head` must fail
      (undefined `setProcessGroup` / `spawnDetached` / `syscall.Flock` …);
      record the first error line in this plan under ⚠️/➕ if it is anything
      other than a missing-symbol error
- [ ] `go build ./...`, `make lint`, `make test-race` (it names
      `./internal/shared/lock` explicitly) — must pass before Task 9

### Task 9: Windows — platform statement in docs and README; commit 4

**Files:**
- Modify: `docs/internals/packages.md`
- Modify: `docs/reference/config/notifications.md`
- Modify: `docs/reference/config/userconfig.md`
- Modify: `docs/i18n/ru/reference/config/notifications.md`
- Modify: `docs/i18n/ru/reference/config/userconfig.md`
- Modify: `README.md`
- Modify: `docs/i18n/ru/README.md`
- Modify: `CHANGELOG.md`
- Modify: `internal/core/docs/content_hashes_gen.go` (generated, git-tracked)

- [ ] `packages.md:168` (validate/bridge `path.IsAbs` rationale — keep the
      rule, drop "a Windows host"), `:177` (mermaid — drop "default kill on
      Windows"), `:273` (notify — platform split wording stays; it is
      `!darwin`, not Windows-specific — verify no Windows claim), `:280,281`
      (bridge — drop the `exec_windows.go` / `spawn_windows.go` names),
      `:294` (bridgeproto — replace "mirroring the shared/lock fallback shape"),
      `:296` (`shared/lock` — no longer "Unix-only build tag"; say the package
      is unix-only by construction and a `GOOS=windows` build fails at compile
      time on purpose)
- [ ] `notifications.md:50,131,197` + RU `:52,133,199`; `userconfig.md:24` +
      RU `:26`: replace the "every OS (Linux, macOS, Windows)" / "Windows
      toast" claims with "macOS and Linux; on Windows run dwe inside WSL2",
      matching `bridge.md:272`
- [ ] `README.md`: one "Supported platforms" sentence under `## Install`
      (macOS Intel + Apple Silicon, Linux; Windows through WSL2 with dwe
      installed in the distro); same in `docs/i18n/ru/README.md` under
      `## Установка`
- [ ] `CHANGELOG.md` `### Removed`: Windows build stubs entry with the
      platform statement
- [ ] `make gen-docs-manifest`, then copy the new hashes into the RU headers
      of `reference/config/notifications.md`, `reference/config/userconfig.md`
      **and `README.md`** — the root README is a manifest topic too
      (`scripts/gen-docs-content-hashes.sh:29-34`, `content_hashes_gen.go:9`)
      and `docs/i18n/ru/README.md:1` carries a `> Translated from: README.md @ …`
      header; `make embedded-docs`
- [ ] `make test` for `./internal/core/docs/...`
- [ ] commit: `chore: drop Windows build stubs`

### Task 10: Verify acceptance criteria

- [ ] every item in Overview is implemented: `state:` and `ui:` fail to load
      with the strict-root error; `.env` ends its system block with
      `COMPOSE_PROJECT_NAME`; `GOOS=windows go build ./...` fails
- [ ] full suite: `make build && make test && make test-race && make lint`
- [ ] `go vet ./...` clean; `cd web && npm test`
- [ ] `git log --oneline -4` shows the four commits in order; on a throwaway
      branch `git revert --no-edit` of commit 4, then 3, then 2, then 1
      applies cleanly in that reverse order (the forward-order property does
      not hold — see Development Approach)
- [ ] `dwe docs show --lang ru <every RU page touched>` shows no stale
      translation warning (provenance hashes match the regenerated manifest)
- [ ] grep the twelve real workspaces for `name: COMPOSE_PROJECT_NAME` under
      `exports:`, and for top-level `state:` / `ui:` in `workspace.yml`,
      `workspace/defaults.yml`, `workspace/local.yml` — expected: zero hits
- [ ] live check on the beetDeck workspace with `bin/dwe`: `dwe render env`
      prints `COMPOSE_PROJECT_NAME=<lowercase>` as the last system line and
      the value equals the `-p` argument echoed by `dwe status --debug`
      (compose probes echo at Debug level); `dwe run` deploys;
      `dwe test run <one scenario>` passes and the copy's `.env` (path from
      the run's manifest) carries the copy's stamped name, not the original's
- [ ] ⚠️/➕ every deviation into this plan

### Task 11: [Final] Update documentation and file the plan

- [ ] `AGENTS.md` Critical Patterns: no new pattern is needed; confirm the
      `Encrypted secrets` and `Compose project name` bullets still read
      correctly with four reserved names and the envfile signature change
- [ ] `docs/internals/packages.md`: final read-through of the four edited
      sections for stale line references
- [ ] the "Upgrade notes" section below is complete in EN + RU and ready to
      paste into the upgrade guide when that page is written
- [ ] move this plan to `docs/plans/completed/`

## Upgrade notes (ready to paste into the upgrade guide)

Written now so the guide, which is authored after this change lands, can lift
them verbatim.

### EN

**`state:` removed.** The top-level `state:` key is no longer accepted in
`workspace.yml`, `workspace/defaults.yml` or `workspace/local.yml`; a project
that still declares it fails to load with
`unknown root key "state" … allowed: …` naming the file. It had no effect
beyond one line in the bare-`dwe` summary and was never exported to `.env`.
Delete the key; a free-form value belongs under `vars:`.

**`ui:` removed.** The `ui.commands` block (`default_expanded_depth`,
`auto_collapse_empty`, `show_type_badges`) is no longer accepted; a project
that declares it fails to load with the same strict-root error. The command
browser is unchanged and runs with the former defaults (top-level groups
expanded, empty subtrees collapsed during filtering, type badges on). Delete
the block. Hotkeys, the fallback ladder and mouse behaviour are now documented
under *Interactive browser* on the commands reference page.

**`COMPOSE_PROJECT_NAME` is reserved.** `.env` now ends its system block with
`COMPOSE_PROJECT_NAME=<name>`, where `<name>` is the compose project name
`dwe` passes as `-p`: `project_name` from `workspace/docker.yml` or
`docker.local.yml`, otherwise `<project.prefix>-<project.name>`, always
lowercased. It can differ from `PROJECT`, which stays the verbatim
`project.name`. A project whose `exports.env` already declares a rule named
`COMPOSE_PROJECT_NAME` fails to load **every** command with
`exports.env[N]: "COMPOSE_PROJECT_NAME" is a reserved system variable …`;
delete the rule — the built-in line carries the same value. Scripts that
rebuilt the name from `PROJECT` by hand can read the variable instead. The
line is regenerated on every `dwe run` and `dwe render env`; no forced
redeploy is needed.

Because `.env` sits in the compose project directory, a raw `docker compose`
run from the project root — and a pipeline `type: shell` step that calls
compose itself — now scopes to dwe's project name, above any top-level
`name:` in your compose file. If your compose file declared a different
`name:` (`dwe validate` has been warning about this as
`config.compose_project_name`), resources created under that old name are no
longer visible to raw compose: either set `project_name` in
`workspace/docker.yml` to the old value (this re-scopes `dwe` itself too, so
redeploy once), or change the compose file to
`name: ${COMPOSE_PROJECT_NAME}`, which now resolves from `.env`.

**Windows builds.** dwe supports macOS and Linux; on Windows run it inside
WSL2 with dwe installed in the distro. The Windows compile stubs are gone, so
`GOOS=windows go build` now fails instead of producing a binary that takes no
project locks.

### RU

**Ключ `state:` удалён.** Корневой ключ `state:` больше не принимается в
`workspace.yml`, `workspace/defaults.yml` и `workspace/local.yml`; проект, где
он остался, не загружается с ошибкой `unknown root key "state" … allowed: …`
с именем файла. Он влиял только на одну строку сводки `dwe` без аргументов и
никогда не экспортировался в `.env`. Удалите ключ; произвольное значение
живёт в `vars:`.

**Блок `ui:` удалён.** Блок `ui.commands` (`default_expanded_depth`,
`auto_collapse_empty`, `show_type_badges`) больше не принимается; проект с ним
не загружается с той же ошибкой строгого корня. Браузер команд не изменился и
работает с прежними умолчаниями (верхние группы раскрыты, пустые поддеревья
сворачиваются при фильтре, бейджи типов включены). Удалите блок. Хоткеи,
лестница фолбэков и мышь теперь описаны в разделе *Interactive browser* на
странице справочника команд.

**`COMPOSE_PROJECT_NAME` зарезервировано.** Системный блок `.env` теперь
заканчивается строкой `COMPOSE_PROJECT_NAME=<name>`, где `<name>` — имя
compose-проекта, которое `dwe` передаёт в `-p`: `project_name` из
`workspace/docker.yml` или `docker.local.yml`, иначе
`<project.prefix>-<project.name>`, всегда в нижнем регистре. Оно может
отличаться от `PROJECT`, который по-прежнему равен `project.name` как есть.
Проект, в чьём `exports.env` уже есть правило с именем `COMPOSE_PROJECT_NAME`,
перестаёт загружаться для **всех** команд с ошибкой
`exports.env[N]: "COMPOSE_PROJECT_NAME" is a reserved system variable …`;
удалите правило — встроенная строка несёт то же значение. Скрипты, которые
собирали имя из `PROJECT` вручную, могут читать переменную напрямую. Строка
перегенерируется на каждом `dwe run` и `dwe render env`; принудительный
передеплой не нужен.

Поскольку `.env` лежит в каталоге compose-проекта, «сырой» `docker compose`
из корня проекта — и шаг пайплайна `type: shell`, который сам вызывает
compose, — теперь работает в проекте с именем dwe, поверх любого верхнего
`name:` в compose-файле. Если compose-файл объявлял другое `name:`
(`dwe validate` предупреждал об этом как `config.compose_project_name`),
ресурсы, созданные под старым именем, для «сырого» compose перестают быть
видны: либо задайте `project_name` в `workspace/docker.yml` равным старому
значению (это переключит и сам `dwe`, поэтому один раз передеплойте), либо
замените в compose-файле на `name: ${COMPOSE_PROJECT_NAME}` — теперь оно
резолвится из `.env`.

**Сборки под Windows.** dwe поддерживает macOS и Linux; под Windows
запускайте его внутри WSL2, установив dwe в дистрибутив. Заглушки для
компиляции под Windows удалены, поэтому `GOOS=windows go build` теперь
падает, а не собирает бинарь, который не берёт проектные локи.

## Post-Completion

**Manual verification**
- On each of the twelve real workspaces after upgrading the binary: `dwe
  render env | tail -3` shows the new line; any Makefile or script that
  computed the compose name from `PROJECT` can be simplified to read
  `COMPOSE_PROJECT_NAME` (optional, not required for correctness).
- Confirm on one workspace that a manual `docker compose ps` from the project
  root now lists the same containers as `dwe status` — that is the user-facing
  benefit of the export.

**External updates**
- The upgrade guide, when written, takes the "Upgrade notes" section above.
- The installed skill copy outside this repo is refreshed from `skills/dwe/`
  after merge (it is loaded from the published branch, not from a feature
  branch).
