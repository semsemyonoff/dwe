# Render kind `workspace`: template packs at the workspace root

## Overview
- New render kind `workspace`: packs under `workspace/templates/workspace/<pack>/` render into
  the project root, enabled by a top-level `render.workspace: [<pack>…]` list.
- Solves running per-workspace tools generically: tools configured at the workspace root (ralphex `.ralphex/`,
  root agent docs, `.mcp.json`, …) get a pack without a dwe change per tool. ralphex is the
  first consumer; its pack lives in the workspace repo, dwe ships the mechanism, a guide and a
  tested starter pack under `examples/workspace-packs/` (not embedded in the binary).
- Reuses the shared manifest schema, `packroot` `.local` overrides and `packcommon.TemplateData`.
- Rationale, code facts, safety rules and the ralphex recipe:
  `docs/plans/notes/20261005-render-workspace-packs.md`.

## Context (from discovery)
- Packs: `internal/core/execution/templates/{ai,ide,git,config,manifest,packroot,packcommon}`.
- CLI: `internal/cli/render/{render.go,ai.go,common.go,secrets_test.go}`; validate:
  `internal/core/validate/templates/{ai.go,ide.go,overrides.go}`, `internal/cli/validate/validate.go`.
- Config root: `allowedRootKeys` (`internal/core/project/config/workspace.go:40`) mirrored by
  `tpl.KnownVarHeads` (`internal/shared/tpl/render_command.go:131`); unknown nested keys via
  `formalBlockStructs` (`internal/core/validate/config/formal_blocks.go:25`).
- Scaffold gitignore: `internal/core/workflow/scaffold/gitignore.go:45` (`/.ralphex/`).

## Development Approach
- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully before moving to the next; small, focused changes
- every task includes new/updated tests; all tests pass before the next task
- update this plan when scope changes; keep `ai`/`ide`/`git`/`config` behavior unchanged

## Testing Strategy
- unit tests per task, table-driven; temp projects built in Go
- CLI tests follow `internal/cli/render` style: temp project + `cmd.RunE` (`ai_test.go`), no goldens

## Progress Tracking
- mark completed items `[x]` immediately; new tasks get ➕, blockers ⚠️

## Solution Overview
- Kind `workspace`, dest root = project root. No service selection; packs come from an explicit list.
- Manifest schema unchanged. `from` ending in `.tmpl` renders through `text/template` with
  `missingkey=error`; any other `from` is copied byte-for-byte (ralphex prompts contain their own
  `{{PLAN_FILE}}`). Destination mode follows the source (`0755`/`0644`), applied with an explicit
  chmod so re-renders converge.
- `TemplateData` is `packcommon.TemplateData` with empty `Service`/`Resolved`/`ServiceCfg`, built
  from the sanitized config like `render ai`.
- Two phases: plan (validate + render every entry into memory + all destination filesystem
  checks), then write. Any failure in the plan phase writes nothing.

## Technical Details
- Config: `render: { workspace: [ralphex] }`; `RenderConfig{Workspace []string}` on
  `DweConfig.Render`. `deepMerge` replaces lists wholesale, so `workspace: []` in `local.yml`
  clears it. No name validation at load time.
- Pack name/duplicate validation lives in `workspace.ValidatePacks` (render + validator).
- Protected destinations, case-insensitive per path component, for render `to` and
  `symlinks[].link`: `workspace.yml`, `workspace/`, `services/`, `.dwe/`, `.git/`, `.env`.
- Cross-pack collisions: equal paths and file-vs-directory prefixes (`.ralphex` vs `.ralphex/config`).
- CLI: `dwe render workspace [pack…]`; no args → `render.workspace` (empty → info line, exit 0);
  explicit names work outside the list; listed pack not found → hard error. No JSON mode (same as
  `render ai`). Pack-name completion through `cmdctx.CompletionConfigPath`.
- Writes: dest symlink or directory → refuse; existing regular file → overwrite.
- Out of scope: removing files dropped from a manifest (documented as a limitation), hooks or
  commands inside render, automatic render in `dwe run`/deploy (explicit step only).

