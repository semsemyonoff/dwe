> Translated from: guides/preflight-checks.md @ 0ac09d8b4b04

# Preflight-проверки

Деплой, упавший через три минуты, потому что `ghcr.io` отклонил pull, — хуже, чем деплой, который вообще отказался стартовать. Preflight-проверки — это ворота готовности, которые CLI прогоняет перед тем, как любой шаг деплоя, запуск контейнера или остановка коснутся вашей машины. Они отвечают на вопрос «готово ли окружение?» и прерываются с полезной диагностикой, если нет.

DWE поставляется с семью встроенными env-пробами (docker / git / shell / порты) и декларативным `workspace/validate.yml`, куда вы добавляете проверки под конкретный проект: «авторизованы ли мы в реестре?», «задана ли `DATABASE_URL`?», «поднят ли корпоративный VPN?». Это руководство разбирает оба слоя.

Полная схема — в [`../reference/config/validate.md`](../reference/config/validate.md); эта страница описывает приёмы, к которым вы будете обращаться при настройке.

## Встроенные пробы `env.*`

Семь проб встроены в CLI и запускаются при каждом вызове `dwe validate` и при каждом preflight (отключить их нельзя). Они покрывают предусловия уровня хоста, общие для всех проектов DWE:

| Проба | Падает, когда |
|---|---|
| `env.docker_bin` | `docker` отсутствует в `PATH` (или указанное переопределение недоступно). |
| `env.docker_daemon` | Сокет docker-демона недоступен. |
| `env.docker_compose` | Плагин Compose v2 отсутствует или слишком старый. |
| `env.git_bin` | `git` отсутствует в `PATH`. |
| `env.shell_bin` | Настроенный шелл отсутствует. |
| `env.project_perms` | Корень проекта недоступен для записи. |
| `env.ports_free` | Порт хоста, объявленный включённым сервисом, занят посторонним процессом или другим compose-проектом. (На стадии `stop` проба пропускает сама себя.) |

Запустить их по отдельности:

```shell
dwe validate env                # все семь
dwe validate env ports_free     # одну пробу
```

Это первая команда, к которой стоит обратиться, когда что-то идёт не так — см. [`troubleshooting.md`](troubleshooting.md).

## Объявление проверки в `validate.yml`

Проверки под конкретный проект идут в `workspace/validate.yml`. У каждой записи есть ID, описание, стадии, на которых она выполняется, и тело — либо один из встроенных видов проверок, либо пользовательская команда:

```yaml
# workspace/validate.yml
checks:
  - id: ghcr-login
    description: Authenticated against ghcr.io
    stages: [deploy]
    severity: error
    hint: |
      Run `docker login ghcr.io` with a personal access token.
    type: builtin
    cmd: shell
    with:
      cmd: docker pull ghcr.io/owner/private-image:latest --quiet
      timeout: 30s

  - id: project-deps
    description: Required CLIs installed
    stages: [run]
    type: command
    cmd: deps.check
```

Принимаются два значения `type:`:

- **`type: builtin`** — направляет к одному из пяти встроенных видов проверок, описанных ниже. Тело проверки лежит в `with:`.
- **`type: command`** — направляет к декларативной пользовательской команде из `workspace/commands/`. Цель должна быть `type: shell` или `type: script` — workflow, service_exec и прочие отклоняются при загрузке. `with:` передаётся команде как полезная нагрузка `params:`.

Файл необязателен: если его нет, выполняются только пробы `env.*`.

## Встроенные виды проверок

Под `type: builtin` доступны пять видов проверок. Все они годятся и в качестве тела `check:` у шага деплоя, так что одну и ту же форму можно переиспользовать между пайплайнами.

### `shell`

Запускает однострочник под POSIX `sh -c`. Выход с кодом 0 — успех.

```yaml
- id: registry-reachable
  description: ghcr.io reachable
  stages: [deploy]
  type: builtin
  cmd: shell
  with:
    cmd: curl -fsS -o /dev/null https://ghcr.io/v2/
    timeout: 5s
```

Шелл здесь всегда `sh`, независимо от настроенного в проекте, — так проверки остаются переносимыми между хостами.

### `file_exists`

Проверяет наличие файла на диске (путь относительно корня проекта).

```yaml
- id: db-dump-present
  description: Seed dump exists for first-run import
  stages: [deploy]
  severity: warning
  hint: Download from s3://team-dumps/latest.sql and place at .dwe/seed.sql
  type: builtin
  cmd: file_exists
  with:
    path: .dwe/seed.sql
```

### `executable_in_path`

Проверяет, что исполняемый файл находится через `PATH`.

```yaml
- id: jq-installed
  description: jq available for compose introspection
  stages: [deploy]
  severity: warning
  type: builtin
  cmd: executable_in_path
  with:
    name: jq
```

### `env_keys_present`

Проверяет, что один или несколько ключей присутствуют с непустыми значениями в файле формата `.env`. Записи `KEY=`, `KEY=""` и `KEY=''` все считаются пустыми.

```yaml
- id: app-secrets
  description: Required app secrets configured in .env
  stages: [run, deploy]
  severity: error
  hint: |
    Copy .env.example to .env and fill in:
      DATABASE_URL, REDIS_URL, JWT_SECRET
  type: builtin
  cmd: env_keys_present
  with:
    file: .env
    keys: [DATABASE_URL, REDIS_URL, JWT_SECRET]
```

### `tcp_reachable`

