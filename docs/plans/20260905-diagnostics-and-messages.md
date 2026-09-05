# Diagnostics and messages — messages that tell the truth

Four independent fixes to what `dwe` says when a project is slightly wrong.
Grouped because they are one user story (a config mistake that today is either
silent or explained wrong) and one batch of reference-doc edits — one RU mirror
pass, one `content_hashes_gen.go` regeneration.

## Overview

1. **The compose isolation scanner honours `shared: true`.** The recipe the
   docs recommend for a cross-project package cache — `docker.yml`
   `resources.volumes.<key>: {name: dwe_npm_cache, shared: true}` plus a raw
   compose `volumes: npm_cache: {external: true, name: dwe_npm_cache}` — is
   flagged twice per volume (`external_volume` + `named_volume`) on every
   `dwe validate tests`, permanently and unfixably, even though `dwe` itself
   creates that volume through `ensure_before`. Three real workspaces carry
   these warnings today (beetDeck: 4, laravel and magento: 2 each). The scanner
   learns to read the existing `shared: true` declaration; **no new config
   surface**. Acknowledged findings go silent in `dwe validate tests` and
   `dwe test run`, and stay listed — marked `"shared": true` — in
   `dwe test list --output json`'s `cost_profile.isolation_findings`, which
   reports facts, not verdicts.
2. **`dwe validate` reports the default-pipeline state honestly.** `config.info`
   already distinguishes absent / all-comment / authored (`built-in dashboard
   is active`), but `config.deploy` and `config.lifecycle` say a bare
   `no deploy.yml`, and — worse — the all-comment `deploy.yml` that `dwe init`
   scaffolds earns **OK** while the built-in default pipeline is what actually
   runs. Same three-state model for deploy / lifecycle / reset. Also fixes
   `docs/reference/config/deploy/builtins.md`, which claims predicate builtins
   work inside `when:` — they do not; `builtin.Inventory()` and
   `condition.Predicates()` are disjoint registries.
3. **An unresolvable dot-path is reported instead of rendering empty.**
   `exports.env[].from: vars.db.passwrod` passes every check today and
   `dwe render env` prints `DB_PASSWORD=`. Same for `exports.env[].when`,
   `params.<p>.default_from`, `params.<p>.options.from` and
   `context.<c>.from`. Two validators gain typed checks (warning), and
   `dwe render env` prints a stderr warning at the exact moment the user sees
   the empty value.
4. **Strict-loader unknown-field errors name the file, the field and the
   allowed set.** Every `KnownFields(true)` loader surfaces yaml.v3's raw
   `field defaults not found in type config.DeployConfig`. A new leaf package
   wraps that into `workspace/deploy.yml:12: unknown field "defaults" — allowed
   here: fail_fast, log, phases`, with a hint that a field the author did not
   invent may come from a newer `dwe`. The root-key message keeps its `vars:`
   advice (correct for a home-made key) and gains the same hint.

Not in scope: networks in item 1 (no `docker.yml` network resource exists and
no workspace has a network finding); a load-time hard error for item 3 (breaks
the legitimate `from:` + `default:` optional-path pattern); wiring `Line` into
`dwe validate` diagnostics from item 4's typed error (nice-to-have, separate);
the `.env` regeneration inside deploy pipelines (item 3's warning is
`dwe render env` only).

## Context (from discovery)

Line numbers are against `release/0.6.0` at `3de2cbd7`.

**Item 1 — scanner**
- `internal/core/project/config/compose_scan.go`: `IsolationFinding` `:32-40`
  (`Kind, Resource, Value, HostPort, Blocking, Message, File`),
  `ScanComposeIsolation(cfg, projectRoot)` `:98` (collapse across the `-f`
  chain via `effective map[declKey]int`; its godoc `:87-97` calls it "a leaf
  function" reading only compose files — no longer true once it reads
  `docker.yml`), `scanNamedEntity` `:546-590` (the `external:` and `name:`
  findings; `Value` is **not** set on the named finding today — `explicit`
  only lands in `Message`). The compose files themselves are decoded with a
  plain lenient `yaml.Unmarshal` `:189`, untouched by item 4.
- `internal/core/project/config/docker.go:54-84`: `DockerResourcesConfig`,
  `DockerVolumeConfig{Name, Shared, EnsureBefore}`, `ResolveName(projectName)`
  (shared → `Name` verbatim). `LoadDockerConfigOrEmpty(baseDir, cfg)` `:112`
  (lenient, merges `docker.local.yml`).
- Consumers: `internal/core/validate/tests/tests.go:99-107` (every finding →
  `SeverityWarning`), `internal/core/workflow/envtest/runner.go:542-570`
  (`scanComposeIsolationGate`: warn all, block on `Blocking`),
  `internal/cli/test/profile.go:183-190` (`isolation_findings`, non-blocking
  only; `testIsolationFindingJSON{Kind, Resource}` `:70`; `sharedVolumes`
  already counts `shared: true` volumes `:115-119` — so the profiler holds
  the docker config already and `profile()` calls the scanner once per
  scenario; the scanner re-reads `docker.yml` per call, which is cheap and
  keeps the `(cfg, projectRoot)` signature every caller already uses),
  `internal/core/validate/config/container_name.go:53` (container_name only).
- Tests: `internal/cli/test` has **no goldens**; the isolation assertions are
  field-level in `internal/cli/test/list_test.go:262` and `:385`.
  `TestRunTestList_CostProfileHeavyProject` (`:184`) already declares
  `composer: {name: composer-cache, shared: true}` (`:206`) against a compose
  `volumes: composer-cache: {external: true}` (`:198`) — that is the
  "match by key when compose has no `name:`" case, so it becomes the suite's
  first `Shared: true` finding as soon as task 1 lands.
- Docs: `docs/reference/config/tests.md` — kinds table `:358-363`, "Compose
  isolation scanner" `:354-370` (of which `:368` "every finding is printed as
  a warning" and `:369` "emits every finding as a warning, regardless of
  `Blocking`" become false), `:352` ("findings are emitted once per project as
  warnings"), `cost_profile` table row `:294`, limitations `:406`; RU mirror
  `docs/i18n/ru/reference/config/tests.md`. `docs/internals/packages.md:83`
  describes the scanner and already omits `Value` from its field list.
- `skills/dwe/references/integration-tests.md:23` makes a non-empty
  `isolation_findings` a hard stop for an agent; shared entries stay in that
  list, so the row needs a `shared` qualifier in the release wrap-up.
- Live reproduction (`~/Projects/beetDeck`, `dwe validate tests --output json`):
  4 warnings — `npm_cache` (compose.yaml), `pip_cache` (compose.yaml,
  compose/tools/mcp.yml), each as `external_volume` + `named_volume`; the
  compose declarations are `{external: true, name: dwe_npm_cache}` and
  `docker.yml` declares both with `shared: true`.

