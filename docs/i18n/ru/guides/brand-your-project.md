> Translated from: guides/brand-your-project.md @ 366dbdc7658e

# Брендирование проекта

Сделайте, чтобы `dwe` выглядел как *ваш* проект. Кастомизируйте ASCII-шапку, встречающую разработчиков при первом запуске, выставьте палитру под идентичность вашей команды, и наполните дашборд `dwe info` так, чтобы каждый URL, hostname и кред, нужный новому участнику, был в одной команде.

Два файла покрывают всю поверхность: `workspace/styles.yml` — визуальная идентичность (шапка + цвета + сепаратор), `workspace/info.yml` — содержимое дашборда. Оба опциональны — DWE поставляет вменяемые дефолты — и оба загружаются независимо от слоистого проектного конфига.

## Разделы

- [Выбор шапки](#выбор-шапки)
- [Цветовая палитра](#цветовая-палитра)
- [Символ сепаратора](#символ-сепаратора)
- [Наполнение info-дашборда](#наполнение-info-дашборда)
- [Типы элементов, к которым вы будете обращаться](#типы-элементов-к-которым-вы-будете-обращаться)
- [Условная видимость](#условная-видимость)
- [Замечание про иконки и emoji](#замечание-про-иконки-и-emoji)

## Выбор шапки

`workspace/styles.yml` контролирует брендированную шапку, которую рендерят `dwe` (без аргументов) и `dwe info`. Brand-identity строка (`{▪} DWE · <project> · <version>`) всегда рендерится; всё остальное накладывается сверху.

```yaml
# workspace/styles.yml
header:
  lines:
    - "Welcome to"
    - "DWE Laravel"
  font: doom
  tagline: "Local dev, container-orchestrated."
```

- `header.lines` рендерится как ASCII-арт через FIGlet. Две короткие строки обычно смотрятся лучше одной длинной — баннерные шрифты криво переносятся на узких ширинах.
- `header.font` принимает стандартные имена FIGlet-шрифтов: `doom`, `banner`, `big`, `block`, `slant` и похожие. Дефолт — `doom`.
- `header.tagline` — одна приглушённая строка под brand-line. Опустите, если хотите более компактную шапку.

ASCII-блок всегда красится токеном `accent` — отдельного `header.color` нет. Поменяйте accent — шапка перекрасится.

См. [styles.yml reference — header](../reference/config/styles.md#header) для полной схемы.

## Цветовая палитра

DWE использует **семь семантических цветовых токенов**. Каждая UI-поверхность — таблицы, секции статуса, браузер команд, вывод `--help` — красится из этой палитры.

| Токен | Что красит |
|-------|------------|
| `accent` | Brand-line, ASCII-шапка, заголовки секций, фокус-бордеры, заголовки таблиц, активная пагинация |
| `success` | Состояния OK / running / enabled, успешные уведомления |
| `warning` | Warning-диагностика, partial / degraded состояния |
| `danger` | Error-диагностика, failed-уведомления |
| `muted` | Счётчики, сепараторы, приглушённые строки, tree-глифы, описания help |
| `border` | Дефолтные (несфокусированные) рамки панелей и таблиц |
| `text` | Body-текст — оставьте пустым, чтобы терминал сам выбрал foreground |

Переопределяйте любое подмножество. Токены, которые вы опустите (или оставите пустыми), фолбэкнутся на встроенные дефолты, разные для светлого и тёмного фона:

```yaml
colors:
  accent:  "#A78BFA"   # пурпурный бренд
  success: "#10B981"   # зелёный с уклоном в teal
  muted:   "#94A3B8"
```

Пара вещей, которые стоит знать сразу:

- **Только hex-строки.** Голые ANSI 256-коды или имена цветов не принимаются.
- **Один override применяется в обоих режимах.** Отдельных под-веток `light:` / `dark:` нет — выбирайте hex, который читается на обоих фонах, или полагайтесь на встроенные light/dark-дефолты.
- **Фон терминала детектится один раз на старте.** Поменять `workspace/styles.yml` и перезапустить `dwe` — поддерживаемый способ перетеминга; работающий процесс не делает hot-reload.

Для монохромного вида ставьте `accent` и `success` в один тоновый ряд и пусть `muted` / `border` дают контраст.

См. [styles.yml reference — colors](../reference/config/styles.md#colors) и [Light / dark resolution](../reference/config/styles.md#light--dark-resolution) для полного справочника токенов и встроенных дефолтов.

## Символ сепаратора

Ключ `separator:` контролирует символ между лейблом и значением в строках определений (`Project · laravel`):

```yaml
separator: "·"
```

Распространённые альтернативы: `"·"` (middle dot, дефолт), `"—"` (em dash), `":"` (двоеточие — терсно, но читается как заголовочный разрыв). Выбрали один раз — и забыли.

## Наполнение info-дашборда

`workspace/info.yml` объявляет, что показывает `dwe info`: секции, элементы, условные строки, шаблонные выражения. Загружается отдельно от 3-слойного конфига и *не* мерджится между слоями — на проект ровно один `info.yml`.

Минимальная форма:

```yaml
# workspace/info.yml
sections:
  - id: <section-id>
    title: "Optional Section Title"
    items:
      - type: <item-type>
        # ... поля элемента

footer: true
```

Секции рендерятся в порядке объявления. Опциональный `footer: true` добавляет закрывающую table-header строку под последней секцией.

Если `workspace/info.yml` отсутствует, DWE рендерит встроенный дефолт с секцией `URLs` (авто-генерируется из host-ов и портов сервисов) и секцией `Hosts` (строки `/etc/hosts`, которые разработчик должен добавить). Дефолта достаточно многим проектам, чтобы отгружаться без правки `info.yml`.

См. [info.yml reference](../reference/config/info.md) для полной схемы.

## Типы элементов, к которым вы будете обращаться

Семь типов покрывают всё, что рендерит дашборд:

| Тип | Рендерится как | Обязательные поля |
|------|----------------|-------------------|
| `definition` | строка `Label — Value` с опциональной иконкой | `name`, `value` |
| `info` | info-цветной текст | `text` |
| `warning` | warning-цветной текст | `text` |
| `auto-urls` | URL-ы сервисов, авто-генерируемые из `service.yml` | — |
| `auto-hosts` | hostname-ы, авто-генерируемые из `service.yml` | — |
| `subgroup` | контейнер с опциональным заголовком и вложенными элементами | `items` |
| `separator` | blank-line разделитель | — |

Два `auto-*` типа — сердце поддерживаемого `info.yml`: вместо того, чтобы хардкодить каждый URL и host, наведите их на ваши определения сервисов и пусть DWE итерирует. Новый сервис → новая строка автоматически.

Рабочий пример, комбинирующий частые типы:

```yaml
sections:
  - id: project
    items:
      - type: subgroup
        title: DWE
        hide_on_empty: false
        items:
          - type: definition
            name: Project
            value: "{{ .Project.FullName }}"
          - type: definition
            name: State
            value: "{{ .State }}"
            when: "{{ .State }}"

  - id: urls
    title: URLs
    items:
      - type: auto-urls
        include: [app, tool]
        hide: [varnish]
        port_via: nginx

  - id: hosts
    title: Hosts
    items:
      - type: warning
        text: "Add these to your /etc/hosts:"
      - type: auto-hosts
        include: [app, tool, infra]

footer: true
```

Для per-item справочника полей см. [info.yml — item types](../reference/config/info.md#item-types).

## Условная видимость

Каждый элемент принимает `when:`-выражение Go-шаблона. Элементы с falsy-результатом отбрасываются из рендера; секции и subgroup-ы могут авто-скрываться при пустоте через `hide_on_empty:`.

```yaml
- type: definition
  name: SPX
  value: "{{ .Runtime.SPX.Path }}"
  when: "{{ .Runtime.SPX.Path }}"        # только когда SPX сконфигурирован

- type: definition
  name: Adminer
  value: '{{ appURL ((index .Services "adminer").Host "web") ((index .Services "main").Port "http") .Runtime.UseHTTPS }}'
  when: '{{ (index .Services "adminer").Enabled }}'   # только когда сервис включён
```

Три распространённых грабли:

- Всегда квотируйте template-выражения. YAML иначе парсит `{{ ... }}` как flow-маппинг.
- Доступ к полю сервиса идёт через синтаксис Go `text/template` — `(index .Services "main").Host "web"`, со скобками вокруг `index`.
- `.Project`, `.Services`, `.Runtime` и `.State` экспонированы на верхнем уровне. Свои ключи, которые вы добавили в `defaults.yml`, лежат под `.Cfg.Raw`.

См. [info.yml — template expressions](../reference/config/info.md#template-expressions) для полной шаблонной поверхности и сигнатуры хелпера `appURL`.

## Замечание про иконки и emoji

Элементы `definition` поддерживают поле `icon:`, которое префиксирует глиф к значению. Используйте умеренно — щепотка иконок делает дашборд читаемее; стена иконок — это шум.

Единственное техническое правило: **предпочитайте кодпоинты с `Emoji_Presentation=Yes`** (например, `📦`, `🐳`, `💾`). Text-default-кодпоинты вроде `🛢` (U+1F6E2), `🗄` (U+1F5C4) и `⚙` (U+2699) отбрасываются на рендере, чтобы держать колонки таблиц выровненными, потому что замеры ширины терминала расходятся между сочетаниями шрифт + терминал. `dwe validate` отметит их при добавлении.

Полное пояснение с разбором по символам — в [`icon` field — emoji caveat](../reference/config/services/fields.md#icon-field).

## См. также

- [styles.yml reference](../reference/config/styles.md) — полная схема, цветовые токены, light/dark resolution
- [info.yml reference](../reference/config/info.md) — полная схема, типы элементов, template-выражения
- [services/fields reference — icon field](../reference/config/services/fields.md#icon-field) — безопасность emoji
- [shared-ide-and-agent-config](shared-ide-and-agent-config.md) — шаринг template-паков тем же способом, что и брендинг
