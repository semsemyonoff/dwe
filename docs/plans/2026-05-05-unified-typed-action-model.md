# Unified Typed Action Model

## Overview

Apply one consistent typed action model — `type` / `cmd` / `with` — to **pipeline steps** (deploy / reset / lifecycle) and **user-command definitions**. No backward compatibility for the surfaces in scope. **Workflow step entries (`steps:` inside `type: workflow`) are explicitly out of scope** for this plan and keep their current `command:` / `confirm:` / `with:` / `when:` (string) syntax. They will be migrated separately.

Canonical step shape (in scope):

```yaml
type: <executor>     # what runs it
cmd:  <string>       # what to run (for executors whose payload is a single string)
with: <mapping>      # optional structured params (for builtins, etc.)
```

After the rework:

- `DeployStep` (deploy / reset / lifecycle phases) carries only `type`, `cmd`, `with`, plus orchestration fields (`name`, `description`, `when`, `check`, `continue_on_error`). Old fields (`run`, `devbox`, `command`, `builtin`, `service_configs_copy`, `mode`) are removed.
- User-command definitions rename `type: command` (host shell) → `type: shell` and `run:` → `cmd:`. `argv:` survives as the advanced direct-exec alternative for `type: shell`, `service_exec`, `service_run` (mutually exclusive with `cmd:`). `type: command` is now reserved for workflow-context command-ID references.
- **`check:` becomes a typed *action*** (same union as `DeployStep`'s `type:`): success = pass, error = fail. The user's example `check: {type: builtin, cmd: service_configs_check, with: {service: main}}` works directly — engine builtins compose into `check:` slots.
- **`when:` (on pipeline steps and phases) becomes a typed *condition***: `type: builtin | shell | template`. Builtin predicates (`dir-empty`, `file-exists`, etc.) keep their existing implementation in `internal/condition`; the typed `Condition` form just exposes them via `cmd:` plus optional path/args.
- **Workflow `when:` stays as the existing mini-language string** (`"{{ ... }}"`, `"dir-empty path"`, `"cmd: ..."`). Workflow conditions and the legacy `condition.Classify` / `EvalBuiltin(string,...)` / `EvalCmd(string,...)` code paths remain intact.
- `devbox deploy config` and `devbox deploy config-check` CLI commands are removed; both are accessible only as builtins inside pipelines (`service_configs_copy`, new `service_configs_check`).
- **Strict YAML decode** (`yaml.Decoder.KnownFields(true)`) is wired into the deploy / reset / lifecycle / command-file loaders so removed fields produce clear errors at load time, not silent ignores.
- **Behavior change for `continue_on_error` + failing `check:`:** today a failed `check:` aborts the pipeline regardless of the step's `continue_on_error`. After this rework, when `continue_on_error: true` AND the body succeeded but the `check:` action failed, the step is reported as failed but the pipeline continues — symmetric with body failures. (When the body itself fails with `continue_on_error: true`, `check:` is still skipped, preserving today's semantic.) This intentional change is documented in deploy.md.
- Docs are rewritten to describe the *current* schema only — no "previously this was…" narrative.

End state for the in-scope surfaces: one mental model — `type:` decides executor, `cmd:` tells the executor what to run, `with:` passes structured params. **Known wart** carried over from this scope: `type: builtin` in a pipeline `when:` resolves to a *predicate* registry (in `condition`), while `type: builtin` in a step body or in `check:` resolves to the *engine builtin* registry (in `internal/builtin`). Two namespaces, same keyword — disambiguated by YAML position. Documented explicitly; merging the registries is a follow-up.

## Context (from discovery)

**Pipeline step config & dispatch:**

- `internal/config/devbox.go` — `DeployStep` (lines 176-193): fields `Run`, `Devbox`, `Command`, `Builtin`, `With`, plus `When`, `Check`, `ContinueOnError`, plus deprecated `ServiceConfigsCopy`/`Mode`. `DeployPhase` (143-150) carries `When`, `Untracked`, `Steps`. `validatePhaseSteps` (924-981) enforces "exactly one of {Run, Devbox, Command, Builtin}".
- `internal/pipeline/executor.go` (52-102) — `ExecStep` dispatch by field-presence (`step.Builtin != ""`, `step.Command != ""`, `step.Devbox != ""`, else shell).
- `internal/pipeline/step.go` (39-82) — `stepBadge()`, `StepCommand()` printers branch on the same fields.
- `internal/pipeline/resolve.go` (22-38, 42-67) — phase/step `when` evaluation; templates resolved at plan time, builtins/cmd at runtime.

**User commands:**

- `internal/usercommands/model/types.go` (17-343) — `CommandType` constants: `command`, `script`, `service_exec`, `service_run`, `workflow`, `devbox`. `CommandDef` carries `Run` (shell), `Argv` (raw argv), `Script`, `Steps[]` (workflow), `Service`/`User`/`Workdir`/`Mode` (service exec/run), `Runner` (override).
- `internal/usercommands/runtime/runner.go` (80-99) — `NewRunner` dispatches by `CommandType` to `HostRunner`, `DevboxRunner`, `ServiceExecRunner`, `ServiceRunRunner`, `ScriptRunner`, `WorkflowRunner` in `runner_*.go` siblings.
- `WorkflowStep.When` is a string today; resolved via `internal/tpl/render_command.go` `EvalCommandCondition` (237-275).

**Conditions:**

