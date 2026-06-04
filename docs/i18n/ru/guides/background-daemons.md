> Translated from: guides/background-daemons.md @ cbd42c65deb0

# Фоновые демоны

Некоторые процессы не укладываются в модель «запрос-ответ», которой ваш app-контейнер занят целыми днями. Laravel queue worker, файловый watcher, пересобирающий ассеты, websocket-мост, планировщик задач — каждому из них нужно продолжать работать между командами разработчика, переживать выход из `dwe shell` и быть достаточно заметным, чтобы про него не забыли. `type: daemon` — это декларативная форма для таких процессов.

Один YAML-блок при загрузке реестра разворачивается в четыре виртуальные команды (`.start`, `.logs`, `.stop`, `.restart`), и каждая из них появляется в `dwe commands list`, в интерактивном браузере, в автодополнении и как цель шага внутри воркфлоу. Контейнеры-демоны отслеживаются по стандартным docker-меткам — отдельного файла состояния нет — и автоматически останавливаются вместе со стеком.

Полная схема — в [`../reference/config/commands/types.md#type-daemon`](../reference/config/commands/types.md#type-daemon); эта страница описывает приёмы, к которым вы будете обращаться при настройке.

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

Демоном, а не обычным `service_run`, это делают три вещи:

- **`service:` должен быть литеральным.** Шаблонизация (`${...}` / `{{...}}`) запрещена — метка `dwe.daemon.id` обязана оставаться стабильной между перезапусками, чтобы автодополнение, статус и авто-реап могли соотносить состояние.
- **`argv:` (или `cmd:`) — это долгоживущий процесс.** Без таймаута — именно он становится PID 1 в контейнере.
- **Блок `daemon:`** задаёт шаблон имени контейнера и значения жизненного цикла по умолчанию.

`service`, `workdir`/`workdir_from`, `user`, `env`, `params`, `argv`, `compose_args` подчиняются той же семантике, что и у `type: service_run`. Сама исходная команда `queue` **не** запускается напрямую — запускаются только четыре виртуальные команды.

## Четыре виртуальные команды

| Виртуальный ID | Что делает | Заметки |
|---|---|---|
| `queue.start` | `docker compose run -d --name <full> ... <argv>` | В отсоединённом режиме; `--no-deps` оставляет остальной стек нетронутым. |
| `queue.logs` | `docker logs -f --tail=100 <full>` | На переднем плане; Ctrl-C отсоединяет, но контейнер продолжает работать. |
| `queue.stop` | `docker stop -t <stop_timeout> <full>` | Идемпотентна — отсутствие контейнера не считается ошибкой. |
| `queue.restart` | `queue.stop`, затем `queue.start` | Реализована как виртуальный воркфлоу, пробрасывающий все объявленные параметры. |

Полный цикл использования:

```bash
# Запустить воркер
dwe cmd queue.start

# Смотреть логи (Ctrl-C отсоединяет, контейнер остаётся)
dwe cmd queue.logs

# Что сейчас запущено?
dwe status daemons

# Перезапустить после изменения конфига
dwe cmd queue.restart

# Остановить только этого демона
dwe cmd queue.stop
```

## Несколько экземпляров демона через `params:`

В примере выше объявлен один параметр (`name`), поэтому одно и то же определение демона может управлять сколь угодно большим числом экземпляров контейнера — по одному на очередь, по одному на пул воркеров, по одному на корневой каталог файлового watcher-а.

```bash
# Три воркера, по одному на очередь
dwe cmd queue.start --set name=emails
dwe cmd queue.start --set name=webhooks
dwe cmd queue.start --set name=video

dwe status daemons
# php_queue_emails    running   2m
# php_queue_webhooks  running   2m
# php_queue_video     running   1m

# Перезапустить только video
dwe cmd queue.restart --set name=video

# Остановить всё разом — см. «Авто-реап при dwe stop» ниже
dwe stop
```

Объявление `params:` задаёт как итоговое имя контейнера (через `${param.name}` в `container_template`), так и строку `argv:`. Два требования:

1. Каждый `${param.X}`, на который ссылается `container_template`, должен быть объявлен в `params:` **и** иметь regex в `pattern:`. Pattern носит рекомендательный характер; основная защита — regex, применяемый к уже отрендеренному полному имени контейнера (`^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$`).
2. Итоговое имя контейнера — это `<project.full>-<rendered template>`, поэтому оно остаётся уникальным между несколькими копиями одного проекта на одной машине.

## Поля блока `daemon:`

| Поле | По умолчанию | Эффект |
|---|---|---|
| `container_template` | — (обязательно) | Шаблон имени контейнера. Рендерится в пространстве шаблонов команды, затем к нему добавляется префикс `<project.full>-`. |
| `on_already_running` | `error` | `error` прерывает `.start`, если контейнер уже существует; `noop` делает `.start` идемпотентной (удобно для скриптов вида «запустить, если ещё не запущено»). |
| `auto_remove` | `true` | Когда `true`, `.start` добавляет `--rm`, чтобы контейнер удалялся при остановке (никаких зависших остановленных контейнеров в `docker ps -a`). |
| `stop_timeout` | `10s` | Сколько `docker stop` ждёт до SIGKILL. Строка длительности; значения меньше секунды округляются вверх до 1s. |

Если демону нужно окно мягкого завершения длиннее дефолтных 10s (например, «доделать текущую задачу» для queue-воркеров) — увеличьте `stop_timeout`:

```yaml
daemon:
  container_template: "php_queue_${param.name}"
  stop_timeout: 60s
```

## Видимость: метки и `dwe status daemons`

Каждый контейнер-демон несёт три стандартные метки, поэтому `docker ps` остаётся единственным источником истины:

- `dwe.project=<project.full>` — ограничивает область этим проектом.
- `dwe.daemon.id=<base>` — ID исходной команды (например, `services.main.queue`).
- `dwe.daemon.params=<json>` — параметры на момент запуска (например, `{"name":"emails"}`).

`dwe status daemons` фильтрует по этим меткам и показывает каждого работающего демона, имя его контейнера, ID демона, параметры запуска и время работы. Обращайтесь к ней всякий раз, когда задумываетесь «не забыл ли я что-то остановить?» — например, после перезапуска стека через `dwe restart` (см. следующий раздел).

## Авто-реап при `dwe stop`

При каждом запуске `dwe stop` к стоп-пайплайну спереди добавляется синтетическая фаза `_auto_reap_daemons`. Она перебирает все контейнеры с меткой `dwe.project=<full>` и непустым `dwe.daemon.id` и останавливает их параллельно. **Отключить это нельзя** — демоны всегда выключаются вместе со стеком. Фаза видна в выводе `dwe stop --plan`.

`dwe restart` — это `dwe stop`, за которым следует `dwe run`. На этапе остановки каждый демон реапится как обычно, но этап запуска их **не** перезапускает — демоны намеренно отделены от основного жизненного цикла и не запускаются автоматически при `dwe run`. Если после перезапуска они нужны вам снова — вызовите `<id>.start` явно:

```bash
dwe restart
dwe cmd queue.start --set name=emails
dwe cmd queue.start --set name=webhooks
```

Если приходится делать это каждый раз — заверните в команду-воркфлоу:

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

## Безопасность: держите секреты вне `params:`

Значения параметров попадают в метку `dwe.daemon.params` в виде JSON, который `docker inspect` открывает любому, у кого есть доступ к docker-сокету на хосте. **Никогда не кладите секреты в `params:`.**

Для токенов, паролей и любых чувствительных значений используйте `env:` — env-значения передаются через окружение контейнера (через `-e KEY` плюс само значение в `cmd.Env` хост-процесса), а не через argv хоста, поэтому они не появляются ни в `ps`, ни в `/proc/<pid>/cmdline`, ни в метках контейнера.

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

Работает и обратное правило: не пытайтесь различать экземпляры через `env:` — env-значения не попадают в имя контейнера, поэтому два демона с одинаковым `container_template` и разными `env:` столкнутся. Используйте `params:` для того, что должно быть частью идентичности (имя очереди, наблюдаемый каталог, размер пула); `env:` — для того, что нужно процессу для работы (учётные данные, строки подключения, фича-флаги).

## Перекрёстные ссылки

- [`../reference/config/commands/types.md#type-daemon`](../reference/config/commands/types.md#type-daemon) — полная схема, правила валидации, метки, реализация виртуальных команд.
- [`author-project-commands.md`](author-project-commands.md) — другие типы команд (`shell`, `service_exec`, `workflow`), которыми оборачивают демонов.
- [`daily-workflow.md`](daily-workflow.md) — `dwe status` и сценарий stop/restart.
