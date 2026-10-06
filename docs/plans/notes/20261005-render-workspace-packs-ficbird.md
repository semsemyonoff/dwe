# ficbird multi-repo prompt overrides (source material)

Unified diffs of the ficbird workspace's `.ralphex/` overrides (prompts/agents dated 2026-10-03)
against `ralphex --dump-defaults` of the same build (v1.7.0-24c19b1). Source material for the
example pack's phase blocks in `docs/plans/completed/20261005-render-workspace-packs.md` Tasks 8–9; not to be
copied verbatim: drop the anchor `--allow-empty` commit (replaced by `ws-git` as `vcs_command`),
`repos.sh` → `ws-git ws-*`, project-specific names, and ficbird policy lines (they belong in
workspace policy fragments). `finalize` is omitted: its override is fully commented out, so
ralphex falls back to the embedded default, and `finalize_enabled = false`.

## prompts/task.txt

```diff
@@ -1,3 +1,10 @@
+# LOCAL OVERRIDE (ficbird workspace) — the embedded ralphex default plus a
+# multi-repository layer. This workspace is one git repository and every
+# services/<name>/src is another (services/ is gitignored here), so the stock
+# prompts' `git diff` / `git status` / `git commit` see only compose, dwe config
+# and the plan. See .ralphex/scripts/repos.sh. Rebuild from
+# `ralphex --dump-defaults` when ralphex updates its defaults.
+#
 # task execution prompt
 # this prompt is used for each task iteration in phase 1
 #
@@ -7,11 +14,18 @@
 #   {{GOAL}} - human-readable goal description
 #   {{DEFAULT_BRANCH}} - default branch name (main, master, trunk, etc.)
 
+
+MULTI-REPOSITORY WORKSPACE — READ BEFORE ANY GIT COMMAND:
+The only repositories in this run are listed in .ralphex/repos; .ralphex/base-ref names their required release comparison base and .ralphex/task-branch names the task branch. Do not scan, edit or commit any other checkout, including ficbird-configs.
+- Before branch preparation, use `bash .ralphex/scripts/repos.sh check` and `bash .ralphex/scripts/repos.sh status`. After the plan's preflight prepares every task branch, use `bash .ralphex/scripts/repos.sh log`, `diff [--stat]` and `wip [--stat]` for task changes. Missing refs, wrong branches and git errors are blockers; do not substitute main or ignore a failing helper. A plain workspace diff cannot cover the product repositories.
+- Stage explicit task-owned paths in the repository that owns them: `git -C services/<name>/src add <files> && git -C services/<name>/src commit -m "<message>"`. Never stage services/ from the workspace, arbitrary leftovers or someone else's changes. Never use --no-verify.
+- Never push, in any repository.
+
 Read the plan file at {{PLAN_FILE}}. Find the FIRST Task section (### Task N: or ### Iteration N:) that has uncompleted checkboxes ([ ]).
 
-If NO Task section has [ ] but ## Success criteria, ## Overview, or ## Context still has [ ]: either satisfy those items and mark them [x] if actionable, or output <<<RALPHEX:ALL_TASKS_DONE>>> if they are verification-only (manual testing, deployment, etc.) — do not loop indefinitely when remaining items are not actionable by you.
+When no task checkboxes remain, verify the plan's acceptance criteria before reporting ALL_TASKS_DONE. An unchecked or unproven required criterion is a blocker, even if it is outside a Task heading.
 
-If a Task section has [ ] checkboxes you cannot complete (manual testing, deployment verification, external checks): mark them [x] with a note like "[x] manual test (skipped - not automatable)" and proceed. Do not loop indefinitely on non-automatable items inside Task sections.
+Never mark an unexecuted, skipped or failed required check [x]. Exercise the actual bot/browser/MCP surfaces required by the plan. If a required capability, permission or credential is unavailable, retain incomplete checkboxes, report the exact evidence and output <<<RALPHEX:TASK_FAILED>>>. The plan's recorded bounded local permissions do not authorise production, installs, general restarts or unrelated repairs.
 
 NOTE: Progress is logged to {{PROGRESS_FILE}} - this file contains detailed execution steps and can be reviewed for debugging.
 
@@ -28,7 +42,7 @@
 This helps the user understand what's happening in the current iteration.
 
 STEP 1 - IMPLEMENT:
-- Read the plan's Overview and Context sections to understand the work
+- Read the plan's Overview, Context and Development Approach sections to understand the work
 - Implement ALL items in the current Task section (all [ ] checkboxes under it)
 - Write tests for the implementation
 
@@ -38,7 +52,7 @@
 
 STEP 3 - COMPLETE (after validation passes):
 - Update progress: edit {{PLAN_FILE}} and change [ ] to [x] for each checkbox you implemented in the current Task section. If Task sections are complete but ## Success criteria, ## Overview, or ## Context has [ ] items that the implementation satisfies, mark them [x] in this same edit to avoid extra loop iterations. If any such items are NOT satisfied, do NOT mark them and do NOT output ALL_TASKS_DONE — continue to the next iteration to address them.
-- Commit all changes (code + updated plan) with message: feat: <brief task description>
+- Commit all changes with message: feat: <brief task description> — one commit in EACH repository you changed (see MULTI-REPOSITORY WORKSPACE above; the plan file is in the workspace repository). Then run `bash .ralphex/scripts/repos.sh status` and make sure nothing you changed is left uncommitted in any repository
 - Check if any [ ] checkboxes remain in Task sections (### Task N: or ### Iteration N:)
 - If NO more [ ] checkboxes in the entire plan, output exactly: <<<RALPHEX:ALL_TASKS_DONE>>>
 - If more Task sections have [ ] checkboxes, STOP HERE - do not continue
```

