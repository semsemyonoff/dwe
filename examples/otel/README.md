# otel trace lookup (`traces.py`)

A compact text view over Tempo traces, for a coding agent or a human in a terminal: what did this request do — SQL, slow spans, N+1 patterns, exceptions — without opening Grafana. It is the lookup script of the [OpenTelemetry guide](../../docs/guides/observability-otel.md); the guide shows the tool service, the overlays and the `dwe cmd otel.traces` command that runs it.

Stdlib only, Python 3.9+. It talks to Tempo's HTTP API (`TEMPO_URL`, default `http://otel:3200`) and, for `selftest` only, to the collector's OTLP/HTTP ingest (`OTEL_OTLP_URL`, default `http://otel:4318`) — so it runs inside the compose network, not on the host.

## Install

Copy both files into the project and mount the directory where the command runs:

```bash
mkdir -p workspace/otel
cp examples/otel/traces.py examples/otel/test_traces.py workspace/otel/
```

## Subcommands

| Subcommand | What it does |
|------------|--------------|
| `summary [--last 15m] [--service S] [--root RE] [--exclude-root RE] [--sort db\|dbmax\|dur\|count\|errors] [--top N]` | Aggregate recent traces by root span name: where DB load, slowness and errors come from. Start here. |
| `list` (default) `[--last] [--service] [--errors] [--min-duration 200ms] [--name RE] [--root RE] [--exclude-root RE] [--kind K] [--limit N] [-q TRACEQL]` | One line per matching trace. |
| `show <trace-id> [--full] [--json] [--max-spans N]` | One trace as an indented span tree; repeated siblings fold (`×25 SELECT … <- possible N+1`), exceptions print with the app's own frames. |
| `traceparent` | A fresh W3C `traceparent` header and its trace id, for the `traceparent → curl → show` loop. |
| `services [--last 24h]` | Service names Tempo has spans for in the window. |
| `selftest` | Posts a synthetic trace through OTLP ingest, reads it back from Tempo and checks the rendering. |

`-h` works on every subcommand. Both the old and the new semantic-convention keys are read (`db.statement` / `db.query.text`, `http.method` / `http.request.method`, …), so Python, Go, Node and PHP instrumentations render the same way.

## Tests

Pure-logic unit tests, no network:

```bash
python3 -m unittest discover -s examples/otel -p 'test_*.py'    # in this repo
python3 -m unittest discover -s workspace/otel -p 'test_*.py'   # in a project
```