- `internal/condition/condition.go` — `Classify(expr)` returns `KindTemplate` (contains `{{`), `KindBuiltin` (bare predicate), `KindCmd` (string prefixed with `cmd:`). `EvalBuiltin` (70-100) supports `dir-exists`, `dir-missing`, `dir-empty`, `dir-not-empty`, `file-exists`, `file-missing`. `EvalCmd` (106-123) runs `sh -c` for portability — keep this.
- Used by pipeline (`pipeline/resolve.go`, `pipeline/executor.go` post-step `Check`) and workflow (`tpl/render_command.go EvalCommandCondition`).
- **Import-cycle constraint**: `internal/tpl/render_command.go:12` imports `internal/condition`. Therefore `internal/condition` must NOT import `internal/tpl`. Plan-time template evaluation lives in callers (pipeline/resolve, ui/info, workflow runtime), not in `condition`. The `condition` package owns: typed model + validation + runtime evaluators (builtin / shell). Template `expr` is rendered by the caller using `tpl`, then interpreted as bool.

**Workflow steps (explicitly out of scope for this plan):**

- `internal/usercommands/model/types.go:223-237` — `WorkflowStep` keeps its current shape: `Command` (registered ID) XOR `Confirm` (prompt string), `With map[string]string`, `When string` (legacy mini-language), `ContinueOnError bool`. Untouched here; migrated in a follow-up plan.
- Consequence: `condition.Classify`, `EvalBuiltin(string,...)`, `EvalCmd(string,...)`, `IsRuntime(string)`, and the `KindTemplate/KindBuiltin/KindCmd` enum survive — they're used only by the workflow string-condition path. New typed-condition code lives alongside them, not as a replacement.
- `internal/tpl/render_command.go EvalCommandCondition(expr string, ...)` keeps its current string signature (only workflow callers).

**Deploy config CLI commands to remove:**

- `internal/command/deploy.go` — `newDeployConfigCmd()` (298-331) wraps builtin `service_configs_copy`. `newDeployConfigCheckCmd()` (337-381) does ad-hoc filesystem checks. Both registered in `newDeployCmd()` (37-38).

**Builtins:**

- `internal/builtin/builtin.go` registry (61-68): `confirm`, `message`, `service_configs_copy`, `service_dirs_ensure`, `docker_remove_project_volumes`, `remove_paths`. Add `service_configs_check` (port `newDeployConfigCheckCmd` body). **Predicates stay in `internal/condition`** — the engine builtin registry is not extended with `dir_empty`/`file_exists`/etc. as part of this plan.

**YAML decode strictness:**

- The deploy/reset/lifecycle loaders and the command-file parser use `yaml.Unmarshal` (lenient), which silently ignores unknown keys. **In-scope strict-decode targets** (chosen because they hold user-edited YAML where removed fields must surface as clean migration errors): `internal/config/devbox.go:509` `LoadDeployConfig`, the equivalent reset loader, `LoadLifecycleConfig` (~832/911), and **`internal/usercommands/model/types.go:676` `ParseCommandFile`** (the actual decode point — `LoadCommandFile` in `internal/usercommands/loader/loader.go:67` just calls `ParseCommandFile`). Other `yaml.Unmarshal` sites (info / styles / docker / project / localconfig / topology) stay lenient — they read non-step user input or internal views and aren't affected by this refactor.

**Single-step command paths (`devbox deploy step` / `devbox reset step`):**

- `internal/command/deploy.go:241-283` (`newDeployStepCmd`) and `internal/command/reset.go:150+` (`newResetStepCmd`) duplicate today's pipeline orchestration: each manually evaluates `step.When` (template vs runtime), prints a dry-run via `pipeline.StepCommand`, calls `pipeline.ExecStep`, then evaluates `step.Check` against `condition.EvalRuntime`. After the rework these paths must (a) use the typed `*Condition` for `When:`, (b) call `pipeline.ExecAction(step.Action(), actx)` for the body, (c) decide explicitly whether to evaluate `Check:` (probably yes, mirroring full pipeline runs), (d) update dry-run output for the new step shape.
- `internal/command/deploy_test.go:14-47` calls `pipeline.ExecStep` directly to assert builtin-validation behavior; once `ExecStep` is folded into `Run`, these tests need to call the new `pipeline.ExecAction` (or whichever surface remains) with an `ActionContext`.
- **Not affected (workflow-only renderers):** `internal/command/command_cmd.go:543-544` and `internal/command/docs.go:481-482` iterate `def.Steps` (`WorkflowStep`), whose `When` stays a string in this plan. Leave both unchanged.

**Docs to rewrite:**

- `docs/reference/config/deploy.md`
- `docs/reference/config/lifecycle.md`
- `docs/reference/config/commands.md`
- `docs/reference/config/index.md` (cross-refs)

**Tests touching these areas:**

- `internal/config/devbox_test.go`
- `internal/pipeline/{executor,step,plain}_test.go`
- `internal/deploy/{plan,service_plan}_test.go`
- `internal/reset/*_test.go`
- `internal/lifecycle/*_test.go`
- `internal/usercommands/model/types_test.go`
- `internal/usercommands/runtime/runner*_test.go`
- `internal/condition/condition_test.go`
- `internal/tpl/render_command_test.go` (`TestEvalCommandCondition_*`)
- `internal/builtin/*_test.go`
- `internal/command/deploy_test.go`

## Development Approach

