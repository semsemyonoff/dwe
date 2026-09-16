# Agent command discovery: JSON list gains `description`/`service`, one shared summary, a trigger rule in the shipped agent templates

## Overview

One branch (`feat/0.6.2-command-index`, cut from `release/0.6.2`), three commits,
one PR into `release/0.6.2`.

**The problem.** The command registry is the single asset every project leans on,
and it measurably drives agent behaviour — on one project the share of `dwe`
invocations differed by 30× over a week, and the only variable was whether a
command was declared for the service. Yet the discovery channel is broken at both
ends:

- **The pull channel is a trap.** `dwe commands list --output json` emits
  `id/group/title/type/private/params` and **no `description`, no `service`**
  (`internal/cli/command/list.go:29-36`). The scaffolded root `AGENTS.md` tells
  every agent "for any command whose output you parse — including
  `dwe commands list` — add `--output json`"
  (`internal/core/workflow/scaffold/templates/AGENTS.md.tmpl`). We route the agent
  onto the one branch where the field it picks a command by has been stripped: it
  sees 74 opaque ids and goes back to `npm run test`. The text branch has the
  descriptions; the JSON branch does not.
- **The push channel says nothing.** Both shipped agent templates carry a passive
  capability mention ("`dwe commands list` — list this project's declared dev
  commands"). Measured outcome: in two large sessions on a real project the agent
  ran `dwe docs llms-txt` zero times and `dwe commands list` never fired from that
  line either.

**Change A — `commands list --output json` gains `description` and `service`.**
Additive; `list.json.golden` is regenerated; a consumer reading known keys is
unaffected. Descriptions localize through the same `store.*` helper as the text
branch — this is CLI output, not a tracked file.

**Change B — one shared walk, two consumers.** Two plain-data types land in
`usercommands/model` and one builder next to the registry facade. The render
packs' shared `TemplateData`
(`internal/core/execution/templates/packcommon/packcommon.go:96-104`) — which
today has no commands at all — gains the index plus hub-scoped accessors, and
`cli/docs` stops hand-rolling its own walk and maps the shared index into
`llms-txt`'s existing local type. **`core/docs` keeps its own flat
`CommandSummary`**: the whole docs subsystem imports no other internal package
today, which is why `ServiceSummary`, `BuiltinSummary` and `ConditionSummary`
exist as flat mirrors in the first place (`generator.go:30-33` states the rule for
the execution layer). What is single-sourced is the walk, which is where the
duplication actually was.

**Change C — the shipped templates push a rule, not a table.** The hub
`AGENTS.md` template gains a block naming this hub's command groups with their
authored descriptions, their declared counts and the exact scoped call, wrapped
in a conditional trigger rule. The root template gets the same rule in generic
prose, with no data and no generation.

**Why not the full table** (this reverses the original intent, on evidence):
`dwe commands list <group>` already accepts a positional group filter and returns
exactly what a baked table would contain — 1415 bytes for podlapka's `admin`
group, descriptions included. Baking it into the file buys one saved tool call and
pays permanently: 5–10 KB per hub on a large project (magento's full listing is
9.9 KB), multiplied by every hub, in a file whose whole purpose is to be loaded
before anyone knows whether it is relevant. The project's own doctrine already
says so — podlapka's `workspace/scripts/agents/budget.sh`: *"an AGENTS.md is
loaded into EVERY agent session whether or not it is relevant, so each line costs
tokens on every turn forever … Move the detail out … then leave a one-line pointer
in its place."* What failed before was not the pointer's existence but its form: a
capability mention with no trigger, no priority and no negative boundary. This
plan fixes the pull channel first, then replaces the passive mention with a rule
that names when to look, what wins, and what not to run until the check is done.

All three changes are user-observable → `CHANGELOG.md` `## [Unreleased]` gains
`### Added` (shared index, template block) and `### Changed` (the two JSON
fields). No `## Upgrading to 0.6.2` entry is needed: nothing a project has
written stops working, and no command changes its exit code.

### Non-goals

Decided during design; do not re-litigate during implementation:

- **No command table in any shipped template.** Not in the hub template, not in
  the root one. A project that wants one has the data in `TemplateData` and its
  own pack to render it in.
- **The `inspect` DTO is not touched** (`internal/cli/command/inspect.go`), and
  the `list` DTO change is additive only — no field is renamed, reordered or
  removed, no existing consumer changes.
- **No `bridge` field in the summary.** Effective bridge is only computed inside
  a container (`registry/visibility.go:134-147` unconditionally clears
  `BridgeHidden` on the host, and `collectEffectiveBridge` is unexported), so
  reading `def.Bridge` directly lies for any command that inherited `bridge:`
  from its `group:` header. When it is needed, export the helper from `registry`
  — do not duplicate it.
- **No `Params`, no `AcceptsArgs`, no `Title` in the *command* summary** (the
  *group* summary keeps `Title` — it is the group line's fallback text when a
  `group:` header declares no description). No shipped
  template prints them, `llms-txt` ignores them and Change A does not add them to
  the JSON DTO. `dwe cmd -i <id> --output json` returns params in full,
  `ReferencesArgs()` is a one-line call, and `Title` is the last dotted segment of
  `ID` (`list.go:101`). Add when a consumer exists — the same rule that keeps
  `bridge` out.
- **`core/docs` stays a leaf.** `llmstxt` keeps its own flat `CommandSummary`;
  the shared type does not cross into the docs subsystem.
- **No intent classifier.** Deriving domains ("tests", "migrations") from id or
  title text is a heuristic over English naming that breaks on the first project
  with domain-specific names — and group naming conventions across the twelve
  local workspaces are already incompatible (`admin` / `site` in podlapka, 17
  nested `services.magento.*` groups in magento). `group.title` /
  `group.description` are the authored intent markup; there is no second one.
- **`ApplyVisibility` is deliberately NOT called on the pack-render path.** See
  Technical Details; the pack index is *declared* commands, not *currently
  available* ones.
- **Existing workspaces are not updated.** Template packs live in the project
  (`workspace/templates/ai/<pack>/`), not in the binary — podlapka owns five,
  ficbird three — and the scaffold template is written once at `dwe init`. The
  new block therefore reaches newly scaffolded projects only; retrofitting the
  twelve existing ones is separate work. The block is published as a copyable
  snippet in `docs/reference/render/ai.md` so that work is mechanical.
- **No new config keys.** Nothing in `service.yml`, `workspace.yml` or a command
  file changes shape.
- **The root `AGENTS.md` gets no generated data.** It is scaffolded once and
  hand-maintained; a generated table there would be empty on every fresh project
  (the scaffold declares no starter commands) and would never refresh.

## Context (from discovery)

Verified on `release/0.6.2` @ `de2600e0`.

**The three existing projections**

- `internal/cli/command/list.go:23-45` — `commandsListJSON` / `commandEntryJSON`
  (`id`, `group`, `title`, `type`, `private`, `params`), built by
  `buildCommandsListJSON` (`:53`), which excludes `Hidden` after the command has
  run `ApplyVisibility`. Golden: `internal/cli/command/testdata/list.json.golden`.
- `internal/cli/command/inspect.go` — 26 fields, its own golden
  (`inspect.json.golden`). Out of scope.
- `internal/core/docs/llmstxt/generator.go:11-15` — `CommandSummary{ID,
  Description}`, consumed by `writeCommandsSection` (`:190-202`), filled by
  `collectCommandSummaries` (`internal/cli/docs/llmstxt_collectors.go:65-78`).
  `core/docs` + `core/docs/llmstxt` currently depend on **no** other internal
  package; the flat mirror types are what keeps that true.

**The pack side**

- `packcommon.TemplateData` (`packcommon.go:96-104`) — `Project`, `Service`,
  `Resolved`, `ServiceCfg`, `Runtime`, `Services`, `Cfg`. No commands. Filter
  accessors are methods on the struct (`AppServices` `:107`, `ToolServices`,
  `InfraServices`) — the pattern the new accessors follow.
- Six construction sites, three render + three dry-run validators:
  `internal/cli/render/ai.go:161` (inside `renderAgentsForService` `:114`, called
  from the loop at `:100`), `internal/cli/render/ide.go:169`
  (`renderIDEConfigs` `:161`, loop `:100`),
  `internal/core/execution/templates/git/git.go:367` (built from its own `ctx`
  struct, driven by `internal/cli/render/git.go:103`),
  `internal/core/validate/templates/ai.go:180` (`validateService` `:98`, loop
  `:76`), `.../ide.go:177`, `.../git.go:186`.
- `validate.Context` already carries `CommandRegistry any` (nil-tolerant,
  `internal/core/validate/validate.go:36`), populated once per run at
  `internal/cli/validate/validate.go:524`. The three templates validators need no
  load of their own.

**The group tree contains nodes nobody declared**

`Registry.ensureGroup` (`internal/core/usercommands/registry/registry.go:161-177`)
creates **every ancestor** of a group id, whether or not a `group:` header exists
for it. magento's command files live at `workspace/commands/services/magento.yml`
and `.../services/magento/*.yml` with no `services.yml`, so the tree holds a
`services` node with a zero `GroupMeta` and no commands of its own. It is not an
authored group: it has no title and no description. Any group projection must
drop such synthetic nodes, or the collapse rule below picks `services` (empty
description, and on another project it would also pull in sibling hubs' commands)
instead of `services.magento`, whose header does carry
`description: Commands for the Magento application service`.

`Registry.list` (`registry.go:227-253`) already matches a prefix on dot
boundaries (`id == prefix || strings.HasPrefix(id, prefix+".")`) and drops
`Private` and `BridgeHidden` unconditionally — so `admin` cannot swallow
`administration`, and that rule does not need re-deriving anywhere else.

**The two shipped templates**

- `internal/core/workflow/scaffold/templates/AGENTS.md.tmpl` — root file, `[[ ]]`
  delimiters, scaffold-only data (`.Name`); "Useful commands" opens with the
  passive `dwe commands list` bullet, and the "Data as JSON" convention lists
  `dwe commands list` among the commands to call with `-o json`.
- `internal/core/workflow/scaffold/templates/workspace/templates/ai/default/AGENTS.md.tmpl.tmpl`
  — the hub pack template (`{{ }}` over `TemplateData`); "Working on this service"
  opens with the same passive bullet.
- Both are pinned inside `internal/core/workflow/scaffold/testdata/golden_default.txt`
  by *rendered content* (root at `:87`, pack template at `:534`), so either edit
  regenerates that golden.

**The service join (the trap this plan must not step in)**

- A command's `service:` is a **compose service / container name**, not a
  `cfg.Services` map key. `ServiceConfig.Container` defaults to the map key when
  absent (`internal/core/project/config/workspace.go:2393-2394`), and lookups go
  through `config.ServiceByContainer` (`:1480-1490`). On magento the key is
  `magento`, `service.yml` declares `container: app-magento`, and all 49
  container commands say `service: app-magento` — a filter comparing against
  `TemplateData.Resolved` (the map key) would drop the entire hub's registry.
- `service:` may itself be a template expression: it is rendered as a
  command-template at run time
  (`internal/core/usercommands/runtime/runners/service/exec.go:133-145`), and the
  contract explicitly allows `service: app-${param.service}` for generic
  per-service commands. At pack-render time there are no params, so such a
  command resolves to nothing and cannot land in any scoped slice. This is one of
  two reasons the rule must end with an unscoped fallback call.
- The second reason: a command with no `service:` at all can still be the
  canonical entry point for a hub — podlapka's `db` group declares `service:` on
  2 of its 10 commands, the rest being host-side `script`/`shell`; `admin.pull`
  and `admin.e2e` sit in the `admin` group with no service.

**Sizes measured on the real workspaces** (9 projects, `dwe commands list`)

| project | commands | full text listing | hub `AGENTS.md` today |
|---|---|---|---|
| dwe-meetup | 4 | 0.5 KB | — |
| cueBreaker | 10 | 0.9 KB | — |
| alto | 16 | 1.8 KB | — |
| beetDeck | 26 | 2.9 KB | — |
| AlbFetcharr | 27 | 3.0 KB | — |
| laravel | 43 | 6.0 KB | — |
| ficbird | 52 | 5.8 KB | 1.1–3.5 KB |
| magento | 63 | 9.9 KB | 1.6 KB |
| podlapka | 74 | 9.2 KB | 1.9–3.5 KB |

Scoped: `dwe commands list admin` on podlapka is 1415 bytes of text, 1067 bytes of
compact JSON (JSON is *smaller* than text today only because it omits the
descriptions this plan adds back; after Change A the two are comparable). The
planned block is ~700 bytes plus one line per group.

**Conventions that bind this work** (from `AGENTS.md` critical patterns)

- Display-string localization: never read `def.Description` in display code; use
  the typed `store.*` helpers via a threaded translator. Storage/hashing sites
  stay English.
- Section renderer signature contract: `internal/cli/` is the single writer to
  stdout/stderr; the new builder returns data and never prints.
- `info.yml` auto-blocks / renderers must not `range cfg.Services` — iteration
  order must be deterministic.
- JSON output mode: gate any `warning:` write behind `flags.Output != "json"` —
  **does not apply to the three pack renderers**, which have no JSON mode at all
  (`internal/cli/render/env.go:65` is the only `flags.Output` read under
  `internal/cli/render/`, and ai/ide/git already write their per-file success
  lines to `render.Stdout()`). See Technical Details > Index loading.

## Development Approach

- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change (`make embedded-docs` once, then focused
  `go test ./internal/...`; `make test` before each commit)
- maintain backward compatibility: no command file, `service.yml` or
  `workspace.yml` needs to change; no existing JSON key changes meaning
- follow the `AGENTS.md` critical patterns listed under Context above

## Testing Strategy

- **unit tests**: required for every task; table-driven where the input space is a
  list of shapes (summary construction, group counting, hub scoping, collapse
  rules, the one shared render failure policy)
- **golden tests**: `list.json.golden` and `golden_default.txt` are regenerated,
  never hand-edited
- **no e2e UI tests** in this project; live verification on real projects is in
  Post-Completion
- byte-stability is a test, not a hope: the same project rendered under two
  locales must produce identical bytes

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

1. **`commandEntryJSON` gains `description` and `service`** — `description`
   through `translator.CommandDescription(locale, id, def.Description)` exactly as
   the text branch resolves it; `service` through `def.EffectiveService()`, both
   `omitempty`. Nothing else in the DTO moves.
2. **`model.CommandSummary` and `model.CommandGroupSummary`** land in
   `usercommands/model` — a leaf package `packcommon` can import without a cycle.
3. **`usercommands.CommandIndex(reg, tr, locale)`** returns both slices from one
   walk: commands from `reg.List("")` (non-private; hidden excluded only when the
   caller already ran `ApplyVisibility`), groups from the group tree with a
   declared count per group prefix.
4. **`cli/docs` maps the shared index into the existing `llmstxt.CommandSummary`**
   — its hand-rolled walk (`collectCommandSummaries`) goes away, the llms-txt
   types and output do not change, and `core/docs` keeps its zero internal
   dependencies.
5. **`TemplateData` gains `Commands`, `CommandGroups` and two accessors** —
   `ServiceCommands` and `ServiceCommandGroups` — both joining on
   `ServiceCfg.Container`, never on `Resolved`. (`{{ if .Commands }}` covers the
   "any commands at all" case; no accessor for it.)
6. **One index per invocation.** Each of the three render entry points builds the
   index once, before its per-service loop, and threads it into every
   `TemplateData`. The three dry-run validators build it from
   `ctx.CommandRegistry`, which is already loaded once per validate run.
7. **One failure policy for all three render commands: warn once, render on.**
   A registry load error produces a single `w.Warning` naming the file and the
   real consequence (the command index is unavailable; `Commands` and
   `CommandGroups` render empty), and rendering continues.

   Not a hard fail, for two reasons — and **not** for the deploy-pipeline reason
   that an earlier draft gave. That reason was wrong and is worth recording so it
   is not reinvented: `runDeploy` loads the registry at
   `internal/cli/deploy/deploy.go:508`, holds the error across preflight and
   returns it at `:539` — before the locks, before `ResolvePlan`, before any
   step. A project with an unparseable `workspace/commands/*.yml` therefore never
   reaches a pipeline-nested `render ai` at all, whatever this policy says. The
   actual reasons are: (a) `dwe render ide|git` must not fail over a domain their
   shipped templates never read, and one policy across three sibling commands is
   cheaper to keep honest than three; (b) a direct `dwe render ai` on a
   half-broken project is more useful finishing with a warning than refusing —
   `dwe validate` is where that error is reported properly.

   The requirement that a generated agent contract must not *silently* claim zero
   commands is met by the warning, not by an exit code. The three dry-run
   validators stay silent: the `commands` domain already reports the real failure
   and `templates` must not double-report it.
8. **The hub template gains a `## Declared commands` block**, entirely under
   `{{ if .ServiceCommandGroups }}` so a fresh project renders no block at all.
9. **The root template gains the same rule as prose**, and its "Useful commands"
   opening bullet is rewritten to match.

## Technical Details

### Types (`internal/core/usercommands/model/summary.go`)

```go
// CommandSummary is the shared agent-facing projection of a command. It carries
// DECLARED state: `hide:` is not evaluated here — see CommandIndex.
type CommandSummary struct {
	ID          string
	Group       string
	Description string
	Type        string
	Service     string // EffectiveService(): compose service / container name, may be an unrendered ${...} expression
}

// CommandGroupSummary is one AUTHORED group node with its declared command
// count. Synthetic ancestors created by ensureGroup are not represented.
type CommandGroupSummary struct {
	ID          string
	Title       string
	Description string
	Count       int // len(reg.List(ID)) — see CommandIndex
}
```

`Service` is `EffectiveService()` (`model/types.go:1033-1038`) — `Runner.Service`
when the runner overrides, else the top-level field. `Type` is kept because it is
the one field that tells an agent whether the command enters a container at all
(`service_exec` vs `script`/`shell`), and the human listing already shows it;
everything else a future consumer might want is one call away on the `CommandDef`.

### The builder (`internal/core/usercommands/`)

```go
func CommandIndex(reg *Registry, tr i18n.Translator, locale string) ([]model.CommandSummary, []model.CommandGroupSummary)
```

- Commands come from `reg.List("")` — non-private, and hidden-filtered **only if**
  the caller already ran `ApplyVisibility`. Sorted by ID (as `List` returns).
- Descriptions through `tr.CommandDescription(locale, id, fallback)`; group title
  and description through `tr.GroupTitle` / `tr.GroupDescription`
  (`internal/shared/i18n/translator.go:8,20,24`).
- Groups come from walking `reg.Groups()` (`registry.go:255`, nodes at `:14-34`).
  Three nodes are skipped: the root (`ID == ""`), any node whose `Count` is 0, and
  any node whose **raw `gn.Meta.Title` and `gn.Meta.Description` are both empty**
  *and* which has no commands directly attached (`GroupNode.Commands` empty). The
  predicate reads the **raw meta fields**, never the resolved `Title` below: that
  one falls back to `gn.Name`, which `ensureGroup` sets for every node, so a skip
  keyed on it would never fire and magento's `services` would survive. Such a node
  carries nothing a template line can print. magento's `services` is the live case
  — `ensureGroup`
  (`registry.go:161-177`) materializes every dotted ancestor, and there is no
  `services.yml` to give it meta. The test is on `Title`/`Description`
  specifically, **not** on a zero `GroupMeta`: a header-only file declaring just
  `group: {bridge: …}` or `group: {hide: …}` over a subtree loads fine
  (`CommandFile.Validate`, `model/types.go:1632-1647`, only iterates
  `f.Commands`), and such a node has non-zero meta, no direct commands and no
  printable text — under a zero-meta test it would survive, become the shallowest
  qualifying group, swallow sibling hubs and render with an empty description.
  That is the same failure by another door.
- `Count` is `len(reg.List(group.ID))` — the size of that listing **in the state
  of the registry passed in**, which inherits `list`'s `Private` and
  `BridgeHidden` filters for free. On the pack path (no `ApplyVisibility`) that is
  the declared count, which is not the same number a live `dwe commands list <id>`
  prints when a `hide:` is active — see the visibility contract below. It is
  **not** derived by walking `GroupNode.Commands`, which includes private commands
  (`registry.go:25-26`).
- The **emitted** `Title` is `tr.GroupTitle(locale, id, gn.Name)` — the fallback is
  the node's **`Name`** (last id segment), exactly as the text listing resolves it
  (`list.go:422`), not `gn.Meta.Title`. (Only the emitted value; the skip
  predicate above stays on the raw meta fields.) This matters: a command file with no
  `group:` header at all produces a node with empty meta but real commands, which
  the builder keeps; with a `Meta.Title` fallback it would carry empty title and
  description, could still collapse a described descendant, and the template would
  then print no line for it at all.
- Deterministic order: groups sorted by ID; no map iteration reaches the output.
- Pure data, no writer, no error return — a nil registry yields two nil slices.

**The visibility contract, stated once and tested:** `ApplyVisibility`
(`registry/visibility.go:41`) evaluates `hide:` expressions at run time,
including `cmd:` / `builtin:` predicates that shell out, and its result depends on
the state of the stack. `llms-txt` calls it (`cli/docs/llmstxt.go:94-99`) and must
keep calling it — it is a live per-invocation document. The pack render path must
**not**: baking a runtime-conditional set into a file on disk makes `AGENTS.md`
flip between renders as the stack goes up and down. The consequence is explicit
and is not a defect to be "fixed": a group with an active `hide:` prints its
**declared** count in the file while the live `dwe commands list <group>` returns
fewer, or none. The template therefore labels the number `N declared`, never as a
promise about the size of the call's output — its job is to show that the declared
area is non-empty.

### `TemplateData` additions (`packcommon.go`)

```go
Commands      []model.CommandSummary
CommandGroups []model.CommandGroupSummary
```

Accessors, mirroring the existing `AppServices` shape:

- `ServiceCommands()` — commands whose `Service` equals `d.ServiceCfg.Container`.
  Never `d.Resolved`: `Container` defaults to the map key
  (`workspace.go:2393-2394`), so the comparison is correct for both the
  key == container and the key ≠ container project (magento: key `magento`,
  container `app-magento`).
- `ServiceCommandGroups()` — the groups that own this hub: a group qualifies when
  at least one command under its prefix belongs to `ServiceCommands()`. Qualifying
  groups are then **collapsed to the shallowest**: any group whose ID is a
  dot-prefixed descendant of another qualifying group is dropped, because
  `dwe commands list <shallow>` already covers it. Because the builder never emits
  synthetic ancestors, the collapse can only land on an authored group: podlapka's
  `admin` + `admin.lint` collapse to `admin` (13 declared); magento's seventeen
  `services.magento.*` collapse to `services.magento` (55 declared) and **not** to
  the meta-less `services` node above it.

A hub with no qualifying group (every command declared without a `service:`, or
only through a templated one) yields an empty slice and the whole block
disappears — the unscoped fallback in the rule is what covers that project.

### Index loading

- `cli/render/ai.go` — build once before the loop at `:100`; pass into
  `renderAgentsForService`.
- `cli/render/ide.go` — build once before the loop at `:100`; pass into
  `renderIDEConfigs`.
- `cli/render/git.go` — `git.Context` (`templates/git/git.go:308`) gains the two
  slices. The index is built **before the per-service loop at
  `internal/cli/render/git.go:84`**
  and threaded through `renderGitHooksForService` (`:103`) into the `Context`
  literal at `:160`. Building it at `:160` would load the registry once per
  service and fire N warnings — the same trap as the other two sites, one
  function deeper.
- All three: on `LoadRegistryFromConfigPath` error, exactly one `w.Warning`
  (`internal/shared/render/output.go:63`) per invocation — not per service — via a
  shared helper in `internal/cli/render/common.go`, which already houses
  `warnNoPack` / `warnSelectionSkips` (three copies of the same branch is the
  alternative). The text names the file and states the **actual** effect —
  *command index unavailable; `Commands` / `CommandGroups` render empty* — not
  "the block is omitted": ide/git templates have no block, and a project's own ai
  pack may not either.
  **Stream: stdout**, through the same `render.Stdout()` writer as every other
  line these commands emit (`warnNoPack` included). `render.Writer` has no stderr
  sibling, and splitting one warning off to stderr would put it out of order with
  the surrounding per-file lines. Pinned by a test so the choice is not
  re-litigated by accident. **No `flags.Output` gate**: the pack renderers
  have no JSON mode (only `cli/render/env.go:65` reads `flags.Output`), and their
  per-file success lines already go to `render.Stdout()`, so a gate here would be
  a convention transplanted from commands that do not exist.
- `validate/templates/{ai,ide,git}.go` — build from `ctx.CommandRegistry` (already
  loaded once per run at `cli/validate/validate.go:524`); nil registry → empty
  index, no diagnostic.
- Packs always call with `i18n.NopTranslator{}`
  (`internal/shared/i18n/translator.go:48`) and an empty locale, never
  `rflags.I18n`. (`i18n.TranslatorOrNop` takes a `*Store` — `translator.go:98`;
  `TranslatorOrNop(nil)` is equivalent.) `AGENTS.md` is a file on disk read by an
  agent; it must be byte-identical regardless of the active locale, the same
  reason `journal/hash.go` stays English.

### The hub template block

Rendered shape (podlapka's `admin` hub):

```markdown
## Declared commands

- **admin** — Developer tasks for the admin SPA (runs in the admin container) —
  13 declared: `dwe commands list admin --output json`

Before any task that falls under a group description above — tests, linters,
formatters, builds, codegen, migrations, seeds, cache or token management,
package-manager scripts — make that call once per session, and again after
`workspace/commands/` changes. If a declared command matches the intent, run it
with `dwe cmd <id>`; inspect an unfamiliar one with
`dwe cmd -i <id> --output json`. A scoped listing is a fast first lookup, not
proof of absence: before falling back, run one full
`dwe commands list --output json` — project-wide and workflow commands are
declared without a service and will not appear above. Only when that finds no
match, use `dwe shell admin -c '<cmd>'`. Do not invoke the underlying
npm/composer/make/docker command directly until that check has been made.
```

The `dwe shell` target is `{{ .Resolved }}` (the service key, as the existing
template already uses at `AGENTS.md.tmpl.tmpl:23`), **not** the group id — they
coincide on podlapka's `admin` but not on magento, where the group is
`services.magento` and the shell target is `magento`.

**The group line's text falls back: description → title → the line is omitted.**
A `description:` on a `group:` header is optional and description-less authored
groups are normal (magento's `services/magento/db.yml` has no `group:` header at
all), so the template must not emit `- **id** —  — N declared`. `Title` is in
`CommandGroupSummary` precisely to serve as that fallback. A group with neither
prints nothing — and cannot reach the line anyway, since the builder drops it.

Four properties are load-bearing and none may be trimmed to save bytes:

1. **The trigger is a category, not a verb list** — "any task that falls under a
   group description above", with the verbs as examples. A closed verb list is
   always incomplete (podlapka alone has token minting, content import and cache
   management outside it) and an agent reads the omission as permission.
2. **The priority** — a declared command outranks the direct invocation.
3. **The negative boundary** — do not run npm/composer/make/docker until checked.
4. **The bounded cost** — `once per session`, plus the one invalidation event.
   Without it the rule reads as a tax on every run and gets rationalized away.

The block's group line is the only generated part; everything else is static
template text. `group.description` supplies the intent wording — authored by the
project, not derived by us.

### The root template

Same rule, unscoped (`dwe commands list --output json`), no data. The "Useful
commands" list keeps its shape; its first bullet is rewritten from a capability
mention into the rule's short form with a pointer to the same two follow-ups
(`dwe cmd <id>`, `dwe cmd -i <id> --output json`). The "Data as JSON" bullet stays
as-is — after Change A it is finally honest about `dwe commands list`.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): tasks achievable within this
  codebase — code changes, tests, documentation updates
- **Post-Completion** (no checkboxes): items requiring external action — live
  verification on real projects, skill copy sync, consuming-project updates

## Implementation Steps

### Task 1: `commands list --output json` gains `description` and `service`

**Files:**
- Modify: `internal/cli/command/list.go` (`commandEntryJSON` :29-36, `commandDefToEntryJSON` :97)
- Modify: `internal/cli/command/command_json_test.go` (:62, :76, :96, :109, :137, :363-391 — this is where the DTO tests and the golden comparison live, not `list_test.go`)
- Modify: `internal/cli/command/testdata/list.json.golden` (regenerated with `UPDATE_GOLDEN=1`; the scaffold golden in Task 5 uses a `-update` **flag** instead — do not mix them up)

- [x] add `Description string \`json:"description,omitempty"\`` and `Service string \`json:"service,omitempty"\`` to `commandEntryJSON`, placed after `Title` and `Type` respectively; resolve description through the translator exactly as the text branch does (`list.go:436`), service through `def.EffectiveService()`
- [x] regenerate `list.json.golden` with `UPDATE_GOLDEN=1` (do not hand-edit)
- [x] write a test asserting the existing keys are unchanged in name and order for a fixture command (a positive pin, not an eyeball "confirm")
- [x] write table-driven tests: a command with a description emits it; a command without emits no key; `service:` present; `service:` absent → no key; `runner: {service: x}` wins over the top-level field; a templated `service: app-${param.svc}` is emitted verbatim (unrendered) rather than dropped or expanded
- [x] write a test that the localized description reaches the JSON branch (translator stub returning a marker) — the JSON branch must not fall back to the raw `def.Description` while the text branch localizes
- [x] run `go test ./internal/cli/command/...` - must pass before task 2

### Task 2: shared summary types and the `CommandIndex` builder

**Files:**
- Create: `internal/core/usercommands/model/summary.go`
- Create: `internal/core/usercommands/model/summary_test.go`
- Modify: `internal/core/usercommands/usercommands.go` (add `CommandIndex` beside `LoadRegistry` :155-158)
- Create: `internal/core/usercommands/index_test.go`

- [x] define `CommandSummary` and `CommandGroupSummary` per Technical Details, with the doc comment stating the declared-not-runtime contract on both types
- [x] implement `CommandIndex(reg, tr, locale)`: commands from `reg.List("")`, groups from the walked tree with `Count = len(reg.List(id))` and emitted `Title = tr.GroupTitle(locale, id, gn.Name)`; skip the root, zero-count groups, and text-less ancestors — the skip predicate is **raw `gn.Meta.Title == "" && gn.Meta.Description == "" && len(gn.Commands) == 0`**, NOT a zero-`GroupMeta` test and NOT the resolved `Title` (which is never empty, since `gn.Name` is set on every node — a skip keyed on it is dead code and magento's `services` would survive); deterministic order, nil registry → two nils
- [x] write table-driven tests for the command slice: private excluded; `Service` via `runner:` override; `Service` empty when undeclared; description and group strings taken from the translator (stub asserts it was consulted); order sorted by ID
- [x] write table-driven tests for the group slice: a group with 2 public + 1 private command reports `Count` 2 — a `GroupNode.Commands` walk would report 3, so this case is what pins the derivation to `reg.List`; a group with 8 direct + 5 in one subgroup reports 13; a group holding only private commands is omitted entirely; nested group ids keep their full dotted form; group description comes from the translator, and `Title` falls back to the last id segment for a file with no `group:` header (assert it is the segment, not empty)
- [x] write **the synthetic-ancestor test** on magento's real shape: command files at `services/magento.yml` and `services/magento/<n>.yml` with NO `services.yml` — the emitted groups contain `services.magento` (with its description) and the subgroups, and contain **no** `services` entry, even though `ensureGroup` created that node
- [x] write the **header-only ancestor test** (the hole a zero-meta test would leave open): the same tree plus a `services.yml` containing only `group: {bridge: {enabled: true}}` — the `services` node now has non-zero meta but still no title, no description and no direct commands, and must still be absent from the emitted groups
- [x] write **the divergence test**: a registry where one group's commands carry a constant-true `hide: '{{ true }}'` (the `evalHideFn` stub seam is unexported in `package registry` — `visibility_test.go:15-27` — and is not reachable from `package usercommands`, so use a template that needs no stack rather than moving the test); `CommandIndex` WITHOUT `ApplyVisibility` reports the full declared count, and the same builder AFTER `ApplyVisibility` reports fewer — with a comment in the test naming this as the intended contract and stating that "fixing" it by calling `ApplyVisibility` inside the builder would make a generated file depend on Docker state
- [x] run `go test ./internal/core/usercommands/...` - must pass before task 3

### Task 3: `llms-txt` reuses the shared walk (no type crosses the docs boundary)

**Files:**
- Modify: `internal/cli/docs/llmstxt_collectors.go` (`collectCommandSummaries` :65-78 becomes a mapper over `usercommands.CommandIndex`)
- Modify: `internal/cli/docs/llmstxt_collectors_test.go`

- [x] `collectCommandSummaries` calls `usercommands.CommandIndex(reg, tr, locale)` and maps `ID`/`Description` into the existing `llmstxt.CommandSummary`; the group slice is discarded (llms-txt has no group section)
- [x] `internal/core/docs/**` is NOT touched: no new import, no type change, no output change — assert with `go list -deps ./internal/core/docs/...` showing no internal package **outside `internal/core/docs/...`** (today the set is exactly `core/docs`, `core/docs/export`, `core/docs/llmstxt`, `core/docs/mermaid`, `core/docs/render`)
- [x] keep the locale as the already-resolved `i18n.ResolveLocale(...)` value at `llmstxt.go:68`; do not "simplify" it to `rflags.Locale` (wrong namespace for this surface)
- [x] write a test pinning the Commands section output for a fixture registry, byte-identical to `release/0.6.2`
- [x] run `go test ./internal/core/docs/... ./internal/cli/docs/...` - must pass before task 4

### Task 4a: `TemplateData` carries the index; the two accessors

**Files:**
- Modify: `internal/core/execution/templates/packcommon/packcommon.go` (`TemplateData` :96-104, accessors beside `AppServices` :107)
- Modify: `internal/core/execution/templates/packcommon/packcommon_test.go`

- [x] add `Commands` / `CommandGroups` fields and the two accessors; `ServiceCommands` joins on `ServiceCfg.Container` with a comment naming the magento key≠container case and pointing at `config.ServiceByContainer`
- [x] implement `ServiceCommandGroups` with the shallowest-group collapse over the builder's authored-only group set (descendant dropped when an ancestor qualifies; dot-boundary prefix match, the same rule `registry.list` already applies at `:242-243`)
- [x] write table-driven tests for `ServiceCommands`: key == container; key ≠ container (magento shape — the whole hub must be found; podlapka's `map` / `map-edit` is a second real case); a command with an unrendered `${param.*}` service is excluded without error; a command with no service is excluded; `extends` chain where `Resolved` differs from `Service`
- [x] write table-driven tests for `ServiceCommandGroups`: two-level collapse (`admin` + `admin.lint` → `admin`); **the magento shape with the synthetic ancestor present in the registry** (`services` + `services.magento` + 17 leaves → exactly `services.magento`, with a non-empty description); two independent groups both kept; dot-boundary non-collision (`admin` vs `administration`); hub with zero qualifying groups → empty slice
- [x] run `go test ./internal/core/execution/templates/...` - must pass before task 4b

### Task 4b: six sites thread the index; one warn-once failure policy

**Files:**
- Modify: `internal/cli/render/ai.go` (loop :100, `renderAgentsForService` :114, data :161)
- Modify: `internal/cli/render/ide.go` (loop :100, `renderIDEConfigs` :161, data :169)
- Modify: `internal/cli/render/git.go` (build before the per-service loop at :84, new parameter on `renderGitHooksForService` :103, `git.Context` construction :160) and `internal/core/execution/templates/git/git.go` (`Context` :308, data :367)
- Modify: `internal/core/validate/templates/{ai.go:180,ide.go:177,git.go:186}`
- Modify: `internal/cli/render/common.go` (the shared warn-once helper, beside `warnNoPack` :64)
- Modify: `internal/cli/render/{ai,ide,git}_test.go`, `internal/cli/render/common_test.go`, `internal/core/validate/templates/*_test.go`
- Create: `internal/core/execution/templates/packcommon/templatedata_sites_test.go` (drift test)

- [x] thread the index through all six construction sites, built once per invocation / per validate run; validators read `ctx.CommandRegistry` and build nothing of their own
- [x] implement the failure policy through one helper in `common.go`: exactly one `w.Warning` per invocation naming the file and saying the command index is unavailable, then render with an empty index; no `flags.Output` gate; validators stay silent
- [x] write a `go/ast` drift test over every `TemplateData{...}` composite literal in non-test files, asserting each sets `Commands` — precedent to copy: `internal/core/execution/condition/condition_test.go` (`parser.ParseFile`), plus the repo-walking helpers in `templates/ide/source_regression_test.go:146` and `cli/docs/agentsmd_test.go:39`. It lives in `packcommon` but must walk `internal/cli/render` and `internal/core/validate/templates` too. State its one blind spot in a comment: a future site that declares `var d TemplateData` and assigns fields afterwards is not a composite literal and will not be caught
- [x] write the failure-policy tests: on a project with an unparseable command file each of the three render commands still succeeds, warns exactly **once** (not once per service — assert against a two-service fixture), names the file, writes that warning to **stdout**, and renders with `Commands` / `CommandGroups` empty; the templates validators emit no diagnostic for the same project. The "no `## Declared commands` heading" assertion belongs to Task 5, not here — the shipped template does not carry that heading yet, so asserting its absence now is vacuous and would stay green even if a non-empty index were passed
- [x] run `go test ./internal/cli/render/... ./internal/core/validate/templates/...` - must pass before task 5

### Task 5: the shipped templates push the rule

**Files:**
- Modify: `internal/core/workflow/scaffold/templates/workspace/templates/ai/default/AGENTS.md.tmpl.tmpl`
- Modify: `internal/core/workflow/scaffold/templates/AGENTS.md.tmpl`
- Modify: `internal/core/workflow/scaffold/testdata/golden_default.txt` (regenerated via the `-update` flag, `scaffold_test.go:70`)
- Modify: `internal/core/workflow/scaffold/scaffold_test.go` (`goldenCompare` :67, its `*update` read :70, the call site :104)
- Modify: `internal/core/workflow/scaffold/starter_artefacts_test.go` (:245-257 asserts substrings of the rendered hub file)
- Modify: `internal/cli/render/ai_test.go`

- [x] add the `## Declared commands` block to the hub template per Technical Details, entirely under `{{ if .ServiceCommandGroups }}`, with the four load-bearing properties intact, the count labelled `N declared` and the shell fallback using `{{ .Resolved }}`
- [x] rewrite the root template's first "Useful commands" bullet into the generic form of the rule; leave the "Data as JSON" bullet unchanged
- [x] regenerate `golden_default.txt` (`-update` flag, not `UPDATE_GOLDEN`)
- [x] write a render test on a fixture project with two hubs: each hub's block names only its own groups, the counts match `CommandIndex`, and the exact scoped call string is present verbatim
- [x] write the byte-stability test **through the render command's RunE** with `rflags.I18n` set to a Russian-populated `*i18n.Store` (the field is `*Store`, not the interface), and assert the bytes equal the run with no store. Asserting it at the `packcommon`/`CommandIndex` level is vacuous — the nop translator is hard-coded inside that call, so there is no injection point below RunE. It is also NOT about `$LANG`: the render path never reads it
- [x] write a test that a project with zero declared commands renders no `## Declared commands` heading at all, and the same assertion for the broken-registry project from Task 4b (the index is empty there for a different reason, and this is where the heading exists to be absent)
- [x] write the group-line fallback tests: a group with a description prints it; a group with only a title prints the title; neither case ever renders a line with an empty text segment (`- **id** —  — N declared`)
- [x] run `make test` - must pass before task 6

### Task 6: documentation and CHANGELOG

**Files:**
- Modify: `docs/reference/render/ai.md` + `docs/i18n/ru/reference/render/ai.md`
- Modify: `docs/reference/config/commands/index.md` + `docs/i18n/ru/reference/config/commands/index.md` (no page documents the list JSON fields today; the repo keeps no per-command markdown — the CLI surface is `dwe --help` — so this gets a short subsection, not a new page)
- Modify: `docs/internals/packages.md` (§ usercommands, § templates)
- Modify: `skills/dwe/SKILL.md` (:132 teaches `dwe commands list | grep <service>` — the workaround Change A removes)
- Modify: `skills/dwe/references/recipes.md` (:43 `grep -E '<service>\.(test|lint|check)'`)
- Modify: `skills/dwe/references/authoring-commands.md` (:24 `commands list --all --output json`)
- Modify: `CHANGELOG.md`

- [x] `render/ai.md`: document `TemplateData.Commands` / `CommandGroups` / the three accessors, the declared-not-runtime contract, and publish the shipped block as a **copyable snippet** for existing projects (their packs are project-owned and receive nothing automatically)
- [x] `commands/index.md`: the two new JSON fields, with the note that `service` may be an unrendered template expression and that `description` is localized (so a tool parsing it must not treat it as a stable key)
- [x] `packages.md`: the builder's single-source role, the synthetic-ancestor skip, the `Container` join, the shallowest-group collapse, the `ApplyVisibility` split (llms-txt yes / packs no) and the warn-once render policy
- [x] `skills/dwe`: replace the `commands list | grep` workarounds with the now-sufficient `dwe commands list --output json` (the skill is generic and has no `<group>` to substitute — the scoped form belongs only where a hub file already named the group), and teach the same priority-and-fallback rule the templates now carry (the skill is the agent-facing surface this work exists for — editing it is implementation, not post-completion sync)
- [x] `CHANGELOG.md` `## [Unreleased]`: `### Added` (shared index + template block), `### Changed` (the two JSON fields)
- [x] refresh the RU translation hashes for every touched page
- [x] `make build` (embedded docs must not go stale), then `make test`
- [x] run `make lint` - must pass before task 7

### Task 7: verify acceptance criteria

- [x] `dwe commands list --output json` on a project carries `description` and `service`; `--pretty` unchanged in shape; `inspect` output byte-identical to before
- [x] `dwe render ai` on a scaffolded fixture project with commands writes the block; on one without commands writes no block
- [x] all three `dwe render` pack commands on a project with a broken command file succeed with exactly one warning naming the file, and the hub file comes out without the block
- [x] `dwe validate` on that project reports the failure once, in the `commands` domain only
- [x] `dwe docs llms-txt` output on a fixture is byte-identical to `release/0.6.2`
- [x] `make test`, `make lint` clean

### Task 8: [Final] Update documentation

- [ ] re-read the CHANGELOG entries and `docs/reference/render/ai.md` together for consistent wording on the declared-vs-live distinction — the file says "declared", the docs say why, and neither promises the count matches a live listing
- [ ] `AGENTS.md`: add at most a one-line pointer if the `Container` join or the visibility split proves to be a trap during implementation; the write-up goes to `packages.md` (the file stays under `agentsMdBudget` — `TestAgentsMdBudget` pins it)
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Live verification** (binary from the branch, real projects on this machine):

- podlapka (74 commands, 5 packs, 4 hubs): paste the block into one pack by hand,
  `dwe render ai`, confirm the `admin` hub names `admin` with 13 declared and the
  exact call, and that `dwe commands list admin --output json` returns 13 entries
  that now carry `description` and `service`.
- magento (key `magento`, `container: app-magento`): the block must name
  **two** groups — `services.magento` (55 declared, "Commands for the Magento
  application service") and `app` (`app.yml:52` also declares
  `service: app-magento`, and `app` is not nested under `services.magento`, so the
  second line is correct, not a collapse regression). This project proves two
  things at once — the `Container` join (a regression shows up as an empty block)
  and the synthetic-ancestor skip (a regression shows up as a `services` line with
  no description). Note the empty-description signature is shared with the
  title-only fallback case, so read the group **id** to tell them apart.
- ficbird: `dwe render ai` across its three packs; diff review that nothing else
  in the hub files moved.
- All nine local workspaces: `dwe commands list --output json` before/after,
  confirming the schema change is purely additive and no existing key moved.
- The control agent run before/after on one project — the share of `dwe`
  invocations against direct `npm`/`make`/`docker` calls. This is the only
  measurement that answers the question the work was done for. If the rule does
  not move it, the fallback is to bake ids into the block; the data is already in
  `TemplateData`, so that is a template edit, not new plumbing.

**External updates**

- Sync the installed copy of `skills/dwe` after the branch merges — the loaded
  skill comes from `main`, so the in-repo edits from Task 6 do not reach the
  running agent until then.
- The twelve existing workspaces keep their current packs and receive nothing
  automatically. Retrofitting them with the block (from the snippet published in
  `docs/reference/render/ai.md`) is separate work, deliberately out of this plan.