**Item 2 — validate three states**
- Loaders in `internal/core/project/config/workspace.go`:
  `LoadLifecycleConfig` `:3096`, `loadProjectDeployConfigDecode` `:3151`,
  `loadServiceDeployConfigDecode` `:3176`, `loadDeployConfigDecode` `:3201`.
  Each tolerates `io.EOF` (all-comment file → zero-valued cfg) — that branch
  is where "defaulted" is known and then thrown away. Only three of the four
  need to expose it: no validator reports per-service `deploy.yml` state, so
  `loadServiceDeployConfigDecode` is left alone.
  `ParseDeployConfigForValidation` `:3231`, `LoadProjectDeployConfig` `:3242`,
  `LoadResetConfig` `:3262`.
- Model to copy: `LoadInfoConfigWithState` + `InfoConfigState`
  (`info.go:253-300`).
- Validators in `internal/core/validate/config/workspace.go`:
  `lifecycleValidator.Run` `:1092-1133` (`no lifecycle.yml`),
  `deployValidator.Run` `:1140-1200` (`no deploy.yml`, then `ResolvePlan`
  cross-check), `resetValidator.Run` `:1211-1265` (absent → silent, on
  purpose — the scaffold never ships `reset.yml`). Info validator wording at
  `:766` / `:774`.
