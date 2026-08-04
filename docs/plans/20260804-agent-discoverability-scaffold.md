# Plan C — Discoverability and starting canvas

## Overview

The two analysed sessions produced a counter-intuitive result: **most of what the agent
needed already existed and was never found.**

| Capability | Shipped since | Uses across 2 sessions / 5 workspaces |
|---|---|---|
| `dwe validate --quiet` | May | **0** — replaced by 7 hand-written python parsers |
| `dwe validate --level error,warning` | 11 Jun | **0** |
| `-v` / `--debug` | shipped | **0** across 94 `dwe` invocations, including while debugging `Bad substitution` |
| `docs show --anchors` / `--toc` | shipped | **0** — all 26 `docs show` calls truncated with `head`/`sed` |
| builtins (`buildRegistry`) | **11 action + 8 predicate + 5 internal = 24**; 19 user-callable | the large majority used by nobody |
| auto-injected `PROJECT`/`UID`/`GID` (`workspace.go:1468`) | shipped | reverse-engineered from another project's `.env`; `docs search "UID GID env"` returned `[]` |

**Correction carried over from Plan B's review** — an earlier draft of this plan listed
"`check:` accepts the same predicates as `when:`" as a discoverability failure and planned
to write that into the skill and `llms-txt`. It is **false**: `when: {type: builtin}` and
`check: {type: builtin}` are two disjoint registries (see Plan B, Context). Authors write
`test -n "$(ls -A …)"` in `check:` because `dir-not-empty` is unavailable there. What
belongs in the docs is the *distinction between the two registries*, not an invitation to
use predicates that will be rejected.

The skill's very first instruction is already `dwe docs llms-txt --lang en`, so a
guaranteed-read entry point exists — it just carries a service line and a flat list of
links today. This plan makes that entry point earn its position, fixes the search that
returns nothing for any two-word query, and puts the genuinely universal parts of a
workspace into `dwe init` so the agent stops reverse-engineering other repositories
(~20 MCP file fetches per session, which in one case forced two context compactions).

**Scope boundary, decided explicitly:** only patterns that follow from DWE's own mechanics
ship in the scaffold ("class 1"). Infrastructure conventions — two-layer images, publish
targets, entrypoint/sync-deps scripts, SFTP fixtures, Forgejo vs GitHub CI — are **out of
scope entirely**, in the scaffold and in the skill alike. The five surveyed workspaces
belong to one owner and three were copied from the other two, so their agreement measures
house style, not universality.

**Depends on Plans A and B.** The scaffolded pipeline skeleton must contain the final
primitives (`source_clone`, `check: auto`), and the noise cleanup in Plan A is what makes
a freshly scaffolded project validate silently. Specifically, **Task 4 cannot merge before
Plan A Task 7**: activating `dir` on the scaffolded service is exactly what *unmasks* the
three `template pack not found` diagnostics per service — today they stay silent only
because `svc.Dir` is empty and the template validators bail out with an info line instead.

**Explicit policy decision — `dwe test run` becomes agent-runnable under conditions.**
Today the rule is unconditional the other way, and it is written in six places:
`skills/dwe/references/integration-tests.md` §1 ("Never run it on your own initiative …
executes only when the user explicitly asks"), §2 (table: "hand to the user, only on
explicit ask"), §3, plus `SKILL.md:113`, `:142-144` (which argues *specifically* against
the change proposed here), `:161`, `:183`, and `recipes.md:8`. Changing a safety rule is a
decision, not a documentation chore, so it is recorded here rather than buried in a
checkbox: **the owner has decided that an agent may run `dwe test run` unattended when the
cost profile shows a cheap scenario, and must ask otherwise — with uncertainty defaulting
to asking.** Task 7 must rewrite all six sites coherently; leaving any of them is the
contradiction failure mode this branch has already hit once.

## Context (from discovery)

- `internal/cli/docs/llmstxt.go` + `llmstxt_collectors.go` — the generator. Runs
  project-aware inside a project, generic outside. Session evidence: on a fresh project it
  emitted one service line plus ~70 doc links, after which the agent left for another
  repository.
