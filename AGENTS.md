# Repository Guidelines

## Project Structure & Module Organization

This is a Go CLI project named `dwe` (Dev Workspace Engine). DWE is a developer tool for local development environments running on Docker. Typical flow: a developer installs DWE locally, enters a project with DWE configuration, runs `dwe`, and the CLI detects the current directory as a DWE project. From there it automates deploy/setup steps, orchestrates Docker services, and runs project commands.

`AGENTS.md` is the canonical file; `CLAUDE.md` is a symlink to it. Edit `AGENTS.md` — overwriting `CLAUDE.md` will break the symlink.

The executable entrypoint lives in `cmd/dwe`; most code is under `internal/`. Tests sit next to code as `*_test.go`; fixtures live in package-local `testdata/`.

`internal/` is organized in three layers (layering rules — `depguard` activation pending):

- **`internal/cli/`** — cobra command tree. Composition root in `cli/root.go`; no domain logic. One subpackage per command subtree, each exporting `NewCmd(groupID, flags)`.
- **`internal/core/`** — domain logic, subclustered into `project/` (what is a DWE project), `execution/` (pipeline engine), `workflow/` (deploy / lifecycle / reset / snapshot / setup), `usercommands/` (declarative command system), `validate/` (static project validation), `docs/` (embedded docs subsystem), `ui/` (domain-aware renderers — sink layer, imported only by `cli/`), and `notify/` (desktop notifications).
- **`internal/shared/`** — leaf infrastructure (`docker/`, `git/`, `daemon/`, `lock/`, `pathsafe/`, `envfile/`, `render/`, `liveui/`, `tpl/`, `i18n/`, `version/`, `prompt/`, `promptcache/`).

**Per-package responsibilities, invariants, and cross-package contracts live in [`docs/internals/packages.md`](docs/internals/packages.md).** Read the relevant section there before modifying any package — it captures non-obvious load-bearing details (sequencing, sentinels, allowlists, render ordering, cross-cutting CLI patterns like JSON output mode and display-string localization) that are expensive to re-derive from the code.

## Configuration Documentation

Keep behavior and docs aligned. DWE user-facing documentation lives in `docs/reference/` (schemas) and `docs/guides/` (task-oriented recipes):

- `docs/reference/config/` — project config files (`workspace.md`, `services/`, `docker.md`, `info.md`, `styles.md`), pipelines (`deploy/`, `lifecycle.md`, `reset.md`, `conditions.md`, `state/`), user commands (`commands/`), user-level notifications and i18n (`notifications.md`, `i18n.md`), snapshot workflows (`snapshot.md`), project readiness checks (`validate.md`), setup wizard (`setup.md`), and command browser settings (`ui.md`).
- `docs/reference/render/` — render subcommands (env / ide / ai / git) and template-pack mechanics (manifest schema, local overrides, collision policies).
- `docs/guides/` — task-oriented recipes and integrations (e.g. Starship prompt). Each page solves a concrete user-facing problem that cuts across the reference. Translations live in `docs/i18n/<lang>/guides/`.

The live CLI surface (flag list, subcommand tree) is `dwe --help`; no per-command markdown files are kept in the repo.

Update these when changing schemas, commands, service toggles, deploys, or hooks.

For AI agent orientation, `dwe docs llms-txt` emits a compact llms.txt index (project context, services, commands, and docs pointers) designed for AI agents to load once and navigate the project — run it inside a project for a project-aware snapshot, or outside one for a generic dwe reference.

### Documentation site (`web/`)

`web/` holds an Astro Starlight site published to GitHub Pages (`https://semsemyonoff.github.io/dwe/`) by `.github/workflows/docs.yml` on every push to `main` that touches `docs/**` or `web/**`. It is a **build-time mirror** of `docs/reference/` + `docs/guides/` (and the `docs/i18n/ru/` mirror); `docs/internals/` and `docs/plans/` are excluded. The canonical `docs/*.md` stay byte-identical — **edit `docs/` (and the root `README.md`), never the generated tree.** `web/scripts/sync-docs.mjs` transforms `docs/` → the gitignored `web/src/content/docs/` (title-from-H1, link rewriting to base-aware slugs / GitHub blobs for internals & non-docs repo files, i18n remap), derives the sidebar from each `index.md`'s ordered TOC, and builds the per-locale root landing pages from `README.md` / `docs/i18n/ru/README.md` (images they reference are copied into the gitignored `web/public/`). Preview with `cd web && npm run dev`; `npm test` covers the transform. Dangling `.md` links in `docs/` are degraded to plain text with a build warning (not a hard failure). No Go code is involved.

## Build, Test, and Development Commands