- `AGENTS.md:81` states the contract this item surfaces ("Exactly four strict
  *pipeline* loaders also tolerate `io.EOF` …"); `packages.md` § Core —
  Foundation describes the pipeline loaders.
- Goldens / tests: `internal/cli/validate/testdata/validate_config.json.golden`
  (`UPDATE_GOLDEN=1`, see `internal/cli/validate/*_test.go:951-970`),
  `internal/core/validate/config/workspace_test.go` (search `no deploy.yml`,
  `no lifecycle.yml`). Leave `deploy_after_test.go:118` and
  `service_hooks_test.go:147` alone — a different, per-service message.
- Docs: `docs/reference/config/validate.md:86` (three-state paragraph for
  `config.info`), `:90` (reset absence sentence).
  `docs/reference/config/deploy/builtins.md:7,28,305` say `check:`/`when:`
  (RU mirror `:9,30,307`); `:339` (`source_clone`) is correct and stays.
  The heading `## Two \`type: builtin\` registries` lives in
  `docs/reference/config/conditions.md:153` (NOT `deploy/conditions.md`), so
  from `builtins.md` the link is `../conditions.md#two-type-builtin-registries`
  (precedent: `deploy/conditions.md:114`); the RU anchor is
  `#два-регистра-type-builtin` (`docs/i18n/ru/reference/config/conditions.md:155`).

**Item 3 — dot-paths**
- `config.ResolvePath(m, path) (any, bool)` `workspace.go:3562`; the
  `found == false` return is the criterion (a `nil` at a present key is fine).
  It is literally what `envfile.BuildContent` uses (`render.go:92,104`), so
  validator and renderer cannot drift — that is why the validators use it
  rather than `template_refs.go:90`'s package-local `resolvesInRaw`.
- `ExportRule{Name, From, Default, Required, Format, When, Comment}`
  `workspace.go:1559`; rendered in `internal/shared/envfile/render.go:85-120`
  (`When` falsy → rule skipped; `From` missing → `Default`; `Required` and
  empty → error). `dwe render env` CLI: `internal/cli/render/env.go:37-52`
  (`runRenderEnv(flags, outputPath)` — no `cmd` parameter, writes with bare
  `fmt.Print`; `internal/cli/render/env_test.go` covers only
  `envfile.BuildContent`, there is no `runRenderEnv` test to extend). The repo
  convention for stderr is `cmd.ErrOrStderr()` (`cli/validate/validate.go:557`,
  `cli/deploy/menu.go:129`).
  Rules may sit in any layer (`exports` is a formal block,
  `validate/config/formal_blocks.go:27`); the docs say `workspace/defaults.yml`
  but only `workspace.yml` is guaranteed to exist (`layers.go:50-60`).
  `config.LoadRawLayers(workspacePath) []Layer{Path, Data}` `layers.go:50`
  locates the declaring file. `validate.Context.ConfigPath` is populated by
  `cli/validate/validate.go:557`; the empty-Context constructions at
  `preflight.go:117` and `menu.go:729` never reach `config.exports` (preflight
  registers only `env.*`, `config.validate`, `secrets.unresolved`, `checks.*`
  — `preflight.go:128-160`), but a `ConfigPath == ""` fallback is cheap
  (precedent `validate/config/workspace.go:45-48`).
- Commands: `model.ParamDef.DefaultFrom` `types.go:580`,
  `ParamOptions.From` `:510/:565` (written as `${…}`, stored stripped),
  `model.ContextDef.From` `:622`. The validator
  `internal/core/validate/commands/commands.go:104-106` iterates
  `parsedFiles → cf.Commands` — the **as-written** command files, with
  `relFile` at `:105` — not the registry (built only at `:243` for a separate
  pass), so daemon-expanded synthetic commands never reach it and duplicates
  are impossible. Targets are colon-separated: `commands:<id>` then
  `commands:<id>:params.<name>` (`:323-325`). The existing default-in-options
  check `:464-510` resolves `DefaultFrom` and `Options.From` and goes silent
  (`canCheck = false`) on a miss.
- The `varsusage` scanner is deliberately NOT the vehicle: `manifest.yml`
  `render[].from` is a file path, and the scanner carries no typed context
  (has `default:`? is `required`?). `template_refs.go` stays as is and
  produces no duplicate: `structuralKeys` (`scan.go:129-132`) is gated
  `!matchAll` (`:339`), so `EnumerateAllUsages` never sees `from:` /
  `default_from:`, and `options:` is not in `templatedKeys`.
- Existing validator docs: `docs/reference/config/validate.md:40-52` (domain
  table), `:78` (`config.template_refs` paragraph — the new validators sit
  next to it). `docs/reference/render/env.md:89-110` (rule fields, evaluation
  order).
- Validator registry: `internal/core/validate/config/all.go`.

**Item 4 — strict loaders**
- 12 `KnownFields(true)` sites: `workspace.go:2376` (service.yml, after a
  hand-validated first pass), `:3103/:3158/:3183/:3208` (pipelines),
  `snapshot.go:152`, `validate.go:200`,
  `internal/core/usercommands/model/types.go:1697` (`YAML parse error: %w`),
  `internal/core/execution/templates/manifest/manifest.go:73`,
  `internal/core/workflow/envtest/scenario.go:85`,
  `internal/core/workflow/setup/loader.go:31`,
  `internal/shared/i18n/loader.go:22` (`parseBundle(io.Reader)`, called from
  `Load` `:64` and `LoadProjectBundles` `:167` with the bytes in hand).
  Four prose comments also mention `KnownFields(true)` (`workspace.go:491,
  517, 540, 1012`) — grep for the call form `.KnownFields(true)`.
- `model.ParseCommandFile(data []byte)` `types.go:1648` has **no path**;
  `cf.FilePath` is assigned by the caller `usercommands/loader/loader.go:101`
  after the parse, which also wraps the error with the file name; the function
  is re-exported at `usercommands/usercommands.go:218`. Its lenient first pass
  `:1650-1677` (`allowedFieldsFor`) already rejects unknown per-command
  top-level keys with `command %q: field %q not allowed for type %q`, so the
  strict second pass only ever sees **nested** unknowns (`params.x.widgett`)
  and commands with no `type:`.
- `manifest.go:62` decodes from an `os.Open` reader, wraps `os.ErrNotExist`
  with `ErrManifestMissing` (`:64`) and turns `io.EOF` into a "manifest is
  empty" error (`:76`).
- `checkKnownFields` `workspace.go:523-535` compensates for yaml.v3 bypassing
  `KnownFields` inside a custom `UnmarshalYAML` and returns
  `field X not found in type config.DeployStep` **without** a line, against
  the hand-maintained `deployStepKnownFields` `:493` / `parallelGroupKnownFields`
  — nothing cross-checks them against `DeployStep`'s tags `:440-467`. yaml.v3
  re-raises a non-`*TypeError` from an unmarshaler verbatim
  (`decode.go:365-375` → `fail(err)` → `yaml.go:289-305` `handleErr`), so it
  reaches the caller as a plain error, not inside `TypeError.Errors`.
- `yaml.TypeError{Errors []string}`; lines look like
  `line 3: field foo not found in type config.rawCheckEntry`. yaml.v3 names
  the type with `reflect.Type.String()`.
- Root-key message: `internal/core/project/config/layers.go:385-386`; docs
  example `docs/reference/config/workspace.md:108` (already stale — omits the
  `; allowed top-level keys: …` clause the code emits); `snapshot.md:88`
  mentions strict decoding.
- Tests pinning today's text: `internal/core/validate/config/validate_yml_test.go:65-69`
  (`parse workspace/validate.yml: yaml: unmarshal errors:\n  line 3: field foo
  not found in type config.rawCheckEntry`);
  `internal/core/validate/commands/commands_test.go:741-743` and
  `internal/cli/deploy/menu_test.go:141` are synthetic strings (`YAML parse
  error:` / `field unknownfield not found in type`) — they keep passing but
  pin a shape that stops existing, so they get rewritten to the new one.
- `docs/internals/packages.md` § Shared — Leaf Infrastructure `:285`;
  `AGENTS.md:80-81` "YAML loader strictness" bullet. `TestAgentsMdBudget`
  (`internal/cli/docs/agentsmd_test.go:28`, `agentsMdBudget = 40 * 1024`)
  pins the Critical Patterns byte budget and `AGENTS.md` is **40920 B today
  — 40 B of headroom**; `TestAgentsMdPointersResolve` requires every
  `§ \`internal/shared/yamlstrict/\`` pointer to match a literal
  `- \`internal/shared/yamlstrict/\`` bullet in `packages.md`.

## Development Approach

- **testing approach**: Regular — code first, then tests in the same task.
- one branch `feat/diagnostics-and-messages` off `release/0.6.0`; **four
  commits, one per item**, in the order below (smallest blast radius first,
  the twelve-site loader refactor last so it reverts as one commit).
- each commit appends its own `CHANGELOG.md` `## [Unreleased]` entry; reverts
  therefore go in reverse order only. Two stronger reasons for the same rule:
  commits 2 and 3 both rewrite the single-line
  `internal/cli/validate/testdata/validate_config.json.golden`, and commit 4
  restructures the loader bodies commit 2 reshaped (`workspace.go:3096-3230`).
- every docs edit has an EN + RU mirror (`docs/i18n/ru/...`), followed by
  `make build` (re-syncs the embedded tree, regenerates
  `internal/core/docs/content_hashes_gen.go`).
- complete each task fully before moving to the next; small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in
  that task — success and error cases, listed as separate checklist items.
- **CRITICAL: all tests must pass before starting the next task.**
- **CRITICAL: update this plan file when scope changes during implementation.**
- public loader signatures do not change; runtime behaviour of deploy / run /
  stop is untouched by every item.

## Testing Strategy

- **unit tests**: required for every task (table-driven where there is a
  matrix — scanner cases, `yamlstrict` type shapes, validator states).
- **goldens**: `internal/cli/validate/testdata/*.golden` via `UPDATE_GOLDEN=1`,
  reviewed by eye before committing.
- **e2e**: none in this repo. Live checks against `~/Projects/beetDeck` are
  listed under Post-Completion.
- always `make test` (never bare `go test ./...` — the embedded docs tree is
  generated) and `make lint`.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Solution Overview

- **Item 1** — `ScanComposeIsolation` keeps its `(cfg, projectRoot)` signature
  and loads `docker.yml` itself via `LoadDockerConfigOrEmpty` (same package;
  a load error yields an empty shared set — the scanner stays advisory). After
  the `-f` collapse, each surviving volume's effective name (last explicit
  `name:`, else the map key) is looked up in the set of `shared: true` volume
  names; a hit marks both its `external_volume` and `named_volume` findings
  `Shared: true`. `Blocking` is untouched. Callers own policy, as today:
  validate and the runner skip `Shared`; the cost profile serializes it.
- **Item 2** — `config.PipelineFileState` + three `*WithState` loader siblings
  expose the `io.EOF` branch the decode helpers already take. Validators map
  absent → info (deploy, lifecycle; reset stays silent), all-comment → info,
  authored → OK, with wording mirroring `config.info`.
- **Item 3** — two typed validators (new `config.exports`, extended
  `commands`) plus a pure `envfile.UnresolvedRules` that the `render env` CLI
  turns into stderr warnings. One criterion everywhere:
  `config.ResolvePath` reports not-found.
- **Item 4** — `internal/shared/yamlstrict.Decode(data, out, file)` is the
  single strict decoder. It reflects the allowed yaml tags of every type
  reachable from `out`, parses `*yaml.TypeError` (and the plain
  `line N: field X not found in type T` form a custom unmarshaler raises),
  and returns a typed error whose text carries file, line, field and the
  allowed set. `io.EOF` and every non-decode error pass through untouched
  (with the file prefixed), so the four pipeline loaders keep their
  all-comment semantics.

