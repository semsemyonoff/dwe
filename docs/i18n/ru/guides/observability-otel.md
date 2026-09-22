> Translated from: guides/observability-otel.md @ 6fe2b188eb59

# Наблюдаемость с OpenTelemetry

Вам нужны трейсы, метрики и логи dev-стека — для себя и для кодового агента, который в нём работает, — без правок в продакшен-образах сервисов и без того, чтобы включать это всем подряд. Это руководство строит опциональный tool-сервис `otel`, который любой разработчик включает одной командой:

```bash
dwe services enable otel --apply    # бэкенд поднят, сервисы экспортируют в него
dwe services disable otel --apply   # его нет; сервисы снова работают на своём обычном конфиге
```

Механизма «паков» в DWE нет, и он здесь не нужен: вся фича — это **tool-сервис, чей compose-оверлей заодно патчит соседние app-сервисы**. Когда сервис включён, оверлей передаётся в Compose и применяются обе половины; когда выключен, файл вообще не читается, и app-сервисы работают ровно по базовому определению без каких-либо накладных расходов.

Что получится в итоге:

- `grafana/otel-lgtm` — OTel Collector + Tempo (трейсы) + Loki (логи) + Prometheus (метрики) + Grafana в одном контейнере, доступный по `http://otel.<project>.localhost`.
- App-сервисы, экспортирующие OTLP в него и инструментированные под свой язык (ниже рецепты для Python, Go и Node).
- Текстовый поиск трейсов для агента (`dwe cmd otel.traces`) и раздел для вашего `AGENTS.md`, чтобы агент читал трейс раньше, чем код.

## 1. Tool-сервис

```yaml
# workspace/services/otel/service.yml
# tool: бэкенд OpenTelemetry (grafana/otel-lgtm). Опциональный, по умолчанию ВЫКЛЮЧЕН.
# Оверлей ЗАОДНО патчит app-сервисы, чтобы они экспортировали сюда, — один
# переключатель включает всю фичу целиком (см. compose/otel.yml).
type: tool
container: "otel"
compose:
  - compose/otel.yml
hosts:
  web: otel.myproject.localhost
icon: "📊"
info:
  title: "Grafana · traces, metrics, logs (OpenTelemetry)"
notes:
  disable: "Optional dev tool; the stack runs without it."
```

Без `ports:` — Grafana доступна через vhost Caddy, а приём OTLP (`otel:4318`) и query API Tempo (`otel:3200`) используются только внутри compose-сети. Сервис `type: tool` не может объявлять `depends_on`, и ему это не нужно: dev-инструмент никогда не блокирует стек.

Выключите его по умолчанию в `workspace/defaults.yml` (тяжёлый образ, шесть процессов и инструментированное приложение, о котором никто не просил):

```yaml
services:
  "otel":
    enabled: false
```

Добавьте vhost в Caddyfile. Caddy разрешает апстрим лениво, так что блок безвреден, пока контейнера нет:

```caddyfile
http://otel.myproject.localhost {
	reverse_proxy otel:3000
}
```

Запустите `dwe validate` перед первым `dwe run`: собственные валидаторы проекта могут пинить каждый опциональный сервис где-то ещё — например, сценарий интеграционного теста, перечисляющий, какие сервисы он включает и выключает, — и новый tool-сервис нужно добавить и туда.

Больше для Grafana за прокси ничего не нужно — собственный vhost в корне означает, что не нужны ни `GF_SERVER_ROOT_URL`, ни подпуть. Анонимный доступ в образе — `Admin`; для dev-машины нормально, но знать об этом стоит.

## 2. Оверлей

