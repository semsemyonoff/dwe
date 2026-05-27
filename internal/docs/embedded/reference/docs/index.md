# Documentation Subsystem

Devbox includes an integrated documentation browser and reference system. Access embedded built-in docs, project-specific docs (when present), render markdown with syntax highlighting, and view mermaid diagrams inline.

## Contents

- [Quick start](#quick-start)
- [Commands](#commands)
  - [`devbox docs` — Interactive TUI browser](#devbox-docs--interactive-tui-browser)
  - [`devbox docs show <topic>` — Render a single topic](#devbox-docs-show-topic--render-a-single-topic)
  - [`devbox docs list` — List all topics](#devbox-docs-list--list-all-topics)
  - [`devbox docs export <dir>` — Export all docs to disk](#devbox-docs-export-dir--export-all-docs-to-disk)
  - [`devbox docs cache clear` — Clear the mermaid cache](#devbox-docs-cache-clear--clear-the-mermaid-cache)
- [Language behavior](#language-behavior)
  - [Locale resolution](#locale-resolution)
  - [Long-form documentation translations](#long-form-documentation-translations)
  - [Translation file layout](#translation-file-layout)
  - [Content-hash staleness check](#content-hash-staleness-check)
- [Mermaid diagrams](#mermaid-diagrams)
  - [Configuration](#configuration)
  - [Rendering modes](#rendering-modes)
  - [Cache management](#cache-management)
- [Project docs](#project-docs)
- [Configuration reference](#configuration-reference)

## Quick start

View docs interactively:
```bash
devbox docs
```

Show a specific topic in the terminal:
```bash
devbox docs show config/services
devbox docs show config/devbox#binaries
```

List all available topics:
```bash
devbox docs list
```

Export all docs to a directory:
```bash
devbox docs export ./my-docs
```

## Commands

### `devbox docs` — Interactive TUI browser

Open an interactive terminal UI to browse all documentation.

**Usage:**
```bash
devbox docs
```

**Requirements:**
- TTY (terminal) — non-interactive use (pipes, agents) must use `devbox docs show <topic>` or `devbox docs list` instead.

**Navigation:**
- `j` / `k` — Move up/down in the left tree pane
- `h` / `l` — Collapse/expand folders
- `Tab` — Switch focus between tree (left) and content (right)
- `Enter` — Open a topic
- `/` — Search headings (within the current document)
- `n` / `N` — Jump to next/previous search result
- `]d` / `[d` — Navigate to next/previous mermaid diagram
- `d` / `Enter` on diagram — View diagram inline (if supported) or open in system viewer
- `o` — Always open diagram in system viewer
- `y` — Copy diagram source code via clipboard (OSC 52)
- `L` — Cycle through available languages for the current topic
- `e` — Jump to English original (when viewing a translation)
- `r` — Reload the current topic (picks up live edits in project docs)
- `?` — Show help
- `q` — Quit

**Layout:**
- **Left pane:** Collapsible file tree of all topics (Devbox built-ins and Project docs if present)
- **Right pane:** Rendered markdown content with syntax highlighting
- **Status bar:** Current topic path, mermaid progress, active language, and keyboard hints

### `devbox docs show <topic>` — Render a single topic

Render a single documentation topic to stdout.

**Usage:**
```bash
devbox docs show <topic> [--lang <code>] [--raw] [--source all|devbox|project]
```

**Arguments:**
- `<topic>` — Topic path, optionally with an anchor: `config/services`, `config/services#binaries`, `config/services.md#binaries`. Fuzzy matching supported (case-insensitive substring).

**Flags:**
- `--lang <code>` — Render in a specific language (2-letter code; e.g., `ru`, `de`). Defaults to the system locale or `en`.
- `--raw` — Output raw markdown (no syntax highlighting, no mermaid rendering). Useful for pipes and programmatic consumption.
- `--source <all|devbox|project>` — Search scope (default `all`). `devbox` searches only built-in docs; `project` searches only `./docs/`; `all` searches both.

**Output:**
- **TTY:** Glamour-rendered markdown with syntax highlighting. Mermaid diagrams are rendered to PNG and cached; inline display on capable terminals (kitty, ghostty, wezterm), system viewer fallback on others.
- **Pipe or `--raw`:** Raw markdown, no ANSI escapes.

**Examples:**
```bash
# Render in the current locale or English fallback
devbox docs show config/services

# Render in Russian (with stale-translation banner if applicable)
devbox docs show config/services --lang ru

# Render raw markdown (agent-friendly)
devbox docs show config/services --raw

# Show only built-in docs (skip project ./docs/)
devbox docs show config/services --source devbox
```

### `devbox docs list` — List all topics

List all available documentation topics (flat format).

**Usage:**
```bash
devbox docs list [--lang <code>] [--source all|devbox|project]
```

**Flags:**
- `--lang <code>` — Filter by language (default: active locale or `en`).
- `--source <all|devbox|project>` — Search scope (default `all`).

**Output:**
Tab-separated columns (agent-friendly):
```
<source>	<path>	<language>
devbox	config/devbox	en
devbox	config/services	en
devbox	config/services	ru
project	guides/setup	en
```

**Example:**
```bash
$ devbox docs list
devbox	reference/config/devbox	en
devbox	reference/config/services	en
devbox	reference/config/services	ru
project	guides/getting-started	en
```

### `devbox docs export <dir>` — Export all docs to disk

Export all documentation topics to a directory on disk (useful for offline reading, publishing, or CI pipelines).

**Usage:**
```bash
devbox docs export <dir> [--lang <code>] [--include-project] [--include-internals] [--force]
```

**Arguments:**
- `<dir>` — Target directory (will be created if missing).

**Flags:**
- `--lang <code>` — Export language (default: active locale or `en`). Per-file fallback: missing translation → English with a banner.
- `--include-project` — Include `./docs/` (project-local documentation).
- `--include-internals` — Include `docs/internals/` (architecture / developer docs).
- `--force` — Overwrite non-empty target directory.

**Output:**
Markdown files (with mermaid blocks preserved as source — IDE-friendly). Non-translated files include a note:
```
> **Note:** This file is not translated to `ru`. Original English version below.
```

**Examples:**
```bash
# Export built-in reference docs (English)
devbox docs export ./docs-en/

# Export in Russian with project docs
devbox docs export ./docs-ru/ --lang ru --include-project

# Overwrite existing directory
devbox docs export ./docs-latest/ --force
```

### `devbox docs cache clear` — Clear the mermaid cache

Remove all cached mermaid diagram renders.

**Usage:**
```bash
devbox docs cache clear
```

**Details:**
- Clears the XDG cache directory (`$XDG_CACHE_HOME/devbox/mermaid/` or fallback).
- Harmless if no cache exists.
- Cached diagrams are automatically regenerated on next view.

**Example:**
```bash
devbox docs cache clear
# → "Removed 42 cached diagrams"
```

## Language behavior

### Locale resolution

Devbox picks an active locale via the precedence chain:

1. **`--lang` flag** (on `docs show` / `docs export` / `docs list`; per-invocation)
2. **`DEVBOX_LANGUAGE` environment variable**
3. **`language` setting in userconfig** (`~/.config/devbox/config` or `.devbox/config`)
4. **System `$LANG`** (parsed to 2-letter code)
5. **Default:** `en` (English)

Per-file fallback: if a markdown file is not translated to the resolved locale, Devbox automatically uses the English version and displays an info banner.

### Long-form documentation translations

See [Localization (i18n) — Long-form documentation translations](../config/i18n.md#long-form-documentation-translations) for complete details on the two i18n namespaces and translation file layout.

### Translation file layout

Long-form documentation translations live in a separate namespace from command/UI strings:

```
docs/
  reference/               # English built-ins
    config/
      devbox.md
    ...
  internals/
    architecture.md
    ...
  i18n/
    ru/                    # Russian translations
      reference/
        config/
          devbox.md        # Translated version
      internals/
        ...
    de/                    # German translations
      ...
```

Translations are **optional**; missing translations fall back to English with an info banner.

### Content-hash staleness check

Each translated markdown file includes a header line that records the SHA256 hash of the English version at translation time:

```markdown
> Translated from: config/devbox @ a1b2c3d4e5f6

# Devbox Configuration
...
```

When you view a translation, Devbox compares this hash against the embedded manifest (generated at build time). If they differ, the translation is marked **stale** and a warning banner appears:

```
⚠ This translation is outdated (last synced at <hash>, current is <hash>). Press `e` to view the English version.
```

Translators can update the hash as part of their pull request; Devbox re-generates the manifest at the next `make build`.

## Mermaid diagrams

Mermaid-syntax diagrams (flowcharts, sequence diagrams, state machines, etc.) in documentation are rendered to PNG inline.

### Configuration

Three settings control mermaid behavior. Add them to your `devbox.yml`:

```yaml
binaries:
  mmdc: mmdc  # Path or name of mermaid-cli (optional; default "mmdc" on $PATH)

docs:
  mermaid: auto       # "auto" | "mmdc" | "off" (default: "auto")
  cache_size_mb: 100  # Max size of diagram cache (default: 100)
```

### Rendering modes

**`mermaid: auto` (default)**
- If `mmdc` is installed and accessible → render diagrams to PNG and cache them
- If `mmdc` is missing → degrade to raw mermaid source blocks with a hint (`📊 [mmdc not installed — Y to copy]`)
- No error; seamless fallback

**`mermaid: mmdc` (strict)**
- Require `mmdc` to be installed and accessible
- If missing → show a placeholder (`📊 [mmdc required but not found]`) and log a warning at startup
- Useful in CI/automated contexts where mermaid is a hard dependency

**`mermaid: off` (disabled)**
- Never render diagrams; always show raw mermaid blocks
- Useful for low-bandwidth or resource-constrained environments

### Installing `mmdc`

`mmdc` (mermaid-cli) drives a headless Chromium via puppeteer. Two install paths:

- **npm (recommended)** — `npm i -g @mermaid-js/mermaid-cli`. Puppeteer manages its own Chromium download under `~/.cache/puppeteer/` and stays in sync with the installed mermaid-cli version. Upgrading is `npm update -g @mermaid-js/mermaid-cli`.
- **Homebrew** — `brew install mermaid-cli`. Works, but the formula pins a specific puppeteer version that expects an exact Chromium build; if your `~/.cache/puppeteer/` doesn't already contain that build the first render fails with `Could not find Chrome (ver. …)`. Fix by either running `npx puppeteer browsers install chrome@<version-from-error>` once, or switching to the npm install above which avoids the version-pinning problem entirely.

Verify the install with a one-off render outside Devbox:

```sh
echo 'flowchart LR; A-->B' > /tmp/x.mmd
mmdc -i /tmp/x.mmd -o /tmp/x.png
```

If that produces a PNG, `devbox docs` will too.

### Diagram theme (`mermaid_theme`)

Override which mermaid theme is rendered, independent of the terminal background. Set in the user-config file (`~/.config/devbox/config` global, `.devbox/config` per-project, env var wins).

| Key | Type | Default | Values |
|---|---|---|---|
| `mermaid_theme` | string | `auto` | `auto` / `dark` / `light` |

- `auto` — probe the terminal background and pick a matching theme.
- `dark` / `light` — hard-pin the theme. Useful for transparent terminals where background detection is unreliable, or to standardise the cached PNGs across machines.

Env override: `DEVBOX_MERMAID_THEME=dark`. The chosen theme is part of the cache key, so flipping the value re-renders rather than serving a wrong-themed PNG.

### Cache management

Diagram PNG files are cached under `$XDG_CACHE_HOME/devbox/mermaid/` (or system temp dir as fallback).

Cache key includes the mermaid source, rendering width, theme (dark/light), and `mmdc` version — so upgrading mermaid-cli automatically invalidates old renders.

**LRU eviction:** When the cache exceeds the configured size, oldest diagrams (by last-accessed time) are deleted.

Clear the cache manually:
```bash
devbox docs cache clear
```

## Project docs

Devbox automatically detects and includes documentation from your project's `./docs/` directory.

When `./docs/` exists, the TUI browser shows a second top-level branch:
```
Devbox
  reference/
  internals/
Project
  guides/
  examples/
```

If `./docs/` is absent or empty, the Project branch is omitted.

**Structure:**
```
<project>/
  docs/
    guides/
      setup.md
    examples/
      docker.md
    README.md
```

**Hot reload:** Edit files in `./docs/` while the TUI is open; the viewport updates automatically (press `r` to manually reload if auto-detect misses an edit).

## Configuration reference

### `binaries.mmdc`

Path or name of the mermaid-cli executable. Defaults to `mmdc` (searched on `$PATH`).

```yaml
binaries:
  mmdc: /usr/local/bin/mmdc   # Absolute path
  mmdc: mmdc                   # Name on $PATH (default)
```

### `docs.mermaid`

Mermaid rendering mode. One of:
- `auto` — Render if `mmdc` available; fall back to raw blocks if missing (default)
- `mmdc` — Require `mmdc`; error placeholder if missing
- `off` — Never render; always show raw mermaid blocks

Default: `auto`

```yaml
docs:
  mermaid: auto   # Default
  mermaid: mmdc   # Strict mode
  mermaid: off    # Disabled
```

### `docs.cache_size_mb`

Maximum size of the mermaid diagram cache in MB. Defaults to `100`.

```yaml
docs:
  cache_size_mb: 50   # 50 MB cache
  cache_size_mb: 500  # 500 MB cache
```

Once the cache exceeds this limit, oldest diagrams are automatically deleted (LRU eviction).

## Related commands

- `devbox docs generate` — Generate markdown documentation from user commands (existing; unchanged)
- `devbox validate` — Check for documentation errors and orphan translation entries
- `devbox command list` — List user commands with translated descriptions
