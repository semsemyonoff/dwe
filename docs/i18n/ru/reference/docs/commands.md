> Translated from: reference/docs/commands.md @ 8f64cfd145a5

# Неинтерактивные команды документации

Подкоманды `dwe docs` для использования в пайпах, скриптах, агентах и CI. Также описывают настройку mermaid-диаграмм и пути установки `mmdc`.

## `dwe docs show <topic>`

Рендер одной темы документации в stdout.

**Использование:**
```bash
dwe docs show <topic> [--lang <code>] [--raw] [--source all|dwe|project] [--anchors] [--toc]
```

**Аргументы:**
- `<topic>` — путь темы, опционально с якорем: `config/lifecycle`, `config/services/fields`, `config/workspace#field-reference`, `config/services/fields.md#ports-field`. Поддерживается нечёткое сопоставление (нечувствительный к регистру поиск по подстроке); многостраничные темы вроде `config/services` неоднозначны сами по себе — указывайте конкретную подстраницу.

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
dwe docs show config/workspace#field-reference --raw --lang en

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
dwe	reference/config/workspace	en
dwe	reference/config/services/fields	en
dwe	reference/config/services/fields	ru
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

## `dwe docs search <query>`

Искать по всем темам документации нечувствительную к регистру буквальную подстроку и выводить секции, которые её содержат. Сделано для пайпов, скриптов, агентов и CI.

**Использование:**
```bash
dwe docs search <query> [--source all|dwe|project] [--lang <code>] [--limit <n>] [--output text|json] [--pretty]
```

**Аргументы:**
- `<query>` — буквальная подстрока для поиска (нечувствительно к регистру). Совпадения внутри огороженных блоков кода тоже считаются — именно там обычно встречаются имена схем.

**Флаги:**
- `--source <all|dwe|project>` — источник доков (по умолчанию `all`). `dwe` ищет только во встроенных доках; `project` — только в `./docs/`; `all` — в обоих.
- `--lang <code>` — код языка (по умолчанию: активная локаль или `en`).
- `--limit <n>` — максимум строк результата (по умолчанию `50`; `0` = без ограничения).
- `--output <text|json>` — формат вывода (глобальный флаг; по умолчанию `text`).
- `--pretty` — форматированный JSON-вывод (только с `--output json`).

**Вывод:**
- **`text` (по умолчанию):** через табуляцию, по одной строке на совпавшую секцию: `<source>\t<path>#<anchor>\t<count>`. Секции сортируются по числу совпадений (по убыванию), затем по пути. Вступительный текст под H1 (до первого H2) выводится с пустым якорем.
- **`--output json`:** JSON-массив записей `{source, path, anchor, count}` (path и anchor разделены; anchor пустой для вступительного текста под H1 до первого H2/H3).
- **Ноль совпадений:** stdout остаётся пустым (text) или `[]` (JSON), код выхода — 0. В текстовом режиме однострочное уведомление уходит в **stderr** и называет запрос, активный `--source` и разрешённую локаль — два фильтра, которые чаще всего дают ложно пустой результат. В JSON-режиме уведомления нет, поэтому потребитель в пайпе видит одинаковый вывод в любом случае.

**Примеры:**
```bash
dwe docs search depends_on
dwe docs search 'RunContext.Render' --source dwe
dwe docs search topo-sort --lang en --limit 5
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

Сгенерировать один документ [llms.txt](https://llmstxt.org/) — плотный брифинг, дающий AI-агенту полную картину того, что представляет собой данный DWE-проект и где искать подробности. Project-agnostic часть ограничена 12 КБ (проверяется тестом на `--no-project`); project-aware документ добавляет сверху сервисы, команды и URL и потому растёт вместе с воркспейсом.

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
- *Внутри проекта*: H1 с именем проекта, summary-блок, далее `## Project` (сервисы, URL, хосты), `## Commands` (пользовательские команды), секции брифинга (ниже), `## Documentation` (ссылки на темы как `dwe-docs://path`) и `## Quick start`.
- *Вне проекта* (или с `--no-project`): обобщённый DWE-справочник — H1 «dwe», summary-блок, секции брифинга, `## Documentation`, `## Quick start`. Без секций, специфичных для проекта.

