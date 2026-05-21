# User Notifications

## Overview

Add native desktop notifications fired when long-running Devbox operations complete (success or failure). Notifications are a **user-level** concern (preferences live outside the project), not a project-level concern. The MVP ships a single backend — `gen2brain/beeep` (native OS notifier) — but the architecture leaves room for `telegram` / `webhook` channels later without rework.

**Problem:** developers don't sit and watch `devbox deploy` / `devbox run` / long-running scripted commands; they alt-tab away. They need a passive signal that the work finished so they can come back.

**Integration shape:**
- New `internal/userconfig/` package — loads flat `key = value` config from the platform-native user config dir (resolved via `os.UserConfigDir()`) and `.devbox/config` (per-project user override), merged with embedded defaults and env-var overrides. **Path is platform-native, not hardcoded `~/.config`** — see Technical Details § "Global config path resolution" for the per-OS resolved paths.
- New `internal/notify/` package — exposes one concrete type `*Notifier` (constructor `notify.New(cfg) *Notifier`) with an unexported `backend` interface internally dispatching between `noopBackend` and `nativeBackend` (beeep). Consumers each declare a small local `notifier` interface (one method) for testability — they never depend on a public `Notifier` interface. `New` returns a `*Notifier` whose internal `enabled` bit is `false` (so all `Notify` calls short-circuit) when notifications are disabled or the environment is non-interactive (CI / `DEVBOX_NONINTERACTIVE=1` / non-TTY stdout); the per-event `Notify` check additionally gates on the matching per-operation toggle.
- New `Notify bool` field on `model.CommandDef` — opt-in per-command notifications.
- Hookpoints: `internal/command/deploy.go` (deploy RunE), `internal/lifecycle/RunRun` (with a `lifecycle.RunContext.SkipNotify` bypass so `RunRestart` doesn't fire), and `internal/usercommands/runtime/RunCommand` (gated by `rc.Cmd.Notify && !rc.SkipNotify`, where `SkipNotify` on `runtime.RunContext` is set by **every** transitive invocation site — workflow runner and pipeline action dispatch — so only the top-level user-invoked command fires).
- New validator rules in `internal/validate/commands/`: `notify: true` on `type: daemon` → error; `notify: true` inside a `parallel:` block → info-level diagnostic (silently ignored at runtime).
- New doc page `docs/reference/config/notifications.md` + `notify:` added to `commands.md`.

**Configuration keys (MVP):**

```
notify_enabled          = true   # master switch
notify_run_enabled      = true   # gate for `devbox run`
notify_deploy_enabled   = true   # gate for `devbox deploy`
notify_commands_enabled = true   # gate for user commands with `notify: true`
notify_channels         = native # comma-separated; future: telegram, webhook
```

A notification fires only when `notify_enabled && notify_<kind>_enabled` are both true (and the environment is interactive and the channel list is non-empty). The per-operation toggles let a user mute one kind of notification (e.g. `devbox run` runs every minute during inner-loop dev — silence it) without disabling the others or editing per-command opt-ins.

## Context (from discovery)

Concrete file references gathered before drafting:

- **Deploy RunE end-of-function**: `internal/command/deploy.go:135-143` (cobra wiring); `deployRunCmd` has many error returns + a single success-return at the tail of `runDeployPipeline`. Defer-based wrap is the only sane way to capture outcome.
- **`lifecycle.RunRun`**: `internal/lifecycle/run.go:52` — `RunRun(ctx RunContext) error`; success return at `:171`. `RunContext` struct at `internal/lifecycle/run.go:27-36`.
- **`RunRestart`**: `internal/lifecycle/run.go:175` — calls `RunStop` then `RunRun`. We need `RunContext.SkipNotify` so the inner `RunRun` does **not** notify.
- **`RunStop` / `RunPhases`**: `internal/lifecycle/stop.go:25`, `internal/lifecycle/phases.go:23` — out of scope, no notify hook.
- **`usercommands.RunCommand`**: facade `internal/usercommands/usercommands.go:230-231` → `runtime.RunCommand` at `internal/usercommands/runtime/runner.go:125-176`; final return at `:175`. Runner invocation at `:162`. Defer-based wrap around `runner.Run` is the hookpoint.
- **`CommandDef`**: `internal/usercommands/model/types.go:398-484` — no existing `Notify` field. Strict YAML decoding (loader uses `KnownFields(true)` for command files).
- **TTY / non-interactive detection**: `internal/ui/interactive.go:14` — `ui.IsInteractiveFn` (package-level function var; injectable for tests). Uses `github.com/charmbracelet/x/term` + checks `term.IsTerminal(fd)`. Already consumed by `internal/command/deploy.go:283`.
- **`RunContext.UnderParallel`**: `internal/usercommands/runtime/runner.go:74-79`. Already plumbed; reuse for the silent-ignore guard.
- **Validator pattern**: `internal/validate/commands/commands.go:328-336` is the canonical example (confirm-inside-parallel → error). The daemon-rule validator at `internal/validate/commands/daemon.go:42-100` is the matching style for new field-level checks.
- **User-scope config**: confirmed **nothing** in `internal/` currently uses `os.UserConfigDir` / `XDG_CONFIG_HOME`. `internal/userconfig/` is a greenfield package.
- **Confirm-in-parallel current treatment**: hard validator **error** with `Hint: "move confirm steps outside the parallel block"`. The spec deliberately diverges for `notify:` — info-level only — because notify is not load-bearing for correctness, just a UX nicety that the runtime quietly skips.
- **`go.mod`**: Go 1.26; `github.com/charmbracelet/x/term v0.2.2` and `mattn/go-isatty v0.0.20` (indirect via charm) already present. `beeep` is **not** present — needs `go get github.com/gen2brain/beeep`.

## Development Approach

- **Testing approach**: **Regular (code first, then tests)** — per user preference. Each task lands code first, then unit tests for the code added in that task, before the next task starts.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task:
  - unit tests for new functions / methods / parsers / validators
  - unit tests for modified functions / methods
  - both success and error / edge-case scenarios
  - update existing test cases if behavior changes (e.g. validator test fixtures get a new case)
- **CRITICAL: all tests must pass before starting the next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation**.
- Run `make test` after each task; run `make lint` before finishing.
- Maintain backward compatibility: missing user config → defaults; missing global config (whatever `os.UserConfigDir()` resolves to per-OS) → silent fallback to `.devbox/config` → defaults.

## Testing Strategy

- **Unit tests**: required for every task (see Development Approach).
  - `internal/userconfig/`: parser table-driven cases (comments, lists, full-line `#`, inline-comment rejection, dotted-key reject, bool / list / string types, env override, project-over-global, precedence ordering).
  - `internal/notify/`: `New` returns `noopNotifier` when disabled / non-TTY / `CI=true` / `DEVBOX_NONINTERACTIVE=1`; `noopNotifier` is silent; `nativeNotifier` calls `beeep.Notify` with the right title / icon shape (inject a fake beeep call via package-level function var, same pattern as `ui.IsInteractiveFn`).
  - `internal/usercommands/model/`: round-trip YAML for `notify: true` / `notify: false` / absent.
  - `internal/validate/commands/`: daemon + notify → error; parallel + notify → info; non-parallel non-daemon + notify → no diagnostic.
  - `internal/command/deploy.go`, `internal/lifecycle/run.go`, `internal/usercommands/runtime/runner.go`: assert notifier is invoked on success path, invoked on failure path, **not** invoked when `SkipNotify` is set (transitive-invocation case for runtime; restart case for lifecycle).
- **E2E tests**: this project does not have UI-based e2e tests (no Playwright / Cypress / browser harness). Skip the e2e bucket.
- Manual smoke test (in Post-Completion, not a task checkbox): trigger a real notification on macOS via `make build && ./bin/devbox deploy` against a tiny project.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues / blockers with ⚠️ prefix.
- Update plan if implementation deviates from original scope.
- Keep plan in sync with actual work done.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, doc files, validator wiring — all in-repo and automatable by the agent.
- **Post-Completion** (no checkboxes): manual macOS notification smoke test, optional Linux smoke test, future telegram / webhook backends, CLI `--notify` flag work.

## Implementation Steps

### Task 1: Add `internal/userconfig/` package with parser + loader

- [x] create `internal/userconfig/config.go`: `Config` struct with `NotifyEnabled bool`, `NotifyRunEnabled bool`, `NotifyDeployEnabled bool`, `NotifyCommandsEnabled bool`, `NotifyChannels []string`; private reserved fields (`notifyTelegramToken`, `notifyTelegramChat`, `notifyWebhookURLs`) decoded but not exposed yet so future channels don't require schema migration
- [x] add a single accessor `(c *Config) NotifyKindEnabled(kind notify.OperationKind) bool` that returns `c.NotifyEnabled && c.Notify<Kind>Enabled` — single point of truth for the per-op gate so the notifier doesn't restate the master-switch logic. (Or, to avoid the import cycle if `notify` ends up importing `userconfig`, expose the matrix as `NotifyEnabledFor(name string) bool` keyed on the string `"deploy"` / `"run"` / `"command"`.)
- [x] create `internal/userconfig/parser.go`: flat `key = value` parser per spec — full-line `#` comments only, inline-comment rejected as parse error, comma-separated lists, dotted keys forbidden (`notify_telegram_token` is the only spelling), `true`/`false` booleans, unknown keys → warning (not error — preserves forward-compat for telegram/webhook keys when MVP-only binary reads a config that already has them). **Error message convention** (golang-error-handling): all parser errors lowercase, no trailing punctuation, prefixed with `"userconfig: "` (sentinel-style — identifies the origin). Wrapping at higher layers uses `fmt.Errorf("userconfig: parsing %s: %w", path, err)` with `%w` so callers can `errors.Is` against the parser sentinels if we ever add them.
- [x] create `internal/userconfig/load.go`: `Load(projectRoot string) (*Config, error)` — applies defaults → reads `<os.UserConfigDir()>/devbox/config` (missing file is fine, returns nil-error) → reads `<projectRoot>/.devbox/config` (missing file is fine) → applies env. Resolved path per OS: Linux `$XDG_CONFIG_HOME/devbox/config` or `~/.config/devbox/config`, macOS `~/Library/Application Support/devbox/config`, Windows `%AppData%\devbox\config`. Env keys: `DEVBOX_NOTIFY_ENABLED`, `DEVBOX_NOTIFY_RUN_ENABLED`, `DEVBOX_NOTIFY_DEPLOY_ENABLED`, `DEVBOX_NOTIFY_COMMANDS_ENABLED`, `DEVBOX_NOTIFY_CHANNELS`. Each layer's parser errors **do** bubble up — silent only on `os.ErrNotExist`. File mode for the **global** file is `0600` (write-on-create path, not a read enforcement — we don't reject a non-`0600` config since user may have set it themselves).
- [x] write tests for parser: comments, list parsing, inline-comment rejection, dotted-key rejection, type coercion, empty file, blank lines, whitespace tolerance
- [x] write tests for loader precedence: defaults, global-only, project-only, both, env overrides both, parser-error bubbles, missing-file silent
- [x] write tests for the `Config` defaults (all four `Notify*Enabled` default `true`, `NotifyChannels=["native"]`)
- [x] write tests for `NotifyEnabledFor` (or the equivalent gate helper) covering: master off → all kinds false; master on + per-op off → that kind false; master on + per-op on → true; unknown kind → false
- [x] run `make test` — must pass before Task 2

