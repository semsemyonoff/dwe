# Devbox Docs — Embedded Documentation, TUI, and Mermaid Rendering

## Overview

Add a documentation subsystem to the devbox CLI:

- **Embedded built-in docs** via `//go:embed` so the binary ships its own reference and architecture notes. The canonical source-of-truth tree lives at the repo root (`docs/reference/`, `docs/internals/`, `docs/i18n/<lang>/reference/...`) because it is also the published user-facing documentation. Because `//go:embed` cannot reach paths outside its own package directory, a sync step (driven by `make embedded-docs` and `go generate ./internal/docs/...`) mirrors these trees into `internal/docs/embedded/`, and `internal/docs/embed.go` does `//go:embed embedded`. **The synced tree is committed** (not gitignored) so `go build`, `go install`, IDE builds, and release tarballs all ship the docs. CI guards against drift via `git diff --exit-code internal/docs/embedded/` after running the sync. The doc set is versioned with the binary.
- **Project docs** read from `./docs/` in the user's devbox project (sibling of `devbox/`). The TUI shows them as a second top-level branch (omitted when absent). Hot-reloaded via `fsnotify`.
- **New subcommands** under `devbox docs`:
  - `devbox docs` (no args, TTY) — interactive TUI browser.
  - `devbox docs show <topic>[#anchor]` — render markdown to stdout (glamour in TTY, raw markdown in pipes/agents).
  - `devbox docs list` — flat topic list.
  - `devbox docs export <dir>` — dump markdown to disk.
  - `devbox docs cache clear` — wipe the mermaid cache.
  - `devbox docs generate` — **existing**, untouched.
- **Mermaid rendering** through a fallback chain: disk cache → runtime `mmdc` → raw block. No build-time pre-render. PNGs cached under `$XDG_CACHE_HOME/devbox/mermaid/`. In TUI, inline images on capable terminals (kitty/ghostty/wezterm) via `rasterm`; system viewer (`open`/`xdg-open`/`start`) elsewhere.
- **Language resolution reuses the existing `i18n.ResolveLocale`** chain (`--lang flag → userconfig.Language → DEVBOX_LANGUAGE → $LANG → "en"`). Per-file fallback: missing translation → English with a banner. Content-hash header staleness check on translations (NOT git SHA — see Technical Details "Hash choice").
- **Configuration:** `docs.mermaid: auto|mmdc|off`, `docs.cache_size_mb: 100`, and `binaries.mmdc` (nil-safe accessor `config.MmdcBin`). No `docs.lang` (one canonical language channel project-wide).
  - `auto` (default): try mmdc if present on PATH (resolved via `config.MmdcBin`); if missing → silent raw-block fallback with hint.
  - `mmdc`: require mmdc; if missing or unavailable → emit an error placeholder (`<📊 [mmdc not installed]>`), but the doc itself still renders.
  - `off`: never invoke mmdc; always raw block.

Solves: today devbox has no in-CLI way to read its own reference docs (only `devbox docs generate` produces files for downstream consumption). Mixed teams cannot read docs in their language. Mermaid diagrams in markdown render as code blocks everywhere.

Does not break the existing `devbox docs generate` flow — it remains under the same parent command.

## Context (from discovery)

**Files/components involved:**

- `internal/command/docs.go` — existing parent `docs` cobra command plus `generate`. Extended with new subcommands. `runE` for `devbox docs` itself is currently absent (cobra prints help by default); we wire a TTY-aware default action.
- `internal/config/devbox.go:26` (`BinariesConfig`) — adds `Mmdc string` field; new accessor `config.MmdcBin(cfg)` mirrors `DockerBin`/`GitBin`/`ShellBin`. New top-level `docs` block in the devbox config schema with `mermaid` and `cache_size_mb`.
- `internal/i18n/locale.go` — **reused as-is**. The new `internal/docs/lang.go` calls `i18n.ResolveLocale` and adds per-file fallback + sha staleness, not a parallel resolver.
- `internal/command/root.go` — `rflags.{I18n, Locale}` already resolved in `PersistentPreRunE`. The new subcommands consume them via `i18n.TranslatorOrNop(rflags.I18n)` for any user-visible strings.
- `internal/ui/cmdbrowser/` and `internal/liveui/` — existing in-process bubbletea/v2 patterns (no `tea.NewProgram`, no `term.MakeRaw`). The docs TUI follows the same shape.
- `docs/reference/` and `docs/internals/` — existing on-disk markdown trees; what gets embedded.
- `docs/i18n/<lang>/reference/...` — **new** directory layout for translations of long-form docs. Distinct namespace from `devbox/i18n/<lang>.yml` (which is for command/UI strings and is unchanged).

**Related patterns found:**

- Nil-safe `*Bin(cfg)` accessors in `internal/config/devbox.go` (lines 33–66) — the model for `MmdcBin`.
- Existing strict-YAML loaders for user-edited config (deploy, reset, lifecycle, command files) use `yaml.Decoder.KnownFields(true)`. The new `docs:` block in `devbox.yml` is part of the main config, which already uses lenient unmarshal — match the surrounding behavior, do not introduce strict for one nested field.
- Existing TUI: `internal/liveui/liveline.go` documents the nine non-negotiable bubbletea/v2 invariants — same constraints apply here (use `Model.View()` directly, no `tea.NewProgram`, no terminal capability queries beyond what `rasterm`/lipgloss expose).
- Existing translator pattern: every display-string read goes through `i18n.Translator` (`*i18n.Store` or `NopTranslator`); completion paths use `i18n.TranslatorOrNop` to handle nil-store in `__complete`.
- Project lock seam (`lock.AcquireProjectLocks`) — **not** needed for docs (read-only; no mutations of project state). Confirmed by walking the locked-command list in CLAUDE.md.

**Dependencies identified:**

New external Go deps:
- `github.com/charmbracelet/glamour` — markdown render
- `github.com/BourgeoisBear/rasterm` — terminal image capabilities + escape sequences
- `github.com/fsnotify/fsnotify` — hot reload for project docs
<!-- goldmark NOT added as a direct dep. Glamour exposes no extender hook (verified via pkg.go.dev — only TermRendererOption with WithStylePath/WithWordWrap/etc.). Mermaid handling is done via text-level preprocessing of the markdown bytes BEFORE glamour sees them. See Task 4. -->

- `golang.org/x/sync/errgroup` — bounded worker pool with context cancellation (used by the TUI prefetch pool). Probably already a transitive dep; if not, add explicitly
- `go.uber.org/goleak` — test-only; detects goroutine leaks in tests for `mermaid`, `tui`, and the watcher

Runtime optional: `mmdc` (mermaid-cli) on `$PATH` — feature gates to raw-block fallback when absent.

Already in `go.mod`: bubbletea/v2, bubbles/v2, lipgloss, gopkg.in/yaml.v3, embed, slog.

## Development Approach

- **Testing approach: Regular** — code first, tests in the same task before moving on. Golden tests for glamour-rendered output, table-driven tests for topic/lang resolution, fixture-based tests for mermaid fallback chain.
- Complete each task fully before the next; small focused changes; run `make test` (or scoped `go test ./internal/docs/...`) after each task.
- Per project policy (CLAUDE.md): pre-release, no `schema_version`, no migration shims. Free to rename. The existing `devbox docs generate` stays; no back-compat hacks needed because the new commands are additive.
- **CRITICAL: every task includes new/updated tests** before moving on. Both success and error paths.
- **CRITICAL: keep this plan in sync with reality** — add ➕ tasks for discoveries, ⚠️ for blockers.
- TUI tests are limited to unit-level coverage of pure components (tree widget, key handler, topic resolver). End-to-end TUI testing is out of scope (no Playwright equivalent in this project; matches the existing `internal/ui/cmdbrowser/` test surface).
- **Concurrency hygiene**: any package that spawns goroutines (mermaid renderer chain, prefetch pool, fsnotify watcher) declares `func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }`. Every test that exercises concurrent code runs under `go test -race`. Package-level `make test` runs without `-race` by default; CI runs both. Every goroutine has a clear exit via `context.Context` cancellation — fire-and-forget is forbidden.

## Testing Strategy

- **Unit tests** (required per task):
  - `internal/docs/` — table-driven for topic resolver, language fallback, SHA staleness check.
  - `internal/docs/render` — golden tests for glamour output (sample input → stable output bytes) and AST-transformer (mermaid block → placeholder inline node).
  - `internal/docs/mermaid/` — fixture fakes for `mmdc` (a tiny shell script that writes a known PNG) so the chain is exercised without a real `mmdc`. Cache LRU eviction tested with `os.Chtimes` to control mtime (the LRU key — atime would be unreliable across filesystems).
  - `internal/docs/tui/` — tree widget collapse/expand, key handler dispatch, search index. Render of the full model under a fixed window size as a smoke test.
  - `internal/docs/export/` — diff-against-fixture for the dumped tree.
