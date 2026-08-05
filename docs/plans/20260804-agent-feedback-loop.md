# Plan A — Close the feedback loop (`dwe validate` catches what actually breaks)

## Overview

Analysis of two real agent-driven workspace setup sessions (alto 2026-07-07, cueBreaker
2026-07-09) plus five existing workspaces produced one dominant finding: **the agent
learned the schema fine, but the tool never told it when the project was broken.**

- 3 of 3 defects that actually broke the project (`Bad substitution` in deploy, invalid
  compose project name, empty `dwe info`) passed **20 green `dwe validate` runs** and
  reached the user only as a runtime failure pasted back into the session.
- Meanwhile every run emitted 9–10 warnings, of which **0 were ever fixed** — 6 of them
  were `template pack not found` on a freshly scaffolded project. Signal drowned in noise,
  so the agent read validate output through `grep` and committed "validate clean" on a
  broken project three times.

This plan makes the static checks catch the real defect classes, removes the noise that
made them invisible, and fixes six localized bugs that actively mislead (a scaffold that
lies about its own semantics, a hint that recommends an invalid value, a hint naming a
file that does not exist).

Out of scope (later plans): Plan B — primitives (`service_exec` dynamic argv, `when:`/
`check:` duplication, `source_clone` builtin). Plan C — discoverability (`llms-txt` as the
real entry point, `docs search` tokenization, class-1 scaffold content, skill rules, cost
profile for `dwe test`). **Task 1–2 here (whitelist rendering) is a prerequisite for
Plan B's `source_clone`**, which is why it leads this plan.

## Context (from discovery)

- `internal/shared/tpl/render_command.go` — `varPattern` (line 109) is
  `\$\{([a-zA-Z_][a-zA-Z0-9_.]*)\}`; `CompileVarSyntax` (line 145) routes by head
  namespace (`files`, `host`, `param`, `context`, `snapshot`, `generated`, `args`) and
  **falls through to `{{ resolve .Raw %q }}` for everything else** (line 203). Unknown
  heads therefore render to `""`. `VarPattern` is already exported (line 115).
- `internal/core/execution/pipeline/executor.go:214` — `execShellAction` passes `a.Cmd`
  to `sh -c` **verbatim**, no template pass. This is the `Bad substitution` source.
- `internal/core/execution/pipeline/executor.go:282` (`execCommandAction`) →
  `usercommands.BuildRunContext` → `runtime/build_context.go:69` renders string `with:`
  values through `tpl.RenderCommand`. **This asymmetry is the whole bug**: `with:` renders,
  `cmd:` does not.
- `internal/core/workflow/envtest/render.go:51` — scenario `cmd:` **already** renders via
  `tpl.RenderCommand` with no unknown-head protection: an existing latent hole where
  `${HOME}` in a scenario silently collapses.
- `internal/core/execution/pipeline/step.go:107` — `StepCommand` `case "shell"` returns
  `strings.TrimSpace(step.Cmd)`; this is what `dwe deploy plan` prints, which is why the
  plan showed `git clone --branch ${vars.source.backend.branch}` as "what will run".
- `internal/core/execution/pipeline/resolve.go:290` — `resolveStepWhen` evaluates only
  `condition.TypeTemplate` via `tpl.EvalCondition`; a `when: {type: shell, cmd: …}` is a
  runtime condition whose `cmd` never sees the template engine.
- `internal/core/project/config/docker.go` — **two** resolution entry points:
  `ResolveComposeProjectName` (line 245, disk-reading) and `ComposeProjectName` (line 275,
  in-memory), both ending at `cfg.Project.FullName()` with no case normalization.
  `ComposeProjectNameCandidates` (line 298) derives a legacy second candidate from
  `FullName()` and drops it when equal to the primary.
- `internal/core/validate/config/` — validators are plain structs implementing
  `validate.Validator` (`ID()`, `Domain()`, `Run(ctx) []validate.Diagnostic`) registered in
  `all.go:8` `All()`. `compose_name.go` already parses the `ComposeFiles()` chain with a
  narrow yaml struct — reusable for the `container_name` check.
- `internal/core/project/config/info.go:251` — `LoadInfoConfig` returns
  `DefaultInfoConfig()` **only** on `os.ErrNotExist`; a present all-comment file yields an
  empty `InfoConfig`. No fallback at the call site either (`internal/cli/info/info.go:66`).
- `internal/core/validate/config/workspace.go:754` — the info validator emits
  `SeverityOK` when the file exists and parses, and `info: "no info.yml"` when absent —
  exactly inverted from reality.
- `internal/core/project/config/workspace.go:1215/1224` —
  `IDERenderEnabledExplicit() (enabled, explicit bool)`; `renderEnabledExplicit` defaults
  app→on, others→off. **The `explicit` bit already exists and is exactly what the noise
  fix needs.**
- `internal/core/validate/templates/{ai,git,ide}.go:128/135/128` — hints say
  `set services.%s.render.X.enabled: false in services.yml`; **no `services.yml` exists in
  DWE** (it is `workspace/services/<name>/service.yml`, or the `services:` block in
  `defaults.yml`).
- `internal/core/workflow/scaffold/templates/workspace/lifecycle.yml:14` — commented
  `run.update.mode`, removed from `LifecycleRunConfig`
  (`internal/core/project/config/workspace.go:2922`) when the policy moved to top-level
  `update:`. The loader is strict (`KnownFields(true)`) and the file invites
  "uncomment a section to override".
- `internal/cli/deploy/deploy.go` — `plan` is declared inline (no `plan.go`), has its own
  `--format table|shell`, `Args: cobra.NoArgs`, and never calls `cmdctx.WriteData`, so the
  global `--output json` is silently ignored.
- `internal/cli/validate/validate.go:266` — `newValidateConfigSubCmd(..., "services", ...)`
  is a **leaf running one validator**; `dwe validate config services` prints `ok:1`,
  indistinguishable from a full run (`dwe validate config` → `ok:10`).