### Task 2: Add `internal/notify/` package — concrete `*Notifier` with internal backend interface

**Design note (per Go skills review):** `New(cfg)` returns a **concrete `*Notifier`** struct, not the public `Notifier` interface. The interface that varies between implementations (`noopBackend` vs `nativeBackend`) is **unexported** (`backend`) and lives inside `notify` — that's exactly where it's consumed. This satisfies "accept interfaces, return structs" (golang-structs-interfaces) while keeping testability via the same backend seam.

- [x] create `internal/notify/types.go`:
  - `Op` enum (`OpUnknown` at iota 0, `OpDeploy`, `OpRun`, `OpCommand`) — type name is `Op` so constants follow the type-name-prefix convention (golang-naming)
  - `Op.configKey() string` returning `"deploy"` / `"run"` / `"command"` / `""` for the userconfig gate accessor
  - `Outcome` enum (`OutcomeUnknown` at iota 0, `OutcomeSuccess`, `OutcomeFailure`)
  - `Event` struct: `Kind Op`, `Operation string` (human-readable display label used in the title), `Outcome`, `Duration time.Duration`, `Err error`, `Project string`
- [x] create `internal/notify/notifier.go`: **concrete `Notifier` struct** (not interface)
  ```go
  type Notifier struct {
      cfg     *userconfig.Config
      backend backend // unexported interface, see below
      enabled bool    // pre-computed factory-level enable bit
  }
  func (n *Notifier) Notify(ctx context.Context, ev Event) {
      if n == nil || !n.enabled { return }
      if !n.cfg.NotifyEnabledFor(ev.Kind.configKey()) { return }
      n.backend.notify(ctx, ev) // best-effort; backend swallows errors internally
  }
  ```
