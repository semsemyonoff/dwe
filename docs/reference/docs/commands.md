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

Anchors printed by `--anchors` / `--toc` are exactly the strings `topic#anchor` accepts — including the underscores in every builtin name and snake_case key (`config/deploy/builtins#service_dirs_ensure`). Take them from that listing rather than deriving them from the rendered heading text.

**Output:**
- **TTY:** Glamour-rendered markdown with syntax highlighting. Mermaid diagrams are rendered to PNG and cached; inline display on capable terminals (kitty, ghostty, wezterm), system viewer fallback on others.
- **Pipe or `--raw`:** Raw markdown, no ANSI escapes.
- **stderr:** when a *whole* long document (≥ 120 lines and ≥ 4 sections) is piped or captured rather than shown on a terminal, one note names `--toc` and the `topic#anchor` form. It is silent on a TTY, when an anchor was requested, and under `--anchors`/`--toc`. It goes to stderr so that `dwe docs show <topic> | head` — the case it addresses — still receives it.

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

## `dwe docs search <query>`

Search every documentation topic and emit the sections that contain the query. Built for pipes, scripts, agents, and CI.

**Usage:**
```bash
dwe docs search <query> [--literal] [--source all|dwe|project] [--lang <code>] [--limit <n>] [--output text|json] [--pretty]
```

**Arguments:**
- `<query>` — One or more words. The query is split on whitespace and **every** word must be present for a section to match (AND). Each word matches as a case-insensitive **substring**, so identifiers work (`depends_on:`, `RunContext.Render`). Matches inside fenced code blocks are counted too — that's where schema names usually appear.

**Flags:**
- `--literal` — Match the whole query as one substring instead of splitting it into words. Needed because `docs search` takes exactly one argument and the shell strips quotes, so `'a b'` and `a b` arrive identical — quoting cannot select literal mode.
- `--source <all|dwe|project>` — Doc source (default `all`). `dwe` searches only built-in docs; `project` searches only `./docs/`; `all` searches both.
- `--lang <code>` — Language code (default: active locale or `en`).
- `--limit <n>` — Maximum result rows (default `50`; `0` = unlimited).
- `--output <text|json>` — Output format (global flag; default `text`).
- `--pretty` — Pretty-print JSON output (only with `--output json`).

**Matching:**
- **Substring, not word-boundary.** The known trade-off: a short word matches inside a longer one — `uid` also matches `guide`/`guides`, `env` also matches `environment`. Deliberate, because word-boundary matching would break `depends_on:`.
- **Two tiers.** First, sections that contain every word. Then, for a document where *no* section holds them all but the document as a whole does, one row anchored at its densest section — a page explaining a pair of concepts in two adjacent sections would otherwise be invisible to the query naming both. The tier is a **tie-break, not the primary sort key**: `<count>` decides first, and a tier-1 row outranks a tier-2 row only when the two match equally often — so a page matching four times still leads a section matching once.
- **`<count>` is the rarest word's occurrences, not the total.** Summing would let a section with 40 hits of `vars` and one of `interpolation` outrank the section actually about the pair; it also makes a repeated word (`vars vars`) harmless.

**Output:**
- **`text` (default):** Tab-separated, one row per matching section: `<source>\t<path>#<anchor>\t<count>\t<snippet>`. Rows are sorted by match count (descending), then by tier, then by path, anchor and source. Lead text under the H1 (before the first H2) is reported with an empty anchor.
- **`--output json`:** A JSON array of `{source, path, anchor, count, snippet}` records (path and anchor are split; anchor is empty for lead text under the H1 before the first H2/H3).
- **`<snippet>`** is the source line carrying the most distinct words of the query (densest line, first wins ties), so a hit is actionable without a second `docs show`. It is whitespace-collapsed (tabs and newlines removed — markdown tables contain both) and capped at 160 bytes on a rune boundary, so a TSV row can never gain a fifth field. The column is **append-only**: a consumer reading fields `[0..2]` is unaffected.
- **Zero matches:** stdout stays empty (text) or `[]` (JSON) and the exit code stays 0. In text mode a one-line notice goes to **stderr** naming the query, the active `--source` and the resolved locale — the filters that most often produce a false empty result — and suggests dropping a word (or dropping `--literal`, when that flag is what produced the empty result). JSON mode emits no notice, so a piped consumer sees byte-identical output either way.