## prompts/review_first.txt

```diff
@@ -1,3 +1,10 @@
+# LOCAL OVERRIDE (ficbird workspace) — the embedded ralphex default plus a
+# multi-repository layer. This workspace is one git repository and every
+# services/<name>/src is another (services/ is gitignored here), so the stock
+# prompts' `git diff` / `git status` / `git commit` see only compose, dwe config
+# and the plan. See .ralphex/scripts/repos.sh. Rebuild from
+# `ralphex --dump-defaults` when ralphex updates its defaults.
+#
 # first review prompt
 # this prompt is used for the first (comprehensive) review pass in phase 2
 # launches 5 parallel reviewer agents for thorough code review
@@ -12,6 +19,14 @@
 #
 # agents are defined in ~/.config/ralphex/agents/ (user) or pkg/config/defaults/agents/ (builtin)
 
+
+MULTI-REPOSITORY WORKSPACE — READ BEFORE ANY GIT COMMAND:
+The only repositories in this run are listed in .ralphex/repos; .ralphex/base-ref names their required comparison base and .ralphex/task-branch names their task branch. Other checkouts, including ficbird-configs, and unrelated pre-existing work are out of scope.
+- Use `bash .ralphex/scripts/repos.sh check`, `log`, `diff [--stat]`, `wip [--stat]` and `status` from the workspace root. The helper requires the task branch for change reads and fails on a missing release ref or git error. Do not bypass it with main-based diffs; a plain workspace diff does not cover product code.
+- Commit only verified task-owned fixes, with explicit paths in their owning repository (`git -C services/<name>/src add <files> && git -C services/<name>/src commit -m "<message>"`). Never stage services/ from the workspace, commit arbitrary leftovers or use --no-verify.
+- Never push, in any repository.
+- ralphex watches ONLY the workspace repository's HEAD to decide whether a review iteration changed anything. So whenever you commit fixes in any other repository, ALSO make one commit in the workspace that names them, e.g. `git commit --allow-empty -m "fix: address code review findings" -m "services/app/src: <sha>" -m "services/admin/src: <sha>"`. Without it ralphex reports "no changes detected" and skips the pass that verifies your fixes.
+
 Code review of: {{GOAL}}
 
 Progress log: {{PROGRESS_FILE}} (contains task execution and previous review iterations)
@@ -19,8 +34,10 @@
 ## Step 1: Get Branch Context
 
 Run both commands to understand what was done:
-- `git log {{DEFAULT_BRANCH}}..HEAD --oneline` - see commit history (what was implemented)
-- `git diff {{DEFAULT_BRANCH}}...HEAD` - see actual code changes
+- `bash .ralphex/scripts/repos.sh log` - task commits in the explicitly scoped repositories
+- `bash .ralphex/scripts/repos.sh diff --stat` and `bash .ralphex/scripts/repos.sh diff` - committed task changes
+- `bash .ralphex/scripts/repos.sh status` - inspect scoped dirt; commit only this run's known task-owned fixes after verification, never arbitrary leftovers
+The agents' own instruction to run `git diff {{DEFAULT_BRANCH}}...HEAD` covers the workspace repository only; use `bash .ralphex/scripts/repos.sh diff` instead — it covers the workspace too, and the agent files say the same.
 
 ## Step 2: Launch ALL 5 Review Agents IN PARALLEL
 
@@ -59,13 +76,12 @@
 - CONFIRMED: Real issue, fix it
 - FALSE POSITIVE: Doesn't exist or already mitigated - discard
 
-IMPORTANT: Pre-existing issues (linter errors, failed tests) should also be fixed.
-Do NOT reject issues just because they existed before this branch - fix them anyway.
+IMPORTANT: Fix pre-existing issues only when they intersect this task's changed behaviour and approved scope. Unrelated linter/test failures are reported as blockers, not permission to expand the change. Do not mark required verification skipped or weaken gates.
 
 ### 3.3 Fix All Confirmed Issues
 1. Fix all CONFIRMED issues (all types: bugs, tests, smells, docs, etc.)
 2. Run tests and linter to verify fixes - ALL tests must pass, ALL linter issues resolved
-3. Commit fixes: `git commit -m "fix: address code review findings"`
+3. Commit fixes: `fix: address code review findings`, one commit in EACH repository you changed (`git -C <repo> ...`), plus the workspace anchor commit described in MULTI-REPOSITORY WORKSPACE above
 
 ## Step 4: Signal Completion
 
```