- **Integration-ish tests** for the cobra wiring: `devbox docs show <topic>` against a fixture project, both TTY (with `--raw` simulating non-TTY) and pipe modes. `devbox docs list` output stability.
- **No new e2e UI tests.** Same convention as `cmdbrowser`.
- Run `make test` after each task. Final task runs `make lint` plus the full suite.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Keep plan in sync with actual work done.

## Solution Overview

**Architecture (high-level):**

```
                              cobra: internal/command/docs.go
                              ┌──────────────────────────────────┐
                              │  docs (TTY → TUI; non-TTY → err) │
                              │  docs show / list / export       │
                              │  docs cache clear                │
                              │  docs generate  (existing)        │
                              └────────────┬─────────────────────┘
                                           │ all subcmds receive
                                           │ rflags.{I18n, Locale}
                                           ▼
        ┌──────────────────────────────────────────────────────────────┐
        │  internal/docs                                               │
        │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐      │
        │  │ embed    │  │ source   │  │ topic    │  │ lang     │      │
        │  │ (go:emb) │  │ DocRoot  │  │ resolve  │  │ fallback │      │
        │  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘      │
        │       └────────────┴─────────────┴───────────────┘            │
        │                            │                                  │
        │  ┌──────────┐         ┌────▼─────┐         ┌──────────────┐  │
        │  │ render   │ ◄────── │  tree    │ ──────► │  search idx  │  │
        │  │ glamour  │         └──────────┘         └──────────────┘  │
        │  │ + text   │                                                 │
        │  │ preproc  │                                                 │
        │  └────┬─────┘                                                 │
        └───────┼───────────────────────────────────────────────────────┘
                │ mermaid placeholders + DiagramRef side-channel
                ▼
        ┌──────────────────────┐         ┌──────────────────────┐
        │  internal/docs/      │         │  internal/docs/tui   │
        │  mermaid             │         │  bubbletea v2 model  │
        │  Renderer chain:     │         │  tree + viewport     │
        │  cache → mmdc → raw  │         │  keys, diagram nav   │
        │  XDG disk cache      │         │  OSC 52 copy         │
        │  rasterm capability  │         │  fsnotify hot reload │
        └──────────────────────┘         └──────────────────────┘
```

**Key design decisions and rationale:**

1. **One language channel, but docs commands re-resolve unclamped.** `--lang` is the only docs-specific knob; everything else flows through `i18n.ResolveLocale`. Eliminates `DEVBOX_DOCS_LANG` and `docs.lang`. Per file the resolver returns `(content, sourceLang, isStale)` so the UI layer chooses banners. **Important: `rflags.Locale` is clamped to the `internal/i18n.Store` (`root.go:125` uses `store.ClampLocale(...)`)** — fine for command/UI strings (the store is authoritative there), but WRONG for long-form markdown which lives in a different namespace (`docs/i18n/<lang>/...`). Docs commands MUST call `i18n.ResolveLocale(df.lang, cfg.Language, os.Getenv("LANG"))` themselves and feed the raw result to `docs.ResolveContent`, which performs per-file fallback against the markdown tree. Never use `rflags.Locale` for selecting docs translations.
2. **Two distinct i18n namespaces, no merge.** `devbox/i18n/<lang>.yml` (existing) is for UI/command strings. `docs/i18n/<lang>/reference/...` (new) is for long-form markdown. They never share a loader or validator.
3. **Mermaid is best-effort with graceful degradation.** Missing `mmdc`, missing TTY, broken cache — all degrade to a placeholder text with a hint, never error out. Cache key includes `mmdc_version` so an upgrade invalidates renders without manual clearing. Mermaid handling is **text-level preprocessing** of the markdown bytes BEFORE glamour sees them — no goldmark direct import, no parallel parser (glamour exposes no `goldmark.Extender` hook; verified via pkg.go.dev).
4. **TUI uses the existing in-process bubbletea pattern.** No `tea.NewProgram`. Mirrors `internal/ui/cmdbrowser/` and respects the nine `liveui` invariants. Tests cover the model in isolation; no TUI capture-and-diff harness.
5. **`internal/docs/` is command-layer-free.** It imports `internal/i18n` and `internal/config` but NOT `internal/command`. The cobra layer (`internal/command/docs.go`) composes the docs API into subcommands and is the sole writer of stdout/stderr — mirroring the `stack/` and `ui/` boundary documented in CLAUDE.md.
6. **No project lock.** Docs commands are read-only. No `lock.AcquireProjectLocks`.
7. **Clipboard via OSC 52 only.** Zero system deps, SSH-friendly. tmux without `set -g set-clipboard on` gets a one-time hint.
8. **Embed scope is fixed.** Source-of-truth at `docs/reference docs/internals docs/i18n` (repo root). The sync script copies those three trees into `internal/docs/embedded/` and `//go:embed embedded` picks them up. **The synced tree is committed to the repo** (not gitignored) so that `go build`, `go install`, IDE builds, and release-tarball checkouts all ship the docs. A CI guard ensures `internal/docs/embedded/` matches the source on every PR. `docs/plans/` is NOT embedded. Authors editing docs run `make embedded-docs` (or `go generate ./internal/docs/...`) and commit both trees.

9. **Content-hash manifest is a committed generated file.** `internal/docs/content_hashes_gen.go` (containing `var ContentHashes map[string]string`) is generated by `make build` / `go generate ./internal/docs/...` AND committed. It is not gitignored. Drift between source markdown and the manifest surfaces as a git diff during code review. On a fresh checkout (no `make build`), tests still compile because the file is present with the last-committed map. The fallback for missing/empty manifest entries (no-banner) remains as the safety net.

## Technical Details

### Package boundary

```
internal/docs/
  embed.go            // //go:embed embedded (synced from docs/* via scripts/sync-embedded-docs.sh;
                      // committed tree; //go:generate directive runs the sync)
                      // package-level BuiltinFS fs.FS — fs.Sub strips the "embedded/" prefix
  embedded/           // committed mirror of docs/reference, docs/internals, docs/i18n
  source.go           // type DocRoot { Name, FS fs.FS, ProjectPath string }
                      // Sources() returns []DocRoot — devbox (always), project (if ./docs exists)
  tree.go             // BuildTree(root DocRoot) (*Node, error) — directories + files
  lang.go             // ResolveContent(root DocRoot, relPath, locale string)
                      //   → (content []byte, sourceLang string, stale bool, err error)
                      // calls i18n.ResolveLocale upstream once; here only per-file fallback +
                      // content-hash staleness check against ContentHashes manifest
  topic.go            // ParseTopic("config/services#anchor") → (path, anchor, err)
                      // Resolve(roots, topic) — exact match → fuzzy fallback (substring-only)
                      // AllTopics(roots, locale) — flat list for `list`
  content_hashes.go   // ContentHashFor(relPath) — accessor; "" means staleness check disabled
  content_hashes_gen.go // generated: var ContentHashes = map[string]string{...}
                        // sha256(file bytes)[:12] per docs/reference + docs/internals file

internal/docs/render/
  glamour.go               // Render(content []byte, opts RenderOpts) (RenderResult, error)
                           // RenderOpts { Theme, Width, MermaidRenderer mermaid.Renderer, CanInline bool }
                           // RenderResult { Output []byte; Diagrams []DiagramRef }
  mermaid_preprocess.go    // Text-level scanner replaces ```mermaid fenced blocks with placeholder
                           // text BEFORE glamour. Captures source into Diagrams side-channel.
                           // NO goldmark direct import — glamour exposes no Extender hook.

internal/docs/mermaid/
  renderer.go         // type Renderer interface {
                      //   Render(ctx context.Context, src string, theme Theme, width int) ([]byte, error)
                      // }
                      // Chain(renderers...) — first non-(nil, NotAvailable) wins
                      // Disabled{} → ErrRenderingDisabled
  cache.go            // FileCache: XDG_CACHE_HOME/devbox/mermaid/<key>.png
                      // Key: sha256(src + theme + width + mmdcVersion)[:32]
                      // singleflight.Group dedups concurrent same-key misses
                      // LRU by mtime (refreshed on cache hit via os.Chtimes); cap from docs.cache_size_mb
                      // Atomic write: write-temp + os.Rename
  mmdc.go             // MmdcRenderer{ Bin, Version func() string } — Version wrapped in sync.OnceValue
                      // 10s per-render timeout via exec.CommandContext
                      // ErrMmdcNotAvailable on exec.ErrNotFound
  mmdc_unix.go        // //go:build unix — sets SysProcAttr.Setpgid; on ctx timeout
                      // syscall.Kill(-pgid, SIGKILL) reaps chrome subprocess
  mmdc_windows.go     // //go:build windows — relies on CommandContext default kill
  term.go             // Capability detection via rasterm + env (best-effort, false-positive/negative
                      // tolerated; user falls back to `o` for system viewer).
                      // CanInline() bool; OpenSystem(path string) error
                      //   (open/xdg-open/cmd /c start ""  — empty title arg mandatory on Windows)

