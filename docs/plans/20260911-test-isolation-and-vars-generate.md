# 0.6.1: host-script isolation in `dwe test`, `vars set --generate`

## Overview

Two independent changes ship on one branch (`feat/0.6.1-test-isolation`, cut
from `release/0.6.1`) as four commits plus a close-out commit that moves this
plan to `docs/plans/completed/`, one PR into `release/0.6.1` (milestone 0.6.1).
Commits in order:

**Commit 1 — the copy's compose project name reaches host shell steps and
scripts.** `dwe test` isolates its disposable copy through a unique compose
project name, but a host script that builds `docker compose -p` itself can still
address the live stack. The recommended project-side fix is
`PROJECT="${COMPOSE_PROJECT_NAME:-dwe-myproj}"` — and today it does not work
inside `dwe test`: `ScrubComposeEnv` strips every `COMPOSE_*` from the process,
nothing re-sources `.env` on that path, and the in-process `type: shell`
scenario step inherits no `COMPOSE_PROJECT_NAME`, so the fallback resolves to
the **live** name. The `type: script` contract lacks the variable everywhere,
even under a plain `dwe cmd`. After the fix, pipeline shell steps (and shell
`check:`) derive the name from the config they run with, and the `type: script`
contract carries the same `COMPOSE_PROJECT_NAME` / `COMPOSE_FILE` pair the
`type: shell` command contract already has.

**Commit 2 — `tests.host_project_name` warning.** `dwe validate` flags a host
script or shell step that passes compose a project name not derived from
`$COMPOSE_PROJECT_NAME`. Validate-only, only in projects with
`workspace/tests/`.

**Commit 3 — documented isolation boundary.** What `dwe test` guarantees, which
child processes see the copy's name, what is not isolated, and the recipe for
host scripts.

**Commit 4 — `dwe vars set <var> --generate hex[:N]|base64url[:N]|uuid`.**
Writes a freshly generated random value to `local.yml`, refusing to overwrite an
existing local value without `--force`. Replaces the copy-paste python
one-liners setup wizards currently put into question descriptions.

Commits 1, 2 and 4 are user-observable → each carries its own `CHANGELOG.md`
`## [Unreleased]` entry; commit 3 is documentation only and adds none. Every
commit carries its own EN + RU docs.

### Non-goals

Decided during design; do not re-litigate during implementation:

- **No `.env` sourcing and no `os.Setenv` in the test runner.** `--parallel`
  runs scenarios as goroutines in one process with different copy names, so a
  process-global variable cannot carry them. The name is derived per spawn from
  the config the step runs with.
- **Pipeline shell steps get `COMPOSE_PROJECT_NAME` only, not `COMPOSE_FILE`.**
  They never had `COMPOSE_FILE` (it is not a reserved `.env` export); adding it
  would change what a bare `docker compose` in a deploy step resolves.
- **Not covered, documented only:** shell `when:` predicates
  (`condition.EvalCmd`), the builtin `shell` probe, and anything launched
  outside dwe (a developer running the script by hand).
- **No diagnostic on `${COMPOSE_PROJECT_NAME:-<live name>}`.** The fallback is
  legitimate for scripts also run by hand. No suppression marker for the new
  warning (zero hits across local projects today; add one on the first false
  positive).
- **The validator never guesses.** Literal project names, positional `$1`,
  values not traceable within one hop, and nesting deeper than one referenced
  file are skipped, never flagged.
- **`--generate` is the CLI flag only.** A `generate:` key in `setup.yml` and
  `dwe secrets set --generate` are out of scope; the generator lives in a shared
  leaf so the former can reuse it later.

## Context (from discovery)

**Commit 1 — env**
- `internal/core/workflow/envtest/run.go:125-145` — `ScrubComposeEnv` unsets
  every `COMPOSE_*`, called at the top of `dwe test run` / `clean`
  (`internal/cli/test/run.go:138,216`, `clean.go:76`).
- `.env` is sourced into the process only by `internal/cli/deploy/deploy.go:661`
  and `internal/core/workflow/lifecycle/run.go:179,282` (`deploy.SourceDotEnv`,
  `internal/core/workflow/deploy/plan.go:115` — `os.Setenv`, so it overwrites an
  ambient value). The test runner's in-process `runSteps`
  (`envtest/runner.go:717-773`) sources nothing.
- `internal/core/execution/pipeline/executor.go:250-257` — `execShellAction`
  never sets `cmd.Env`. Shell `check:` goes through the same `ExecAction`
  (`executor.go:1017`). `ActionContext` carries `Cfg` and `DockerCfg`
  (`executor.go:106-110`, populated at `:937` and `:1007`).
- Every production `RunWithOptions` caller passes `DockerConfig`
  (`envtest/runner.go:760`, `lifecycle/phases.go:80`, `cli/deploy/deploy.go:846`,
  `cli/lifecycle/reset.go:260,496`); the deprecated `Run` / `ExecStep` wrappers
  (`executor.go:366,429-440`) do not. **`dwe reset step` bypasses
  `RunWithOptions`**: `resetStepCmd` builds its own `ActionContext` without
  `DockerCfg` and calls `ExecAction` for the body and the `check:`
  (`internal/cli/lifecycle/reset.go:762-790`).
- Paths that never sourced `.env` and therefore gain (or change) the variable:
  `dwe test`, `dwe stop`, the stop half of `dwe restart`, `dwe reset run`
  (incl. `--service`), `dwe reset step`. Deploy and run keep the same value.
- `internal/core/project/config/docker.go:273` `ResolveComposeProjectName`
  (loads docker config from disk, refuses a secret marker) and `:316`
  `ComposeProjectName(dockerCfg, cfg)` (in-memory, same precedence, both
  lowercase). The reserved `.env` export uses the former
  (`internal/shared/envfile/render.go:27`, resolver call ~`:64`). Known existing
  divergence, not introduced here: a non-string `project_name:` (e.g. `123`)
  reads as `FullName` in `readDockerProjectName` (`docker.go:389`) but as the
  string through `LoadDockerConfig`; `-p` already uses the in-memory side, and
  the injection matches `-p`.
- `files_gate.command` names a user command whose `files:` block is probed
  (`execution/filesgate/gate.go:106`) — a file probe, no shell spawn, so it is
  outside this change.