**Field evidence for the whitelist decision — zero risk in workspaces, a stale contract in
this repository.** Across all five workspaces every `${UID}`/`${GID}` occurrence sits in a
YAML **comment**; there is not a single shell-style `${VAR}` in any executable field
(authors use the dwe form `${host.uid}`), and the one real config pack
(`beetDeck/…/config/backend/config.yaml.tmpl`) contains no `${…}` at all. **But inside this
repository the whitelist breaks a documented contract**: `docs/reference/render/config.md`
(lines 58–59, 69) states "**Top-level config** uses the bare dot-path
(`${databases.main}`)" with a working `DB_HOST=${databases.main.host}` example — mirrored
in `docs/i18n/ru/reference/render/config.md:60-61,71`,
`docs/reference/config/services/fields.md:596` (+RU:598), `docs/internals/packages.md:273`
(§ tpl, which literally describes the unconditional fallthrough), and the
`internal/cli/render/config.go:52` help text. That form has in fact been dead since the
strict root landed (`databases` is not in `allowedRootKeys`), so the docs already lie —
but nine test files still assert the old behaviour and must be migrated in Task 1:
`execution/templates/config/config_test.go:64`,
`execution/builtin/services/render_builtins_test.go:92`,
`workflow/lifecycle/run_test.go:675,836`, `usercommands/runtime/build_context_test.go:262`,
`execution/pipeline/executor_test.go:1345`, `usercommands/model/types_test.go:2639`,
`usercommands/loader/loader_test.go:453,779`,
`usercommands/runtime/files_resolve_test.go:1002`,
`shared/tpl/render_command_test.go:28`.

## Critical constraints for the executor (traps — read before every task)

1. **`make build` / `make test`, never bare `go build` / `go test ./...`.** The embedded
   docs tree (`internal/core/docs/embedded/`) is gitignored and generated; a bare `go test`
   sees an empty tree and the docs tests fail. For focused work: `make embedded-docs` once,
   then `go test ./internal/...` directly.
2. **`internal/cli/` is the single writer to stdout/stderr.** `internal/core/validate/`
   returns diagnostics; renderers return strings. Never print from core.
3. **`validate` is the documented exception to JSON-mode error handling** — it emits
   diagnostics-as-data (`{summary, diagnostics[]}`) even at severity=error, rather than the
   `{"error":{…}}` envelope. Do not "fix" this while touching validate.
4. **Unknown head → leave the `${…}` literal in place**, never render to `""`. This is the
   entire safety argument of Task 1; a regression here silently corrupts shell commands
   instead of loudly failing them.
4a. **Render at resolve time, never at exec time** — see Technical Details for the three
   independent reasons (journal hash, plan output, `with:` coverage). An exec-time render
   looks simpler and is wrong.
4b. **`${vars.*}` now reaches the text of a host `sh -c` program — treat this as ordinary
   substitution, not a boundary.** A var containing shell metacharacters will behave as
   shell syntax, exactly as it would if the author had typed the value inline. That is the
   expected semantics of a developer tool running on the developer's own machine; the
   whitelist in this plan is a **correctness** control (unknown heads must not collapse to
   `""`), never a security one. Do not add sanitizing, quoting or gating on this basis.
4c. **Render into a copy.** `With` and `Check` are reference types shared with the loaded
   config; in-place rendering makes `ProjectConfigHash` depend on deploy scope and makes a
   second resolve double-render. Deep-copy before rendering.
4d. **Only render strings containing a `${…}` with a KNOWN head — and only on the
   PIPELINE path.** A bare `VarPattern` match is not enough, because a shell-style
   `${CONTAINER}` would pull the string into the renderer and the untouched `{{ }}` beside
   it would then be executed against `RenderContext`. But the gate must live in the
   pipeline resolve helper, **never** inside `RenderCommand`: user commands document and
   rely on mixing `${…}` with raw `{{ }}` in one field (six examples across the reference
   docs), and changing `RenderCommand` would break them.
4e. **Rendered values now reach output surfaces.** After resolve-time rendering,
   `StepCommand` prints substituted values, so `dwe deploy plan`, `--format shell`, the
   Task 12 JSON payload, the pipeline log and `-v` tracing show real values where they show
   the literal today. This is the intended gain — a plan that prints what will actually run
   — and it is the developer's own machine and their own `local.yml`. Mention it in the
   docs; do not build masking.
5. **Scaffold template edits require golden updates** —
   `internal/core/workflow/scaffold/testdata/golden_default.txt`.
6. **Docs are mirrored**: an English page under `docs/reference/` usually has a Russian
   twin under `docs/i18n/ru/reference/`. Update both, then `make build` to resync the
   embedded copy and regenerate `internal/core/docs/content_hashes_gen.go`.
7. **Loader strictness is deliberate and asymmetric**: deploy/reset/lifecycle/command-file
   loaders use `KnownFields(true)`; info/styles/docker/localconfig use lenient
   `yaml.Unmarshal`. Task 9 must fix `info.yml` **without** switching it to a strict
   decoder.
8. **The four strict pipeline loaders already tolerate `io.EOF`** from an all-comment file
   so the built-in default applies — Task 9 is the same class of fix for `info.yml`, but
   `yaml.Unmarshal` (not a Decoder) returns no `io.EOF`; detect "no sections decoded"
   instead.
9. **Do not weaken any existing real diagnostic while removing noise** (Tasks 7–8). The
   rule is *implicit default + absent artifact → silent*; *explicit opt-in + absent
   artifact → warning*. Never silence the explicit case.

## Development Approach

- **testing approach**: **TDD for validators** (new/changed diagnostics: write the
  `testdata` fixture carrying the defect and the expected diagnostic first, then the
  validator), **regular for everything else** (rendering core, loaders, CLI output).
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change
- maintain backward compatibility

## Testing Strategy

- **unit tests**: required for every task (see Development Approach above)
- **e2e tests**: this project has no UI e2e suite. The closest analogue is
  `dwe test` (integration scenarios) — not exercised by this plan, but Task 15 must confirm
  `make test` stays green including the `envtest` package touched in Task 2.
- table-driven tests are the project norm for parsing/validation; follow it.
- validator tests go through the existing per-domain harness (see
  `runComposeNameValidator` in `internal/core/validate/config/compose_name_test.go` for the
  established shape).

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

Three mechanisms, applied in order:

1. **Make templates work where authors already expect them to** (Tasks 1–3). `${…}` with a
   *known* head renders in pipeline `cmd:` and shell-`when:` exactly as it already does in
   command `with:`; an *unknown* head stays literal instead of collapsing to `""`. The
   whitelist is the union of the config root keys (`allowedRootKeys`, which is what
   `resolve .Raw` can actually reach) and the special namespaces `CompileVarSyntax` already
   routes. A validator then catches the residual failure mode — a *known* head that
   resolves to nothing, i.e. a typo.
2. **Make the identity of the compose project correct by construction** (Tasks 4–6).
   Normalize the project name at both resolution points, fix the hint that recommended the
   invalid spelling, and add checks for the two remaining ways to get it wrong
   (`container_name` casing, declared-but-unexported ports).