**Examples:**
```bash
dwe docs search depends_on
dwe docs search 'RunContext.Render' --source dwe --literal
dwe docs search 'UID GID env' --lang en --limit 5
dwe docs search topo-sort --lang en --limit 5
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

Emit a single [llms.txt](https://llmstxt.org/) document — a dense briefing that gives an AI agent a complete picture of what this DWE project is and where to find more detail. The project-agnostic part is capped at 12KB (enforced by a test on `--no-project`); the project-aware document adds services, commands and URLs on top and therefore grows with the workspace.

**Usage:**
```bash
dwe docs llms-txt                          # print to stdout
dwe docs llms-txt --out llms.txt           # write to file
dwe docs llms-txt --include-internals      # include internals/* topics
dwe docs llms-txt --no-project             # force project-agnostic output
dwe docs llms-txt --lang ru                # localize command descriptions
```

**Flags:**
- `--out PATH` — write to PATH instead of stdout. Parent directories are created as needed. Named `--out` (like `dwe docs generate`) so it does not shadow the global `--output`/`-o` format flag.
- `--lang CODE` — language for command descriptions. Defaults to user config / `$LANG` / `en`.
- `--include-internals` — include the `internals/` architecture docs in the Documentation section.
- `--no-project` — force the project-agnostic shape even when run inside a DWE project.

**Output shapes:**
- *Inside a project*: H1 with project name, a blockquote summary, then `## Project` (services, URLs, hosts), `## Commands` (user commands), the briefing sections below, `## Documentation` (topic links as `dwe-docs://path`), and `## Quick start`.
- *Outside a project* (or with `--no-project`): generic DWE reference — H1 "dwe", blockquote, the briefing sections, `## Documentation`, `## Quick start`. No project-specific sections.

**Briefing sections** (identical in both shapes — they describe DWE itself):
- `## Builtins` — every registered step builtin (`name — kind — purpose`, including `internal` ones), then the disjoint `when:` predicate registry. The two registries share the word "builtin" and accept nothing from each other; the section says so explicitly.
- `## Template syntax by site` — which of `${...}` / `{{ ... }}` is evaluated where, and which `${...}` namespaces are unavailable in pipeline fields.
- `## Diagnostics and machine-readable output` — `--quiet`, `--level`, `-v`/`--debug`, `docs show --toc`/`--anchors`, and the `-o json` exceptions.
- `## Reserved env names` — the names `dwe render env` always emits itself (`PROJECT`, `UID`, `GID`), which `exports.env` rules may not redeclare.

**Details:**
- Read-only. Acquires no project lock and runs no preflight; works without `workspace.yml`.
- The global `--output text|json` (`-o`) flag is accepted but has no effect here — like `dwe docs show`, this command always emits markdown. Use `--out PATH` to write it to a file.
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

- **npm (recommended)** — `npm i -g @mermaid-js/mermaid-cli`. Puppeteer downloads its own Chromium under `~/.cache/puppeteer/` at install time. Upgrading is `npm update -g @mermaid-js/mermaid-cli`.
- **Homebrew** — `brew install mermaid-cli`. The formula pins a specific puppeteer version that expects an exact Chromium build.

Either way, the bundled puppeteer expects a **specific** Chromium build. If `~/.cache/puppeteer/` doesn't contain it — a cleared cache, a `--ignore-scripts` install, or a mermaid-cli upgrade that bumped the expected version without re-fetching the browser — every render fails with `Could not find Chrome (ver. …)` even though `mmdc` itself is on `$PATH`. The browser cache can go missing regardless of which install path you used.

The obvious fix backfires in two ways, and the error's own advice (`npx puppeteer browsers install chrome-headless-shell`) walks into both:

- **Wrong product** — mermaid-cli launches with `headless: 'shell'`, so it needs the **`chrome-headless-shell`** build, *not* full `chrome`. The error message says "Chrome" even while it is resolving `chrome-headless-shell`, so installing `chrome@<ver>` leaves the render failing with the identical error.
- **Wrong version** — a bare `npx puppeteer browsers install …` runs the *latest* standalone puppeteer, which pins a **newer** Chromium build than the (often older) puppeteer-core bundled inside your mermaid-cli. mermaid-cli only ever looks for the exact build it pins, so the newer download sits unused and the render still fails.

Fix it (install-method-agnostic) by installing the exact product **and** the version the error names — always pin `@<version-from-error>`:

```sh
npx @puppeteer/browsers install chrome-headless-shell@<version-from-error>
```

In `dwe docs`, when a diagram shows `📊 Diagram N/M — render failed`, put the cursor on it and press `E` to open the full mmdc error (it names the missing Chrome version to pass above). Pinning the version sidesteps the standalone-puppeteer drift; naming `chrome-headless-shell` matches what `headless: 'shell'` actually launches.

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