## prompts/review_second.txt

```diff
@@ -1,3 +1,10 @@
+# LOCAL OVERRIDE (ficbird workspace) — the embedded ralphex default plus a
+# multi-repository layer. This workspace is one git repository and every
+# services/<name>/src is another (services/ is gitignored here), so the stock
+# prompts' `git diff` / `git status` / `git commit` see only compose, dwe config
+# and the plan. See .ralphex/scripts/repos.sh. Rebuild from
+# `ralphex --dump-defaults` when ralphex updates its defaults.
+#
 # second review prompt
 # this prompt is used for the final review pass in phase 4
 # focuses on critical/major issues only, uses 2 agents
@@ -12,6 +19,14 @@
 #
 # agents are defined in ~/.config/ralphex/agents/ (user) or pkg/config/defaults/agents/ (builtin)
 
+
+MULTI-REPOSITORY WORKSPACE — READ BEFORE ANY GIT COMMAND:
+The only repositories in this run are listed in .ralphex/repos; .ralphex/base-ref names their required comparison base and .ralphex/task-branch names their task branch. Other checkouts, including ficbird-configs, and unrelated pre-existing work are out of scope.
+- Use `bash .ralphex/scripts/repos.sh check`, `log`, `diff [--stat]`, `wip [--stat]` and `status` from the workspace root. The helper requires the task branch for change reads and fails on a missing release ref or git error. Do not bypass it with main-based diffs; a plain workspace diff does not cover product code.
+- Commit only verified task-owned fixes, with explicit paths in their owning repository (`git -C services/<name>/src add <files> && git -C services/<name>/src commit -m "<message>"`). Never stage services/ from the workspace, commit arbitrary leftovers or use --no-verify.
+- Never push, in any repository.
+- ralphex watches ONLY the workspace repository's HEAD to decide whether a review iteration changed anything. So whenever you commit fixes in any other repository, ALSO make one commit in the workspace that names them, e.g. `git commit --allow-empty -m "fix: address code review findings" -m "services/app/src: <sha>" -m "services/admin/src: <sha>"`. Without it ralphex reports "no changes detected" and skips the pass that verifies your fixes.
+
 Second code review pass of: {{GOAL}}
 
 Progress log: {{PROGRESS_FILE}} (contains task execution and previous review iterations)
@@ -19,8 +34,10 @@
 ## Step 1: Get Branch Context
 
 Run both commands to understand what was done:
-- `git log {{DEFAULT_BRANCH}}..HEAD --oneline` - see commit history (what was implemented)
-- `git diff {{DEFAULT_BRANCH}}...HEAD` - see actual code changes
+- `bash .ralphex/scripts/repos.sh log` - task commits in the explicitly scoped repositories
+- `bash .ralphex/scripts/repos.sh diff --stat` and `bash .ralphex/scripts/repos.sh diff` - committed task changes
+- `bash .ralphex/scripts/repos.sh status` - inspect scoped dirt; commit only this run's known task-owned fixes after verification, never arbitrary leftovers
+The agents' own instruction to run `git diff {{DEFAULT_BRANCH}}...HEAD` covers the workspace repository only; use `bash .ralphex/scripts/repos.sh diff` instead — it covers the workspace too, and the agent files say the same.
 
 ## Step 2: Launch Review Agents IN PARALLEL
 
@@ -48,8 +65,7 @@
 
 ### 3.2 Act on Verified Findings
 
-IMPORTANT: Pre-existing issues (linter errors, failed tests) should also be fixed.
-Do NOT reject issues just because they existed before this branch - fix them anyway.
+IMPORTANT: Fix pre-existing issues only when they intersect this task's changed behaviour and approved scope. Unrelated linter/test failures are reported as blockers, not permission to expand the change. Do not mark required verification skipped or weaken gates.
 
 SIGNAL LOGIC - READ CAREFULLY:
 
@@ -66,7 +82,7 @@
 Path B - Issues found AND fixed:
 1. Fix verified critical/major issues only
 2. Run tests and linter - ALL tests must pass, ALL linter issues resolved
-3. Commit fixes: `git commit -m "fix: address code review findings"`
+3. Commit fixes: `fix: address code review findings`, one commit in EACH repository you changed (`git -C <repo> ...`), plus the workspace anchor commit described in MULTI-REPOSITORY WORKSPACE above
 4. End your output with a plain-text summary of what you fixed. Emit NO marker at all - not REVIEW_DONE, not TASK_FAILED.
    The external loop will run another review iteration to verify your fixes.
    Your fixes might have introduced new issues - another iteration must check.
```