- `internal/core/docs/search.go:34` — `Search` is a deliberate **case-insensitive literal
  substring** match of the whole query, attributed to the nearest H2/H3, ranked by count.
  The comment states the design intent (identifiers over regex), which holds for
  `depends_on:` but makes any natural-language phrase return `[]`. Results carry
  `source/path/anchor/count` and **no snippet**, so a hit still costs a second `docs show`.
- `internal/cli/docs/docs.go` / `search.go` — on this branch an empty result already emits
  a stderr notice (commit `4390b1ad`); stdout stays byte-empty, JSON stays silent.
- `internal/core/project/config/workspace.go:1468` — `ReservedExportNames =
  ["PROJECT", "UID", "GID"]`, always emitted by the renderer before user rules and
  forbidden as user rule names.
- `internal/core/workflow/scaffold/templates/` — **12 files** + 4 directories. The service
  template is `workspace/services/app/service.yml.tmpl` (note the `.tmpl`), shipping `dir`,
  `ports`, `render.ide.enabled` and `icon` commented — and **no `info:` block at all**, so
  `info.title` has to be added, not uncommented. The commented `dir` value is
  `./[[ .Service ]]` (i.e. `./app`), which contradicts both the `.gitignore` entry
  (`/services/`) and `populate-init-repo.md:56` (`dir: ./services/<name>`) — Task 4 must
  name the value it activates and fix that divergence. `defaults.yml.tmpl` ships
  `exports.env` commented with a `from: services.<name>.ports.http` example.
  `workspace/docker.yml` ships `project_name` commented and is **not** a `.tmpl`
  (`templates.go:23`: `templateSuffix = ".tmpl"`), so it is copied byte-for-byte and cannot
  compute a lowercased value; the only registered template func is `yamlEsc`
  (`templates.go:123`). Golden:
  `internal/core/workflow/scaffold/testdata/golden_default.txt`.
- `internal/core/workflow/scaffold/scaffold.go:209` — `applyServicePlan` drops only files
  under `workspace/services/app/` when `Service == ""`. Anything else the scaffold gains
  that *references* the starter service (a scenario, an ai pack) would still be written and
  would dangle. Covered by the existing `TestScaffold_EmptyServiceLoadsClean`.
- `internal/core/validate/env/ports.go:283` — `portsFreeValidator` → `CollectPortConflicts`
  → `fail(...)`, and `fail` (`validate/env/env.go:17`) returns **`SeverityError`**;
  `env.All(cfg)` is part of the default run (`internal/cli/validate/validate.go:700`).
  A scaffold that declares a real port therefore turns a busy host port into a hard error.
- `internal/cli/test/list.go` — `dwe test list` with `-o json`. `workspace/tests/` exists in
  **0 of 5** workspaces (the feature postdates them), so a scaffolded starter scenario is
  also the feature's first real user.
- `skills/dwe/SKILL.md` (184 lines) + 8 references (~1570 lines). Already documents the
  JSON-envelope traps, `--tty`, and the run-things guidance added on this branch.
- Class-1 evidence from the survey (mechanics, not house style): hub triplet
  `dir` / `dir_internal: /workspace` / `work_dir_internal: /workspace/src` identical across
  9/9 services that use it; `icon` + `info.title` filled by 19/19 services; port declared
  in `service.yml` requires a paired `exports.env` rule or it is display-only (beetDeck is
  live proof of the failure); `project_name` pinned by 5/5 (3 of them hand-lowercased).

## Critical constraints for the executor (traps — read before every task)

1. **`make build` / `make test`, never bare `go test ./...`** — and this plan touches the
   docs subsystem directly, where a stale embedded tree is exactly what breaks.
2. **Every scaffold template edit requires regenerating**
   `internal/core/workflow/scaffold/testdata/golden_default.txt`.
3. **`dwe docs llms-txt`'s `--output` is a FILE PATH, not a format** — `-o json` writes a
   file named `json`. Do not "add JSON support" by wiring the global flag; if a structured
   form is wanted it needs its own flag, and the skill's existing warning must stay true.
4. **`docs show --output json` is deliberately ignored** (the document *is* the payload).
   Leave it.
5. **Do not grow the scaffold beyond class 1.** Anything infrastructure-shaped is out of
   scope by decision, not by omission — see Overview.
