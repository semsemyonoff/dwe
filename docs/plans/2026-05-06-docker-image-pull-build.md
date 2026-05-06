# Docker image pull/build subcommands

## Overview

Add two new generic image-management subcommands to `devbox docker`:

```
devbox docker pull  [--all] [services...]
devbox docker build [--all] [--force] [services...]
```

Behavior:

- Default file set is the current active compose pipeline: `compose.base` + every enabled tool overlay + every enabled service overlay (i.e. `cfg.ComposeFiles()`).
- `--all` switches the file set to `base + every configured overlay`, regardless of whether the overlay is enabled in `devbox/local.yml`. It is a per-invocation override only — it MUST NOT mutate `devbox/local.yml` or otherwise change persistent enabled state.
- `--force` is valid for `build` only. It maps to appending `--no-cache --pull` to the `docker compose build` invocation.
- `[services...]` are passed through to `docker compose pull|build` as positional service arguments.

Legacy `make` targets that should map onto the new commands:

```
image_pull              -> devbox docker pull
image_pull_all          -> devbox docker pull --all
image_rebuild           -> devbox docker build
image_rebuild_%         -> devbox docker build <service>
image_rebuild_force     -> devbox docker build --force
image_rebuild_all       -> devbox docker build --all
image_rebuild_all_force -> devbox docker build --all --force
```

Note on legacy nuance: `image_rebuild_%` in `legacy/devbox/make/commands/image.mk` actually used `docker-compose-all` (it built single services against the all-overlay file list). The user's spec for the new CLI deliberately drops that — `devbox docker build <svc>` uses the active compose set, not all overlays. Users who want the old behavior write `devbox docker build --all <svc>`. This is an intentional behavior change documented here so it does not get surprise-reverted.

Out of scope (stay project-specific, do not migrate):
- `image_base_build_*` (base image build via per-service `bash build.sh`)
- `image_show_tags`

## Context (from discovery)

Files involved:

- `internal/command/docker.go` — cobra command tree (`newDockerCmd`, sibling `newDockerUpCmd`/etc., `newDockerPipeline`). New subcommand constructors go here.
- `internal/command/docker_test.go` — register/wiring tests for docker subcommands.
- `internal/command/lifecycle_test.go` — `TestDockerGroupStillIntact` (line 57) asserts the exhaustive list of registered docker subcommands; the `want` slice at line 61 must be extended for `pull` and `build`.
- `internal/docker/compose.go` — `Compose` struct, `NewCompose`, `BuildArgs`, `Exec`, per-command default args map. New `NewComposeAll` lives here.
- `internal/docker/compose_test.go` — unit tests for arg construction.
- `internal/config/devbox.go` — `ComposeFiles()` at line 205 over `Compose.Base`, `Compose.Overlays`, `Services[*].Compose`. New `ComposeFilesAll()` sits next to it.
- `internal/config/docker.go` — `DockerArgs` struct (lines 82–93), per-subcommand default `[]string` lists. Add `Pull` and `Build` fields.
- `docs/reference/config/docker.md` — documents `args.*` keys and the docker subcommand surface. Update for `pull`/`build` and `--all`/`--force`.

Patterns found:

- Each docker subcommand is a thin cobra constructor that calls `newDockerPipeline(flags, "<command>")` and then `p.compose.Exec("<command>", args...)`. Pull/build will follow the same shape, with one extra step: when `--all` is set, swap the `Compose` instance for one built from `ComposeFilesAll()`.
- `dockerCfg.Env.ShouldGenerateEnv(command)` is consulted in `newDockerPipeline` to decide whether to regenerate `.env`. Pull/build are passed to that function as their command names ("pull" / "build"), so users can opt into env generation by adding them to `env.commands` in `docker.yml`. No code change is needed for that mechanism — it already key-matches strings.
- `Compose.CommandArgs` is a `map[string][]string` keyed by docker subcommand. `NewCompose` populates it from `DockerArgs.Up`, `.Down`, etc. We extend with `pull` and `build` keys mapped to the new `DockerArgs.Pull` / `.Build` fields.