- `make build` runs `go mod tidy`, syncs `docs/` into `internal/core/docs/embedded/` (via `scripts/sync-embedded-docs.sh`), regenerates `internal/core/docs/content_hashes_gen.go` (via `scripts/gen-docs-content-hashes.sh`), cross-compiles the bridge shim into `internal/core/bridge/shimassets/bin/` via `scripts/build-shims.sh`, builds `./cmd/dwe`, and writes `bin/dwe`. Run `make build` (not `go build`) after editing docs under `docs/reference/`, `docs/guides/`, `docs/internals/`, or `docs/i18n/` — otherwise the embedded docs in the binary will be stale.
- `make test` / `make test-v` / `make test-race` run the test suite. They depend on both `embedded-docs` and `shims`, so the sync runs before tests every time. **Always use `make test*` — `go test ./...` directly will see an empty `internal/core/docs/embedded/` tree on a fresh checkout (it is gitignored and generated) and the docs-subsystem tests will fail.**
- `make lint` runs `golangci-lint` checks.
- `make tidy` updates `go.mod` and `go.sum`.
- `make clean` removes the built binary from `bin/`.

For focused work prefer `make embedded-docs` once, then `go test ./internal/cli` or `go test ./internal/core/usercommands/... -run TestName` directly — the sync is idempotent and the embedded tree persists until the next `docs/` edit.

## Coding Style & Naming Conventions

Follow `.editorconfig`: Go files use tabs with width 4; YAML, JSON, and shell files use two spaces. Run `gofmt` and `goimports`; `golangci-lint` enforces `errcheck`, `govet`, `staticcheck`, `revive`, `gocritic`, and `modernize`.

## Testing Guidelines

Prefer table-driven tests for command parsing, config resolution, Docker orchestration, and runner behavior. Keep fixtures in `testdata/` and avoid developer-specific Docker state unless the package isolates it. Every behavior change should update nearby `*_test.go` coverage. Run `make test` before opening a PR.

## Commit & Pull Request Guidelines

Recent history uses conventional-style commits such as `feat(commands): ...`, `fix(commands): ...`, `docs(config): ...`, and `refactor(commands)!: ...`. Keep the scope tied to the package or user-facing area, and use `!` only for breaking changes. Pull requests should include a summary, verification commands, linked issues when applicable, and screenshots or terminal output when changing CLI presentation.

A PR that changes anything a user can observe — a new or renamed flag, a changed default, a removed config key, a different message — also adds an entry under `## [Unreleased]` in `CHANGELOG.md`. Release notes are cut from that file by `scripts/changelog-release-notes.sh`, which fails the release when the tagged version has no section, so a missing entry surfaces at tag time rather than in the published notes.

## Critical Patterns

Recurring traps and load-bearing contracts.
Each pattern has a full write-up in [`docs/internals/packages.md`](docs/internals/packages.md) — read the named `§` before touching the relevant area.
A bullet here is a **trap plus a pointer**, not a write-up: name what breaks and where the contract lives, one sentence per line.
New invariants go into `packages.md` and gain at most a pointer here; `TestAgentsMdBudget` pins that.

- **JSON output mode (`--output json`, `--pretty`)** — read-only commands route data through `cmdctx.WriteData[T]` and *return* the typed `cmdctx.Err`/`ErrWrap`, which carry the exit code and which `main.go`'s handler serializes via `cmdctx.WriteError` into a `{"error":{…}}` envelope on **stderr**, so stdout stays parseable; gate any `warning:` write behind `flags.Output != "json"`.
  Diagnostic commands (`validate`) are the exception: diagnostics-as-data on stdout even at severity=error.
  See § CLI (cross-cutting behaviors).

- **Display-string localization** — never read `def.Description` in display code; thread `rflags.I18n` (`i18n.TranslatorOrNop` on completion paths) and use the typed `store.*` helpers.
  Storage and hashing sites stay English — a locale reaching `journal/hash.go` makes the deployment hash language-dependent.
  See § CLI (cross-cutting behaviors).

- **Binary accessors** — never read `cfg.Binaries.*`; use the nil-safe `config.DweBin / DockerBin / ShellBin / GitBin / MmdcBin`.
  Deploy/condition step `when:` predicates hardcode `sh` for POSIX portability on purpose — do not "fix" them to the accessor.
  See § Core — Foundation (`project/config/`).

- **YAML loader strictness** — pipeline / command-file / manifest / scenario / service-folder loaders are strict `KnownFields(true)`; info, styles, docker, local.yml and topology are lenient. Match the surface you join, or you swallow the typo strictness exists to catch, or hard-fail a file authors treat as free-form.
  Exactly four strict *pipeline* loaders also tolerate `io.EOF` so an all-comment file reads as absent and the built-in default survives — that is what makes `dwe init`'s inert scaffold overrides load; mirror it in any new strict pipeline loader.
  See § Core — Foundation (`project/config/`).