- The copy carries its own name in its config: `envtest.WriteDockerIdentity`
  (`runner.go:396`) writes `project_name` into the copy's
  `docker.yml`/`docker.local.yml`. That is why `type: command` scenario steps
  already see the right name.
- `internal/core/usercommands/runtime/runners/host/host.go:98-138` —
  `hostContractEnv` exports `DWE_BIN`, `COMPOSE_PROJECT_NAME` (omitted when
  empty), `COMPOSE_FILE` (absolute, omitted when empty) from `rc.Compose()`
  (`spec/runner.go:141`). Its comment at `:107` claims the script contract has
  the same variables — false today.
- `internal/core/usercommands/runtime/runners/script/script.go:84-152` —
  `buildContractEnv` returns only `DWE_*`; `execScript` builds
  `os.Environ() + env: + contract + colour` (`:191-206`).
- `rc.DockerConfig` for an in-pipeline `type: command` is set by
  `usercommands/runtime/build_context.go:146`.
- Three entry points build a `RunContext` by hand WITHOUT `DockerConfig`:
  snapshot workflows (`internal/core/workflow/snapshot/exec.go:115`),
  service-toggle hooks (`internal/cli/service/service_plan.go:527`) and reset
  hooks (`internal/cli/lifecycle/reset.go:642`); workflow leaves propagate it
  unchanged (`runners/workflow/step.go:101`). With a nil `DockerConfig`,
  `RunContext.Compose()` (`spec/runner.go:141-155`) falls back to
  `project.prefix/name` and ignores `docker.yml project_name`. On these paths
  the same fallback also drives container leaves: `service_exec` /
  `service_run` build their compose from `rc.Compose()`
  (`runners/service/exec.go:93`, `run.go:43`), so today a project with
  `project_name: custom` execs into the wrong compose project there and misses
  `docker.yml` `args.*` and `ProcessEnv` (`shared/docker/compose.go:70-105`).
  The `type: shell` contract is wrong the same way; the new script contract
  would inherit it.
- `internal/shared/bridgeclient/env.go:47-51` — comment says `execShellAction`
  never assigns `cmd.Env`; `DWE_NESTED_RUNTIME` still reaches the child because
  the new env is built from `os.Environ()` at spawn time, but the comment must
  be updated.
- Docs: `docs/reference/config/commands/types.md:77-93` (the `type: shell`
  contract table and its notes), `:174` (the `type: script` table);
  `docs/reference/config/deploy/steps.md:15` (pipeline `type: shell` steps —
  currently says nothing about their environment).

**Commit 2 — validator**
- `internal/core/validate/tests/tests.go:34` — `All()` returns only
  `scenariosValidator`; `Run` returns nil when `workspace/tests/` is absent
  (`:52-57`); the registry comes from `ctx.CommandRegistry.(*registry.Registry)`
  (`:79`).
- Pipeline walk precedent: `internal/core/validate/config/parallel_groups.go`
  (project + per-service deploy, lifecycle run/stop, reset). Loaders:
  `config.LoadServiceDeployConfigs` (`workspace.go:3491`),
  `LoadServiceResetConfigs` (`:3529`), `LoadLifecycleConfig` (`:3115`),
  `LoadResetConfig` (`:3318`), `LoadProjectDeployConfig` (`:3281`, the
  standalone loader for `workspace/deploy.yml`; the precedent validator reads
  `ctx.Cfg.Deploy` instead). Steps are
  `config.DeployStep` with `Parallel` sub-steps and `Check *Action`
  (`workspace.go:446`).
- Scenario steps: `envtest.LoadScenario` (already called per file by
  `scenariosValidator.validateFile`).
- User commands: NOT from the registry — it keeps no source file per command
  (`FilePath` lives on `model.CommandFile`, `model/types.go:1626`) and its
  listing drops `BridgeHidden` commands, so `dwe validate` inside a container
  would scan a different set. Parse the command files the way
  `internal/core/validate/commands/commands.go:85-110` does (`parsedFiles`,
  `cf.FilePath`, `sortedCommandNames(cf)`); that covers hidden commands and
  needs no registry.
  The host runner executes `model.CommandTypeShell` (`host.Runner`;
  `CommandTypeDwe` → `host.DweRunner`, `runtime/runner.go:110-113`); a shell
  command carries `cmd:` or `argv:` (`allowedFieldsFor(CommandTypeShell)`).
  `model.CommandDef{Type, Cmd, Script *ScriptDef}` (`model/types.go:798-872`),
  `ScriptDef{Shell, Path, Plan, Run, Cleanup}` (`:635`).
- Validation framework contract (AGENTS.md "Validation framework"): per-file,
  independent validators; the `tests` domain is validate-only.

**Commit 3 — docs**
- `docs/reference/config/tests.md`: `## Isolation model` (:162),
  `## dwe validate tests` (:341), `## Compose isolation scanner` (:358),
  `## Documented limitations` (:460).
- `docs/guides/integration-tests.md`: `## Resolving an isolation failure`
  (:168) — the new section goes after it.
- `docs/reference/config/validate.md:51` — the `tests.*` domain row.
- `docs/guides/upgrading.md` — `## Upgrading to 0.6.1` (:33) already has
  `### Integration tests` (:74).
- `docs/internals/packages.md` — § envtest bullet (:108), § validate/tests
  bullet (~:173), the `DWE_NESTED_RUNTIME` paragraph (:315, mentions
  `execShellAction`).
- RU mirrors exist for every page above under `docs/i18n/ru/`.

**Commit 4 — `--generate`**
- `internal/cli/vars/set.go` — `newVarsSetCmd` (:39), `runVarsSet` (:86,
  coerces through `varsusage.CoerceScalar` at :114), `writeVarOverride` (:175,
  takes the locks), `writeVarOverrideCore` (:211, container gate + write +
  rollback).
- `CoerceScalar` turns short hex into numbers (`1234` → int, `12e4` → float), so
  a generated value must bypass it; the node writer already quotes number-like
  strings (`internal/cli/vars/set_test.go:51`, `quoted-int-stays-string`).
- Overwrite check: `config.ResolveLayeredPath` → `LayeredValue{LocalOK, Local}`
  (`config/layers.go:568-622`; an explicit null is present-but-nil).
- Precedent for the mutual-exclusion error: `secrets_value_ambiguous`
  (`internal/cli/secrets/set.go:218`).
