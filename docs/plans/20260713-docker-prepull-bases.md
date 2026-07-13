# Docker build base prepull (`build.prepull_bases`)

## Overview

`dwe docker build` (and first-deploy `dwe docker up`, which builds missing images) fails on
`FROM git.horn/...` bases with buildkit `failed to fetch oauth token … no route to host`:
Docker Desktop's buildkit fetcher cannot reach LAN registries, while daemon-side
`docker pull` reaches them fine. Proven experimentally across all builder drivers
(docker / docker-container / host-net); switching gvisor↔vpnkit does not help. Only
services whose base image is absent from the local image store fail — the docker-driver
buildkit shares the daemon's image store, so a locally-present `FROM` resolves without
network.

Fix: an **opt-in** `build.prepull_bases: true` flag in `workspace/docker.yml`. When
enabled, before `compose build` / `compose up` dwe derives the external `FROM` base
images of the services about to build and best-effort `docker pull`s the **missing**
ones via the daemon, so buildkit resolves every `FROM` locally. Buildkit stays the
builder (no deprecated classic-builder fallback).

Alternatives considered and rejected during design: `DOCKER_BUILDKIT=0` (deprecated
builder, different cache semantics); retry-on-failure by matching buildkit error text
(brittle); a declarative prepull ref list in docker.yml (duplication drifting from
Dockerfiles); fixing at the Docker Desktop level (per-developer, not a tool fix).

## Context (from discovery)

- `internal/core/project/config/docker.go` — `DockerConfig`, lenient loader
  (`yaml.Unmarshal`, no `KnownFields`), base + `docker.local.yml` merged via `deepMerge`.
  No interplay with `detectPresentArgsKeys` (that mechanism is `args:`-only). No clash
  with `DockerArgs.Build` (that one is nested under `args:`).
- `internal/cli/docker/docker.go` — `newDockerBuildCmd` / `newDockerUpCmd` /
  `resolveBuildInvocation` / `newDockerPipeline` (shared setup; regenerates `.env` for
  `up`/`build` before the command runs — ordering matters for `compose config`).
- `internal/shared/docker/compose.go` — `Compose`, `BuildArgs` vs `BuildInternalArgs`,
  `output()`, `BuildEnv()`, `BinName()`. Raw-docker exec pattern in `volumes.go`
  (`exec.Command(bin, "volume", "inspect", name).Run()` + `//nolint:gosec`).
- Test seams: `writeStub(t, body)` stub docker binary helper at
  `internal/shared/docker/compose_test.go:625`.
- Default deploy/lifecycle pipelines run `dwe docker up --wait`
  (`internal/core/workflow/deploy/defaults.go`), and `compose up` builds missing images
  — that is why the prepull hook covers **both** `build` and `up`.
- Already verified — no work needed: the `dockerValidator` in
  `internal/core/validate/config/workspace.go` has no field allowlist (it only checks
  that `LoadDockerConfig` loads cleanly), so the new `build:` key needs no validator
  change; `envtest.WriteDockerIdentity` preserves a copied `docker.yml` untouched
  (writes only `project_name` into `docker.local.yml`), so `build:` flows into test
  copies; the ru docs mirror `docs/i18n/ru/reference/config/docker.md` exists and must
  be updated alongside the en page.

## Critical constraints for the executor (traps — read before every task)

1. **`BuildInternalArgs`, never `BuildArgs`, for `compose config --format json`** —
   user-configured `args.global` (e.g. `--ansi always`) must not corrupt
   machine-readable JSON output.
2. **`internal/cli/` is the single writer to stdout/stderr.** `internal/shared/docker`
   returns mechanics + errors only and never prints user-facing text (`trace.Command`
   echoes and streamed subprocess output are fine — user-facing *messages* are not).
3. **The entire prepull step is advisory.** Any internal error (compose config,
   Dockerfile parse, pull) → warning + proceed to `compose build`/`up`. Enabling the
   flag must never make a build *worse* than without it.