- **Strict root + `vars:` sandbox + top-level `update:`** — `LoadConfig` rejects any top-level key outside `allowedRootKeys`, per layer so the error names the file; a new formalized top-level field MUST be added there or every project declaring it fails to load.
  Arbitrary free-form values have exactly one home, `vars:`; self-update is the top-level `update: {mode: on|off}`, which **replaced** `lifecycle.yml`'s `run.update`.
  See § Core — Foundation (`project/config/`) and § `internal/shared/git/`.

- **Validation framework** — domains register via an `All()` constructor collected by `buildRegistry`; validators are per-file and independent, so `dwe validate` proceeds past a failed config load instead of going blind on every other file.
  Only `env/` + `checks/` (plus the cherry-picked `config.validate` validator) run in preflight — a content mistake in any other domain must never block a lifecycle command.
  See § Core — Validation.

- **Preflight + locks ordering** — lifecycle commands call `preflight.Run` before any side effect *including the locks*, so a `type: command` check never runs under an operation lock; only then the project locks (deploy.lock → snapshot.lock, released in reverse).
  Take them as a pair via `lock.AcquireProjectLocks(baseDir)`, never `lock.Acquire` on the individual files; from `internal/cli/` go through `cmdctx.AcquireProjectLocksOrReport` — or `AcquireProjectLocksSilent` while a full-screen TUI is live, since a lock-held banner printed mid-frame corrupts the alt screen (`core/workflow/` cannot import `cmdctx` and calls the pair directly).
  Docs commands and `dwe logs` are read-only: no preflight, no locks.
  See § `internal/core/execution/preflight/` and § `internal/shared/lock/`.

- **Config-load helpers** — wrap `config.LoadConfig` with `config.LoadConfigOrWrap` and docker config with `config.LoadDockerConfigOrEmpty`; do not re-roll either wrap.
  The typed `cmdctx.ErrWrap("project_invalid_config")` envelope is a SEPARATE path — folding it into `LoadConfigOrWrap` strips the machine-readable envelope off every config failure in JSON mode.
  See § Core — Foundation (`project/config/`).

- **Grouped commands: no child `PersistentPreRunE`** — cobra never chains the hook; a child's *replaces* the parent's, silently dropping the root's project resolution, styles and locale.
  Do shared per-subtree setup in a `RunE` loader helper instead (`loadStatusContext`).
  See § `internal/cli/status/`.

- **Completion path safety** — cobra's hidden `__complete` bypasses `PersistentPreRunE`, so a `ValidArgsFunction` must call `cmdctx.CompletionConfigPath(flags, cmd)` before any config-dependent work.
  On ANY error return empty completions + `ShellCompDirectiveNoFileComp` and print **nothing** — the shell parses the callback's stdout as the candidate list, so a diagnostic line is offered as a completion.
  See § `internal/cli/cmdctx/`.

- **Section renderer signature contract** — `core/project/stack/` collectors and `core/ui/render/` renderers return strings; `internal/cli/` is the single writer to stdout/stderr.
  Threading an `io.Writer` into either package moves sink policy (JSON suppression, stdout-vs-stderr) out of the CLI and breaks the byte-compared goldens; two exceptions are grandfathered, do not add a third.
  See § `internal/core/ui/render/` and § `internal/core/project/stack/`.

- **Pipeline defaults** — `deploy.yml` / `reset.yml` / each `lifecycle.yml` section are optional, each with a paired `Default*Config()` + `Ensure*Config(loaded) (cfg, defaulted bool)`; the bool feeds only `cmdctx.EmitDefaultNotice` (stderr, no-op in JSON).
  Composition is **full replacement** — an active user file replaces the whole built-in pipeline, so a half-edit silently drops phases; `EnsureStopConfig` is the one exception and always prepends `autoReapPhase()`.
  See § Core — Workflow.

