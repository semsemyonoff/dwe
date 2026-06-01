# Documentation Subsystem

DWE includes an integrated documentation browser and reference system. Access embedded built-in docs, project-specific docs (when present), render markdown with syntax highlighting, and view mermaid diagrams inline.

## Pages

- [Interactive TUI browser](browser.md) — `dwe docs` keys, layout, search
- [Non-interactive commands](commands.md) — `show`, `list`, `export`, `llms-txt`, `cache clear`, plus mermaid configuration and `mmdc` install
- [Translations and language behavior](translations.md) — locale resolution, file layout, content-hash staleness check

## Quick start

View docs interactively:
```bash
dwe docs
```

Show a specific topic in the terminal:
```bash
dwe docs show config/services/fields
dwe docs show config/workspace#binary-overrides
```

List all available topics:
```bash
dwe docs list
```

Export all docs to a directory:
```bash
dwe docs export ./my-docs
```

## Project docs

DWE automatically detects and includes documentation from your project's `./docs/` directory.

When `./docs/` exists, the TUI browser shows a second top-level branch:
```
DWE
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

- `dwe docs generate` — Generate markdown documentation from user commands
- `dwe validate` — Check for documentation errors and orphan translation entries
- `dwe command list` — List user commands with translated descriptions