internal/docs/tui/
  model.go            // bubbletea Model: tree, viewport, status bar, focus
  view.go             // composes tree (left) + viewport (right) + status (bottom)
  keys.go             // KeyMap with all bindings (table in Overview)
  tree_widget.go      // collapsible tree, two top-level branches (Devbox, Project?)
  search.go           // fuzzy index over heading lines; n/N navigation
  diagram.go          // ]d/[d navigation; o (system viewer); y (OSC 52); inline popup overlay
  watcher.go          // fsnotify wrapper for the project DocRoot only (embed is static)
  osc52.go            // emit "\x1b]52;c;<base64>\x07"; tmux-without-passthrough hint

internal/docs/export/
  export.go           // ExportTree(dst string, roots []DocRoot, opts ExportOpts) error
                      // ExportOpts { Lang, IncludeProject, IncludeInternals, Force }

internal/command/
  docs.go             // existing; add subcommands: show, list, export, cache (with `clear` child),
                      // and a RunE on the docs parent itself (TTY → TUI; non-TTY → hint error)
  docs_show.go        // new sub
  docs_list.go        // new sub
  docs_export.go      // new sub
  docs_cache.go       // new sub (cache clear)
```

### Config schema additions

```go
// internal/config/devbox.go
type BinariesConfig struct {
    Devbox string `yaml:"devbox"`
    Docker string `yaml:"docker"`
    Shell  string `yaml:"shell"`
    Git    string `yaml:"git"`
    Mmdc   string `yaml:"mmdc"`            // new
}

// new top-level block on DevboxConfig
type DocsConfig struct {
    Mermaid     string `yaml:"mermaid"`         // "auto" | "mmdc" | "off"
    CacheSizeMB int    `yaml:"cache_size_mb"`   // default 100
}

// new accessor (mirrors DockerBin etc.)
func MmdcBin(cfg *DevboxConfig) string {
    if cfg == nil || cfg.Binaries.Mmdc == "" {
        return "mmdc"
    }
    return cfg.Binaries.Mmdc
}
```

Defaults applied at load time (`Mermaid == "" → "auto"`, `CacheSizeMB == 0 → 100`). Validation: `Mermaid` is one of `{"", "auto", "mmdc", "off"}`; anything else is a load error from the main devbox.yml validator (`internal/validate/config/devbox.go`).

### Topic resolution

- Input `config/services` → search across roots: `docs/reference/config/services.md` in the devbox root, `./docs/config/services.md` in the project root if present.
- Exact match wins. On miss, fuzzy match against the flat topic list (`AllTopics`). Fuzzy: case-insensitive substring on the joined path. On exactly one match → use it. On multiple → print candidates to stderr, exit 1. On none → "topic not found" + suggestions if any.
- `#anchor` is parsed off and threaded to the renderer for jump-to behavior in show/TUI.

### Language fallback per file

```
ResolveContent(root, relPath, locale) → (content, sourceLang, stale, err)

1. if locale == "en" → read root.FS at relPath; sourceLang="en"; stale=false
2. else read root.FS at "i18n/<locale>/" + relPath
   - hit: parse content-hash header from the first ">Translated from: ... @ <hash>"
          compare with the content hash of the en-file at the EMBEDDED snapshot
          (looked up via the build-time generated manifest, NOT a runtime git call)
          → return (content, locale, hash mismatch)
   - miss: read en-file at relPath → return (content, "en", false)
                                     (banner emitted by the renderer/UI)
3. read errors propagate
```

**Hash choice: content hash, not git commit SHA.** Earlier drafts of this plan used `git log -1 --pretty=%h -- <file>` for the manifest. That has a chicken-and-egg problem: in a PR that touches an en-file AND its translation in one commit, `git log` returns the *previous* commit (the file is modified-not-yet-committed during generation). New files are worse — untracked → skipped → no manifest entry. Solution: the manifest value is `sha256(file bytes)[:12]` (12-char hex prefix). Translators paste the same hash into their `> Translated from: ... @ <hash>` header. Stable across rebases, cherry-picks, file renames, and works on fresh checkouts with no `.git/` directory. The build-time generator walks `docs/reference/` and `docs/internals/` and computes the hash; no git calls anywhere in the runtime.

If no manifest is present (e.g. tests build without `go generate`), the staleness check returns `false` (no banner) — translations are shown as-is.

### Mermaid renderer chain

```go
type Renderer interface {
    // Render returns PNG bytes or ErrMmdcNotAvailable / context.DeadlineExceeded / other error.
    // Width is part of the cache key (see Task 5) so callers vary it per render.
    Render(ctx context.Context, src string, theme Theme, width int) ([]byte, error)
}

// Composition in internal/docs/mermaid:
//   chain := Chain(NewFileCache(cacheDir, capBytes), NewMmdc(bin), Disabled{})
//   if cfg.Docs.Mermaid == "off" → chain := Disabled{}
//
// Cache wraps the underlying renderer:
//   FileCache.Render returns cached bytes if present, else delegates and stores.
```

`Disabled{}` always returns `ErrRenderingDisabled` → the text-level preprocessor emits a `[diagrams disabled]` placeholder.

mmdc invocation (Unix path — see Task 5 for the build-tagged Windows variant):
```go
// mmdc_unix.go (//go:build unix)
cmd := exec.CommandContext(ctx, bin, "-i", inPath, "-o", outPath,
    "-b", "transparent", "-t", themeArg, "--width", strconv.Itoa(width), "--quiet")
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
// on ctx timeout: syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) to reap chrome subprocesses
```

Cache safety:
- Cache root: `os.UserCacheDir()` + `/devbox/mermaid/`. Falls back to `os.TempDir()` if unavailable.
- Per-entry: write `tmp.<random>.png` then `os.Rename` to `<key>.png`. Atomic.
- Concurrent same-key misses deduplicated via `golang.org/x/sync/singleflight` (eliminates duplicate mmdc invocations + Windows rename-races).
- LRU eviction: walk dir, sort by **mtime** (atime is unreliable across filesystems — noatime/relatime mounts), delete oldest until under cap. `os.Chtimes(path, now, now)` is called on every cache-read to refresh mtime as the "recently used" signal.
- Cache clear: `os.RemoveAll(cacheDir)` and recreate.

### Inline diagrams vs system viewer

`internal/docs/mermaid/term.go`:

```go
func CanInline() bool {
    if os.Getenv("TMUX") != "" { return false }   // tmux passthrough is too unreliable
    if rasterm.IsKittyCapable() { return true }   // kitty, ghostty, wezterm
    return false                                  // everything else
}

func OpenSystem(path string) error { … }          // open/xdg-open/start
```

In TUI: `Enter` on a focused diagram → if `CanInline()` show a popup overlay with the PNG, else call `OpenSystem`. `o` always `OpenSystem`. `y` always OSC 52 of the source.

### `devbox docs` parent command behavior

```go
// internal/command/docs.go
parent.RunE = func(cmd *cobra.Command, args []string) error {
    if !isInteractive(cmd.OutOrStdout()) {
        return errors.New("devbox docs without arguments requires a TTY; use 'devbox docs show <topic>' or 'devbox docs list' for non-interactive use")
    }
    return runDocsTUI(cmd, rflags)
}
```

`isInteractive` uses `term.IsTerminal(int(os.Stdout.Fd()))` (same as elsewhere in the codebase).

`devbox docs show` rendering:
- TTY → glamour render with current theme (via `lipgloss.HasDarkBackground()`); mermaid blocks rendered inline if `CanInline()`, otherwise printed with a path/hint and the raw block preserved.
- non-TTY → raw markdown emitted via stdout, mermaid blocks left untouched. **No ANSI, no extra whitespace.**
- `--raw` flag forces raw even in TTY.