- No reusable random-value generator exists (`crypto/rand` is used only for
  local IDs in snapshot/envtest/bridgeproto).
- Docs: `docs/reference/config/vars.md` (`### dwe vars set` :107, `## JSON
  output` :298, `## Container behavior` :319) + RU; `docs/reference/config/setup.md`
  question-field table (:107-116) + RU.

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
  `go test ./internal/...`; `make lint && make test` before each commit)
- maintain backward compatibility
- follow the AGENTS.md critical patterns, in particular: compose project name
  comes from the single resolver (`config.ComposeProjectName` /
  `ResolveComposeProjectName`, never re-derived inline); `dwe test` runner tests
  stub `execDweFunc`; validators are per-file and independent; `local.yml` is
  written only through `local/local_node.go` (via `writeVarOverrideCore`);
  JSON-mode errors are typed `cmdctx.Err`

## Testing Strategy

- **unit tests**: required for every task; table-driven for the shell-text
  scanner, the `--generate` grammar and the generator
- **no e2e UI tests** in this project; live verification on local projects is in
  Post-Completion
- child-process env is asserted by running a real `sh -c 'printf …'` through
  the pipeline/runner with a captured writer — no Docker involved.
  `envtest.runSteps` touches no `Runner` field and spawns no `dwe` subprocess,
  so a fixture copy plus the package's `noopReporter`
  (`envtest/runner_test.go:24`) and a per-run captured writer is enough (new
  fixture — no existing test calls it). Never `pipeline.NewPlainReporter` in a
  concurrent test: it installs process-global prompt hooks and supports one
  reporter per process (`pipeline/plain.go:170`). The concurrent case runs two
  goroutines over two temp copies inside one test (never `t.Parallel` together
  with `t.Setenv`), under `go test -race`

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

### Commit 1 — derive the name per spawn