4. **`--force` semantics change ONLY under the flag.** Flag off → `--no-cache --pull`
   byte-identical to today. Flag on + `--force` → daemon-side unconditional pull of the
   derived bases, and compose gets only `--no-cache` (no `--pull`).
5. **Run `make test`, never bare `go test ./...`** (embedded docs tree is generated).
   For focused iteration: `make embedded-docs` once, then
   `go test ./internal/shared/docker/... -run TestName` is fine.
6. **No moby/buildkit dependency.** The repo has zero docker SDK deps and shells out to
   the docker binary everywhere; the Dockerfile FROM/ARG parser is hand-rolled.
7. Go files: tabs, `gofmt` + `goimports`; code comments in English; conventional
   commits `feat(docker): ...`.

## Development Approach

- **testing approach**: Regular (code first, then tests within the same task) —
  table-driven, matching repo convention
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional — they are a required part of the checklist
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- maintain backward compatibility: with `build.prepull_bases` absent/false, every code
  path must be byte-identical to current behavior (zero extra execs)

## Testing Strategy

- **unit tests**: required for every task; parser tests are pure table-driven; exec
  paths use the `writeStub` stub-binary pattern from
  `internal/shared/docker/compose_test.go`
- no UI e2e in this repo; final verification is `make test` + `make lint`

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Solution Overview

Flow (flag on): `dwe docker build [svc...]` / `dwe docker up` →
`compose config --format json` → collect `build.{context,dockerfile,dockerfile_inline,args}`
per service → parse Dockerfiles for external `FROM` refs (ARG-substituted, stages and
`scratch` excluded) → dedupe → for each ref missing locally (`docker image inspect`)
run `docker pull <ref>` streaming to the terminal → proceed to the normal
`compose build`/`compose up`.

Key decisions:
- **Missing-only pull by default** — precisely fixes the broken case without changing
  the "cached base is not refreshed" semantics and without re-pulling bases already
  present locally.
- **`--force` + flag on** pulls all derived bases unconditionally (daemon-side
  "re-pull base images") and drops `--pull` from compose args (buildkit `--pull` is
  broken for LAN registries — same root cause).
- **Both `build` and `up` hooks** — default deploy runs `dwe docker up --wait`, and
  `compose up` builds missing images via the same broken buildkit path; hooking only
  `build` would leave first deploy on a clean machine broken.
- Service filter: `build` with args → only the named services; `up` → all services
  from the config output (up builds dependencies too; args must not narrow).

## Technical Details

Config (`internal/core/project/config/docker.go`):

```go
// DockerBuildConfig holds image-build policy for compose build/up paths.
type DockerBuildConfig struct {
    // PrepullBases pulls external FROM base images via the daemon before
    // buildkit builds, working around builders that cannot reach LAN registries.
    PrepullBases bool `yaml:"prepull_bases"`
}

// on DockerConfig:
Build DockerBuildConfig `yaml:"build"`
```

Derivation (`internal/shared/docker/prepull.go` + `dockerfilerefs.go`):

- `func (c *Compose) DeriveBuildBases(services []string) ([]string, error)` — empty
  `services` = all. Runs `c.output(c.BuildInternalArgs("config", "--format", "json"))`
  on the **same Compose instance** the command will `Exec` with (so `--all` /
  `ComposeFilesAll` is handled for free). JSON shape: `services.<name>.build.
  {context, dockerfile, dockerfile_inline, args}`; `context` is absolute after
  `compose config`; `dockerfile` is relative to context, default `Dockerfile`;
  `dockerfile_inline` is parsed from the string directly; services without `build:`
  are skipped. Returns sorted unique external refs.
- Parser (`dockerfilerefs.go`), hand-rolled, handles: line continuations `\`,
  comments, case-insensitive instructions, the `# escape=` directive (one check at
  file start); `ARG NAME=default` before the first `FROM` collected as defaults with
  compose `build.args` overriding them; `FROM [--platform=...] <ref> [AS stage]` —
  stage names accumulate, refs matching a previously declared stage or `scratch` are
  not external; `${VAR}`, `${VAR:-def}`, `$VAR` substitution in refs from ARG values;
  a ref with an unresolvable variable (incl. buildkit builtins like `$BUILDPLATFORM`)
  is skipped with a `trace` warning, never an error.