`devbox docs export`:
- Refuses non-empty target dir without `--force`.
- Mermaid blocks preserved as ` ```mermaid ` source (IDE-friendly).
- Per-file fallback: missing translation → en + `> **Note:** This file is not translated to <lang>. Original English version below.` banner.

### i18n integration

All user-visible strings (banners, status-bar hints, help text shown in the TUI) go through `rflags.I18n` with `i18n.TranslatorOrNop` at the boundary. New `ui.docs.*` keys are added to `internal/i18n/translations/en.yml` and recorded in `internal/i18n/known_keys.go` (`KnownUIKeys`). Examples: `ui.docs.banner.translation_missing`, `ui.docs.banner.translation_stale`, `ui.docs.tui.help.search`, `ui.docs.mermaid.rendering`, `ui.docs.mermaid.failed`.

Cobra `Short`/`Long` on the new subcommands themselves stay English (matches the localization plan's explicit scope).

### Lock and preflight

Docs commands are read-only. They do NOT call `lock.AcquireProjectLocks` and do NOT run preflight. `devbox docs` works without a devbox project (no `devbox.yml`) — the project DocRoot is simply omitted from `Sources()` in that case.

## What Goes Where

- **Implementation Steps (`[ ]` checkboxes):** code, tests, embedded fixture files, generated content-hash manifest, documentation under `docs/reference/`.
- **Post-Completion (no checkboxes):** manual verification of TUI on kitty/ghostty/wezterm/iTerm2/tmux; verifying that a fixture project with `./docs/` populates the Project branch; verifying mermaid render in a terminal that supports kitty graphics.

## Implementation Steps

### Task 1: Config plumbing — `binaries.mmdc`, `docs.mermaid`, `docs.cache_size_mb`

**Files:**
- Modify: `internal/config/devbox.go`
- Modify: `internal/validate/config/devbox.go`
- Modify: `docs/reference/config/devbox.md`
- Modify: `internal/config/devbox_test.go`
- Modify: `internal/validate/config/devbox_test.go`

- [x] add `Mmdc string \`yaml:"mmdc"\`` to `BinariesConfig` (line 26)
- [x] add `MmdcBin(cfg *DevboxConfig) string` accessor returning `"mmdc"` when nil/empty
- [x] add top-level `Docs DocsConfig \`yaml:"docs"\`` field on `DevboxConfig`; define `DocsConfig { Mermaid string; CacheSizeMB int \`yaml:"cache_size_mb"\` }`
- [x] apply defaults at load time: `Mermaid == "" → "auto"`, `CacheSizeMB == 0 → 100`
- [x] in `allowedFieldsFor` (devbox.go:607) add `docs` to the top-level allowed set; add `mmdc` to the `binaries` allowed nested set (lenient loader, no explicit validation needed; validated in devboxValidator instead)
- [x] in `internal/validate/config/devbox.go` add `docs` to the strict allowed-keys list; validate `docs.mermaid ∈ {"auto","mmdc","off"}` and `docs.cache_size_mb >= 0`
- [x] update `docs/reference/config/devbox.md` with the new `docs:` block and `binaries.mmdc` entry
- [x] write tests: parses both fields; defaults applied; rejects `mermaid: bogus`; rejects negative cache size
- [x] run `go test ./internal/config/... ./internal/validate/config/...` — must pass

### Task 2: `internal/docs` skeleton — embed wiring, source, tree

**Files:**
- Create: `internal/docs/embed.go`
- Create: `internal/docs/embedded/` (synced tree; committed — populated by the sync script on first run)
- Create: `internal/docs/source.go`
- Create: `internal/docs/tree.go`
- Create: `internal/docs/embed_test.go`
- Create: `internal/docs/source_test.go`
- Create: `internal/docs/tree_test.go`
- Create: `scripts/sync-embedded-docs.sh` (called from `make embedded-docs` and `go generate`; idempotent rsync-equivalent)
- Modify: `Makefile` (add `embedded-docs` target; `build` depends on it)