Dependencies identified:

- No new third-party dependencies.
- `--force` interacts only with `BuildArgs("build", ...)`: appending `"--no-cache"`, `"--pull"` after defaults but before user-supplied positional args. Since `BuildArgs` puts caller-supplied `extraArgs` last, the cleanest path is for the build cobra command to compute its own extra-args slice (force flags + service positionals) and pass that into `Exec`.

Approach decisions (from planning):

- `ComposeFilesAll()` lives as a method on `*DevboxConfig`, mirroring `ComposeFiles()`.
- Compose plumbing uses a sibling constructor `NewComposeAll(cfg, dockerCfg)` instead of mutating `Files` post-construction or adding option args to `NewCompose`. Existing callers of `NewCompose` are unaffected.
- Testing approach: regular (code first, then unit tests in adjacent `_test.go`).

### Test seam for command-level tests (important)

`docker.Compose.Exec` is hardwired to `exec.Command`, so a naive cobra-level test of `RunE` would actually shell out to docker. We avoid that by splitting each new cobra command's logic into a pure helper that produces what `Exec` needs, and then testing the helper directly:

```go
// returns the *Compose to use and the extra args to pass to Exec("build", extraArgs...)
func resolveBuildInvocation(cfg *config.DevboxConfig, dockerCfg *config.DockerConfig,
    all, force bool, services []string) (*docker.Compose, []string)

func resolvePullInvocation(cfg *config.DevboxConfig, dockerCfg *config.DockerConfig,
    all bool, services []string) (*docker.Compose, []string)
```

Each cobra `RunE` becomes a thin shell: load config via `newDockerPipeline`, call the resolver, then `compose.Exec(cmd, extraArgs...)`. Tests target the resolver: assert the returned `Compose.Files` matches `ComposeFiles()` vs `ComposeFilesAll()`, and assert the returned `extraArgs` slice (force flags + service positionals) — using `BuildArgs("build", extraArgs...)` for end-to-end argument shape verification. No `Exec`, no Docker, no fake binary needed.

## Development Approach

- Testing approach: **Regular** (code first, tests second).
- Complete each task fully before moving to the next.
- Make small, focused changes.
- Every task that ships code MUST add or update tests in the same task. Tests are not optional.
- All tests must pass before starting the next task.
- Update this plan file when scope shifts during implementation.
- Run `make test` (or `go test ./<package>`) after each change.
- Maintain backward compatibility: existing docker subcommands keep their current behavior; `args.up`/`args.down`/etc. behavior is unchanged.

## Testing Strategy

- **Unit tests**: required for every task touching code.
  - `internal/config`: `ComposeFilesAll` must exercise base-only, with disabled tool overlay, with disabled service overlay, mixed enabled/disabled, and ordering invariants (sorted tool keys, sorted service names).
  - `internal/docker`: `NewComposeAll` must populate `Files` from `ComposeFilesAll()`. `BuildArgs("pull", ...)` and `BuildArgs("build", ...)` must include configured `args.pull`/`args.build` defaults and place `extraArgs` after them.
  - `internal/command`: registration test for `pull` and `build` under `devbox docker`; flag parsing tests for `--all` and `--force` (build only — `pull --force` must error).
- **E2E tests**: this repo has no Playwright/Cypress; nothing to add at that layer. Manual verification entries go in Post-Completion.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.

## What Goes Where

- **Implementation Steps** (`[ ]`): code, config, tests, docs — all automatable inside this repo.
- **Post-Completion** (no checkboxes): manual sanity runs against a real project; consumers updating their `docker.yml` to use new `args.pull`/`args.build`.

## Implementation Steps

### Task 0: Capture coverage baseline

- [x] run `go test -cover ./internal/config ./internal/docker ./internal/command` on the unchanged tree
- [x] record the per-package coverage % directly in this plan in a "Baseline" line below this task (e.g. `Baseline: config=72.3%, docker=81.0%, command=65.4%`) so Task 5 has a concrete target to compare against
- [x] no code changes; no tests to run beyond the baseline command itself

