# Localization (i18n) for User Commands and Generated Docs

## Overview

Add a two-layer localization system to devbox that allows translating user-facing strings (currently only English) into other languages, while keeping English as the canonical authoring language.

**What gets localized in this iteration:**

- User command `description`, `confirmation_text`, and per-parameter descriptions.
- Command group `description` — surfaces in TUI browser group nodes (`command_cmd.go:590,606` read `gn.Meta.Description`). Group `title` is only consumed by the docs generator index in v1 (no current TUI surface for it); both are wired through `GroupTitle` / `GroupDescription` helpers so adding a TUI title surface later is free.
- Markdown generator section headers and property labels (`## Properties`, `## Parameters`, `**Working directory**`, etc.).
- Infrastructure prepared for future TUI/CLI string localization (the `ui.*` namespace and the `i18n.T` API are added but most TUI labels themselves remain English in this iteration — see explicit scope in Task 6).

**What is intentionally NOT translated (and why):**

- Cobra `Short`/`Long` strings of devbox's own commands — separately scoped track.
- Progress prints from `runDocsGenerate` (`"%s CLI docs written to %s\n"` etc.) — operational logs, not localizable in v1.
- `def.Description` and friends *as stored in the journal hash* (`internal/deploy/journal/hash.go:336,368`) — hashing the *translated* string would invalidate state across locale changes. Display reads get the i18n indirection; hash reads stay raw.

**Scope of `DEVBOX_LANGUAGE` / `userconfig.Language`:** in v1 the resolved locale affects only user-command surfaces (description, confirmation_text, parameter descriptions, group title/description) and the generator's `ui.*` strings. Devbox's own Cobra Short/Long/Long text remains English — that namespace (`builtin.commands.*`) is reserved but not consumed in v1. Document this explicitly in `docs/reference/config/i18n.md` to set expectations.

**What does NOT get localized:**

- Error messages and logs (machine-readable, not user-facing prose).
- The long-form devbox product documentation under `docs/reference/` (separately scoped track).
- Per-command long-form markdown overlays (rejected during brainstorm).

**Problem it solves:** today every user-facing string is English-only. Mixed teams cannot read command descriptions in their native language; devbox cannot produce a `docs/reference/commands/ru/` tree alongside `en/`.

**Integration approach:** YAML command files stay 100% English (the canonical source); translations live in sidecar files under a known schema. A new `internal/i18n` package owns loading and lookup. Every existing rendering call site that touches `def.Description` / `def.ConfirmationText` / param descriptions / hardcoded section headers gains an `i18n.T(locale, key, fallback)` indirection.

## Context (from discovery)

**Files/components involved:**

- `internal/i18n/` — NEW package: embedded built-in translations, project-layer loader, `Store`, `T()` lookup.
- `internal/userconfig/{config.go,parser.go,load.go}` — add `Language` field, parse `language=` key, accept `DEVBOX_LANGUAGE` env. Userconfig already layers global → project (`.devbox/config`) → env, so a single field gets all three layers for free.
- `internal/usercommands/model/types.go` — no schema change; `CommandDef` field set stays as-is. `Description`, `ConfirmationText`, and `Params[].Description` are the strings that get the i18n indirection at *render* time.
- `internal/command/docs.go` — `runDocsGenerate` (`:70`), `writeCommandMarkdown` (`:395`, hardcodes `## Properties`, `## Command`, `## Parameters`, etc.), `genCommandsIndex` (`:617`). Adds `--lang` flag; threads locale into the two writers.
- `internal/command/command_cmd.go` — `printInspectAt` (`:664`), the closure passed to TUI as `Item.Inspect`. Same i18n indirection on `def.Description`.
- `internal/ui/cmdbrowser/run.go` (`:50-62`) — `Item.Description` populated upstream; the populating call site (in `internal/command/command_cmd.go`) substitutes the translated string before constructing the `Item`.
- `internal/validate/i18n/` — NEW domain. Symmetric to existing `internal/validate/{config,commands,checks,...}`. Registers via `All()` function (no `init()` side effects). Reads project `devbox/i18n/*.yml`, cross-references with the user-command registry to flag orphan/unknown-field warnings.
- `cmd/devbox/main.go` / `internal/command/root.go` — single locale-resolution point during root init (after `userconfig.Load`). Result stored on `rootFlags` and threaded explicitly (no globals).

**Related patterns found:**

