# Non-interactive Docs Commands

`dwe docs` subcommands for use in pipes, scripts, agents, and CI. Also covers mermaid diagram configuration and `mmdc` install paths.

## `dwe docs show <topic>`

Render a single documentation topic to stdout.

**Usage:**
```bash
dwe docs show <topic> [--lang <code>] [--raw] [--source all|dwe|project] [--anchors] [--toc]
```

**Arguments:**
- `<topic>` — Topic path, optionally with an anchor: `config/lifecycle`, `config/services/fields`, `config/workspace#field-reference`, `config/services/fields.md#ports-field`. Fuzzy matching supported (case-insensitive substring); multi-page topics like `config/services` are ambiguous on their own — pass the specific sub-page.

**Flags:**
- `--lang <code>` — Render in a specific language (2-letter code; e.g., `ru`, `de`). Defaults to the system locale or `en`.
- `--raw` — Output raw markdown (no syntax highlighting, no mermaid rendering). Useful for pipes and programmatic consumption.
- `--source <all|dwe|project>` — Search scope (default `all`). `dwe` searches only built-in docs; `project` searches only `./docs/`; `all` searches both.
- `--anchors` — Print every anchor slug for the topic (one per line) and exit. Useful for shell completion of `topic#anchor` forms.
- `--toc` — Print the topic's table of contents as TSV (`level\tslug\ttext`, one heading per line) and exit. Agent-friendly outline of the page.

**Output:**
- **TTY:** Glamour-rendered markdown with syntax highlighting. Mermaid diagrams are rendered to PNG and cached; inline display on capable terminals (kitty, ghostty, wezterm), system viewer fallback on others.
- **Pipe or `--raw`:** Raw markdown, no ANSI escapes.

**Examples:**
```bash
# Render in the current locale or English fallback
dwe docs show config/services/index

# Render in Russian (with stale-translation banner if applicable)
dwe docs show config/services/index --lang ru

# Render raw markdown (agent-friendly); scope to a specific section via anchor
dwe docs show config/services/fields --raw --lang en
dwe docs show config/workspace#field-reference --raw --lang en

# Show only built-in docs (skip project ./docs/)
dwe docs show config/services/fields --source dwe --lang en
```

## `dwe docs list`

List all available documentation topics (flat format).

**Usage:**
```bash
dwe docs list [--lang <code>] [--source all|dwe|project] [--match <glob>]
```

**Flags:**
- `--lang <code>` — Filter by language (default: active locale or `en`).
- `--source <all|dwe|project>` — Search scope (default `all`).
- `--match <glob>` — Filter topic paths by shell-style glob. `*` matches one path segment; `**` crosses `/`. Examples: `reference/config/*`, `reference/commands/**`.

**Output:**
Tab-separated columns (agent-friendly):
```
<source>	<path>	<language>
dwe	reference/config/workspace	en
dwe	reference/config/services/fields	en
dwe	reference/config/services/fields	ru
project	guides/setup	en
```

**Example:**
```bash
$ dwe docs list
dwe	reference/config/workspace	en
dwe	reference/config/services/fields	en
dwe	reference/config/services/fields	ru
project	guides/getting-started	en
```

## `dwe docs export <dir>`

Export all documentation topics to a directory on disk (useful for offline reading, publishing, or CI pipelines).

**Usage:**
```bash
dwe docs export <dir> [--lang <code>] [--include-project] [--include-internals] [--force]
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
dwe docs export ./docs-en/

# Export in Russian with project docs
dwe docs export ./docs-ru/ --lang ru --include-project

# Overwrite existing directory
dwe docs export ./docs-latest/ --force
```

## `dwe docs llms-txt`