Baseline: config=85.1%, docker=36.6%, command=58.2%

### Task 1: Add `ComposeFilesAll()` to DevboxConfig

- [x] in `internal/config/devbox.go`, add method `(c *DevboxConfig) ComposeFilesAll() []string` next to `ComposeFiles()` (~line 205). It must:
  - emit `Compose.Base` first when non-empty
  - iterate `Compose.Overlays` in sorted key order, appending each overlay path unconditionally (no `toolOverlayEnabled` gate)
  - iterate `Services` in sorted name order, appending each service's `Compose` paths unconditionally (no `Enabled` gate)
- [x] add table-driven tests in `internal/config/devbox_test.go` (config-package coverage) covering: base only, base + disabled tool overlay, base + disabled service overlay, mixed enabled/disabled across both, missing base. Note: `internal/command/compose_test.go` exercises `ComposeFiles()` from the command-package side; new `ComposeFilesAll()` coverage belongs in the config package and should NOT be added to `internal/command/compose_test.go`.
- [x] run `go test ./internal/config/...` — must pass before next task

### Task 2: Wire `pull`/`build` into Compose policy args

- [x] in `internal/config/docker.go`, extend `DockerArgs` struct with `Pull []string` (yaml: `pull`) and `Build []string` (yaml: `build`)
- [x] in `internal/docker/compose.go`, extend the `cmdArgs` map inside `NewCompose` to register `"pull": dockerCfg.Args.Pull` and `"build": dockerCfg.Args.Build`
- [x] add `NewComposeAll(cfg, dockerCfg)` in `internal/docker/compose.go` that builds the same `Compose` as `NewCompose` but populates `Files` from `cfg.ComposeFilesAll()`. Internally factor out a private builder so `NewCompose`/`NewComposeAll` differ only in the file source — no code duplication of the `cmdArgs` map
- [x] add YAML loader coverage in `internal/config/docker_test.go` (the file already exercises `args.up` parsing and override behavior at lines ~12 and ~117): assert `LoadDockerConfig` populates `Args.Pull` and `Args.Build` from `args.pull` / `args.build`, and assert that `docker.local.yml` replaces (does not merge) the tracked list — matching the existing `args.up` override test case
- [x] add tests in `internal/docker/compose_test.go`:
  - `NewComposeAll` populates `Files` from `ComposeFilesAll()` (covering a fixture where enabled vs all differs)
  - `BuildArgs("pull", "svc")` emits configured `args.pull` defaults before `"svc"`
  - `BuildArgs("build", "--no-cache", "--pull", "svc")` places those after `args.build` defaults
- [x] run `go test ./internal/config/... ./internal/docker/...` — must pass before next task

### Task 3: Add `devbox docker pull` cobra command

- [ ] in `internal/command/docker.go`, add a pure helper `resolvePullInvocation(cfg *config.DevboxConfig, dockerCfg *config.DockerConfig, all bool, services []string) (*docker.Compose, []string)` that returns the `*Compose` (built via `NewCompose` or `NewComposeAll` based on `all`) and the extra args slice (just `services` for pull) — no Exec call inside the helper
- [ ] in `internal/command/docker.go`, add `newDockerPullCmd(flags *rootFlags) *cobra.Command`:
  - `Use: "pull [services...]"`, short description, `SilenceUsage: true`
  - bool flag `--all` (default false)
  - `RunE`: call `newDockerPipeline(flags, "pull")`; pass `p.cfg, p.dockerCfg, all, args` into `resolvePullInvocation`; call `compose.Exec("pull", extraArgs...)` on the result