6. **The scaffolded project must validate clean** after Plan A — and "clean" needs a scope,
   because the `env` domain checks the **host**, not the config: a declared port that
   happens to be busy is a legitimate `SeverityError` and nothing about the scaffold can
   prevent it. Read the criterion as *zero `config` and `templates` diagnostics*, and note
   that the existing `validity_test.go` runs only `validatecfg.All()` — extend it to
   `templates.All()` or it will keep asserting something narrower than it claims.
7. **`internal/cli/` is the single writer to stdout/stderr** — relevant to Tasks 3 and 6.
8. **llms.txt sections are built in `core/docs/llmstxt`, data is collected in `cli/docs`**
   (see `Opts.Commands` / `Opts.Services`). Do not pull the execution layer into the docs
   subsystem to enumerate builtins; pass the inventory in through `Opts`.
9. **`llmstxt.go:67` resolves the locale via `i18n.ResolveLocale`, not `rflags.Locale`** —
   this is a documented rule in AGENTS.md; do not "simplify" it while editing Task 1.
10. **`service.yml` is strict-decoded against TWO allowlists** (`allowedFieldsFor` +
   `servicesAllowedFields`) — only relevant if Tasks 4–5 ever add a *new* field rather than
   activating an existing one.
11. **A scaffolded starter scenario must not be expensive to run** — it is the first thing
   an agent will execute under the new self-check policy. Keep it to "the stack deploys and
   the services report healthy".
12. **The cost profile reports facts, not a verdict.** The decision rule lives in the skill
   so it can change without a binary release; the CLI must not emit `cheap`/`expensive`.
13. **`i18n`: `docs/i18n/ru/` mirrors, plus `workspace/i18n/<lang>.yml` is a different
   namespace entirely** — do not cross them.
14. **Skill and `AGENTS.md.tmpl` must not contradict each other** — the branch already had
    one instance of `SKILL.md` and `recipes.md` stating opposite rules about what may run
    without asking. The `dwe test run` policy change touches **six** sites; see Overview.

## Development Approach

- **testing approach**: **regular** (code first, then tests). No new validators here; Plan
  A carries the TDD-for-validators rule.
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change
- maintain backward compatibility

## Testing Strategy

- **unit tests**: required for every task
- **golden tests**: the scaffold golden is the primary regression net for Tasks 4–5
- **e2e tests**: none in this project; Task 8 verifies `make test` and a real
  `dwe init` → `dwe validate` → `dwe test list` round trip by hand
- docs-subsystem tests already exist for search and llms-txt shape — extend rather than
  replace

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

Three levers, in order of leverage:

1. **Make the guaranteed-read page carry the knowledge** (Task 1). The skill already
   mandates `dwe docs llms-txt --lang en` as step one. Adding the builtin/predicate
   inventory, the diagnostic flags, the auto-injected env names, and a one-screen map of
   which template syntax is evaluated where converts a link index into a briefing.
2. **Repair the two discovery channels that actively mislead** (Tasks 2–3): a search that
   returns `[]` for any phrase, and command output that never mentions the flags that would
   have made it parseable.
3. **Ship the universal parts of a workspace** (Tasks 4–6) so the first hour is spent
   authoring rather than reconstructing, plus the cost profile that lets an agent decide
   whether it may run `dwe test run` unattended.

Task 7 aligns the skill with all of the above and encodes the self-check policy.

## Technical Details

**llms-txt additions** (Task 1) — kept compact, since the whole value is that it is read in
full: one line per builtin (`name — kind — one-line purpose`), one line per predicate, the
diagnostic flag set (`--level`, `--quiet`, `-v`, `--debug`, `--anchors`, `--toc`), the
reserved env names, and a short table of template substrates (`${…}` in commands and
pipelines; Go-template `{{ }}` in info/render packs; where neither applies). Target: a few
hundred lines, not a schema dump — the schema stays in `docs show`.

**Search tokenization** (Task 2) — split the query on whitespace, require **all** tokens to
appear in the section (AND), rank by total occurrences; a quoted query keeps today's exact
literal behaviour. Add a short snippet per hit so a result is actionable without a second
call. Existing single-token behaviour (`depends_on:`) must stay byte-identical.

