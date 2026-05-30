> Translated from: reference/concepts/project-layout.md @ b1f1358a776a

# Раскладка проекта

Как типичный проект Devbox выглядит на диске: отслеживаемое дерево конфигурации под `devbox.yml` + `devbox/`, параллельные оверлеи `compose/`, runtime-артефакты `.devbox/` и стандартные папки для конфигов, томов и снапшотов.

## Содержание

- [Форма проекта](#форма-проекта)
- [Корневые файлы](#корневые-файлы)
- [Дерево конфигурации `devbox/`](#дерево-конфигурации-devbox)
- [Оверлеи `compose/`](#оверлеи-compose)
- [Runtime-данные сервисов](#runtime-данные-сервисов)
- [Управляемый runtime каталог `.devbox/`](#управляемый-runtime-каталог-devbox)
- [Сводка по отслеживанию в git](#сводка-по-отслеживанию-в-git)
- [Что читать дальше](#что-читать-дальше)

## Форма проекта

Проект Devbox — это любая директория, корень которой содержит `devbox.yml`. CLI идёт вверх от текущей рабочей директории, чтобы найти его. Вокруг этого якоря сосуществуют три семейства папок:

- **Отслеживаемое дерево конфигурации** под `devbox/` и корневые конфигурационные файлы — закоммичены, версионируются, источник истины для формы проекта.
- **Отслеживаемые runtime-оверлеи** — файлы Docker Compose под `compose/` и шаблоны конфигов сервисов под `configs/`. Devbox не генерирует их; они живут рядом с деревом конфигурации, и на них ссылаются из него.
- **Runtime-данные**, которые производят Devbox и контейнеры — `.devbox/` (учётные записи CLI), `volumes/` (цели bind-mount) и `snapshots/` (распакованное хранилище снапшотов). Gitignored.

```mermaid
flowchart TD
  Root["project/"]

  Root --> RootFiles["devbox.yml<br/>.gitignore<br/>README.md"]
  Root --> DevboxDir["devbox/<br/>дерево конфигурации (отслеживается)"]
  Root --> ComposeDir["compose/<br/>оверлеи compose (отслеживается)"]
  Root --> ConfigsDir["configs/<br/>шаблоны сервисов (отслеживается)"]
  Root --> VolumesDir["volumes/<br/>bind-mount контейнеров (gitignored)"]
  Root --> SnapsDir["snapshots/<br/>распакованные снапшоты (gitignored)"]
  Root --> DotDir[".devbox/<br/>runtime-данные CLI (gitignored)"]

  DevboxDir --> DevboxServices["services/<name>/"]
  DevboxDir --> DevboxCommands["commands/"]
  DevboxDir --> DevboxTemplates["templates/"]
  DevboxDir --> DevboxI18n["i18n/"]
  DevboxDir --> DevboxScripts["scripts/"]
  DevboxDir --> DevboxPipelines["deploy.yml<br/>lifecycle.yml<br/>reset.yml<br/>info.yml<br/>setup.yml<br/>validate.yml<br/>defaults.yml<br/>local.yml"]

  ComposeDir --> ComposeInfra["infra/"]
  ComposeDir --> ComposeServices["services/"]
  ComposeDir --> ComposeTools["tools/"]

  DotDir --> DotDeploy["deploy/<br/>state.yml · deploy.lock"]
  DotDir --> DotSnaps["snapshots/<br/>current · snapshot.lock"]
  DotDir --> DotLogs["logs/<br/>deploy.log · run.log · stop.log · reset.log"]
  DotDir --> DotConfig["config<br/>per-project оверрайд userconfig"]
```

Имена папок, отличные от `devbox.yml` и `devbox/`, — это конвенции, а не требования. CLI рад находить `compose/`-файлы где угодно — сервисы ссылаются на них относительным путём в `service.yml` (`compose: [compose/services/web.yml]`). Папки ниже описывают раскладку, к которой большинство проектов сходится; единственные ограничения, которые накладывает CLI, — это папка на сервис под `devbox/services/` и `devbox.yml` в корне проекта.

## Корневые файлы

| Файл | Назначение | Читатель | Писатель | Отслеживается |
|------|---------|--------|--------|---------|
| `devbox.yml` | Идентичность проекта: `schema_version`, `project.name`, `project.prefix`, опциональные переопределения бинарников | CLI на каждом вызове | Автор вручную | да |
| `.gitignore` | Скрывает `.devbox/`, `volumes/`, `snapshots/` и `devbox/local.yml` от контроля версий | git | Автор вручную | да |
| `README.md` | Специфичная для проекта точка входа (не README Devbox CLI) | люди | Автор вручную | да |

Минимальный `devbox.yml`:

```yaml
schema_version: "2"

project:
  name: my-project
  prefix: devbox
```

Полный справочник по полям живёт в [`devbox.yml`](../config/devbox.md).

## Дерево конфигурации `devbox/`

Всё декларативное в проекте — сервисы, пайплайны, команды, шаблоны, переводы — живёт под `devbox/`. CLI загружает это дерево на старте; ничто вне него (кроме `devbox.yml` и compose-файлов, на которые он ссылается) не участвует в конфигурации проекта.

| Путь | Назначение | Читатель | Писатель | Отслеживается |
|------|---------|--------|--------|---------|
| `devbox/defaults.yml` | Версионированные дефолты проекта: `services.<name>.enabled`, `runtime`, `state`, `exports.env`, `compose`, `ide` | CLI (слой merge 2) | Автор вручную | да |
| `devbox/local.yml` | Оверрайды на разработчика поверх `defaults.yml`: переопределения портов, флаги enabled, креды, ответы мастера | CLI (слой merge 3) | Автор вручную + setup wizard | нет |
| `devbox/services/<name>/` | Одна папка на сервис. Имя папки — это ID сервиса — нет поля `name:`. | Сервисный загрузчик CLI | Автор вручную | да (кроме оверрайдов `local.yml`) |
| `devbox/commands/` | Декларативные user-команды, выставляемые как `devbox <name>` | Реестр команд CLI | Автор вручную | да |
| `devbox/templates/` | Template-паки, потребляемые `devbox render` (env / ide / ai / git) | Render-пайплайн CLI | Автор вручную | да |
| `devbox/i18n/` | Per-locale переопределения строк (`<lang>.yml`); парятся со встроенными дефолтами | i18n-стор CLI | Автор вручную + переводчики | да |
| `devbox/scripts/` | Shell-скрипты, на которые ссылаются декларативные команды и пайплайны | Шаги пайплайна + user-команды | Автор вручную | да |
| `devbox/deploy.yml` | Верхнеуровневый пайплайн-оркестратор деплоя. Опционально — у Devbox есть встроенный дефолт. | Исполнитель deploy | Автор вручную | да |
| `devbox/lifecycle.yml` | Фазы и хуки `run` / `stop` / `restart` | Исполнитель lifecycle | Автор вручную | да |
| `devbox/reset.yml` | Пайплайн сброса проекта | Исполнитель reset | Автор вручную | да |
| `devbox/info.yml` | Элементы информационной панели (заголовок, URL, хосты, команды, кастомные секции) | Рендерер `devbox info` | Автор вручную | да |
| `devbox/setup.yml` | Вопросы мастера настройки (input / confirm / select / multiselect) | Workflow setup | Автор вручную | да |
| `devbox/validate.yml` | Проверки готовности проекта (`shell` / `file_exists` / `tcp_reachable` / …) | `devbox validate` + preflight | Автор вручную | да |
| `devbox/docker.yml` | Слой оркестрации compose: шаблон имени проекта, список файлов, топология, скрытые сервисы | Подсистема Docker | Автор вручную | да |
| `devbox/docker.local.yml` | Per-developer оверрайды compose, глубоко мержёные поверх `docker.yml` | Подсистема Docker | Автор вручную | нет |
| `devbox/styles.yml` | Палитра семантических токенов (info / success / warn / error / muted / heading / link) | UI-стилизация | Автор вручную | да |
| `devbox/ui.yml` | Указатели поведения TUI (`default_expanded_depth`, `auto_collapse_empty`, `show_type_badges`) | TUI-рендереры | Автор вручную | да |
| `devbox/notifications.yml` | Гейты desktop-нотификаций на операцию | Подсистема notify | Автор вручную | да |

### Папка на сервис

Каждый сервис живёт в `devbox/services/<name>/`. Имя папки — это канонический ID сервиса; переименование папки переименовывает сервис. Папка всегда содержит `service.yml`; опциональные `deploy.yml` и `reset.yml` объявляют сервис-специфичные пайплайны, которые оркестратор инлайнит в нужной точке в топологическом порядке.

```text
devbox/services/web/
├── service.yml      # обязательный: type, container, compose, ports, hosts, configs, dirs
├── deploy.yml       # опциональный: пайплайн деплоя сервиса
└── reset.yml        # опциональный: пайплайн сброса сервиса
```

Allowlist полей по типу (какие поля может объявлять `type: app` / `type: tool` / `type: infra`) обеспечивается строго — см. [`services/fields.md`](../config/services/fields.md).

### `defaults.yml` vs `local.yml` vs `devbox.yml`

Эти три файла сливаются в одну эффективную конфигурацию:

1. `devbox.yml` устанавливает структуру (идентичность проекта, версию схемы).
2. `devbox/defaults.yml` заполняет отслеживаемые дефолты.
3. `devbox/local.yml` переопределяет значения на разработчика (gitignored).

Каждый слой опционален; отсутствующие ключи проваливаются на слой ниже. Карты портов сервисов и карты хостов глубоко мержатся по имени записи, так что `local.yml` может переопределить один порт без перечисления остальных. См. [`devbox.yml`](../config/devbox.md) для детальной модели merge.

## Оверлеи `compose/`

`compose/` содержит файлы Docker Compose, на которые ссылается `devbox/services/<name>/service.yml`. Devbox не генерирует и не владеет этими файлами — он собирает их список в runtime и передаёт его как `docker compose -f a.yml -f b.yml …`.

| Путь | Назначение | Читатель | Писатель | Отслеживается |
|------|---------|--------|--------|---------|
| `compose/infra/` | Оверлеи для сервисов `type: infra` (БД, очереди, кэши) | Docker Compose через Devbox | Автор вручную | да |
| `compose/services/` | Оверлеи для сервисов `type: app` | Docker Compose через Devbox | Автор вручную | да |
| `compose/tools/` | Оверлеи для сервисов `type: tool` (админ-UI, одноразовые утилиты) | Docker Compose через Devbox | Автор вручную | да |

Типичный оверлей сервиса объявляет образ контейнера, монтирование из `volumes/` и `configs/` проекта и любое окружение, экспортированное из `defaults.yml`:

```yaml
services:
  web:
    image: nginx:latest
    container_name: ${PROJECT}-web
    volumes:
      - ./services/web/src:/var/www/html
      - ./configs/web/nginx.conf:/etc/nginx/conf.d/default.conf
    ports:
      - "${WEB_HTTP_PORT}:80"
```

Список compose-файлов также подбирает `devbox/docker.local.yml` последним, так что per-developer оверрайды (альтернативные образы, debug-порты, дополнительные тома) накладываются поверх отслеживаемых оверлеев без их редактирования. См. [`docker.yml`](../config/docker.md) и [Интеграцию с Docker](docker.md) для полной сборки.

## Runtime-данные сервисов

Две папки обычно соседствуют с деревом конфигурации и держат runtime-данные, которые читают или пишут контейнеры.

| Путь | Назначение | Читатель | Писатель | Отслеживается |
|------|---------|--------|--------|---------|
| `configs/<service>/…` | Шаблоны конфигов сервиса, копируемые в контейнеры через `service.yml.configs:` | Контейнеры (чтение) | Автор вручную | да |
| `volumes/<service>/…` | Цели bind-mount для персистентных данных контейнеров (БД, загрузки, кэши) | Контейнеры (чтение/запись) | Контейнеры (запись) | нет |

`configs/` отслеживается, потому что шаблоны — часть проекта; `volumes/` gitignored, потому что содержит сгенерированные runtime-данные, варьирующиеся от машины к машине.

Точные имена папок — это конвенции — сервисы ссылаются на них относительным путём в compose-оверлее и в блоке `configs:` `service.yml`. Проект может использовать `etc/` вместо `configs/` или разбить тома по папкам сервисов (`devbox/services/<name>/var/`). Раскладка выше — самая распространённая форма.

## Управляемый runtime каталог `.devbox/`

Всё, что Devbox пишет во время нормальной работы, попадает под `.devbox/`. Папка gitignored и безопасна для удаления — следующий запуск пайплайна пересоберёт всё, что нужно.

| Путь | Назначение | Читатель | Писатель | Отслеживается |
|------|---------|--------|--------|---------|
| `.devbox/deploy/state.yml` | Идемпотентный журнал деплоя: per-step `action_hash`, `status`, `started_at`, `duration` | Исполнитель deploy + `devbox deploy state show` | Исполнитель deploy | нет |
| `.devbox/deploy/deploy.lock` | Эксклюзивный `flock`, удерживаемый во время `devbox deploy run` | Подсистема блокировок | Подсистема блокировок | нет |
| `.devbox/snapshots/snapshot.lock` | Эксклюзивный `flock`, удерживаемый во время мутаций снапшотов | Подсистема блокировок | Подсистема блокировок | нет |
| `.devbox/snapshots/current` | Указатель на активный снапшот, выставляется `snapshot restore` | Подсистема снапшотов | Подсистема снапшотов | нет |
| `.devbox/snapshots/.pre-restore-backup/` | Pre-restore копия `devbox/local.yml` + deploy state для ручного восстановления | Оператор (вручную) | Подсистема снапшотов | нет |
| `.devbox/logs/deploy.log` | Объединённый stdout/stderr последнего `devbox deploy run` (когда `log: true`) | Оператор (вручную) + `devbox logs` | Исполнитель deploy | нет |
| `.devbox/logs/run.log` · `stop.log` · `reset.log` | Объединённый stdout/stderr соответствующей фазы lifecycle (когда `log: true`) | Оператор (вручную) + `devbox logs` | Исполнители lifecycle / reset | нет |
| `.devbox/config` | Оверрайд user-config на проект (язык, тема mermaid, гейты нотификаций) | CLI на каждом вызове | Автор вручную | нет |

### Порядок блокировок

Команды, мутирующие проект, берут `deploy.lock` до `snapshot.lock` (алфавитный порядок) и освобождают в обратном. Чтения (docs, status) не берут блокировки. См. [Состояние и блокировки](state-and-locks.md).

### Восстановление после краха

Файл состояния пишется атомарно после каждого шага. Если деплой прерван, следующий `devbox deploy run` находит последний известный статус, трактует in-progress шаг как упавший и перезапускает с этого места. Устаревшие `flock`-файлы, оставшиеся от `kill -9`, обнаруживаются (lock-файл содержит PID держателя; если процесса нет, lock трактуется как устаревший) и тихо переподбираются.

## Сводка по отслеживанию в git

Чистый `.gitignore` для проекта Devbox покрывает runtime-управляемые пути. Runtime никогда не пишет вне этих папок.

```text
.devbox/
volumes/
snapshots/
devbox/local.yml
devbox/docker.local.yml
```

Всё остальное — `devbox.yml`, остальная часть `devbox/`, весь `compose/`, весь `configs/` — отслеживается. Авторы редактируют отслеживаемое дерево; CLI пишет только внутрь gitignored-папок (с одним исключением: setup wizard добавляет смерженные ответы в `devbox/local.yml`, который сам gitignored).

## Что читать дальше

- [Начало работы](getting-started.md) — собрать бинарник, войти в проект, запустить первый `devbox deploy`.
- [Архитектура](architecture.md) — как устроен сам CLI и что встроено vs читается с диска.
- [Интеграция с Docker](docker.md) — как собирается список compose-файлов из папок выше.
- [Состояние и блокировки](state-and-locks.md) — что записывает `.devbox/deploy/state.yml` и как блокировки сериализуют мутации.
- [`devbox.yml`](../config/devbox.md) — справочник по полям трёхслойного конфига.
- [`services/`](../config/services/index.md) — структура папки сервиса и allowlist полей по типу.
