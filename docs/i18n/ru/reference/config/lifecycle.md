> Translated from: reference/config/lifecycle.md @ 70b4fb4374f4

# lifecycle.yml

Декларации пайплайнов run / stop, движущие `devbox run`, `devbox stop` и `devbox restart`.

## Содержание

- [Назначение](#назначение)
- [Форма пайплайна](#форма-пайплайна)
- [Структура](#структура)
- [`run.update`](#runupdate)
- [`run.show_info` / `run.final_message`](#runshow_info--runfinal_message)
- [`stop.final_message`](#stopfinal_message)
- [`log` (логирование в файл)](#log-логирование-в-файл)
- [Hook-фазы](#hook-фазы)
- [Минимальный пример](#минимальный-пример)
- [Валидация](#валидация)
- [Параллельные группы шагов](#параллельные-группы-шагов)
- [Частые ловушки](#частые-ловушки)
- [Связанные команды](#связанные-команды)

## Назначение

`devbox/lifecycle.yml` декларирует два пайплайна:

- **`run:`** — выполняется `devbox run` (и `devbox restart` после stop). Оборачивает стандартную последовательность `docker up` + `docker wait` опциональным update-пробом и pre/post hook-фазами.
- **`stop:`** — выполняется `devbox stop` (и первой половиной `devbox restart`). Оборачивает `docker down` опциональными pre/post hook-фазами.

Он загружается отдельно через `LoadLifecycleConfig()` и **не** мерджится с трёхслойным конфигом.

Файл опционален для всех команд, которые его используют.

Когда `lifecycle.yml` отсутствует или отсутствует секция, Devbox подставляет встроенный дефолтный пайплайн и печатает одну info-строку в stderr: `Using built-in default <run|stop> pipeline (override with devbox/lifecycle.yml).` Info-строка подавляется в режиме `--output json`.

**Дефолтный пайплайн `run:`** (срабатывает, когда `lifecycle.yml` отсутствует или не имеет секции `run:`):

| Поле | Значение |
|-------|-------|
| `update.mode` | `off` (без git-проба) |
| `show_info` | `true` |
| `final_message` | `Project is ready for work!` |
| Фазы | Одна фаза `start`: один шаг `type: devbox` с `cmd: "docker up --wait"` |

**Дефолтный пайплайн `stop:`** (срабатывает, когда `lifecycle.yml` отсутствует или не имеет секции `stop:`):

| Поле | Значение |
|-------|-------|
| `final_message` | `Project is stopped. Have a nice day!` |
| Фазы | Auto-reap фаза (см. ниже) + одна фаза `stop`: один шаг `type: devbox` с `cmd: "docker down"` |

Всякий раз, когда запускается пайплайн `stop:` (дефолтный или пользовательский), автоматически препендится фаза `_auto_reap_daemons`; opt-out нет, и она видна в plan output для прозрачности. Она останавливает все фоновые демоны, запущенные через команды [`type: daemon`](commands/types.md#type-daemon).

`devbox docker up` и `devbox docker down` — тонкие passthrough'и Docker Compose и никогда не используют этот пайплайн; сырые `docker compose stop` / `restart` остаются доступными через `devbox docker stop` / `devbox docker restart`.

## Форма пайплайна

```mermaid
flowchart LR
  subgraph run["devbox run"]
    direction LR
    U[update probe] --> PRE[pre hooks] --> UP[docker up] --> WAIT[docker wait] --> POST[post hooks] --> INFO[info] --> MSG1[final_message]
  end
  subgraph stop["devbox stop"]
    direction LR
    SPRE[pre hooks] --> DOWN[docker down] --> SPOST[post hooks] --> MSG2[final_message]
  end
  subgraph restart["devbox restart"]
    direction LR
    R1[stop pipeline] --> R2[run pipeline<br/>--no-update]
  end
```

`docker up` выдаётся как шаг `type: devbox` с `cmd: "docker up"` внутри фазы `start`. Ожидание health контейнеров использует шаг `type: builtin` с `cmd: docker_wait_healthy`. Они не магические — исполнитель пайплайна зовёт их как любой другой шаг, поэтому они подхватывают политику из `docker.yml`.

## Структура

```yaml
run:
  update:
    mode: on            # on | off
  show_info: true
  final_message: "Project is ready for work!"
  log: false            # tee status + child stdout/stderr to .devbox/logs/run.log
  phases:
    - name: <phase>
      description: <text>
      when:             # optional: typed condition (see deploy/conditions.md)
        type: builtin|shell|template
        cmd: <string>
        expr: <string>
      steps:
        - name: <step>
          type: shell|devbox|command|builtin
          cmd: <value>
          with:         # optional: parameters
            key: value

stop:
  final_message: "Project is stopped. Have a nice day!"
  log: false            # tee status + child stdout/stderr to .devbox/logs/stop.log
  phases:
    - name: <phase>
      description: <text>
      when:             # optional: typed condition
        type: builtin|shell|template
        cmd: <string>
        expr: <string>
      steps:
        - name: <step>
          type: shell|devbox|command|builtin
          cmd: <value>
          with:         # optional: parameters
            key: value
```

Фазы и шаги используют ту же форму, что [deploy.yml](deploy/index.md): `name`, `description`, `when`, `untracked`, `steps[]`, плюс per-step `type` / `cmd` / `with`, `when`, `check`, `files_gate`, `continue_on_error`. См. справочник deploy для полной грамматики шага, включая [`files_gate:` (предусловие для файлов)](deploy/conditions.md#files_gate-pre-condition-for-files).

`deploy_services: true` **не** разрешено в lifecycle-пайплайнах.

## `run.update`

Опциональный update-проб запускается до любой фазы. Он может фетчить из upstream-ремоута, детектить drift и (в зависимости от `mode`) пуллить `--ff-only`. Успешный pull триггерит in-process перезагрузку `DevboxConfig`, `LifecycleConfig` и реестра команд до выполнения фаз.

| Поле | Тип | По умолчанию | Описание |
|-------|------|---------|-------------|
| `update.mode` | string | `on` (когда блок `update:` присутствует) | Одно из `on`, `off`. Само написание ключа `update:` — это opt-in. |

Поведение mode:

| Mode | Фетчит | Пуллит | Поведение, когда отстаёт |
|------|---------|-------|------------------------|
| `on` | да | с согласия | Спрашивает до pull; на non-TTY проверяет upstream-drift и предупреждает (check-семантика). |
| `off` | нет | нет | Проб выключен (то же, что флаг `--no-update`). |

Слоистый приоритет в runtime: флаг `--no-update` > флаг `--update <mode>` > `EffectiveMode()` из YAML.

Когда проб находит грязное дерево, отсутствие upstream или сбой fetch, он предупреждает и продолжает — пайплайн run никогда не блокируется пробом.

## `run.show_info` / `run.final_message`

| Поле | Тип | По умолчанию | Описание |
|-------|------|---------|-------------|
| `show_info` | bool | `false` | Дописать рендер `devbox info` после последней фазы. |
| `final_message` | string | `Project is ready for work!` | Сообщение об успехе, печатаемое в самом конце. |

## Гейт деплоя required-сервисов

`devbox run` автоматически гейтит на том, чтобы required-сервисы были задеплоены. До старта пайплайна run команда проверяет, что все **tracked**-сервисы (те, что появляются в разрешённом плане деплоя) имеют `status: deployed` в state-файле.

Если какой-то tracked-сервис ещё не задеплоен, `devbox run` выходит с ошибкой: "run `devbox deploy run` first". Это предотвращает запуск против частично инициализированного окружения — обход гейта просто отдал бы `docker compose up` сервис, чьи тома/конфиги/база данных никогда не провижились, и run упал бы почти сразу с несвязанной ошибкой. Всегда сначала деплойте.

Подробности см. в [state/index.md](state/index.md).

## `stop.final_message`

| Поле | Тип | По умолчанию | Описание |
|-------|------|---------|-------------|
| `final_message` | string | `Project is stopped. Have a nice day!` | Сообщение об успехе, печатаемое в самом конце. |

## `log` (логирование в файл)

Верхнеуровневое поле и на `run:`, и на `stop:`. По умолчанию `false` для lifecycle-пайплайнов (в отличие от `deploy.yml`, где дефолт — `true`).

Когда включено, статус-сообщения devbox и stdout/stderr дочерних процессов теются в `.devbox/logs/<name>.log` (с убранными ANSI-кодами) — `.devbox/logs/run.log` для run, `.devbox/logs/stop.log` для stop.

```yaml
run:
  log: true     # tee to .devbox/logs/run.log
```

## Hook-фазы

Hook-фазы — это конвенциональные имена (`pre` / `post`), используемые для оборачивания стандартной работы start/stop. Добавляйте `continue_on_error: true` на каждый шаг, чтобы упавший хук не прерывал основной lifecycle:

```yaml
run:
  phases:
    - name: pre
      description: Before-run hooks (continue on failure)
      steps:
        - name: before-run
          type: command
          cmd: project.before-run
          continue_on_error: true

    - name: start
      description: Start containers and wait for health
      steps:
        - name: up
          type: devbox
          cmd: "docker up"
        - name: wait
          type: builtin
          cmd: docker_wait_healthy

    - name: post
      description: After-run hooks (continue on failure)
      steps:
        - name: after-run
          type: command
          cmd: project.after-run
          continue_on_error: true
```

`continue_on_error: true` приводит к тому, что падение репортится через `FailStep` (красный ✗), но выполнение переходит к следующему шагу, и пост-шаговый `check` не вычисляется.

## Минимальный пример

```yaml
# devbox/lifecycle.yml
run:
  update:
    mode: on
  show_info: true
  final_message: "Project is ready for work!"
  phases:
    - name: start
      description: Start containers and wait for health
      steps:
        - name: up
          type: devbox
          cmd: "docker up"
        - name: wait
          type: builtin
          cmd: docker_wait_healthy

stop:
  final_message: "Project is stopped. Have a nice day!"
  phases:
    - name: stop
      description: Stop and remove containers
      steps:
        - name: down
          type: devbox
          cmd: "docker down"
```

## Валидация

`LoadLifecycleConfig()` обеспечивает:

- Каждый шаг в `run.phases` и `stop.phases` имеет поле `type:` с одним из `shell`, `devbox`, `command`, `builtin`.
- `update.mode`, когда выставлен, — одно из `on`, `off`. Старые значения (`prompt`, `auto`, `check`) отвергаются с понятной ошибкой.
- `update.enabled` не разрешён (удалён в пользу `mode: off` для отключения проба).
- `deploy_services: true` отвергается (валидно только в `deploy.yml`).
- `final_message` и `log` нормализуются в дефолты при отсутствии.

## Параллельные группы шагов

Lifecycle-фазы используют тот же контейнер step-group `parallel:`, что `deploy.yml`. Шаг может декларировать `parallel: { max_concurrent, fail_fast, steps }` вместо leaf-тела, и внутренние под-шаги запускаются параллельно с той же семантикой отмены, журнала и репортёра. См. [deploy → Параллельные группы шагов](deploy/examples.md#parallel-step-groups) для схемы, дефолтов, правил валидации и модели выполнения.

## Частые ловушки

- **Забыть `continue_on_error: true` на hook-шагах** — без него упавший pre-stop хук прерывает всю последовательность stop, и контейнеры никогда не останавливаются.
- **Использование `update: {}` с `enabled: true`** — поле `enabled` больше не поддерживается. Само написание ключа `update:` — это opt-in; используйте `mode: off` для отключения проба или полностью опустите ключ `update:`.
- **Добавление `deploy_services`-фаз** — они только для деплоя. Lifecycle-пайплайны вызывают сервисы через ссылки `type: command`.
- **Редактирование `lifecycle.yml` для использования прямых вызовов `docker compose`** — публичный API — это `type: devbox` с `cmd: "docker up"`. Прямые вызовы `docker compose` обходят политику из `docker.yml`.

## Связанные команды

- `devbox run` — выполнить пайплайн run (с опциональным update-пробом)
- `devbox run --no-update` — пропустить update-проб
- `devbox run --update <mode>` — переопределить сконфигурированный mode
- `devbox stop` — выполнить пайплайн stop
- `devbox restart` — `stop`, затем `run --no-update`
- `devbox docker up` / `devbox docker down` — сырой passthrough Docker Compose (не использует этот пайплайн)