- **Testing approach**: Regular — implement each task's code change, then update/add table-driven tests within the same task. Tests live next to code.
- **No backward compatibility**: removed fields stay removed. The user maintains their own YAML migration outside this CLI; we do not ship aliases or deprecation shims.
- **Atomic per-task compile**: each task lands in a state where `make build` and `make test` are green. Where a single coherent change spans both call site and definition (e.g. typed `Condition`/`Action` adoption on `DeployPhase`/`DeployStep`), do them in one task.
- **Workflow steps are explicitly out of scope.** This plan touches pipeline step shape and command-definition shape (`type: shell` rename, `cmd:` rename). Workflow `steps:` entries stay on their current `command:` / `confirm:` / `with:` / `when:` (string) syntax. Don't rename or restructure them; don't touch `condition.Classify` or the legacy string-condition machinery (workflow still uses it).
- **Fail loudly on legacy YAML**: when the loader encounters a removed field (`run:`, `devbox:`, `command:`, `builtin:`, `service_configs_copy:`, `mode:` on `DeployStep`; `run:` on user-command types other than scripts), produce a clear validation error pointing to the new shape — strict YAML decode (`yaml.KnownFields(true)` if not already on) is preferred so unknown keys are surfaced automatically.
- **CRITICAL: every task MUST update tests** alongside the code it touches.
- **CRITICAL: all tests must pass before starting next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation.**
- After each task: run `make test`, then `make lint`, then `make build && ./bin/devbox info` smoke check.

## Testing Strategy

- **Unit tests**: required per task. Table-driven for parsing/dispatch/validation; behavioral tests for executor and runners.
- **No e2e suite** in this Go module — `go test ./...` is the full coverage gate.
- **Doc check**: after the docs task, run `./bin/devbox docs generate --scope cli` to confirm CLI reference still builds without the removed `deploy config*` commands.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Keep this plan in sync with actual work.

## What Goes Where

- **Implementation Steps** (`[ ]`): code changes, test changes, doc rewrites — all inside this repo.
- **Post-Completion** (no checkboxes): manual project-YAML migration in any consuming Devbox projects (out of scope here).

## Implementation Steps

### Task 1: Typed `Condition` (for `when:`) and `Action` (for `check:` / step bodies) — alongside legacy string path

Introduce the new typed types without removing the legacy string-condition machinery. The legacy `condition.Classify` / `EvalBuiltin(string,...)` / `EvalCmd(string,...)` / `IsRuntime(string)` / `KindTemplate|Builtin|Cmd` enum stay alive — they continue to serve `WorkflowStep.When` (string), which is intentionally untouched in this plan.

- [x] add `Condition` struct in `internal/condition/condition.go`: `Type` (`builtin|shell|template`), `Cmd` (string, used by `builtin` predicate name + args, and by `shell` for the command line), `Expr` (string, template body), `With` (`map[string]any`, reserved; predicate args today live in `Cmd`)
- [x] add `(c *Condition) UnmarshalYAML` accepting *only* the mapping form; reject string shorthand at decode time with a clear error
- [x] add `Condition.Validate()`: `builtin`/`shell` → non-empty `Cmd`; `template` → non-empty `Expr`
- [x] add `(c Condition) IsRuntime() bool` (true for `builtin` and `shell`)
- [x] add `EvalRuntimeTyped(c Condition, projectRoot string) (bool, error)` that internally dispatches: `builtin` → existing `EvalBuiltin(c.Cmd, projectRoot)`; `shell` → existing `EvalCmd(c.Cmd, projectRoot)`. Reuses the existing predicate logic; no new registry, no engine-builtin coupling
- [x] **Do NOT add a plan-time template evaluator inside `condition`** — `tpl` already imports `condition`. Plan-time template rendering for `type: template` lives in callers (pipeline/resolve, ui/info)
- [x] keep `Classify`, `IsRuntime(string)`, `EvalRuntime(string,string)`, `EvalBuiltin`, `EvalCmd`, and the `Kind*` enum intact for the workflow string-condition path
- [x] add typed `Action` struct in `internal/config/action.go`: `Type` (`shell|devbox|command|builtin`), `Cmd` (string), `With` (`map[string]any`); add `Validate()` and a custom `UnmarshalYAML` that rejects unknown keys (defense-in-depth on top of strict decode at the file level)
- [x] write `internal/condition/condition_typed_test.go` for the new types: YAML decode (positive + reject string shorthand), validation, `EvalRuntimeTyped` against a temp dir
- [x] write `internal/config/action_test.go` for `Action` decode + validate
- [x] run `make test` — must pass before Task 2

### Task 2: Adopt typed `Condition` on `DeployPhase` / `DeployStep.When` + strict decode for pipeline loaders

Scope of this task: typed `Condition` for `When:` only. **`DeployStep.Check` stays a `string` for the duration of this task** and continues to use the existing string-classified evaluator. This keeps every test green without stubs and avoids cross-task dependency on `ExecAction`. Typed `Action` adoption for `Check:` and step bodies happens together in Task 3.