## What Goes Where
- Implementation Steps: dwe code, tests, example packs, docs, skill references, CHANGELOG.
- Post-Completion: installing the ralphex starter in ficbird/podlapka, upstream ralphex proposal.

## Validation Commands
- `make embedded-docs` (once, and after docs edits)
- `go test ./internal/core/execution/templates/... ./internal/cli/render/... ./internal/core/validate/templates/... ./internal/core/validate/config/... ./internal/core/project/config/... ./internal/shared/tpl/... ./internal/cli/validate/... ./internal/core/workflow/scaffold/... ./internal/core/docs/llmstxt/... ./internal/cli/docs/...`
- `golangci-lint run ./internal/core/execution/templates/... ./internal/cli/render/... ./internal/core/validate/... ./internal/core/project/config/... ./internal/shared/tpl/... ./internal/cli/validate/... ./internal/core/workflow/scaffold/... ./internal/core/docs/llmstxt/...`

## Implementation Steps

### Task 1: Extract shared file writer and symlink helper into `packcommon`

**Files:**
- Modify: `internal/core/execution/templates/packcommon/packcommon.go`
- Modify: `internal/core/execution/templates/ai/ai.go`
- Modify: `internal/core/execution/templates/ide/ide.go`
- Create/Modify: `internal/core/execution/templates/packcommon/*_test.go`

- [x] split the current `ai.RenderTemplateFile` into `packcommon.PrepareFile` (resolve via packroot,
      render or verbatim-read into a buffer, mode normalized: any exec bit → `0755`, else `0644`) and `packcommon.CheckDest` + `WriteFile`
      (containment, symlink and real-path checks, refuse symlink/directory dest, write, explicit
      `os.Chmod` when mode-from-source is on); error labels are parameters
- [x] move `EnsureRelativeSymlink` into `packcommon` with a label/hint parameter (ai keeps its
      `render.ai.enabled` hint, ide its own)
- [x] switch `ai` and `ide` `RenderTemplateFile`/`EnsureRelativeSymlink` to the helpers; `ai`/`ide`
      stay template + `0644`, no chmod
- [x] write tests: template, verbatim with `{{PLAN_FILE}}` byte-for-byte, `0755` source, overwrite
      `0644`→`0755` and back, `.local` override hit
- [x] write tests for errors: dest escapes root, symlinked dest, dest is a directory, symlinked parent
- [x] run tests — existing `ai`/`ide` tests pass unchanged; must pass before next task

Validation: `make build`, the plan's scoped tests, and scoped `golangci-lint` passed.
The scoped tests used a temporary stub for only `docker version --format {{.Server.Version}}`
because the local Docker daemon socket was unavailable; no repository test was skipped or changed.

### Task 2: `render` root key

**Files:**
- Modify: `internal/core/project/config/workspace.go`
- Modify: `internal/shared/tpl/render_command.go`
- Modify: `internal/core/validate/config/formal_blocks.go`
- Modify: tests in `internal/core/project/config/` and `internal/core/validate/config/`

- [x] add `render` to `allowedRootKeys` and to `tpl.KnownVarHeads`; add `Render RenderConfig` to `DweConfig`
- [x] register `"render": reflect.TypeFor[config.RenderConfig]()` in `formalBlockStructs`
- [x] write tests: decode, last-layer-wins across layers, `local.yml` `workspace: []` clears the list, absent key
- [x] write test: `render: {workspce: …}` yields the `config.unknown_field:render` warning
- [x] run tests (incl. `TestAllowedRootKeysSubsetOfKnownVarHeads`); must pass before next task

Validation: `make embedded-docs`, `make build`, the plan's scoped tests, and scoped
`golangci-lint` passed, including the root-key/template-head cross-checks.
The scoped tests used a temporary stub for only `docker version --format {{.Server.Version}}`
because the local Docker daemon socket was unavailable; no repository test was skipped or changed.

### Task 3: Workspace pack plan phase (resolve, validate, render in memory)

**Files:**
- Create: `internal/core/execution/templates/workspace/workspace.go`
- Create: `internal/core/execution/templates/workspace/workspace_test.go`

