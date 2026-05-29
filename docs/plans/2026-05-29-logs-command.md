# `devbox logs <service>` command

## Overview

Add a new top-level command `devbox logs <service>` that streams Docker container logs for a project service through a thin, project-aware wrapper. Without this command, AI agents diagnosing unhealthy services have to know `docker logs <container-name>` directly — which requires knowing the container naming scheme. With it, the agent uses the service name from `devbox.yml` / `devbox/services/*/` and devbox handles container resolution.

Behaves like `docker logs` for the human path (text passthrough) and emits NDJSON (newline-delimited JSON, one object per log line) in `--output json` mode. Supports `--tail`, `--since`, and `--follow`. Reads logs from a single service per invocation (multi-service is out of scope for v1).

This is Wave 1 deliverable #3 from the AI integration roadmap. It depends on Plan 1 (`2026-05-29-json-state-output.md`) for the global `--output` flag mechanism; if Plan 1 is not yet landed when this plan is implemented, a local `--json` flag is acceptable as a temporary measure to be migrated later.

## Context (from discovery)

- **Convention**: devbox already shells out to docker CLI (`docker stop`, `docker compose`) via `os/exec` rather than using the Docker SDK. New command follows the same pattern (`docker logs`).
- **Container resolution**: `internal/shared/daemon/daemon.go` exports `ResolveContainerName(projectFullName, renderedTemplate) (string, error)`. Used today by stop and reset paths; reused here.
- **`docker logs` flag semantics** (verified via docs):
  - `--tail N` — number of trailing lines (default "all"; we default to 50)
  - `--since DURATION|TIMESTAMP` — relative or absolute lower bound
  - `--follow` / `-f` — stream new lines as they arrive
  - `--timestamps` — prefix each line with RFC3339Nano (we always pass this for the JSON path; gate it for the text path)
  - Docker writes container stdout to our stdout and container stderr to our stderr; this is how stream attribution survives.
- **CLAUDE.md context**: this is a read-only command (no lock, no preflight). Service iteration must use `config.DeployOrder` if listing.
- **Binary accessor**: `config.DockerBin(cfg)` is the nil-safe path to the docker binary (per CLAUDE.md Key Patterns).

## Development Approach

- **Testing approach**: Regular (code first, then tests).
- Complete each task fully before moving to the next.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task.
- **CRITICAL: all tests must pass before starting next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run `make test` after each task.
- Use fake docker binaries in tests (same approach as `internal/shared/docker/stop_test.go`).

## Testing Strategy

- **Unit tests**: for line parsing, timestamp parsing, JSON envelope shape.
- **CLI integration tests**: invoke `logs <service>` with a fake docker binary that produces canned output; assert stdout/stderr and exit code.
- **No real-docker tests**: keep CI hermetic. The fake-docker pattern from `stop_test.go` is the standard.
- **No e2e**: no UI.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.

## Solution Overview

**Command shape**: `devbox logs <service> [--tail N] [--since DUR|TS] [--follow] [--output text|json]`

**Text mode** (default): pass `docker logs` output through unchanged. stdout of container → our stdout; stderr → our stderr. Timestamps included only when `--timestamps` is set on the caller (proxied to docker).