## Technical Details

### Item 1 — `IsolationFinding.Shared`

```go
type IsolationFinding struct {
    Kind     IsolationKind
    Resource string
    Value    string // explicit name: (named_* kinds) / container_name
    HostPort int
    Blocking bool
    // Shared marks a volume finding acknowledged by a docker.yml
    // resources.volumes entry with shared: true whose resolved name equals
    // the compose volume's effective name. Callers decide what to do with
    // it; the scanner only reports the fact.
    Shared   bool
    Message  string
    File     string
}
```

Effective name per compose volume key after collapse: the `Value` of the
surviving `KindNamedVolume` finding for that key, else the key itself.
`scanNamedEntity` starts setting `Value: explicit` on the named finding so the
collapse loop can read it. Shared set: `{vol.ResolveName("") | vol.Shared}`
over `dockerCfg.Resources.Volumes` (shared volumes return `Name` verbatim, so
the project name is irrelevant). Networks are never marked.

Cost profile JSON:

```json
{"kind": "external_volume", "resource": "npm_cache", "shared": true}
```

`shared` is emitted only when true (`omitempty`) so existing output stays
byte-identical for unacknowledged findings.

### Item 2 — `PipelineFileState`

```go
type PipelineFileState int

const (
    PipelineStateAuthored PipelineFileState = iota
    // PipelineStateDefaultFallback is an empty or all-comment file: the
    // decoder hit io.EOF, the returned cfg is zero-valued and the caller's
    // Ensure*Config substitutes the built-in default.
    PipelineStateDefaultFallback
)

func ParseDeployConfigForValidationWithState(path string) (*DeployConfig, PipelineFileState, error)
func LoadLifecycleConfigWithState(path string) (*LifecycleConfig, PipelineFileState, error)
func LoadResetConfigWithState(path string) (*ProjectDeployConfig, PipelineFileState, error)
```

Absent file still returns `os.ErrNotExist` (validators already branch on
`errNotExist`). The three decode helpers grow a `(cfg, state, err)` internal
shape; the existing public loaders drop the state.

Validator matrix:

| state | `config.deploy` / `config.lifecycle` | `config.reset` |
|---|---|---|
| absent | info `no deploy.yml — built-in default pipeline is active` | silent (unchanged) |
| all-comment / empty | info `deploy.yml has no active content (all comments or empty) — built-in default pipeline is active` | same, info |
| authored | OK | OK |

The deploy / reset `ResolvePlan` cross-check runs in the fallback state too
(it already does — the plan resolves from the default).

### Item 3 — dot-path checks

Criterion: `_, found := config.ResolvePath(cfg.Raw, path); !found`.

`config.exports` (new, `internal/core/validate/config/exports.go`):

- Domain `config`, Target `config.exports`, Severity **warning**. No
  `SeverityOK` row when everything resolves (same convention as
  `config.template_refs` and `config.ports_exports`), so no golden changes.
- File: the layer whose `exports.env` list carries an entry with this `name`
  (via `config.LoadRawLayers(ctx.ConfigPath)`), else
  `relPath(ctx.ProjectRoot, ctx.ConfigPath)`; when `ctx.ConfigPath == ""`
  fall back to `workspace.yml` under `ctx.ProjectRoot` (the only layer
  guaranteed to exist).
- Message: `exports.env[APP_PORT]: from "services.app.ports.htpp" does not
  resolve in the merged config` (same shape for `when`).
- Hint by rule shape: no `default` and not `required` → `the variable renders
  empty — check for a typo or add a default`; has `default` → `the default is
  always used — check for a typo`; `required: true` → `dwe render env fails on
  this rule — check for a typo`.
- Reserved-name rules are already rejected at load; skip them defensively.

`commands` validator extension (`internal/core/validate/commands/commands.go`):

- per command in each parsed file (`parsedFiles → cf.Commands`, the loop at
  `commands.go:104-106`): `params.<p>.default_from`, `params.<p>.options.from`,
  `context.<c>.from`. Warning; targets follow the existing colon convention —
  `commands:<id>:params.<name>` (the existing `paramTarget`) and
  `commands:<id>:context.<name>`; File = `relFile`.
- Message: `params.branch: default_from "vars.git.brnch" does not resolve in
  the merged config`. The existing default-in-options check keeps its
  `canCheck = false` short-circuit — the miss now has its own warning above.

`envfile.UnresolvedRules`:

```go
type UnresolvedRule struct{ Name, From string }

// UnresolvedRules returns the exports.env rules that BuildContent will
// render as NAME= (empty): when passes, no default, not required, and from
// does not resolve. Pure; never writes.
func UnresolvedRules(cfg *config.DweConfig) []UnresolvedRule
```

`runRenderEnv` gains the `*cobra.Command` (`runRenderEnv(cmd, flags,
outputPath)`), writes content through `cmd.OutOrStdout()` instead of bare
`fmt.Print`, and prints, for each unresolved rule, to `cmd.ErrOrStderr()`
and only when `flags.Output != "json"`:

```
warning: exports.env[DB_PASSWORD]: from "vars.db.passwrod" does not resolve — rendered empty
```

before writing content (stdout or `--out`). That seam is what makes the CLI
test a `cmd.SetOut` / `cmd.SetErr` pair. `BuildContent` / `Write` are
unchanged.

### Item 4 — `internal/shared/yamlstrict`

```go
// Decode strictly decodes data into out (a non-nil pointer). Unknown-field
// errors are rewritten into *Error; io.EOF passes through untouched so
// callers keep their "all-comment file is absent" rule; any other error is
// returned prefixed with file.
func Decode(data []byte, out any, file string) error

type UnknownField struct {
    Line    int      // 0 when yaml gave none
    Field   string
    Type    string   // reflect.Type.String() as yaml names it
    Allowed []string // nil when Type is not reachable from out
}

type Error struct {
    File    string
    Unknown []UnknownField
    Other   []string // remaining TypeError lines, verbatim
    err     error    // the original *yaml.TypeError / plain error, for Unwrap
}
```

`Error()` text, one line per unknown field, hint once at the end:

```
workspace/deploy.yml:12: unknown field "defaults" — allowed here: fail_fast, log, phases
(a field you did not invent may come from a newer dwe version — check `dwe version`)
```

Without a line: `workspace/deploy.yml: unknown field …`. With an empty
`file` (a caller that has no path, see `ParseCommandFile` below): no prefix
at all — `line 12: unknown field …` — and the caller's existing wrap supplies
the file. Without an allowed set (type not reachable): the `— allowed here:`
clause is omitted. `Other` lines are emitted as `file: <line verbatim>`.