- [x] `scripts/sync-embedded-docs.sh`: mirrors `docs/reference docs/internals docs/i18n` → `internal/docs/embedded/{reference,internals,i18n}`. Uses `rsync -a --delete` if available else `rm -rf` of subdirs + `cp -R`. Idempotent.
- [x] Makefile target `embedded-docs` runs the script; `build` target depends on it
- [x] **`internal/docs/embed.go` carries a `//go:generate` directive** that runs the same sync script, so `go generate ./internal/docs/...` works as an alternative to `make build` (covers IDE workflows, `go install`, third-party tooling). Document this in `docs/internals/packages.md` (Task 16)
- [x] **the synced tree is committed.** Fresh-checkout gets a real (non-empty) embed and `go build ./cmd/devbox` / `go install` / IDE builds / release tarballs all ship the docs. Trade-off: the repo carries a duplicated tree at `docs/` and `internal/docs/embedded/`, and PRs touching docs must include both. Idempotent sync makes the duplication automatic; CI rejects PRs where `make embedded-docs` produces a diff (catches forgotten regeneration)
- [x] CI guard: a new step `make embedded-docs && git diff --exit-code internal/docs/embedded/` runs after the test suite. Non-zero exit = author forgot to regenerate. Surface as a clear "run `make embedded-docs` and commit" message
- [x] `internal/docs/embed.go`: `//go:embed embedded` exposing `BuiltinFS fs.FS` (package-level var); strips the `embedded/` prefix via `fs.Sub` so callers see `reference/...`, `internals/...`, `i18n/...` at the root
- [x] startup-time sanity: if `BuiltinFS` is effectively empty (no `reference/` entry), `Sources()` logs a debug-level slog with the hint `"embedded docs are empty — run 'make embedded-docs' (or 'make build') to populate"`. Should never happen for a properly-built binary, but catches misconfiguration in tests/dev
- [x] define `DocRoot { Name string; FS fs.FS; ProjectPath string }`; `Sources(projectRoot string) []DocRoot` returns devbox root always + project root if `<projectRoot>/docs/` exists and is readable
- [x] define `Node { Name, Path string; Children []*Node; IsDir bool }` and `BuildTree(root DocRoot) (*Node, error)` walking `root.FS`
- [x] tree omits files with non-`.md` extension; sorts children stably (directories before files, alphabetical)
- [x] tests use a small `testdata/embedded_fixture/` tree for shape/sort/filter assertions independent of the real docs (so updates to actual reference docs don't break unit tests). A separate smoke test verifies `BuiltinFS` contains at least `reference/config/devbox.md` — this passes on a fresh checkout because the embed tree is committed
- [x] run `make build && go test ./internal/docs/...` — must pass

### Task 3a: Topic parsing and resolution

**Files:**
- Create: `internal/docs/topic.go`
- Create: `internal/docs/topic_test.go`

- [x] `ParseTopic(input string) (path, anchor string, err error)` — splits `<path>#<anchor>`; trims `.md` if user wrote it; rejects empty path
- [x] `Resolve(roots []DocRoot, topic string, locale string) (*ResolvedTopic, error)` — exact match first across roots in declared order; on miss collect case-insensitive substring matches across all topics; if exactly one → return it; if multiple → return `MultipleMatchesError` with the list; if none → return `NotFoundError` with the same substring candidates (no second algorithm — substring-only, per YAGNI)
- [x] `AllTopics(roots []DocRoot, locale string) []TopicEntry` — flat list with `{Path, DisplayName, Lang, Source}` per file
- [x] tests: `ParseTopic` covers `config/services`, `config/services#anchor`, `config/services.md#anchor`, empty input, trailing slashes; `Resolve` covers exact, fuzzy-single, fuzzy-multi (returns sorted candidates), fuzzy-none; `AllTopics` walks both built-in and project roots; deterministic order
- [x] run `go test ./internal/docs/...` — must pass before Task 3b

### Task 3b: Language fallback + content-hash manifest loader

**Files:**
- Create: `internal/docs/lang.go`
- Create: `internal/docs/content_hashes.go` (handwritten loader; consumes generated data)
- Create: `internal/docs/content_hashes_gen.go` (committed, regenerated by `make build` — initial version is `var ContentHashes = map[string]string{}`)
- Create: `internal/docs/lang_test.go`
- Create: `internal/docs/content_hashes_test.go`

- [ ] `content_hashes.go` provides `ContentHashFor(relPath string) string` returning `ContentHashes[relPath]` or `""` when absent. Document that `""` means "manifest empty or file missing → staleness check disabled for this file"
- [ ] `content_hashes_gen.go` initial commit: `var ContentHashes = map[string]string{}` (empty map). Generator script (Task 13) overwrites this file in place; the file is committed so fresh checkouts compile
- [ ] `ResolveContent(root DocRoot, relPath, locale string) (content []byte, sourceLang string, stale bool, err error)` per the algorithm in Technical Details. On `locale != "en"`: read `i18n/<locale>/` + relPath; if present, parse the content-hash header (first line matching `^>\s*Translated from:\s*\S+\s*@\s*([0-9a-f]{12,64})\s*$`); compare with `ContentHashFor(relPath)`. If manifest entry is `""` → `stale = false` (no banner)
- [ ] `ContentHashFor(relPath string) string` returns `ContentHashes[relPath]` (12-char `sha256` prefix) or `""` when absent. **Hash is content-based, NOT git-based** — see Technical Details "Hash choice" for rationale
- [ ] tests: `ResolveContent` covers locale=en, locale=ru with translation matching content-hash, locale=ru with translation mismatching content-hash (stale=true), locale=ru without translation (en fallback, sourceLang="en"), locale=ru with translation but empty manifest entry (stale=false), malformed content-hash header (treated as missing → stale=false)
- [ ] run `go test ./internal/docs/...` — must pass before Task 4

### Task 4: Glamour render + mermaid text-level preprocess

**Files:**
- Create: `internal/docs/render/glamour.go`
- Create: `internal/docs/render/mermaid_preprocess.go`
- Create: `internal/docs/render/glamour_test.go`
- Create: `internal/docs/render/mermaid_preprocess_test.go`
- Create: `internal/docs/render/testdata/...` (golden inputs/outputs)
- Modify: `go.mod` (add glamour)

- [ ] `RenderOpts { Theme string; Width int; MermaidRenderer mermaid.Renderer; CanInline bool }`. When `Width <= 0`, default to `100` (handles both non-TTY and unknown TTY width)
- [ ] `Render(content []byte, opts RenderOpts) (RenderResult, error)` where `RenderResult { Output []byte; Diagrams []DiagramRef }`. **Text-level preprocess** the markdown bytes to replace ` ```mermaid\n...\n``` ` fenced blocks with the appropriate placeholder text BEFORE passing to glamour. Reason: as of glamour v0.x / v2.x, there is no exported hook to register a `goldmark.Extender` — the renderer is constructed internally and only `TermRendererOption`s like `WithStylePath`, `WithWordWrap` are exposed (verified via pkg.go.dev). Spinning up a parallel goldmark parser to mutate the AST would require running goldmark twice — rejected. Text-level preprocess is the primary design, not a fallback
- [ ] preprocess function: line-based scanner that tracks fence state. On entering a ` ```mermaid ` line, capture lines until the closing ` ``` `, replace the whole block with one line of placeholder text. Edge cases: nested fences (rare in markdown but possible inside HTML examples — track outer fence info string only); blocks at EOF without a closing fence (treat the whole remainder as the mermaid source and emit a placeholder + warning)
- [ ] then construct glamour via `glamour.NewTermRenderer(glamour.WithStylePath(themeName(opts.Theme)), glamour.WithWordWrap(opts.Width))` and render the preprocessed bytes
- [ ] mermaid source captured during preprocess is returned in `RenderResult.Diagrams` so the TUI can overlay images, the prefetch worker can queue renders, and `y` (OSC 52) can copy the source. `DiagramRef { LineInRendered int; Source string; Index int }`. All call sites (Task 6 `show`, Tasks 9–11 TUI) consume `result.Output` for the textual render and `result.Diagrams` for the side-channel
<!-- replaced by text-level preprocess above; no goldmark.Extender used -->
- [ ] placeholder text variants (determined synchronously at preprocess time — the actual mmdc invocation is asynchronous via prefetch): `mermaid.Renderer == nil` (off) → `<📊 [diagrams disabled]>`; `MermaidRenderer` known-unavailable (cached `ErrMmdcNotAvailable`) → `<📊 [mmdc not installed — Y to copy]>`; otherwise → `<📊 [view schema]>` (TUI overlays the actual image when prefetch completes). For `show` outside the TUI, render synchronously inline and use `<📊 [render failed: Y to copy]>` on timeout/error
- [ ] raw markdown path (used by `docs show --raw` and non-TTY): caller does not invoke `Render` at all — emits the input bytes verbatim
- [ ] tests: golden test for a markdown sample with headings, code, list, mermaid block → stable bytes for width=100, dark theme; preprocess test verifies the placeholder for each variant; nested-fence edge case test (mermaid inside a quoted markdown example must NOT be replaced — only outermost fences); unclosed fence at EOF test
- [ ] run `go test ./internal/docs/render/...` — must pass

### Task 5: Mermaid disk cache + mmdc renderer + capability detection

**Files:**
- Create: `internal/docs/mermaid/renderer.go`
- Create: `internal/docs/mermaid/cache.go`
- Create: `internal/docs/mermaid/mmdc.go`
- Create: `internal/docs/mermaid/term.go`
- Create: `internal/docs/mermaid/cache_test.go`
- Create: `internal/docs/mermaid/mmdc_test.go`
- Create: `internal/docs/mermaid/term_test.go`
- Create: `internal/docs/mermaid/testdata/fake-mmdc.sh`
- Modify: `go.mod` (add rasterm)

- [ ] `Renderer` interface: `Render(ctx context.Context, src string, theme Theme, width int) ([]byte, error)`. **Width is a first-class parameter** because it's part of the cache key — without it in the signature, callers could not vary width per render and the cache would either over-cache or alias keys. Theme is its own enum (`Dark`/`Light`)
- [ ] `Chain(...Renderer)`; `Disabled{}` returning `ErrRenderingDisabled`
- [ ] `FileCache{ Dir string; CapBytes int64; Version func() string; sf singleflight.Group; mu sync.Mutex }` with `Render(ctx, src, theme, width)`:
  - compute `key = sha256(src + "|" + string(theme) + "|" + strconv.Itoa(width) + "|" + cache.Version())[:32]` where `Version` is wrapped in `sync.OnceValue` (Go 1.21+) at construction time so it runs at most once per `FileCache` instance and is race-safe by construction
  - if `<Dir>/<key>.png` exists → read and return; on read, `os.Chtimes(path, now, now)` to refresh mtime (the LRU eviction key — atime is too unreliable across filesystems)
  - else use `sf.Do(key, func() (any, error) { ... })` (from `golang.org/x/sync/singleflight`) so concurrent same-key misses share one render — eliminates duplicate mmdc invocations and Windows-unfriendly `os.Rename` races on already-existing destinations
  - inside the singleflight closure: delegate to wrapped renderer; on success atomically write (`tmp.<rand>.png` → `os.Rename`)
  - LRU eviction guarded by `mu`: even with singleflight, two DIFFERENT keys can write concurrently and both trigger eviction. The eviction critical section is: walk dir, sum sizes, if over cap → sort by mtime, delete oldest until under cap. Hold `mu` only across the eviction walk+delete (not across the upstream render or the cache hit read)
- [ ] `MmdcRenderer{ Bin string; Version func() string }` — `Version` defaults to `sync.OnceValue(func() string { return probeMmdcVersion(bin) })` (returns `"unknown"` on error). Tests inject a deterministic `Version` to control cache keys
- [ ] `mermaid.New(bin string, cacheDir string, capBytes int64) Renderer` helper: constructs `MmdcRenderer{Bin: bin}`, then `FileCache{Dir: cacheDir, CapBytes: capBytes, Version: mmdc.Version}` wrapping it via `Chain`. Single entry point used by the command layer
- [ ] invocation per Technical Details: `exec.CommandContext(ctx, bin, args...)`. **Setpgid + process-group kill is Unix-only** — `syscall.SysProcAttr{Setpgid: true}` and `syscall.Kill(-pgid, SIGKILL)` don't exist on Windows. Split into build-tagged files:
  - `mmdc_unix.go` (`//go:build unix`): sets `Setpgid: true` before `cmd.Start()`; on `ctx.Err() != nil` calls `syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)` to reap mmdc's chrome child
  - `mmdc_windows.go` (`//go:build windows`): no `Setpgid`; relies on `exec.CommandContext`'s default kill behavior. Windows users get a (rare) leftover chrome process on timeout — acceptable trade-off; documented in package comment
  - After `cmd.Wait()` returns, the goroutine exits — no leak on either platform
- [ ] on `errors.Is(err, exec.ErrNotFound)` → return `ErrMmdcNotAvailable`
- [ ] `term.CanInline() bool` — false when `os.Getenv("TMUX") != ""`; true when `rasterm.IsKittyCapable()`; else false. **Treat this as a best-effort hint, not a guarantee** — env-based detection can produce false positives (wezterm reports kitty-compat but image sizes may render imperfectly) and false negatives (newer terminals). If a render fails or looks wrong, the user falls back to `o` (system viewer). Document the limitation in package comment
- [ ] `term.OpenSystem(path string) error`:
  - darwin → `exec.Command("open", path)`
  - linux/bsd → `exec.Command("xdg-open", path)`
  - windows → `exec.Command("cmd", "/c", "start", "", path)` — the empty title argument `""` is **mandatory** to handle paths with spaces correctly; do NOT omit it
  - quote-safety: `exec.Command` already quotes args; no manual quoting
- [ ] `mermaid.CacheDir()` resolves `$XDG_CACHE_HOME/devbox/mermaid/` else `os.UserCacheDir() + /devbox/mermaid/` else `os.TempDir() + /devbox-mermaid/`
- [ ] tests using a fixture `fake-mmdc.sh` (committed under `internal/docs/mermaid/testdata/`) that copies a fixed PNG to the output path (no real mmdc dependency): cache miss → invokes fake → caches; cache hit → no second invocation (assert via call counter on injected `Version`); LRU eviction with `os.Chtimes` to fake mtime; mmdc timeout simulated via fake script sleeping past 10s with a small test-only timeout override
- [ ] tests for `OpenSystem` use a test seam: inject the `exec.Command` constructor so the test asserts the argv shape (`cmd /c start "" <path>` on windows) without actually shelling out
- [ ] tests for `CanInline` are env-driven (set/unset `TMUX`, set/unset `KITTY_WINDOW_ID`)
- [ ] run `go test -race ./internal/docs/mermaid/...` — must pass (`-race` catches `sync.Once` regressions)

### Task 6: `devbox docs show` and `devbox docs list`

**Files:**
- Modify: `internal/command/docs.go` (register new sub commands)
- Create: `internal/command/docs_show.go`
- Create: `internal/command/docs_list.go`
- Create: `internal/command/docs_show_test.go`
- Create: `internal/command/docs_list_test.go`

- [ ] `devbox docs show <topic>` — `Args: cobra.ExactArgs(1)` (no inline `len(args)` checks in `RunE`). Flags: `--lang <code>`, `--raw`, `--source devbox|project|all` (default **`all`** — agents and humans both usually want the full set; reduces surprise)
- [ ] `ValidArgsFunction` on `show`: dynamic completion of topic names via `docs.AllTopics(...)`. Per the CLAUDE.md completion-path-safety pattern, wrap any i18n calls with `i18n.TranslatorOrNop(rflags.I18n)` because `__complete` bypasses `PersistentPreRunE`. On any error → return `(nil, cobra.ShellCompDirectiveNoFileComp)` silently
- [ ] use `cmd.OutOrStdout()` / `cmd.ErrOrStderr()` for ALL output — never write to `os.Stdout` / `os.Stderr` directly. Tests rely on this to redirect to `bytes.Buffer`
- [ ] resolution flow: **always call `i18n.ResolveLocale(df.lang, cfg.Language, os.Getenv("LANG"))` directly** — do NOT use `rflags.Locale` because it is clamped to the YAML translation store (which is a different namespace from long-form markdown). `Sources(projectRoot)` → filter by `--source`; `docs.Resolve(roots, topic, locale)` → `ResolveContent` → render. Per-file fallback inside `ResolveContent` handles missing markdown translations
- [ ] if `!isInteractive(stdout) || df.raw` → write raw bytes (preserve the optional banner for missing/stale translations as plain markdown blockquote). **No ANSI escapes in this path.** No glamour invocation at all
- [ ] if TTY and not `--raw` → `render.Render(content, RenderOpts{Theme: themeFromBg(), Width: termWidthOr(100), MermaidRenderer: mermaid.Chain(cache, mmdc), CanInline: term.CanInline()})`. `termWidthOr` returns 100 when width cannot be detected
- [ ] `devbox docs list` — `Args: cobra.NoArgs`. Flags: `--lang <code>`, `--source devbox|project|all` (default `all`)
  - emits one line per topic to `cmd.OutOrStdout()`: `<source>\t<path>\t<lang>` (tab-separated, agent-friendly)
- [ ] resolution errors map to user-friendly messages: `NotFoundError` → "topic 'X' not found" + suggestions; `MultipleMatchesError` → "ambiguous; candidates: ..." to stderr, exit 1
- [ ] all user-visible strings go through `i18n.TranslatorOrNop(rflags.I18n)` for `ui.docs.*` keys (add to `en.yml` + `KnownUIKeys` in this task)
- [ ] tests: fixture project with built-in + project doc + ru translation; show exact / fuzzy / missing; list devbox-only / project-only / all; raw vs TTY (using a `bytes.Buffer` as stdout — non-TTY path)
- [ ] run `go test ./internal/command/... ./internal/docs/...` — must pass

### Task 7: `devbox docs cache clear`

**Files:**
- Create: `internal/command/docs_cache.go`
- Create: `internal/command/docs_cache_test.go`

- [ ] `devbox docs cache` parent (`Args: cobra.NoArgs`, RunE prints help via `cmd.Help()`) + `clear` child subcommand (`Args: cobra.NoArgs`)
- [ ] `clear`: resolves cache dir via `mermaid.CacheDir()` (XDG-aware), `os.RemoveAll`, then recreate the dir with `0o700`
- [ ] reports count of removed entries via `cmd.OutOrStdout()`: "removed N cached diagrams"
- [ ] gracefully reports "no cache to clear" when the dir does not exist
- [ ] **no `PersistentPreRunE` on the `docs cache` parent** — relies on the root's, which is sufficient. Adding one would silently replace the root's project resolution per the CLAUDE.md "Cobra does NOT chain `PersistentPreRunE`" note
- [ ] tests: clearing a populated tempdir leaves it empty; clearing a missing dir reports the no-op message; permission error surfaces cleanly
- [ ] run `go test ./internal/command/...` — must pass

### Task 8: `devbox docs export`

**Files:**
- Create: `internal/docs/export/export.go`
- Create: `internal/command/docs_export.go`
- Create: `internal/docs/export/export_test.go`
- Create: `internal/command/docs_export_test.go`

- [ ] `ExportOpts { Lang string; IncludeProject, IncludeInternals, Force bool }`
- [ ] `ExportTree(dst string, roots []DocRoot, opts ExportOpts) error`:
  - if `dst` exists and is non-empty and `!Force` → error
  - mkdir -p `dst`
  - walk roots; for each markdown file: `ResolveContent(root, relPath, opts.Lang)` → write to `<dst>/<root.Name>/<relPath>` (or `<dst>/reference/...`, `<dst>/internals/...`, `<dst>/project/...` per Overview)
  - mermaid blocks left as ` ```mermaid `
  - when fallback to en happened, prepend `> **Note:** This file is not translated to '<lang>'. Original English version below.\n\n` to the file content
- [ ] cobra wiring: `devbox docs export <dir>` — `Args: cobra.ExactArgs(1)`. Flags: `--lang <code>`, `--include-project`, `--include-internals`, `--force`. Output via `cmd.OutOrStdout()` / `cmd.ErrOrStderr()`
- [ ] without flags: only `reference/` exported; internals excluded by default; project excluded by default
- [ ] tests: empty dir → ok; non-empty dir without `--force` → error; with `--force` → overwrites; project source toggled; internals toggled; missing-translation banner appears
- [ ] run `go test ./internal/docs/export/... ./internal/command/...` — must pass

### Task 9: TUI model + tree widget + viewport + status bar (no diagrams, no search yet)

**Files:**
- Create: `internal/docs/tui/model.go`
- Create: `internal/docs/tui/view.go`
- Create: `internal/docs/tui/keys.go`
- Create: `internal/docs/tui/tree_widget.go`
- Create: `internal/docs/tui/model_test.go`
- Create: `internal/docs/tui/tree_widget_test.go`

- [ ] `Model{ Tree, Viewport, Status, FocusZone, CurrentTopic, Roots []DocRoot, Locale, Lang, Renderer }` — bubbletea/v2 Model
- [ ] `Init() tea.Cmd`, `Update(msg tea.Msg) (tea.Model, tea.Cmd)`, `View() string`
- [ ] left pane: collapsible tree with two top-level branches (`Devbox`, `Project (./docs)` when present); `j/k` navigation, `h/l` collapse/expand, `Enter` open file
- [ ] right pane: viewport with glamour-rendered content of the current file
- [ ] bottom status bar: `<relPath>  📊 <rendered>/<total>  [<lang>]  ?:help  q:quit`
- [ ] `Tab` switches focus between tree and viewport (viewport gains `j/k` scroll when focused)
- [ ] `g`/`G` jump start/end; `q`/`Ctrl+C` quit
- [ ] theme auto-detect via `lipgloss.HasDarkBackground()`
- [ ] follow the nine `liveui` invariants (no `tea.NewProgram`, no `term.MakeRaw`, etc.)
- [ ] tests: tree widget collapse/expand state machine; key handler dispatch; `View()` produces non-empty string for a known model; opening a topic populates the viewport
- [ ] run `go test ./internal/docs/tui/...` — must pass

### Task 10: TUI search (titles), diagram navigation, OSC 52 clipboard

**Files:**
- Create: `internal/docs/tui/search.go`
- Create: `internal/docs/tui/diagram.go`
- Create: `internal/docs/tui/osc52.go`
- Create: `internal/docs/tui/search_test.go`
- Create: `internal/docs/tui/diagram_test.go`
- Create: `internal/docs/tui/osc52_test.go`
- Modify: `internal/docs/tui/model.go` (wire new components)
- Modify: `internal/docs/tui/keys.go` (register new bindings)

- [ ] search index over markdown headings (`^#+\s+`) across the visible roots; built lazily on TUI start; `/` opens prompt, `n`/`N` cycle matches
- [ ] diagram tracking: per-file list of mermaid block positions; `]d`/`[d` cycles; current diagram is highlighted (invert colors on the placeholder line)
- [ ] `Enter` on a focused diagram → if `term.CanInline()` show popup overlay (a transient bubbletea overlay rendering the PNG via rasterm), else `term.OpenSystem(path)`
- [ ] `o` always `OpenSystem`
- [ ] `y` always OSC 52 copy of the diagram source: `fmt.Fprintf(out, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(src)))`
- [ ] tmux hint: if `$TMUX != ""` and the user hits `y` for the first time in the session, print a one-time hint to the status bar reminding to enable `set -g set-clipboard on` in `.tmux.conf` for OSC 52 to work. **Do not shell out to `tmux show-option`** — blocking calls in the bubbletea update loop violate the liveui invariants. Show the hint unconditionally under tmux; users with the option already enabled will dismiss it once
- [ ] tests: search index extraction, n/N cycling; diagram navigation order; OSC 52 byte sequence (capture via `bytes.Buffer`); tmux-hint logic (env-driven)
- [ ] run `go test ./internal/docs/tui/...` — must pass

### Task 11: TUI fsnotify hot reload + language cycling + outdated translation banner

**Files:**
- Create: `internal/docs/tui/watcher.go`
- Create: `internal/docs/tui/watcher_test.go`
- Modify: `internal/docs/tui/model.go` (wire watcher, banner, language cycle)
- Modify: `internal/docs/tui/keys.go` (`L`, `e`, `r`)
- Modify: `go.mod` (add fsnotify)

- [ ] `Watcher` wrapping `fsnotify.Watcher`; watches the project DocRoot tree only (embed is static and ignored); emits `FileChangedMsg` into the bubbletea event loop via a `chan<-` returned to the model
- [ ] **explicit lifecycle**: `NewWatcher(ctx context.Context, root string) (*Watcher, error)`. The internal event-pump goroutine selects on `fsnotify.Watcher.Events`, `fsnotify.Watcher.Errors`, AND `ctx.Done()`. On context cancellation the goroutine returns; the model calls `cancel()` on `tea.Quit` (the cancel func is stored on the model). No fire-and-forget — every goroutine has a clear exit
- [ ] `Watcher.Close()` is a wrapper around `cancel()` + `fsnotify.Watcher.Close()`; idempotent
- [ ] on `FileChangedMsg` for the current file → re-read and re-render
- [ ] `L` cycles through available locales for the current file (computed via `topic.AvailableLocalesFor(roots, relPath)`); persists choice for the session only (no userconfig write)
- [ ] `e` shows English original when the current file is a translation (`sourceLang != "en"`)
- [ ] `r` manual reload of the current file
- [ ] outdated translation banner: when `ResolveContent` reports `stale=true`, prepend a banner line to the rendered viewport: "⚠ This translation is outdated (last synced at X, current is Y). Press `e` to view the English version."
- [ ] missing translation banner: when `sourceLang != requestedLocale`, prepend "ℹ Translation not available for `<lang>`. Showing English version."
- [ ] tests: file write in tempdir triggers reload; language cycle visits each available locale; banner text matches for stale and missing cases; **`goleak.VerifyNone(t)` in a `TestMain`** confirms the watcher goroutine exits when its context is cancelled (catches the most likely leak in this task)
- [ ] run `go test ./internal/docs/tui/...` — must pass

### Task 11.5: TUI mermaid prefetch worker pool

**Files:**
- Create: `internal/docs/tui/prefetch.go`
- Create: `internal/docs/tui/prefetch_test.go`
- Modify: `internal/docs/tui/model.go` (wire prefetch on startup; thread progress into status bar)

- [ ] `Prefetch{ ctx, cancel, renderer mermaid.Renderer, progress chan<- ProgressMsg }` with a bounded worker pool of 2 (cap derived from `runtime.GOMAXPROCS(0)` clamped to `[2, 3]` — never more, mmdc is heavyweight)
- [ ] **per-task error swallowing**: workers are spawned via `g.Go(func() error { _ = renderOne(ctx, work); return nil })` — individual render failures are LOGGED (slog.Debug) but NOT returned from the worker func. Reason: `errgroup.WithContext` cancels the whole group on first non-nil error; one mermaid diagram with bad syntax must NOT cancel siblings or kill the prefetch. Use `g.SetLimit(2)` for the bounded pool (NOT a hand-rolled `chan struct{}` semaphore). The errgroup's context cancellation remains the EXIT mechanism (driven externally by `cancel()` on `tea.Quit`), not an error-propagation mechanism
- [ ] priority queue (typed slice + `sort.Slice` on insert is fine; no need for a heap at this scale): current file's diagrams first, then siblings, then everything else. Single producer (the TUI's "file opened" event), multiple workers consuming
- [ ] producer-consumer channel: producer owns and closes the work channel on `ctx.Done()`. Workers handle BOTH closed-channel AND context cancellation:
  ```go
  for {
      select {
      case work, ok := <-ch:
          if !ok { return nil }  // channel closed → exit (closed chans stay selectable, would spin on zero-value otherwise)
          renderOne(ctx, work)
      case <-ctx.Done():
          return nil
      }
  }
  ```
  The `ok` check is **mandatory** — `case work := <-ch:` without `ok` would spin on the zero value once the producer closes the channel