- [x] change `DeployPhase.When` and `DeployStep.When` from `string` to `*condition.Condition` in `internal/config/devbox.go` (nil = unconditional)
- [x] **leave `DeployStep.Check` as `string`** — typed `*Action` adoption is in Task 3 (paired with `ExecAction`)
- [x] **`WorkflowStep.When` stays `string`** — workflow out of scope
- [x] change `ResolvedStep.RuntimeWhen` and `ResolvedStep.PhaseWhen` from `string` to `*condition.Condition` in `internal/pipeline/step.go:24-25`; nil = no runtime when (template + plan-time-evaluated cases)
- [x] update `internal/pipeline/resolve.go` (~22-77): for phase/step `When` of `type: template`, render `c.Expr` via `tpl` in the caller, truthy → include / falsy → skip; for runtime kinds (`builtin`/`shell`), store the `*Condition` on `ResolvedStep.RuntimeWhen` / `PhaseWhen`
- [x] update `internal/pipeline/executor.go` `Run` loop (lines 237-275): replace string-keyed `condition.EvalRuntime(rs.RuntimeWhen, workDir)` with `condition.EvalRuntimeTyped(*rs.RuntimeWhen, workDir)` (nil-check first); same for `PhaseWhen`. The skip-message string in `rep.SkipStep` (line 275) currently embeds the raw `RuntimeWhen` string — derive a short human-readable form from the typed condition (e.g. `"when: builtin dir-empty foo"` / `"when: shell test -f x"`)
- [x] update `internal/pipeline/print.go:65-66` to render the typed `RuntimeWhen` (use the same human-readable formatter as the skip message); update `internal/deploy/print.go` and `internal/reset/print.go` similarly wherever they render `RuntimeWhen` / `PhaseWhen`
- [x] update `internal/deploy/plan_test.go:158-272` and any reset/lifecycle tests that assert on `RuntimeWhen` / `PhaseWhen` string values to compare against typed `Condition` values
- [x] **leave the existing string-form `Check:` evaluation untouched** — it keeps using `condition.Classify` + `EvalBuiltin(string)` / `EvalCmd(string)` for now
- [x] **switch pipeline loaders to strict decode**: in `internal/config/devbox.go` (`LoadDeployConfig` ~509, `LoadResetConfig`, `LoadLifecycleConfig`) replace `yaml.Unmarshal(data, &x)` with `dec := yaml.NewDecoder(bytes.NewReader(data)); dec.KnownFields(true); err := dec.Decode(&x)`. Other loaders (info / styles / docker) stay loose unless touching them is incidentally necessary; strictness for command files is wired in Task 4
- [x] update `internal/config/devbox_test.go` to use typed `Condition` in fixtures
- [x] update `internal/pipeline/{executor,resolve}_test.go` and `internal/deploy/plan_test.go` to use typed conditions
- [x] add a strict-decode test that loads a deploy YAML with a string-form `when:` and confirms it fails with a clear unmarshal error pointing at the new mapping form
- [x] add a strict-decode test that loads a deploy YAML with an unknown top-level field on `DeployStep` (e.g. `notafield: x`) and confirms it fails as an unknown field
- [x] run `make test` — must pass before Task 3

### Task 3: Typed `DeployStep` body (`type` / `cmd` / `with`), `Check` → `*Action`, shared `ExecAction` dispatcher

Two coupled migrations done together — both step-body dispatch and `Check:` evaluation route through the same new `pipeline.ExecAction`. Doing them in one task lets `ExecAction` land complete (no stubs) and lets Task 2's string-form `Check:` flip directly to the typed form here.

**Action type shape decision:** prefer **inline** `Type` / `Cmd` / `With` fields on `DeployStep` rather than embedding `config.Action`. yaml.v3's interaction between `inline` struct embedding and a custom `UnmarshalYAML` on the embedded type is awkward to reason about, especially with `KnownFields(true)`. Inline keeps decode predictable. Provide a helper `(s DeployStep) Action() config.Action` for call sites that want the action-shaped value.

**Orchestration boundary:** `pipeline.ExecAction` is the action-dispatch primitive only. It does NOT evaluate `when:`, call the reporter, run hooks, evaluate `check:`, or apply `continue_on_error` — those stay in the existing `pipeline.Run` loop (`internal/pipeline/executor.go:194-`). This task introduces a small `pipeline.ActionContext` carrier with the fields `ExecAction` actually needs from today's `ExecStep` signature: `WorkDir string`, `Cfg *config.DevboxConfig`, `Reg *usercommands.Registry`, `LogWriter io.Writer`, `SkipConfirm bool`. (The existing `builtin.ExecContext` is a builtin-package type; `ActionContext` is a pipeline-package type that wraps the inputs and produces a `builtin.ExecContext` only when dispatching to a builtin action.) `Run` constructs an `ActionContext` once per step and passes it to `ExecAction` for both body and `Check:`.

