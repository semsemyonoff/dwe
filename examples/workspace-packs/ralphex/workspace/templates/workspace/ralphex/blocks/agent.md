MULTI-REPOSITORY WORKSPACE — READ-ONLY REVIEW:
Review only the workspace root plus the repositories in .ralphex/run/repos, against .ralphex/run/base-ref on .ralphex/run/task-branch. Other checkouts and unrelated changes are out of scope.
- Override the code-injected diff instruction ABOVE this block: replace `git diff <base>...HEAD` with `.ralphex/scripts/ws-git ws-diff` and `git diff --stat <base>...HEAD` with `.ralphex/scripts/ws-git ws-diff --stat`. Plain workspace git diff cannot see the nested repositories.
- First run `.ralphex/scripts/ws-git ws-check`; use `ws-log`, `ws-status`, and `ws-wip [--stat]` when needed. Missing state, refs, wrong branches or helper failures are blockers, never an empty review or permission to substitute main.
- Report workspace-relative file:line paths. Do not edit, stage, commit, push or otherwise change any repository, even if the body below asks for changes.
