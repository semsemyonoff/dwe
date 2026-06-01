> Translated from: reference/docs/commands.md @ 4b6c920ee97c

# Неинтерактивные команды документации

Подкоманды `dwe docs` для использования в пайпах, скриптах, агентах и CI. Также описывают настройку mermaid-диаграмм и пути установки `mmdc`.

## `dwe docs show <topic>`

Рендер одной темы документации в stdout.

**Использование:**
```bash
dwe docs show <topic> [--lang <code>] [--raw] [--source all|dwe|project] [--anchors] [--toc]
```

**Аргументы:**
- `<topic>` — путь темы, опционально с якорем: `config/lifecycle`, `config/services/fields`, `config/workspace#binary-overrides`, `config/services/fields.md#ports-field`. Поддерживается нечёткое сопоставление (нечувствительный к регистру поиск по подстроке); многостраничные темы вроде `config/services` неоднозначны сами по себе — указывайте конкретную подстраницу.

**Флаги:**
- `--lang <code>` — рендер на конкретном языке (2-буквенный код; например `ru`, `de`). По умолчанию — системная локаль или `en`.
- `--raw` — вывод сырого markdown (без подсветки синтаксиса, без рендера mermaid). Полезно для пайпов и программного потребления.
- `--source <all|dwe|project>` — область поиска (по умолчанию `all`). `dwe` ищет только во встроенных доках; `project` — только в `./docs/`; `all` — в обоих.
- `--anchors` — вывести все якоря темы (по одному в строке) и выйти. Полезно для shell-автодополнения форм `topic#anchor`.
- `--toc` — вывести оглавление темы как TSV (`level\tslug\ttext`, по одному заголовку в строке) и выйти. Удобная для агентов схема страницы.

**Вывод:**
- **TTY:** отрендеренный через glamour markdown с подсветкой синтаксиса. Mermaid-диаграммы рендерятся в PNG и кешируются; inline-отображение в способных терминалах (kitty, ghostty, wezterm), fallback на системный просмотрщик в остальных.
- **Пайп или `--raw`:** сырой markdown без ANSI-escape.

**Примеры:**
```bash
# Рендер в текущей локали или fallback на английский
dwe docs show config/services/index

# Рендер на русском (с плашкой об устаревшем переводе, если применимо)
dwe docs show config/services/index --lang ru

# Рендер сырого markdown (удобно для агентов); скоуп на секцию через якорь
dwe docs show config/services/fields --raw --lang en
dwe docs show config/workspace#binary-overrides --raw --lang en

# Показать только встроенные доки (пропустить проектные ./docs/)
dwe docs show config/services/fields --source dwe --lang en
```

## `dwe docs list`

Вывести список всех доступных тем документации (плоский формат).

**Использование:**
```bash
dwe docs list [--lang <code>] [--source all|dwe|project] [--match <glob>]
```

**Флаги:**
- `--lang <code>` — фильтр по языку (по умолчанию: активная локаль или `en`).
- `--source <all|dwe|project>` — область поиска (по умолчанию `all`).
- `--match <glob>` — фильтр путей тем по shell-glob. `*` совпадает с одним сегментом; `**` пересекает `/`. Примеры: `reference/config/*`, `reference/commands/**`.

**Вывод:**
Колонки через табуляцию (удобно для агентов):
```
<source>	<path>	<language>
dwe	config/workspace	en
dwe	config/services/fields	en
dwe	config/services/fields	ru
project	guides/setup	en
```

**Пример:**
```bash
$ dwe docs list
dwe	reference/config/workspace	en
dwe	reference/config/services/fields	en
dwe	reference/config/services/fields	ru
project	guides/getting-started	en
```

## `dwe docs export <dir>`

Экспортировать все темы документации в каталог на диске (полезно для офлайн-чтения, публикации или CI-пайплайнов).

**Использование:**
```bash
dwe docs export <dir> [--lang <code>] [--include-project] [--include-internals] [--force]
```

**Аргументы:**
- `<dir>` — целевой каталог (будет создан, если отсутствует).

**Флаги:**
- `--lang <code>` — язык экспорта (по умолчанию: активная локаль или `en`). Пофайловый fallback: отсутствующий перевод → английский с плашкой.
- `--include-project` — включить `./docs/` (проектная документация).
- `--include-internals` — включить `docs/internals/` (архитектурные/разработческие доки).
- `--force` — перезаписать непустой целевой каталог.

**Вывод:**
Markdown-файлы (с сохранёнными mermaid-блоками как исходник — удобно для IDE). Непереведённые файлы включают заметку:
```
> **Note:** This file is not translated to `ru`. Original English version below.
```

**Примеры:**
```bash
# Экспорт встроенных справочных доков (английский)
dwe docs export ./docs-en/

# Экспорт на русском с проектными доками
dwe docs export ./docs-ru/ --lang ru --include-project

# Перезаписать существующий каталог
dwe docs export ./docs-latest/ --force
```

## `dwe docs llms-txt`

