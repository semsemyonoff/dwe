> Translated from: reference/config/setup.md @ 9c89a25376ef

# setup.yml

Интерактивные вопросы установки для свежих проектов.

## Содержание

- [Назначение](#назначение)
- [Как это работает](#как-это-работает)
- [Структура](#структура)
- [Поля верхнего уровня](#поля-верхнего-уровня)
- [Поля записи вопроса](#поля-записи-вопроса)
- [Типы вопросов](#типы-вопросов)
- [Пресеты валидации](#пресеты-валидации)
- [Правила области записи](#правила-области-записи)
- [Примеры](#примеры)
- [Связанные команды](#связанные-команды)

## Назначение

`workspace/setup.yml` определяет интерактивные промпты, которые запускаются, когда разработчик впервые заходит в свежий проект (без `workspace/local.yml` или с пустым). Визард собирает ответы, пишет их в `workspace/local.yml` как смерженные настройки и затем переходит к деплою.

Используйте setup-вопросы для одноразовой конфигурации на разработчика:
- API-ключи или секреты (хранятся в `local.yml`, который gitignored)
- Переключатели сервисов (какие опциональные инструменты разработчик хочет включить)
- Переопределения портов (когда есть конфликты локальных портов)
- Пользовательские пути или имена хостов

Setup-визард — часть более широкого потока `dwe deploy` — запуск `dwe deploy` без подкоманды открывает интерактивное меню, в котором появляется опция Wizard, если есть setup-вопросы.

## Как это работает

1. Разработчик запускает `dwe deploy` в интерактивном терминале на свежем проекте.
2. CLI проверяет конфликты портов и загружает `workspace/setup.yml` (если есть).
3. Если оба пусты (ни вопросов, ни конфликтов), визард не запускается — сразу переходим к деплою.
4. Если что-то есть, меню открывается с опцией **Wizard**.
5. Визард выполняется:
   - Сначала промпты port-конфликтов (если есть) — разработчик выбирает порты-переопределения.
   - Затем setup-вопросы (если есть) — разработчик отвечает на каждый промпт.
   - Наконец, оба набора ответов глубоко мержатся в `workspace/local.yml` и атомарно записываются.
6. Конфиг перезагружается из обновлённого `local.yml`, запускается preflight, и деплой идёт нормально.

Если разработчик отменяет визард на любом шаге (Ctrl-C), `local.yml` остаётся нетронутым — никаких частичных записей.

## Структура

```yaml
questions:
  - id: api-key
    title: GitHub Token
    description: Personal access token for private repos (optional)
    type: input
    required: false
    writes: db.api_key

  - id: enable-postgres
    title: Enable PostgreSQL?
    type: confirm
    required: false
    writes: services.postgres.enabled

  - id: http-port
    title: Web server port
    description: Port to run the local app on
    type: input
    required: true
    writes: services.web.ports.http
    validate:
      preset: port

  - id: select-locale
    title: Preferred language
    type: select
    required: true
    writes: app.locale
    options:
      - value: en
        label: English
      - value: fr
        label: Français
      - value: de
        label: Deutsch

  - id: enable-caching
    title: Use Redis caching?
    type: confirm
    writes: app.cache.enable
```

Файл опционален. Если он отсутствует, визард (если вызван) обрабатывает только port-конфликты.

## Поля верхнего уровня

| Поле | Тип | Обязательное | Описание |
|------|-----|--------------|----------|
| `questions` | list | да | Записи вопросов (см. ниже). Может быть пустым. |

Неизвестные поля верхнего уровня отвергаются на загрузке (strict decoding).

## Поля записи вопроса

| Поле | Тип | Обязательное | Описание |
|------|-----|--------------|----------|
| `id` | string | да | Уникальный идентификатор этого вопроса. Используется как ключ при сборе ответов. |
| `type` | string | да | Один из `input`, `select`, `multiselect`, `confirm`. Неизвестные значения отвергаются валидацией. |
| `title` | string | да | Текст промпта, показываемый разработчику. |
| `description` | string | нет | Более длинное объяснение, показываемое под заголовком. |
| `required` | bool | нет | Если true (дефолт false), визард требует непустой ответ перед продолжением. Для `confirm` `required` игнорируется (всегда опционален). |
| `writes` | string | да | Dot-path, по которому ответ сохраняется в `local.yml`. Должен быть уникален по всем вопросам. См. [Правила области записи](#правила-области-записи). |
| `options` | list | нет | Валидно только для `select` и `multiselect`. Список пар `{value, label}`. Обязательно для обоих типов. |
| `validate` | object | нет | Опциональные правила валидации. Имеет два поля (взаимоисключающие): `preset` (именованный пресет вроде `port` / `hostname`) или `regex` (regex-паттерн). Имеет смысл только для `type: input`. |

Схемные правила, форсируемые на загрузке:

- `id` должен быть уникален по записям.
- `writes` должен быть уникален по записям и следовать правилам синтаксиса dot-path (см. ниже).
- Неизвестные поля верхнего уровня внутри вопроса отвергаются.

Схемные правила, форсируемые валидацией (запускается через `dwe validate`):

- `type` должен быть одним из четырёх известных значений.
- `writes` должен следовать правилам области и синтаксиса ниже.
- `validate.preset` и `validate.regex` не могут быть заданы одновременно.
- `validate.*` имеет смысл только для `type: input`; задание любого из них на `select`, `multiselect` или `confirm` — ошибка.
- `select` и `multiselect` должны иметь непустой `options` с уникальными непустыми строками `value`.
- Записи service-оверлея имеют правила консистентности типов (см. [Правила области записи](#правила-области-записи)).

## Типы вопросов

### `input`

Поле свободного ввода текста с опциональной валидацией.

**Возвращает**: `string` (или `int`, если используется числовой пресет — см. [Пресеты валидации](#пресеты-валидации))

**Пример**:
```yaml
- id: db-password
  type: input
  title: Database password
  required: true
  writes: db.password
```

### `select`

Single-choice выпадающий список. Разработчик выбирает одно значение.

**Возвращает**: `string` (значение `value` выбранной опции)

**Пример**:
```yaml
- id: log-level
  type: select
  title: Logging level
  required: true
  writes: app.log_level
  options:
    - value: debug
      label: Debug (verbose)
    - value: info
      label: Info (normal)
    - value: error
      label: Error (quiet)
```

### `multiselect`

Multi-choice список. Разработчик выбирает ноль или больше значений.

**Возвращает**: `[]string` (срез выбранных полей `value`)

**Пример**:
```yaml
- id: plugins
  type: multiselect
  title: Plugins to enable
  writes: app.plugins
  options:
    - value: auth
      label: Authentication
    - value: logging
      label: Logging
    - value: metrics
      label: Metrics
```

### `confirm`

Тумблер yes/no.

**Возвращает**: `bool` (`true` для yes, `false` для no)

**Пример**:
```yaml
- id: enable-debug
  type: confirm
  title: Enable debug mode?
  writes: app.debug
```

Замечание: `required: true` на confirm — это no-op и даёт предупреждение валидации. Confirm всегда возвращает валидный ответ (либо true, либо false).

## Пресеты валидации

Пресеты — это сокращённые валидаторы для типичных паттернов. Каждый пресет определяет, какие значения принимаются, И какой Go-тип пишется в `local.yml`.

Используйте `validate: { preset: <name> }` внутри блока `validate` вопроса.

### `port`

Валидирует номер порта (1–65535) и пишет `int`.

```yaml
- id: http-port
  type: input
  title: Web server port
  writes: services.web.ports.http
  validate:
    preset: port
```

Ввод разработчика `"8080"` сохраняется как целое `8080` в `local.yml`, так что шаблоны могут использовать его как число.

### `hostname`

Валидирует DNS-имя хоста (формат RFC 1123 short-name) и пишет `string`.

```yaml
- id: postgres-host
  type: input
  title: Postgres hostname
  writes: services.postgres.hosts.internal
  validate:
    preset: hostname
```

### `path`

Валидирует непустой filesystem-путь и пишет `string`.

```yaml
- id: workspace-dir
  type: input
  title: Workspace directory
  writes: app.workspace
  validate:
    preset: path
```

### `non-empty`

Валидирует, что ввод не пустой (whitespace-only отвергается), и пишет `string`.

```yaml
- id: api-key
  type: input
  title: API key
  writes: db.api_key
  validate:
    preset: non-empty
```

### Без пресета, без regex

Если ни `preset`, ни `regex` не заданы, ввод принимается как есть (любая непустая строка при `required: true`, любая строка иначе).

```yaml
- id: app-name
  type: input
  title: Application name
  writes: app.name
  # No validation; any input is accepted
```

### Кастомный regex

Валидируйте ввод против regex-паттерна. Ввод должен матчить паттерн целиком.

```yaml
- id: email
  type: input
  title: Email address
  writes: user.email
  validate:
    regex: "^[a-z0-9+._-]+@[a-z0-9.-]+$"
```

Паттерн должен компилироваться как валидный Go-regex. Невалидные паттерны ловятся `dwe validate` до того, как визард вообще запустится.

## Правила области записи

Поле `writes:` — это dot-path, определяющий, где в `workspace/local.yml` сохраняется ответ. Не все пути разрешены — визард форсит правила, чтобы ответы безопасно сливались со схемой конфига.

### Запрещённые namespace'ы верхнего уровня

Эти ключи верхнего уровня зарезервированы и не могут быть записаны визардом:

- `info.*` — неизменяемые метаданные проекта
- `styles.*` — конфигурация UI-цветов
- `docker.*` — конфигурация политики движка
- `binaries.*` — конфигурация переопределения бинарей

Попытка записи в любой из них триггерит ошибку валидации.

### Формы листьев service-оверлея

При записи под `services.<name>.` разрешены только три точных пути-листа:

| Путь | Тип | Тип вопроса | Описание |
|------|-----|-------------|----------|
| `services.<name>.enabled` | `bool` | `confirm` (обязательно) | Тумблер enabled-состояния сервиса. |
| `services.<name>.ports.<port_name>` | `int` | `input` с `preset: port` (обязательно) | Переопределить объявленный порт сервиса. |
| `services.<name>.hosts.<host_name>` | `string` | `input` (любой пресет OK) | Переопределить объявленное имя хоста сервиса. |

Примеры **разрешённых** записей:
- `services.web.enabled` — должен идти из вопроса `type: confirm`
- `services.web.ports.http` — должен идти из `type: input` с `validate.preset: port`
- `services.postgres.hosts.internal` — может идти из любого `type: input`

Примеры **запрещённых** записей:
- `services.web` (отсутствует лист `.enabled` / `.ports.X` / `.hosts.X`) — перезаписал бы весь конфиг сервиса
- `services.web.ports` (отсутствует конкретное имя порта) — перезаписал бы все порты
- `services.web.container` — не в разрешённом наборе листьев
- `services.web.ports.http` из `type: select` — неправильный тип вопроса для пути

Сообщения об ошибках валидации называют конкретное упавшее ограничение (например, "service ports require `type: input` with `validate.preset: port`").

### Не-сервисные пути

Везде в остальном `local.yml` (кастомные ключи верхнего уровня, `db.*`, `app.*` и т.д.) разрешён любой dot-path и допустим любой тип вопроса:

```yaml
- writes: db.name                    # ✓ allowed
- writes: db.connection.host         # ✓ allowed
- writes: db.connection.port         # ✓ allowed
- writes: app.feature_flags          # ✓ allowed
- writes: custom.setting             # ✓ allowed
```

Визард пишет типизированное значение ответа дословно (string для `input` / `select` / `confirm`, slice для `multiselect`) и доверяет потребляющему конфигу (шаблоны, экспорты и т.д.) обрабатывать его адекватно.

## Примеры

### Минимальный setup только с переопределением порта

```yaml
questions: []
```

Если есть конфликты портов, визард открывается и промптит на переопределения. Записи вопросов не нужны.

### API-ключ + тумблер сервиса

```yaml
questions:
  - id: github-token
    type: input
    title: GitHub personal access token
    description: Used for private repo access. Leave blank to skip.
    required: false
    writes: secrets.github_token
    validate:
      preset: non-empty

  - id: enable-postgres
    type: confirm
    title: Enable PostgreSQL?
    writes: services.postgres.enabled
```

### Сервис с кастомным именем хоста

```yaml
questions:
  - id: database-host
    type: input
    title: Database hostname
    description: The address where your database lives
    required: true
    writes: services.postgres.hosts.internal
    validate:
      preset: hostname

  - id: db-port
    type: input
    title: Database port
    required: true
    writes: services.postgres.ports.db
    validate:
      preset: port
```

### Multi-choice плагины

```yaml
questions:
  - id: enabled-plugins
    type: multiselect
    title: Plugins to enable
    description: Select any combination (space to toggle, enter to confirm)
    required: false
    writes: app.plugins
    options:
      - value: auth
        label: Authentication
      - value: analytics
        label: Analytics
      - value: export
        label: Export to S3
      - value: webhooks
        label: Webhooks
```

### Сложный кастомный namespace

```yaml
questions:
  - id: workspace-root
    type: input
    title: Workspace root directory
    required: true
    writes: workspace.root
    validate:
      preset: path

  - id: cache-backend
    type: select
    title: Cache backend
    required: true
    writes: cache.backend
    options:
      - value: redis
        label: Redis
      - value: memcached
        label: Memcached
      - value: local
        label: Local (in-memory, not persistent)

  - id: enable-profiling
    type: confirm
    title: Enable performance profiling?
    writes: debug.profiling
```

## Связанные команды

- `dwe deploy` — открывает меню визарда на свежих проектах
- `dwe validate` — проверяет схему `workspace/setup.yml` и пути записи
- `dwe validate setup` — валидирует только setup-домен