3. **Restore the signal-to-noise ratio** (Tasks 7–8) with one rule — *diagnostics report
   "something is wrong", not "something is unused"* — implemented on the existing
   `explicit` tristate rather than new config.

Tasks 9–11 fix the localized bugs, Tasks 12–14 make the two commands an agent leans on
machine-readable and honest.

## Technical Details

**Whitelist composition** (Task 1). Known heads =
`allowedRootKeys` (`internal/core/project/config/workspace.go` — `project`, `runtime`,
`exports`, `compose`, `services`, `vars`, `update`, `bridge`, `state`, `schema_version`, …)
∪ `{files, host, param, context, snapshot, generated, args}` (the cases
`CompileVarSyntax` already switches on). Layering: `tpl` is a leaf under `internal/shared/`
and **must not import `internal/core/project/config`** — the key list is therefore passed
in or duplicated as an exported slice in `tpl` with a compile-time cross-check test in the
`config` package asserting the two stay in sync.

**Rendering happens at RESOLVE time, on the whole step** (Task 2). This is load-bearing and
was the single largest correction from plan review — an exec-time render in
`execShellAction` would have been wrong three ways:

1. **It breaks the journal contract.** `executor.go:551,731` compute
   `journal.StepHash(rs.Step)` over the **unrendered** `cmd:`, and `ProjectConfigHash`
   (`journal/hash.go:141`) hashes services + deploy configs — the `vars:` block is **not**
   in it. Changing `vars.source.branch` would leave the step hash unchanged, so the step
   would be skipped as up-to-date while the actual command differs. That is silent
   corruption of exactly the scenario this plan exists to fix.
2. **It leaves `dwe deploy plan` lying.** `StepCommand` (`step.go:107`) reads
   `config.DeployStep`, not the executed action, so a resolvable `${vars.x}` would still
   print raw — the motivating symptom from the Overview would survive, and Task 13 would
   flag correct references as suspicious.
3. **It does not cover `with:`** — and Plan B's `source_clone` takes
   `with: {repo, dir, branch}`. Four real workspaces already pass `${vars.*}` through
   `with:` (`beetDeck/…/frontend/deploy.yml:27-29`, `cueBreaker/…/{frontend,backend}/deploy.yml`).

So: render in `resolveLeafStep` / `ResolvePhaseSteps`, covering `cmd`, the string leaves of
`with`, `check`, **and `when.Cmd`** — mirroring `envtest/render.go:renderStep` in shape,
though **not** in mutation strategy (see below). One code path, no asymmetry between
`workspace/tests/*.yml` and `deploy.yml`, and Plan B's dependency holds as stated. Runtime
shell `when:` is evaluated in three places (`executor.go:509` phase, `:735` step, `:972`
parallel sub-step), all consuming the resolved step.

`when.Cmd` is in the list for two reasons: the Context section records it as a real defect
(all five workspaces write literal paths in gates, one documenting the limitation in a
comment), and **Plan B needs the symmetry** — its `check: auto` derives the check from
`when.Cmd`, so if one is rendered and the other is not, the derived check compares
different text than the gate it inverts.

**Render into a copy — never mutate the loaded config.** `config.DeployStep` is passed by
value, but `With map[string]any` and `Check *Action` are reference types pointing at memory
owned by `deployCfg`/`svcDeploys`. `envtest/renderStep` mutates in place, which is safe
there because it owns the scenario and renders once — copying that literally here is a bug:
`internal/cli/deploy/deploy.go:503` computes `serviceHashes` **before** resolve while `:557`
computes `projectHash` **after**, so in-place rendering would make the project hash depend
on which services were resolved (`--service` narrows the set). Deep-copy `With` and clone
`*Check` before rendering, and add a test that resolving the same `*DweConfig` twice yields
byte-identical results.

**Only render strings that contain a KNOWN head — not merely any `${…}`.**
`RenderCommand` (`render_command.go:267-269`) returns early only when the compiled string
has no `{{` left; otherwise it parses and executes. So a plain
`docker inspect -f '{{.State.Status}}'` would fail at resolve with
`can't evaluate field State in type *tpl.RenderContext` — an idiom that works today because
pipeline `cmd:` is not rendered at all.

A gate on `tpl.VarPattern.MatchString(s)` is **not sufficient**:
`docker inspect -f '{{.State.Status}}' ${CONTAINER}` matches the pattern via `${CONTAINER}`,
enters rendering, keeps `${CONTAINER}` literal per the whitelist — and then still explodes
on the untouched `{{.State.Status}}`. Gate instead on **"contains at least one `${…}` whose
head is in `KnownVarHeads`"**, so a command using only shell-style `${VAR}` plus Go-template
text is never rendered at all.

**The gate is PIPELINE-RESOLVE-ONLY. It must not change `RenderCommand` semantics.**
This is the single most dangerous way to implement this task wrong. User **commands**
legitimately mix both syntaxes in one field, and it is documented:
`docs/reference/templates.md:146` shows
`cmd: "mariadb -u${vars.db.user}{{ with .Params.database }} -D{{ . }}{{ end }}"`, and
`path: "${param.dump_dir}/${param.database}{{ if .Params.dump_date }}_{{ now | date … }}{{ end }}.sql.gz"`
appears in `templates.md:45`, `config/commands/templating.md:56`,
`config/commands/index.md:183`, `directives.md:447` and `types.md:129`. Commands reach
`tpl.RenderCommand` through their own path (`BuildRunContext`), which must keep rendering
unconditionally. Implement the gate in the pipeline resolve helper — **not** inside
`RenderCommand`, and not as a global rule.

Within pipeline steps, mixed known-head + raw `{{ }}` in one field stays unsupported by
decision: once a step string needs `${vars.x}`, a raw `{{ … }}` beside it is executed
against `RenderContext` and fails. Document it as a pipeline limitation with a clear error
rather than building a `${…}`-only renderer in `tpl` — that would be a new primitive with
its own divergence risk, for a case no workspace exercises. (Checked: no `deploy.yml` in
the five workspaces uses `{{ }}` at all.)

**Hashing `vars` is required for the plan to achieve anything.** Resolve-time rendering
makes `StepHash` sensitive to `vars`, but execution never gets that far: `deploy.go:557`
computes `ProjectConfigHash` and `:503` `ServiceConfigHash`, **neither of which includes
`vars`** (`hash.go:141` hashes tracked services + deploy configs only), and when every hash
matches, deploy returns early with `already up-to-date` without consulting per-step hashes
at all. So `vars` (or the referenced slice of `cfg.Raw`) must enter the project/service
hash. **Owner decision: hash the whole `vars` block.**

