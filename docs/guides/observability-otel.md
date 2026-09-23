# Observability with OpenTelemetry

You want traces and metrics for your dev stack — for yourself and for the coding agent working in it — without touching the production images of your services and without turning it on for everyone. This guide builds an opt-in `otel` tool service that any developer switches on with one command:

```bash
dwe services enable otel --apply    # backend up, services export to it
dwe services disable otel --apply   # gone; services run their plain config again
```

There is no "pack" mechanism in DWE and none is needed: a **tool service whose `compose:` overlay ships the backend and whose `compose_after:` overlay patches the neighbouring app services** is the whole feature. Enabled, both files are passed to Compose; disabled, neither is read, so the app services run exactly their base definition with zero overhead.

What you end up with:

- `grafana/otel-lgtm` — OTel Collector + Tempo (traces) + Loki (logs) + Prometheus (metrics) + Grafana in one container, reachable at `http://otel.<project>.localhost`. Loki ships in the image, but the recipes here do not export application logs (`OTEL_LOGS_EXPORTER: "none"`): logs stay in the container's own output and correlate with traces where the app writes `trace_id`/`span_id` into its log lines (the Go `slog` wrapper below). Switching OTLP logs on (for example `OTEL_LOGS_EXPORTER: "otlp"` with Python's distro) is possible but not covered or verified here.
- App services exporting OTLP to it, instrumented per language (recipes below for Python, Go, Node and PHP).
- A text trace lookup for the agent (`dwe cmd otel.traces`) plus a section for your `AGENTS.md`, so the agent reads a trace before it reads the code.

## 1. The tool service

```yaml
# workspace/services/otel/service.yml
# tool: OpenTelemetry backend (grafana/otel-lgtm). Optional, OFF by default.
# The backend lives in compose:; the app patch that makes services export to
# it lives in compose_after:, so it wins over each app's own overlay — one
# toggle switches the whole feature (see compose/otel.yml and
# compose/otel-apps.yml).
type: tool
container: "otel"
compose:
  - compose/otel.yml
compose_after:
  - compose/otel-apps.yml
hosts:
  web: otel.myproject.localhost
icon: "📊"
info:
  title: "Grafana · traces, metrics (OpenTelemetry)"
notes:
  disable: "Optional dev tool; the stack runs without it."
```

No `ports:` — Grafana is reached through the proxy vhost, and OTLP ingest (`otel:4318`) and Tempo's query API (`otel:3200`) are used only inside the compose network. A `type: tool` service cannot declare `depends_on`, and it must not: a dev tool never gates the stack.

Turn it off by default in `workspace/defaults.yml` (heavy image, six processes, and an instrumented app nobody asked for):

```yaml
services:
  "otel":
    enabled: false
```

Add the vhost to your Caddyfile. Caddy resolves the upstream lazily, so the block is harmless while the container does not exist:

```caddyfile
http://otel.myproject.localhost {
	reverse_proxy otel:3000
}
```

nginx is not lazy: a literal `proxy_pass http://otel:3000` is resolved once at startup, and nginx refuses to start when the name does not exist yet — the `otel` container is created alongside nginx with no `depends_on`. Resolve per request through Docker's DNS instead, and let the otel overlay mount the vhost template into the (always-on) proxy service, so nginx sees it only while the tool is on:

```nginx
# configs/nginx/templates/tools/otel.conf.template — the official image's
# envsubst replaces only variables set in its environment, so $host stays.
server {
    listen 80;
    server_name ${OTEL_HOST};
    resolver 127.0.0.11 valid=10s;
    set $otel_upstream http://otel:3000;
    location / {
        proxy_pass $otel_upstream;
        proxy_set_header Host $host;
        proxy_set_header Upgrade $http_upgrade;     # Grafana Live websocket
        proxy_set_header Connection "upgrade";
    }
}
```

```yaml
# compose/otel.yml — next to the otel service
  nginx:
    volumes:
      - ./configs/nginx/templates/tools/otel.conf.template:/etc/nginx/templates/otel.conf.template:ro
    environment:
      OTEL_HOST: "${OTEL_HOST:-otel.myproject.localhost}"
```

```yaml
# workspace/defaults.yml — .env carries the host only while the tool is on
exports:
  env:
    - name: OTEL_HOST
      from: services.otel.hosts.web
      when: services.otel.enabled
```

