# Translations and Language Behavior

How Devbox resolves the active locale, where translated long-form docs live on disk, and how the content-hash staleness check keeps translations honest.

## Locale resolution

Devbox picks an active locale via the precedence chain:

1. **`--lang` flag** (on `docs show` / `docs export` / `docs list`; per-invocation)
2. **`DWE_LANGUAGE` environment variable**
3. **`language` setting in userconfig** (`~/.config/devbox/config` or `.devbox/config`)
4. **System `$LANG`** (parsed to 2-letter code)
5. **Default:** `en` (English)

Per-file fallback: if a markdown file is not translated to the resolved locale, Devbox automatically uses the English version and displays an info banner.

## Long-form documentation translations

See [Localization (i18n) — Long-form documentation translations](../config/i18n.md#long-form-documentation-translations) for complete details on the two i18n namespaces and translation file layout.

## Translation file layout

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

## Content-hash staleness check

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

## See also

- [Interactive TUI browser](browser.md) — keys `L` (cycle languages) and `e` (jump to English original)
- [Non-interactive commands](commands.md) — `--lang` flag on `show`, `list`, `export`, `llms-txt`
- [Overview](index.md) — quick start, project docs, configuration reference