- [ ] register it in `newDockerCmd` (in `internal/command/docker.go`, around line 21–31). Place it after `newDockerWaitCmd` and before `newDockerProjectNameCmd` — pull/build are image-management primitives and group naturally at the end of the lifecycle list before the diagnostic `project-name` command
- [ ] update `internal/command/lifecycle_test.go` `TestDockerGroupStillIntact` (line 57): extend the `want` slice at line 61 to include `"pull"` (currently `[]string{"up", "down", "stop", "restart", "logs", "ps", "exec", "run", "wait", "project-name"}`). Without this update, registering pull breaks the test.
- [ ] add tests in `internal/command/docker_test.go`:
  - command registers under `devbox docker pull`
  - `resolvePullInvocation(cfg, dockerCfg, false, []string{"svc"})` returns a `*Compose` whose `Files == cfg.ComposeFiles()` and extra args `[]string{"svc"}`
  - `resolvePullInvocation(cfg, dockerCfg, true, []string{"svc"})` returns a `*Compose` whose `Files == cfg.ComposeFilesAll()`
  - `newDockerPullCmd(flags).ParseFlags([]string{"--force"})` returns an error (cobra rejects unknown flag before `RunE`); confirms `--force` is build-only and cannot leak onto pull
- [ ] run `go test ./internal/command/...` — must pass before next task

### Task 4: Add `devbox docker build` cobra command

- [ ] in `internal/command/docker.go`, add a pure helper `resolveBuildInvocation(cfg *config.DevboxConfig, dockerCfg *config.DockerConfig, all, force bool, services []string) (*docker.Compose, []string)` that returns the `*Compose` (NewCompose vs NewComposeAll based on `all`) and the extra args slice. When `force` is true, the extra args begin with `"--no-cache", "--pull"` followed by `services`; otherwise just `services`
- [ ] in `internal/command/docker.go`, add `newDockerBuildCmd(flags *rootFlags) *cobra.Command`:
  - `Use: "build [services...]"`, short description, `SilenceUsage: true`
  - bool flag `--all`
  - bool flag `--force`
  - `RunE`: build pipeline with `"build"`; pass into `resolveBuildInvocation`; call `compose.Exec("build", extraArgs...)`
- [ ] register it in `newDockerCmd` directly after the `pull` registration from Task 3 (so the order ends `... wait, pull, build, project-name`)
- [ ] update `internal/command/lifecycle_test.go` `TestDockerGroupStillIntact` `want` slice (now updated for `pull` in Task 3) to also include `"build"`
- [ ] add tests in `internal/command/docker_test.go`:
  - command registers under `devbox docker build`
  - `resolveBuildInvocation(cfg, dockerCfg, false, false, nil)`: `Compose.Files == cfg.ComposeFiles()`, extraArgs is empty
  - `resolveBuildInvocation(cfg, dockerCfg, true, false, []string{"svc"})`: `Compose.Files == cfg.ComposeFilesAll()`, extraArgs `[]string{"svc"}`
  - `resolveBuildInvocation(cfg, dockerCfg, false, true, []string{"svc"})`: `Compose.Files == cfg.ComposeFiles()`, extraArgs `[]string{"--no-cache", "--pull", "svc"}`
  - `resolveBuildInvocation(cfg, dockerCfg, true, true, []string{"svc"})`: `Compose.Files == cfg.ComposeFilesAll()`, extraArgs `[]string{"--no-cache", "--pull", "svc"}`
  - end-to-end shape: feed the returned `(compose, extraArgs)` into `compose.BuildArgs("build", extraArgs...)` and assert that any configured `args.build` defaults sit before the force flags, and the force flags sit before positional services (i.e. `... build <args.build> --no-cache --pull svc`)
- [ ] run `go test ./internal/command/...` — must pass before next task

### Task 5 (Task N-1): Verify acceptance criteria