Run `dwe validate` before the first `dwe run`: a project's own validators may pin every optional service somewhere else too — an integration-test scenario that lists which services it enables and disables, for instance — and a new tool service has to be added there as well.

Nothing else is needed for Grafana behind the proxy — its own vhost at the root means no `GF_SERVER_ROOT_URL` and no sub-path. Anonymous access in the image is `Admin`; fine for a dev box, worth knowing.

## 2. The overlay

```yaml
# compose/otel.yml — the otel backend.
# Relative bind-mount paths resolve against the directory of the first -f file
# (compose.base — the project root in the usual layout), not compose/.
services:
  otel:
    # Pin it: `:latest` is sticky in the local image cache. 0.33.1 is multi-arch.
    image: grafana/otel-lgtm:0.33.1
    # No container_name: a fixed name bypasses compose project scoping and
    # collides with the copies `dwe test` runs (`dwe validate` warns).
    restart: unless-stopped
    # The image declares no VOLUME: every stateful path (Grafana, Loki,
    # Prometheus, Tempo, Pyroscope) lives under /data in the writable layer, so
    # without a volume every recreate — overlay edit + `dwe run`, enable/disable
    # --apply, `dwe reset` — drops all telemetry. A project-scoped named volume
    # keeps it. Dashboards come from the repo (below).
    volumes:
      - otel_data:/data
      - ./configs/otel/provisioning/dashboards.yaml:/otel-lgtm/grafana/conf/provisioning/dashboards/grafana-dashboards.yaml:ro
      - ./configs/otel/dashboards:/otel-lgtm/grafana-dashboards-myproject:ro
    # The image ships its own HEALTHCHECK (`/otel-lgtm/docker/healthcheck.sh`,
    # 30s / 5s / 3 retries) but NO start_period. `dwe run` uses `compose up
    # --wait`; six processes cold-booting take longer than 90 s, so without this
    # block the container goes unhealthy and the whole `dwe run` fails.
    healthcheck:
      test: ["CMD", "/otel-lgtm/docker/healthcheck.sh"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 120s
      start_interval: 5s

volumes:
  otel_data:
```

```yaml
# compose/otel-apps.yml — the patch of the app services, listed in
# compose_after: so it is emitted after every service group (tool → infra →
# app) and wins over the app's own overlay instead of losing to it.
# Compose merges this into the base definition: `command` REPLACES,
# `environment` and `volumes` MERGE. No `depends_on: otel` in either
# direction — spans emitted before the collector answers are dropped with a
# warning in the app log, which is the intended behaviour.
services:
  app:
    environment:
      OTEL_SERVICE_NAME: "app"
      OTEL_RESOURCE_ATTRIBUTES: "service.namespace=${COMPOSE_PROJECT_NAME},deployment.environment.name=dev"
      OTEL_EXPORTER_OTLP_ENDPOINT: "http://otel:4318"
      OTEL_EXPORTER_OTLP_PROTOCOL: "http/protobuf"
      OTEL_TRACES_EXPORTER: "otlp"
      OTEL_METRICS_EXPORTER: "otlp"
      OTEL_LOGS_EXPORTER: "none"
      # Dev is "make a request, then look it up": flush spans every 1 s and
      # push metrics every 10 s (rate() panels need two samples to draw).
      # Per-language knobs for a long-lived process: no-ops under php-fpm,
      # where each request builds a fresh SDK and flushes at shutdown.
      OTEL_BSP_SCHEDULE_DELAY: "1000"
      OTEL_METRIC_EXPORT_INTERVAL: "10000"
```

Traps worth knowing before the first `dwe run`:

- **`dwe validate` does not see the missing `start_period`.** The failure shows up as a hung or failed `dwe run`, not as a warning.
- **`enable|disable --apply` restarts the whole stack.** A service without `on_enable` / `on_disable` gets the default `requires: restart`, i.e. a full `dwe restart` — database and network included (~11 s on a small Laravel stack) — and the app answers 502 through the proxy until its container is healthy again. Wait for its health endpoint, not for the `dwe` command to return. The lighter route is to drop `--apply`: `dwe services enable otel` writes the toggle, then `dwe run` (`compose up --wait` with the default `--remove-orphans`) recreates only the containers whose definition changed — `otel`, the patched apps, the proxy if the overlay touches it — and clears the pending restart.
- **The first minutes show "No data" in every `rate()` panel.** That is the export interval, not a broken pipeline; make some requests and look in Explore → Tempo first.
- **A `depends_on` on an optional service in the base `compose.yaml` breaks the stack while that service is disabled.** Keep the coupling inside the overlay.
- **Patch only services that exist whenever the tool is on.** A patch block carries no `image:` or `build:`, so it is valid only on top of a definition from an earlier file. Patch a service from the base `compose.yaml`, or an always-on one. If the patched app lives in its own overlay (`compose/services/site.yml`, say) and a developer disables that app while `otel` stays enabled, `compose/otel-apps.yml` still declares `site:` with only `environment:` / `volumes:`, and Compose rejects the whole project because the service has neither an image nor a build context. `compose_after:` fixes the **order** the patch is emitted in, not whether the patched service **exists** — this trap is still live.
- **Variants built with `extends:` are not instrumented.** A debug variant declared as `extends: {file: compose.yaml, service: app}` reads the base file, not the `compose_after:` patch, so it runs without the OTEL_* environment, silently. Patching it in `compose/otel-apps.yml` instead hits the previous trap whenever the variant is off.
- **`compose_after:` wins over an app's own overlay.** DWE emits a `compose_after:` file after every service group (tool → infra → app; `dwe compose files` prints the chain), so `compose/otel-apps.yml` lands after `compose/<app>.yml` and wins every whole-value merge: `command:`, `healthcheck:`, and any `environment:` key both files set. New env keys and extra `volumes:` merge fine from either tier. Put a whole-value override in `compose_after:` — a plain `compose:` overlay still loses that merge to a later app overlay.

### Dashboards

The image provisions three dashboards: RED metrics (classic histogram), RED metrics (native histogram — needs a Prometheus feature this bundle does not enable) and a JVM one. Replace its provider file with your own, keep the classic RED one, and point a second provider at a directory in your repo:

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

Datasource UIDs in the image are `prometheus`, `tempo`, `loki`, `pyroscope`. There is no write-back from the container to the repo: edit in the UI, copy the JSON model, paste it over the file under `configs/otel/dashboards/`, commit.

Do not expect a dashboard to move between languages. Metric names follow the instrumentation: Python exports `http_server_duration_milliseconds_*` with `http_target` plus process/GC metrics, while the Go recipe below is traces-only and exports no application metrics at all. Check what actually arrives with `curl http://otel:9090/api/v1/label/__name__/values` from any container. Panels built on Tempo's span metrics (`traces_spanmetrics_*`: `span_name`, `span_kind`, `status_code`, `service`) are the one part that carries over unchanged.

## 3. Instrumenting the services

The app patch (`compose/otel-apps.yml`) carries the OTEL_* environment; what each service does with it depends on the language. The goal for every recipe is the same: the app repo carries no diff when the tool is off, and as little as possible when it is on.

### Python — zero diff

Auto-instrumentation via `opentelemetry-instrument`, layered over the project venv at container start. The app patch replaces the app `command`:

```yaml
# compose/otel-apps.yml
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

`watchfiles` is not an OpenTelemetry package: it is the hot-reload runner the base compose already used, so it must be a dev dependency of the project — otherwise add `--with watchfiles`. Pins are one release train (SDK 1.44.0 ↔ contrib 0.65b0); bump them together. Instrument one DB layer only (SQLAlchemy *or* asyncpg), or every query appears twice. The child process a watcher spawns inherits the layered PATH, so hot reload keeps working. `--with` resolves outside `uv.lock`; if the feature stays, move the packages to a non-default dependency group and use `uv run --frozen --group otel`. Root spans for background workers or bot handlers are not automatic: a few lines on `opentelemetry-api` (a no-op without the SDK) around each tick give the trace a name. OTLP over HTTP (4318), not gRPC — no `grpcio` wheel to build on a fresh Python.

### Go — a small diff, gated by env

There is no runtime injection for Go; the app links the SDK. Keep it inert unless the endpoint is set, mirroring the "empty DSN means no SDK" pattern most error trackers use:

```go
// internal/telemetry/telemetry.go
// Init installs the OTel SDK when OTEL_EXPORTER_OTLP_ENDPOINT is set and
// returns a shutdown func. With the variable empty it does nothing and the
// otelhttp / otelpgx wrappers below fall through to the no-op global provider.
func Init(ctx context.Context) (shutdown func(context.Context) error, err error) {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return func(context.Context) error { return nil }, nil
	}
	exp, err := otlptracehttp.New(ctx) // endpoint, protocol, headers: all from OTEL_* env
	if err != nil {
		return nil, err
	}
	res, _ := resource.New(ctx,
		resource.WithFromEnv(),
		// Not WithProcess(): it includes WithProcessCommandArgs(), which would put
		// argv — DSNs, tokens passed as flags — into every exported resource.
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
	otel.SetTextMapPropagator(propagation.TraceContext{}) // reads/writes `traceparent`
	return tp.Shutdown, nil
}
```

```go
// HTTP server: otelhttp outermost, health probes filtered out. chi knows the
// route pattern only AFTER routing, so the outer handler cannot name the span;
// a second, inner middleware renames it once the request has been routed.
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

// pgx pool: attach the tracer to the pool config. sqlc-generated queries start
// with `-- name: GetEntries :many`; without a name func that comment line
// becomes the span name, so derive it from the header instead.
cfg, err := pgxpool.ParseConfig(dsn)
if err != nil {
	return nil, fmt.Errorf("parse database DSN: %w", err)
}
cfg.ConnConfig.Tracer = otelpgx.NewTracer(
	// otelpgx.WithIncludeQueryParameters(), // opt-in: literal parameter values in spans
	otelpgx.WithSpanNameFunc(sqlcSpanName),
)
pool, err := pgxpool.NewWithConfig(ctx, cfg)
```

`WithIncludeQueryParameters` is commented out on purpose: it writes literal parameter values into spans, and Grafana here grants anonymous `Admin`. Enable it only on a local-only stack with non-sensitive data.

The recipe is **traces-only**: `Init` installs no MeterProvider, so `otelhttp` records its metrics into the no-op global meter and nothing is exported. Set `OTEL_METRICS_EXPORTER: "none"` in the Go service's patch rather than copying `otlp` from the example above. RED panels for a Go service come from Tempo's span metrics (`traces_spanmetrics_*`), which are derived from the traces themselves.

Modules: `go.opentelemetry.io/otel`, `go.opentelemetry.io/otel/sdk`, `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp`, `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp`, `github.com/exaring/otelpgx`. Expect `go get` to bump the whole `go.opentelemetry.io/otel` family that an error-tracking SDK already pulled in transitively — keep them on one version. Outgoing HTTP calls get `otelhttp.NewTransport(http.DefaultTransport)`. Log lines correlate with traces through a small `slog.Handler` wrapper that adds `trace_id`/`span_id` from `trace.SpanContextFromContext(ctx)` when the span is valid — a no-op otherwise, so it can be installed unconditionally.

`otelpgx` ≥ 0.12 speaks the current semantic conventions: `db.system.name`, `db.query.text`, `db.operation.name`, `db.namespace`, `db.collection.name`, plus its own `pgx.query.parameters` when `WithIncludeQueryParameters` is on. Anything that reads the spans has to know both the old and the new key sets — see section 4.

### Node — zero diff via `NODE_OPTIONS`

Node loads instrumentation before the app when told to with `--import`; nothing in the app repo changes. The packages live in a directory of the workspace with their own `package.json`, mounted into the container and installed on first start:

```yaml
# compose/otel-apps.yml
  site:
    environment:
      NODE_OPTIONS: "--import /opt/otel-node/register.mjs"
      OTEL_SERVICE_NAME: "site"
      OTEL_RESOURCE_ATTRIBUTES: "service.namespace=${COMPOSE_PROJECT_NAME},deployment.environment.name=dev"
      OTEL_EXPORTER_OTLP_ENDPOINT: "http://otel:4318"
      # exporter-trace-otlp-http is JSON-only; the protobuf flavour is a
      # different package. Say what is true rather than copy the Go value.
      OTEL_EXPORTER_OTLP_PROTOCOL: "http/json"
      OTEL_TRACES_EXPORTER: "otlp"
      OTEL_METRICS_EXPORTER: "none"
      OTEL_LOGS_EXPORTER: "none"
      OTEL_NODE_ENABLED_INSTRUMENTATIONS: "http,undici"
      OTEL_BSP_SCHEDULE_DELAY: "1000"
      # Turns off the SSR half of an in-app Sentry SDK — see the traps below.
      SENTRY_DSN: ""
    volumes:
      - ./workspace/otel/node:/opt/otel-node
```

```text
workspace/otel/node/
  package.json         # @opentelemetry/api (same 1.x as the app's own copy!),
  package-lock.json    # auto-instrumentations-node, sdk-node, sdk-trace-base,
                       # exporter-trace-otlp-http, instrumentation — pinned
  ensure.sh            # hash-gated `npm ci`, non-fatal, run by register.mjs
  register.mjs         # the --import entry point
  instrumentation.mjs  # NodeSDK + span processor
```

`register.mjs` is the only thing `NODE_OPTIONS` names, and it has to be defensive because **`NODE_OPTIONS` is container-wide**: it reaches `npm` itself, the image's dependency sync, and the compose healthcheck's `node -e`. So it (1) returns at once when `process.argv[1]` is empty (that is the healthcheck probe), (2) runs `ensure.sh` synchronously with `NODE_OPTIONS` cleared for the child `npm ci` (or the install recurses into itself), (3) calls `module.register('@opentelemetry/instrumentation/hook.mjs', import.meta.url)` — the ESM loader hook that lets `instrumentation-http` patch the framework's server — and (4) imports `instrumentation.mjs`, which starts a `NodeSDK` with `getNodeAutoInstrumentations()` and the OTLP exporter configured from the environment.

Undici (the global `fetch`) is hooked through `diagnostics_channel`, so an SSR framework calling the backend propagates `traceparent` with no code. Things that break the zero-diff promise or add noise:

- **A Sentry SDK v8+ in the app.** `Sentry.init` runs its own OpenTelemetry setup. The `@opentelemetry/api` global registry is first-wins, so an SDK registered from `--import` keeps the provider, but Sentry's preloaded http/undici instrumentations still run and emit a second server span for every request through *your* provider. The `site:` patch in `compose/otel-apps.yml` sets `SENTRY_DSN: ""` — a whole-value `environment:` key like this wins over the app's own overlay — so the SSR half of Sentry is off while the tool is on for any app that treats a falsy `SENTRY_DSN` as disabled. An app that falls back to a default DSN on any falsy value (`dsn: process.env.SENTRY_DSN || defaultDsn`) or hardcodes the DSN is not covered: an empty and an unset variable both reach the default, so the fix belongs in the app's own init code. The browser half (`PUBLIC_*`, inlined at build) is untouched. Say so in the overlay's comments.
- **Health probes.** The compose healthcheck's own process is skipped by the `argv[1]` guard, but its request still reaches the instrumented server. Filtering inside the http instrumentation config misses spans other instrumentations create, so drop them in a `SpanProcessor` wrapper around the `BatchSpanProcessor` (`onEnd`: path `/` with no `user-agent`, `/@vite/`, `/@fs/`, `/node_modules/`, `/__astro*`), which sits downstream of every instrumentation. Note that a healthcheck which renders a full page still produces real backend traffic every interval, and the backend traces it.
- **No `http.route` on the site side.** The framework does not feed a route template to the instrumentation, so server spans are named by path. Group by path prefix in the lookup tool.

### PHP (php-fpm, Laravel) — no app diff, one image line

PHP auto-instrumentation has two halves: the `opentelemetry` C extension (the hook engine, which cannot be installed at container start) and the SDK plus instrumentation packages (Composer). Both stay out of the app repo. The extension is built into the image but **not enabled**; the packages go into a vendor directory of their own under the workspace; the overlay mounts the ini that loads both, so with the tool off PHP runs exactly as before.

```dockerfile
# The app image. pecl needs phpize + a compiler; the Debian php:*-fpm images keep both.
# No docker-php-ext-enable: no conf.d ini, so the .so is never loaded (the xdebug pattern).
ARG OTEL_EXT_VER=1.4.2
RUN pecl install -o -f opentelemetry-${OTEL_EXT_VER} && rm -rf /tmp/pear
```

Why not simply enable it: a loaded extension installs the observer API even with no hooks registered and costs ~25% on a microbenchmark of user-function calls. Gating it by a build arg or a second image instead would force a rebuild on every toggle. The image line itself needs one rebuild — `dwe docker build <service>` — because neither `dwe run` nor `dwe services enable --apply` rebuilds images.

```yaml
# compose/otel-apps.yml — command: is a whole-value key, hence compose_after:
  app:
    command: ["/opt/otel-php/ensure.sh", "php-fpm", "-F"]   # the image ENTRYPOINT still runs first
    volumes:
      - ./workspace/otel/php:/opt/otel-php                  # rw: ensure.sh writes build/
      - ./workspace/otel/php/otel.ini:/usr/local/etc/php/conf.d/zz-otel.ini:ro
    environment:
      OTEL_PHP_AUTOLOAD_ENABLED: "true"
      OTEL_SERVICE_NAME: "app"
      OTEL_RESOURCE_ATTRIBUTES: "service.namespace=${COMPOSE_PROJECT_NAME},deployment.environment.name=dev"
      OTEL_EXPORTER_OTLP_ENDPOINT: "http://otel:4318"
      OTEL_EXPORTER_OTLP_PROTOCOL: "http/protobuf"
      OTEL_TRACES_EXPORTER: "otlp"
      OTEL_METRICS_EXPORTER: "none"   # a periodic reader never ticks inside one request
      OTEL_LOGS_EXPORTER: "none"
      OTEL_PROPAGATORS: "tracecontext,baggage"
      OTEL_PHP_DISABLED_INSTRUMENTATIONS: "pdo"   # one DB layer — see below
```

```text
workspace/otel/php/
  composer.base.json   # open-telemetry/sdk, exporter-otlp, opentelemetry-auto-laravel, opentelemetry-auto-pdo;
                       # allow-plugins: tbachert/spi (the SDK finds exporter + instrumentations through it)
  ensure.sh            # hash-gated composer update into build/, non-fatal, then exec "$@"
  prepend.php          # auto_prepend_file: the app's autoloader first, then build/vendor
  otel.ini             # extension=opentelemetry + auto_prepend_file=/opt/otel-php/prepend.php
  .gitignore           # build/
```

**A sidecar vendor must not pull a second Laravel.** Resolved on its own, `opentelemetry-auto-laravel` requires `laravel/framework`, and Composer installs a second full framework (~75 packages) next to the app's, some at different versions — two copies of one class behind two autoloaders. `ensure.sh` therefore writes `build/composer.json` from `composer.base.json` plus the app's `composer.lock`: every locked package goes under `replace` at its exact locked version, and whatever those packages replace or provide (`illuminate/*`, `psr/http-client-implementation`, …) goes under `provide`:

```bash
jq --slurpfile lock "$app_lock" '
  ($lock[0].packages + ($lock[0]["packages-dev"] // [])) as $pkgs
  | .replace = ($pkgs | map({(.name): .version}) | add)
  | .provide = ($pkgs | map(. as $p | ((.replace // {}) + (.provide // {}))
      | to_entries | map({(.key): (if .value == "self.version" then $p.version else .value end)}))
      | flatten | add // {})
' composer.base.json > build/composer.json
(cd build && composer update --no-dev --no-interaction --quiet)
```

Composer then checks the OTel constraints against what the app really has and installs only the OTel-only packages (13 in the test project). The hash covers `composer.base.json` and the app lock, so an unchanged restart skips Composer and a `composer update` in the app re-resolves on the next start; the price is Packagist access on that start and no committed lock for the sidecar.

`prepend.php` requires the **app's** `vendor/autoload.php` before the sidecar's — the SDK's bootstrap files need the app's psr/guzzle/polyfill classes at once, and Composer's `getLoader()` returns the cached loader when `public/index.php` requires it again. It is also scoped, because the ini reaches every PHP process in the container and loading the app's vendor into `composer`'s own process mixes two Symfony Console versions:

```php
<?php
(static function (): void {
    $sapi = PHP_SAPI;
    $script = (string) ($_SERVER['SCRIPT_FILENAME'] ?? '');
    if ($sapi !== 'fpm-fcgi' && !($sapi === 'cli' && basename($script) === 'artisan')) {
        return;
    }
    $app = '/workspace/src/vendor/autoload.php';
    $otel = __DIR__ . '/build/vendor/autoload.php';
    if (!extension_loaded('opentelemetry') || !is_file($app) || !is_file($otel)) {
        return;
    }
    // Queue workers: drop root spans, keep jobs (they carry the dispatcher's traceparent).
    if ($sapi === 'cli' && in_array($_SERVER['argv'][1] ?? '', ['queue:work', 'queue:listen'], true)
        && getenv('OTEL_TRACES_SAMPLER') === false) {
        putenv('OTEL_TRACES_SAMPLER=parentbased_always_off');
    }
    require $app;
    require $otel;
})();
```

What you get: a server span per request with `http.route`, one `sql <VERB>` span per query with the new keys (`db.system.name`, `db.query.text`, placeholders, not values), a `Command <name>` root for `php artisan <cmd>`, and a queued job as a `process` span **inside the trace that dispatched it** — Laravel carries `traceparent` in the job payload. The whole stack added ~4.5 ms at p50 to an 11 ms welcome page. Traps:

- **One DB layer.** `opentelemetry-auto-laravel`'s query watcher already emits a span per query; with `auto-pdo` also active each statement gains `PDO::prepare` / `PDOStatement::execute` / `fetchAll` siblings. Keep `pdo` in `OTEL_PHP_DISABLED_INSTRUMENTATIONS` unless you need `PDO::connect` or raw PDO calls that bypass the query builder.
- **Queue polling noise.** `queue:listen` spawns `queue:work --once` every ~3 s; without the `parentbased_always_off` rule each poll is a 3-second trace with ~20 SQL spans, even on an empty queue. The flip side: a job dispatched with no trace context (from a process `prepend.php` skips, or queued while the tool was off) is not traced at all.
- **`clear_env`.** php-fpm's default `clear_env = yes` strips every `OTEL_*` variable from the workers and nothing is traced, with no error. The official image sets `clear_env = no`; a project's own pool config has to keep it.
- **Export still costs the worker.** Every request builds a fresh SDK and flushes in its shutdown handler, so `OTEL_BSP_SCHEDULE_DELAY` does nothing here. Laravel calls `fastcgi_finish_request()` first — the client does not wait for the export, but the FPM worker does.
- **Collector down, overlay on** (`dwe stop otel`): requests still answer 200, but each writes an export stack trace to the FPM log. Consider `OTEL_PHP_LOG_DESTINATION`.

## 4. Traces as text for the agent

Grafana is for humans. An agent needs a command with compact output — Tempo's HTTP API is enough for that, and two endpoints carry the lookup: `GET :3200/api/search?q=<TraceQL>&start=&end=&limit=` and `GET :3200/api/v2/traces/<hex>`. The lookup reads traces through v2 because it returns the standard OTLP shape (`{"trace": {"resourceSpans": […]}}`); v1 (`/api/traces/<hex>`) returns the same spans under a legacy `batches` key. v2 answers an unknown id with an empty `200` rather than a `404`, so an empty `resourceSpans` means not found. The search index lags 10–60 s (~45 s observed); a trace is readable by id at once, which is what makes the lookup loop below reliable. Tag-value lookups (`/api/v2/search/tag/resource.service.name/values`) need `start`/`end` too: without them Tempo answers from its live store only, and a service whose spans are already in completed blocks — after an `otel` restart, say — drops out of the list.

DWE ships a stdlib-only Python script for this in [`examples/otel/`](../../examples/otel/README.md) (`traces.py` plus its unit tests): `summary`, `list`, `show <id>`, `traceparent`, `services` and `selftest` subcommands. Copy it to `workspace/otel/`. It runs inside any container of the stack that has `python3` — the `otel` image itself has only `curl` and `bash`, and neither do the official `php:*` images, Debian ones included; check a candidate with `docker exec <container> python3 --version` — and is mounted by the overlay so the command exists exactly when the backend does:

```yaml
# compose/otel-apps.yml, the patched app service
    volumes:
      - ./workspace/otel:/opt/otel:ro
```

```yaml
# workspace/commands/otel.yml
group:
  title: Otel
  description: "Trace lookup against the otel tool service (Tempo), for triage without opening Grafana"
  # Hidden while the tool is off: the mount above does not exist then.
  hide: '{{ not (index .Raw "services" "otel" "enabled") }}'

commands:
  traces:
    type: service_exec
    description: "Traces as text (SQL, slow spans, N+1, exceptions): -- summary | list | show <id> | traceparent | -h"
    service: app
    mode: exec-or-fail   # never spin up a throwaway container with no traffic
    workdir: /workspace/src
    argv: [python3, /opt/otel/traces.py, "${args}"]
    messages:
      error: "otel.traces failed — is the app running? (dwe run); is otel enabled? (dwe services enable otel --apply)"
```

If no container carries Python (a PHP stack), do not add a second tool service: declare a throwaway compose service in the same `compose/otel.yml`, behind a profile so `up` never starts it, and target it with `service_run`. `service:` takes a compose service name, so the service needs no `workspace/services/` folder; `service_run` always passes `--entrypoint ""`, so the argv names the interpreter:

```yaml
# compose/otel.yml
  otel-lookup:
    image: python:3.13-alpine
    profiles: ["tools"]          # never started by `compose up`
    working_dir: /opt/otel
    volumes:
      - ./workspace/otel:/opt/otel:ro
```

```yaml
# workspace/commands/otel.yml
  traces:
    type: service_run            # compose run --rm, ~0.7 s per call
    service: otel-lookup
    argv: [python3, /opt/otel/traces.py, "${args}"]
```

Semantic conventions moved: newer instrumentations (Go, Node) emit `db.query.text` / `db.system.name` where older ones emit `db.statement` / `db.system`, and `http.request.method` / `url.path` instead of `http.method` / `http.target`. A lookup tool has to read both.

The loop the agent uses:

```bash
dwe cmd otel.traces -- traceparent            # prints a header value + trace_id
curl -H 'traceparent: 00-<id>-<span>-01' http://api.myproject.localhost/api/v1/things
dwe cmd otel.traces -- show <trace_id>        # exactly that request, no search
dwe cmd otel.traces -- summary --last 15m     # when you do not know WHERE yet
```

`show` folds repeated sibling spans (`×25 SELECT … <- possible N+1`) and prints exceptions with the app's own stack frames. A request sent with a caller-made `traceparent` has a remote parent, so Tempo cannot name its root; it lands in the `-` row of `summary`.

## 5. Tell the agent

Add a short section to `AGENTS.md` (or a page it links to). The wording that worked:

> Only while the optional `otel` tool is enabled (`dwe services list`). When the question is "what did this request actually do" — too many queries, a slow endpoint, a 500, a misbehaving worker — look at the trace BEFORE reading code: `dwe cmd otel.traces -- summary --last 15m` when you do not know where yet, the `traceparent → curl → show` loop when you do. Some periodic jobs are chatty by design; compare against what the code says the job should do before calling it a regression.

Without such a pointer an agent bypasses `dwe` altogether (`docker logs`, `docker exec … psql`), and a declared read-only `db.query` command saves it more turns than the traces do.

## Known rough edges

- Without the `otel_data` volume every recreate of the container loses all telemetry: editing the overlay and running `dwe run`, `dwe services enable|disable --apply`, `dwe reset`. Stopping and starting the same container (`dwe restart otel`, `dwe stop` then `dwe run`) keeps it either way.
- At debug log level the OTLP exporter's own HTTP calls appear in the app log about once a second (Python: `urllib3.connectionpool … POST /v1/traces`). Raise `OTEL_BSP_SCHEDULE_DELAY` or silence that logger.
- Health probes excluded from HTTP instrumentation still emit parentless `PING` / `SELECT` / `connect` traces from their DB checks. Filter with `--exclude-root` / `--kind server` in the lookup tool, or at the collector.
- Tempo's span metrics carry only `span_name`, so a SQL span is named by its verb; the actual statement is visible only inside the trace.

## See also

- [Adding a service](add-a-service.md) — the tool-service shape this guide builds on.
- [Authoring project commands](author-project-commands.md) — `service_exec`, `hide:`, `${args}`.
- [`../reference/config/services/index.md`](../reference/config/services/index.md) — `type: tool`, `compose:` and `compose_after:` overlays, `hosts:`.
