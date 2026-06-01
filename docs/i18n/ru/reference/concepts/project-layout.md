> Translated from: reference/concepts/project-layout.md @ fe1231fae96f

# Раскладка проекта

Как типичный проект DWE выглядит на диске: отслеживаемое дерево конфигурации под `workspace.yml` + `workspace/`, параллельные оверлеи `compose/`, runtime-артефакты `.dwe/` и стандартные папки для конфигов, томов и снапшотов.

## Содержание

- [Форма проекта](#форма-проекта)
- [Корневые файлы](#корневые-файлы)
- [Дерево конфигурации `workspace/`](#дерево-конфигурации-workspace)
- [Оверлеи `compose/`](#оверлеи-compose)
- [Runtime-данные сервисов](#runtime-данные-сервисов)
- [Управляемый runtime каталог `.dwe/`](#управляемый-runtime-каталог-dwe)
- [Сводка по отслеживанию в git](#сводка-по-отслеживанию-в-git)
- [Что читать дальше](#что-читать-дальше)

## Форма проекта

Проект DWE — это любая директория, корень которой содержит `workspace.yml`. CLI идёт вверх от текущей рабочей директории, чтобы найти его. Вокруг этого якоря сосуществуют три семейства папок:

- **Отслеживаемое дерево конфигурации** под `workspace/` и корневые конфигурационные файлы — закоммичены, версионируются, источник истины для формы проекта.
- **Отслеживаемые runtime-оверлеи** — файлы Docker Compose под `compose/` и шаблоны конфигов сервисов под `configs/`. DWE не генерирует их; они живут рядом с деревом конфигурации, и на них ссылаются из него.
- **Runtime-данные**, которые производят DWE и контейнеры — `.dwe/` (учётные записи CLI), `volumes/` (цели bind-mount) и `snapshots/` (распакованное хранилище снапшотов). Gitignored.

```mermaid
flowchart TD
  Root["project/"]

  Root --> RootFiles["workspace.yml<br/>.gitignore<br/>README.md"]
  Root --> DWEDir["workspace/<br/>дерево конфигурации (отслеживается)"]
  Root --> ComposeDir["compose/<br/>оверлеи compose (отслеживается)"]
  Root --> ConfigsDir["configs/<br/>шаблоны сервисов (отслеживается)"]
  Root --> VolumesDir["volumes/<br/>bind-mount контейнеров (gitignored)"]
  Root --> SnapsDir["snapshots/<br/>распакованные снапшоты (gitignored)"]
  Root --> DotDir[".dwe/<br/>runtime-данные CLI (gitignored)"]

  DWEDir --> DWEServices["services/<name>/"]
  DWEDir --> DWECommands["commands/"]
  DWEDir --> DWETemplates["templates/"]
  DWEDir --> DWEI18n["i18n/"]
  DWEDir --> DWEScripts["scripts/"]
  DWEDir --> DWEPipelines["deploy.yml<br/>lifecycle.yml<br/>reset.yml<br/>info.yml<br/>setup.yml<br/>validate.yml<br/>defaults.yml<br/>local.yml"]

  ComposeDir --> ComposeInfra["infra/"]
  ComposeDir --> ComposeServices["services/"]
  ComposeDir --> ComposeTools["tools/"]

  DotDir --> DotDeploy["deploy/<br/>state.yml · deploy.lock"]
  DotDir --> DotSnaps["snapshots/<br/>current · snapshot.lock"]
  DotDir --> DotLogs["logs/<br/>deploy.log · run.log · stop.log · reset.log"]
  DotDir --> DotConfig["config<br/>per-project оверрайд userconfig"]
```

Имена папок, отличные от `workspace.yml` и `workspace/`, — это конвенции, а не требования. CLI рад находить `compose/`-файлы где угодно — сервисы ссылаются на них относительным путём в `service.yml` (`compose: [compose/services/web.yml]`). Папки ниже описывают раскладку, к которой большинство проектов сходится; единственные ограничения, которые накладывает CLI, — это папка на сервис под `workspace/services/` и `workspace.yml` в корне проекта.

## Корневые файлы

| Файл | Назначение | Читатель | Писатель | Отслеживается |
|------|---------|--------|--------|---------|
| `workspace.yml` | Идентичность проекта: `project.name`, `project.prefix`, опциональные переопределения бинарников | CLI на каждом вызове | Автор вручную | да |
| `.gitignore` | Скрывает `.dwe/`, `volumes/`, `snapshots/` и `workspace/local.yml` от контроля версий | git | Автор вручную | да |
| `README.md` | Специфичная для проекта точка входа (не README DWE CLI) | люди | Автор вручную | да |

Минимальный `workspace.yml`:

```yaml
project:
  name: my-project
  prefix: myprefix
```

Полный справочник по полям живёт в [`workspace.yml`](../config/workspace.md).

## Дерево конфигурации `workspace/`

Всё декларативное в проекте — сервисы, пайплайны, команды, шаблоны, переводы — живёт под `workspace/`. CLI загружает это дерево на старте; ничто вне него (кроме `workspace.yml` и compose-файлов, на которые он ссылается) не участвует в конфигурации проекта.

| Путь | Назначение | Читатель | Писатель | Отслеживается |
|------|---------|--------|--------|---------|
| `workspace/defaults.yml` | Версионированные дефолты проекта: `services.<name>.enabled`, `runtime`, `state`, `exports.env`, `compose`, `ide` | CLI (слой merge 2) | Автор вручную | да |
| `workspace/local.yml` | Оверрайды на разработчика поверх `defaults.yml`: переопределения портов, флаги enabled, креды, ответы мастера | CLI (слой merge 3) | Автор вручную + setup wizard | нет |
| `workspace/services/<name>/` | Одна папка на сервис. Имя папки — это ID сервиса — нет поля `name:`. | Сервисный загрузчик CLI | Автор вручную | да (кроме оверрайдов `local.yml`) |
| `workspace/commands/` | Декларативные user-команды, выставляемые как `dwe <name>` | Реестр команд CLI | Автор вручную | да |
| `workspace/templates/` | Template-паки, потребляемые `dwe render` (env / ide / ai / git) | Render-пайплайн CLI | Автор вручную | да |
| `workspace/i18n/` | Per-locale переопределения строк (`<lang>.yml`); парятся со встроенными дефолтами | i18n-стор CLI | Автор вручную + переводчики | да |
| `workspace/scripts/` | Shell-скрипты, на которые ссылаются декларативные команды и пайплайны | Шаги пайплайна + user-команды | Автор вручную | да |
| `workspace/deploy.yml` | Верхнеуровневый пайплайн-оркестратор деплоя. Опционально — у DWE есть встроенный дефолт. | Исполнитель deploy | Автор вручную | да |
| `workspace/lifecycle.yml` | Фазы и хуки `run` / `stop` / `restart` | Исполнитель lifecycle | Автор вручную | да |
| `workspace/reset.yml` | Пайплайн сброса проекта | Исполнитель reset | Автор вручную | да |
| `workspace/info.yml` | Элементы информационной панели (заголовок, URL, хосты, команды, кастомные секции) | Рендерер `dwe info` | Автор вручную | да |
| `workspace/setup.yml` | Вопросы мастера настройки (input / confirm / select / multiselect) | Workflow setup | Автор вручную | да |
| `workspace/validate.yml` | Проверки готовности проекта (`shell` / `file_exists` / `tcp_reachable` / …) | `dwe validate` + preflight | Автор вручную | да |
| `workspace/docker.yml` | Слой оркестрации compose: шаблон имени проекта, список файлов, топология, скрытые сервисы | Подсистема Docker | Автор вручную | да |
| `workspace/docker.local.yml` | Per-developer оверрайды compose, глубоко мержёные поверх `docker.yml` | Подсистема Docker | Автор вручную | нет |
| `workspace/styles.yml` | Палитра семантических токенов (info / success / warn / error / muted / heading / link) | UI-стилизация | Автор вручную | да |
| `workspace/ui.yml` | Указатели поведения TUI (`default_expanded_depth`, `auto_collapse_empty`, `show_type_badges`) | TUI-рендереры | Автор вручную | да |
| `workspace/notifications.yml` | Гейты desktop-нотификаций на операцию | Подсистема notify | Автор вручную | да |

### Папка на сервис

Каждый сервис живёт в `workspace/services/<name>/`. Имя папки — это канонический ID сервиса; переименование папки переименовывает сервис. Папка всегда содержит `service.yml`; опциональные `deploy.yml` и `reset.yml` объявляют сервис-специфичные пайплайны, которые оркестратор инлайнит в нужной точке в топологическом порядке.

```text
workspace/services/web/
├── service.yml      # обязательный: type, container, compose, ports, hosts, configs, dirs
├── deploy.yml       # опциональный: пайплайн деплоя сервиса
└── reset.yml        # опциональный: пайплайн сброса сервиса
```

Allowlist полей по типу (какие поля может объявлять `type: app` / `type: tool` / `type: infra`) обеспечивается строго — см. [`services/fields.md`](../config/services/fields.md).

### `defaults.yml` vs `local.yml` vs `workspace.yml`

Эти три файла сливаются в одну эффективную конфигурацию:

1. `workspace.yml` устанавливает структуру (идентичность проекта, версию схемы).
2. `workspace/defaults.yml` заполняет отслеживаемые дефолты.
3. `workspace/local.yml` переопределяет значения на разработчика (gitignored).

Каждый слой опционален; отсутствующие ключи проваливаются на слой ниже. Карты портов сервисов и карты хостов глубоко мержатся по имени записи, так что `local.yml` может переопределить один порт без перечисления остальных. См. [`workspace.yml`](../config/workspace.md) для детальной модели merge.

## Оверлеи `compose/`

`compose/` содержит файлы Docker Compose, на которые ссылается `workspace/services/<name>/service.yml`. DWE не генерирует и не владеет этими файлами — он собирает их список в runtime и передаёт его как `docker compose -f a.yml -f b.yml …`.

| Путь | Назначение | Читатель | Писатель | Отслеживается |
|------|---------|--------|--------|---------|
| `compose/infra/` | Оверлеи для сервисов `type: infra` (БД, очереди, кэши) | Docker Compose через DWE | Автор вручную | да |
| `compose/services/` | Оверлеи для сервисов `type: app` | Docker Compose через DWE | Автор вручную | да |
| `compose/tools/` | Оверлеи для сервисов `type: tool` (админ-UI, одноразовые утилиты) | Docker Compose через DWE | Автор вручную | да |

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

Список compose-файлов также подбирает `workspace/docker.local.yml` последним, так что per-developer оверрайды (альтернативные образы, debug-порты, дополнительные тома) накладываются поверх отслеживаемых оверлеев без их редактирования. См. [`docker.yml`](../config/docker.md) и [Интеграцию с Docker](docker.md) для полной сборки.

## Runtime-данные сервисов

Две папки обычно соседствуют с деревом конфигурации и держат runtime-данные, которые читают или пишут контейнеры.

| Путь | Назначение | Читатель | Писатель | Отслеживается |
|------|---------|--------|--------|---------|
| `configs/<service>/…` | Шаблоны конфигов сервиса, копируемые в контейнеры через `service.yml.configs:` | Контейнеры (чтение) | Автор вручную | да |
| `volumes/<service>/…` | Цели bind-mount для персистентных данных контейнеров (БД, загрузки, кэши) | Контейнеры (чтение/запись) | Контейнеры (запись) | нет |

`configs/` отслеживается, потому что шаблоны — часть проекта; `volumes/` gitignored, потому что содержит сгенерированные runtime-данные, варьирующиеся от машины к машине.

Точные имена папок — это конвенции — сервисы ссылаются на них относительным путём в compose-оверлее и в блоке `configs:` `service.yml`. Проект может использовать `etc/` вместо `configs/` или разбить тома по папкам сервисов (`workspace/services/<name>/var/`). Раскладка выше — самая распространённая форма.

## Управляемый runtime каталог `.dwe/`

Всё, что DWE пишет во время нормальной работы, попадает под `.dwe/`. Папка gitignored и безопасна для удаления — следующий запуск пайплайна пересоберёт всё, что нужно.

| Путь | Назначение | Читатель | Писатель | Отслеживается |
|------|---------|--------|--------|---------|
| `.dwe/deploy/state.yml` | Идемпотентный журнал деплоя: per-step `action_hash`, `status`, `started_at`, `duration` | Исполнитель deploy + `dwe deploy state show` | Исполнитель deploy | нет |
| `.dwe/deploy/deploy.lock` | Эксклюзивный `flock`, удерживаемый во время `dwe deploy run` | Подсистема блокировок | Подсистема блокировок | нет |
| `.dwe/snapshots/snapshot.lock` | Эксклюзивный `flock`, удерживаемый во время мутаций снапшотов | Подсистема блокировок | Подсистема блокировок | нет |
| `.dwe/snapshots/current` | Указатель на активный снапшот, выставляется `snapshot restore` | Подсистема снапшотов | Подсистема снапшотов | нет |
| `.dwe/snapshots/.pre-restore-backup/` | Pre-restore копия `workspace/local.yml` + deploy state для ручного восстановления | Оператор (вручную) | Подсистема снапшотов | нет |
| `.dwe/logs/deploy.log` | Объединённый stdout/stderr последнего `dwe deploy run` (когда `log: true`) | Оператор (вручную) + `dwe logs` | Исполнитель deploy | нет |
| `.dwe/logs/run.log` · `stop.log` · `reset.log` | Объединённый stdout/stderr соответствующей фазы lifecycle (когда `log: true`) | Оператор (вручную) + `dwe logs` | Исполнители lifecycle / reset | нет |
| `.dwe/config` | Оверрайд user-config на проект (язык, тема mermaid, гейты нотификаций) | CLI на каждом вызове | Автор вручную | нет |

### Порядок блокировок

Команды, мутирующие проект, берут `deploy.lock` до `snapshot.lock` (алфавитный порядок) и освобождают в обратном. Чтения (docs, status) не берут блокировки. См. [Состояние и блокировки](state-and-locks.md).

### Восстановление после краха

Файл состояния пишется атомарно после каждого шага. Если деплой прерван, следующий `dwe deploy run` находит последний известный статус, трактует in-progress шаг как упавший и перезапускает с этого места. Устаревшие `flock`-файлы, оставшиеся от `kill -9`, обнаруживаются (lock-файл содержит PID держателя; если процесса нет, lock трактуется как устаревший) и тихо переподбираются.

## Сводка по отслеживанию в git

Чистый `.gitignore` для проекта DWE покрывает runtime-управляемые пути. Runtime никогда не пишет вне этих папок.

```text
.dwe/
volumes/
snapshots/
workspace/local.yml
workspace/docker.local.yml
```

Всё остальное — `workspace.yml`, остальная часть `workspace/`, весь `compose/`, весь `configs/` — отслеживается. Авторы редактируют отслеживаемое дерево; CLI пишет только внутрь gitignored-папок (с одним исключением: setup wizard добавляет смерженные ответы в `workspace/local.yml`, который сам gitignored).

## Что читать дальше

- [Начало работы](getting-started.md) — собрать бинарник, войти в проект, запустить первый `dwe deploy`.
- [Архитектура](architecture.md) — как устроен сам CLI и что встроено vs читается с диска.
- [Интеграция с Docker](docker.md) — как собирается список compose-файлов из папок выше.
- [Состояние и блокировки](state-and-locks.md) — что записывает `.dwe/deploy/state.yml` и как блокировки сериализуют мутации.
- [`workspace.yml`](../config/workspace.md) — справочник по полям трёхслойного конфига.
- [`services/`](../config/services/index.md) — структура папки сервиса и allowlist полей по типу.
