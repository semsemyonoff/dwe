# Run ralphex across workspace repositories

A DWE workspace can contain a root Git repository and separate service repositories.
Ralphex's normal Git commands see only the root: a service commit can look like no
change to its review loop, and a root diff misses the implementation. The ralphex
[workspace pack](../reference/render/workspace.md) installs a Git wrapper and prompt
blocks that review the root together with the repositories selected for each plan.
Unrelated service checkouts stay outside that scope. No empty root anchor commit is
needed after a service commit.

The starter lives in [examples/workspace-packs/ralphex/README.md](../../examples/workspace-packs/ralphex/README.md).
After copying it, your workspace owns the pack, commands and scripts. DWE supplies
the generic render mechanism; it does not embed ralphex's prompts or update your
copy of the starter.

## Install the starter

Install ralphex locally. The starter targets its v1.7.0 default layout and regenerates
overrides from the installed version. Its scripts need POSIX sh, awk, diff, Git,
and either sha256sum or shasum.

Run `dwe --version`, then fetch the DWE tag matching that installed version. From
your workspace root, with the matching tag substituted below:

```sh
DWE_TAG='<matching-dwe-tag>'
git clone --depth 1 --branch "$DWE_TAG" https://github.com/semsemyonoff/dwe.git /tmp/dwe-starters
cp -R /tmp/dwe-starters/examples/workspace-packs/ralphex/workspace/. workspace/
```

The trailing `/.` copies the directory's contents with both BSD and GNU cp. Merge
the starter's `workspace.yml.snippet` into your root `workspace.yml`, preserving any
other entries in `render.workspace`. The snippet selects the `ralphex` pack and
optionally sets its default branch. The copied `workspace/commands/ralphex.yml`
provides its command group; the README links all starter files.

Add this line to the root `.gitignore`:

```gitignore
/.ralphex/run/
```

Remove an older scaffold's `/.ralphex/` rule, so the generated configuration and
prompts can be committed. Ralphex rewrites `.ralphex/.gitignore` itself; keep the
run-state rule in the root file. Generate the pack and prompts, then commit:

```sh
dwe render workspace
dwe cmd ralphex.prompts
git add workspace.yml workspace/ .gitignore .ralphex/
git commit -m 'feat: configure workspace ralphex'
```

Rendering writes `.ralphex/config`, the executable `.ralphex/scripts/ws-git` and
four phase blocks. Prompt generation inserts those blocks into ten overrides:
five prompts (`task`, `review_first`, `review_second`, `codex`, `codex_review`) and
five review agents. It preserves the installed defaults, including their trailing
signal rules, and never copies the dumped config over the rendered config.
Ralphex markers such as `{{PLAN_FILE}}` stay literal in the pack's verbatim files.
Generation rejects symlinked output directories and owned destinations that are
not ordinary files before changing any override or defaults stamp.

## Set scope for each plan

The agent that writes the plan prepares git and scope in one step, then hands the
launch line to a human, who runs ralphex in a terminal.

1. Choose a base ref, a task branch and the repositories in scope (`repos` is a
   whitespace-separated list of workspace-relative paths without spaces in their
   names; `repos=.` means a root-only plan). The root is always included.
2. Write the plan, then run scope with `prepare=true` and the plan path:

   ```sh
   dwe cmd ralphex.scope --set repos='services/api/src services/web/src' --set base=plan-base --set branch=task/example --set prepare=true --set plan=docs/plans/example.md
   ```

3. Give the last line of the output to the human. It is ready to paste; the
   `ws-prepare` and `ws-check` lines come before it:

   ```text
   Launch ralphex with:
   ralphex --base-ref plan-base --branch task/example docs/plans/example.md
   ```

Values are POSIX-quoted in that line when needed. `plan` is optional: it must be a
relative path, must not start with `-`, and must be an existing regular file. When
omitted, the line contains a literal `<plan>` placeholder to fill in. Plan paths
with whitespace are rejected: ralphex v1.7.0 does not recognize such a plan as the
only uncommitted file when it creates the root branch, and refuses to start.

The base must not refer to `HEAD` or to the task branch in any spelling
(`task/example`, `heads/task/example`, `refs/heads/task/example`, `task/example~1`):
it would follow the task branch tip and hide the task's changes. Spellings are
compared case-insensitively, because macOS file systems usually are. A base that has to
be created as a tag cannot be named like a ref path (`refs/…`, `heads/…`, `tags/…`,
`remotes/…`).

`prepare=true` runs `ws-git ws-prepare` for the root and every scoped repository:

- If the base does not resolve, it creates a lightweight tag at the current HEAD.
- In non-root repositories it switches to an existing task branch, which must
  contain the base, or creates the branch from the base (not from HEAD) with no
  upstream.
- The root is handled differently. If it is on ralphex's `default_branch` (read
  from `.ralphex/config`; `main` or `master` if absent), its branch is left alone:
  ralphex creates or switches `--branch` there at launch and auto-commits the plan.
  Otherwise the root is treated like the other repositories. The rendered default
  branch is `main`, or `vars.ralphex.default_branch` when set.