- [ ] progress reporting: `ProgressMsg{Rendered, Total int}` sends MUST `select` on `ctx.Done()` to avoid blocking after `tea.Quit`:
  ```go
  select { case progress <- ProgressMsg{r, t}: case <-ctx.Done(): return }
  ```
- [ ] skip already-cached entries (cheap `os.Stat` of expected sha-path before queueing)
- [ ] tests with a fake renderer that records call order: confirm priority ordering, bounded concurrency (max 2 simultaneous via a counter incremented in the fake), clean exit on context cancellation, `goleak.VerifyNone(t)` confirms no leaks after the prefetch completes or is cancelled
- [ ] run `go test -race ./internal/docs/tui/...` — must pass

### Task 12: Wire `devbox docs` parent command (TTY → TUI, non-TTY → hint error)

**Files:**
- Modify: `internal/command/docs.go`
- Create: `internal/command/docs_root_test.go`

- [ ] define `runDocsTUI(cmd *cobra.Command, rflags *rootFlags) error` that constructs the bubbletea model and runs it via the project's existing in-process pattern (mirrors `cmdbrowser/run.go`)
- [ ] set `RunE` on the docs parent (`Args: cobra.NoArgs`): non-TTY → return error "devbox docs without arguments requires a TTY; use 'devbox docs show <topic>' or 'devbox docs list' for non-interactive use"; TTY → `runDocsTUI`. Determine TTY via `term.IsTerminal(int(os.Stdout.Fd()))` (the actual fd, not `cmd.OutOrStdout()` which may be a buffer in tests — but the **output writing** still goes through `cmd.OutOrStdout()`)
- [ ] **no `PersistentPreRunE` added to the docs parent or any child** — relies on the root's existing hook for project resolution + i18n setup. Cobra does not chain hooks; adding one here would silently replace the root's (per CLAUDE.md)
- [ ] keep the existing `docs generate` subcommand untouched
- [ ] thread `rflags.Locale`, `rflags.I18n`, `cfg` (loaded by root), `projectRoot`
- [ ] wire the prefetch worker pool from Task 11.5 — pass it the mermaid chain and a buffered progress channel
- [ ] **shutdown ordering on `tea.Quit`**: BEFORE returning `tea.Quit` from the model, call `prefetch.cancel()` AND `watcher.Close()`. This ensures in-flight goroutines see `ctx.Done()` and exit cleanly before bubbletea tears down its event loop. Bubbletea will otherwise drop messages from already-running cmds, which is fine for our progress msgs but matters for the leak detector. Pattern:
  ```go
  case quitMsg:
      m.prefetch.cancel()
      m.watcher.Close()
      return m, tea.Quit
  ```
