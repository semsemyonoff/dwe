# Notes: render kind `workspace`

Side file for `docs/plans/20261005-render-workspace-packs.md`. Research and rationale live
here so the plan stays compact.

## Why a generic kind instead of a ralphex pack in dwe

- Pack *contents* are already user-authored (`workspace/templates/<kind>/<pack>/`). What is
  hardcoded is the *kind*: dest root, selection gates, collision policy, template syntax.
- All four kinds (`ai`, `ide`, `git`, `config`) are per-service and write into `svc.Dir` (or
  `.git/hooks`). `ai`/`ide` explicitly refuse the project root. Arbitrary per-service files are
  already covered by `render config` (`to` is relative to `svc.Dir`).
- The missing scope is the workspace root: `.ralphex/`, root `AGENTS.md`, `.mcp.json`,
  `mise.toml`, `.editorconfig`, ...
- A ralphex-specific pack in dwe would couple dwe to ralphex internals (prompt names,
  `--dump-defaults`, version drift). The pack lives in the workspace repo; dwe ships the
  mechanism, a guide and a starter under `examples/workspace-packs/ralphex/` (precedent:
  `examples/otel/`). Not embedded in the scaffold: ralphex is optional per project. A Go test keeps
  the examples valid against the kind; ralphex's own drift is caught only by `dwe cmd ralphex.prompts --set check=true`.

## Code facts

- `ai.RenderTemplateFile` (`internal/core/execution/templates/ai/ai.go:161`) and
  `ide.RenderTemplateFile` (`ide/ide.go:248`) are near-identical: they differ only in the kind
  string and error wording ("hub directory" vs "service dir"). Existing tests may assert those
  strings, so the shared helper takes labels as parameters.
- `packroot.Resolve(projectRoot, kind, packName, rel)` already handles `.local` overrides for
  any kind string.
- `manifest.ValidateShape(m, destRoot, label)` checks containment and duplicates; protected
  paths and cross-pack collisions are new and workspace-specific.
- `allowedRootKeys` (`internal/core/project/config/workspace.go:40`): strict root, a new
  `render` key is needed. Merge semantics should match `bridge.vars_writable` (list,
  last-layer-wins).
- `varsusage` scans only `config` templates raw; `ai`/`ide`/`git` Go templates are not
  tracked. Workspace packs follow `ai`, so no change in `varsusage/scan.go`.
- Scaffold `.gitignore` (`internal/core/workflow/scaffold/gitignore.go:45`) adds `/.ralphex/`.
  The block is written at init only (missing lines appended), so removing it affects only new
  projects.
- Render packs' command index stays English and visibility-blind (`AGENTS.md`, Critical
  Patterns): reuse the same `TemplateData` construction as `cli/render/ai.go`.

## Protected destinations

A render `to` or a `symlinks[].link` resolving to or under any of these is a hard error at
plan time: `workspace.yml`, `workspace/`, `services/`, `.dwe/`, `.git/`, `.env`. Compare per path
component with `strings.EqualFold`: APFS is case-insensitive by default, so `Workspace.yml` or
`.GIT/hooks` must not slip through.

## Safety rules from plan review

- `os.WriteFile` sets mode only on create: an explicit `os.Chmod` is needed for mode-from-source
  (precedent: `git/git.go:437-442`, `config/config.go:352-365`).
- `packcommon.DryRunRender` parses every render entry (`packcommon.go:269-283`) and would fail on
  verbatim `{{PLAN_FILE}}`: the workspace plan phase executes only `.tmpl` entries.
- "Validate all, then write" needs destination filesystem checks (dest symlink/directory,
  symlinked parents) in the plan phase too; `ai.RenderTemplateFile` does them only at write time.
- Cross-pack collisions include file-vs-directory prefixes (`.ralphex` vs `.ralphex/config`).
- Config is loaded sanitized (`LoadConfigSanitizedOrWrap`), as rendered output is committed.
- No pack-name validation in `LoadConfig`: it would import `execution/templates/manifest` into the
  project layer and make a typo fatal for every command. `bridge.vars_writable` precedent: check
  only as a diagnostic.

## ralphex recipe (guide content, not dwe code)

Findings from the analysis of ralphex v1.7.0 (2026-09-30), rechecked against
ralphex main `a736d5e` and the live ficbird setup (2026-10-05):