Emit a single [llms.txt](https://llmstxt.org/) document — a dense ~2-5KB index that gives an AI agent a complete picture of what this DWE project is and where to find more detail.

**Usage:**
```bash
dwe docs llms-txt                          # print to stdout
dwe docs llms-txt --output llms.txt        # write to file
dwe docs llms-txt --include-internals      # include internals/* topics
dwe docs llms-txt --no-project             # force project-agnostic output
dwe docs llms-txt --lang ru                # localize command descriptions
```

**Flags:**
- `--output PATH` — write to PATH instead of stdout. Parent directories are created as needed.
- `--lang CODE` — language for command descriptions. Defaults to user config / `$LANG` / `en`.
- `--include-internals` — include the `internals/` architecture docs in the Documentation section.
- `--no-project` — force the project-agnostic shape even when run inside a DWE project.

**Output shapes:**
- *Inside a project*: H1 with project name, a blockquote summary, then `## Project` (services, URLs, hosts), `## Commands` (user commands), `## Documentation` (topic links as `dwe-docs://path`), and `## Quick start`.
- *Outside a project* (or with `--no-project`): generic DWE reference — H1 "dwe", blockquote, `## Documentation`, `## Quick start`. No project-specific sections.

**Details:**
- Read-only. Acquires no project lock and runs no preflight; works without `workspace.yml`.
- Disabled services and private commands are excluded.
- The `dwe-docs://<path>` link scheme corresponds to topic paths consumable by `dwe docs show <path>`.

## `dwe docs cache clear`

Remove all cached mermaid diagram renders.

**Usage:**
```bash
dwe docs cache clear
```

**Details:**
- Clears the XDG cache directory (`$XDG_CACHE_HOME/dwe/mermaid/` or fallback).
- Harmless if no cache exists.
- Cached diagrams are automatically regenerated on next view.

**Example:**
```bash
dwe docs cache clear
# → "Removed 42 cached diagrams"
```

## Mermaid diagrams

Mermaid-syntax diagrams (flowcharts, sequence diagrams, state machines, etc.) in documentation are rendered to PNG inline.

### Rendering modes

**`mermaid: auto` (default)**
- If `mmdc` is installed and accessible → render diagrams to PNG and cache them
- If `mmdc` is missing → degrade to inline placeholders of the form `📊 Diagram N/M — rendering disabled` (with a `y`-to-copy-source hint), plus a one-time startup banner: **⚠ `mmdc` not installed.** Mermaid diagrams cannot render. Install with `npm i -g @mermaid-js/mermaid-cli`
- No error; seamless fallback

**`mermaid: mmdc` (strict)**
- Require `mmdc` to be installed and accessible
- If missing → falls back to the same `📊 Diagram N/M — rendering disabled` placeholders and the same **⚠ `mmdc` not installed** startup banner as `auto` (no distinct strict-mode placeholder)
- Useful in CI/automated contexts where mermaid is a hard dependency

**`mermaid: off` (disabled)**
- Never render diagrams; always show raw mermaid blocks
- Useful for low-bandwidth or resource-constrained environments

Configure with `docs.mermaid` in `workspace.yml`. See [Configuration reference](index.md#configuration-reference) for the schema.

### Installing `mmdc`

`mmdc` (mermaid-cli) drives a headless Chromium via puppeteer. Two install paths:

- **npm (recommended)** — `npm i -g @mermaid-js/mermaid-cli`. Puppeteer manages its own Chromium download under `~/.cache/puppeteer/` and stays in sync with the installed mermaid-cli version. Upgrading is `npm update -g @mermaid-js/mermaid-cli`.
- **Homebrew** — `brew install mermaid-cli`. Works, but the formula pins a specific puppeteer version that expects an exact Chromium build; if your `~/.cache/puppeteer/` doesn't already contain that build the first render fails with `Could not find Chrome (ver. …)`. Fix by either running `npx puppeteer browsers install chrome@<version-from-error>` once, or switching to the npm install above which avoids the version-pinning problem entirely.

Verify the install with a one-off render outside DWE:

```sh
echo 'flowchart LR; A-->B' > /tmp/x.mmd
mmdc -i /tmp/x.mmd -o /tmp/x.png
```

If that produces a PNG, `dwe docs` will too.

### Diagram theme (`mermaid_theme`)

Override which mermaid theme is rendered, independent of the terminal background. Set in the user-config file (`~/.config/dwe/config` global, `.dwe/config` per-project, env var wins).

| Key | Type | Default | Values |
|---|---|---|---|
| `mermaid_theme` | string | `auto` | `auto` / `dark` / `light` |

- `auto` — probe the terminal background and pick a matching theme.
- `dark` / `light` — hard-pin the theme. Useful for transparent terminals where background detection is unreliable, or to standardise the cached PNGs across machines.

Env override: `DWE_MERMAID_THEME=dark`. The chosen theme is part of the cache key, so flipping the value re-renders rather than serving a wrong-themed PNG.

### Cache management

Diagram PNG files are cached under `$XDG_CACHE_HOME/dwe/mermaid/` (or system temp dir as fallback).

Cache key includes the mermaid source, rendering width, theme (dark/light), and `mmdc` version — so upgrading mermaid-cli automatically invalidates old renders.

**LRU eviction:** When the cache exceeds the configured size, oldest diagrams (by last-accessed time) are deleted.

Clear the cache manually:
```bash
dwe docs cache clear
```

## See also

- [Interactive TUI browser](browser.md) — `dwe docs` keys, layout, search
- [Translations and language behavior](translations.md) — locale resolution, staleness checks