- [x] add `Type` (`yaml:"type"`), `Cmd` (`yaml:"cmd"`), `With` (`yaml:"with,omitempty"`) directly on `DeployStep` in `internal/config/devbox.go`
- [x] remove `DeployStep.Run`, `Devbox`, `Command`, `Builtin`, `ServiceConfigsCopy`, `Mode` fields
- [x] add `(s DeployStep) Action() config.Action` helper
- [x] rewrite `validatePhaseSteps` (~924-981): for the step body, `Type` ∈ {`shell`,`devbox`,`command`,`builtin`}; non-empty `Cmd`; reject `with` for `shell`/`devbox`; allow `with` for `command`/`builtin`. **For `step.Check != nil`, call `step.Check.Validate()` (the standalone `Action.Validate()` from Task 1) and wrap returned errors with `step "<name>" (phase "<phase>") check: <err>`** so failures point at the right location. Strict decode (Task 2) handles unknown-key errors; this layer enforces semantics
- [x] change `DeployStep.Check` from `string` to `*config.Action` (nil = no post-check)
- [x] introduce `pipeline.ActionContext` struct in `internal/pipeline/executor.go` with `WorkDir`, `Cfg`, `Reg`, `LogWriter`, `SkipConfirm`; export it so callers (deploy / reset / lifecycle) can construct it
- [x] introduce `pipeline.ExecAction(a config.Action, actx ActionContext) error` — switch on `a.Type` → `runShell` / `runDevboxSubcommand` / `execCommand` / `execBuiltin`. Pure dispatch — no reporter, no `when`, no hooks, no `check`
- [x] rewrite the existing `ExecStep` (`executor.go:52-102`) into a thin wrapper or fold its dispatch directly into `Run`'s body-execution branch (`executor.go:280-302`). The body path becomes: build `ActionContext` once, call `ExecAction(step.Action(), actx)`. The `check:` path becomes: if `rs.Step.Check != nil`, call `ExecAction(*rs.Step.Check, actx)` and treat returned error as check failure
- [x] **`continue_on_error` + failing check:** in `Run`, when a step's body succeeded and `check:` returned an error, route the failure through the same `continue_on_error` branch as a body failure (today the check-failure path always aborts; align with body-failure semantics so a step that explicitly opts into continuing on errors continues even if its check fails). Keep the existing semantic that body-failure with `continue_on_error: true` skips `check:` entirely
- [x] keep `when` evaluation, reporter calls (`StartStep`/`SkipStep`/`FailStep`/`FinishStep`), pre/post hooks, and `continue_on_error` orchestration in `Run` — `ExecAction` knows nothing about them
- [x] update `internal/pipeline/step.go` `stepBadge()` and `StepCommand()` to switch on `Type`
- [x] update `internal/pipeline/print.go` `Step.Check` rendering (~lines 68-69) to format the typed `*Action` (e.g. `"check: builtin service_configs_check"`)
- [x] update `internal/deploy/plan.go`, `internal/reset/plan.go`, and any service-plan resolver that constructs steps
- [ ] migrate `service_configs_copy` shorthand call sites in `internal/deploy/` and `internal/lifecycle/` to emit `type: builtin, cmd: service_configs_copy, with: {service, mode}` directly
- [x] **update single-step CLI paths** (`internal/command/deploy.go:241-283` `newDeployStepCmd` and `internal/command/reset.go:150+` `newResetStepCmd`): switch the manual `step.When` evaluation to use the typed `*Condition` (template path → caller-side `tpl` render; runtime path → `condition.EvalRuntimeTyped`); replace the direct `pipeline.ExecStep(step, ...)` call with constructing an `ActionContext` and calling `pipeline.ExecAction(step.Action(), actx)`; replace the manual `step.Check` evaluation with `pipeline.ExecAction(*step.Check, actx)` when `step.Check != nil` (single-step commands keep evaluating checks — they exist precisely to dry-run a step end-to-end); refresh dry-run output (`pipeline.StepCommand`) for the new step shape
- [x] **do not touch** `internal/command/docs.go:481-482` or `internal/command/command_cmd.go:543-544` — both iterate `WorkflowStep.When` (string, out of scope), not pipeline `step.When`
- [x] update `internal/command/deploy_test.go:14-47` to call the new `ExecAction` surface (with `ActionContext`) instead of `ExecStep`
- [x] update `internal/config/devbox_test.go` for the new step shape — include cases that confirm removed fields produce strict-decode errors and that `step.Check` with bad type / empty cmd produces the wrapped `step "x" (phase "y") check: ...` error
- [ ] update `internal/pipeline/{executor,step,plain}_test.go`, `internal/deploy/{plan,service_plan}_test.go`, `internal/reset/*_test.go`, `internal/lifecycle/*_test.go` — (in progress: struct literals and assertions updated, remaining work: YAML fixture updates)
- [x] **add executor-level behavior tests for `check:` semantics** in `internal/pipeline/executor_test.go` (these belong here because Task 3 owns the behavior change; Task 5 adds `service_configs_check`-specific coverage on top). Use generic primitives — `type: shell, cmd: "true"` and `cmd: "false"` — to keep the tests free of builtin-specific concerns:
  - [x] successful body + passing check → step reports success
  - [x] successful body + failing check → step reported as failed and pipeline aborts (no further steps run)
  - [x] successful body + failing check + `continue_on_error: true` → **new behavior**: step reported as failed, pipeline continues to next step
  - [x] failing body + `continue_on_error: true` → step reported as failed, pipeline continues, **`check:` is not invoked** (verify via a counter/spy on a `cmd: "touch marker"` check that the marker is absent)
- [ ] run `make test` — must pass before Task 4

### Task 4: Typed user-command action (`type: shell`, `cmd:`) + strict decode for command files — no `WorkflowStep` changes

Rename host-shell type and the payload field, and switch command-file loading to strict decode so legacy `run:` produces a clear unknown-field error rather than silently falling through to a "cmd required" semantic-validation error. **`WorkflowStep` is intentionally untouched** — its `command:` / `confirm:` / `with:` / `when:` (string) syntax remains.

- [ ] in `internal/usercommands/model/types.go`: rename constant `CommandTypeCommand = "command"` → `CommandTypeShell = "shell"`; update doc comment to "host shell command"
- [ ] rename `CommandDef.Run` field → `CommandDef.Cmd` (yaml tag `cmd`); applies to types `shell`, `devbox`, `service_exec`, `service_run`
- [ ] keep `CommandDef.Argv` as the alternate raw-argv form for `type: shell` and `type: service_exec`/`service_run` (mutually exclusive with `Cmd`); document in the type doc comment
- [ ] keep `CommandDef.Script` (`ScriptDef`) untouched — `type: script` carries structured config, not a single command string; document this exception in `commands.md`
- [ ] update `internal/usercommands/runtime/runner.go` `NewRunner` dispatch: case `CommandTypeShell` → `HostRunner`, etc.
- [ ] update `runner_host.go`, `runner_devbox.go`, `runner_service.go` (exec + run), `runner_workflow.go` to read `Cmd` instead of `Run`. **`runner_workflow.go` itself is unchanged in shape** — it still references `WorkflowStep.Command` / `WorkflowStep.Confirm`; only its calls into other runners (which now read `Cmd`) shift
- [ ] **switch command-file decode to strict**: in `internal/usercommands/model/types.go` `ParseCommandFile` (line 676; this is where the `yaml.Unmarshal` actually lives) replace `yaml.Unmarshal(data, &cf)` with `dec := yaml.NewDecoder(bytes.NewReader(data)); dec.KnownFields(true); err := dec.Decode(&cf)`. **`LoadCommandFile` lives in `internal/usercommands/loader/loader.go:67` and just calls `model.ParseCommandFile`** — no decoder change needed there, but verify its error wrapping still surfaces the file path through the strict-decode error
- [ ] add a `ParseCommandFile` test in `internal/usercommands/model/types_test.go`: legacy `run:` produces an unknown-field error (NOT a later `cmd required` semantic error)
- [ ] add a `LoadCommandFile` test in `internal/usercommands/loader/loader_test.go`: same broken file, error must include the file path
- [ ] add a validation test that a CommandDef with `type: command` (legacy host-shell name) fails type validation with a useful error pointing at `type: shell`
- [ ] grep for any remaining `Run:` field accesses on `CommandDef`; update all callers
- [ ] update `internal/usercommands/model/types_test.go` and `runtime/runner*_test.go` table-driven cases
- [ ] run `make test` — must pass before Task 5