- [ ] watcher/progress channels are consumed via reissued `tea.Cmd`s (the canonical bubbletea pattern for async sources): each delivered message returns a new `tea.Cmd` that re-reads from the channel. Source goroutines never block sending because the cmds are always pending
- [ ] mermaid chain assembly per `cfg.Docs.Mermaid` value:
  - `"off"` → `mermaid.Disabled{}` (placeholder `<📊 [diagrams disabled]>`)
  - `"auto"` (default) → `mermaid.Chain(mermaid.NewFileCache(...), mermaid.NewMmdc(config.MmdcBin(cfg)))` where `NewMmdc` returns `ErrMmdcNotAvailable` silently on `exec.ErrNotFound` (placeholder becomes `<📊 [mmdc not installed — Y to copy]>`, no log noise)
  - `"mmdc"` → same chain as `auto`, BUT `NewMmdc` in "strict" mode: on `exec.ErrNotFound` returns `ErrMmdcRequired` (placeholder `<📊 [mmdc required but not found]>`, plus one slog.Warn at process start so CI surfaces the misconfiguration)
- [ ] verify `auto` vs `mmdc` divergence has a dedicated test in Task 5 (add a row to the `MmdcRenderer` table-driven test if missing)
- [ ] tests: with a `bytes.Buffer` stdout (non-TTY), `RunE` returns the hint error; TTY path is exercised by the TUI tests in tasks 9–11 (no e2e harness)
- [ ] run `go test ./internal/command/...` — must pass

