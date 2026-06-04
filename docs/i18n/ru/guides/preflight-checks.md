> Translated from: guides/preflight-checks.md @ 0ac09d8b4b04

# Preflight-проверки

Деплой, упавший через три минуты, потому что `ghcr.io` отверг pull, — хуже, чем деплой, который сразу отказался стартовать. Preflight-проверки — это ворота готовности, которые CLI прогоняет до любого шага деплоя, старта контейнера или остановки на вашей машине. Они отвечают на вопрос «мир готов?» и прерывают с полезной диагностикой, если нет.

DWE поставляет семь захардкоженных env-проб (docker / git / shell / порты) плюс декларативный `workspace/validate.yml`, куда вы добавляете проектные — «авторизованы ли мы в registry?», «задана ли `DATABASE_URL`?», «поднят ли корпоративный VPN?». Это руководство проходит по обоим слоям.

Полная схема — в [`../reference/config/validate.md`](../reference/config/validate.md); эта страница про authoring-паттерны.

## Встроенные пробы `env.*`

Семь проб захардкожены в CLI и запускаются на каждом `dwe validate` и каждом preflight (отключить их нельзя). Они покрывают host-уровневые предусловия, общие для каждого проекта DWE:

| Проба | Падает, когда |
|---|---|
| `env.docker_bin` | `docker` не в `PATH` (или переопределение отсутствует). |
| `env.docker_daemon` | docker-сокет демона недоступен. |
| `env.docker_compose` | Плагин Compose v2 отсутствует или слишком старый. |
| `env.git_bin` | `git` не в `PATH`. |
| `env.shell_bin` | Настроенный шелл отсутствует. |
| `env.project_perms` | Корень проекта недоступен на запись. |
| `env.ports_free` | Host-порт, объявленный включённым сервисом, занят чужим процессом или другим compose-проектом. (Само-пропускается на стадии `stop`.) |

Запустить их по отдельности:

```shell
dwe validate env                # все семь
dwe validate env ports_free     # одну пробу
```

Это правильная первая команда, когда что-то чувствуется не так — см. [`troubleshooting.md`](troubleshooting.md).

## Объявление проверки в `validate.yml`

Проектные проверки идут в `workspace/validate.yml`. У каждой записи есть ID, описание, стадии, на которых она работает, и тело — либо встроенный inspection-вид, либо пользовательская команда:

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

- **`type: builtin`** — диспетчер на один из пяти встроенных видов проверок ниже. Тело лежит в `with:`.
- **`type: command`** — диспетчер на декларативную пользовательскую команду из `workspace/commands/`. Цель должна быть `type: shell` или `type: script` — workflow, service_exec и другие отвергаются при загрузке. `with:` пробрасывается как `params:`-payload команды.

Файл опциональный: при его отсутствии работают только `env.*`-пробы.

## Виды встроенных проверок

Пять inspection-видов доступны под `type: builtin`. Все они также годятся как тела `check:` у deploy-шага, так что одну и ту же форму можно переиспользовать между пайплайнами.

### `shell`

Запускает one-liner под POSIX `sh -c`. Exit 0 = pass.

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

Шелл всегда `sh` независимо от настроенного в проекте — это держит проверки переносимыми между хостами.

### `file_exists`

Проверяет наличие файла на диске (относительно корня проекта).

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

Проверяет, что бинарь резолвится через `PATH`.

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

Проверяет, что один или несколько ключей существуют с непустыми значениями в `.env`-стиле файла. `KEY=`, `KEY=""` и `KEY=''` все считаются пустыми.

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

Пытается TCP-дозвон до `host:port`.

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

Проверка работает, когда стадия вызывающего входит в её список `stages:`. Зарезервированы четыре стадии со встроенными хуками:

| Стадия | Триггер |
|---|---|
| `deploy` | `dwe deploy run`, `dwe validate --stage deploy` |
| `run` | `dwe run`, `dwe restart` (run-нога), `dwe validate --stage run` |
| `stop` | `dwe stop`, `dwe restart` (stop-нога), `dwe validate --stage stop` |
| `command` | `dwe validate --stage command` (зарезервировано; автоматического хука пока нет) |