Pull mechanics (`internal/shared/docker/prepull.go`, patterned after `volumes.go`):

- `ImageExists(ref)` — `exec.Command(bin, "image", "inspect", ref)` with `c.BuildEnv()`
  / `c.BaseDir`; exit ≠ 0 → missing (best-effort: treat probe failure as missing).
- `PullImage(ref)` — `exec.Command(bin, "pull", ref)` with stdout/stderr **streamed to
  the terminal** (base pulls can take minutes; silence looks like a hang), env/dir as
  above, echo via `trace.Command` like `Compose.Exec` does.

CLI integration (`internal/cli/docker/docker.go`):

- `resolveBuildInvocation` gains a `prepull bool` parameter: `force && prepull` →
  extraArgs get only `--no-cache`; `force && !prepull` → `--no-cache --pull` as today.
- New `prepullBases(errOut io.Writer, compose *dockerpkg.Compose, services []string, force bool)`
  helper in `internal/cli/docker/` — the derive → inspect → pull → warn loop, the only
  place that prints user-facing text (warnings via `fmt.Fprintf(errOut, "warning: ...\n")`,
  `errOut = cmd.ErrOrStderr()` at call sites). It **returns nothing** (or always-nil):
  warnings only, execution always proceeds to compose. One loud case: a **missing**
  base failed to pull → explicit warning naming the ref and stating the build will
  likely fail.
- Call sites, both gated on `p.dockerCfg.Build.PrepullBases` and both after
  `newDockerPipeline` (`.env` regenerated — required by `compose config`):
  - build `RunE`: `resolveBuildInvocation(..., prepull)` →
    `prepullBases(cmd.ErrOrStderr(), compose, args, force)` → `compose.Exec("build", extraArgs...)`
  - up `RunE`: `prepullBases(cmd.ErrOrStderr(), p.compose, nil, false)` → `p.compose.Exec("up", extra...)`
- `dwe docker up` as a deploy-pipeline subprocess (`type: dwe` step): pull streaming
  lands in the step log like compose output — nothing special needed.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, and docs changes in this repo
- **Post-Completion** (no checkboxes): enabling the flag in consuming projects, manual
  verification against a real LAN registry

## Implementation Steps

### Task 1: Add `build.prepull_bases` config field

**Files:**
- Modify: `internal/core/project/config/docker.go`
- Modify: `internal/core/project/config/docker_test.go`

- [ ] add `DockerBuildConfig` struct (`PrepullBases bool \`yaml:"prepull_bases"\``) and
      `Build DockerBuildConfig \`yaml:"build"\`` field on `DockerConfig`, with doc
      comments per Technical Details
- [ ] write test: `build.prepull_bases: true` in docker.yml loads as `Build.PrepullBases == true`
- [ ] write test: absent `build:` block → zero value `false`
- [ ] write test: docker.local.yml overriding `build.prepull_bases` wins over base (deepMerge)
- [ ] run `go test ./internal/core/project/config/...` — must pass before task 2

### Task 2: Dockerfile FROM/ARG parser

**Files:**
- Create: `internal/shared/docker/dockerfilerefs.go`
- Create: `internal/shared/docker/dockerfilerefs_test.go`