**Hand-maintained allowlists stay honest.** `checkKnownFields` reports
against `deployStepKnownFields` / `parallelGroupKnownFields`, while
`yamlstrict` prints the *reflected* tag set of `config.DeployStep` /
`config.ParallelGroup`. Today the two agree; a test in
`internal/core/project/config` pins `deployStepKnownFields == reflected tags
of DeployStep` (and the parallel-group pair) so a future drift fails a test
instead of advertising a field the loader rejects. The reflection helper is
exported from `yamlstrict` for exactly this test (`yamlstrict.AllowedFields(t
reflect.Type) []string`).

Allowed-set index: walk `reflect.TypeOf(out).Elem()` recursively through
struct fields (yaml tag name, skipping `yaml:"-"` and unexported; `,inline`
flattens the embedded struct's tags into the parent), pointers, slices,
arrays, maps (element type), with a visited set against cycles. Key is
`t.String()`. Untagged exported fields use yaml.v3's default (lower-cased
field name).

Line parsing: `^(?:line (\d+): )?field (\S+) not found in type (\S+)$` applied
to each `TypeError.Errors` line AND to a plain error's text (the custom
`UnmarshalYAML` path). `checkKnownFields` starts producing
`line %d: field %s not found in type %s` from `value.Content[i].Line`;
first-unknown-stops behaviour is unchanged.

Call-site rule: replace the `yaml.NewDecoder` + `KnownFields(true)` +
`Decode` triple with `yamlstrict.Decode(data, &cfg, path)`; drop the caller's
`parse %s: %w` / `load %s: %w` / `YAML parse error: %w` prefix on **that**
error (the file is now in the text); keep the prefix on the follow-up shape
validation errors (`validatePhaseSteps`, etc.). `path` is the project-relative
path where the caller has it, otherwise whatever the caller already prints.
Two exceptions:

- `model.ParseCommandFile(data)` keeps its signature (it is re-exported from
  `usercommands`) and passes `file = ""`; `loader.ParseCommandFile`'s existing
  `parse command file %s:` wrap keeps supplying the name.
- `manifest.Load` switches from `os.Open` + decoder to `os.ReadFile` +
  `yamlstrict.Decode`, preserving the `ErrManifestMissing` + `os.ErrNotExist`
  double-wrap and the `io.EOF` → "manifest is empty" branch.

Root-key message (`layers.go:387`) becomes:

```
workspace.yml: unknown top-level key "db" — move custom values under "vars:" (e.g. vars.db.*); allowed top-level keys: …; a key you did not invent may come from a newer dwe version — check `dwe version`
```

## Implementation Steps

### Task 1: Scanner marks volumes acknowledged by `shared: true`

**Files:**
- Modify: `internal/core/project/config/compose_scan.go`
- Modify: `internal/core/project/config/compose_scan_test.go`

- [ ] add `Shared bool` to `IsolationFinding` (godoc as in Technical Details); update `ScanComposeIsolation`'s godoc (`:87-97`) — it now also reads `workspace/docker.yml` + `docker.local.yml`, still without importing `diag`/`validate`
- [ ] `scanNamedEntity`: set `Value: explicit` on the `named_*` finding
- [ ] `ScanComposeIsolation`: load `LoadDockerConfigOrEmpty(projectRoot, cfg)`; on error use an empty set; build `sharedNames` from `Resources.Volumes` entries with `Shared` (`ResolveName("")`)
- [ ] after the collapse loop, compute each volume key's effective name (surviving `KindNamedVolume.Value`, else key) and set `Shared = true` on its surviving `external_volume` / `named_volume` findings; never on networks, never on blocking kinds
- [ ] table tests: shared match by explicit `name:`; match by key when compose has no `name:`; `name:` mismatch (not shared); `shared: false` (not shared); absent `docker.yml` (not shared); network with the same name (not shared); a later overlay `!reset`ting `name:` changes the effective name and the match
- [ ] test that `Blocking` and `Message` are unchanged for shared findings
- [ ] run `go test ./internal/core/project/config/...` — must pass before task 2

### Task 2: Scanner consumers honour `Shared`; docs + changelog for item 1

**Files:**
- Modify: `internal/core/validate/tests/tests.go`
- Modify: `internal/core/validate/tests/tests_test.go`
- Modify: `internal/core/workflow/envtest/runner.go`
- Modify: `internal/core/workflow/envtest/runner_test.go`
- Modify: `internal/cli/test/profile.go`
- Modify: `internal/cli/test/list_test.go`
- Modify: `docs/reference/config/tests.md`, `docs/i18n/ru/reference/config/tests.md`
- Modify: `docs/internals/packages.md` (§ `compose_scan.go` entry)
- Modify: `CHANGELOG.md`

- [ ] `tests.go`: skip `f.Shared` findings before building the `tests.isolation` warning
- [ ] `runner.go` `scanComposeIsolationGate`: skip `f.Shared` findings before the warn / blocking loop
- [ ] `profile.go`: add `Shared bool \`json:"shared,omitempty"\`` to `testIsolationFindingJSON`, populate from `f.Shared`
- [ ] tests: validate emits no `tests.isolation` row for a shared volume and still emits one for an unacknowledged one; runner gate prints no warning for shared; `list_test.go`: `TestRunTestList_CostProfileHeavyProject` (`:184`) now expects `shared: true` on its `composer-cache` finding (it already declares the matching `shared: true` volume), and a new case asserts the key is absent for an unacknowledged finding
- [ ] `tests.md` EN + RU: kinds table gets a sentence on `shared: true` acknowledgement; "Compose isolation scanner" section explains the match rule (effective name vs `docker.yml` shared volume name) and that networks are not covered; `:352`, `:368`, `:369` ("every finding is printed as a warning" / "regardless of `Blocking`") rewritten to exclude acknowledged findings; `cost_profile` `isolation_findings` row documents `shared`; limitations bullet `:406` qualified
- [ ] `packages.md`: scanner entry (`:83`) — add `Shared` (and the missing `Value`) to the field list, one sentence on the `docker.yml` read and the match rule
- [ ] `CHANGELOG.md` `### Changed`: isolation scanner no longer warns on volumes declared `shared: true` in `docker.yml`; `dwe test list --output json` marks them `"shared": true`
- [ ] `make build` (embedded docs + hashes), `make test` — must pass; commit 1: `fix(tests): isolation scanner honours shared volumes`

### Task 3: `PipelineFileState` and the three `*WithState` loaders

**Files:**
- Modify: `internal/core/project/config/workspace.go`
- Modify: `internal/core/project/config/workspace_test.go`