### Task 5: Add `service_configs_check` builtin

Port the verification logic out of the soon-to-be-removed CLI command into an engine builtin so deploy pipelines can use it as a `check:` action.

- [ ] create `internal/builtin/configs_check.go`: implement `serviceConfigsCheckBuiltin` satisfying `Builtin` (`Validate`, `Describe`, `Run`)
- [ ] reuse the body of `newDeployConfigCheckCmd` (currently `internal/command/deploy.go:337-381`): accept `with.service` (string, required), look up service in `cfg.Services`, iterate declared `configs`, verify each `services/<svc>/configs/<file>` exists; return error listing missing files
- [ ] register `"service_configs_check": serviceConfigsCheckBuiltin{}` in `internal/builtin/builtin.go`
- [ ] write `internal/builtin/configs_check_test.go` — table-driven on the builtin's `Run`: missing service, no configs declared, all present, some missing, with mountpoints
- [ ] add a **builtin-specific** executor integration test in `internal/pipeline/executor_test.go` (or `internal/deploy/plan_test.go`) wiring `service_configs_check` as a `check:` on a `service_configs_copy` step: success when configs were copied; failure when a config file is absent. Generic check-orchestration semantics (pass/fail/abort/`continue_on_error`) are already covered by Task 3's tests — don't duplicate them here
- [ ] run `make test` — must pass before Task 6

### Task 6: Remove `devbox deploy config` and `devbox deploy config-check` CLI commands

- [ ] delete `newDeployConfigCmd()` (`internal/command/deploy.go:298-331`)
- [ ] delete `newDeployConfigCheckCmd()` (`internal/command/deploy.go:337-381`)
- [ ] remove their `AddCommand` registrations in `newDeployCmd()` (~37-38)
- [ ] delete obsoleted helper functions if no longer referenced
- [ ] update `internal/command/deploy_test.go` — remove tests for the deleted commands
- [ ] run `make test` — must pass before Task 7

### Task 7: Sweep fixtures and embedded YAML in test files

Catch every sample YAML the binary ships, generates, or tests against. **Many fixtures are inline strings inside `*_test.go` (heredocs, raw string literals), not separate `testdata/` files** — sweep both. Scope greps to specific YAML positions; lifecycle.yml's top-level `run:`, ScriptDef's `run:` phase, and WorkflowStep's `command:` must NOT be flagged.

**Phase 1 — DeployStep / ResetStep / LifecycleStep legacy keys:**

- [ ] testdata files: `grep -rnE '^\s+(run|devbox|command|builtin|service_configs_copy):' internal/{config,pipeline,deploy,reset,lifecycle}/testdata/ 2>/dev/null`
- [ ] embedded in tests: `grep -rnE '^\s+(run|devbox|command|builtin|service_configs_copy):' internal/{config,pipeline,deploy,reset,lifecycle}/*_test.go 2>/dev/null`
- [ ] for each hit, inspect context: only update those that appear inside a `steps:` list. Skip top-level lifecycle `run:`/`stop:` sections and ScriptDef phase keys (`script.run`, `script.cleanup`, …)

**Phase 2 — legacy CommandDef host-runner fixtures:**

- [ ] testdata files: under `internal/usercommands/**/testdata/`, find files with both `type: command` and `run:` at top level
- [ ] embedded in tests: `grep -rn 'type: command' internal/usercommands/**/*_test.go` and check for paired `run:`
- [ ] migrate `type: command` + `run: foo` → `type: shell` + `cmd: foo`

**Phase 3 — legacy DeployStep `when:` / `check:` string conditions:**

- [ ] testdata files: `grep -rnE '^\s+(when|check):\s+["'\''][^"'\'']' internal/{config,pipeline,deploy,reset,lifecycle}/testdata/ 2>/dev/null`
- [ ] embedded in tests: `grep -rnE '\b(When|Check):\s+["'\'']' internal/{config,pipeline,deploy,reset,lifecycle}/*_test.go 2>/dev/null` (Go-literal struct constructions) and `grep -rnE '^\s+(when|check):\s+["'\''][^"'\'']' internal/{config,pipeline,deploy,reset,lifecycle}/*_test.go 2>/dev/null` (YAML heredocs)
- [ ] every match in a deploy/reset/lifecycle fixture/test becomes a typed object. **Workflow fixtures keep the string form** — skip `internal/usercommands/**/`

**Phase 4 — final sweep:**

- [ ] update each fixture/inline YAML to the new shape; rerun the test that owns it
- [ ] run `make test && make lint` — must pass before Task 8

### Task 8: Rewrite documentation

Single source of truth for the new schema. Describe only the current state — no migration narrative.