Пытается установить TCP-соединение с `host:port`.

```yaml
- id: corporate-vpn
  description: Internal git mirror is reachable (VPN up?)
  stages: [deploy, run]
  severity: error
  hint: Connect to the corporate VPN and retry.
  type: builtin
  cmd: tcp_reachable
  with:
    host: git.internal.example.com
    port: 22
    timeout: 2s
```

## Стадии

Проверка выполняется, когда стадия вызывающей стороны входит в её список `stages:`. Зарезервированы четыре стадии со встроенными хуками:

| Стадия | Запускается через |
|---|---|
| `deploy` | `dwe deploy run`, `dwe validate --stage deploy` |
| `run` | `dwe run`, `dwe restart` (этап запуска), `dwe validate --stage run` |
| `stop` | `dwe stop`, `dwe restart` (этап остановки), `dwe validate --stage stop` |
| `command` | `dwe validate --stage command` (зарезервировано; автоматического хука пока нет) |

`dwe validate` без `--stage` прогоняет все проверки независимо от стадии. `dwe restart` составной (запускает оба этапа — `stop` и `run`). `dwe reset run` использует только стадию `stop`.

Опечатки в `stages:` при загрузке вызывают предупреждение с подсказкой ближайшего совпадения, так что `stages: [deplooy]` не превратится молча в проверку, которая никогда не запускается.

## Привязка к сервисам

Некоторые проверки имеют смысл только при включённом конкретном сервисе — JWT-секрет важен, только если поднят контейнер API. Используйте `services:`, чтобы включать проверку по принципу ИЛИ:

```yaml
- id: api-jwt-secret
  description: JWT_SECRET configured for API
  stages: [run, deploy]
  services: [api]              # запускается только если api включён
  severity: error
  hint: Set JWT_SECRET in services/api/.env
  type: builtin
  cmd: env_keys_present
  with:
    file: services/api/.env
    keys: [JWT_SECRET]
```

Семантика:

- Опустить `services:` → проверка выполняется всегда (когда совпадает стадия).
- `services: [api]` → выполняется тогда и только тогда, когда `api` включён в текущем локальном конфиге.
- `services: [api, worker]` → выполняется, если включён `api` ИЛИ `worker`. Если все перечисленные сервисы отключены — проверка молча пропускается.

Привязка к сервисам и фильтр по стадиям — независимые фильтры, объединяемые по И: сначала проверяется совпадение стадии, затем привязка к сервису.

## Severity и `--strict`

Каждая проверка объявляет severity, который определяет, как preflight реагирует на её падение:

| `severity:` | По умолчанию? | Влияние на код выхода |
|---|---|---|
| `error` | да | Preflight падает; deploy/run/stop прерывается. |
| `warning` | нет | Печатается диагностика; пайплайн продолжается. С `dwe validate --strict` предупреждения становятся ошибками. |
| `info` | нет | Печатается диагностика; ничего не блокируется. |

`error` — значение по умолчанию, оставляйте его для обычного случая. Берите `warning`, когда проверка отражает «мягкое ожидание» (например, «вам, вероятно, нужен seed-дамп»), а `info` — для информационного вывода.

## Линтеры (только в validate)

`workspace/validate.yml` также предоставляет блок `linters:` для запуска известных внешних линтеров (shellcheck, hadolint) и произвольных адаптеров `type: generic`. Линтеры работают **только** при `dwe validate` — в preflight они **не** запускаются. Preflight отвечает на вопрос «можем ли мы запуститься?», а не «чист ли код?».

```yaml
linters:
  shellcheck:
    paths: [workspace/scripts, scripts]
    severity: warning
  hadolint:
    paths: ["."]
    filenames: [Dockerfile]
```

Встроенные адаптеры срабатывают автоматически: если исполняемый файл есть в `PATH` и подходящие файлы найдены — линтер запускается; иначе он молча пропускается. Укажите `enabled: false`, чтобы явно его отключить. Полная схема — в [`../reference/config/validate.md#external-linters`](../reference/config/validate.md#external-linters).

## Отдельный вызов

Не обязательно ждать `dwe deploy`, чтобы прогнать проверку — её можно вызвать напрямую:

```shell
dwe validate                        # все домены (env, config, checks, linters)
dwe validate env                    # только семь встроенных env-проб
dwe validate checks                 # только декларативные проверки
dwe validate checks ghcr-login      # одна проверка по ID
dwe validate linters                # только линтеры
dwe validate linters shellcheck     # один линтер
```

`dwe validate checks <id>` вдобавок обходит привязку `services:`, так что можно проверить привязанную запись, даже когда все её целевые сервисы отключены.

Полезные флаги:

- `--stage <name>` — ограничить `checks.*` одной стадией.
- `--strict` — предупреждения дают ненулевой код выхода.
- `--quiet` — скрыть строки `ok` / `info`.

Если во время итераций проверка срабатывает в preflight слишком часто или слишком ретиво, любая lifecycle-команда принимает `--skip-preflight`. Используйте его с осторожностью: он обходит все пробы, включая env-проверки.

## Перекрёстные ссылки

- [`../reference/config/validate.md`](../reference/config/validate.md) — полная схема, домены валидатора, правила адаптеров линтеров.
- [`troubleshooting.md`](troubleshooting.md) — что делать, когда проба падает.
- [`author-project-commands.md`](author-project-commands.md) — написание пользовательских команд, к которым обращаются проверки `type: command`.