- [ ] add `PipelineFileState` (`Authored`, `DefaultFallback`) next to the pipeline loaders, godoc referencing `InfoConfigState`
- [ ] `loadDeployConfigDecode`, `loadProjectDeployConfigDecode`, `LoadLifecycleConfig`: thread the `io.EOF` outcome out as a state (internal helper shape `(cfg, state, err)`); public `LoadProjectDeployConfig`, `LoadResetConfig`, `LoadLifecycleConfig`, `ParseDeployConfigForValidation` keep their signatures. `loadServiceDeployConfigDecode` is deliberately NOT threaded — no validator reports per-service pipeline state
- [ ] add `ParseDeployConfigForValidationWithState`, `LoadLifecycleConfigWithState`, `LoadResetConfigWithState`
- [ ] tests: absent → `os.ErrNotExist`; all-comment and empty → `DefaultFallback`, zero cfg, nil error; authored → `Authored`; a syntax error still errors; each existing public loader's behaviour unchanged (existing tests keep passing)
- [ ] run `go test ./internal/core/project/config/...` — must pass before task 4

### Task 4: Validators report the three pipeline states

**Files:**
- Modify: `internal/core/validate/config/workspace.go`
- Modify: `internal/core/validate/config/workspace_test.go`
- Modify: `internal/cli/validate/testdata/validate_config.json.golden` (+ any sibling golden carrying the old text)
- Modify: `docs/reference/config/validate.md`, `docs/i18n/ru/reference/config/validate.md`
- Modify: `docs/internals/packages.md` (§ Core — Foundation pipeline loaders, § Core — Validation)

- [ ] `deployValidator`, `lifecycleValidator`, `resetValidator` switch to the `*WithState` loaders; absent → info `no deploy.yml — built-in default pipeline is active` (deploy, lifecycle only; reset keeps its silent branch and comment); `DefaultFallback` → info `<file> has no active content (all comments or empty) — built-in default pipeline is active` (all three); `Authored` → OK
- [ ] keep the deploy / reset `ResolvePlan` cross-check running after the info row in the fallback state
- [ ] tests: per validator, the three states produce the expected severity + message; the all-comment deploy.yml case explicitly asserts **not** OK
- [ ] regenerate goldens with `UPDATE_GOLDEN=1`, diff by eye — only the two message strings move
- [ ] `validate.md` EN + RU: extend the `config.info` paragraph (`:86`) to the three pipeline files (absent / no active content / authored), rewrite the reset sentence (`:90`): absence stays silent, an all-comment file is reported
- [ ] `packages.md`: pipeline-loader paragraph gains `PipelineFileState` + the three `*WithState` siblings (the `io.EOF` tolerance is now observable, not just swallowed); § Core — Validation gains the three-state rule for `config.deploy` / `config.lifecycle` / `config.reset`. `AGENTS.md` is NOT touched here — its budget is handled once in task 12
- [ ] run `go test ./internal/core/validate/... ./internal/cli/validate/...` — must pass before task 5

### Task 5: `builtins.md` — predicate builtins are `check:`-only; commit 2

**Files:**
- Modify: `docs/reference/config/deploy/builtins.md`
- Modify: `docs/i18n/ru/reference/config/deploy/builtins.md`
- Modify: `CHANGELOG.md`