**Cost profile** (Task 6) — `dwe test list -o json` gains a per-scenario object describing
what the run would cost, as facts: number of enabled services, whether any pipeline step
runs `docker build`, whether any step fetches an external artifact, whether dependency
installation steps are present, external images referenced, and total healthcheck
`start_period`. Plus a `last_run` block when the journal has one. No verdict field.

## What Goes Where

- **Implementation Steps** (`[ ]`): CLI, docs subsystem, scaffold templates, skill files,
  tests, documentation in this repository.
- **Post-Completion** (no checkboxes): re-running the original setup scenario to confirm
  the improvement, and distributing the updated skill.

## Implementation Steps

### Task 1: Turn `llms-txt` into a real briefing

**Files:**
- Modify: `internal/core/docs/llmstxt/generator.go` (`Opts`, `Generate` — the actual
  generator; the CLI files below only collect data)
- Modify: `internal/core/docs/llmstxt/testdata/llms_txt_project.golden`
- Modify: `internal/core/docs/llmstxt/testdata/llms_txt_no_project.golden`
- Modify: `internal/cli/docs/llmstxt_collectors.go`
- Modify: `internal/cli/docs/llmstxt.go` (including the `Long` text, see the budget item)
- Modify: `internal/cli/docs/llmstxt_test.go`
- Modify: `internal/core/execution/builtin/` (export an inventory — see first checkbox)

- [ ] **prerequisite**: there is no way to enumerate builtins today. `buildRegistry` is
      unexported (`builtin.go:97`) and the only exported entry points are
      `Get`/`KindOf`/`Validate`/`Describe`/`Run`/`IsInteractive`. Worse, there is no static
      one-line purpose to print: `spec.Builtin` (`spec/spec.go:42`) offers only
      `Describe(with map[string]any) string`, which is per-invocation. So this task must
      first add an exported inventory accessor **and** a static summary string to all 24
      builtins
- [ ] collect the inventory in `cli/docs` and pass it through `Opts` (constraint 8) — do not
      import the execution layer into `core/docs`
- [ ] add the builtin/predicate inventory section, one line each with kind and summary,
      **naming the two disjoint `type: builtin` registries** (`when:` conditions vs
      `check:`/step-body builtins) — that distinction is the actual knowledge gap
- [ ] add the diagnostic flag section (`--level`, `--quiet`, `-v`/`--debug`,
      `docs show --anchors`/`--toc`) and the reserved auto-injected env names from
      `config.ReservedExportNames`
- [ ] add a compact "which template syntax is evaluated where" table
- [ ] **respect the declared size budget**: `llmstxt.go:33` advertises "a dense ~2-5KB
      index", and `SKILL.md:23` makes this command a mandatory first step of every session,
      so growth is a permanent token tax. Set a hard number (proposal: ≤ 12KB), enforce it
      with a test, and update the `Long` text, `docs/reference/docs/commands.md` and
      `AGENTS.md.tmpl` to match whatever number is chosen
- [ ] write tests asserting each new section is present, and that the builtin inventory is
      derived from the registry (add a builtin in a test → it appears)
- [ ] run tests — must pass before task 2

### Task 2: Tokenized `docs search` with snippets

**Files:**
- Modify: `internal/core/docs/search.go`
- Modify: `internal/core/docs/search_test.go`
- Modify: `internal/cli/docs/search.go`
- Modify: `internal/cli/docs/search_test.go`

- [ ] split queries on whitespace and require all tokens per section (AND). **Literal mode
      must be a flag, not quoting**: `docs search` is `Args: cobra.ExactArgs(1)` and the
      shell strips quotes, so `"interpolation vars"` and `interpolation vars` arrive
      identical — only absurd nested quoting could distinguish them. Add `--literal` (or
      `--match any|all|literal`)
- [ ] decide the ranking rule and pin it: summing occurrences lets a section with 40 hits
      of "vars" and one of "interpolation" outrank the section actually about the pair.
      Either use min-across-tokens or record "sum, deliberately"