- [x] `ResolvePack(projectRoot, name)`: `ValidatePackName`, strict lookup under
      `workspace/templates/workspace/`, symlinked or non-directory pack → error
- [x] `Plan(projectRoot, names, data)`: reject duplicate names; load manifests;
      `manifest.ValidateShape` (dest root = project root); protected-path check on `to` and `link`
      (case-insensitive); cross-pack collisions incl. file-vs-directory prefixes; sources via
      `ValidateSourcesWith`
- [x] in the same phase prepare every render entry into memory via `packcommon.PrepareFile`
      (only `.tmpl` are executed — do not reuse `DryRunRender`, it parses verbatim files) and run
      `packcommon.CheckDest` for every destination and symlink parent; return the planned writes
- [x] write tests for success: two packs, `.local` override, verbatim `{{PLAN_FILE}}` + template mix
- [x] write tests for errors: missing pack, duplicate name, each protected path for `to` and
      `link`, case-variant (`Workspace.yml`, `.GIT/hooks`), equal and prefix collisions, missing
      source, `missingkey` error, dest is a directory
- [x] run tests — must pass before next task

Validation: `make embedded-docs`, `make build`, the plan's scoped tests, and scoped
`golangci-lint` passed. Tests verify that successful and failed plans leave files,
directories, permissions and symlinks unchanged, including a failure in the second pack.
The scoped tests used a temporary stub for only `docker version --format {{.Server.Version}}`
because the local Docker daemon socket was unavailable; no repository test was skipped or changed.

### Task 4: Workspace pack write phase

**Files:**
- Modify: `internal/core/execution/templates/workspace/workspace.go`
- Modify: `internal/core/execution/templates/workspace/workspace_test.go`

- [ ] `Render(projectRoot, names, data)`: `Plan`, then `packcommon.WriteFile` per entry (mode from
      source, chmod) and `packcommon.EnsureRelativeSymlink` per symlink
- [ ] return a result per pack (written files, override hits) for CLI output
- [ ] write tests: render into a temp project, overwrite existing file, `0755` kept, mode converges on re-render
- [ ] write tests: a failure in the second pack (bad template, protected path, dest directory) leaves
      the first pack's destinations untouched
- [ ] run tests — must pass before next task

### Task 5: `dwe render workspace` command

**Files:**
- Create: `internal/cli/render/workspace.go`
- Create: `internal/cli/render/workspace_test.go`
- Modify: `internal/cli/render/render.go`
- Modify: `internal/cli/render/secrets_test.go`
- Modify: `internal/core/execution/templates/packcommon/templatedata_sites_test.go`

- [ ] subcommand: args → explicit packs, no args → `cfg.Render.Workspace`; empty → info line, exit 0
- [ ] load config with `config.LoadConfigSanitizedOrWrap`, command index via `loadCommandIndex`,
      output via `render.Stdout()`; no JSON mode, same as `render ai`
- [ ] pack-name completion via `cmdctx.CompletionConfigPath` (AGENTS.md completion path safety)
- [ ] update `render` `Long`, `Example` and the `NewCmd` doc comment
- [ ] add `internal/cli/render/workspace.go` to `want` in `templatedata_sites_test.go`
- [ ] write tests (temp project + `RunE`): default list, explicit pack, empty list, listed pack
      missing, protected path; `TestNewWorkspaceCmd_rendersMarkerNotPlaintext` in `secrets_test.go`
- [ ] run tests — must pass before next task

### Task 6: `dwe validate templates workspace`

**Files:**
- Create: `internal/core/validate/templates/workspace.go`
- Create: `internal/core/validate/templates/workspace_test.go`
- Modify: `internal/core/validate/templates/ide.go` (validator list, `&AIValidator{}` at :213)
- Modify: `internal/cli/validate/validate.go`

- [ ] `WorkspaceValidator` (domain `templates`, id `workspace`): runs `workspace.Plan` on
      `render.workspace` with `sanitizedCfg(ctx)` and `commandIndex(ctx)`; errors as diagnostics;
      info when the list is empty
- [ ] register it; add the `validate templates workspace` subcommand; add `workspace` to help at
      `validate.go:299-300` and the templates `Long` (:351)