- Validator domain layout (`internal/validate/<domain>/`) with `All()` registry export — exactly the shape this plan reuses for `i18n.*`.
- `userconfig` is flat key=value (NOT YAML); env vars follow `DEVBOX_<UPPER>` naming. New field uses the same pattern: `language=` and `DEVBOX_LANGUAGE`.
- The doc generator currently inline-concatenates strings (no templates) — i18n calls slot in cleanly without introducing a templating layer.
- `KnownFields(true)` is used for *user-edited pipeline/command/manifest* YAML. Translation files are user-edited YAML too → strict decode (typo'd field surfaces as a load error, not a silent miss).

**Dependencies identified:**

- `gopkg.in/yaml.v3` (already in `go.mod`).
- Standard library only for the rest (`embed`, `path/filepath`, `strings`).

## Development Approach

- **Testing approach:** Regular (implementation first, then tests). Tests are required in every task before moving on.
- Complete each task fully before the next; small focused changes; run `make test` (or scoped `go test ./internal/i18n/...`) after each task.
- Maintain forward design correctness — per project policy (CLAUDE.md), no `schema_version` bumps, no migration shims; rename freely.
- **CRITICAL: every task includes new/updated tests** for the code it touches. Tests cover success and error paths. Tests must pass before starting the next task.
- **CRITICAL: update this plan if scope shifts during implementation** (add ➕ tasks, ⚠️ blockers).

## Testing Strategy

- **Unit tests** (required per task): each new package gets `*_test.go`. Table-driven where natural (`Store.T` lookup matrix, `ResolveLocale` precedence matrix, parser edge cases).
- **Integration-ish tests** for `docs generate --lang` via the existing pattern (write fixture command set + i18n file under a temp project root, run the generator, diff output). Mirrors existing tests in `internal/command/`.
- **No new e2e UI tests.** TUI Item.Description swap is exercised by a unit test on the populating site; the TUI itself is currently not covered by automated browser tests.
- Run `make test` after each task. Final task runs the full suite.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Keep plan in sync with actual work done.

## Solution Overview

**Architecture (high-level):**

```
                       ┌────────────────────────────┐
                       │  internal/i18n.Store        │
                       │  - locales: map[lang]Bundle │
                       │  - T(lang, key, fallback)   │
                       └─────────────┬──────────────┘
                                     │
                ┌────────────────────┴────────────────────┐
                │                                         │
   embed: internal/i18n/translations/*.yml    devbox/i18n/*.yml
   (built-in, ships with binary)              (project overlay, optional)
                │                                         │
                └────────────────────┬────────────────────┘
                                     │ project-wins deep merge
                                     ▼
                       Lookup chain for key K in locale L:
                         L → en (same store) → fallback arg → ""
```

**Locale resolution order** (single resolver, called once at root-init):

1. `--lang` flag (only on `docs generate`; per-invocation, not stored on rootFlags globally)
2. `userconfig.Language` (already layered global → project → `DEVBOX_LANGUAGE` env)
3. System `$LANG` → parsed to 2-letter code (`ru_RU.UTF-8` → `ru`)
4. Default `"en"`

If the resolved locale is not present in either store layer → silent fallback to `"en"` (no warning; user's system `$LANG` is not a localization opt-in).

**Key design decisions and rationale:**

- **Sidecar over inline.** YAML command files stay clean English; translations live separately. Authors and translators don't step on each other.
- **Two layers, same format, project-wins.** Devbox ships built-in UI strings + (eventually) translations of its own built-in commands. Projects add translations for their own user commands. Same file format → same loader and validator. Project can override built-in if desired (e.g. customize a UI phrase) — allowed, not flagged.
- **`Store.T(locale, key, fallback)` rather than gettext-style `T("Hello")`.** Keys are stable IDs (`commands.build.docker.description`). The fallback argument lets per-command lookups gracefully degrade to the original `def.Description` without inventing a synthetic English entry.
- **Strict YAML decode.** Translation files are user-edited; strict mode catches typos in field names like `descripton:` immediately.
- **Resolver runs once.** Locale is stored on `rootFlags` after `userconfig.Load` and threaded explicitly through call sites that need it. No global state, no goroutine-local.
- **No hot reload, no lazy load.** Loaded once at startup; covers the whole CLI invocation.

## Technical Details

### `internal/i18n` package

```go
package i18n

// Bundle is the parsed contents of one translation file.
type Bundle struct {
    UI       map[string]string          // ui.<key>
    Commands map[string]CommandStrings  // commands.<id>
    Groups   map[string]GroupStrings    // groups.<id>
}

type CommandStrings struct {
    Description      string
    ConfirmationText string
    Params           map[string]ParamStrings  // params.<paramName>
}

type ParamStrings struct {
    Description string
}

type GroupStrings struct {
    Title       string
    Description string
}

// Store holds merged bundles per locale.
type Store struct {
    locales map[string]*Bundle  // key: 2-letter code; "en" always present
}

// Load reads embedded built-in files plus optional project overlay from
// <projectRoot>/devbox/i18n/*.yml. Each locale: built-in → project, project-wins.
// Project parse errors are NOT returned as a fatal error from Load (so a
// broken translation file does not break runtime UI); they are surfaced
// instead via LoadProjectBundles for the validator.
func Load(projectRoot string) (*Store, error)

// AvailableLocales returns the union of locales present in built-in and
// project layers. Always includes "en".
func (s *Store) AvailableLocales() []string

// --- UI strings: generic, used only for the flat ui.* namespace ---
// T resolves a ui.* key (e.g. "ui.docs.section.properties") for the given
// locale. Lookup chain: locale → "en" → fallback → "". Only valid for
// keys with the "ui." prefix.
func (s *Store) T(locale, uiKey, fallback string) string

// --- Typed helpers for namespaces with dotted IDs ---
// Command and group IDs themselves contain dots ("services.main.db.migrate"),
// so a generic dotted-key API would be ambiguous to parse. Each typed
// helper has the same lookup chain (locale → en → fallback → "").
func (s *Store) CommandDescription(locale, commandID, fallback string) string
func (s *Store) CommandConfirmationText(locale, commandID, fallback string) string
func (s *Store) ParamDescription(locale, commandID, paramName, fallback string) string
func (s *Store) GroupTitle(locale, groupID, fallback string) string
func (s *Store) GroupDescription(locale, groupID, fallback string) string
```

**Why typed helpers, not a single `T(key, fallback)`:** command IDs (`services.main.db.migrate`) and group IDs (`services.main.db`) are dot-separated by construction (see `internal/usercommands/loader/loader.go:45,68`). A naive `strings.SplitN` on a flat key like `commands.services.main.db.migrate.description` cannot decide where `<id>` ends and `<field>` begins. Typed helpers receive the ID as a discrete argument, so no parsing is required. Param names are also not currently constrained against dots — `ParamDescription(commandID, paramName, ...)` takes them as separate arguments.

**Load-error API for the validator:**

```go
// LoadProjectBundles returns per-file parse results for the project layer
// only. Used by internal/validate/i18n to surface malformed translation
// files as diagnostics from `devbox validate`. The merged Store hides
// per-file source information; this is the canonical accessor for raw data.
func LoadProjectBundles(projectRoot string) ([]ProjectFile, error)

type ProjectFile struct {
    Path     string   // absolute path; for directory-level failures, the directory path
    Locale   string   // 2-letter code parsed from filename ("ru" from "ru.yml"); "" for directory-level failures
    Bundle   *Bundle  // nil if ParseErr is non-nil
    ParseErr error    // strict-decode failures OR directory-level read/glob/permission errors (folded into a sentinel ProjectFile with empty Locale)
}
```

**Error handling contract:**
- Missing `devbox/i18n/` directory → returns `(nil, nil)` (clean absent state).
- Directory exists but can't be read (permissions, OS error) → returns `([]ProjectFile{{Path: dirPath, Locale: "", ParseErr: err}}, nil)` — sentinel entry that surfaces as one diagnostic from `validate/i18n`.
- Directory readable, individual file fails strict decode → that file's `ProjectFile.ParseErr` is set; siblings still parse.
- The aggregate `error` return is reserved for programmer errors (e.g. `projectRoot` is empty) and should be rare. The validator treats a non-nil aggregate error as a fatal i18n-load diagnostic.

**Embed layout:**

```
internal/i18n/translations/
  en.yml        # canonical UI strings + built-in command descriptions
  // future: ru.yml, etc. — added incrementally
```

**Project overlay layout (lives in user projects, not in devbox repo):**

```
<project>/
  devbox/
    i18n/
      ru.yml    # any 2-letter language code
```

### File schema (both layers)

```yaml
ui:
  docs.section.properties:  "Properties"
  docs.section.command:     "Command"
  docs.section.parameters:  "Parameters"
  docs.section.context:     "Context"
  docs.section.environment: "Environment"
  docs.section.with:        "With"
  docs.section.script:      "Script"
  docs.section.argv:        "Argv"
  docs.section.files:       "Files"
  docs.property.id:           "ID"
  docs.property.type:         "Type"
  docs.property.group:        "Group"
  docs.property.private:     "Private"
  docs.property.confirmation: "Confirmation"
  docs.property.confirmation_text: "Confirmation text"
  docs.property.success_message:   "Success message"
  docs.property.error_message:     "Error message"
  docs.property.shell:    "Shell"
  docs.property.service:  "Service"
  docs.property.workdir:  "Working directory"
  docs.property.builtin:  "Builtin"

commands:
  build.docker:                 # full CommandDef.ID (group.localname)
    description: "..."
    confirmation_text: "..."
    params:
      tag:
        description: "..."

groups:
  build:                        # full group ID (dotted)
    title: "..."
    description: "..."
```

Strict YAML decode (`yaml.Decoder.KnownFields(true)`) — typos fail loudly. The `en.yml` built-in file populates only the `ui.*` section: `commands.*` and `groups.*` entries are authored by *project translators* and lookups for them fall through directly to the original YAML value via the `T()` fallback argument. (No need to duplicate canonical English in `en.yml`.)

**Locale scoping note:** the `commands` / `groups` namespaces are populated *only* in project translation files (`devbox/i18n/<lang>.yml`). The built-in layer's job is `ui.*` plus future localization of devbox's own command set (out of scope for v1). This keeps the built-in `en.yml` small and stable.

### userconfig changes

```go
// internal/userconfig/config.go
type Config struct {
    // ... existing notify fields ...
    Language string  // "" = unset; resolver falls through to $LANG / "en"
}

// internal/userconfig/parser.go — apply()
case "language":
    cfg.Language = val

// internal/userconfig/load.go — applyEnv()
const envLanguage = "DEVBOX_LANGUAGE"
if v, ok := os.LookupEnv(envLanguage); ok {
    cfg.Language = strings.TrimSpace(v)
}
```

`Defaults()` leaves `Language` empty intentionally — the resolver handles the fallback chain.

### Locale resolver

```go
// internal/i18n/locale.go
// ResolveLocale picks the active locale per the documented precedence.
// flagLang is the docs-generate --lang value ("" if not given).
func ResolveLocale(flagLang, configLang, sysLang string) string {
    if flagLang != "" {
        return normalize(flagLang)
    }
    if configLang != "" {
        return normalize(configLang)
    }
    if sysLang != "" {
        if code := parseSystemLang(sysLang); code != "" {
            return code
        }
    }
    return "en"
}

// normalize lowercases and trims to 2 letters where applicable.
// parseSystemLang handles forms like "ru_RU.UTF-8" → "ru", "C" → "", "POSIX" → "".
```

### docs generate integration

- Add `--lang <code>` flag to `docsFlags`. Default `""` → resolve from userconfig/`$LANG` (single language only).
- Output layout is **always** `docs/reference/commands/<lang>/<group>/<name>.md` — including `en`. Per CLAUDE.md project policy (pre-release, no back-compat hacks), we don't preserve the legacy "en goes to root" shape just because that's what exists today; live monorepo projects update with this CLI change.
- `genCommandsIndex` writes one `commands/<lang>/index.md` per language run.
- `writeCommandMarkdown` and `genCommandsIndex` take `(store *i18n.Store, locale string)` parameters. Section/property strings go through `store.T(locale, "ui.docs.section.X", "Properties")` (English literal as fallback). Per-command and per-group fields use the typed helpers: `store.CommandDescription(locale, def.ID, def.Description)`, `store.CommandConfirmationText(...)`, `store.ParamDescription(locale, def.ID, p.Name, p.Description)` (docs.go:530), `store.GroupTitle(...)`, `store.GroupDescription(...)`.
- The `--lang` value is normalized through `i18n.ResolveLocale(df.lang, "", "")` before use, so `--lang ru_RU.UTF-8` resolves to `ru` and writes to `commands/ru/...`, matching how the store keys translations. Never use the raw `df.lang` string for path construction or lookups.
- `--lang all` is **deferred** to a follow-up task once a second locale ships. With only `en.yml` in built-in, `all` is a no-op; adding it now is YAGNI.

### Run-time UI integration

- `printInspectAt` in `internal/command/command_cmd.go` accepts a `Store` + locale and looks up the same keys before formatting (read sites: `:677-678`, `:685`).
- The site that builds `Item.Description` for the TUI browser substitutes the translated string (`:427`).
- `command list` rendering takes the same indirection (`:292-293`, `:317` for params, `:911-912` for another param-description path).
- Confirmation prompt (`:247`) substitutes translated `confirmation_text` via `store.CommandConfirmationText(...)`.
- Group metadata used by the browser (`:590` `gn.Meta.Description`, `:606` `cmd.Description` group display, `registry.go:100`) reads through `store.GroupDescription(locale, groupID, raw)`.
- **Completion path** (`command_cmd.go:641-642`, `CompletionWithDesc`): the `__complete` codepath bypasses `PersistentPreRunE`. When `rflags.I18n` is nil in completion handlers, return the raw `def.Description` verbatim — never panic, never fail loudly.

### Journal hash safety

`internal/deploy/journal/hash.go:336,368` hashes `Description` fields as part of step/phase identity. These reads MUST continue to use the raw `def.Description` / `phase.Description` / `step.Description` field values — never the translated string — or locale changes would invalidate journals and force needless re-deploys. The i18n indirection applies only to *display* call sites; storage/hashing/persistence sites stay raw. An explicit verification checkbox lives in Task 7.

### Validator (new domain `i18n.*`)

`internal/validate/i18n/` mirrors `internal/validate/checks/` layout. Driven by `i18n.LoadProjectBundles(baseDir)` (per-file metadata) rather than the merged `Store`, so each diagnostic can point at the offending `devbox/i18n/<lang>.yml`:

- `all.go` — `All(projectFiles []i18n.ProjectFile, registry *usercommands.Registry) []validate.Validator`.
- `parse_error.go` — one `Error` diagnostic per `ProjectFile` with non-nil `ParseErr` (strict-decode failures, directory-level read failures via a sentinel ProjectFile — see "Directory-level failures" below).
- `orphan.go` — `Warning` per `commands.<id>` or `groups.<id>` entry whose `<id>` is not in the registry. Per-locale disambiguation via the `ID` field (`<locale>/<id>`).
- `unknown_ui_key.go` — `Warning` per `ui.*` key in a project file that is not in `i18n.KnownUIKeys` (catches typos like `ui.docs.section.properies` that `KnownFields(true)` cannot detect on `map[string]string`).
- Wired into the validate command's registry assembly (`internal/command/validate.go` near the existing `valchecks.AllForStage(...)` call at `:479`).
- Does NOT fail on absent translation files (silent skip).

### Built-in coverage CI test

`internal/i18n/coverage_test.go`: walks the `embed.FS`, ensures every non-`en` bundle has the same set of `ui.*` keys as `en.yml`. Missing key → test failure. Extra key → allowed (forward-compat).

## What Goes Where

- **Implementation Steps (`[ ]` checkboxes):** code, tests, embedded fixture files, documentation under `docs/reference/`.
- **Post-Completion (no checkboxes):** manual verification of `docs generate --lang ru` output on a real project; future task to actually populate Russian translations (this plan ships infrastructure + English baseline + a small Russian sample for tests, not a full Russian translation).

## Implementation Steps

### Task 1: `internal/i18n` package skeleton + embedded English baseline

**Files:**
- Create: `internal/i18n/store.go`
- Create: `internal/i18n/bundle.go`
- Create: `internal/i18n/loader.go`
- Create: `internal/i18n/translations/en.yml`
- Create: `internal/i18n/store_test.go`
- Create: `internal/i18n/loader_test.go`

- [x] define `Bundle`, `CommandStrings`, `ParamStrings`, `GroupStrings`, `Store`, `ProjectFile` types in `bundle.go` / `store.go`
- [x] implement `parseBundle(r io.Reader) (*Bundle, error)` using `yaml.Decoder` with `KnownFields(true)` (struct-level strictness — note that `Bundle.UI` is `map[string]string` and typo'd ui-key names are NOT caught here; key whitelisting lives in Task 8's validator)
- [x] implement `Load(projectRoot string) (*Store, error)`: read all built-in `translations/*.yml` from `embed.FS`, then overlay any `<projectRoot>/devbox/i18n/*.yml` (project-wins deep merge per locale). **A project parse error is NOT a fatal Load error** — the bad file is skipped, runtime UI degrades gracefully, and the validator surfaces the error via `LoadProjectBundles` instead
- [x] implement `LoadProjectBundles(projectRoot string) ([]ProjectFile, error)` per the error-handling contract in Technical Details: `(nil, nil)` if dir absent; directory-level OS error folded into a sentinel `ProjectFile{Path: dirPath, Locale: "", ParseErr: err}`; per-file strict-decode failures set `ProjectFile.ParseErr` without aborting; aggregate `error` only for programmer errors. Used only by the validator domain
- [x] implement the generic `Store.T(locale, uiKey, fallback string) string` — only for `ui.*` keys. Lookup chain locale → en → fallback → ""
- [x] implement typed helpers: `CommandDescription(locale, id, fallback)`, `CommandConfirmationText(locale, id, fallback)`, `ParamDescription(locale, id, paramName, fallback)`, `GroupTitle(locale, id, fallback)`, `GroupDescription(locale, id, fallback)` — same lookup chain; ID and field name are discrete arguments, no key parsing
- [x] implement `Store.AvailableLocales() []string` returning union of built-in + project, "en" always first
- [x] populate `translations/en.yml` with all `ui.docs.*` keys listed in Technical Details (section headers + property labels) — `commands.*` and `groups.*` stay absent here (canonical English lives in YAML command files; helper fallback handles missing keys)
- [x] write table-driven tests for `T()`: ui hit, ui miss → fallback, unknown locale → en, project overlay wins, fallback empty when locale "" and key absent everywhere
- [x] write table-driven tests for typed helpers: hit/miss, dotted IDs like `services.main.db.migrate` resolve correctly, param description with paramName containing underscore, groups variant
- [x] write tests for `parseBundle`: valid file, strict-decode rejects typo'd struct field (e.g. `descripton:`), empty file, invalid YAML
- [x] write tests for `Load`: built-in only, built-in + project overlay, missing project dir → built-in only, malformed project file → non-fatal (bad file skipped, others loaded)
- [x] write tests for `LoadProjectBundles`: enumerates all *.yml under devbox/i18n/, parses each with strict decode, captures ParseErr per file, returns `(nil, nil)` when dir absent, returns sentinel `ProjectFile{Locale: ""}` when dir unreadable
- [x] run `go test ./internal/i18n/...` — must pass before Task 2

### Task 2: Built-in coverage test for translation keys

**Files:**
- Create: `internal/i18n/coverage_test.go`

- [x] walk `embed.FS`, parse each `<lang>.yml`, ensure every `ui.*` key in `en.yml` exists in every other built-in locale
- [x] fail test with a clear message listing the missing keys per locale
- [x] allow extra keys in non-`en` bundles (forward-compat: future-only keys are fine)
- [x] since only `en.yml` exists for now, test passes trivially — but locks the contract for when `ru.yml` etc. are added
- [x] run `go test ./internal/i18n/...` — must pass before Task 3

### Task 3: Locale resolver

**Files:**
- Create: `internal/i18n/locale.go`
- Create: `internal/i18n/locale_test.go`

- [x] implement `ResolveLocale(flagLang, configLang, sysLang string) string` per the documented precedence
- [x] implement `normalize(s string) string`: trim, lowercase, drop region (`ru-ru` / `ru_RU` → `ru`), drop encoding (`ru_RU.UTF-8` → `ru`)
- [x] implement `parseSystemLang(s string) string`: handles `ru_RU.UTF-8`, `en_US`, `POSIX` (→ ""), `C` (→ ""), empty → ""
- [x] table-driven test for `ResolveLocale`: flag wins, config wins when no flag, sys-lang when config empty, "en" when all empty, "en" when sys is "POSIX"/"C"
- [x] table-driven test for `parseSystemLang` covering all known shapes
- [x] run `go test ./internal/i18n/...` — must pass before Task 4

### Task 4: userconfig — add `Language` field

**Files:**
- Modify: `internal/userconfig/config.go`
- Modify: `internal/userconfig/parser.go`
- Modify: `internal/userconfig/load.go`
- Modify: `internal/userconfig/load_test.go`
- Modify: `internal/userconfig/parser_test.go`

- [x] add `Language string` field to `Config` (unexported tags not needed; flat parser keys it by `language`)
- [x] leave `Defaults().Language` empty (resolver handles fallback)
- [x] add `case "language":` in `parser.apply()` that sets `cfg.Language = val` (no parse error — any string is accepted; locale validity is the resolver/store's concern)
- [x] add `envLanguage = "DEVBOX_LANGUAGE"` constant in `load.go` and handle it in `applyEnv()` (trim, set)
- [x] update `parser_test.go`: parsing `language=ru`, parsing empty value, malformed line still rejected
- [x] update `load_test.go`: global file sets language, project file overrides, env wins over both
- [x] run `go test ./internal/userconfig/...` — must pass before Task 5

### Task 5: Resolve locale once in root PersistentPreRunE, thread through rootFlags

**Files:**
- Modify: `internal/command/root.go` (extends the EXISTING `PersistentPreRunE` at `:73` — do NOT add a new hook on any child command, per CLAUDE.md "Cobra does NOT chain `PersistentPreRunE`")
- Modify: `internal/command/root_test.go` (or wherever rootFlags init is tested)

**Important context:** `userconfig.Load` is currently called only on demand by notification paths (e.g. `internal/command/deploy.go:277`) where load failure is downgraded to a slog warning. Loading it eagerly in root must follow the same non-fatal policy — a malformed notifications config (or any future userconfig key) must NOT break unrelated commands.

- [x] add `Locale string` and `I18n *i18n.Store` fields to `rootFlags`
- [x] inside the existing root `PersistentPreRunE`, after the existing project/styles resolution: call `userconfig.Load(projectRoot)`. **On error**: `slog.Warn("userconfig load failed; locale falls through to $LANG/en", "err", err)` and treat language as empty. Do NOT return the error
- [x] then call `i18n.Load(projectRoot)`. **On error**: `slog.Warn("i18n load failed; UI strings will use English fallbacks", "err", err)` and assign `&i18n.Store{}` (empty non-nil store) to `rflags.I18n`. Do NOT return the error
- [x] call `i18n.ResolveLocale("", cfg.Language, os.Getenv("LANG"))` and store on `rflags.Locale`
- [x] document in code comment on `rootFlags`: callers needing localized strings read `rflags.I18n` and `rflags.Locale`, never package-level globals. The store and locale are guaranteed non-nil after a successful PersistentPreRunE pass
- [x] **completion path safety:** `ValidArgsFunction` callbacks (e.g. `command_cmd.go:74`, `:610`) run on cobra's `__complete` codepath which bypasses `PersistentPreRunE`, so `rflags.I18n` may be nil. Wrap completion-site lookups in a nil check: `if rflags.I18n != nil { use typed helper } else { use raw description }`. Verified in Task 7
- [x] write tests asserting: with no userconfig and no `$LANG`, `rflags.Locale == "en"`; with `LANG=ru_RU.UTF-8`, `rflags.Locale == "ru"`; with `userconfig.Language="de"`, `rflags.Locale == "de"`; with userconfig load error, command does NOT fail and `rflags.Locale == "en"` (or `$LANG` value); with i18n load total failure, `rflags.I18n` is a non-nil empty store
- [x] run `go test ./internal/command/...` — must pass before Task 6

### Task 6: docs generate — `--lang` flag, locale path, T() indirection

**Files:**
- Modify: `internal/command/docs.go`
- Modify: `internal/command/docs_test.go` (or create if absent)

**Scope decision (explicit):** this task localizes `writeCommandMarkdown` body and `genCommandsIndex` entries only. Cobra `Short`/`Long` strings on `devbox docs` itself, progress prints at `:139` / `:155` / `:165`, and any `genCLI*` paths (CLI reference for devbox's own commands) stay English in v1.

- [ ] add `lang string` to `docsFlags`; `cmd.Flags().StringVar(&df.lang, "lang", "", "Language code (default: from userconfig / $LANG)")` — no `all` value in v1 (deferred until a second built-in locale exists)
- [ ] in `runDocsGenerate` (scope=commands path): resolve effective locale — `df.lang == ""` → `rflags.Locale`, otherwise → `i18n.ResolveLocale(df.lang, "", "")` so `--lang ru_RU.UTF-8` normalizes to `ru` (matches how the store is keyed). Never use the raw `df.lang` string for the path or for lookups
- [ ] **path layout: always** `commands/<lang>/<group>/<name>.md` — including `en`. The existing path `commands/<group>/<name>.md` is dropped per project pre-release policy; live monorepo projects update with this change
- [ ] `genCommandsIndex` writes `commands/<lang>/index.md`
- [ ] change signatures: `writeCommandMarkdown(def, dir, store, locale)`, `genCommandsIndex(reg, dir, includePrivate, store, locale)`, `genRegistryMarkdown(...)` — thread store/locale down
- [ ] replace every literal section header in `writeCommandMarkdown` with `store.T(locale, "ui.docs.section.<X>", "English literal")` — cover at minimum: `## Properties` (`:404`), `## Command` (`:428`, `:441`), `## Argv` (`:431`, `:444`), `## Script` (`:460`), `## With` (`:471`), and the `## Parameters` section (find by grep around the param table around `:530`). The English literal is the fallback argument
- [ ] replace property labels in the properties table (`:406-421`) and any inline `**Key:**` strings via `ui.docs.property.<key>` keys: `ID`, `Type`, `Group`, `Private`, `Confirmation`, `Confirmation text`, `Success message`, `Error message`, `Working directory`, `Service`, `Compose args`, `Shell`, `Script`, `Builtin`. All keys must exist in `en.yml` (Task 1)
- [ ] replace `def.Description` insert (`:400-401`) with `store.CommandDescription(locale, def.ID, def.Description)`; same for `def.EffectiveConfirmationText()` (`:414`) → `store.CommandConfirmationText(locale, def.ID, def.EffectiveConfirmationText())`
- [ ] replace per-parameter description in the parameters table at `:530` with `store.ParamDescription(locale, def.ID, p.Name, p.Description)`
- [ ] update `genCommandsIndex` (`:617`, descriptions at `:659`) to use `store.CommandDescription(locale, def.ID, def.Description)`; group titles via `store.GroupTitle(locale, groupID, rawTitle)`
- [ ] **fix `genTopLevelIndex`** (`internal/command/docs.go:682`) — currently links to `commands/index.md`; under the new layout the index lives at `commands/<lang>/index.md`. When a non-default locale was generated, the top-level index links to that locale-scoped path (or simply lists per-locale subdirs if multiple runs accumulate)
- [ ] write tests: default locale generates `en/` subtree; `--lang ru` with a fixture project supplying `devbox/i18n/ru.yml` generates `ru/` subtree with translated descriptions and section headers; missing per-command translation key falls back to original English `def.Description`; missing built-in `ui.*` key falls back to the literal English argument
- [ ] run `go test ./internal/command/...` — must pass before Task 7

### Task 7: Run-time UI — signatures, TUI browser, `command list`, inspect, completion, runtime confirmation

**Files:**
- Modify: `internal/command/command_cmd.go` (enumerated sites + signature changes below)
- Modify: `internal/usercommands/runtime/confirmation.go` (workflow/runtime confirmation path)
- Modify: `internal/usercommands/runtime/build_context.go` (where `RunContext` is constructed — add `Translator` and `Locale` fields populated from `rflags`)
- Modify: `internal/usercommands/runtime/runner_workflow.go` (verify the copied RunContext preserves the translator)
- Modify: `internal/command/command_cmd_test.go` and the runtime confirmation tests

**Signature plumbing** (call sites do not currently have `rflags`):

- `printRunHeader` (`command_cmd.go:287`) — currently takes only `def`. Add `(store *i18n.Store, locale string)` or a small `Translator` interface
- `paramFieldsFromDef` (`:305`) — currently `(def, prefilled, provided)`. Add translator
- `makeBrowserSelector` (`:421`) — add translator
- Group/tree builders (`:537`) — add translator

A `Translator` interface (`{ CommandDescription(...), ParamDescription(...), ... }`) implemented by `*i18n.Store` keeps the dependency narrow. **Nil contract:** functions that take a `Translator` argument MUST NOT receive `nil` — callers always pass either a real `*i18n.Store` or `i18n.NopTranslator{}` (a zero-value struct whose methods just `return fallback`). The helper `i18n.TranslatorOrNop(s *i18n.Store) Translator` makes this explicit at the boundary (returns `s` if non-nil, else `NopTranslator{}`). Internal functions can then assume non-nil and skip nil checks. This avoids the previous wording's contradiction between "nil is identity" and "nil is not allowed".

**Display sites to update** (from grep — all in `internal/command/command_cmd.go`):
- `:247` — confirmation prompt title: `def.EffectiveConfirmationText()` → typed helper
- `:292-293` — `def.Description` displayed alongside command
- `:317` — `Description: p.Description` (translate before struct assignment)
- `:427` — `Description: d.Description` for `cmdbrowser.Item.Description`
- `:590` — `Desc: gn.Meta.Description` → `GroupDescription(...)` keyed on the *group ID*, not a command ID
- `:606` — `Desc: cmd.Description` — audit: this is a group's display name (not a command), translate via `GroupDescription`
- `:641-642` — `CompletionWithDesc(d.ID, d.Description)` in `ValidArgsFunction` — nil-store guard
- `:677-678` — `def.Description` in inspect output
- `:685` — `def.EffectiveConfirmationText()` in inspect output
- `:911-912` — `p.Description` (param description display)

**Runtime confirmation** (`internal/usercommands/runtime/confirmation.go:39`):
- Currently reads `ctx.Cmd.EffectiveConfirmationText()` raw. The top-level prompt at `command_cmd.go:247` is one entry point; runtime is the OTHER, hit by workflow steps that re-enter via `RunCommand` (see `runner_workflow.go:300` — copies `RunContext`).
- Add `Translator Translator` and `Locale string` fields to `RunContext`. **`BuildRunContext` keeps a no-op default** (`Translator: i18n.NopTranslator{}`, `Locale: ""`) because `build_context.go` has no access to command rootFlags. The outer callers populate the fields AFTER calling `BuildRunContext`, mirroring the existing pattern at `command_cmd.go:236` where other runtime-only fields are set on the constructed context.
- `confirmation.go:39` looks up via `ctx.Translator.CommandConfirmationText(ctx.Locale, ctx.Cmd.ID, ctx.Cmd.EffectiveConfirmationText())`. With the NopTranslator default it stays English when no caller wires up i18n.

**Scope of runtime confirmation localization in v1:**
- Localized: outer `runCommandByID` (top-level user command invocation) and any workflow re-entry through it (`runner_workflow.go:300` copies the parent `RunContext` including the translator).
- **NOT localized in v1:** pipeline `type: command` checks at `internal/pipeline/executor.go:258` and files-gate probes at `:600` also build `RunContext` for user commands, but per CLAUDE.md ("`type: command` checks are run with `SkipConfirm=true` / `NonInteractive=true` / `SkipNotify=true` / discarded stdout / captured stderr"), they do not surface user-visible confirmation prompts. Their `RunContext` keeps the NopTranslator default — no user impact. If they ever begin surfacing user-visible prompts, threading `rflags.I18n` into the pipeline action context becomes a follow-up; document this as a known scope cut in `docs/reference/config/i18n.md`.

**Hash safety** — no source change. The hash code at `internal/deploy/journal/hash.go:336,368` has NO locale input and reads `phase.Description` / `step.Description` directly from the typed structs — it cannot become locale-dependent unless someone wires a translator into it. The earlier plan's "build a step, hash it, switch locale" regression test is meaningless (the hash function literally cannot see a locale). Replace with: a one-line code-level invariant comment at the top of `hash.go` (`// Hashes are computed from raw, untranslated fields; do not introduce locale-dependent inputs here.`) plus a code-review checklist note. No new test.

- [ ] design `Translator` interface in `internal/i18n` (the five typed helpers as methods). `*i18n.Store` satisfies it. Add `NopTranslator struct{}` with methods that just `return fallback`. Add `TranslatorOrNop(s *i18n.Store) Translator` helper
- [ ] thread `Translator` through signatures: `printRunHeader`, `paramFieldsFromDef`, `makeBrowserSelector`, tree builders. Nil is forbidden — callers pass `i18n.TranslatorOrNop(rflags.I18n)`
- [ ] add `Translator Translator` and `Locale string` fields to `RunContext`. `BuildRunContext` defaults them to `i18n.NopTranslator{}` and `""`
- [ ] **plumb `runCommandByID` signature**: add `Translator i18n.Translator` and `Locale string` to `runOpts` (`command_cmd.go:37`). The two call sites at `:93` and `:127` already have `rflags` in scope — they pass `Translator: i18n.TranslatorOrNop(rflags.I18n)` and `Locale: rflags.Locale` into `runOpts`. Inside `runCommandByID` (after the existing `BuildRunContext`), assign `ctx.Translator = opts.Translator` and `ctx.Locale = opts.Locale` before any rendering or confirmation
- [ ] **workflow manual sub-context** (`runner_workflow.go:293`): this site constructs `RunContext{}` field-by-field (NOT a struct copy), so Translator/Locale will be the zero values unless explicitly propagated. Add explicit `Translator: rc.Translator` and `Locale: rc.Locale` to the manual construction
- [ ] **belt-and-suspenders normalization** in `ConfirmCommand` (`confirmation.go:26`): on entry, if `ctx.Translator == nil`, set it to `i18n.NopTranslator{}`. Defends against any future manual-construction sites that forget to propagate
- [ ] update each enumerated site at `:247`, `:292`, `:317`, `:427`, `:590`, `:606`, `:677-678`, `:685`, `:911-912` to use the typed helpers
- [ ] update runtime confirmation at `confirmation.go:39` to use `ctx.Translator.CommandConfirmationText(...)` — covers workflow re-entry too
- [ ] at `:641-642` (completion): wrap via `i18n.TranslatorOrNop(rflags.I18n)` before calling the typed helpers — handles the nil-store case in `__complete` paths uniformly
- [ ] add the one-line invariant comment to `internal/deploy/journal/hash.go` documenting "no locale inputs here"
- [ ] write tests: fixture store with translations for one command + one group; `command list`, inspect, browser tree, and completion descriptions show translated strings; with no translation present, raw values are shown
- [ ] write workflow confirmation test: workflow step references a translated command, the confirmation prompt rendered inside the workflow uses the translated text (covers the `runner_workflow.go:300` copied-RunContext path)
- [ ] write completion-path test asserting nil `rflags.I18n` does not panic and returns raw descriptions
- [ ] run `go test ./internal/command/... ./internal/usercommands/...` — must pass before Task 8

### Task 8: Validator — `internal/validate/i18n` domain

**Files:**
- Create: `internal/validate/i18n/all.go`
- Create: `internal/validate/i18n/orphan.go`
- Create: `internal/validate/i18n/parse_error.go`
- Create: `internal/validate/i18n/unknown_ui_key.go`
- Create: `internal/validate/i18n/all_test.go`
- Modify: `internal/command/validate.go` — at the registry assembly site (around `:479` where `valchecks.AllForStage(...)` is called); the new `i18n.All(...)` call goes alongside. Load via `i18n.LoadProjectBundles(baseDir)` ONCE in `runValidate` and pass the result into both `i18n.All(...)` and (if useful) into other downstream consumers

**Why `LoadProjectBundles` instead of the merged `Store`:** the validator needs per-file path information (to point diagnostics at the right `devbox/i18n/<lang>.yml`), source-layer awareness (only project entries trigger orphan warnings — built-in entries are devbox's own and never reference user commands), and access to parse errors. The merged `Store` collapses all of this. `LoadProjectBundles` returns `[]ProjectFile{Path, Locale, Bundle, ParseErr}` — exactly what the validator needs without exporting `store.locales`.

**Signature parity reference:** look at `internal/validate/checks/loader.go` for the existing `All`/`AllForStage` shape. The new `i18n` domain takes `All(projectFiles []i18n.ProjectFile, loadErr error, reg *usercommands.Registry)` — `loadErr` is the aggregate error returned by `LoadProjectBundles`, threaded through so the parse-error validator can emit a single `i18n.load_error` diagnostic for the programmer-error case without the caller having to special-case it.

**Three validator kinds:**

1. **parse_error.go** — one `Diagnostic` per `ProjectFile` with non-nil `ParseErr`. Distinguish by the sentinel: `pf.Locale == ""` → `Severity: Error, Scope: "i18n.load_error"` (directory-level read failure, points at the dir); otherwise → `Severity: Error, Scope: "i18n.parse_error", File: pf.Path` (per-file strict-decode failure). Also: if the aggregate `error` from `LoadProjectBundles` is non-nil, the caller emits a single `Scope: "i18n.load_error"` diagnostic for the programmer-error case and skips this validator

2. **orphan.go** — for each project `ProjectFile` with a non-nil bundle, iterate `bundle.Commands` keys and `bundle.Groups` keys; for each `<id>` missing from `reg` → `Diagnostic{Severity: Warning, Scope: "i18n.orphan", File: pf.Path, ID: pf.Locale+"/"+id, ...}`. Hint: "translation references a command/group that no longer exists — rename or remove the entry"

3. **unknown_ui_key.go** — `Bundle.UI` is `map[string]string`, so `KnownFields(true)` does NOT catch typo'd keys like `ui.docs.section.properies`. Maintain an authoritative whitelist of `ui.*` keys (the same set built-in `en.yml` populates — defined as a package-level slice in `internal/i18n` so the validator and `en.yml` stay in sync). For each project `ProjectFile` UI key not in the whitelist → `Diagnostic{Severity: Warning, Scope: "i18n.unknown_ui_key", File: pf.Path, ...}`. Hint: "unknown ui key; if intentional, file a request to add it to the canonical set"

- [ ] implement `All(projectFiles []i18n.ProjectFile, loadErr error, reg *usercommands.Registry) []validate.Validator` aggregating the three validators; when `loadErr != nil` the parse-error validator emits a single `i18n.load_error` diagnostic in addition to any per-file diagnostics
- [ ] implement the three validators in their dedicated files
- [ ] in `internal/i18n` add a package-level `KnownUIKeys` slice (or set) — both `en.yml` ingest and the `unknown_ui_key` validator use this as the source of truth. Add a `coverage_test.go` (or extend Task 2's) asserting every key in `en.yml` is in `KnownUIKeys` and vice versa
- [ ] in `runValidate`: call `projectFiles, loadErr := i18n.LoadProjectBundles(baseDir)` once, then `i18n.All(projectFiles, loadErr, cmdReg)` alongside existing `valchecks` / `valenv` registrations
- [ ] do NOT register at `init()` — same convention as other domains
- [ ] tests:
  - parse error (per-file): malformed project file → one `i18n.parse_error` Error diagnostic with `File` set
  - load error (dir-level): unreadable `devbox/i18n/` directory → one `i18n.load_error` Error diagnostic
  - orphan: project translation references unknown command/group ID → Warning, suppressed for known IDs, per-locale disambiguation via `ID` field
  - unknown_ui_key: typo'd ui key in project file → Warning; key in whitelist → no diagnostic
  - empty project: no `devbox/i18n/` dir → zero diagnostics
- [ ] run `go test ./internal/validate/... ./internal/i18n/...` — must pass before Task 9

### Task 9: Reference documentation

**Files:**
- Create: `docs/reference/config/i18n.md`
- Modify: `docs/reference/config/notifications.md` (cross-reference `language` if helpful; keep notifications scope clean — main doc lives in `i18n.md`)
- Modify: `docs/reference/cli/` regenerated section for `docs generate` — handled by running `make build && bin/devbox docs generate --scope cli`

- [ ] write `docs/reference/config/i18n.md` covering: precedence, file format with full key reference (`ui.*`, `commands.<id>.*`, `groups.<id>.*`), project file location, examples of `en.yml` and `ru.yml`, behavior on missing locale, how `docs generate --lang` works (always emits `commands/<lang>/...`, no `--lang all` in v1)
- [ ] include section "Validation" linking to validator behavior (orphan warnings; unknown-field is a hard load-time error via strict YAML)
- [ ] mention the `DEVBOX_LANGUAGE` env var and the `language` key in userconfig
- [ ] regenerate CLI reference: `make build && bin/devbox docs generate --scope cli` to pick up the new `--lang` flag
- [ ] no tests required for docs task itself, but verify the regenerated CLI markdown reflects the new flag
- [ ] commit only the docs changes; do not move plan yet

### Task 10: Verify acceptance criteria

- [ ] verify all Overview items are implemented: command/param/confirmation translation in list / browser / inspect / docs, `ui.*` keys in docs generator, `--lang` flag, two-layer file load, validator domain
- [ ] verify edge cases: missing translation file (silent), unknown locale via `$LANG` (silent fallback to en), invalid `--lang` value (e.g. unknown-code or empty after normalization), userconfig load failure (silent warning, locale resolves to $LANG/en)
- [ ] run full test suite: `make test`
- [ ] run linter: `make lint`
- [ ] manually generate docs against a fixture project: `bin/devbox docs generate --scope commands --lang ru` and inspect output

### Task 11: [Final] Update CLAUDE.md / packages.md and move plan

**Files:**
- Modify: `docs/internals/packages.md` (add entry for `internal/i18n` and `internal/validate/i18n`)
- Modify: `AGENTS.md` (CLAUDE.md is a symlink) — add to Key Patterns if any non-obvious invariant emerged (probably one: "always look up display strings through `rflags.I18n.T(locale, key, def.Field)` — never read `def.Description` directly in display code")
- Move: `docs/plans/2026-05-26-localization.md` → `docs/plans/completed/2026-05-26-localization.md`

- [ ] update `docs/internals/packages.md` with the new packages and their cross-package contracts
- [ ] add a Key Patterns entry to `AGENTS.md` if a load-bearing invariant exists (display-string lookup convention)
- [ ] `mkdir -p docs/plans/completed && git mv docs/plans/2026-05-26-localization.md docs/plans/completed/`
- [ ] final commit message: `feat(i18n): localization for user commands and generated docs`

## Post-Completion

*Items requiring manual intervention or external systems — informational only.*

**Manual verification:**

- Generate docs against the user's real project: `bin/devbox docs generate --scope commands --lang ru` — confirm output structure, fallback behavior, index links.
- Run `bin/devbox command list` and the TUI browser in a project with a partial Russian translation file; verify translated and untranslated commands render correctly side-by-side.
- Set `LANG=ru_RU.UTF-8` (no userconfig change) and re-run; confirm Russian is picked up if `devbox/i18n/ru.yml` exists.

**Follow-up tracks (NOT part of this plan):**

- Populate actual Russian translations for built-in UI strings (`internal/i18n/translations/ru.yml`).
- Localize TUI labels (`command list` column headers, browser hints, inspect property names) — uses the same `ui.*` mechanism, just adds more keys.
- Localize built-in cobra command descriptions (the `builtin.commands.*` namespace is already reserved).
- Long-form per-command markdown overlays (consciously deferred during brainstorm).