- Everything is validated in every repository before anything is changed, except
  failures only `git switch` can detect (a dirty-tree conflict, a branch checked
  out in another worktree).
- Created tags and branches are not rolled back on a later failure. Prepare is
  idempotent: fix the cause and rerun it.
- Dirty working trees are not pre-checked; a `git switch` conflict fails with git's
  message.

Manual alternative: prepare the tags and branches yourself in every scoped
repository and the root, then run the same command with `prepare=false` (the
default) and, optionally, `plan=…`. Launch ralphex with the same base and branch.

The command writes `.ralphex/run/{repos,base-ref,task-branch}`, runs `ws-check` and
restores previous run state if validation fails. Never commit that run state.
Repositories must be ordinary checkouts with a `.git` directory; absolute paths,
`..` components and symlinks are rejected. Existing run-state settings must be
ordinary files; symlinks and other file types are rejected before changing any
state.

`ws-check` is strict. Every non-root repository must be on the task branch with the
base as an ancestor. The root must be either on the task branch, or on
`default_branch` with the base as an ancestor of the existing task branch (or of
HEAD when that branch is absent). A failure is a blocker for the run, not
something to fix by creating branches ad hoc.

Inspect the selected repositories with:

```sh
.ralphex/scripts/ws-git ws-check
.ralphex/scripts/ws-git ws-status
.ralphex/scripts/ws-git ws-log
.ralphex/scripts/ws-git ws-diff --stat
.ralphex/scripts/ws-git ws-wip
```

`ws-check` enforces the contract above. During the run, `ws-log`, `ws-diff` and
`ws-wip` need every scoped repository, including the root, on the task branch with
the base as an ancestor (ralphex switches the root at launch). They fail explicitly
when that is not so. The prompt blocks direct task and review agents to these
commands and to commits made per repository with explicit task-owned paths.
The wrapper also combines the scoped HEADs and working-tree changes for ralphex's
change detection. Before any run state exists, intercepted Git probes cover the
root only so bootstrap and plan generation can work; every `ws-*` command and
every task or review run needs scope first.

## Add workspace policy

Keep project-specific rules in your copy of
`workspace/templates/workspace/ralphex/policy/`. Add manifest entries that copy
them into `.ralphex/policy/`. A `policy/<phase>.md` fragment applies to all files
of that phase; `policy/<file>.md` applies to one prompt or agent.

| Phase | Files |
|---|---|
| `task` | `task` |
| `review` | `review_first`, `review_second`, `codex` |
| `codex_review` | `codex_review` |
| `agent` | `documentation`, `implementation`, `quality`, `simplification`, `testing` |

For example, put verification and pre-existing-issue rules in `policy/task.md`
and `policy/review.md`, or an `AGENTS.md` review rule in
`policy/documentation.md`. Insertions are ordered: phase block, phase policy,
file policy. When phase and file names coincide, the policy is inserted once.

The first line of a block or policy fragment must not start with `#`; generation
rejects it because ralphex can strip leading Markdown headings with its comment
header. Blocks and policy go at the start of the body after that header; agent
frontmatter is preserved too. After editing pack blocks, policy or the manifest:

```sh
dwe render workspace
dwe cmd ralphex.prompts
```

Review and commit the updated pack sources and `.ralphex/` outputs.

## Check changes after a ralphex update

```sh
dwe cmd ralphex.prompts --set check=true
```

Check mode regenerates into a temporary directory, compares all ten overrides,
and reports version/default hash drift against `.ralphex/defaults.stamp`. It writes
nothing to the workspace and exits non-zero for changed overrides or stamp drift.
This catches changed defaults, edited blocks or policy, and hand-edited prompts.
Review the differences, regenerate with `dwe cmd ralphex.prompts`, and commit.

Unowned files in `.ralphex/prompts/` and `.ralphex/agents/` produce warnings and are
retained. Remove obsolete overrides deliberately: stale `custom_eval`,
`custom_review` or `make_plan` files can shadow newer defaults. Custom agents may
need the workspace scope block added separately. Rendering also retains outputs
whose manifest entries were removed; remove those files yourself.

## Limitations and future prepend support

- End-of-run diff statistics cover the root repository only.
- `custom_review` and `custom_eval` are not overridden; with
  `external_review_tool = custom`, review covers the root only.
- `--worktree` is unsupported. The starter sets `use_worktree = false`.
- Finalize is disabled with `finalize_enabled = false`, because its rebase covers
  only the root.

This is the stage-one integration: full overrides are generated from installed
defaults plus insertions. If upstream ralphex adds prompt and agent prepend
support, a stage-two pack can copy blocks and policy as verbatim prepend files;
the `ralphex.prompts` command and defaults stamp can then be removed. That support
is a future migration, not a prerequisite for this starter.