Сгенерировать один документ [llms.txt](https://llmstxt.org/) — плотный индекс ~2–5 КБ, дающий AI-агенту полную картину того, что представляет собой данный DWE-проект и где искать подробности.

**Использование:**
```bash
dwe docs llms-txt                          # печать в stdout
dwe docs llms-txt --output llms.txt        # запись в файл
dwe docs llms-txt --include-internals      # включить темы internals/*
dwe docs llms-txt --no-project             # принудительно сгенерировать project-agnostic вывод
dwe docs llms-txt --lang ru                # локализовать описания команд
```

**Флаги:**
- `--output PATH` — записать в PATH вместо stdout. Родительские каталоги создаются по необходимости.
- `--lang CODE` — язык описаний команд. По умолчанию — пользовательская конфигурация / `$LANG` / `en`.
- `--include-internals` — включить архитектурные доки `internals/` в раздел Documentation.
- `--no-project` — принудительно вывести project-agnostic форму даже внутри dwe-проекта.

**Формы вывода:**
- *Внутри проекта*: H1 с именем проекта, summary-блок, далее `## Project` (сервисы, URL, хосты), `## Commands` (пользовательские команды), `## Documentation` (ссылки на темы как `dwe-docs://path`) и `## Quick start`.
- *Вне проекта* (или с `--no-project`): обобщённый DWE-справочник — H1 «dwe», summary-блок, `## Documentation`, `## Quick start`. Без секций, специфичных для проекта.

**Подробности:**
- Только чтение. Не берёт проектную блокировку и не запускает preflight; работает без `workspace.yml`.
- Отключённые сервисы и приватные команды исключаются.
- Схема ссылок `dwe-docs://<path>` соответствует путям тем, потребляемым `dwe docs show <path>`.

## `dwe docs cache clear`

Удалить все закешированные mermaid-диаграммы.

**Использование:**
```bash
dwe docs cache clear
```

**Подробности:**
- Очищает XDG-кеш (`$XDG_CACHE_HOME/dwe/mermaid/` или fallback).
- Безвреден, если кеша нет.
- Закешированные диаграммы автоматически регенерируются при следующем просмотре.

**Пример:**
```bash
dwe docs cache clear
# → "Removed 42 cached diagrams"
```

## Mermaid-диаграммы

Диаграммы в синтаксисе mermaid (flowchart, sequence, state machine и т. д.) внутри документации рендерятся в PNG прямо на месте.

### Режимы рендера

**`mermaid: auto` (по умолчанию)**
- Если `mmdc` установлен и доступен → рендер диаграмм в PNG с кешированием
- Если `mmdc` отсутствует → деградация до сырых mermaid-блоков с подсказкой (`📊 [mmdc not installed — Y to copy]`)
- Без ошибки; плавный fallback

**`mermaid: mmdc` (строгий)**
- Требует, чтобы `mmdc` был установлен и доступен
- Если отсутствует → плейсхолдер (`📊 [mmdc required but not found]`) и предупреждение на старте
- Полезно в CI/автоматизации, где mermaid — жёсткая зависимость

**`mermaid: off` (выключено)**
- Никогда не рендерит диаграммы; всегда показывает сырые mermaid-блоки
- Полезно для окружений с ограниченной полосой или ресурсами

Настройка — через `docs.mermaid` в `workspace.yml`. См. [Справочник конфигурации](index.md) для схемы.

### Установка `mmdc`

`mmdc` (mermaid-cli) запускает headless Chromium через puppeteer. Два способа установки:

- **npm (рекомендуется)** — `npm i -g @mermaid-js/mermaid-cli`. Puppeteer сам управляет загрузкой Chromium в `~/.cache/puppeteer/` и держит её в синхронизации с установленной версией mermaid-cli. Обновление — `npm update -g @mermaid-js/mermaid-cli`.
- **Homebrew** — `brew install mermaid-cli`. Работает, но формула пиннит конкретную версию puppeteer, ожидающую точную сборку Chromium; если в `~/.cache/puppeteer/` нужной сборки ещё нет, первый рендер падает с `Could not find Chrome (ver. …)`. Лечение: либо один раз выполнить `npx puppeteer browsers install chrome@<version-from-error>`, либо переключиться на npm-установку выше, у которой такой проблемы пиннинга нет.

Проверьте установку одноразовым рендером вне DWE:

```sh
echo 'flowchart LR; A-->B' > /tmp/x.mmd
mmdc -i /tmp/x.mmd -o /tmp/x.png
```

Если PNG получился, `dwe docs` тоже справится.

### Тема диаграмм (`mermaid_theme`)

Переопределить, какая mermaid-тема рендерится, независимо от фона терминала. Задаётся в пользовательском конфиге (`~/.config/dwe/config` — глобально, `.dwe/config` — для проекта, переменная окружения побеждает).

| Ключ | Тип | По умолчанию | Значения |
|---|---|---|---|
| `mermaid_theme` | string | `auto` | `auto` / `dark` / `light` |

- `auto` — определяет фон терминала и подбирает подходящую тему.
- `dark` / `light` — жёстко фиксируют тему. Полезно для прозрачных терминалов, где автоопределение фона ненадёжно, или чтобы стандартизировать кешированные PNG между машинами.

Override через окружение: `DWE_MERMAID_THEME=dark`. Выбранная тема — часть ключа кеша, поэтому смена значения вызывает перерендер, а не отдачу не той темы.

### Управление кешем

PNG-файлы диаграмм кешируются в `$XDG_CACHE_HOME/dwe/mermaid/` (или системный temp как fallback).

В ключ кеша входят исходник mermaid, ширина рендера, тема (dark/light) и версия `mmdc` — поэтому апгрейд mermaid-cli автоматически инвалидирует старые рендеры.

**Вытеснение по LRU:** когда кеш превышает заданный размер, самые старые диаграммы (по времени последнего обращения) удаляются.

Очистка кеша вручную:
```bash
dwe docs cache clear
```

## См. также

- [Интерактивный TUI-браузер](browser.md) — клавиши `dwe docs`, раскладка, поиск
- [Переводы и поведение языка](translations.md) — разрешение локали, проверки устаревания