**One-time consequence to state up front**: adding `vars` to the project hash changes it
once for every existing project, so the first deploy after the upgrade re-runs steps
instead of skipping them. The steps are idempotent and gated, so this is safe — but it is
visible and belongs in the release notes.

**Empty-resolution diagnostic** (Task 3). Scan with `tpl.VarPattern`, but resolve **only**
heads from `allowedRootKeys`. The special namespaces (`param`, `context`, `files`, `host`,
`snapshot`, `generated`, `args`) do **not** live in `Raw` at all — resolving them there
would warn on every `${param.*}` (99+ occurrences of `${param.database}` in this
repository's fixtures alone, hundreds across the workspaces) and on every
`${generated.*}`, which resolves to `""` by contract on the first deploy. A validator built
to kill noise must not become its largest source — that would contradict Tasks 7–8.
Build on `internal/core/project/varsusage/scan.go` rather than re-deriving which fields
render: it is already field-aware, already uses `tpl.VarPattern`, and AGENTS.md names it as
the single scanner. Note its API is **query-driven** (`ScanUsages(projectRoot, queryPath)`
finds one known path; `Usage` does not carry the referenced path), so this needs a new
enumeration entry point plus a path field — see Task 3, which sizes it accordingly.

**Ports/exports pairing** (Task 6). For every `services.<n>.ports.<key>`, look for an
`ExportRule` whose `From` is `services.<n>.ports.<key>`. Missing → warning explaining that
the port is display-only, that `local.yml` overrides will not move the binding, and that
`dwe test` host-port isolation will silently not apply.

## What Goes Where

- **Implementation Steps** (`[ ]`): all code, tests, scaffold templates, goldens and docs
  inside this repository.
- **Post-Completion** (no checkboxes): verification against the real workspaces, which live
  outside this repo.

## Implementation Steps

### Task 1: Whitelist unknown head namespaces in `CompileVarSyntax`

**Files:**
- Modify: `internal/shared/tpl/render_command.go`
- Modify: `internal/shared/tpl/render_command_test.go`
- Modify: `internal/core/project/config/workspace_test.go` (sync cross-check)
- Modify (migrate stale assertions): `internal/core/execution/templates/config/config_test.go`,
  `internal/core/execution/builtin/services/render_builtins_test.go`,
  `internal/core/workflow/lifecycle/run_test.go`,
  `internal/core/usercommands/runtime/build_context_test.go`,
  `internal/core/execution/pipeline/executor_test.go`,
  `internal/core/usercommands/model/types_test.go`,
  `internal/core/usercommands/loader/loader_test.go`,
  `internal/core/usercommands/runtime/files_resolve_test.go`
- Modify (documented contract that dies with this change): `docs/reference/render/config.md`,
  `docs/i18n/ru/reference/render/config.md`,
  `docs/reference/config/services/fields.md`,
  `docs/i18n/ru/reference/config/services/fields.md`,
  `docs/internals/packages.md` (§ `internal/shared/tpl/`),
  `internal/cli/render/config.go` (help text)

- [x] add an exported `KnownVarHeads` slice in `tpl` covering the config root keys
      (`allowedRootKeys`: `schema_version, project, runtime, state, exports, compose, ui,
      docs, services, vars, update, bridge, stop`) plus the special namespaces already
      switched on in `CompileVarSyntax` (`files, host, param, context, snapshot, generated,
      args`)
- [x] change the default branch: emit `{{ resolve .Raw %q }}` only when the head is known;
      otherwise return the original `${…}` match unchanged
- [x] **`__configPath` stays out of `KnownVarHeads`** (decision): `varPattern` admits a
      leading `_`, so `${__configPath}` would otherwise resolve. It is an internal key the
      loader injects (`workspace.go:1762`), not part of the authoring contract, and a
      reference to it in a command is almost certainly a mistake — so it renders as a
      literal. Pin it with a test so the choice is visible rather than accidental
- [x] migrate the eight test files above off the bare top-level dot-path form
      (`${databases.main}` → `${vars.databases.main}`) — that form has been dead since the
      strict root landed and the assertions encode the pre-strict contract
- [x] update the docs and help text that still promise the bare form, stating plainly that
      free-form values live under `vars:`
- [x] write tests: `${vars.x}` / `${services.app.ports.http}` / `${project.name}` still
      compile to `resolve`; `${HOME}` / `${PATH}` / `${UNKNOWN_THING}` survive literally
- [x] write tests for the boundary cases: `${host.uid}`, `${args}`, `${files.a.path}`, a
      dotted unknown head (`${FOO.bar}`) staying literal, and the known-head/unknown-subkey
      pair `${host.bogus}` / `${args.0}` (today both resolve to `""` — decide and pin)
- [x] add a cross-check test in `internal/core/project/config` asserting `allowedRootKeys`
      ⊆ `tpl.KnownVarHeads`, so a future root key cannot silently stop rendering.
      Note: `tpl` already imports `internal/core/execution/condition`
      (`render_command.go:10`), so "tpl is a leaf" is a convention, not an enforced
      invariant — the duplicated list plus cross-check is still the right call, since
      `internal/core/project/config` does not import `tpl` and `workspace_test.go` is
      `package config` and can see the unexported list
- [x] run tests — must pass before task 2

### Task 2: The shared render helper — `cmd`, `with`, `check` into a copy

*(Split out of an over-large single task: 2 builds the helper, 2b extends it to the three
`when` scopes, 2c wires the `reset step` bypass, 2d adds the hash. Each is independently
testable.)*

**Files:**
- Create: `internal/core/execution/pipeline/render.go` (the shared helper)
- Create: `internal/core/execution/pipeline/render_test.go`
- Modify: `internal/core/execution/pipeline/resolve.go`
- Modify: `internal/core/execution/pipeline/resolve_test.go`

- [x] write the helper: render `cmd`, the string leaves of `with` (recursing into nested
      maps and sequences) and `check`, **into a copy** — deep-copy `With`, clone `*Check`,
      never mutate `cfg` / `deployCfg` (constraint 4c)
- [x] gate every string on "**contains a `${…}` with a known head**" — not on
      `VarPattern.MatchString`, which would still drag
      `docker inspect -f '{{.State.Status}}' ${CONTAINER}` into the renderer and fail on the
      untouched Go template (constraint 4d)
- [x] **a `RenderCommand` error fails the resolve** (decision, not a choice): returning the
      literal instead would reproduce today's `Bad substitution` at runtime, only later and
      with a worse message, which is the opposite of this plan's purpose. A consequence to
      accept knowingly: `RenderCommand` calls `validateSnapshotScope` with
      `SnapshotScopeNone`, so `${snapshot.x}` in a pipeline step becomes a hard resolve error
      where today it reaches `sh` as a literal — correct, since it never resolved there
- [x] **`timeout:` and `FilesGate.With` are rendered too** (decision): `FilesGate.With` is
      hashed by `StepHash`, so leaving it unrendered is an asymmetry, and a templated
      `timeout:` is otherwise a silent parse failure
- [x] call the helper at the very top of `resolveLeafStep` — **before** `builtin.Validate` /
      `spec.Validate` (`resolve.go:134`, `:139`, `:145`) and before `parseStepTimeout`, all
      of which read the fields being rendered
- [x] write tests: `cmd: "git clone ${vars.source.repo} ${vars.source.dir}"` resolves
      substituted; a `type: command` step's `with: {repo: "${vars.source.repo}"}` too (Plan
      B's dependency); a nested `with` value at depth ≥ 2 renders
- [x] write tests: `cmd: 'echo ${HOME}'` keeps `${HOME}`;
      `cmd: "docker inspect -f '{{.State.Status}}' x"` resolves **unchanged**; the **mixed**
      `cmd: "docker inspect -f '{{.State.Status}}' ${CONTAINER}"` also unchanged
- [x] write regression tests proving **user commands are untouched**: the documented mixed
      forms (`cmd` with `${vars.x}` + `{{ with .Params.x }}`, `files.path` with `${param.x}`
      + `{{ if … }}`, workflow `with`/`when`) render exactly as today — the gate lives on the
      pipeline path only (constraint 4d)
- [x] write a test: resolving the same config twice is idempotent (guards against
      double-render and in-place mutation)
- [x] run tests — must pass before task 2b

### Task 2b: Extend rendering to all three `when` scopes

**Files:**
- Modify: `internal/core/execution/pipeline/resolve.go`
- Modify: `internal/core/execution/pipeline/resolve_test.go`
- Modify: `internal/core/workflow/envtest/render_test.go`

- [x] render the runtime `when` of **phases** and **parallel group parents** as well as leaf
      steps: `phaseRuntimeWhen` is stored directly, and `resolveParallelStep` resolves the
      group's own `when` before its substeps reach `resolveLeafStep`, so a `${vars.*}` there
      would stay unrendered while the executor evaluates it from the stored pointer.
      Clone-then-render at each scope
- [x] pin the known divergence: `resolveParallelStep` builds its `ResolvedStep` directly, so
      `rs.Step.Parallel.Steps[i]` stays raw while `rs.Parallel.Steps[i].Step` is rendered.
      Harmless for `StepHash` (Parallel is not hashed) — test it so it is not later mistaken
      for a bug
- [x] write tests for the three scopes separately: phase-level, parallel-group parent, leaf
- [x] write a test that `when.Cmd` renders — Plan B derives `check: auto` from exactly this
      string and needs both sides to match byte for byte
- [x] write a test in `envtest` pinning that scenario `cmd:` keeps `${HOME}` literal (closes
      the pre-existing latent hole)
- [x] run tests — must pass before task 2c

### Task 2c: Use the helper on the `dwe reset step` path

**Files:**
- Modify: `internal/cli/lifecycle/reset.go`
- Modify: `internal/cli/lifecycle/reset_test.go`

- [x] call the Task 2 helper before every raw-step read in `reset step`: it takes a step from
      `FindStep`, evaluates `step.When` itself, prints `pipeline.StepCommand(step)`, then
      runs `step.Action()` and `*step.Check` — all bypassing `ResolvePhaseSteps`
- [x] confirm the dry-run output path prints the rendered form too, so what is previewed is
      what would run
- [x] write tests on that path for `cmd`, `when.cmd` and `check.with`
- [x] write a test that `reset step` and `reset run` produce the same rendered command for
      the same step (the divergence this task exists to remove)
- [x] run tests — must pass before task 2d

### Task 2d: Hash `vars` so a changed value actually re-runs the step

**Files:**
- Modify: `internal/core/workflow/deploy/journal/hash.go`
- Modify: `internal/core/workflow/deploy/journal/hash_test.go`
- Modify: `internal/cli/deploy/deploy.go` tests as needed

Without this the whole plan is inert: `deploy.go:557`/`:503` compute the project and
service hashes, `vars` is in neither, and a full hash match returns early with
`already up-to-date` before any per-step hash is consulted. Rendering would work and
nothing would re-run.

- [x] include the `vars` block in `ProjectConfigHash` (owner decision: the whole block, not
      only referenced paths — simpler and cannot drift out of sync with the reference
      scanner)
- [x] **`ServiceConfigHash` must carry it too — this is a requirement, not a decision.**
      A scoped deploy never consults the project hash: `computeScopeState`
      (`deploy.go:847-851`) compares `svc.ConfigHash == serviceHashes[name]` for a
      single-service run, and `makeSkipDecider` uses `serviceHashes` for service-scoped
      steps as well. So `dwe deploy run --service app` after changing a var referenced only
      by `services/app/deploy.yml` would still report `already up-to-date` if only the
      project hash gained `vars`
- [x] write an **end-to-end** test for **both** paths: change a `vars:` value referenced by
      a step → the next `deploy run` executes that step, **and** `deploy run --service app`
      does too. A test that only asserts "StepHash changed" is tautological — `StepHash` is
      a pure function of the step — and would pass while the behaviour stays broken
- [x] write a test that changing an unrelated `vars:` entry also invalidates (accepted
      cost of hashing the whole block — pin it so the trade-off is visible)
- [x] record the one-time re-run in the release notes (constraint: every existing project's
      project hash changes once on upgrade) — captured in the `ServiceConfigHash` doc comment
      now; the user-facing release-notes entry is Task 16's explicit checkbox (final task,
      once the whole plan lands)