## prompts/codex.txt

```diff
@@ -1,3 +1,10 @@
+# LOCAL OVERRIDE (ficbird workspace) — the embedded ralphex default plus a
+# multi-repository layer. This workspace is one git repository and every
+# services/<name>/src is another (services/ is gitignored here), so the stock
+# prompts' `git diff` / `git status` / `git commit` see only compose, dwe config
+# and the plan. See .ralphex/scripts/repos.sh. Rebuild from
+# `ralphex --dump-defaults` when ralphex updates its defaults.
+#
 # codex evaluation prompt
 # this prompt is used when claude evaluates codex review output
 # codex runs in phase 3, between first and second claude reviews
@@ -8,6 +15,14 @@
 #   {{DEFAULT_BRANCH}} - default branch name (main, master, trunk, etc.)
 #   {{CODEX_OUTPUT}} - output from codex code review
 
+
+MULTI-REPOSITORY WORKSPACE — READ BEFORE ANY GIT COMMAND:
+The only repositories in this run are listed in .ralphex/repos; .ralphex/base-ref names their required comparison base and .ralphex/task-branch names their task branch. Other checkouts, including ficbird-configs, and unrelated pre-existing work are out of scope.
+- Use `bash .ralphex/scripts/repos.sh check`, `log`, `diff [--stat]`, `wip [--stat]` and `status` from the workspace root. A failed helper is a blocker, not an empty diff; never substitute main or another branch.
+- Commit only verified task-owned fixes with explicit paths in the owning repository (`git -C services/<name>/src add <files> && git -C services/<name>/src commit -m "<message>"`). Never stage services/ from the workspace, commit arbitrary leftovers or use --no-verify.
+- Never push, in any repository.
+- ralphex watches ONLY the workspace repository's HEAD to decide whether a review iteration changed anything. So whenever you commit fixes in any other repository, ALSO make one commit in the workspace that names them, e.g. `git commit --allow-empty -m "fix: address code review findings" -m "services/app/src: <sha>" -m "services/admin/src: <sha>"`. Without it ralphex reports "no changes detected" and skips the pass that verifies your fixes.
+
 External code review evaluation.
 
 Codex reviewed the code and found:
@@ -30,8 +45,7 @@
 - **Valid issues**: Fix them (edit files, run tests/linter to verify)
 - **Invalid/irrelevant issues**: Explain why they don't apply (intentional design, already mitigated, misunderstood context) - your explanation will be passed to Codex for re-evaluation
 
-IMPORTANT: Pre-existing issues (linter errors, failed tests) should also be fixed.
-Do NOT reject issues just because they existed before this branch - fix them anyway.
+IMPORTANT: Fix pre-existing issues only when they intersect this task's changed behaviour and approved scope. Report unrelated linter/test failures as blockers rather than expanding the change. Never mark unexecuted required proof complete or weaken gates.
 
 ## After Evaluation
 
@@ -48,8 +62,8 @@
 - STOP here — the loop will re-run the external tool with your explanations for context
 
 **If Codex reports NO actionable issues** (empty output, "no issues found", "NO ISSUES FOUND"):
-- Run `git diff` to review ALL uncommitted changes (accumulated fixes from multiple iterations)
-- Commit all fixes with message: "fix: address codex review findings"
+- Run `bash .ralphex/scripts/repos.sh wip` to review ALL uncommitted changes in every repository (accumulated fixes from multiple iterations)
+- Commit all fixes with message: "fix: address codex review findings" — one commit in EACH repository that has them, plus the workspace anchor commit described above
 - Output exactly: <<<RALPHEX:CODEX_REVIEW_DONE>>>
 
 CRITICAL: The CODEX_REVIEW_DONE signal means "codex found nothing to fix". Only output it when codex itself reported no issues. If you fixed anything, do NOT output the signal.
```

