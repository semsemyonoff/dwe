> Translated from: guides/preflight-checks.md @ 0118a90a2c11

# Preflight-проверки

Деплой, падающий через три минуты из-за того, что `ghcr.io` отверг pull, хуже, чем деплой, который вообще отказался стартовать. Preflight — это гейт готовности, который DWE запускает до того, как любой шаг деплоя, старт контейнера или остановка тронут твою машину. Он отвечает на вопрос «готов ли мир?» и прерывается с полезной диагностикой, если нет.

На каждом preflight работают два слоя: семь захардкоженных host-probe'ов (docker / git / shell / порты — отключить нельзя) и проектные проверки, которые ты объявляешь в `workspace/validate.yml`. Этот гайд — про **рабочий процесс авторинга**; пополевая схема (каждый probe, builtin, стадия и severity) живёт в [`../reference/config/validate.md`](../reference/config/validate.md).

## Добавь первую проверку

Проверка — это ID, описание, стадии, на которых она бежит, и тело. Положи это в `workspace/validate.yml`:

```yaml
checks:
  - id: ghcr-login
    description: Authenticated against ghcr.io
    stages: [deploy]
    hint: Run `docker login ghcr.io` with a personal access token.
    type: builtin
    cmd: shell
    with:
      cmd: docker pull ghcr.io/owner/private-image:latest --quiet
      timeout: 30s
```

Теперь `dwe deploy run` откажется стартовать, пока pull не пройдёт, печатая твой `hint`, если не прошёл. Файл опционален — без `validate.yml` бегут только host-probe'ы.

Тело — это либо `type: builtin` (встроенный вид инспекции: `shell`, `file_exists`, `env_keys_present`, `config_keys_present`, `tcp_reachable` — [полный список с параметрами](../reference/config/validate.md#available-builtins)), либо `type: command`, который диспатчит на пользовательскую команду `type: shell`/`script` из `workspace/commands/`.

## Когда проверка запускается

`stages:` решает, на каком моменте жизненного цикла срабатывает проверка — `deploy`, `run`, `stop` или `post-setup` (полная таблица в [reference](../reference/config/validate.md#stages)). Один момент стоит выделить здесь:

- Проверка, зависящая от значения, которое **setup-визард** пишет в `local.yml`, должна использовать `stages: [post-setup]`, а не `[deploy]`. Проверка `[deploy]` бежит ещё и на раннем pre-wizard gate, где это значение ещё не задано — поэтому заблокирует тебя до того, как ты доберёшься до визарда. `post-setup` бежит **только** на финальном preflight: после визарда либо прямо перед деплоем, когда визарда нет (например `dwe deploy run`).

`services: [api]` дополнительно гейтит проверку на случай, когда включён названный сервис (семантика ИЛИ по списку). Gate по стадии и по сервису — независимые AND-фильтры; см. [привязку к сервисам](../reference/config/validate.md#service-gating).

## Рецепт: требовать значение перед деплоем

Визард спрашивает значение и пишет его в `local.yml`; проверка `post-setup` гарантирует, что оно действительно задано перед деплоем — и ловит это на `dwe deploy run` тоже, где визард не запускается:

```yaml
checks:
  - id: db-api-key-set
    description: db.api_key must be set before deploy
    stages: [post-setup]
    hint: Run `dwe deploy` and complete the wizard, or set db.api_key in workspace/local.yml.
    type: builtin
    cmd: config_keys_present
    with:
      keys: [db.api_key]
```

`config_keys_present` читает **смерженный конфиг в памяти**, поэтому сразу видит запись визарда в `local.yml` — без зависимости от отрендеренного `.env`. Проверяй тот же точечный путь, который использует `writes:` визарда: top-level неймспейс вроде `db.*` или `app.*`. (Посервисные секреты не могут жить по пути `services.<name>.env.*` в `local.yml` — держи их в `.env` сервиса и проверяй через `env_keys_present`.) Подробности: [`config_keys_present`](../reference/config/validate.md#config_keys_present).

## Запуск проверок по требованию

Не обязательно ждать `dwe deploy`, чтобы запустить проверку:

```shell
dwe validate                     # все домены (env, config, checks, linters)
dwe validate env                 # только семь host-probe'ов
dwe validate checks              # только декларативные проверки
dwe validate checks ghcr-login   # одна проверка по ID (также обходит services-gate)
dwe validate --stage deploy      # только проверки, привязанные к стадии
```

Полезные флаги: `--strict` (предупреждения дают ненулевой код выхода), `--quiet` (скрыть ok/info-строки). Пока итерируешь, любая lifecycle-команда принимает `--skip-preflight`, чтобы полностью обойти гейт — используй осторожно, он пропускает и host-probe'ы.

`dwe validate env` — правильная первая команда, когда что-то ощущается не так; см. [`troubleshooting.md`](troubleshooting.md).

> В `validate.yml` также есть блок `linters:` (shellcheck / hadolint / кастомные адаптеры). Линтеры бегут только на `dwe validate` — **никогда** в preflight, который отвечает «можем ли мы запуститься?», а не «чист ли код?». Схема: [внешние линтеры](../reference/config/validate.md#external-linters).

## Перекрёстные ссылки

- [`../reference/config/validate.md`](../reference/config/validate.md) — полная схема: каждый probe, builtin, стадия, severity и блок `linters:`.
- [`troubleshooting.md`](troubleshooting.md) — что делать, когда probe падает.
- [`author-project-commands.md`](author-project-commands.md) — авторинг пользовательских команд, на которые диспатчат проверки `type: command`.