- [x] run tests — must pass before task 3

### Task 3: Validator — `${…}` with a known head that resolves to nothing

**Files:**
- Create: `internal/core/validate/config/template_refs.go`
- Create: `internal/core/validate/config/template_refs_test.go`
- Create: `internal/core/validate/config/testdata/template_ref_typo/…`
- Modify: `internal/core/project/varsusage/scan.go` (new enumeration entry point + a path
  field on `Usage` — see the first checkbox; this is not a filter widening)
- Modify: `internal/core/validate/config/all.go`

- [x] (TDD) write the fixture: a service `deploy.yml` referencing `${vars.opechatka}` plus
      a valid `${vars.source.repo}`, and the test asserting exactly one warning naming the
      file, step and field
- [x] make `varsusage`'s `with:` scanning **recurse** through nested maps and sequences
      first. Today it looks only at direct scalar values immediately under a `with` mapping
      (`scan.go:271-277`), while Task 2 renders string leaves at any depth — so without this
      the renderer and the validator disagree about which references exist
- [x] extend `varsusage` with an **enumeration** entry point. Today it is query-driven —
      `ScanUsages(projectRoot, queryPath)` looks for one known path and matches on dot
      boundaries, and `Usage` carries `File/Line/Kind/Text` but **not the referenced
      path**. The validator needs the inverse: collect every `${known-head.path}` and test
      each for resolvability. So this is a new exported function plus a path field on
      `Usage` — not "widen a filter", and the task is roughly twice the size the first
      draft implied