- [ ] add a short snippet to each hit. In TSV this **breaks a documented contract** —
      `<source>\t<path>#<anchor>\t<count>` is specified in `--help` and
      `docs/reference/docs/commands.md:77-104`, and `TestDocsSearchTSV` requires exactly
      three fields. Decide: fourth column or JSON-only. Either way sanitize the snippet
      (collapse whitespace, strip `\t` and `\n` — markdown tables contain both — and cap
      the length)
- [ ] update the texts that become false: `emitNoSearchMatches`
      (`internal/cli/docs/search.go:117-122` says "Search is a literal case-insensitive
      substring match") and the command's `Short`/`Long`
- [ ] write tests: `interpolation vars` and `UID GID env` (the two real queries that
      returned `[]`) now return hits; `--literal` still matches the exact phrase
- [ ] write a regression test that single-token identifier search (`depends_on:`) is
      byte-identical to today (all four existing core tests are single-token, so the AND
      change is low-risk — but pin it)
- [ ] run tests — must pass before task 3

### Task 3: Point-of-need hints in command output

**Files:**
- Modify: `internal/cli/validate/validate.go`
- Modify: corresponding `*_test.go`

- [ ] when `validate` emits a long human table, add a single trailing stderr line naming
      `--level error` / `--quiet`, reusing the established notice shape
      (`render.NewWriter(cmd.ErrOrStderr()).Info(...)` with an early return when
      `flags.Output == "json"`, as `cmdctx.EmitDefaultNotice` / `emitNoSearchMatches` do) —
      not a new writer
- [ ] define "long" as a concrete threshold (diagnostic count or rendered line count), so
      the negative test below is actually testable
- [ ] write tests: hint appears above the threshold on the human path, never on stdout,
      never in JSON, and not below the threshold
- [ ] run tests — must pass before task 4

*(Scope note: the earlier draft carried an open-ended "audit for one or two other high-cost
misses" checkbox. Removed — an unbounded scan inside a task violates the one-logical-unit
rule. If other hint sites are wanted, they are their own task.)*

### Task 4: Class-1 content in existing scaffold templates

**Files:**
- Modify: `internal/core/workflow/scaffold/templates/workspace/services/app/service.yml.tmpl`
- Modify: `internal/core/workflow/scaffold/templates/workspace/defaults.yml.tmpl`
- Modify: `internal/core/workflow/scaffold/testdata/golden_default.txt`
- Modify: `internal/core/workflow/scaffold/validity_test.go`

**Blocked by Plan A Task 7** — activating `dir` is precisely what unmasks the three
`template pack not found` diagnostics per service (see Overview).

- [ ] make the hub triplet active (`dir`, `dir_internal: /workspace`,
      `work_dir_internal: /workspace/src`) — identical in 9/9 services that use it — and
      settle the `dir` value: the template says `./[[ .Service ]]`, `.gitignore` ignores
      `/services/`, and `populate-init-repo.md:56` says `./services/<name>`. Pick one and
      fix the other two
- [ ] make `icon` active and **add** an `info:` block with `title` (19/19 services fill
      both; the template has no `info:` block at all today, so this is an addition)
- [ ] **decide the port question first**, because it gates the rest: a real `ports:` value
      makes `portsFreeValidator` return `SeverityError` on any host where that port is
      busy — a very common state right after `dwe init`. Options: leave `ports:` commented
      (then `exports.env` must stay commented too, see next item); pick an unlikely-to-clash
      port; or exclude the `env` domain from the acceptance criterion. Record the choice
- [ ] keep `exports.env` and `ports:` **in the same state** — a `from: services.<svc>.ports.http`
      rule with no active `ports.http` resolves to nothing, i.e. the scaffold would emit
      exactly the defect Plan A Task 3 flags
- [ ] remove the commented `render.ide.enabled: true` example (it is the default for
      `type: app`; only `false` is meaningful)
- [ ] **drop the `project_name` item**: `workspace/docker.yml` is not a `.tmpl`, so it is
      copied verbatim and cannot lowercase anything, and Plan A Task 4 already normalizes at
      both resolution points — an active value here would add nothing. (If it is kept for
      readability, the file must first be renamed to `.tmpl` and a `lower` func registered.)
      Also fix its header, which claims "an active docker.yml replaces the built-in policy"
      — `applyDockerArgsDefaults` (`docker.go:207`) works off `presentKeys`, so that is
      false, and it is the same "scaffold lies about itself" defect Plan A is fixing
- [ ] regenerate the golden; extend `validity_test.go` beyond `validatecfg.All()` to
      `templates.All()` and assert zero `config`+`templates` diagnostics (constraint 6)
- [ ] run tests — must pass before task 5

### Task 5: Scaffold a pipeline skeleton, an ai pack and a starter scenario

**Files:**
- Create: `internal/core/workflow/scaffold/templates/workspace/services/app/deploy.yml`
- Create: `internal/core/workflow/scaffold/templates/workspace/templates/ai/...`
- Create: `internal/core/workflow/scaffold/templates/workspace/tests/...`
- Modify: `internal/core/workflow/scaffold/` (file list)
- Modify: `internal/core/workflow/scaffold/testdata/golden_default.txt`

- [ ] **first**: extend `applyServicePlan` (`scaffold.go:209`) so artefacts that reference
      the starter service are dropped together with it. Today it filters only
      `workspace/services/app/`, so a scenario or ai pack naming `app` would still be
      written when `Service == ""` and would dangle. Add a test alongside the existing
      `TestScaffold_EmptyServiceLoadsClean`
- [ ] (5a) add a per-service `deploy.yml` skeleton with the `hub → image → render` phase
      shape reproduced by 5/5 workspaces, using Plan B's primitives (`source_clone`,
      `check: auto`) so the scaffold teaches the final idiom
