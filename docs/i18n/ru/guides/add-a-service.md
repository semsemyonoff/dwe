> Translated from: guides/add-a-service.md @ de5f1af8b48b

# Добавление сервиса

В проекте уже есть приложение `web`, и теперь вы хотите рядом отгрузить `worker` — фоновый процесс, который снимает задачи из очереди. Это руководство проходит по минимальному скелету: куда какой файл кладётся и как проверить результат до коммита.

Тот же рецепт применим, когда вы добавляете базу, UI админки или любой другой контейнер, который команда должна разделять между собой — меняется только `type:`.

## Выберите тип сервиса

Каждый сервис объявляет один из трёх типов. Выберите подходящий по роли:

| `type:` | Когда использовать | Пример |
|---------|--------------------|--------|
| `app` | Сервис, у которого исходный код живёт в репо, собирается и может рендерить IDE/AI/git-конфиг. | `web`, `worker`, `api` |
| `infra` | Бэкинг-контейнер без исходников. Другие сервисы могут на него `depends_on`. | `db`, `redis`, `minio` |
| `tool` | Standalone-утилитный контейнер — UI базы, mail catcher, фронтенд observability. Не может быть таргетом `depends_on`. | `adminer`, `mailpit` |

Если не уверены — по умолчанию `app`, когда репо владеет кодом, и `infra`, когда это готовый образ. Воркер, который запускает ваш код, — это `app`.

Справочник: [`../reference/config/services/index.md`](../reference/config/services/index.md) о полном per-type allowlist полей.

## Раскладка папки

Каждый сервис живёт в своей папке под `workspace/services/`. Имя папки **и есть** ключ сервиса — поля `name:` нет.

```
workspace/services/
  web/                  # уже есть
    service.yml
  worker/               # новый
    service.yml
```

Минимальный `service.yml` для воркера-приложения:

```yaml
# workspace/services/worker/service.yml
type: app
container: app-worker
dir: ./services/worker
dir_internal: /workspace
icon: "⚙️"
```

Этого достаточно для загрузки: без портов, без хостов (воркер не отдаёт трафик), а `container:` становится именем docker-контейнера. Если опустить `container:`, оно по умолчанию равно имени папки.

Для `infra`-сервиса с портом:

```yaml
# workspace/services/queue/service.yml
type: infra
required: true        # всегда-on бэкинг-сервис
container: queue
ports:
  amqp: 5672
```

См. [`../reference/config/services/fields.md`](../reference/config/services/fields.md) для каждого поля и того, что оно контролирует.

## Наследование полей через `extends:`

Если ваш новый app разделяет большую часть настроек с существующим (тот же базовый образ, те же mount-ы, тот же шелл), используйте `extends:` для наследования:

```yaml
# workspace/services/worker/service.yml
type: app
extends: web              # наследуем dir_internal, dirs, cli, configs, render…
container: app-worker
required: false           # web: required: true; worker — опциональный
dir: ./services/worker    # ребёнок задаёт свой dir
```

Что наследуется и что нет:

- **Наследуется**, когда ребёнок их опускает: `dir_internal`, `work_dir_internal`, `dirs`, `configs`, `cli`, `render`, `compose`.
- **Никогда не наследуется**: `container`, `required`, `depends_on`. Каждый ребёнок объявляет их явно.
- **List-поля** (`dirs`, `cli.env`) — родитель даёт дефолты, ребёнок добавляет записи.
- **`compose:`** — список ребёнка целиком заменяет родительский; мерджа нет.

`extends:` — **только для apps**. На `tool` и `infra` он отвергается при загрузке.

Справочник: [`../reference/config/services/extends.md`](../reference/config/services/extends.md).

## Подключение compose-оверлея

`service.yml` описывает DWE-метаданные. Сам контейнер — образ, команда, mount-ы, сети — живёт в Docker Compose-оверлейном файле, на который указывает `service.yml`:

```yaml
# workspace/services/worker/service.yml
type: app
container: app-worker
dir: ./services/worker
compose:
  - compose/services/worker/overlay.yml
```