- [x] build the validator on that entry point rather than re-deriving which fields render —
      `varsusage` is named in AGENTS.md as the single field-aware scanner, and a second list
      would drift
- [x] resolve **only** heads from `allowedRootKeys`; exclude `param`, `context`, `files`,
      `host`, `snapshot`, `args` entirely (they do not live in `Raw`) and `generated`
      explicitly (lenient-by-contract: absent → `""` on the first deploy). Without this the
      validator fires on every `${param.*}` — 99+ in this repo's fixtures alone — and
      becomes the noise Tasks 7–8 are removing
- [x] register it in `All()`
- [x] write tests for the negative cases: unknown head (`${HOME}`), `${param.x}`,
      `${generated.key}` and a resolvable reference all produce **no** diagnostic
- [x] run tests — must pass before task 4

### Task 4: Normalize the compose project name

**Files:**
- Modify: `internal/core/project/config/docker.go`
- Modify: `internal/core/project/config/docker_test.go`

- [x] normalize to lowercase in both `ResolveComposeProjectName` and `ComposeProjectName`
      (single shared helper — the two must never diverge)
- [x] **keep** the legacy candidate when it differs only by case. Note the current code
      already collapses on exact equality only (`docker.go:308`: `full != primary`), so this
      is a **regression test**, not a change — write it as such
- [x] add the **pre-normalization** value as a candidate too. This is the real gap: an
      explicit `docker.yml project_name: dwe-cueBreaker` becomes `dwe-cuebreaker` after
      normalization, and the original spelling appears in no candidate at all (the second
      candidate is derived from `Project.FullName()`, not from the pre-normalized value).
      Docker **does** allow uppercase in container names, and the compose-bypass paths
      (daemon builtins, `StopContainer`/`RemoveContainer`, label filters) build
      `<project>-<name>` directly — so containers created under the old spelling would be
      orphaned by exactly the argument this plan uses for the `FullName` route
- [x] write tests: `project.name: cueBreaker` yields `dwe-cuebreaker`; an explicit
      `docker.yml project_name` is passed through unchanged when already lowercase
- [x] write tests for the candidates helper: case-only difference keeps **two** candidates
      (canonical first), exact match keeps one
- [x] run tests — must pass before task 5

### Task 5: Fix the compose-name hint and add a `container_name` casing check

**Files:**
- Modify: `internal/core/validate/config/compose_name.go`
- Modify: `internal/core/validate/config/compose_name_test.go`
- Create: `internal/core/validate/config/container_name.go`
- Create: `internal/core/validate/config/container_name_test.go`
- Modify: `internal/core/validate/config/all.go`

- [x] (TDD) write the test pinning that the hint's **first** suggestion is a valid compose
      project name (today it recommends the CamelCase form docker rejects — an agent
      followed it and got a failed `reset run`)
- [x] reorder the hint so the `docker.yml project_name` route leads, and never suggest a
      value that would be rejected
- [x] (TDD) write the fixture + test for a `container_name:` that **diverges from the
      derived `<project>-<service>`** — note the defect is divergence, not casing: a
      lowercase `container_name: myapp` breaks `dwe stop <name>`, `dwe restart <name>` and
      the daemon builtins exactly the same way, since those resolve the derived name.
      Correction found during implementation: `dwe stop`/`restart` and
      `docker_stop_remove_container` already resolve containers via compose project+service
      **labels** (`docker.LookupServiceContainer`), not by guessing the derived name — only
      the daemon builtins (`daemon.ResolveContainerName`) build it directly, and daemons are
      not compose services so this check doesn't apply to them either. The diagnostic wording
      was written to state the real risk honestly: divergence is a foot-gun for raw
      `docker`/`docker compose` usage, scripts and docs that assume the derived name — not a
      claim that it breaks dwe's own compose-bypass paths, which are already robust to it
- [x] surface the finding through the existing `config.ScanComposeIsolation`
      (`compose_scan.go:78,108`), which **already** parses the `ComposeFiles()` chain — but
      note two gaps the first draft treated as drop-in reuse: (a) `scanComposeDoc` flags
      **any** non-empty `container_name` (`svc.ContainerName != ""`), without comparing to
      the derived name and without skipping `${…}`-interpolated values, so both "silent"
      cases below fail as-is; (b) the value itself is not exposed —
      `IsolationFinding.Resource` is the **service** name and the value only appears inside
      the human `Message`, so `IsolationFinding` needs a `Value` field, which touches its
      two existing consumers (`validate/tests`, envtest runner)
- [x] **filter by kind**: wire only `KindContainerName` into the `config` domain. Mapping
      `Blocking → error` wholesale would also raise `KindRawHostPort`, which fires on any
      literal `"5001:5000"` in compose — normal dev practice (beetDeck lives that way) —
      turning `dwe validate` red almost everywhere and contradicting Tasks 7–8 and both
      acceptance criteria in Task 15. Precedent: `dwe validate tests` deliberately demotes
      **all** findings to `SeverityWarning`
- [x] write tests for the silent cases: derived-matching names, interpolated `${…}` names
- [x] run tests — must pass before task 6

### Task 6: Validator — declared port without a matching `exports.env` rule

**Files:**
- Create: `internal/core/validate/config/ports_exports.go`
- Create: `internal/core/validate/config/ports_exports_test.go`
- Create: `internal/core/validate/config/testdata/ports_unexported/…`
- Modify: `internal/core/validate/config/all.go`

- [x] (TDD) write the fixture reproducing the live beetDeck defect: `ports.http` declared
      on a service, no `ExportRule` with `from: services.<n>.ports.http`
- [x] write the test asserting a warning whose message states the port is display-only,
      that a `local.yml` override will not move the binding, and that `dwe test` host-port
      isolation will silently not apply
- [x] implement the validator over `cfg.Services` × `cfg.Exports.Env`, iterating services
      through `config.DeployOrder(...)` (never `range cfg.Services` — map order is random
      and would make the test flaky)