- [ ] (5b) add a minimal ai render pack (present in 5/5) producing `AGENTS.md` + the
      `CLAUDE.md` symlink. Note it renders into the **service hub** (`services/app/`, which
      is gitignored and absent until the first clone), which is a different file from the
      **root** `AGENTS.md` the scaffold already writes — Task 7 must disambiguate the two in
      the skill, since `SKILL.md:28` currently says "never edit the generated `AGENTS.md`"
      and the root one is meant to be edited. (Verified: an ai pack validates cleanly even
      with the hub directory absent — `ai.ValidateManifest` does not require `destRoot`.)
- [ ] (5c) add a starter `workspace/tests/` scenario limited to "stack deploys, services
      healthy" (constraint 11) — the feature currently has no users to learn from
- [ ] regenerate the golden
- [ ] write tests: scaffolded pipeline resolves without error; scenario loads through
      `envtest.LoadScenario`; ai pack renders; `Service == ""` produces none of the three
- [ ] run tests — must pass before task 6

*(5a/5b/5c are separable if the task proves too large in practice — they share only the
golden.)*

### Task 6: Cost profile for `dwe test`

**Files:**
- Modify: `internal/cli/test/list.go`
- Modify: `internal/cli/test/list_test.go`

- [ ] add a per-scenario cost-profile object to the JSON output, restricted to facts that
      are **computable without guessing**: enabled service count, presence of `docker build`
      steps, presence of `source_clone` steps, `build:` sections in compose, referenced
      external images, summed healthcheck `start_period`
- [ ] **drop "presence of dependency-install steps"** — detecting it means string-matching
      `npm install` / `composer install` / …, a heuristic that will both lie and drift
      (YAGNI). Same for a generic "external-artifact fetch" unless it reduces to a
      checkable construct
- [ ] **drop `last_run`** — there is no source for it. `envtest` persists nothing across
      runs: `report.go` writes only for a failed run and only before teardown, and the
      manifest is deleted by teardown; the deploy journal knows nothing about scenarios.
      Adding it means designing per-scenario run persistence (where it is written — likely
      `Runner.finish` before `teardown()`, mirroring `collectReport` — what format, who
      prunes it, what `dwe test clean` does with it). That is its own plan item, not a
      checkbox here
- [ ] preserve the current invariants of `list`: no Docker, no locks, no config load
      required — today it runs on a broken config. A failed config load must degrade to
      "no profile", never to an error
- [ ] emit facts only — no `cheap`/`expensive` verdict (constraint 12)
- [ ] write tests for a minimal project (no build) and a heavy one (build + external
      images), asserting the distinguishing fields, plus one with an unloadable config