- [ ] rewrite `docs/reference/config/deploy.md`: pipeline step schema using `type/cmd/with`, the four executors (`shell`, `devbox`, `command`, `builtin`), `when:` typed-condition syntax, `check:` typed-action syntax (call out the distinction explicitly: `when:` returns bool via `condition.Condition`; `check:` runs an `Action`), list of registered engine builtins (`service_configs_copy`, new `service_configs_check`, `message`, `confirm`, etc.), the separate predicate vocabulary used in `when: type: builtin` (`dir-empty`, `file-exists`, …), `untracked` semantics, and **`continue_on_error` semantics including the new behavior**: failing body with `continue_on_error: true` → step fails, `check:` skipped, pipeline continues; passing body with failing `check:` and `continue_on_error: true` → step fails, pipeline continues (this is a behavior change from prior versions where check failure always aborted), examples mirroring the user's write-up
- [ ] rewrite `docs/reference/config/lifecycle.md`: lifecycle pipelines reuse the same step shape; cross-link to deploy.md
- [ ] rewrite `docs/reference/config/commands.md`: the six command `type:` values (`shell`, `devbox`, `service_exec`, `service_run`, `script`, `workflow`); explicitly document the canonical `cmd:` payload, that `argv:` is the advanced direct-exec alternative, that `script:` is the structured exception, and that **workflow step entries (`steps:` inside `type: workflow`) keep the existing `command:` / `confirm:` / `with:` / `when:` (string) syntax** — flag this as a known scope-narrowing
- [ ] add a "Conditions" section in `docs/reference/config/conditions.md` covering the three `when:` types (`builtin`, `shell`, `template`); document that `when: type: builtin` looks up *predicates* (`dir-empty`, `file-exists`, etc.) while step body / `check: type: builtin` looks up *engine builtins* (`service_configs_copy`, `service_configs_check`, etc.); document shell-condition semantics (hardcoded `sh -c`, exit 0 = true); document template `expr` evaluation context
- [ ] update `docs/reference/config/index.md` cross-references
- [ ] update `AGENTS.md` (canonical; `CLAUDE.md` is a symlink) "Project Structure" section: refresh the `internal/builtin/` registry list (add `service_configs_check`); do not edit `CLAUDE.md` directly
- [ ] run `./bin/devbox docs generate --scope cli` and confirm the generated CLI reference no longer mentions `deploy config` / `deploy config-check`
- [ ] run `make test` — must pass before Task 9

### Task 9: Verify acceptance criteria

- [ ] run full `make test` — all packages green
- [ ] run `make lint` — zero issues; fix findings rather than suppressing
- [ ] `make build && ./bin/devbox info` smoke check
- [ ] grep verification (Go source): zero references to `step.Run`, `step.Devbox`, `step.Command`, `step.Builtin`, `step.ServiceConfigsCopy`, `step.Mode`, `CommandTypeCommand`, `cmd.Run` (on `CommandDef`). **Do NOT grep for `WorkflowStep.Command` / `WorkflowStep.Confirm` / `condition.Classify` / `condition.EvalBuiltin` / `condition.EvalCmd`** — those legitimately survive on the workflow string-condition path
- [ ] grep verification (YAML fixtures): zero matches for legacy DeployStep keys (`run:`, `devbox:`, `command:`, `builtin:`, `service_configs_copy:`, `mode:`) indented under `steps:` in `internal/{config,pipeline,deploy,reset,lifecycle}/testdata/`; zero legacy CommandDef `run:` paired with `type: command`; zero string-form `when:`/`check:` in deploy/reset/lifecycle fixtures
- [ ] verify each requirement from Overview maps to a concrete code change
- [ ] confirm `service_configs_check` is reachable as `check: {type: builtin, cmd: service_configs_check, with: {service: main}}` from a deploy step (covered by the integration test added in Task 5)
- [ ] confirm strict-decode error path: a deliberately-broken fixture with `run:` on a DeployStep produces a clear error message naming the field

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`.*

## Technical Details

**Two separate types — `Condition` for pipeline `when:`, `Action` for `check:` and step bodies:**

```go
// internal/condition/condition.go — typed form for pipeline when:.
// Workflow when: keeps the legacy string form; Classify/EvalBuiltin/EvalCmd remain.
package condition

type Type string

const (
    TypeBuiltin  Type = "builtin"  // predicate name + args in Cmd (e.g. "dir-empty foo")
    TypeShell    Type = "shell"    // sh -c <Cmd>; exit 0 = true, non-zero = false
    TypeTemplate Type = "template" // Go template body in Expr; rendered at plan time by caller
)

type Condition struct {
    Type Type           `yaml:"type"`
    Cmd  string         `yaml:"cmd,omitempty"`
    Expr string         `yaml:"expr,omitempty"`
    With map[string]any `yaml:"with,omitempty"` // reserved; predicate args today live in Cmd
}

func (c Condition) IsRuntime() bool { return c.Type == TypeBuiltin || c.Type == TypeShell }

// EvalRuntimeTyped dispatches to the existing string-based predicate/shell evaluators.
// No new registry; reuses internal/condition's existing logic.
func EvalRuntimeTyped(c Condition, projectRoot string) (bool, error) {
    switch c.Type {
    case TypeBuiltin: return EvalBuiltin(c.Cmd, projectRoot)
    case TypeShell:   return EvalCmd(c.Cmd, projectRoot)
    }
    return false, fmt.Errorf("EvalRuntimeTyped: %s is not a runtime kind", c.Type)
}
```

```go
// internal/config/action.go — used in DeployStep body and DeployStep.Check.
package config