```yaml
# compose/services/worker/overlay.yml
services:
  app-worker:
    image: ${DWE_PROJECT_PREFIX}/worker:latest
    build:
      context: ./services/worker
      dockerfile: Dockerfile
    command: ["node", "worker.js"]
    depends_on:
      - queue
    volumes:
      - ./services/worker:/workspace
```

Compose-файл — это обычный Docker Compose YAML; DWE не переписывает Compose, он компонует оверлеи. Команда `dwe compose files` показывает полный порядок мерджа оверлеев DWE.

## Регистрация переключателя в `defaults.yml`

Если сервис опциональный (не `required: true`), объявите состояние enabled по умолчанию в `workspace/defaults.yml`:

```yaml
# workspace/defaults.yml
services:
  worker:
    enabled: true     # включён у всех по умолчанию; индивидуально оверрайдится в local.yml
```

Разработчик, которому воркер не нужен, делает `dwe services disable worker`, и это пишет `services.worker.enabled: false` в `workspace/local.yml`.

Обязательные сервисы (`required: true` в `service.yml`) всегда включены и непереключаемы — для них этот шаг пропускайте.

Справочник: [`../reference/config/workspace.md`](../reference/config/workspace.md).

## Опционально: шаги деплоя для сервиса

Если вашему сервису нужна подготовка на деплой — установка зависимостей, прогон миграций, сборка ассетов — добавьте `deploy.yml` рядом с `service.yml`:

```yaml
# workspace/services/worker/deploy.yml
steps:
  - id: install-deps
    type: shell
    cmd: |
      $DWE_BIN shell worker -c "npm install"
  - id: run-migrations
    type: shell
    when: "${services.queue.enabled}"
    cmd: |
      $DWE_BIN shell worker -c "npm run migrate"
```

Эти шаги запускаются на `dwe deploy` для каждого включённого сервиса, у которого есть `deploy.yml`, в порядке, который вычисляет пайплайн деплоя. Шаги могут пропускаться через журнал, если соответствующий config-хеш не изменился — это автоматически.

Per-service `deploy.yml` валиден для любого типа сервиса (app / tool / infra), не только для apps. Используйте редко: простые контейнеры, которые просто `up`, в `deploy.yml` не нуждаются.

Справочник: [`../reference/config/deploy/index.md`](../reference/config/deploy/index.md).

## Опционально: render-паки (IDE / AI / git)

Для `type: app` DWE может рендерить per-service IDE-конфиг, AGENTS.md и сниппеты `.gitignore` из общих template-паков. Опт-ин в `service.yml`:

```yaml
# workspace/services/worker/service.yml
render:
  ide: { enabled: true, template: node }
  ai:  { enabled: true, template: node }
  git: { enabled: true, template: node }
```

Значение `template:` — это имя пака под `workspace/templates/{ide,ai,git}/<pack>/`. Запустите `dwe render ide` (и `dwe render ai`, `dwe render git`), чтобы посмотреть, что собирается у каждого рендера.

Если ничего из этого не нужно — опустите блок `render:` целиком.

См. [`shared-ide-and-agent-config.md`](shared-ide-and-agent-config.md) о полном template-pack workflow и цепочке разрешения пака.

## Проверка

После написания файлов, до деплоя:

```shell
dwe validate config services
```

Команда прогоняет per-service проверки схемы: обязательные поля по типу, разрешённые поля по типу, валидные диапазоны портов, корректно сформированная цепочка `extends:`, отсутствие циклов в `depends_on`.

Если валидация прошла, включите сервис и задеплойте:

```shell
dwe services enable worker         # если он был выключен в local.yml
dwe deploy run --service worker    # прогон только deploy-шагов этого сервиса
dwe run                            # поднять стек
dwe status                         # убедиться, что новый сервис поднялся
```

`dwe deploy run --service <имя>` целится в один сервис. Удобно при итеративной работе — не надо ждать перепрогона всего пайплайна.

Если что-то падает — смотрите `dwe logs worker` и [`troubleshooting.md`](troubleshooting.md) о распространённых модах отказа.