- [x] create `internal/notify/backend.go`: unexported `backend` interface with single method `notify(ctx context.Context, ev Event)`; two implementations:
  - `noopBackend struct{}` — does nothing
  - `nativeBackend struct{}` — Task 3 wires beeep here
- [x] add compile-time interface checks (`var _ backend = (*noopBackend)(nil)` and same for nativeBackend) — golang-structs-interfaces convention
- [x] create `internal/notify/factory.go`: `New(cfg *userconfig.Config) *Notifier` — returns `&Notifier{enabled: false, backend: &noopBackend{}}` if `cfg == nil`, `!cfg.NotifyEnabled` (master switch), `len(cfg.NotifyChannels) == 0`, or `!isInteractiveForNotify()`. Otherwise returns `&Notifier{cfg: cfg, backend: <picked-by-channels>, enabled: true}`. `isInteractiveForNotify()` is a package-level function var (test seam) that returns `false` if `os.Getenv("CI") != ""`, `os.Getenv("DEVBOX_NONINTERACTIVE") != ""`, or `!ui.IsInteractiveFn(os.Stdout)`. **A nil `*Notifier` is safe to call** — `Notify` short-circuits on `n == nil`. Hookpoints don't need nil-guards.
- [x] write tests for `New`: every factory-level disabled-condition produces a Notifier whose backend is `noopBackend` (probe via type assertion through an internal test accessor like `func (n *Notifier) backendForTest() backend`); enabled config produces `nativeBackend`
- [x] write tests for the per-op gate inside `Notifier.Notify`: `Kind=OpRun` + `NotifyRunEnabled=false` → backend NOT called; `Kind=OpRun` + `NotifyRunEnabled=true` → backend called; same matrix for `OpDeploy` and `OpCommand`; `Kind=OpUnknown` → backend NOT called (defensive); use a `recordingBackend` test double swapped in via a constructor option or direct field write in `_test.go`
- [x] write tests for `Notify` on nil receiver and on `enabled=false` Notifier — no panic, no backend call
- [x] write tests for the detection seam: each env-var / TTY combo flips the result
- [x] run `make test` — must pass before Task 3

### Task 3: Wire `gen2brain/beeep` into `nativeBackend` with timeout

- [x] `go get github.com/gen2brain/beeep` and `go mod tidy`; verify `go.sum` and no transitive surprises (record the licence in case `make lint` has a license-check linter — current `.golangci.yml` does not, but check)
- [x] in `internal/notify/backend.go` (or a new `native.go`): implement `nativeBackend.notify(ctx, ev)` —
  - format title (`✓ Devbox: <op> succeeded` vs `✗ Devbox: <op> failed`), body (project name + humanised duration; on failure append first line of `Err.Error()` truncated to ~200 chars to keep the toast readable)
  - call beeep via a package-level function var `beeepNotify func(title, body, icon string) error` (test seam — default is `beeep.Notify`)
  - **Wrap in a timeout** (golang-design-patterns: "every external call should have a timeout"). `beeep` is synchronous and on some platforms the OS notifier daemon can stall. **Bounded-concurrency variant** (review finding: a naive goroutine + timeout leaks goroutines on stall):
    ```go
    type nativeBackend struct {
        sem chan struct{} // capacity 1, acts as a non-blocking semaphore; nil before init
    }

    func newNativeBackend() *nativeBackend {
        return &nativeBackend{sem: make(chan struct{}, 1)}
    }

    func (b *nativeBackend) notify(ctx context.Context, ev Event) {
        // Non-blocking acquire; drop the event if a previous beeep call is
        // still pending. The slot is released *by the goroutine* when
        // beeepNotify finally returns — not when this function returns — so
        // a hung beeep call keeps the slot occupied forever, bounding leaked
        // goroutines to at most one per CLI process for the worst case.
        select {
        case b.sem <- struct{}{}:
        default:
            slog.Debug("notify backend busy, dropping event", "kind", ev.Kind)
            return
        }

        done := make(chan error, 1)
        go func() {
            // Slot release ownership lives here, not in the caller. If beeep
            // never returns, the slot is never released and all subsequent
            // notify() calls hit the `default` branch above and drop.
            defer func() { <-b.sem }()
            done <- beeepNotify(title, body, "")
        }()

        select {
        case err := <-done:
            if err != nil { slog.Debug("notify backend failed", "err", err) }
        case <-time.After(2 * time.Second):
            slog.Debug("notify backend timed out; slot remains occupied until beeep returns")
        case <-ctx.Done():
            slog.Debug("notify backend cancelled by ctx; slot remains occupied until beeep returns")
        }
    }
    ```
  - **Why a semaphore instead of `sync.Mutex` + `TryLock`**: with `mu.TryLock` + `defer mu.Unlock()` in the caller, the mutex would be released as soon as `notify` returns on the timeout/cancel branches — defeating the bound. Moving unlock ownership into the goroutine via a channel-based semaphore makes the "slot held until beeep returns" property structural rather than commentary.
  - **Drop-on-busy policy** is documented in `docs/reference/config/notifications.md` (Task 9): if a previous notification is still pending (or its OS notifier daemon hung), the new event is silently dropped with a debug log. Acceptable because devbox operations are long-running — back-to-back notifications within 2 seconds are unusual, and the policy bounds resource usage to one inner goroutine maximum per CLI process.
  - **Tests for the bound** (Task 3): use a `beeepNotify` test stub that blocks on a channel the test controls. Call `notify` once → assert goroutine running, slot occupied. Call `notify` a second time → assert it returns immediately with the debug-drop. Unblock the stub → assert the slot frees and a third call proceeds.
- [x] best-effort error handling: backend errors NEVER surface upward — `slog.Debug` only. This is intentional — the notification subsystem must never block a deploy or change its exit code.
- [x] wire `factory.New` to instantiate `nativeBackend` when `slices.Contains(cfg.NotifyChannels, "native")`; unknown channels logged at debug level and skipped (matches the forward-compat principle); a channel list containing only unknown entries falls back to `noopBackend` with `enabled: false`
- [x] write tests for `nativeBackend.notify`: success-event title format, failure-event title format + truncated err body, duration formatting (sub-second, seconds, minutes), beeep error swallowed via debug log only; **timeout case** — fake beeep that never returns, assert call returns within 2.1s without panic; **ctx.Done case** — pre-cancelled context returns immediately
- [x] write tests for `factory.New` selecting `nativeBackend` when `native` is in channels, `noopBackend` when an unknown channel is the only one present
- [x] run `make test` — must pass before Task 4