- [x] register in `All()`; write the negative test (correctly paired port → silent)
- [x] no overlap to resolve here — Task 5 wires **only** `KindContainerName` into the
      `config` domain, so `KindRawHostPort` stays where it is and this validator is the only
      voice on ports. (Keep this note: the first draft deferred the decision to this task,
      which was the wrong place — the severity choice belongs where the scanner is wired in)
- [x] run tests — must pass before task 7

### Task 7: Silence render-pack diagnostics on implicit defaults; fix the `services.yml` hint

**Files:**
- Modify: `internal/core/validate/templates/ai.go`
- Modify: `internal/core/validate/templates/ide.go`
- Modify: `internal/core/validate/templates/git.go`
- Modify: `internal/core/validate/templates/*_test.go`

- [x] (TDD) write tests: a `type: app` service with **no** `render.*` key and no pack →
      **no** diagnostic; the same service with explicit `render.ai.enabled: true` and no
      pack → warning preserved
- [x] thread `explicit` from `AIRenderEnabledExplicit` / `IDERenderEnabledExplicit` /
      `GitRenderEnabledExplicit` into the three validators and gate the "pack not found"
      diagnostic on it
- [x] fix the hint text in all three: `services.yml` → `workspace/services/<name>/service.yml`
- [x] write a test pinning the hint names a path that exists in DWE
- [x] run tests — must pass before task 8

### Task 8: Apply the same rule to the remaining always-on noise

**Files:**
- Modify: `internal/core/validate/config/workspace.go`
- Modify: `internal/core/validate/templates/git.go`
- Modify: corresponding `*_test.go`

- [x] (TDD) write tests for each: absent `reset.yml` on a project that never declared one →
      silent; `render.git` enabled implicitly with no `src/.git` before the first deploy →
      silent (today it advises creating a repo or disabling render, contradicting the
      intended clone-on-deploy order)
- [x] apply the implicit/explicit gate to both
- [x] audit the remaining default-on diagnostics for the same pattern and record in this
      plan (➕) any found beyond these two
- [x] write tests confirming the explicit-opt-in variants still warn
- [x] run tests — must pass before task 9

➕ Audit findings (implicit-default + absent-artifact noise pattern), beyond the two fixed
above:
- `internal/core/validate/config/workspace.go` `stylesValidator` "no styles.yml" —
  **not** the same pattern: `styles.yml.tmpl` is unconditionally shipped by the scaffold
  (`internal/core/workflow/scaffold/templates/workspace/styles.yml.tmpl`), so an absent
  `styles.yml` on an existing project means the user deliberately deleted it, same as
  `deploy.yml`/`lifecycle.yml`/`docker.yml`. Left untouched, deliberately.
- `internal/core/validate/commands/commands.go:55-66` — `Validator.Run` emits
  `SeverityInfo "no command files"` unconditionally whenever `workspace/commands/` is
  absent. **This is the same noise pattern**: the directory is never shipped by the
  scaffold (grep confirms no `workspace/commands` anywhere in
  `internal/core/workflow/scaffold/templates/`), so every freshly scaffolded project hits
  this Info on every `dwe validate` run, with no opt-in flag to gate on. Not fixed here —
  out of this task's declared file scope (`commands.go` is not in Task 8's Files list) —
  recorded for a follow-up fix using the same "unconditionally silent on absent, no
  explicit-opt-in analog" treatment applied to `reset.yml` above.
- `internal/core/validate/templates/ai.go` / `ide.go` — already correctly gated (mirror
  the `git.go` fix from Task 7); no change needed.
- `internal/core/validate/snapshot/`, `checks/`, `i18n/`, `setup/`, `linters/`, `env/`,
  `bridge/`, `tests/tests.go` — audited, no unconditional absent-artifact diagnostic found.

### Task 9: An all-comment `info.yml` must fall back to the built-in dashboard

**Files:**
- Modify: `internal/core/project/config/info.go`
- Modify: `internal/core/project/config/info_test.go`
- Modify: `internal/core/workflow/scaffold/templates/workspace/info.yml` (only if wording
  still misstates behavior after the fix)

- [x] define the fallback criterion precisely: **"the document decoded no top-level keys
      at all"**, detected by a pre-pass into `map[string]any` — *not* "Sections is empty".
      `InfoConfig` is `{Sections []InfoSection; Footer bool}` (`info.go:13-17`), so
      "no sections" conflates three different states and would silently override a
      deliberate `sections: []` or a file carrying only `footer: true`
- [x] treat that state as absent and return `DefaultInfoConfig()`, mirroring the `io.EOF`
      tolerance of the four strict pipeline loaders — **without** switching this loader to
      a strict decoder
- [x] write tests for all four states: fully commented → default; empty file → default;
      deliberate `sections: []` → empty dashboard (default **not** restored); one real
      section → that section only