- [ ] lines 7, 28, 305 (RU 9, 30, 307): "`check:`/`when:`" → "`check:`"; add one sentence after `:7` linking `[two type: builtin registries](../conditions.md#two-type-builtin-registries)` (EN) / `(../conditions.md#два-регистра-type-builtin)` (RU): `when: {type: builtin}` takes the predicate registry (`dir-exists`, `file-missing`, …), never these builtins; confirm with `make build` that no dangling-link warning is printed
- [ ] leave `:339` (`source_clone` gates itself) as is
- [ ] `CHANGELOG.md` `### Changed`: `dwe validate` names the built-in default pipeline when `deploy.yml` / `lifecycle.yml` / `reset.yml` are absent or all-comment (the scaffold's inert `deploy.yml` no longer reports OK); docs fix for predicate builtins
- [ ] `make build`, `make test` — must pass; commit 2: `feat(validate): report the default-pipeline state for deploy/lifecycle/reset`

### Task 6: `config.exports` validator

**Files:**
- Create: `internal/core/validate/config/exports.go`
- Create: `internal/core/validate/config/exports_test.go`
- Modify: `internal/core/validate/config/all.go`

- [ ] implement `exportsValidator` (`ID() "exports"`, `Domain() "config"`): iterate `ctx.Cfg.Exports.Env`, skip reserved names, check `From` and non-empty `When` with `config.ResolvePath(ctx.Cfg.Raw, …)`; warning with the message / hint shapes from Technical Details; File from `config.LoadRawLayers(ctx.ConfigPath)` (first layer whose `exports.env` entry has this `name`), fallback `relPath(ctx.ProjectRoot, ctx.ConfigPath)`, and `workspace.yml` under `ctx.ProjectRoot` when `ConfigPath` is empty
- [ ] no `SeverityOK` row when everything resolves (the `template_refs` / `ports_exports` convention) — goldens stay unchanged in this task
- [ ] register in `all.go`
- [ ] tests: resolving `from` → no diag; missing `from` without default → warning + "renders empty" hint; with default → "default is always used"; `required: true` → "fails"; missing `when` → warning; nil-valued present key → no diag; File points at `local.yml` when the rule lives there; `ConfigPath == ""` → File is `workspace.yml`
- [ ] run `go test ./internal/core/validate/... ./internal/cli/validate/...` — must pass before task 7

### Task 7: `commands` validator checks `default_from` / `options.from` / `context.from`

**Files:**
- Modify: `internal/core/validate/commands/commands.go`
- Modify: `internal/core/validate/commands/commands_test.go`

- [ ] inside the existing per-parsed-file loop (`commands.go:104-106`, which never sees daemon-expanded synthetic commands), warn on each of `params.<p>.default_from`, `params.<p>.options.from`, `context.<c>.from` that does not resolve in `cfg.Raw`; Target = the existing `paramTarget` (`commands:<id>:params.<name>`) and `commands:<id>:context.<name>`; File = `relFile`
- [ ] keep the existing default-in-options check as is (its `canCheck = false` branch now sits under the new warning)
- [ ] tests: each of the three fields, resolving vs not; a command with both `default_from` miss and options mismatch yields the warning and not a spurious error; `cfg == nil` → no diags
- [ ] run `go test ./internal/core/validate/commands/...` — must pass before task 8

### Task 8: `dwe render env` warns on rules that render empty; docs + commit 3

**Files:**
- Modify: `internal/shared/envfile/render.go`
- Modify: `internal/shared/envfile/render_test.go`
- Modify: `internal/cli/render/env.go`
- Modify: `internal/cli/render/env_test.go`
- Modify: `docs/reference/render/env.md`, `docs/i18n/ru/reference/render/env.md`
- Modify: `docs/reference/config/validate.md`, `docs/i18n/ru/reference/config/validate.md`
- Modify: `docs/internals/packages.md` (§ Core — Validation, § `envfile`)
- Modify: `CHANGELOG.md`

- [ ] `envfile.UnresolvedRules(cfg) []UnresolvedRule` — same skip rules as `BuildContent` (reserved names, `When` falsy), then `Default == "" && !Required && !found`
- [ ] `runRenderEnv(cmd, flags, outputPath)`: take the `*cobra.Command`, write content via `cmd.OutOrStdout()` (replacing the bare `fmt.Print`), and before rendering print each unresolved rule as `warning: exports.env[NAME]: from "…" does not resolve — rendered empty` to `cmd.ErrOrStderr()`, gated by `flags.Output != "json"`; both the stdout and `--out` paths
- [ ] tests (`envfile`): resolving rule, missing with default, required, `when` falsy, format bool/int with missing path — only the empty-render case is returned
- [ ] tests (`cli/render`, new — `env_test.go` currently covers only `BuildContent`): build the cobra command against a temp project with `cmd.SetOut` / `cmd.SetErr` buffers; warning lands on the err buffer and the out buffer is byte-identical to `BuildContent`; no warning under `--output json`
- [ ] `env.md` EN + RU: "Evaluation order" / "Value resolution" gains the warning; `validate.md` EN + RU: `config.exports` paragraph next to `config.template_refs` (`:78`), `commands` paragraph mentions the three dot-path fields
- [ ] `packages.md`: one sentence each in § Core — Validation (`config.exports`, commands dot-path checks) and the `envfile` entry (`UnresolvedRules` is pure; the CLI is the writer)
- [ ] `CHANGELOG.md` `### Added`: `dwe validate` warns on an `exports.env` / `params` / `context` dot-path that does not resolve; `dwe render env` warns on stderr for a rule rendered empty
- [ ] `make build`, `make test` — must pass; commit 3: `feat(validate): warn on unresolvable from:/default_from: dot-paths`

### Task 9: `internal/shared/yamlstrict` package

**Files:**
- Create: `internal/shared/yamlstrict/yamlstrict.go`
- Create: `internal/shared/yamlstrict/yamlstrict_test.go`

- [ ] `Decode(data, out, file)`: `yaml.NewDecoder` + `KnownFields(true)`; `io.EOF` returned untouched; `*yaml.TypeError` → `*Error`; a plain error matching the unknown-field regex → `*Error` with one `UnknownField`; any other error → `fmt.Errorf("%s: %w", file, err)` (no prefix when `file == ""`)
- [ ] exported `AllowedFields(t reflect.Type) []string` + the allowed-set index by reflection (`reflect.Type.String()` → tags; struct / pointer / slice / array / map recursion; `,inline` flattening; `yaml:"-"` and unexported skipped; untagged → yaml.v3's lower-cased default; cycle guard)
- [ ] `Error.Error()` exactly as in Technical Details (one line per unknown field, `Other` lines verbatim with file prefix, hint once at the end, `file:line:` / `file:` / `line N:` prefix forms); `Unwrap()` returns the original error
- [ ] tests: single unknown field with line and allowed set; nested type (slice of struct, map of struct, pointer) resolves the right allowed set; `,inline` fields appear in the parent's set; two unknown fields → two lines, one hint; type not reachable → no allowed clause; empty `file` → no prefix; `io.EOF` passthrough (`errors.Is`); syntax error passthrough with file prefix; plain-error form (a fixture type whose `UnmarshalYAML` returns `line 4: field x not found in type pkg.T`); `errors.As(err, *yaml.TypeError)` works through `Unwrap` on the `TypeError` path (the plain-error path unwraps to the plain error — assert that too)
- [ ] run `go test ./internal/shared/yamlstrict/...` — must pass before task 10

### Task 10: Migrate the `config`-package loaders; `checkKnownFields` gains a line

**Files:**
- Modify: `internal/core/project/config/workspace.go`
- Modify: `internal/core/project/config/snapshot.go`
- Modify: `internal/core/project/config/validate.go`
- Modify: `internal/core/project/config/workspace_test.go`, `snapshot_test.go`, `validate_test.go`
- Modify: `internal/core/validate/config/validate_yml_test.go`

- [ ] `checkKnownFields`: `fmt.Errorf("line %d: field %s not found in type %s", value.Content[i].Line, key, typeName)`
- [ ] replace the decoder triple at `workspace.go:2376` (service.yml — keep the `loading service %q definition:` context as an outer wrap), `:3103`, `:3158`, `:3183`, `:3208`, `snapshot.go:152`, `validate.go:200` with `yamlstrict.Decode(data, &cfg, path)`; drop the now-redundant `parse %s:` on that error, keep it on shape-validation errors
- [ ] confirm the four pipeline loaders' `errors.Is(err, io.EOF)` branch still fires (task 3's state tests cover it)
- [ ] add the drift test: `deployStepKnownFields` == `yamlstrict.AllowedFields(reflect.TypeFor[DeployStep]())` and `parallelGroupKnownFields` == the `ParallelGroup` set (sorted, exact)
- [ ] update tests that pin the old text (`validate_yml_test.go:65-69`; grep `not found in type` and `parse workspace/` across `*_test.go` in these packages); add one unknown-field assertion per migrated loader checking file + field + an allowed entry
- [ ] run `go test ./internal/core/project/config/... ./internal/core/validate/...` — must pass before task 11

### Task 11: Migrate the remaining strict loaders

**Files:**
- Modify: `internal/core/usercommands/model/types.go` (+ `types_test.go`)
- Modify: `internal/core/execution/templates/manifest/manifest.go` (+ test)
- Modify: `internal/core/workflow/envtest/scenario.go` (+ test)
- Modify: `internal/core/workflow/setup/loader.go` (+ test)
- Modify: `internal/shared/i18n/loader.go` (+ test)

- [ ] `types.go:1697`: `yamlstrict.Decode(data, &cf, "")` — signature of `ParseCommandFile` unchanged (re-exported at `usercommands.go:218`), drop `YAML parse error:`; `loader.go:101`'s `parse command file %s:` wrap keeps naming the file. Note the lenient first pass already rejects top-level per-command unknowns, so the new text appears for nested unknowns and type-less commands only
- [ ] `manifest.go:62-76`: `os.ReadFile` + `yamlstrict.Decode`, preserving the `ErrManifestMissing`/`os.ErrNotExist` double-wrap and the `io.EOF` → "manifest is empty" branch; `scenario.go:85` (keep the "scenario file is empty or invalid" wrap — `io.EOF` is deliberately NOT tolerated there); `setup/loader.go:31`
- [ ] `i18n/loader.go`: `parseBundle(data []byte, file string)`; `Load` passes the embedded name, `LoadProjectBundles` the project-relative path; `io.EOF` → empty bundle as today
- [ ] update tests pinning the old strings in each package (incl. the synthetic strings at `validate/commands/commands_test.go:741-743` and `cli/deploy/menu_test.go:141`, rewritten to the new shape); add one unknown-field assertion per loader
- [ ] `go build ./...` and `git grep -nE '\.KnownFields\(true\)' -- internal ':!*_test.go'` → only `internal/shared/yamlstrict/` (the two test-local strict decoders in `internal/shared/i18n/coverage_test.go:22,68` are assertions, not loaders, and stay as they are)
- [ ] run `make test` — must pass before task 12

### Task 12: Root-key hint, docs, internals; commit 4

**Files:**
- Modify: `internal/core/project/config/layers.go` (+ `layers_test.go`)
- Modify: `docs/reference/config/workspace.md`, `docs/i18n/ru/reference/config/workspace.md`
- Modify: `docs/reference/config/snapshot.md`, `docs/i18n/ru/reference/config/snapshot.md`
- Modify: `docs/internals/packages.md`
- Modify: `AGENTS.md`
- Modify: `CHANGELOG.md`

- [ ] `layers.go:387`: append `; a key you did not invent may come from a newer dwe version — check \`dwe version\``; update its test
- [ ] `workspace.md:108` example (EN + RU) shows the new root-key text — the example is already stale (it omits the `; allowed top-level keys: …` clause), so the diff is the whole line, not one clause; add a short "Unknown field errors" note near it with the `yamlstrict` message shape; `snapshot.md:88` sentence (EN + RU) points at the same shape
- [ ] `packages.md`: new `- \`internal/shared/yamlstrict/\`` bullet under § Shared — Leaf Infrastructure (single strict decoder; `io.EOF` passthrough is load-bearing for the pipeline loaders; the plain-error form from custom unmarshalers; allowed set by reflection keyed on `reflect.Type.String()`; the `AllowedFields` drift test for hand lists; the empty-`file` form for `ParseCommandFile`); one sentence in the `config/` loaders paragraph pointing at it
- [ ] `AGENTS.md` "YAML loader strictness" bullet (`:80-81`): **rewrite, do not append** — the file is 40920 B against a 40960 B budget. Replace the second line ("Exactly four strict *pipeline* loaders also tolerate `io.EOF` …") with a shorter one that says: every strict decode goes through `yamlstrict.Decode`; the four pipeline loaders' `io.EOF` tolerance is surfaced as `PipelineFileState`; and add `§ \`internal/shared/yamlstrict/\`` to the See line. Net byte change must be ≤ 0; `go test ./internal/cli/docs -run 'TestAgentsMdBudget|TestAgentsMdPointersResolve'` green
- [ ] `CHANGELOG.md` `### Changed`: unknown-field errors from every strict config file now name the file, line, field and the allowed fields, with a newer-version hint; the root-key error carries the same hint
- [ ] `make build`, `make test`, `make lint` — must pass; commit 4: `refactor(config): wrap strict-loader unknown-field errors`

### Task 13: Verify acceptance criteria

- [ ] item 1: on `~/Projects/beetDeck`, `bin/dwe validate tests` → 0 warnings; `bin/dwe test list --output json` shows `npm_cache` / `pip_cache` with `"shared": true`; temporarily flip one `shared: true` to `false` → the two warnings return
- [ ] item 2: `bin/dwe init` into a temp dir, `bin/dwe validate config` → `config.deploy` is **info** "has no active content"; delete the file → info "no deploy.yml — built-in default pipeline is active"; `reset.yml` absent → no row
- [ ] item 3: on beetDeck, misspell one `exports.env[].from` → `bin/dwe validate config` warns with the "renders empty" hint; `bin/dwe render env` prints the stderr warning and `NAME=` on stdout; `--output json` prints no warning; revert
- [ ] item 4: add `defaults: {}` at the top of beetDeck's `workspace/deploy.yml` → `bin/dwe deploy plan` prints `workspace/deploy.yml:N: unknown field "defaults" — allowed here: …` plus the hint; in a command file use a **nested** unknown (`params.<p>.widgett: input` — a top-level one is caught by the lenient first pass with its own message) and in `service.yml` a top-level unknown; check the multi-line text reads well in the bare `dwe` menu path too; revert
- [ ] `git grep -nE '\.KnownFields\(true\)' -- internal ':!*_test.go'` → only `internal/shared/yamlstrict/` (the two test-local strict decoders in `internal/shared/i18n/coverage_test.go:22,68` are assertions, not loaders, and stay as they are)
- [ ] run full suite: `make build && make test && make lint`
- [ ] `go test -race` on the touched packages (`make test-race` if time allows)

### Task 14: [Final] Update documentation

- [ ] re-read the four `CHANGELOG.md` entries as a block — user-facing wording, no internals
- [ ] `docs/internals/packages.md` sections from tasks 2, 8, 12 are consistent with the code as landed
- [ ] `AGENTS.md` unchanged beyond the one pointer; `TestAgentsMdBudget` green
- [ ] `README.md` needs nothing (no new command or flag)
- [ ] open PR `feat/diagnostics-and-messages` → `release/0.6.0`; move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — informational only.*

**Manual verification**
- Run `dwe validate` (all scopes) in the other live workspaces (laravel,
  magento, alto, cueBreaker, AlbFetcharr) and confirm: the `tests.isolation`
  warnings are gone where the volume is `shared: true`; no new `config.exports`
  / `commands` warnings that are false positives (a real one is a bug in that
  workspace, not in dwe — note it there).
- Confirm the new unknown-field message reads well in the deploy menu path
  (`dwe` bare, project with a typo in `deploy.yml`) — it is multi-line now.

**External updates**
- The upgrade page for this release should mention: the scaffold's inert
  `deploy.yml` now reports at info instead of OK; new warnings may appear in
  projects with a stale `from:`; unknown-field error text changed (any script
  grepping for `not found in type` breaks).
- `skills/dwe/**` and `AGENTS.md.tmpl` regeneration happens in the release
  wrap-up, not here. One row to carry there explicitly:
  `skills/dwe/references/integration-tests.md:23` treats a non-empty
  `isolation_findings` as a hard stop; after item 1 an entry with
  `"shared": true` is acknowledged and must not stop the agent.
