# Ralphex in a DWE workspace

This is a starter owned by the workspace after installation. Copy it from the
DWE tag matching your installed `dwe --version`. The prompt layout targets
ralphex v1.7.0; regeneration uses your installed ralphex defaults, and check mode
exposes drift after upgrades.

From the workspace root:

```sh
cp -R <dwe-checkout>/examples/workspace-packs/ralphex/workspace/. workspace/
```

Merge [workspace.yml.snippet](workspace.yml.snippet) into `workspace.yml` (retain
other packs in `render.workspace`). Add `/.ralphex/run/` to the ROOT `.gitignore`,
and remove an older scaffold's `/.ralphex/` rule. Ralphex rewrites its own
`.ralphex/.gitignore`, so put the run ignore rule in the root file. Then:

```sh
dwe render workspace
dwe cmd ralphex.prompts
git add workspace.yml workspace/ .gitignore .ralphex/
git commit -m 'feat: configure workspace ralphex'
```

The pack renders `.ralphex/config`, the executable `scripts/ws-git`, and four
verbatim phase blocks. The command regenerates ten prompt/agent overrides from
`ralphex --dump-defaults`; it never copies the dumped config. Prompt variable
markers such as `{{PLAN_FILE}}` remain literal for ralphex. Generation needs POSIX
sh, awk, diff, and either sha256sum or shasum; Git is required for run scope.
Before writing, it rejects symlinked output directories and owned destinations
that are not ordinary files, leaving existing overrides and the stamp untouched.

For each plan, create the same base tag/ref in EVERY scoped repository AND THE
ROOT. Set scope before launching ralphex:

```sh
dwe cmd ralphex.scope --set repos='services/api/src services/web/src' --set base=plan-base --set branch=task/example
ralphex --base-ref plan-base --branch task/example docs/plans/example.md
```

`repos` takes whitespace-separated workspace-relative paths without whitespace
in their names. Use `repos=.` for a root-only plan. The root is always included.
The script rejects symlinked or non-regular run-state settings before changing
state, validates ordinary checkouts and refs, and restores previous run state
on failure. `ws-check`/`ws-status` can inspect release checkouts; the plan's
preflight must prepare the task branch in every scoped repo, including the root,
before `ws-log`/`ws-diff`/`ws-wip` work. They require the base to be an ancestor.
Ralphex creates `--branch` in the root only when it starts on `default_branch`.
The base and branch passed to ralphex must match the scope settings.

Review reads go through `.ralphex/scripts/ws-git ws-diff [--stat]`, `ws-log`,
`ws-wip [--stat]`, and `ws-status`. The wrapper also combines the scoped Git
fingerprints used by ralphex's review loop. No root anchor commit is needed.
Never use `--worktree`: this pack sets `use_worktree = false`. Finalize is disabled
because its rebase covers the root only. End-of-run diffstats remain root-only;
`custom_eval`/`custom_review` are not overridden, so `external_review_tool = custom`
reviews the root only.

To add workspace policy, create `policy/<phase>.md` or `policy/<file>.md` in your
copy of `workspace/templates/workspace/ralphex/` and add manifest entries rendering
them to `.ralphex/policy/`. Phase names are `task`, `review`, `codex_review`, `agent`;
file names include `review_first`, `documentation`, etc. Insertions are ordered:
block, phase policy, file policy (once when phase and file names match). Their first
line must not start with `#`, or ralphex's comment stripping may remove it. Keep
project-specific review/verification rules in these fragments.

After changing blocks or policy, run `dwe render workspace` then
`dwe cmd ralphex.prompts`. After updating ralphex:

```sh
dwe cmd ralphex.prompts --set check=true
```

Check mode writes nothing, compares all ten overrides, reports version/default
stamp drift, and exits non-zero for changes. Unowned prompt/agent files produce
warnings and are retained: stale overrides can shadow newer defaults; custom
agents may lack the scope block. Review differences, regenerate, and commit the
updated `.ralphex/` files. Upstream prepend support could eventually replace this
stage-one generator with verbatim manifest entries.