- [ ] add `internal/core/validate/templates/workspace.go` to `want` in `templatedata_sites_test.go`
- [ ] write tests: clean pack (incl. verbatim `{{PLAN_FILE}}`) → no diagnostics, missing pack,
      protected path, template execution error; the validator passes `commandIndex(ctx)` into
      `TemplateData` (own test, like `TestTemplateValidators_DryRunSeesCommandIndex`)
- [ ] run tests — must pass before next task

### Task 7: Scaffold and llms.txt

**Files:**
- Modify: `internal/core/workflow/scaffold/gitignore.go`
- Modify: `internal/core/workflow/scaffold/testdata/golden_default.txt`
- Modify: `internal/core/docs/llmstxt/generator.go`

- [ ] remove `/.ralphex/` from `dweGitignoreBlock`; update the golden (check
      `starter_artefacts_test.go` and `docs/guides/start-a-new-project.md` for other mentions)
- [ ] llms.txt "Template syntax by site" row (`generator.go:306`): add `workspace`, note only `.tmpl`
      files are templates; stay within `TestDocsLlmsTxtCommand_SizeBudget`
- [ ] run `go test ./internal/core/workflow/scaffold/... ./internal/core/docs/llmstxt/... ./internal/cli/docs/...`

### Task 8: `ws-git` for the ralphex example

Contract: notes § "Example pack" (`ws-git` contract); ficbird `repos.sh` semantics in
`docs/plans/notes/20261005-render-workspace-packs-ficbird.md`.

**Files:**
- Create: `examples/workspace-packs/ralphex/workspace/templates/workspace/ralphex/scripts/ws-git`
- Create: `internal/core/execution/templates/workspace/examples_wsgit_test.go`

- [ ] `ws-git` (POSIX sh, `#!/bin/sh`) per the notes contract: scope = root + `.ralphex/run/repos`
      (root always in scope); intercepts exactly `rev-parse HEAD`, `diff HEAD`,
      `ls-files -z --others --exclude-standard` (NUL via `xargs -0`, per notes), silent on success;
      near-miss argv passes through; no run state → intercepted calls root only, every `ws-*` errors;
      partial/invalid run state → non-zero exit; `ws-check`, `ws-status`, `ws-log`,
      `ws-diff [--stat]`, `ws-wip [--stat]` with branch/ancestor checks
- [ ] `chmod +x` the file on disk, then `git add` it; verify `git ls-files -s` shows `100755`
- [ ] add the go.mod walk-up helper (`findRepoRoot`, as in `ide/source_regression_test.go:146`) to
      the test package; Task 9 reuses it
- [ ] write `ws-git` behavior test (`t.Skip` without `git`; `gitInit`/`requireGit` pattern from
      `stack/gitworkspace_test.go:86-108`): copy the script to `<tmp>/.ralphex/scripts/ws-git` (0755)
      and exec it directly, not via `sh`; root with `/services/` and `/.ralphex/run/` in
      `.gitignore`, 2 scoped nested repos + 1 unscoped dirty repo, base tag and task branch in root
      and scoped repos; HEAD changes after a scoped commit, not after an unscoped one;
      `diff HEAD`/`ls-files -z` repo-prefixed, NUL-separated, stderr empty, untracked names with a
      space and non-ASCII survive, empty untracked set yields empty output; near-miss argv passes
      through; no run state → root only and `ws-check` fails; missing scoped repo, base missing in
      a repo or in the root → non-zero; `ws-diff` fails off the task branch;
      `hash-object -- <prefixed name>` and pass-through work from the root
- [ ] run tests — must pass before next task

### Task 9: ralphex pack, commands and minimal example

Contract: notes § "Example pack"; ficbird block source material in
`docs/plans/notes/20261005-render-workspace-packs-ficbird.md`.

**Files:**
- Create: `examples/workspace-packs/README.md`, `examples/workspace-packs/ralphex/README.md`
- Create: `examples/workspace-packs/ralphex/workspace/templates/workspace/ralphex/{manifest.yml,config.tmpl,blocks/task.md,blocks/review.md,blocks/codex_review.md,blocks/agent.md}`
  (`manifest.yml` per the notes table, including `scripts/ws-git` from Task 8)
