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
Changing a safety rule is a decision, not a documentation chore, so it is recorded here
rather than buried in a checkbox: **the owner has decided that an agent may run
`dwe test run` unattended when the scenario is cheap, and must ask otherwise — with
uncertainty defaulting to asking.**

**The condition is cheap AND isolated, not cheap alone.** The existing rule rests on two
arguments (see Technical Details): cost, and the fact that isolation is not total. The
owner's decision addressed cost; the isolation half is therefore carried into the profile
as facts, and the policy text reads: run unattended only when the profile shows a cheap
scenario **and** no isolation findings (shared/external/named resources) **and** no
`type: shell` steps — otherwise ask.

Today the rule is unconditional the other way, in **thirteen** places across three files
(the first draft said "six" and misattributed one of them):
- `integration-tests.md` — `:3` (intro: "the **user** runs the mutating `dwe test run`"),
  `:26` (end of §2), §1 ("Never run it on your own initiative"), §2 table ("hand to the
  user, only on explicit ask"), `:138` (§8), `:154` (§9), `:162` and `:164` (§9 — "run only
  when the user asks", table header "Command (user runs)"). **§3 does not contain the
  policy** — it was cited in error and must not be edited on that basis.
- `SKILL.md` — `:113`, `:127` ("destructive … forms are below"), `:142-144` (argues
  *specifically* against this change), `:161`, `:183`.
- `recipes.md` — `:8`.

Leaving any of them is the contradiction failure mode this branch has already hit once.

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
what the run would cost, restricted to facts computable without string-guessing: number of
enabled services **after the scenario's `env.services` overlay**, presence of `build:` in
compose, external images referenced (excluding local build tags), max healthcheck
`start_period` across services, and the isolation facts below. **No `last_run` and no
"dependency-install steps"** — both were dropped in Task 6, the first for lack of any
source of truth, the second as an unreliable heuristic. No verdict field.

**Isolation facts belong in the profile too.** The current rule ("`dwe test run` only on
explicit ask") rests on two arguments, not one, and `integration-tests.md:26` states the
second plainly: isolation is not total — `shared: true` volumes are reused verbatim,
`container_name:` / named / `external:` resources bypass compose-project scoping, and host
side effects of `shell` steps (absolute paths, `~`, binds outside the project) are not
sandboxed. Cost alone therefore cannot justify unattended runs. These facts are already
computable: the non-blocking findings of `config.ScanComposeIsolation`
(`KindNamedVolume`/`KindExternalVolume`/`KindNamedNetwork`/`KindExternalNetwork`), the count
of `shared: true` volumes, and whether any scenario or deploy step is `type: shell`.
Verified on the real workspaces: alto and cueBreaker both declare `external: true` cache
volumes shared across projects, which a test run writes into.

**The honest limit of the profile** (state it in Task 6 rather than discover it later): it
distinguishes *whether* there is a build, not what the build costs. The dominant factor —
whether the layer cache is warm, seconds versus ~10 minutes — is not modelled, and was lost
with `last_run`. Measured against the two real workspaces, both land in "there is a build →
ask", so the new policy would not fire on either. That is acceptable for a first cut, but
it must be written down, and Task 7's wording must not promise more than the profile
carries.

## What Goes Where

- **Implementation Steps** (`[ ]`): CLI, docs subsystem, scaffold templates, skill files,
  tests, documentation in this repository.
- **Post-Completion** (no checkboxes): re-running the original setup scenario to confirm
  the improvement, and distributing the updated skill.

## Implementation Steps

### Task 1a: Export a builtin inventory

*(Split out of the original Task 1: this half lives in the execution layer, 1b in the docs
layer, and the combined task carried eight checkboxes against a norm of ~5.)*

**Files:**
- Modify: `internal/core/execution/builtin/spec/spec.go` (`Entry`)
- Modify: `internal/core/execution/builtin/builtin.go` (root map + package doc-comment)
- Modify: `internal/core/execution/builtin/{containers,services,fs,env,interaction}/*.go`
  (the five `Builtins()` maps)
- Modify: `internal/core/execution/builtin/builtin_test.go`

There is no way to enumerate builtins today: `buildRegistry` is unexported
(`builtin.go:97`) and the exported surface is only
`Get`/`KindOf`/`Validate`/`Describe`/`Run`/`IsInteractive`; and there is no static purpose
string, since `spec.Builtin` (`spec/spec.go:42`) offers only the per-invocation
`Describe(with)`.

- [ ] add `Summary string` to **`spec.Entry`** (`spec/spec.go:89`), next to the existing
      `Kind` — **not** a `Summary()` method on the `spec.Builtin` interface. The interface
      route means 24 edits across 23 files in six packages; the `Entry` route is 6 edits
      (five `Builtins()` maps plus the root map in `builtin.go:98`) and keeps `Entry` as the
      single home of metadata. Verified safe: `spec.Builtin`/`builtin.Builtin` is not
      implemented anywhere outside the `builtin/` tree, and there are no test doubles
- [ ] add an exported inventory accessor over the registry
- [ ] fix the package doc-comment `builtin.go:29-37`, which currently mislabels
      `docker_daemon_start`/`_logs`/`_stop` as `KindAction` while the registry
      (`containers/containers.go:13-15`) has them as `KindInternal` — once the inventory is
      generated, that comment becomes a second, contradicting list
- [ ] write a test asserting **every** registry entry has a non-empty `Summary` (the
      compiler cannot catch a missing one, and Plan B adds a 25th builtin)
- [ ] run tests — must pass before task 1b

### Task 1b: Turn `llms-txt` into a real briefing

**Files:**
- Modify: `internal/core/docs/llmstxt/generator.go` (`Opts`, `Generate` — the actual
  generator; the CLI files only collect data)
- Modify: `internal/core/docs/llmstxt/testdata/llms_txt_project.golden`
- Modify: `internal/core/docs/llmstxt/testdata/llms_txt_no_project.golden`
- Modify: `internal/cli/docs/llmstxt_collectors.go`
- Modify: `internal/cli/docs/llmstxt.go` (including the `Long` text, see the budget item)
- Modify: `internal/cli/docs/llmstxt_test.go`

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
      so growth is a permanent token tax. Measured today: `--no-project` is 4490 B,
      project-aware in alto is 5844 B. Enforce the budget on the **`--no-project`** output
      (or on the static sections alone) — a flat cap on project-aware output would be a test
      of someone else's workspace size and would fail on a large one. Estimated addition
      (~24 inventory lines + flags + reserved env + substrate table + the two-registries
      note) ≈ 3 KB → ~7.5–9 KB, so a 12 KB cap is realistic. Update the `Long` text,
      `docs/reference/docs/commands.md` and `AGENTS.md.tmpl` to the chosen number
- [ ] write tests asserting each new section is present. Note the inventory cannot be tested
      by "add a builtin in a test" from the docs layer — `registry` is a package-level var
      built by `buildRegistry()`. Test it as: (a) in `builtin`, every entry has a summary
      (Task 1a); (b) in the docs layer, the section is built from a stubbed `Opts` inventory
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
- [ ] tokenize with `strings.Fields`, **not** `strings.Split(q, " ")`: an empty token makes
      `countCaseInsensitive` return 0 (`search.go:117` returns early on an empty needle) and
      the AND gate would then zero out **every** result for a query with a double space
- [ ] use **min-across-tokens** for ranking: summing lets a section with 40 hits of "vars"
      and one of "interpolation" outrank the section actually about the pair, and min also
      makes duplicate tokens (`vars vars`) harmless
- [ ] pin the token semantics as a decision: matching stays **substring** (the documented
      design intent at `search.go:26-30`, and required for `depends_on:`), which means
      `uid` matches inside `guide`/`guides` and `env` inside `environment`. Record it with
      that example so the false-positive is a known trade-off, not a surprise
- [ ] add a **second tier**: if no section satisfies AND, apply AND at document level and
      attribute the hit to the section with the largest contribution. Without it the fix is
      only half-done — measured on the real docs tree, `UID GID env` returns the right
      sections (`config/workspace.md#exports.env`, `render/env.md#System variables`), but
      `interpolation vars` still misses `reference/templates.md` because the two tokens live
      in different sections of it
- [ ] add a short snippet to each hit. In TSV this **breaks a documented contract** —
      `<source>\t<path>#<anchor>\t<count>` is specified in `--help` and
      `docs/reference/docs/commands.md:77-104`, and `TestDocsSearchTSV` requires exactly
      three fields. Decide: fourth column or JSON-only. Either way sanitize the snippet
      (collapse whitespace, strip `\t` and `\n` — markdown tables contain both — and cap
      the length)
- [ ] update the texts that become false: `emitNoSearchMatches`
      (`internal/cli/docs/search.go:117-122` says "Search is a literal case-insensitive
      substring match") and the command's `Short`/`Long`
- [ ] decide which line the snippet comes from — `searchInDoc` (`search.go:81-111`) keeps
      only counters today. The line containing the most tokens is markedly more useful than
      the first line containing any, and costs one variable
- [ ] write tests asserting **relevance, not non-emptiness**: `interpolation vars` must
      return `reference/templates.md` in the top N, and `UID GID env` must return
      `config/workspace.md#exports.env`. "Returns hits" would pass on noise — measured, the
      naive AND does exactly that for the first query
- [ ] write a regression test that single-token identifier search (`depends_on:`) is
      byte-identical to today (all four existing core tests are single-token, so the AND
      change is low-risk — but pin it). Note this test does **not** discriminate min from
      sum ranking (they coincide for one token) — add a separate multi-token ordering test
- [ ] (checked) `coredocs.Search` has exactly one caller (`cli/docs/search.go:81`); the TUI
      and llms-txt do not use it, so the change is local
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
      `work_dir_internal: /workspace/src`) — identical in 9/9 services that use it — with
      `dir: ./services/[[ .Service ]]`. The evidence is one-sided: `.gitignore` ignores
      `/services/` (generated by `scaffold/gitignore.go`), `populate-init-repo.md:56` says
      `./services/<name>`, 9/9 services use that form, and the ai packs of alto/cueBreaker
      render into `services/<name>/`. The template's current `./[[ .Service ]]` is the
      outlier and gets fixed here
- [ ] make `icon` active and **add** an `info:` block with `title` (19/19 services fill
      both; the template has no `info:` block at all today, so this is an addition)
- [ ] **the port question is decided: leave `ports:` and `exports.env` commented, but
      paired**, with a comment stating the class-1 rule ("a port without a paired
      `exports.env` rule is display-only"). Three reasons: the scaffolded `compose.yaml`
      contains **no `services:` block at all**, so an active port binds nothing; an active
      port makes `dwe validate` depend on whether that host port happens to be busy
      (`portsFreeValidator` → `SeverityError`), turning the one deterministic acceptance
      criterion into a flaky, host-dependent check; and the knowledge transfers *better* as
      a comment that states the rule than as a pair that merely demonstrates it. The
      rejected third option — excluding the `env` domain from the criterion — would weaken
      exactly the check that catches regressions
- [ ] this also gates Task 5c: with `ports:` commented, the starter scenario **cannot**
      reference `${services.app.ports.http}` — `envtest` renders `${…}` before resolve, the
      reference would collapse to `""`, and `http_check` would get `http://localhost:/health`
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
      `check: auto`) so the scaffold teaches the final idiom. **Fallback**: Plan B calls its
      `source_clone` task "the most droppable task in this plan" — if it is dropped, the
      skeleton uses a `type: shell` clone plus `check: auto` instead (after Plan A Task 2,
      `${vars.*}` renders in `cmd:`), which costs the skeleton nothing
- [ ] (5b) add a minimal ai render pack (present in 5/5) producing `AGENTS.md` + the
      `CLAUDE.md` symlink. Note it renders into the **service hub** (`services/app/`, which
      is gitignored and absent until the first clone), which is a different file from the
      **root** `AGENTS.md` the scaffold already writes — Task 7 must disambiguate the two in
      the skill, since `SKILL.md:28` currently says "never edit the generated `AGENTS.md`"
      and the root one is meant to be edited. (Verified: an ai pack validates cleanly even
      with the hub directory absent — `ai.ValidateManifest` does not require `destRoot`.)
- [ ] (5c) add a starter `workspace/tests/` scenario — with two facts acknowledged in the
      file itself. First, it **cannot** follow the "shipped fully commented" idiom every
      other inert scaffold uses: the `envtest` loader is strict and an empty/all-comment
      file is an **error**, so this is the one scaffold file that must be active (and the
      one exception to Plan A Task 11's "uncommenting each inert scaffold must load
      cleanly" table). Second, on a fresh scaffold `compose.yaml` has no services, so
      "stack deploys, services healthy" would pass **vacuously** — which matters because
      constraint 11 makes this the first thing an agent runs under the new policy. Prefer an
      honest template: `description:` plus `steps: []` and a comment saying to add
      assertions once the service exists in compose. It also cannot reference
      `${services.app.ports.http}` — see Task 4
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
- Modify: `internal/core/project/config/compose_scan.go` (extend the existing narrow compose
  parser — Plan A Task 5 explicitly forbids adding a third one)

- [ ] add a per-scenario cost-profile object to the JSON output, restricted to facts that
      are **computable without guessing**: enabled service count, `build:` sections in
      compose, external images referenced, **max** healthcheck `start_period`, plus the
      isolation facts (see Technical Details)
- [ ] compute the service count **over the scenario's `env.services` overlay** — otherwise
      every scenario in a file reports identical numbers, which is exactly what distinguishes
      them (the documented `redis-off.yml` example)
- [ ] exclude services that have `build:` from "external images" — a local build tag like
      `image: alto-app:dev` is not something to pull, and counting it makes the fact lie in
      the least helpful direction
- [ ] use **max** rather than sum for `start_period`: `docker up --wait` waits in parallel,
      so a sum over-estimates the more services there are
- [ ] **drop "presence of dependency-install steps"** — detecting it means string-matching
      `npm install` / `composer install` / …, a heuristic that will both lie and drift
      (YAGNI). Drop "presence of `docker build` steps" for the same reason — it is the same
      kind of string match, and the checkable construct (`build:` in compose) is already in
      the list; in both real workspaces the build goes through compose, not through a step
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
- [ ] record the honest limit in the docs and in the profile's own description: it tells
      whether there **is** a build, not what the build costs; layer-cache warmth is not
      modelled (it went with `last_run`). Measured on alto and cueBreaker, both land in
      "there is a build → ask", so the unattended path would not fire on either today
- [ ] write tests for a minimal project (no build) and a heavy one (build + external
      images), asserting the distinguishing fields; one with an unloadable config; and one
      where two scenarios in the same file differ only by `env.services` and get different
      profiles
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

- [ ] implement the policy decision recorded in the Overview across **all thirteen sites**
      listed there — unattended only when the profile shows a cheap scenario **and** no
      isolation findings **and** no `type: shell` steps; ask otherwise, and whenever unsure.
      `SKILL.md:142-144` currently argues the opposite explicitly and must be rewritten, not
      merely amended. Do **not** edit `integration-tests.md` §3 — it does not contain the
      policy and was cited in error
- [ ] give the conditional rule a structural home: `dwe test run` currently sits under the
      heading "You **MUST NOT** invoke these MUTATING commands yourself" (`SKILL.md:153`),
      so rewriting the bullet in place would make the heading false. Move the conditional
      form into the existing "**Running project tasks — judge the task, not the verb**"
      subsection (`:132-151`), which already has the right shape, and leave `:161` as a
      pointer to it
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
      `dwe validate` reports no `config`/`templates` diagnostics, `dwe docs llms-txt --lang en`
      answers the questions the two sessions had to reverse-engineer, `dwe test list -o json`
      carries a cost profile
- [ ] verify **rendering**, not only validation: `render.ai` is on by default for
      `type: app` (`renderEnabledExplicit`), so after Task 5b a fresh `dwe deploy run` will
      try to render into `services/app/`, which does not exist until the first clone.
      Confirm that path behaves sanely
- [ ] note the reciprocal dependency: **this task is what makes Plan A's Task 15 acceptance
      criterion true.** Measured on a real `dwe init`, Plan A alone leaves one warning
      (`service "app" … has no dir or dir_internal`), and only activating `dir` here clears
      it — while simultaneously creating the template-pack warnings Plan A Task 7 absorbs
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
- [ ] note the merge order with Plan A: both plans touch
      `internal/cli/validate/validate.go` (Plan A Task 14 adds scope to the summary, Task 3
      here adds the hint line) — A lands first
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
