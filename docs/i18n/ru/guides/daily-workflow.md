> Translated from: guides/daily-workflow.md @ d6776417ac0f

# Повседневная работа

После [подключения к проекту](joining-a-project.md) и зелёного первого деплоя ежедневная работа с DWE сводится к небольшому набору команд. Это руководство проходит по ним примерно в том порядке, в каком вы будете к ним обращаться: проверить статус, переключить сервис, зайти в шелл, запустить проектную команду, посмотреть логи, остановить или перезапустить.

## Быстрый статус

```shell
dwe status
```

По умолчанию выводится здоровье стека и каждая секция по порядку: apps, tools, infra, deploy, topology, git workspace, daemons. Каждую секцию можно адресовать напрямую — удобно, когда нужен только один срез:

```shell
dwe status apps
dwe status deploy
dwe status daemons
```

Чтобы сократить дефолтный вывод, передавайте флаги `--no-<секция>` — например, `dwe status --no-git --no-topology` пропустит две самые медленные секции, если хочется быстро проверить контейнеры.

`dwe status` — read-only и работает без локов, так что её безопасно вызывать во время идущего деплоя или рестарта. Печатаемая «обложка» проекта (URL-ы, host-алиасы, креды) — это `dwe info`, она описана в [подключении к проекту](joining-a-project.md#дашборд-info).

## Переключение опциональных сервисов

Большинство проектов поставляют пару опциональных сервисов — дашборд метрик, второго воркера, UI для админки. Они объявлены `enabled: false` в `workspace/defaults.yml`, и вы включаете их per-machine.

Интерактивный multi-select:

```shell
dwe services
```

Откроется чек-лист опциональных сервисов. Подтверждение запишет ваш выбор в `workspace/local.yml` и пересоберёт `.env`.

CLI-форма:

```shell
dwe services enable adminer
dwe services disable second
```

По умолчанию переключение только пишет `local.yml` и фиксирует **отложенную операцию**. Контейнер ещё не запущен и не остановлен — `dwe status` подсветит отложенные изменения. Чтобы немедленно выполнить лайфсайкл-шаги, передайте `--apply`:

```shell
dwe services enable adminer --apply
```

Без `--apply` следующий `dwe deploy` (или `dwe deploy run --service adminer`) применит новый выбор. Модель отложенных изменений нужна, чтобы можно было набатчить несколько переключений и применить их за один проход.

Справочник: [`../reference/config/state/index.md`](../reference/config/state/index.md) о механике pending-state, [`../reference/config/services/index.md`](../reference/config/services/index.md) о схеме.

## Шелл-доступ

```shell
dwe shell <сервис>
```

Открывает интерактивный шелл внутри указанного контейнера. Без аргумента команда автоматически выбирает единственный включённый сервис или показывает селектор, если их несколько.

`mode:` определяет, как открывается шелл:

| Режим | Поведение |
|-------|-----------|
| `auto` (по умолчанию) | `docker exec`, если контейнер работает; `compose run --rm`, если нет; ошибка, если контейнер остановлен. |
| `exec` | Всегда `docker exec` — ошибка, если контейнер не работает. |
| `run` | Всегда поднимает свежий контейнер через `docker compose run --rm`. Удобно, когда нужен чистый шелл, не трогая работающий. |

```shell
dwe shell main --mode run --shell sh
dwe shell main --root
dwe shell main --user deploy --workdir /app
```

Для одноразового запуска (один вызов команды и выход с её exit-кодом) используйте `-c`:

```shell
dwe shell main -c "composer install"
dwe shell main -c "php artisan migrate" --mode run
```

TTY выделяется только если и stdin, и stdout — терминалы, так что пайпы работают корректно (`dwe shell main -c "ls -la" | grep ...`). Шелл-бинарь, пользователь, рабочая директория и env-дефолты берутся из блока `cli:` каждого сервиса в `service.yml`; флаги переопределяют per-invocation.

## Запуск проектных команд

Проекты объявляют свои операции под `workspace/commands/` — сидинг базы, сброс кэшей, инициализация окружения, деплои. Каждая операция становится вызываемой командой:

```shell
dwe commands              # интерактивный селектор по всем публичным командам
dwe commands list         # обычный листинг
dwe commands db.seed      # прямой запуск
dwe cmd db.seed           # `cmd` — короткий алиас
```

Параметры передаются через `--set key=value`:

```shell
dwe cmd db.seed --set env=staging --set count=10
```

Для скриптов:

- `-y` / `--yes` пропускает промты подтверждения (в том числе любое `confirmation: true` у команды).
- `--silent` подавляет финальное desktop-уведомление для команд с `notify: true`.
- `-i` / `--inspect` печатает разрешённое определение команды вместо запуска — удобно проверить, что соберут `--set` и шаблонизатор, перед выполнением.

```shell
dwe cmd db.seed --yes --silent
dwe cmd db.seed --inspect
```

Справочник: [`../reference/config/commands/index.md`](../reference/config/commands/index.md), [`../reference/config/commands/types.md`](../reference/config/commands/types.md).

## Просмотр логов

```shell
dwe logs <сервис>
```

Стримит логи Docker-контейнера для одного сервиса. По умолчанию печатает последние 50 строк и выходит. Чтобы следить:

```shell
dwe logs main --follow
dwe logs main --tail 100 --follow
dwe logs main --since 5m
```

`Ctrl-C` прерывает стрим; стек продолжает работать. `--since` принимает длительность (`5m`, `1h`) или RFC3339-временную метку. С `--output json` вывод — NDJSON (по одному `{"ts","stream","msg"}` на строку) для пайпа в инструменты сбора логов.

`dwe logs` — read-only и без локов, как и `dwe status`.

## Повторный осмотр состояния проекта

Для URL-ов, host-алиасов и печатаемой «где что лежит» страницы смотрите [подключение к проекту → дашборд info](joining-a-project.md#дашборд-info) — `dwe info` это та же команда, только в повседневном использовании.

## Стоп и рестарт

```shell
dwe stop
dwe restart
```

`dwe stop` запускает полный жизненный цикл остановки: before-stop hooks → `docker compose down` → after-stop hooks. Демоны, объявленные пользовательскими командами, авто-реапятся (отказа нет). `dwe restart` склеивает полный stop с фазой `run`, пропуская git-update пробу.

Per-service варианты:

```shell
dwe stop main
dwe restart main
```

Они обходят compose и lifecycle-хуки — напрямую вызывают `docker stop` / `docker restart` для одного контейнера. Single-service форма работает даже после того, как сервис был отключён — иногда именно это нужно при подчистке только что переключённого сервиса. Демоны при per-service stop/restart не реапятся.

Для голой остановки через compose без хуков остаются низкоуровневые команды: `dwe docker stop` (остановить контейнеры на месте) и `dwe docker down` (остановить и удалить). Это аварийные люки; в обычной работе нужны `dwe stop` и `dwe restart`.

## Что дальше

- [`troubleshooting.md`](troubleshooting.md) — что делать, когда одна из команд на этой странице вернула несчастливый ответ.
- [`switching-tasks-with-snapshots.md`](switching-tasks-with-snapshots.md) — поставить одну задачу на паузу, переключиться на другую и вернуться без потери состояния.