## prompts/codex_review.txt

```diff
@@ -1,3 +1,10 @@
+# LOCAL OVERRIDE (ficbird workspace) — the embedded ralphex default plus a
+# multi-repository layer. This workspace is one git repository and every
+# services/<name>/src is another (services/ is gitignored here), so the stock
+# prompts' `git diff` / `git status` / `git commit` see only compose, dwe config
+# and the plan. See .ralphex/scripts/repos.sh. Rebuild from
+# `ralphex --dump-defaults` when ralphex updates its defaults.
+#
 # codex review prompt
 # this prompt is sent to the codex external review tool (or compatible wrapper)
 # codex reviews code changes and reports findings for claude to evaluate
@@ -10,12 +17,15 @@
 #   {{DEFAULT_BRANCH}} - default branch name (main, master, trunk, etc.)
 #   {{GOAL}} - human-readable goal description
 
+
 Review the code changes for: {{GOAL}}
 
 ## Get the Diff
 
 Run: {{DIFF_INSTRUCTION}}
 
+That command covers only the workspace git repository. .ralphex/repos defines this run's repository scope, .ralphex/base-ref its required release base and .ralphex/task-branch its task branch. Also run `bash .ralphex/scripts/repos.sh diff --stat` and `diff` for committed changes, or `wip --stat` and `wip` for uncommitted changes. Use their workspace-relative file paths. A helper error is a blocker: do not fall back to main. Review only task-owned changes in the declared scope; do not inspect or repair unrelated checkouts or leftovers.
+
 ## Context
 
 Plan: {{PLAN_FILE}}
```

## agents/quality.txt

