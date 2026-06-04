> Translated from: guides/background-daemons.md @ cbd42c65deb0

# Фоновые демоны

Некоторые процессы не укладываются в request-response форму, которой целый день занимается ваш app-контейнер. Laravel queue worker, файл-watcher, пересобирающий ассеты, websocket-bridge, scheduled-task runner — каждому надо продолжать работать между командами разработчика, переживать выход из `dwe shell` и быть достаточно заметным, чтобы никто не забыл, что он там. `type: daemon` — это декларативная форма для такого.

Один YAML-блок разворачивается во время загрузки реестра в четыре виртуальные команды (`.start`, `.logs`, `.stop`, `.restart`), каждая из которых появляется в `dwe commands list`, интерактивном браузере, completion и как step-таргет внутри workflow. Демон-контейнеры отслеживаются через стандартные docker-метки — отдельного state-файла нет — и авто-останавливаются вместе со стеком.

Полная схема — в [`../reference/config/commands/types.md#type-daemon`](../reference/config/commands/types.md#type-daemon); эта страница про authoring-паттерны, к которым вы будете обращаться.

## Анатомия блока демона

```yaml
# workspace/commands/queue.yml
commands:
  queue:
    type: daemon
    description: Laravel queue worker
    service: app-main             # литеральное имя compose-сервиса (без ${...})
    workdir_from: services.main.work_dir_internal
    user: www-data
    env:
      QUEUE_CONNECTION: redis
    params:
      name:
        default: default
        pattern: ^[a-zA-Z0-9_-]+$
    argv:
      - php
      - artisan
      - queue:listen
      - --timeout=0
      - --queue=${param.name}
    daemon:
      container_template: "php_queue_${param.name}"
      on_already_running: error
      auto_remove: true
      stop_timeout: 10s
```

Три вещи делают это демоном, а не обычным `service_run`:

- **`service:` должен быть литеральным.** Шаблонизация (`${...}` / `{{...}}`) отвергается — метка `dwe.daemon.id` обязана оставаться стабильной между перезапусками, чтобы completion, статус и авто-реап могли коррелировать состояние.
- **`argv:` (или `cmd:`) — это долгоживущий процесс.** Никакого таймаута — это то, что становится PID 1 в контейнере.
- **Блок `daemon:`** объявляет шаблон имени контейнера и lifecycle-дефолты.

`service`, `workdir`/`workdir_from`, `user`, `env`, `params`, `argv`, `compose_args` следуют той же семантике, что и у `type: service_run`. Сама source-команда `queue` **не** запускается напрямую — запускаются только четыре виртуальные команды.

## Четыре виртуальные команды

| Виртуальный ID | Что делает | Заметки |
|---|---|---|
| `queue.start` | `docker compose run -d --name <full> ... <argv>` | Детач; `--no-deps` оставляет остальной стек нетронутым. |
| `queue.logs` | `docker logs -f --tail=100 <full>` | Foreground; Ctrl-C отцепляет, но контейнер продолжает работать. |
| `queue.stop` | `docker stop -t <stop_timeout> <full>` | Идемпотентна — отсутствующий контейнер не ошибка. |
| `queue.restart` | `queue.stop`, затем `queue.start` | Реализована как виртуальный workflow, прокидывающий все объявленные params. |

End-to-end:

```bash
# Запустить воркер
dwe cmd queue.start

# Тейлить (Ctrl-C отцепляет, контейнер остаётся)
dwe cmd queue.logs

# Что сейчас запущено?
dwe status daemons

# Рестарт после изменения конфига
dwe cmd queue.restart

# Остановить только этого демона
dwe cmd queue.stop
```

## Множественные инстансы через `params:`

В примере выше один объявленный param (`name`), так что то же определение демона может рулить произвольным числом инстансов контейнера — по одному на очередь, по одному на пул воркеров, по одному на корень файл-watcher-а.

```bash
# Три воркера, по одному на очередь
dwe cmd queue.start --set name=emails
dwe cmd queue.start --set name=webhooks
dwe cmd queue.start --set name=video

dwe status daemons
# php_queue_emails    running   2m
# php_queue_webhooks  running   2m
# php_queue_video     running   1m

# Рестарт только video
dwe cmd queue.restart --set name=video

# Остановить всё одним махом — см. «Авто-реап на dwe stop» ниже
dwe stop
```

Объявление `params:` ведёт и собранное имя контейнера (через `${param.name}` в `container_template`), и `argv:`. Два требования:

1. Каждый `${param.X}` в `container_template` должен быть объявлен в `params:` **и** нести `pattern:`-regex. Pattern — это совет; авторитетная защита — пост-рендер regex на полное имя контейнера (`^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$`).
2. Собранное имя контейнера — это `<project.full>-<rendered template>`, так что оно остаётся уникальным между несколькими чек-аутами одного проекта на одной машине.

## Поля блока `daemon:`

| Поле | Дефолт | Эффект |
|---|---|---|
| `container_template` | — (обязательно) | Шаблон имени контейнера. Рендерится в пространстве шаблонов команды и затем префиксуется `<project.full>-`. |
| `on_already_running` | `error` | `error` прерывает `.start`, если контейнер уже есть; `noop` делает `.start` идемпотентной (удобно для «start if needed»-скриптов). |
| `auto_remove` | `true` | Когда true, `.start` добавляет `--rm`, чтобы контейнер удалялся при остановке (никаких зомби-stopped в `docker ps -a`). |
| `stop_timeout` | `10s` | Сколько `docker stop` ждёт до SIGKILL. Duration-строка; sub-second округляются вверх до 1s. |

Если демону нужно окно graceful-shutdown длиннее дефолтных 10s (например, «дорешать текущую задачу» для queue-воркеров) — поднимите `stop_timeout`:

```yaml
daemon:
  container_template: "php_queue_${param.name}"
  stop_timeout: 60s
```

## Видимость: метки и `dwe status daemons`

Каждый демон-контейнер несёт три стандартные метки, так что `docker ps` — единственный источник правды:

- `dwe.project=<project.full>` — скоупит к этому проекту.
- `dwe.daemon.id=<base>` — ID source-команды (например, `services.main.queue`).
- `dwe.daemon.params=<json>` — params на момент старта (например, `{"name":"emails"}`).

`dwe status daemons` фильтрует по этим меткам и показывает каждого работающего демона, имя его контейнера, daemon-ID, params запуска и uptime. Используйте, когда задумываетесь «не забыл ли я что-то остановить?» — например, после рестарта стека через `dwe restart` (см. следующий раздел).

## Авто-реап на `dwe stop`

При каждом запуске `dwe stop` к стоп-пайплайну прицепляется синтетическая фаза `_auto_reap_daemons`. Она перечисляет каждый контейнер с меткой `dwe.project=<full>` и непустым `dwe.daemon.id` и останавливает их параллельно. **Опт-аута нет** — демоны всегда выключаются вместе со стеком. Фаза видна в выводе `dwe stop --plan`.

`dwe restart` — это `dwe stop` плюс `dwe run`. Стоп-нога реапит каждого демона как обычно, но run-нога **не** перезапускает их — демоны намеренно отделены от основного жизненного цикла и не авто-стартуют на `dwe run`. Если они нужны обратно после рестарта — зовите `<id>.start` явно:

```bash
dwe restart
dwe cmd queue.start --set name=emails
dwe cmd queue.start --set name=webhooks
```

Если делаете это каждый раз — заверните в workflow-команду:

```yaml
commands:
  workers.up:
    type: workflow
    description: Start all queue workers
    steps:
      - command: queue.start
        with: { name: emails }
      - command: queue.start
        with: { name: webhooks }
      - command: queue.start
        with: { name: video }
```

## Безопасность: секреты не кладите в `params:`

Значения params попадают в метку `dwe.daemon.params` как JSON, который `docker inspect` показывает кому угодно с доступом к docker-сокету на хосте. **Никогда не кладите секреты в `params:`.**

Для токенов, паролей и любых чувствительных значений используйте `env:` — env-значения проходят через окружение контейнера (через `-e KEY` плюс значение в `cmd.Env` хост-процесса), никогда через host-argv, так что они не появляются в `ps`, `/proc/<pid>/cmdline` или метках контейнера.

```yaml
commands:
  queue:
    type: daemon
    service: app-main
    params:
      name:                       # OK — имя очереди не секрет
        default: default
        pattern: ^[a-zA-Z0-9_-]+$
    env:
      QUEUE_TOKEN: ${secrets.queue_token}   # OK — значения идут через env, не через метки
    argv:
      - php
      - artisan
      - queue:listen
      - --queue=${param.name}
    daemon:
      container_template: "queue_${param.name}"
```

Обратное правило тоже работает: не тяните `env:`, чтобы различать инстансы — env-значения не попадают в имя контейнера, так что два демона с одинаковым `container_template` и разными `env:` столкнутся. Используйте `params:` для того, что должно быть частью идентичности (имя очереди, корень наблюдения, размер пула); `env:` — для того, что нужно процессу для работы (креды, connection strings, фичефлаги).

## Перекрёстные ссылки

- [`../reference/config/commands/types.md#type-daemon`](../reference/config/commands/types.md#type-daemon) — полная схема, правила валидации, метки, реализация виртуальных команд.
- [`author-project-commands.md`](author-project-commands.md) — другие типы команд (`shell`, `service_exec`, `workflow`), которыми оборачивают демонов.
- [`daily-workflow.md`](daily-workflow.md) — `dwe status` и поток stop/restart.
