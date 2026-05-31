# Non-interactive Docs Commands

`devbox docs` subcommands for use in pipes, scripts, agents, and CI. Also covers mermaid diagram configuration and `mmdc` install paths.

## `devbox docs show <topic>`

Render a single documentation topic to stdout.

**Usage:**
```bash
devbox docs show <topic> [--lang <code>] [--raw] [--source all|devbox|project] [--anchors] [--toc]
```

**Arguments:**
- `<topic>` — Topic path, optionally with an anchor: `config/lifecycle`, `config/services/fields`, `config/devbox#binary-overrides`, `config/services/fields.md#ports-field`. Fuzzy matching supported (case-insensitive substring); multi-page topics like `config/services` are ambiguous on their own — pass the specific sub-page.

**Flags:**
- `--lang <code>` — Render in a specific language (2-letter code; e.g., `ru`, `de`). Defaults to the system locale or `en`.
- `--raw` — Output raw markdown (no syntax highlighting, no mermaid rendering). Useful for pipes and programmatic consumption.
- `--source <all|devbox|project>` — Search scope (default `all`). `devbox` searches only built-in docs; `project` searches only `./docs/`; `all` searches both.
- `--anchors` — Print every anchor slug for the topic (one per line) and exit. Useful for shell completion of `topic#anchor` forms.
- `--toc` — Print the topic's table of contents as TSV (`level\tslug\ttext`, one heading per line) and exit. Agent-friendly outline of the page.

**Output:**
- **TTY:** Glamour-rendered markdown with syntax highlighting. Mermaid diagrams are rendered to PNG and cached; inline display on capable terminals (kitty, ghostty, wezterm), system viewer fallback on others.
- **Pipe or `--raw`:** Raw markdown, no ANSI escapes.

**Examples:**
```bash
# Render in the current locale or English fallback
devbox docs show config/services/index

# Render in Russian (with stale-translation banner if applicable)
devbox docs show config/services/index --lang ru

# Render raw markdown (agent-friendly); scope to a specific section via anchor
devbox docs show config/services/fields --raw --lang en
devbox docs show config/devbox#binary-overrides --raw --lang en

# Show only built-in docs (skip project ./docs/)
devbox docs show config/services/fields --source devbox --lang en
```

## `devbox docs list`

List all available documentation topics (flat format).

**Usage:**
```bash
devbox docs list [--lang <code>] [--source all|devbox|project] [--match <glob>]
```

**Flags:**
- `--lang <code>` — Filter by language (default: active locale or `en`).
- `--source <all|devbox|project>` — Search scope (default `all`).
- `--match <glob>` — Filter topic paths by shell-style glob. `*` matches one path segment; `**` crosses `/`. Examples: `reference/config/*`, `reference/commands/**`.

**Output:**
Tab-separated columns (agent-friendly):
```
<source>	<path>	<language>
devbox	config/devbox	en
devbox	config/services/fields	en
devbox	config/services/fields	ru
project	guides/setup	en
```

**Example:**
```bash
$ devbox docs list
devbox	reference/config/devbox	en
devbox	reference/config/services/fields	en
devbox	reference/config/services/fields	ru
project	guides/getting-started	en
```

## `devbox docs export <dir>`

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

## `devbox docs llms-txt`

Emit a single [llms.txt](https://llmstxt.org/) document — a dense ~2-5KB index that gives an AI agent a complete picture of what this devbox project is and where to find more detail.

**Usage:**
```bash
devbox docs llms-txt                          # print to stdout
devbox docs llms-txt --output llms.txt        # write to file
devbox docs llms-txt --include-internals      # include internals/* topics
devbox docs llms-txt --no-project             # force project-agnostic output
devbox docs llms-txt --lang ru                # localize command descriptions
```

**Flags:**
- `--output PATH` — write to PATH instead of stdout. Parent directories are created as needed.
- `--lang CODE` — language for command descriptions. Defaults to user config / `$LANG` / `en`.
- `--include-internals` — include the `internals/` architecture docs in the Documentation section.
- `--no-project` — force the project-agnostic shape even when run inside a devbox project.

**Output shapes:**
- *Inside a project*: H1 with project name, a blockquote summary, then `## Project` (services, URLs, hosts), `## Commands` (user commands), `## Documentation` (topic links as `devbox-docs://path`), and `## Quick start`.
- *Outside a project* (or with `--no-project`): generic devbox reference — H1 "devbox", blockquote, `## Documentation`, `## Quick start`. No project-specific sections.

**Details:**
- Read-only. Acquires no project lock and runs no preflight; works without `devbox.yml`.
- Disabled services and private commands are excluded.
- The `devbox-docs://<path>` link scheme corresponds to topic paths consumable by `devbox docs show <path>`.

## `devbox docs cache clear`

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

## Mermaid diagrams

Mermaid-syntax diagrams (flowcharts, sequence diagrams, state machines, etc.) in documentation are rendered to PNG inline.

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

Configure with `docs.mermaid` in `devbox.yml`. See [Configuration reference](index.md#configuration-reference) for the schema.

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

## See also

- [Interactive TUI browser](browser.md) — `devbox docs` keys, layout, search
- [Translations and language behavior](translations.md) — locale resolution, staleness checks
