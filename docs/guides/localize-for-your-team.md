# Localize for your team

Your team works in Russian, German, French — any language other than English. DWE ships an English baseline for its own surface, but every user command you author (`dwe db.seed`, `dwe build.docker`, …) and every section header in generated command documentation can be translated per project, side-by-side with the English source.

This guide walks through where translations live, what gets translated and what stays English, a worked example, and how to switch locales without touching files. For the full key reference, see [i18n reference](../reference/config/i18n.md).

## Sections

- [Locale resolution](#locale-resolution)
- [File layout](#file-layout)
- [What gets translated](#what-gets-translated)
- [What does NOT get translated](#what-does-not-get-translated)
- [Worked example](#worked-example)
- [Validation](#validation)
- [Per-invocation switching](#per-invocation-switching)

## Locale resolution

DWE picks an active locale from four sources, highest precedence first:

1. **`--lang` flag** on any `dwe docs` subcommand (`show`, `list`, `search`, `export`, `generate`, `llms-txt`). Per-invocation, never stored.
2. **`DWE_LANGUAGE` environment variable.** Overrides the userconfig `language` field for one shell or one command.
3. **`language` field in userconfig** (`~/.config/dwe/config` or `.dwe/config`). Set this once per developer to persist a preferred locale.
4. **System `$LANG`** — parsed to a 2-letter code (`ru_RU.UTF-8` → `ru`). `C` and `POSIX` are ignored and the chain continues.
5. **Default:** `en`.

Codes like `ru-RU`, `ru_RU`, or `ru_RU.UTF-8` normalize to `ru` at every layer. Empty inputs are skipped.

In practice, most developers set `language: ru` (or whichever code) in their userconfig once and forget about it. `DWE_LANGUAGE` and `--lang` are escape hatches for one-off overrides.

## File layout

Translations are project-level YAML files under `workspace/i18n/`. There is no global or user-level translation directory — every project ships its own.

```
workspace/
  i18n/
    ru.yml
    de.yml
    fr.yml
```

The filename is the 2-letter language code. A missing `workspace/i18n/` directory is silently treated as "English only" — there is no error and no warning.

Translation files use **strict YAML decoding**: an unknown key (`descripton:` for `description:`, for example) fails to load and `dwe validate` surfaces a clear error. Typos do not silently slip through.

## What gets translated

The YAML translation store carries three buckets of strings:

| Key family | Covers |
|------------|--------|
| `commands.<id>.description` | the one-line description shown by `dwe commands list` and `dwe <id> --help` |
| `commands.<id>.confirmation_text` | the prompt shown before a destructive command runs (when `confirmation: true`) |
| `commands.<id>.messages.success` / `.error` | the lines printed on success/failure when a command sets `messages:` |
| `commands.<id>.params.<name>.description` | help text for each declared parameter |
| `commands.<id>.params.<name>.options.<value>` | per-value labels for enum-style params (e.g. `prod: "Production"`) |
| `groups.<id>.title` / `.description` | command-group title and description in the TUI browser |
| `ui.docs.section.*` / `ui.docs.property.*` | section headers and property labels emitted by `dwe docs generate` and the property tables shown by `dwe commands <id>` |

The command ID in `commands.<id>` is the same dotted form you type at the shell — `workspace/commands/db/seed.yml` is `commands.db.seed`. Group IDs follow the same path-based convention.

All fields are optional. Omit any key and DWE falls back to the English value from the command's source YAML (or the built-in English baseline for `ui.*`). Partial translations are fine — translated and untranslated strings coexist in the same output without warning.

## What does NOT get translated

Out of scope for the YAML store:

- **DWE's own Cobra commands.** `dwe deploy --help`, `dwe run --help`, `dwe docs --help`, and every other built-in command stays English regardless of locale. The translation store covers project user commands only.
- **Runtime error messages and logs.** These are machine-readable and always English so they can be grepped and pasted into issue trackers without surprise.
- **Long-form reference docs** under `docs/reference/` and `docs/internals/`. These are translated through a separate **markdown** namespace at `docs/i18n/<lang>/reference/...` and `docs/i18n/<lang>/internals/...`. Different loader, different validator, different file format — the two namespaces do not merge. See [i18n reference — long-form documentation translations](../reference/config/i18n.md#long-form-documentation-translations).

Keeping these scopes separate lets each one move independently: you can ship Russian translations for your team's `dwe db.seed` commands without waiting on a complete reference-docs translation.

## Worked example

**English source** — the canonical command YAML:

```yaml
# workspace/commands/db/seed.yml
- id: db.seed
  description: "Seed the database with fixture data"
  confirmation: true
  confirmation_text: "Wipe and re-seed the database?"
  messages:
    success: "Database seeded"
    error: "Seeding failed"
  params:
    - name: env
      description: "Target environment"
      options:
        - dev
        - staging
  type: service_exec
  service: db
  cmd: "/usr/local/bin/seed --env ${env}"
```

**Russian translation** — same shape under `commands.db.seed`:

```yaml
# workspace/i18n/ru.yml
ui:
  docs.section.parameters: "Параметры"
  docs.section.command: "Команда"
  docs.property.workdir: "Рабочая директория"

commands:
  db.seed:
    description: "Заполнить базу тестовыми данными"
    confirmation_text: "Очистить и заново заполнить базу?"
    messages:
      success: "База заполнена"
      error: "Не удалось заполнить базу"
    params:
      env:
        description: "Целевое окружение"
        options:
          dev: "Разработка"
          staging: "Стейджинг"

groups:
  db:
    title: "База данных"
    description: "Команды управления базой данных"
```

Three things to notice:

- The command source YAML stays 100% English. Authors and translators work on different files and never collide.
- `confirmation_text` is translated, but the underlying `confirmation: true` toggle stays in the source — translations carry *strings*, not behavior.
- The `ui:` block is optional. If you skip it, generated docs use the English section headers — partial coverage is fine.

After saving `ru.yml`, run:

```sh
DWE_LANGUAGE=ru dwe commands list   # → "Заполнить базу тестовыми данными"
DWE_LANGUAGE=ru dwe db.seed --help  # → translated description + params
```

## Validation

`dwe validate` checks every file under `workspace/i18n/`. Three classes of finding:

| Finding | Severity | What it means |
|---------|----------|---------------|
| parse error | error | strict decode caught an unknown field or YAML syntax error — fix the file |
| orphan entry | warning | `commands.<id>` references a command that no longer exists in `workspace/commands/` — rename or remove the entry |
| unknown UI key | warning | a `ui.*` key that is not in the canonical whitelist — open an issue if you need a new one |

Warnings are informational by default. To make them block CI:

```sh
dwe validate --strict
```

To target the i18n domain only:

```sh
dwe validate translations
```

Validation runs early (preflight) so a broken translation file does not surface deep in a deploy run.

## Per-invocation switching

For quick locale flips without editing config:

```sh
# One command in Russian
DWE_LANGUAGE=ru dwe commands list

# Whole shell session in German
export DWE_LANGUAGE=de
dwe commands list      # German
dwe db.seed --help     # German

# Explicit --lang on a docs subcommand always wins
DWE_LANGUAGE=de dwe docs show config/services/fields --lang ru
# → renders the Russian translation regardless of DWE_LANGUAGE
```

`DWE_LANGUAGE` slots **above** the userconfig `language` field and **below** the `--lang` flag. That ordering lets a developer pin their default in userconfig, override it for one terminal via the env var, and still get an explicit `--lang ru` for a one-off doc lookup.

## See also

- [i18n reference](../reference/config/i18n.md) — every key in the YAML store, validation rules, long-form markdown translations
- [userconfig reference](../reference/config/userconfig.md) — set `language:` per developer
- [author-project-commands](author-project-commands.md) — write the English command source that translations point at
- [brand-your-project](brand-your-project.md) — customize the dashboard header (separate from text translation)