- [ ] write a test asserting the human output is unchanged
- [ ] run tests — must pass before task 7

### Task 7: Align the skill

**Files:**
- Modify: `skills/dwe/SKILL.md` (four policy sites: `:113`, `:142-144`, `:161`, `:183`)
- Modify: `skills/dwe/references/integration-tests.md` (§1, §2 table, §3 — the strictest
  statement of the old rule; **this file was missing from the first draft**)
- Modify: `skills/dwe/references/recipes.md` (`:8`)
- Modify: `skills/dwe/references/populate-init-repo.md`
- Modify: `skills/dwe/references/pipelines-and-orchestration.md`
- Modify: `internal/core/workflow/scaffold/templates/AGENTS.md.tmpl` (+ golden)

- [ ] implement the policy decision recorded in the Overview across **all six sites** —
      `dwe test run` may be run unattended when the cost profile shows a cheap scenario;
      ask when it shows a build from scratch or external images, and whenever the agent is
      unsure. `SKILL.md:142-144` currently argues the opposite explicitly and must be
      rewritten, not merely amended
- [ ] state that the agent should judge what an image build actually costs (thin layer over
      a published base vs building from scratch) rather than treating "there is a build" as
      an automatic stop
- [ ] document the class-1 rules that cannot be validated statically (mount the whole hub,
      not just `src/`; one definition — the deploy reuses the dev-facing command rather than
      duplicating it as a shell step)
- [ ] document the **two disjoint `type: builtin` registries** (`when:` conditions vs
      `check:`/step-body builtins) and point at the inventory now in `llms-txt`. The first
      draft said "`check:` accepts the same predicates as `when:`" — that is false and must
      not be written anywhere
- [ ] disambiguate the two `AGENTS.md` files: the root one is scaffolded and meant to be
      edited; the hub one is rendered by the ai pack and must not be. `SKILL.md:28` reads as
      if there is only the rendered one
- [ ] verify SKILL.md, all references and `AGENTS.md.tmpl` agree with each other and with
      the new scaffold; regenerate the scaffold golden
- [ ] run tests — must pass before task 8

### Task 8: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented
- [ ] scaffold a throwaway project with `dwe init`, then confirm end to end:
      `dwe validate` is silent, `dwe docs llms-txt --lang en` answers the questions the two
      sessions had to reverse-engineer, `dwe test list -o json` carries a cost profile
- [ ] re-run the two real search queries that returned `[]` and confirm useful hits
- [ ] run full test suite: `make test`
- [ ] run `make lint`
- [ ] verify test coverage meets project standard

### Task 9: [Final] Update documentation

- [ ] update `docs/reference/docs/commands.md` (search: TSV contract at lines 77-104, the
      new `--literal`/match flag, the llms-txt size budget) and its ru mirror
- [ ] update `docs/reference/config/tests.md` for the cost profile, and its ru mirror
- [ ] update `docs/guides/start-a-new-project.md` for the richer scaffold, and its ru mirror
- [ ] update the `--help` texts changed along the way (`search.go` Short/Long,
      `llmstxt.go` Long)
- [ ] run `make build` to resync embedded docs and content hashes
- [ ] update `AGENTS.md` Critical Patterns if any new load-bearing contract emerged
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Validation of the whole effort**:
- the honest test is a fresh agent-driven setup of a new project. The baseline to beat,
  from the analysed sessions: ~20 external repository fetches, three runtime failures
  discovered by the user rather than the tool, and a workspace committed and pushed without
  ever having been executed
- re-checking a couple of the two-word `docs search` queries an agent would naturally type

**Distribution**:
- the skill ships from this repository (`skills/dwe/`), but the copy in use is installed
  separately (`~/.agents/skills/dwe`) — it needs syncing before the next session benefits
- the five existing workspaces gain nothing automatically; class-1 improvements apply to
  newly scaffolded projects, and retrofitting is optional

**Explicitly out of scope** (decided, not forgotten): two-layer images, entrypoint and
sync-deps scripts, `Makefile` base-image targets, CI workflows, SFTP fixtures, and the
`.forgejo` vs `.github` choice. These are infrastructure conventions of one owner, not DWE
mechanics.