- [x] verify the scaffold header claim ("shipped fully commented so that default stays
      active") is now true, and adjust wording if the implemented semantics differ
- [x] run tests — must pass before task 10

### Task 10: Invert the `info` validator verdict

**Files:**
- Modify: `internal/core/validate/config/workspace.go`
- Modify: `internal/core/validate/config/workspace_test.go`

- [x] (TDD) write tests for the same four states Task 9 pinned: all-comment (default
      active) reports the inert state honestly rather than `SeverityOK`; deliberate
      `sections: []` reports its own state; an authored dashboard reports OK; an absent
      file stays informational
- [x] implement the inverted verdict
- [x] ensure the wording distinguishes "inert scaffold, built-in dashboard active",
      "deliberately empty dashboard" and "authored dashboard"
- [x] run tests — must pass before task 11

### Task 11: Drop the removed `run.update` from the lifecycle scaffold

**Files:**
- Modify: `internal/core/workflow/scaffold/templates/workspace/lifecycle.yml`
- Modify: `internal/core/workflow/scaffold/testdata/golden_default.txt`

- [x] remove the commented `update:` block from the `run:` example (the field no longer
      exists on `LifecycleRunConfig`; uncommenting the block as the file invites produces a
      strict-decode hard error)
- [x] point the comment at the top-level `update:` block instead, so the migration is
      discoverable from where the old field used to be
- [x] regenerate the scaffold golden
- [x] write a **table-driven** test covering every inert scaffold mirror shipped by the
      scaffolder (`lifecycle.yml`, `deploy.yml`, `info.yml`, `docker.yml` — `reset.yml` and
      `defaults.yml` are not part of the scaffold's inert-mirror set: no `reset.yml` is
      scaffolded, and `defaults.yml` carries active keys so it is not fully inert):
      uncommenting each file wholesale must load cleanly. Tasks 9 and 11 fix the same class
      of defect — "an inert mirror that lies about itself" — and one table closes it for good
- [x] run tests — must pass before task 12

### Task 12: `dwe deploy plan --output json`

**Files:**
- Modify: `internal/cli/deploy/deploy.go`
- Modify: `internal/cli/deploy/plan_test.go`

- [ ] define the typed plan payload (phases → steps → type, cmd, gates) and emit it through
      `cmdctx.WriteData[T]` when `--output json` is set, leaving `--format table|shell`
      untouched otherwise
- [ ] ensure no ANSI escapes leak into the JSON path (the captured session shows
      `\x1b[38;5;45m…` in piped plan output)
- [ ] write tests for the JSON shape (stable key set, deterministic ordering)
- [ ] write a test asserting the human `--format` paths are byte-identical to today
- [ ] run tests — must pass before task 13

### Task 13: Mark unresolved templates in the plan output

**Files:**
- Modify: `internal/core/execution/pipeline/step.go`
- Modify: `internal/cli/deploy/deploy.go`
- Modify: corresponding `*_test.go`

- [ ] with resolve-time rendering (Task 2) the plan already prints substituted values, so a
      `${…}` surviving into `StepCommand` output is **almost always** an unknown head —
      annotate those in both the human and JSON plan output so the plan stops presenting
      them as "what will run". Two exceptions to keep in mind rather than mis-flag: a
      `${vars.x}` whose *value* itself contains `${…}`, and any string that failed the
      `VarPattern` gate. (Had rendering stayed at exec time, this task would have flagged
      correct references too — see Technical Details.)
- [ ] note in the docs that the plan now prints substituted values rather than `${vars.*}`
      literals (constraint 4e) — that is the point of the change, no masking
- [ ] keep the annotation out of `--format shell` (that output must stay executable)
- [ ] write tests covering: resolved template renders substituted; unknown head shown as
      literal with annotation; JSON payload carries the flag
- [ ] run tests — must pass before task 14

### Task 14: Report scope when `validate` is narrowed

**Files:**
- Modify: `internal/cli/validate/validate.go`
- Modify: `internal/cli/validate/validate_test.go`

- [ ] include the active scope (domain + validator id, or "all") in both the human summary
      line and the JSON summary, so `dwe validate config services` (one check) is no longer
      indistinguishable from `dwe validate config` (ten)
- [ ] keep the diagnostics-as-data contract intact (constraint 3)
- [ ] write tests: full run, domain run, leaf run — each reports its own scope
- [ ] write a test asserting the JSON summary gained the field without breaking existing keys
- [ ] run tests — must pass before task 15

### Task 15: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented
- [ ] reproduce the three original defects as fixtures and confirm each is now caught
      statically: `${vars.*}` in a shell step (now renders), CamelCase project name (now
      normalized + flagged), all-comment `info.yml` (now falls back)
- [ ] confirm a freshly scaffolded single-service project produces **zero** `config` and
      `templates` warnings from `dwe validate`. Two corrections to the first draft, both
      measured on a real `dwe init`: (a) today a fresh scaffold emits exactly **one**
      warning — `service "app" (type app) has no dir or dir_internal`
      (`validate/config/workspace.go:560`) — and the six `template pack not found` lines do
      **not** appear, because the template validators bail out with an info line while
      `svc.Dir` is empty. They materialize only once Plan C Task 4 activates `dir`, which is
      precisely what Tasks 7–8 here are built to absorb. (b) The `env` domain checks the
      **host** (a busy port is a legitimate error), so it is out of scope for this
      criterion. Consequently **this acceptance criterion is only fully satisfiable
      together with Plan C Task 4** — state that rather than letting it read as achievable
      by Plan A alone
- [ ] run full test suite: `make test`
- [ ] run `make lint`
- [ ] verify test coverage meets project standard

### Task 16: [Final] Update documentation

- [ ] document the whitelist rendering rule and the resolve-time evaluation point in
      `docs/reference/` (where templates are evaluated) — the current page's omission is
      what three workspaces documented by hand in YAML comments
- [ ] document the new validators (`ports`/`exports` pairing, `container_name` divergence,
      empty template refs) in `docs/reference/config/validate.md`
- [ ] confirm the contract migration from Task 1 landed everywhere:
      `docs/reference/render/config.md`, `docs/reference/config/services/fields.md`
      (§ `render.config`), `docs/internals/packages.md` (§ `internal/shared/tpl/` — it
      describes the old unconditional fallthrough verbatim), and the
      `internal/cli/render/config.go` help text
- [ ] note the one-time hash change in the release notes (adding `vars` to the project hash
      makes the first deploy after upgrade re-run steps in every existing project)
- [ ] document the remaining exception: `config.resolveVarTemplate` (`docker.go:339`) keeps
      its own semantics for `${dot.path}` in `docker.yml project_name` and **errors** on an
      unresolvable path. After Task 1 the same `${FOO}` is a literal in `deploy.yml` and a
      hard error in `docker.yml` — one sentence, so the difference is deliberate
- [ ] update the Russian mirrors under `docs/i18n/ru/`
- [ ] run `make build` to resync embedded docs and content hashes
- [ ] update `AGENTS.md` Critical Patterns — **not conditional**: a rule that changes the
      meaning of every `${...}` in the product is load-bearing by definition
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Verification against real workspaces** (outside this repo):
- run `dwe validate` in `beetDeck` — the ports/exports validator should flag
  `services.{backend,frontend,dbgate}.ports.http` as declared-but-unexported. Precisely:
  beetDeck has **no** port export rules at all, and `BEETDECK_HTTP_PORT` /
  `BEETDECK_VITE_PORT` appear only inside `compose.yaml`
  (`"${BEETDECK_HTTP_PORT:-5001}:5000"`), so the binding survives on the fallback default
  alone. Consider the mirror check too — a `${NAME:-default}` in compose with no export
  rule producing `NAME` — which additionally catches a typo in an export name
- run `dwe validate` in all five workspaces (`podlapka`, `AlbFetcharr`, `beetDeck`, `alto`,
  `cueBreaker`) and confirm the warning count drops sharply without losing any real signal
- confirm `dwe deploy plan` in `cueBreaker` now shows substituted clone coordinates
- spot-check that normalizing the project name does not orphan containers in workspaces
  that already pinned a lowercase `project_name` (expected: no change, since the pinned
  value is passed through)

**Follow-on plans**:
- Plan B (primitives) depends on Tasks 1–2 landing first
- Plan C (scaffold class-1 content) depends on Plan B's final primitive set