- All git calls go through `vcs_command` (`pkg/git/external.go`). The wrapper intercepts
  `rev-parse HEAD`, `diff HEAD` and `ls-files --others --exclude-standard` across the run's scope;
  the rest pass through to git. This fixes "no changes detected" in the review loop and
  `review_patience` cutoffs; the `--allow-empty` anchor commit is not needed.
- The `rev-parse HEAD` value is only compared for equality and never fed into another git call:
  `review.go:70,96` (no-changes exit), `git_state.go:58-65` (`review_patience` stalemate),
  `external.go:422-433` (`diffStats`: compared with `rev-parse <baseRef>`, which passes through to
  root, so they never match and `diff --numstat <base>...HEAD` runs on root only). A synthetic hash
  is safe. Known limitation: end-of-run diffstats count the root repo only; not intercepted.
- Exact intercepted argv: `rev-parse HEAD` (also via `hasCommits`, `LC_ALL=C`), `diff HEAD`,
  `ls-files -z --others --exclude-standard` (NUL-separated). For each untracked name ralphex then
  runs `hash-object -- <name>` from the root: repo-prefixed paths resolve there, so it passes
  through. Bootstrap `rev-parse --show-toplevel` passes through (root).
- `run()` uses `CombinedOutput()` and the root as cwd: anything `ws-git` writes to stderr on an
  intercepted call ends up in the hash, the fingerprint or the `ls-files` names. Intercepted calls
  are silent on success; failures exit non-zero.
- `--worktree` is incompatible: set `use_worktree = false` in config.
- No include/append for prompts: files are replaced whole (local → global → embedded).
- The agents' diff instruction is not in the agent files: `reviewContextInstruction()`
  (`pkg/processor/prompts.go:117-133`) prepends "First run `git diff <base>...HEAD` and
  `git diff --stat <base>...HEAD`…" to every agent body in code. An override cannot remove it, so
  the agent block (in all 5 agents) overrides *the instruction above it*, both forms.
- `{{DIFF_INSTRUCTION}}` in `codex_review` is `git diff <base>...HEAD` on the first iteration and
  plain `git diff` after (`pkg/processor/prompts.go:66-74`); the codex_review block maps them to
  `ws-git ws-diff` and `ws-git ws-wip`.
- `{{DEFAULT_BRANCH}}` is ralphex `--base-ref` (`cmd/ralphex/main.go:1053`). `.ralphex/` is found
  from cwd; a relative `vcs_command` resolves against the root.
- Blocks go at the **start of the body** of each file on top of `ralphex --dump-defaults`; signal
  rules at the end of the defaults stay last. Not at byte 0: every default prompt opens with a `#`
  comment header (variable docs), and ralphex strips only a *leading* block of 2+ `#` lines
  (`pkg/config/prompts.go:134-176`). Prepending would push that header into the body, so it would
  reach the LLM (with its `{{VAR}}` mentions substituted). Insert after the leading comment block
  followed by one blank line. The strip counts *any* leading line starting with `#` and stops at the
  first other line, so a block or policy fragment must not start with `#` (a `## Heading` right
  after the header would be stripped with it): the first line is plain text (ficbird:
  `MULTI-REPOSITORY WORKSPACE — …`); the generator rejects a fragment whose first line starts with
  `#`. For agents (no header in v1.7.0 defaults, raw load in `agents.go:167-181`): `buildAgent`
  also finds frontmatter after leading `#` lines (`agents.go:203-215`), so skip leading `#` lines,
  then a `---` frontmatter block if present, then insert.
- Overrides (10): `task`, `review_first`, `review_second`, `codex`, `codex_review`, and 5 agents.
  `finalize` is not overridden: `config.tmpl` sets `finalize_enabled = false` (finalize rebases the
  root only).