### Task 13: Content-hash manifest generator wired into `make build`

**Files:**
- Modify: `Makefile` (add `gen-docs-manifest` target; `build` depends on it; also runnable standalone)
- Create: `scripts/gen-docs-content-hashes.sh`
- Modify: `internal/docs/content_hashes_gen.go` (the committed initial stub from Task 3b — regenerated by the script)

- [ ] `gen-docs-manifest` target runs `scripts/gen-docs-content-hashes.sh internal/docs/content_hashes_gen.go`
- [ ] script walks `docs/reference docs/internals` (NOT the gitignored `internal/docs/embedded/`), computes the first 12 hex chars of `sha256(file bytes)` per file (using `sha256sum`/`shasum -a 256` + `cut`, NOT `git`), writes a Go source file with `package docs` and `var ContentHashes = map[string]string{...}`. Edge cases: missing file → skip; permission error on file → skip with stderr warning; the script needs neither `git` nor `.git/` to run, by design — fresh tarball checkouts work
- [ ] `build` target depends on `gen-docs-manifest`
- [ ] **the generated file IS COMMITTED.** Drift between source markdown and the manifest surfaces as a git diff in PRs (reviewer can spot a stale-banner regression at a glance). `.gitignore` does NOT include this file. This means `go test ./...` on a fresh checkout compiles without `make build`
- [ ] script is idempotent: re-running with no changes produces no diff. Order: (1) collect entries, (2) **sort keys lexically before emitting the map literal** (Go map literals don't reorder at runtime; the textual order is load-bearing for the "drift surfaces as PR diff" property), (3) pipe the output through `gofmt -s` for stable whitespace
- [ ] verify `make build && bin/devbox docs show config/services` works end-to-end after a `make tidy`
- [ ] manually create a `docs/i18n/ru/reference/config/services.md` with a stale content-hash header; confirm the outdated banner shows
- [ ] run `make build && make test` — both must pass

### Task 14: Reference documentation

**Files:**
- Create: `docs/reference/cli/docs.md` (or extend existing — depends on whether the CLI ref already covers docs)
- Create: `docs/reference/docs/index.md` (new section explaining the docs subsystem from the user's perspective: TUI, show/list/export, language behavior, mermaid)
- Modify: `docs/reference/config/devbox.md` (already touched in Task 1; cross-link)
- Modify: `docs/reference/config/i18n.md` (cross-link to long-form docs translation namespace and clarify that `devbox/i18n/<lang>.yml` and `docs/i18n/<lang>/...` are different namespaces)

- [ ] write `docs/reference/docs/index.md` covering: commands (TUI, show, list, export, cache), language behavior (link to `i18n.md`), mermaid behavior (auto/mmdc/off, cache, system viewer vs inline), project docs (./docs), translation file layout with content-hash header example
- [ ] **expand `docs/reference/config/i18n.md` substantially** (not just cross-link). Add a dedicated top-level section "Long-form documentation translations" covering:
  - the two namespaces explicitly: `devbox/i18n/<lang>.yml` (YAML, command/UI strings — existing) vs `docs/i18n/<lang>/<source-tree>/...` (markdown, long-form — new). Different loaders, different validators, different lifecycles.
  - directory layout (mirror of `docs/reference/` and `docs/internals/`)
  - content-hash header format: exact regex, hash derivation (`sha256` of the en-file bytes, first 12 hex chars), how the staleness check works against the embedded manifest, why content-hash and not git-sha
  - how `--lang` flows from the CLI through `i18n.ResolveLocale` into per-file fallback
  - example translated file with header
  - explicit non-overlap: `devbox validate`'s `i18n.*` domain does NOT touch the markdown translations in v1 (planned for follow-up `docs.*` domain)
- [ ] regenerate CLI reference: `make build && bin/devbox docs generate --scope cli` to pick up the new subcommands and flags
- [ ] no automated test; manual diff of generated CLI ref
- [ ] run `make build` — must succeed

### Task 15: Verify acceptance criteria (manual verification gate)

**This task is a human-driven verification checklist.** No new test code is added here — the unit/integration tests live in Tasks 1–13. Each item is exercised by hand against a real or fixture project. Tick items as verified.

- [ ] `devbox docs` in a TTY launches the TUI; in a pipe returns the hint error
- [ ] `devbox docs show config/services` works in both TTY (glamour) and pipe (raw markdown, zero ANSI)
- [ ] `devbox docs show config/services --lang ru` (with a fixture translation) renders Russian; without it, English with banner
- [ ] `devbox docs show config/services --raw` outputs raw markdown even in TTY
- [ ] `devbox docs list` outputs tab-separated topics
- [ ] `devbox docs export /tmp/x` writes a markdown tree; `--lang ru` applies the per-file fallback banner
- [ ] `devbox docs export /tmp/x` on a non-empty dir errors without `--force`
- [ ] `devbox docs cache clear` removes the cache dir contents
- [ ] `devbox docs generate` still works (regression)
- [ ] mermaid render: with `mmdc` on PATH → PNG cached and shown inline (on kitty/ghostty/wezterm) or printed as placeholder elsewhere; without `mmdc` → raw block fallback with hint
- [ ] `docs.mermaid: off` in `devbox.yml` → no mmdc invocation under any path
- [ ] Project docs branch appears when `./docs/` exists; absent otherwise
- [ ] TUI: hot reload edits a project doc file → viewport updates
- [ ] TUI: `L` cycles languages; `e` jumps to English original on a translated file
- [ ] TUI: `y` on a diagram copies source via OSC 52
- [ ] run full test suite: `make test`
- [ ] run linter: `make lint`

### Task 16: [Final] Update CLAUDE.md / packages.md and move plan

> **Note for the implementer:** task numbering in this section refers to the final sequence (1, 2, 3a, 3b, 4–13, 14, 15, 16). Total tasks: 16.

**Files:**
- Modify: `docs/internals/packages.md` (add entries for `internal/docs`, `internal/docs/render`, `internal/docs/mermaid`, `internal/docs/tui`, `internal/docs/export`)
- Modify: `AGENTS.md` (CLAUDE.md is a symlink) — add a Key Patterns entry: docs subsystem invariants (no project lock, no preflight, embed scope, SHA-manifest fallback policy, mermaid chain composition)
- Move: `docs/plans/2026-05-26-devbox-docs.md` → `docs/plans/completed/2026-05-26-devbox-docs.md`

- [ ] update `docs/internals/packages.md` per-package responsibilities for the new packages
- [ ] add Key Patterns entries to `AGENTS.md`:
  - "Docs subsystem read-only" — never call `lock.AcquireProjectLocks`; never run preflight; works without `devbox.yml`
  - "Two i18n namespaces" — `devbox/i18n/<lang>.yml` (command/UI strings, YAML) vs `docs/i18n/<lang>/reference/...` (long-form markdown). Different loaders, different validators, do not merge.
  - "Mermaid chain composition" — `mermaid.Chain(FileCache, MmdcRenderer)`; `cfg.Docs.Mermaid == "off"` short-circuits to `Disabled{}`
  - "Content-hash manifest fallback" — staleness check returns `false` when the generated map is empty or the entry is absent; manifest values are `sha256(file bytes)[:12]`, NOT git commit SHAs; never call `git` at runtime
- [ ] `git mv docs/plans/2026-05-26-devbox-docs.md docs/plans/completed/`
- [ ] final commit message: `feat(docs): embedded documentation with TUI, mermaid rendering, and translation fallback`

## Post-Completion

*Items requiring manual intervention or external systems — informational only.*

**Manual verification:**

- Run `bin/devbox docs` in kitty, ghostty, wezterm, iTerm2, Alacritty, gnome-terminal, Terminal.app, and tmux. Verify inline mermaid render where expected and `OpenSystem` fallback elsewhere.
- Run `bin/devbox docs` in a tmux session both with and without `set -g set-clipboard on`; verify the one-time clipboard hint shows up only when the option is off.
- Verify a real translation: add `docs/i18n/ru/reference/config/services.md` with a stale content-hash header and confirm the outdated banner.
- Run `mmdc --version` to verify the cache key invalidates correctly after a mermaid-cli upgrade.

**Follow-up tracks (NOT part of this plan):**

- HTML/PDF export.
- Full-text search (currently headings only).
- Validate-domain `docs.*` for stale-translation warnings in `devbox validate`.
- Localization of devbox's own cobra `Short`/`Long` strings (including the new `docs` subcommands themselves).
- Inline mermaid render in non-kitty terminals via embedded mermaid.js + chromedp (consciously rejected — runtime mmdc or nothing).
- Build-time pre-render of mermaid diagrams (consciously rejected).
- Populate actual Russian translations for `docs/i18n/ru/reference/...`.