**Секции брифинга** (одинаковы в обеих формах — они описывают сам DWE):
- `## Builtins` — все зарегистрированные step-билтины (`имя — вид — назначение`, включая `internal`), затем непересекающийся реестр предикатов `when:`. Оба реестра называются «builtin», но не принимают имена друг друга — секция говорит об этом прямо.
- `## Template syntax by site` — где вычисляется `${...}`, а где `{{ ... }}`, и какие пространства имён `${...}` недоступны в полях пайплайна.
- `## Diagnostics and machine-readable output` — `--quiet`, `--level`, `-v`/`--debug`, `docs show --toc`/`--anchors` и исключения для `-o json`.
- `## Reserved env names` — имена, которые `dwe render env` всегда выводит сам (`PROJECT`, `UID`, `GID`) и которые нельзя переобъявить правилом `exports.env`.

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
- Если `mmdc` отсутствует → деградация до inline-плейсхолдеров вида `📊 Diagram N/M — rendering disabled` (с подсказкой скопировать исходник по `y`), плюс однократный стартовый баннер: **⚠ `mmdc` not installed.** Mermaid diagrams cannot render. Install with `npm i -g @mermaid-js/mermaid-cli`
- Без ошибки; плавный fallback

**`mermaid: mmdc` (строгий)**
- Требует, чтобы `mmdc` был установлен и доступен
- Если отсутствует → fallback на те же плейсхолдеры `📊 Diagram N/M — rendering disabled` и тот же стартовый баннер **⚠ `mmdc` not installed**, что и в `auto` (отдельного плейсхолдера для строгого режима нет)
- Полезно в CI/автоматизации, где mermaid — жёсткая зависимость

**`mermaid: off` (выключено)**
- Никогда не рендерит диаграммы; всегда показывает сырые mermaid-блоки
- Полезно для окружений с ограниченной полосой или ресурсами

Настройка — через `docs.mermaid` в `workspace.yml`. См. [Справочник конфигурации](index.md#справочник-конфигурации) для схемы.

### Установка `mmdc`

`mmdc` (mermaid-cli) запускает headless Chromium через puppeteer. Два способа установки:

- **npm (рекомендуется)** — `npm i -g @mermaid-js/mermaid-cli`. Puppeteer при установке скачивает собственный Chromium в `~/.cache/puppeteer/`. Обновление — `npm update -g @mermaid-js/mermaid-cli`.
- **Homebrew** — `brew install mermaid-cli`. Формула пиннит конкретную версию puppeteer, ожидающую точную сборку Chromium.

В любом случае встроенный puppeteer ждёт **конкретную** сборку Chromium. Если её нет в `~/.cache/puppeteer/` — очищенный кеш, установка с `--ignore-scripts` или апгрейд mermaid-cli, поднявший ожидаемую версию без перекачки браузера — каждый рендер падает с `Could not find Chrome (ver. …)`, даже если сам `mmdc` есть в `$PATH`. Кеш браузера может пропасть независимо от способа установки.

Очевидное лечение даёт осечку сразу по двум причинам, и совет из самой ошибки (`npx puppeteer browsers install chrome-headless-shell`) попадает в обе:

- **Не тот продукт** — mermaid-cli запускается с `headless: 'shell'`, поэтому ему нужна сборка **`chrome-headless-shell`**, а *не* полный `chrome`. В тексте ошибки написано «Chrome», хотя резолвится именно `chrome-headless-shell`, так что установка `chrome@<ver>` оставит рендер падающим с той же ошибкой.
- **Не та версия** — голый `npx puppeteer browsers install …` запускает *свежий* standalone-puppeteer, который пинит **более новую** сборку Chromium, чем (обычно более старый) puppeteer-core внутри вашего mermaid-cli. mermaid-cli ищет строго ту сборку, что пинит сам, поэтому новая закачка лежит без дела, а рендер всё равно падает.

Лечение (не зависит от способа установки) — поставить ровно тот продукт **и** версию, что названы в ошибке; всегда пиньте `@<version-from-error>`:

```sh
npx @puppeteer/browsers install chrome-headless-shell@<version-from-error>
```

В `dwe docs`, когда у диаграммы показано `📊 Diagram N/M — render failed`, поставьте на неё курсор и нажмите `E` — откроется полный текст ошибки mmdc (в нём указана недостающая версия Chrome для команды выше). Пиннинг версии обходит дрейф standalone-puppeteer; продукт `chrome-headless-shell` соответствует тому, что реально запускает `headless: 'shell'`.

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