- ralphex's own `.ralphex/.gitignore` excludes `progress/` and `worktrees/`; `.ralphex/` is meant
  to be committed (upstream issue #431). ralphex rewrites that file to its exact content on every
  run (`pkg/git/service.go:620-643`) and it ignores itself, so nothing can be added there.

## Decisions from the ficbird review (2026-10-05)

Source: the live ficbird `.ralphex/` (prompts/agents 2026-10-03, ralphex v1.7.0-24c19b1). Diffs
against that build's defaults and its `repos.sh`:
`docs/plans/notes/20261005-render-workspace-packs-ficbird.md`.

- **Scope is per-run state, not workspace config.** A plan names its repos, base and task branch
  (ficbird: 5 repos, base = a local tag cut in each repo, branch per plan). An unrelated checkout
  must not enter review; `ficbird-configs` is explicitly out of scope. So no repo list rendered
  from services, no `base_refs`/`extra_repos` vars.
- **Prompts: insertions only.** Phase blocks after the leading header, phrased to override the
  body's own instructions explicitly. ficbird additionally rewrote body lines; not done here, so it
  stays compatible with an upstream prepend feature and survives default drift.
- **Workspace policy fragments.** Project rules (ficbird: pre-existing issues policy, never mark an
  unexecuted check `[x]`, `CLAUDE.md`→`AGENTS.md`) are optional workspace-owned fragments inserted
  after the phase block. The starter ships none.
- The anchor `--allow-empty` commit in ficbird blocks is obsolete with `ws-git` as `vcs_command`.
- `repos.sh` is bash (`mapfile`, associative arrays) and does not run on macOS stock bash 3.2:
  `ws-git` is POSIX sh.
- ficbird holds stale `custom_eval`/`custom_review`/`make_plan` copies (old dumps without an
  override header) silently shadowing newer defaults: check mode must flag prompt/agent files the
  generator does not own.

## Example pack

Layout (`examples/workspace-packs/ralphex/workspace/templates/workspace/ralphex/`, copied into a
workspace as `workspace/templates/workspace/ralphex/`):

| from | to | mode |
|---|---|---|
| `config.tmpl` | `.ralphex/config` | template: `vcs_command=.ralphex/scripts/ws-git`, `use_worktree = false`, `finalize_enabled = false`, `default_branch` from `vars.ralphex.default_branch` (guarded, default `main`) |
| `scripts/ws-git` | `.ralphex/scripts/ws-git` | verbatim, executable |
| `blocks/{task,review,codex_review,agent}.md` | `.ralphex/blocks/…` | verbatim |

A workspace adds policy fragments by putting files into `policy/` of its copy of the pack with
manifest entries to `.ralphex/policy/`: `policy/<phase>.md` (the four phase names) applies to every
file of the phase, `policy/<file>.md` (e.g. `documentation.md`, `review_first.md`) to one file;
order: block, phase fragment, file fragment (once when the names coincide, e.g. `task`).

**Run state** (per plan, never committed): `.ralphex/run/{repos,base-ref,task-branch}`. Install
adds `/.ralphex/run/` to the workspace root `.gitignore` (the pack cannot write it; ralphex owns
`.ralphex/.gitignore`). Written by project command `ralphex.scope` (`--set repos="<paths>"
--set base=<ref> --set branch=<name>`, all required) via `workspace/scripts/ralphex-scope.sh`: it
fails with a hint unless `git check-ignore -q .ralphex/run/repos` (otherwise ralphex sees
uncommitted files), writes the files, runs `.ralphex/scripts/ws-git ws-check` and restores the
previous state on failure — one validator. Or by hand per the plan. `base` is also passed to
`ralphex --base-ref`: ws-git cannot see ralphex flags.

The root is always in scope: `base` must exist in the root as well (cut the base tag in the root
too), and for `ws-log`/`ws-diff`/`ws-wip` the root must be on `task-branch` with `base` as an
ancestor. ralphex creates `--branch` in the root only when the root is on `default_branch`
(`pkg/git/service.go:202-210`); the plan preflight cuts the branch in each scoped repo.

**`ws-git` contract** (POSIX sh, root resolved from the script's own location):

- Scope = the root plus `.ralphex/run/repos` (root-relative paths, one per line, `.` allowed and
  implied; deduped; no `..`, absolute paths or symlinks). Each must be an ordinary checkout
  (`<path>/.git` directory) and resolve `base-ref`.
- Intercepted for ralphex: exactly `rev-parse HEAD` (combined via `git hash-object --stdin` over
  `<path> <HEAD>` lines), `diff HEAD` (concatenated, `--src-prefix/--dst-prefix` per repo) and
  `ls-files -z --others --exclude-standard` (NUL output, repo-prefixed). Everything else passes
  through to root git. Silent on success.
- No run state at all → intercepted calls cover the root only, so bootstrap and `make_plan` work.
  `ws-check` (and every `ws-*`) without run state is an error; the task/review/codex blocks require
  `ws-check` first, so every task or review run needs `ralphex.scope` (`repos=.` for a root-only
  plan). Partial or invalid run state → every call exits non-zero with a message.
- NUL handling: POSIX sh cannot hold NUL in variables and has no `read -d`/`sed -z`/`mapfile`.
  Prefix `ls-files -z` names through `xargs -0 sh -c 'for f; do printf "%s%s\0" "$p" "$f"; done' sh`
  (a no-op on empty input with both GNU and BSD xargs); names with spaces and non-ASCII must survive.
- Near-miss argv (`rev-parse HEAD~1`, `diff HEAD --stat`, `ls-files --others` without `-z`) pass
  through unchanged.
- Agent subcommands, semantics ported from ficbird `repos.sh`: `ws-check` (resolve scope/base, show
  branches), `ws-status`, `ws-log` (`base..HEAD`), `ws-diff [--stat]` (`base...HEAD`),
  `ws-wip [--stat]` (tracked changes vs HEAD + untracked names). `ws-log`/`ws-diff`/`ws-wip`
  require every repo on `task-branch` with `base-ref` as an ancestor; failures are errors, never
  empty output.

**Prompts, stage 1**: `workspace/scripts/ralphex-prompts.sh`, run by project command
`ralphex.prompts` (`type: shell`, `cmd: sh workspace/scripts/ralphex-prompts.sh`). dwe commands
take no ad-hoc flags, so check mode is a `check` bool param (`default: false`, so no interactive
confirm prompt) exported via `env:` (value `"true"`/`"false"`), called as
`dwe cmd ralphex.prompts --set check=true` (see `docs/reference/config/commands/directives.md`,
Params / Pass-through arguments). The script:

- runs `ralphex --dump-defaults=<tmp>`. The v1.7.0 dump holds `config`,
  `agents/{documentation,implementation,quality,simplification,testing}.txt` and 9 prompts;
- writes the 10 overridden files to `.ralphex/prompts/` and `.ralphex/agents/`: the dumped default
  with the block and policy fragments (order above) inserted at the start of the body (rule above). Phase map: `task`→task; `review_first`, `review_second`, `codex`→review;
  `codex_review`→codex_review; 5 agents→agent. Never copies the dumped `config`;
- records ralphex version + sha of the dumped defaults in `.ralphex/defaults.stamp` (not inside
  prompts — that would reach the LLM context);
- check mode writes nothing: it regenerates the 10 files into a temp dir and diffs them against
  `.ralphex/` (covers default drift, edited blocks/policy and hand edits; non-zero exit on a
  difference), reports stamp drift, and warns (does not fail) about files in `.ralphex/prompts/` /
  `.ralphex/agents/` the generator does not own: stale shadows, or legitimate custom agents, which
  then lack the agent block.

**Block content**: generalize the ficbird blocks from the source file — scope from
`.ralphex/run/`, `ws-git ws-*` instead of `repos.sh`, explicit overrides of body instructions
("ignore the `git diff/log {{DEFAULT_BRANCH}}` commands below; use `ws-git ws-diff`/`ws-log`";
"commit per repository with `git -C <repo>`, explicit task-owned paths; never stage `services/`
from the workspace, never `--no-verify`, never push"), no anchor commit, no project names. The
agent block is read-only review and overrides the code-injected instruction *above* it
(`git diff <base>...HEAD` and `git diff --stat <base>...HEAD` → `ws-git ws-diff [--stat]`); the
codex_review block maps both `{{DIFF_INSTRUCTION}}` forms. No block starts with `#`.

Known limitations (for the guide): end-of-run diffstats are root-only; `custom_review`/
`custom_eval` are not overridden, so `external_review_tool = custom` reviews the root only;
`--worktree` is unsupported.

**Stage 2** (after an upstream `prompts/<name>.prepend.txt` feature): blocks and policy become
verbatim prepend entries in the manifest; `ralphex.prompts` and the stamp go away.
