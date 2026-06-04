> Translated from: guides/add-a-service.md @ 2efb25e1a48a

# Добавление сервиса

В проекте уже есть приложение `web`, и теперь вы хотите добавить рядом `worker` — фоновый процесс, который разбирает задачи из очереди. Это руководство показывает минимальный набор файлов, необходимый для нового сервиса: где какой файл лежит и как проверить результат до коммита.

Тот же рецепт работает и при добавлении базы, админ-панели или любого другого контейнера, общего для всей команды, — меняется только `type:`.

## Выберите тип сервиса

Каждый сервис объявляет один из трёх типов. Выберите подходящий по роли:

| `type:` | Когда использовать | Пример |
|---------|--------------------|--------|
| `app` | Сервис, исходный код которого лежит в репозитории, собирается и может рендерить IDE/AI/git-конфиг. | `web`, `worker`, `api` |
| `infra` | Вспомогательный контейнер без исходников. Другие сервисы могут указывать его в `depends_on`. | `db`, `redis`, `minio` |
| `tool` | Самостоятельный служебный контейнер — UI для базы, перехватчик почты, фронтенд для observability. Не может быть целью `depends_on`. | `adminer`, `mailpit` |

Если сомневаетесь — выбирайте `app`, когда код сервиса лежит в репозитории, и `infra`, когда это готовый образ. Воркер, выполняющий ваш собственный код, — это `app`.

Справочник: [`../reference/config/services/index.md`](../reference/config/services/index.md) — полный список допустимых полей для каждого типа.

## Раскладка папки

Каждый сервис живёт в собственной папке внутри `workspace/services/`. Имя папки **и есть** ключ сервиса — поля `name:` нет.

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

Этого достаточно для загрузки: без портов, без хостов (воркер не обслуживает трафик), а `container:` становится именем docker-контейнера. Если `container:` опустить, оно по умолчанию совпадает с именем папки.

Для `infra`-сервиса с портом:

```yaml
# workspace/services/queue/service.yml
type: infra
required: true        # постоянно работающий вспомогательный сервис
container: queue
ports:
  amqp: 5672
```

См. [`../reference/config/services/fields.md`](../reference/config/services/fields.md) — описание каждого поля и того, на что оно влияет.

## Наследование полей через `extends:`

Если у вашего нового приложения большая часть настроек совпадает с существующим (тот же базовый образ, те же монтирования, тот же шелл), используйте `extends:` для наследования:

```yaml
# workspace/services/worker/service.yml
type: app
extends: web              # наследуем dir_internal, dirs, cli, configs, render…
container: app-worker
required: false           # web — required: true; worker — опциональный
dir: ./services/worker    # потомок задаёт свой dir
```

Что наследуется, а что нет:

- **Наследуется**, если потомок его не задаёт: `dir_internal`, `work_dir_internal`, `dirs`, `configs`, `cli`, `render`, `compose`.
- **Никогда не наследуется**: `container`, `required`, `depends_on`. Каждый потомок обязан объявлять их явно.
- **Поля-списки** (`dirs`, `cli.env`) — родитель задаёт значения по умолчанию, потомок добавляет к ним свои записи.
- **`compose:`** — список потомка целиком заменяет родительский, а не объединяется с ним.

`extends:` работает **только для приложений**. Для `tool` и `infra` он отклоняется при загрузке.

Справочник: [`../reference/config/services/extends.md`](../reference/config/services/extends.md).

## Подключение compose-оверлея

`service.yml` описывает метаданные в формате DWE. Сам контейнер — образ, команда, монтирования, сети — описывается в Docker Compose-оверлее, на который ссылается `service.yml`:

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

Compose-файл — это обычный Docker Compose YAML; DWE не переписывает Compose, а компонует оверлеи. Команда `dwe compose files` показывает полный порядок, в котором DWE объединяет оверлеи.

## Регистрация переключателя в `defaults.yml`

Если сервис опциональный (не `required: true`), задайте, включён ли он по умолчанию, в `workspace/defaults.yml`:

```yaml
# workspace/defaults.yml
services:
  worker:
    enabled: true     # включён у всех по умолчанию; каждый может переопределить в local.yml
```

Разработчик, которому воркер не нужен, выполняет `dwe services disable worker`, и это записывает `services.worker.enabled: false` в `workspace/local.yml`.

Обязательные сервисы (`required: true` в `service.yml`) включены всегда и не переключаются — для них этот шаг пропускайте.

Справочник: [`../reference/config/workspace.md`](../reference/config/workspace.md).

## Опционально: шаги деплоя для сервиса

Если вашему сервису нужна подготовка во время деплоя — установка зависимостей, прогон миграций, сборка ассетов — добавьте `deploy.yml` рядом с `service.yml`:

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

Эти шаги выполняются при `dwe deploy` для каждого включённого сервиса, у которого есть `deploy.yml`, в порядке, который вычисляет пайплайн деплоя. Шаг может быть пропущен через журнал, если соответствующий хеш конфига не изменился, — это происходит автоматически.

Отдельный `deploy.yml` допустим для сервиса любого типа (app / tool / infra), не только для приложений. Применяйте его экономно: простым контейнерам, которым достаточно `up`, он не нужен.

Справочник: [`../reference/config/deploy/index.md`](../reference/config/deploy/index.md).

## Опционально: render-паки (IDE / AI / git)

Для сервисов с `type: app` DWE может рендерить для каждого сервиса IDE-конфиг, AGENTS.md и фрагменты `.gitignore` из общих наборов шаблонов. Подключите это в `service.yml`:

```yaml
# workspace/services/worker/service.yml
render:
  ide: { enabled: true, template: node }
  ai:  { enabled: true, template: node }
  git: { enabled: true, template: node }
```

Значение `template:` — это имя набора в `workspace/templates/{ide,ai,git}/<pack>/`. Запустите `dwe render ide` (а также `dwe render ai`, `dwe render git`), чтобы посмотреть, что выдаст каждый из рендереров.

Если ничего из этого не нужно — целиком опустите блок `render:`.

См. [`shared-ide-and-agent-config.md`](shared-ide-and-agent-config.md) — полный воркфлоу работы с наборами шаблонов и цепочка их разрешения.

## Проверка

Написав файлы, проверьте их перед деплоем:

```shell
dwe validate config services
```

Команда прогоняет проверки схемы для каждого сервиса: обязательные поля по типу, допустимые поля по типу, корректные диапазоны портов, правильно сформированная цепочка `extends:`, отсутствие циклов в `depends_on`.

Если валидация прошла, включите сервис и запустите стек:

```shell
dwe services enable worker    # если он был выключен в local.yml
dwe run                       # поднять стек
dwe status                    # убедиться, что новый сервис поднялся
```

Если вы добавили `deploy.yml` для `worker`, можно сначала прогнать только его шаги деплоя:

```shell
dwe deploy run --service worker    # только если у worker есть deploy.yml
dwe run
dwe status
```

`dwe deploy run --service <имя>` завершается ошибкой, если у сервиса нет `deploy.yml`: это не замена `dwe run` и не способ запустить сервис, у которого нет пайплайна деплоя.

Если что-то падает — смотрите `dwe logs worker` и [`troubleshooting.md`](troubleshooting.md) о типичных причинах сбоев.