```diff
@@ -1,3 +1,14 @@
+# LOCAL OVERRIDE (ficbird workspace) — the embedded ralphex default plus a
+# multi-repository layer. This workspace is one git repository and every
+# services/<name>/src is another (services/ is gitignored here), so the stock
+# prompts' `git diff` / `git status` / `git commit` see only compose, dwe config
+# and the plan. See .ralphex/scripts/repos.sh. Rebuild from
+# `ralphex --dump-defaults` when ralphex updates its defaults.
+#
+
+
+MULTI-REPOSITORY WORKSPACE: review only repositories listed in .ralphex/repos, against .ralphex/base-ref and on .ralphex/task-branch. Use `bash .ralphex/scripts/repos.sh diff --stat` and `diff` from the workspace root; report its workspace-relative file:line paths. A helper failure is a blocker, never an empty review or permission to use main. Other checkouts and unrelated changes are out of scope. Read-only: do not change any repository.
+
 Review code for bugs, security issues, and quality problems.
 
 ## Correctness Review
```

## agents/documentation.txt

```diff
@@ -1,6 +1,17 @@
+# LOCAL OVERRIDE (ficbird workspace) — the embedded ralphex default plus a
+# multi-repository layer. This workspace is one git repository and every
+# services/<name>/src is another (services/ is gitignored here), so the stock
+# prompts' `git diff` / `git status` / `git commit` see only compose, dwe config
+# and the plan. See .ralphex/scripts/repos.sh. Rebuild from
+# `ralphex --dump-defaults` when ralphex updates its defaults.
+#
+
+
+MULTI-REPOSITORY WORKSPACE: review only repositories listed in .ralphex/repos, against .ralphex/base-ref and on .ralphex/task-branch. Use `bash .ralphex/scripts/repos.sh diff --stat` and `diff` from the workspace root; report its workspace-relative file:line paths. A helper failure is a blocker, never an empty review or permission to use main. Other checkouts and unrelated changes are out of scope. Read-only: do not change any repository.
+
 Review code changes and identify missing documentation updates, plus existing documentation the changes made stale or inaccurate.
 
-First read the current README.md and CLAUDE.md - report a gap only when the item is not already documented.
+First read the current README.md and AGENTS.md - report a gap only when the item is not already documented. This workspace and its service repositories have no `CLAUDE.md` by design — Claude Code reads `AGENTS.md` natively; never report one as missing and never create one.
 
 ## README.md (Human Documentation)
 
@@ -26,9 +37,9 @@
 
 If the repository maintains other documentation (docs/ site, CHANGELOG, man pages, --help text), apply the same criteria to those.
 
-## CLAUDE.md (AI Knowledge Base)
+## AGENTS.md (AI Knowledge Base)
 
-Check if changes require CLAUDE.md updates:
+Check if changes require AGENTS.md updates:
 
 Must document:
 - New architectural patterns established by these changes
```

Agents `implementation`, `simplification` and `testing` carry the same block as `quality`.

## scripts/repos.sh (bash; semantics to port to POSIX sh `ws-git ws-*`)

