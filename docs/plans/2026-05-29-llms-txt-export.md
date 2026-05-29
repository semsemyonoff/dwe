# `devbox docs llms-txt` — AI-targeted project index

## Overview

Add a new subcommand `devbox docs llms-txt` that emits a single text document in the [llms.txt](https://llmstxt.org/) format. The file is a dense, ~2-5KB index designed for AI agents: project context, available commands, services, and pointers to embedded reference documentation. An agent loading it once gets a complete picture of "what this devbox project is and where to find more detail," without having to ingest the full embedded docs tree.

The command works both inside a project (project-aware: includes services, user commands, info config) and outside one (project-agnostic: generic devbox reference).

Output is to stdout by default; `--output <path>` writes to a file. There is no `--output json` flag — the format IS the format (a single text/markdown stream).

This is Wave 1 deliverable #2 from the AI integration roadmap, parallel to the JSON state output refactor (Plan: `2026-05-29-json-state-output.md`).

## Context (from discovery)

- **Existing docs subsystem** (`internal/cli/docs/` + `internal/core/docs/`) has the building blocks: embedded reference docs, content-hash manifest, locale handling, export to directory, CLI generation.
- **Existing files** in `internal/cli/docs/`: `docs.go`, `list.go`, `show.go`, `search.go`, `export.go`, `generate.go`, `cache.go`. A new `llmstxt.go` slots in cleanly.
- **`docs export`** exports the full docs tree to a directory (multi-file). It's NOT the right home for llms.txt (which is a single stream).
- **`docs generate`** generates CLI reference (markdown/yaml/man). Also not the right home (different audience: tooling, not AI).
- **Project data sources** for the index: `devbox.yml` (project name, full name), `devbox/services/*/service.yml` (service names, types, descriptions), `devbox/info.yml` (URLs, hosts), user commands registry, embedded docs catalog.
- **CLAUDE.md**: docs subsystem is read-only — no lock, no preflight. Project root detection is optional. Same applies here.

## Development Approach

- **Testing approach**: Regular (code first, then tests). Golden-file testing is the natural fit for output-shape commands.
- Complete each task fully before moving to the next.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task.
- **CRITICAL: all tests must pass before starting next task** — no exceptions.
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run `make test` after each task.

## Testing Strategy

- **Unit tests**: required per task.
- **Golden files**: `testdata/llms_txt_project.golden` for project-aware output, `testdata/llms_txt_no_project.golden` for project-agnostic output. Use a small fixture project (or extend an existing one in `testdata/`).
- **Format conformance**: a parsing-style test that confirms output follows llms.txt structure (single H1 title, optional blockquote summary, H2 section headers, link list items).
- **No e2e**: no UI; CLI golden tests cover the surface.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.

## Solution Overview

**Command shape**: `devbox docs llms-txt [--output PATH] [--lang LANG] [--no-project]`

**Output structure** (llms.txt minimal spec):
```
# <Project Name>

> One- or two-sentence project summary, derived from devbox.yml or generic devbox tagline.

## Project

- Project name, status entry points
- Services (name, type, brief)
- URLs/hosts from info.yml (when present)

## Commands

- Command index (id, one-line description) — including user commands

## Documentation

- Embedded reference docs (config, pipelines, commands, render, snapshot) as link list
- Internal architecture docs (optional, gated by --include-internals)

## Quick start

- `devbox status` — see what's running
- `devbox validate` — check project health
- `devbox info` — project overview
- `devbox --help`
```

**Key design decisions**:
- Stdout by default; `--output PATH` writes file (and creates parent dirs if absent).
- `--lang` resolves via the same chain as other docs commands (flag → user config → $LANG → en).
- Embedded reference doc links use a synthetic scheme `devbox-docs://<path>` (resolvable via `devbox docs show <topic>` for AI agents that can re-invoke).
- Outside a project (no `devbox.yml`): emits a "generic" llms.txt with just the Commands and Documentation sections — useful for "what is devbox" orientation.
- No `--output json` — output format is itself the document.

## Technical Details

### CLI entry point
`internal/cli/docs/llmstxt.go` — register via `newDocsLlmsTxtCmd(flags)`, wire into `newDocsCmd(...)` in `docs.go`.

### Generator
`internal/core/docs/llmstxt/generator.go` — pure function `Generate(opts) (string, error)` that takes:
```go
type Opts struct {
    ProjectRoot    string             // empty when no project
    Locale         string             // from flag/config/system
    IncludeIntern  bool               // for internals docs section
    DocTopics      []docs.TopicEntry  // injected; from docs.AllTopics(docs.Sources(projectRoot), locale)
    Commands       []commandSummary   // injected; from usercommands registry
    Services       []serviceSummary   // injected; from config.Services
    InfoSnapshot   *infoSummary       // injected; from info.LoadInfoConfig (nil if no project)
}
```

Pure-function shape so unit tests can drive it without touching disk. Note: there is no `docs.Catalog` type in the codebase — the real iteration primitive is `docs.AllTopics(roots []DocRoot, locale string) []TopicEntry` (verified at `internal/core/docs/topic.go`).

### Data collectors

**Boundary decision**: collectors live in `internal/cli/docs/` (NOT `internal/core/docs/llmstxt/`) to avoid bloating the core package's import surface — `internal/core/docs/` currently has no `internal/core/project/config` or `internal/core/usercommands` dependency, and pulling them in just for collectors is unwarranted. The pure-function `Generate(opts)` stays in core (testable, deterministic); collectors that touch repo state stay in cli.

Signatures (in `internal/cli/docs/llmstxt.go`):
- `collectCommandSummaries(reg *usercommands.Registry, tr i18n.Translator, locale string) []commandSummary` — translator injected explicitly per CLAUDE.md i18n contract (avoid `rflags.Locale` directly; use `i18n.TranslatorOrNop(rflags.I18n)` at call site to safely handle completion-path bypass)
- `collectServiceSummaries(cfg *config.DevboxConfig, tr i18n.Translator, locale string) []serviceSummary` — iterates via `config.DeployOrder(cfg, types)` (NOT `range cfg.Services`)
- `collectInfoSummary(cfg *config.DevboxConfig, root string) *infoSummary` — uses `config.LoadInfoConfig` + auto-block expansion directly (does NOT depend on Plan 1; calls the same `ui.RenderInfo` data path used today)

### Format writer
- Internal `strings.Builder`-based writer with `writeTitle`, `writeBlockquote`, `writeSection(name, items)` helpers. No external library.
- Each item is a markdown link `[label](url)` per llms.txt spec.

### Error handling
- Missing `devbox.yml`: emit generic llms.txt; do not error.
- Missing services/info: skip those sections gracefully.
- I/O error writing to `--output PATH`: return wrapped error (will surface as text-mode error; if Plan 1 lands first, can use `cmdctx.CodedError` with code `llms_txt_write_failed`).

## What Goes Where

- **Implementation Steps**: command registration, generator, collectors, file output, tests.
- **Post-Completion**: validation against llms.txt parsers (optional), inclusion in release notes when shipping.

## Implementation Steps

### Task 1: Generator skeleton + pure function shape

**Files:**
- Create: `internal/core/docs/llmstxt/generator.go`
- Create: `internal/core/docs/llmstxt/generator_test.go`

- [x] define `Opts` struct with EXPORTED summary types so cli collectors can populate them: `ProjectRoot, Locale string`, `IncludeIntern bool`, `DocTopics []docs.TopicEntry`, `Commands []CommandSummary`, `Services []ServiceSummary`, `InfoSnapshot *InfoSummary`. The Technical Details snippet uses lowercase names for brevity; the actual types MUST be exported PascalCase for cross-package use.
- [x] define `Generate(opts Opts) (string, error)` that returns the full document
- [x] implement internal writers: `writeTitle(b *strings.Builder, title string)`, `writeBlockquote(b *strings.Builder, text string)`, `writeSection(b *strings.Builder, heading string, items []sectionItem)`
- [x] `sectionItem` type: `{Label, URL, Description string}` — rendered as `- [Label](URL) — Description` per llms.txt spec
- [x] generic project-agnostic output when `opts.ProjectRoot == ""`: title "devbox", brief tagline, then Commands + Documentation sections only
- [x] write tests for project-agnostic path (snapshot via golden)
- [x] write tests for empty/minimal opts (no services, no commands)
- [x] run `go test ./internal/core/docs/llmstxt/...` — must pass before Task 2

### Task 2: Project-aware collectors (in cli layer)

**Files:**
- Create: `internal/cli/docs/llmstxt_collectors.go` (collectors live here, not in core, to avoid bloating core/docs imports — see boundary decision in Technical Details)
- Create: `internal/cli/docs/llmstxt_collectors_test.go`
- Modify: `internal/core/docs/llmstxt/generator.go` (extend `Generate` to handle project-aware sections)
- Create: `internal/core/docs/llmstxt/testdata/llms_txt_project.golden`

- [ ] implement `collectServiceSummaries(cfg *config.DevboxConfig, tr i18n.Translator, locale string) []llmstxt.ServiceSummary` — iterate via `config.DeployOrder(cfg, types)` per CLAUDE.md service-iteration rule; include name, type, dir, info.title
- [ ] implement `collectCommandSummaries(reg *usercommands.Registry, tr i18n.Translator, locale string) []llmstxt.CommandSummary` — translator is injected explicitly; resolve descriptions via the typed interface method `tr.CommandDescription(locale, id, fallback)` directly (the `Translator` interface at `internal/shared/i18n/translator.go` exposes it as a method, not a free function on `*Store`). Use `i18n.TranslatorOrNop(rflags.I18n)` at the cli RunE call site to safely tolerate completion-path bypass.
- [ ] implement `collectInfoSummary(cfg *config.DevboxConfig, root string) *llmstxt.InfoSummary` — load info.yml via `config.LoadInfoConfig`, resolve vars, gather title + section count (does NOT depend on Plan 1 — calls existing rendering path directly)
- [ ] update `Generate` to render Project / Services / Commands / Documentation / Quick start sections when project is present
- [ ] write tests with fake registry + cfg, comparing to golden file
- [ ] run `go test ./internal/cli/docs/... ./internal/core/docs/llmstxt/...` — must pass before Task 3

### Task 3: Docs catalog reference section

**Files:**
- Modify: `internal/core/docs/llmstxt/generator.go`
- Modify: `internal/core/docs/llmstxt/generator_test.go`

- [ ] use `[]docs.TopicEntry` (already in `Opts.DocTopics`) — populate via `docs.AllTopics(docs.Sources(projectRoot), locale)` at the cli RunE call site (per `internal/core/docs/topic.go`)
- [ ] in `Generate`, filter `DocTopics` by category: items under `reference/` become Documentation section entries; items under `internals/` are gated by `opts.IncludeIntern`
- [ ] format each topic as a link: `[<topic.Title>](devbox-docs://<topic.RelPath>) — <topic.Summary>` per llms.txt spec
- [ ] **embedded-docs staleness on fresh checkout**: if `opts.DocTopics` is empty (e.g. fresh checkout without `make build`, embedded tree gitignored), gracefully emit an empty Documentation section — do NOT error. Matches the CLAUDE.md content-hashes manifest "absence → no banner" pattern.
- [ ] write tests verifying both with-internals and without-internals shapes
- [ ] write test verifying empty-DocTopics produces no Documentation entries but doesn't fail
- [ ] run `go test ./internal/core/docs/llmstxt/...` — must pass before Task 4

### Task 4: CLI command + flag wiring

**Files:**
- Create: `internal/cli/docs/llmstxt.go`
- Modify: `internal/cli/docs/docs.go` (register new subcommand)
- Create: `internal/cli/docs/llmstxt_test.go`

- [ ] register `newDocsLlmsTxtCmd(flags *cmdctx.RootFlags) *cobra.Command` in `docs.go` similar to existing `newDocsExportCmd`
- [ ] cobra command shape: `Use: "llms-txt"`, `Args: cobra.NoArgs` (no positional arguments — per golang-spf13-cobra: declare with Args, never `len(args)` checks in RunE), `SilenceUsage: true` (match existing docs subcommand convention)
- [ ] flags: `--output PATH` (string, default ""), `--lang LANG` (string, default ""), `--include-internals` (bool, default false), `--no-project` (bool, default false — forces project-agnostic output even if devbox.yml exists)
- [ ] RunE: resolve locale via `i18n.ResolveLocale(flag, cfgLang, $LANG)` (same chain as other docs commands per CLAUDE.md)
- [ ] RunE: assemble `Opts` (load registry, cfg, info as relevant); call `llmstxt.Generate(opts)`
- [ ] RunE: if `--output PATH` empty, write to `cmd.OutOrStdout()`; else `os.WriteFile(path, []byte(out), 0644)` with `os.MkdirAll(filepath.Dir(path), 0755)`
- [ ] command runs WITHOUT project (per docs subsystem rules: no lock, no preflight, project root optional). **No `allowedWithoutProject` change needed** — `root.go:227` already does `strings.HasPrefix(path, "devbox docs")` which covers `devbox docs llms-txt`. Verified.
- [ ] write CLI-level test: invoke command, capture stdout, assert non-empty + contains expected H1
- [ ] run `go test ./internal/cli/docs/...` — must pass before Task 5

### Task 5: File output + error path

**Files:**
- Modify: `internal/cli/docs/llmstxt.go`
- Modify: `internal/cli/docs/llmstxt_test.go`

- [ ] handle `--output PATH`: create parent dirs (`MkdirAll`), write file, return early
- [ ] wrap file-write error with `cmdctx.ErrWrap("llms_txt_write_failed", err).WithDetail("path", p)` IF Plan 1 (`2026-05-29-json-state-output.md`) Task 1 has already landed; otherwise use plain `fmt.Errorf`
- [ ] write test: `--output /tmp/some/nested/llms.txt` creates the file and writes content
- [ ] write test: `--output /forbidden/path/llms.txt` returns an error (use a known-unwritable path or simulate with read-only temp dir)
- [ ] run `go test ./internal/cli/docs/...` — must pass before Task 6

### Task 6: Verify acceptance criteria + smoke test

- [ ] `devbox docs llms-txt` (no project): outputs generic llms.txt to stdout, exit 0
- [ ] `devbox docs llms-txt` (inside project): outputs project-aware llms.txt to stdout
- [ ] `devbox docs llms-txt --output /tmp/llms.txt`: writes file, stdout empty, exit 0
- [ ] `devbox docs llms-txt --lang ru` (conditional — only verify if `ru` translations are configured in fixture; skip otherwise): uses ru descriptions for commands/services
- [ ] `devbox docs llms-txt --include-internals`: output contains the internals section
- [ ] `devbox docs llms-txt --no-project` inside a project: output matches the no-project shape
- [ ] output H1 + blockquote + sections — single `# Title`, optional `> summary`, `## Section` headings
- [ ] command does NOT acquire any lock and does NOT run preflight (verify with deploy.lock unchanged after invocation)
- [ ] `make test` and `make lint` pass

### Task 7: Update documentation

**Files:**
- Modify: `docs/reference/` (auto-regen via `make build`)
- Modify: `AGENTS.md` / `CLAUDE.md` (mention llms.txt as the AI orientation entry point)
- Move: this plan to `docs/plans/completed/`

- [ ] run `make build` to regenerate embedded docs and CLI reference
- [ ] add a short note to AGENTS.md "Configuration Documentation" section mentioning `devbox docs llms-txt` as the agent-targeted index
- [ ] verify content-hashes manifest is updated and committed (per CLAUDE.md CI guard)
- [ ] `mkdir -p docs/plans/completed && mv docs/plans/2026-05-29-llms-txt-export.md docs/plans/completed/`

## Post-Completion

**Manual verification**:
- Run `devbox docs llms-txt > /tmp/llms.txt` in a real devbox project; open in editor and verify shape, density, useful pointers.
- Feed `/tmp/llms.txt` to Claude Code or another LLM with "tell me about this project" and assess whether it can orient itself.
- Optional: validate against an llms.txt parser if one is available (e.g. linting tool from llmstxt.org).

**Future enhancements** (not blocking):
- Auto-emit `llms.txt` at project root via a hook on `devbox deploy run` completion (opt-in).
- Localize the "Quick start" block via `i18n` store keys.
- Add a `devbox docs llms-full-txt` variant that inlines the full embedded reference docs (much larger output, for agents with bigger context windows).