- Create: `examples/workspace-packs/ralphex/workspace/scripts/{ralphex-prompts.sh,ralphex-scope.sh}`
- Create: `examples/workspace-packs/ralphex/workspace/commands/ralphex.yml`
- Create: `examples/workspace-packs/ralphex/workspace.yml.snippet`
- Create: `examples/workspace-packs/root-agents/workspace/templates/workspace/root-agents/{manifest.yml,AGENTS.md.tmpl,...}`
- Create: `internal/core/execution/templates/workspace/examples_test.go`
- Create: `internal/core/execution/templates/workspace/examples_scripts_test.go`

- [ ] layout mirrors a workspace; install is `cp -R examples/workspace-packs/<name>/workspace/. <ws>/workspace/`
      (trailing `/.` — BSD and GNU cp agree) plus the snippet when present; the ralphex README adds
      `/.ralphex/run/` to and removes an older scaffold's `/.ralphex/` from the root `.gitignore`,
      and says the base must also exist in the root; the root-agents README warns that it overwrites
      the scaffold's root `AGENTS.md` (move its content into the template first; a `CLAUDE.md` copy
      fallback goes stale, a symlink does not). READMEs say packs are starters owned by the
      workspace afterwards and to take them from the tag matching the installed dwe
- [ ] `config.tmpl` (guarded `vars.ralphex.default_branch`, default `main`), `blocks/*.md` generalized
      from the ficbird source per the notes (scope from `.ralphex/run/`, `ws-git ws-*`, explicit
      overrides of body git instructions, no anchor commit, no project names; agent block read-only
      and overrides the code-injected diff instruction above it; codex_review maps both
      `{{DIFF_INSTRUCTION}}` forms; no block starts with `#`)
- [ ] `ralphex-scope.sh` + `ralphex.scope` (`repos`, `base`, `branch`, all required): `check-ignore`
      hint, write `.ralphex/run/*`, run `ws-git ws-check`, restore on failure. `ralphex-prompts.sh` +
      `ralphex.prompts` (`check` bool, `default: false`, `env:`): 10 files, block + phase/file policy
      fragments after the leading header and a blank line (agents: after leading `#` lines and
      frontmatter), fragments starting with `#` rejected, never the dumped `config`, stamp; check mode
      regenerates into a temp dir and diffs (non-zero on difference), reports stamp drift, warns on
      unowned files, writes nothing
- [ ] `chmod +x` both scripts on disk, then `git add`; verify `git ls-files -s` shows `100755`
- [ ] `root-agents`: minimal pack with a `.tmpl` (root `AGENTS.md` listing `.Services`) and a verbatim file
- [ ] write `examples_test.go` (`findRepoRoot` from Task 8 to find `examples/`):
      per example build a temp workspace (minimal `workspace.yml` + snippet when present, precedent
      `validate/templates/command_index_test.go:41`), `config.LoadConfigSanitized`, `workspace.Plan`
      → no errors, `.tmpl` rendered (also without `vars.ralphex`), verbatim byte-identical, `ws-git`
      planned `0755`; `usercommands.LoadRegistryFromConfigPath` + the `validate/commands` validator → no errors/warnings
- [ ] write `examples_scripts_test.go` with a stub `ralphex` on PATH (`t.Skip` without `sh`) answering
      `--version` and `--dump-defaults=<dir>` (`config`, `prompts/`, `agents/` mimicking v1.7.0:
      prompts with a `#` header, one agent with leading `#` lines + frontmatter): 10 files written;
      applying ralphex's leading-comment strip rule to each prompt leaves the block's first line
      first; phase and file policy fragments in order; block after frontmatter; a fragment starting
      with `#` is rejected; `config` untouched; stamp written; check mode: clean → zero exit, edited
      block / hand-edited prompt / changed stub defaults → non-zero, stale unowned prompt → warning,
      nothing written; `ralphex-scope.sh` writes run state, rejects a missing repo or ref and
      restores the previous files, fails when `.ralphex/run/` is not ignored