`execShellAction` builds `cmd.Env = append(os.Environ(),
"COMPOSE_PROJECT_NAME="+name)` where `name =
config.ComposeProjectName(actx.DockerCfg, actx.Cfg)`, **only when
`actx.DockerCfg != nil` and `name != ""`**; otherwise `cmd.Env` stays nil
(inherit, today's behaviour). The `DockerCfg` guard matters: without the docker
config the in-memory resolver falls back to `project.prefix/name` and would
overwrite a correct `.env`-sourced value that came from `docker.yml
project_name`. Every production `RunWithOptions` caller passes it; `dwe reset
step`, which calls `ExecAction` directly, is fixed to load it
(`config.LoadDockerConfigOrEmpty(workDir, cfg)`) so the same step behaves the
same under `reset step` and `reset run`. Only the deprecated `Run` / `ExecStep`
wrappers stay without it.

Paths that never sourced `.env` — `dwe test`, `dwe stop`, the stop half of
`dwe restart`, `dwe reset run` / `reset step` — now get the config value, which
overrides an ambient `COMPOSE_PROJECT_NAME` from the developer's shell. That is
the intent (it is the name dwe passes as `-p`) and goes into the CHANGELOG.

Invariant (stated in a comment and pinned by a test): wherever `.env` was
sourced (deploy, run), the injected value equals the one already in the process,
because the reserved export is computed by `ResolveComposeProjectName` over the
same docker config and project config. So deploy/run behaviour does not change;
`dwe test` and any path that never sourced `.env` gain the variable.

The `type: script` contract gets the compose pair through one shared helper,
`runio.ComposeContractEnv(rc spec.RunContext) []string`, extracted from
`hostContractEnv` (which keeps `DWE_BIN` and calls the helper). The script
runner appends it after `DWE_*`, so the contract still wins over `env:` on a key
collision, as documented for host.

The three hand-built `RunContext` entry points (snapshot workflows,
service-toggle hooks, reset hooks) load `config.LoadDockerConfigOrEmpty(baseDir,
cfg)` and set `DockerConfig`, returning a load error explicitly. Threading it at
the entry points — not lazily inside `RunContext.Compose()`, which has no error
path — keeps the contract equal to the `-p` dwe passes elsewhere. It is a
user-visible fix beyond the contract: on those three paths container commands
(`service_exec` / `service_run`) and builtins now use `docker.yml
project_name`, `args.*` and the docker process env, like every other path; the
existing `type: shell` contract there is fixed too.

### Commit 2 — `tests.host_project_name`

A second validator in the `tests` domain (`ID() == "host_project_name"`),
silent when `workspace/tests/` is absent, never in preflight. It gates ONLY on
that directory. Pipeline files load through their standalone loaders in every
case — `config.LoadProjectDeployConfig(path)` (`workspace.go:3281`),
`LoadResetConfig`, `LoadLifecycleConfig`, `LoadServiceResetConfigs(baseDir)`,
`envtest.LoadScenario` — so one code path serves both cases. With a nil
`ctx.Cfg` (failed merged-config load) only per-service `deploy.yml` files are
skipped (`LoadServiceDeployConfigs` needs `Cfg.Services`) — per the
"validators are per-file and independent" contract. It collects shell
text from a fixed set of sources, scans each with a pure function, and emits one
`SeverityWarning` per offending line, deduplicated by (file, location, line) —
location is the step address or command ID for YAML `cmd` text and empty for a
script file, so two offending steps in one `deploy.yml` are two warnings.

**Sources** (by reference, never a glob over `workspace/scripts/`, which would
miss scripts kept inside a service's source tree):

| source | text scanned | reported as |
|---|---|---|
| `type: shell` user commands (parsed command files, hidden ones included) | `cmd`; `argv` only when it is `[<posix shell>, -c, <payload>]` — then the payload (any other `argv` is exec'd without a shell, so `$…` never expands there) | the command file, command ID |
| `type: script` user commands (same parse) whose `script.shell` is empty or a POSIX shell | files `script.path` / `plan` / `run` / `cleanup` | `path:line` |
| `type: shell` steps and shell `check:` (incl. `parallel` sub-steps) in `workspace/deploy.yml`, per-service `deploy.yml` / `reset.yml`, `workspace/reset.yml`, `workspace/lifecycle.yml` run/stop, every scenario file | `cmd` | the YAML file, step address |
| a file an inline `cmd` above invokes as `bash <path>` / `sh <path>` / `./<path>` (one level, path relative to the project root, no `${…}` in it, must exist) | file content | `path:line` |

A file reached from several sources is scanned once. A referenced path is
resolved against the project root only — a command's `workdir:` is ignored, so
`./bin/x.sh` under a `workdir:` command is missed (documented limitation).

"POSIX shell" = basename `sh`, `bash`, `dash`, `ksh`, `zsh`; a `./<path>`
reference is scanned only when its shebang names one of them or it has no
shebang. Every file read (script paths and references alike) is confined, in
this order: a raw reference that is absolute (`filepath.IsAbs`) is skipped
before joining (`Join(root, "/etc/x")` would otherwise land inside the root);
the joined path must pass `pathsafe.ContainedRel` (rejects `../` escapes); the
root and the file are both resolved with `filepath.EvalSymlinks` and the real
file must pass `pathsafe.EnsureRealUnder` (on macOS `t.TempDir()` itself sits
under a symlinked `/var`, so resolving only one side fails); `os.Stat` (never
`Open` first — opening a FIFO blocks) must report a regular file of at most
1 MiB, anything larger is skipped whole. A file failing any check is skipped
silently — the validator reads the project, nothing else.

**Rule** (`scanShellText(text string) []hit`): see Technical Details. The
diagnostic message names the location, the offending value, and the fix:
`use "${COMPOSE_PROJECT_NAME:-<current value>}" — inside dwe test the current
value addresses the live stack`.

### Commit 3 — isolation boundary docs

A single "what is isolated" table in `tests.md` § Isolation model, a guide
section with the recipe, and the limitations list; `packages.md` records the
per-spawn derivation and the new validator.

### Commit 4 — `--generate`

A new leaf `internal/shared/randval`:

- `Parse(spec string) (Spec, error)` — `hex[:N]`, `base64url[:N]`, `uuid`; N is
  bytes of entropy, default 32, range 1..1024; `uuid` takes no N.
- `Generate(s Spec, r io.Reader) (string, error)` — `hex.EncodeToString`,
  `base64.URLEncoding` (padded — a Fernet key requires padding), UUID v4 from 16
  bytes (version/variant bits set per RFC 9562). The reader is `crypto/rand.Reader`
  in production and a fixed reader in tests.

`dwe vars set <var> --generate SPEC [--force]`:

1. path validation as today;
2. `--generate` with a positional value → `vars_value_ambiguous`; invalid spec →
   `vars_generate_invalid` (checked before any lock);
3. generate the value; it bypasses `CoerceScalar` and is written as a Go string;
4. under the project locks, before the write: if `local.yml` already holds a
   non-null value at the path and `--force` is absent → `vars_value_exists`
   with hint `pass --force to regenerate`. A value from `workspace.yml` or
   another lower layer does not block;
5. write, reload, confirm — same output as a plain `set` (the value is printed;
   `vars` output is never redacted), JSON `{var, value}`.

`--force` without `--generate` is a usage error (`vars_force_requires_generate`)
so the flag never silently does nothing. The interactive form never opens with
`--generate`, so the flag works in JSON and non-interactive mode.

## Technical Details

### Commit 1

- `execShellAction` comment: why the name is injected per spawn (the test
  runner scrubs `COMPOSE_*` and runs scenarios concurrently in one process) and
  why the `DockerCfg` guard exists.
- `runio.ComposeContractEnv` returns `COMPOSE_PROJECT_NAME=…` (omitted when
  empty) and `COMPOSE_FILE=…` (colon-joined, absolute against `rc.ProjectRoot`,
  omitted when no files) — byte-identical to today's `hostContractEnv` output.

### Commit 2 — scanner

1. Join `\`-newline continuations into logical lines, keeping the physical line
   number of the first line.
2. Drop comments: a `#` at the start of a word outside quotes ends the line.
   Skip here-doc bodies (`<<WORD` / `<<-WORD` … `WORD`).
3. Split each logical line into simple commands on `;`, `&&`, `||`, `|`, `&`
   and newline (outside quotes).
4. **Compose invocation — command position only:** skip leading `VAR=value`
   assignments and the wrappers `exec`, `command`, `env`, `sudo`, `time`,
   `nice`; the next word must be `docker-compose` (last path element), or a word
   whose last path element is `docker` or that is a `$…`/`${…}` expansion
   (`${DOCKER:-docker}`) IMMEDIATELY followed by `compose`. So
   `echo docker compose -p …` and `docker --context x compose -p …` are not
   anchored — deliberate, pinned as no-hit rows. After the anchor, walk compose's **global** flags until the first
   word not starting with `-` (the subcommand): `-p V`, `-p=V`, `-pV`,
   `--project-name V`, `--project-name=V` capture `V`; other value-taking globals
   (`-f`, `--file`, `--project-directory`, `--env-file`, `--profile`, `--ansi`,
   `--progress`, `--parallel`) skip their value. A `-p` after the subcommand
   (`exec db psql -p 5432`) is never looked at; `mkdir -p` never anchors.
5. **Assignment:** `[export] COMPOSE_PROJECT_NAME=V` (also as a prefix
   assignment before a command) captures `V`.
6. **Classify a value** (surrounding quotes stripped) — one classifier, used
   for `V` and for the one-hop `W`:
   Checked in this order, first match wins:
   - **ok** — a `$COMPOSE_PROJECT_NAME` / `${COMPOSE_PROJECT_NAME` reference
     followed by a boundary (`}`, `:`, `-`, `?`, `+`, `=`, end) ANYWHERE in the
     value — covers `${COMPOSE_PROJECT_NAME:-…}` and override idioms such as
     `${OVERRIDE:-$COMPOSE_PROJECT_NAME}`; `${COMPOSE_PROJECT_NAME_OLD}` is not a
     reference;
   - **skip** — a positional (`$1`, `$@`, `$*`, `${1:-…}`) or a command
     substitution (`$(…)`, backticks) anywhere in the value, or a pure literal;
   - **ref** — exactly `$X` / `${X}`;
   - **hit** — anything else (a composite such as `${PREFIX:-dwe}-${NAME}`).

   `V` ok/skip → nothing; hit → report. `V` ref → take EVERY
   `[export|local|readonly|declare|typeset] [-opts…] X=W` in the same text
   (option words such as `-r` / `-x` after the keyword skipped; collected in
   the same single pass — no backward search, linear in the text) and classify
   each `W` once: report only when at least one exists and ALL are hit;
   anything else → nothing. This deliberately ignores control flow: with
   `PROJECT=live-${A}; false && PROJECT="$COMPOSE_PROJECT_NAME"` the assignments
   disagree, so the scanner stays silent rather than guess which one runs.
   Variables set only by `read` / `for` have no assignment → nothing.
7. `hit{Line int, Value string}`; unit-tested as a pure function.

The shape that motivated the check, which the tests must include:
`PROJECT="${PROJECT_PREFIX:-dwe}-${PROJECT_NAME:-myproj}"` then
`docker compose -p "$PROJECT" exec …` → hit; with
`PROJECT="${COMPOSE_PROJECT_NAME:-${PROJECT_PREFIX:-dwe}-${PROJECT_NAME:-myproj}}"`
→ OK.

**Diagnostic shape:** `Domain "tests"`, `Target "tests.host_project_name"`,
`File` = the project-relative file, message prefixed with the location
(`line N` for a script, the command ID or step address for YAML `cmd`). For a
YAML `cmd` the scanner's line is relative to the `cmd` text: it appears only in
the message (`step deploy/seed, cmd line 2`) and the dedup key, never as a file
line.

### Commit 4 — errors

| code | when |
|---|---|
| `vars_value_ambiguous` | `--generate` and a positional value |
| `vars_generate_invalid` | bad spec (unknown kind, N not an int, N out of 1..1024, N on `uuid`) |
| `vars_force_requires_generate` | `--force` without `--generate` |
| `vars_value_exists` | local non-null value, no `--force` (detail `var`, hint names `--force`) |

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, docs, CHANGELOG — all
  inside this repo
- **Post-Completion** (no checkboxes): live runs on local projects, PR and
  review

## Implementation Steps

### Task 1: Inject the compose project name into pipeline shell steps

**Files:**
- Modify: `internal/core/execution/pipeline/executor.go`
- Modify: `internal/core/execution/pipeline/executor_test.go`
- Modify: `internal/shared/bridgeclient/env.go` (comment only)
- Modify: `internal/core/project/config/docker_test.go` (or the file holding the resolver tests)
- Modify: `internal/cli/lifecycle/reset.go`
- Modify: `internal/cli/lifecycle/` reset-step tests

- [x] in `execShellAction`, set `cmd.Env = append(os.Environ(), "COMPOSE_PROJECT_NAME="+name)` when `actx.DockerCfg != nil` and `config.ComposeProjectName(actx.DockerCfg, actx.Cfg) != ""`; leave `cmd.Env` nil otherwise; comment the why (scrubbed env + concurrent scenarios) and the `DockerCfg` guard
- [x] `resetStepCmd` (`reset.go:762`) loads `config.LoadDockerConfigOrEmpty(workDir, cfg)` into `ActionContext.DockerCfg` (the `check:` copies the same actx)
- [x] update the `EnvNestedRuntime` comment in `bridgeclient/env.go:47-51` (execShellAction now builds its env from `os.Environ()`, so the marker still flows; the per-spawn-list argument stands)
- [x] write tests: a shell step (`printf '%s' "$COMPOSE_PROJECT_NAME"`) sees `project.prefix-name`; with `docker.yml project_name: Other` sees `other`; an ambient `COMPOSE_PROJECT_NAME` (`t.Setenv`) is overridden by the config value; nil `DockerCfg` → the ambient value passes through unchanged; empty name → not set
- [x] write test: a shell `check:` sees the same value as the body
- [x] write test (config package): for fixtures without `docker.yml`, with `project_name`, with a `docker.local.yml` override, with a `${vars.*}` template in `project_name` and with an uppercase name, `ResolveComposeProjectName(root, cfg) == ComposeProjectName(LoadDockerConfigOrEmpty(root, cfg), cfg)` — pins the "same value as `.env`" invariant (a non-string `project_name:` is a known pre-existing divergence, see Context — keep it out of the table)
- [x] write test: `dwe reset step` on a `type: shell` step sees the config name, with `docker.yml project_name` set
- [x] run `go test ./internal/core/execution/pipeline/... ./internal/core/project/config/... ./internal/shared/bridgeclient/... ./internal/cli/lifecycle/...` - must pass before task 2

### Task 2: Give the `type: script` contract the compose pair

**Files:**
- Modify: `internal/core/usercommands/runtime/internal/runio/` (new helper in the existing package; test beside it)
- Modify: `internal/core/usercommands/runtime/runners/host/host.go`
- Modify: `internal/core/usercommands/runtime/runners/script/script.go`
- Modify: `internal/core/usercommands/runtime/runners/script/script_test.go`
- Modify: `internal/core/usercommands/runtime/runners/host/host_test.go` (only if a test pins the helper boundary)
- Modify: `internal/core/workflow/snapshot/exec.go` + its test
- Modify: `internal/cli/service/service_plan.go` + its test
- Modify: `internal/cli/lifecycle/reset.go` (hook runner) + its test

- [x] extract the compose half of `hostContractEnv` into `runio.ComposeContractEnv(rc)`; `hostContractEnv` = `DWE_BIN` + helper (output byte-identical)
- [x] `buildContractEnv` appends `runio.ComposeContractEnv(ctx)` after the `DWE_*` entries; update the package doc comment's variable list and fix the misleading comment at `host.go:107`
- [x] snapshot workflows, service-toggle hooks and reset hooks set `RunContext.DockerConfig` from `config.LoadDockerConfigOrEmpty(baseDir, cfg)`; a load error is returned, never swallowed
- [x] write tests: a script sees `COMPOSE_PROJECT_NAME` and absolute `COMPOSE_FILE`; both omitted when the name is empty / there are no files; the contract wins over a colliding `env:` entry
- [x] write tests: with `docker.yml project_name: custom`, a `type: shell` and a `type: script` leaf run through each of the three entry points (and through a workflow leaf under one of them) see `custom`
- [x] write test: a `service_exec` leaf run through one of the entry points builds its compose with `ProjectName == "custom"` (inspect the built argv / `rc.Compose()` through the existing runner seam, no Docker)
- [x] confirm existing host contract tests pass unchanged
- [x] run `go test ./internal/core/usercommands/runtime/... ./internal/core/workflow/snapshot/... ./internal/cli/service/... ./internal/cli/lifecycle/...` - must pass before task 3

### Task 3: Pin the copy's name inside `dwe test`, document, commit

**Files:**
- Modify: `internal/core/workflow/envtest/runner_test.go` (or a new `runsteps_env_test.go`)
- Modify: `docs/reference/config/commands/types.md` + `docs/i18n/ru/reference/config/commands/types.md`
- Modify: `docs/reference/config/deploy/steps.md` + `docs/i18n/ru/reference/config/deploy/steps.md`
- Modify: `skills/dwe/references/authoring-commands.md`
- Modify: `CHANGELOG.md`

- [ ] write test: `runSteps` (with `noopReporter`) over a fixture copy whose `docker.local.yml` carries a test identity runs a scenario `type: shell` step that writes `$COMPOSE_PROJECT_NAME` to a file → the copy's name, not the original's, with `COMPOSE_PROJECT_NAME` of the live project set in the process env
- [ ] write test: the same through a `type: command` step targeting a `type: script` command
- [ ] write test: two `runSteps` calls on two copies, run as two goroutines in one test with `noopReporter` and per-run writers, each see their own name; run it with `go test -race`
- [ ] `skills/dwe/references/authoring-commands.md` (~:74): the `script` entry inherits `COMPOSE_PROJECT_NAME` / `COMPOSE_FILE` like `shell`
- [ ] `types.md` `type: script` contract table (:174) gains `COMPOSE_PROJECT_NAME` / `COMPOSE_FILE` with the same omission rules and collision note as the `type: shell` table (:77-93); RU mirror line for line
- [ ] `deploy/steps.md` (`type: shell`, :15): shell steps and shell `check:` get `COMPOSE_PROJECT_NAME` from the project config (the name dwe passes as `-p`; no `COMPOSE_FILE`); `when:` predicates and the builtin `shell` probe do not; RU mirror
- [ ] `CHANGELOG.md` `### Fixed` (the section already exists at `:48`): inside `dwe test` a scenario's shell steps now see the copy's `COMPOSE_PROJECT_NAME`, so `${COMPOSE_PROJECT_NAME:-…}` no longer falls back to the live stack; shell steps of `dwe stop` / `restart` / `reset` also get dwe's name instead of an ambient one; `type: script` commands receive `COMPOSE_PROJECT_NAME` / `COMPOSE_FILE` like `type: shell`; commands run from snapshot workflows, service-toggle hooks and reset hooks now honour `docker.yml project_name` — container commands there exec into the right compose project with the project's `docker.yml` `args`, and the shell/script contract carries the same name
- [ ] `make build`, refresh the RU `> Translated from: … @ <hash>` headers of `commands/types.md` and `deploy/steps.md`
- [ ] `make lint && make test && (cd web && npm run build)` - must pass
- [ ] commit `fix(test): pass the copy's compose project name to host shell steps and scripts`

### Task 4: Shell-text scanner for compose project names

**Files:**
- Create: `internal/core/validate/tests/hostproject_scan.go`
- Create: `internal/core/validate/tests/hostproject_scan_test.go`

- [ ] implement continuation joining, comment stripping and simple-command splitting (quote-aware), keeping first-line numbers
- [ ] implement the compose anchor + global-flag walk and the `COMPOSE_PROJECT_NAME=` assignment capture
- [ ] implement the value judgement with the one-hop `X=` lookup (Technical Details § Commit 2, step 6)
- [ ] write table tests — hits: the composite-name shape above (via `$PROJECT`), inline composite `-p "${A:-dwe}-x"`, `--project-name=…`, `-p=…`, `docker-compose -p …`, `${DOCKER:-docker} compose -p …`, `export COMPOSE_PROJECT_NAME="${A}-x"`, a continued line reporting its first line number
- [ ] write table tests — hits (boundary): `-p "${COMPOSE_PROJECT_NAME_OLD}-x"` (the `_` defeats ok, the composite is a hit — whereas a bare, unassigned `-p "${COMPOSE_PROJECT_NAME_OLD}"` is a ref with no assignment → no hit, pin it too); a hit after `&` and in the second line of a multi-line `cmd`; `declare PROJECT="${A}-x"` then `-p "$PROJECT"`
- [ ] write table tests — no hits: `${COMPOSE_PROJECT_NAME}`, `$COMPOSE_PROJECT_NAME`, `${COMPOSE_PROJECT_NAME:-dwe-x}`, `$PROJECT` assigned from `${COMPOSE_PROJECT_NAME:-…}`, `$1`, a literal, `$X` with no assignment, `$X` assigned from `$Y`, `PROJECT="${OVERRIDE:-$COMPOSE_PROJECT_NAME}"`, `-p "${1:-$COMPOSE_PROJECT_NAME}"`, `-p "${1:-dwe-x}"`, `declare -r PROJECT="$COMPOSE_PROJECT_NAME"` plus `PROJECT="${A}-x"` elsewhere (disagreeing), `PROJECT=$(get_project)`, `` PROJECT=`cmd` ``, `PROJECT="$(docker compose ls -q | head -1)"`, `-p "$(…)"`, a `read`-assigned variable, `mkdir -p`, `docker compose exec db psql -p 5432`, `docker --context x compose -p "${A}-x"` (not anchored, deliberate), a commented-out line, `-p` inside a quoted string argument, a `-p` inside a here-doc body
- [ ] write table tests — position and lexing: `echo docker compose -p "${A}-x"` → no hit; `sudo docker compose -p "${A}-x"` and `env X=1 docker compose -p "${A}-x"` → hit; an escaped `\;` inside a word does not split; a quoted string spanning a `\`-continuation; two here-docs in one text; `PROJECT="${A}-x"` twice then `-p "$PROJECT"` → hit; `PROJECT="${A}-x"; false && PROJECT="$COMPOSE_PROJECT_NAME"` then `-p "$PROJECT"` → no hit
- [ ] run `go test ./internal/core/validate/tests/ -run Scan` - must pass before task 5

### Task 5: `host_project_name` validator over its sources

**Files:**
- Create: `internal/core/validate/tests/hostproject.go`
- Create: `internal/core/validate/tests/hostproject_test.go`
- Modify: `internal/core/validate/tests/tests.go`

- [ ] add `hostProjectNameValidator` to `All()`; nil only when `workspace/tests/` is absent — a nil `ctx.Cfg` skips per-service `deploy.yml` files only (Solution Overview § Commit 2)
- [ ] collect sources per the Solution Overview table: command files parsed like `validate/commands/commands.go:85-110` (no registry; `type: shell` → `cmd`, or the `-c` payload of a POSIX-shell `argv`; `type: script` → its files when its shell is a POSIX shell); walk `Parallel` sub-steps and `Check`; load pipeline files with the existing loaders; a parse/load error skips that source silently — other validators report it
- [ ] follow `bash|sh <path>` / `./<path>` references from inline `cmd` one level; scan each file once
- [ ] emit `SeverityWarning` diagnostics deduplicated by (file, location, line) (shape in Technical Details)
- [ ] write tests: no `workspace/tests/` → no diagnostics; a hit in a host shell command (`cmd` and `argv`), a hidden command, a script file (`path` and phased `run`), a deploy step, a shell `check:`, a parallel sub-step, a per-service `deploy.yml`, lifecycle `run`, a scenario step, a file referenced from a step `cmd`; two offending steps in one `deploy.yml` → two diagnostics; a script reached twice → one diagnostic; a container-side command with `-p` in its `cmd` → ignored; unreadable/missing referenced file → ignored; nil `ctx.CommandRegistry` → commands still scanned; a clean project → no diagnostic (and the `scenarios` validator's OK rows unchanged)
- [ ] implement the interpreter allowlist (`script.shell`, `argv` `-c` payload, `./path` shebang) and the file-read confinement (contained lexical + real path, regular file, 1 MiB cap)
- [ ] write tests: `argv: [docker, compose, -p, "$X"]` → ignored; `argv: [sh, -c, "docker compose -p \"${A}-x\""]` → hit; `script.shell: python3` → not scanned; a `./x.py` with a python shebang → not scanned; an absolute path, a `../` path, a symlink pointing outside the root, a directory/FIFO and a >1 MiB file → skipped without error; nil `ctx.Cfg` → command files and project-level pipeline files still scanned
- [ ] run `go test ./internal/core/validate/...` - must pass before task 6

### Task 6: Document the validator and commit

**Files:**
- Modify: `docs/reference/config/tests.md` + RU
- Modify: `docs/reference/config/validate.md` + RU
- Modify: `docs/guides/upgrading.md` + RU
- Modify: `docs/internals/packages.md`
- Modify: `CHANGELOG.md`

- [ ] `tests.md` § `dwe validate tests` (:341): the new `tests.host_project_name` warning — sources, the rule in one paragraph, the fix, what it skips (literal, positional, command substitution, untraceable, deeper than one referenced file, a referenced path under a command's `workdir:`, `docker --context … compose`, `argv` other than `[<shell>, -c, …]` such as `[bash, -lc, …]`, non-POSIX-shell scripts, files outside the project or over 1 MiB); RU mirror, anchors checked with `dwe docs show --lang ru --anchors`
- [ ] `validate.md:51` `tests.*` row mentions host scripts that compute their own compose project name; RU
- [ ] `upgrading.md` § Upgrading to 0.6.1 → `### Integration tests` (:74): the new warning can fail `dwe validate --strict` in CI; the fix is `${COMPOSE_PROJECT_NAME:-…}`; RU
- [ ] `packages.md` § validate/tests bullet (~:173): second validator (command sources parsed from files, not the registry — why), source set, scanner rules, never in preflight, "never guesses" policy
- [ ] `CHANGELOG.md` `### Added`: `dwe validate` warns (`tests.host_project_name`) when a host script or shell step passes compose a project name not derived from `$COMPOSE_PROJECT_NAME`
- [ ] `make build`, refresh RU `Translated from` headers of every edited page
- [ ] `make lint && make test && (cd web && npm run build)` - must pass
- [ ] commit `feat(validate): warn when a host script computes its own compose project name`

### Task 7: Document the `dwe test` isolation boundary and commit

**Files:**
- Modify: `docs/reference/config/tests.md` + RU
- Modify: `docs/guides/integration-tests.md` + RU
- Modify: `docs/internals/packages.md`
- Modify: `skills/dwe/references/integration-tests.md`

- [ ] `tests.md` § Isolation model (:162): table — guaranteed (unique compose project name, `services.*.ports` remap, own `.dwe/`, own generated store); who sees the copy's `COMPOSE_PROJECT_NAME` (shell steps and shell `check:`, `type: shell` / `type: script` commands, `type: command`, `type: dwe` — the child dwe resolves it itself); who does not (shell `when:` predicates, the builtin `shell` probe, anything launched outside dwe); `files_gate` probes files and spawns no shell, so it is not listed
- [ ] `tests.md` § Documented limitations (:460): not isolated — self-built project or container names, `container_name:`, raw ports (blocking) and interpolated ports (`interpolated_host_port`), external and `shared: true` volumes, host side effects outside the copy, `.git/`
- [ ] `integration-tests.md`: new section after "Resolving an isolation failure" (:168) — host scripts and the project name: `${COMPOSE_PROJECT_NAME:-<live>}` for scripts also run by hand, `${COMPOSE_PROJECT_NAME:?}` for scripts only dwe runs; the validator catches the common mistake and what it cannot catch; RU mirror
- [ ] `packages.md`: § envtest bullet (:108) — the runner relies on per-spawn derivation, never on process env; the `DWE_NESTED_RUNTIME` paragraph (:315) — `execShellAction` now sets `cmd.Env`; a Core — Execution note on the `DockerCfg` guard and the `.env` invariant
- [ ] `skills/dwe/references/integration-tests.md` (~:48, isolation limits): the host-script recipe and the new warning, one short paragraph
- [ ] no CHANGELOG entry (documentation only)
- [ ] `make build`, refresh RU headers, anchors checked
- [ ] `make lint && make test && (cd web && npm run build)` - must pass
- [ ] commit `docs(test): document what dwe test isolates and how host scripts stay inside the copy`

### Task 8: `randval` generator

**Files:**
- Create: `internal/shared/randval/randval.go`
- Create: `internal/shared/randval/randval_test.go`

- [ ] `Spec{Kind, Bytes}`, `Parse`, `Generate` per the Solution Overview; errors name the accepted forms
- [ ] write table tests for `Parse`: every valid form incl. default N, bounds 1 and 1024; errors for unknown kind, `hex:0`, `hex:1025`, `hex:x`, `uuid:16`, empty
- [ ] write tests for `Generate` with a fixed reader: hex length `2N`, lowercase alphabet, decodes back to the input bytes; base64url length `4*ceil(N/3)`, padded, URL alphabet, decodes back; uuid format `8-4-4-4-12`, version nibble 4, variant `10xx`
- [ ] write test: a short reader → error
- [ ] run `go test ./internal/shared/randval/...` - must pass before task 9

### Task 9: `dwe vars set --generate`

**Files:**
- Modify: `internal/cli/vars/set.go`
- Modify: `internal/cli/vars/set_test.go`

- [ ] add `--generate` and `--force` flags; update `Use`/`Long`/`Example`; track presence with `cmd.Flags().Changed("generate")` (an explicit `--generate=` must be `vars_generate_invalid`, never "flag absent" → form)
- [ ] in `runVarsSet`: flag-combination errors and `Parse` before any lock; generate via a package-level reader seam (`randReader = rand.Reader`); skip `CoerceScalar` for the generated string
- [ ] pass an "only if absent" option into `writeVarOverrideCore` so the `LocalOK && Local != nil` check runs under the locks, AFTER the container gate and before the file is touched — inside a container a non-writable var must still report `vars_not_container_writable`, never `vars_value_exists`; the TUI path (`writeVarOverrideSilent`) passes the option off
- [ ] write tests: each kind writes a string (all-digit hex from a seeded reader reloads as a string, quoted in `local.yml`); `--generate` + value → `vars_value_ambiguous`; bad spec → `vars_generate_invalid`; `--force` alone → `vars_force_requires_generate`; existing local value → `vars_value_exists`, file unchanged; `--force` overwrites; explicit null in `local.yml` and a `workspace.yml` default do not block; JSON output `{var, value}`; non-interactive works without a TTY; container gate still refuses a non-writable var; container + non-writable + existing local value → `vars_not_container_writable`
- [ ] write test: `--generate=` → `vars_generate_invalid`, no form opened, file untouched
- [ ] run `go test ./internal/cli/vars/...` - must pass before task 10

### Task 10: Document `--generate` and commit

**Files:**
- Modify: `docs/reference/config/vars.md` + RU
- Modify: `docs/reference/config/setup.md` + RU
- Modify: `docs/internals/packages.md`, `AGENTS.md`
- Modify: `skills/dwe/references/render-and-vars.md`
- Modify: `CHANGELOG.md`

- [ ] `vars.md` § `dwe vars set` (:107): the flag, grammar, byte semantics, padding, refusal and `--force`, string typing; § JSON output (:298) — the new error codes; § Container behavior (:319) — `--generate` obeys `bridge.vars_writable`; mention that the value is printed (§ Output is not redacted) and that secret material belongs in `dwe secrets set --stdin`; RU
- [ ] `setup.md` question fields (:107-116): a short note under the table — for a secret-like answer, tell the developer to run `dwe vars set <path> --generate …` instead of pasting a python one-liner into `description:`; RU
- [ ] `packages.md`: a new `internal/shared/randval/` leaf entry (grammar, padded base64url, reader injected, reused by nothing else yet) and the `--generate` contract under § `internal/cli/vars/` (bypasses `CoerceScalar`; exists-check under the locks after the container gate); add `randval/` to the `internal/shared/` leaf list in `AGENTS.md` (respect `TestAgentsMdBudget`)
- [ ] `skills/dwe/references/render-and-vars.md` §5: mention `--generate` as the command to hand the user for secret-like vars, keeping the "`vars set` is a handoff — never run it" framing
- [ ] `CHANGELOG.md` `### Added`: `dwe vars set <var> --generate hex[:N]|base64url[:N]|uuid [--force]`
- [ ] `make build`, refresh RU headers
- [ ] `make lint && make test && (cd web && npm run build)` - must pass
- [ ] commit `feat(vars): generate a random value with dwe vars set --generate`

### Task 11: Verify acceptance criteria

- [ ] commit 1: a scenario shell step and a script command inside `dwe test` see the copy's name, concurrently too; deploy/run values unchanged (Task 1 invariant test)
- [ ] commit 2: every listed source is scanned; the motivating shape warns; the fixed shape and every "no hit" row stay silent
- [ ] commit 3: the isolation table matches the code paths verified in Tasks 1-3
- [ ] commit 4: every row of the error table and both refusal/force paths are covered
- [ ] run full test suite: `make test`
- [ ] run `make lint`
- [ ] run `cd web && npm run build`

### Task 12: [Final] Update documentation

Skill and reference edits already landed in their commits (Tasks 3, 7, 10);
this task only closes out.

- [ ] re-read the three CHANGELOG entries and the Upgrading addition together for consistent wording
- [ ] update `AGENTS.md` only if a new trap emerged (respect `TestAgentsMdBudget`); candidate: "pipeline shell steps get the compose project name per spawn — never via process env" as a pointer into `packages.md`
- [ ] scaffold `AGENTS.md.tmpl`: touch only if it already discusses `dwe test` isolation or `vars set`; if changed, regenerate `internal/core/workflow/scaffold/testdata/golden_default.txt`
- [ ] move this plan to `docs/plans/completed/`
- [ ] `make lint && make test` - must pass
- [ ] commit `docs: close out the test isolation and vars generate plan`

## Post-Completion

*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Manual verification:**
- `dwe test run` on a local project with a scenario step
  `type: shell, cmd: 'echo "$COMPOSE_PROJECT_NAME"'` prints the copy's
  `…-t-<scenario>-<runid>` name; the same with `--parallel 2` over two
  scenarios prints two different names.
- A `type: script` command that echoes `$COMPOSE_PROJECT_NAME` prints the
  project's name under `dwe cmd`.
- `dwe validate` on every local project: zero `tests.host_project_name` false
  positives; reintroducing the composite `PROJECT=` shape in one script makes
  it warn.
- `dwe vars set <setup var> --generate base64url:32` on a project whose setup
  wizard asks for a Fernet key: the app accepts the key; a second run refuses,
  `--force` regenerates.

**Before the PR:**
- the maintainer runs their own internal-reference check over the changed and
  new files (excluding `docs/plans/`).

**PR:**
- one PR `feat/0.6.1-test-isolation` → `release/0.6.1`, milestone 0.6.1; trigger
  the review with a `@coderabbitai full review` comment (auto-review does not
  fire on this repo).
