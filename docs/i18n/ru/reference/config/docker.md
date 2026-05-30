> Translated from: reference/config/docker.md @ be17105554b4

# docker.yml / docker.local.yml

Политика выполнения Compose для devbox-проекта.

## Содержание

- [Назначение](#назначение)
- [devbox docker vs devbox compose](#devbox-docker-vs-devbox-compose)
- [Структура](#структура)
- [Справочник полей](#справочник-полей)
  - [`project_name`](#project_name)
  - [`args`](#args)
  - [`process_env`](#process_env)
  - [`topology`](#topology)
  - [`resources`](#resources)
- [docker.local.yml](#dockerlocalyml)
- [Частые ловушки](#частые-ловушки)
- [Связанные команды](#связанные-команды)

## Назначение

`devbox/docker.yml` контролирует, как `devbox docker` собирает и выполняет команды `docker compose`: имя проекта, args на каждую подкоманду и process environment. Файл `.env` автоматически регенерируется CLI до `{up, run, exec, restart, build}` — это поведение **неконфигурируемо**.

Он загружается отдельно через `LoadDockerConfig()` и не мерджится с трёхслойным конфигом.

Локальные переопределения идут в `devbox/docker.local.yml` (gitignored). Шаблон в `devbox/docker.local.example.yml`. Локальные переопределения deep-merge'атся в `docker.yml` до анмаршалинга — local выигрывает при конфликте ключей, списки заменяются.

```mermaid
flowchart LR
  A["devbox/docker.yml"] --> M(["deepMerge"])
  B["devbox/docker.local.yml<br/>optional, gitignored"] --> M
  M --> P(["resolveVarTemplate<br/>$#123;...#125; against DevboxConfig.Raw"])
  P --> R[("DockerConfig")]
```

## devbox docker vs devbox compose

| Команда | Назначение |
|---------|---------|
| `devbox docker <subcommand>` | Публичный lifecycle API. Применяются policy-args. Используйте в Makefile'ах, шагах деплоя и YAML-командах. |
| `devbox compose raw <args...>` | Низкоуровневый диагностический pass-through. Без policy-args. Используйте только для отладки. |
| `devbox compose files` | Показать список активных compose-файлов (диагностика). |
| `devbox compose argv` | Показать полный эффективный argv, включая policy-args (диагностика). |

В Makefile'ах, декларациях YAML-команд и шагах деплоя разрешены только подкоманды `devbox docker`. Прямые вызовы `docker compose` обходят политику и не должны появляться ни в какой автоматизации.

## Структура

```yaml
project_name: "${project.prefix}-${project.name}"

args:
  global: ["--ansi", "always", "--progress", "tty"]
  up: ["-d", "--remove-orphans"]
  logs: ["-f"]
  run: ["--rm"]
  pull: []
  build: []

process_env:
  DOCKER_CLI_HINTS: "false"

topology:
  hidden: [redis-insight-setup]

resources:
  volumes:
    composer_cache:
      name: devbox_composer_cache
      shared: true
      ensure_before: [up, deploy]
```

## Справочник полей

### `project_name`

```yaml
project_name: "${project.prefix}-${project.name}"
```

Имя Docker Compose-проекта, передаваемое как `-p <name>` в каждый compose-вызов. Поддерживает `${dot.path}` lookup'ы в смерженный devbox-конфиг (см. [Шаблоны](../templates.md) — пространства имён `${...}`). По умолчанию разрешается в `devbox-laravel`.

Локальное переопределение:
```yaml
# docker.local.yml
project_name: "my-custom-project"
```

### `args`

Списки args на каждую подкоманду. Каждый ключ — это имя docker-подкоманды; `global` применяется к каждому вызову перед args, специфичными для подкоманды.

```yaml
args:
  global: ["--ansi", "always", "--progress", "tty"]
  up: ["-d", "--remove-orphans"]
  logs: ["-f"]
  run: ["--rm"]
  pull: ["--policy", "always"]
  build: ["--progress", "plain"]
```

Доступные ключи подкоманд: `global`, `up`, `down`, `stop`, `restart`, `logs`, `ps`, `exec`, `run`, `pull`, `build`. (Health-чеки контейнеров используют билтин `docker_wait_healthy` в шагах пайплайна, конфигурируемый через параметры `timeout` и `interval`.)

**Дефолты на ключ:**

Четыре подкоманды имеют встроенные дефолты, применяемые автоматически, когда ключ отсутствует и в `docker.yml`, и в `docker.local.yml`:

| Ключ | По умолчанию |
|-----|---------|
| `up` | `["-d", "--remove-orphans"]` |
| `logs` | `["-f"]` |
| `run` | `["--rm"]` |
| `down` | `["--remove-orphans"]` |

Другие ключи (`global`, `stop`, `restart`, `ps`, `exec`, `pull`, `build`) не имеют дефолтов — они nil, если отсутствуют, пустые, если явно `[]`, заполнены, если явно выставлены.

**Nil vs. явный empty:**

Дефолты применяются **только когда ключ отсутствует в YAML-источнике**. Явный пустой список (`key: []`) отказывается от дефолта:

```yaml
# Отсутствующий ключ up → используется дефолт [-d, --remove-orphans]
args:
  logs: []  # Явный empty → дефолт не применяется, остаётся []

# Явный up → используется указанное значение, без мерджа с дефолтом
args:
  up: ["--no-deps"]  # Заменяет дефолт, без мерджа
```

При переопределении в `docker.local.yml` список заменяет отслеживаемый дефолт целиком (списки не мерджатся):

```yaml
# docker.local.yml — убрать --progress tty (не поддерживается некоторыми терминалами)
args:
  global: ["--ansi", "always"]
  # up, logs, run, down не указаны → дефолты по-прежнему применяются из docker.yml или встроенные
```

**Подкоманды управления образами (`pull` и `build`)**

Подкоманды `pull` и `build` включают опциональные флаги для контроля набора файлов и поведения кеша:

- `devbox docker pull [--all] [services...]` — притянуть образы для сервисов. По умолчанию использует активный набор compose-файлов (base + включённые оверлеи). Флаг `--all` тянет против всех сконфигурированных оверлеев, независимо от локального состояния enable, без модификации `devbox/local.yml`.

- `devbox docker build [--all] [--force] [services...]` — собрать образы для сервисов. По умолчанию ведёт себя так же, как pull. Флаг `--force` дописывает `--no-cache --pull` для обхода layer-кеша Docker и повторного pull базовых слоёв. `--all` и `--force` можно комбинировать.

При конфигурации `args.pull` или `args.build` они применяются перед позиционными сервисами или force-флагами. Пример:

```yaml
args:
  pull: ["--policy", "always"]
  build: ["--progress", "plain"]
```

Флаг `--all` — это переопределение только на один вызов: он НЕ модифицирует `devbox/local.yml` и не сохраняется между командами.

### `process_env`

Переменные окружения, передаваемые в каждый дочерний процесс `docker compose`. Не влияет на окружение контейнера — только на сам процесс CLI compose.

```yaml
process_env:
  DOCKER_CLI_HINTS: "false"
```

Полезно для подавления шума Docker CLI, появляющегося даже когда вывод пайпится.

### `topology`

```yaml
topology:
  hidden: [redis-insight-setup]
```

| Поле | Описание |
|-------|-------------|
| `hidden` | Имена compose-сервисов, исключаемые из дерева топологии и health-чеков |

Полезно для init-контейнеров, которые отрабатывают один раз и выходят — скрытие предотвращает ожидание билтином `docker_wait_healthy`.

### `resources`

Декларирует Docker-ресурсы, которые должны существовать перед определёнными командами.

```yaml
resources:
  volumes:
    composer_cache:
      name: devbox_composer_cache
      shared: true
      ensure_before: [up, deploy]
```

| Поле | Описание |
|-------|-------------|
| `volumes.<key>.name` | Базовое имя тома. Реальное имя в Docker зависит от `shared`: shared-тома используют `name` буквально; non-shared тома сохраняются как `<project_name>_<name>`, чтобы делить жизненный цикл и scope с compose-проектом (соответствуя соглашению Docker Compose для именованных томов, объявленных внутри `compose.yaml`). |
| `volumes.<key>.shared` | Когда `true`, том независим от проекта: реальное имя в Docker равно `name`, и том переживает project reset'ы. Когда `false` (по умолчанию), том project-scoped — runtime префиксует его `<project_name>_`, и `docker_remove_project_volumes` (reset-билтин) убирает его вместе с проектом. |
| `volumes.<key>.ensure_before` | Триггеры, идемпотентно создающие том при отсутствии. Поддерживаемые значения: `up`, `deploy`. |

```yaml
resources:
  volumes:
    composer_cache:                 # логический ключ
      name: devbox_composer_cache   # реальное имя в Docker (shared)
      shared: true
      ensure_before: [up, deploy]

    build_artifacts:                # реальное имя в Docker = "<project_name>_build_artifacts"
      name: build_artifacts
      ensure_before: [deploy]
```

`docker_remove_project_volumes` (reset-билтин) удаляет каждый том, чьё имя начинается с `<project_name>_`, поэтому non-shared тома сбрасываются вместе с проектом, тогда как shared-тома выживают.

## docker.local.yml

Локальные переопределения для docker-политики. Gitignored. Используйте `devbox/docker.local.example.yml` как стартовый шаблон.

Распространённые переопределения:

```yaml
# Переопределить имя проекта
project_name: "personal-laravel"

# Убрать --progress tty (не поддерживается некоторыми терминалами)
args:
  global: ["--ansi", "always"]

# Отключить авто-генерацию .env (пре-генерируется в CI)
env:
  auto_generate: false

# Подавить Docker-подсказки
process_env:
  DOCKER_CLI_HINTS: "false"
```

## Частые ловушки

- **Прямые `docker compose` в Makefile'ах или YAML** — всегда используйте `devbox docker`. Прямые вызовы обходят policy-args, имя проекта и авто-генерацию `.env`.
- **Добавление compose-флагов в Make-рецептах** — флаги принадлежат секции args в `docker.yml`, не Make. Make lifecycle-таргеты зовут `devbox docker` без флагов.
- **Частичное переопределение args** — `args.up` в `docker.local.yml` заменяет отслеживаемый список, не дописывает к нему. Включайте все нужные флаги.
- **Глобальное отключение `auto_generate`** — если вы его отключили, вам надо вручную регенерировать `.env` перед compose-командами, которые от него зависят.

## Связанные команды

- `devbox docker up|down|stop|restart|logs|ps|exec|run|wait|pull|build` — команды lifecycle и управления образами
- `devbox compose files` — показать список активных compose-файлов
- `devbox compose argv` — показать полный эффективный argv
- `devbox render env` — вручную регенерировать `.env`
