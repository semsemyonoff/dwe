> Translated from: reference/config/info.md @ b8812accfb64

# info.yml

Конфигурация дашборда info.

## Содержание

- [Назначение](#назначение)
- [Структура](#структура)
- [Поля верхнего уровня](#поля-верхнего-уровня)
- [Поля секции](#поля-секции)
- [Типы элементов](#типы-элементов)
  - [`definition`](#definition)
  - [`info`](#info)
  - [`warning`](#warning)
  - [`auto-urls`](#auto-urls)
  - [`auto-hosts`](#auto-hosts)
  - [`subgroup`](#subgroup)
  - [`separator`](#separator)
- [Декоративные элементы](#декоративные-элементы)
- [Шаблонные выражения](#шаблонные-выражения)
  - [Доступные данные шаблона](#доступные-данные-шаблона)
  - [Функции шаблона](#функции-шаблона)
  - [Условия `when`](#условия-when)
- [`footer`](#footer)
- [Поведение по умолчанию при отсутствии info.yml](#поведение-по-умолчанию-при-отсутствии-infoyml)
- [Пример: полный info.yml](#пример-полный-infoyml)
- [Типичные ошибки](#типичные-ошибки)
- [Связанные команды](#связанные-команды)

## Назначение

`workspace/info.yml` объявляет содержимое дашборда `dwe info`: секции, элементы, условную видимость и шаблонные выражения. Рендерится через `render.Info()` (в `internal/core/ui/render/`) с использованием Lipgloss.

Загружается отдельно через `LoadInfoConfig()`. Не сливается с 3-слойным конфигом.

## Структура

```yaml
sections:
  - id: <section-id>
    title: "Optional Section Title" # shown as a bordered box header
    items:
      - type: <item-type>
        <item-fields>

footer: true
```

## Поля верхнего уровня

| Поле | Тип | По умолчанию | Описание |
|------|-----|--------------|----------|
| `sections` | list | — | Упорядоченный список определений секций. |
| `footer` | bool | `false` | Если `true`, после всех секций рендерится завершающая строка-заголовок таблицы. |

## Поля секции

| Поле | Тип | По умолчанию | Описание |
|------|-----|--------------|----------|
| `id` | string | — | Уникальный идентификатор секции |
| `title` | string | — | Опциональный заголовок, рендерящийся над списком элементов |
| `items` | list | — | Упорядоченный список определений элементов |
| `hide_on_empty` | bool | `false` | Полностью пропустить секцию (без заголовка, без рамки), когда ни один элемент не пережил when-фильтрацию. Замечание: у subgroup'ов другой дефолт (`true`). |

## Типы элементов

| Тип | Рендерится как | Обязательные поля |
|-----|----------------|-------------------|
| `definition` | Строка `Label — Value`, с опциональной иконкой | `name`, `value` |
| `info` | Строка info-цвета | `text` |
| `warning` | Строка warning-цвета | `text` |
| `auto-urls` | Динамически сгенерированные URL'ы, организованные по сервисам | — |
| `auto-hosts` | Динамически сгенерированные имена хостов из сервисов | — |
| `subgroup` | Контейнер с опциональным заголовком и вложенными элементами | `items` |
| `separator` | Пустая строка-разделитель | — |

Все элементы принимают опциональный `when:` (Go template-выражение). Элементы с falsy `when` выбрасываются из рендера. Все элементы поддерживают опциональный булев флаг `decorative` (см. [Декоративные элементы](#декоративные-элементы)).

> **Замечание о рендеринге `auto-urls` и `auto-hosts`:** элементы `auto-urls` и `auto-hosts` разворачиваются во время рендера (когда выполняется `dwe info`), а не во время загрузки YAML. Разворот происходит внутри info-рендерера через консультацию с текущим конфигом и итерацию сервисов в deploy-порядке. Этот дизайн гарантирует, что дашборд всегда отражает свежие определения и состояние сервисов. Детали архитектуры о render-time развороте через паттерн `Source*Spec` см. в [`docs/internals/packages.md`](../../internals/packages.md).

### `definition`

Пара label + value, рендерится как `Label — Value`.

```yaml
- type: definition
  name: Project
  value: "{{ .Project.FullName }}"
  icon: "🔗"
  indent: 2
  when: "{{ .State }}"
```

| Поле | Описание |
|------|----------|
| `name` | Текст метки |
| `value` | Текст значения (обычная строка или шаблонное выражение) |
| `icon` | Опциональная эмодзи или символ, подставляемый перед значением. Предпочитайте кодпойнты с `Emoji_Presentation=Yes` (например, `📦`, `🐳`, `💾`); text-default кодпойнты вроде `🛢`, `🗄`, `⚙` помечаются `dwe validate` и выбрасываются во время рендера, чтобы сохранить выравнивание колонок — полный комментарий см. в [поле `icon`](services/fields.md#icon-field) в справочнике сервисов. |
| `indent` | Опциональное число ведущих пробелов. Дефолт для definition-элементов — `2`; передайте `0`, чтобы прижать к левому краю. Отрицательные значения отвергаются. |
| `when` | Условие; элемент скрыт, если falsy |

### `info`

Информационная текстовая строка.

```yaml
- type: info
  text: '127.0.0.1	{{ (index .Services "main").Host "web" }}'
  indent: 0
  when: '{{ (index .Services "adminer").Enabled }}'
```

| Поле | Описание |
|------|----------|
| `text` | Текст сообщения (обычная строка или шаблонное выражение) |
| `indent` | Опциональное число ведущих пробелов |
| `when` | Условие; элемент скрыт, если falsy |

### `warning`

Строка-предупреждение (рендерится в warning-цвете).

```yaml
- type: warning
  text: "Please add this to your /etc/hosts file:"
```

| Поле | Описание |
|------|----------|
| `text` | Текст предупреждения (обычная строка или шаблонное выражение) |
| `when` | Условие; элемент скрыт, если falsy |

### `auto-urls`

Динамически генерирует список сервисных URL из настроенных сервисов проекта. Сервисы объявляют свои хосты и порты в `workspace/services/<name>/service.yml`; `auto-urls` рендерит их с опциональной фильтрацией и кастомизацией.

```yaml
- type: auto-urls
  include: [app, tool]
  hide: [varnish]
  hide_paths:
    main: ["SPX profiler"]
  port_via: nginx
  when: "{{ .Services }}"
```

| Поле | Тип | По умолчанию | Описание |
|------|-----|--------------|----------|
| `include` | list | `[app, tool]` | Типы сервисов для включения: любая комбинация `app`, `tool`, `infra`. |
| `hide` | list | — | Ключи папок сервисов, которые нужно полностью исключить. Неизвестные ключи молча игнорируются. |
| `hide_paths` | map | — | Исключить отдельные суб-пути по ключу сервиса и имени пути (например, `main: ["SPX profiler"]` скрывает путь "SPX profiler" под сервисом "main"). |
| `port_via` | string | auto-detected | Переопределяет, какой сервис использовать как front-прокси для генерации основных URL. Когда пусто, авто-детект ищет один включённый сервис `type: infra`, объявляющий либо `ports.http: 80` (http-трафик), либо `ports.https: 443` (https-трафик). Явно названные сервисы обязаны существовать; отсутствующие сервисы дают ошибку. Авто-детект возвращает «без прокси», если найдено ноль или несколько кандидатов (в этом случае рендерятся только прямые URL `localhost:<port>`). |
| `when` | string | — | Условие; элемент скрыт, если falsy. |

**Примеры авто-детекта `port_via`:**

С авто-детектом (дефолт — без поля `port_via:`):
```yaml
# Auto-detects if exactly one infra service has ports.http: 80
- type: auto-urls
  include: [app, tool]
```

В этом случае, если сервис `nginx` имеет `ports: {http: 80}` и он `type: infra` и включён, он выбирается автоматически. App- и tool-сервисы тогда рендерятся как `proxied URL | localhost:port` (если они объявляют собственные порты) или просто `proxied URL` (если только host). Другие сервисы без авто-детектированного прокси рендерятся только как `localhost:port`.

С явным переопределением `port_via:`:
```yaml
# Always use named service as proxy, even if it's not type: infra
- type: auto-urls
  include: [app, tool]
  port_via: api_gateway
```

Когда `port_via` задан явно, этот сервис обязан существовать, иначе во время рендера выбрасывается ошибка. Названный сервис используется для конструирования прокси-URL независимо от его `type:`.

Когда авто-детект находит ноль или несколько infra-сервисов с целевым портом, **прокси не выбирается** и сервисы рендерятся только со своими прямыми портами (или только с хостами, если порт отсутствует):
```yaml
# No eligible infra service found → app/tool with only localhost:<port> URLs
- type: auto-urls
  include: [app, tool]
```

Сервисы участвуют в `auto-urls` через свой блок `info:` в `service.yml` (схема — в [services/index.md](services/index.md)). Каждый сервис может объявить:
- `title` — переопределяет заголовок сервиса (по умолчанию — title-cased имя папки)
- `primary_host` — какую запись `hosts` поднимать как основной URL (дефолт: `web`)
- `primary_port` — какую запись `ports` поднимать (дефолт: `http`)
- `paths` — упорядоченный список суб-путей под основным URL

Сервисы без блока `info` включаются в типы `include`, но рендерят только свой основной URL, если присутствуют хосты и порты.

**Правила сборки URL:**
- `hosts[primary_host]` **и** `ports[primary_port]` (прямой биндинг) → `<proxied URL> | <direct URL>`
- только `hosts[primary_host]` → `<proxied URL>` (если `port_via` доступен)
- только `ports[primary_port]` → `http://localhost:<port>`
- ни того, ни другого → строка молча пропускается

`<proxied URL>` использует порты сервиса `port_via` для выбора схемы/порта; `<direct URL>` использует собственный порт сервиса. Порты `:80` и `:443` из вывода опускаются.

### `auto-hosts`

Динамически генерирует список всех имён хостов из сервисов для конфигурации `/etc/hosts`.

```yaml
- type: auto-hosts
  include: [app, tool, infra]
  ip: 127.0.0.1
  hide: [varnish]
  when: "{{ .Services }}"
```

| Поле | Тип | По умолчанию | Описание |
|------|-----|--------------|----------|
| `include` | list | `[app, tool, infra]` | Типы сервисов для включения: любая комбинация `app`, `tool`, `infra`. |
| `ip` | string | `127.0.0.1` | IP-адрес, ассоциируемый со всеми именами хостов. Значения здесь не валидируются на формат IP; `dwe validate` эмитит предупреждение, если парсинг падает. |
| `hide` | list | — | Ключи папок сервисов для полного исключения. Неизвестные ключи молча игнорируются. |
| `when` | string | — | Условие; элемент скрыт, если falsy. |

Рендерит каждую запись `hosts` из включённых сервисов в двухколоночной таблице (`IP  Hostname`), сохраняя deploy-порядок, дедуплицируя имена хостов и отфильтровывая `localhost`.

### `subgroup`

Элемент-контейнер, группирующий связанные элементы и опционально показывающий заголовок.

```yaml
- type: subgroup
  title: "Tools"
  hide_on_empty: false
  items:
    - type: definition
      name: Adminer
      icon: "🛢"
      value: '{{ appURL ((index .Services "adminer").Host "web") ((index .Services "main").Port "http") .Runtime.UseHTTPS }}'
      when: '{{ (index .Services "adminer").Enabled }}'
    - type: definition
      name: RedisInsight
      icon: "📊"
      value: '{{ appURL ((index .Services "redis_insight").Host "web") ((index .Services "main").Port "http") .Runtime.UseHTTPS }}'
      when: '{{ (index .Services "redis_insight").Enabled }}'
```

| Поле | Тип | По умолчанию | Описание |
|------|-----|--------------|----------|
| `title` | string | — | Опциональный заголовок subgroup'а (обычная строка или шаблонное выражение). Когда пуст, subgroup рендерится без заголовка. |
| `items` | list | — | Обязательное. Упорядоченный список определений дочерних элементов. Может содержать любой тип элемента, включая вложенные subgroup'ы. |
| `when` | string | — | Условие; когда falsy, весь subgroup (включая всех детей) пропускается. Когда truthy, каждый дочерний элемент оценивается по собственному `when`. |
| `hide_on_empty` | bool | `true` | Полностью пропустить subgroup, когда ни один дочерний элемент не пережил when-фильтрацию. (Противоположно дефолту секций; у subgroup'ов дефолт — `true`.) |
| `decorative` | bool | `false` | Когда `true`, subgroup никогда не считается контентом для родительской проверки `hide_on_empty`, даже если он производит вывод. |

Subgroup'ы могут быть вложены произвольно.

### `separator`

Пустая строка для разделения контента внутри секции.

```yaml
- type: separator
```

Без полей. Полезен, когда соседним элементам нужно визуальное «дыхание» без введения новой секции.

## Декоративные элементы

По умолчанию элементы делятся на две категории: **content**-элементы, которые засчитываются для видимости секции, и **декоративные** — нет.

| Тип | Дефолт `decorative` |
|-----|----------------------|
| `definition` | `false` |
| `info` | `false` |
| `warning` | `false` |
| `subgroup` | `false` |
| `separator` | `true` |

Флаг `decorative` на любом типе элемента переопределяет дефолт:

```yaml
- type: warning
  text: "Only informational"
  decorative: true    # Makes this warning not count as content
```

```yaml
- type: separator
  decorative: false   # Makes this separator count as content, keeping the section visible
```

Когда `hide_on_empty: true` на секции или subgroup, блок полностью пропускается, если ни один элемент не пережил и `when`-фильтрацию, и проверку content-vs-decorative. Блок только с декоративными элементами (или без элементов) всё равно может отрендериться, если у него есть title и `hide_on_empty: false`.

## Шаблонные выражения

Все поля `text`, `value` и `when` поддерживают синтаксис Go template, вычисляемый против `DweConfig`.

### Доступные данные шаблона

| Выражение | Тип | Описание |
|-----------|-----|----------|
| `{{ .Project.Name }}` | string | Имя проекта |
| `{{ .Project.FullName }}` | string | Объединённый префикс + имя |
| `{{ .State }}` | string | Активное состояние (пусто, если нет) |
| `{{ .Runtime.UseHTTPS }}` | bool | HTTPS включён. |
| `{{ .Runtime.SPX.Path }}` | string | Путь SPX-профайлера. |
| `{{ (index .Services "main").Enabled }}` | bool | Включён ли сервис `main` (обязательные сервисы всегда true). |
| `{{ (index .Services "main").Container }}` | string | Имя контейнера сервиса `main`. |
| `{{ (index .Services "main").Port "http" }}` | int | Поиск порта по имени. `Port(name)` — метод на `ServiceConfig` (возвращает `0`, если отсутствует). |
| `{{ (index .Services "main").Host "web" }}` | string | Поиск хоста по имени. `Host(name)` возвращает `""`, если отсутствует. |
| `{{ .AppServices }}` / `{{ .ToolServices }}` / `{{ .InfraServices }}` | `map[string]ServiceConfig` | Подмножества, отфильтрованные по `type:` — удобно для `{{ range }}` по одной категории. |

### Функции шаблона

Info-шаблонам доступна стандартная поверхность шаблонов DWE: доменный хелпер `appURL` плюс реестры sprout (`std`, `strings`, `numeric`, `slices`, `maps`, `regexp`, `conversion`, `time`, `filesystem`, `semver`). Полный справочник хелперов — в [Шаблонах](../templates.md).

Пример использования `appURL`:

```yaml
value: '{{ appURL ((index .Services "main").Host "web") ((index .Services "main").Port "http") .Runtime.UseHTTPS }}'
# → http://laravel.localhost (or https://… when use_https is true)
```

### Условия `when`

Поля `when` принимают любое шаблонное выражение, вычисляющееся в truthy/falsy-значение. Пустая строка, `false` и `0` — falsy; всё остальное — truthy.

```yaml
when: "{{ .State }}"                                   # show only when state is non-empty
when: '{{ (index .Services "adminer").Enabled }}'      # show only when adminer is enabled
when: "{{ .Runtime.SPX.Path }}"                        # show only when SPX path is set
```

## `footer`

```yaml
footer: true
```

Когда true, под всеми секциями рендерится строка-футер (обычно показывает подсказку help).

## Поведение по умолчанию при отсутствии info.yml

Когда `workspace/info.yml` не существует, используется встроенная дефолтная конфигурация. Она рендерит две секции:

1. Секция **URLs** с элементом `auto-urls` (дефолт `include: [app, tool]`; без фильтрации)
2. Секция **Hosts** с warning и элементом `auto-hosts` (дефолт `include: [app, tool, infra]`)

Это позволяет проектам без `info.yml` сразу увидеть осмысленный дашборд, показывающий связность всех сервисов, целиком построенный из определений сервисов в `workspace/services/*/service.yml`. Сервисы вносят детали через свои блоки `info:` (title, paths, ключи host/port). Никакого редактирования `info.yml` для старта не требуется.

Чтобы кастомизировать дашборд, создайте `workspace/info.yml` со своими `sections` и `items`. Встроенный дефолт не используется, если файл существует, даже если в нём нет элементов `auto-urls` или `auto-hosts`.

## Пример: полный info.yml

```yaml
sections:
  - id: dwe_info
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
      # Automatically render all app and tool services with their hosts/ports
      - type: auto-urls
        include: [app, tool]
        hide: [varnish]
        hide_paths:
          main: ["SPX profiler"]
        port_via: nginx

  - id: credentials
    title: Credentials
    items:
      - type: subgroup
        title: Database
        hide_on_empty: true
        items:
          - type: definition
            name: User
            value: "{{ .Project.Name }}_user"
      - type: subgroup
        title: API Key
        hide_on_empty: true
        items:
          - type: warning
            text: "Check .env for sensitive credentials"

  - id: hosts
    title: Hosts
    items:
      - type: warning
        text: "Add these to your /etc/hosts:"
      # Automatically render all service hostnames
      - type: auto-hosts
        include: [app, tool, infra]

footer: true
```

## Типичные ошибки

- **Голые значения `when:` без шаблонного синтаксиса** — `when: .State` невалидно; должно быть `when: "{{ .State }}"`.
- **Отсутствие кавычек вокруг шаблонных выражений** — YAML парсит `{{ ... }}` как flow-маппинг, если оно без кавычек. Всегда кавычьте шаблонные строки.
- **Синтаксис поиска сервиса** — Go text/template требует `index` для доступа к map по строковому ключу: `(index .Services "main")` возвращает `ServiceConfig`. Дальше поля структуры идут в PascalCase (`.Container`, `.Enabled`), а порты / хосты используют методы-аксессоры `Port` / `Host` с именем порта/хоста как аргументом: `(index .Services "main").Port "http"`. Скобки вокруг `index`-выражения обязательны, чтобы метод вызывался на возвращённом `ServiceConfig`.
- **Использование конфигурационных ключей, отсутствующих в структуре DweConfig** — в шаблонах доступны только поля, экспонированные на типизированной структуре `DweConfig`. Кастомные ключи, добавленные в `defaults.yml`, лежат в `Raw`, но не доступны в данных шаблона, если не экспонированы явно.
- **Порядок аргументов `appURL`** — порядок такой: `host`, `port`, `useHTTPS`, затем опциональный `path`. Перестановка port и useHTTPS молча даёт неправильные URL. При линковке инструмента, маршрутизируемого через основной reverse-прокси, комбинируйте имя хоста инструмента с портом основного сервиса: `appURL ((index .Services "adminer").Host "web") ((index .Services "main").Port "http") .Runtime.UseHTTPS`.
- **`hide_on_empty` с декоративными элементами** — по умолчанию content-элементы вроде `definition`, `info` и `warning` засчитываются для видимости секции, а `separator` — нет. Используйте флаг `decorative`, чтобы переопределить: задайте `decorative: true` на content-элементе, чтобы исключить его из расчёта видимости, или `decorative: false` на separator'е, чтобы он засчитывался как контент. Секция с `hide_on_empty: true` полностью скрыта, если ни один content-элемент (после `when`-фильтрации) не пережил.
- **Рендеринг футера с `hide_on_empty`** — при `footer: true` футер рендерится только в том случае, если хотя бы одна секция произвела вывод. Если все секции скрыты через `hide_on_empty`, футер тоже подавляется.

## Связанные команды

- `dwe info` — рендер полного дашборда
- `dwe` (без аргументов) — показывает компактную сводку (не из `info.yml`, использует `render.Summary`)