### Task 4: Add `Notify bool` field to `model.CommandDef`

- [x] add `Notify bool \`yaml:"notify,omitempty"\`` to `CommandDef` in `internal/usercommands/model/types.go`
- [x] verify the field round-trips through `loader.ParseCommandFile` / `loader.LoadCommandFile` (strict KnownFields decoding) — should be free since we added a `yaml` tag matching the new key
- [x] write tests in `internal/usercommands/model/` (or `loader/`) for: command file with `notify: true`, `notify: false`, and `notify` absent (zero value)
- [x] confirm `validate/commands/` validator tests for unknown-field rejection are unaffected by the new field (no regressions in the fixtures)
- [x] run `make test` — must pass before Task 5

### Task 5: Add validator rules for `notify:`

**Scope simplification** (review finding): the planned "deeply nested / transitive workflow" detection in the validator would require a cross-workflow graph traversal (`WalkWorkflowSteps` walks only one workflow's syntax tree, per `internal/usercommands/registry/registry.go:247`). That complexity is unnecessary because the runtime `SkipNotify` guard added in Task 8 already correctly suppresses every transitive case. The validator's role here is purely **educational** — surface the obvious cases statically so users aren't surprised. Restrict detection to **direct containment**: a `parallel.steps[*]` entry whose resolved `CommandDef` has `Notify == true`.

- [x] in `internal/validate/commands/notify.go` (new file) add a validator running in **two phases**:
  1. **Per-file phase** (no registry needed): for each `CommandDef` with `Notify == true` and `Type == CommandTypeDaemon` → emit `SeverityError`. Message: `"<id>: notify is not allowed on type: daemon commands"`, hint: `"daemons have no completion event; remove notify or change the command type"`.
  2. **Registry-aware phase**: after the registry is built (see `internal/validate/commands/commands.go:222` where the cross-ref validator already runs post-registry-build), iterate every `WorkflowStep` inside every `WorkflowParallel.Steps`. Resolve `step.Command` (the command-ID field on `WorkflowStep`, per `internal/usercommands/model/types.go:284` — **not** `step.Cmd`, which doesn't exist on `WorkflowStep`) against the registry; if the resolved `CommandDef.Notify == true`, emit `SeverityInfo`. Message: `"<id>: notify on a direct sub-step inside a parallel block is ignored at runtime"`, hint: `"the runtime suppresses notifications for any command invoked from inside another command — make it the top-level command if you want a notification"`.
- [x] hook the daemon-phase check into the existing per-file `commands` validator list. Hook the registry-aware phase next to the existing post-registry cross-ref validation in `internal/validate/commands/commands.go` (around `:222`); use the registry handle that's already in scope there.
- [x] write tests in `internal/validate/commands/` for: daemon+notify → error; direct-parallel-sub-step + notify → info; top-level + notify → no diagnostic; daemon without notify → no new diagnostic; parallel sub-step referencing a non-existent command → no notify diagnostic (cross-ref validator already handles the missing-ref case)
- [x] update any golden / table fixtures under `internal/validate/commands/testdata/` (no fixtures present; inline cases added)
- [x] **explicitly out of scope for the validator**: transitive containment (workflow A → workflow B → parallel with notify-cmd). Documented because the runtime guard handles it correctly; adding a static graph walker is not worth the maintenance cost. Note this in the validator's file-level doc comment.
- [x] run `make test` — must pass before Task 6

### Task 6: Hook notifier into `devbox deploy`

- [ ] in `internal/command/deploy.go` `deployRunCmd`: install the notifier defer **before** `config.LoadConfig` so a malformed `devbox.yml` still triggers a failure notification. Follow the shared hookpoint contract exactly (see Technical Details § "Hookpoint contract"):
  - capture `start := time.Now()` at function entry
  - declare `var projectName string` (defaults to empty; assigned only after main config load succeeds — panic-safe on early `LoadConfig` failure)
  - `ucfg, ucfgErr := userconfig.Load(projectRoot)`; on parser error, `slog.Warn(...)` and set `ucfg = nil` (deploy must never be blocked by the notification subsystem)
  - `notifier := newNotifier(ucfg)` — using the consumer-local seam, not `notify.New` directly (see test-seam bullet below)
  - install `defer func() { notifier.Notify(context.Background(), notify.Event{Kind: notify.OpDeploy, Operation: "deploy", Outcome: outcomeFromErr(err), Duration: time.Since(start), Err: err, Project: projectName}) }()` — uses `context.Background()` because the notification fires after the operation has finished (see Technical Details § "Context propagation at hookpoints")
  - after the defer is installed, call `cfg, err := config.LoadConfig(...)`; on success, `projectName = cfg.Project.Name`
- [ ] `outcomeFromErr` helper: `nil` → `OutcomeSuccess`, anything else → `OutcomeFailure`. Define once in `internal/notify/` so all three hookpoints share it.
- [ ] userconfig load failure (parser error in a user-edited file) → log warning via `slog.Warn` and proceed with a nil-`cfg` notifier (which `notify.New` tolerates → returns a notifier with `enabled=false` that no-ops every call). Deploy must never be blocked by a notification subsystem.
- [ ] write tests in `internal/command/` for: success path fires notifier with `OutcomeSuccess`; failure path fires with `OutcomeFailure` and the err attached; userconfig load error → warning printed, deploy continues; **early main-config-load failure** → notifier still fires (`OutcomeFailure`, `Project == ""`, no panic) — this guards the panic-safe `projectName` ordering documented in the hookpoint contract. **Test seam shape** — because `notify.New` returns the concrete `*notify.Notifier` with unexported backend fields, the test seam needs a **consumer-local interface**. In `internal/command/notify.go` (new tiny file) declare:
  ```go
  type notifier interface { Notify(context.Context, notify.Event) }
  var newNotifier func(*userconfig.Config) notifier = func(cfg *userconfig.Config) notifier {
      return notify.New(cfg)
  }
  ```
  Tests override `newNotifier` with a recording fake. This pattern satisfies "define interfaces where consumed" (golang-structs-interfaces) — `command` declares exactly the contract it needs. Apply the same pattern in `internal/lifecycle/notify.go` and `internal/usercommands/runtime/notify.go` for Tasks 7 and 8.
- [ ] run `make test` — must pass before Task 7

### Task 7: Hook notifier into `lifecycle.RunRun` with `SkipNotify` bypass

- [ ] add `SkipNotify bool` to `lifecycle.RunContext` (`internal/lifecycle/run.go:27-36`)
- [ ] in `RunRun`: change the signature to declare a named return `err error`. Follow the shared hookpoint contract (see Technical Details § "Hookpoint contract"):
  - `if !ctx.SkipNotify { ...install notifier... }` — when `SkipNotify` is set (the `RunRestart` case) the entire notification setup block is skipped
  - inside the block: capture `start := time.Now()`; declare `var projectName string`; `ucfg, ucfgErr := userconfig.Load(...)`; on parser error `slog.Warn(...)` + `ucfg = nil`; `notifier := newNotifier(ucfg)` (consumer-local seam, see Task 6)
  - install `defer func() { notifier.Notify(context.Background(), notify.Event{Kind: notify.OpRun, Operation: "run", Outcome: outcomeFromErr(err), Duration: time.Since(start), Err: err, Project: projectName}) }()`
  - after the defer is installed, call the existing main-config load; on success `projectName = cfg.Project.Name`
- [ ] in `RunRestart` (`internal/lifecycle/run.go:175`): set `ctx.SkipNotify = true` on the inner `RunRun` call so restart does not double-notify (and matches the spec's "restart does not notify" rule). Document the reason in a one-line comment because the why is non-obvious.
- [ ] write tests in `internal/lifecycle/` for: `RunRun` invokes notifier on success and failure; `RunRun` with `SkipNotify=true` does **not** invoke notifier; `RunRestart` propagates `SkipNotify=true` to its inner `RunRun` (assert by intercepting the notifier factory). `RunStop` is not modified — no test addition needed there. Use the consumer-local `notifier` interface seam (`internal/lifecycle/notify.go`) declared per Task 6's pattern.
- [ ] write tests for the `notify.Event.Project` field — the project name is correctly populated from `cfg.Project.Name`
- [ ] run `make test` — must pass before Task 8

### Task 8: Hook notifier into `usercommands.RunCommand` (gated on `Cmd.Notify` and not-`SkipNotify`)

**Why `UnderParallel` is not enough** (review finding): `UnderParallel` is only set inside parallel groups. Sequential workflow sub-steps inherit `UnderParallel: rc.UnderParallel` from their parent (`runner_workflow.go:225`) — so a `notify: true` command invoked as a sequential workflow sub-step has `UnderParallel == false` and would fire. Same for pipeline actions, which set `UnderParallel = actx.Parallel` (`pipeline/executor.go:276`). A naive `!UnderParallel` gate produces duplicate / noisy notifications for every transitively-invoked command.

**Fix**: add a dedicated **`SkipNotify bool`** field on `runtime.RunContext`. Semantics: "this invocation is not the user's top-level command — do not fire end-of-command notifications." Set transitively by **every** internal invocation site; only the top-level orchestrator leaves it at zero-value `false`.

- [ ] add `SkipNotify bool` to `runtime.RunContext` (`internal/usercommands/runtime/runner.go:28-88`), documented with the rule "always set true when one runtime invokes another"
- [ ] in `internal/usercommands/runtime/runner.go` `RunCommand` (`:125-176`): after `runner.Run(ctx, rc)`, gate notification on `rc.Cmd.Notify == true && !rc.SkipNotify`. Use defer pattern with named return `err`. Build `Event.Kind = notify.OpCommand`, `Event.Operation = "command:" + rc.Cmd.ID`. (Field is `rc.Cmd`, not `rc.CommandDef`.) The `OpCommand` kind routes the per-op gate to `NotifyCommandsEnabled`.
- [ ] **Propagate `SkipNotify=true` at every internal invocation site:**
  - **`runner_workflow.go` sequential dispatch** (`:225` area): set `subRC.SkipNotify = true` (in addition to inheriting `UnderParallel`)
  - **`runner_workflow.go` parallel dispatch**: same — `subRC.SkipNotify = true`
  - **`internal/pipeline/executor.go` action dispatch** (`:276` area, where `actx.Parallel` becomes `UnderParallel`): also set `SkipNotify = true` when building the inner `RunContext` for pipeline-invoked commands. Pipeline-invoked commands are by definition not top-level.
- [ ] **Top-level entry points** explicitly **leave `SkipNotify` at false**: `internal/command/commands.go` `runCommandByID` is the canonical top-level orchestrator — no change needed (zero-value is correct). Document the contract in the field's doc comment.
- [ ] **Cheap path with broad failure coverage**: short-circuit before any I/O, but install the defer early enough to catch pre-run errors. Looking at the current `RunCommand` body (`internal/usercommands/runtime/runner.go:125-176`) there are five error-returning steps: `ComputeFilePaths` (`:141`), `ConfirmCommand` (`:148`), `PrepareFileEffects` (`:152`), `NewRunner` (`:157`), and `runner.Run` (`:162`), plus `emitCommandMessage` on the success path. A `notify: true` command that fails at any of these should fire a failure notification — users want a signal that the command they invoked is done, regardless of whether it died early or late.

  Pseudocode (defer is installed **immediately after** the nil-safe Render scaffolding, **before** `ComputeFilePaths`):
  ```go
  func RunCommand(ctx context.Context, rc RunContext) (err error) {
      // existing nil-safe Render scaffolding (lines 126-139, no error returns)
      if rc.Render == nil { rc.Render = &tpl.RenderContext{} }
      if rc.Render.Raw == nil && rc.Config != nil { rc.Render.Raw = rc.Config.Raw }
      if rc.Render.Params == nil { rc.Render.Params = make(map[string]any) }
      if rc.Render.Context == nil { rc.Render.Context = make(map[string]any) }

      // Cheap-path check + notifier install BEFORE any error-returning step.
      // Guard the rc.Cmd.Notify read defensively in case a caller passes nil.
      if rc.Cmd != nil && rc.Cmd.Notify && !rc.SkipNotify {
          start := time.Now()
          var projectName string
          if rc.Config != nil { projectName = rc.Config.Project.Name }
          ucfg, ucfgErr := userconfig.Load(rc.ProjectRoot)
          if ucfgErr != nil { slog.Warn("userconfig load failed; notifications disabled", "err", ucfgErr); ucfg = nil }
          notifier := newNotifier(ucfg)
          cmdID := rc.Cmd.ID
          defer func() {
              notifier.Notify(context.Background(), notify.Event{
                  Kind:      notify.OpCommand,
                  Operation: "command:" + cmdID,
                  Outcome:   outcomeFromErr(err),
                  Duration:  time.Since(start),
                  Err:       err,
                  Project:   projectName,
              })
          }()
      }

      paths, err := ComputeFilePaths(rc)        // errors here now notify
      if err != nil { return err }
      // ...ConfirmCommand, PrepareFileEffects, NewRunner, runner.Run — all errors notify...
  }
  ```
  Three notes:
  1. The cheap-path check uses `rc.Cmd != nil` first — defensive against future callers passing nil; existing call sites all pass non-nil but the cost of the guard is zero.
  2. The defer captures `cmdID` and `projectName` as locals so any later mutation of `rc.Cmd` or `rc.Config` (e.g., by sub-command expansion) doesn't change the event payload.
  3. Unlike the deploy / lifecycle hookpoints (which always set up the notifier), the runtime hookpoint is conditional on `notify: true` — workflow sub-steps with `notify: false` (the common case) skip the entire setup block including `userconfig.Load`, avoiding the per-sub-step file read.
- [ ] write a test asserting that a workflow with many `notify: false` sub-steps does **not** trigger a `userconfig.Load` per sub-step — use a `userconfigLoadFunc` package-level test seam (same pattern as `beeepNotify`) and count invocations.
- [ ] write tests in `internal/usercommands/runtime/` for: `Notify=true` + `SkipNotify=false` + success → notifier called with success; `Notify=true` + `SkipNotify=false` + failure → notifier called with failure; `Notify=true` + `SkipNotify=true` → notifier **not** called; `Notify=false` → never called regardless of `SkipNotify`; **pre-run failure coverage** — assert that errors from `ComputeFilePaths`, `ConfirmCommand`, `PrepareFileEffects`, and `NewRunner` all still fire the notification (use a fake `model.CommandDef` shape that triggers each early-return path, or test seams on the helpers)
- [ ] write a test asserting the workflow runner sets `SkipNotify=true` on every sub-step `RunContext` it builds (both sequential and parallel paths)
- [ ] write a test asserting the pipeline executor sets `SkipNotify=true` when invoking a command through a pipeline action
- [ ] write an integration-style test: top-level command `A` is a workflow that contains a `notify: true` sub-step `B`. Run `A` (no `notify:` on A). Assert: zero notifications fired (B's `SkipNotify` was true).
- [ ] write a second integration-style test: top-level command `A` has `notify: true`, contains a parallel block with sub-step `B` (also `notify: true`). Run `A`. Assert: exactly one notification fired (for `A` only; B's `SkipNotify` was true).
- [ ] use the consumer-local `notifier` interface seam pattern from Task 6 (declare `type notifier interface { Notify(context.Context, notify.Event) }` in `internal/usercommands/runtime/notify.go`)
- [ ] run `make test` — must pass before Task 9

### Task 9: Documentation

- [ ] create `docs/reference/config/notifications.md` with sections:
  - file locations + creation rules (`0600` for global, gitignored `.devbox/config` for project) with the per-OS path table from "Global config path resolution"
  - flat-format syntax + precedence order
  - complete key reference for MVP (`notify_enabled`, `notify_run_enabled`, `notify_deploy_enabled`, `notify_commands_enabled`, `notify_channels`); the gate-matrix table — master switch ANDed with per-op switch ANDed with environment-interactive
  - reserved-for-future keys with explicit "not yet wired" note
  - **"When notifications fire" behavior matrix** — must explicitly cover **all** of these to set correct expectations (the previous draft only mentioned parallel):
    - `devbox deploy` always notifies (gated by `notify_deploy_enabled`)
    - `devbox run` notifies (gated by `notify_run_enabled`); `devbox restart` / `devbox stop` / `devbox reset` **never** notify
    - `devbox commands <id>` notifies **only when** `<id>` is the top-level invoked command **and** that `CommandDef` has `notify: true`; commands invoked transitively — as a workflow sub-step (sequential or parallel) or from a deploy pipeline action — are **always suppressed at runtime** regardless of their own `notify:` field. The rule is: "the notification fires for the command you typed, not for any command it runs internally."
    - daemon commands (`.start` / `.logs` / `.stop` / `.restart`) reject `notify: true` at validation time (no completion event semantics for daemons)
  - environment-variable overrides for **all four** `notify_*_enabled` keys plus `notify_channels`
  - non-interactive detection rules (CI / `DEVBOX_NONINTERACTIVE` / non-TTY)
  - **drop-on-busy policy**: if the OS notifier daemon stalls, subsequent notifications within that operation are dropped with a debug log (no user-visible error)
  - sample config block showing common settings (mute `notify_run_enabled` for the inner-loop case)
- [ ] update `docs/reference/config/commands.md`: add `notify:` to the `CommandDef` schema reference; document the daemon-rejection rule (validator error); document the **top-level-only suppression rule** — "`notify: true` fires only when the command is the top-level `devbox commands <id>` target; commands invoked transitively (as a workflow sub-step, sequential or parallel, or from a deploy pipeline action) have their notification suppressed at runtime regardless of `notify:` value"; note that the validator emits an **info** diagnostic for the static-detectable direct-parallel-sub-step case as an early warning, but the runtime guard is the actual enforcement and covers transitive cases too; cross-link to `notifications.md`
- [ ] update `AGENTS.md` (and thus `CLAUDE.md` via the symlink) "Key Patterns" or package list: brief mention of `internal/userconfig/` and `internal/notify/`, the notifier hookpoints, and the `SkipNotify` invariant on `lifecycle.RunRestart`. Keep it to ~3-4 lines total — same density as existing entries.
- [ ] no code changes in this task; tests not applicable. (Docs-only task is the documented exception — no `run tests` checkbox needed beyond a final `make build` to confirm nothing broke during prior tasks' cleanup.)
- [ ] run `make build` — must succeed before Task 10

### Task 10: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented:
  - userconfig package loads global + project flat configs with precedence
  - notify package: native + noop + factory with detection seam
  - `model.CommandDef.Notify` field + YAML round-trip (accessed at runtime as `rc.Cmd.Notify`)
  - validator rules: daemon→error, parallel→info
  - deploy + RunRun + RunCommand all wired with defer-based notification
  - `RunRestart` bypasses notification via `SkipNotify`
  - docs page exists, commands schema updated
- [ ] verify edge cases:
  - userconfig parser errors don't crash deploy / run / command (warning + noop fallback)
  - non-TTY / CI / `DEVBOX_NONINTERACTIVE=1` short-circuit to `noopNotifier`
  - beeep backend error swallowed (debug log only)
  - command failure path still fires `OutcomeFailure` notification
  - parallel sub-step with `notify: true` is silently skipped at runtime
- [ ] run `make test` — full unit test suite must pass
- [ ] run `make lint` — all lint issues must be fixed (no `nolint` shortcuts without justification)
- [ ] run `make build` — binary builds cleanly

## Technical Details

### `userconfig` package internals

- Defaults: `NotifyEnabled = true`, `NotifyRunEnabled = true`, `NotifyDeployEnabled = true`, `NotifyCommandsEnabled = true`, `NotifyChannels = []string{"native"}`. All four `Notify*Enabled` flags ship `true` so a user who only sets `notify_enabled = false` cleanly mutes everything (master switch behavior); a user who wants fine-grained muting flips just the per-op flag they care about.
- Gate accessor: `(c *Config) NotifyEnabledFor(kind string) bool` returns `c.NotifyEnabled && <matching per-op flag>`. Switch on `kind`:
  - `"deploy"` → `NotifyDeployEnabled`
  - `"run"` → `NotifyRunEnabled`
  - `"command"` → `NotifyCommandsEnabled`
  - anything else → `false` (defensive). The string-keyed form keeps `notify` from importing `userconfig` (avoids cycle); the `notify` package translates its `OperationKind` enum to the string at the call site.
- Env var spelling mirrors keys: `DEVBOX_<UPPER_SNAKE>` (`DEVBOX_NOTIFY_ENABLED`, `DEVBOX_NOTIFY_RUN_ENABLED`, `DEVBOX_NOTIFY_DEPLOY_ENABLED`, `DEVBOX_NOTIFY_COMMANDS_ENABLED`, `DEVBOX_NOTIFY_CHANNELS`). Boolean env: `1`/`true`/`yes` truthy; `0`/`false`/`no` falsy. Lists: comma-separated.
- Parser invariants:
  - Lines are trimmed of leading / trailing whitespace.
  - Lines starting with `#` after trim are comments.
  - Blank lines are ignored.
  - Lines containing `#` after the value are **errors** (`inline comments not supported at line N`).
  - Keys containing `.` are **errors** (`dotted keys not allowed; use _ separators`).
  - Unknown keys are **warnings** logged via `slog.Warn` (forward-compat with future channels).
  - Each line that's neither blank nor comment must match `^[a-z][a-z0-9_]*\s*=\s*.*$` — if not, parse error.
- `Load(projectRoot)`:
  1. Start from defaults.
  2. Attempt global read at `<os.UserConfigDir()>/devbox/config`. Missing → skip silently. Parse error → return wrapped error.

### Global config path resolution

The global file path uses `os.UserConfigDir()` (Go standard library) — platform-native, **not** XDG-everywhere:

| OS | Resolved path |
| --- | --- |
| Linux | `$XDG_CONFIG_HOME/devbox/config` (falls back to `$HOME/.config/devbox/config`) |
| macOS | `$HOME/Library/Application Support/devbox/config` |
| Windows | `%AppData%\devbox\config` (typically `C:\Users\<user>\AppData\Roaming\devbox\config`) |

This matches Go ecosystem convention (the same path tree where most Go CLIs and the Go toolchain itself put per-user config). The documentation page (`docs/reference/config/notifications.md`, Task 9) lists all three paths explicitly so users can locate their config without guessing. `os.UserConfigDir()` errors (no `$HOME`, etc.) propagate via `Load`'s returned error — the loader does not silently skip on resolution failure (that would mask a real environment problem).
  3. Attempt project read at `projectRoot + "/.devbox/config"`. Same rules.
  4. Apply env overrides last in the file-precedence layer (but env still loses to nothing — env is the highest precedence).

### `notify` package internals

- **Public surface**: one concrete type `*Notifier`, one constructor `New(cfg) *Notifier`, one method `Notify(ctx, ev)`, plus `Event` / `Op` / `Outcome` types. No exported interface. (Per golang-structs-interfaces: return concrete types from constructors; define interfaces where consumed — here the only interface is the unexported `backend` consumed inside `notify` itself.)
- `Event.Outcome` enum:
  ```go
  type Outcome int
  const (
      OutcomeUnknown Outcome = iota
      OutcomeSuccess
      OutcomeFailure
  )
  ```
  `OutcomeUnknown` at iota 0 lets us catch unset-Outcome bugs in tests.
- `Event.Kind` enum (type name `Op` so constants follow the type-name-prefix convention per golang-naming):
  ```go
  type Op int
  const (
      OpUnknown Op = iota
      OpDeploy
      OpRun
      OpCommand
  )
  func (k Op) configKey() string {
      switch k {
      case OpDeploy:  return "deploy"
      case OpRun:     return "run"
      case OpCommand: return "command"
      default:        return ""
      }
  }
  ```
- **Per-op gate placement**: inside `Notifier.Notify(ctx, ev)`, after the factory-level `enabled` check. Sequence is `n == nil → enabled → master+per-op → backend.notify`. A miss at any stage returns silently.
- **Nil-safe**: `(*Notifier)(nil).Notify(...)` is a no-op. Hookpoints can capture the return of `New(cfg)` and call `.Notify` without nil checks; this matters because `userconfig.Load` failure produces a `nil` cfg → `New(nil)` returns a Notifier with `enabled=false` (not a nil pointer) but the nil-safety is belt-and-suspenders.
- `Notify` is best-effort: errors from the backend are logged at debug, never returned to caller. The backend itself is wrapped in a 2-second timeout + ctx-cancellation `select` so a stalled OS notifier daemon never delays the calling operation.
- **Compile-time interface checks** (golang-structs-interfaces): `var _ backend = (*noopBackend)(nil)` and `var _ backend = (*nativeBackend)(nil)` placed near each implementation.
- Title format strings (locked):
  - success: `"✓ Devbox: %s succeeded"`
  - failure: `"✗ Devbox: %s failed"`
- Body format (locked):
  - success: `"%s · %s"` (project, humanised duration)
  - failure: `"%s · %s\n%s"` (project, duration, truncated first-line err — truncate at 200 chars)
- Duration humanising: `< 1s` → "Xms"; `< 60s` → "X.Xs"; `< 1h` → "Xm Ys"; `>= 1h` → "Xh Ym".

### Hookpoint contract (shared across all three sites)

Every hookpoint follows the same shape:

```go
func ...(...) (err error) {
    start := time.Now()

    // Capture project name into a local that defaults to "" so the defer is
    // panic-safe even if main config load fails before assignment. Reading
    // cfg.Project.Name directly inside the defer would NPE on early returns.
    var projectName string

    ucfg, ucfgErr := userconfig.Load(projectRoot)
    if ucfgErr != nil {
        slog.Warn("userconfig load failed; notifications disabled for this run", "err", ucfgErr)
        ucfg = nil // notify.New tolerates nil
    }
    notifier := newNotifier(ucfg) // consumer-local seam, see Task 6
    defer func() {
        // context.Background() is intentional: the notification fires AFTER
        // the operation has finished (success or error), so the operation's
        // own context is moot. We never want a cancelled operation to also
        // suppress the "I finished" notification — that's the entire UX
        // value of the notification. The backend applies its own internal
        // 2-second timeout independently (see Task 3).
        notifier.Notify(context.Background(), notify.Event{
            Kind:      notify.Op<Deploy|Run|Command>,
            Operation: "<op>",
            Outcome:   outcomeFromErr(err),
            Duration:  time.Since(start),
            Err:       err,
            Project:   projectName, // empty string if cfg never loaded
        })
    }()

    cfg, err := config.LoadConfig(...)
    if err != nil { return err } // defer fires with projectName == ""
    projectName = cfg.Project.Name

    // ...existing body...
}
```

**Why the order matters**: the userconfig load + notifier construction + defer must happen *before* main config load so that a malformed `devbox.yml` still triggers a "deploy failed" notification — that's exactly the user-visible failure mode that benefits most from a passive signal. The cost is that `projectName` will be empty in the notification body when main config never loads; the backend renders that as just the operation name + duration, which is acceptable. Tests must cover this case explicitly: assert `cfg`-load-failure path → notifier called with `OutcomeFailure` and `Project == ""`, no panic.

- The defer relies on **named return** for `err`. Confirm each hookpoint has (or is changed to have) a named return.
- For `RunRun` the defer body is additionally wrapped in `if !ctx.SkipNotify { ... }`.
- For `RunCommand` the defer body is wrapped in `if rc.Cmd.Notify && !rc.SkipNotify { ... }` — `Cmd` is the field name on `runtime.RunContext` for the `*model.CommandDef`, and `SkipNotify` is the transitive-invocation flag set by every internal call site (Task 8).

### Context propagation at hookpoints

Each hookpoint calls `notifier.Notify(context.Background(), event)` — **not** the operation's own context. Two reasons:

1. **The notification fires in the `defer`, after the operation has returned.** By that point the operation's `ctx` may already be cancelled (Ctrl-C, timeout, parent shutdown). If we passed it through, every cancelled deploy would also suppress the "I finished" notification — which is the exact UX moment users care about.
2. **The backend has its own internal 2-second timeout** (see Task 3's `nativeBackend.notify`), so using `context.Background()` doesn't risk unbounded waits.

This sidesteps an implementation question that came up during review — `deployRunCmd` and `lifecycle.RunRun` do not currently have a `context.Context` in scope (`deploy.go:135`, `run.go:52`). We deliberately do **not** thread one through just for notifications. If a future need (telegram backend, HTTP-bound channels) requires a real cancellable ctx, the right move then is to add `context.Context` to `lifecycle.RunContext` and pass `cmd.Context()` into `deployRunCmd` — but that's beyond MVP scope.

### Validator placement

The new `notify:` validator goes in `internal/validate/commands/notify.go` (new file) and is registered in `internal/validate/commands/All()`. Walking parallel containment uses `registry.WalkWorkflowSteps` (already exists per `AGENTS.md` reference, path: `step[i]` at root, `step[i].parallel.steps[j]` inside groups).

### Go-skills compliance summary

Plan was reviewed against `golang-project-layout`, `golang-naming`, `golang-structs-interfaces`, `golang-error-handling`, and `golang-design-patterns`. Decisions explicitly grounded in those skills:

| Skill rule | Plan decision |
| --- | --- |
| "Return concrete types from constructors" (structs-interfaces) | `New(cfg) *Notifier`, not `Notifier` interface. The interface that varies (`backend`) is unexported and lives where consumed. |
| "Enum type-name prefix" (naming) | Type `Op` with constants `OpUnknown`/`OpDeploy`/`OpRun`/`OpCommand`. Same for `Outcome` → `OutcomeUnknown`/... |
| "Zero value = unknown/invalid sentinel" (naming + design-patterns) | `OpUnknown` and `OutcomeUnknown` both at iota 0; `Op.configKey()` returns `""` for `OpUnknown` (defensive). |
| "Compile-time interface checks" (structs-interfaces) | `var _ backend = (*noopBackend)(nil)` for each impl. |
| "Single handling rule: log OR return, not both" (error-handling) | `userconfig.Load` errors logged-and-degrade-to-noop at hookpoints (deliberate; documented). Backend errors logged-only (best-effort by design). |
| "Wrap with `%w` and lowercase + no punctuation" (error-handling) | Parser errors prefixed `"userconfig: "`, wrapped at loader boundary via `%w`. |
| "Every external call has a timeout" (design-patterns) | `beeep.Notify` wrapped in goroutine + `select` on `time.After(2s)` and `ctx.Done()`. |
| "Avoid `init()` and hidden globals" (design-patterns) | No `init()` functions. Test seams (`isInteractiveForNotify`, `beeepNotify`) are package-level `var func(...)` — mirrors the project's existing `ui.IsInteractiveFn` pattern. |
| "Define interfaces where consumed" (structs-interfaces) | Two layers of interfaces, each owned by its consumer: (a) the unexported `backend` interface inside `notify` (consumed by `Notifier.Notify` to dispatch between noop/native); (b) consumer-local `notifier` interface declared per-package in `internal/command/`, `internal/lifecycle/`, and `internal/usercommands/runtime/` so each call site can swap in a recording fake for tests. `notify.New` returns concrete `*Notifier`, which satisfies all three consumer-local interfaces structurally. |
| "Accept dependencies, don't import-cycle" (project-layout) | `notify` imports `userconfig`; `userconfig` does not import `notify`. The gate accessor on `userconfig.Config` is string-keyed (`NotifyEnabledFor(kind string)`) so the dependency arrow is one-way. |

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only.*

**Manual verification:**
- macOS 14+: `make build && ./bin/devbox deploy` against a sample project — confirm native notification appears with the success title; force a failure (kill a service before deploy) — confirm failure notification.
- Linux (if available): same drill; beeep uses `notify-send` under the hood. If `notify-send` is missing, confirm the debug log shows the backend error and deploy still succeeds.
- Quick smoke that `CI=1 ./bin/devbox deploy` does **not** show a notification (env short-circuit).
- Confirm `.devbox/config` is properly gitignored (`.devbox/` is already in `.gitignore`; verify nothing slips through).

**Out of scope for this plan (deferred):**
- CLI flags `--notify` / `--no-notify` — env var is sufficient for MVP.
- Telegram backend (reserved keys are decoded but no notifier exists).
- Webhook backend (same).
- Notification thresholds (minimum duration before a notification fires).
- Separate `on_success` / `on_failure` toggles.
- Notifications on `reset` / `restart` / `stop`.
- Per-sub-step notifications inside parallel blocks.
- Custom icon paths / custom notification sounds.