`dwe validate` без `--stage` прогоняет каждую проверку независимо от стадии. `dwe restart` композитный (фиксирует обе ноги `stop` и `run`). `dwe reset run` использует только стадию `stop`.

Опечатки в `stages:` дают warning при загрузке с near-match подсказкой, так что `stages: [deplooy]` не будет молча никогда не запускаться.

## Гейтинг по сервисам

Некоторые проверки имеют смысл только при включённом конкретном сервисе — JWT-секрет важен, только если контейнер API поднят. Используйте `services:` для OR-гейта:

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

- Опустить `services:` → проверка работает всегда (когда совпадает стадия).
- `services: [api]` → работает iff `api` включён в текущем локальном конфиге.
- `services: [api, worker]` → работает iff `api` ИЛИ `worker` включён. Все перечисленные отключены — молчаливый skip.

Service-гейт и stage-фильтр — независимые AND-фильтры: сначала совпадение стадии, потом гейт по сервису.

## Severity и `--strict`

Каждая проверка объявляет severity, который контролирует, как preflight реагирует на её падение:

| `severity:` | По умолчанию? | Эффект на exit-код |
|---|---|---|
| `error` | да | Preflight падает; deploy/run/stop прерывается. |
| `warning` | нет | Диагностика печатается; пайплайн продолжается. С `dwe validate --strict` warning-и становятся ошибками. |
| `info` | нет | Диагностика печатается; никогда не блокирует. |

`error` — дефолт, оставляйте для частого случая. Берите `warning`, когда проверка фиксирует «мягкое ожидание» (например, «вам наверное нужен seed-dump»), а `info` — для информативного вывода.

## Линтеры (только validate)

`workspace/validate.yml` также экспонирует блок `linters:` для запуска известных внешних линтеров (shellcheck, hadolint) и произвольных адаптеров `type: generic`. Линтеры работают **только** на `dwe validate` — они **не** идут в preflight. Preflight отвечает на «можем ли мы запуститься?», а не на «чист ли код?».

```yaml
linters:
  shellcheck:
    paths: [workspace/scripts, scripts]
    severity: warning
  hadolint:
    paths: ["."]
    filenames: [Dockerfile]
```

Встроенные адаптеры авто-детектят: если бинарь в `PATH` и подходящие файлы есть — линтер работает; иначе молча пропускается. Поставьте `enabled: false`, чтобы опт-аутнуться явно. См. [`../reference/config/validate.md#external-linters`](../reference/config/validate.md#external-linters) о полной схеме.

## Standalone-вызов

Не обязательно ждать `dwe deploy`, чтобы прогнать проверку — её можно вызвать напрямую:

```shell
dwe validate                        # каждый домен (env, config, checks, linters)
dwe validate env                    # только семь захардкоженных env-проб
dwe validate checks                 # только декларативные проверки
dwe validate checks ghcr-login      # одна проверка по ID
dwe validate linters                # только линтеры
dwe validate linters shellcheck     # один линтер
```

`dwe validate checks <id>` к тому же обходит гейт `services:`, так что можно проверить gated-запись, даже когда её таргет-сервисы все отключены.

Полезные флаги:

- `--stage <name>` — ограничить `checks.*` одной стадией.
- `--strict` — warning-и дают non-zero exit.
- `--quiet` — спрятать строки `ok` / `info`.

Если проверка срабатывает в preflight слишком часто или слишком эффективно, пока вы итерируете, — каждая lifecycle-команда принимает `--skip-preflight`. Используйте осторожно: он обходит каждую пробу, включая env-овые.

## Перекрёстные ссылки

- [`../reference/config/validate.md`](../reference/config/validate.md) — полная схема, домены валидатора, правила адаптера линтеров.
- [`troubleshooting.md`](troubleshooting.md) — что делать, когда проба падает.
- [`author-project-commands.md`](author-project-commands.md) — авторство пользовательских команд, на которые диспетчерится `type: command`.