**JSON mode** (when `--output json` is set on root, or local `--json` if Plan 1 hasn't shipped): always invoke docker with `--timestamps`; parse each line into `{"ts": "2026-05-29T07:30:00Z", "stream": "stdout"|"stderr", "msg": "..."}`; emit as NDJSON (one JSON object per line, no array wrapper, no trailing comma — this is the standard streaming-friendly shape).

**Key design decisions**:
- Single service per invocation; multi-service is out of scope (could be a future `devbox logs --all` with prefixed line attribution).
- `--follow` works in both modes; JSON mode emits NDJSON live as docker produces lines.
- Unknown service → `service_unknown` `CodedError` with hint listing available services.
- Container not running but exists → docker logs returns the buffered history; we pass through.
- Container does not exist (not deployed) → `container_not_found` `CodedError`.

## Technical Details

### Package
`internal/cli/logs/` — new package, one entry: `NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command`. Registered in `internal/cli/root.go` under the Environment group (alongside `status`).

### Service-to-container resolution
- Load config via the existing `RootFlags.ConfigPath` path (project required; do NOT add to `allowedWithoutProject`).
- Validate `<service>` arg against `cfg.Services` keys (folder-key space per CLAUDE.md).
- Resolve container name: `daemon.ResolveContainerName(cfg.Project.FullName(), cfg.Services[name].Container)` — same call surface used by stop.

### Subprocess wiring
- Build args: `[]string{"logs"}`; append `"--tail", strconv.Itoa(tail)`, `"--since", since` (when set), `"--follow"` (when set), `"--timestamps"` (always when JSON mode), then container name.
- `exec.CommandContext(ctx, config.DockerBin(cfg), args...)`.
- Text mode: `cmd.Stdout = cmd.OutOrStdout(); cmd.Stderr = cmd.ErrOrStderr()` — direct passthrough; no parsing overhead.
- JSON mode: pipe both streams through line readers; for each line, build `logLineJSON{Ts, Stream, Msg}` and write via `json.NewEncoder(cmd.OutOrStdout()).Encode(rec)` (newline added automatically — NDJSON).

### NDJSON line shape
```go
type logLineJSON struct {
    Ts     string `json:"ts"`               // RFC3339Nano from docker --timestamps
    Stream string `json:"stream"`           // "stdout" | "stderr"
    Msg    string `json:"msg"`              // line content with trailing newline stripped
}
```

### Timestamp parsing
- docker `--timestamps` format: `2024-01-15T10:30:45.123456789Z <message>` — first whitespace-delimited token is the timestamp.
- If parsing fails (rare; docker version mismatch), fall back to `time.Now().UTC().Format(time.RFC3339Nano)` and continue — don't drop the line.

### Concurrent stream reading

Per golang-concurrency skill principles: every goroutine needs a clear exit; only the sender closes; default to small buffers (large buffers mask backpressure); always include `ctx.Done()` in select.

- **Goroutines**: two readers (one per pipe). Each owns a typed send-only channel `chan<- logLineJSON`.
- **Channel**: single `chan logLineJSON` shared between the two readers (writers) and the main drain loop (reader). Buffer size = **16** (small bounded — enough to amortize handoff cost, not so large that backpressure from main is masked). NOT 256.
- **Ownership / close protocol**: an `errgroup.Group` with both readers as members. Main goroutine spawns: `eg.Go(readStdout); eg.Go(readStderr)` then `go func() { eg.Wait(); close(ch) }()` — the closer goroutine ensures `close(ch)` runs exactly once AFTER both readers have exited (so neither writer can panic on send-after-close).
- **Drain**: `for rec := range ch { json.NewEncoder(cmd.OutOrStdout()).Encode(rec) }` — single writer to stdout, no race; loop exits on channel close.
- **Cancellation**: each reader's scanner loop checks `ctx.Err()` between `Scan()` calls (the pipe will EOF when the child process is killed via ctx cancellation, so the reader exits naturally — but explicit ctx check makes it deterministic).
- `errgroup.WithContext` (NOT raw `errgroup.Group`) so one reader's error cancels the other; final error surfaces from `eg.Wait()`.

### Signal handling
- `cmd/devbox/main.go:60` calls `fang.Execute(context.Background(), root, ...)` with a raw context — fang does NOT install signal handling. Each command that needs SIGINT/SIGTERM cancellation must wire its own.
- For `--follow` mode this command must call `signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)` and pass the resulting ctx to `exec.CommandContext`.
- Use the **`daemon_logs` builtin pattern** (`internal/core/execution/builtin/daemon_logs.go:68-93`) as the reference implementation: `cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }` for graceful shutdown, `cmd.WaitDelay = 3 * time.Second` to force-kill after a grace period, and exit-code handling that treats SIGINT (exit 130), negative-with-ctx-cancelled, and `exec.ErrWaitDelay` as clean exits.

### Flag interplay with Plan 1
- If Plan 1's global `--output text|json` is available (`rflags.Output`), use it.
- If not yet available, fall back to a local `--json` boolean flag on this command and migrate later. Mark this as a follow-up in the plan when the situation is detected at implementation time.

## What Goes Where

- **Implementation Steps**: package creation, command wiring, service resolution, subprocess plumbing, JSON path, follow mode, tests.
- **Post-Completion**: manual verification on a running project, future multi-service variant.

## Implementation Steps

### Task 1: Package skeleton + command registration

**Files:**
- Create: `internal/cli/logs/logs.go`
- Modify: `internal/cli/root.go` (register under environment group)
- Create: `internal/cli/logs/logs_test.go`

- [x] create `internal/cli/logs/logs.go` with `NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command`
- [x] cobra command: `Use: "logs <service>"`, `Args: cobra.ExactArgs(1)`, RunE wiring to local `runLogs(cmd, flags, args, options)`
- [x] register flags: `--tail` (int, default 50), `--since` (string, default ""), `--follow` / `-f` (bool, default false)
- [x] (conditional) if Plan 1 has landed, no local `--json` flag — use `rflags.Output`. Else add `--json` (bool, default false) as transitional flag.
- [x] in `root.go`: `root.AddCommand(cmdLogs.NewCmd(groupEnvironment, flags))`
- [x] write test: `devbox logs` (no args) errors with usage; `devbox logs <name>` accepts
- [x] run `go test ./internal/cli/logs/...` and `go test ./internal/cli/...` — must pass before Task 2

### Task 2: Service resolution

**Files:**
- Modify: `internal/cli/logs/logs.go`
- Modify: `internal/cli/logs/logs_test.go`

- [x] in `runLogs`: load config via `config.LoadConfig(flags.ConfigPath)`
- [x] validate service name: `if _, ok := cfg.Services[name]; !ok { return cmdctx.Err("service_unknown", ...).WithHint("available: " + strings.Join(known, ", ")).WithDetail("requested", name) }` (use plain `fmt.Errorf` if CodedError not yet available from Plan 1)
- [x] resolve container: `containerName, err := daemon.ResolveContainerName(cfg.Project.FullName(), cfg.Services[name].Container)`; propagate any error
- [x] write test: unknown service returns service_unknown error
- [x] write test: known service resolves to expected container name (use minimal config fixture)
- [x] run `go test ./internal/cli/logs/...` — must pass before Task 3

### Task 3: Subprocess wiring (text mode)

**Files:**
- Modify: `internal/cli/logs/logs.go`
- Modify: `internal/cli/logs/logs_test.go`
- Create: `internal/cli/logs/testdata/fake-docker-text.sh` (fake docker binary)

- [x] in `runLogs`: build args slice with `logs`, `--tail`, optionally `--since`, optionally `--follow`, container name
- [x] text mode: `exec.CommandContext(ctx, config.DockerBin(cfg), args...)`; `cmd.Stdout = cmd.OutOrStdout()`; `cmd.Stderr = cmd.ErrOrStderr()`; `cmd.Run()`
- [x] propagate non-zero exit code from docker; if docker emits a recognizable "No such container" message on stderr, transform to `container_not_found` CodedError. **Reuse existing pattern**: both string-match sites live in `internal/shared/docker/stop.go` (lines 34 and 61 — there is NO separate `rm.go`); extract to `internal/shared/docker/errors.go` as `IsNoSuchContainerErr(stderr string) bool` (preferred, since logs needs it too) and refactor stop.go to use the helper, or copy the same pattern locally for parity.
- [x] write fake docker shell script (executable) that echoes canned text + exits 0
- [x] write test: text mode produces expected stdout
- [x] write test: fake docker exits with code 1 → command returns wrapped error
- [x] run `go test ./internal/cli/logs/...` — must pass before Task 4

### Task 4: JSON (NDJSON) mode + timestamp parsing

**Files:**
- Modify: `internal/cli/logs/logs.go`
- Create: `internal/cli/logs/parse.go` (line parser + DTO)
- Create: `internal/cli/logs/parse_test.go`
- Create: `internal/cli/logs/testdata/fake-docker-json.sh`

- [x] in `parse.go`: define `logLineJSON{Ts, Stream, Msg string}` with JSON tags `ts`, `stream`, `msg`
- [x] implement `parseLine(stream, raw string) logLineJSON` — split on first whitespace; if first token parses as `time.RFC3339Nano`, use it; else use `time.Now().UTC().Format(time.RFC3339Nano)`; strip trailing CR/LF from msg
- [x] in `logs.go`: when JSON mode, append `--timestamps` to docker args; pipe stdout and stderr via `cmd.StdoutPipe()` / `cmd.StderrPipe()`
- [x] spawn two reader goroutines via `errgroup.WithContext`. Each reader signature is `func(ch chan<- logLineJSON) error` — explicit send-only channel direction (golang-concurrency: "specify channel direction; the compiler prevents misuse"). Each scans its pipe with `bufio.Scanner`, calls `parseLine`, sends to a shared channel buffered to **16** (small bounded buffer — large buffers mask backpressure per skill)
- [x] **bump `bufio.Scanner` buffer**: docker logs routinely exceed the default 64KB max-token-size on stack traces, JSON dumps, base64 payloads. Before each reader's scan loop, call `sc.Buffer(make([]byte, 64*1024), 1024*1024)` (start 64KB, cap 1MB). On `sc.Err() == bufio.ErrTooLong`, emit a synthetic truncation record (`{"stream": "...", "msg": "<truncated: line exceeded 1MB>"}`) rather than dropping the stream silently
- [x] **panic safety in readers**: each reader goroutine uses `defer func() { if r := recover(); r != nil { /* convert to error via fmt.Errorf and return — errgroup will cancel siblings */ } }()` so a parser panic doesn't leave the closer goroutine waiting forever on `eg.Wait()`. Alternatively, the drain loop can use `for { select { case rec, ok := <-ch: ...; case <-ctx.Done(): return ctx.Err() } }` so ctx cancellation unblocks the drain even if close never fires
- [x] a closer goroutine waits for both readers via `eg.Wait()` then `close(ch)` exactly once; main goroutine drains the channel via `for rec := range ch { ... }` and writes each record via `json.NewEncoder(cmd.OutOrStdout()).Encode(rec)`
- [x] each reader's loop checks `ctx.Err()` between `Scan()` iterations as a defensive belt-and-braces signal (the pipe EOF on subprocess kill is the primary exit)
- [x] wait for both pipe readers AND the subprocess; on subprocess error after pipe close, propagate
- [x] write `parse.go` table-driven tests covering: valid RFC3339Nano line, malformed timestamp (fallback), empty message, CRLF stripping
- [x] write integration test with fake docker that emits both stdout and stderr lines → assert NDJSON shape and stream attribution. **NDJSON ordering note**: within a single stream `bufio.Scanner` preserves order; across two streams (stdout/stderr goroutines feeding one channel) ordering is inherently non-deterministic — test must assert per-stream ordering (`stdout` events appear in emit order; `stderr` events appear in emit order) and set-equality on the full collection, NOT a fixed cross-stream sequence
- [x] **fake-docker fixture sketch** (for `testdata/fake-docker-json.sh`):
      ```sh
      #!/bin/sh
      echo "2026-05-29T07:30:00.000000000Z line-from-stdout"
      echo "2026-05-29T07:30:01.000000000Z line-from-stderr" >&2
      echo "2026-05-29T07:30:02.000000000Z another-stdout"
      ```
      Marked executable; uses the standard `--timestamps` format docker emits.
- [x] run `go test ./internal/cli/logs/...` — must pass before Task 5

### Task 5: `--follow` mode + signal handling

**Files:**
- Modify: `internal/cli/logs/logs.go`
- Modify: `internal/cli/logs/logs_test.go`

**Correction**: fang's `context.Background()` does NOT install signals. This command must wire its own. The `daemon_logs` builtin at `internal/core/execution/builtin/daemon_logs.go:68-93` is the reference pattern — read it before implementing.

**Goroutine leak detection** (golang-concurrency Best Practice #9): `go.uber.org/goleak` is already in `go.mod` as a direct dependency — no new dep required. Import and add to logs_test.go: `func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }`. This catches any goroutine that escapes the test boundary — critical given this command spawns 2-3 goroutines per `--follow` invocation.

- [x] in `runLogs` (for `--follow` mode only): `ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM); defer stop()`; pass `ctx` to `exec.CommandContext`
- [x] set `cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }` so docker gets SIGINT (graceful) instead of SIGKILL when ctx cancels
- [x] set `cmd.WaitDelay = 3 * time.Second` to force-kill if docker ignores SIGINT for too long
- [x] in error handling: treat `exit code 130` (SIGINT-killed), `ProcessState.ExitCode() == -1` with `ctx.Err() != nil`, and `errors.Is(err, exec.ErrWaitDelay)` ALL as clean exits (return nil), per `daemon_logs.go` precedent
- [x] write test: fake docker that loops emitting lines until SIGTERM; test cancels ctx after a small number of lines; verify (a) test completes within ~5s, (b) at least N NDJSON records emitted, (c) RunE returns nil
- [x] write test: `--follow` text mode passthrough also survives cancellation cleanly
- [x] run `go test ./internal/cli/logs/...` — must pass before Task 6

### Task 6: Error mapping + edge cases

**Files:**
- Modify: `internal/cli/logs/logs.go`
- Modify: `internal/cli/logs/logs_test.go`

- [ ] docker stderr scan for known patterns: `"No such container"` → `container_not_found` CodedError; `"Cannot connect to the Docker daemon"` → `docker_unavailable` CodedError (when these mapping codes exist; otherwise plain wrapped errors)
- [ ] when `--since` value is parseable as duration (e.g. `5m`, `1h`) accept; when parseable as RFC3339 timestamp accept; otherwise fail-fast with `invalid_since` CodedError BEFORE invoking docker
- [ ] when `--tail` is negative, fail-fast with `invalid_tail` error
- [ ] write tests covering each error path
- [ ] run `go test ./internal/cli/logs/...` — must pass before Task 7

### Task 7: Verify acceptance criteria + smoke test

- [ ] `devbox logs <service>` outside a project: errors with project_not_found (default rule applies)
- [ ] `devbox logs unknown` in a project: service_unknown error with hint listing available services
- [ ] `devbox logs <service>` in text mode: stdout matches direct `docker logs <container>`
- [ ] `devbox logs <service> --output json`: NDJSON, one `{ts, stream, msg}` per line
- [ ] `devbox logs <service> --follow`: streams text continuously, Ctrl-C exits cleanly
- [ ] `devbox logs <service> --tail 10 --since 5m`: bounded output
- [ ] `devbox logs <service> --output json --follow`: streams NDJSON live; SIGINT ends output without partial JSON object
- [ ] no lock acquired, no preflight run (verify deploy.lock / snapshot.lock unchanged)
- [ ] `make test` and `make lint` pass

### Task 8: Update documentation

**Files:**
- Modify: `docs/reference/cli/` (auto-regen via `make build`)
- Modify: `AGENTS.md` (mention `devbox logs` as the diagnostic entry point in Key Patterns / status section)
- Move: this plan to `docs/plans/completed/`

- [ ] run `make build` to regenerate embedded docs and CLI reference
- [ ] add one-line mention in AGENTS.md under the Configuration Documentation / Status section about `devbox logs <service>` for runtime log inspection
- [ ] verify content-hashes manifest updated (CI guard)
- [ ] `mkdir -p docs/plans/completed && mv docs/plans/2026-05-29-logs-command.md docs/plans/completed/`

## Post-Completion

**Manual verification**:
- Deploy a real project; run `devbox logs <service>` and verify output matches `docker logs <container>` directly.
- Run `devbox logs <service> --output json --tail 20 | jq -r 'select(.stream == "stderr") | .msg'` and verify stream attribution works for filtering.
- Run `devbox logs <service> --follow` against a service that emits logs periodically; verify live streaming and clean Ctrl-C exit.

**Future enhancements** (not blocking):
- `devbox logs --all` — multi-service mode with prefixed line attribution `service|...`. Likely needs a second iteration of design re: NDJSON shape (add `service` field).
- `--grep PATTERN` server-side filter (current workaround: `devbox logs X --output json | jq 'select(.msg | contains(...))'`).
- Integration with `devbox status apps` to make `health: unhealthy` rows include a hint pointing at `devbox logs <name>`.