- [ ] run tests — must pass before next task

### Task 10: Verify acceptance criteria
- [ ] `render.workspace` renders listed packs into the root; `.tmpl` templated, others verbatim,
      mode follows source; protected paths and collisions rejected before any write
- [ ] `ai`/`ide`/`git`/`config` behavior unchanged
- [ ] run full test suite: `make test`
- [ ] run linter: `make lint`

### Task 11: [Final] Update documentation
- [ ] create `docs/reference/render/workspace.md` (activation, `.tmpl` vs verbatim, mode, protected
      paths, collisions, two-phase write, template data, CLI, validate); add it to
      `docs/reference/render/index.md` (kind table, TOC), `docs/reference/index.md:11`,
      `docs/reference/templates.md:3`, `docs/reference/config/validate.md:119`
- [ ] `docs/reference/config/workspace.md`: `render.workspace` field, allowlist block and the literal
      error message example (:102-110)
- [ ] create `docs/guides/run-ralphex-in-a-workspace.md`: why (notes), install from the examples
      (link `examples/workspace-packs/ralphex/README.md`, a file — precedent
      `docs/guides/observability-otel.md:457`; fetch from the tag matching the installed dwe; do not
      inline the files), the `cp -R …/workspace/.` command and the `/.ralphex/run/` ignore line,
      first `dwe render workspace` + `dwe cmd ralphex.prompts`, commit `.ralphex/`; per plan:
      `dwe cmd ralphex.scope …` then `ralphex --base-ref <base> --branch <branch> <plan>`;
      maintenance (ralphex update → `dwe cmd ralphex.prompts --set check=true`), policy fragments,
      root in scope (base in root too), known limitations from the notes, stage 2 note; add to
      `docs/guides/index.md`; mention the examples in `docs/reference/render/workspace.md`
- [ ] `README.md:217` render list; mirror all new/changed pages and `docs/i18n/ru/README.md:219` in
      `docs/i18n/ru/`; after `make gen-docs-manifest` update the `> Translated from: … @ <hash>`
      headers (`TestRussianTranslationsAreFresh`)
- [ ] `skills/dwe/SKILL.md:153` (mutating render commands) and `skills/dwe/references/render-and-vars.md`
      (new bullet + "apply by source" lines :75,79) with a pointer to the guide
- [ ] `docs/internals/packages.md`: the kind under execution templates and § `internal/cli/render/`.
      Do not grow `AGENTS.md` (32 B under `TestAgentsMdBudget`)
- [ ] `CHANGELOG.md` `## [Unreleased]`: new kind, new root key, scaffold no longer ignores `.ralphex/`,
      example packs under `examples/workspace-packs/`
- [ ] run `make gen-docs-manifest` (commit `internal/core/docs/content_hashes_gen.go`), `make embedded-docs`, `make test`, `make lint`

*Note: ralphex moves this plan to docs/plans/completed/ when it finishes.*

## Post-Completion
*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Workspace repos (ficbird, podlapka):**
- `cp -R examples/workspace-packs/ralphex/workspace/. <ws>/workspace/`, apply the snippet, add
  `/.ralphex/run/` and remove `/.ralphex/` in the root `.gitignore`, run `dwe render workspace` and
  `dwe cmd ralphex.prompts`, commit `.ralphex/`
- ficbird: move its policy lines into fragments — pre-existing issues and never mark an unexecuted
  check `[x]` into `policy/task.md` / `policy/review.md`, `AGENTS.md` into `policy/documentation.md`; delete stale
  `custom_eval`/`custom_review`/`make_plan` overrides and `scripts/repos.sh`; plans switch from
  `.ralphex/{repos,base-ref,task-branch}` to `dwe cmd ralphex.scope`
- check the done criterion: ralphex runs a plan with commits in several service repos and
  reviews the combined diff without manual prompt edits

**Upstream ralphex:**
- propose `prompts/<name>.prepend.txt` / `.append.txt` (and for agents), optionally
  `diff_command`; once merged, switch the pack to stage 2 and delete `ralphex.prompts`