```yaml
# compose/otel.yml — бэкенд otel И патч app-сервисов.
# Относительные пути bind-маунтов разрешаются от каталога первого -f файла
# (compose.base — корень проекта при обычной раскладке), а не от compose/.
services:
  otel:
    # Пиньте версию: `:latest` залипает в локальном кэше образов. 0.33.1 — multi-arch.
    image: grafana/otel-lgtm:0.33.1
    # Без container_name: фиксированное имя обходит скоупинг compose-проекта и
    # конфликтует с копиями, которые запускает `dwe test` (`dwe validate` предупреждает).
    restart: unless-stopped
    # Образ не объявляет VOLUME: всё состояние (Grafana, Loki, Prometheus,
    # Tempo, Pyroscope) лежит в /data в записываемом слое, поэтому без тома каждое
    # пересоздание — правка оверлея + `dwe run`, enable/disable --apply,
    # `dwe reset` — теряет всю телеметрию. Именованный том в рамках проекта её
    # сохраняет. Дашборды берутся из репозитория (ниже).
    volumes:
      - otel_data:/data
      - ./configs/otel/provisioning/dashboards.yaml:/otel-lgtm/grafana/conf/provisioning/dashboards/grafana-dashboards.yaml:ro
      - ./configs/otel/dashboards:/otel-lgtm/grafana-dashboards-myproject:ro
    # В образе есть собственный HEALTHCHECK (`/otel-lgtm/docker/healthcheck.sh`,
    # 30s / 5s / 3 retries), но БЕЗ start_period. `dwe run` использует `compose up
    # --wait`; холодный старт шести процессов занимает больше 90 с, так что без этого
    # блока контейнер становится unhealthy и весь `dwe run` падает.
    healthcheck:
      test: ["CMD", "/otel-lgtm/docker/healthcheck.sh"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 120s
      start_interval: 5s

  # --- патч базового app-сервиса -------------------------------------------
  # Compose сливает это с базовым определением: `command` ЗАМЕНЯЕТСЯ,
  # `environment` и `volumes` СЛИВАЮТСЯ. Никакого `depends_on: otel` ни в одну
  # сторону — спаны, отправленные до того, как коллектор ответит, отбрасываются
  # с предупреждением в логе приложения, и это задуманное поведение.
  app:
    environment:
      OTEL_SERVICE_NAME: "app"
      OTEL_RESOURCE_ATTRIBUTES: "service.namespace=${COMPOSE_PROJECT_NAME},deployment.environment.name=dev"
      OTEL_EXPORTER_OTLP_ENDPOINT: "http://otel:4318"
      OTEL_EXPORTER_OTLP_PROTOCOL: "http/protobuf"
      OTEL_TRACES_EXPORTER: "otlp"
      OTEL_METRICS_EXPORTER: "otlp"
      OTEL_LOGS_EXPORTER: "none"
      # В dev сценарий «сделал запрос — посмотрел»: сбрасываем спаны каждую 1 с и
      # отправляем метрики каждые 10 с (панелям с rate() нужно два сэмпла).
      OTEL_BSP_SCHEDULE_DELAY: "1000"
      OTEL_METRIC_EXPORT_INTERVAL: "10000"

volumes:
  otel_data:
```

Ловушки, о которых стоит знать до первого `dwe run`:

- **`dwe validate` не видит отсутствующий `start_period`.** Сбой проявляется как зависший или упавший `dwe run`, а не как предупреждение.
- **Приложение отвечает 502 через прокси ~30 с после `enable --apply`**, пока его контейнер пересоздаётся. Ждите его health-эндпоинт, а не возврата из команды `dwe`.
- **Первые минуты все панели с `rate()` показывают «No data».** Это интервал экспорта, а не сломанный пайплайн; сделайте несколько запросов и сначала загляните в Explore → Tempo.
- **`depends_on` на опциональный сервис в базовом `compose.yaml` ломает стек, пока этот сервис выключен.** Держите связку внутри оверлея.
- **Патчите только сервисы, которые существуют всегда, когда включён инструмент.** В блоке патча нет ни `image:`, ни `build:`, поэтому он валиден только поверх определения из более раннего файла. Патчите сервис из базового `compose.yaml` или всегда включённый. Если пропатченное приложение живёт в собственном оверлее (например, `compose/services/site.yml`) и разработчик выключает его, оставив `otel` включённым, `compose/otel.yml` всё равно объявляет `site:` только с `environment:` / `volumes:`, и Compose отвергает весь проект: у сервиса нет ни образа, ни контекста сборки.
- **Оверлей tool-сервиса не может переопределить то, что задаёт собственный оверлей приложения.** DWE передаёт compose-файлы в порядке `base → tools → infra → apps` (по алфавиту внутри каждой группы; `dwe compose files` печатает цепочку), поэтому если app-сервис определён в своём `compose/<app>.yml`, а не в базовом файле, этот файл идёт *после* `compose/otel.yml` и выигрывает при каждом слиянии целым значением: `command:`, `healthcheck:` и любой ключ `environment:`, который задают оба файла. Новые env-ключи и дополнительные `volumes:` по-прежнему сливаются нормально. Проектируйте патч так, чтобы он только добавлял, — внедрённый скрипт, который сам делает свою настройку (рецепт для Node ниже), вместо заменённого `command:`.

### Дашборды

Образ провижинит три дашборда: RED-метрики (классическая гистограмма), RED-метрики (нативная гистограмма — нужна фича Prometheus, которую этот бандл не включает) и дашборд для JVM. Замените его файл провайдера своим, оставьте классический RED и направьте второй провайдер на каталог в вашем репозитории:

```yaml
# configs/otel/provisioning/dashboards.yaml
apiVersion: 1
providers:
  - name: "RED Metrics (classic histogram)"
    type: file
    options:
      path: /otel-lgtm/grafana-dashboard-red-metrics-classic.json
      foldersFromFilesStructure: false
  - name: "myproject"
    type: file
    folder: "myproject"
    allowUiUpdates: true
    disableDeletion: false
    options:
      path: /otel-lgtm/grafana-dashboards-myproject
      foldersFromFilesStructure: false
```

UID источников данных в образе — `prometheus`, `tempo`, `loki`, `pyroscope`. Обратной записи из контейнера в репозиторий нет: правьте в UI, копируйте JSON-модель, вставляйте её поверх файла в `configs/otel/dashboards/`, коммитьте.

Не рассчитывайте, что дашборд переедет между языками. Имена метрик следуют инструментированию: Python экспортирует `http_server_duration_milliseconds_*` с `http_target` плюс метрики процесса/GC, а рецепт для Go ниже только трейсовый и никаких метрик приложения не экспортирует. Проверить, что реально приходит, можно через `curl http://otel:9090/api/v1/label/__name__/values` из любого контейнера. Единственное, что переносится без изменений, — панели на span-метриках Tempo (`traces_spanmetrics_*`: `span_name`, `span_kind`, `status_code`, `service`).

## 3. Инструментирование сервисов

Оверлей несёт окружение OTEL_*; что каждый сервис с ним делает, зависит от языка. Цель у всех рецептов одна: в репозитории приложения нет никакого диффа, пока инструмент выключен, и как можно меньше — когда включён.

### Python — без диффа

Автоинструментирование через `opentelemetry-instrument`, накладываемое поверх venv проекта при старте контейнера. Оверлей заменяет `command` приложения:

```yaml
  app:
    command:
      - uv
      - run
      - --frozen
      - --with
      - opentelemetry-distro==0.65b0
      - --with
      - opentelemetry-exporter-otlp-proto-http==1.44.0
      - --with
      - opentelemetry-instrumentation-fastapi==0.65b0
      - --with
      - opentelemetry-instrumentation-sqlalchemy==0.65b0
      - --with
      - opentelemetry-instrumentation-redis==0.65b0
      - watchfiles
      - --filter
      - python
      - opentelemetry-instrument python -m app
      - app
    environment:
      OTEL_PYTHON_FASTAPI_EXCLUDED_URLS: "healthz"
```

Пины относятся к одному релизному поезду (SDK 1.44.0 ↔ contrib 0.65b0); поднимайте их вместе. Инструментируйте только один слой БД (SQLAlchemy *или* asyncpg), иначе каждый запрос появится дважды. Дочерний процесс, который запускает вотчер, наследует наложенный PATH, так что горячая перезагрузка продолжает работать. `--with` разрешается мимо `uv.lock`; если фича остаётся надолго, перенесите пакеты в недефолтную группу зависимостей и используйте `uv run --frozen --group otel`. Корневые спаны для фоновых воркеров и обработчиков ботов автоматически не появляются: несколько строк на `opentelemetry-api` (без SDK это no-op) вокруг каждого тика дают трейсу имя. OTLP по HTTP (4318), а не gRPC — не нужно собирать wheel `grpcio` на свежем Python.

### Go — небольшой дифф, включаемый через env

Рантайм-инъекции для Go нет; приложение линкует SDK. Держите его инертным, пока эндпоинт не задан, — по образцу «пустой DSN — нет SDK», которым пользуется большинство трекеров ошибок:

```go
// internal/telemetry/telemetry.go
// Init устанавливает OTel SDK, когда задан OTEL_EXPORTER_OTLP_ENDPOINT, и
// возвращает функцию завершения. При пустой переменной ничего не делает, и
// обёртки otelhttp / otelpgx ниже проваливаются в глобальный no-op провайдер.
func Init(ctx context.Context) (shutdown func(context.Context) error, err error) {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}
	exp, err := otlptracehttp.New(ctx) // эндпоинт, протокол, заголовки: всё из OTEL_* env
	if err != nil {
		return nil, err
	}
	res, _ := resource.New(ctx,
		resource.WithFromEnv(),
		// Не WithProcess(): он включает WithProcessCommandArgs(), и argv — DSN,
		// токены, переданные флагами, — попал бы в каждый экспортируемый ресурс.
		resource.WithProcessPID(),
		resource.WithProcessExecutableName(),
		resource.WithProcessRuntimeName(),
		resource.WithProcessRuntimeVersion(),
	)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(time.Second)),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{}) // читает/пишет `traceparent`
	return tp.Shutdown, nil
}
```

```go
// HTTP-сервер: otelhttp самым внешним, health-пробы отфильтрованы. chi знает
// шаблон маршрута только ПОСЛЕ роутинга, поэтому внешний хендлер не может назвать
// спан; второй, внутренний middleware переименовывает его, когда запрос уже смаршрутизирован.
r.Use(func(next http.Handler) http.Handler {
	return otelhttp.NewHandler(next, "http", otelhttp.WithFilter(func(r *http.Request) bool {
		switch r.URL.Path {
		case "/healthz", "/readyz", "/version":
			return false
		}
		return true
	}))
})
r.Use(func(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if pattern := chi.RouteContext(r.Context()).RoutePattern(); pattern != "" {
			trace.SpanFromContext(r.Context()).SetName(r.Method + " " + pattern)
		}
	})
})

// Пул pgx: подключаем трейсер к конфигу пула. Запросы, сгенерированные sqlc,
// начинаются с `-- name: GetEntries :many`; без функции имени эта строка-комментарий
// становится именем спана, поэтому выводим его из заголовка.
cfg, err := pgxpool.ParseConfig(dsn)
if err != nil {
	return nil, fmt.Errorf("parse database DSN: %w", err)
}
cfg.ConnConfig.Tracer = otelpgx.NewTracer(
	otelpgx.WithIncludeQueryParameters(), // буквальные значения параметров в спанах: только для dev-приёмника
	otelpgx.WithSpanNameFunc(sqlcSpanName),
)
pool, err := pgxpool.NewWithConfig(ctx, cfg)
```

Рецепт **только трейсовый**: `Init` не ставит MeterProvider, поэтому `otelhttp` пишет свои метрики в глобальный no-op meter, и ничего не экспортируется. Задайте `OTEL_METRICS_EXPORTER: "none"` в патче Go-сервиса, а не копируйте `otlp` из примера выше. RED-панели для Go-сервиса строятся на span-метриках Tempo (`traces_spanmetrics_*`), которые выводятся из самих трейсов.

Модули: `go.opentelemetry.io/otel`, `go.opentelemetry.io/otel/sdk`, `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp`, `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp`, `github.com/exaring/otelpgx`. Ожидайте, что `go get` поднимет всё семейство `go.opentelemetry.io/otel`, которое SDK трекера ошибок уже подтянул транзитивно, — держите их на одной версии. Исходящим HTTP-вызовам нужен `otelhttp.NewTransport(http.DefaultTransport)`. Строки логов коррелируют с трейсами через небольшую обёртку `slog.Handler`, которая добавляет `trace_id`/`span_id` из `trace.SpanContextFromContext(ctx)`, когда спан валиден, — иначе это no-op, так что её можно ставить безусловно.

`otelpgx` ≥ 0.12 говорит на актуальных семантических конвенциях: `db.system.name`, `db.query.text`, `db.operation.name`, `db.namespace`, `db.collection.name`, плюс собственный `pgx.query.parameters`. Всё, что читает спаны, должно знать и старый, и новый набор ключей — см. раздел 4.

### Node — без диффа через `NODE_OPTIONS`

Node загружает инструментирование раньше приложения, если попросить об этом через `--import`; в репозитории приложения ничего не меняется. Пакеты лежат в каталоге воркспейса со своим `package.json`, монтируются в контейнер и устанавливаются при первом старте:

```yaml
  site:
    environment:
      NODE_OPTIONS: "--import /opt/otel-node/register.mjs"
      OTEL_SERVICE_NAME: "site"
      OTEL_RESOURCE_ATTRIBUTES: "service.namespace=${COMPOSE_PROJECT_NAME},deployment.environment.name=dev"
      OTEL_EXPORTER_OTLP_ENDPOINT: "http://otel:4318"
      # exporter-trace-otlp-http умеет только JSON; protobuf-вариант — это
      # другой пакет. Пишите то, что правда, а не копируйте значение из Go.
      OTEL_EXPORTER_OTLP_PROTOCOL: "http/json"
      OTEL_TRACES_EXPORTER: "otlp"
      OTEL_METRICS_EXPORTER: "none"
      OTEL_LOGS_EXPORTER: "none"
      OTEL_NODE_ENABLED_INSTRUMENTATIONS: "http,undici"
      OTEL_BSP_SCHEDULE_DELAY: "1000"
    volumes:
      - ./workspace/otel/node:/opt/otel-node
```

```text
workspace/otel/node/
  package.json         # @opentelemetry/api (та же 1.x, что и собственная копия приложения!),
  package-lock.json    # auto-instrumentations-node, sdk-node, sdk-trace-base,
                       # exporter-trace-otlp-http, instrumentation — запиненные
  ensure.sh            # `npm ci` с проверкой хэша, не фатальный, запускается из register.mjs
  register.mjs         # точка входа для --import
  instrumentation.mjs  # NodeSDK + span processor
```

`register.mjs` — единственное, что называет `NODE_OPTIONS`, и он обязан быть защитным, потому что **`NODE_OPTIONS` действует на весь контейнер**: он доходит до самого `npm`, до синхронизации зависимостей в образе и до `node -e` в compose-хелсчеке. Поэтому он (1) сразу возвращается, когда `process.argv[1]` пуст (это проба хелсчека), (2) синхронно запускает `ensure.sh` с очищенным `NODE_OPTIONS` для дочернего `npm ci` (иначе установка рекурсивно зайдёт в саму себя), (3) вызывает `module.register('@opentelemetry/instrumentation/hook.mjs', import.meta.url)` — ESM-хук загрузчика, который позволяет `instrumentation-http` пропатчить сервер фреймворка, — и (4) импортирует `instrumentation.mjs`, который запускает `NodeSDK` с `getNodeAutoInstrumentations()` и OTLP-экспортером, настроенным из окружения.

Undici (глобальный `fetch`) перехватывается через `diagnostics_channel`, так что SSR-фреймворк, вызывающий бэкенд, передаёт `traceparent` без кода. Что нарушает обещание «без диффа» или добавляет шум:

- **Sentry SDK v8+ в приложении.** `Sentry.init` запускает собственную настройку OpenTelemetry. Глобальный реестр `@opentelemetry/api` работает по принципу «кто первый, тот и прав», поэтому SDK, зарегистрированный из `--import`, сохраняет провайдер, но предзагруженные инструментирования http/undici от Sentry всё равно работают и дают второй серверный спан на каждый запрос через *ваш* провайдер. Поскольку оверлей не может переопределить `SENTRY_DSN` приложения (см. ловушку с порядком выше), `register.mjs` удаляет `SENTRY_DSN` из `process.env` до старта приложения: пока инструмент включён, SSR-половина Sentry выключена. Браузерная половина (`PUBLIC_*`, встраивается при сборке) не затронута. Скажите об этом в комментариях оверлея.
- **Health-пробы.** Собственный процесс compose-хелсчека пропускается проверкой `argv[1]`, но его запрос всё равно доходит до инструментированного сервера. Фильтрация в конфиге http-инструментирования пропускает спаны, которые создают другие инструментирования, поэтому отбрасывайте их в обёртке `SpanProcessor` вокруг `BatchSpanProcessor` (`onEnd`: путь `/` без `user-agent`, `/@vite/`, `/@fs/`, `/node_modules/`, `/__astro*`) — она стоит после всех инструментирований. Учтите, что хелсчек, рендерящий полную страницу, всё равно создаёт реальный трафик к бэкенду на каждом интервале, и бэкенд его трейсит.
- **Нет `http.route` на стороне сайта.** Фреймворк не передаёт шаблон маршрута в инструментирование, так что серверные спаны именуются по пути. Группируйте по префиксу пути в инструменте поиска.

## 4. Трейсы текстом для агента

Grafana — для людей. Агенту нужна команда с компактным выводом, и для этого достаточно HTTP API Tempo, причём всего двух эндпоинтов: `GET :3200/api/search?q=<TraceQL>&start=&end=&limit=&spss=` и `GET :3200/api/traces/<hex>`. Поисковый индекс отстаёт на 10–15 с; трейс по id читается сразу, и именно это делает цикл поиска ниже надёжным.

Рецепт поставляет Python-скрипт только на stdlib (`workspace/otel/traces.py`) с подкомандами `summary`, `list`, `show <id>`, `traceparent`, `services` и `selftest`. Он работает внутри любого контейнера стека, где есть `python3`, — в самом образе `otel` есть только `curl` и `bash`; проверьте кандидата через `docker exec <container> python3 --version` (в Go- и PHP-образах на Debian он обычно есть, в Alpine — нет), — и монтируется оверлеем, так что команда существует ровно тогда, когда существует бэкенд:

```yaml
# compose/otel.yml, в пропатченном app-сервисе
    volumes:
      - ./workspace/otel:/opt/otel:ro
```

```yaml
# workspace/commands/otel.yml
group:
  title: Otel
  description: "Trace lookup against the otel tool service (Tempo), for triage without opening Grafana"
  # Скрыта, пока инструмент выключен: маунта выше тогда нет.
  hide: '{{ not (index .Raw "services" "otel" "enabled") }}'

commands:
  traces:
    type: service_exec
    description: "Traces as text (SQL, slow spans, N+1, exceptions): -- summary | list | show <id> | traceparent | -h"
    service: app
    mode: exec-or-fail   # никогда не поднимать одноразовый контейнер без трафика
    workdir: /workspace/src
    argv: [python3, /opt/otel/traces.py, "${args}"]
    messages:
      error: "otel.traces failed — is the app running? (dwe run); is otel enabled? (dwe services enable otel --apply)"
```

Если ни в одном контейнере нет Python, добавьте сайдкар `python:3-alpine` вторым tool-сервисом и направьте команду на него. Семантические конвенции сдвинулись: новые инструментирования (Go, Node) пишут `db.query.text` / `db.system.name` там, где старые пишут `db.statement` / `db.system`, и `http.request.method` / `url.path` вместо `http.method` / `http.target`. Инструмент поиска должен читать оба варианта.

Цикл, которым пользуется агент:

```bash
dwe cmd otel.traces -- traceparent            # печатает значение заголовка + trace_id
curl -H 'traceparent: 00-<id>-<span>-01' http://api.myproject.localhost/api/v1/things
dwe cmd otel.traces -- show <trace_id>        # ровно этот запрос, без поиска
dwe cmd otel.traces -- summary --last 15m     # когда вы ещё не знаете, ГДЕ искать
```

`show` сворачивает повторяющиеся соседние спаны (`×25 SELECT … <- possible N+1`) и печатает исключения со стек-фреймами самого приложения. У запроса, отправленного с `traceparent`, созданным вызывающей стороной, есть удалённый родитель, поэтому Tempo не может назвать его корень; он попадает в строку `-` в `summary`.

## 5. Расскажите агенту

Добавьте короткий раздел в `AGENTS.md` (или на страницу, на которую он ссылается). Формулировка, которая сработала:

> Only while the optional `otel` tool is enabled (`dwe services list`). When the question is "what did this request actually do" — too many queries, a slow endpoint, a 500, a misbehaving worker — look at the trace BEFORE reading code: `dwe cmd otel.traces -- summary --last 15m` when you do not know where yet, the `traceparent → curl → show` loop when you do. Some periodic jobs are chatty by design; compare against what the code says the job should do before calling it a regression.

Без такого указателя агент обходит `dwe` целиком (`docker logs`, `docker exec … psql`), а объявленная read-only команда `db.query` экономит ему больше ходов, чем трейсы.

## Известные шероховатости

- Без тома `otel_data` каждое пересоздание контейнера теряет всю телеметрию: правка оверлея и `dwe run`, `dwe services enable|disable --apply`, `dwe reset`. Остановка и повторный запуск того же контейнера (`dwe restart otel`, `dwe stop`, затем `dwe run`) её сохраняют в любом случае.
- На уровне логирования debug собственные HTTP-вызовы OTLP-экспортера появляются в логе приложения примерно раз в секунду (Python: `urllib3.connectionpool … POST /v1/traces`). Увеличьте `OTEL_BSP_SCHEDULE_DELAY` или заглушите этот логгер.
- Health-пробы, исключённые из HTTP-инструментирования, всё равно дают трейсы без родителя — `PING` / `SELECT` / `connect` — от своих проверок БД. Фильтруйте через `--exclude-root` / `--kind server` в инструменте поиска или на коллекторе.
- Span-метрики Tempo несут только `span_name`, так что SQL-спан назван по своему глаголу; сам запрос виден только внутри трейса.

## См. также

- [Добавление сервиса](add-a-service.md) — форма tool-сервиса, на которой строится это руководство.
- [Авторство проектных команд](author-project-commands.md) — `service_exec`, `hide:`, `${args}`.
- [`../reference/config/services/index.md`](../reference/config/services/index.md) — `type: tool`, `compose:`-оверлеи, `hosts:`.