```bash
#!/bin/bash
# Task-scoped multi-repository reads for the workspace's ralphex prompts.
# Product checkouts are gitignored by the workspace, so its own diff cannot
# cover them. Explicit scope prevents an unrelated checkout entering review.
#
# .ralphex/repos lists workspace-relative repositories, one per line.
# .ralphex/base-ref and .ralphex/task-branch each contain one required value.
# Missing repositories, refs and git failures are errors, never empty diffs.
# diff/log/wip also require the task branch and the release base as an ancestor.
# check/status can inspect the release checkouts before Task 1 prepares branches.
#
#   repos.sh check          resolve scope/base and show current branches
#   repos.sh diff [--stat]  committed task changes, base-ref...HEAD
#   repos.sh wip [--stat]   tracked changes against HEAD and untracked names
#   repos.sh log           task commits ahead of base-ref
#   repos.sh status        inspect scoped working trees without changing them
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"

fail() {
    printf 'repos.sh: %s\n' "$*" >&2
    exit 1
}

read_setting() {
    local file="$1"
    local -a values=()
    [ -f "$file" ] || fail "missing configuration: $file"
    mapfile -t values < "$file"
    [ "${#values[@]}" -eq 1 ] && [ -n "${values[0]}" ] ||
        fail "$file must contain exactly one nonempty line"
    printf '%s\n' "${values[0]}"
}

base_ref=$(read_setting .ralphex/base-ref)
task_branch=$(read_setting .ralphex/task-branch)
git check-ref-format --branch "$task_branch" >/dev/null ||
    fail "invalid task branch: $task_branch"
[ -f .ralphex/repos ] || fail "missing configuration: .ralphex/repos"
mapfile -t repos < .ralphex/repos
[ "${#repos[@]}" -gt 0 ] || fail ".ralphex/repos must not be empty"

validate_scope() {
    local r current
    local -A seen=()
    for r in "${repos[@]}"; do
        [[ "$r" = . || "$r" =~ ^services/[^/]+/src$ ]] ||
            fail "invalid repository path in .ralphex/repos: $r"
        [ -z "${seen[$r]+present}" ] || fail "duplicate scoped repository: $r"
        seen[$r]=1
        [ -d "$r/.git" ] || fail "missing ordinary checkout: $r"
        git -C "$r" rev-parse --verify "$base_ref^{commit}" >/dev/null ||
            fail "$r: configured base does not resolve: $base_ref"
        current=$(git -C "$r" symbolic-ref --quiet --short HEAD) ||
            fail "$r: detached HEAD is not a task checkout"
        case "$cmd" in
        diff|log|wip)
            [ "$current" = "$task_branch" ] ||
                fail "$r: expected task branch $task_branch, found $current"
            git -C "$r" merge-base --is-ancestor "$base_ref" HEAD ||
                fail "$r: configured release base is not an ancestor of HEAD"
            ;;
        esac
    done
}

label() {
    if [ "$1" = . ]; then echo "workspace"; else echo "$1"; fi
}

prefix() {
    if [ "$1" = . ]; then echo ""; else echo "$1/"; fi
}

cmd="${1:-}"
shift || true
case "$cmd" in
check|diff|wip|log|status) ;;
*)
    echo "usage: repos.sh check | diff [--stat] | wip [--stat] | log | status" >&2
    exit 2
    ;;
esac
validate_scope

case "$cmd" in
check)
    for r in "${repos[@]}"; do
        printf '%s: branch=%s base=%s task=%s\n' "$(label "$r")" \
            "$(git -C "$r" symbolic-ref --quiet --short HEAD)" "$base_ref" "$task_branch"
    done
    ;;
diff)
    for r in "${repos[@]}"; do
        p=$(prefix "$r")
        echo "### $(label "$r") — $task_branch (base: $base_ref)"
        git -C "$r" --no-pager diff --src-prefix="a/$p" --dst-prefix="b/$p" "$@" "$base_ref...HEAD"
        echo
    done
    ;;
wip)
    for r in "${repos[@]}"; do
        out=$(git -C "$r" status --porcelain)
        [ -n "$out" ] || continue
        p=$(prefix "$r")
        echo "### $(label "$r")"
        git -C "$r" --no-pager diff --src-prefix="a/$p" --dst-prefix="b/$p" "$@" HEAD
        untracked=$(git -C "$r" ls-files --others --exclude-standard)
        if [ -n "$untracked" ]; then
            while IFS= read -r file; do
                printf 'untracked: %s%s\n' "$p" "$file"
            done <<< "$untracked"
        fi
        echo
    done
    ;;
log)
    for r in "${repos[@]}"; do
        echo "### $(label "$r") (base: $base_ref)"
        git -C "$r" --no-pager log --oneline "$base_ref..HEAD"
    done
    ;;
status)
    for r in "${repos[@]}"; do
        out=$(git -C "$r" status --porcelain)
        [ -n "$out" ] || continue
        echo "### $(label "$r")"
        echo "$out"
    done
    ;;
*)
    echo "usage: repos.sh check | diff [--stat] | wip [--stat] | log | status" >&2
    exit 2
    ;;
esac
```