- [ ] implement `externalBaseRefs(dockerfile []byte, buildArgs map[string]string) []string`
      (exact name/signature at implementer's discretion, but pure — no I/O): handles
      continuations, comments, case-insensitive instructions, `# escape=` directive,
      pre-FROM `ARG` defaults overridden by buildArgs, `FROM [--platform=...] <ref> [AS stage]`,
      stage-name refs and `scratch` excluded, `${VAR}`/`${VAR:-def}`/`$VAR` substitution,
      unresolvable-var refs skipped with a `trace` warning. Documented limitation (in
      the func comment, not handled): a `FROM x` line inside a `RUN <<EOF` heredoc body
      would be misparsed — extremely rare and advisory-safe
- [ ] write table-driven tests: simple FROM; multi-stage with `AS` + stage reuse; `scratch`;
      ARG default used in FROM; buildArgs override beating ARG default; `${VAR:-def}` fallback;
      `--platform` flag on FROM; line continuation inside FROM/ARG; comments interleaved
- [ ] write error/edge tests: unresolvable `${UNSET}` ref skipped; `$BUILDPLATFORM`-style
      builtin skipped; empty file → no refs; `# escape=\`` directive honored;
      lowercase `from`/`arg` recognized
- [ ] run `go test ./internal/shared/docker/... -run Dockerfile` — must pass before task 3

### Task 3: `DeriveBuildBases` — compose config → external refs

**Files:**
- Create: `internal/shared/docker/prepull.go`
- Create: `internal/shared/docker/prepull_test.go`

- [ ] implement `func (c *Compose) DeriveBuildBases(services []string) ([]string, error)`
      per Technical Details: `c.output(c.BuildInternalArgs("config", "--format", "json"))`,
      narrow JSON struct for `services.<name>.build`, `dockerfile` resolved relative to
      absolute `context` (default `Dockerfile`) — but when `dockerfile` is already
      absolute, use it as-is (`filepath.IsAbs` guard; compose config emits absolute
      dockerfile values verbatim), `dockerfile_inline` parsed directly, per-service
      `build.args` (compose config normalizes to `map[string]string` and DROPS
      null-valued args entirely — defensive null handling is fine but the normal case
      is a plain string map; a dropped arg correctly falls back to the Dockerfile's
      pre-FROM `ARG` default), service filter (empty = all), dedupe + sort
- [ ] write tests with `writeStub` (canned `compose config` JSON on stdout) + real temp
      Dockerfiles: multi-service dedupe; service filter honored; service without `build:`
      skipped; `dockerfile_inline`; `build.args` override reaching the parser
- [ ] write error tests: stub exiting non-zero → error returned (caller degrades it to a
      warning); unreadable Dockerfile → error or per-service skip (pick one, document in
      the func comment); assert the stub received `compose config` WITHOUT global args
      (BuildInternalArgs contract)
- [ ] run `go test ./internal/shared/docker/...` — must pass before task 4

### Task 4: `ImageExists` / `PullImage` mechanics

**Files:**
- Modify: `internal/shared/docker/prepull.go`
- Modify: `internal/shared/docker/prepull_test.go`

- [ ] implement `func (c *Compose) ImageExists(ref string) bool` — raw
      `exec.Command(bin, "image", "inspect", ref)` (pattern: `volumes.go` +
      `//nolint:gosec`), `c.BuildEnv()` env, `c.BaseDir` dir, exit ≠ 0 → false.
      NOTE: setting env+dir is a DELIBERATE deviation from `volumeExists` (which sets
      neither) — required so `DOCKER_HOST`/context overrides from `process_env` apply;
      do not "simplify" it back
- [ ] implement `func (c *Compose) PullImage(ref string) error` — `exec.Command(bin, "pull", ref)`,
      stdout/stderr connected to `os.Stdout`/`os.Stderr` (streaming), env/dir as above,
      `trace.Command` echo before running (mirror `Compose.Exec`)
- [ ] write tests with `writeStub`: ImageExists true/false by stub exit code; PullImage
      invokes `pull <ref>` (record args via stub) and propagates non-zero exit as error
- [ ] run `go test ./internal/shared/docker/...` — must pass before task 5

### Task 5: CLI wiring — build + up hooks, `--force` interplay

**Files:**
- Modify: `internal/cli/docker/docker.go`
- Modify: `internal/cli/docker/docker_test.go`

- [ ] add `prepull bool` parameter to `resolveBuildInvocation`; `force && prepull` →
      only `--no-cache`; all other combinations byte-identical to today
- [ ] update ALL existing `resolveBuildInvocation` call sites for the new parameter —
      besides the production site (`docker.go`), `docker_test.go` calls it at lines
      ~384, ~598, ~648, ~649; adapt those calls (pass `false`), do NOT delete or
      rewrite the tests themselves
- [ ] implement `prepullBases(errOut io.Writer, compose *dockerpkg.Compose, services []string, force bool)`:
      derive (error → warning, return); for each ref — `force` → always pull,
      else pull only when `!ImageExists(ref)`; pull failure of a missing base → loud
      warning naming the ref ("build will likely fail"); pull failure otherwise →
      quieter warning; never returns an error, execution always continues (advisory
      contract — trap #3). Warnings via `fmt.Fprintf(errOut, "warning: ...\n")` with
      `errOut = cmd.ErrOrStderr()` at the call sites (the `internal/cli/service/`
      convention; keeps the helper testable). stderr is safe in the deploy-subprocess
      context — `dwe docker up` runs as a child process, not inside the parent's live
      frame
- [ ] wire build `RunE` and up `RunE` behind `p.dockerCfg.Build.PrepullBases` per
      Technical Details (order: resolve invocation → prepull → Exec); flag off → zero
      extra execs
- [ ] extend `resolveBuildInvocation` table tests: (force,prepull) matrix — (t,t) →
      `--no-cache` only; (t,f) → `--no-cache --pull`; (f,*) → services only
- [ ] write tests for `prepullBases` with `writeStub`: missing ref pulled; present ref
      skipped; force pulls present ref; derivation failure → warning text on stderr and
      no panic/no error; verify warning wording for the missing-base-pull-failed case
- [ ] run `go test ./internal/cli/docker/...` — must pass before task 6

### Task 6: Documentation

**Files:**
- Modify: `docs/reference/config/docker.md`
- Modify: `docs/i18n/ru/reference/config/docker.md`
- Modify: `docs/internals/packages.md`

- [ ] add `build:` section to `docs/reference/config/docker.md`: `prepull_bases` schema,
      missing-only semantics, `--force` behavior under the flag, advisory guarantee,
      covers both `dwe docker build` and `dwe docker up` (hence deploy), rationale
      (Docker Desktop buildkit fetcher vs LAN registry)
- [ ] mirror the same section in `docs/i18n/ru/reference/config/docker.md` (in Russian)
- [ ] add a short note to `docs/internals/packages.md` § `internal/shared/docker/`
      (DeriveBuildBases/ImageExists/PullImage + the advisory + BuildInternalArgs
      contracts) and to the § CLI section covering the prepull hook placement
- [ ] run `make build` (embedded docs regeneration) — must succeed before task 7

### Task 7: Verify acceptance criteria

- [ ] verify all Overview requirements: flag default-off byte-identical paths; missing-only
      pull; force interplay; advisory degradation; build+up coverage
- [ ] verify edge cases from the trap list (grep new code: no `BuildArgs` for config JSON,
      no printing from `internal/shared/docker`)
- [ ] run full test suite: `make test`
- [ ] run `make lint`

### Task 8: [Final] Update documentation and close out

- [ ] update `AGENTS.md` Critical Patterns ONLY if a genuinely load-bearing trap emerged
      during implementation that future agents must know (otherwise skip — packages.md
      from task 6 is the right home)
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification** (a consuming dwe project on Docker Desktop with a LAN registry):
- enable `build: { prepull_bases: true }` in `workspace/docker.yml` (or `docker.local.yml`)
- `docker image rm <lan-registry>/<base>` for a service base, then `dwe docker build <svc>`
  → base is pulled by the daemon, buildkit build succeeds
- fresh-clone scenario: `dwe deploy run` on a machine without cached bases succeeds
- `dwe docker build --force <svc>` → bases re-pulled daemon-side, compose gets `--no-cache` only

**External system updates**:
- after enabling the flag in the consuming project: remove any per-service pull-base
  workaround steps from `workspace/services/*/deploy.yml` (they become duplicates)