- [ ] verify `devbox docker pull` and `devbox docker build` appear in `devbox docker --help` and have working `--help` output
- [ ] verify the legacy mapping table from Overview matches actual emitted `BuildArgs` for each row (write a single table-driven test in `internal/command/docker_test.go` that asserts the docker invocation for each legacy key)
- [ ] verify `--all` does not write to `devbox/local.yml`. Test approach: in a fixture project under a temp dir with a known `devbox/local.yml`, capture its SHA-256, call `resolvePullInvocation(cfg, dockerCfg, true, nil)` and `resolveBuildInvocation(cfg, dockerCfg, true, false, nil)`, re-hash, and assert equality. Combined with the resolver-purity coverage in Tasks 3/4 (resolvers take only struct inputs and return only structs/slices, no path or writer params) this is sufficient — the cobra `RunE` shells are thin wrappers that only call `newDockerPipeline` (read-only load) and `compose.Exec` (read-only `docker compose ...`), so no write path exists. No build tags or executor injection needed
- [ ] run full `make test`
- [ ] run `make lint` — all issues must be fixed
- [ ] run `go test -cover ./internal/config ./internal/docker ./internal/command` and confirm each package coverage % is at or above the values captured in Task 0 baseline (recorded in this plan when Task 0 runs)

### Task 6: [Final] Update documentation

- [ ] in `docs/reference/config/docker.md`:
  - update `args` section (around line 95) to list `pull` and `build` as available subcommand keys
  - add a "Subcommand reference" or extend the existing `devbox docker ...` table with `pull` and `build` rows, including `--all` and `--force` semantics
  - call out that `--all` is a per-invocation file-set override and does NOT modify `devbox/local.yml`
- [ ] if `env.commands` example mentions specific subcommands, add `pull` / `build` to the example as candidates worth auto-regenerating `.env` for (no behavior change — `ShouldGenerateEnv` already key-matches strings)

## Technical Details

Data structures changed:

- `internal/config.DockerArgs` gains `Pull []string` (yaml `pull`) and `Build []string` (yaml `build`). Backward compatible — both default to nil/empty.
- `internal/docker.Compose.CommandArgs` gains `"pull"` and `"build"` keys when constructed via `NewCompose`/`NewComposeAll`.

New public API:

- `(*config.DevboxConfig).ComposeFilesAll() []string`
- `docker.NewComposeAll(cfg *config.DevboxConfig, dockerCfg *config.DockerConfig) *docker.Compose`

Argument flow for `devbox docker build --all --force svc-x svc-y`:

```
docker compose
  -p <project>
  -f <base> -f <overlay-1> ... -f <overlay-N>      # ComposeFilesAll
  <args.global>
  build
  <args.build>                                     # configured defaults
  --no-cache --pull                                # injected by --force
  svc-x svc-y                                      # positional args
```

Argument flow for `devbox docker pull svc-x` (no `--all`):

```
docker compose
  -p <project>
  -f <base> -f <enabled-overlays...>               # ComposeFiles
  <args.global>
  pull
  <args.pull>
  svc-x
```

`--force` validation: cobra wires `--force` only on `build`. No need to add a runtime check on `pull`; cobra rejects unknown flags by default.

`.env` regeneration: `dockerCfg.Env.ShouldGenerateEnv("pull")` and `("build")` already work because `ShouldGenerateEnv` does a `slices.Contains(e.Commands, command)` lookup. Users opt in via `docker.yml`:

```yaml
env:
  auto_generate: true
  commands: [up, run, exec, restart, pull, build]
```

## Post-Completion

*Items requiring manual intervention or external systems — informational only.*

**Manual verification:**
- In a real devbox project, run `devbox docker pull` and confirm only enabled overlays' images are pulled.
- Run `devbox docker pull --all` and confirm images for disabled overlays also get pulled, while `devbox/local.yml` remains unchanged on disk.
- Run `devbox docker build --force` against a known-rebuildable service and confirm `--no-cache --pull` semantics (cache is bypassed, base layers re-pulled).
- Run `devbox docker build --all svc-x` to confirm a single-service build can target an overlay that is currently disabled in `local.yml`.

**External system updates:**
- Consuming projects that previously used `make image_*` targets can migrate their automation/CI to `devbox docker pull|build` once this lands.
- Any project that wants policy defaults for the new subcommands needs to add `args.pull` / `args.build` to its `docker.yml` (no migration is forced — empty defaults preserve current behavior).
