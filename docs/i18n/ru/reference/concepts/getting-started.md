> Translated from: reference/concepts/getting-started.md @ 7c7c9334834b

# Начало работы

Первое прохождение Devbox: войти в проект, запустить пайплайн деплоя, поднять стек и прочитать информационную панель.

## Содержание

- [Войти в проект](#войти-в-проект)
- [Первый `devbox deploy`](#первый-devbox-deploy)
- [Первый `devbox run`](#первый-devbox-run)
- [Первый `devbox info`](#первый-devbox-info)
- [Что читать дальше](#что-читать-дальше)

## Войти в проект

Проект Devbox — это любая директория с `devbox.yml` в корне. CLI автоматически обнаруживает её, идя вверх от текущей рабочей директории.

Минимальный скелет проекта:

```text
my-project/
├── devbox.yml
└── devbox/
    ├── defaults.yml
    └── services/
        └── web/
            └── service.yml
```

`devbox.yml` объявляет идентичность проекта:

```yaml
schema_version: "2"

project:
  name: my-project
  prefix: devbox
```

`devbox/defaults.yml` несёт дефолты runtime и карту переключений опциональных сервисов:

```yaml
runtime:
  use_https: false

services:
  web:
    enabled: true
```

`devbox/services/web/service.yml` объявляет один сервис:

```yaml
type: app
description: Web application

container: my-project-web

compose:
  files:
    - compose/services/web.yml

dirs:
  base: services/web
  src: services/web/src

ports:
  http:
    name: HTTP
    container: 80
    host: 8080

hosts:
  primary:
    name: Primary
    value: my-project.localhost
```

Перейдите в проект и подтвердите, что CLI его распознаёт:

```sh
cd my-project
devbox validate
```

`devbox validate` запускает проверки готовности проекта: env-пробы, декларативные проверки и валидаторы по доменам (сервисы, deploy, info, styles, …). Завершается с ненулевым кодом, если какая-то проверка падает. См. [`validate.yml`](../config/validate.md) для каталога проверок.

## Первый `devbox deploy`

Пайплайн деплоя устанавливает, настраивает и мигрирует прикладные сервисы. Он декларативен — `devbox/deploy.yml` перечисляет фазы и шаги. Если файла нет, Devbox использует встроенный дефолтный пайплайн, который инлайнит собственный `devbox/services/<name>/deploy.yml` каждого включённого сервиса, запускает `docker up --wait` и выводит информационную панель.

Просмотрите разрешённый план перед запуском:

```sh
devbox deploy plan
```

`deploy plan` только читает. Он загружает оркестратор и каждый включённый сервисный пайплайн, разворачивает шаблоны и цепочки `extends:`, применяет топологический порядок из `after:` и печатает финальное дерево фаз/шагов без выполнения.

Выполните деплой:

```sh
devbox deploy run
```

Запуск рапортует статус фаз и шагов маркерами `✓ ✗ ◎ ·` и зеркалит вывод в `.devbox/logs/deploy.log`. Состояние журналируется в `.devbox/deploy/state.yml`, чтобы повторные запуски пропускали шаги, у которых `action_hash` и входы не изменились — см. [Состояние и блокировки](state-and-locks.md).

Минимальный `devbox/deploy.yml` выглядит так:

```yaml
log: true

phases:
  - name: services
    deploy_services: true

  - name: start
    steps:
      - name: docker-up
        type: devbox
        cmd: docker up --wait
```

Маркер `deploy_services: true` говорит оркестратору инлайнить `devbox/services/<name>/deploy.yml` каждого включённого сервиса в этой точке в топологическом порядке. Фаза `start` затем поднимает стек через Docker Compose. См. [`deploy.yml`](../config/deploy/index.md) для всех поддерживаемых типов шагов и билтинов.

## Первый `devbox run`

`devbox run` управляет жизненным циклом, определённым в `devbox/lifecycle.yml`:

```sh
devbox run
```

Порядок выполнения: опциональная проба обновления Git → before-run хуки → `docker compose up` → `docker compose wait` → after-run хуки → опциональный вывод info → финальное сообщение о готовности.

Используйте `--no-update`, чтобы пропустить пробу обновления Git на чистой выгрузке, или `--update on`, чтобы её форсировать:

```sh
devbox run --no-update
devbox run --update on
```

Правило приоритета: `--no-update` > `--update` > `lifecycle.yml.update.mode` — см. [`lifecycle.yml`](../config/lifecycle.md).

Остановите стек через `devbox stop` (запускает `before-stop` хуки → `docker compose down` → `after-stop` хуки). Перезапустите через `devbox restart` (stop + run с `--no-update`).

## Первый `devbox info`

Информационная панель читает `devbox/info.yml` и рендерит проектный контекст: заголовок проекта, URL и хосты включённых сервисов, группы команд и любые кастомные секции.

```sh
devbox info
```

По умолчанию Devbox запускает `info` автоматически в конце `run` и `deploy run`. Отдельная команда — те же данные по требованию.

`info.yml` поддерживает элементы `type: auto-urls` и `type: auto-hosts`, которые разворачиваются во время рендеринга из карт `ports:` и `hosts:` каждого включённого сервиса — поэтому панель остаётся синхронизированной с оверлеями сервисов без ручных правок. См. [`info.yml`](../config/info.md).

## Что читать дальше

- [Архитектура](architecture.md) — как `cli/`, `core/` и `shared/` складываются вместе; что встроено vs читается в runtime.
- [Раскладка проекта](project-layout.md) — для чего каждая папка под `devbox/`, и что генерируется под `.devbox/`.
- [Пайплайны](pipelines.md) — модель выполнения phase / step / condition, которую разделяют deploy, reset и lifecycle.
- [Интеграция с Docker](docker.md) — сборка compose-файлов, вывод имени проекта, случаи обхода lifecycle.
- [Состояние и блокировки](state-and-locks.md) — что записывает `state.yml`, как `deploy.lock` и `snapshot.lock` сериализуют мутации.
- [Справочник по конфигурации](../config/index.md) — справочник на уровне полей, когда вы знаете форму системы.
- Запустите `devbox docs` для того же содержимого в интерактивном браузере или `devbox docs llms-txt` для компактного индекса для AI-агента.