- **Single-block → multi-command sugar** — `type: daemon` expands at `LoadRegistry` time into N virtual `CommandDef`s (source dropped from `byID`, synthetic group `ensureGroup`'d, each synthetic keeping a back-reference for inspect); it is the template for any future declarative multi-command type.
  Expansion is **config-blind** — `LoadRegistry` holds no `*DweConfig`, so config-derived values must resolve at builtin runtime and param-derived ones stay `${param.<name>}` literals, never baked into `with:`.
  See § Core — User Commands.

- **`info.yml` auto-blocks + `service.yml` allowlists** — `type: auto-urls`/`auto-hosts` expand at *render* time, and renderers MUST iterate `config.DeployOrder(...)`, never `range cfg.Services` (randomized map order ⇒ flaky goldens).
  A new top-level `service.yml` field needs BOTH `allowedFieldsFor` and its hand-mirrored `servicesAllowedFields`; the decode is `KnownFields(true)`, so a miss is a hard load error on one side or a false "invalid file" on the other, and nothing cross-checks them.
  See § `internal/core/ui/render/` and § Core — Foundation (`project/config/`).

- **Pending-state lifecycle** — `dwe services enable/disable` without `--apply` writes `local.yml`/`.env` immediately but records journal pending only through the `everDeployed` gate; a journal **load error defaults to `true`**, so a corrupt file can never silently swallow the toggle's intent.
  Clear with `ClearPendingOps` plus a contributor-derived `clears` slice — `ClearPending` erases pending recorded by unrelated sessions.
  See § `internal/cli/service/` and § Core — Workflow (`deploy/journal/`).

- **Compose-bypass for per-service stop/reset/restart** — `dwe stop|restart <name>` go straight to `docker stop|restart <containerName>` because the compose overlay is filtered out for disabled services; per-service restart also skips lifecycle hooks, preflight and locks on purpose.
  `dwe reset run --service <name>` stops+removes via the synthetic `docker_stop_remove_container` builtin, NOT `stopServiceLocked`, and never removes volumes — opt in with `docker_remove_project_volumes`.
  See § `internal/cli/lifecycle/`.

- **Per-service folder symmetry** — every service is `workspace/services/<name>/`, folder name = map key (no `name:` field); `service.yml` is required, `deploy.yml`/`reset.yml` optional and valid for ANY service type.
  A new per-service file type must also join `knownServiceFiles` in the `services-folder` validator, or `dwe validate` warns in every project that adopts it.
  See § Core — Foundation (`project/config/`) and § Core — Validation.

- **Snapshot template scope gate** — `${snapshot.*}` resolves only inside snapshot workflow blocks; `tpl.RenderCommand` calls `validateSnapshotScope` BEFORE `CompileVarSyntax`.
  Never add scope logic to `CompileVarSyntax` — it has no error path and must stay pure-syntactic.
  See § `internal/shared/tpl/`.

- **`${...}` known-head whitelist** — `CompileVarSyntax` rewrites `${X}` only when the head is in `tpl.KnownVarHeads` AND `X` carries a tail (`${args}` excepted); anything else stays a **literal**, because rewriting a head-only `${host}`/`${files}` — a shell variable colliding with a namespace name, common in `cmd:` — silently erased it to `""` or dumped a `Raw` sub-map as `map[...]` text.
  It is a correctness control, not a security boundary; ask `tpl.IsVarNamespaceRef`/`IsKnownVarHead` rather than re-indexing the slice or re-deriving the tail rule elsewhere.
  The pipeline context is Raw + Host only, so `tpl.ValidateRawScope` runs FIRST on every string and rejects `param`/`context`/`files`/`generated`/`args` — their lenient resolvers would render `git checkout ${param.branch}` down to `git checkout` plus a trailing space, invisible to both detectors. Do NOT extend it to `usercommands.buildRunContext` or workflow sub-steps, where `${param.*}` is legitimate.
  See § `internal/shared/tpl/`, § Core — Execution (`pipeline/`), § Core — Validation and § Core — Foundation (`project/config/`, for the one `docker.yml project_name` exception).

- **Resolve-time pipeline rendering** — `cmd`, `with:` leaves, `check`, `files_gate`, `timeout` and shell `when:` render **once** at resolve time into a deep copy; a render error fails the step, and `dwe reset step` must call `RenderStep`/`RenderWhen` itself.
  Rendering is NOT idempotent, so a resolved `with:` must go through `usercommands.BuildPreRenderedRunContext` — `BuildRunContext`'s ungated second pass re-parses a resolved literal `{{` as a Go template and double-expands a `${vars.*}` value.
  The wider `with:` gate never applies to a `type: builtin` map, whose `{{ }}` belongs to the builtin's own template space — widening it aborts the whole plan.
  Plan renderers read `ResolvedStep.DisplayPhaseWhen()`, not `rs.Phase.When`; the whole `vars:` block is hashed into BOTH config hashes, or `already up-to-date` skips a step whose rendered command changed.
  See § Core — Execution (`pipeline/`), § `internal/shared/tpl/` and § Core — Workflow (`deploy/journal/`).

- **Live view is not a bubbletea program** — `liveui` drives `bubbles/v2` models by hand and paints a footer into normal scrollback beside the pipeline's own output; never `tea.NewProgram`, `term.MakeRaw`, or terminal capability queries.
  Nine numbered non-negotiable invariants govern how it shares the terminal with the child process — they are enumerated in the `liveui` package doc (`liveline.go`), and the code cites them by number; read them first.
  See § `internal/shared/liveui/`.

- **Prompt hot path** — `dwe prompt` and `dwe prompt --check` bypass cobra entirely via `isPromptInvocation` in `cmd/dwe/main.go`; the cobra command is `Hidden: true`, for `--help` discoverability.
  `internal/shared/prompt` must not use lipgloss — it auto-downgrades to no-color when stdout is piped, which it always is under starship.
  See § `internal/shared/prompt/`, § `internal/cli/prompt/` and § Entrypoint.

- **Prompt cache contract** — only commands that end with the WHOLE stack in a known state write `.dwe/prompt-cache.yml` (TTL 2 min); every scoped or unknown-outcome site calls `promptcache.Remove` instead of guessing.
  `dwe prompt`'s own refresh writes `running` but **never** `stopped` — a zero-result `docker ps` is indistinguishable from a wrong label filter — though past `staleTrustCap` it still *renders* stopped: the rule bounds writes, not rendering.
  See § `internal/shared/promptcache/`.

- **Docs subsystem read-only** — never call `lock.AcquireProjectLocks` from a docs command and never run preflight.
  For markdown *content* resolve locale via `i18n.ResolveLocale(flagLang, cfgLang, sysLang)`, never `rflags.Locale` — that one is clamped to the YAML store, the wrong namespace for markdown (it stays correct in `docs generate`, which renders YAML-store command strings).
  See § Core — Docs.

- **Two i18n namespaces** — `workspace/i18n/<lang>.yml` (YAML UI strings, per-key deep merge, validated by the `i18n/` domain) versus `docs/i18n/<lang>/**` (markdown docs, whole-file fallback, no validator, no `internals/` mirror).
  Different loaders, no merge — a key never crosses between them.
  See § `internal/shared/i18n/`.

- **Local compose overlays** — `compose.extra` and `services.<name>.compose.extra` are valid **only** in `workspace/local.yml`; other root layers hard-error with a pointer there.
  Both are `yaml:"-"` and injected post-decode before `ResolveServiceExtends` — never add them to `allowedFieldsFor()` or `servicesAllowedFields`, or the strict decoder promotes a per-developer overlay into a git-tracked structural field and the gate is gone.
  See § Core — Foundation (`project/config/`).

- **Config render + generated-once values** — service configs materialize through `execution/templates/config/` on the `${...}` substrate, not the deprecated `service_configs_copy`; pair `service_configs_render_check` as the render step's `check:`, or template edits and store clears stop applying because the journal hash cannot see either.
  DWE **harvests, never mints**, and `HarvestGenerated` MUST skip a field the store already holds *without reading its file*: `dwe reset run` keeps the store while wiping the hub, so an unconditional re-read fails the whole deploy over a value it already has (found by a control run) — do not "fix" that with a second `generated-missing` gate.
  `renderConfigsForRun` runs after the deploy gate but before phases; the `missingGeneratedKeys` and `config.SharesExtendsParentHub` skips are what stop `dwe run` blanking secrets.
  See § Core — Execution (`templates/config/`, `builtin/`), § `internal/shared/generatedstore/` and § `internal/core/workflow/lifecycle/`.

- **Diagnostic trace routing (`-v` / `--debug`)** — all diagnostic echo goes through the `internal/shared/trace` leaf and is **stderr-only**, so stdout (incl. `--output json`) stays clean; a hand-rolled `fmt.Fprintln(os.Stderr)` bypasses the routing and lands un-framed mid-live-frame.
  Inside a pipeline the printer frames the line above the footer but writes its text to `os.Stderr`, never `live.Println` (which is stdout) — that is what makes `dwe run --debug 2>debug.log` capture in-pipeline echoes.
  Read-only docker probes echo at Debug only (keeps `dwe status -v` quiet); `trace.FormatCommand` is the single quoting source, so compose error-wrap goldens must stay byte-identical to it.
  See § `internal/shared/trace/`.

- **Host bridge env contract** — the daemon force-sets `DWE_INVOKED_FROM=container` + `DWE_NONINTERACTIVE=1` on every forked `dwe` (client-sent values are stripped, which is what makes the policy unspoofable) and strips or overrides `PATH`, the loader families, shell-startup hooks, `IFS` and the host-identity set.
  The forked host `dwe` resolves `docker`/`git`/`sh` by bare name, so forwarding any spawn-influencing variable is host code execution across the trust boundary — never exempt one without confirming that; `bridgeclient.StripEnv` is the single strip-set source.
  Daemon files use a bridge-private flock, never `lock.AcquireProjectLocks`, and daemon-touching CLI tests must stub the `ensureDaemonFn`/`stopDaemonFn`/`probeDaemonFn` seams or they re-exec the test binary.
  See § Core — Bridge and § `internal/cli/bridge/`.

- **Container command policy** — the container CLI surface is allowlist / default-deny (`bridgeAllowedTopLevel`), so a NEW top-level command stays blocked until listed; inside `commands` a user command additionally needs its own opt-in `bridge:` block.
  The workflow runner never consults `BridgeHidden` — a bridged workflow executes non-bridged sub-commands host-side.
  The `.dwe/compose.bridge.yml` overlay step ALWAYS runs (regenerate or delete).
  See § CLI (container command policy), § Core — Bridge and § Core — User Commands.

- **`dwe vars` + comment-preserving `local.yml` writer** — `local/local_node.go` is the SINGLE `local.yml` write path, and `ApplyOverlayToNode` derives `Tag`/`Style` from the coerced NEW value, not the old node — keeping the old `DoubleQuoted` style would make `vars set x true` write a quoted string forever.
  `vars set` coerces through the PINNED `varsusage.CoerceScalar` grammar, and per-layer resolution goes through `config.LoadLayers`/`ResolveLayeredPath` so `LoadConfig` and `vars inspect` cannot drift.
  The usage scanner is field-aware, not a grep: `templatedKeys` must list every field the resolver renders (incl. `timeout`, `files_gate.command`, `argv_append_from`), or a `${vars.typo}` there renders to `""` silently and `config.template_refs` never sees it.
  `bridge.vars_writable` is a deny-by-default dot-boundary container-write allowlist enforced at RUNTIME inside `vars set`, because the command allowlist is prefix-wide and cannot see the var argument.
  See § `internal/core/project/local/`, § `internal/core/project/varsusage/`, § `internal/cli/vars/`, § Core — Foundation (`project/config/`) and § `internal/core/ui/cmdbrowser/`.

- **Forms unification (`ask`/`widgets`)** — `widgets.RunHuhForm` is the single executor for every huh form, and hooks must fire **exactly once per prompt**, so a wrapper whose default seam calls it must not fire hooks itself.
  Cancel surfaces as `widgets.ErrCancelled` — check `errors.Is`, never the raw `huh.ErrUserAborted`; custom quit keys are declarative (`ask.RunOptions.Quit`), and `huh.NewForm` outside `ui/ask` + `ui/widgets` in production code should grep empty.
  See § `internal/core/ui/ask/`, § `internal/core/ui/widgets/` and `docs/internals/tui-keymap.md` § 7.

- **`tui.Plugin` framework** — `cmdbrowser`, `docstui` and `statustui` are the three consumers on the shared `core/ui/tui` Frame, and `core/docs` must stay import-free of `core/ui` (that is why the docs browser lives in `core/ui/docstui`).
  The interface is frozen after `CapturingInput()`, and `tui.Run` already wraps `widgets.RunWithPromptHooks` and owns alt-screen/mouse/teardown — wrapping again double-fires the hooks.
  `Frame.renderBody` adds `Padding(0,1)`, so a plugin's inner width is **outer − 4**, not −2; recompute every width bucket against it.
  Narrow-terminal handling is deliberately three different shapes, and docstui's first `loadTopic` fires from `Update(tea.WindowSizeMsg)` because `Resize` returns no `tea.Cmd`.
  See § `internal/core/ui/tui/`, § `internal/core/ui/docstui/`, § `internal/core/ui/cmdbrowser/`, § `internal/core/ui/statustui/` and § Dependency Rules.

- **TUI mouse, wheel and overlay routing** — mouse, focus, overlay-close and wheel reach a plugin ONLY as `PanelClickMsg`/`FocusChangedMsg`/`OverlayClosedMsg`/`WheelMsg`, never raw, and a wheel must never move focus.
  `WheelMsg.Delta` is the NET **coalesced** notch count (±N, including a capturing overlay's own wheel): `tea.WithFilter` trailing-debounces it because bubbletea v2's unbuffered FIFO plus one `View()` per message froze the UI behind a momentum flood, and a leading-edge variant scrolled the wrong way.
  `Overlay.ReleaseMouse` + `FullScreen` trade click/wheel away for native selection — the terminal cannot clip selection to a sub-rectangle, so the whole screen must become overlay text.
  See § `internal/core/ui/tui/` and § `internal/core/ui/docstui/`.

- **In-TUI form overlays + generic tree engine** — an embedded huh form returns NO Submit/Cancel cmd and completes **asynchronously**: poll `FormOverlay.State()` after every forwarded `Update` AND return huh's cmds, or the form silently never finishes; `MaxHeight` is a CAP measured from a once-captured natural height, never a fixed height.
  A capturing top overlay must be `ReplaceTop`'d, never `Push`ed, and a self-close travels as `tui.CloseOverlayMsg{Token}` — a plugin that can open more than one overlay MUST stamp a unique non-zero `CloseToken`, or a deferred close pops whatever modal is on top by then.
  `cmdbrowser.Options.Edit`/`RunForm` are opt-in: nil keeps the old exit-and-return flows byte-identical (goldens).
  In `tui/tree.Engine[N]` rendering stays in the consumer, expansion is keyed by stable `Key` so it survives a `SetRoots` node-graph rebuild, and `RebuildVisible` must never re-park the cursor (that flips a cmdbrowser golden).
  See § `internal/core/ui/tui/`, § `internal/core/ui/tui/tree/`, § `internal/core/ui/cmdbrowser/` and § `internal/cli/command/`.

- **Compose project name: one resolver, lowercased** — use `config.ResolveComposeProjectName(baseDir, cfg)` or its in-memory sibling `config.ComposeProjectName(dockerCfg, cfg)`; never re-derive the `dockerCfg.ProjectName ?: cfg.Project.FullName()` precedence inline.
  Both lowercase (Compose rejects uppercase, project names are free-form text), and five call sites already drifted — the `-p` value then scopes compose's own containers, networks and volumes under a different project than every `<project>-<container>` label lookup.
  `shared/prompt.readComposeProjectName` is the one deliberate hot-path exception and repeats the lowercasing by hand; existing uppercase-named projects see their old containers and volumes as a different project.
  See § Core — Foundation (`project/config/`).

- **Predicate-as-body assertions + always-run** — `kindAllowed` accepts a `KindPredicate` builtin as a step **body** (an assertion: `false` fails the step), so user-command `type: builtin` defs take predicates too; `KindInternal` stays engine-only.
  Such a step must re-run despite a matching deployment hash, and `pipeline.StepForcesRun` is the single lever — it MUST gate on `step.Type == "builtin" && builtin.KindOf(step.Cmd) == KindPredicate`, since classifying by `cmd` text alone force-runs a `type: shell` step whose command happens to be `shell`.
  See § Core — Execution (`builtin/`, `pipeline/`).

- **Integration-test scenarios (`dwe test`)** — scenario files load strict (an empty file is an **error**, unlike the pipeline `Ensure*` defaults) and are rendered ONLY by `ResolvePhaseSteps`: a scenario-local pre-pass renders twice, and rendering is not idempotent, so the scenario would exercise a command the deploy pipeline never runs.
  `envtest.ScrubComposeEnv()` runs before any flock, goroutine, UI or subprocess, and every runner test must stub the `execDweFunc` seam or the test binary re-execs itself.
  See § `internal/core/workflow/envtest/` and § `internal/cli/test/`.

- **`dwe test` isolation & cleanup** — the runner takes a per-scenario flock only (never `lock.AcquireProjectLocks` on the original project), writes the manifest before touching Docker so a half-dead run stays sweepable, tears down strictly by the manifest's recorded identity and never appends `-v`, and remaps every enabled service's host port from one `AllocatePorts` batch so `ports_free` preflight and the actual compose bind move together.
  Failure reports are collected only for a failed non-`--keep` run, BEFORE teardown, under a fresh context, and both captures take `BuildInternalArgs` + `ps --all` + combined stdout/stderr — otherwise a project's `args.logs: ["-f"]` or a hidden stderr stream silently guts the report.
  `dwe test clean` sweeps only what `validateManifestIdentity` re-derives from `(baseDir, scenario, runID)` — canonical symlink-free paths, and a `compose_project` pinned to the COPY's stamped identity rather than the current root config, or a run kept across a `project.name` rename is stranded.
  At `--parallel`, goroutines never return errors into the errgroup (siblings must not cancel each other) and the aggregated display engages only at effective N>1 (N=1 stays byte-identical).
  Per-step `timeout:` bounds the step **body** only, never its `check:`.
  See § `internal/core/workflow/envtest/`, § `internal/cli/test/`, § `internal/core/project/config/compose_scan.go`, § `internal/core/validate/tests/` and § Core — Execution (`pipeline/`).

- **Pipeline primitives: `argv_append_from` / `check: auto` / `source_clone`** — `argv_append_from` is argv-only host program text rendered ONLY via `runio.RenderArgvAppendFrom`, and its shared `withoutArgs` helper stays unexported — hiding `${args}` is only a consistency rule *here*, but in `RenderShellCommand` it is what keeps caller bytes out of program text; output is DATA, one element per line, and empty output skips via `spec.ErrArgvAppendEmpty` while the step still **journals as success**, so it needs a `files_gate`/`check:`.
  `check: auto` decodes to the `config.AutoCheckType` sentinel at LOAD time (ask `config.IsAutoCheck`) — a resolve-time-only rewrite would silently stop the step forcing re-runs; `pipeline.ResolveAutoCheck` is the single derivation, the rewrite takes a FRESH pointer (else a second resolve yields `! ( ! ( … ) )`), and plan renderers read `ResolvedStep.DisplayCheck()`.
  `source_clone` gates itself, so it replaces the caller's `when:`/`check:` pair, and sets `GIT_ASKPASS`/`SSH_ASKPASS` **empty** on purpose (a dummy program would not defeat an inherited GUI helper) — do not "fix" that.
  See § Core — Execution (`pipeline/`, `builtin/`), § Core — Foundation (`project/config/`), § Core — User Commands and § `internal/cli/lifecycle/`.

- **Two disjoint builtin registries** — `builtin.Inventory()` (step bodies and `check:`) and `condition.Predicates()` (`when:`) share the word "builtin" and accept nothing from each other; never document them as interchangeable.
  A builtin's summary lives on the `spec.Entry` struct, so a test rather than the compiler catches a missing one; a new `when:` verb must land in BOTH `Predicates()` and `EvalBuiltin`'s switch, guarded by a `go/ast` drift test (a `len()` count would be tautological).
  See § Core — Execution (`builtin/`, `condition/`).

- **`llms-txt` budget + `docs search` ranking** — `cli/docs` is the only layer that may import `execution/`: builtin and condition data reach `core/docs/llmstxt` through `Opts`, never an import.
  `dwe docs llms-txt --no-project` is capped at 12 KB by a test, and that number is duplicated in four places; its file flag is `--out PATH` — a local `--output` would shadow the root's and make `-o` unresolvable, so `-o json` now reaches the root format flag and is simply ignored here.
  `docs search` (see also § `internal/cli/docs/`) ANDs tokens ranked by MIN per-token count (a sum lets the commonest word win), with the per-document tier a tie-break BELOW the count — ordering tiers outright buried the strongest answer, invisibly at a narrowed `--limit`; substring matching is deliberate, and the snippet is sanitized (non-printables dropped, whitespace collapsed) and capped *including* the ellipsis so the 4th TSV column can never become a fifth field.
  See § Core — Docs.

- **`dwe test list` cost profile** — `cost_profile` reports facts, never a cheap/expensive verdict; that rule lives in `skills/dwe/SKILL.md` so it changes without a release.
  It is built **only** in JSON mode and every failure path returns nil, so it cannot weaken `list`'s no-Docker / no-locks / works-with-an-unloadable-config contract.
  `host_steps` is deliberately wider than `type: shell` but excludes dwe's own subcommands — counting those would leave every project permanently gated.
  See § `internal/cli/test/`.

- **Scaffold starter artefacts** — `applyServicePlan` drops `workspace/services/<name>/` **plus** every path in `serviceScopedOutputs` when `--service ""`, pinned against the rendered plan so a template rename cannot make the rule a silent no-op; the ai pack's `manifest.yml` and `workspace/tests/smoke.yml` are the only inert-mirror files that ship ACTIVE.
  The per-service `deploy.yml` skeleton must stay commented — not because it fails to resolve (a test uncomments it and resolves green) but because an active skeleton makes `dwe deploy run` clone its placeholder repo on every fresh project; its `bootstrap` step is `type: dwe` + `docker run`, never a bare `docker compose` in a `type: shell` step, which resolves a different compose project.
  Any template edit requires regenerating `testdata/golden_default.txt`.
  See § Core — Workflow (`scaffold/`).

- **Anchor derivation + long-doc hint** — `parseHeadingSlugLabel` is the single derivation for every anchor surface, with the **slug from the RAW heading text and the label from the stripped one**.
  Never write `Slugify(stripInlineMarkdown(x))` and never re-slug `Heading.Text`: `stripEmphasis` ate `_` as an emphasis marker, so three surfaces advertised `servicedirsensure` while the resolver answered only to `service_dirs_ensure` — on every builtin name and snake_case key.
  `emitLongDocHint` writes its one line to **stderr** because the target case is `docs show <topic> | head`, which truncates stdout only; its silence gates keep it from becoming a banner.
  See § Core — Docs.

- **Responsive tables: three traps** — every renderer in `internal/core/ui/render/` degrades shrink → wrap → records through one shared `tableView.Render(budget)`, and a new table inherits that for free by being a `tableView`, but:
  (1) a non-TTY sink gives budget 0, which disables shrinking *and* the record fallback but NOT wrapping, and the goldens hold only because `TestMain` pins the `termWidthFn` seam — check that seam before "fixing" a golden by touching budget logic;
  (2) the budget follows the **sink**, so `DiagnosticsTable` probes stderr while its twin `DiagnosticsByDomain` probes stdout — backwards, it shrinks `dwe validate > report.txt` and leaves `2>/dev/null` unbounded; `width=0` means unbounded, never "probe the sink", so ship an `…At(width)` sibling with every sink-probing entry point and thread the same width into `stack.wrapSection` → `render.SectionTitleAt`;
  (3) `fitRows` must raise each natural width back up to its `columnFloors` value before distributing the deficit, because a `Max` cap can clamp a column below an unbreakable token that the wrap helpers will never split — skip the floor-raise and `distributeDeficit` sees negative headroom, *widens* the column, and the table overflows while still reporting `ok=true`.
  See § `internal/core/ui/render/`, § `internal/core/ui/styles/` and § `internal/core/project/stack/`.

## Agent-Specific Instructions

Do not overwrite unrelated local changes. Keep command examples current with the `Makefile`. When changing CLI behavior, update tests and config docs.