type Action struct {
    Type string         `yaml:"type"` // shell | devbox | command | builtin
    Cmd  string         `yaml:"cmd"`
    With map[string]any `yaml:"with,omitempty"`
}
```

`*Condition` / `*Action` fields: nil = unconditional / no post-check. Non-nil with zero values is rejected by validation.

**`DeployStep` (illustrative — inline fields, not embedded `Action`):**

```go
type DeployStep struct {
    Name string `yaml:"name"`

    // Action shape, inlined directly. Inline beats embedding here because yaml.v3
    // interactions between `,inline`, custom UnmarshalYAML, and KnownFields(true)
    // are awkward to reason about — keep decode boring and predictable.
    Type string         `yaml:"type"` // shell | devbox | command | builtin
    Cmd  string         `yaml:"cmd"`
    With map[string]any `yaml:"with,omitempty"`

    Description     string               `yaml:"description,omitempty"`
    When            *condition.Condition `yaml:"when,omitempty"`
    Check           *Action              `yaml:"check,omitempty"`
    ContinueOnError bool                 `yaml:"continue_on_error,omitempty"`
}

// Action returns the action-shaped slice of this step for ExecAction callers.
func (s DeployStep) Action() Action {
    return Action{Type: s.Type, Cmd: s.Cmd, With: s.With}
}
```

**`pipeline.ExecAction` and `ActionContext` (illustrative):**

```go
// ActionContext carries the inputs that ExecAction needs from a step or check.
// It is constructed once per step by Run and reused for both body and check.
type ActionContext struct {
    WorkDir     string
    Cfg         *config.DevboxConfig
    Reg         *usercommands.Registry
    LogWriter   io.Writer
    SkipConfirm bool
}

// ExecAction is the action-dispatch primitive. It does NOT evaluate when:,
// call the reporter, run hooks, evaluate check:, or apply continue_on_error —
// those concerns stay in pipeline.Run.
func ExecAction(a config.Action, actx ActionContext) error {
    switch a.Type {
    case "shell":   return runShell(actx, a.Cmd)
    case "devbox":  return runDevboxSubcommand(actx, a.Cmd)
    case "command": return execCommand(actx, a.Cmd, a.With)
    case "builtin": return execBuiltin(actx, a.Cmd, a.With) // builds builtin.ExecContext internally
    }
    return fmt.Errorf("unknown action type %q", a.Type)
}
```

**`pipeline.Run` orchestration (unchanged location, fields swapped to typed):**

```
for each ResolvedStep rs:
  evaluate rs.PhaseWhen / rs.RuntimeWhen  ── condition.EvalRuntimeTyped
  reporter.StartStep                       ── reporter
  err := ExecAction(rs.Step.Action(), actx)
  if err != nil:
    handle continue_on_error → FailStep + maybe continue, skip check
    continue
  if rs.Step.Check != nil:
    cerr := ExecAction(*rs.Step.Check, actx)
    if cerr != nil:
      handle continue_on_error → FailStep + maybe continue   ← new symmetric behavior
      continue
  reporter.FinishStep
```

**WorkflowStep (UNCHANGED — out of scope):**

```go
// internal/usercommands/model/types.go — kept as-is for this plan.
type WorkflowStep struct {
    Command         string            `yaml:"command"`
    With            map[string]string `yaml:"with"`
    Confirm         string            `yaml:"confirm"`
    When            string            `yaml:"when"` // legacy mini-language string
    ContinueOnError bool              `yaml:"continue_on_error"`
}
```

**Why `when:` and `check:` use different shapes:** `when:` returns a boolean and supports plan-time `template` evaluation (no side effects); `check:` runs an arbitrary action whose error/success becomes pass/fail. Sharing one type would force `template` into a slot it can't satisfy (no plan-time template after a step ran) and force conditions to support every action executor.

**Two `type: builtin` namespaces (known wart):** `when: {type: builtin, cmd: dir-empty foo}` resolves the *predicate* `dir-empty` inside `internal/condition`. Step body / `check: {type: builtin, cmd: service_configs_check, with: ...}` resolves the *engine builtin* `service_configs_check` inside `internal/builtin`. Same keyword, different registries, disambiguated by YAML position. Documented in conditions.md; merging the registries is a follow-up plan.

**Import direction preserved:** `tpl` imports `condition` (existing). `condition` does NOT import `tpl` or `builtin`. Plan-time template rendering for `Condition.Expr` lives in the caller (pipeline/resolve), which already imports both `condition` and `tpl`.

**Condition portability:** shell-typed conditions continue to use hardcoded `sh -c` (not the project's configured `ShellBin`) — preserved from the existing intentional design.

**Strict-decode caveat:** `yaml.v3`'s `Decoder.KnownFields(true)` is strict at every level, so any extra keys anywhere in the file fail. That's the desired behavior for deploy/reset/lifecycle/command-file loaders. If a fixture intentionally tolerates extra keys, that loader stays loose.

**Open decisions to flag during implementation:**

- ⚠️ `CommandDef.Argv` survives alongside `CommandDef.Cmd` (mutually exclusive) — confirmed by user. Revisit only if Task 4 validation gets ugly.
- ⚠️ `script:` structured form remains the exception. Revisit only if the schema docs read as inconsistent.
- ⚠️ The two `type: builtin` registries (predicate vs engine) is an explicit known wart. Don't try to clean up here — that's a separate plan.

## Post-Completion

*Items requiring manual intervention or external systems — informational only.*

**Manual project-YAML migration (out of scope for this CLI repo):**

- Any consuming Devbox project's `devbox/deploy.yml`, `devbox/lifecycle.yml`, `devbox/reset.yml`, and `devbox/commands/**/*.yml` must be hand-migrated to the new typed shape. The CLI will refuse to load legacy YAML with a clear validation error pointing to the new field names.
- Suggested user-side migration order: conditions (`when:`/`check:`) → deploy/lifecycle/reset steps → user commands.
- The CLI does not ship a migration script. If the user wants one, it lives outside this repo.

**External verification:**

- Smoke-test on the user's primary Devbox project after release: run `devbox deploy`, `devbox run`, `devbox stop`, and a representative user command, confirming both happy path and a deliberately-failing condition.
